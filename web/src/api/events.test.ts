import { describe, expect, it, vi } from 'vitest';
import { type ConnectionState, EventStream, type ServerEvent, SseParser } from './events';

describe('SseParser', () => {
  it('parses fields across chunk boundaries, CRLF, comments and multi-line data', () => {
    const out: Array<{ id: string | null; event: string; data: string }> = [];
    const retries: number[] = [];
    const p = new SseParser(
      (m) => out.push(m),
      (ms) => retries.push(ms),
    );
    p.push('retry: 3000\n: connected\n\nid: 7\nevent: movie.up');
    p.push('dated\r\ndata: {"a":1}\r\n\r\n');
    p.push('id: 8\ndata: line1\ndata: line2\n\n');
    p.push('data: no-id-change\n\n');
    expect(retries).toEqual([3000]);
    expect(out).toEqual([
      { id: '7', event: 'movie.updated', data: '{"a":1}' },
      { id: '8', event: 'message', data: 'line1\nline2' },
      { id: '8', event: 'message', data: 'no-id-change' },
    ]);
  });
});

/** A fetch whose streams are pushed by the test. */
function fakeServer(failFirst = 0) {
  let failures = failFirst;
  const calls: Array<{
    headers: Record<string, string>;
    push: (s: string) => void;
    close: () => void;
    fail: () => void;
  }> = [];
  const fetchFn = vi.fn((_url: string, init?: RequestInit) => {
    if (failures > 0) {
      failures--;
      calls.push({ headers: {}, push: () => {}, close: () => {}, fail: () => {} });
      return Promise.reject(new TypeError('network down'));
    }
    const enc = new TextEncoder();
    let ctrl!: ReadableStreamDefaultController<Uint8Array>;
    const body = new ReadableStream<Uint8Array>({
      start(c) {
        ctrl = c;
      },
    });
    init?.signal?.addEventListener('abort', () => {
      try {
        ctrl.error(new DOMException('aborted', 'AbortError'));
      } catch {
        /* closed */
      }
    });
    let reject!: (e: unknown) => void;
    const p = new Promise<Response>((resolve, rej) => {
      reject = rej;
      queueMicrotask(() => resolve(new Response(body, { status: 200 })));
    });
    calls.push({
      headers: (init?.headers ?? {}) as Record<string, string>,
      push: (s) => ctrl.enqueue(enc.encode(s)),
      close: () => ctrl.close(),
      fail: () => reject(new TypeError('network down')),
    });
    return p;
  });
  return { calls, fetchFn: fetchFn as unknown as typeof fetch };
}

const tick = () => new Promise((r) => setTimeout(r, 0));

describe('EventStream', () => {
  it('delivers events, reconnects with Last-Event-ID and reports its state', async () => {
    const { calls, fetchFn } = fakeServer();
    const events: ServerEvent[] = [];
    const states: ConnectionState[] = [];
    const s = new EventStream({
      url: '/api/v1/events',
      fetch: fetchFn,
      onEvent: (e) => events.push(e),
      onResync: () => {},
      onState: (c) => states.push(c),
      backoff: (n, retry) => (retry / 100) * n,
    });
    s.start();
    await vi.waitFor(() => expect(calls.length).toBe(1));
    expect(calls[0]!.headers['Last-Event-ID']).toBeUndefined();
    calls[0]!.push(
      'retry: 1000\n\nid: 41\nevent: movie.updated\ndata: {"id":41,"type":"movie.updated","movieId":3}\n\n',
    );
    await vi.waitFor(() => expect(events).toHaveLength(1));
    expect(events[0]).toMatchObject({ type: 'movie.updated', movieId: 3 });
    expect(s.connection.status).toBe('open');

    calls[0]!.close(); // server went away
    await vi.waitFor(() =>
      expect(states.some((c) => c.status === 'reconnecting' && c.attempt === 1 && c.nextRetryAt)).toBe(true),
    );
    await vi.waitFor(() => expect(calls.length).toBe(2));
    expect(calls[1]!.headers['Last-Event-ID']).toBe('41');
    await tick();
    expect(states.at(-1)?.status).toBe('open');
    s.stop();
  });

  it('backs off on repeated failures and retries at once on retryNow()', async () => {
    const { calls, fetchFn } = fakeServer(2);
    const s = new EventStream({
      url: '/e',
      fetch: fetchFn,
      onEvent: () => {},
      onResync: () => {},
      backoff: (n) => (n === 1 ? 10 : 60_000),
    });
    s.start();
    await vi.waitFor(() => expect(calls.length).toBe(2)); // first failure, retried after 10 ms
    await vi.waitFor(() => expect(s.connection.attempt).toBe(2)); // second failure: next try in 60 s
    expect(s.connection.nextRetryAt! - Date.now()).toBeGreaterThan(50_000);
    s.retryNow();
    await vi.waitFor(() => expect(calls.length).toBe(3));
    await vi.waitFor(() => expect(s.connection.status).toBe('open'));
    s.stop();
  });

  it('calls onResync when the server says events were missed', async () => {
    const { calls, fetchFn } = fakeServer();
    const onResync = vi.fn();
    const s = new EventStream({ url: '/e', fetch: fetchFn, onEvent: () => {}, onResync });
    s.start();
    await vi.waitFor(() => expect(calls.length).toBe(1));
    calls[0]!.push('event: resync\ndata: {"reason":"buffer overflow"}\n\n');
    await vi.waitFor(() => expect(onResync).toHaveBeenCalledWith('buffer overflow'));
    s.stop();
  });
});

describe('EventStream and 429', () => {
  it('waits at least the Retry-After of a 429 before reconnecting', async () => {
    vi.useFakeTimers();
    const fetch = vi.fn().mockResolvedValue(new Response(null, { status: 429, headers: { 'Retry-After': '30' } }));
    const states: number[] = [];
    const s = new EventStream({
      url: 'http://x/events',
      fetch,
      onEvent: () => {},
      onResync: () => {},
      backoff: () => 1000,
      now: () => 0,
      onState: (c) => c.nextRetryAt && states.push(c.nextRetryAt),
    });
    s.start();
    await vi.advanceTimersByTimeAsync(0);
    expect(states.at(-1)).toBe(30_000);
    s.stop();
    vi.useRealTimers();
  });
});
