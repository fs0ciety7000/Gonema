// Sign-in, end to end, on a server with a FRESH data directory and the default auth
// (no GONEMA_AUTH=disabled_for_local), see e2e/README.md:
//   gonema serve -data-dir /tmp/gonema-auth -bind 127.0.0.1:7677
// GONEMA_E2E_AUTH_URL points at it (default http://127.0.0.1:7677). The suite creates the
// account, so it runs once per fresh directory; it ends with the sign-in throttled (429)
// for 15 minutes from 127.0.0.1.
import assert from 'node:assert/strict';
import { after, before, describe, it } from 'node:test';
import { axe, launch } from '../helpers.mjs';

const AUTH = (process.env.GONEMA_E2E_AUTH_URL ?? 'http://127.0.0.1:7677').replace(/\/$/, '');
const USER = 'margaux';
const PASSWORD = 'correct horse battery staple';

async function status(headers = {}) {
  return (await fetch(`${AUTH}/api/v1/auth/status`, { headers })).json();
}

describe('sign-in: first account, API key once, sign out, sign in, protected routes', () => {
  let browser;
  let ctx;
  let page;
  let apiKey = '';
  const errors = [];
  before(async () => {
    let s;
    try {
      s = await status();
    } catch (e) {
      throw new Error(`No Gonema server at ${AUTH} (${e.message}). See e2e/README.md (auth suite).`);
    }
    if (!s.setupRequired || s.authMode !== 'enabled')
      throw new Error(
        `${AUTH} must run on a fresh data directory with the default GONEMA_AUTH (got ${JSON.stringify(s)}).`,
      );
    browser = await launch();
    ctx = await browser.newContext({ viewport: { width: 1440, height: 900 }, deviceScaleFactor: 1 });
    await ctx.grantPermissions(['clipboard-read', 'clipboard-write'], { origin: AUTH });
    page = await ctx.newPage();
    page.on('pageerror', (e) => errors.push(e.message));
    page.on('console', (m) => {
      // 401 answers are expected while signed out; the browser logs them as failed resources
      if (m.type() === 'error' && !/401|429|Unauthorized|Too Many/.test(m.text())) errors.push(m.text());
    });
  });
  after(async () => {
    await browser?.close();
  });

  it('sends a first visit to the setup, where only the welcome and account steps are open', async () => {
    await page.goto(`${AUTH}/films`);
    await page.waitForURL(/\/setup/);
    await page.getByRole('heading', { name: 'Your private cinema is almost open.' }).waitFor();
    const steps = page.getByRole('navigation', { name: 'Setup steps' });
    assert.equal(await steps.getByRole('link').count(), 2, 'welcome and account only');
    assert.equal(await page.getByRole('button', { name: 'Skip for now' }).count(), 0, 'the account cannot be skipped');
    await page.getByRole('button', { name: /Let’s begin/ }).click();
    await page.getByRole('heading', { name: 'Secure the booth' }).waitFor();
  });

  it('creates the account and shows the API key once', async () => {
    await page.getByRole('button', { name: /Create the account/ }).click();
    await page.getByText('Choose a username: you type it to sign in.').waitFor();
    await page.getByLabel('Username').fill(USER);
    await page.getByLabel('Password', { exact: true }).fill(PASSWORD);
    await page.getByRole('button', { name: /Create the account/ }).click();
    const field = page.getByRole('textbox', { name: 'API key' });
    await field.waitFor();
    apiKey = await field.inputValue();
    assert.match(apiKey, /^gnm_\S{20,}$/);
    await page.getByText('You won’t see it again.').waitFor();
    await page.getByRole('button', { name: 'Copy' }).click();
    await page.getByText(/Copied to the clipboard|blocked the copy/).waitFor();
    // the key works in X-Api-Key, and the API is closed without it
    assert.equal((await fetch(`${AUTH}/api/v1/system/status`, { headers: { 'X-Api-Key': apiKey } })).status, 200);
    assert.equal((await fetch(`${AUTH}/api/v1/system/status`)).status, 401);
    const a11y = await axe(page);
    assert.deepEqual(a11y, [], 'account step with the key: no axe violation');
    await page.getByRole('button', { name: /Continue/ }).click();
    await page.getByRole('heading', { name: 'Connect film metadata' }).waitFor();
    assert.equal(await page.getByText(apiKey).count(), 0, 'the key is gone once the step is left');
    assert.equal(await page.locator(`input[value="${apiKey}"]`).count(), 0);
  });

  it('Settings › Security shows the last 4 characters only, and the username is in the sidebar', async () => {
    await page.goto(`${AUTH}/settings/security`);
    await page.getByRole('heading', { name: 'API key' }).waitFor();
    await page.getByText(`gnm_••••••••${apiKey.slice(-4)}`).waitFor();
    assert.ok(!(await page.content()).includes(apiKey), 'full key never rendered again');
    const nav = page.getByRole('navigation', { name: 'Main' });
    await nav.getByText(USER, { exact: true }).waitFor();
  });

  it('signs out from the sidebar', async () => {
    await page.getByRole('button', { name: 'Sign out' }).first().click();
    await page.waitForURL(/\/login$/);
    await page.getByRole('heading', { name: 'Back to the booth' }).waitFor();
    assert.equal((await status()).authenticated, false);
  });

  it('a protected URL leads to sign-in, then back to it; a wrong password is refused calmly', async () => {
    await page.goto(`${AUTH}/films?layout=table`);
    await page.waitForURL(/\/login\?redirect=/);
    assert.equal(new URL(page.url()).searchParams.get('redirect'), '/films?layout=table');
    await page.getByLabel('Username').fill(USER);
    await page.getByLabel('Password', { exact: true }).fill('not the password');
    await page.getByRole('button', { name: 'Sign in' }).click();
    await page.getByRole('alert').getByText('That username and password don’t match.', { exact: false }).waitFor();
    assert.equal(await page.getByLabel('Password', { exact: true }).inputValue(), '', 'password cleared');
    const a11y = await axe(page);
    assert.deepEqual(a11y, [], 'sign-in page with an error: no axe violation');
    await page.getByLabel('Password', { exact: true }).fill(PASSWORD);
    await page.getByRole('button', { name: 'Sign in' }).click();
    await page.waitForURL((u) => u.pathname === '/films' && u.searchParams.get('layout') === 'table');
    await page.getByRole('heading', { level: 1, name: 'Films' }).waitFor();
  });

  it('a session ended elsewhere sends the next request to sign-in, with the way back', async () => {
    const sec = await (await fetch(`${AUTH}/api/v1/settings/security`, { headers: { 'X-Api-Key': apiKey } })).json();
    assert.ok(sec.sessions.length >= 1);
    for (const s of sec.sessions) {
      const r = await fetch(`${AUTH}/api/v1/auth/sessions/${s.id}`, {
        method: 'DELETE',
        headers: { 'X-Api-Key': apiKey },
      });
      assert.equal(r.status, 204);
    }
    await page.getByRole('navigation', { name: 'Main' }).getByRole('link', { name: 'Queue' }).click();
    await page.waitForURL(/\/login\?redirect=/);
    assert.equal(new URL(page.url()).searchParams.get('redirect'), '/queue');
  });

  it('too many failures: a calm countdown, and the button waits', async () => {
    // throttling counts per address: fail from Node (same address) until the server says 429
    let last = 0;
    for (let i = 0; i < 25 && last !== 429; i++) {
      last = (
        await fetch(`${AUTH}/api/v1/auth/login`, {
          method: 'POST',
          headers: { 'Content-Type': 'application/json' },
          body: JSON.stringify({ username: USER, password: `wrong-${i}-xxxxxx` }),
        })
      ).status;
    }
    assert.equal(last, 429);
    await page.getByLabel('Username').fill(USER);
    await page.getByLabel('Password', { exact: true }).fill(PASSWORD);
    await page.getByRole('button', { name: 'Sign in' }).click();
    await page.getByText('Too many attempts.').waitFor();
    await page.locator('.countdown').waitFor();
    assert.match(await page.locator('.countdown').textContent(), /^\d+:\d\d$/);
    assert.equal(await page.getByRole('button', { name: 'Sign in' }).isDisabled(), true);
    assert.deepEqual(errors, []);
  });
});
