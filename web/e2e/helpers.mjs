// Shared Playwright helpers. The tests run against a real Gonema server started on a
// demo database (see e2e/README.md):
//   gonema demo seed -films 10000 -data-dir /tmp/gonema-demo
//   GONEMA_AUTH=disabled_for_local gonema serve -data-dir /tmp/gonema-demo   (binary built with `make release`)
// GONEMA_E2E_URL points at it (default http://127.0.0.1:7676). Without the local mode, set
// GONEMA_E2E_API_KEY: every request (Node and browser) then carries it in X-Api-Key.
// The browser is the preinstalled Chromium (PLAYWRIGHT_CHROMIUM, default /opt/pw-browsers/chromium).
import { readFileSync } from 'node:fs';
import { createRequire } from 'node:module';

const require = createRequire(import.meta.url);
const { chromium } = require('playwright');

export const BASE = (process.env.GONEMA_E2E_URL ?? 'http://127.0.0.1:7676').replace(/\/$/, '');
const EXECUTABLE = process.env.PLAYWRIGHT_CHROMIUM ?? '/opt/pw-browsers/chromium';
const API_KEY = process.env.GONEMA_E2E_API_KEY ?? '';
/** Headers that authenticate a request (the API key, when one is given). */
export const AUTH_HEADERS = API_KEY ? { 'X-Api-Key': API_KEY } : {};

/** The demo server must let the suites in: local mode (GONEMA_AUTH=disabled_for_local) or the API key. */
export async function ensureServer() {
  let auth;
  try {
    auth = await (await fetch(`${BASE}/api/v1/auth/status`, { headers: AUTH_HEADERS })).json();
  } catch (e) {
    throw new Error(`No Gonema server at ${BASE} (${e.message}). See e2e/README.md.`);
  }
  if (!auth.authenticated)
    throw new Error(
      `The server at ${BASE} asks for a sign-in: start it with GONEMA_AUTH=disabled_for_local, or set GONEMA_E2E_API_KEY.`,
    );
  const s = await (await fetch(`${BASE}/api/v1/system/status`, { headers: AUTH_HEADERS })).json();
  if (!s.demo) throw new Error(`The server at ${BASE} is not running a demo database. See e2e/README.md.`);
  return s;
}

export async function launch() {
  return chromium.launch({ executablePath: EXECUTABLE });
}

/** New page with a theme and optional reduced motion; collects console errors. */
export async function openPage(
  browser,
  { theme = 'dark', width = 1440, height = 900, reducedMotion = 'no-preference' } = {},
) {
  const ctx = await browser.newContext({
    viewport: { width, height },
    deviceScaleFactor: 1,
    reducedMotion,
    ...(API_KEY ? { extraHTTPHeaders: AUTH_HEADERS } : {}),
  });
  await ctx.addInitScript((t) => localStorage.setItem('gn-theme', t), theme);
  const page = await ctx.newPage();
  page.errors = [];
  page.on('pageerror', (e) => page.errors.push(e.message));
  page.on('console', (m) => {
    if (m.type() === 'error') page.errors.push(m.text());
  });
  return page;
}

/** Freezes running animations at a fixed frame (as ?freeze=1 does in the mockups). */
export async function freeze(page) {
  await page.evaluate(() => {
    for (const a of document.getAnimations()) {
      try {
        a.currentTime = 700;
        a.pause();
      } catch {}
    }
  });
}

export async function api(path, init) {
  const r = await fetch(`${BASE}/api/v1${path}`, { ...init, headers: { ...AUTH_HEADERS, ...init?.headers } });
  if (!r.ok) throw new Error(`${init?.method ?? 'GET'} ${path}: ${r.status}`);
  return r.status === 204 ? null : r.json();
}

/** axe-core (WCAG 2.1 A/AA + best-practice), as docs/design/tools/render.js does for the mockups. */
export async function axe(page) {
  // evaluated through the DevTools protocol: not blocked by the page's CSP
  await page.evaluate(readFileSync(require.resolve('axe-core/axe.min.js'), 'utf8'));
  return page.evaluate(async () => {
    const r = await window.axe.run(document, {
      runOnly: { type: 'tag', values: ['wcag2a', 'wcag2aa', 'wcag21a', 'wcag21aa', 'best-practice'] },
    });
    return r.violations.map((v) => ({
      id: v.id,
      impact: v.impact,
      help: v.help,
      nodes: v.nodes
        .slice(0, 5)
        .map((n) => ({ target: n.target.join(' '), summary: n.failureSummary?.split('\n').slice(0, 3).join(' ') })),
      count: v.nodes.length,
    }));
  });
}
