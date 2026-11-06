import type { IndexerStatus, SearchResult, SearchSnapshot } from '@/api/schema';
import { parseQuality } from '@/lib/quality';

/* Interactive search state, fed by the POST response, the snapshot (GET /searches/{id})
   and the event stream (search.batch / indexer_done / indexer_failed / finished).
   Events may arrive before the POST answers, twice (reconnect replay), or out of order:
   the reducer buffers events of an unknown search, merges results by key and never moves
   an indexer back to "pending". */

export type SearchStatus = 'idle' | 'starting' | 'running' | 'finished' | 'failed';

export type SearchState = {
  movieId: number;
  searchId: string | null;
  status: SearchStatus;
  error: string | null;
  indexers: IndexerStatus[];
  results: Map<string, SearchResult>;
  /** Events of a search whose id is not known yet (the POST has not answered). */
  pending: SearchEvent[];
  startedAt: string | null;
};

export type BatchData = {
  searchId: string;
  movieId?: number;
  indexer?: string;
  results?: SearchResult[] | null;
  duplicates?: Array<{ key: string; indexer: string }> | null;
  ranks?: Record<string, number> | null;
};
export type IndexerData = { searchId: string; movieId?: number; indexer: IndexerStatus };
export type FinishedData = { searchId: string; movieId?: number; status?: string; total?: number; accepted?: number };

export type SearchEvent =
  | { type: 'search.batch'; data: BatchData }
  | { type: 'search.indexer_done'; data: IndexerData }
  | { type: 'search.indexer_failed'; data: IndexerData }
  | { type: 'search.finished'; data: FinishedData };

export type SearchAction =
  | { type: 'start' }
  | { type: 'started'; snapshot: SearchSnapshot }
  | { type: 'snapshot'; snapshot: SearchSnapshot }
  | { type: 'start-failed'; error: string }
  | { type: 'event'; event: SearchEvent };

export function initialSearch(movieId: number): SearchState {
  return {
    movieId,
    searchId: null,
    status: 'idle',
    error: null,
    indexers: [],
    results: new Map(),
    pending: [],
    startedAt: null,
  };
}

const ORDER: Record<IndexerStatus['status'], number> = { pending: 0, done: 1, failed: 1 };

function mergeIndexer(list: IndexerStatus[], ix: IndexerStatus): IndexerStatus[] {
  const i = list.findIndex((x) => x.id === ix.id || (ix.id === 0 && x.name === ix.name));
  if (i < 0) return [...list, ix];
  const old = list[i]!;
  // A finished indexer never goes back to pending (late or replayed events).
  if (ORDER[ix.status] < ORDER[old.status]) return list;
  const next = list.slice();
  next[i] = { ...old, ...ix };
  return next;
}

function mergeResults(
  results: Map<string, SearchResult>,
  add: readonly SearchResult[] | null | undefined,
  duplicates?: BatchData['duplicates'],
  ranks?: BatchData['ranks'],
): Map<string, SearchResult> {
  if (!add?.length && !duplicates?.length && !ranks) return results;
  const next = new Map(results);
  for (const r of add ?? []) {
    const old = next.get(r.key);
    next.set(r.key, old ? { ...old, ...r, sources: union(old.sources, r.sources) } : r);
  }
  for (const d of duplicates ?? []) {
    const old = next.get(d.key);
    if (old) next.set(d.key, { ...old, sources: union(old.sources, [d.indexer]) });
  }
  if (ranks)
    for (const [k, rank] of Object.entries(ranks)) {
      const old = next.get(k);
      if (old && old.rank !== rank) next.set(k, { ...old, rank });
    }
  return next;
}

function union(a: readonly string[] | null | undefined, b: readonly string[] | null | undefined): string[] {
  return [...new Set([...(a ?? []), ...(b ?? [])])];
}

function applySnapshot(s: SearchState, snap: SearchSnapshot): SearchState {
  let indexers = s.indexers;
  for (const ix of snap.indexers ?? []) indexers = mergeIndexer(indexers, ix);
  const finished = snap.status === 'finished' || snap.status === 'failed';
  return {
    ...s,
    searchId: snap.id,
    startedAt: snap.startedAt,
    status: finished ? (snap.status as SearchStatus) : s.status === 'finished' ? 'finished' : 'running',
    error: snap.error || s.error,
    indexers,
    results: mergeResults(s.results, snap.results),
  };
}

function applyEvent(s: SearchState, ev: SearchEvent): SearchState {
  switch (ev.type) {
    case 'search.batch':
      return { ...s, results: mergeResults(s.results, ev.data.results, ev.data.duplicates, ev.data.ranks) };
    case 'search.indexer_done':
    case 'search.indexer_failed':
      return { ...s, indexers: mergeIndexer(s.indexers, ev.data.indexer) };
    case 'search.finished':
      return {
        ...s,
        status: ev.data.status === 'failed' ? 'failed' : 'finished',
        // Indexers still pending when the search ends did not answer.
        indexers: s.indexers.map((x) =>
          x.status === 'pending' ? { ...x, status: 'failed', error: x.error || 'No answer' } : x,
        ),
      };
  }
}

export function searchReducer(s: SearchState, a: SearchAction): SearchState {
  switch (a.type) {
    case 'start':
      return { ...initialSearch(s.movieId), status: 'starting' };
    case 'start-failed':
      return { ...s, status: 'failed', error: a.error };
    case 'started':
    case 'snapshot': {
      if (a.type === 'snapshot' && s.searchId && a.snapshot.id !== s.searchId) return s;
      let next = applySnapshot({ ...s, pending: [] }, a.snapshot);
      for (const ev of s.pending) if (ev.data.searchId === a.snapshot.id) next = applyEvent(next, ev);
      return next;
    }
    case 'event': {
      const ev = a.event;
      if (ev.data.movieId !== undefined && ev.data.movieId !== s.movieId) return s;
      if (!s.searchId) {
        // The POST has not answered yet: keep the event for when it does (bounded).
        if (s.status !== 'starting') return s;
        return { ...s, pending: [...s.pending.slice(-500), ev] };
      }
      if (ev.data.searchId !== s.searchId) return s;
      return applyEvent(s, ev);
    }
  }
}

// ----------------------------------------------------------------- views

export type Decision = 'all' | 'accepted' | 'rejected';
export type SearchFilters = {
  decision: Decision;
  protocol: 'any' | 'usenet' | 'torrent';
  /** Resolution ("2160p"), or "any". */
  quality: string;
  indexer: string;
  text: string;
};

export const DEFAULT_FILTERS: SearchFilters = {
  decision: 'all',
  protocol: 'any',
  quality: 'any',
  indexer: 'all',
  text: '',
};

const byRank = (a: SearchResult, b: SearchResult) => a.rank - b.rank || b.score.total - a.score.total;

export function resolutionOf(r: Pick<SearchResult, 'quality'>): string {
  return parseQuality(r.quality)?.res ?? 'Unknown';
}

export function filterResults(all: Iterable<SearchResult>, f: SearchFilters) {
  const text = f.text.trim().toLowerCase();
  const accepted: SearchResult[] = [];
  const rejected: SearchResult[] = [];
  for (const r of all) {
    if (f.protocol !== 'any' && r.protocol !== f.protocol) continue;
    if (f.quality !== 'any' && resolutionOf(r) !== f.quality) continue;
    if (f.indexer !== 'all' && r.indexer !== f.indexer && !(r.sources ?? []).includes(f.indexer)) continue;
    if (text && !r.title.toLowerCase().includes(text) && !(r.parsed.releaseGroup ?? '').toLowerCase().includes(text))
      continue;
    (r.accepted ? accepted : rejected).push(r);
  }
  accepted.sort(byRank);
  rejected.sort(byRank);
  return {
    accepted: f.decision === 'rejected' ? [] : accepted,
    rejected: f.decision === 'accepted' ? [] : rejected,
    counts: { accepted: accepted.length, rejected: rejected.length },
  };
}

export function counts(results: Iterable<SearchResult>) {
  let accepted = 0;
  let total = 0;
  for (const r of results) {
    total++;
    if (r.accepted) accepted++;
  }
  return { total, accepted, rejected: total - accepted };
}
