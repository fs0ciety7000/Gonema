import { useInfiniteQuery } from '@tanstack/react-query';
import { getRouteApi, Link, useNavigate } from '@tanstack/react-router';
import { useWindowVirtualizer } from '@tanstack/react-virtual';
import { ChevronDown, Download, History as HistoryIcon, Info, X } from 'lucide-react';
import { useEffect, useLayoutEffect, useMemo, useRef } from 'react';
import { API_ROOT } from '@/api/client';
import { useLibrary } from '@/api/queries';
import type { HistoryRow } from '@/api/schema';
import { fetchHistory, type HistoryQuery } from '@/api/screens';
import { QualityBadge, ScorePill } from '@/components/ui/Badges';
import { Button, buttonClass } from '@/components/ui/Button';
import { Chip } from '@/components/ui/Chip';
import { cx } from '@/components/ui/cx';
import { Notice, Skeleton } from '@/components/ui/Feedback';
import { Menu, MenuRadioGroup, MenuRadioItem } from '@/components/ui/Menu';
import { PosterArt } from '@/components/ui/PosterArt';
import { formatWhen } from '@/lib/format';
import { qualityName } from '@/lib/quality';
import { EVENT } from '@/screens/movie/parts';

const route = getRouteApi('/history');
const PAGE = 100;

export type EventKind = 'grab' | 'import' | 'failure' | 'other';
/** Server-side event filters of each kind ("other" is everything else, filtered here). */
const EVENTS_OF: Record<string, NonNullable<HistoryQuery['events']>> = {
  grab: ['grabbed', 'grab_overridden'],
  import: ['imported', 'upgraded', 'file_renamed'],
  failure: ['grab_failed', 'download_failed', 'import_failed', 'blocklisted', 'retries_exhausted', 'retry_scheduled'],
};
const KINDS: ReadonlyArray<readonly [string, string]> = [
  ['all', 'All'],
  ['grab', 'Grabs'],
  ['import', 'Imports & upgrades'],
  ['failure', 'Failures'],
  ['other', 'Other'],
];
const SINCE: ReadonlyArray<readonly [string, string]> = [
  ['any', 'Any time'],
  ['1d', 'Last 24 hours'],
  ['7d', 'Last 7 days'],
  ['30d', 'Last 30 days'],
];
const SINCE_MS: Record<string, number> = { '1d': 86_400_000, '7d': 7 * 86_400_000, '30d': 30 * 86_400_000 };

export const kindOf = (event: string): EventKind => (EVENT[event]?.kind ?? 'other') as EventKind;

/** Entries that belong to one download (grabbed → imported → upgraded) are drawn as a chain. */
export function chainFlags(list: ReadonlyArray<Pick<HistoryRow, 'chainId' | 'downloadId'>>): boolean[] {
  const id = (e: Pick<HistoryRow, 'chainId' | 'downloadId'> | undefined) => e?.chainId || e?.downloadId || 0;
  return list.map((e, i) => {
    const c = id(e);
    return !!c && (id(list[i - 1]) === c || id(list[i + 1]) === c);
  });
}

function useFilters() {
  const s = route.useSearch();
  return { event: s.event ?? 'all', movie: s.movie, since: s.since ?? 'any' };
}

export function HistoryToolbar() {
  const f = useFilters();
  const navigate = useNavigate();
  const lib = useLibrary();
  const set = (p: Record<string, string | number | undefined>) =>
    void navigate({ to: '/history', search: (s) => ({ ...s, ...p }), replace: true });
  const film = f.movie ? lib.data?.find((m) => m.id === f.movie) : undefined;
  return (
    <>
      <ChipMenu
        k="Event"
        value={f.event}
        options={KINDS}
        onChange={(v) => set({ event: v === 'all' ? undefined : v })}
      />
      {f.movie ? (
        <span className="chip is-on">
          <span className="k">Film</span> {film?.title ?? `#${f.movie}`}
          <button
            className="remove"
            type="button"
            aria-label="Remove filter Film"
            onClick={() => set({ movie: undefined })}
          >
            <X className="i i-xs" aria-hidden="true" />
          </button>
        </span>
      ) : null}
      <ChipMenu
        k="When"
        value={f.since}
        options={SINCE}
        onChange={(v) => set({ since: v === 'any' ? undefined : v })}
      />
      <a
        className={buttonClass('ghost', 'sm')}
        href={exportUrl(f)}
        download
        title="Every entry matching these filters, as a CSV file"
      >
        <Download className="i" aria-hidden="true" />
        Export
      </a>
    </>
  );
}

/** CSV export of GET /history/export with the filters on screen ("Other" exports every event). */
export function exportUrl(f: { event: string; movie?: number; since: string }, now = Date.now()): string {
  const q = new URLSearchParams({ format: 'csv' });
  const events = EVENTS_OF[f.event];
  if (events) q.set('event', events.join(','));
  if (f.movie) q.set('movieId', String(f.movie));
  const ms = SINCE_MS[f.since];
  if (ms) q.set('since', new Date(now - ms).toISOString());
  return `${API_ROOT}/history/export?${q}`;
}

function ChipMenu({
  k,
  value,
  options,
  onChange,
}: {
  k: string;
  value: string;
  options: ReadonlyArray<readonly [string, string]>;
  onChange: (v: string) => void;
}) {
  const label = options.find((o) => o[0] === value)?.[1] ?? value;
  return (
    <Menu
      label={k}
      align="end"
      trigger={
        <Chip pressed={value !== options[0]?.[0]} aria-label={`${k}: ${label}`}>
          <span className="k">{k}</span> {label}
          <ChevronDown className="i" aria-hidden="true" />
        </Chip>
      }
    >
      <MenuRadioGroup value={value} onValueChange={(v) => onChange(String(v))}>
        {options.map(([v, l]) => (
          <MenuRadioItem key={v} value={v} selected={v === value}>
            {l}
          </MenuRadioItem>
        ))}
      </MenuRadioGroup>
    </Menu>
  );
}

type Data = { reason?: unknown; previous?: { quality?: unknown } | null };
const str = (v: unknown) => (typeof v === 'string' && v ? v : null);

export function HistoryPanel() {
  const f = useFilters();
  // The cut-off is fixed when the filter is chosen (an hour boundary keeps the query key stable).
  const since = useMemo(
    () =>
      SINCE_MS[f.since]
        ? new Date(Math.floor((Date.now() - SINCE_MS[f.since]!) / 3_600_000) * 3_600_000).toISOString()
        : undefined,
    [f.since],
  );
  const events = EVENTS_OF[f.event];

  const q = useInfiniteQuery({
    queryKey: ['history', 'all', f.event, f.movie ?? 0, since ?? ''],
    initialPageParam: '',
    queryFn: ({ pageParam, signal }) =>
      fetchHistory({ events, movieId: f.movie, since, cursor: pageParam || undefined, limit: PAGE }, signal),
    getNextPageParam: (last) => last.nextCursor || undefined,
    staleTime: 15_000,
  });

  const all = useMemo(() => (q.data?.pages ?? []).flatMap((p) => p.entries ?? []), [q.data]);
  const list = useMemo(
    () => (f.event === 'other' ? all.filter((e) => kindOf(e.event) === 'other') : all),
    [all, f.event],
  );
  const chain = useMemo(() => chainFlags(list), [list]);
  const canMore = !!q.hasNextPage;

  const tbody = useRef<HTMLTableSectionElement>(null);
  const margin = useRef(0);
  useLayoutEffect(() => {
    if (tbody.current) margin.current = tbody.current.getBoundingClientRect().top + window.scrollY;
  });
  const virt = useWindowVirtualizer({
    count: list.length,
    estimateSize: () => 88,
    overscan: 10,
    scrollMargin: margin.current,
    getItemKey: (i) => list[i]?.id ?? i,
  });
  const items = virt.getVirtualItems();
  const lastIndex = items[items.length - 1]?.index ?? 0;
  // Load the next page as the end comes into view; keep loading while filters hide everything loaded.
  useEffect(() => {
    if (!canMore || q.isFetchingNextPage) return;
    if (lastIndex >= list.length - 15 || list.length < 30) void q.fetchNextPage();
  }, [lastIndex, list.length, canMore, q]);

  if (q.isPending)
    return (
      <div className="hist" aria-hidden="true">
        {[0, 1, 2, 3, 4].map((i) => (
          <div className="hist-sk" key={i}>
            <Skeleton style={{ width: 80 }} delay={i * 60} />
            <Skeleton style={{ width: 140 }} delay={i * 60} />
            <Skeleton style={{ width: '60%' }} delay={i * 60} />
          </div>
        ))}
      </div>
    );
  if (q.isError)
    return (
      <Notice
        tone="failed"
        role="alert"
        title="History could not be loaded."
        action={
          <Button size="sm" onClick={() => void q.refetch()}>
            Try again
          </Button>
        }
      >
        {q.error.message}
      </Notice>
    );
  const filtered = f.event !== 'all' || !!f.movie || f.since !== 'any';
  if (!list.length && !canMore)
    return (
      <div className="empty">
        <span className="glyph" aria-hidden="true">
          <HistoryIcon className="i" />
        </span>
        <h2>{filtered ? 'Nothing of this kind here.' : 'No reels logged yet.'}</h2>
        <p>
          {filtered
            ? 'No event matches these filters. Loosen one to see more.'
            : 'Every grab, import, upgrade and failure will be written here, with the reason behind it.'}
        </p>
        {!filtered && (
          <div className="actions">
            <Link className="btn btn-secondary btn-sm" to="/discover">
              Add a film
            </Link>
          </div>
        )}
      </div>
    );

  const padTop = items.length ? items[0]!.start - virt.options.scrollMargin : 0;
  const padBottom = items.length ? virt.getTotalSize() - items[items.length - 1]!.end : 0;

  return (
    <div className="hist">
      <table className="tbl" aria-label="History, newest first" aria-rowcount={list.length + 1}>
        <colgroup>
          <col style={{ width: 120 }} />
          <col style={{ width: 210 }} />
          <col style={{ width: 170 }} />
          <col />
          <col style={{ width: 148 }} />
          <col style={{ width: 76 }} />
        </colgroup>
        <thead>
          <tr aria-rowindex={1}>
            <th scope="col">When</th>
            <th scope="col">Film</th>
            <th scope="col">Event</th>
            <th scope="col">What happened and why</th>
            <th scope="col">Quality</th>
            <th scope="col" className="r">
              Score
            </th>
          </tr>
        </thead>
        <tbody ref={tbody}>
          {padTop > 0 && (
            // biome-ignore lint/a11y/noAriaHiddenOnFocusable: spacer row of the virtual list, never focusable
            <tr aria-hidden="true" className="pad">
              <td colSpan={6} style={{ height: padTop }} />
            </tr>
          )}
          {items.map((v) => {
            const e = list[v.index]!;
            const meta = EVENT[e.event] ?? { label: e.event.replace(/_/g, ' '), icon: Info, kind: 'other' as const };
            const Icon = meta.icon;
            const d = (e.data ?? {}) as Data;
            const m = e.movie?.id ? e.movie : undefined;
            const prevQ = str(d.previous?.quality);
            const linked = chain[v.index];
            const reason = e.reason || str(d.reason) || '';
            return (
              <tr
                key={e.id}
                ref={virt.measureElement}
                data-index={v.index}
                aria-rowindex={v.index + 2}
                className={cx(linked && 'chain')}
              >
                <td className="when">
                  <time dateTime={e.createdAt}>{formatWhen(e.createdAt)}</time>
                </td>
                <td>
                  {m ? (
                    <Link className="film" to="/films/$movieId" params={{ movieId: m.id }}>
                      <PosterArt className="thumb" src={m.posterUrl} title={m.title} color={m.dominantColor} />
                      <span>{m.title}</span>
                    </Link>
                  ) : (
                    <span className="film t-secondary">{e.movieId ? `Film #${e.movieId}` : 'Removed film'}</span>
                  )}
                </td>
                <td>
                  <span className="status" data-status={meta.status ?? 'unmonitored'}>
                    <Icon className="i" aria-hidden="true" />
                    {meta.label}
                  </span>
                  {linked && <span className="sr-only">, same download as the neighbouring rows</span>}
                </td>
                <td className="reason">
                  {e.event === 'upgraded' && prevQ ? `From ${qualityName(prevQ)}. ` : ''}
                  {reason}
                  {e.sourceTitle && e.sourceTitle !== m?.title && (
                    <span className="rel" title={e.sourceTitle}>
                      {e.sourceTitle}
                    </span>
                  )}
                </td>
                <td>{e.quality && <QualityBadge quality={e.quality} />}</td>
                <td className="r">
                  {e.customScore !== null && e.customScore !== undefined && <ScorePill score={e.customScore} />}
                </td>
              </tr>
            );
          })}
          {padBottom > 0 && (
            // biome-ignore lint/a11y/noAriaHiddenOnFocusable: spacer row of the virtual list, never focusable
            <tr aria-hidden="true" className="pad">
              <td colSpan={6} style={{ height: padBottom }} />
            </tr>
          )}
        </tbody>
      </table>
      (q.isFetchingNextPage || canMore) && (
      <p className="hist-more t-caption t-muted" aria-live="polite">
        {q.isFetchingNextPage ? 'Loading earlier events…' : ' '}
      </p>
      )
    </div>
  );
}
