// The whole first run, on a server with a FRESH data directory (see e2e/README.md):
//   gonema serve -data-dir /tmp/gonema-journey -bind 127.0.0.1:7678
// GONEMA_E2E_JOURNEY_URL points at it (default http://127.0.0.1:7678).
// welcome → account (API key once) → TMDB → library folder → quality → indexer → client → first film.
//
// Mocked with page.route, because they need the outside world (and only those):
//   - PUT/GET /settings/tmdb: TMDB validates the key over the network;
//   - GET /lookup and POST /movies: they ask TMDB for the film;
//   - POST /indexers/test and POST /download-clients/test: no indexer or client is reachable.
// Real: account creation, the root folder (a new directory), the quality profile choice, the
// indexer and client records (saved by the server), onboarding progress and the library.
import assert from 'node:assert/strict';
import { mkdtempSync } from 'node:fs';
import { tmpdir } from 'node:os';
import { join } from 'node:path';
import { after, before, describe, it } from 'node:test';
import { launch } from '../helpers.mjs';

const BASE = (process.env.GONEMA_E2E_JOURNEY_URL ?? 'http://127.0.0.1:7678').replace(/\/$/, '');
const json = (body, status = 200) => ({ status, contentType: 'application/json', body: JSON.stringify(body) });
const FILM = { tmdbId: 990_001, title: 'Continental Drift', year: 1998 };

describe('first run: from an empty server to the first film', () => {
  let browser;
  let page;
  const errors = [];
  const calls = [];
  let tmdbSaved = false;
  before(async () => {
    const s = await (await fetch(`${BASE}/api/v1/auth/status`)).json().catch(() => null);
    if (!s?.setupRequired)
      throw new Error(`${BASE} must be a Gonema server on a fresh data directory (got ${JSON.stringify(s)}).`);
    browser = await launch();
    const ctx = await browser.newContext({ viewport: { width: 1440, height: 900 }, deviceScaleFactor: 1 });
    page = await ctx.newPage();
    page.on('pageerror', (e) => errors.push(e.message));
    await page.route('**/api/v1/settings/tmdb', (r) => {
      calls.push(`${r.request().method()} /settings/tmdb`);
      if (r.request().method() === 'PUT') {
        tmdbSaved = true;
        return r.fulfill(json({ configured: true }));
      }
      return r.fulfill(json({ configured: tmdbSaved }));
    });
    await page.route('**/api/v1/lookup?**', (r) => {
      calls.push('GET /lookup');
      return r.fulfill(
        json([
          {
            ...FILM,
            releaseDate: '1998-04-03',
            overview: 'A seismologist returns to the island town her father mapped.',
            originalLanguage: 'en',
            rating: { value: 7.1, votes: 120 },
            posterPath: '',
            posterUrl: null,
            inLibrary: false,
            monitored: false,
          },
        ]),
      );
    });
    await page.route('**/api/v1/movies', (r) => {
      if (r.request().method() !== 'POST') return r.fallback();
      calls.push('POST /movies');
      const body = r.request().postDataJSON();
      return r.fulfill(json({ id: 999_999, ...FILM, monitored: body.monitored, collection: null }, 201));
    });
    for (const p of ['indexers', 'download-clients'])
      await page.route(`**/api/v1/${p}/test**`, (r) => {
        calls.push(`POST /${p}/test`);
        return r.fulfill(json({ ok: true, message: 'Answered in 42 ms (mocked).' }));
      });
  });
  after(async () => {
    await browser?.close();
  });

  it('welcome, then the account with its API key', async () => {
    await page.goto(`${BASE}/`);
    await page.waitForURL(/\/setup/);
    await page.getByRole('button', { name: /Let’s begin/ }).click();
    await page.getByLabel('Username').fill('admin');
    await page.getByLabel('Password', { exact: true }).fill('a long and quiet passphrase');
    await page.getByRole('button', { name: /Create the account/ }).click();
    await page.getByRole('textbox', { name: 'API key' }).waitFor();
    await page.getByRole('button', { name: /Continue/ }).click();
  });

  it('TMDB key (validation mocked)', async () => {
    await page.getByRole('heading', { name: 'Connect film metadata' }).waitFor();
    await page.getByLabel('API Read Access Token').fill('eyJhbGciOiJIUzI1NiJ9.mocked-read-access-token');
    await page.getByRole('button', { name: /Check and continue/ }).click();
    await page.getByText('Key works.').waitFor();
    await page.getByRole('button', { name: /^Continue/ }).click();
  });

  it('library folder (a real new directory)', async () => {
    await page.getByRole('heading', { name: /where/i, level: 1 }).waitFor();
    const dir = mkdtempSync(join(tmpdir(), 'gonema-journey-movies-'));
    await page.getByLabel('Root folder').fill(dir);
    await page.getByRole('button', { name: /Add and continue/ }).click();
    await page.getByText(dir).first().waitFor();
    await page.getByRole('button', { name: /^Continue/ }).click();
  });

  it('quality profile', async () => {
    await page.getByRole('heading', { name: 'How should your films look?' }).waitFor();
    await page.locator('.choice label').first().click();
    await page.getByRole('button', { name: /^Continue/ }).click();
  });

  it('indexer (connection test mocked, record saved by the server)', async () => {
    await page.getByRole('heading', { name: 'Add an indexer' }).waitFor();
    await page.getByLabel('API URL').fill('https://indexer.example/api');
    await page.getByLabel('API key').fill('0123456789abcdef');
    await page.getByRole('button', { name: /Test and add/ }).click();
    await page.getByRole('button', { name: /^Continue/ }).waitFor();
    await page.getByRole('button', { name: /^Continue/ }).click();
  });

  it('download client (connection test mocked, record saved by the server)', async () => {
    await page.getByRole('heading', { name: 'Connect your download client' }).waitFor();
    await page.getByRole('group', { name: 'Client type' }).getByRole('button', { name: 'SABnzbd' }).click();
    await page.getByLabel('Host').fill('127.0.0.1');
    const key = page.getByLabel('API key');
    if (await key.count()) await key.fill('0123456789abcdef0123456789abcdef');
    await page.getByRole('button', { name: /Test and continue/ }).click();
  });

  it('first film: search, add, and the setup is done', async () => {
    await page.getByRole('heading', { level: 1, name: /first film|First screening|in the programme/i }).waitFor();
    await page
      .getByRole('searchbox')
      .or(page.getByLabel(/Film title|Search TMDB|Title/))
      .first()
      .fill('Continental');
    await page.getByRole('button', { name: 'Search', exact: true }).click();
    await page.locator('.search-hit b', { hasText: FILM.title }).waitFor();
    await page.getByRole('button', { name: 'Add and search' }).click();
    await page.getByRole('heading', { name: /in the programme|on its way/ }).waitFor();
    const onboarding = await page.evaluate(async () => (await fetch('api/v1/onboarding')).json());
    const done = Object.fromEntries(onboarding.steps.map((s) => [s.id, s.done]));
    // real server state: account, folder, quality, indexer and client are done (TMDB and the film were mocked)
    assert.deepEqual(
      {
        account: done.account,
        root_folder: done.root_folder,
        indexer: done.indexer,
        download_client: done.download_client,
      },
      { account: true, root_folder: true, indexer: true, download_client: true },
    );
    assert.deepEqual([...new Set(calls)].sort(), [
      'GET /lookup',
      'GET /settings/tmdb',
      'POST /download-clients/test',
      'POST /indexers/test',
      'POST /movies',
      'PUT /settings/tmdb',
    ]);
    assert.deepEqual(errors, []);
  });
});
