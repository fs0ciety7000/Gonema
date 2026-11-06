import { describe, expect, it } from 'vitest';
import type { IndexerStatus, SearchResult, SearchSnapshot } from '@/api/schema';
import { formatAge } from './InteractiveSearchScreen';
import {
  DEFAULT_FILTERS,
  filterResults,
  initialSearch,
  type SearchAction,
  type SearchEvent,
  type SearchState,
  searchReducer,
} from './searchModel';

const SID = 'a'.repeat(32);
const ix = (id: number, status: IndexerStatus['status'], extra: Partial<IndexerStatus> = {}): IndexerStatus => ({
  id,
  name: `Indexer ${id}`,
  status,
  count: 0,
  durationMs: 0,
  ...extra,
});
const rel = (key: string, o: Partial<SearchResult> = {}): SearchResult =>
  ({
    key,
    title: `Film.2024.2160p.${key}-GRP`,
    size: 1,
    protocol: 'usenet',
    indexerId: 1,
    indexer: 'Indexer 1',
    sources: ['Indexer 1'],
    seeders: -1,
    leechers: -1,
    grabs: -1,
    publishDate: null,
    flags: [],
    quality: 'webdl-2160p',
    qualityName: 'WEBDL-2160p',
    parsed: { releaseGroup: 'GRP' },
    score: { total: 10 },
    accepted: true,
    rejections: [],
    rank: 1,
    ...o,
  }) as SearchResult;

const snap = (o: Partial<SearchSnapshot> = {}): SearchSnapshot => ({
  id: SID,
  movieId: 7,
  status: 'running',
  startedAt: '2026-11-06T10:00:00Z',
  indexers: [ix(1, 'pending'), ix(2, 'pending')],
  results: [],
  ...o,
});
const ev = (e: SearchEvent): SearchAction => ({ type: 'event', event: e });
const run = (actions: SearchAction[], s: SearchState = initialSearch(7)) => actions.reduce(searchReducer, s);

describe('interactive search reducer', () => {
  it('keeps events that arrive before the POST answers and replays them once the id is known', () => {
    const s = run([
      { type: 'start' },
      ev({ type: 'search.batch', data: { searchId: SID, movieId: 7, results: [rel('k1')] } }),
      ev({ type: 'search.indexer_done', data: { searchId: SID, movieId: 7, indexer: ix(1, 'done', { count: 1 }) } }),
      { type: 'started', snapshot: snap() },
    ]);
    expect(s.searchId).toBe(SID);
    expect(s.status).toBe('running');
    expect([...s.results.keys()]).toEqual(['k1']);
    expect(s.indexers.find((x) => x.id === 1)?.status).toBe('done');
    expect(s.pending).toEqual([]);
  });

  it('ignores events of another search and of another film', () => {
    const s = run([
      { type: 'start' },
      { type: 'started', snapshot: snap() },
      ev({ type: 'search.batch', data: { searchId: 'b'.repeat(32), movieId: 7, results: [rel('other')] } }),
      ev({ type: 'search.batch', data: { searchId: SID, movieId: 8, results: [rel('film8')] } }),
    ]);
    expect(s.results.size).toBe(0);
  });

  it('never moves an indexer back to pending (late or replayed events) and marks failures', () => {
    const s = run([
      { type: 'start' },
      { type: 'started', snapshot: snap() },
      ev({ type: 'search.indexer_failed', data: { searchId: SID, indexer: ix(2, 'failed', { error: 'timeout' }) } }),
      ev({ type: 'search.indexer_done', data: { searchId: SID, indexer: ix(1, 'done', { count: 3 }) } }),
      // a snapshot taken before those events (stream replay after a reconnect)
      { type: 'snapshot', snapshot: snap({ indexers: [ix(1, 'pending'), ix(2, 'pending')] }) },
    ]);
    expect(s.indexers.map((x) => [x.id, x.status])).toEqual([
      [1, 'done'],
      [2, 'failed'],
    ]);
    expect(s.indexers[1]?.error).toBe('timeout');
  });

  it('merges duplicates into the sources of a release and applies new ranks', () => {
    const s = run([
      { type: 'start' },
      { type: 'started', snapshot: snap() },
      ev({ type: 'search.batch', data: { searchId: SID, results: [rel('k1', { rank: 1 }), rel('k2', { rank: 2 })] } }),
      ev({
        type: 'search.batch',
        data: {
          searchId: SID,
          indexer: 'Indexer 2',
          results: [rel('k3', { rank: 1, indexer: 'Indexer 2', sources: ['Indexer 2'] })],
          duplicates: [{ key: 'k1', indexer: 'Indexer 2' }],
          ranks: { k3: 1, k1: 2, k2: 3 },
        },
      }),
    ]);
    expect(s.results.get('k1')?.sources).toEqual(['Indexer 1', 'Indexer 2']);
    expect([...s.results.values()].sort((a, b) => a.rank - b.rank).map((r) => r.key)).toEqual(['k3', 'k1', 'k2']);
  });

  it('a late batch after search.finished still adds its results; pending indexers then count as no answer', () => {
    const s = run([
      { type: 'start' },
      { type: 'started', snapshot: snap() },
      ev({ type: 'search.finished', data: { searchId: SID, status: 'finished' } }),
      ev({ type: 'search.batch', data: { searchId: SID, results: [rel('late')] } }),
    ]);
    expect(s.status).toBe('finished');
    expect(s.results.has('late')).toBe(true);
    expect(s.indexers.every((x) => x.status === 'failed')).toBe(true);
  });

  it('a start failure keeps the server message', () => {
    const s = run([
      { type: 'start' },
      { type: 'start-failed', error: 'No enabled indexer can search for this movie.' },
    ]);
    expect(s.status).toBe('failed');
    expect(s.error).toMatch(/No enabled indexer/);
  });
});

describe('result filters', () => {
  const all = [
    rel('a2', { rank: 2 }),
    rel('a1', { rank: 1 }),
    rel('r1', { accepted: false, rank: 3, protocol: 'torrent', quality: 'bluray-1080p', indexer: 'Indexer 2' }),
  ];
  it('puts accepted releases first, best rank first, rejected after', () => {
    const v = filterResults(all, DEFAULT_FILTERS);
    expect(v.accepted.map((r) => r.key)).toEqual(['a1', 'a2']);
    expect(v.rejected.map((r) => r.key)).toEqual(['r1']);
    expect(v.counts).toEqual({ accepted: 2, rejected: 1 });
  });
  it('filters by decision, protocol, resolution, indexer and text', () => {
    expect(filterResults(all, { ...DEFAULT_FILTERS, decision: 'rejected' }).accepted).toEqual([]);
    expect(filterResults(all, { ...DEFAULT_FILTERS, protocol: 'torrent' }).counts).toEqual({
      accepted: 0,
      rejected: 1,
    });
    expect(filterResults(all, { ...DEFAULT_FILTERS, quality: '1080p' }).rejected).toHaveLength(1);
    expect(filterResults(all, { ...DEFAULT_FILTERS, indexer: 'Indexer 2' }).counts.accepted).toBe(0);
    expect(filterResults(all, { ...DEFAULT_FILTERS, text: 'a1-grp' }).accepted.map((r) => r.key)).toEqual(['a1']);
  });
});

describe('formatAge', () => {
  const now = Date.parse('2026-11-06T12:00:00Z');
  it('says minutes, hours, days and years', () => {
    expect(formatAge('2026-11-06T11:50:00Z', now)).toBe('10 min');
    expect(formatAge('2026-11-05T12:00:00Z', now)).toBe('24 h');
    expect(formatAge('2026-10-25T12:00:00Z', now)).toBe('12 d');
    expect(formatAge(null, now)).toBe('—');
  });
});
