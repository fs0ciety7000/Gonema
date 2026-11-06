import '@/styles/lot3.css';
import { useMutation, useQuery, useQueryClient } from '@tanstack/react-query';
import { Link } from '@tanstack/react-router';
import { CircleCheck, CircleSlash, CloudOff, Info, RefreshCw, TriangleAlert } from 'lucide-react';
import { useEffect, useState } from 'react';
import { ApiError } from '@/api/client';
import {
  clientsQuery,
  healthChecksQuery,
  healthIssuesQuery,
  indexerApi,
  indexersQuery,
  lot3Keys,
  runHealthChecks,
} from '@/api/lot3';
import { dismissJournal, journalQuery, opsKeys } from '@/api/ops';
import { keys } from '@/api/queries';
import type { HealthIssue, ImportJournalEntry, Indexer } from '@/api/schema';
import { SearchField, ThemeToggle, Topbar } from '@/components/shell/Topbar';
import { StatusText } from '@/components/ui/Badges';
import { Button, buttonClass } from '@/components/ui/Button';
import { Dialog } from '@/components/ui/Dialog';
import { EmptyState, Skeleton } from '@/components/ui/Feedback';
import { toast } from '@/components/ui/Toast';
import { formatClock, formatWhen } from '@/lib/format';

const SEV: Record<HealthIssue['severity'], { tone: 'failed' | 'missing' | 'info'; label: string }> = {
  error: { tone: 'failed', label: 'Needs action' },
  warning: { tone: 'missing', label: 'Warning' },
  info: { tone: 'info', label: 'For information' },
};

/** Health links point at settings pages ("/settings/indexers", "/queue"); anything else is not linked. */
export function issueLink(link: string | undefined): string | null {
  if (!link) return null;
  return /^\/(settings(\/[a-z-]+){0,2}|queue|history|wanted|blocklist)$/.test(link) ? link : null;
}

const CHECK_NAME: Record<string, string> = {
  indexers: 'Indexers',
  clients: 'Download clients',
  downloadclients: 'Download clients',
  recyclebin: 'Recycle bin',
  downloads: 'Downloads',
  tmdb: 'TMDB',
  metadata: 'TMDB',
  clock: 'Server clock',
  rootfolders: 'Root folders',
  root_folders: 'Root folders',
  disk: 'Disk space',
  backups: 'Backups',
  imports: 'Interrupted imports',
  recycle_bin: 'Recycle bin',
  mediaservers: 'Media servers',
  notifications: 'Notifications',
};
const checkName = (id: string) => CHECK_NAME[id] ?? id.replace(/[._-]+/g, ' ').replace(/^\w/, (c) => c.toUpperCase());

/** Journal id of an interrupted-import issue ("import.interrupted:42" → 42). */
export function journalIdOf(issueId: string): number | null {
  const m = /^import\.interrupted:(\d+)$/.exec(issueId);
  return m ? Number(m[1]) : null;
}

const MODE: Record<ImportJournalEntry['mode'], string> = { move: 'Move', copy: 'Copy', hardlink: 'Hardlink' };

/** "Mark as handled": the user says the files are where they belong. Changes no file. */
function HandledDialog({
  entry,
  onClose,
}: {
  entry: { id: number; source?: string; destination?: string } | null;
  onClose: () => void;
}) {
  const qc = useQueryClient();
  const m = useMutation({
    mutationFn: () => dismissJournal(entry!.id),
    onSuccess: () => {
      void qc.invalidateQueries({ queryKey: opsKeys.journal });
      void qc.invalidateQueries({ queryKey: keys.health });
      toast.show({ title: 'Marked as handled.', body: 'Gonema changed no file.' });
      onClose();
    },
    onError: (e) => toast.error('Still listed', e instanceof ApiError ? e.detail : String(e)),
  });
  return (
    <Dialog
      open={!!entry}
      onOpenChange={(o) => !o && onClose()}
      title="Mark this import as handled?"
      description="Do this once the file is where you want it (moved by hand, rescanned or imported manually). Gonema changes no file: it only stops listing this import."
      actions={
        <>
          <Button variant="ghost" onClick={onClose}>
            Not yet
          </Button>
          <Button variant="primary" loading={m.isPending} onClick={() => m.mutate()}>
            Mark as handled
          </Button>
        </>
      }
    >
      {entry?.source && (
        <dl className="journal-paths">
          <dt>From</dt>
          <dd className="t-mono-sm">{entry.source}</dd>
          <dt>To</dt>
          <dd className="t-mono-sm">{entry.destination}</dd>
        </dl>
      )}
    </Dialog>
  );
}

function IssueCard({ issue, onHandled }: { issue: HealthIssue; onHandled: (id: number) => void }) {
  const journalId = journalIdOf(issue.id);
  const s = SEV[issue.severity] ?? SEV.info;
  const link = issueLink(issue.link);
  const Icon = issue.severity === 'error' ? TriangleAlert : issue.severity === 'warning' ? CloudOff : Info;
  return (
    <li className="notice issue" data-tone={s.tone}>
      <Icon className="i" aria-hidden="true" />
      <div className="grow">
        <div className="ntitle">
          {issue.title}
          <span className="sr-only">, {s.label}</span>
        </div>
        <div className="nbody">{issue.message}</div>
        {!!issue.fix?.length && (
          <ul className="fixes">
            {issue.fix.map((f) => (
              <li key={f}>{f}</li>
            ))}
          </ul>
        )}
        <span className="t-caption t-secondary">
          {issue.source && `${issue.source} · `}since {formatWhen(issue.since)}
        </span>
      </div>
      {journalId !== null ? (
        <Button size="sm" onClick={() => onHandled(journalId)}>
          Mark as handled…
        </Button>
      ) : (
        link && (
          <Link className={buttonClass('secondary', 'sm')} to={link as never}>
            {link === '/settings/backups' ? 'Open backups' : 'Open settings'}
          </Link>
        )
      )}
    </li>
  );
}

function IndexerRow({ x }: { x: Indexer }) {
  const qc = useQueryClient();
  const test = useMutation({
    mutationFn: () => indexerApi.testSaved(x.id),
    onSuccess: (r) => {
      toast.show({
        tone: r.ok ? 'neutral' : 'error',
        title: r.ok ? `${x.name} answered.` : `${x.name} failed the test.`,
        body: r.message,
      });
      void qc.invalidateQueries({ queryKey: lot3Keys.indexers });
    },
    onError: (e) => toast.error(`${x.name} could not be tested`, e instanceof ApiError ? e.detail : String(e)),
  });
  const h = x.health;
  const state = h?.state ?? 'ok';
  return (
    <li>
      <span>
        {x.name}
        <small>
          {[
            x.kind === 'torznab' ? 'Torznab' : 'Newznab',
            `priority ${x.priority}`,
            state === 'disabled' && h?.disabledUntil ? `paused until ${formatClock(new Date(h.disabledUntil))}` : '',
            h?.lastOkAt ? `last answer ${formatWhen(h.lastOkAt)}` : '',
          ]
            .filter(Boolean)
            .join(' · ')}
        </small>
        {h?.lastError && state !== 'ok' && <small className="err">{h.lastError}</small>}
      </span>
      {state === 'ok' ? (
        <StatusText status="available">OK</StatusText>
      ) : state === 'disabled' ? (
        <StatusText status="missing">Paused</StatusText>
      ) : (
        <StatusText status="failed">{`${h?.failureCount ?? 0} failures`}</StatusText>
      )}
      <Button
        size="sm"
        variant="ghost"
        loading={test.isPending}
        onClick={() => test.mutate()}
        aria-label={`Test ${x.name}`}
      >
        Test
      </Button>
    </li>
  );
}

export function HealthScreen() {
  const qc = useQueryClient();
  const issues = useQuery(healthIssuesQuery);
  const checks = useQuery(healthChecksQuery);
  const indexers = useQuery(indexersQuery);
  const clients = useQuery(clientsQuery);
  const journal = useQuery(journalQuery);
  const [handling, setHandling] = useState<number | null>(null);
  const handlingEntry = handling === null ? null : (journal.data?.find((j) => j.id === handling) ?? { id: handling });
  useEffect(() => {
    document.title = 'Health · Gonema';
    if (location.hash === '#indexers') document.getElementById('indexers')?.scrollIntoView();
  }, []);
  const run = useMutation({
    mutationFn: runHealthChecks,
    onSuccess: (list) => {
      qc.setQueryData(keys.health, list);
      void qc.invalidateQueries({ queryKey: lot3Keys.healthChecks });
      toast.show({
        title: list.length ? `${list.length} ${list.length === 1 ? 'issue' : 'issues'} found.` : 'All clear.',
        body: list.length ? 'Each one says what to do.' : 'Every health check passed.',
      });
    },
    onError: (e) => toast.error('The checks could not run', e instanceof ApiError ? e.detail : String(e)),
  });
  const list = [...(issues.data ?? [])].sort(
    (a, b) => ['error', 'warning', 'info'].indexOf(a.severity) - ['error', 'warning', 'info'].indexOf(b.severity),
  );
  return (
    <>
      <Topbar>
        <SearchField />
        <div className="spacer" />
        <ThemeToggle />
      </Topbar>
      <main id="main" className="page health-page" tabIndex={-1}>
        <div className="page-head">
          <div>
            <h1>Health</h1>
            <p className="sub" aria-live="polite">
              {issues.isPending
                ? ' '
                : list.length
                  ? `${list.length} ${list.length === 1 ? 'thing needs' : 'things need'} a look. Everything else runs on its own.`
                  : 'Everything is running. Gonema checks again on its own.'}
            </p>
          </div>
          <Button
            icon={<RefreshCw className="i" aria-hidden="true" />}
            loading={run.isPending}
            onClick={() => run.mutate()}
          >
            Run checks now
          </Button>
        </div>
        <div className="health-grid">
          <section aria-labelledby="iss-h" className="health-issues">
            <h2 id="iss-h" className="sec-h">
              Issues
            </h2>
            {issues.isPending ? (
              <Skeleton className="sk-wanted" />
            ) : list.length ? (
              <ul className="issues">
                {list.map((i) => (
                  <IssueCard key={i.id} issue={i} onHandled={setHandling} />
                ))}
              </ul>
            ) : (
              <EmptyState title="Nothing to fix." headingLevel={3}>
                Indexers, clients, folders and TMDB all answered at the last check.
              </EmptyState>
            )}
          </section>
          <div className="health-side">
            {!!journal.data?.length && (
              <section className="panel" aria-labelledby="journal-h">
                <div className="panel-hd">
                  <h2 id="journal-h">Interrupted imports</h2>
                </div>
                <div className="panel-bd">
                  <p className="t-secondary t-body-sm">
                    A restart cut these imports short. Gonema settled what it could and never deletes files on a guess:
                    check each one, then mark it as handled.
                  </p>
                  <ul className="mini-rows">
                    {journal.data.map((j) => (
                      <li key={j.id}>
                        <span>
                          <span className="t-mono-sm journal-path">{j.destination}</span>
                          <small>
                            {MODE[j.mode] ?? j.mode} from <span className="t-mono-sm">{j.source}</span> ·{' '}
                            {formatWhen(j.updatedAt)}
                          </small>
                          {j.message && <small className="err">{j.message}</small>}
                        </span>
                        <Button size="sm" variant="ghost" onClick={() => setHandling(j.id)}>
                          Mark as handled…
                        </Button>
                      </li>
                    ))}
                  </ul>
                </div>
              </section>
            )}
            <section className="panel" aria-labelledby="indexers">
              <div className="panel-hd">
                <h2 id="indexers" tabIndex={-1}>
                  Indexer health
                </h2>
                <Link className={buttonClass('ghost', 'sm')} to="/settings/$section" params={{ section: 'indexers' }}>
                  Manage
                </Link>
              </div>
              <div className="panel-bd">
                {indexers.data?.length ? (
                  <ul className="mini-rows">
                    {indexers.data.map((x) => (
                      <IndexerRow key={x.id} x={x} />
                    ))}
                  </ul>
                ) : (
                  <p className="t-secondary t-body-sm">
                    {indexers.isPending ? ' ' : 'No indexer yet. Add one so Gonema can look for releases.'}
                  </p>
                )}
              </div>
            </section>
            <section className="panel" aria-labelledby="clients-h">
              <div className="panel-hd">
                <h2 id="clients-h">Download clients</h2>
                <Link
                  className={buttonClass('ghost', 'sm')}
                  to="/settings/$section"
                  params={{ section: 'download-clients' }}
                >
                  Manage
                </Link>
              </div>
              <div className="panel-bd">
                {clients.data?.length ? (
                  <ul className="mini-rows">
                    {clients.data.map((c) => {
                      const failing = c.enabled && (c.health?.failureCount ?? 0) > 0;
                      return (
                        <li key={c.id}>
                          <span>
                            {c.name}
                            <small>
                              {c.kind} · priority {c.priority}
                              {!c.enabled && ' · disabled'}
                            </small>
                            {failing && c.health.lastError && <small className="err">{c.health.lastError}</small>}
                          </span>
                          {!c.enabled ? (
                            <StatusText status="unmonitored">Off</StatusText>
                          ) : failing ? (
                            <StatusText status="failed">Failing</StatusText>
                          ) : (
                            <StatusText status="available">OK</StatusText>
                          )}
                        </li>
                      );
                    })}
                  </ul>
                ) : (
                  <p className="t-secondary t-body-sm">{clients.isPending ? ' ' : 'No download client yet.'}</p>
                )}
              </div>
            </section>
            <section className="panel" aria-labelledby="checks-h">
              <div className="panel-hd">
                <h2 id="checks-h">Checks</h2>
              </div>
              <div className="panel-bd">
                <ul className="mini-rows">
                  {(checks.data ?? []).map((c) => (
                    <li key={c.id}>
                      <span>
                        {checkName(c.id)}
                        <small>
                          last {c.lastRun ? formatWhen(c.lastRun) : 'never'} · next{' '}
                          {c.nextRun ? formatWhen(c.nextRun) : '—'}
                        </small>
                        {c.lastError && <small className="err">{c.lastError}</small>}
                      </span>
                      {c.lastError ? (
                        <span className="status" data-status="failed">
                          <CircleSlash className="i" aria-hidden="true" />
                          Didn’t run
                        </span>
                      ) : c.issues ? (
                        <StatusText status="missing">{`${c.issues} ${c.issues === 1 ? 'issue' : 'issues'}`}</StatusText>
                      ) : (
                        <span className="status" data-status="available">
                          <CircleCheck className="i" aria-hidden="true" />
                          OK
                        </span>
                      )}
                    </li>
                  ))}
                </ul>
              </div>
            </section>
          </div>
        </div>
      </main>
      <HandledDialog entry={handlingEntry} onClose={() => setHandling(null)} />
    </>
  );
}
