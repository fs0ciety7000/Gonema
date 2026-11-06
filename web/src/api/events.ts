/* Server-Sent Events client for GET /api/v1/events.
   Built on fetch rather than EventSource so that reconnection is ours: the
   Last-Event-ID header is sent on every reconnect (the server replays what was
   missed, or sends "resync"), the backoff grows while the server is away, and the
   connection state is observable for the offline / reconnecting UI. */

export type ServerEvent = {
  id: number;
  type: string;
  time?: string;
  movieId?: number;
  data?: unknown;
};

export type ConnectionStatus = 'connecting' | 'open' | 'reconnecting';

export type ConnectionState = {
  status: ConnectionStatus;
  /** Consecutive failed attempts since the last successful connection. */
  attempt: number;
  /** When the next attempt starts (ms epoch), while reconnecting. */
  nextRetryAt: number | null;
  /** When the connection was lost (ms epoch), while reconnecting. */
  lostAt: number | null;
};

export type EventStreamOptions = {
  url: string;
  fetch?: typeof fetch;
  onEvent: (e: ServerEvent) => void;
  /** The server says events were missed: refetch everything. */
  onResync: (reason: string) => void;
  onState?: (s: ConnectionState) => void;
  /** The server answered 401 (signed out): the stream stops instead of retrying. */
  onUnauthorized?: () => void;
  /** Delay before attempt n (1-based), given the server's advised retry. */
  backoff?: (attempt: number, retryMs: number) => number;
  now?: () => number;
};

export const MAX_BACKOFF_MS = 30_000;

export function defaultBackoff(attempt: number, retryMs: number): number {
  const base = Math.min(MAX_BACKOFF_MS, retryMs * 2 ** Math.max(0, attempt - 1));
  return Math.round(base * (0.8 + Math.random() * 0.4));
}

/** Incremental parser of the text/event-stream format (WHATWG HTML §9.2). */
export class SseParser {
  private buf = '';
  private data: string[] = [];
  private event = '';
  private id: string | null = null;
  constructor(
    private readonly dispatch: (msg: { id: string | null; event: string; data: string }) => void,
    private readonly onRetry: (ms: number) => void,
  ) {}

  push(chunk: string) {
    this.buf += chunk;
    let nl = this.buf.search(/\r\n|\r|\n/);
    while (nl >= 0) {
      const line = this.buf.slice(0, nl);
      const len = this.buf.startsWith('\r\n', nl) ? 2 : 1;
      this.buf = this.buf.slice(nl + len);
      this.line(line);
      nl = this.buf.search(/\r\n|\r|\n/);
    }
  }

  private line(line: string) {
    if (line === '') {
      if (this.data.length) this.dispatch({ id: this.id, event: this.event || 'message', data: this.data.join('\n') });
      this.data = [];
      this.event = '';
      return;
    }
    if (line.startsWith(':')) return;
    const i = line.indexOf(':');
    const field = i < 0 ? line : line.slice(0, i);
    let value = i < 0 ? '' : line.slice(i + 1);
    if (value.startsWith(' ')) value = value.slice(1);
    if (field === 'data') this.data.push(value);
    else if (field === 'event') this.event = value;
    else if (field === 'id' && !value.includes('\0')) this.id = value;
    else if (field === 'retry' && /^\d+$/.test(value)) this.onRetry(Number(value));
  }
}

export class EventStream {
  lastEventId: string | null = null;
  private retryMs = 3000;
  private attempt = 0;
  private controller: AbortController | null = null;
  private timer: ReturnType<typeof setTimeout> | null = null;
  private stopped = true;
  private state: ConnectionState = { status: 'connecting', attempt: 0, nextRetryAt: null, lostAt: null };
  private readonly fetchFn: typeof fetch;
  private readonly backoff: (attempt: number, retryMs: number) => number;
  private readonly now: () => number;

  constructor(private readonly opts: EventStreamOptions) {
    this.fetchFn = opts.fetch ?? ((...a) => fetch(...a));
    this.backoff = opts.backoff ?? defaultBackoff;
    this.now = opts.now ?? Date.now;
  }

  get connection(): ConnectionState {
    return this.state;
  }

  start() {
    if (!this.stopped) return;
    this.stopped = false;
    void this.connect();
  }

  stop() {
    this.stopped = true;
    if (this.timer) clearTimeout(this.timer);
    this.timer = null;
    this.controller?.abort();
    this.controller = null;
  }

  /** Skips the wait and reconnects now ("Retry now"). */
  retryNow() {
    if (this.stopped || this.state.status === 'open') return;
    if (this.timer) clearTimeout(this.timer);
    this.timer = null;
    void this.connect();
  }

  private setState(s: Partial<ConnectionState>) {
    this.state = { ...this.state, ...s };
    this.opts.onState?.(this.state);
  }

  private async connect() {
    this.controller?.abort();
    const controller = new AbortController();
    this.controller = controller;
    if (this.state.status !== 'reconnecting') this.setState({ status: 'connecting', nextRetryAt: null });
    const headers: Record<string, string> = { Accept: 'text/event-stream', 'Cache-Control': 'no-cache' };
    if (this.lastEventId) headers['Last-Event-ID'] = this.lastEventId;
    try {
      const res = await this.fetchFn(this.opts.url, { headers, signal: controller.signal, cache: 'no-store' });
      if (res.status === 401 && this.opts.onUnauthorized) {
        this.stop();
        this.opts.onUnauthorized();
        return;
      }
      if (!res.ok || !res.body) throw new Error(`HTTP ${res.status}`);
      this.attempt = 0;
      this.setState({ status: 'open', attempt: 0, nextRetryAt: null, lostAt: null });
      await this.read(res.body, controller.signal);
      if (!controller.signal.aborted) this.scheduleReconnect(); // server closed the stream
    } catch {
      if (!controller.signal.aborted) this.scheduleReconnect();
    }
  }

  private async read(body: ReadableStream<Uint8Array>, signal: AbortSignal) {
    const reader = body.getReader();
    const decoder = new TextDecoder();
    const parser = new SseParser(
      (msg) => this.message(msg),
      (ms) => {
        this.retryMs = ms;
      },
    );
    try {
      while (!signal.aborted) {
        const { value, done } = await reader.read();
        if (done) break;
        parser.push(decoder.decode(value, { stream: true }));
      }
    } finally {
      reader.releaseLock();
    }
  }

  private message(msg: { id: string | null; event: string; data: string }) {
    if (msg.id !== null) this.lastEventId = msg.id;
    if (msg.event === 'resync') {
      let reason = '';
      try {
        reason = String((JSON.parse(msg.data) as { reason?: unknown }).reason ?? '');
      } catch {
        /* keep empty */
      }
      this.opts.onResync(reason);
      return;
    }
    let parsed: ServerEvent;
    try {
      parsed = JSON.parse(msg.data) as ServerEvent;
    } catch {
      return;
    }
    if (!parsed.type) parsed.type = msg.event;
    this.opts.onEvent(parsed);
  }

  private scheduleReconnect() {
    if (this.stopped) return;
    this.attempt++;
    const delay = this.backoff(this.attempt, this.retryMs);
    const now = this.now();
    this.setState({
      status: 'reconnecting',
      attempt: this.attempt,
      nextRetryAt: now + delay,
      lostAt: this.state.lostAt ?? now,
    });
    this.timer = setTimeout(() => {
      this.timer = null;
      void this.connect();
    }, delay);
  }
}
