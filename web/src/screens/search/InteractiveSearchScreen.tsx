import '@/styles/lot2.css';
import { useQueryClient } from '@tanstack/react-query';
import { getRouteApi, Link, useNavigate } from '@tanstack/react-router';
import { useWindowVirtualizer } from '@tanstack/react-virtual';
import {
  ChevronDown,
  ChevronRight,
  ChevronUp,
  CircleCheck,
  CircleX,
  CloudOff,
  Download,
  Info,
  Newspaper,
  RefreshCw,
  Search,
  Sparkles,
} from 'lucide-react';
import { type CSSProperties, type KeyboardEvent, memo, useCallback, useEffect, useMemo, useRef, useState } from 'react';
import { ApiError } from '@/api/client';
import { keys, useMovie, useProfiles } from '@/api/queries';
import type { IndexerStatus, SearchResult } from '@/api/schema';
import { grabResult } from '@/api/screens';
import { ThemeToggle, Topbar } from '@/components/shell/Topbar';
import { HdrFlag, QualityBadge, ScorePill } from '@/components/ui/Badges';
import { Button, buttonClass, IconButton } from '@/components/ui/Button';
import { Chip } from '@/components/ui/Chip';
import { cx } from '@/components/ui/cx';
import { Notice, Skeleton } from '@/components/ui/Feedback';
import { Input } from '@/components/ui/Field';
import { Menu, MenuRadioGroup, MenuRadioItem } from '@/components/ui/Menu';
import { PosterArt } from '@/components/ui/PosterArt';
import { Segmented } from '@/components/ui/Segmented';
import { Switch } from '@/components/ui/Switch';
import { toast } from '@/components/ui/Toast';
import { formatBytes, formatClock, formatRuntime, formatScore } from '@/lib/format';
import { isTypingTarget } from '@/lib/keys';
import { qualityName } from '@/lib/quality';
import { GrabAnywayPopover } from './GrabAnyway';
import { counts, DEFAULT_FILTERS, type Decision, filterResults, resolutionOf, type SearchFilters } from './searchModel';
import { useInteractiveSearch } from './useInteractiveSearch';

const route = getRouteApi('/films/$movieId/search');
const VIRTUAL_AFTER = 100;

export function formatAge(date: string | null | undefined, now = Date.now()): string {
  if (!date) return '—';
  const t = Date.parse(date);
  if (Number.isNaN(t)) return '—';
  const min = Math.max(0, (now - t) / 60_000);
  if (min < 60) return `${Math.max(1, Math.round(min))} min`;
  const h = min / 60;
  if (h < 48) return `${Math.round(h)} h`;
  const d = h / 24;
  if (d < 365) return `${Math.round(d)} d`;
  return `${(d / 365).toFixed(1).replace(/\.0$/, '')} y`;
}

type GrabState = 'grabbing' | 'grabbed';

type Row =
  | { kind: 'group'; id: string; label: string; n: number; note: string }
  | { kind: 'wait'; id: string; indexer: IndexerStatus }
  | { kind: 'sk'; id: string; w: number }
  | { kind: 'release'; id: string; r: SearchResult }
  | { kind: 'detail'; id: string; r: SearchResult };

export function InteractiveSearchScreen() {
  const { movieId } = route.useParams();
  const navigate = useNavigate();
  const qc = useQueryClient();
  const movie = useMovie(movieId);
  const profiles = useProfiles();
  const { state, restart } = useInteractiveSearch(movieId);
  const [filters, setFilters] = useState<SearchFilters>(DEFAULT_FILTERS);
  const [showReasons, setShowReasons] = useState(true);
  const [expanded, setExpanded] = useState<Set<string>>(() => new Set());
  const [grabs, setGrabs] = useState<Map<string, GrabState>>(() => new Map());
  const [confirm, setConfirm] = useState<string | null>(null);
  const tableRef = useRef<HTMLTableSectionElement>(null);

  const m = movie.data;
  useEffect(() => {
    if (m) document.title = `Interactive search · ${m.title} · Gonema`;
  }, [m]);

  const all = useMemo(() => [...state.results.values()], [state.results]);
  const total = useMemo(() => counts(all), [all]);
  const view = useMemo(() => filterResults(all, filters), [all, filters]);
  const pending = state.indexers.filter((x) => x.status === 'pending');
  const failed = state.indexers.filter((x) => x.status === 'failed');
  const qualities = useMemo(
    () =>
      [...new Set(all.map(resolutionOf))].sort((a, b) => (Number.parseInt(b, 10) || 0) - (Number.parseInt(a, 10) || 0)),
    [all],
  );
  const indexerNames = useMemo(() => state.indexers.map((x) => x.name), [state.indexers]);
  const searching = state.status === 'starting' || state.status === 'running';
  const best = view.accepted[0]?.key;

  const toggle = useCallback((key: string) => {
    setExpanded((s) => {
      const n = new Set(s);
      if (n.has(key)) n.delete(key);
      else n.add(key);
      return n;
    });
  }, []);

  const grab = useCallback(
    async (r: SearchResult, override = false) => {
      if (!state.searchId) return;
      setGrabs((g) => new Map(g).set(r.key, 'grabbing'));
      try {
        const q = await grabResult(state.searchId, r.key, override);
        setGrabs((g) => new Map(g).set(r.key, 'grabbed'));
        void qc.invalidateQueries({ queryKey: keys.queue });
        void qc.invalidateQueries({ queryKey: keys.movie(movieId) });
        toast.show({
          title: override ? 'Grabbed on your request' : 'Grabbed',
          body: (
            <>
              <span className="t-mono-sm tbody-rel">{r.title}</span>
              {q.client ? ` Sent to ${q.client}.` : ''}
              {override ? ' Logged as a manual grab; it will not be retried automatically.' : ''}
            </>
          ),
          icon: <Download className="i" />,
          action: { label: 'Open queue', onClick: () => void navigate({ to: '/queue' }) },
        });
      } catch (e) {
        setGrabs((g) => {
          const n = new Map(g);
          n.delete(r.key);
          return n;
        });
        toast.error('The release was not grabbed', e instanceof ApiError ? e.detail : undefined);
      }
    },
    [state.searchId, qc, movieId, navigate],
  );

  const rows = useMemo(() => {
    const out: Row[] = [];
    const push = (r: SearchResult) => {
      out.push({ kind: 'release', id: r.key, r });
      if (expanded.has(r.key)) out.push({ kind: 'detail', id: `${r.key}:d`, r });
    };
    if (view.accepted.length || filters.decision !== 'rejected')
      out.push({ kind: 'group', id: 'g-acc', label: 'Accepted', n: view.accepted.length, note: 'best first' });
    for (const r of view.accepted) push(r);
    for (const ix of pending) {
      out.push({ kind: 'wait', id: `w-${ix.id}`, indexer: ix });
      out.push({ kind: 'sk', id: `s-${ix.id}-1`, w: 72 }, { kind: 'sk', id: `s-${ix.id}-2`, w: 64 });
    }
    if (view.rejected.length)
      out.push({
        kind: 'group',
        id: 'g-rej',
        label: 'Rejected',
        n: view.rejected.length,
        note: showReasons ? 'reason shown under each release' : 'reasons hidden',
      });
    for (const r of view.rejected) push(r);
    return out;
  }, [view, pending, expanded, filters.decision, showReasons]);

  // Keyboard: ↑/↓ between releases, Enter expands, G grabs (rejected: asks first), Esc leaves.
  const onKeyDown = (ev: KeyboardEvent<HTMLTableSectionElement>) => {
    const tr = (ev.target as HTMLElement).closest<HTMLTableRowElement>('tr[data-key]');
    if (!tr || ev.target !== tr) return;
    const key = tr.dataset.key!;
    const list = rows.filter((x) => x.kind === 'release').map((x) => x.id);
    const i = list.indexOf(key);
    if (ev.key === 'ArrowDown' || ev.key === 'ArrowUp') {
      ev.preventDefault();
      const next = list[Math.max(0, Math.min(list.length - 1, i + (ev.key === 'ArrowDown' ? 1 : -1)))];
      if (next) focusRow(next);
    } else if (ev.key === 'Enter') {
      ev.preventDefault();
      toggle(key);
    } else if ((ev.key === 'g' || ev.key === 'G') && !ev.metaKey && !ev.ctrlKey && !ev.altKey) {
      ev.preventDefault();
      const r = state.results.get(key);
      if (!r || grabs.has(key)) return;
      if (r.accepted) void grab(r);
      else setConfirm(key);
    }
  };

  const virtual = rows.length > VIRTUAL_AFTER;
  const scrollMargin = useRef(0);
  const virt = useWindowVirtualizer({
    count: virtual ? rows.length : 0,
    estimateSize: (i) =>
      rows[i]?.kind === 'group' || rows[i]?.kind === 'wait' ? 40 : rows[i]?.kind === 'detail' ? 240 : 64,
    overscan: 8,
    scrollMargin: scrollMargin.current,
    getItemKey: (i) => rows[i]?.id ?? i,
  });
  useEffect(() => {
    if (tableRef.current) scrollMargin.current = tableRef.current.getBoundingClientRect().top + window.scrollY;
  });

  const focusRow = (key: string) => {
    const el = tableRef.current?.querySelector<HTMLElement>(`tr[data-key="${CSS.escape(key)}"]`);
    if (el) el.focus();
    else if (virtual) {
      const i = rows.findIndex((x) => x.id === key);
      virt.scrollToIndex(i, { align: 'auto' });
      requestAnimationFrame(() =>
        tableRef.current?.querySelector<HTMLElement>(`tr[data-key="${CSS.escape(key)}"]`)?.focus(),
      );
    }
  };

  // Esc: back to the film (not from a field, a menu or the confirmation).
  useEffect(() => {
    const onKey = (ev: globalThis.KeyboardEvent) => {
      if (ev.key !== 'Escape' || ev.defaultPrevented || confirm) return;
      if (isTypingTarget(ev.target, '[role="menu"], [role="listbox"], [role="dialog"]')) return;
      void navigate({ to: '/films/$movieId', params: { movieId } });
    };
    window.addEventListener('keydown', onKey);
    return () => window.removeEventListener('keydown', onKey);
  }, [navigate, movieId, confirm]);

  const profile = profiles.data?.find((p) => p.id === m?.qualityProfileId);
  const file = m?.file && (m.file as { id?: number }).id ? m.file : null;
  const color = m?.dominantColor ?? null;
  const visibleRows = virtual ? virt.getVirtualItems() : null;
  const padTop = visibleRows?.length ? visibleRows[0]!.start - virt.options.scrollMargin : 0;
  const padBottom = visibleRows?.length ? virt.getTotalSize() - visibleRows[visibleRows.length - 1]!.end : 0;

  const renderRow = (row: Row, measure?: (el: Element | null) => void, index?: number) => {
    const common = { ref: measure, 'data-index': index } as const;
    switch (row.kind) {
      case 'group':
        return (
          <tr key={row.id} className="grp" {...common}>
            <td colSpan={8}>
              {row.label} <span className="n">· {row.n}</span> <span className="note">{row.note}</span>
            </td>
          </tr>
        );
      case 'wait':
        return (
          <tr key={row.id} className="grp" {...common}>
            <td colSpan={8}>
              <span className="ix wait ix-sm">
                <span className="pulse" aria-hidden="true" />
                Waiting for {row.indexer.name}
              </span>
            </td>
          </tr>
        );
      case 'sk':
        return (
          // biome-ignore lint/a11y/noAriaHiddenOnFocusable: a placeholder row; nothing in it is focusable
          <tr key={row.id} className="sk" aria-hidden="true" {...common}>
            <td>
              <Skeleton style={{ width: 84 }} />
            </td>
            <td>
              <Skeleton style={{ width: `${row.w}%` }} />
              <Skeleton style={{ width: '22%', height: 10, marginTop: 8 }} />
            </td>
            <td>
              <Skeleton style={{ width: 56, marginLeft: 'auto' }} />
            </td>
            <td>
              <Skeleton style={{ width: 32, marginLeft: 'auto' }} />
            </td>
            <td>
              <Skeleton style={{ width: 60 }} />
            </td>
            <td>
              <Skeleton style={{ width: 84 }} />
            </td>
            <td>
              <Skeleton style={{ width: 40, marginLeft: 'auto' }} />
            </td>
            <td />
          </tr>
        );
      case 'release':
        return (
          <ReleaseRow
            key={row.id}
            r={row.r}
            best={row.r.key === best}
            expanded={expanded.has(row.r.key)}
            showReasons={showReasons}
            grab={grabs.get(row.r.key)}
            confirming={confirm === row.r.key}
            onToggle={toggle}
            onGrab={grab}
            onConfirm={setConfirm}
            measure={measure}
            index={index}
          />
        );
      case 'detail':
        return (
          <tr key={row.id} className="detail" {...common}>
            <td colSpan={8}>
              <Breakdown r={row.r} onGrabAnyway={() => setConfirm(row.r.key)} grabbed={grabs.has(row.r.key)} />
            </td>
          </tr>
        );
    }
  };

  return (
    <div
      className="movie-scope search-page-scope"
      style={{ ['--movie-dominant' as string]: color ?? undefined } as CSSProperties}
    >
      <Topbar>
        <nav className="crumbs" aria-label="Breadcrumb">
          <Link to="/films">Films</Link>
          <ChevronRight className="i" aria-hidden="true" />
          <Link to="/films/$movieId" params={{ movieId }}>
            {m?.title ?? '…'}
          </Link>
          <ChevronRight className="i" aria-hidden="true" />
          <span aria-current="page">Interactive search</span>
        </nav>
        <div className="spacer" />
        <ThemeToggle />
      </Topbar>
      <main id="main" className="page search-page" tabIndex={-1}>
        <div className="is-head">
          <div className="thumb">
            <PosterArt src={m?.posterUrl ?? null} title={m?.title ?? ''} color={color} eager />
          </div>
          <div>
            <h1>Interactive search</h1>
            <div className="ctx">
              <span>
                {m
                  ? `${m.title}${m.year ? ` (${m.year})` : ''}${m.runtimeMin ? ` · ${formatRuntime(m.runtimeMin)}` : ''}`
                  : ' '}
              </span>
              {profile && (
                <>
                  <span className="sep" aria-hidden="true" />
                  <span>
                    Profile <b>{profile.name}</b>
                  </span>
                </>
              )}
              <span className="sep" aria-hidden="true" />
              {file ? (
                <span className="row-inline">
                  On disk: {qualityName(file.quality)} <ScorePill score={file.customScore} />
                </span>
              ) : (
                <span>Nothing on disk</span>
              )}
              {profile && (
                <>
                  <span className="sep" aria-hidden="true" />
                  <span>Cutoff: {qualityName(profile.cutoffQuality)}</span>
                </>
              )}
            </div>
          </div>
          <div className="right">
            <Button
              icon={<RefreshCw className="i" aria-hidden="true" />}
              loading={state.status === 'starting'}
              onClick={() => {
                setExpanded(new Set());
                setGrabs(new Map());
                void restart();
              }}
            >
              Search again
            </Button>
            <Link className={buttonClass('ghost')} to="/films/$movieId" params={{ movieId }}>
              Done <span className="kbd">Esc</span>
            </Link>
          </div>
        </div>

        <div className="indexers" role="status" aria-live="polite" aria-label="Indexer progress">
          {state.indexers.map((ix) => (
            <IndexerPill key={ix.id || ix.name} ix={ix} startedAt={state.startedAt} />
          ))}
          {state.status === 'starting' && !state.indexers.length && (
            <span className="t-secondary">Asking your indexers…</span>
          )}
          {(all.length > 0 || state.status === 'finished') && (
            <span className="summary">
              {total.total} {total.total === 1 ? 'release' : 'releases'} ·{' '}
              <b className="acc">{total.accepted} accepted</b> · {total.rejected} rejected
            </span>
          )}
        </div>

        {state.status === 'failed' && state.error && (
          <Notice
            tone="failed"
            role="alert"
            icon={<Search className="i" aria-hidden="true" />}
            title={state.indexers.length ? 'The search stopped.' : 'Gonema could not search for this film.'}
            action={
              <Button size="sm" onClick={() => void restart()}>
                Try again
              </Button>
            }
          >
            {state.error}
          </Notice>
        )}
        {failed.map((ix) => (
          <Notice
            key={ix.id || ix.name}
            icon={<CloudOff className="i" aria-hidden="true" />}
            title={`${ix.name} didn’t answer.`}
            action={
              <Button size="sm" onClick={() => void restart()}>
                Retry
              </Button>
            }
          >
            {ix.error ? `${ix.error}. ` : ''}The other indexers kept searching.
            {ix.willRetryAt ? ` Gonema pauses it until ${formatClock(new Date(ix.willRetryAt))}.` : ''} Retry searches
            every indexer again.
          </Notice>
        ))}

        {(all.length > 0 || searching) && (
          // biome-ignore lint/a11y/useSemanticElements: a labelled group of filter controls, not a form
          <div className="filters" role="group" aria-label="Filter releases">
            <Segmented<Decision>
              label="Decision"
              value={filters.decision}
              onChange={(d) => setFilters((f) => ({ ...f, decision: d }))}
              options={[
                {
                  value: 'all',
                  label: (
                    <>
                      All <span className="t-muted tnum">{view.counts.accepted + view.counts.rejected}</span>
                    </>
                  ),
                },
                {
                  value: 'accepted',
                  label: (
                    <>
                      Accepted <span className="t-muted tnum">{view.counts.accepted}</span>
                    </>
                  ),
                },
                {
                  value: 'rejected',
                  label: (
                    <>
                      Rejected <span className="t-muted tnum">{view.counts.rejected}</span>
                    </>
                  ),
                },
              ]}
            />
            <FilterMenu
              k="Protocol"
              value={filters.protocol}
              options={[
                ['any', 'Any'],
                ['usenet', 'Usenet'],
                ['torrent', 'BitTorrent'],
              ]}
              onChange={(v) => setFilters((f) => ({ ...f, protocol: v as SearchFilters['protocol'] }))}
            />
            <FilterMenu
              k="Quality"
              value={filters.quality}
              options={[['any', 'Any'], ...qualities.map((q) => [q, q] as [string, string])]}
              onChange={(v) => setFilters((f) => ({ ...f, quality: v }))}
            />
            <FilterMenu
              k="Indexer"
              value={filters.indexer}
              options={[['all', 'All'], ...indexerNames.map((n) => [n, n] as [string, string])]}
              onChange={(v) => setFilters((f) => ({ ...f, indexer: v }))}
            />
            <Input
              wrapperClassName="filter-text"
              icon={<Search className="i" aria-hidden="true" />}
              placeholder="Filter by name or group…"
              aria-label="Filter releases by name or group"
              value={filters.text}
              onChange={(ev) => setFilters((f) => ({ ...f, text: ev.target.value }))}
            />
            <Switch checked={showReasons} onCheckedChange={setShowReasons} className="reasons-switch">
              Show reasons
            </Switch>
          </div>
        )}

        {(all.length > 0 || pending.length > 0) && (
          <div className="results-box">
            <table className="rtbl" aria-label={`Releases for ${m?.title ?? 'this film'}, accepted first`}>
              <colgroup>
                <col style={{ width: 132 }} />
                <col />
                <col style={{ width: 88 }} />
                <col style={{ width: 64 }} />
                <col style={{ width: 118 }} />
                <col style={{ width: 136 }} />
                <col style={{ width: 80 }} />
                <col style={{ width: 150 }} />
              </colgroup>
              <thead>
                <tr>
                  <th scope="col">Quality</th>
                  <th scope="col">Release</th>
                  <th scope="col" className="r">
                    Size
                  </th>
                  <th scope="col" className="r">
                    Age
                  </th>
                  <th scope="col">Peers</th>
                  <th scope="col">Indexer</th>
                  <th scope="col" className="r">
                    Score
                  </th>
                  <th scope="col">
                    <span className="sr-only">Action</span>
                  </th>
                </tr>
              </thead>
              {/* biome-ignore lint/a11y/noNoninteractiveElementInteractions: keys of the focusable release rows, delegated */}
              <tbody ref={tableRef} onKeyDown={onKeyDown}>
                {visibleRows ? (
                  <>
                    {padTop > 0 && (
                      // biome-ignore lint/a11y/noAriaHiddenOnFocusable: spacer row of the virtual list, never focusable
                      <tr aria-hidden="true" className="pad">
                        <td colSpan={8} style={{ height: padTop }} />
                      </tr>
                    )}
                    {visibleRows.map((v) => renderRow(rows[v.index]!, virt.measureElement, v.index))}
                    {padBottom > 0 && (
                      // biome-ignore lint/a11y/noAriaHiddenOnFocusable: spacer row of the virtual list, never focusable
                      <tr aria-hidden="true" className="pad">
                        <td colSpan={8} style={{ height: padBottom }} />
                      </tr>
                    )}
                  </>
                ) : (
                  rows.map((row) => renderRow(row))
                )}
              </tbody>
            </table>
            {confirm && state.results.get(confirm) && (
              <GrabAnywayPopover
                r={state.results.get(confirm)!}
                anchor={() =>
                  tableRef.current?.querySelector<HTMLElement>(`[data-grab-anyway="${CSS.escape(confirm)}"]`) ??
                  tableRef.current?.querySelector<HTMLElement>(`tr[data-key="${CSS.escape(confirm)}"]`) ??
                  null
                }
                onCancel={() => {
                  const k = confirm;
                  setConfirm(null);
                  requestAnimationFrame(() => focusRow(k));
                }}
                onConfirm={() => {
                  const r = state.results.get(confirm)!;
                  setConfirm(null);
                  void grab(r, true);
                }}
              />
            )}
          </div>
        )}

        {state.status === 'finished' && all.length === 0 && (
          <div className="empty">
            <span className="glyph" aria-hidden="true">
              <Search className="i" />
            </span>
            <h2>No release out there yet.</h2>
            <p>
              {state.indexers.length
                ? `${state.indexers.filter((x) => x.status === 'done').length} of ${state.indexers.length} indexers answered with nothing for this film.`
                : 'No indexer answered.'}{' '}
              {m?.monitored
                ? 'Gonema keeps watching the RSS feeds for it.'
                : 'Monitor the film to let Gonema watch for it.'}
            </p>
            <div className="actions">
              <Button size="sm" onClick={() => void restart()}>
                Search again
              </Button>
            </div>
          </div>
        )}
      </main>
    </div>
  );
}

function Elapsed({ from }: { from: string | null }) {
  const [now, setNow] = useState(Date.now());
  useEffect(() => {
    const t = setInterval(() => setNow(Date.now()), 100);
    return () => clearInterval(t);
  }, []);
  const s = from ? Math.max(0, (now - Date.parse(from)) / 1000) : 0;
  return <>{s.toFixed(1)} s</>;
}

function IndexerPill({ ix, startedAt }: { ix: IndexerStatus; startedAt: string | null }) {
  if (ix.status === 'pending')
    return (
      <span className="ix wait">
        <span className="pulse" aria-hidden="true" />
        {ix.name}{' '}
        <span className="m">
          searching · <Elapsed from={startedAt} />
        </span>
      </span>
    );
  if (ix.status === 'failed')
    return (
      <span className="ix err">
        <CloudOff className="i" aria-hidden="true" />
        {ix.name} <span className="m">{ix.error ? 'failed' : 'no answer'}</span>
      </span>
    );
  return (
    <span className="ix ok">
      <CircleCheck className="i" aria-hidden="true" />
      {ix.name}{' '}
      <span className="m">
        {ix.count} · {(ix.durationMs / 1000).toFixed(1)} s
      </span>
    </span>
  );
}

function FilterMenu({
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

const flagsOf = (r: SearchResult) => (r.flags ?? []).filter(Boolean).slice(0, 6);

const ReleaseRow = memo(function ReleaseRow({
  r,
  best,
  expanded,
  showReasons,
  grab,
  confirming,
  onToggle,
  onGrab,
  onConfirm,
  measure,
  index,
}: {
  r: SearchResult;
  best: boolean;
  expanded: boolean;
  showReasons: boolean;
  grab: GrabState | undefined;
  confirming: boolean;
  onToggle: (key: string) => void;
  onGrab: (r: SearchResult) => void;
  onConfirm: (key: string) => void;
  measure?: (el: Element | null) => void;
  index?: number;
}) {
  const rej = !r.accepted;
  const torrent = r.protocol === 'torrent';
  const soft = (r.rejections ?? []).every((x) => !x.permanent);
  const extra = (r.sources ?? []).filter((s) => s !== r.indexer).length;
  return (
    <tr
      ref={measure}
      data-index={index}
      data-key={r.key}
      tabIndex={-1}
      className={cx('rel', rej && 'rej', expanded && 'expanded')}
    >
      <td>
        <QualityBadge quality={r.quality} />
      </td>
      <td>
        <div className="rel-name" title={r.title}>
          {r.title}
        </div>
        {(best || flagsOf(r).length > 0) && (
          <div className="rel-tags">
            {best && (
              <span className="best">
                <Sparkles className="i i-xs" aria-hidden="true" />
                Best match
              </span>
            )}
            {flagsOf(r).map((f) => (
              <HdrFlag key={f}>{f}</HdrFlag>
            ))}
          </div>
        )}
        {rej && showReasons && (r.rejections ?? []).length > 0 && (
          <ul className="reasons">
            {(r.rejections ?? []).map((x) => (
              <li key={x.code + x.message} className={cx('reason', !x.permanent && 'soft')}>
                {x.permanent ? <CircleX className="i" aria-hidden="true" /> : <Info className="i" aria-hidden="true" />}
                <span>{x.message}</span>
              </li>
            ))}
          </ul>
        )}
      </td>
      <td className="r muted-cell">{formatBytes(r.size)}</td>
      <td className="r muted-cell">{formatAge(r.publishDate)}</td>
      <td>
        {torrent ? (
          r.seeders >= 0 ? (
            <span className="peers">
              <span className="sr-only">
                {r.seeders} seeders, {Math.max(0, r.leechers)} leechers
              </span>
              <span className="up" aria-hidden="true">
                ▲ {r.seeders}
              </span>
              <span className="dn" aria-hidden="true">
                ▼ {Math.max(0, r.leechers)}
              </span>
            </span>
          ) : (
            <span className="proto">BitTorrent</span>
          )
        ) : (
          <span className="proto">
            <Newspaper className="i" aria-hidden="true" />
            Usenet
          </span>
        )}
      </td>
      <td className="muted-cell">
        {r.indexer}
        {extra > 0 && (
          <span className="t-muted" title={(r.sources ?? []).join(', ')}>
            {' '}
            +{extra}
          </span>
        )}
      </td>
      <td className="r">
        {rej ? (
          <ScorePill score={r.score.total} />
        ) : (
          <ScorePill
            score={r.score.total}
            label={`Score ${formatScore(r.score.total)}, ${expanded ? 'hide' : 'show'} breakdown`}
            onClick={() => onToggle(r.key)}
          />
        )}
      </td>
      <td>
        <div className="grab">
          <IconButton
            size="sm"
            label={`${expanded ? 'Hide' : 'Show'} ${rej ? 'the decision' : 'score details'} for ${r.title}`}
            aria-expanded={expanded}
            onClick={() => onToggle(r.key)}
          >
            {expanded ? (
              <ChevronUp className="i i-sm" aria-hidden="true" />
            ) : rej && !soft ? (
              <Info className="i i-sm" aria-hidden="true" />
            ) : (
              <ChevronDown className="i i-sm" aria-hidden="true" />
            )}
          </IconButton>
          {rej ? (
            soft && (
              <Button
                size="sm"
                variant="secondary"
                data-grab-anyway={r.key}
                aria-haspopup="dialog"
                aria-expanded={confirming}
                disabled={!!grab}
                loading={grab === 'grabbing'}
                onClick={() => onConfirm(r.key)}
              >
                {grab === 'grabbed' ? 'Grabbed' : 'Grab anyway…'}
              </Button>
            )
          ) : (
            <Button
              size="sm"
              variant={best ? 'secondary' : 'ghost'}
              icon={
                grab === 'grabbed' ? (
                  <CircleCheck className="i" aria-hidden="true" />
                ) : (
                  <Download className="i" aria-hidden="true" />
                )
              }
              aria-label={grab === 'grabbed' ? `Grabbed ${r.title}` : `Grab ${r.title}`}
              disabled={!!grab}
              loading={grab === 'grabbing'}
              onClick={() => onGrab(r)}
            >
              {grab === 'grabbed' ? 'Grabbed' : 'Grab'}
            </Button>
          )}
        </div>
      </td>
    </tr>
  );
});

const UPGRADE: Record<string, string> = {
  quality: 'higher in your profile',
  score: 'a higher score',
  revision: 'a newer revision (proper or repack)',
  cutoff: 'it reaches the cutoff',
};

function Breakdown({ r, onGrabAnyway, grabbed }: { r: SearchResult; onGrabAnyway: () => void; grabbed: boolean }) {
  const rules = r.score.rules ?? [];
  return (
    <div className="breakdown">
      <div>
        <p className="bh">Score breakdown</p>
        <ul className="bd-list">
          {rules.length === 0 && <li className="t-muted">No custom format matched this release.</li>}
          {rules.map((x) => (
            <li key={x.rule}>
              <span>{x.rule}</span>
              <b className={x.score > 0 ? 'pos' : x.score < 0 ? 'neg' : 'zero'}>{formatScore(x.score)}</b>
            </li>
          ))}
          <li className="total">
            <span>Total</span>
            <b className={r.score.total > 0 ? 'pos' : r.score.total < 0 ? 'neg' : 'zero'}>
              {formatScore(r.score.total)}
            </b>
          </li>
        </ul>
      </div>
      <div className="decision">
        <p className="bh">Decision</p>
        {r.accepted ? (
          <div className="ok">
            <CircleCheck className="i" aria-hidden="true" />
            <span>
              {r.upgrade
                ? `Accepted as an upgrade: ${qualityName(r.upgrade.from.quality)} (${formatScore(r.upgrade.from.customScore)}) → ${r.qualityName} (${formatScore(r.score.total)})${UPGRADE[r.upgrade.reason] ? `, ${UPGRADE[r.upgrade.reason]}` : ''}.`
                : `Accepted: ${r.qualityName} fits your profile${r.rank === 1 ? ' and ranks first' : `, ranked #${r.rank}`}.`}
            </span>
          </div>
        ) : (
          <div className="no">
            <span>Rejected for {(r.rejections ?? []).length === 1 ? 'this reason' : 'these reasons'}:</span>
          </div>
        )}
        <ul>
          {!r.accepted && (r.rejections ?? []).map((x) => <li key={x.code + x.message}>{x.message}</li>)}
          {(r.notes ?? []).map((n) => (
            <li key={n}>{n}</li>
          ))}
          {r.accepted && r.rank === 1 && <li>Ranked first: best quality, then highest score.</li>}
        </ul>
        {!r.accepted && (
          <div>
            <Button size="sm" variant="secondary" disabled={grabbed} data-grab-anyway={r.key} onClick={onGrabAnyway}>
              {grabbed ? 'Grabbed' : 'Grab anyway…'}
            </Button>
          </div>
        )}
      </div>
    </div>
  );
}
