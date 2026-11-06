import '@/styles/table.css';
import { useWindowVirtualizer } from '@tanstack/react-virtual';
import { ArrowDown, ArrowUp } from 'lucide-react';
import {
  type KeyboardEvent,
  type MouseEvent,
  memo,
  type ReactNode,
  useCallback,
  useEffect,
  useLayoutEffect,
  useMemo,
  useRef,
  useState,
} from 'react';
import type { LibraryItem } from '@/api/model';
import type { LiveDownload } from '@/components/movie/PosterCard';
import { QualityBadge, ScorePill } from '@/components/ui/Badges';
import { Checkbox } from '@/components/ui/Checkbox';
import { cx } from '@/components/ui/cx';
import { ListMenu, openContextMenuFrom, useListMenu } from '@/components/ui/Menu';
import { PosterArt } from '@/components/ui/PosterArt';
import { formatBytes, formatDate, formatEta } from '@/lib/format';
import { displayStatus, STATUS, statusText } from '@/lib/status';
import { COLUMNS, type ColumnId } from './columns';
import type { SortDir, SortKey } from './model';
import type { SelectionApi } from './useSelection';

export { COLUMNS, type ColumnId };

type Props = {
  items: readonly LibraryItem[];
  selection: SelectionApi;
  live: ReadonlyMap<number, LiveDownload>;
  columns: ReadonlySet<ColumnId>;
  sort: SortKey;
  dir: SortDir;
  onSort: (key: SortKey) => void;
  density: string;
  rootPath: (m: LibraryItem) => string | undefined;
  hrefOf: (m: LibraryItem) => string;
  onOpen: (m: LibraryItem, from: HTMLElement | null) => void;
  menu: (m: LibraryItem) => ReactNode;
  loading: boolean;
  focusId?: number | null;
  /** Shown muted next to the Path header ("Path /data/movies"). */
  pathRoot?: string | undefined;
};

const ROW_H = { airy: 56, compact: 36 } as const;

function folderOf(m: LibraryItem) {
  const p = m.path.replace(/^\/+/, '');
  return p.endsWith('/') ? p : `${p}/`;
}

export function LibraryTable({
  items,
  selection,
  live,
  columns,
  sort,
  dir,
  onSort,
  density,
  rootPath,
  hrefOf,
  onOpen,
  menu,
  loading,
  focusId,
  pathRoot,
}: Props) {
  const rowH = ROW_H[density === 'airy' ? 'airy' : 'compact'];
  const bodyRef = useRef<HTMLTableSectionElement>(null);
  const [top, setTop] = useState(0);
  useLayoutEffect(() => {
    const measure = () => {
      const el = bodyRef.current;
      if (el) setTop(el.getBoundingClientRect().top + window.scrollY);
    };
    measure();
    window.addEventListener('resize', measure);
    return () => window.removeEventListener('resize', measure);
  }, []);

  const v = useWindowVirtualizer({
    count: items.length,
    estimateSize: () => rowH + 1,
    overscan: 10,
    useFlushSync: false,
    scrollMargin: top,
  });
  // biome-ignore lint/correctness/useExhaustiveDependencies: row height follows density
  useEffect(() => v.measure(), [rowH]);

  const [active, setActive] = useState(0);
  const pending = useRef<number | null>(null);
  const activeId = items[Math.min(active, items.length - 1)]?.id;

  const focusRow = useCallback(
    (i: number) => {
      const m = items[i];
      if (!m) return;
      setActive(i);
      pending.current = m.id;
      v.scrollToIndex(i, { align: 'auto' });
    },
    [items, v],
  );

  useEffect(() => {
    if (pending.current === null) return;
    let raf = 0;
    let n = 0;
    const go = () => {
      const tr = bodyRef.current?.querySelector<HTMLElement>(`tr[data-movie-id="${pending.current}"]`);
      if (tr) {
        tr.focus({ preventScroll: true });
        pending.current = null;
      } else if (n++ < 20) raf = requestAnimationFrame(go);
    };
    go();
    return () => cancelAnimationFrame(raf);
  });

  // biome-ignore lint/correctness/useExhaustiveDependencies: once per focus request
  useEffect(() => {
    if (focusId == null || !items.length) return;
    const i = items.findIndex((m) => m.id === focusId);
    if (i >= 0) focusRow(i);
  }, [focusId, items.length > 0]);

  const onKeyDown = (ev: KeyboardEvent<HTMLTableSectionElement>) => {
    const tr = (ev.target as HTMLElement).closest<HTMLElement>('tr[data-movie-id]');
    if (!tr) return;
    const onRow = ev.target === tr;
    const i = items.findIndex((m) => m.id === Number(tr.dataset.movieId));
    if (i < 0) return;
    const m = items[i]!;
    const page = Math.max(1, Math.floor(window.innerHeight / rowH) - 4);
    const keys: Record<string, number> = {
      ArrowDown: i + 1,
      ArrowUp: i - 1,
      PageDown: i + page,
      PageUp: i - page,
      Home: 0,
      End: items.length - 1,
    };
    const next = keys[ev.key];
    if (next !== undefined && onRow) {
      ev.preventDefault();
      const to = Math.max(0, Math.min(items.length - 1, next));
      if (ev.shiftKey) {
        if (!selection.isSelected(m.id)) selection.toggle(m.id);
        selection.range(items[to]!.id);
      }
      focusRow(to);
    } else if (ev.key === ' ' && onRow) {
      ev.preventDefault();
      if (ev.shiftKey) selection.range(m.id);
      else selection.toggle(m.id);
    } else if (ev.key === 'Enter' && onRow) {
      ev.preventDefault();
      onOpen(m, tr.querySelector<HTMLElement>('.thumb'));
    } else if (ev.key === 'Escape' && selection.selected.size) {
      ev.preventDefault();
      selection.clear();
    } else if ((ev.key === 'a' || ev.key === 'A') && (ev.metaKey || ev.ctrlKey) && onRow) {
      ev.preventDefault();
      selection.selectAll();
    }
  };

  const { range, toggle } = selection;
  const onRowClick = useCallback(
    (ev: MouseEvent<HTMLTableRowElement>, m: LibraryItem, i: number) => {
      const t = ev.target as HTMLElement;
      if (t.closest('button, input, label, a')) return;
      setActive(i);
      if (ev.shiftKey) range(m.id);
      else if (ev.metaKey || ev.ctrlKey) toggle(m.id);
    },
    [range, toggle],
  );
  const onSelectClick = useCallback(
    (ev: MouseEvent<HTMLInputElement>, m: LibraryItem) => {
      if (ev.shiftKey) {
        ev.preventDefault();
        range(m.id);
      } else toggle(m.id);
    },
    [range, toggle],
  );

  const ctxMenu = useListMenu((t) => {
    const tr = t.closest<HTMLElement>('tr[data-movie-id]');
    return tr ? items.find((x) => x.id === Number(tr.dataset.movieId)) : undefined;
  });
  const ctx = ctxMenu.item;
  const ctxOpen = ctxMenu.open;

  const all = items.length > 0 && items.every((m) => selection.selected.has(m.id));
  const some = !all && items.some((m) => selection.selected.has(m.id));
  const visible = useMemo(() => COLUMNS.filter((c) => columns.has(c.id)), [columns]);
  const vItems = v.getVirtualItems();
  // Rows are placed one by one (grid rows of fixed height): a row entering the window never moves
  // the others, so the browser paints only that row (see e2e/fidelity.md, scroll performance).
  const template = `44px minmax(0, 1fr) ${visible.map((c) => `${c.width}px`).join(' ')} 48px`;
  const ariaSort = (k?: SortKey) =>
    k && k === sort ? (dir === 'asc' ? 'ascending' : 'descending') : k ? 'none' : undefined;

  // The header does not change while scrolling: built once per sort / selection / columns change.
  const { selectAll, clear } = selection;
  // biome-ignore lint/correctness/useExhaustiveDependencies: ariaSort derives from sort and dir
  const head = useMemo(
    () => (
      <thead role="rowgroup">
        <tr aria-rowindex={1} role="row">
          <th className="c-check" scope="col" role="columnheader">
            <Checkbox
              checked={all}
              indeterminate={some}
              aria-label={all ? 'Clear the selection' : `Select all ${items.length.toLocaleString('en-US')} films`}
              onCheckedChange={(c) => (c ? selectAll() : clear())}
            />
          </th>
          <th scope="col" role="columnheader" aria-sort={ariaSort('title')}>
            <SortButton label="Title" active={sort === 'title'} dir={dir} onClick={() => onSort('title')} />
          </th>
          {visible.map((c) => (
            <th key={c.id} scope="col" role="columnheader" className={c.align} aria-sort={ariaSort(c.sort)}>
              {c.sort ? (
                <SortButton label={c.label} active={sort === c.sort} dir={dir} onClick={() => onSort(c.sort!)} />
              ) : (
                <>
                  {c.label}
                  {c.id === 'path' && pathRoot && (
                    <>
                      {' '}
                      <span className="t-muted path-root">{pathRoot}</span>
                    </>
                  )}
                </>
              )}
            </th>
          ))}
          <th scope="col" role="columnheader">
            <span className="sr-only">Actions</span>
          </th>
        </tr>
      </thead>
    ),
    [all, some, sort, dir, visible, pathRoot, items.length, onSort, selectAll, clear],
  );

  return (
    <>
      {/* biome-ignore lint/a11y/noStaticElementInteractions: right click on a row opens the list's menu (keyboard: Shift+F10, or the row's "more" button) */}
      <div className="table-wrap" onContextMenu={ctxMenu.onContextMenu}>
        <table
          className="tbl lib-tbl"
          data-scrolling={v.isScrolling || undefined}
          style={{ ['--tbl-cols' as string]: template }}
          // ARIA grid on a table (multi-select, DESIGN.md); roles are explicit on every element (biome.json override)
          role="grid"
          aria-label="Films"
          aria-rowcount={items.length + 1}
          aria-multiselectable="true"
          aria-busy={loading || undefined}
          id="library-panel"
        >
          {head}
          {/* keyboard navigation of the rows (role=grid), delegated */}
          <tbody ref={bodyRef} onKeyDown={onKeyDown} role="rowgroup" style={{ height: v.getTotalSize() }}>
            {vItems.map((vr) => {
              const m = items[vr.index]!;
              return (
                <TableRow
                  key={m.id}
                  m={m}
                  index={vr.index}
                  top={vr.start - v.options.scrollMargin}
                  scrolling={v.isScrolling}
                  selected={selection.selected.has(m.id)}
                  active={m.id === activeId}
                  context={ctxOpen && ctx?.id === m.id}
                  live={live.get(m.id)}
                  columns={columns}
                  root={rootPath(m)}
                  href={hrefOf(m)}
                  onOpen={onOpen}
                  onRowClick={onRowClick}
                  onActive={setActive}
                  onSelectClick={onSelectClick}
                />
              );
            })}
          </tbody>
        </table>
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
  );
}

type RowProps = {
  m: LibraryItem;
  index: number;
  /** Offset of the row in the body (px). */
  top: number;
  /** The list is scrolling: rows that enter now show their colour until it stops (no image decode per frame). */
  scrolling: boolean;
  selected: boolean;
  active: boolean;
  context: boolean;
  live: LiveDownload | undefined;
  columns: ReadonlySet<ColumnId>;
  root: string | undefined;
  href: string;
  onOpen: (m: LibraryItem, from: HTMLElement | null) => void;
  onRowClick: (ev: MouseEvent<HTMLTableRowElement>, m: LibraryItem, i: number) => void;
  onActive: (i: number) => void;
  onSelectClick: (ev: MouseEvent<HTMLInputElement>, m: LibraryItem) => void;
};

/** One row, memoised: scrolling re-renders only the rows that enter the window. */
const TableRow = memo(function TableRow({
  m,
  index,
  top,
  scrolling,
  selected,
  active,
  context,
  live: lv,
  columns,
  root,
  href,
  onOpen,
  onRowClick,
  onActive,
  onSelectClick,
}: RowProps) {
  const s = displayStatus(m);
  const [thumb, setThumb] = useState(!scrolling);
  useEffect(() => {
    if (!scrolling) setThumb(true);
  }, [scrolling]);
  const text =
    s === 'downloading' && lv
      ? [`${Math.round(lv.progress * 100)}%`, formatEta(lv.etaSeconds)].filter(Boolean).join(' · ')
      : s === 'available'
        ? STATUS.available.label
        : statusText(m, s);
  return (
    <tr
      data-movie-id={m.id}
      role="row"
      style={{ transform: `translateY(${top}px)` }}
      aria-rowindex={index + 2}
      aria-selected={selected}
      tabIndex={active ? 0 : -1}
      className={cx(context && 'is-context')}
      onClick={(ev) => onRowClick(ev, m, index)}
      onFocus={(ev) => {
        if (ev.target === ev.currentTarget) onActive(index);
      }}
    >
      <td role="gridcell" className="c-check">
        <Checkbox
          checked={selected}
          tabIndex={-1}
          aria-label={`Select ${m.title}`}
          onClick={(ev) => onSelectClick(ev, m)}
        />
      </td>
      <td role="gridcell" className={cx(s === 'unmonitored' && 'unmon')}>
        <div className="t-cell">
          <span className="thumb" data-poster-frame={m.id}>
            <PosterArt src={thumb ? m.posterUrl : null} title={m.title} color={m.dominantColor} />
          </span>
          <a
            className="name"
            href={href}
            tabIndex={-1}
            onClick={(ev) => {
              if (ev.button !== 0 || ev.metaKey || ev.ctrlKey || ev.shiftKey) return;
              ev.preventDefault();
              onOpen(m, ev.currentTarget.parentElement?.querySelector<HTMLElement>('.thumb') ?? null);
            }}
          >
            {m.title}
          </a>
          {m.year > 0 && <span className="yr">{m.year}</span>}
        </div>
      </td>
      {columns.has('quality') && <td role="gridcell">{m.quality ? <QualityBadge quality={m.quality} /> : <Dash />}</td>}
      {columns.has('size') && (
        <td role="gridcell" className="r">
          {m.size ? formatBytes(m.size) : <Dash />}
        </td>
      )}
      {columns.has('score') && (
        <td role="gridcell" className="r">
          {m.score !== null && m.quality ? <ScorePill score={m.score} /> : <Dash />}
        </td>
      )}
      {columns.has('status') && (
        <td role="gridcell">
          <span className="status" data-status={s}>
            <span className="ico" aria-hidden="true" />
            {text}
          </span>
        </td>
      )}
      {columns.has('added') && (
        <td role="gridcell" className="muted">
          {formatDate(m.addedAt)}
        </td>
      )}
      {columns.has('path') && (
        <td role="gridcell" className="mono">
          <span className="path" title={root ? `${root.replace(/\/+$/, '')}/${folderOf(m)}` : folderOf(m)}>
            <span className="t-muted">…/</span>
            {m.quality ? folderOf(m) : <span className="t-muted">{folderOf(m)}</span>}
          </span>
        </td>
      )}
      <td role="gridcell">
        <button
          type="button"
          className="icon-btn sm row-more"
          aria-label={`Actions for ${m.title}`}
          aria-haspopup="menu"
          tabIndex={-1}
          onClick={(ev) => openContextMenuFrom(ev.currentTarget)}
        >
          <span className="ico ico-more" aria-hidden="true" />
        </button>
      </td>
    </tr>
  );
});

function Dash() {
  return <span className="t-muted">—</span>;
}

function SortButton({
  label,
  active,
  dir,
  onClick,
}: {
  label: string;
  active: boolean;
  dir: SortDir;
  onClick: () => void;
}) {
  const Icon = active && dir === 'desc' ? ArrowDown : ArrowUp;
  return (
    <button type="button" onClick={onClick}>
      {label} <Icon className="i" aria-hidden="true" />
    </button>
  );
}
