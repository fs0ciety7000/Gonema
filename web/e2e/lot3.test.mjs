// Lot 3 flows on the real server + demo library: home, setup (resume and skip), calendar, ⌘K palette,
// wanted, blocklist (Undo), health, settings (quality profile, scoring rule tester, naming preview),
// pause all, history export, 404 search. Mocked with page.route only what the demo cannot answer
// (no network, no TMDB key, no indexer): the indexer connection test, TMDB people search and
// filmography, and the interactive search stream (see e2e/mocks.mjs and e2e/README.md).
import assert from 'node:assert/strict';
import { after, before, describe, it } from 'node:test';
import { api, BASE, ensureServer, launch, openPage } from './helpers.mjs';
import { demoFilms, mockInteractiveSearch, SEARCH_ID } from './mocks.mjs';

const json = (body, status = 200) => ({ status, contentType: 'application/json', body: JSON.stringify(body) });
const put = (path, body) =>
  api(path, { method: 'PUT', headers: { 'Content-Type': 'application/json' }, body: JSON.stringify(body) });

describe('lot 3: home, setup, calendar, palette, wanted, blocklist, health, settings', () => {
  let browser;
  let films;
  before(async () => {
    await ensureServer();
    browser = await launch();
    films = await demoFilms(api);
  });
  after(async () => {
    // leave the demo as the other suites expect it: setup skipped
    await put('/onboarding', { dismissed: true }).catch(() => {});
    await browser?.close();
  });

  it('opens the guided setup while it is incomplete, resumes at the first step left, and Skip lands on Home', async () => {
    await put('/onboarding', { dismissed: false });
    const page = await openPage(browser);
    await page.goto(`${BASE}/`);
    await page.waitForURL(/\/setup$/);
    // the demo has a folder, profiles and films but no account (local mode) and no TMDB key:
    // the setup resumes at the account step, which stays optional in the local mode
    await page.getByRole('heading', { name: 'Secure the booth' }).waitFor();
    await page.getByLabel('Username').waitFor();
    const steps = page.getByRole('navigation', { name: 'Setup steps' });
    await steps.getByRole('link', { name: /Library folder.*done/ }).waitFor();
    await steps.getByRole('link', { name: /Welcome/ }).click();
    await page.getByRole('heading', { name: 'Your private cinema is almost open.' }).waitFor();
    await page.getByRole('button', { name: 'Skip for now' }).click();
    await page.waitForURL(`${BASE}/`);
    await page.getByRole('heading', { name: /Good (morning|afternoon|evening)|Late show/ }).waitFor();
    assert.equal((await api('/onboarding')).dismissed, true);
    assert.deepEqual(page.errors, []);
    await page.context().close();
  });

  it('home shows the dashboard: tiles, queue, coming up, disks, activity; the wanted tile offers the bulk search', async () => {
    const d = await api('/dashboard');
    const page = await openPage(browser);
    await page.goto(`${BASE}/`);
    const lib = page.getByRole('region', { name: 'Library' });
    await lib.getByText(d.library.total.toLocaleString('en-US'), { exact: true }).waitFor();
    await page.getByRole('region', { name: 'Queue' }).locator('.q-item').first().waitFor();
    await page.getByRole('region', { name: 'Disk space' }).getByRole('meter').first().waitFor();
    // the wanted tile starts the bulk search of the released ones (v1.1: tested in v11.test.mjs); Wanted is in the sidebar
    await page
      .getByRole('region', { name: 'Wanted' })
      .getByRole('button', { name: /^Search the [\d,]+ released$/ })
      .waitFor();
    await page
      .getByRole('navigation', { name: 'Main' })
      .getByRole('link', { name: /^Wanted/ })
      .click();
    await page.waitForURL(/\/wanted$/);
    await page.locator('.wanted tbody tr').first().waitFor();
    await page.getByRole('tab', { name: /Cutoff unmet/ }).click();
    await page.waitForURL(/kind=cutoff/);
    await page.locator('.wanted tbody tr').first().waitFor();
    assert.deepEqual(page.errors, []);
    await page.context().close();
  });

  it('calendar: monitored films by default, announced unmonitored on request, agenda view', async () => {
    const page = await openPage(browser);
    const calls = [];
    page.on('request', (r) => r.url().includes('/api/v1/calendar') && calls.push(new URL(r.url())));
    await page.goto(`${BASE}/calendar`);
    await page.locator('.month .day').first().waitFor();
    await page.waitForTimeout(300);
    assert.ok(calls.length >= 1);
    assert.equal(calls[0].searchParams.get('unmonitored'), null, 'monitored only by default');
    const chip = page.getByRole('button', { name: 'Announced & unmonitored' });
    assert.equal(await chip.getAttribute('aria-pressed'), 'false');
    await chip.click();
    await page.waitForURL(/all=/);
    await page.waitForTimeout(400);
    assert.equal(calls.at(-1).searchParams.get('unmonitored'), 'true');
    await page.getByRole('radio', { name: 'Agenda' }).click();
    await page.waitForURL(/view=agenda/);
    await page.locator('.agenda, .empty').first().waitFor();
    await page.getByRole('button', { name: 'Next month' }).click();
    await page.waitForURL(/month=\d{4}-\d{2}/);
    assert.deepEqual(page.errors, []);
    await page.context().close();
  });

  it('Ctrl+K opens the palette (lazy chunk), finds a film, opens it; > lists actions; Esc returns focus', async () => {
    const page = await openPage(browser);
    const chunks = [];
    page.on('response', (r) => /CommandPalette-.*\.js/.test(r.url()) && chunks.push(r.url()));
    await page.goto(`${BASE}/calendar`);
    await page.locator('.month').waitFor();
    assert.equal(chunks.length, 0, 'the palette is not part of the first load');
    await page.keyboard.press('Control+k');
    const input = page.getByRole('combobox', { name: /Search films, people/ });
    await input.waitFor();
    assert.equal(chunks.length, 1);
    const film = films[0];
    await input.fill(film.title);
    const option = page
      .getByRole('option', { name: new RegExp(film.title.replace(/[.*+?^${}()|[\]\\]/g, '\\$&')) })
      .first();
    await option.waitFor();
    await input.press('Enter');
    await page.waitForURL(/\/films\/\d+$/);
    await page.keyboard.press('Control+k');
    await input.fill('>queue');
    await page.getByRole('option', { name: /Go to Queue/ }).waitFor();
    await input.press('Escape');
    await input.waitFor({ state: 'detached' });
    assert.deepEqual(page.errors, []);
    await page.context().close();
  });

  it('blocklist: a blocked group is listed; Remove applies at once with Undo, nothing is sent until the toast closes', async () => {
    const entry = await api('/blocklist/groups', {
      method: 'POST',
      headers: { 'Content-Type': 'application/json' },
      body: JSON.stringify({ group: 'E2EGRP', reason: 'e2e test entry' }),
    });
    const page = await openPage(browser);
    const deletes = [];
    page.on('request', (r) => r.method() === 'DELETE' && r.url().includes('/blocklist/') && deletes.push(r.url()));
    try {
      await page.goto(`${BASE}/blocklist`);
      const row = page.getByRole('row', { name: /E2EGRP/ });
      await row.waitFor();
      await row.getByRole('button', { name: /Remove group E2EGRP/ }).click();
      await row.waitFor({ state: 'detached' });
      await page.getByRole('button', { name: 'Undo' }).click();
      await page.getByRole('row', { name: /E2EGRP/ }).waitFor();
      assert.deepEqual(deletes, []);
    } finally {
      await api(`/blocklist/${entry.id}`, { method: 'DELETE' }).catch(() => {});
      await page.context().close();
    }
  });

  it('queue: Pause all and Resume all go to the server; history Export is a CSV of the filters on screen', async () => {
    const page = await openPage(browser);
    const posts = [];
    page.on('request', (r) => r.method() === 'POST' && /\/queue\/(pause|resume)$/.test(r.url()) && posts.push(r.url()));
    await page.goto(`${BASE}/queue`);
    await page.getByRole('button', { name: /Pause all|Resume all/ }).click();
    // the demo client is disabled: the server answers per client and the toast says what happened
    await page
      .locator('[role="status"], [role="alert"]')
      .filter({ hasText: /paused|resumed|Nothing/i })
      .first()
      .waitFor();
    assert.equal(posts.length, 1);
    await page.goto(`${BASE}/history?event=failure`);
    const href = await page.getByRole('link', { name: 'Export' }).getAttribute('href');
    const url = new URL(href, BASE);
    assert.equal(url.searchParams.get('format'), 'csv');
    assert.match(url.searchParams.get('event'), /download_failed/);
    const r = await fetch(url);
    assert.equal(r.status, 200);
    assert.match(r.headers.get('content-type'), /csv/);
    assert.deepEqual(page.errors, []);
    await page.context().close();
  });

  it('health lists issues with their fix and links to the matching settings; checks run on demand', async () => {
    const page = await openPage(browser);
    await page.goto(`${BASE}/system/health`);
    await page.locator('.issue').first().waitFor();
    await page.getByRole('button', { name: 'Run checks now' }).click();
    await page
      .getByRole('status')
      .filter({ hasText: /issue|All clear/ })
      .first()
      .waitFor();
    await page.locator('.issue').getByRole('link', { name: 'Open settings' }).first().click();
    await page.waitForURL(/\/settings\//);
    await page.getByRole('navigation', { name: 'Settings sections' }).waitFor();
    assert.deepEqual(page.errors, []);
    await page.context().close();
  });

  it('settings: a profile score is edited and saved, the tester scores a release, naming previews live', async () => {
    const page = await openPage(browser);
    await page.goto(`${BASE}/settings/quality`);
    const rules = page.getByRole('region', { name: /Scoring rules/ });
    const first = rules.getByRole('textbox', { name: /^Score for / }).first();
    await first.waitFor();
    const before = await first.inputValue();
    await first.fill('+7');
    await page.getByText('Unsaved changes').waitFor();
    const saved = page.waitForResponse(
      (r) => r.request().method() === 'PUT' && /\/quality-profiles\/\d+$/.test(r.url()),
    );
    await page.getByRole('button', { name: 'Save profile' }).click();
    assert.equal((await saved).status(), 200);
    await page.getByText(/Saved · applies to/).waitFor();
    // put the score back
    await first.fill(before);
    await page.getByRole('button', { name: 'Save profile' }).click();
    await page.getByText(/Saved · applies to/).waitFor();
    // tester
    await page
      .getByRole('textbox', { name: 'Release name to test' })
      .fill('Some.Film.2024.2160p.UHD.BluRay.REMUX.DV.HDR10.TrueHD.Atmos.7.1-GROUP');
    await page.getByRole('list', { name: 'Score breakdown' }).getByText('Total').waitFor();
    await page.locator('.parsed').getByText('2160p').first().waitFor();
    // keyboard reorder of qualities
    const grip = page.getByRole('button', { name: /^Move .*position 1 of/ });
    await grip.focus();
    await page.keyboard.press('ArrowDown');
    await page.getByText(/moved to position 2/).waitFor();
    await page.getByRole('button', { name: 'Discard' }).click();
    // naming preview
    await page
      .getByRole('navigation', { name: 'Settings sections' })
      .getByRole('link', { name: 'Library & naming' })
      .click();
    const preview = page.getByRole('status', { name: 'Preview' });
    await preview.locator('span').first().waitFor();
    assert.match(await preview.textContent(), /\S/);
    assert.deepEqual(page.errors, []);
    await page.context().close();
  });

  it('settings: adding an indexer tests it first; secrets are write-only (indexer test mocked: no network)', async () => {
    const page = await openPage(browser);
    const bodies = [];
    await page.route('**/api/v1/indexers/test**', (r) => {
      bodies.push(r.request().postDataJSON());
      r.fulfill(
        json({
          ok: false,
          message: 'Reached the indexer, but it refused the API key (100: Incorrect user credentials).',
        }),
      );
    });
    await page.goto(`${BASE}/settings/indexers`);
    await page.getByRole('button', { name: 'Add indexer' }).click();
    const dialog = page.getByRole('dialog', { name: 'Add an indexer' });
    await dialog.getByRole('textbox', { name: 'Name' }).fill('E2E indexer');
    await dialog.getByRole('textbox', { name: 'URL' }).fill('not a url');
    await dialog.getByRole('button', { name: 'Test' }).click();
    await dialog.getByText(/full address starting with http/).waitFor();
    await dialog.getByRole('textbox', { name: 'URL' }).fill('https://indexer.example');
    await dialog.getByLabel('API key').fill('secret-key-123');
    await dialog.getByRole('button', { name: 'Test' }).click();
    await dialog.getByText(/refused the API key/).waitFor();
    assert.equal(bodies[0].apiKey, 'secret-key-123');
    assert.equal(await dialog.getByLabel('API key').getAttribute('type'), 'password');
    await dialog.getByRole('button', { name: 'Cancel' }).click();
    await dialog.waitFor({ state: 'detached' });
    assert.deepEqual(
      page.errors.filter((e) => !/indexers\/test/.test(e)),
      [],
    );
    await page.context().close();
  });

  it('interactive search: Retry asks only the failed indexer again; the notice links to indexer health (stream mocked)', async () => {
    const page = await openPage(browser);
    const retries = [];
    await mockInteractiveSearch(page, films[0]);
    await page.route(`**/api/v1/searches/${SEARCH_ID}/indexers/*/retry**`, (r) => {
      retries.push(r.request().url());
      r.fulfill(
        json(
          {
            id: SEARCH_ID,
            movieId: films[0].id,
            startedAt: new Date().toISOString(),
            status: 'running',
            indexers: [{ id: 5, name: 'Cinémathèque', status: 'pending', count: 0, durationMs: 0 }],
            results: [],
          },
          202,
        ),
      );
    });
    await page.goto(`${BASE}/films/${films[0].id}/search`);
    const health = page.getByRole('link', { name: 'Indexer health' });
    await health.waitFor();
    assert.match(await health.getAttribute('href'), /\/system\/health#indexers$/);
    await page.getByRole('button', { name: 'Retry Cinémathèque' }).click();
    await page.waitForTimeout(300);
    assert.equal(retries.length, 1);
    assert.match(retries[0], /\/indexers\/5\/retry\?force=true$/);
    await page.getByText('Cinémathèque didn’t answer.').waitFor({ state: 'detached' });
    assert.equal(await page.getByRole('link', { name: 'Indexer health' }).count(), 0);
    await page.context().close();
  });

  it('discover: people are found by name, then their filmography opens (TMDB mocked)', async () => {
    const page = await openPage(browser);
    await page.route('**/api/v1/settings/tmdb', (r) => r.fulfill(json({ configured: true })));
    await page.route('**/api/v1/discover/people?**', (r) =>
      r.fulfill(
        json({
          page: 1,
          totalPages: 1,
          totalResults: 1,
          results: [
            {
              tmdbId: 4242,
              name: 'Ines Calloway',
              knownForDepartment: 'Directing',
              popularity: 3,
              profileUrl: null,
              knownFor: [],
            },
          ],
        }),
      ),
    );
    await page.route('**/api/v1/discover/person/4242', (r) =>
      r.fulfill(
        json({
          tmdbId: 4242,
          person: { tmdbId: 4242, name: 'Ines Calloway', profileUrl: null, birthday: null, deathday: null },
          movies: [],
        }),
      ),
    );
    await page.route('**/api/v1/discover/**', (r) => r.fallback());
    await page.goto(`${BASE}/discover?tab=discover&list=people`);
    await page.getByRole('textbox', { name: 'Person’s name or TMDB link' }).fill('ines');
    await page.getByRole('button', { name: 'Search', exact: true }).click();
    await page.getByRole('button', { name: /Ines Calloway/ }).click();
    await page.getByRole('heading', { name: /Ines Calloway · filmography/ }).waitFor();
    await page.context().close();
  });

  it('404 offers the palette', async () => {
    const page = await openPage(browser);
    await page.goto(`${BASE}/nowhere/at/all`);
    await page.getByRole('heading', { name: /reel isn’t in the booth/ }).waitFor();
    await page.getByRole('button', { name: /^Search/ }).click();
    await page.getByRole('combobox', { name: /Search films, people/ }).waitFor();
    await page.context().close();
  });
});
