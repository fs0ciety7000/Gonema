import '@/styles/system.css';
import { useMutation, useQuery, useQueryClient } from '@tanstack/react-query';
import { useVirtualizer } from '@tanstack/react-virtual';
import {
  ArrowDownToLine,
  Check,
  ChevronUp,
  Copy,
  Database,
  Download,
  FileVideo,
  Lock,
  Radio,
  RefreshCw,
  Search,
} from 'lucide-react';
import { Fragment, memo, type ReactNode, useCallback, useEffect, useMemo, useRef, useState } from 'react';
import { notifyUnauthorized } from '@/api/client';
import { EventStream } from '@/api/events';
import {
  LOG_LEVELS,
  type LogLevel,
  logDownloadUrl,
  logSettingsQuery,
  logStreamUrl,
  logsQuery,
  opsKeys,
  parseLogEntry,
  putLogLevel,
} from '@/api/ops';
import type { LogEntry } from '@/api/schema';
import { problemText } from '@/components/auth/AuthParts';
import { Button, buttonClass, IconButton, Kbd } from '@/components/ui/Button';
import { Chip } from '@/components/ui/Chip';
import { Skeleton } from '@/components/ui/Feedback';
import { Select } from '@/components/ui/Select';
import { Switch } from '@/components/ui/Switch';
import { toast } from '@/components/ui/Toast';
import { formatNumber } from '@/lib/format';
import { HeaderActions } from './headerSlot';

const MAX_LINES = 5000;
const LINE_H = 24;
const LABEL: Record<LogLevel, string> = { debug: 'Debug', info: 'Info', warn: 'Warn', error: 'Error' };
const SPOKEN: Record<LogLevel, string> = { debug: 'Debug', info: 'Info', warn: 'Warning', error: 'Error' };
const SHORT: Record<LogLevel, string> = { debug: 'DBG', info: 'INF', warn: 'WRN', error: 'ERR' };
const RANK: Record<LogLevel, number> = { debug: 0, info: 1, warn: 2, error: 3 };
/** What the server writes in place of a secret (internal/logbuf.Redacted). */
export const REDACTED = '[redacted]';

const clock = new Intl.DateTimeFormat('en-GB', {
  hour: '2-digit',
  minute: '2-digit',
  second: '2-digit',
  hour12: false,
});
const timeOf = (iso: string) => {
  const d = new Date(iso);
  return Number.isNaN(d.getTime()) ? '' : clock.format(d);
};

export const attrsText = (a: LogEntry['attrs']) =>
  Object.entries(a ?? {})
    .map(([k, v]) => `${k}=${/\s/.test(v) ? JSON.stringify(v) : v}`)
    .join(' ');

/** Client-side text match, as the server's q: case-insensitive, in the message or the attributes. */
export function matches(e: LogEntry, q: string) {
  if (!q) return true;
  const t = q.toLowerCase();
  return e.message.toLowerCase().includes(t) || attrsText(e.attrs).toLowerCase().includes(t);
}

/** Merges new entries (by id, oldest first) and keeps the last MAX_LINES. */
export function mergeEntries(prev: readonly LogEntry[], add: readonly LogEntry[]): LogEntry[] {
  if (!add.length) return prev as LogEntry[];
  const last = prev.at(-1)?.id ?? -1;
  const fresh = add.filter((e) => e.id > last);
  if (!fresh.length) return prev as LogEntry[];
  const next = prev.concat(fresh);
  return next.length > MAX_LINES ? next.slice(next.length - MAX_LINES) : next;
}

/** The lowest level among those shown: the API filters from it, the rest is filtered here. */
export function minLevel(shown: ReadonlySet<LogLevel>): LogLevel {
  return LOG_LEVELS.find((l) => shown.has(l)) ?? 'error';
}

/** Entries per level, in what is held in memory here. */
export function countLevels(entries: readonly LogEntry[]): Record<LogLevel, number> {
  const c: Record<LogLevel, number> = { debug: 0, info: 0, warn: 0, error: 0 };
  for (const e of entries) c[e.level]++;
  return c;
}

/** Splits a value around the server's redaction marker, so secrets read as a token, not as text. */
export function Redactable({ text }: { text: string }) {
  if (!text.includes(REDACTED)) return text;
  const parts = text.split(REDACTED);
  return (
    <>
      {parts.map((p, i) => (
        // biome-ignore lint/suspicious/noArrayIndexKey: fixed split of one string
        <Fragment key={i}>
          {p}
          {i < parts.length - 1 && (
            <span className="red">
              <Lock className="i" aria-hidden="true" />
              <span aria-hidden="true">•••</span> redacted
            </span>
          )}
        </Fragment>
      ))}
    </>
  );
}

function Attrs({ a }: { a: LogEntry['attrs'] }) {
  const list = Object.entries(a ?? {});
  if (!list.length) return null;
  return (
    <>
      {list.map(([k, v]) => (
        <span key={k} className="a">
          {' '}
          {k}=
          <b>
            <Redactable text={/\s/.test(v) ? JSON.stringify(v) : v} />
          </b>
        </span>
      ))}
    </>
  );
}

async function copyJson(e: LogEntry): Promise<boolean> {
  try {
    if (navigator.clipboard && window.isSecureContext) {
      await navigator.clipboard.writeText(JSON.stringify(e, null, 2));
      return true;
    }
  } catch {
    /* fall through */
  }
  return false;
}

function Detail({ e, onCollapse }: { e: LogEntry; onCollapse: () => void }) {
  const [copied, setCopied] = useState<boolean | null>(null);
  return (
    <section className="log-detail" aria-label="Details of this entry">
      <div className="dh">
        <span className="t-mono-sm">{e.time}</span>
        <span>entry #{formatNumber(e.id)}</span>
        <span className="row">
          <Button
            size="sm"
            variant="ghost"
            icon={copied ? <Check className="i" aria-hidden="true" /> : <Copy className="i" aria-hidden="true" />}
            onClick={() => void copyJson(e).then(setCopied)}
          >
            {copied ? 'Copied' : 'Copy as JSON'}
          </Button>
          <IconButton size="sm" label="Collapse entry" onClick={onCollapse}>
            <ChevronUp className="i i-sm" aria-hidden="true" />
          </IconButton>
        </span>
      </div>
      {copied === false && (
        <p className="t-caption t-muted" role="status">
          This browser blocked the copy (plain HTTP). Download the logs instead.
        </p>
      )}
      <dl className="attrs">
        <dt>message</dt>
        <dd>
          <Redactable text={e.message} />
        </dd>
        {e.component && (
          <>
            <dt>component</dt>
            <dd>{e.component}</dd>
          </>
        )}
        {Object.entries(e.attrs ?? {}).map(([k, v]) => (
          <Fragment key={k}>
            <dt>{k}</dt>
            <dd>
              <Redactable text={v} />
            </dd>
          </Fragment>
        ))}
      </dl>
    </section>
  );
}

const Line = memo(function Line({
  e,
  top,
  index,
  open,
  measure,
  onToggle,
}: {
  e: LogEntry;
  top: number;
  index: number;
  open: boolean;
  measure: (el: HTMLElement | null) => void;
  onToggle: (id: number) => void;
}) {
  return (
    <li ref={measure} data-index={index} className="ln-item" style={{ transform: `translateY(${top}px)` }}>
      <button
        type="button"
        className={open ? 'ln open' : 'ln'}
        data-lv={e.level}
        aria-expanded={open}
        onClick={() => onToggle(e.id)}
      >
        <time dateTime={e.time}>{timeOf(e.time)}</time>
        <span className="lvl">
          <span aria-hidden="true">{SHORT[e.level]}</span>
          <span className="sr-only">{SPOKEN[e.level]}</span>
        </span>
        <span className="comp">{e.component}</span>
        <span className="msg">
          <Redactable text={e.message} />
          <Attrs a={e.attrs} />
        </span>
      </button>
      {open && <Detail e={e} onCollapse={() => onToggle(e.id)} />}
    </li>
  );
});

function useDebounced<T>(v: T, ms: number): T {
  const [d, setD] = useState(v);
  useEffect(() => {
    const t = setTimeout(() => setD(v), ms);
    return () => clearTimeout(t);
  }, [v, ms]);
  return d;
}

function Viewer() {
  const [shown, setShown] = useState<ReadonlySet<LogLevel>>(() => new Set<LogLevel>(['info', 'warn', 'error']));
  const level = minLevel(shown);
  const [text, setText] = useState('');
  const q = useDebounced(text.trim(), 250);
  const [live, setLive] = useState(true);
  const [entries, setEntries] = useState<LogEntry[]>([]);
  const [follow, setFollow] = useState(true);
  const [unseen, setUnseen] = useState(0);
  const [openId, setOpenId] = useState<number | null>(null);
  const list = useQuery(logsQuery(level, q));
  const scrollRef = useRef<HTMLElement>(null);

  // The recent entries (server-side filter from the lowest level shown), then the live tail on top.
  useEffect(() => {
    if (list.data) setEntries(list.data.entries ?? []);
  }, [list.data]);

  const qRef = useRef(q);
  qRef.current = q;
  const refetch = useRef(list.refetch);
  refetch.current = list.refetch;
  useEffect(() => {
    if (!live) return;
    let batch: LogEntry[] = [];
    let raf = 0;
    const flush = () => {
      raf = 0;
      const add = batch.filter((e) => matches(e, qRef.current));
      batch = [];
      if (add.length) setEntries((prev) => mergeEntries(prev, add));
    };
    const s = new EventStream({
      url: logStreamUrl(level),
      onEvent: (ev) => {
        const e = parseLogEntry(ev);
        if (!e || RANK[e.level] < RANK[level]) return;
        batch.push(e);
        if (!raf) raf = requestAnimationFrame(flush);
      },
      onResync: () => void refetch.current(),
      onUnauthorized: notifyUnauthorized,
    });
    s.start();
    return () => {
      s.stop();
      cancelAnimationFrame(raf);
    };
  }, [live, level]);

  const counts = useMemo(() => countLevels(entries), [entries]);
  const visible = useMemo(() => entries.filter((e) => shown.has(e.level)), [entries, shown]);
  // the column stays hidden until the server names components (older servers send none)
  const hasComponent = useMemo(() => visible.some((e) => e.component), [visible]);

  const v = useVirtualizer({
    count: visible.length,
    getScrollElement: () => scrollRef.current,
    estimateSize: () => LINE_H,
    overscan: 20,
    getItemKey: (i) => visible[i]?.id ?? i,
  });

  // Follow the tail while the reader is at the bottom (and nothing is open); otherwise count arrivals.
  const prevLen = useRef(0);
  useEffect(() => {
    const added = visible.length - prevLen.current;
    prevLen.current = visible.length;
    if (!visible.length) return;
    if (follow && openId === null) v.scrollToIndex(visible.length - 1, { align: 'end' });
    else if (added > 0) setUnseen((n) => n + added);
  }, [visible, follow, openId, v]);

  const onScroll = useCallback(() => {
    const el = scrollRef.current;
    if (!el) return;
    const atBottom = el.scrollHeight - el.scrollTop - el.clientHeight < LINE_H * 2;
    setFollow(atBottom);
    if (atBottom) setUnseen(0);
  }, []);

  const jump = () => {
    setFollow(true);
    setUnseen(0);
    setOpenId(null);
    v.scrollToIndex(visible.length - 1, { align: 'end' });
  };
  const toggle = useCallback((id: number) => setOpenId((o) => (o === id ? null : id)), []);
  // an entry opened near the bottom: bring its details into view once they are measured
  const visibleRef = useRef(visible);
  visibleRef.current = visible;
  useEffect(() => {
    if (openId === null) return;
    const i = visibleRef.current.findIndex((e) => e.id === openId);
    if (i < 0) return;
    const raf = requestAnimationFrame(() => v.scrollToIndex(i, { align: 'auto' }));
    return () => cancelAnimationFrame(raf);
  }, [openId, v]);
  const toggleLevel = (l: LogLevel) =>
    setShown((s) => {
      const n = new Set(s);
      if (n.has(l)) n.delete(l);
      else n.add(l);
      return n.size ? n : s; // at least one level stays shown
    });

  const items = v.getVirtualItems();
  const empty = !list.isPending && !visible.length;
  const oldest = entries[0];
  const shownList = LOG_LEVELS.filter((l) => shown.has(l))
    .map((l) => LABEL[l].toLowerCase())
    .join(', ');
  return (
    <section className="panel viewer" aria-labelledby="logs-h">
      <h2 id="logs-h" className="sr-only">
        Log entries
      </h2>
      <div className="vt">
        {/* biome-ignore lint/a11y/useSemanticElements: a group of toggle buttons, not a form fieldset */}
        <div className="chips" role="group" aria-label="Levels shown">
          {LOG_LEVELS.map((l) => (
            <Chip key={l} pressed={shown.has(l)} data-lv={l} onClick={() => toggleLevel(l)}>
              <span className="lv" aria-hidden="true" />
              {LABEL[l]}
              {RANK[l] >= RANK[level] && <span className="k">{formatNumber(counts[l])}</span>}
            </Chip>
          ))}
        </div>
        <label className="input search">
          <Search className="i" aria-hidden="true" />
          <input
            type="search"
            placeholder="Search the logs…"
            aria-label="Search the logs"
            value={text}
            maxLength={200}
            onChange={(ev) => setText(ev.target.value)}
          />
        </label>
        <div className="spacer" />
        <Switch checked={live} onCheckedChange={setLive}>
          Live
        </Switch>
        <span className="divider" aria-hidden="true" />
        <a className={buttonClass('ghost', 'sm')} href={logDownloadUrl} download>
          <Download className="i" aria-hidden="true" />
          Download
        </a>
      </div>
      <div className="logwrap" data-nocomp={hasComponent ? undefined : ''}>
        <section
          ref={scrollRef}
          className="log"
          // biome-ignore lint/a11y/noNoninteractiveTabindex: a scrollable log must be reachable by keyboard (WCAG 2.1.1)
          tabIndex={0}
          aria-label={`Log entries, ${formatNumber(visible.length)} shown`}
          aria-busy={list.isPending || undefined}
          onScroll={onScroll}
        >
          {list.isPending ? (
            <Skeleton className="sk-wanted" />
          ) : list.isError && !entries.length ? (
            <p className="logs-empty" role="alert">
              The logs could not be read: {problemText(list.error)}
            </p>
          ) : empty ? (
            <p className="logs-empty">
              {q ? `No ${shownList} entry mentions “${q}”.` : `Nothing logged at ${shownList} level yet.`}
            </p>
          ) : (
            <ol style={{ height: v.getTotalSize() }}>
              {items.map((it) => {
                const e = visible[it.index]!;
                return (
                  <Line
                    key={e.id}
                    e={e}
                    top={it.start}
                    index={it.index}
                    open={openId === e.id}
                    measure={v.measureElement}
                    onToggle={toggle}
                  />
                );
              })}
            </ol>
          )}
        </section>
        {!follow && unseen > 0 && (
          <button type="button" className="newpill" onClick={jump}>
            <ArrowDownToLine className="i" aria-hidden="true" />
            {formatNumber(unseen)} new <Kbd>End</Kbd>
          </button>
        )}
      </div>
      <div className="vf">
        <span>
          <Radio className="i" aria-hidden="true" />
          {!live
            ? 'Paused: turn Live on to follow new entries.'
            : follow && openId === null
              ? 'Live: new entries appear as they are written.'
              : 'Live, following paused while you read: new entries wait below.'}
        </span>
        <span className="spacer" />
        {list.data && (
          <span>
            {formatNumber(visible.length)} of the last {formatNumber(list.data.capacity)} entries
            {oldest ? ` · oldest ${timeOf(oldest.time)}` : ''}
          </span>
        )}
      </div>
    </section>
  );
}

function ServerLevel() {
  const qc = useQueryClient();
  const s = useQuery(logSettingsQuery);
  const m = useMutation({
    mutationFn: putLogLevel,
    onMutate: (level) => {
      const prev = qc.getQueryData(opsKeys.logSettings);
      if (s.data) qc.setQueryData(opsKeys.logSettings, { ...s.data, level });
      return prev;
    },
    onSuccess: (r) => {
      qc.setQueryData(opsKeys.logSettings, r);
      toast.show({ title: `Server log level: ${LABEL[r.level]}.`, body: 'Until the next restart.' });
    },
    onError: (e, _l, prev) => {
      if (prev) qc.setQueryData(opsKeys.logSettings, prev);
      toast.error('The log level did not change', problemText(e));
    },
  });
  return (
    <HeaderActions>
      <div className="lvlbox">
        <span className="field-label" id="lvl-l">
          Server writes from
        </span>
        {s.data ? (
          <Select
            labelledBy="lvl-l"
            value={s.data.level}
            onValueChange={(l) => m.mutate(l)}
            options={LOG_LEVELS.map((l) => ({ value: l, label: LABEL[l] }))}
          />
        ) : (
          <Skeleton className="sk-row" />
        )}
      </div>
    </HeaderActions>
  );
}

function Facts() {
  const s = useQuery(logSettingsQuery).data;
  if (!s) return null;
  const f = s.file;
  const fact = (icon: ReactNode, body: ReactNode) => (
    <span>
      {icon}
      <span>{body}</span>
    </span>
  );
  return (
    <div className="facts">
      {fact(
        <RefreshCw className="i" aria-hidden="true" />,
        <>
          Level applies at once, until the next restart (<span className="t-mono-sm">GONEMA_LOG_LEVEL</span> wins then)
        </>,
      )}
      {fact(
        <Database className="i" aria-hidden="true" />,
        <>
          In memory: <b>last {formatNumber(s.bufferSize)} entries</b>
        </>,
      )}
      {fact(
        <FileVideo className="i" aria-hidden="true" />,
        f?.enabled ? (
          <>
            File: <span className="t-mono-sm">{f.path}</span> · rotated at {f.maxSizeMB} MB, {f.keep} kept
          </>
        ) : (
          <>
            File: none, the server writes to standard error (<span className="t-mono-sm">GONEMA_LOG_FILE</span> adds
            one)
          </>
        ),
      )}
    </div>
  );
}

export function LogsSection() {
  return (
    <div className="sys-stack logs-stack">
      <ServerLevel />
      <Facts />
      <Viewer />
    </div>
  );
}

/** Settings overview card: the last lines (not virtualised). */
export function RecentLogLines({ count = 4 }: { count?: number }) {
  const list = useQuery({ ...logsQuery('info', '', count), staleTime: 10_000 });
  const entries = list.data?.entries ?? [];
  const lines = useMemo(() => entries.slice(-count), [entries, count]);
  if (list.isPending) return <Skeleton className="sk-row" />;
  if (!lines.length) return <p className="t-secondary t-body-sm">Nothing logged yet.</p>;
  return (
    <ul className="logs-mini" aria-label="Recent log lines">
      {lines.map((e) => (
        <li key={e.id} data-level={e.level}>
          {timeOf(e.time)} {SHORT[e.level]} {e.component ? `${e.component.padEnd(8)} ` : ''}
          {e.message} {attrsText(e.attrs)}
        </li>
      ))}
    </ul>
  );
}

export default LogsSection;
