import { useWindowVirtualizer } from '@tanstack/react-virtual';
import {
  type CSSProperties,
  type KeyboardEvent,
  type MouseEvent,
  type ReactNode,
  useCallback,
  useEffect,
  useLayoutEffect,
  useMemo,
  useRef,
  useState,
} from 'react';
import type { LibraryItem } from '@/api/model';
import { openMovieDialog } from '@/components/movie/dialogStore';
import { type LiveDownload, PosterCard, PosterSkeleton } from '@/components/movie/PosterCard';
import { cx } from '@/components/ui/cx';
import { ListMenu, useListMenu } from '@/components/ui/Menu';
import { displayStatus } from '@/lib/status';
import { buildRows, type GridRow, indexRows, navigate } from './gridNav';
import type { Group } from './model';
import type { SelectionApi } from './useSelection';

const HEADER_H = 56;
/** Card height below the 2:3 poster: .pc gap 10 + title 20 + gap 2 + sub 18. */
const META_H = 50;

type PosterGridProps = {
  groups: readonly Group[];
  items: readonly LibraryItem[];
  selection: SelectionApi;
  live: ReadonlyMap<number, LiveDownload>;
  loading: boolean;
  /** First paint of this view: cards rise in with a short stagger (never on scroll, filter or keys). */
  entering: boolean;
  density: string;
  label: string;
  hrefOf: (m: LibraryItem) => string;
  onOpen: (m: LibraryItem, frame: HTMLElement | null) => void;
  onSearch: (m: LibraryItem) => void;
  onToggleMonitor: (m: LibraryItem) => void;
  /** I: interactive search for the focused film. */
  onInteractive?: (m: LibraryItem) => void;
  menu: (m: LibraryItem) => ReactNode;
  focusId?: number | null;
  /** Bumped when filters or sort change: the grid cross-fades in place (150 ms). */
  epoch: number;
};

function px(style: CSSStyleDeclaration, name: string, fallback: number) {
  const v = Number.parseFloat(style.getPropertyValue(name));
  return Number.isFinite(v) ? v : fallback;
}

export function PosterGrid({
  groups,
  items,
  selection,
  live,
  loading,
  entering,
  density,
  label,
  hrefOf,
  onOpen,
  onSearch,
  onToggleMonitor,
  onInteractive,
  menu,
  focusId,
  epoch,
}: PosterGridProps) {
  const wrapRef = useRef<HTMLDivElement>(null);
  const [box, setBox] = useState({ width: 0, posterMin: 168, gapX: 24, gapY: 32, top: 0 });

  // Columns follow the design grid: auto-fill, minmax(--poster-min, 1fr).
  // biome-ignore lint/correctness/useExhaustiveDependencies: density changes the CSS variables read here
  useLayoutEffect(() => {
    const el = wrapRef.current;
    if (!el) return;
    const measure = () => {
      const cs = getComputedStyle(el);
      const r = el.getBoundingClientRect();
      setBox({
        width: el.clientWidth,
        posterMin: px(cs, '--poster-min', 168),
        gapX: px(cs, '--grid-gap-x', 24),
        gapY: px(cs, '--grid-gap-y', 32),
        top: r.top + window.scrollY,
      });
    };
    measure();
    const ro = new ResizeObserver(measure);
    ro.observe(el);
    return () => ro.disconnect();
  }, [density]);

  const cols = Math.max(1, Math.floor((box.width + box.gapX) / (box.posterMin + box.gapX)));
  const cardW = box.width ? (box.width - box.gapX * (cols - 1)) / cols : box.posterMin;
  const rowH = Math.round(cardW * 1.5 + META_H);
  const withHeaders = groups.length > 1 || (groups[0]?.label ?? '') !== '';

  const rows: GridRow[] = useMemo(
    () =>
      buildRows(
        groups.map((g) => ({ key: g.key, label: g.label, size: g.items.length })),
        cols,
        withHeaders,
      ),
    [groups, cols, withHeaders],
  );
  const rowOf = useMemo(() => indexRows(rows, items.length), [rows, items.length]);

  const v = useWindowVirtualizer({
    count: rows.length,
    estimateSize: (i) => (rows[i]?.kind === 'header' ? HEADER_H : rowH + box.gapY),
    overscan: 3,
    // let React batch scroll renders instead of flushing synchronously inside the scroll handler
    useFlushSync: false,
    scrollMargin: box.top,
  });
  // biome-ignore lint/correctness/useExhaustiveDependencies: re-measure when the geometry changes
  useEffect(() => v.measure(), [rowH, box.gapY, rows]);

  // Cross-fade in place when filters / sort change (transform-free, 150 ms).
  const gridRef = useRef<HTMLDivElement>(null);
  const firstEpoch = useRef(epoch);
  useEffect(() => {
    if (epoch === firstEpoch.current) return;
    gridRef.current?.animate([{ opacity: 0.35 }, { opacity: 1 }], {
      duration: 150,
      easing: 'cubic-bezier(0.2, 0, 0, 1)',
    });
  }, [epoch]);

  // ------------------------------------------------------------ roving focus
  const [active, setActive] = useState(0);
  const pendingFocus = useRef<number | null>(null);
  const activeIndex = Math.min(active, Math.max(0, items.length - 1));
  const activeId = items[activeIndex]?.id;

  const focusItem = useCallback(
    (index: number) => {
      const m = items[index];
      if (!m) return;
      setActive(index);
      pendingFocus.current = m.id;
      const r = rowOf[index] ?? 0;
      v.scrollToIndex(r, { align: 'auto' });
    },
    [items, rowOf, v],
  );

  // After render, move DOM focus to the pending card (retry while the row mounts).
  useEffect(() => {
    if (pendingFocus.current === null) return;
    let tries = 0;
    let raf = 0;
    const attempt = () => {
      const id = pendingFocus.current;
      if (id === null) return;
      const link = wrapRef.current?.querySelector<HTMLElement>(`[data-movie-id="${id}"] [data-card-link]`);
      if (link) {
        link.focus({ preventScroll: true });
        pendingFocus.current = null;
      } else if (tries++ < 20) raf = requestAnimationFrame(attempt);
    };
    attempt();
    return () => cancelAnimationFrame(raf);
  });

  // Coming back from a film: its poster takes the focus again.
  // biome-ignore lint/correctness/useExhaustiveDependencies: once per focus request
  useEffect(() => {
    if (focusId == null || !items.length) return;
    const i = items.findIndex((m) => m.id === focusId);
    if (i >= 0) focusItem(i);
  }, [focusId, items.length > 0]);

  const onFocusCard = useCallback(
    (m: LibraryItem) => {
      const i = items.indexOf(m);
      if (i >= 0) setActive(i);
    },
    [items],
  );

  const onKeyDown = (ev: KeyboardEvent<HTMLDivElement>) => {
    const target = ev.target as HTMLElement;
    const cell = target.closest<HTMLElement>('[data-movie-id]');
    if (!cell) return;
    const id = Number(cell.dataset.movieId);
    const index = items.findIndex((m) => m.id === id);
    if (index < 0) return;
    const onLink = target.hasAttribute('data-card-link');
    const pageRows = Math.max(1, Math.floor(window.innerHeight / (rowH + box.gapY)) - 1);
    const next = navigate(ev.key, index, rows, rowOf, items.length, { ctrl: ev.ctrlKey || ev.metaKey, pageRows });
    if (next !== null) {
      ev.preventDefault();
      if (ev.shiftKey && next !== index) {
        if (!selection.isSelected(id)) selection.toggle(id);
        selection.range(items[next]!.id);
      }
      focusItem(next);
      return;
    }
    const m = items[index]!;
    if (ev.key === ' ' && onLink) {
      ev.preventDefault();
      if (ev.shiftKey) selection.range(id);
      else selection.toggle(id);
    } else if ((ev.key === 'a' || ev.key === 'A') && (ev.metaKey || ev.ctrlKey)) {
      ev.preventDefault();
      selection.selectAll();
    } else if (ev.key === 'Escape' && selection.selected.size) {
      ev.preventDefault();
      selection.clear();
    } else if (!ev.metaKey && !ev.ctrlKey && !ev.altKey && onLink) {
      if (ev.key === 's' || ev.key === 'S') {
        ev.preventDefault();
        onSearch(m);
      } else if ((ev.key === 'm' || ev.key === 'M') && !ev.shiftKey) {
        ev.preventDefault();
        onToggleMonitor(m);
      } else if ((ev.key === 'i' || ev.key === 'I') && !ev.shiftKey) {
        ev.preventDefault();
        onInteractive?.(m);
      } else if (ev.key === 'T' && ev.shiftKey) {
        ev.preventDefault();
        openMovieDialog({ kind: 'tags', targets: [{ id: m.id, title: m.title }] });
      } else if (ev.key === '.') {
        ev.preventDefault();
        cell.querySelector<HTMLButtonElement>('[data-more]')?.click();
      }
    }
  };

  // ------------------------------------------------------------ pointer selection
  const onLinkClick = useCallback(
    (ev: MouseEvent<HTMLAnchorElement>, m: LibraryItem) => {
      if (ev.button !== 0) return;
      if (ev.metaKey || ev.ctrlKey) {
        ev.preventDefault();
        selection.toggle(m.id);
        return;
      }
      if (ev.shiftKey) {
        ev.preventDefault();
        selection.range(m.id);
        return;
      }
      ev.preventDefault();
      onOpen(m, ev.currentTarget.closest<HTMLElement>('[data-poster-frame]'));
    },
    [selection, onOpen],
  );

  const onSelectClick = useCallback(
    (ev: MouseEvent<HTMLInputElement>, m: LibraryItem) => {
      if (ev.shiftKey) {
        ev.preventDefault();
        selection.range(m.id);
      } else selection.toggle(m.id);
    },
    [selection],
  );

  // ------------------------------------------------------------ context menu (one for the whole grid)
  const ctxMenu = useListMenu((t) => {
    const cell = t.closest<HTMLElement>('[data-movie-id]');
    return cell ? items.find((x) => x.id === Number(cell.dataset.movieId)) : undefined;
  });
  const ctx = ctxMenu.item;

  const selecting = selection.selected.size > 0;
  const vItems = v.getVirtualItems();
  const firstPaintRows = useRef<Set<number> | null>(null);
  if (entering && firstPaintRows.current === null && vItems.length && box.width) {
    firstPaintRows.current = new Set(vItems.map((r) => r.index));
  }

  return (
    <div ref={wrapRef} className="pg-wrap" data-density={density}>
      {loading && !items.length ? (
        <div className="poster-grid" aria-hidden="true">
          {Array.from({ length: cols * 2 }, (_, i) => (
            // biome-ignore lint/suspicious/noArrayIndexKey: static skeleton slots
            <PosterSkeleton key={i} delay={(i % cols) * 60} />
          ))}
        </div>
      ) : (
        <>
          {/* biome-ignore lint/a11y/useSemanticElements: a virtualised poster grid cannot be a <table> */}
          <div
            ref={gridRef}
            role="grid"
            aria-label={label}
            aria-rowcount={rows.length}
            aria-colcount={cols}
            aria-multiselectable="true"
            id="library-panel"
            className={cx('pg', selecting && 'selecting')}
            data-scrolling={v.isScrolling || undefined}
            style={{ height: v.getTotalSize(), ['--cols' as string]: cols } as CSSProperties}
            onKeyDown={onKeyDown}
            onContextMenu={ctxMenu.onContextMenu}
          >
            {vItems.map((vr) => {
              const row = rows[vr.index];
              if (!row) return null;
              const style: CSSProperties = { transform: `translateY(${vr.start - v.options.scrollMargin}px)` };
              if (row.kind === 'header') {
                return (
                  // biome-ignore lint/a11y/useSemanticElements: group header row of the grid
                  // biome-ignore lint/a11y/useFocusableInteractive: rows are not focus targets, cells' links are
                  <div key={`h-${row.key}`} role="row" aria-rowindex={vr.index + 1} className="pg-header" style={style}>
                    {/* biome-ignore lint/a11y/useSemanticElements: header cell of a role=grid */}
                    {/* biome-ignore lint/a11y/useFocusableInteractive: a group heading, not a sortable column */}
                    <div role="columnheader" aria-colspan={cols}>
                      <h2>
                        {row.label} <span className="count">{row.count.toLocaleString('en-US')}</span>
                      </h2>
                    </div>
                  </div>
                );
              }
              const enter = firstPaintRows.current?.has(vr.index) && entering;
              const cells: ReactNode[] = [];
              for (let i = row.start; i < row.end; i++) {
                const m = items[i]!;
                const order = i - row.start + (vr.index - (vItems[0]?.index ?? 0)) * cols;
                cells.push(
                  <PosterCard
                    key={m.id}
                    movie={m}
                    status={displayStatus(m)}
                    href={hrefOf(m)}
                    selected={selection.selected.has(m.id)}
                    active={m.id === activeId}
                    selecting={selecting}
                    live={live.get(m.id)}
                    onLinkClick={onLinkClick}
                    onSelectClick={onSelectClick}
                    onSearch={onSearch}
                    onToggleMonitor={onToggleMonitor}
                    onFocusCard={onFocusCard}
                    eager={vr.index < 2}
                    className={enter ? 'pc-enter' : undefined}
                    style={enter ? ({ ['--i' as string]: Math.min(order, 12) } as CSSProperties) : undefined}
                  />,
                );
              }
              return (
                // biome-ignore lint/a11y/useSemanticElements: a virtualised poster grid cannot be a <table>
                // biome-ignore lint/a11y/useFocusableInteractive: rows are not focus targets, cells' links are
                <div
                  key={`r-${vr.index}`}
                  role="row"
                  aria-rowindex={vr.index + 1}
                  className="pg-row"
                  style={{ ...style, height: rowH }}
                >
                  {cells}
                </div>
              );
            })}
          </div>
          <ListMenu
            open={ctxMenu.open}
            onOpenChange={ctxMenu.setOpen}
            point={ctxMenu.point}
            returnFocus={ctxMenu.returnFocus}
            label={ctx ? `Actions for ${ctx.title}` : 'Actions'}
            heading={ctx?.title}
          >
            {ctx && menu(ctx)}
          </ListMenu>
        </>
      )}
    </div>
  );
}
