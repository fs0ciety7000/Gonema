import { useMutation, useQuery, useQueryClient } from '@tanstack/react-query';
import { useNavigate } from '@tanstack/react-router';
import { KeyRound, LogOut, RefreshCw, ShieldCheck } from 'lucide-react';
import { type FormEvent, useState } from 'react';
import { signOut } from '@/api/auth';
import { ApiError } from '@/api/client';
import { changePassword, opsKeys, regenerateApiKey, revokeSession, securityQuery } from '@/api/ops';
import type { AuthSession, SecuritySettings } from '@/api/schema';
import {
  ApiKeyReveal,
  formatCountdown,
  MAX_PASSWORD,
  MIN_PASSWORD,
  PasswordInput,
  passwordStrength,
  problemText,
  retryAfterSeconds,
  StrengthMeter,
  useSecondsLeft,
} from '@/components/auth/AuthParts';
import { StatusText } from '@/components/ui/Badges';
import { Button } from '@/components/ui/Button';
import { Dialog } from '@/components/ui/Dialog';
import { Skeleton } from '@/components/ui/Feedback';
import { Field } from '@/components/ui/Field';
import { toast } from '@/components/ui/Toast';
import { formatWhen } from '@/lib/format';
import { Panel } from './Sections';

/** "Firefox on Linux", "Chrome on macOS", "curl": enough to recognise one's own devices. */
export function deviceOf(ua: string): string {
  if (!ua) return 'Unknown device';
  const browser = /Edg\//.test(ua)
    ? 'Edge'
    : /Firefox\//.test(ua)
      ? 'Firefox'
      : /Chrome\//.test(ua)
        ? 'Chrome'
        : /Safari\//.test(ua)
          ? 'Safari'
          : (ua.split(/[/\s]/)[0] ?? 'Unknown');
  const os = /iPhone|iPad/.test(ua)
    ? 'iOS'
    : /Android/.test(ua)
      ? 'Android'
      : /Windows/.test(ua)
        ? 'Windows'
        : /Mac OS X|Macintosh/.test(ua)
          ? 'macOS'
          : /Linux/.test(ua)
            ? 'Linux'
            : '';
  return os ? `${browser} on ${os}` : browser;
}

function SignIn({ s }: { s: SecuritySettings }) {
  const local = s.authMode === 'disabled_for_local';
  return (
    <Panel id="sec-signin" title="Sign-in">
      <ul className="mini-rows">
        <li>
          <span>
            Login page
            <small>
              {local
                ? 'Skipped for loopback and LAN addresses reached directly (GONEMA_AUTH=disabled_for_local).'
                : 'Required on every network.'}
            </small>
          </span>
          {local ? (
            <StatusText status="missing">Off on this network</StatusText>
          ) : (
            <StatusText status="available">On</StatusText>
          )}
        </li>
        <li>
          <span>
            Account
            <small>
              {s.user
                ? `${s.user.username} · created ${formatWhen(s.user.createdAt)}${s.user.lastLoginAt ? ` · last sign-in ${formatWhen(s.user.lastLoginAt)}` : ''}`
                : 'No account yet. Create it from the setup screen.'}
            </small>
          </span>
        </li>
        <li>
          <span>
            Session cookie
            <small>
              Secure flag:{' '}
              {s.cookieSecure === 'auto' ? 'automatic (on over HTTPS)' : s.cookieSecure === 'true' ? 'always' : 'never'}
              {s.trustedOrigins?.length ? ` · trusted origins: ${s.trustedOrigins.join(', ')}` : ''}
            </small>
          </span>
        </li>
      </ul>
      <p className="explain">
        These come from the start options (<code>GONEMA_AUTH</code>, <code>GONEMA_COOKIE_SECURE</code>,{' '}
        <code>GONEMA_TRUSTED_ORIGINS</code>). Change them where Gonema is started, then restart it.
      </p>
    </Panel>
  );
}

function PasswordPanel({ hasAccount }: { hasAccount: boolean }) {
  const qc = useQueryClient();
  const [current, setCurrent] = useState('');
  const [next, setNext] = useState('');
  const [errors, setErrors] = useState<{ current?: string; next?: string; form?: string }>({});
  const [blockedUntil, setBlockedUntil] = useState<number | null>(null);
  const left = useSecondsLeft(blockedUntil);
  const m = useMutation({
    mutationFn: () => changePassword(current, next),
    onSuccess: () => {
      setCurrent('');
      setNext('');
      setErrors({});
      void qc.invalidateQueries({ queryKey: opsKeys.security });
      toast.show({ title: 'Password changed.', body: 'Every other browser was signed out; this one stays signed in.' });
    },
    onError: (e) => {
      const status = e instanceof ApiError ? e.status : 0;
      if (status === 429) {
        const res = (e as { response?: Response }).response;
        setBlockedUntil(Date.now() + retryAfterSeconds(res) * 1000);
        setErrors({});
      } else if (status === 422 && /current/i.test(problemText(e))) setErrors({ current: problemText(e) });
      else if (status === 422) setErrors({ next: problemText(e) });
      else setErrors({ form: problemText(e) });
    },
  });
  const submit = (ev: FormEvent) => {
    ev.preventDefault();
    const e: typeof errors = {};
    if (!current) e.current = 'Type the current password.';
    if (next.length < MIN_PASSWORD) e.next = `Use at least ${MIN_PASSWORD} characters.`;
    else if (next.length > MAX_PASSWORD) e.next = `Use at most ${MAX_PASSWORD} characters.`;
    else if (next === current) e.next = 'The new password must differ from the current one.';
    setErrors(e);
    if (!e.current && !e.next) m.mutate();
  };
  const blocked = left > 0;
  const strength = passwordStrength(next);
  return (
    <Panel id="sec-password" title="Password">
      {!hasAccount ? (
        <p className="explain">There is no account yet: nothing to change.</p>
      ) : (
        <form className="set-form" onSubmit={submit} noValidate>
          <Field label="Current password" error={errors.current}>
            {({ id, describedBy, invalid }) => (
              <PasswordInput
                id={id}
                autoComplete="current-password"
                aria-describedby={describedBy}
                invalid={invalid}
                value={current}
                onChange={(ev) => setCurrent(ev.target.value)}
              />
            )}
          </Field>
          <Field
            label="New password"
            aside={strength.label ? <span className="t-muted field-aside">{strength.label}</span> : undefined}
            error={errors.next}
            hint="At least 10 characters. Every other browser is signed out when it changes."
          >
            {({ id, describedBy, invalid }) => (
              <>
                <PasswordInput
                  id={id}
                  autoComplete="new-password"
                  aria-describedby={describedBy}
                  invalid={invalid}
                  value={next}
                  onChange={(ev) => setNext(ev.target.value)}
                />
                <StrengthMeter password={next} />
              </>
            )}
          </Field>
          {errors.form && <p className="field-error">{errors.form}</p>}
          {blocked && (
            <p className="explain" role="status">
              Too many wrong attempts. Gonema pauses password changes for a moment: try again in{' '}
              <span className="countdown t-mono-sm">{formatCountdown(left)}</span>.
            </p>
          )}
          <div className="row">
            <Button type="submit" variant="secondary" loading={m.isPending} disabled={blocked}>
              Change password
            </Button>
          </div>
        </form>
      )}
    </Panel>
  );
}

function Sessions({ s }: { s: SecuritySettings }) {
  const qc = useQueryClient();
  const navigate = useNavigate();
  const [hidden, setHidden] = useState<ReadonlySet<number>>(new Set());
  const list = (s.sessions ?? []).filter((x) => !hidden.has(x.id));
  const revoke = (x: AuthSession) => {
    setHidden((h) => new Set(h).add(x.id));
    const restore = () =>
      setHidden((h) => {
        const n = new Set(h);
        n.delete(x.id);
        return n;
      });
    toast.show({
      title: `Signed out ${deviceOf(x.userAgent)}`,
      body: 'That browser must sign in again.',
      undo: restore,
      onClose: (reason) => {
        if (reason === 'undo') return;
        revokeSession(x.id)
          .then(() => qc.invalidateQueries({ queryKey: opsKeys.security }))
          .catch((e) => {
            restore();
            toast.error('That session is still open', problemText(e));
          });
      },
    });
  };
  return (
    <Panel id="sec-sessions" title="Signed-in browsers">
      {!list.length ? (
        <p className="explain">
          {s.method === 'local'
            ? 'This browser uses the local access (no session). Browsers that sign in appear here.'
            : 'No other browser is signed in.'}
        </p>
      ) : (
        <ul className="mini-rows sessions">
          {list.map((x) => (
            <li key={x.id}>
              <span>
                {deviceOf(x.userAgent)}
                <small>
                  {[x.ip, `signed in ${formatWhen(x.createdAt)}`, `last seen ${formatWhen(x.lastSeenAt)}`]
                    .filter(Boolean)
                    .join(' · ')}
                </small>
              </span>
              {x.current ? <span className="this">This browser</span> : <span />}
              {x.current ? (
                <Button
                  size="sm"
                  variant="ghost"
                  icon={<LogOut className="i" aria-hidden="true" />}
                  onClick={() => void signOut(qc).finally(() => navigate({ to: '/login', replace: true }))}
                >
                  Sign out
                </Button>
              ) : (
                <Button
                  size="sm"
                  variant="ghost"
                  onClick={() => revoke(x)}
                  aria-label={`Sign out ${deviceOf(x.userAgent)}, ${x.ip}`}
                >
                  Sign out
                </Button>
              )}
            </li>
          ))}
        </ul>
      )}
    </Panel>
  );
}

function ApiKeyPanel({ s }: { s: SecuritySettings }) {
  const qc = useQueryClient();
  const [open, setOpen] = useState(false);
  const [key, setKey] = useState<string | null>(null);
  const m = useMutation({
    mutationFn: regenerateApiKey,
    onSuccess: (r) => {
      setKey(r.apiKey);
      void qc.invalidateQueries({ queryKey: opsKeys.security });
    },
    onError: (e) => toast.error('No new key was made', problemText(e)),
  });
  const k = s.apiKey;
  const close = () => {
    setOpen(false);
    setKey(null); // the key is forgotten as soon as the dialog closes
    m.reset();
  };
  return (
    <Panel id="sec-apikey" title="API key">
      <ul className="mini-rows">
        <li>
          <span>
            <span className="t-mono-sm">{k.present ? `gnm_••••••••${k.last4 ?? ''}` : 'No key'}</span>
            <small>
              {k.present
                ? [
                    k.createdAt ? `made ${formatWhen(k.createdAt)}` : '',
                    k.lastUsedAt ? `used ${formatWhen(k.lastUsedAt)}` : 'never used',
                  ]
                    .filter(Boolean)
                    .join(' · ')
                : 'Make one for the apps that talk to Gonema.'}
            </small>
          </span>
          <Button
            size="sm"
            variant="ghost"
            icon={<RefreshCw className="i" aria-hidden="true" />}
            disabled={!s.user}
            onClick={() => setOpen(true)}
          >
            {k.present ? 'Regenerate…' : 'Make a key…'}
          </Button>
        </li>
      </ul>
      <p className="explain">
        Apps send it in the <code>X-Api-Key</code> header. Gonema stores only a fingerprint, so the full key is shown
        once, when it is made.
      </p>
      <Dialog
        open={open}
        onOpenChange={(o) => (o ? setOpen(true) : close())}
        icon={<KeyRound className="i" aria-hidden="true" />}
        title={key ? 'Your new API key' : k.present ? 'Make a new API key?' : 'Make an API key?'}
        description={
          key
            ? 'The previous key no longer works. Put this one in the apps that used it.'
            : k.present
              ? `The current key (ending in ${k.last4 ?? '…'}) stops working at once. Every app that uses it needs the new one.`
              : 'The key is shown once, right after it is made.'
        }
        actions={
          key ? (
            <Button variant="primary" onClick={close}>
              Done
            </Button>
          ) : (
            <>
              <Button variant="ghost" onClick={close}>
                Cancel
              </Button>
              <Button variant="danger" loading={m.isPending} onClick={() => m.mutate()}>
                {k.present ? 'Replace the key' : 'Make the key'}
              </Button>
            </>
          )
        }
      >
        {key && <ApiKeyReveal apiKey={key} />}
      </Dialog>
    </Panel>
  );
}

function Outbound({ s }: { s: SecuritySettings }) {
  const yes = (on: boolean) =>
    on ? <StatusText status="available">Allowed</StatusText> : <StatusText status="unmonitored">Blocked</StatusText>;
  return (
    <Panel
      id="sec-outbound"
      title={
        <>
          <ShieldCheck className="i" aria-hidden="true" />
          Outbound connections
        </>
      }
    >
      <p className="explain">
        Gonema checks every address it connects to, after the name is resolved, so a URL typed in a connection cannot
        reach the server’s own network by surprise. Read only: set <code>GONEMA_OUTBOUND_ALLOW_LOOPBACK</code>,{' '}
        <code>GONEMA_OUTBOUND_ALLOW_PRIVATE</code> and <code>GONEMA_WEBHOOK_ALLOW_PRIVATE</code> where Gonema starts.
      </p>
      <table className="policy">
        <caption className="sr-only">Addresses Gonema may connect to, per kind of connection</caption>
        <thead>
          <tr>
            <th scope="col">Connection</th>
            <th scope="col">This machine (loopback)</th>
            <th scope="col">Local network (private)</th>
          </tr>
        </thead>
        <tbody>
          <tr>
            <th scope="row">
              Integrations
              <small>Indexers, download clients, media servers, ntfy, Gotify, e-mail…</small>
            </th>
            <td>{yes(s.outbound.integration.allowLoopback)}</td>
            <td>{yes(s.outbound.integration.allowPrivate)}</td>
          </tr>
          <tr>
            <th scope="row">
              Webhooks
              <small>Generic webhook, Discord, Slack</small>
            </th>
            <td>{yes(s.outbound.webhook.allowLoopback)}</td>
            <td>{yes(s.outbound.webhook.allowPrivate)}</td>
          </tr>
        </tbody>
      </table>
      {!!s.outbound.alwaysBlocked?.length && (
        <>
          <span className="field-label">Never contacted</span>
          <ul className="blocked-list">
            {s.outbound.alwaysBlocked.map((b) => (
              <li key={b}>{b}</li>
            ))}
          </ul>
        </>
      )}
    </Panel>
  );
}

export function SecuritySection() {
  const q = useQuery(securityQuery);
  if (q.isPending) return <Skeleton className="sk-wanted" />;
  if (!q.data)
    return (
      <p className="t-secondary t-body-sm" role="alert">
        The security settings could not be read: {problemText(q.error)}{' '}
        <Button size="sm" onClick={() => void q.refetch()}>
          Try again
        </Button>
      </p>
    );
  const s = q.data;
  return (
    <>
      <SignIn s={s} />
      <PasswordPanel hasAccount={!!s.user} />
      <Sessions s={s} />
      <ApiKeyPanel s={s} />
      <Outbound s={s} />
    </>
  );
}

export default SecuritySection;
