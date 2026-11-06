import { describe, expect, it } from 'vitest';
import { parseOnboarding } from '@/api/onboarding';
import type { CalendarEvent, HistoryEntry, QualityProfileDetail } from '@/api/schema';
import { attemptsOf } from '@/screens/activity/FailedDownloadScreen';
import { exportUrl } from '@/screens/activity/HistoryPanel';
import { monthGrid, parseMonth, statusOf, toEvents, weekStart } from '@/screens/calendar/calendarModel';
import { issueLink } from '@/screens/health/HealthScreen';
import { ago, summaryLine } from '@/screens/home/HomeScreen';
import { regionName } from '@/screens/movie/parts';
import { currentStep } from '@/screens/onboarding/OnboardingScreen';
import { matchFilms, parseQuery } from '@/screens/palette/CommandPalette';
import { initialSearch, searchReducer } from '@/screens/search/searchModel';
import { parseList, toBody, validate } from '@/screens/settings/form';
import {
  conditionError,
  draftError,
  draftOf,
  moveEntry,
  plainWords,
  sizeRange,
  toInput,
} from '@/screens/settings/qualityModel';

const profile = (over: Partial<QualityProfileDetail> = {}): QualityProfileDetail => ({
  id: 1,
  name: 'Cinema HD',
  upgradeAllowed: true,
  cutoffQuality: 'bluray-1080p',
  cutoffLabel: 'Bluray-1080p',
  cutoffScore: 100,
  minScore: 0,
  minUpgradeStep: 10,
  entries: [
    { qualities: ['remux-1080p'], allowed: true },
    { qualities: ['bluray-1080p'], allowed: true },
    { group: 'WEB 1080p', qualities: ['webdl-1080p', 'webrip-1080p'], allowed: true },
    { qualities: ['hdtv-720p'], allowed: false },
  ],
  items: null,
  languages: null,
  preferredProtocol: '',
  scores: [{ ruleId: 7, rule: 'DV', score: 40 }],
  sizes: [],
  usage: { movies: 3, collections: 0, importLists: 0 },
  ...over,
});
const names = new Map([
  ['bluray-1080p', 'Bluray-1080p'],
  ['remux-1080p', 'Remux-1080p'],
]);

describe('quality profile draft', () => {
  it('round-trips a profile into the PUT body, scores by rule', () => {
    const body = toInput(draftOf(profile()));
    expect(body.cutoff).toBe('bluray-1080p');
    expect(body.scores).toEqual([{ ruleId: 7, score: 40 }]);
    expect(body).not.toHaveProperty('sizes'); // untouched sizes are kept by the server
  });
  it('reorders entries and keeps the others in place', () => {
    const e = draftOf(profile()).entries;
    expect(moveEntry(e, 0, 2).map((x) => x.group || x.qualities?.[0])).toEqual([
      'bluray-1080p',
      'WEB 1080p',
      'remux-1080p',
      'hdtv-720p',
    ]);
    expect(moveEntry(e, 0, 9)).toEqual(e);
  });
  it('says what blocks a save', () => {
    expect(draftError(draftOf(profile()))).toBeNull();
    expect(draftError({ ...draftOf(profile()), name: ' ' })).toMatch(/name/);
    expect(draftError({ ...draftOf(profile()), cutoff: 'hdtv-720p' })).toMatch(/cutoff/);
    expect(draftError({ ...draftOf(profile()), minScore: 200 })).toMatch(/lower than the minimum/);
  });
  it('reads the profile in plain words', () => {
    const s = plainWords(draftOf(profile()), names);
    expect(s).toContain('1080p');
    expect(s).toContain('at least 10 points');
    expect(s).toContain('Bluray-1080p scoring +100');
    expect(plainWords({ ...draftOf(profile()), upgradeAllowed: false }, names)).toContain('no upgrades');
  });
  it('gives sizes for a two-hour film', () => {
    expect(sizeRange({ minMbPerMin: 50, maxMbPerMin: 200 })).toBe('6–24 GB');
    expect(sizeRange({ minMbPerMin: 50, maxMbPerMin: 0 })).toBe('from 6 GB');
    expect(sizeRange({ minMbPerMin: 0, maxMbPerMin: 0 })).toBe('any size');
  });
  it('validates rule conditions', () => {
    expect(conditionError({ kind: 'hdr', value: '' })).toMatch(/value/);
    expect(conditionError({ kind: 'title_regex', value: '(' })).toMatch(/regular expression/);
    expect(conditionError({ kind: 'size_range', min: 10, max: 5 })).toMatch(/above/);
    expect(conditionError({ kind: 'size_range', min: 10, max: 0 })).toBeNull();
  });
});

describe('connection forms', () => {
  it('drops blank secrets so stored ones are kept, parses numbers and lists', () => {
    const spec = [
      { kind: 'secret', name: 'apiKey', label: 'API key', stored: true },
      { kind: 'number', name: 'priority', label: 'Priority', min: 1 },
      { kind: 'list', name: 'categories', label: 'Categories', numbers: true },
    ] as const;
    expect(toBody(spec, { apiKey: '', priority: '3', categories: '2000, 2040,x' })).toEqual({
      priority: 3,
      categories: [2000, 2040],
    });
    expect(toBody(spec, { apiKey: 'new' })).toMatchObject({ apiKey: 'new' });
    expect(validate(spec, { priority: 0 })).toEqual({ priority: 'Priority must be 1 or more.' });
    expect(parseList('a b,,c')).toEqual(['a', 'b', 'c']);
  });
  it('requires full URLs', () => {
    expect(validate([{ kind: 'url', name: 'u', label: 'URL', required: true }], { u: 'host:8080' }).u).toMatch(/http/);
  });
});

describe('calendar', () => {
  it('builds Monday-first grids of whole weeks', () => {
    const g = monthGrid(new Date(2026, 10, 1)); // November 2026 starts on a Sunday
    expect(g.length % 7).toBe(0);
    expect(g[0]!.getDay()).toBe(1);
    expect(g[0]!.getDate()).toBe(26);
    expect(weekStart(new Date(2026, 10, 5)).getDate()).toBe(2);
    expect(parseMonth('2026-02', new Date()).getMonth()).toBe(1);
    expect(parseMonth('nope', new Date(2026, 4, 9)).getMonth()).toBe(4);
  });
  it('maps server events with their status, filtered by release type', () => {
    const movie = {
      id: 1,
      title: 'Ninefold',
      tmdbId: 1,
      year: 2026,
      monitored: true,
      state: 'upgradable',
      status: 'released',
      hasFile: true,
      posterUrl: null,
      dominantColor: null,
      quality: 'webdl-1080p',
      qualityName: 'WEBDL-1080p',
      minimumAvailability: 'released',
    } as const;
    const events: CalendarEvent[] = [
      { date: '2026-11-12', type: 'physical', note: 'Blu-ray may upgrade', searchable: true, movie },
      { date: '2026-11-03', type: 'digital', note: 'Already have it', searchable: true, movie },
      { date: '2026-11-04', type: 'cinema', note: '', searchable: false, movie: { ...movie, monitored: false } },
    ];
    const out = toEvents(events, new Set(['digital', 'physical']));
    expect(out.map((e) => [e.day, e.status])).toEqual([
      ['2026-11-03', 'available'],
      ['2026-11-12', 'upgraded'],
    ]);
    expect(statusOf({ state: 'missing', monitored: false }, 'cinema')).toBe('unmonitored');
  });
});

describe('home, health, history, film page', () => {
  it('summarises calmly', () => {
    expect(summaryLine(0, 0, 0)).toBe('All quiet in the booth.');
    expect(summaryLine(3, 2, 1)).toBe('3 prints on their way, 2 premieres this week. One thing needs a look.');
    expect(ago(new Date(Date.now() - 4 * 60_000).toISOString())).toBe('4 min ago');
  });
  it('links health issues to settings pages only', () => {
    expect(issueLink('/settings/indexers')).toBe('/settings/indexers');
    expect(issueLink('/settings/library/root-folders')).toBe('/settings/library/root-folders');
    expect(issueLink('https://evil.example')).toBeNull();
    expect(issueLink('/system/status')).toBeNull();
  });
  it('exports history with the filters on screen', () => {
    const u = new URL(exportUrl({ event: 'failure', movie: 4, since: '7d' }, Date.UTC(2026, 10, 6)));
    expect(u.pathname).toMatch(/\/history\/export$/);
    expect(u.searchParams.get('format')).toBe('csv');
    expect(u.searchParams.get('event')).toContain('download_failed');
    expect(u.searchParams.get('movieId')).toBe('4');
    expect(u.searchParams.get('since')).toBe('2026-10-30T00:00:00.000Z');
    expect(new URL(exportUrl({ event: 'all', since: 'any' })).searchParams.has('event')).toBe(false);
  });
  it('names release regions', () => {
    expect(regionName('us')).toBe('United States');
    expect(regionName('FR')).toBe('France');
    expect(regionName('')).toBeNull();
    expect(regionName('<b>')).toBeNull();
  });
  it('lists the releases tried for a failed download', () => {
    const h = [
      {
        id: 1,
        event: 'grabbed',
        sourceTitle: 'A-ZEPHYR',
        downloadId: 10,
        createdAt: '2026-11-03T17:00:00Z',
        customScore: 30,
        movieId: 1,
        quality: null,
        data: { indexer: 'Nebula' },
      },
      {
        id: 2,
        event: 'download_failed',
        sourceTitle: 'A-ZEPHYR',
        downloadId: 10,
        createdAt: '2026-11-03T17:40:00Z',
        customScore: null,
        movieId: 1,
        quality: null,
        data: { reason: 'encrypted archive' },
      },
      {
        id: 3,
        event: 'grabbed',
        sourceTitle: 'A-LUMEN',
        downloadId: 11,
        createdAt: '2026-11-03T17:48:00Z',
        customScore: 25,
        movieId: 1,
        quality: null,
        data: {},
      },
    ] as HistoryEntry[];
    const a = attemptsOf(h, 11);
    expect(a.map((x) => [x.n, x.release, x.state])).toEqual([
      [1, 'A-ZEPHYR', 'failed'],
      [2, 'A-LUMEN', 'now'],
    ]);
    expect(a[0]!.sub).toContain('blocklisted: encrypted archive');
  });
});

describe('setup and palette', () => {
  it('reads the server steps and resumes at the first one not done', () => {
    const s = parseOnboarding({
      complete: false,
      dismissed: false,
      next: 'indexer',
      steps: [
        { id: 'tmdb', done: true, available: true, count: 1 },
        { id: 'root_folder', done: true, available: true, count: 1 },
        { id: 'quality_profile', done: true, available: true, count: 1 },
        { id: 'indexer', done: false, available: true, count: 0 },
      ],
    });
    expect(s.done).toMatchObject({ tmdb: true, folder: true, quality: true, indexer: false, film: false });
    expect(currentStep(undefined, s.done)).toBe('indexer');
    expect(currentStep(undefined, {})).toBe('welcome');
    expect(currentStep('client', s.done)).toBe('client');
    expect(currentStep('bogus', s.done)).toBe('indexer');
  });
  it('understands > and @ prefixes', () => {
    expect(parseQuery('>pause', 'all')).toEqual({ q: 'pause', scope: 'actions' });
    expect(parseQuery('@hale', 'all')).toEqual({ q: 'hale', scope: 'people' });
    expect(parseQuery(' drift ', 'films')).toEqual({ q: 'drift', scope: 'films' });
  });
  it('ranks local matches prefix first (offline fallback)', () => {
    const lib = ['Continental Drift', 'Drift', 'Halcyon Drift', 'Café Drôle'].map((title, id) => ({ id, title }));
    expect(matchFilms(lib as never, 'drift').map((m) => m.title)).toEqual([
      'Drift',
      'Halcyon Drift',
      'Continental Drift',
    ]);
    expect(matchFilms(lib as never, 'drole').map((m) => m.title)).toEqual(['Café Drôle']);
  });
});

describe('interactive search: one indexer retried', () => {
  it('puts the failed indexer back to pending and keeps the others', () => {
    let s = initialSearch(1);
    s = {
      ...s,
      searchId: 's1',
      status: 'finished',
      indexers: [
        { id: 1, name: 'Nebula', status: 'done', count: 4, durationMs: 100 },
        { id: 2, name: 'AltReel', status: 'failed', count: 0, durationMs: 15000, error: 'timeout' },
      ],
    };
    const r = searchReducer(s, { type: 'retry', indexerId: 2 });
    expect(r.status).toBe('running');
    expect(r.indexers.map((x) => x.status)).toEqual(['done', 'pending']);
    expect(r.indexers[1]!.error).toBeUndefined();
  });
});
