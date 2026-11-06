import '@/styles/lot3.css';
import '@/styles/phase8.css';
import { useQueryClient } from '@tanstack/react-query';
import { getRouteApi, useNavigate } from '@tanstack/react-router';
import { ChevronRight, Clock, Info, LogIn, TriangleAlert } from 'lucide-react';
import { type FormEvent, useEffect, useId, useRef, useState } from 'react';
import { authStatusQuery, safeRedirect } from '@/api/auth';
import { api } from '@/api/client';
import {
  AtmosphereWall,
  formatCountdown,
  PasswordInput,
  retryAfterSeconds,
  useSecondsLeft,
} from '@/components/auth/AuthParts';
import { Mark, Wordmark } from '@/components/shell/Logo';
import { ThemeToggle } from '@/components/shell/Topbar';
import { Button } from '@/components/ui/Button';
import { Notice } from '@/components/ui/Feedback';
import { Field, Input } from '@/components/ui/Field';

const route = getRouteApi('/login');

type Problem = { kind: 'wrong' | 'invalid' | 'offline' | 'server'; title: string; body?: string } | null;

/** Circumference of the countdown ring (r = 26). */
const RING = 163.4;

/** Sign-in page (login.html: ?state=default | error | throttled). */
export function LoginScreen() {
  const { redirect } = route.useSearch();
  const navigate = useNavigate();
  const qc = useQueryClient();
  const id = useId();
  const [username, setUsername] = useState('');
  const [password, setPassword] = useState('');
  const [busy, setBusy] = useState(false);
  const [problem, setProblem] = useState<Problem>(null);
  const [missing, setMissing] = useState<{ username?: string; password?: string }>({});
  const [blocked, setBlocked] = useState<{ until: number; total: number } | null>(null);
  const [helpOpen, setHelpOpen] = useState(false);
  const left = useSecondsLeft(blocked?.until ?? null);
  const waiting = blocked !== null && left > 0;
  const pwRef = useRef<HTMLInputElement>(null);
  const userRef = useRef<HTMLInputElement>(null);

  useEffect(() => {
    document.title = 'Sign in · Gonema';
    userRef.current?.focus();
  }, []);

  // the wait is over: the password field comes back, focus goes to it
  const wasWaiting = useRef(false);
  useEffect(() => {
    if (wasWaiting.current && !waiting) pwRef.current?.focus();
    wasWaiting.current = waiting;
  }, [waiting]);

  const submit = async (ev: FormEvent) => {
    ev.preventDefault();
    if (busy || waiting) return;
    const m: typeof missing = {};
    if (!username.trim()) m.username = 'Type your username.';
    if (!password) m.password = 'Type your password.';
    setMissing(m);
    if (m.username || m.password) {
      (m.username ? userRef : pwRef).current?.focus();
      return;
    }
    setBusy(true);
    setProblem(null);
    try {
      const { response, error } = await api.POST('/auth/login', { body: { username: username.trim(), password } });
      if (response.ok) {
        // Every cached answer belongs to the previous state: start clean, then go back where the user was.
        qc.clear();
        await qc.fetchQuery(authStatusQuery);
        void navigate({ href: safeRedirect(redirect) ?? '/', replace: true });
        return;
      }
      const detail = (error as { detail?: string } | undefined)?.detail;
      if (response.status === 401) {
        setProblem({
          kind: 'wrong',
          title: 'That username and password don’t match.',
          body: 'Check both, then try again. The password field was cleared.',
        });
        setPassword('');
        setHelpOpen(true);
        // after React re-enables and clears the field
        requestAnimationFrame(() => pwRef.current?.focus());
      } else if (response.status === 429) {
        const wait = retryAfterSeconds(response);
        setBlocked({ until: Date.now() + wait * 1000, total: wait });
        setPassword('');
      } else if (response.status === 409) {
        void navigate({ to: '/setup', search: { step: 'account' }, replace: true });
      } else if (response.status === 422) {
        setProblem({ kind: 'invalid', title: detail ?? 'Check the username and the password.' });
      } else {
        setProblem({ kind: 'server', title: detail ?? `The server answered ${response.status}. Try again in a moment.` });
      }
    } catch {
      setProblem({
        kind: 'offline',
        title: 'Gonema could not be reached.',
        body: 'Check that the server is running, then try again.',
      });
    } finally {
      setBusy(false);
    }
  };

  const minutes = blocked ? Math.max(1, Math.ceil(blocked.total / 60)) : 1;
  const fraction = blocked && blocked.total > 0 ? left / blocked.total : 0;
  return (
    <div className="ob-page login-page">
      <AtmosphereWall />
      <header className="ob-top">
        <span className="brand" role="img" aria-label="Gonema">
          <Mark />
          <Wordmark />
        </span>
        <div className="spacer" />
        <ThemeToggle />
      </header>
      <div className="login-stage">
        <main id="main" className="login glass-2" aria-labelledby={`${id}-h`}>
          <div>
            <span className="t-overline">Sign in</span>
            <h1 id={`${id}-h`}>Back to the booth</h1>
            <p className="intro">Sign in to run your collection on this server.</p>
          </div>
          {waiting ? (
            <div className="login-wait" role="status">
              <div className="ring" aria-hidden="true">
                <svg viewBox="0 0 56 56">
                  <circle className="bg" cx="28" cy="28" r="26" />
                  <circle className="fg" cx="28" cy="28" r="26" style={{ strokeDashoffset: RING * (1 - fraction) }} />
                </svg>
                <span className="t">{formatCountdown(left)}</span>
              </div>
              <div>
                <b>Sign-in is paused for a few minutes.</b>
                <span className="d">
                  Too many wrong attempts, so Gonema slows guessers down. Nothing is locked: the form comes back on its
                  own.
                </span>
                <span className="sr-only">
                  You can try again in about {minutes} {minutes === 1 ? 'minute' : 'minutes'}.
                </span>
              </div>
            </div>
          ) : (
            problem && (
              <Notice
                tone="failed"
                icon={<TriangleAlert className="i" aria-hidden="true" />}
                title={problem.title}
                role="alert"
              >
                {problem.body}
              </Notice>
            )
          )}
          {blocked !== null && !waiting && (
            <p className="sr-only" role="status">
              You can sign in again.
            </p>
          )}
          <form onSubmit={submit} noValidate className="login-form">
            <Field label="Username" error={missing.username}>
              {({ id: fid, describedBy, invalid }) => (
                <Input
                  ref={userRef}
                  id={fid}
                  name="username"
                  autoComplete="username"
                  autoCapitalize="none"
                  spellCheck={false}
                  aria-describedby={describedBy}
                  invalid={invalid}
                  value={username}
                  onChange={(ev) => setUsername(ev.target.value)}
                />
              )}
            </Field>
            <Field label="Password" error={missing.password}>
              {({ id: fid, describedBy, invalid }) => (
                <PasswordInput
                  ref={pwRef}
                  id={fid}
                  name="password"
                  autoComplete="current-password"
                  aria-describedby={describedBy}
                  invalid={invalid}
                  disabled={waiting}
                  value={password}
                  onChange={(ev) => setPassword(ev.target.value)}
                />
              )}
            </Field>
            <Button
              type="submit"
              variant="primary"
              size="lg"
              loading={busy}
              disabled={waiting}
              icon={
                waiting ? <Clock className="i" aria-hidden="true" /> : <LogIn className="i" aria-hidden="true" />
              }
              className="login-submit"
            >
              {waiting ? (
                <>
                  Try again in <span className="countdown">{formatCountdown(left)}</span>
                </>
              ) : (
                'Sign in'
              )}
            </Button>
          </form>
          <details className="login-help" open={helpOpen} onToggle={(ev) => setHelpOpen(ev.currentTarget.open)}>
            <summary>
              <ChevronRight className="i" aria-hidden="true" />
              Forgot the password?
            </summary>
            <div className="help-bd">
              <p>Gonema has one admin account and no reset by e-mail. Whoever runs the server can make a new one:</p>
              <ol>
                <li>Stop Gonema.</li>
                <li>
                  Remove the account from the <code>users</code> table of <code>gonema.db</code>.
                </li>
                <li>Start it again: this page offers to create the account anew.</li>
              </ol>
              <span className="keep">
                <Info className="i" aria-hidden="true" />
                Films, settings and connections are kept. The API key goes with the account: put the new one in the
                apps that use it.
              </span>
            </div>
          </details>
        </main>
      </div>
    </div>
  );
}
