import { act, render, screen } from '@testing-library/react';
import { describe, expect, it, vi } from 'vitest';
import type { CollectionFilm, LibraryFolder, LibraryImport, LibraryScan } from '@/api/v11';
import { bulkLine, bulkSummary, bulkTitle } from '@/components/shell/BulkSearchStrip';
import { bulkSearch, isEnded, parseBulkEvent } from '@/lib/bulkSearch';
import { feedUrl, parseDays } from '@/screens/calendar/CalendarFeedDialog';
import {
  addableOf,
  addSettingsOf,
  missingOf,
  pipOf,
  releaseLabel,
  yearsOf,
} from '@/screens/collections/collectionModel';
import { cleanSubPath } from '@/screens/discover/import/FolderStep';
import {
  belowCutoff,
  confidenceLevel,
  doneSummary,
  feedOf,
  initialChoice,
  listWords,
  reviewRows,
  secondsLeft,
  selectionSummary,
  tabCounts,
  toSelections,
  withFolder,
} from '@/screens/discover/import/importModel';
import {
  defaultGroupName,
  draftError,
  groupNameError,
  groupQualities,
  mergeWithNext,
  type ProfileDraft,
  renameGroup,
  splitGroup,
  ungroupQuality,
} from '@/screens/settings/qualityModel';

// ----------------------------------------------------------------- collections

const film = (over: Partial<CollectionFilm>): CollectionFilm => ({
  tmdbId: 1,
  title: 'A',
  year: 2020,
  releaseDate: null,
  posterUrl: null,
  state: 'on_disk',
  inLibrary: true,
  released: true,
  movieId: 1,
  monitored: true,
  status: 'released',
  minimumAvailability: 'released',
  releaseCinema: null,
  releaseDigital: null,
  releasePhysical: null,
  quality: null,
  qualityName: null,
  hdr: null,
  sizeOnDisk: null,
  progress: null,
  dominantColor: null,
  excluded: false,
  nextRelease: null as never,
  ...over,
});

const counts = {
  total: 4,
  inLibrary: 2,
  onDisk: 1,
  downloading: 0,
  wanted: 1,
  notInLibrary: 2,
  missing: 1,
  upcoming: 1,
  sizeOnDisk: 0,
  yearFrom: 2012,
  yearTo: 2026,
};

describe('collections', () => {
  const films = [
    film({ tmdbId: 1, state: 'on_disk' }),
    film({ tmdbId: 2, state: 'wanted' }),
    film({ tmdbId: 3, state: 'missing', inLibrary: false, movieId: null }),
    film({ tmdbId: 4, state: 'not_released', inLibrary: false, movieId: null, released: false }),
  ];
  it('counts the missing films as the mockup: released ones, plus announced ones when not monitored', () => {
    expect(missingOf({ monitored: false, counts, films })).toBe(2);
    expect(missingOf({ monitored: true, counts, films })).toBe(1);
    expect(addableOf({ monitored: true, films }).map((f) => f.tmdbId)).toEqual([3]);
    expect(addableOf({ monitored: false, films }).map((f) => f.tmdbId)).toEqual([3, 4]);
    expect(
      addableOf({ monitored: false, films: [film({ state: 'missing', inLibrary: false, excluded: true })] }),
    ).toEqual([]);
  });
  it('maps film states to the card pips and labels release dates', () => {
    expect(films.map(pipOf)).toEqual(['d', 'w', '', '']);
    expect(pipOf({ state: 'upcoming' })).toBe('u');
    expect(pipOf({ state: 'downloading' })).toBe('l');
    const now = Date.parse('2026-11-06T12:00:00Z');
    expect(releaseLabel({ year: 2026, nextRelease: { date: '2026-11-27', type: 'cinema' } }, now)).toBe(
      'In cinemas 27 Nov',
    );
    expect(releaseLabel({ year: 2026, nextRelease: { date: '2026-11-14', type: 'physical' } }, now)).toBe(
      'Out 14 Nov · physical',
    );
    expect(releaseLabel({ year: 2027, nextRelease: null as never }, now)).toBe('Announced for 2027');
    expect(yearsOf({ counts })).toBe('2012–2026');
    expect(yearsOf({ counts: { ...counts, yearFrom: 0, yearTo: 0 } })).toBe('');
  });
  it('adds films with the collection settings, else those of its first library film', () => {
    const lib = new Map([[7, { qualityProfileId: 3, rootFolderId: 2 }]]);
    const base = { searchOnAdd: true, minimumAvailability: 'released', films: [film({ movieId: 7 })] };
    expect(addSettingsOf({ ...base, qualityProfileId: null, rootFolderId: null }, lib, 1, 1)).toEqual({
      qualityProfileId: 3,
      rootFolderId: 2,
      minimumAvailability: 'released',
      searchOnAdd: true,
    });
    expect(addSettingsOf({ ...base, qualityProfileId: 9, rootFolderId: 8 }, lib, 1, 1)?.qualityProfileId).toBe(9);
    expect(
      addSettingsOf({ ...base, films: [], qualityProfileId: null, rootFolderId: null }, lib, undefined, 1),
    ).toBeNull();
  });
});

// ----------------------------------------------------------------- bulk search

describe('bulk search', () => {
  const p = {
    jobId: 4,
    label: 'missing_released',
    state: 'running',
    total: 12,
    done: 3,
    grabbed: 1,
    noResult: 2,
    skipped: 0,
    failed: 0,
    updatedAt: '',
  };
  it('validates the event data and keeps the newest job', () => {
    expect(parseBulkEvent(null)).toBeNull();
    expect(parseBulkEvent({ state: 'running' })).toBeNull();
    expect(parseBulkEvent({ ...p, done: 'x' })?.done).toBe(0);
    bulkSearch.set(parseBulkEvent(p));
    bulkSearch.set(parseBulkEvent({ ...p, jobId: 3, done: 9 }));
    expect(bulkSearch.get()?.jobId).toBe(4);
    expect(bulkSearch.remembered()).toBe(4);
    bulkSearch.set(parseBulkEvent({ ...p, state: 'cancelled' }));
    expect(isEnded(bulkSearch.get())).toBe(true);
    expect(bulkSearch.remembered()).toBeNull();
    bulkSearch.dismiss();
    expect(bulkSearch.get()).toBeNull();
  });
  it('says what runs and what was done', () => {
    const s = parseBulkEvent(p)!;
    expect(bulkTitle(s)).toBe('Searching the 12 released');
    expect(bulkTitle({ label: 'selection', total: 1 })).toBe('Searching 1 film');
    expect(bulkLine(s)).toBe('3 of 12 · 1 grabbed');
    expect(bulkLine({ ...s, state: 'waiting', reason: 'Every indexer is paused.' })).toBe('Every indexer is paused.');
    expect(bulkSummary({ ...s, state: 'cancelled' })).toEqual({
      title: 'Search cancelled',
      body: '3 of 12 searched · 1 grabbed · 2 with nothing acceptable. Grabs already made stay.',
    });
  });
  it('the strip shows the progress, cancels and ends with a summary toast', async () => {
    const { default: Strip } = await import('@/components/shell/BulkSearchStrip');
    const fetchMock = vi.fn(
      async (_req: Request) =>
        new Response(JSON.stringify({ ...p, jobId: 5, state: 'running', reason: 'Cancelling' }), {
          status: 202,
          headers: { 'Content-Type': 'application/json' },
        }),
    );
    vi.stubGlobal('fetch', fetchMock);
    const { RouterProvider, createRouter, createRootRoute } = await import('@tanstack/react-router');
    const root = createRootRoute({ component: () => <Strip /> });
    const router = createRouter({ routeTree: root });
    // a new job: the store shows the end of a job once (job 4 ended in the test above)
    const q = { ...p, jobId: 5 };
    bulkSearch.set(parseBulkEvent(q));
    render(<RouterProvider router={router} />);
    expect(await screen.findByText('Searching the 12 released')).toBeInTheDocument();
    expect(screen.getByRole('progressbar', { name: 'Searching the 12 released' })).toHaveAttribute(
      'aria-valuenow',
      '25',
    );
    await act(async () => screen.getByRole('button', { name: 'Cancel' }).click());
    expect(fetchMock).toHaveBeenCalled();
    const req = fetchMock.mock.calls[0]![0] as unknown as Request;
    expect(req.method).toBe('DELETE');
    expect(req.url).toMatch(/\/bulk-searches\/5$/);
    act(() => bulkSearch.set(parseBulkEvent({ ...q, state: 'finished', done: 12 })));
    expect(bulkSearch.get()).toBeNull();
    // a late event of the ended job does not bring the strip back
    act(() => bulkSearch.set(parseBulkEvent({ ...q, state: 'running' })));
    expect(bulkSearch.get()).toBeNull();
    vi.unstubAllGlobals();
  });
});

// ----------------------------------------------------------------- iCal feed

describe('iCal feed', () => {
  it('reads days and builds the URL', () => {
    expect(parseDays('14')).toBe(14);
    expect(parseDays('-1')).toBeNull();
    expect(parseDays('4000')).toBeNull();
    expect(parseDays('1.5')).toBeNull();
    expect(feedUrl({ url: 'https://x/feed.ics?key=k' })).toBe('https://x/feed.ics?key=k');
    expect(feedUrl({ path: '/api/v1/calendar.ics?key=k' }, 'https://media.example.org/gonema/')).toBe(
      'https://media.example.org/api/v1/calendar.ics?key=k',
    );
    expect(feedUrl({})).toBeNull();
  });
});

// ----------------------------------------------------------------- folder import

const folder = (over: Partial<LibraryFolder>): LibraryFolder => ({
  folder: 'A',
  name: 'A',
  path: '/m/A',
  status: 'new',
  hdr: [],
  candidates: [{ tmdbId: 1, title: 'A', year: 2001, confidence: 0.96, posterUrl: null, reason: 'Title and year' }],
  ...over,
});

describe('folder import', () => {
  const folders = [
    folder({ folder: 'b', status: 'new', size: 10 }),
    folder({ folder: 'a', status: 'ambiguous' }),
    folder({ folder: 'c', status: 'no_match', candidates: [] }),
    folder({ folder: 'd', status: 'in_library' }),
    folder({ folder: 'e', status: 'new', rejections: ['no video file'] }),
  ];
  it('selects only new matched folders by default, kept in place and monitored', () => {
    expect(initialChoice(folders[0]!, 3)).toMatchObject({
      selected: true,
      profileId: 3,
      monitored: true,
      mode: 'keep',
    });
    expect(initialChoice(folders[1]!, 3).selected).toBe(false);
    expect(initialChoice(folders[2]!, 3).match).toBeNull();
    expect(initialChoice(folders[4]!, 3).selected).toBe(false);
  });
  it('counts and orders the review tabs ("needs a look" first)', () => {
    expect(tabCounts(folders)).toEqual({ all: 5, look: 2, new: 2, library: 1 });
    expect(reviewRows(folders, 'all', 'look').map((f) => f.folder)).toEqual(['a', 'c', 'b', 'e', 'd']);
    expect(reviewRows(folders, 'all', 'name').map((f) => f.folder)).toEqual(['a', 'b', 'c', 'd', 'e']);
    expect(reviewRows(folders, 'library', 'look').map((f) => f.folder)).toEqual(['d']);
  });
  it('builds the confirm body with the per-folder mode, and the summary line', () => {
    const choices = Object.fromEntries(folders.map((f) => [f.folder, initialChoice(f, 2)]));
    choices.b = { ...choices.b!, mode: 'move', monitored: false };
    choices.d = { ...choices.d!, selected: true }; // in the library: never sent
    expect(toSelections(folders, choices)).toEqual([
      { folder: 'b', tmdbId: 1, qualityProfileId: 2, monitored: false, mode: 'move', searchOnAdd: false },
    ]);
    expect(selectionSummary(folders, choices)).toEqual({ count: 1, bytes: 10, kept: 0, moved: 1 });
  });
  it('rates the confidence like the mockup', () => {
    expect(confidenceLevel(0.97)).toEqual({ pct: 97, level: 4, word: 'high' });
    expect(confidenceLevel(0.8).word).toBe('medium');
    expect(confidenceLevel(0.52)).toEqual({ pct: 52, level: 2, word: 'low' });
  });
  it('tells a file below its profile cutoff', () => {
    const catalog = [
      { id: 'dvd', rank: 1 },
      { id: 'webdl-1080p', rank: 5 },
      { id: 'bluray-1080p', rank: 6 },
    ];
    const profile = {
      cutoffQuality: 'HD',
      upgradeAllowed: true,
      entries: [{ group: 'HD', qualities: ['webdl-1080p', 'bluray-1080p'], allowed: true }],
    };
    expect(belowCutoff('dvd', profile, catalog)).toBe(true);
    expect(belowCutoff('webdl-1080p', profile, catalog)).toBe(false);
    expect(belowCutoff('dvd', { ...profile, upgradeAllowed: false }, catalog)).toBe(false);
    expect(belowCutoff(undefined, profile, catalog)).toBe(false);
  });
  it('streams folders into the scan and feeds the newest first', () => {
    const scan: LibraryScan = {
      id: 1,
      jobId: 1,
      rootFolderId: 1,
      path: '',
      status: 'pending',
      total: 0,
      done: 0,
      createdAt: new Date(0).toISOString(),
      updatedAt: '',
      counts: { new: 0, ambiguous: 0, noMatch: 0, inLibrary: 0 },
      folders: [],
    };
    const s1 = withFolder(scan, { done: 1, total: 3, folder: folders[1]! });
    const s2 = withFolder(s1, { done: 2, total: 3, folder: folders[0]! });
    expect(s2).toMatchObject({
      status: 'running',
      done: 2,
      total: 3,
      counts: { new: 1, ambiguous: 1, noMatch: 0, inLibrary: 0 },
    });
    expect(s2.folders?.map((f) => f.folder)).toEqual(['a', 'b']);
    expect(feedOf(s2, ['a', 'b']).map((f) => f.folder)).toEqual(['b', 'a']);
    expect(feedOf(s2, []).map((f) => f.folder)).toEqual(['b', 'a']);
    expect(secondsLeft({ done: 5, total: 10, createdAt: new Date(0).toISOString() }, 10_000)).toBe(10);
    expect(secondsLeft({ done: 1, total: 10, createdAt: new Date(0).toISOString() }, 10_000)).toBeNull();
  });
  it('summarises the confirmation (results and summary object)', () => {
    const r: LibraryImport = {
      results: [
        { folder: 'b', outcome: 'kept', belowCutoff: true },
        { folder: 'x', outcome: 'failed', error: 'Permission denied.', belowCutoff: false },
      ],
      summary: {
        imported: 1,
        kept: 1,
        moved: 0,
        added: 0,
        skipped: 0,
        failed: 1,
        belowCutoff: 1,
        collectionsCompleted: [{ id: 1, name: 'A' }],
        collectionsGrown: [
          { id: 1, name: 'A' },
          { id: 2, name: 'B' },
        ],
      },
    };
    expect(doneSummary(r)).toMatchObject({
      imported: 1,
      failed: 1,
      failures: [{ folder: 'x', error: 'Permission denied.' }],
      collections: ['A', 'B'],
    });
    expect(listWords(['A', 'B', 'C'])).toBe('A, B and C');
  });
  it('accepts only sub-paths inside the root folder', () => {
    expect(cleanSubPath(' /Films A–M/ ')).toBe('Films A–M');
    expect(cleanSubPath('')).toBe('');
    expect(cleanSubPath('../etc')).toBeNull();
    expect(cleanSubPath('a/.hidden')).toBeNull();
  });
});

// ----------------------------------------------------------------- quality groups

const names = new Map([
  ['remux-2160p', 'Remux-2160p'],
  ['webdl-2160p', 'WEBDL-2160p'],
  ['webrip-2160p', 'WEBRip-2160p'],
  ['bluray-1080p', 'Bluray-1080p'],
]);
const draft = (): ProfileDraft => ({
  name: 'UHD',
  upgradeAllowed: true,
  entries: [
    { qualities: ['remux-2160p'], allowed: true },
    { qualities: ['webdl-2160p'], allowed: true },
    { qualities: ['webrip-2160p'], allowed: false },
    { qualities: ['bluray-1080p'], allowed: true },
  ],
  cutoff: 'webdl-2160p',
  minScore: 0,
  cutoffScore: 0,
  minUpgradeStep: 1,
  scores: {},
  sizes: null,
});

describe('quality groups', () => {
  it('names a group after its members and checks the name', () => {
    expect(defaultGroupName(['webdl-2160p', 'webrip-2160p'], names)).toBe('WEB 2160p');
    expect(defaultGroupName(['remux-2160p', 'bluray-1080p'], names)).toBe('Remux-2160p + Bluray-1080p');
    const d = groupQualities(draft(), ['webdl-2160p', 'webrip-2160p'], 'WEB 2160p');
    expect(defaultGroupName(['webdl-2160p', 'webrip-2160p'], names, d.entries)).toBe('WEB 2160p 2');
    expect(groupNameError(d.entries, ' ', names)).toMatch(/name/);
    expect(groupNameError(d.entries, 'web 2160P', names)).toBe('Another group has this name.');
    expect(groupNameError(d.entries, 'web 2160P', names, 'WEB 2160p')).toBeNull();
    expect(groupNameError(d.entries, 'Bluray-1080p', names)).toMatch(/quality/);
  });
  it('groups single qualities where the best was, the cutoff follows', () => {
    const d = groupQualities(draft(), ['webrip-2160p', 'webdl-2160p'], 'WEB 2160p');
    expect(d.entries).toEqual([
      { qualities: ['remux-2160p'], allowed: true },
      { group: 'WEB 2160p', qualities: ['webdl-2160p', 'webrip-2160p'], allowed: true },
      { qualities: ['bluray-1080p'], allowed: true },
    ]);
    expect(d.cutoff).toBe('WEB 2160p');
    expect(draftError(d)).toBeNull();
    const r = renameGroup(d, 'WEB 2160p', 'Web UHD');
    expect(r.cutoff).toBe('Web UHD');
    expect(r.entries[1]?.group).toBe('Web UHD');
  });
  it('splits, takes one out and merges', () => {
    const d = groupQualities(draft(), ['webdl-2160p', 'webrip-2160p'], 'WEB 2160p');
    const s = splitGroup(d, 'WEB 2160p');
    expect(s.entries.map((e) => e.qualities)).toEqual([
      ['remux-2160p'],
      ['webdl-2160p'],
      ['webrip-2160p'],
      ['bluray-1080p'],
    ]);
    expect(s.cutoff).toBe('webdl-2160p');
    const u = ungroupQuality(d, 'WEB 2160p', 'webrip-2160p');
    expect(u.entries).toHaveLength(4); // a group of one becomes a single quality
    const m = mergeWithNext(d, 1, names);
    expect(m.entries[1]).toEqual({
      group: 'WEB 2160p',
      qualities: ['webdl-2160p', 'webrip-2160p', 'bluray-1080p'],
      allowed: true,
    });
    expect(m.cutoff).toBe('WEB 2160p');
    const m2 = mergeWithNext(draft(), 0, names);
    expect(m2.entries[0]?.group).toBe('Remux + WEBDL 2160p');
    expect(m2.cutoff).toBe(m2.entries[0]?.group);
  });
});
