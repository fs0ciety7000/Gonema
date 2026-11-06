// Lot 2 flows on the real server + demo library. What the demo cannot answer is mocked with
// page.route (see e2e/mocks.mjs): TMDB-backed endpoints (no key, no network in the demo) and the
// interactive search stream (no indexer in the demo). Queue, history, tags, deletion and the film
// pages are the real server.
import assert from 'node:assert/strict';
import { after, before, describe, it } from 'node:test';
import { api, BASE, ensureServer, launch, openPage } from './helpers.mjs';
import { demoFilms, FAKE_ADDED_ID, mockInteractiveSearch, mockTmdb, SEARCH_ID } from './mocks.mjs';

describe('lot 2: add, interactive search, activity, library dialogs', () => {
  let browser;
  let films;
  before(async () => {
    await ensureServer();
    browser = await launch();
    films = await demoFilms(api);
  });
  after(async () => {
    await browser?.close();
  });

  it('A opens Add & discover; without a TMDB key the screen says so and the key form reports the server’s check', async () => {
    const page = await openPage(browser);
    await page.goto(`${BASE}/films`);
    await page.locator('.pc-link').first().waitFor();
    await page.keyboard.press('a');
    await page.waitForURL(/\/discover/);
    await page.getByRole('heading', { name: 'Film metadata isn’t connected yet.' }).waitFor();
    // the real PUT would call TMDB (no network here): its validation answer is mocked
    await page.route('**/api/v1/settings/tmdb', (r) =>
      r.request().method() === 'PUT'
        ? r.fulfill({
            status: 422,
            contentType: 'application/problem+json',
            body: JSON.stringify({ title: 'Unprocessable Entity', status: 422, detail: 'TMDB refused the key (401).' }),
          })
        : r.fallback(),
    );
    await page.getByRole('button', { name: 'Add a TMDB key' }).click();
    const dialog = page.getByRole('dialog', { name: 'Connect film metadata' });
    await dialog.getByRole('textbox', { name: 'API Read Access Token' }).fill('short');
    await dialog.getByRole('button', { name: 'Check and save' }).click();
    await dialog.getByText(/too short for a TMDB key/).waitFor();
    await dialog
      .getByRole('textbox', { name: 'API Read Access Token' })
      .fill('eyJhbGciOiJIUzI1NiJ9.fake-but-long-enough');
    await dialog.getByRole('button', { name: 'Check and save' }).click();
    await dialog.getByText(/TMDB refused the key \(401\)\. Check that you copied the whole key\./).waitFor();
    assert.deepEqual(
      page.errors.filter((e) => !/422/.test(e)),
      [],
    );
    await page.context().close();
  });

  it('searches TMDB as you type, links films already in the library, adds with options and undoes (TMDB mocked)', async () => {
    const page = await openPage(browser);
    const calls = [];
    await mockTmdb(page, films, calls);
    await page.goto(`${BASE}/discover`);
    const field = page.getByRole('searchbox', { name: /Search TMDB/ });
    await field.fill('drift');
    await page.getByText(/6 films on TMDB/).waitFor();
    assert.equal(calls.filter((c) => c.method === 'GET').length, 1, 'typing is debounced into one lookup');
    const inLib = page.getByRole('link', { name: new RegExp(`^In library( · wanted)? ?: open ${films[0].title}`) });
    assert.match(await inLib.getAttribute('href'), new RegExp(`/films/${films[0].id}$`));
    // the panel shows the selected film; ↓ moves, ↑ comes back
    await page.getByRole('heading', { level: 2, name: 'Continental Drift' }).waitFor();
    await field.press('ArrowDown');
    await page.getByRole('heading', { level: 2, name: films[0].title }).waitFor();
    await field.press('ArrowUp');
    const tags = page.getByRole('textbox', { name: 'Tags' });
    await tags.fill('e2e-drama');
    await tags.press('Enter');
    await field.press('Enter');
    const toast = page.getByRole('status').filter({ hasText: 'Added Continental Drift (1998)' });
    await toast.waitFor();
    const post = calls.find((c) => c.method === 'POST');
    assert.equal(post.body.tmdbId, 900_000);
    assert.equal(post.body.monitored, true);
    assert.equal(post.body.searchOnAdd, true);
    assert.deepEqual(post.body.tags, ['e2e-drama']);
    assert.ok(post.body.rootFolderId > 0 && post.body.qualityProfileId > 0);
    await page.getByRole('link', { name: /^In library ?: open Continental Drift/ }).waitFor();
    await toast.getByRole('button', { name: 'Undo' }).click();
    await page.waitForFunction(() => document.body.textContent.includes('Removed Continental Drift again'));
    assert.ok(calls.some((c) => c.method === 'DELETE' && c.url.endsWith(`/movies/${FAKE_ADDED_ID}`)));
    await page.context().close();
  });

  it('streams interactive search results per indexer, explains rejections and grabs (search mocked)', async () => {
    const page = await openPage(browser);
    const calls = [];
    const movie = films[0];
    await mockInteractiveSearch(page, movie, calls);
    await page.goto(`${BASE}/films/${movie.id}/search`);
    const status = page.getByRole('status', { name: 'Indexer progress' });
    await status.getByText('Kinoarchive').waitFor();
    await page.locator('tr.rel').first().waitFor();
    assert.match(await status.textContent(), /9 releases · 4 accepted · 5 rejected/);
    await page.getByText('Waiting for Kinoarchive').waitFor();
    await page.getByText('Cinémathèque didn’t answer.').waitFor();
    const keys = await page.locator('tr.rel').evaluateAll((rows) => rows.map((r) => r.dataset.key));
    assert.deepEqual(keys, ['r1', 'r2', 'r3', 'r4', 'x1', 'x2', 'x3', 'x4', 'x5'], 'accepted first, best rank first');
    await page.getByText('720p is below your profile’s floor (1080p).').waitFor();
    // rejected rows are dimmed by colour, never opacity
    const opacity = await page
      .locator('tr.rej')
      .first()
      .evaluate((r) => getComputedStyle(r).opacity);
    assert.equal(opacity, '1');
    // keyboard: Enter expands the score breakdown, G grabs
    await page.locator('tr[data-key="r1"]').focus();
    await page.keyboard.press('Enter');
    await page.getByText('Score breakdown').waitFor();
    await page.getByText(/Accepted as an upgrade/).waitFor();
    await page.keyboard.press('g');
    await page
      .locator('tr[data-key="r1"]')
      .getByRole('button', { name: /^Grabbed / })
      .waitFor();
    assert.deepEqual(calls.find((c) => c.url.endsWith('/grab'))?.body, { key: 'r1' });
    // Grab anyway: the confirmation recalls the reason, then sends override=true
    await page.locator('tr[data-key="x1"]').getByRole('button', { name: 'Grab anyway…' }).click();
    const confirm = page.getByRole('dialog', { name: 'Grab a rejected release?' });
    await confirm.getByText(/Only 1 seeder/).waitFor();
    await confirm.getByRole('button', { name: 'Grab anyway' }).click();
    await page.waitForFunction(() => document.body.textContent.includes('Grabbed on your request'));
    assert.deepEqual(calls.filter((c) => c.url.endsWith('/grab')).at(-1)?.body, { key: 'x1', override: true });
    // filters
    await page.getByRole('radio', { name: /^Rejected/ }).click();
    assert.equal(await page.locator('tr.rel:not(.rej)').count(), 0);
    assert.ok(calls.some((c) => c.url.includes(`/movies/${movie.id}/search/interactive`)));
    assert.ok(SEARCH_ID);
    await page.context().close();
  });

  it('queue: pauses with an optimistic status, removes with Undo before anything is sent', async () => {
    const page = await openPage(browser);
    const sent = [];
    // the demo download client is disabled: its pause answer is mocked, the queue itself is real
    await page.route('**/api/v1/queue/*/pause', (r) => {
      sent.push(r.request().url());
      return r.fulfill({ status: 204 });
    });
    page.on('request', (r) => {
      if (r.method() === 'DELETE' && r.url().includes('/queue/')) sent.push(`DELETE ${r.url()}`);
    });
    await page.goto(`${BASE}/queue`);
    await page.getByRole('heading', { level: 1, name: 'Activity' }).waitFor();
    const first = page
      .locator('.qi')
      .filter({ has: page.getByRole('button', { name: /^Pause / }) })
      .first();
    await first.waitFor();
    const title = (await first.locator('.t a').textContent()).trim();
    const row = page.locator('.qi').filter({ has: page.getByRole('link', { name: title, exact: true }) });
    await row.getByRole('button', { name: `Pause ${title}` }).click();
    await row.getByRole('button', { name: `Resume ${title}` }).waitFor();
    assert.ok(sent.some((u) => u.endsWith('/pause')));
    await row.getByRole('button', { name: `Remove ${title}…` }).click();
    const pop = page.getByRole('dialog', { name: `Remove ${title}` });
    assert.equal(await pop.getByRole('checkbox', { name: /delete the downloaded data/i }).isChecked(), false);
    await pop.getByRole('button', { name: 'Remove' }).click();
    await page.getByRole('link', { name: title, exact: true }).waitFor({ state: 'detached' });
    await page
      .getByRole('status')
      .filter({ hasText: `${title}: removed` })
      .getByRole('button', { name: 'Undo' })
      .click();
    await page.getByRole('link', { name: title, exact: true }).first().waitFor();
    assert.equal(sent.filter((s) => s.startsWith('DELETE')).length, 0, 'Undo before the toast closes sends nothing');
    await page.context().close();
  });

  it('history: filters by kind, keeps the filter in the URL', async () => {
    const page = await openPage(browser);
    await page.goto(`${BASE}/history`);
    await page.getByRole('table', { name: 'History, newest first' }).waitFor();
    await page.getByRole('button', { name: 'Event: All' }).click();
    await page.getByRole('menuitemradio', { name: 'Failures' }).click();
    await page.waitForURL(/event=failure/);
    await page.locator('tbody tr[data-index]').first().waitFor();
    const events = await page.locator('tbody tr[data-index] td:nth-child(3) .status').allTextContents();
    assert.ok(events.length > 0);
    assert.ok(
      events.every((t) => /failed|Blocklisted|Retr/i.test(t)),
      `only failures: ${[...new Set(events)]}`,
    );
    await page.context().close();
  });

  it('bulk tags: assigns a new tag to two films in one request, Undo restores (real server)', async () => {
    const page = await openPage(browser);
    await page.goto(`${BASE}/films`);
    const boxes = page.locator('.pc-select input');
    await page.locator('.pc').first().waitFor();
    await page.locator('.pc').nth(0).hover();
    await boxes.nth(0).click();
    await page.locator('.pc').nth(1).hover();
    await boxes.nth(1).click();
    const bulk = [];
    page.on('request', (r) => {
      if (r.method() === 'POST' && /\/movies\/bulk/.test(r.url())) bulk.push({ url: r.url(), body: r.postDataJSON() });
    });
    await page
      .getByRole('toolbar', { name: /Bulk actions for 2 selected films/ })
      .getByRole('button', { name: 'Tags' })
      .click();
    const dialog = page.getByRole('dialog', { name: /Tags · 2 films/ });
    const tag = `e2e-${Date.now().toString(36)}`;
    await dialog.getByRole('textbox', { name: 'New tag' }).fill(tag);
    await dialog.getByRole('button', { name: 'Add tag' }).click();
    await dialog.getByRole('button', { name: 'Apply 1 change' }).click();
    const toast = page.getByRole('status').filter({ hasText: 'Tags updated on 2 films' });
    await toast.waitFor();
    assert.deepEqual(bulk[0].body.set.addTags, [tag]);
    assert.equal(bulk[0].body.ids.length, 2);
    await toast.getByRole('button', { name: 'Undo' }).click();
    await page.waitForFunction(() => true);
    await page.waitForTimeout(500);
    assert.ok(bulk.some((b) => b.url.endsWith('/movies/bulk/restore')));
    await page.context().close();
  });

  it('delete file: explains the recycle bin and only deletes after the explicit confirmation (real server)', async () => {
    const cols = await api('/movies/ids?sort=added&order=desc');
    const i = cols.state.findIndex((s, k) => s === 'available' && cols.quality?.[k]);
    const id = cols.id[i];
    const page = await openPage(browser);
    const deletes = [];
    page.on('request', (r) => {
      if (r.method() === 'DELETE') deletes.push(r.url());
    });
    await page.goto(`${BASE}/films/${id}`);
    await page.getByRole('button', { name: 'Delete file…' }).click();
    const dialog = page.getByRole('dialog', { name: /Delete the file of/ });
    await dialog.getByText(/recycle bin.*days.*can be restored/).waitFor();
    await dialog.getByRole('button', { name: 'Cancel' }).click();
    await dialog.waitFor({ state: 'detached' });
    assert.equal(deletes.length, 0);
    await page.getByRole('button', { name: 'Delete file…' }).click();
    await page
      .getByRole('dialog', { name: /Delete the file of/ })
      .getByRole('button', { name: 'Move file to recycle bin' })
      .click();
    await page.getByRole('status').filter({ hasText: 'file moved to the recycle bin' }).waitFor();
    assert.equal(deletes.length, 1);
    assert.match(deletes[0], new RegExp(`/movies/${id}/file\\?confirm=true$`));
    await page.context().close();
  });
});
