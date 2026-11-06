/* Pure layout + keyboard navigation of the poster grid, kept apart for tests.
   Rows are either a group header or a run of cards [start, end) in list order. */

export type GridRow =
  | { kind: 'header'; key: string; label: string; count: number }
  | { kind: 'cards'; start: number; end: number };

export function buildRows(
  groupSizes: ReadonlyArray<{ key: string; label: string; size: number }>,
  cols: number,
  withHeaders: boolean,
): GridRow[] {
  const rows: GridRow[] = [];
  let offset = 0;
  for (const g of groupSizes) {
    if (withHeaders) rows.push({ kind: 'header', key: g.key, label: g.label, count: g.size });
    for (let i = 0; i < g.size; i += cols)
      rows.push({ kind: 'cards', start: offset + i, end: offset + Math.min(g.size, i + cols) });
    offset += g.size;
  }
  return rows;
}

/** rowOf[i] = index of the row holding item i. */
export function indexRows(rows: readonly GridRow[], count: number): Int32Array {
  const rowOf = new Int32Array(count);
  rows.forEach((r, ri) => {
    if (r.kind === 'cards') for (let i = r.start; i < r.end; i++) rowOf[i] = ri;
  });
  return rowOf;
}

function cardRow(rows: readonly GridRow[], from: number, step: 1 | -1): number {
  for (let r = from + step; r >= 0 && r < rows.length; r += step) if (rows[r]?.kind === 'cards') return r;
  return -1;
}

/** Index of the item a key moves to, or null when the key is not a navigation key. */
export function navigate(
  key: string,
  index: number,
  rows: readonly GridRow[],
  rowOf: Int32Array,
  count: number,
  opts: { ctrl?: boolean; pageRows?: number } = {},
): number | null {
  if (count === 0) return null;
  const i = Math.max(0, Math.min(count - 1, index));
  const ri = rowOf[i] ?? 0;
  const row = rows[ri];
  if (row?.kind !== 'cards') return null;
  const col = i - row.start;
  const toRow = (target: number) => {
    const r = rows[target];
    if (r?.kind !== 'cards') return i;
    return Math.min(r.start + col, r.end - 1);
  };
  switch (key) {
    case 'ArrowRight':
      return Math.min(count - 1, i + 1);
    case 'ArrowLeft':
      return Math.max(0, i - 1);
    case 'ArrowDown': {
      const r = cardRow(rows, ri, 1);
      return r < 0 ? i : toRow(r);
    }
    case 'ArrowUp': {
      const r = cardRow(rows, ri, -1);
      return r < 0 ? i : toRow(r);
    }
    case 'Home':
      return opts.ctrl ? 0 : row.start;
    case 'End':
      return opts.ctrl ? count - 1 : row.end - 1;
    case 'PageDown':
    case 'PageUp': {
      const step = key === 'PageDown' ? 1 : -1;
      let r = ri;
      for (let n = 0; n < (opts.pageRows ?? 3); n++) {
        const next = cardRow(rows, r, step);
        if (next < 0) break;
        r = next;
      }
      return toRow(r);
    }
    default:
      return null;
  }
}
