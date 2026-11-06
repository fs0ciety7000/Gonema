import { getRouteApi, Link, useNavigate } from '@tanstack/react-router';
import { ListFilter, Plus, ScanSearch } from 'lucide-react';
import {
  type ComponentPropsWithoutRef,
  lazy,
  type ReactNode,
  Suspense,
  useCallback,
  useEffect,
  useMemo,
  useRef,
  useState,
} from 'react';
import { computeStats, type LibraryItem } from '@/api/model';
import { useLibrary, useQueue, useRootFolders } from '@/api/queries';
import { MovieMenuItems } from '@/components/movie/MovieMenuItems';
import type { LiveDownload } from '@/components/movie/PosterCard';
import { useMovieActions } from '@/components/movie/useMovieActions';
import { AddFilmButton, QueuePill, SearchField, ThemeToggle, Topbar } from '@/components/shell/Topbar';
import { Button, buttonClass } from '@/components/ui/Button';
import { Deferred } from '@/components/ui/Deferred';
import { EmptyState, Notice } from '@/components/ui/Feedback';
import { Tabs } from '@/components/ui/Tabs';
import { setBrowseOrder } from '@/lib/browseOrder';
import { formatBytes, formatNumber } from '@/lib/format';
import { isTypingTarget } from '@/lib/keys';
import { reachable } from '@/lib/nav';
import { useDelayedFlag } from '@/lib/useDelayedFlag';
import { openMovieTransition, takeLastOpened } from '@/lib/viewTransition';
import mascotEmpty from '../../../../docs/brand/kit/mascot/kernel-empty-library.svg?url';
import { COLUMNS, type ColumnId, LibraryTable } from './LibraryTable';
import {
  applyFilters,
  DEFAULT_DIR,
  type Density,
  FILTER_KEYS,
  type Filters,
  groupItems,
  type Layout,
  type LibrarySearch,
  pickFilters,
  type SortKey,
  sortItems,
} from './model';
import { PosterGrid } from './PosterGrid';
import { ActiveFilters, ColumnsMenu, GroupMenu, LayoutControls, SortMenu } from './Toolbar';
import { useSavedViews } from './useSavedViews';
import { useSelection } from './useSelection';

const route = getRouteApi('/films');

// Interaction-only pieces (popovers with fields, the selection bar) load after the first paint.
const FiltersButton = lazy(() => import('./FiltersPopover').then((m) => ({ default: m.FiltersButton })));
const SaveViewButton = lazy(() => import('./FiltersPopover').then((m) => ({ default: m.SaveViewButton })));
const BulkBar = lazy(() => import('./BulkBar').then((m) => ({ default: m.BulkBar })));

const COLS_KEY = 'gn-table-columns';
function readColumns(): Set<ColumnId> {
  try {
    const raw = JSON.parse(localStorage.getItem(COLS_KEY) ?? 'null') as unknown;
    if (Array.isArray(raw)) return new Set(raw.filter((c): c is ColumnId => COLUMNS.some((x) => x.id === c)));
  } catch {
    /* default */
  }
  return new Set(COLUMNS.map((c) => c.id));
}

const densityKey = (view: string, layout: Layout) => `gn-density:${view}:${layout}`;
function rememberedDensity(view: string, layout: Layout): Density {
  try {
    const v = localStorage.getItem(densityKey(view, layout));
    if (v === 'airy' || v === 'compact') return v;
  } catch {
    /* default */
  }
  return layout === 'table' ? 'compact' : 'airy';
}

export function LibraryScreen() {
  const search = route.useSearch();
  const navigate = useNavigate();
  const lib = useLibrary();
  const queue = useQueue();
  const roots = useRootFolders();
  const actions = useMovieActions();
  const { views, add: addView, saving } = useSavedViews();

  const viewId = search.view ?? 'all';
  const layout: Layout = search.layout ?? 'grid';
  const density: Density = search.density ?? rememberedDensity(viewId, layout);
  const sort: SortKey = search.sort ?? 'added';
  const dir = search.dir ?? DEFAULT_DIR[sort];
  const group = search.group ?? 'none';
  const filters = pickFilters(search);

  const setSearch = useCallback(
    (patch: Partial<LibrarySearch>, replace = true) =>
      void navigate({
        to: '/films',
        search: (prev: LibrarySearch) => {
          const next: Record<string, unknown> = { ...prev, ...patch };
          for (const k of Object.keys(next)) if (next[k] === undefined) delete next[k];
          return next as LibrarySearch;
        },
        replace,
        resetScroll: false,
      }),
    [navigate],
  );

  const all = lib.data ?? [];
  const stats = useMemo(() => computeStats(all), [all]);
  const filtersKey = JSON.stringify(filters);
  // biome-ignore lint/correctness/useExhaustiveDependencies: filters is rebuilt each render; its JSON is the identity
  const filtered = useMemo(() => applyFilters(all, filters), [all, filtersKey]);
  const sorted = useMemo(() => sortItems(filtered, sort, dir), [filtered, sort, dir]);
  const groups = useMemo(() => groupItems(sorted, layout === 'grid' ? group : 'none'), [sorted, group, layout]);
  const ordered = useMemo(() => (groups.length === 1 ? sorted : groups.flatMap((g) => g.items)), [groups, sorted]);
  const order = useMemo(() => ordered.map((m) => m.id), [ordered]);
  const genres = useMemo(() => [...new Set(all.flatMap((m) => m.genres))].sort(), [all]);
  const counts = useMemo(() => new Map(views.map((v) => [v.id, applyFilters(all, v.filters).length])), [all, views]);

  const selection = useSelection(order);
  const { prune } = selection;
  useEffect(() => {
    if (lib.data) prune(new Set(lib.data.map((m) => m.id)));
  }, [lib.data, prune]);
  const selectedItems = useMemo(
    () => ordered.filter((m) => selection.selected.has(m.id)),
    [ordered, selection.selected],
  );

  const live = useMemo(() => {
    const map = new Map<number, LiveDownload>();
    for (const q of queue.data ?? [])
      map.set(q.movieId, {
        progress: q.progress,
        speedBps: q.speedBps,
        etaSeconds: q.etaSeconds,
        paused: q.status === 'paused',
      });
    return map;
  }, [queue.data]);

  const rootOf = useMemo(() => new Map((roots.data ?? []).map((r) => [r.id, r.path])), [roots.data]);
  const [columns, setColumns] = useState<Set<ColumnId>>(readColumns);

  // First paint of a view staggers in; later renders never do.
  const seen = useRef(new Set<string>());
  const [entering, setEntering] = useState(false);
  const viewKey = `${viewId}:${layout}`;
  useEffect(() => {
    if (!lib.data || seen.current.has(viewKey)) return;
    seen.current.add(viewKey);
    setEntering(true);
    const t = setTimeout(() => setEntering(false), 700);
    return () => clearTimeout(t);
  }, [lib.data, viewKey]);

  const epoch = useMemo(() => JSON.stringify([filters, sort, dir, group]), [filters, sort, dir, group]);
  const epochN = useRef(0);
  const lastEpoch = useRef(epoch);
  if (lastEpoch.current !== epoch) {
    lastEpoch.current = epoch;
    epochN.current++;
  }

  const [focusId] = useState(() => takeLastOpened());
  const showSkeleton = useDelayedFlag(lib.isPending);

  // G / T switch layout (not while typing).
  useEffect(() => {
    const onKey = (ev: KeyboardEvent) => {
      if (ev.metaKey || ev.ctrlKey || ev.altKey) return;
      if (isTypingTarget(ev.target, '[role="menu"], [role="listbox"]')) return;
      if (ev.key === 'g' || ev.key === 'G') setSearch({ layout: undefined, density: undefined });
      else if (ev.key === 't' || ev.key === 'T') setSearch({ layout: 'table', density: undefined });
    };
    window.addEventListener('keydown', onKey);
    return () => window.removeEventListener('keydown', onKey);
  }, [setSearch]);

  const hrefOf = useCallback((m: LibraryItem) => `${document.baseURI.replace(/\/$/, '')}/films/${m.id}`, []);
  const onOpen = useCallback(
    (m: LibraryItem, frame: HTMLElement | null) => {
      setBrowseOrder(order);
      void openMovieTransition(m.id, frame, () => navigate({ to: '/films/$movieId', params: { movieId: m.id } }));
    },
    [navigate, order],
  );
  const onSearchFilm = useCallback((m: LibraryItem) => void actions.search([{ id: m.id, title: m.title }]), [actions]);
  const onToggleMonitor = useCallback(
    (m: LibraryItem) => void actions.setMonitored([{ id: m.id, title: m.title }], !m.monitored),
    [actions],
  );
  const menu = useCallback(
    (m: LibraryItem) => (
      <MovieMenuItems movie={m} onOpen={() => onOpen(m, document.querySelector(`[data-poster-frame="${m.id}"]`))} />
    ),
    [onOpen],
  );

  const changeFilters = (f: Partial<Filters>) => setSearch(f);
  const clearFilters = () =>
    setSearch(Object.fromEntries(FILTER_KEYS.map((k) => [k, undefined])) as Partial<LibrarySearch>);
  const currentView = views.find((v) => v.id === viewId) ?? views[0]!;
  const selectView = (id: string) => {
    const v = views.find((x) => x.id === id);
    if (!v) return;
    const cleared = Object.fromEntries(FILTER_KEYS.map((k) => [k, undefined]));
    setSearch(
      { ...cleared, ...v.filters, view: id === 'all' ? undefined : id, sort: v.sort, dir: v.dir, density: undefined },
      false,
    );
  };

  const emptyLibrary = lib.isSuccess && all.length === 0;
  const noMatch = lib.isSuccess && all.length > 0 && filtered.length === 0;
  const isTable = layout === 'table';
  const rootPath = useCallback((m: LibraryItem) => rootOf.get(m.rootFolderId), [rootOf]);
  const firstRoot = roots.data?.[0]?.path;

  const viewTabs = (
    <Tabs
      label="Saved views"
      panelId="library-panel"
      value={currentView.id}
      onChange={selectView}
      className={isTable ? 'tabs-plain' : undefined}
      items={views.map((v, i) => ({
        value: v.id,
        label: v.name,
        count: lib.data ? formatNumber(counts.get(v.id) ?? 0) : undefined,
        className: i >= 3 ? 'hide-tablet' : undefined,
      }))}
    />
  );

  return (
    <>
      <Topbar>
        <SearchField value={search.q ?? ''} onSearch={(q) => setSearch({ q: q || undefined })} />
        <div className="spacer" />
        <QueuePill />
        <ThemeToggle />
        <AddFilmButton />
      </Topbar>
      <main
        id="main"
        className={isTable ? 'page library-page is-table' : 'page library-page'}
        data-density={density}
        tabIndex={-1}
      >
        <div className="page-head">
          <div>
            <h1>Films</h1>
            <p className="sub" aria-live="polite">
              {lib.data
                ? `${formatNumber(stats.total)} films · ${formatNumber(stats.onDisk)} on disk${stats.totalSize ? ` · ${formatBytes(stats.totalSize)}` : ''}`
                : ' '}
            </p>
          </div>
          <div className="toolbar" role="toolbar" aria-label="Library display">
            <Deferred
              placeholder={(intent) => (
                <StaticToolButton label="Filters" icon={<ListFilter className="i" aria-hidden="true" />} {...intent} />
              )}
            >
              {(openNow) => (
                <FiltersButton filters={filters} genres={genres} onChange={changeFilters} defaultOpen={openNow} />
              )}
            </Deferred>
            {!isTable && (
              <SortMenu sort={sort} dir={dir} onChange={(s, d) => setSearch({ sort: s, dir: d ?? DEFAULT_DIR[s] })} />
            )}
            {!isTable && (
              <GroupMenu group={group} onChange={(g) => setSearch({ group: g === 'none' ? undefined : g })} />
            )}
            {isTable && (
              <ColumnsMenu
                columns={columns}
                onChange={(c) => {
                  setColumns(c);
                  try {
                    localStorage.setItem(COLS_KEY, JSON.stringify([...c]));
                  } catch {
                    /* keep in memory */
                  }
                }}
              />
            )}
            <span className="divider" aria-hidden="true" />
            <LayoutControls
              layout={layout}
              density={density}
              onLayout={(l) => setSearch({ layout: l === 'grid' ? undefined : l, density: undefined }, false)}
              onDensity={(d) => {
                try {
                  localStorage.setItem(densityKey(viewId, layout), d);
                } catch {
                  /* keep in URL */
                }
                setSearch({ density: d });
              }}
            />
          </div>
        </div>

        {isTable ? (
          <div className="table-bar">
            {viewTabs}
            <div className="sel" aria-live="polite">
              {selection.selected.size > 0 && <span>{formatNumber(selection.selected.size)} selected</span>}
              {filtered.length > 0 && selection.selected.size < filtered.length && (
                <Button variant="ghost" size="sm" onClick={selection.selectAll}>
                  Select all {formatNumber(filtered.length)}
                </Button>
              )}
            </div>
          </div>
        ) : (
          <div className="views">
            {viewTabs}
            <Deferred
              placeholder={(intent) => (
                <StaticToolButton
                  label="View"
                  icon={<Plus className="i" aria-hidden="true" />}
                  aria-label="Save current filters as a view"
                  {...intent}
                />
              )}
            >
              {(openNow) => (
                <SaveViewButton
                  defaultOpen={openNow}
                  saving={saving}
                  onSave={async (name) => {
                    const id = await addView({
                      name,
                      filters: pickFilters({ ...filters, q: undefined }),
                      sort,
                      dir,
                      density,
                    });
                    if (id) setSearch({ view: id });
                    return id !== null;
                  }}
                />
              )}
            </Deferred>
          </div>
        )}

        <ActiveFilters filters={filters} onChange={changeFilters} onClear={clearFilters} />

        {lib.isError && (
          <Notice
            tone="failed"
            role="alert"
            title="The library could not be loaded."
            action={
              <Button size="sm" onClick={() => void lib.refetch()}>
                Try again
              </Button>
            }
          >
            {lib.error.message} Gonema keeps retrying in the background.
          </Notice>
        )}

        {emptyLibrary ? (
          <EmptyState
            title="The house is dark."
            mascot={mascotEmpty}
            action={
              reachable('discover') ? (
                <Link className={buttonClass('primary')} to={'/discover' as never}>
                  <Plus className="i" aria-hidden="true" />
                  Add a film
                </Link>
              ) : undefined
            }
          >
            Add a film and we’ll start the screening. Already have a collection on disk? Importing a folder matches each
            film.
          </EmptyState>
        ) : noMatch ? (
          <div className="empty">
            <span className="glyph" aria-hidden="true">
              <ScanSearch className="i" />
            </span>
            <h2>No films match.</h2>
            <p>Nothing in the library fits these filters. Loosen one, or clear them all.</p>
            <div className="actions">
              <Button size="sm" onClick={clearFilters}>
                Clear filters
              </Button>
            </div>
          </div>
        ) : isTable ? (
          <>
            <LibraryTable
              items={ordered}
              selection={selection}
              live={live}
              columns={columns}
              sort={sort}
              dir={dir}
              onSort={(k) =>
                setSearch({ sort: k, dir: k === sort ? (dir === 'asc' ? 'desc' : 'asc') : DEFAULT_DIR[k] })
              }
              density={density}
              rootPath={rootPath}
              pathRoot={firstRoot}
              hrefOf={hrefOf}
              onOpen={onOpen}
              menu={menu}
              loading={lib.isPending}
              focusId={focusId}
            />
            <div className="foot-note">
              <span>{lib.data ? `${formatNumber(ordered.length)} of ${formatNumber(all.length)} films` : ''}</span>
              <span>↑↓ move · Space select · ⇧ range · ↵ open · Shift+F10 menu</span>
            </div>
          </>
        ) : (
          <>
            <PosterGrid
              groups={groups}
              items={ordered}
              selection={selection}
              live={live}
              loading={lib.isPending && showSkeleton}
              entering={entering}
              density={density}
              label={`Films, sorted by ${sort === 'added' ? 'date added' : sort}`}
              hrefOf={hrefOf}
              onOpen={onOpen}
              onSearch={onSearchFilm}
              onToggleMonitor={onToggleMonitor}
              menu={menu}
              focusId={focusId}
              epoch={epochN.current}
            />
            {lib.data && (
              <p className="grid-end">
                {formatNumber(ordered.length)} {ordered.length === 1 ? 'film' : 'films'} · arrows move · Space select ·
                ⇧ range · ↵ open · S search · M monitor
              </p>
            )}
          </>
        )}
      </main>
      {selectedItems.length > 0 && (
        <Suspense fallback={null}>
          <BulkBar selected={selectedItems} onClear={selection.clear} />
        </Suspense>
      )}
    </>
  );
}

/** Same look as the lazy button it stands in for; the first intent loads the real one. */
function StaticToolButton({
  label,
  icon,
  ...rest
}: { label: string; icon: ReactNode } & ComponentPropsWithoutRef<'button'>) {
  return (
    <Button variant="ghost" size="sm" icon={icon} aria-haspopup="dialog" {...rest}>
      {label}
    </Button>
  );
}
