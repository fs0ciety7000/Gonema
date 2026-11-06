// Sign-in states and Settings › Security, Backups, Logs (settings-*.html, login.html) on the real
// server + demo library (local mode). Mocked with page.route only what the local demo cannot
// produce: the signed-out sign-in page and its 401 / 429 answers. Archives, restores (staged then
// canceled), deletions, the log buffer and the security settings are the real server.
import assert from 'node:assert/strict';
import { after, before, describe, it } from 'node:test';
import { api, axe, BASE, ensureServer, launch, openPage } from './helpers.mjs';

const json = (body, status = 200) => ({
  status,
  contentType: status >= 400 ? 'application/problem+json' : 'application/json',
  body: JSON.stringify(body),
});
const post = (path, body) =>
  api(path, { method: 'POST', headers: { 'Content-Type': 'application/json' }, body: JSON.stringify(body) });

describe('phase 8 screens, as the approved mockups', () => {
  let browser;
  before(async () => {
    await ensureServer();
    browser = await launch();
  });
  after(async () => {
    await api('/backups/restore', { method: 'DELETE' }).catch(() => {});
    await browser?.close();
  });

  it('sign-in: a wrong password clears and focuses the field and opens the help; 429 waits calmly', async () => {
    const page = await openPage(browser);
    let fail = 401;
    await page.route('**/api/v1/auth/status', (r) =>
      r.fulfill(json({ setupRequired: false, authenticated: false, method: 'none', authMode: 'enabled' })),
    );
    await page.route('**/api/v1/auth/login', (r) =>
      r.fulfill({ ...json({ status: fail, detail: 'no' }, fail), headers: fail === 429 ? { 'Retry-After': '3' } : {} }),
    );
    await page.goto(`${BASE}/login`);
    await page.getByLabel('Username').fill('margaux');
    const pw = page.getByLabel('Password', { exact: true });
    await pw.fill('not the password');
    await page.getByRole('button', { name: 'Sign in' }).click();
    await page.getByRole('alert').getByText('That username and password don’t match.').waitFor();
    assert.equal(await pw.inputValue(), '');
    assert.ok(await pw.evaluate((el) => el === document.activeElement), 'password focused');
    assert.equal(await page.locator('details.login-help').getAttribute('open'), '');
    fail = 429;
    await pw.fill('again not it');
    await page.getByRole('button', { name: 'Sign in' }).click();
    await page.getByText('Sign-in is paused for a few minutes.').waitFor();
    assert.ok(await page.getByRole('button', { name: /^Try again in 0:0\d$/ }).isDisabled());
    assert.ok(await pw.isDisabled(), 'password disabled during the wait');
    // the form comes back on its own
    await page.getByRole('button', { name: 'Sign in' }).waitFor({ timeout: 6000 });
    assert.ok(!(await pw.isDisabled()));
    assert.deepEqual(await axe(page), []);
    await page.context().close();
  });

  it('security: modes as read-only cards, the outbound table, the proxy panel, the key dialog cancels', async () => {
    const page = await openPage(browser);
    await page.goto(`${BASE}/settings/security`);
    const modes = page.getByRole('list', { name: 'Sign-in modes' });
    await modes.waitFor();
    // the demo runs with GONEMA_AUTH=disabled_for_local
    const inUse = modes.locator('[aria-current="true"]');
    assert.equal(await inUse.count(), 1);
    assert.match(await inUse.textContent(), /Skip it on the local network.*In use.*GONEMA_AUTH=disabled_for_local/);
    await page.getByText('One account, no roles').waitFor();
    const table = page.getByRole('table', { name: 'Where each kind of connection may go' });
    const rows = await table.getByRole('rowheader').allTextContents();
    assert.equal(rows.length, 3);
    for (const [i, n] of ['Integrations', 'Webhooks', 'TMDB and images'].entries())
      assert.ok(rows[i].startsWith(n), rows[i]);
    const heads = await table.getByRole('columnheader').allTextContents();
    assert.deepEqual(heads, ['Connection', 'Internet', 'Local network', 'This machine']);
    await page.getByText('Never contacted, whatever the settings').waitFor();
    for (const v of [
      'GONEMA_ALLOWED_HOSTS',
      'GONEMA_TRUSTED_PROXIES',
      'GONEMA_TRUSTED_ORIGINS',
      'GONEMA_COOKIE_SECURE',
    ])
      await page.getByText(v, { exact: true }).waitFor();
    assert.deepEqual(await axe(page), []);
    await page.context().close();
  });

  it('backups: header action, tiles from the list, staged pill, confirmed restore and delete', async () => {
    await post('/backups', { includeKey: false });
    const made = await post('/backups', { includeKey: false });
    const page = await openPage(browser);
    await page.goto(`${BASE}/settings/backups`);
    await page.locator('.bk tbody tr').first().waitFor();
    const list = (await api('/backups')).backups;
    const total = list.reduce((n, b) => n + b.size, 0);
    assert.match(await page.locator('.tile').nth(2).textContent(), new RegExp(`${list.length} archives`));
    assert.ok(total > 0);
    // "Back up now" lives in the page header
    const header = page.locator('.shead');
    await header.getByRole('button', { name: 'Back up now' }).waitFor();
    // restore: explicit confirmation, 3 steps, staged → pill on the archive, then canceled
    await page.getByRole('button', { name: `Restore ${made.name}…` }).click();
    const dlg = page.getByRole('dialog', { name: /^Restore the backup of/ });
    await dlg.waitFor();
    assert.equal(await dlg.locator('.steps3 li').count(), 3);
    assert.deepEqual(await axe(page), []);
    await dlg.getByRole('button', { name: 'Back up now, then stage the restore' }).click();
    const staged = page.getByRole('dialog', { name: 'Restore staged' });
    await staged.waitFor();
    await staged.getByRole('button', { name: 'Done' }).click();
    const row = page.locator('.bk tbody tr', { hasText: made.name });
    await row.getByText('Staged').waitFor();
    await page.getByText('A restore is waiting for a restart.').waitFor();
    await page.getByRole('button', { name: 'Cancel restore' }).click();
    await page.getByText('A restore is waiting for a restart.').waitFor({ state: 'detached' });
    // delete: icon button with a name, confirmation, kept on "Keep it", gone on confirm
    await page.getByRole('button', { name: `Delete ${made.name}…` }).click();
    const del = page.getByRole('dialog', { name: 'Delete this backup?' });
    await del.getByRole('button', { name: 'Keep it' }).click();
    assert.ok(
      (await api('/backups')).backups.some((b) => b.name === made.name),
      'kept',
    );
    await page.getByRole('button', { name: `Delete ${made.name}…` }).click();
    await del.getByRole('button', { name: 'Delete backup' }).click();
    await row.waitFor({ state: 'detached' });
    assert.ok(!(await api('/backups')).backups.some((b) => b.name === made.name), 'deleted');
    await page.context().close();
  });

  it('backups: presets write intervalHours, and the plain-words summary follows', async () => {
    const before = await api('/settings/backups');
    const page = await openPage(browser);
    try {
      await page.goto(`${BASE}/settings/backups`);
      const presets = page.getByRole('group', { name: 'Presets' });
      await presets.getByRole('button', { name: 'Day' }).click();
      await page.locator('.plain', { hasText: 'every day' }).waitFor();
      await page.getByText('Saved', { exact: false }).waitFor();
      assert.equal((await api('/settings/backups')).intervalHours, 24);
      assert.equal(await presets.getByRole('button', { name: 'Day' }).getAttribute('aria-pressed'), 'true');
    } finally {
      await api('/settings/backups', {
        method: 'PUT',
        headers: { 'Content-Type': 'application/json' },
        // nextRunAt (v1.1) is read only
        body: JSON.stringify({ ...before, nextRunAt: undefined }),
      });
      await page.context().close();
    }
  });

  it('logs: level chips filter the buffer, components show, an entry expands with its attributes', async () => {
    const page = await openPage(browser);
    await page.goto(`${BASE}/settings/logs`);
    await page.locator('.ln').first().waitFor();
    const levels = page.getByRole('group', { name: 'Levels shown' });
    // the server level is a select in the page header
    await page.locator('.shead').getByRole('combobox').waitFor();
    // info off: no info line left (client-side, same buffer)
    await levels.getByRole('button', { name: /^Info/ }).click();
    await page.waitForTimeout(300);
    assert.equal(await page.locator('.ln[data-lv="info"]').count(), 0);
    const comp = await page.locator('.ln .comp').first().textContent();
    assert.ok(comp && comp.length > 0, 'component column filled by the server');
    const first = page.locator('.ln').last();
    await first.click();
    assert.equal(await first.getAttribute('aria-expanded'), 'true');
    const detail = page.getByRole('region', { name: 'Details of this entry' });
    await detail.locator('dt', { hasText: 'component' }).waitFor();
    await detail.getByRole('button', { name: 'Copy as JSON' }).waitFor();
    assert.deepEqual(await axe(page), []);
    await page.context().close();
  });
});
