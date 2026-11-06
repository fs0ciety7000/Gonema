/* Pieces shared by the sign-in page, the setup's account step and Settings › Security.
   Lazy: only these screens import them. */
import '@/styles/phase8.css';
import { Check, Copy, Eye, EyeOff, KeyRound } from 'lucide-react';
import { type ComponentPropsWithRef, useEffect, useId, useRef, useState } from 'react';
import { ApiError } from '@/api/client';
import { Button, IconButton } from '@/components/ui/Button';
import { Input } from '@/components/ui/Field';

export const MIN_PASSWORD = 10;
export const MAX_PASSWORD = 256;

/** Password input with a show / hide toggle (the toggle is a real button with a pressed state). */
export function PasswordInput({
  invalid,
  ...rest
}: Omit<ComponentPropsWithRef<'input'>, 'type' | 'size'> & { invalid?: boolean }) {
  const [shown, setShown] = useState(false);
  return (
    <Input
      {...rest}
      type={shown ? 'text' : 'password'}
      invalid={invalid}
      spellCheck={false}
      autoCapitalize="none"
      suffix={
        <IconButton
          size="sm"
          label="Show password"
          aria-pressed={shown}
          onClick={() => setShown((s) => !s)}
          className="pw-toggle"
        >
          {shown ? <EyeOff className="i i-sm" aria-hidden="true" /> : <Eye className="i i-sm" aria-hidden="true" />}
        </IconButton>
      }
    />
  );
}

/** Rough strength of a new password: length first, then variety. For guidance only, never a gate. */
export function passwordStrength(pw: string): { level: 0 | 1 | 2 | 3; label: string } {
  if (pw.length < MIN_PASSWORD) return { level: 0, label: pw ? 'too short' : '' };
  const kinds = [/[a-z]/, /[A-Z]/, /\d/, /[^A-Za-z0-9]/].filter((r) => r.test(pw)).length;
  if (pw.length >= 16 || (pw.length >= 12 && kinds >= 3)) return { level: 3, label: 'strong' };
  if (pw.length >= 12 || kinds >= 3) return { level: 2, label: 'good' };
  return { level: 1, label: 'fair' };
}

export function StrengthMeter({ password }: { password: string }) {
  const s = passwordStrength(password);
  return (
    <div className="pw-meter" data-level={s.level} aria-hidden="true">
      <span />
      <span />
      <span />
    </div>
  );
}

/** Seconds to wait from a 429's Retry-After header (seconds or an HTTP date); 60 when absent. */
export function retryAfterSeconds(res: Response | undefined, now = Date.now()): number {
  const h = res?.headers.get('Retry-After');
  if (!h) return 60;
  if (/^\d+$/.test(h.trim())) return Math.max(1, Number(h.trim()));
  const at = Date.parse(h);
  return Number.isFinite(at) ? Math.max(1, Math.ceil((at - now) / 1000)) : 60;
}

/** "4:05", "0:09" */
export const formatCountdown = (s: number) => `${Math.floor(s / 60)}:${String(Math.max(0, s % 60)).padStart(2, '0')}`;

/** Seconds left until `until` (ms epoch), ticking once a second; 0 when it has passed. */
export function useSecondsLeft(until: number | null): number {
  const [now, setNow] = useState(() => Date.now());
  useEffect(() => {
    if (!until) return;
    setNow(Date.now());
    const t = setInterval(() => setNow(Date.now()), 1000);
    return () => clearInterval(t);
  }, [until]);
  return until ? Math.max(0, Math.ceil((until - now) / 1000)) : 0;
}

/** Problem detail of a failed call, in words for the user. */
export function problemText(e: unknown): string {
  if (e instanceof ApiError) return e.isNetwork ? 'Gonema could not be reached. Check that it is running.' : e.detail;
  return e instanceof Error ? e.message : String(e);
}

/** Copies text; falls back to a selected field and execCommand on plain-HTTP origins (no Clipboard API). */
async function copyText(text: string, field: HTMLInputElement | null): Promise<boolean> {
  try {
    if (navigator.clipboard && window.isSecureContext) {
      await navigator.clipboard.writeText(text);
      return true;
    }
  } catch {
    /* fall through */
  }
  if (!field) return false;
  field.focus();
  field.select();
  try {
    return document.execCommand('copy');
  } catch {
    return false;
  }
}

/** The API key, shown once: copy it, and know it will not be shown again. The key lives only in
 *  this component's props (never stored, never logged). */
export function ApiKeyReveal({ apiKey }: { apiKey: string }) {
  const id = useId();
  const field = useRef<HTMLInputElement>(null);
  const [copied, setCopied] = useState<'yes' | 'no' | null>(null);
  return (
    <section className="key-reveal" aria-labelledby={`${id}-h`}>
      <h2 id={`${id}-h`}>
        <KeyRound className="i i-sm" aria-hidden="true" />
        Your API key
      </h2>
      <p>
        <strong>You won’t see it again.</strong> Copy it now into the app that needs it (a request manager, a script).
        Gonema keeps only a fingerprint of it.
      </p>
      <div className="key-row">
        <Input
          ref={field}
          id={`${id}-key`}
          mono
          readOnly
          value={apiKey}
          aria-label="API key"
          onFocus={(ev) => ev.currentTarget.select()}
          wrapperClassName="key-field"
        />
        <Button
          icon={copied === 'yes' ? <Check className="i" aria-hidden="true" /> : <Copy className="i" aria-hidden="true" />}
          onClick={() => void copyText(apiKey, field.current).then((ok) => setCopied(ok ? 'yes' : 'no'))}
        >
          {copied === 'yes' ? 'Copied' : 'Copy'}
        </Button>
      </div>
      <p className="t-caption t-muted" role="status">
        {copied === 'yes'
          ? 'Copied to the clipboard.'
          : copied === 'no'
            ? 'This browser blocked the copy: the key is selected, press Ctrl+C (⌘C on a Mac).'
            : 'Lost it later? Make a new one in Settings › Security; the old one then stops working.'}
      </p>
    </section>
  );
}
