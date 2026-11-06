import type { LibraryItem } from '@/api/model';
import { parseQuality, qualityRank } from '@/lib/quality';
import { type DisplayStatus, displayStatus, STATUS } from '@/lib/status';

export type Layout = 'grid' | 'table';
export type Density = 'airy' | 'compact';
export type SortKey = 'added' | 'title' | 'year' | 'quality' | 'size' | 'score' | 'status';
export type SortDir = 'asc' | 'desc';
export type GroupKey = 'none' | 'status' | 'decade' | 'resolution';
export type Resolution = '2160p' | '1080p' | '720p' | 'sd';

export type Filters = {
  monitored?: boolean;
  status?: DisplayStatus[];
  yearFrom?: number;
  yearTo?: number;
  genre?: string;
  resolution?: Resolution;
  /** Added within the last n days. */
  added?: number;
  q?: string;
};

export type LibrarySearch = Filters & {
  view?: string;
  layout?: Layout;
  density?: Density;
  sort?: SortKey;
  dir?: SortDir;
  group?: GroupKey;
};

const oneOf = <T extends string>(v: unknown, all: readonly T[]): T | undefined =>
  typeof v === 'string' && (all as readonly string[]).includes(v) ? (v as T) : undefined;

const int = (v: unknown, min: number, max: number): number | undefined => {
  const n = typeof v === 'number' ? v : typeof v === 'string' && v.trim() ? Number(v) : Number.NaN;
  return Number.isInteger(n) && n >= min && n <= max ? n : undefined;
};

export const STATUSES = Object.keys(STATUS) as DisplayStatus[];
export const SORTS: readonly SortKey[] = ['added', 'title', 'year', 'quality', 'size', 'score', 'status'];
export const GROUPS: readonly GroupKey[] = ['none', 'status', 'decade', 'resolution'];
export const RESOLUTIONS: readonly Resolution[] = ['2160p', '1080p', '720p', 'sd'];

/** Validates the /films search params (external input: anything can be typed in the URL). */
export function validateLibrarySearch(raw: Record<string, unknown>): LibrarySearch {
  const out: LibrarySearch = {};
  const view = typeof raw.view === 'string' && /^[\w-]{1,64}$/.test(raw.view) ? raw.view : undefined;
  if (view) out.view = view;
  const layout = oneOf(raw.layout, ['grid', 'table'] as const);
  if (layout) out.layout = layout;
  const density = oneOf(raw.density, ['airy', 'compact'] as const);
  if (density) out.density = density;
  const sort = oneOf(raw.sort, SORTS);
  if (sort) out.sort = sort;
  const dir = oneOf(raw.dir, ['asc', 'desc'] as const);
  if (dir) out.dir = dir;
  const group = oneOf(raw.group, GROUPS);
  if (group && group !== 'none') out.group = group;
  if (raw.monitored === true || raw.monitored === 'true') out.monitored = true;
  else if (raw.monitored === false || raw.monitored === 'false') out.monitored = false;
  const st = (Array.isArray(raw.status) ? raw.status : typeof raw.status === 'string' ? raw.status.split(',') : [])
    .map((s) => oneOf(s, STATUSES))
    .filter((s): s is DisplayStatus => !!s);
  if (st.length) out.status = [...new Set(st)];
  const yf = int(raw.yearFrom, 1870, 2200);
  if (yf !== undefined) out.yearFrom = yf;
  const yt = int(raw.yearTo, 1870, 2200);
  if (yt !== undefined) out.yearTo = yt;
  if (typeof raw.genre === 'string' && raw.genre.length <= 100 && raw.genre.trim()) out.genre = raw.genre.trim();
  const res = oneOf(raw.resolution, RESOLUTIONS);
  if (res) out.resolution = res;
  const added = int(raw.added, 1, 3650);
  if (added !== undefined) out.added = added;
  if (typeof raw.q === 'string' && raw.q.trim()) out.q = raw.q.slice(0, 200);
  return out;
}

export const FILTER_KEYS = ['monitored', 'status', 'yearFrom', 'yearTo', 'genre', 'resolution', 'added', 'q'] as const;

export function pickFilters(s: LibrarySearch): Filters {
  const f: Filters = {};
  for (const k of FILTER_KEYS) if (s[k] !== undefined) (f as Record<string, unknown>)[k] = s[k];
  return f;
}

function resolutionOf(m: LibraryItem): Resolution | null {
  const r = parseQuality(m.quality)?.res;
  if (!r) return null;
  if (r === '2160p' || r === '1080p' || r === '720p') return r;
  return 'sd';
}

const fold = (s: string) => s.normalize('NFKD').replace(/\p{M}/gu, '').toLowerCase();

export function applyFilters(items: readonly LibraryItem[], f: Filters, now = Date.now()): LibraryItem[] {
  const q = f.q ? fold(f.q.trim()) : '';
  const st = f.status?.length ? new Set(f.status) : null;
  const since = f.added ? now - f.added * 86_400_000 : 0;
  return items.filter((m) => {
    if (f.monitored !== undefined && m.monitored !== f.monitored) return false;
    if (f.yearFrom && m.year < f.yearFrom) return false;
    if (f.yearTo && m.year > f.yearTo) return false;
    if (f.genre && !m.genres.includes(f.genre)) return false;
    if (f.resolution && resolutionOf(m) !== f.resolution) return false;
    if (since && m.addedAt < since) return false;
    if (st && !st.has(displayStatus(m))) return false;
    if (q && !fold(m.title).includes(q)) return false;
    return true;
  });
}

export const DEFAULT_DIR: Record<SortKey, SortDir> = {
  added: 'desc',
  title: 'asc',
  year: 'desc',
  quality: 'desc',
  size: 'desc',
  score: 'desc',
  status: 'asc',
};

export const SORT_LABEL: Record<SortKey, string> = {
  added: 'Date added',
  title: 'Title',
  year: 'Year',
  quality: 'Quality',
  size: 'Size',
  score: 'Score',
  status: 'Status',
};

export const GROUP_LABEL: Record<GroupKey, string> = {
  none: 'None',
  status: 'Status',
  decade: 'Decade',
  resolution: 'Resolution',
};

const STATUS_ORDER: Record<DisplayStatus, number> = {
  failed: 0,
  downloading: 1,
  missing: 2,
  upgraded: 3,
  available: 4,
  unmonitored: 5,
};

export function sortItems(items: readonly LibraryItem[], key: SortKey, dir: SortDir): LibraryItem[] {
  const sign = dir === 'asc' ? 1 : -1;
  const val = (m: LibraryItem): number | string => {
    switch (key) {
      case 'added':
        return m.addedAt;
      case 'title':
        return m.sortTitle;
      case 'year':
        return m.year;
      case 'quality':
        return qualityRank(m.quality);
      case 'size':
        return m.size ?? -1;
      case 'score':
        return m.score ?? Number.NEGATIVE_INFINITY;
      case 'status':
        return STATUS_ORDER[displayStatus(m)];
    }
  };
  const decorated = items.map((m) => ({ m, v: val(m) }));
  decorated.sort((a, b) => {
    const c = typeof a.v === 'string' ? a.v.localeCompare(b.v as string) : (a.v as number) - (b.v as number);
    if (c !== 0) return c * sign;
    return a.m.sortTitle.localeCompare(b.m.sortTitle) || a.m.id - b.m.id;
  });
  return decorated.map((d) => d.m);
}

export type Group = { key: string; label: string; items: LibraryItem[] };

export function groupItems(items: readonly LibraryItem[], by: GroupKey): Group[] {
  if (by === 'none') return [{ key: 'all', label: '', items: items as LibraryItem[] }];
  const map = new Map<string, Group>();
  for (const m of items) {
    let key: string;
    let label: string;
    if (by === 'status') {
      const s = displayStatus(m);
      key = s;
      label = STATUS[s].label;
    } else if (by === 'decade') {
      const d = m.year ? Math.floor(m.year / 10) * 10 : 0;
      key = String(d);
      label = d ? `${d}s` : 'Unknown year';
    } else {
      const r = resolutionOf(m);
      key = r ?? 'none';
      label = r ? (r === 'sd' ? 'SD' : r) : 'No file';
    }
    let g = map.get(key);
    if (!g) {
      g = { key, label, items: [] };
      map.set(key, g);
    }
    g.items.push(m);
  }
  return [...map.values()];
}

// ----------------------------------------------------------------- views

export type SavedView = {
  id: string;
  name: string;
  filters: Filters;
  sort?: SortKey;
  dir?: SortDir;
  density?: Density;
  builtin?: boolean;
};

export const BUILTIN_VIEWS: readonly SavedView[] = [
  { id: 'all', name: 'All films', filters: {}, builtin: true },
  { id: 'uhd', name: '4K', filters: { resolution: '2160p' }, builtin: true },
  { id: 'wanted', name: 'Wanted', filters: { monitored: true, status: ['missing'] }, builtin: true },
  { id: 'recent', name: 'Recently added', filters: { added: 30 }, sort: 'added', builtin: true },
];

export function sameFilters(a: Filters, b: Filters): boolean {
  const norm = (f: Filters) => JSON.stringify(FILTER_KEYS.map((k) => (k === 'q' ? undefined : f[k])));
  return norm(a) === norm(b);
}
