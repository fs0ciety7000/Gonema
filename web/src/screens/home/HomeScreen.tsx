import '@/styles/lot3.css';
import { useMutation, useQuery, useQueryClient } from '@tanstack/react-query';
import { Link } from '@tanstack/react-router';
import {
  ArrowDownToLine,
  ChevronRight,
  CircleArrowUp,
  CircleDashed,
  CloudOff,
  Disc3,
  Film,
  type LucideIcon,
  MonitorPlay,
  Sparkles,
  Ticket,
  TriangleAlert,
} from 'lucide-react';
import { type CSSProperties, useEffect, useMemo } from 'react';
import { dashboardQuery, healthChecksQuery, runHealthChecks } from '@/api/lot3';
import { useProgress } from '@/api/progress';
import { keys, useLibrary } from '@/api/queries';
import type { CalendarEvent, Dashboard, DashboardQueueItem, DiskUsage, HistoryRow } from '@/api/schema';
import { AddFilmButton, SearchField, ThemeToggle, Topbar } from '@/components/shell/Topbar';
import { StatusText } from '@/components/ui/Badges';
import { Button, buttonClass } from '@/components/ui/Button';
import { Notice, Skeleton } from '@/components/ui/Feedback';
import { PosterArt } from '@/components/ui/PosterArt';
import { ProgressBar } from '@/components/ui/Progress';
import { toast } from '@/components/ui/toastStore';
import { formatBytes, formatClock, formatEta, formatNumber, formatSpeed } from '@/lib/format';
import { displayStatus, statusText } from '@/lib/status';
import { EVENT } from '@/screens/movie/parts';

const longDate = new Intl.DateTimeFormat('en-GB', { weekday: 'long', day: 'numeric', month: 'long' });
const monthShort = new Intl.DateTimeFormat('en-GB', { month: 'short' });
const dayShort = new Intl.DateTimeFormat('en-GB', { day: 'numeric', month: 'short' });

function greeting(h: number) {
  if (h < 5) return 'Late show tonight';
  if (h < 12) return 'Good morning';
  if (h < 18) return 'Good afternoon';
  return 'Good evening';
}

const plural = (n: number, one: string, many = `${one}s`) => `${formatNumber(n)} ${n === 1 ? one : many}`;

/** One calm sentence: what is on its way, what comes out this week, what needs a look. */
export function summaryLine(downloading: number, premieres: number, issues: number): string {
  const parts: string[] = [];
  if (downloading) parts.push(`${plural(downloading, 'print')} on ${downloading === 1 ? 'its' : 'their'} way`);
  if (premieres) parts.push(`${plural(premieres, 'premiere')} this week`);
  let s = parts.length ? `${parts.join(', ')}.` : 'All quiet in the booth.';
  s = s.charAt(0).toUpperCase() + s.slice(1);
  if (issues) s += ` ${issues === 1 ? 'One thing needs' : `${formatNumber(issues)} things need`} a look.`;
  return s;
}

/** "4 min ago", "2 h ago", "3 Nov" */
export function ago(iso: string | null | undefined, now = Date.now()): string {
  if (!iso) return '';
  const t = Date.parse(iso);
  if (Number.isNaN(t)) return '';
  const m = Math.round((now - t) / 60_000);
  if (m < 1) return 'just now';
  if (m < 60) return `${m} min ago`;
  if (m < 24 * 60) return `${Math.round(m / 60)} h ago`;
  return dayShort.format(new Date(t));
}

// ----------------------------------------------------------------- health alert

function HealthAlert({ d }: { d: Dashboard }) {
  const qc = useQueryClient();
  const check = useMutation({
    mutationFn: runHealthChecks,
    onSuccess: (list) => {
      qc.setQueryData(keys.health, list);
      void qc.invalidateQueries({ queryKey: healthChecksQuery.queryKey });
      void qc.invalidateQueries({ queryKey: dashboardQuery.queryKey });
      if (!list.length) toast.show({ title: 'All clear.', body: 'Every health check passed.' });
    },
    onError: (e) => toast.show({ tone: 'error', title: 'The checks could not run.', body: (e as Error).message }),
  });
  const first = d.health.top?.[0];
  if (!first) return null;
  const more = d.health.issues - 1;
  return (
    <div className="dash-alert c12" role="status" data-tone={first.severity === 'error' ? 'failed' : 'missing'}>
      <span className="ic" aria-hidden="true">
        {first.severity === 'error' ? <TriangleAlert className="i" /> : <CloudOff className="i" />}
      </span>
      <div>
        <b>{first.title}</b>
        <p>
          {first.message}
          {more > 0 && ` ${plural(more, 'other issue')} on the Health page.`}
        </p>
      </div>
      <div className="acts">
        <Button size="sm" loading={check.isPending} onClick={() => check.mutate()}>
          Check again
        </Button>
        <Link className={buttonClass('ghost', 'sm')} to="/health">
          Health
        </Link>
      </div>
    </div>
  );
}

// ----------------------------------------------------------------- tiles

function Tile({
  id,
  icon: Icon,
  label,
  value,
  sub,
  link,
}: {
  id: string;
  icon: LucideIcon;
  label: string;
  value: string | null;
  sub?: string;
  link?: { to: string; search?: Record<string, unknown>; label: string };
}) {
  return (
    <section className="panel tile c3" aria-labelledby={id}>
      <span className="k" id={id}>
        <Icon className="i" aria-hidden="true" />
        {label}
      </span>
      {value === null ? <Skeleton className="sk-v" /> : <span className="v">{value}</span>}
      {sub && <span className="s">{sub}</span>}
      {link && (
        <Link className="s link" to={link.to as never} search={link.search as never}>
          {link.label}
        </Link>
      )}
    </section>
  );
}

function Tiles({ d }: { d: Dashboard | undefined }) {
  const lib = d?.library;
  const w = d?.wanted;
  const m = d?.thisMonth;
  const wanted = w ? w.released + w.notYetReleased : null;
  return (
    <>
      <Tile
        id="t-lib"
        icon={Film}
        label="Library"
        value={lib ? formatNumber(lib.total) : null}
        sub={lib ? `${formatNumber(lib.onDisk)} on disk · ${formatBytes(lib.totalSize)}` : undefined}
      />
      <Tile
        id="t-wanted"
        icon={CircleDashed}
        label="Wanted"
        value={wanted == null ? null : formatNumber(wanted)}
        sub={w ? `${formatNumber(w.released)} released, ${formatNumber(w.notYetReleased)} not out yet` : undefined}
        link={wanted ? { to: '/wanted', label: 'See what’s missing' } : undefined}
      />
      <Tile
        id="t-up"
        icon={CircleArrowUp}
        label="Upgradable"
        value={w ? formatNumber(w.cutoffUnmet) : null}
        sub="below their profile’s cutoff"
        link={w?.cutoffUnmet ? { to: '/wanted', search: { kind: 'cutoff' }, label: 'See which' } : undefined}
      />
      <Tile
        id="t-month"
        icon={Sparkles}
        label="This month"
        value={m ? `+${formatNumber(m.added + m.upgraded)}` : null}
        sub={m ? `${plural(m.added, 'new film')}, ${plural(m.upgraded, 'upgrade')}` : undefined}
      />
    </>
  );
}

// ----------------------------------------------------------------- queue

function QueueRow({ q }: { q: DashboardQueueItem }) {
  const live = useProgress(q.id, { progress: q.progress, speedBps: q.speedBps, etaSeconds: q.etaSeconds, status: q.status });
  const status = live.status || q.status;
  const importing = status === 'importing';
  const paused = status === 'paused';
  const failed = status === 'failed' || status === 'warning';
  const meta = importing
    ? 'Importing'
    : failed
      ? 'Needs a look'
      : paused
        ? `Paused · ${Math.round(live.progress * 100)}%`
        : status === 'queued'
          ? 'Queued'
          : [`${Math.round(live.progress * 100)}%`, formatEta(live.etaSeconds)].filter(Boolean).join(' · ');
  return (
    <div className="q-item">
      <PosterArt src={q.posterUrl} title={q.title} color={q.dominantColor} />
      <div className="min-w-0">
        <div className="t">
          <Link to="/films/$movieId" params={{ movieId: q.movieId }}>
            {q.title}
          </Link>
          <span>{meta}</span>
        </div>
        {importing ? (
          <Skeleton className="sk-bar" />
        ) : (
          <ProgressBar
            value={live.progress}
            label={`${q.title} download`}
            live
            state={failed ? 'failed' : paused ? 'paused' : 'active'}
            size="thin"
          />
        )}
      </div>
      <span className="t-caption t-muted">{q.client}</span>
    </div>
  );
}

function QueuePanel({ d }: { d: Dashboard | undefined }) {
  const q = d?.queue;
  const items = q?.items ?? [];
  return (
    <section className="panel c5" aria-labelledby="q-h">
      <div className="panel-hd">
        <h2 id="q-h">
          <ArrowDownToLine className="i st-dl" aria-hidden="true" />
          Queue
        </h2>
        {!!q?.count && (
          <span className="t-caption tnum">
            {[q.totalSpeedBps > 0 ? formatSpeed(q.totalSpeedBps) : '', q.remainingBytes > 0 ? `${formatBytes(q.remainingBytes)} left` : '']
              .filter(Boolean)
              .join(' · ')}
          </span>
        )}
      </div>
      <div className="panel-bd q-list">
        {!d ? (
          <>
            <Skeleton className="sk-row" />
            <Skeleton className="sk-row" />
          </>
        ) : items.length ? (
          items.slice(0, 3).map((x) => <QueueRow key={x.id} q={x} />)
        ) : (
          <p className="t-secondary dash-none">Nothing in the projector. Gonema checks your indexers on its own.</p>
        )}
        <Link className={buttonClass('ghost', 'sm', 'self-start')} to="/queue">
          {q && q.count > 3 ? `Open queue (${formatNumber(q.count)})` : 'Open queue'}
          <ChevronRight className="i" aria-hidden="true" />
        </Link>
      </div>
    </section>
  );
}

// ----------------------------------------------------------------- coming up

const TYPE_ICON: Record<CalendarEvent['type'], { icon: LucideIcon; label: string }> = {
  cinema: { icon: Ticket, label: 'In cinemas' },
  digital: { icon: MonitorPlay, label: 'Digital release' },
  physical: { icon: Disc3, label: 'Physical' },
};

function ComingUp({ d }: { d: Dashboard | undefined }) {
  const list = d?.upcoming?.slice(0, 3);
  return (
    <section className="panel c4" aria-labelledby="up-h">
      <div className="panel-hd">
        <h2 id="up-h">Coming up</h2>
        <Link to="/calendar">Calendar</Link>
      </div>
      <div className="panel-bd">
        {!list ? (
          <Skeleton className="sk-row" />
        ) : list.length ? (
          <ul className="up-list">
            {list.map((e) => {
              const T = TYPE_ICON[e.type];
              const Icon = T.icon;
              const m = e.movie;
              const date = new Date(`${e.date}T00:00:00`);
              const s = displayStatus({ state: m.state, monitored: m.monitored, failed: false });
              return (
                <li key={`${m.id}-${e.type}`}>
                  <span className="date">
                    <small>{monthShort.format(date)}</small>
                    {date.getDate()}
                  </span>
                  <PosterArt src={m.posterUrl} title={m.title} color={m.dominantColor} />
                  <div className="min-w-0">
                    <Link className="b" to="/films/$movieId" params={{ movieId: m.id }}>
                      {m.title}
                    </Link>
                    <span className="meta">
                      <Icon className="i" aria-hidden="true" />
                      {T.label}
                    </span>
                  </div>
                  {s === 'missing' ? <StatusText status="missing">Wanted</StatusText> : <StatusText status={s} />}
                </li>
              );
            })}
          </ul>
        ) : (
          <p className="t-secondary dash-none">No monitored film comes out in the next two months.</p>
        )}
      </div>
    </section>
  );
}

// ----------------------------------------------------------------- disk

function DiskRow({ r }: { r: DiskUsage }) {
  if (!r.totalSpace)
    return (
      <div className="disk-row">
        <div className="top">
          <code>{r.path}</code>
          <span>{r.accessible ? '—' : 'Unreachable'}</span>
        </div>
        {r.error && <span className="t-caption t-muted">{r.error}</span>}
      </div>
    );
  const used = r.totalSpace - (r.freeSpace ?? 0);
  const ratio = used / r.totalSpace;
  const tight = ratio >= 0.9;
  return (
    <div className={tight ? 'disk-row tight' : 'disk-row'}>
      <div className="top">
        <code>{r.path}</code>
        <span>{formatBytes(r.freeSpace)} free</span>
      </div>
      <div
        className="progress"
        role="meter"
        aria-label={`${r.path} used`}
        aria-valuenow={Math.round(ratio * 100)}
        aria-valuemin={0}
        aria-valuemax={100}
        style={{ ['--p' as string]: ratio } as CSSProperties}
      >
        <div className="fill" />
      </div>
      <span className={tight ? 't-caption tnum warn' : 't-caption t-muted tnum'}>
        {tight
          ? `${Math.round(ratio * 100)}% used${r.roomForFilms != null ? ` · room for about ${plural(r.roomForFilms, 'film')}` : ''}`
          : `${formatBytes(used).replace(/ [A-Z]+$/, '')} of ${formatBytes(r.totalSpace)}`}
      </span>
    </div>
  );
}

function DiskPanel({ d }: { d: Dashboard | undefined }) {
  return (
    <section className="panel c3" aria-labelledby="disk-h">
      <div className="panel-hd">
        <h2 id="disk-h">Disk space</h2>
      </div>
      <div className="panel-bd disk-bd">
        {!d && <Skeleton className="sk-row" />}
        {d && !d.disks?.length && (
          <p className="t-secondary dash-none">
            No library folder yet.{' '}
            <Link to="/settings/$section" params={{ section: 'library' }}>
              Add one
            </Link>
          </p>
        )}
        {d?.disks?.map((r) => (
          <DiskRow key={r.rootFolderId} r={r} />
        ))}
      </div>
    </section>
  );
}

// ----------------------------------------------------------------- activity

const ACT_COLOR: Record<string, string> = {
  downloading: 'var(--status-downloading)',
  available: 'var(--status-available)',
  upgraded: 'var(--status-upgraded)',
  failed: 'var(--status-failed)',
};

function ActivityRow({ e }: { e: HistoryRow }) {
  const ev = EVENT[e.event];
  const Icon = ev?.icon ?? Film;
  const title = e.movie?.title ?? e.sourceTitle;
  const d = new Date(e.createdAt);
  return (
    <li style={{ ['--c' as string]: ACT_COLOR[ev?.status ?? ''] ?? 'var(--text-secondary)' }}>
      <span className="d" aria-hidden="true">
        <Icon className="i" />
      </span>
      <span className="w">
        {e.movieId ? (
          <Link className="b" to="/films/$movieId" params={{ movieId: e.movieId }}>
            {title}
          </Link>
        ) : (
          <b>{title}</b>
        )}{' '}
        {(ev?.label ?? e.event.replace(/_/g, ' ')).toLowerCase()}
        {e.qualityName && ` · ${e.qualityName}`}
        {e.reason && <span className="why">{e.reason}</span>}
      </span>
      <time dateTime={e.createdAt}>
        {new Date().toDateString() === d.toDateString() ? formatClock(d) : dayShort.format(d)}
      </time>
    </li>
  );
}

function RecentActivity({ d }: { d: Dashboard | undefined }) {
  const list = (d?.recentActivity ?? []).slice(0, 4);
  return (
    <section className="panel c7" aria-labelledby="act-h">
      <div className="panel-hd">
        <h2 id="act-h">Recent activity</h2>
        <Link to="/history">History</Link>
      </div>
      <div className="panel-bd">
        {!d ? (
          <Skeleton className="sk-row" />
        ) : list.length ? (
          <ul className="act">
            {list.map((e) => (
              <ActivityRow key={e.id} e={e} />
            ))}
          </ul>
        ) : (
          <p className="t-secondary dash-none">No reels logged yet. Every grab, import and upgrade will show here.</p>
        )}
      </div>
    </section>
  );
}

function RecentlyAdded({ d }: { d: Dashboard | undefined }) {
  const lib = useLibrary();
  const byId = useMemo(() => new Map((lib.data ?? []).map((m) => [m.id, m])), [lib.data]);
  const recent = (d?.recentlyAdded ?? []).slice(0, 5);
  return (
    <section className="panel c5" aria-labelledby="new-h">
      <div className="panel-hd">
        <h2 id="new-h">Recently added</h2>
        <Link to="/films">All films</Link>
      </div>
      <div className="panel-bd">
        {!d ? (
          <Skeleton className="sk-row" />
        ) : recent.length ? (
          <div className="shelf">
            {recent.map((r) => {
              const m = byId.get(r.id);
              const st = displayStatus({ state: r.state as never, monitored: r.monitored, failed: m?.failed ?? false });
              return (
                <Link key={r.id} className="shelf-card" to="/films/$movieId" params={{ movieId: r.id }}>
                  <PosterArt src={r.posterUrl} title={r.title} year={r.year} color={r.dominantColor} />
                  <span className="pc-title">{r.title}</span>
                  <StatusText status={st}>{m ? statusText(m, st) : undefined}</StatusText>
                </Link>
              );
            })}
          </div>
        ) : (
          <p className="t-secondary dash-none">
            The house is dark. <Link to="/discover">Add a film</Link> to start the screening.
          </p>
        )}
      </div>
    </section>
  );
}

// ----------------------------------------------------------------- screen

export function HomeScreen() {
  const dash = useQuery(dashboardQuery);
  const d = dash.data;
  useEffect(() => {
    document.title = 'Home · Gonema';
  }, []);
  const now = new Date();
  const week = useMemo(() => {
    const limit = Date.now() + 7 * 86_400_000;
    return (d?.upcoming ?? []).filter((e) => Date.parse(`${e.date}T00:00:00`) < limit).length;
  }, [d?.upcoming]);
  return (
    <>
      <Topbar>
        <SearchField />
        <div className="spacer" />
        <ThemeToggle />
        <AddFilmButton />
      </Topbar>
      <main id="main" className="page home-page" tabIndex={-1}>
        <div className="hello">
          <div>
            <h1>{greeting(now.getHours())}</h1>
            <p aria-live="polite">{d ? summaryLine(d.queue.downloading, week, d.health.issues) : ' '}</p>
          </div>
          <span className="t-caption t-muted tnum">
            {longDate.format(now)}
            {d?.lastRssSync ? ` · last RSS sync ${ago(d.lastRssSync)}` : ''}
          </span>
        </div>
        {dash.isError ? (
          <Notice
            tone="failed"
            role="alert"
            title="The home screen could not be loaded."
            action={
              <Button size="sm" onClick={() => void dash.refetch()}>
                Try again
              </Button>
            }
          >
            {(dash.error as Error).message}
          </Notice>
        ) : (
          <div className="dash">
            {d && <HealthAlert d={d} />}
            <Tiles d={d} />
            <QueuePanel d={d} />
            <ComingUp d={d} />
            <DiskPanel d={d} />
            <RecentActivity d={d} />
            <RecentlyAdded d={d} />
          </div>
        )}
      </main>
    </>
  );
}
