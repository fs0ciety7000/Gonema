// Fidelity captures + accessibility audit, on the real server with the demo library.
//   node e2e/fidelity.mjs            (ONLY=name1,name2 to run some screens)
// Writes e2e/__screenshots__/<screen>-<theme>.png (the app), compare-<screen>-<theme>.png
// (app | mockup render side by side) and e2e/__screenshots__/a11y-report.json (axe-core).
// States mirror the mockups: grid with 2 selected films and the pointer on a third poster,
// table with one selected row and its context menu open, film page at rest.
// Lot 2: add film (TMDB mocked: the demo has no key), add film · discover (mocked), interactive
// search (stream mocked: the demo has no indexer), queue and history (real), and the dialogs.
import { existsSync, mkdirSync, readFileSync, writeFileSync } from 'node:fs';
import { dirname, join } from 'node:path';
import { fileURLToPath } from 'node:url';
import { api, axe, BASE, ensureServer, freeze, launch, openPage } from './helpers.mjs';
import { demoFilms, mockInteractiveSearch, mockTmdb } from './mocks.mjs';

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

let films;
const fileFilm = async () => {
  const cols = await api('/movies/ids?sort=added&order=desc');
  const i = cols.state.findIndex((st, k) => st === 'upgradable' && cols.quality?.[k]);
  return cols.id[Math.max(i, 0)];
};

SCREENS.push(
  {
    name: 'add-film',
    render: 'add-film',
    url: '/discover?q=drift',
    mock: (page) => mockTmdb(page, films),
    async prepare(page) {
      await page.locator('.res').first().waitFor();
      await settle(page);
    },
  },
  {
    name: 'add-film-discover',
    render: 'add-film-discover',
    url: '/discover?tab=discover',
    mock: (page) => mockTmdb(page, films),
    async prepare(page) {
      await page.locator('.rowp .pc').first().waitFor();
      await page.waitForTimeout(600);
      await settle(page);
    },
  },
  {
    name: 'add-film-no-key',
    url: '/discover',
    async prepare(page) {
      await page.getByRole('heading', { name: /isn’t connected yet/ }).waitFor();
      await settle(page);
    },
  },
  {
    name: 'tmdb-key-dialog',
    url: '/discover',
    async prepare(page) {
      await page.getByRole('button', { name: 'Add a TMDB key' }).click();
      await page.getByRole('dialog').waitFor();
      await settle(page);
    },
  },
  {
    name: 'interactive-search',
    render: 'interactive-search',
    url: () => `/films/${films[0].id}/search`,
    mock: (page) => mockInteractiveSearch(page, films[0]),
    async prepare(page) {
      await page.locator('tr.rel').first().waitFor();
      await page.waitForTimeout(500);
      await page
        .locator('tr[data-key="r1"]')
        .getByRole('button', { name: /Show score details/ })
        .click();
      await settle(page);
    },
  },
  {
    name: 'interactive-search-confirm',
    url: () => `/films/${films[0].id}/search`,
    mock: (page) => mockInteractiveSearch(page, films[0]),
    async prepare(page) {
      await page.locator('tr.rel').first().waitFor();
      await page.locator('tr[data-key="x1"]').getByRole('button', { name: 'Grab anyway…' }).click();
      await page.getByRole('dialog', { name: 'Grab a rejected release?' }).waitFor();
      await settle(page);
    },
  },
  {
    name: 'queue',
    render: 'queue',
    url: '/queue',
    async prepare(page) {
      await page.locator('.qi').first().waitFor();
      await settle(page);
    },
  },
  {
    name: 'queue-history',
    render: 'queue-history',
    url: '/history',
    async prepare(page) {
      await page.locator('.hist tbody tr[data-index]').first().waitFor();
      await settle(page);
    },
  },
  {
    name: 'dialog-tags',
    url: '/films',
    async prepare(page) {
      await page.locator('.pc').first().waitFor();
      await page.locator('.pc').nth(0).hover();
      await page.locator('.pc-select input').nth(0).click();
      await page
        .getByRole('toolbar', { name: /Bulk actions/ })
        .getByRole('button', { name: 'Tags' })
        .click();
      await page.getByRole('dialog').getByRole('checkbox').first().waitFor();
      await settle(page);
    },
  },
  {
    name: 'dialog-move',
    url: '/films',
    async prepare(page) {
      await page.locator('.pc').first().waitFor();
      await page.locator('.pc').nth(0).hover();
      await page.locator('.pc-select input').nth(0).click();
      await page
        .getByRole('toolbar', { name: /Bulk actions/ })
        .getByRole('button', { name: 'Move' })
        .click();
      await page.getByRole('dialog').locator('.plan').waitFor();
      await page.waitForTimeout(500);
      await settle(page);
    },
  },
  {
    name: 'dialog-delete-file',
    url: async () => `/films/${await fileFilm()}`,
    async prepare(page) {
      await page.getByRole('button', { name: 'Delete file…' }).click();
      await page.getByRole('dialog').waitFor();
      await settle(page);
    },
  },
  {
    name: 'dialog-rename',
    url: async () => `/films/${await fileFilm()}`,
    async prepare(page) {
      await page.getByRole('button', { name: 'Rename preview' }).click();
      await page.getByRole('dialog').waitFor();
      await page.waitForTimeout(500);
      await settle(page);
    },
  },
);

// ----------------------------------------------------------------- lot 3
const setupState = (dismissed) => () =>
  api('/onboarding', {
    method: 'PUT',
    headers: { 'Content-Type': 'application/json' },
    body: JSON.stringify({ dismissed }),
  });
const openPalette = async (page, q) => {
  await page.locator('.pc-link').first().waitFor();
  await page.keyboard.press('Control+k');
  const input = page.getByRole('combobox', { name: /Search films, people/ });
  await input.waitFor();
  if (q) {
    await input.fill(q);
    await page.waitForTimeout(700);
  }
  await settle(page);
};

SCREENS.push(
  {
    name: 'dashboard',
    render: 'dashboard',
    url: '/',
    before: setupState(true),
    async prepare(page) {
      await page.locator('.q-item').first().waitFor();
      await page.locator('.shelf-card').first().waitFor();
      await settle(page);
    },
  },
  {
    name: 'calendar',
    render: 'calendar',
    url: '/calendar',
    async prepare(page) {
      await page.locator('.month .ev').first().waitFor();
      await settle(page);
    },
  },
  {
    name: 'calendar-agenda',
    render: 'calendar-agenda',
    url: '/calendar?view=agenda',
    async prepare(page) {
      await page.locator('.agenda .wk li').first().waitFor();
      await settle(page);
    },
  },
  {
    name: 'command-palette',
    render: 'command-palette',
    url: '/films',
    prepare: (page) => openPalette(page),
  },
  {
    name: 'command-palette-query',
    render: 'command-palette-query',
    url: '/films',
    prepare: async (page) => openPalette(page, films[0].title.slice(0, 5)),
  },
  {
    name: 'command-palette-empty',
    render: 'command-palette-empty',
    url: '/films',
    prepare: (page) => openPalette(page, 'zebra crossing'),
  },
  {
    name: 'settings-quality',
    render: 'settings-quality',
    url: '/settings/quality',
    async prepare(page) {
      const edit = page.getByRole('button', { name: /^Edit / }).first();
      await edit.waitFor();
      await edit.click();
      await page.locator('.editor').waitFor();
      await page
        .getByRole('textbox', { name: 'Release name to test' })
        .fill('Halcyon.Drift.2024.2160p.UHD.BluRay.REMUX.DV.HDR10.TrueHD.Atmos.7.1.HEVC-BOBiNE');
      await page.locator('.verdict').waitFor();
      await page.evaluate(() => {
        document.activeElement?.blur?.();
        window.scrollTo(0, 0);
      });
      await settle(page);
    },
  },
  {
    name: 'settings-index',
    render: 'settings-index',
    url: '/settings',
    async prepare(page) {
      await page.locator('.idx .panel').first().waitFor();
      await page.waitForTimeout(500);
      await settle(page);
    },
  },
  ...['welcome', 'tmdb', 'folder', 'quality'].map((step) => ({
    name: `onboarding-${step}`,
    render: `onboarding-${step}`,
    url: `/setup?step=${step}`,
    before: setupState(false),
    async prepare(page) {
      await page.locator('.pane h1').waitFor();
      await page.waitForTimeout(400);
      await settle(page);
    },
  })),
  {
    name: 'onboarding-client-error',
    render: 'onboarding-client-error',
    url: '/setup?step=client',
    before: setupState(false),
    // the demo has no reachable client: the connection test answer is mocked
    mock: (page) =>
      page.route('**/api/v1/download-clients/test**', (r) =>
        r.fulfill({
          status: 200,
          contentType: 'application/json',
          body: JSON.stringify({
            ok: false,
            message: 'qBittorrent answered, but refused the login (HTTP 200 “Fails.” on sign-in).',
          }),
        }),
      ),
    async prepare(page) {
      await page.getByRole('textbox', { name: 'Host' }).fill('qbittorrent');
      await page.getByRole('textbox', { name: 'Username' }).fill('admin');
      await page.getByLabel('Password').fill('adminadmin');
      await page.getByRole('button', { name: /Test and continue/ }).click();
      await page.locator('.fix').waitFor();
      await settle(page);
    },
  },
  {
    name: 'onboarding-film',
    url: '/setup?step=film',
    before: setupState(false),
    async prepare(page) {
      await page.locator('.pane h1').waitFor();
      await settle(page);
    },
  },
  {
    name: 'failed-download',
    render: 'failed-download',
    url: async () => `/queue/${(await failedEntry()).id}`,
    // the demo queue has no failed download: one real entry is shown as failed (retry 2 of 6)
    mock: async (page) => {
      const f = await failedEntry();
      await page.route('**/api/v1/queue', async (r) => {
        const list = await (await r.fetch()).json();
        r.fulfill({
          status: 200,
          contentType: 'application/json',
          body: JSON.stringify(list.map((q) => (q.id === f.id ? f : q))),
        });
      });
    },
    async prepare(page) {
      await page.getByRole('heading', { name: 'The print arrived damaged.' }).waitFor();
      await page.waitForTimeout(500);
      await settle(page);
    },
  },
  {
    name: 'wanted',
    url: '/wanted',
    async prepare(page) {
      await page.locator('.wanted tbody tr').first().waitFor();
      await settle(page);
    },
  },
  {
    name: 'blocklist',
    url: '/blocklist',
    async prepare(page) {
      await page.locator('.hist, .empty').first().waitFor();
      await settle(page);
    },
  },
  {
    name: 'health',
    url: '/system/health',
    async prepare(page) {
      await page.locator('.issue').first().waitFor();
      await settle(page);
    },
  },
  {
    name: 'settings-indexer-dialog',
    url: '/settings/indexers',
    async prepare(page) {
      await page.getByRole('button', { name: 'Add indexer' }).click();
      await page.getByRole('dialog').waitFor();
      await settle(page);
    },
  },
);

let failed;
async function failedEntry() {
  if (failed) return failed;
  const q = (await api('/queue')).find((x) => x.status === 'downloading') ?? (await api('/queue'))[0];
  failed = {
    ...q,
    status: 'failed',
    failedReason: 'SABnzbd: archive is password-protected',
    completedAt: new Date(Date.now() - 3 * 3_600_000).toISOString(),
    messages: [
      `SABnzbd  Download finished: ${q.title}`,
      'SABnzbd  Verifying… OK',
      'SABnzbd  Unpacking failed: archive is encrypted, password required',
      'Gonema   Blocklisted release (reason: encrypted archive), removed it from SABnzbd',
    ],
    retry: {
      attempt: 2,
      of: 6,
      previousDownloadId: 0,
      previousReason: 'archive is password-protected',
      previousRelease: q.title,
    },
    actions: { pause: false, resume: false, remove: true, retryImport: false },
  };
  return failed;
}

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
  films = await demoFilms(api);
  const only = process.env.ONLY?.split(',');
  for (const s of SCREENS) {
    if (only && !only.includes(s.name)) continue;
    for (const theme of ['dark', 'light']) {
      const page = await openPage(browser, { theme, width: s.width ?? 1440, height: s.height ?? 900, bypassCSP: true });
      if (s.before) await s.before();
      if (s.mock) await s.mock(page);
      const path = typeof s.url === 'function' ? await s.url() : s.url;
      await page.goto(`${BASE}${path}`);
      await s.prepare(page);
      const file = join(out, `${s.name}-${theme}.png`);
      await page.screenshot({ path: file });
      const violations = await axe(page);
      report.screens.push({ screen: s.name, theme, url: path, violations, errors: page.errors });
      const render = join(renders, `${s.render}-${theme}.jpg`);
      if (s.render && existsSync(render) && !s.width)
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
    const r404 = join(renders, `404-${theme}.jpg`);
    if (existsSync(r404))
      await compare(browser, join(out, `404-${theme}.png`), r404, join(out, `compare-404-${theme}.png`));
    const violations = await axe(page);
    report.screens.push({ screen: '404', theme, violations, errors: page.errors.filter((e) => !/404/.test(e)) });
    console.log(`404-${theme}: ${violations.length} axe violation(s)`);
    await page.context().close();
  }
  await setupState(true)().catch(() => {});
  if (!only) writeFileSync(join(out, 'a11y-report.json'), `${JSON.stringify(report, null, 2)}\n`);
  else writeFileSync(join(out, 'a11y-report.partial.json'), `${JSON.stringify(report, null, 2)}\n`);
  await browser.close();
}

run().catch((e) => {
  console.error(e);
  process.exit(1);
});
