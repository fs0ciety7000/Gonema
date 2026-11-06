// Fidelity captures + accessibility audit, on the real server with the demo library.
//   node e2e/fidelity.mjs
// Writes e2e/__screenshots__/<screen>-<theme>.png (the app), compare-<screen>-<theme>.png
// (app | mockup render side by side) and e2e/__screenshots__/a11y-report.json (axe-core).
// States mirror the mockups: grid with 2 selected films and the pointer on a third poster,
// table with one selected row and its context menu open, film page at rest.
import { existsSync, mkdirSync, readFileSync, writeFileSync } from 'node:fs';
import { dirname, join } from 'node:path';
import { fileURLToPath } from 'node:url';
import { api, axe, BASE, ensureServer, freeze, launch, openPage } from './helpers.mjs';

const here = dirname(fileURLToPath(import.meta.url));
const out = join(here, '__screenshots__');
const renders = join(here, '..', '..', 'docs', 'design', 'renders');
mkdirSync(out, { recursive: true });

async function settle(page) {
  await page.waitForTimeout(900);
  await freeze(page);
  await page.mouse.move(1, 1);
}

const SCREENS = [
  {
    name: 'library-grid',
    render: 'library-grid',
    url: '/films',
    async prepare(page) {
      await page.locator('.pc-link').first().waitFor();
      await page.waitForTimeout(800);
      const boxes = page.locator('.pc-select input');
      await page.locator('.pc').nth(0).hover();
      await boxes.nth(0).click();
      await page.locator('.pc').nth(6).hover();
      await boxes.nth(6).click();
      await page.waitForTimeout(400);
      await freeze(page);
      await page.locator('.pc').nth(2).hover();
      await page.waitForTimeout(450);
    },
  },
  {
    name: 'library-grid-tablet',
    render: 'library-grid-tablet',
    url: '/films',
    width: 1024,
    height: 768,
    async prepare(page) {
      await page.locator('.pc-link').first().waitFor();
      await settle(page);
    },
  },
  {
    name: 'library-table',
    render: 'library-table',
    url: '/films?layout=table',
    async prepare(page) {
      const rows = page.locator('tbody tr[data-movie-id]');
      await rows.first().waitFor();
      await page.waitForTimeout(500);
      await rows.nth(2).locator('.c-check input').click();
      await rows.nth(2).click({ button: 'right', position: { x: 900, y: 14 } });
      await page.locator('.ctx-menu').waitFor();
      await page.waitForTimeout(350);
      await freeze(page);
    },
  },
  {
    name: 'movie-detail',
    render: 'movie-detail',
    async url() {
      // a film with a file, history and the richest page: the first upgradable one with a collection if any
      const cols = await api('/movies/ids?sort=added&order=desc');
      const ids = cols.id.filter((_, i) => cols.state[i] === 'upgradable').slice(0, 40);
      for (const id of ids) {
        const m = await api(`/movies/${id}`);
        if (m.collection?.id && m.file?.id) return `/films/${id}`;
      }
      return `/films/${ids[0] ?? cols.id[0]}`;
    },
    async prepare(page) {
      await page.locator('.hero h1').waitFor();
      await page.locator('.panel').first().waitFor();
      await settle(page);
    },
  },
];

async function compare(browser, app, render, file) {
  const page = await browser.newPage({ viewport: { width: 2 * 1440 + 24, height: 960 } });
  const toUrl = (p) => `data:image/${p.endsWith('.png') ? 'png' : 'jpeg'};base64,${readFileSync(p).toString('base64')}`;
  await page.setContent(`<!doctype html><body style="margin:0;background:#222;display:flex;gap:24px;font:600 14px system-ui;color:#ddd">
    <figure style="margin:0;width:1440px"><figcaption style="height:30px;padding:6px">App (${BASE})</figcaption><img src="${toUrl(app)}" style="width:1440px"></figure>
    <figure style="margin:0;width:1440px"><figcaption style="height:30px;padding:6px">Mockup render (${render.split('/').pop()})</figcaption><img src="${toUrl(render)}" style="width:1440px"></figure></body>`);
  await page.waitForTimeout(300);
  await page.screenshot({ path: file, fullPage: true });
  await page.close();
}

async function run() {
  await ensureServer();
  const browser = await launch();
  const report = {
    base: BASE,
    date: new Date().toISOString(),
    tags: 'wcag2a, wcag2aa, wcag21a, wcag21aa, best-practice',
    screens: [],
  };
  for (const s of SCREENS) {
    for (const theme of ['dark', 'light']) {
      const page = await openPage(browser, { theme, width: s.width ?? 1440, height: s.height ?? 900, bypassCSP: true });
      const path = typeof s.url === 'function' ? await s.url() : s.url;
      await page.goto(`${BASE}${path}`);
      await s.prepare(page);
      const file = join(out, `${s.name}-${theme}.png`);
      await page.screenshot({ path: file });
      const violations = await axe(page);
      report.screens.push({ screen: s.name, theme, url: path, violations, errors: page.errors });
      const render = join(renders, `${s.render}-${theme}.jpg`);
      if (existsSync(render) && !s.width)
        await compare(browser, file, render, join(out, `compare-${s.name}-${theme}.png`));
      console.log(
        `${s.name}-${theme}: ${violations.length} axe violation(s)${violations.length ? ` ${violations.map((v) => v.id).join(', ')}` : ''}`,
      );
      await page.context().close();
    }
  }
  // the dev-free production build has no /__system; the error and 404 screens are audited too
  for (const theme of ['dark', 'light']) {
    const page = await openPage(browser, { theme, bypassCSP: true });
    await page.goto(`${BASE}/films/999999999`);
    await page.getByRole('heading', { name: /reel isn’t in the booth/ }).waitFor();
    await settle(page);
    await page.screenshot({ path: join(out, `404-${theme}.png`) });
    const violations = await axe(page);
    report.screens.push({ screen: '404', theme, violations, errors: page.errors.filter((e) => !/404/.test(e)) });
    console.log(`404-${theme}: ${violations.length} axe violation(s)`);
    await page.context().close();
  }
  writeFileSync(join(out, 'a11y-report.json'), `${JSON.stringify(report, null, 2)}\n`);
  await browser.close();
}

run().catch((e) => {
  console.error(e);
  process.exit(1);
});
