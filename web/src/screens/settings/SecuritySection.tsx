import '@/styles/system.css';
import { useMutation, useQuery, useQueryClient } from '@tanstack/react-query';
import { Link, useNavigate } from '@tanstack/react-router';
import {
  Ban,
  CircleCheck,
  Globe,
  Info,
  KeyRound,
  Laptop,
  Lock,
  LogOut,
  Monitor,
  Network,
  RefreshCw,
  Router,
  Server,
  ShieldCheck,
  Smartphone,
  Tablet,
  Terminal,
} from 'lucide-react';
import { type FormEvent, type ReactNode, useState } from 'react';
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
  problemText,
  retryAfterSeconds,
  StrengthLabel,
  StrengthMeter,
  useSecondsLeft,
} from '@/components/auth/AuthParts';
import { Button } from '@/components/ui/Button';
import { Checkbox } from '@/components/ui/Checkbox';
import { Dialog } from '@/components/ui/Dialog';
import { Skeleton } from '@/components/ui/Feedback';
import { Field } from '@/components/ui/Field';
import { TOAST_DURATION, toast } from '@/components/ui/Toast';
import { formatClock, formatDate } from '@/lib/format';
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

function DeviceIcon({ ua }: { ua: string }) {
  const I = /iPad|Tablet/.test(ua)
    ? Tablet
    : /iPhone|Mobile|Android/.test(ua)
      ? Smartphone
      : /Windows/.test(ua)
        ? Monitor
        : Laptop;
  return <I className="i" aria-hidden="true" />;
}

/** Where an address is, as the outbound policy sees it: "This machine", "Local network" or "Internet". */
export function networkOf(raw: string): string {
  const ip = raw.trim().toLowerCase().replace(/^::ffff:/, '');
  if (!ip) return '';
  const v4 = /^(\d{1,3})\.(\d{1,3})\.\d{1,3}\.\d{1,3}$/.exec(ip);
  if (v4) {
    const a = Number(v4[1]);
    const b = Number(v4[2]);
    if (a === 127) return 'This machine';
    if (a === 10 || (a === 172 && b >= 16 && b <= 31) || (a === 192 && b === 168)) return 'Local network';
    if ((a === 169 && b === 254) || (a === 100 && b >= 64 && b <= 127)) return 'Local network';
    return 'Internet';
  }
  if (ip === '::1') return 'This machine';
  if (/^f[cd][0-9a-f]{2}:/.test(ip) || /^fe[89ab][0-9a-f]:/.test(ip)) return 'Local network';
  return ip.includes(':') ? 'Internet' : '';
}

/** "2 min ago", "Today at 07:40", "Yesterday at 21:02", "3 days ago", "21 Oct 2026". */
export function sinceWhen(d: string | null | undefined, now = Date.now()): string {
  if (!d) return '';
  const t = new Date(d);
  if (Number.isNaN(t.getTime())) return '';
  const min = Math.floor((now - t.getTime()) / 60_000);
  if (min < 1) return 'Just now';
  if (min < 60) return `${min} min ago`;
  const day = (x: number) => new Date(x).toDateString();
  if (day(now) === t.toDateString()) return `Today at ${formatClock(t)}`;
  if (day(now - 86_400_000) === t.toDateString()) return `Yesterday at ${formatClock(t)}`;
  const days = Math.round((now - t.getTime()) / 86_400_000);
  if (days < 7) return `${days} days ago`;
  return formatDate(t);
}

const lower = (s: string) => (/^(Today|Yesterday|Just)/.test(s) ? s[0]!.toLowerCase() + s.slice(1) : s);

const Env = ({ children }: { children: ReactNode }) => <span className="envvar">{children}</span>;

function ReadOnly({ icon, children }: { icon: ReactNode; children: ReactNode }) {
  return (
    <span className="ro">
      {icon}
      {children}
    </span>
  );
}

const initials = (name: string) =>
  name
    .split(/[\s._-]+/)
    .filter(Boolean)
    .slice(0, 2)
    .map((w) => w[0]!.toUpperCase())
    .join('') || name.slice(0, 2).toUpperCase();

function SignIn({ s }: { s: SecuritySettings }) {
  const local = s.authMode === 'disabled_for_local';
  const modes = [
    {
      on: !local,
      icon: ShieldCheck,
      title: 'Login page on every network',
      text: 'Every browser and app signs in, on the internet and on your LAN alike. Recommended.',
      env: 'GONEMA_AUTH=enabled',
    },
    {
      on: local,
      icon: Router,
      title: 'Skip it on the local network',
      text: 'Direct connections from this machine or your LAN get in without signing in. Unsafe with published Docker ports.',
      env: 'GONEMA_AUTH=disabled_for_local',
    },
  ];
  return (
    <Panel
      id="sec-signin"
      title={
        <>
          <Lock className="i" aria-hidden="true" />
          Sign-in
        </>
      }
      aside={<ReadOnly icon={<Terminal className="i" aria-hidden="true" />}>Set where Gonema starts</ReadOnly>}
    >
      <ul className="modes" aria-label="Sign-in modes">
        {modes.map((m) => {
          const I = m.icon;
          return (
            <li key={m.env} className={m.on ? 'mode on' : 'mode'} aria-current={m.on ? 'true' : undefined}>
              <span className="ic" aria-hidden="true">
                <I className="i" />
              </span>
              <div>
                <b>
                  {m.title}
                  {m.on && <span className="now">In use</span>}
                </b>
                {m.text}
                <div>
                  <Env>{m.env}</Env>
                </div>
              </div>
            </li>
          );
        })}
      </ul>
      <div className="acct">
        {s.user ? (
          <>
            <span className="avatar" aria-hidden="true">
              {initials(s.user.username)}
            </span>
            <span className="who">
              {s.user.username}
              <small>
                The admin account · created {formatDate(s.user.createdAt)}
                {s.user.lastLoginAt ? ` · last sign-in ${lower(sinceWhen(s.user.lastLoginAt))}` : ''}
              </small>
            </span>
          </>
        ) : (
          <>
            <span className="avatar" aria-hidden="true">
              ?
            </span>
            <span className="who">
              No account yet
              <small>
                <Link to="/setup" search={{ step: 'account' }} className="link">
                  Create it from the setup
                </Link>
              </small>
            </span>
          </>
        )}
        <span className="t-caption t-muted">One account, no roles</span>
      </div>
    </Panel>
  );
}

function PasswordPanel({ hasAccount }: { hasAccount: boolean }) {
  const qc = useQueryClient();
  const [current, setCurrent] = useState('');
  const [next, setNext] = useState('');
  const [newKeyToo, setNewKeyToo] = useState(false);
  // shown once after a change with "also make a new API key", forgotten on the next action
  const [newKey, setNewKey] = useState<string | null>(null);
  const [errors, setErrors] = useState<{ current?: string; next?: string; form?: string }>({});
  const [blockedUntil, setBlockedUntil] = useState<number | null>(null);
  const left = useSecondsLeft(blockedUntil);
  const m = useMutation({
    mutationFn: () => changePassword(current, next, newKeyToo),
    onSuccess: (r) => {
      setCurrent('');
      setNext('');
      setErrors({});
      setNewKeyToo(false);
      setNewKey(r.apiKey ?? null);
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
    setNewKey(null);
    const e: typeof errors = {};
    if (!current) e.current = 'Type the current password.';
    if (next.length < MIN_PASSWORD) e.next = `Use at least ${MIN_PASSWORD} characters.`;
    else if (next.length > MAX_PASSWORD) e.next = `Use at most ${MAX_PASSWORD} characters.`;
    else if (next === current) e.next = 'The new password must differ from the current one.';
    setErrors(e);
    if (!e.current && !e.next) m.mutate();
  };
  const blocked = left > 0;
  return (
    <Panel id="sec-password" title="Change the password">
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
            aside={<StrengthLabel password={next} />}
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
          <Checkbox
            checked={newKeyToo}
            onCheckedChange={setNewKeyToo}
            labelClassName="wrap"
            label={
              <span>
                Also make a new API key
                <small>
                  Wise if the password may have leaked. The new key is shown once; apps using the old one must be
                  updated.
                </small>
              </span>
            }
          />
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
          {newKey && <ApiKeyReveal apiKey={newKey} />}
        </form>
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
  const withAccount =
    k.createdAt && s.user && Math.abs(new Date(k.createdAt).getTime() - new Date(s.user.createdAt).getTime()) < 60_000;
  const masked = (
    <>
      gnm_<span className="dots">••••••••</span>
      {k.last4 ?? ''}
    </>
  );
  return (
    <Panel
      id="sec-apikey"
      title={
        <>
          API key <span className="sub">for apps and scripts</span>
        </>
      }
    >
      <div className="keycard">
        <div className="k">
          <KeyRound className="i" aria-hidden="true" />
          {k.present ? (
            <span className="val">{masked}</span>
          ) : (
            <span className="val none">No key yet</span>
          )}
          <Button
            size="sm"
            variant="ghost"
            icon={<RefreshCw className="i" aria-hidden="true" />}
            disabled={!s.user}
            onClick={() => setOpen(true)}
          >
            {k.present ? 'Regenerate…' : 'Make a key…'}
          </Button>
        </div>
        {k.present && (
          <dl className="kv">
            <dt>Made</dt>
            <dd>
              {k.createdAt ? formatDate(k.createdAt) : '—'}
              {withAccount ? ', with the account' : ''}
            </dd>
            <dt>Last used</dt>
            <dd>{k.lastUsedAt ? sinceWhen(k.lastUsedAt) : 'Never'}</dd>
          </dl>
        )}
      </div>
      <div className="hdr">
        <span className="field-label">Apps send it in a header</span>
        <pre>
          {`curl ${location.origin}/api/v1/movies \\\n     -H "`}
          <b>X-Api-Key: gnm_…</b>
          {'"'}
        </pre>
        <span className="no">
          <Info className="i" aria-hidden="true" />
          <span>
            Never in the address (<span className="t-mono-sm">?apikey=</span> is refused): it would end up in proxy logs
            and browser history. Gonema keeps only a fingerprint, so the full key is shown once, when it is made.
          </span>
        </span>
      </div>
      <Dialog
        open={open}
        onOpenChange={(o) => (o ? setOpen(true) : close())}
        className="key-dialog"
        icon={<KeyRound className="i i-lg" aria-hidden="true" />}
        title={key ? 'Your new API key' : k.present ? 'Make a new API key?' : 'Make an API key?'}
        description={
          key
            ? 'The previous key no longer works. Put this one in the apps that used it.'
            : k.present
              ? 'The current key stops working at once. Every app that uses it needs the new one.'
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
              <Button
                variant="danger"
                loading={m.isPending}
                icon={<RefreshCw className="i" aria-hidden="true" />}
                onClick={() => m.mutate()}
              >
                {k.present ? 'Replace the key' : 'Make the key'}
              </Button>
            </>
          )
        }
      >
        {key ? (
          <ApiKeyReveal apiKey={key} />
        ) : (
          <>
            {k.present && (
              <div className="now-key">
                <KeyRound className="i i-sm" aria-hidden="true" />
                Current key{' '}
                <span className="t-mono-sm">
                  gnm_••••••••{k.last4}
                </span>
                <span className="t-caption t-muted">
                  {k.lastUsedAt ? `used ${lower(sinceWhen(k.lastUsedAt))}` : 'never used'}
                </span>
              </div>
            )}
            <ul className="key-points">
              <li>
                The new key is shown <b>once</b>, right after it is made. Copy it before closing.
              </li>
              <li>Browsers stay signed in: sessions don’t use the key.</li>
            </ul>
          </>
        )}
      </Dialog>
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
      title: `Revoked ${deviceOf(x.userAgent)}`,
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
    <Panel
      id="sec-sessions"
      title={
        <>
          Signed-in browsers <span className="sub">{list.length}</span>
        </>
      }
      aside={<span className="t-caption t-muted">A session ends after 14 days unused, 30 days at most</span>}
    >
      {!list.length ? (
        <p className="explain">
          {s.method === 'local'
            ? 'This browser uses the local access (no session). Browsers that sign in appear here.'
            : 'No other browser is signed in.'}
        </p>
      ) : (
        <>
          <div className="sess-hd" aria-hidden="true">
            <span />
            <span>Device</span>
            <span>Address</span>
            <span>Last seen</span>
            <span />
          </div>
          <ul className="sess" aria-label="Signed-in browsers">
            {list.map((x) => {
              const name = deviceOf(x.userAgent);
              const net = networkOf(x.ip);
              return (
                <li key={x.id}>
                  <span className="dev" aria-hidden="true">
                    <DeviceIcon ua={x.userAgent} />
                  </span>
                  <span>
                    <span className="nm">
                      {name}
                      {x.current && <span className="this">This browser</span>}
                    </span>
                    <span className="meta">Signed in {lower(sinceWhen(x.createdAt))}</span>
                  </span>
                  <span className="ip">
                    {x.ip || '—'}
                    {net && <small>{net}</small>}
                  </span>
                  <span className="seen">
                    {x.current ? <span className="live">Active now</span> : sinceWhen(x.lastSeenAt)}
                  </span>
                  <span className="act">
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
                      <Button size="sm" variant="ghost" onClick={() => revoke(x)} aria-label={`Revoke ${name}, ${x.ip}`}>
                        Revoke
                      </Button>
                    )}
                  </span>
                </li>
              );
            })}
          </ul>
        </>
      )}
      <p className="explain">
        Revoke signs that browser out at once (Undo for {Math.round(TOAST_DURATION / 1000)} s). Changing the password
        signs out every browser but this one.
      </p>
    </Panel>
  );
}

const Allowed = () => (
  <span className="status" data-status="available">
    <CircleCheck className="i" aria-hidden="true" />
    Allowed
  </span>
);
const Blocked = () => (
  <span className="status" data-status="unmonitored">
    <Ban className="i" aria-hidden="true" />
    Blocked
  </span>
);
const yes = (on: boolean) => (on ? <Allowed /> : <Blocked />);

function Outbound({ s }: { s: SecuritySettings }) {
  const o = s.outbound;
  return (
    <Panel
      id="sec-outbound"
      title={
        <>
          <ShieldCheck className="i" aria-hidden="true" />
          Outbound connections
        </>
      }
      aside={<ReadOnly icon={<Lock className="i" aria-hidden="true" />}>Read only</ReadOnly>}
    >
      <div className="why">
        <ShieldCheck className="i" aria-hidden="true" />
        <span>
          <b>Gonema checks every address it connects to, after the name is resolved.</b> A URL typed into a connection,
          or a link handed over by an indexer, cannot reach a place on your network it has no business with.
        </span>
      </div>
      <table className="policy">
        <caption className="sr-only">Where each kind of connection may go</caption>
        <thead>
          <tr>
            <th scope="col">Connection</th>
            <th scope="col">
              <Globe className="i" aria-hidden="true" />
              Internet
            </th>
            <th scope="col">
              <Router className="i" aria-hidden="true" />
              Local network
            </th>
            <th scope="col">
              <Server className="i" aria-hidden="true" />
              This machine
            </th>
          </tr>
        </thead>
        <tbody>
          <tr>
            <th scope="row">
              Integrations
              <small>Indexers, download clients, media servers, ntfy, Gotify, Telegram, e-mail</small>
            </th>
            <td>
              <Allowed />
            </td>
            <td>
              {yes(o.integration.allowPrivate)}
              <div>
                <Env>GONEMA_OUTBOUND_ALLOW_PRIVATE</Env>
              </div>
            </td>
            <td>
              {yes(o.integration.allowLoopback)}
              <div>
                <Env>GONEMA_OUTBOUND_ALLOW_LOOPBACK</Env>
              </div>
            </td>
          </tr>
          <tr>
            <th scope="row">
              Webhooks
              <small>Generic webhook, Discord, Slack</small>
            </th>
            <td>
              <Allowed />
            </td>
            <td>
              {yes(o.webhook.allowPrivate)}
              <div>
                <Env>GONEMA_WEBHOOK_ALLOW_PRIVATE</Env>
              </div>
            </td>
            <td>
              {yes(o.webhook.allowLoopback)}
              <span className="fixed">Same switch as the local network</span>
            </td>
          </tr>
          <tr>
            <th scope="row">
              TMDB and images
              <small>Film data, posters and backdrops</small>
            </th>
            <td>
              <Allowed />
            </td>
            <td>
              <Blocked />
              <span className="fixed">Always: public only</span>
            </td>
            <td>
              <Blocked />
              <span className="fixed">Always: public only</span>
            </td>
          </tr>
        </tbody>
      </table>
      {!!o.alwaysBlocked?.length && (
        <div className="never-wrap">
          <span className="field-label" id="sec-never">
            Never contacted, whatever the settings
          </span>
          <ul className="never" aria-labelledby="sec-never">
            {o.alwaysBlocked.map((b) => (
              <li key={b} className="tag plain">
                {b}
              </li>
            ))}
          </ul>
        </div>
      )}
      <p className="explain">
        Your download client on <span className="t-mono-sm">127.0.0.1</span> or a NAS at{' '}
        <span className="t-mono-sm">192.168.x</span> is fine. A webhook has no reason to aim at your network, so it may
        only reach the internet. Links from a public indexer may only lead to public addresses.
      </p>
    </Panel>
  );
}

function Values({ list, none }: { list: readonly string[] | null | undefined; none: string }) {
  return list?.length ? (
    list.map((v) => (
      <span key={v} className="v">
        {v}
      </span>
    ))
  ) : (
    <span className="none">{none}</span>
  );
}

function ReverseProxy({ s }: { s: SecuritySettings }) {
  const cookie =
    s.cookieSecure === 'auto' ? 'auto · on over HTTPS' : s.cookieSecure === 'true' ? 'always' : 'never (plain HTTP)';
  const rows: { name: string; help: string; values: ReactNode; env: string }[] = [
    {
      name: 'Allowed host names',
      help: 'Names a browser may use to reach Gonema, besides IP addresses and localhost.',
      values: <Values list={s.allowedHosts} none="IP addresses and localhost only" />,
      env: 'GONEMA_ALLOWED_HOSTS',
    },
    {
      name: 'Trusted proxies',
      help: 'Their forwarded headers are believed, so the real client address shows above.',
      values: <Values list={s.trustedProxies} none="None: forwarded headers are ignored" />,
      env: 'GONEMA_TRUSTED_PROXIES',
    },
    {
      name: 'Trusted origins',
      help: 'Public addresses allowed to send changes, when the proxy rewrites the host.',
      values: <Values list={s.trustedOrigins} none="None besides this address" />,
      env: 'GONEMA_TRUSTED_ORIGINS',
    },
    {
      name: 'Secure cookie',
      help: 'Sent over HTTPS only.',
      values: <span className="v">{cookie}</span>,
      env: 'GONEMA_COOKIE_SECURE',
    },
  ];
  return (
    <Panel
      id="sec-net"
      title={
        <>
          <Network className="i" aria-hidden="true" />
          Reverse proxy and host names
        </>
      }
      aside={<ReadOnly icon={<Lock className="i" aria-hidden="true" />}>Read only</ReadOnly>}
    >
      <ul className="netlist">
        {rows.map((r) => (
          <li key={r.env}>
            <span className="nm">
              {r.name}
              <small>{r.help}</small>
            </span>
            <span className="vals">
              {r.values}
              <Env>{r.env}</Env>
            </span>
          </li>
        ))}
      </ul>
      <p className="explain">Set these where Gonema starts (environment variables or flags), then restart it.</p>
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
    <div className="sys-stack">
      <SignIn s={s} />
      <div className="sys-cols">
        <PasswordPanel hasAccount={!!s.user} />
        <ApiKeyPanel s={s} />
      </div>
      <Sessions s={s} />
      <Outbound s={s} />
      <ReverseProxy s={s} />
    </div>
  );
}

export default SecuritySection;
