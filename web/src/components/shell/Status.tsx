import { CloudOff, FileVideo, FlaskConical, Globe, RefreshCw, Server } from 'lucide-react';
import { Component, type ErrorInfo, type ReactNode, useEffect, useState } from 'react';
import { retryConnectionNow, useConnection } from '@/api/live';
import { useSystemStatus } from '@/api/queries';
import { Button } from '@/components/ui/Button';
import { ProgressBar } from '@/components/ui/Progress';
import { formatClock } from '@/lib/format';

/** Shown whenever the server runs in demo mode (generated sample library). */
export function DemoBanner() {
  const status = useSystemStatus();
  const demo = (status.data as { demo?: boolean } | undefined)?.demo;
  if (!demo) return null;
  return (
    <aside className="app-banner" data-tone="info" aria-label="Demo mode">
      <FlaskConical className="i" aria-hidden="true" />
      <span>
        <strong>Demo mode.</strong>{' '}
        <span className="t-secondary">These films are generated sample data, not a real library.</span>
      </span>
    </aside>
  );
}

/** Small, calm notice while the event stream reconnects (before the full offline card). */
function useCountdown(until: number | null): number {
  const [now, setNow] = useState(Date.now());
  useEffect(() => {
    if (!until) return;
    const t = setInterval(() => setNow(Date.now()), 250);
    return () => clearInterval(t);
  }, [until]);
  return until ? Math.max(0, until - now) : 0;
}

/** Server unreachable (mockups/error.html): the shell stays as the last known layout behind a calm card. */
export function OfflineOverlay({ children }: { children: ReactNode }) {
  const c = useConnection();
  const offline = c.status === 'reconnecting' && c.attempt >= 2;
  const left = useCountdown(offline ? c.nextRetryAt : null);
  const [window0, setWindow0] = useState(0);
  useEffect(() => {
    if (c.nextRetryAt) setWindow0(Math.max(1, c.nextRetryAt - Date.now()));
  }, [c.nextRetryAt]);
  const origin = typeof location !== 'undefined' ? location.origin : '';
  const port = typeof location !== 'undefined' ? location.port || (location.protocol === 'https:' ? '443' : '80') : '';
  return (
    <>
      <div className="offline-ghost" inert={offline || undefined} aria-hidden={offline || undefined}>
        {offline && (
          <div className="app-banner" data-tone="missing">
            <CloudOff className="i" aria-hidden="true" />
            Offline · showing the last known layout
          </div>
        )}
        {children}
      </div>
      {offline && (
        <div className="offline-overlay">
          <section className="offline-card glass-3" role="alert" aria-labelledby="offline-h">
            <span className="ic">
              <CloudOff className="i i-lg" aria-hidden="true" />
            </span>
            <h1 id="offline-h">Can’t reach Gonema</h1>
            <p>
              The server at <span className="t-mono-sm offline-origin">{origin}</span> stopped answering
              {c.lostAt ? ` at ${formatClock(new Date(c.lostAt))}` : ''}. Your downloads keep running in their clients;
              nothing is lost.
            </p>
            <div className="retry" role="status" aria-live="polite">
              <div className="row">
                <span>Reconnecting… attempt {c.attempt}</span>
                <span>{left > 0 ? `next try in ${Math.ceil(left / 1000)} s` : 'trying now'}</span>
              </div>
              <ProgressBar
                value={window0 ? 1 - left / window0 : 0}
                label="Time until the next attempt"
                size="thin"
                className="retry-bar"
              />
            </div>
            <div className="row">
              <Button
                variant="primary"
                icon={<RefreshCw className="i" aria-hidden="true" />}
                onClick={retryConnectionNow}
              >
                Retry now
              </Button>
            </div>
            <ul className="checks">
              <li>
                <Server className="i" aria-hidden="true" />
                <span>
                  Is the container running? <code>docker ps --filter name=gonema</code>
                </span>
              </li>
              <li>
                <FileVideo className="i" aria-hidden="true" />
                <span>
                  The last lines of the server log usually say why it stopped: <code>docker logs --tail 50 gonema</code>
                </span>
              </li>
              <li>
                <Globe className="i" aria-hidden="true" />
                <span>Behind a reverse proxy? Check it still forwards to port {port}.</span>
              </li>
            </ul>
          </section>
        </div>
      )}
    </>
  );
}

// ----------------------------------------------------------------- errors

export function ScreenError({ error, reset }: { error: unknown; reset?: () => void }) {
  const message = error instanceof Error ? error.message : String(error);
  return (
    <main id="main" className="page" tabIndex={-1}>
      <div className="screen-error">
        <div className="notice" data-tone="failed" role="alert">
          <CloudOff className="i" aria-hidden="true" />
          <div className="grow">
            <div className="ntitle">This screen hit a snag.</div>
            <div className="nbody">
              Something broke while drawing it. The server and your downloads are not affected. Try again; if it keeps
              happening, the message below helps a bug report.
            </div>
            <pre className="t-mono-sm error-detail">{message}</pre>
          </div>
          {reset && (
            <Button size="sm" onClick={reset}>
              Try again
            </Button>
          )}
        </div>
      </div>
    </main>
  );
}

type BoundaryState = { error: unknown };

/** Last-resort boundary around the whole app (route errors are handled per route). */
export class ErrorBoundary extends Component<{ children: ReactNode }, BoundaryState> {
  override state: BoundaryState = { error: null };
  static getDerivedStateFromError(error: unknown): BoundaryState {
    return { error };
  }
  override componentDidCatch(error: unknown, info: ErrorInfo) {
    console.error('Gonema UI error', error, info.componentStack);
  }
  override render() {
    if (this.state.error) return <ScreenError error={this.state.error} reset={() => this.setState({ error: null })} />;
    return this.props.children;
  }
}
