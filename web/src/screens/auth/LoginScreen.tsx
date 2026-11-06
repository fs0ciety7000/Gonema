import '@/styles/lot3.css';
import '@/styles/phase8.css';
import { useQueryClient } from '@tanstack/react-query';
import { getRouteApi, useNavigate } from '@tanstack/react-router';
import { Clock, LogIn, TriangleAlert } from 'lucide-react';
import { type FormEvent, useEffect, useId, useRef, useState } from 'react';
import { authStatusQuery, safeRedirect } from '@/api/auth';
import { api } from '@/api/client';
import {
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

type Problem = { kind: 'wrong' | 'invalid' | 'offline' | 'server'; text: string } | null;

/** Sign-in page (no mockup: derived from onboarding.html, see e2e/fidelity.md). */
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
  const [blockedUntil, setBlockedUntil] = useState<number | null>(null);
  const [blockedMinutes, setBlockedMinutes] = useState(1);
  const left = useSecondsLeft(blockedUntil);
  const blocked = blockedUntil !== null && left > 0;
  const pwRef = useRef<HTMLInputElement>(null);
  const userRef = useRef<HTMLInputElement>(null);

  useEffect(() => {
    document.title = 'Sign in · Gonema';
    userRef.current?.focus();
  }, []);

  const submit = async (ev: FormEvent) => {
    ev.preventDefault();
    if (busy || blocked) return;
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
        setProblem({ kind: 'wrong', text: 'That username and password don’t match. Check both, then try again.' });
        setPassword('');
        pwRef.current?.focus();
      } else if (response.status === 429) {
        const wait = retryAfterSeconds(response);
        setBlockedMinutes(Math.max(1, Math.ceil(wait / 60)));
        setBlockedUntil(Date.now() + wait * 1000);
        setPassword('');
      } else if (response.status === 409) {
        void navigate({ to: '/setup', search: { step: 'account' }, replace: true });
      } else if (response.status === 422) {
        setProblem({ kind: 'invalid', text: detail ?? 'Check the username and the password.' });
      } else {
        setProblem({ kind: 'server', text: detail ?? `The server answered ${response.status}. Try again in a moment.` });
      }
    } catch {
      setProblem({ kind: 'offline', text: 'Gonema could not be reached. Check that the server is running, then try again.' });
    } finally {
      setBusy(false);
    }
  };

  return (
    <div className="ob-page login-page">
      <header className="ob-top">
        <span className="brand" role="img" aria-label="Gonema">
          <Mark />
          <Wordmark />
        </span>
        <div className="spacer" />
        <ThemeToggle />
      </header>
      <main id="main" className="login glass-2" aria-labelledby={`${id}-h`}>
        <form onSubmit={submit} noValidate className="login-form">
          <div>
            <span className="t-overline">Sign in</span>
            <h1 id={`${id}-h`}>Back to the booth</h1>
            <p className="intro">Sign in to run your collection on this server.</p>
          </div>
          {blocked ? (
            <Notice tone="missing" icon={<Clock className="i" aria-hidden="true" />} title="Too many attempts." role="status">
              Gonema pauses sign-in for a few minutes to keep guessers out. You can try again in{' '}
              <span className="t-mono-sm countdown" aria-hidden="true">
                {formatCountdown(left)}
              </span>
              <span className="sr-only">
                about {blockedMinutes} {blockedMinutes === 1 ? 'minute' : 'minutes'}
              </span>
              .
            </Notice>
          ) : (
            problem && (
              <Notice tone="failed" icon={<TriangleAlert className="i" aria-hidden="true" />} title={problem.text} role="alert" />
            )
          )}
          {blockedUntil !== null && !blocked && (
            <p className="sr-only" role="status">
              You can sign in again.
            </p>
          )}
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
            disabled={blocked}
            icon={<LogIn className="i" aria-hidden="true" />}
            className="login-submit"
          >
            Sign in
          </Button>
          <details className="login-help">
            <summary>Forgot the password?</summary>
            <p>
              Gonema has one admin account and no reset by e-mail. On the server, stop Gonema and remove the account
              from the <span className="t-mono-sm">users</span> table of the database, then start it again: this page
              offers to create the account anew. Films, settings and connections are kept; the API key must be
              replaced in the apps that use it.
            </p>
          </details>
        </form>
      </main>
    </div>
  );
}
