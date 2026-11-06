import { render, screen } from '@testing-library/react';
import userEvent from '@testing-library/user-event';
import { describe, expect, it, vi } from 'vitest';
import { safeRedirect } from '@/api/auth';
import { parseLogEntry } from '@/api/ops';
import { ApiKeyReveal, formatCountdown, passwordStrength, retryAfterSeconds } from '@/components/auth/AuthParts';
import { journalIdOf } from '@/screens/health/HealthScreen';
import { currentStep } from '@/screens/onboarding/OnboardingScreen';
import { everyLabel, folderTotals, madeWhen, stagedWhen } from '@/screens/settings/BackupsSection';
import { attrsText, countLevels, matches, mergeEntries, minLevel, Redactable } from '@/screens/settings/LogsSection';
import { deviceOf, initials, networkOf, sinceWhen } from '@/screens/settings/SecuritySection';

describe('sign-in', () => {
  it('only returns inside the app', () => {
    expect(safeRedirect('/films?layout=table')).toBe('/films?layout=table');
    expect(safeRedirect('//evil.example/x')).toBeUndefined();
    expect(safeRedirect('https://evil.example')).toBeUndefined();
    expect(safeRedirect('/\\evil')).toBeUndefined();
    expect(safeRedirect('/login?redirect=/x')).toBeUndefined();
    expect(safeRedirect('/setup')).toBeUndefined();
    expect(safeRedirect('/settings/security')).toBe('/settings/security');
    expect(safeRedirect(42)).toBeUndefined();
  });
  it('reads Retry-After in seconds or as a date', () => {
    const r = (v?: string) => new Response(null, { status: 429, headers: v ? { 'Retry-After': v } : {} });
    expect(retryAfterSeconds(r('125'))).toBe(125);
    expect(retryAfterSeconds(r())).toBe(60);
    const now = Date.parse('2026-11-06T10:00:00Z');
    expect(retryAfterSeconds(r('Fri, 06 Nov 2026 10:01:30 GMT'), now)).toBe(90);
    expect(formatCountdown(125)).toBe('2:05');
  });
  it('rates passwords by length first', () => {
    expect(passwordStrength('short').level).toBe(0);
    expect(passwordStrength('tenletters').level).toBe(1);
    expect(passwordStrength('correct horse battery').level).toBe(3);
  });
  it('opens the account step first when the server says it is missing', () => {
    expect(currentStep(undefined, { account: false, tmdb: true })).toBe('account');
  });
  it('shows the API key once, with a copy button and the warning', async () => {
    const writeText = vi.fn().mockResolvedValue(undefined);
    Object.defineProperty(navigator, 'clipboard', { value: { writeText }, configurable: true });
    Object.defineProperty(window, 'isSecureContext', { value: true, configurable: true });
    render(<ApiKeyReveal apiKey="gnm_abc123" />);
    expect(screen.getByLabelText('API key')).toHaveValue('gnm_abc123');
    expect(screen.getByText(/won’t see it again/)).toBeInTheDocument();
    await userEvent.click(screen.getByRole('button', { name: 'Copy' }));
    expect(writeText).toHaveBeenCalledWith('gnm_abc123');
    expect(await screen.findByText('Copied to the clipboard.')).toBeInTheDocument();
  });
});

describe('settings and health (phase 8)', () => {
  it('names devices from their user agent', () => {
    expect(deviceOf('Mozilla/5.0 (X11; Linux x86_64; rv:140.0) Gecko/20100101 Firefox/140.0')).toBe('Firefox on Linux');
    expect(
      deviceOf(
        'Mozilla/5.0 (Macintosh; Intel Mac OS X 14_0) AppleWebKit/605.1.15 (KHTML, like Gecko) Version/18 Safari/605.1.15',
      ),
    ).toBe('Safari on macOS');
    expect(deviceOf('curl/8.5.0')).toBe('curl');
    expect(deviceOf('')).toBe('Unknown device');
  });
  it('validates and merges live log entries', () => {
    expect(parseLogEntry({ id: 1, time: 't', level: 'nope', message: 'x' })).toBeNull();
    const e = (id: number) =>
      parseLogEntry({ id, time: '2026-11-06T10:00:00Z', level: 'info', message: `m${id}`, attrs: { k: 'a b' } })!;
    expect(attrsText(e(1).attrs)).toBe('k="a b"');
    expect(matches(e(1), 'A B')).toBe(true);
    const merged = mergeEntries([e(1), e(2)], [e(2), e(3)]);
    expect(merged.map((x) => x.id)).toEqual([1, 2, 3]);
  });
  it('labels schedules and journal issues', () => {
    expect(everyLabel(168)).toBe('Every 7 days');
    expect(everyLabel(24)).toBe('Every day');
    expect(everyLabel(6)).toBe('Every 6 hours');
    expect(journalIdOf('import.interrupted:42')).toBe(42);
    expect(journalIdOf('backup.failed')).toBeNull();
  });
});

describe('approved mockups (security, backups, logs)', () => {
  it('names the network of a session address', () => {
    expect(networkOf('192.168.1.24')).toBe('Local network');
    expect(networkOf('10.0.0.3')).toBe('Local network');
    expect(networkOf('172.20.1.1')).toBe('Local network');
    expect(networkOf('172.32.1.1')).toBe('Internet');
    expect(networkOf('127.0.0.1')).toBe('This machine');
    expect(networkOf('::1')).toBe('This machine');
    expect(networkOf('::ffff:192.168.0.2')).toBe('Local network');
    expect(networkOf('fd12:3456::1')).toBe('Local network');
    expect(networkOf('203.0.113.42')).toBe('Internet');
    expect(networkOf('2001:db8::1')).toBe('Internet');
    expect(networkOf('')).toBe('');
  });
  it('says when, as people do', () => {
    const now = new Date(2026, 10, 6, 12, 0).getTime();
    const at = (h: number) => new Date(now - h * 3_600_000).toISOString();
    expect(sinceWhen(at(0.01), now)).toBe('Just now');
    expect(sinceWhen(at(0.05), now)).toBe('3 min ago');
    expect(sinceWhen(at(4), now)).toBe('Today at 08:00');
    expect(sinceWhen(at(20), now)).toBe('Yesterday at 16:00');
    expect(sinceWhen(at(72), now)).toBe('3 days ago');
    expect(sinceWhen(at(24 * 30), now)).toBe('7 Oct 2026');
    expect(initials('margaux')).toBe('MA');
    expect(initials('john.doe')).toBe('JD');
  });
  it('sums the archives and dates them', () => {
    const b = (size: number) => ({ size }) as Parameters<typeof folderTotals>[0][number];
    expect(folderTotals([b(10), b(20), b(-1)])).toEqual({ count: 3, bytes: 30 });
    const now = new Date(2026, 10, 6, 12, 0);
    expect(madeWhen(new Date(2026, 10, 6, 9, 14).toISOString(), now)).toBe('Today, 09:14');
    expect(madeWhen(new Date(2026, 9, 30, 3, 40).toISOString(), now)).toBe('30 Oct, 03:40');
    expect(madeWhen(new Date(2025, 9, 30, 3, 40).toISOString(), now)).toBe('30 Oct 2025, 03:40');
    expect(stagedWhen(new Date(2026, 10, 6, 9, 14).toISOString(), now)).toBe('today at 09:14');
  });
  it('filters logs from the lowest level shown and counts per level', () => {
    expect(minLevel(new Set(['warn', 'info'] as const))).toBe('info');
    expect(minLevel(new Set(['error'] as const))).toBe('error');
    const e = (level: 'info' | 'warn') =>
      parseLogEntry({ id: 1, time: 't', level, message: 'm', component: 'search' })!;
    expect(e('info').component).toBe('search');
    expect(parseLogEntry({ id: 1, time: 't', level: 'info', message: 'm' })!.component).toBe('');
    expect(countLevels([e('info'), e('warn'), e('warn')])).toEqual({ debug: 0, info: 1, warn: 2, error: 0 });
  });
  it('shows redacted values as a token, never as text', () => {
    render(
      <p data-testid="v">
        <Redactable text="url=https://x.example/api?apikey=[redacted]&t=1" />
      </p>,
    );
    const v = screen.getByTestId('v');
    expect(v.textContent).not.toContain('[redacted]');
    expect(v.querySelector('.red')?.textContent).toContain('redacted');
    expect(v.textContent).toContain('&t=1');
  });
});
