import { useMutation, useQuery, useQueryClient } from '@tanstack/react-query';
import { useVirtualizer } from '@tanstack/react-virtual';
import { ArrowDownToLine, Download, Search } from 'lucide-react';
import { memo, useCallback, useEffect, useMemo, useRef, useState } from 'react';
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
import { Button, buttonClass } from '@/components/ui/Button';
import { Skeleton } from '@/components/ui/Feedback';
import { Segmented } from '@/components/ui/Segmented';
import { Switch } from '@/components/ui/Switch';
import { toast } from '@/components/ui/Toast';
import { formatNumber } from '@/lib/format';
import { Panel } from './Sections';

const MAX_LINES = 5000;
const LINE_H = 20;
const LABEL: Record<LogLevel, string> = { debug: 'Debug', info: 'Info', warn: 'Warn', error: 'Error' };
const SHORT: Record<LogLevel, string> = { debug: 'DBG', info: 'INF', warn: 'WRN', error: 'ERR' };
const RANK: Record<LogLevel, number> = { debug: 0, info: 1, warn: 2, error: 3 };

const clock = new Intl.DateTimeFormat('en-GB', {
  hour: '2-digit',
  minute: '2-digit',
  second: '2-digit',
  hour12: false,
});

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

const Line = memo(function Line({
  e,
  top,
  index,
  measure,
}: {
  e: LogEntry;
  top: number;
  index: number;
  measure: (el: HTMLElement | null) => void;
}) {
  const attrs = attrsText(e.attrs);
  const d = new Date(e.time);
  return (
    <li
      ref={measure}
      data-index={index}
      className="log-line"
      data-level={e.level}
      style={{ transform: `translateY(${top}px)` }}
    >
      <time dateTime={e.time}>{Number.isNaN(d.getTime()) ? '' : clock.format(d)}</time>
      <span className="lvl">
        <span aria-hidden="true">{SHORT[e.level]}</span>
        <span className="sr-only">{LABEL[e.level]}</span>
      </span>
      <span>
        {e.message}
        {attrs && <span className="attrs"> {attrs}</span>}
      </span>
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
  const [level, setLevel] = useState<LogLevel>('info');
  const [text, setText] = useState('');
  const q = useDebounced(text.trim(), 250);
  const [live, setLive] = useState(true);
  const [entries, setEntries] = useState<LogEntry[]>([]);
  const [follow, setFollow] = useState(true);
  const [unseen, setUnseen] = useState(0);
  const list = useQuery(logsQuery(level, q));
  const scrollRef = useRef<HTMLElement>(null);

  // The recent entries (server-side filter), then the live tail on top of them.
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

  const v = useVirtualizer({
    count: entries.length,
    getScrollElement: () => scrollRef.current,
    estimateSize: () => LINE_H,
    overscan: 20,
    getItemKey: (i) => entries[i]?.id ?? i,
  });

  // Follow the tail while the reader is at the bottom; otherwise count what arrives.
  const prevLen = useRef(0);
  useEffect(() => {
    const added = entries.length - prevLen.current;
    prevLen.current = entries.length;
    if (!entries.length) return;
    if (follow) v.scrollToIndex(entries.length - 1, { align: 'end' });
    else if (added > 0) setUnseen((n) => n + added);
  }, [entries, follow, v]);

  const onScroll = useCallback(() => {
    const el = scrollRef.current;
    if (!el) return;
    const atBottom = el.scrollHeight - el.scrollTop - el.clientHeight < LINE_H * 2;
    setFollow(atBottom);
    if (atBottom) setUnseen(0);
  }, []);

  const items = v.getVirtualItems();
  const empty = !list.isPending && !entries.length;
  return (
    <Panel
      id="logs-viewer"
      title="Recent entries"
      aside={
        <a className={buttonClass('ghost', 'sm')} href={logDownloadUrl} download>
          <Download className="i" aria-hidden="true" />
          Download
        </a>
      }
    >
      <div className="logs-tools">
        <Segmented
          label="Show from level"
          value={level}
          onChange={setLevel}
          options={LOG_LEVELS.map((l) => ({ value: l, label: LABEL[l] }))}
        />
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
      </div>
      <div className="logs-wrap">
        <section
          ref={scrollRef}
          className="logs-view"
          // biome-ignore lint/a11y/noNoninteractiveTabindex: a scrollable log must be reachable by keyboard (WCAG 2.1.1)
          tabIndex={0}
          aria-label={`Log entries, ${formatNumber(entries.length)} shown`}
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
              {q
                ? `No entry at ${LABEL[level].toLowerCase()} level or above mentions “${q}”.`
                : 'Nothing logged at this level yet.'}
            </p>
          ) : (
            <ol style={{ height: v.getTotalSize() }}>
              {items.map((it) => {
                const e = entries[it.index]!;
                return <Line key={e.id} e={e} top={it.start} index={it.index} measure={v.measureElement} />;
              })}
            </ol>
          )}
        </section>
        {!follow && unseen > 0 && (
          <Button
            size="sm"
            className="jump-latest"
            icon={<ArrowDownToLine className="i" aria-hidden="true" />}
            onClick={() => {
              setFollow(true);
              setUnseen(0);
              v.scrollToIndex(entries.length - 1, { align: 'end' });
            }}
          >
            {`${formatNumber(unseen)} new`}
          </Button>
        )}
      </div>
      <div className="logs-foot">
        <span>
          {live ? 'Live: new entries appear as they are written.' : 'Paused: turn Live on to follow new entries.'}
        </span>
        {list.data && <span>Gonema keeps the last {formatNumber(list.data.capacity)} entries in memory.</span>}
      </div>
    </Panel>
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
  const file = s.data?.file;
  return (
    <Panel id="logs-level" title="What Gonema writes">
      {!s.data ? (
        <Skeleton className="sk-row" />
      ) : (
        <>
          <div className="row">
            <Segmented
              label="Server log level"
              value={s.data.level}
              onChange={(l) => m.mutate(l)}
              options={LOG_LEVELS.map((l) => ({ value: l, label: LABEL[l] }))}
            />
          </div>
          <p className="explain">
            Applies at once and lasts until Gonema restarts; the start option (<code>GONEMA_LOG_LEVEL</code>) wins then.
            Debug is verbose: use it while chasing a problem.
          </p>
          <dl className="kv">
            <dt>In memory</dt>
            <dd>last {formatNumber(s.data.bufferSize)} entries</dd>
            <dt>Log file</dt>
            <dd>
              {file?.enabled ? (
                <>
                  <code>{file.path}</code> · rotated at {file.maxSizeMB} MB, {file.keep} kept
                </>
              ) : (
                'None: the server writes to its standard error (GONEMA_LOG_FILE adds a file).'
              )}
            </dd>
          </dl>
        </>
      )}
    </Panel>
  );
}

export function LogsSection() {
  return (
    <>
      <ServerLevel />
      <Viewer />
    </>
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
          {clock.format(new Date(e.time))} {SHORT[e.level]} {e.message} {attrsText(e.attrs)}
        </li>
      ))}
    </ul>
  );
}

export default LogsSection;
