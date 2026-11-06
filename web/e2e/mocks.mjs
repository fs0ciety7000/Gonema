// Route mocks for what the demo server cannot answer (see e2e/README.md):
// - TMDB-backed endpoints (/lookup, /discover/*, POST /movies, /settings/tmdb): the demo has no TMDB key
//   and no network; films "in the library" in these mocks are real demo films (real ids and posters).
// - interactive search (POST /movies/{id}/search/interactive, /searches/{id}, the event stream): the
//   demo registers no indexer. The stream is one SSE body (batches, indexer_done, indexer_failed) that
//   ends with `retry: 600000`, so the client waits instead of reconnecting during the test.
// Everything else (library, queue, history, film pages, tags, move/rename plans, deletion) is the real server.

export const FAKE_ADDED_ID = 999_999;
const json = (body, status = 200) => ({
  status,
  contentType: status >= 400 ? 'application/problem+json' : 'application/json',
  body: JSON.stringify(body),
});

/** Real demo films to stand in as "already in the library". */
export async function demoFilms(api, n = 12) {
  const cols = await api('/movies/ids?sort=added&order=desc');
  const out = [];
  for (let i = 0; i < cols.id.length && out.length < n; i++) {
    out.push({
      id: cols.id[i],
      title: cols.title[i],
      year: cols.year?.[i] ?? 0,
      posterUrl: cols.posterUrl?.[i] ?? null,
      state: cols.state?.[i] ?? 'missing',
      monitored: cols.monitored?.[i] ?? true,
    });
  }
  return out;
}

const OVERVIEWS = [
  'A seismologist returns to the island town her father mapped, just as the ground starts keeping its own counsel.',
  'An engineer discovers the colony has been drifting toward the terminator line.',
  'Before the ship landed, a faint signal from the surface changed every plan on board.',
  'Snowed in at a mountain station, two strangers swap the stories they never told anyone.',
  'A fishing crew, a missing logbook and one very long winter on the north coast.',
  'The final chapter of the saga, announced for next year.',
];

/** TMDB lookup results: two of them are demo films already in the library. */
export function lookupResults(films) {
  const lib = films.slice(0, 2);
  const titles = ['Continental Drift', null, null, 'Snowdrift', 'The Drifters of Calder Bay', 'Drift: Terminus'];
  return titles.map((t, i) => {
    const f = t ? null : lib[i - 1];
    return {
      tmdbId: 900_000 + i,
      title: f ? f.title : t,
      year: f ? f.year : [1998, 0, 0, 2011, 2005, 2027][i],
      releaseDate: f ? `${f.year}-03-14` : ['1998-04-03', '', '', '2011-11-18', '2005-09-30', '2027-05-21'][i],
      overview: OVERVIEWS[i],
      originalLanguage: 'en',
      rating: { value: [7.1, 0, 0, 6.4, 6.9, 0][i], votes: 120 },
      posterPath: '',
      posterUrl: f?.posterUrl ?? null,
      inLibrary: !!f,
      movieId: f?.id,
      monitored: f?.monitored ?? false,
      state: f?.state ?? null,
      excluded: false,
    };
  });
}

function discoverMovie(i, f, date) {
  return {
    tmdbId: 910_000 + i,
    title: f
      ? f.title
      : [
          'Velvet Static',
          'Ghost Frequency',
          'Neon Orchard',
          'Marrow',
          'Atlas Undone',
          'Iron Meadow',
          'Second Sunrise',
          'Quiet Hours',
          'Small Hours in Cairo',
          'Low Tide, High Noon',
        ][i % 10],
    year: f ? f.year : 2023 + (i % 4),
    releaseDate: date ?? null,
    overview: OVERVIEWS[i % OVERVIEWS.length],
    popularity: 100 - i,
    rating: { value: 7, votes: 50 },
    posterUrl: f?.posterUrl ?? null,
    backdropUrl: null,
    inLibrary: !!f,
    movieId: f?.id ?? null,
    monitored: f?.monitored ?? false,
    state: f?.state ?? null,
    excluded: false,
  };
}

export function discoverPage(list, films) {
  const now = new Date();
  const results = Array.from({ length: 20 }, (_, i) => {
    const f = (list === 'trending' && (i === 2 || i === 3)) || (list === 'popular' && i === 1) ? films[i + 2] : null;
    const d = new Date(now.getTime() + (i + 1) * 5 * 86_400_000).toISOString().slice(0, 10);
    return discoverMovie(
      i + (list === 'upcoming' ? 20 : list === 'popular' ? 40 : 0),
      f,
      list === 'upcoming' ? d : null,
    );
  });
  return { page: 1, totalPages: 1, totalResults: results.length, results };
}

export async function mockTmdb(page, films, calls = []) {
  await page.route('**/api/v1/settings/tmdb', (r) =>
    r.request().method() === 'GET' ? r.fulfill(json({ configured: true })) : r.fallback(),
  );
  await page.route('**/api/v1/lookup?**', (r) => {
    calls.push({ method: 'GET', url: r.request().url() });
    return r.fulfill(json(lookupResults(films)));
  });
  await page.route('**/api/v1/discover/**', (r) => {
    const list = new URL(r.request().url()).pathname.split('/').pop();
    return r.fulfill(json(discoverPage(list, films)));
  });
  await page.route('**/api/v1/movies', (r) => {
    if (r.request().method() !== 'POST') return r.fallback();
    const body = r.request().postDataJSON();
    calls.push({ method: 'POST', url: r.request().url(), body });
    return r.fulfill(
      json(
        {
          id: FAKE_ADDED_ID,
          tmdbId: body.tmdbId,
          title: 'Continental Drift',
          year: 1998,
          monitored: body.monitored,
          collection: null,
        },
        201,
      ),
    );
  });
  await page.route(`**/api/v1/movies/${FAKE_ADDED_ID}`, (r) => {
    calls.push({ method: r.request().method(), url: r.request().url() });
    return r.request().method() === 'DELETE'
      ? r.fulfill({ status: 204 })
      : r.fulfill(json({ title: 'Not Found', status: 404, detail: 'mock' }, 404));
  });
}

// ----------------------------------------------------------------- interactive search

export const SEARCH_ID = 'a'.repeat(32);
const IX = [
  { id: 1, name: 'Nebula NZB', count: 14, durationMs: 1200 },
  { id: 2, name: 'AltReel', count: 9, durationMs: 2000 },
  { id: 3, name: 'Sprocket Tracker', count: 11, durationMs: 3400 },
  { id: 4, name: 'Kinoarchive' },
  { id: 5, name: 'Cinémathèque' },
];

function release(key, title, o) {
  return {
    key,
    title,
    size: o.size,
    protocol: o.torrent ? 'torrent' : 'usenet',
    indexerId: o.ix,
    indexer: IX[o.ix - 1].name,
    sources: [IX[o.ix - 1].name],
    seeders: o.torrent ? o.seeders : -1,
    leechers: o.torrent ? o.leechers : -1,
    grabs: -1,
    publishDate: new Date(Date.now() - o.days * 86_400_000).toISOString(),
    flags: o.flags ?? [],
    quality: o.quality,
    qualityName: o.qualityName,
    parsed: { releaseGroup: title.split('-').pop() },
    score: { total: o.score, rules: o.rules ?? [] },
    accepted: !o.reasons,
    rejections: o.reasons ?? [],
    upgrade: o.upgrade,
    notes: o.notes ?? [],
    rank: o.rank,
  };
}

export function searchResults(title) {
  const t = title.replace(/[^A-Za-z0-9]+/g, '.');
  const acc = [
    release('r1', `${t}.2160p.UHD.BluRay.REMUX.DV.HDR10.TrueHD.Atmos.7.1.HEVC-BOBiNE`, {
      size: 58.4e9,
      ix: 1,
      days: 12,
      quality: 'remux-2160p',
      qualityName: 'Remux-2160p',
      score: 110,
      rank: 1,
      flags: ['DV', 'HDR10', 'ATMOS', 'REMUX'],
      rules: [
        { rule: 'Dolby Vision', score: 40 },
        { rule: 'Lossless Atmos', score: 30 },
        { rule: 'Trusted group', score: 20 },
        { rule: 'Remux', score: 20 },
        { rule: 'HDR10 fallback', score: 0 },
      ],
      upgrade: {
        from: { quality: 'webdl-2160p', customScore: 70, revision: { version: 1, real: 0 } },
        reason: 'quality',
      },
      notes: ['Meets your cutoff, so upgrades stop after this one.', '58.4 GB is within 20–90 GB for Remux-2160p.'],
    }),
    release('r2', `${t}.2160p.BluRay.REMUX.HDR10.TrueHD.Atmos.7.1-MARQUEE`, {
      size: 61.2e9,
      ix: 3,
      torrent: true,
      seeders: 214,
      leechers: 12,
      days: 9,
      quality: 'remux-2160p',
      qualityName: 'Remux-2160p',
      score: 80,
      rank: 2,
      flags: ['HDR10', 'ATMOS', 'REMUX'],
    }),
    release('r3', `${t}.2160p.UHD.BluRay.x265.10bit.DV.HDR10.TrueHD.Atmos.7.1-AURORAHD`, {
      size: 32.6e9,
      ix: 2,
      days: 11,
      quality: 'bluray-2160p',
      qualityName: 'Bluray-2160p',
      score: 90,
      rank: 3,
      flags: ['DV', 'HDR10', 'ATMOS'],
    }),
    release('r4', `${t}.2160p.BluRay.DV.HDR10.DDP5.1.x265-LUMEN`, {
      size: 24.1e9,
      ix: 3,
      torrent: true,
      seeders: 88,
      leechers: 4,
      days: 10,
      quality: 'bluray-2160p',
      qualityName: 'Bluray-2160p',
      score: 75,
      rank: 4,
      flags: ['DV', 'HDR10'],
    }),
  ];
  const rej = [
    release('x1', `${t}.2160p.UHD.BluRay.REMUX.DV.HDR10.TrueHD.Atmos.7.1-NOCTURNE`, {
      size: 59e9,
      ix: 3,
      torrent: true,
      seeders: 1,
      leechers: 0,
      days: 2,
      quality: 'remux-2160p',
      qualityName: 'Remux-2160p',
      score: 100,
      rank: 5,
      flags: ['DV', 'ATMOS', 'REMUX'],
      reasons: [
        {
          code: 'min_seeders',
          message: 'Only 1 seeder. Sprocket Tracker needs at least 3, so it may never finish.',
          permanent: false,
        },
      ],
    }),
    release('x2', `${t}.1080p.BluRay.DTS-HD.MA.7.1.x264-GRANDE`, {
      size: 41.2e9,
      ix: 1,
      days: 12,
      quality: 'bluray-1080p',
      qualityName: 'Bluray-1080p',
      score: 25,
      rank: 6,
      flags: ['DTS-HD'],
      reasons: [
        { code: 'size', message: 'Size 41.2 GB is above 25 GB max for Bluray-1080p at 132 min.', permanent: true },
        { code: 'not_upgrade', message: 'Not an upgrade: 1080p is lower than your 2160p file.', permanent: true },
      ],
    }),
    release('x3', `${t}.720p.BluRay.x264-REELiNG`, {
      size: 5.4e9,
      ix: 3,
      torrent: true,
      seeders: 402,
      leechers: 31,
      days: 12,
      quality: 'bluray-720p',
      qualityName: 'Bluray-720p',
      score: 0,
      rank: 7,
      reasons: [{ code: 'quality', message: '720p is below your profile’s floor (1080p).', permanent: true }],
    }),
    release('x4', `${t}.2160p.WEB-DL.DDP5.1.HDR.H.265-ZEPHYR`, {
      size: 18.2e9,
      ix: 2,
      days: 13,
      quality: 'webdl-2160p',
      qualityName: 'WEBDL-2160p',
      score: 30,
      rank: 8,
      flags: ['HDR10'],
      reasons: [
        {
          code: 'blocklisted',
          message: 'Blocklisted: download failed on 2 Nov (CRC error, part 37).',
          permanent: true,
        },
      ],
    }),
    release('x5', `${t}.2160p.WEBRip.x265.10bit-MiNiPiX`, {
      size: 3.9e9,
      ix: 3,
      torrent: true,
      seeders: 1290,
      leechers: 340,
      days: 6,
      quality: 'webrip-2160p',
      qualityName: 'WEBRip-2160p',
      score: -990,
      rank: 9,
      reasons: [
        {
          code: 'min_score',
          message: 'Score −990 is below your minimum (0): matched “Low-bitrate encoder” (−1000).',
          permanent: true,
        },
      ],
    }),
  ];
  return { acc, rej };
}

function sse(events) {
  return `${events.map((e, i) => `id: ${i + 1}\nevent: ${e.type}\ndata: ${JSON.stringify({ id: i + 1, ...e })}\n\n`).join('')}retry: 600000\n\n`;
}

/** Mocks a search where 3 indexers answered, one is still searching and one failed. */
export async function mockInteractiveSearch(page, movie, calls = []) {
  const { acc, rej } = searchResults(movie.title);
  const startedAt = new Date(Date.now() - 6100).toISOString();
  const status = (ix, s) => ({
    id: ix.id,
    name: ix.name,
    status: s,
    count: ix.count ?? 0,
    durationMs: ix.durationMs ?? 0,
    ...(s === 'failed' ? { error: 'No answer within 15 s' } : {}),
  });
  const byIx = (n) => [...acc, ...rej].filter((r) => r.indexerId === n);
  const ranks = Object.fromEntries([...acc, ...rej].map((r) => [r.key, r.rank]));
  const events = [
    ...[1, 2, 3].flatMap((n) => [
      {
        type: 'search.batch',
        movieId: movie.id,
        data: { searchId: SEARCH_ID, movieId: movie.id, indexer: IX[n - 1].name, results: byIx(n), ranks },
      },
      {
        type: 'search.indexer_done',
        movieId: movie.id,
        data: { searchId: SEARCH_ID, movieId: movie.id, indexer: status(IX[n - 1], 'done') },
      },
    ]),
    {
      type: 'search.indexer_failed',
      movieId: movie.id,
      data: { searchId: SEARCH_ID, movieId: movie.id, indexer: status(IX[4], 'failed') },
    },
  ];
  const snapshot = {
    id: SEARCH_ID,
    movieId: movie.id,
    status: 'running',
    startedAt,
    indexers: [
      status(IX[0], 'done'),
      status(IX[1], 'done'),
      status(IX[2], 'done'),
      status(IX[3], 'pending'),
      status(IX[4], 'failed'),
    ],
    results: [...acc, ...rej],
  };
  await page.route('**/api/v1/events', (r) =>
    r.fulfill({
      status: 200,
      headers: { 'Content-Type': 'text/event-stream', 'Cache-Control': 'no-cache' },
      body: sse(events),
    }),
  );
  await page.route(`**/api/v1/movies/${movie.id}/search/interactive`, (r) => {
    calls.push({ method: 'POST', url: r.request().url() });
    return r.fulfill(
      json(
        {
          ...snapshot,
          searchId: SEARCH_ID,
          location: `/api/v1/searches/${SEARCH_ID}`,
          results: [],
          indexers: IX.map((ix) => status(ix, 'pending')),
        },
        202,
      ),
    );
  });
  await page.route(`**/api/v1/searches/${SEARCH_ID}`, (r) => r.fulfill(json(snapshot)));
  await page.route(`**/api/v1/searches/${SEARCH_ID}/grab`, (r) => {
    const body = r.request().postDataJSON();
    calls.push({ method: 'POST', url: r.request().url(), body });
    return r.fulfill(
      json({ id: 77_001, movieId: movie.id, title: body.key, client: 'qBittorrent', status: 'queued' }, 201),
    );
  });
  return { acc, rej };
}

// ----------------------------------------------------------------- folder import (v1.1)
// The scan matches each folder on TMDB: the demo has no key, so the scan job, the candidates search and the
// confirmation are mocked. The root folder and its sub-folders (GET /library-import/folders) are real: the
// tests make a temporary folder with real (sparse) video files. Films "in the library" are real demo films.

export const IMPORT_SCAN_ID = 9_101;

/** Scan answers for the real folders `names` of root folder `rootFolderId` (created by the caller). */
export function importScanFolders(rootPath, names, films) {
  const pick = (i) => films[i % films.length];
  return names.map((name, i) => {
    const base = { folder: name, name, path: `${rootPath}/${name}`, hdr: [], candidates: [] };
    const kind = i === 0 ? 'ambiguous' : i === 1 ? 'no_match' : i === names.length - 1 ? 'in_library' : 'new';
    const year = 1990 + ((i * 7) % 33);
    const title = name
      .replace(/[.(].*$/, '')
      .replace(/\./g, ' ')
      .trim();
    const q = ['bluray-1080p', 'webdl-2160p', 'webdl-1080p', 'bluray-720p', 'dvd'][i % 5];
    const file = kind === 'no_match' ? {} : { file: `${name}.mkv`, size: (3 + (i % 7) * 2.7) * 1e9, quality: q };
    if (kind === 'no_match')
      return { ...base, status: kind, rejections: ['No film title in these names'], title: name };
    if (kind === 'in_library') {
      const f = pick(i);
      return {
        ...base,
        ...file,
        status: kind,
        title: f.title,
        year: f.year,
        movieId: f.id,
        libraryPath: `${rootPath}/${name}`,
        candidates: [
          {
            tmdbId: 800_000 + i,
            title: f.title,
            year: f.year,
            confidence: 1,
            movieId: f.id,
            posterUrl: f.posterUrl,
            reason: 'the folder of a library movie that has no file yet',
          },
        ],
      };
    }
    const c = {
      tmdbId: 700_000 + i,
      title,
      year,
      confidence: kind === 'ambiguous' ? 0.52 : 0.88 + (i % 3) * 0.04,
      posterUrl: null,
      reason: kind === 'ambiguous' ? 'Title only, no year to compare' : 'Title and year',
    };
    const alt = {
      tmdbId: 710_000 + i,
      title: `${title}: Terminus`,
      year: year + 9,
      confidence: 0.21,
      posterUrl: null,
      reason: 'Not released yet, so unlikely',
    };
    return {
      ...base,
      ...file,
      hdr: i % 5 === 1 ? ['hdr10'] : [],
      status: kind,
      title,
      year,
      candidates:
        kind === 'ambiguous'
          ? [
              c,
              {
                ...pick(i),
                tmdbId: 720_000,
                confidence: 0.38,
                movieId: pick(i).id,
                reason: 'Title contains the folder name',
              },
              alt,
            ]
          : [c],
    };
  });
}

/**
 * Mocks the scan job of `rootFolderId` (folders revealed one by one, `perMs` apart; `hold` stops after that many
 * and keeps the scan running), the TMDB candidates search and the confirmation (each selection kept or moved).
 * `calls` collects the requests the app sent.
 */
export async function mockLibraryImport(page, { rootFolderId, folders, perMs = 250, hold, calls = [] }) {
  let startedAt = 0;
  let cancelled = false;
  const scan = () => {
    const elapsed = startedAt ? Date.now() - startedAt : 0;
    const n = Math.min(folders.length, hold ?? folders.length, Math.floor(elapsed / perMs));
    const shown = folders.slice(0, n);
    const running = !cancelled && n < folders.length && startedAt > 0;
    const counts = { new: 0, ambiguous: 0, noMatch: 0, inLibrary: 0 };
    for (const f of shown)
      counts[{ new: 'new', ambiguous: 'ambiguous', no_match: 'noMatch', in_library: 'inLibrary' }[f.status]]++;
    return {
      id: IMPORT_SCAN_ID,
      jobId: 1,
      rootFolderId,
      path: '',
      status: cancelled ? 'cancelled' : running ? 'running' : 'done',
      total: folders.length,
      done: n,
      createdAt: new Date(startedAt || Date.now()).toISOString(),
      updatedAt: new Date().toISOString(),
      counts,
      folders: shown,
    };
  };
  await page.route('**/api/v1/settings/tmdb', (r) =>
    r.request().method() === 'GET' ? r.fulfill(json({ configured: true })) : r.fallback(),
  );
  await page.route('**/api/v1/library-import/scans', (r) => {
    if (r.request().method() === 'POST') {
      calls.push({ method: 'POST', url: r.request().url(), body: r.request().postDataJSON() });
      startedAt = Date.now();
      cancelled = false;
      const { folders: _f, ...s } = scan();
      return r.fulfill(json({ ...s, status: 'pending', done: 0, total: 0 }, 202));
    }
    if (!startedAt) return r.fulfill(json([]));
    const { folders: _f, ...s } = scan();
    return r.fulfill(json([s]));
  });
  await page.route(`**/api/v1/library-import/scans/${IMPORT_SCAN_ID}`, (r) => {
    if (r.request().method() === 'DELETE') {
      calls.push({ method: 'DELETE', url: r.request().url() });
      cancelled = true;
      const { folders: _f, ...s } = scan();
      return r.fulfill(json(s, 202));
    }
    if (!startedAt) startedAt = Date.now() - perMs * folders.length;
    return r.fulfill(json(scan()));
  });
  await page.route('**/api/v1/library-import/candidates', (r) => {
    const body = r.request().postDataJSON();
    calls.push({ method: 'POST', url: r.request().url(), body });
    return r.fulfill(
      json([
        { tmdbId: 730_001, title: body.query, year: 2011, confidence: 0.93, posterUrl: null, reason: 'Title and year' },
        {
          tmdbId: 730_002,
          title: `${body.query} Returns`,
          year: 2014,
          confidence: 0.41,
          posterUrl: null,
          reason: 'Title starts the same',
        },
      ]),
    );
  });
  await page.route('**/api/v1/library-import/confirm', (r) => {
    const body = r.request().postDataJSON();
    calls.push({ method: 'POST', url: r.request().url(), body });
    const results = body.selections.map((s, i) =>
      i === 0 && body.selections.length > 2
        ? { folder: s.folder, outcome: 'failed', error: `Permission denied on ${s.folder}.`, belowCutoff: false }
        : {
            folder: s.folder,
            outcome: s.mode === 'move' ? 'moved' : 'kept',
            movieId: undefined,
            belowCutoff: i % 2 === 0,
          },
    );
    const ok = results.filter((x) => x.outcome !== 'failed');
    return r.fulfill(
      json({
        results,
        summary: {
          imported: ok.length,
          kept: ok.filter((x) => x.outcome === 'kept').length,
          moved: ok.filter((x) => x.outcome === 'moved').length,
          added: 0,
          skipped: 0,
          failed: results.length - ok.length,
          belowCutoff: ok.filter((x) => x.belowCutoff).length,
          collectionsCompleted: [],
          collectionsGrown: [{ id: 1, name: 'The Ashgrove Cycle' }],
        },
      }),
    );
  });
  return { calls, scan };
}
