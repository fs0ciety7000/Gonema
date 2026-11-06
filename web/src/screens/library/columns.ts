import type { SortKey } from './model';

/** Optional columns of the library table (kept apart so the grid's first load does not carry the table). */
export type ColumnId = 'quality' | 'size' | 'score' | 'status' | 'added' | 'path';
export const COLUMNS: ReadonlyArray<{ id: ColumnId; label: string; width: number; sort?: SortKey; align?: 'r' }> = [
  { id: 'quality', label: 'Quality', width: 150, sort: 'quality' },
  { id: 'size', label: 'Size', width: 92, sort: 'size', align: 'r' },
  { id: 'score', label: 'Score', width: 76, sort: 'score', align: 'r' },
  { id: 'status', label: 'Status', width: 148, sort: 'status' },
  { id: 'added', label: 'Added', width: 104, sort: 'added' },
  { id: 'path', label: 'Path', width: 220 },
];
