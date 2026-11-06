import { describe, expect, it } from 'vitest';
import { fromColumns, type LibraryItem, toLibraryItem } from '@/api/model';
import { buildRows, indexRows, navigate } from './gridNav';
import { applyFilters, groupItems, sortItems, validateLibrarySearch } from './model';

const item = (p: Partial<LibraryItem> & { id: number; title: string }): LibraryItem =>
  toLibraryItem({ state: 'available', monitored: true, year: 2020, addedAt: 0, ...p } as Record<string, unknown>);

describe('validateLibrarySearch', () => {
  it('keeps valid params and drops anything else', () => {
    expect(
      validateLibrarySearch({
        layout: 'table',
        sort: 'nope',
        status: 'missing,bogus,failed',
        yearFrom: '2012',
        yearTo: 99999,
        q: '  ',
        monitored: 'false',
      }),
    ).toEqual({ layout: 'table', status: ['missing', 'failed'], yearFrom: 2012, monitored: false });
  });
});

describe('filters, sort, group', () => {
  const items = [
    item({ id: 1, title: 'Amélie Nights', year: 2001, quality: 'bluray-1080p', size: 9e9 }),
    item({ id: 2, title: 'Zebra', year: 2024, state: 'missing' }),
    item({ id: 3, title: 'Mid', year: 2015, monitored: false, quality: 'remux-2160p', size: 60e9 }),
  ];
  it('filters by status, resolution, year and accent-insensitive search', () => {
    expect(applyFilters(items, { status: ['missing'] }).map((m) => m.id)).toEqual([2]);
    expect(applyFilters(items, { resolution: '2160p' }).map((m) => m.id)).toEqual([3]);
    expect(applyFilters(items, { yearFrom: 2010, yearTo: 2020 }).map((m) => m.id)).toEqual([3]);
    expect(applyFilters(items, { q: 'amelie' }).map((m) => m.id)).toEqual([1]);
    expect(applyFilters(items, { status: ['unmonitored'] }).map((m) => m.id)).toEqual([3]);
  });
  it('sorts with stable tie-breaks', () => {
    expect(sortItems(items, 'title', 'asc').map((m) => m.id)).toEqual([1, 3, 2]);
    expect(sortItems(items, 'size', 'desc').map((m) => m.id)).toEqual([3, 1, 2]);
    expect(sortItems(items, 'quality', 'desc').map((m) => m.id)).toEqual([3, 1, 2]);
  });
  it('groups by decade', () => {
    expect(groupItems(items, 'decade').map((g) => g.label)).toEqual(['2000s', '2020s', '2010s']);
  });
});

describe('fromColumns', () => {
  it('maps the derived states failed / unmonitored to flags', () => {
    const [a, b] = fromColumns({
      id: [1, 2],
      title: ['A', 'B'],
      state: ['failed', 'unmonitored'],
      monitored: [true, true],
      quality: [null, 'webdl-1080p'],
    });
    expect(a).toMatchObject({ state: 'missing', failed: true });
    expect(b).toMatchObject({ state: 'available', monitored: false });
  });
  it('turns the columnar list into items', () => {
    const [m] = fromColumns({
      id: [9],
      title: ['X'],
      hdr: ['DV HDR10'],
      sizeOnDisk: [5],
      progress: [0.5],
      state: ['downloading'],
    });
    expect(m).toMatchObject({ id: 9, title: 'X', hdr: ['DV', 'HDR10'], size: 5, progress: 0.5, state: 'downloading' });
  });
});

describe('grid keyboard navigation', () => {
  // two groups: 5 items then 3 items, 3 columns, with headers
  const rows = buildRows(
    [
      { key: 'a', label: 'A', size: 5 },
      { key: 'b', label: 'B', size: 3 },
    ],
    3,
    true,
  );
  const rowOf = indexRows(rows, 8);
  it('builds header and card rows', () => {
    expect(rows.map((r) => r.kind)).toEqual(['header', 'cards', 'cards', 'header', 'cards']);
  });
  it('moves across rows and skips headers', () => {
    expect(navigate('ArrowRight', 0, rows, rowOf, 8)).toBe(1);
    expect(navigate('ArrowDown', 1, rows, rowOf, 8)).toBe(4); // short row: clamps to its last item
    expect(navigate('ArrowDown', 4, rows, rowOf, 8)).toBe(6); // over the "B" header
    expect(navigate('ArrowUp', 7, rows, rowOf, 8)).toBe(4);
    expect(navigate('End', 0, rows, rowOf, 8)).toBe(2);
    expect(navigate('End', 0, rows, rowOf, 8, { ctrl: true })).toBe(7);
    expect(navigate('ArrowLeft', 0, rows, rowOf, 8)).toBe(0);
    expect(navigate('x', 0, rows, rowOf, 8)).toBeNull();
  });
});
