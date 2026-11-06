// End-to-end flows on the real server + demo library: open the library, filter,
// select, open a film, toggle monitored and undo.
import assert from 'node:assert/strict';
import { after, before, describe, it } from 'node:test';
import { api, BASE, ensureServer, launch, openPage } from './helpers.mjs';

describe('library and film page', () => {
  let browser;
  let page;
  before(async () => {
    await ensureServer();
    browser = await launch();
    page = await openPage(browser);
  });
  after(async () => {
    await browser?.close();
  });

  it('opens the library with the whole demo collection', async () => {
    await page.goto(`${BASE}/films`);
    await page.getByRole('heading', { level: 1, name: 'Films' }).waitFor();
    const grid = page.getByRole('grid', { name: /Films/ });
    await grid.waitFor();
    await page.locator('.pc-link').first().waitFor();
    const sub = await page.locator('.page-head .sub').textContent();
    assert.match(sub, /10,000 films/);
    assert.equal(await page.getByText('Demo mode.').count(), 1, 'demo banner is shown');
    assert.ok((await page.locator('.pc').count()) < 200, 'the grid is virtualised');
  });

  it('filters by status from the Filters popover', async () => {
    // the real popover trigger replaces its placeholder once the chunk is idle-loaded
    await page.locator('button[aria-haspopup="dialog"][aria-expanded]', { hasText: 'Filters' }).click();
    const missing = page.getByRole('dialog', { name: 'Filters' }).getByRole('button', { name: 'Missing' });
    await missing.click();
    await page.keyboard.press('Escape');
    await page.waitForFunction(() => decodeURIComponent(location.search).includes('missing'));
    await page.getByRole('group', { name: 'Active filters' }).getByText('Missing').waitFor();
    const states = await page.locator('.pc').evaluateAll((els) => els.map((e) => e.getAttribute('data-state')));
    assert.ok(states.length > 0);
    assert.ok(
      states.every((s) => s === 'missing'),
      `only missing films: ${[...new Set(states)]}`,
    );
    await page.getByRole('button', { name: 'Clear all' }).click();
    await page.waitForFunction(() => !location.search.includes('status='));
  });

  it('selects with click and shift+click, then clears with Escape', async () => {
    const boxes = page.locator('.pc-select input');
    await page.locator('.pc').first().hover();
    await boxes.nth(0).click();
    await page.locator('.pc').nth(2).hover();
    await boxes.nth(2).click({ modifiers: ['Shift'] });
    const bar = page.getByRole('toolbar', { name: /Bulk actions for 3 selected films/ });
    await bar.waitFor();
    assert.equal(await page.locator('.pc.is-selected').count(), 3);
    await page.keyboard.press('Escape');
    await bar.waitFor({ state: 'detached' });
  });

  it('moves between posters with the arrow keys', async () => {
    const first = page.locator('.pc-link').first();
    await first.focus();
    await page.keyboard.press('ArrowRight');
    const label = await page.evaluate(() => document.activeElement?.getAttribute('aria-label'));
    const second = await page.locator('.pc-link').nth(1).getAttribute('aria-label');
    assert.equal(label, second);
  });

  let movieId;
  let title;
  it('opens a film from its poster', async () => {
    const link = page.locator('.pc-link').first();
    title = (await link.getAttribute('aria-label')).replace(/ \(\d{4}\)$/, '');
    await link.click();
    await page.waitForURL(/\/films\/\d+$/);
    movieId = Number(new URL(page.url()).pathname.split('/').pop());
    await page.getByRole('heading', { level: 1, name: title }).waitFor();
    await page.getByRole('switch', { name: 'Monitored' }).first().waitFor();
  });

  it('toggles monitored optimistically and undoes it', async () => {
    const before = (await api(`/movies/${movieId}`)).monitored;
    const sw = page.locator('.hero .switch');
    assert.equal(await sw.getAttribute('aria-checked'), String(before));
    await sw.click();
    // optimistic: flipped before the PATCH returns (within a frame or two)
    await page.waitForFunction(
      ([want]) => document.querySelector('.hero .switch')?.getAttribute('aria-checked') === want,
      [String(!before)],
      { timeout: 300 },
    );
    const toastUndo = page.getByRole('status').filter({ hasText: title }).getByRole('button', { name: 'Undo' });
    await toastUndo.waitFor();
    await page.waitForFunction(
      async ([id, want]) => (await (await fetch(`api/v1/movies/${id}`)).json()).monitored === want,
      [movieId, !before],
    );
    await toastUndo.click();
    await page.waitForFunction(
      async ([id, want]) => (await (await fetch(`api/v1/movies/${id}`)).json()).monitored === want,
      [movieId, before],
    );
    await page.waitForFunction(
      ([want]) => document.querySelector('.hero .switch')?.getAttribute('aria-checked') === want,
      [String(before)],
    );
  });

  it('J / K walk the list without leaving the page', async () => {
    await page.locator('body').click({ position: { x: 5, y: 600 } });
    await page.keyboard.press('j');
    await page.waitForURL((u) => u.pathname !== `/films/${movieId}`);
    assert.match(page.url(), /\/films\/\d+$/);
    await page.keyboard.press('k');
    await page.waitForURL((u) => u.pathname === `/films/${movieId}`);
  });

  it('has no console errors', () => {
    assert.deepEqual(page.errors, []);
  });
});
