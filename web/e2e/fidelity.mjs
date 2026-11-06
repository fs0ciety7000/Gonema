// Fidelity captures + accessibility audit, on the real server with the demo library.
//   node e2e/fidelity.mjs            (ONLY=name1,name2 to run some screens)
// Writes e2e/__screenshots__/<screen>-<theme>.png (the app), compare-<screen>-<theme>.png
// (app | mockup render side by side) and e2e/__screenshots__/a11y-report.json (axe-core).
// States mirror the mockups: grid with 2 selected films and the pointer on a third poster,
// table with one selected row and its context menu open, film page at rest.
// Lot 2: add film (TMDB mocked: the demo has no key), add film · discover (mocked), interactive
// search (stream mocked: the demo has no indexer), queue and history (real), and the dialogs.
import { existsSync, mkdirSync, mkdtempSync, readFileSync, rmSync, truncateSync, writeFileSync } from 'node:fs';
import { tmpdir } from 'node:os';
import { dirname, join } from 'node:path';
import { fileURLToPath } from 'node:url';
import { api, axe, BASE, ensureServer, freeze, launch, openPage } from './helpers.mjs';
import {
  demoFilms,
  IMPORT_SCAN_ID,
  importScanFolders,
  mockInteractiveSearch,
  mockLibraryImport,
  mockTmdb,
} from './mocks.mjs';

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
  ...['welcome', 'account', 'tmdb', 'folder', 'quality'].map((step) => ({
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
  // Phase 8. The demo runs with GONEMA_AUTH=disabled_for_local (no account): the sign-in page is shown
  // by answering GET /auth/status as signed out, and its error states by answering POST /auth/login.
  {
    name: 'login',
    render: 'login',
    url: '/login?redirect=%2Ffilms',
    mock: (page) => mockSignedOut(page),
    async prepare(page) {
      await page.getByRole('heading', { name: 'Back to the booth' }).waitFor();
      await settle(page);
    },
  },
  {
    name: 'login-error',
    render: 'login-error',
    url: '/login',
    mock: (page) => mockSignedOut(page, 401),
    async prepare(page) {
      await page.getByLabel('Username').fill('margaux');
      await page.getByLabel('Password', { exact: true }).fill('not the password');
      await page.getByRole('button', { name: 'Sign in' }).click();
      await page.getByRole('alert').waitFor();
      await settle(page);
    },
  },
  {
    name: 'login-throttled',
    render: 'login-throttled',
    url: '/login',
    mock: (page) => mockSignedOut(page, 429),
    async prepare(page) {
      await page.getByLabel('Username').fill('margaux');
      await page.getByLabel('Password', { exact: true }).fill('not the password');
      await page.getByRole('button', { name: 'Sign in' }).click();
      await page.getByText('Sign-in is paused for a few minutes.').waitFor();
      await settle(page);
    },
  },
  {
    // the mockup shows the setup token (a browser on another machine): GET /auth/status says so
    name: 'onboarding-account',
    render: 'onboarding-account',
    url: '/setup?step=account',
    before: setupState(false),
    mock: (page) => mockSetup(page),
    async prepare(page) {
      await page.getByRole('heading', { name: 'Secure the booth' }).waitFor();
      await page.getByLabel('Setup token').fill('7KQ2-MV9D-XR4T-PL8C-2WNA-F6HJ');
      await page.getByLabel('Username').fill('margaux');
      await page.getByLabel('Password', { exact: true }).fill('correct-horse-battery');
      await page.evaluate(() => document.activeElement?.blur?.());
      await settle(page);
    },
  },
  {
    // account made: POST /auth/setup is answered (the demo keeps no account), the key is shown once
    name: 'onboarding-account-key',
    render: 'onboarding-account-key',
    url: '/setup?step=account',
    before: setupState(false),
    mock: (page) => mockSetup(page, true),
    async prepare(page) {
      await page.getByLabel('Setup token').fill('7KQ2-MV9D-XR4T-PL8C-2WNA-F6HJ');
      await page.getByLabel('Username').fill('margaux');
      await page.getByLabel('Password', { exact: true }).fill('correct-horse-battery');
      await page.getByRole('button', { name: /Create the account/ }).click();
      await page.getByRole('textbox', { name: 'API key' }).waitFor();
      await page.evaluate(() => document.activeElement?.blur?.());
      await settle(page);
    },
  },
  {
    // local mode, no account: the real answer
    name: 'settings-security',
    url: '/settings/security',
    async prepare(page) {
      await page.getByRole('heading', { name: 'Outbound connections' }).waitFor();
      await settle(page);
    },
  },
  {
    // signed-in state (the local demo has no account): the real answer, with an account, sessions and a key
    name: 'settings-security-account',
    render: 'settings-security',
    url: '/settings/security',
    mock: (page) => mockSecurityAccount(page),
    async prepare(page) {
      await page.getByText('This browser').waitFor();
      await settle(page);
    },
  },
  {
    name: 'settings-security-apikey',
    render: 'settings-security-apikey',
    url: '/settings/security',
    mock: (page) => mockSecurityAccount(page),
    async prepare(page) {
      await page.getByRole('button', { name: 'Regenerate…' }).click();
      await page.getByRole('dialog', { name: 'Make a new API key?' }).waitFor();
      await settle(page);
    },
  },
  {
    // real archives (made here, one with the master key) and a real staged restore, canceled after
    name: 'settings-backups',
    render: 'settings-backups',
    url: '/settings/backups',
    before: async () => {
      const list = await demoBackups();
      await api('/backups/restore', {
        method: 'POST',
        headers: { 'Content-Type': 'application/json' },
        body: JSON.stringify({ name: list.at(-1).name, confirm: true }),
      });
    },
    after: () => api('/backups/restore', { method: 'DELETE' }).catch(() => {}),
    async prepare(page) {
      await page.locator('.bk tbody tr').first().waitFor();
      await page.locator('.pending').waitFor();
      await settle(page);
    },
  },
  {
    name: 'settings-backups-restore',
    render: 'settings-backups-restore',
    url: '/settings/backups',
    before: async () => {
      await demoBackups();
      await api('/backups/restore', { method: 'DELETE' }).catch(() => {});
    },
    async prepare(page) {
      await page.locator('.bk tbody tr').first().waitFor();
      await page
        .getByRole('button', { name: /^Restore .*…$/ })
        .last()
        .click();
      await page.getByRole('dialog', { name: /^Restore the backup of/ }).waitFor();
      await settle(page);
    },
  },
  {
    // live log, one entry expanded (as the mockup)
    name: 'settings-logs',
    render: 'settings-logs',
    url: '/settings/logs',
    async prepare(page) {
      await page.locator('.ln').first().waitFor();
      // the demo's info lines are mostly HTTP requests: open the newest warning the buffer still holds
      // (found by its message), else the last line
      const warn = ((await api('/logs?level=warn&limit=50')).entries ?? []).at(-1);
      if (warn) {
        await page.getByRole('searchbox', { name: 'Search the logs' }).fill(warn.message);
        await page.waitForTimeout(800);
        await page.locator('.ln[data-lv="warn"]').last().click();
      } else await page.locator('.ln').last().click();
      await page.locator('.log-detail').waitFor();
      await page.mouse.move(1, 1);
      await settle(page);
    },
  },
  {
    name: 'settings-about',
    url: '/settings/about',
    async prepare(page) {
      await page.getByRole('heading', { name: 'License and credits' }).waitFor();
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

// ------------------------------------------------------------------ v1.1: collections and Import a folder

/** A real folder of films for the import captures (sparse video files), made once and removed at the end. */
let importRoot;
const IMPORT_FOLDERS = [
  'Drift',
  'home videos',
  'Kessel',
  'Concrete Psalms (2017) [1080p]',
  'Continental Drift 1998',
  'fathom.2021.2160p.web-dl.hdr',
  'Ghost Frequency 2023 MULTi 2160p',
  'marrow',
  'Snowdrift.2011.DVDRip',
  'The Drifters of Calder Bay 2005',
  'Wolves of the Lower Sky (2018)',
  'Halcyon Drift (2024)',
];
async function makeImportRoot() {
  if (importRoot) return importRoot;
  const dir = mkdtempSync(join(tmpdir(), 'gonema-fidelity-'));
  for (const name of IMPORT_FOLDERS) {
    mkdirSync(join(dir, name));
    const f = join(dir, name, `${name.replace(/[^\w]+/g, '.')}.mkv`);
    writeFileSync(f, '');
    truncateSync(f, 900 * 1024 * 1024);
  }
  const root = await api('/root-folders', {
    method: 'POST',
    headers: { 'Content-Type': 'application/json' },
    body: JSON.stringify({ path: dir }),
  });
  importRoot = { dir, root };
  return importRoot;
}
async function dropImportRoot() {
  if (!importRoot) return;
  await api(`/root-folders/${importRoot.root.id}`, { method: 'DELETE' }).catch(() => {});
  rmSync(importRoot.dir, { recursive: true, force: true });
  importRoot = undefined;
}
const importMock =
  (opts = {}) =>
  async (page) => {
    const { dir, root } = await makeImportRoot();
    await mockLibraryImport(page, {
      rootFolderId: root.id,
      folders: importScanFolders(dir, IMPORT_FOLDERS, films),
      perMs: 1,
      ...opts,
    });
  };
/** The collection with the most varied films (states), for the detail capture. */
async function richCollection() {
  const list = (await api('/collections?pageSize=200&sort=missing')).collections;
  const score = (c) => new Set(c.films.map((f) => f.state)).size * 100 - Math.abs(c.films.length - 6);
  return list.sort((a, b) => score(b) - score(a))[0];
}

SCREENS.push(
  {
    name: 'collections',
    render: 'collections',
    url: '/collections',
    async prepare(page) {
      await page.locator('.cc-link').first().waitFor();
      await page.waitForTimeout(700);
      await page.locator('.cc').nth(5).hover();
      await page.waitForTimeout(400);
      await freeze(page);
    },
  },
  {
    name: 'collections-detail',
    render: 'collections-detail',
    url: async () => `/collections?id=${(await richCollection()).id}`,
    async prepare(page) {
      await page.locator('.parts > li').first().waitFor();
      await settle(page);
    },
  },
  {
    name: 'collections-empty',
    render: 'collections-empty',
    url: '/collections',
    mock: (page) =>
      page.route('**/api/v1/collections?**', (r) =>
        r.fulfill(
          json({
            collections: [],
            page: 1,
            pageSize: 60,
            total: 0,
            totals: { collections: 0, films: 0, monitored: 0, notInLibrary: 0, upcoming: 0, withMissing: 0 },
          }),
        ),
      ),
    async prepare(page) {
      await page.getByRole('heading', { name: 'No sagas on the bill yet.' }).waitFor();
      await settle(page);
    },
  },
  {
    name: 'library-import-folder',
    render: 'library-import-folder',
    url: '/discover?tab=import',
    mock: importMock(),
    async prepare(page) {
      const { dir } = await makeImportRoot();
      await page.locator('.roots label').filter({ hasText: dir }).click();
      await page
        .getByRole('list', { name: `Folders in ${dir}` })
        .getByRole('button')
        .first()
        .waitFor();
      await page.mouse.move(1, 1);
      await settle(page);
    },
  },
  {
    name: 'library-import-scan',
    render: 'library-import-scan',
    url: `/discover?tab=import&scan=${IMPORT_SCAN_ID}`,
    mock: importMock({ perMs: 1, hold: 8 }),
    async prepare(page) {
      await page.locator('.feed li:not(.reading)').nth(6).waitFor();
      await settle(page);
    },
  },
  {
    name: 'library-import-review',
    render: 'library-import-review',
    url: `/discover?tab=import&scan=${IMPORT_SCAN_ID}`,
    mock: importMock(),
    async prepare(page) {
      await page.locator('.imp tbody tr').first().waitFor();
      await page.mouse.move(1, 1);
      await settle(page);
    },
  },
  {
    name: 'library-import-review-tablet',
    render: 'library-import-review-tablet',
    url: `/discover?tab=import&scan=${IMPORT_SCAN_ID}`,
    width: 1024,
    height: 768,
    mock: importMock(),
    async prepare(page) {
      await page.locator('.imp tbody tr').first().waitFor();
      await settle(page);
    },
  },
  {
    name: 'library-import-match',
    render: 'library-import-match',
    url: `/discover?tab=import&scan=${IMPORT_SCAN_ID}`,
    mock: importMock(),
    async prepare(page) {
      await page.getByRole('button', { name: 'Change match for Drift' }).click();
      await page.getByRole('option').first().waitFor();
      await page.waitForTimeout(400);
      await freeze(page);
    },
  },
  {
    name: 'library-import-done',
    render: 'library-import-done',
    url: `/discover?tab=import&scan=${IMPORT_SCAN_ID}`,
    mock: importMock(),
    async prepare(page) {
      await page.getByRole('button', { name: /^Import \d+ films?$/ }).click();
      await page.getByRole('heading', { name: /in the house\./ }).waitFor();
      await settle(page);
    },
  },
);

const json = (body, status = 200) => ({
  status,
  contentType: status >= 400 ? 'application/problem+json' : 'application/json',
  body: JSON.stringify(body),
});

/** Sign-in captures: the server answers as signed out; POST /auth/login fails with `fail` (401 or 429). */
async function mockSignedOut(page, fail) {
  await page.route('**/api/v1/auth/status', (r) =>
    r.fulfill(json({ setupRequired: false, authenticated: false, method: 'none', authMode: 'enabled' })),
  );
  if (fail)
    await page.route('**/api/v1/auth/login', (r) =>
      r.fulfill({
        ...json(
          { status: fail, detail: fail === 429 ? 'Too many failed attempts.' : 'Wrong username or password.' },
          fail,
        ),
        headers: fail === 429 ? { 'Retry-After': '272' } : {},
      }),
    );
}

/** Account step: GET /auth/status as seen from another machine (setup token asked); with `created`,
 *  POST /auth/setup answers as the server does (a fictional key: the demo keeps no account). */
async function mockSetup(page, created = false) {
  await page.route('**/api/v1/auth/status', (r) =>
    r.fulfill(
      json({
        setupRequired: true,
        authenticated: false,
        method: 'none',
        authMode: 'enabled',
        setupTokenRequired: true,
      }),
    ),
  );
  if (created)
    await page.route('**/api/v1/auth/setup', (r) =>
      r.fulfill(
        json({
          apiKey: 'gnm_q7Lx2vR9mT4wKc8ZpN3aHd6YsE1uJb5fG0i-OkV3f9a',
          apiKeyLast4: '3f9a',
          expiresAt: new Date(Date.now() + 30 * 86_400_000).toISOString(),
          user: { id: 1, username: 'margaux', createdAt: new Date().toISOString(), lastLoginAt: null },
        }),
      ),
    );
}

/** Real archives for the backup captures: at least three, the newest with the master key. */
let backupsMade = false;
async function demoBackups() {
  if (!backupsMade) {
    const post = (includeKey) =>
      api('/backups', {
        method: 'POST',
        headers: { 'Content-Type': 'application/json' },
        body: JSON.stringify({ includeKey }),
      });
    const have = (await api('/backups')).backups ?? [];
    for (let i = have.length; i < 2; i++) await post(false);
    if (!have.some((b) => b.includesKey)) await post(true);
    backupsMade = true;
  }
  return (await api('/backups')).backups;
}

/** The real /settings/security answer, completed with what a signed-in server has. */
async function mockSecurityAccount(page) {
  const real = await api('/settings/security');
  const now = Date.now();
  const at = (h) => new Date(now - h * 3_600_000).toISOString();
  await page.route('**/api/v1/settings/security', (r) =>
    r.fulfill(
      json({
        ...real,
        method: 'session',
        user: { id: 1, username: 'margaux', createdAt: at(24 * 40), lastLoginAt: at(2) },
        apiKey: { present: true, last4: '3f9a', createdAt: at(24 * 40), lastUsedAt: at(0.05) },
        sessions: [
          {
            id: 3,
            current: true,
            createdAt: at(2),
            lastSeenAt: at(0),
            expiresAt: at(-24 * 30),
            ip: '192.168.1.24',
            userAgent: 'Mozilla/5.0 (X11; Linux x86_64; rv:140.0) Gecko/20100101 Firefox/140.0',
          },
          {
            id: 2,
            current: false,
            createdAt: at(30),
            lastSeenAt: at(5),
            expiresAt: at(-24 * 29),
            ip: '192.168.1.31',
            userAgent:
              'Mozilla/5.0 (iPhone; CPU iPhone OS 18_0 like Mac OS X) AppleWebKit/605.1.15 (KHTML, like Gecko) Version/18.0 Mobile/15E148 Safari/604.1',
          },
          {
            id: 1,
            current: false,
            createdAt: at(24 * 16),
            lastSeenAt: at(24 * 3),
            expiresAt: at(-24 * 14),
            ip: '203.0.113.42',
            userAgent:
              'Mozilla/5.0 (Windows NT 10.0; Win64; x64) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/141.0 Safari/537.36',
          },
        ],
      }),
    ),
  );
}

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
    actions: {
      pause: false,
      resume: false,
      remove: true,
      retryImport: false,
      stopRetrying: true,
      resumeRetrying: false,
    },
    retryStopped: false,
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
      if (s.after) await s.after();
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
  await dropImportRoot();
  if (!only) writeFileSync(join(out, 'a11y-report.json'), `${JSON.stringify(report, null, 2)}\n`);
  else writeFileSync(join(out, 'a11y-report.partial.json'), `${JSON.stringify(report, null, 2)}\n`);
  await browser.close();
}

run().catch((e) => {
  console.error(e);
  process.exit(1);
});
