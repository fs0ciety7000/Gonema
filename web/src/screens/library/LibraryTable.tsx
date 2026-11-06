import { ContextMenu } from '@base-ui/react/context-menu';
import { useWindowVirtualizer } from '@tanstack/react-virtual';
import { ArrowDown, ArrowUp, Ellipsis } from 'lucide-react';
import {
  type KeyboardEvent,
  type MouseEvent,
  type ReactNode,
  useCallback,
  useEffect,
  useLayoutEffect,
  useRef,
  useState,
} from 'react';
import type { LibraryItem } from '@/api/model';
import type { LiveDownload } from '@/components/movie/PosterCard';
import { QualityBadge, ScorePill, StatusText } from '@/components/ui/Badges';
import { IconButton } from '@/components/ui/Button';
import { Checkbox } from '@/components/ui/Checkbox';
import { cx } from '@/components/ui/cx';
import { Menu } from '@/components/ui/Menu';
import { PosterArt } from '@/components/ui/PosterArt';
import { formatBytes, formatDate, formatEta } from '@/lib/format';
import { displayStatus, STATUS, statusText } from '@/lib/status';
import type { SortDir, SortKey } from './model';
import type { SelectionApi } from './useSelection';

export type ColumnId = 'quality' | 'size' | 'score' | 'status' | 'added' | 'path';
export const COLUMNS: ReadonlyArray<{ id: ColumnId; label: string; width: number; sort?: SortKey; align?: 'r' }> = [
  { id: 'quality', label: 'Quality', width: 150, sort: 'quality' },
  { id: 'size', label: 'Size', width: 92, sort: 'size', align: 'r' },
  { id: 'score', label: 'Score', width: 76, sort: 'score', align: 'r' },
  { id: 'status', label: 'Status', width: 148, sort: 'status' },
  { id: 'added', label: 'Added', width: 104, sort: 'added' },
  { id: 'path', label: 'Path', width: 220 },
];

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

  const onRowClick = (ev: MouseEvent<HTMLTableRowElement>, m: LibraryItem, i: number) => {
    const t = ev.target as HTMLElement;
    if (t.closest('button, input, label, a')) return;
    setActive(i);
    if (ev.shiftKey) selection.range(m.id);
    else if (ev.metaKey || ev.ctrlKey) selection.toggle(m.id);
  };

  const [ctx, setCtx] = useState<LibraryItem | null>(null);
  const [ctxOpen, setCtxOpen] = useState(false);

  const all = items.length > 0 && items.every((m) => selection.selected.has(m.id));
  const some = !all && items.some((m) => selection.selected.has(m.id));
  const visible = COLUMNS.filter((c) => columns.has(c.id));
  const vItems = v.getVirtualItems();
  const padTop = vItems.length ? vItems[0]!.start - v.options.scrollMargin : 0;
  const padBottom = vItems.length ? v.getTotalSize() - (vItems[vItems.length - 1]!.end - v.options.scrollMargin) : 0;
  const ariaSort = (k?: SortKey) =>
    k && k === sort ? (dir === 'asc' ? 'ascending' : 'descending') : k ? 'none' : undefined;

  return (
    <ContextMenu.Root open={ctxOpen} onOpenChange={(o) => setCtxOpen(o && ctx !== null)}>
      <ContextMenu.Trigger
        render={
          <div
            className="table-wrap"
            onContextMenuCapture={(ev) => {
              const tr = (ev.target as HTMLElement).closest<HTMLElement>('tr[data-movie-id]');
              const m = tr ? items.find((x) => x.id === Number(tr.dataset.movieId)) : undefined;
              setCtx(m ?? null);
              if (!m) ev.stopPropagation();
            }}
          />
        }
      >
        <table
          className="tbl"
          // biome-ignore lint/a11y/noNoninteractiveElementToInteractiveRole: ARIA grid on a table (multi-select, DESIGN.md)
          role="grid"
          aria-label="Films"
          aria-rowcount={items.length + 1}
          aria-multiselectable="true"
          aria-busy={loading || undefined}
          id="library-panel"
        >
          <colgroup>
            <col style={{ width: 44 }} />
            <col />
            {visible.map((c) => (
              <col key={c.id} style={{ width: c.width }} />
            ))}
            <col style={{ width: 48 }} />
          </colgroup>
          <thead>
            <tr aria-rowindex={1}>
              <th className="c-check" scope="col">
                <Checkbox
                  checked={all}
                  indeterminate={some}
                  aria-label={all ? 'Clear the selection' : `Select all ${items.length.toLocaleString('en-US')} films`}
                  onCheckedChange={(c) => (c ? selection.selectAll() : selection.clear())}
                />
              </th>
              <th scope="col" aria-sort={ariaSort('title')}>
                <SortButton label="Title" active={sort === 'title'} dir={dir} onClick={() => onSort('title')} />
              </th>
              {visible.map((c) => (
                <th key={c.id} scope="col" className={c.align} aria-sort={ariaSort(c.sort)}>
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
              <th scope="col">
                <span className="sr-only">Actions</span>
              </th>
            </tr>
          </thead>
          <tbody ref={bodyRef} onKeyDown={onKeyDown}>
            {padTop > 0 && (
              // biome-ignore lint/a11y/noInteractiveElementToNoninteractiveRole: virtualisation spacer, not a row of data
              <tr role="presentation" className="spacer">
                <td colSpan={visible.length + 3} style={{ height: padTop }} />
              </tr>
            )}
            {vItems.map((vr) => {
              const m = items[vr.index]!;
              const s = displayStatus(m);
              const sel = selection.selected.has(m.id);
              const lv = live.get(m.id);
              const text =
                s === 'downloading' && lv
                  ? [`${Math.round(lv.progress * 100)}%`, formatEta(lv.etaSeconds)].filter(Boolean).join(' · ')
                  : s === 'available'
                    ? STATUS.available.label
                    : statusText(m, s);
              const root = rootPath(m);
              return (
                <tr
                  key={m.id}
                  data-movie-id={m.id}
                  aria-rowindex={vr.index + 2}
                  aria-selected={sel}
                  tabIndex={m.id === activeId ? 0 : -1}
                  className={cx(ctxOpen && ctx?.id === m.id && 'is-context')}
                  onClick={(ev) => onRowClick(ev, m, vr.index)}
                  onFocus={(ev) => {
                    if (ev.target === ev.currentTarget) setActive(vr.index);
                  }}
                >
                  <td className="c-check">
                    <Checkbox
                      checked={sel}
                      tabIndex={-1}
                      aria-label={`Select ${m.title}`}
                      onClick={(ev) => {
                        if (ev.shiftKey) {
                          ev.preventDefault();
                          selection.range(m.id);
                        } else selection.toggle(m.id);
                      }}
                    />
                  </td>
                  <td className={cx(s === 'unmonitored' && 'unmon')}>
                    <div className="t-cell">
                      <span className="thumb" data-poster-frame={m.id}>
                        <PosterArt src={m.posterUrl} title={m.title} color={m.dominantColor} />
                      </span>
                      <a
                        className="name"
                        href={hrefOf(m)}
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
                  {columns.has('quality') && <td>{m.quality ? <QualityBadge quality={m.quality} /> : <Dash />}</td>}
                  {columns.has('size') && <td className="r">{m.size ? formatBytes(m.size) : <Dash />}</td>}
                  {columns.has('score') && (
                    <td className="r">{m.score !== null && m.quality ? <ScorePill score={m.score} /> : <Dash />}</td>
                  )}
                  {columns.has('status') && (
                    <td>
                      <StatusText status={s}>{text}</StatusText>
                    </td>
                  )}
                  {columns.has('added') && <td className="muted">{formatDate(m.addedAt)}</td>}
                  {columns.has('path') && (
                    <td className="mono">
                      <span className="path" title={root ? `${root.replace(/\/+$/, '')}/${folderOf(m)}` : folderOf(m)}>
                        <span className="t-muted">…/</span>
                        {m.quality ? folderOf(m) : <span className="t-muted">{folderOf(m)}</span>}
                      </span>
                    </td>
                  )}
                  <td>
                    <Menu
                      label={`Actions for ${m.title}`}
                      heading={m.title}
                      align="end"
                      trigger={
                        <IconButton size="sm" className="row-more" label={`Actions for ${m.title}`} tabIndex={-1}>
                          <Ellipsis className="i i-sm" aria-hidden="true" />
                        </IconButton>
                      }
                    >
                      {menu(m)}
                    </Menu>
                  </td>
                </tr>
              );
            })}
            {padBottom > 0 && (
              // biome-ignore lint/a11y/noInteractiveElementToNoninteractiveRole: virtualisation spacer, not a row of data
              <tr role="presentation" className="spacer">
                <td colSpan={visible.length + 3} style={{ height: padBottom }} />
              </tr>
            )}
          </tbody>
        </table>
      </ContextMenu.Trigger>
      <ContextMenu.Portal>
        <ContextMenu.Positioner className="menu-positioner">
          <ContextMenu.Popup className="menu ctx-menu" aria-label={ctx ? `Actions for ${ctx.title}` : 'Actions'}>
            {ctx && (
              <>
                <div className="menu-heading" aria-hidden="true">
                  {ctx.title}
                </div>
                {menu(ctx)}
              </>
            )}
          </ContextMenu.Popup>
        </ContextMenu.Positioner>
      </ContextMenu.Portal>
    </ContextMenu.Root>
  );
}

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
