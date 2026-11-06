import { render, screen } from '@testing-library/react';
import userEvent from '@testing-library/user-event';
import { describe, expect, it, vi } from 'vitest';
import { safeRedirect } from '@/api/auth';
import { parseLogEntry } from '@/api/ops';
import { ApiKeyReveal, formatCountdown, passwordStrength, retryAfterSeconds } from '@/components/auth/AuthParts';
import { journalIdOf } from '@/screens/health/HealthScreen';
import { currentStep } from '@/screens/onboarding/OnboardingScreen';
import { everyLabel } from '@/screens/settings/BackupsSection';
import { attrsText, matches, mergeEntries } from '@/screens/settings/LogsSection';
import { deviceOf } from '@/screens/settings/SecuritySection';

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
