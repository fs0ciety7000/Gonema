// v1.1 flows on the real server + demo library: collections (grid, filter, detail, monitoring with Undo, the
// film page's "Open"), Import a folder on a temporary folder with real (sparse) video files, stop / resume
// retrying (queue menu and failed-download page, Undo), bulk search start and cancel (home tile, library bulk bar).
// Mocked with page.route only what the demo cannot answer (see e2e/README.md): the empty collections list
// (the demo has collections), PUT /collections/{id}/monitor (it adds films from TMDB), GET /settings/tmdb
// answered as configured, and the scan job, candidates and
// confirmation of the import (they need TMDB). The real scan of the temporary folder is run too: without a
// TMDB key the server fails it, and the screen must say so. The bulk search is real: the suite adds an
// indexer that cannot be reached (searches end with no result) and removes it at the end.
import assert from 'node:assert/strict';
import { mkdirSync, mkdtempSync, rmSync, truncateSync, writeFileSync } from 'node:fs';
import { tmpdir } from 'node:os';
import { join } from 'node:path';
import { after, before, describe, it } from 'node:test';
import { api, axe, BASE, ensureServer, launch, openPage } from './helpers.mjs';
import { demoFilms, IMPORT_SCAN_ID, importScanFolders, mockLibraryImport } from './mocks.mjs';

const json = (body, status = 200) => ({ status, contentType: 'application/json', body: JSON.stringify(body) });
const post = (path, body) =>
  api(path, { method: 'POST', headers: { 'Content-Type': 'application/json' }, body: JSON.stringify(body ?? {}) });

describe('v1.1: collections, folder import, stop retrying, bulk search', () => {
  let browser;
  let films;
  let dir;
  let root;
  let indexer;
  const FOLDERS = [
    'Drift',
    'home videos',
    'Concrete Psalms (2017) [1080p]',
    'Continental Drift 1998',
    'fathom.2021.2160p.web-dl.hdr',
    'Snowdrift.2011.DVDRip',
  ];

  before(async () => {
    await ensureServer();
    browser = await launch();
    films = await demoFilms(api);
    // a real folder of films: one folder per film, a large (sparse) video in each, and a folder of home videos
    dir = mkdtempSync(join(tmpdir(), 'gonema-import-'));
    for (const name of FOLDERS) {
      mkdirSync(join(dir, name));
      if (name === 'home videos') writeFileSync(join(dir, name, 'notes.txt'), 'not a film');
      else {
        const f = join(dir, name, `${name.replace(/[^\w]+/g, '.')}.mkv`);
        writeFileSync(f, '');
        truncateSync(f, 700 * 1024 * 1024);
      }
    }
    root = await post('/root-folders', { path: dir });
  });

  after(async () => {
    if (indexer) await api(`/indexers/${indexer.id}`, { method: 'DELETE' }).catch(() => {});
    if (root) await api(`/root-folders/${root.id}`, { method: 'DELETE' }).catch(() => {});
    if (dir) rmSync(dir, { recursive: true, force: true });
    await browser?.close();
  });

  // ------------------------------------------------------------------ collections

  it('collections: grid from the real list, name filter, detail, monitoring with Undo, axe clean', async () => {
    const list = await api('/collections?pageSize=200');
    assert.ok(list.total > 0, 'the demo has collections');
    const c = list.collections.find((x) => !x.monitored) ?? list.collections[0];
    const page = await openPage(browser);
    await page.goto(`${BASE}/films`);
    await page
      .getByRole('navigation', { name: 'Main' })
      .getByRole('link', { name: /^Collections/ })
      .click();
    await page.waitForURL(/\/collections$/);
    await page.getByRole('heading', { name: 'Collections', level: 1 }).waitFor();
    await page.getByText(`${list.totals.collections} collections`, { exact: false }).waitFor();
    assert.deepEqual(await axe(page), []);

    await page.getByRole('searchbox', { name: 'Filter collections by name' }).fill(c.name);
    await page.waitForURL(/q=/);
    const card = page.getByRole('article', { name: c.name, exact: true });
    await card.waitFor();
    await card.getByRole('link', { name: new RegExp(`^${c.name}, \\d+ of \\d+ in library$`) }).click();
    await page.waitForURL(new RegExp(`/collections\\?id=${c.id}`));
    await page.getByRole('heading', { name: c.name, level: 1 }).waitFor();
    await page.getByRole('heading', { name: 'Films, in release order' }).waitFor();
    assert.equal(await page.locator('.parts > li').count(), c.films.length);
    assert.deepEqual(await axe(page), []);

    // monitoring applies at once, with Undo. PUT /collections/{id}/monitor adds the released films from TMDB:
    // the demo has no key, so the answer is mocked (the request bodies are checked)
    const puts = [];
    await page.route(`**/api/v1/collections/${c.id}/monitor`, (r) => {
      puts.push(r.request().postDataJSON());
      const { films: _f, counts: _c, ...rest } = c;
      return r.fulfill(
        json({ added: [], skipped: [], upcoming: [], collection: { ...rest, monitored: puts.at(-1).monitored } }),
      );
    });
    await page.getByRole('switch', { name: 'Monitor collection' }).click();
    await page
      .getByRole('status')
      .filter({ hasText: `Monitoring ${c.name}` })
      .waitFor();
    await page.getByRole('button', { name: 'Undo' }).click();
    await page.waitForTimeout(500);
    assert.equal(puts.length, 2);
    assert.equal(puts[0].monitored, true);
    assert.ok(puts[0].qualityProfileId > 0 && puts[0].rootFolderId > 0, 'a profile and a folder are sent');
    assert.equal(puts[1].monitored, false);

    // the film page's collection band opens this page
    const filmId = c.films.find((f) => f.movieId)?.movieId;
    await page.goto(`${BASE}/films/${filmId}`);
    await page.locator('#collection').getByRole('link', { name: 'Open' }).click();
    await page.waitForURL(new RegExp(`/collections\\?id=${c.id}`));
    await page.getByRole('heading', { name: c.name, level: 1 }).waitFor();
    assert.deepEqual(page.errors, []);
    await page.context().close();
  });

  it('collections: empty state (list mocked empty)', async () => {
    const page = await openPage(browser);
    await page.route('**/api/v1/collections?**', (r) =>
      r.fulfill(
        json({
          collections: [],
          page: 1,
          pageSize: 60,
          total: 0,
          totals: { collections: 0, films: 0, monitored: 0, notInLibrary: 0, upcoming: 0, withMissing: 0 },
        }),
      ),
    );
    await page.goto(`${BASE}/collections`);
    await page.getByRole('heading', { name: 'No sagas on the bill yet.' }).waitFor();
    await page.getByRole('link', { name: 'Import a folder' }).click();
    await page.waitForURL(/tab=import/);
    assert.deepEqual(await axe(page), []);
    await page.context().close();
  });

  // ------------------------------------------------------------------ folder import

  it('import: picks the real folder, the real server refuses the scan without TMDB and says so', async () => {
    const page = await openPage(browser);
    await page.route('**/api/v1/settings/tmdb', (r) =>
      r.request().method() === 'GET' ? r.fulfill(json({ configured: true })) : r.fallback(),
    );
    await page.goto(`${BASE}/discover?tab=import`);
    await page.getByRole('heading', { name: 'Import a folder', level: 1 }).waitFor();
    await page.getByText('Nothing is deleted.', { exact: false }).first().waitFor();
    await page.locator('.roots label').filter({ hasText: dir }).click();
    // the sub-folders are the real ones
    const tree = page.getByRole('list', { name: `Folders in ${dir}` });
    for (const name of FOLDERS)
      await tree
        .getByRole('button', { name: new RegExp(`^${name.replace(/[.()[\]]/g, '\\$&')}( Film folder)?$`) })
        .waitFor();
    assert.deepEqual(await axe(page), []);
    await page.getByRole('button', { name: `Scan ${dir}` }).click();
    // refused up front (503), not queued to fail in the background
    const alert = page.getByRole('alert').filter({ hasText: 'The scan did not start.' });
    await alert.waitFor({ timeout: 15_000 });
    assert.match(await alert.innerText(), /No TMDB key is configured/);
    const scans = await api('/library-import/scans');
    assert.equal(scans.filter((sc) => sc.rootFolderId === root.id && sc.status === 'failed').length, 0);
    await page.context().close();
  });

  it('import: scan streams, survives leaving the page, review (change match, move), import, done', async () => {
    const folders = importScanFolders(dir, FOLDERS, films);
    const page = await openPage(browser);
    const { calls } = await mockLibraryImport(page, { rootFolderId: root.id, folders, perMs: 1500 });
    await page.goto(`${BASE}/discover?tab=import`);
    await page.locator('.roots label').filter({ hasText: dir }).click();
    await page
      .getByRole('list', { name: `Folders in ${dir}` })
      .getByRole('button', { name: /^Drift( Film folder)?$/ })
      .click();
    await page
      .getByRole('list', { name: `Folders in ${dir}/Drift` })
      .getByRole('button', { name: 'Up one level' })
      .click();
    await page.getByRole('button', { name: `Scan ${dir}` }).click();
    assert.deepEqual(calls.find((c) => c.method === 'POST')?.body, { rootFolderId: root.id });
    await page.waitForURL(new RegExp(`scan=${IMPORT_SCAN_ID}`));
    await page.getByRole('progressbar', { name: 'Scan progress' }).waitFor();
    await page
      .getByRole('log', { name: 'Folders found' })
      .locator('.fold', { hasText: /^Drift$/ })
      .waitFor();

    // leave and come back: the running scan is shown again
    await page.goto(`${BASE}/films`);
    await page.locator('.pc-link').first().waitFor();
    await page.goto(`${BASE}/discover?tab=import`);
    await page.waitForURL(new RegExp(`scan=${IMPORT_SCAN_ID}`));
    await page.getByRole('progressbar', { name: 'Scan progress' }).waitFor();

    // done: the review opens on its own
    await page.getByRole('tab', { name: /^All/ }).waitFor({ timeout: 20_000 });
    await page.getByText('Nothing is deleted.', { exact: false }).first().waitFor();
    assert.deepEqual(await axe(page), []);

    // the ambiguous folder: change its match by keyboard in the TMDB search
    await page.getByRole('button', { name: 'Change match for Drift' }).click();
    const box = page.getByRole('combobox', { name: 'Search TMDB for this folder' });
    await box.fill('Drift Signal');
    await page.getByRole('option', { name: /Drift Signal 2011/ }).waitFor();
    await box.press('Enter');
    await page.getByText('Picked by you').waitFor();
    assert.ok(calls.some((c) => c.url.endsWith('/library-import/candidates') && c.body.query === 'Drift Signal'));
    await page.getByRole('checkbox', { name: 'Import Drift' }).check();
    // one film moved into the naming
    await page
      .getByRole('radiogroup', { name: /^Files of Concrete Psalms/ })
      .getByRole('radio', { name: 'Move' })
      .click();
    await page.getByText('→ renamed with your naming templates').waitFor();
    await page.getByRole('toolbar', { name: 'Import the selected folders' }).getByText('Nothing is deleted').waitFor();
    const importBtn = page.getByRole('button', { name: /^Import \d+ films?$/ });
    const n = Number((await importBtn.textContent()).match(/\d+/)[0]);
    await importBtn.click();
    await page.getByRole('heading', { name: /in the house\./ }).waitFor();
    const confirm = calls.find((c) => c.url.endsWith('/library-import/confirm'));
    assert.equal(confirm.body.rootFolderId, root.id);
    assert.equal(confirm.body.selections.length, n);
    assert.equal(confirm.body.selections.find((s) => s.folder === 'Drift')?.tmdbId, 730_001);
    assert.equal(confirm.body.selections.find((s) => s.folder.startsWith('Concrete'))?.mode, 'move');
    assert.ok(confirm.body.selections.every((s) => s.mode === 'keep' || s.mode === 'move'));
    await page.getByText('Nothing was deleted.', { exact: false }).waitFor();
    assert.deepEqual(await axe(page), []);
    await page.getByRole('button', { name: /^Review the \d+ left$/ }).click();
    await page.getByRole('tab', { name: /^All/ }).waitFor();
    assert.deepEqual(
      page.errors.filter((e) => !/Failed to load resource/.test(e)),
      [],
    );
    await page.context().close();
  });

  it('import: stop a running scan', async () => {
    const folders = importScanFolders(dir, FOLDERS, films);
    const page = await openPage(browser);
    const { calls } = await mockLibraryImport(page, { rootFolderId: root.id, folders, perMs: 400, hold: 2 });
    await page.goto(`${BASE}/discover?tab=import`);
    await page.locator('.roots label').filter({ hasText: dir }).click();
    await page.getByRole('button', { name: `Scan ${dir}` }).click();
    await page.getByRole('button', { name: 'Review the 2 found so far' }).waitFor({ timeout: 15_000 });
    await page.getByRole('button', { name: 'Stop scan' }).click();
    await page.getByRole('tab', { name: /^All/ }).waitFor();
    assert.equal(await page.locator('.imp tbody tr').count(), 2);
    assert.ok(calls.some((c) => c.method === 'DELETE'));
    await page.context().close();
  });

  // ------------------------------------------------------------------ stop retrying

  it('stop retrying: queue menu with Undo, failed-download page Stop / Resume', async () => {
    const queue = await api('/queue');
    const titleOf = (x) => x.movie?.title ?? x.title;
    const q = queue.find(
      (x) => x.actions?.stopRetrying && !x.retryStopped && queue.filter((y) => titleOf(y) === titleOf(x)).length === 1,
    );
    assert.ok(q, 'a queue entry that can stop retrying');
    const title = q.movie?.title ?? q.title;
    const page = await openPage(browser);
    await page.goto(`${BASE}/queue`);
    await page
      .getByRole('button', { name: `More actions for ${title}` })
      .first()
      .click();
    await page.getByRole('menuitem', { name: 'Stop retrying' }).click();
    await page
      .getByRole('status')
      .filter({ hasText: `Stopped retrying ${title}` })
      .waitFor();
    assert.equal((await api('/queue')).find((x) => x.id === q.id).retryStopped, true);
    await page.getByRole('button', { name: 'Undo' }).click();
    await page.waitForFunction(
      async (id) => !(await (await fetch('api/v1/queue')).json()).find((x) => x.id === id).retryStopped,
      q.id,
    );

    await page.goto(`${BASE}/queue/${q.id}`);
    await page.getByRole('button', { name: 'Stop retrying' }).click();
    await page.getByRole('button', { name: 'Resume retrying' }).waitFor();
    await page.getByText('Retries are stopped', { exact: false }).first().waitFor();
    assert.equal((await api('/queue')).find((x) => x.id === q.id).retryStopped, true);
    const hist = await api(`/movies/${q.movieId}/history?limit=20`);
    assert.ok(
      hist.some((e) => e.event === 'retries_stopped'),
      'written to history',
    );
    await page.getByRole('button', { name: 'Resume retrying' }).click();
    await page.getByRole('button', { name: 'Stop retrying' }).waitFor();
    assert.equal((await api('/queue')).find((x) => x.id === q.id).retryStopped, false);
    assert.deepEqual(await axe(page), []);
    assert.deepEqual(page.errors, []);
    await page.context().close();
  });

  // ------------------------------------------------------------------ bulk search

  it('bulk search: home tile starts it, the strip shows progress, Cancel stops it; bulk bar searches a selection', async () => {
    // the demo has no indexer: one that cannot be reached makes the searches real (and empty)
    indexer = await post('/indexers', {
      name: 'e2e unreachable',
      kind: 'newznab',
      baseUrl: 'http://127.0.0.1:9',
      enableRss: false,
    });
    const page = await openPage(browser);
    await page.goto(`${BASE}/`);
    const tile = page
      .getByRole('region', { name: 'Wanted' })
      .getByRole('button', { name: /^Search the [\d,]+ released$/ });
    const label = (await tile.textContent()).trim();
    await tile.click();
    const strip = page.getByRole('region', { name: 'Bulk search' });
    await strip.getByText(label.replace('Search', 'Searching'), { exact: true }).waitFor();
    await strip.getByRole('progressbar').waitFor();
    assert.deepEqual(await axe(page), []);
    const jobId = Number(await page.evaluate(() => localStorage.getItem('gn-bulk-search')));
    assert.ok(jobId > 0);
    // a reload finds the running search again
    await page.reload();
    await page.getByRole('region', { name: 'Bulk search' }).waitFor();
    await page.getByRole('region', { name: 'Bulk search' }).getByRole('button', { name: 'Cancel' }).click();
    await page.getByRole('status').filter({ hasText: 'Search cancelled' }).waitFor({ timeout: 30_000 });
    assert.equal((await api(`/bulk-searches/${jobId}`)).state, 'cancelled');
    await strip.waitFor({ state: 'detached' });

    // the library bulk bar: two films, one bulk job
    await page.goto(`${BASE}/films`);
    const boxes = page.locator('.pc-select input');
    await page.locator('.pc').nth(0).hover();
    await boxes.nth(0).click();
    await page.locator('.pc').nth(1).hover();
    await boxes.nth(1).click();
    const reqs = [];
    page.on('request', (r) => r.url().endsWith('/api/v1/movies/search') && reqs.push(r.postDataJSON()));
    await page
      .getByRole('toolbar', { name: /Bulk actions/ })
      .getByRole('button', { name: 'Search' })
      .click();
    await page.getByRole('region', { name: 'Bulk search' }).getByText('Searching 2 films', { exact: true }).waitFor();
    assert.equal(reqs[0].ids.length, 2);
    // it may wait for the unreachable indexer's pause: cancel it if it still runs
    await page
      .getByRole('region', { name: 'Bulk search' })
      .getByRole('button', { name: 'Cancel' })
      .click({ timeout: 3000 })
      .catch(() => {});
    await page
      .getByRole('status')
      .filter({ hasText: /Search (finished|cancelled)/ })
      .waitFor({ timeout: 60_000 });
    assert.deepEqual(page.errors, []);
    await page.context().close();
  });
});
