/* UI model of the library, decoupled from the wire format: the adapter accepts the
   list item of GET /movies and the richer compact index rows (file quality, size,
   score, HDR, download progress, last failure) when the server provides them. */

export type MovieState = 'missing' | 'downloading' | 'available' | 'upgradable';
export type ReleaseStatus = 'announced' | 'in_cinemas' | 'released' | 'deleted';

export type LibraryItem = {
  id: number;
  tmdbId: number;
  title: string;
  sortTitle: string;
  year: number;
  status: ReleaseStatus;
  state: MovieState;
  monitored: boolean;
  qualityProfileId: number;
  minimumAvailability: string;
  rootFolderId: number;
  collectionId: number | null;
  path: string;
  addedAt: number;
  genres: string[];
  /** Poster URL from the server (w342; w780 alongside for dense screens). */
  posterUrl: string | null;
  dominantColor: string | null;
  quality: string | null;
  size: number | null;
  score: number | null;
  hdr: string[];
  progress: number;
  failed: boolean;
  releaseDigital: string | null;
  releasePhysical: string | null;
  releaseCinema: string | null;
  tags: string[];
  /** Ids of the film's tags (GET /tags has the labels). */
  tagIds: number[];
};

/** Thumbnail of a server poster URL (w342 → w92, ~2 KB): tables, strips, small rows. */
export const posterThumb = (url: string | null | undefined): string | null =>
  url ? url.replace('/w342/', '/w92/') : null;

type Raw = Record<string, unknown>;

const str = (v: unknown): string | null => (typeof v === 'string' && v !== '' ? v : null);
const num = (v: unknown): number | null => (typeof v === 'number' && Number.isFinite(v) ? v : null);
const pick = (r: Raw, ...keys: string[]) => {
  for (const k of keys) if (r[k] !== undefined && r[k] !== null) return r[k];
  return undefined;
};

function time(v: unknown): number {
  if (typeof v === 'number') return v;
  if (typeof v === 'string') {
    const t = Date.parse(v);
    return Number.isNaN(t) ? 0 : t;
  }
  return 0;
}

function list(v: unknown): string[] {
  if (Array.isArray(v)) return v.filter((x): x is string => typeof x === 'string' && x !== '');
  if (typeof v === 'string' && v.trim()) return v.trim().split(/[\s,]+/);
  return [];
}

const STATES = new Set<MovieState>(['missing', 'downloading', 'available', 'upgradable']);

export function toLibraryItem(r: Raw): LibraryItem {
  const file = (typeof r.file === 'object' && r.file ? r.file : {}) as Raw;
  // The server may also report the derived states "failed" and "unmonitored" (list filters):
  // they are flags here, and the underlying state follows from the file.
  const raw = String(r.state ?? 'missing');
  const hasFile = !!(r.quality ?? r.fileQuality ?? file.quality);
  const state = (STATES.has(raw as MovieState) ? raw : hasFile ? 'available' : 'missing') as MovieState;
  return {
    id: Number(r.id),
    tmdbId: Number(r.tmdbId ?? 0),
    title: String(r.title ?? ''),
    sortTitle: String(r.sortTitle ?? r.title ?? '').toLowerCase(),
    year: Number(r.year ?? 0),
    status: String(r.status ?? 'released') as ReleaseStatus,
    state,
    monitored: raw === 'unmonitored' ? false : Boolean(r.monitored),
    qualityProfileId: Number(r.qualityProfileId ?? 0),
    minimumAvailability: String(r.minimumAvailability ?? 'released'),
    rootFolderId: Number(r.rootFolderId ?? 0),
    collectionId: num(r.collectionId),
    path: String(r.path ?? ''),
    addedAt: time(r.addedAt),
    genres: list(r.genres),
    posterUrl: str(r.posterUrl),
    dominantColor: str(r.dominantColor),
    quality: str(pick(r, 'fileQuality', 'quality') ?? file.quality),
    size: num(pick(r, 'sizeOnDisk', 'fileSize', 'size') ?? file.size),
    score: num(pick(r, 'fileScore', 'customScore', 'score') ?? file.customScore ?? file.score),
    hdr: list(pick(r, 'fileHdr', 'hdr') ?? file.hdr),
    progress:
      num(pick(r, 'downloadProgress', 'progress')) ?? num((r.download as Raw | null | undefined)?.progress) ?? 0,
    failed: raw === 'failed' || Boolean(pick(r, 'lastDownloadFailed', 'failed')),
    releaseDigital: str(r.releaseDigital),
    releasePhysical: str(r.releasePhysical),
    releaseCinema: str(r.releaseCinema),
    tags: list(r.tags),
    tagIds: Array.isArray(r.tagIds) ? r.tagIds.filter((x): x is number => typeof x === 'number') : [],
  };
}

/** The columnar list (one array per field, entry i = movie i): compact on the wire for 10k films. */
export type MovieColumnsWire = {
  total?: number;
  id: number[];
  title: string[];
  year?: number[];
  status?: string[];
  state?: string[];
  failed?: boolean[];
  monitored?: boolean[];
  posterUrl?: Array<string | null>;
  dominantColor?: Array<string | null>;
  quality?: Array<string | null>;
  sizeOnDisk?: Array<number | null>;
  customScore?: Array<number | null>;
  hdr?: string[];
  progress?: Array<number | null>;
  addedAt?: number[];
  rootFolderId?: number[];
  path?: string[];
  genres?: string[][];
  runtimeMin?: number[];
  qualityProfileId?: number[];
  sortTitle?: string[];
  tmdbId?: number[];
  minimumAvailability?: string[];
  collectionId?: Array<number | null>;
  releaseDigital?: Array<string | null>;
  releasePhysical?: Array<string | null>;
  releaseCinema?: Array<string | null>;
  tagIds?: Array<number[] | null>;
};

export function fromColumns(c: MovieColumnsWire): LibraryItem[] {
  const n = c.id.length;
  const out: LibraryItem[] = new Array(n);
  const keys = Object.keys(c).filter((k) => Array.isArray((c as Record<string, unknown>)[k])) as Array<
    keyof MovieColumnsWire
  >;
  for (let i = 0; i < n; i++) {
    const row: Raw = {};
    for (const k of keys) row[k] = (c[k] as unknown[])[i];
    out[i] = toLibraryItem(row);
  }
  return out;
}

export type LibraryStats = {
  total: number;
  onDisk: number;
  totalSize: number;
  monitored: number;
  missing: number;
  downloading: number;
  wanted: number;
};

export function computeStats(items: readonly LibraryItem[]): LibraryStats {
  const s: LibraryStats = {
    total: items.length,
    onDisk: 0,
    totalSize: 0,
    monitored: 0,
    missing: 0,
    downloading: 0,
    wanted: 0,
  };
  for (const m of items) {
    if (m.quality || m.size) s.onDisk++;
    s.totalSize += m.size ?? 0;
    if (m.monitored) s.monitored++;
    if (m.state === 'missing') {
      s.missing++;
      if (m.monitored) s.wanted++;
    }
    if (m.state === 'downloading') s.downloading++;
  }
  return s;
}
