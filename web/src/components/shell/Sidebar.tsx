import { useQuery } from '@tanstack/react-query';
import { Link, useRouterState } from '@tanstack/react-router';
import { UserRound } from 'lucide-react';
import { healthQuery, useLibrary, useQueue, useRootFolders, useSystemStatus } from '@/api/queries';
import { ProgressBar } from '@/components/ui/Progress';
import { DEV_SCREENS } from '@/lib/flags';
import { formatBytes, formatNumber } from '@/lib/format';
import { type NavEntry, visibleNav } from '@/lib/nav';
import { Mark, Wordmark } from './Logo';

function activeSection(pathname: string): string {
  const seg = pathname.split('/').filter(Boolean)[0] ?? 'films';
  return seg === 'home' ? 'home' : seg;
}

function Count({ entry }: { entry: NavEntry }) {
  const lib = useLibrary();
  const queue = useQueue();
  const health = useQuery({ ...healthQuery, enabled: DEV_SCREENS });
  if (entry.id === 'films' && lib.data) return <span className="count">{formatNumber(lib.data.length)}</span>;
  if (entry.id === 'queue' && queue.data?.length) {
    const n = queue.data.length;
    return (
      <span className="count live" title={`${n} downloading`}>
        {n}
      </span>
    );
  }
  if (entry.id === 'wanted' && lib.data) {
    const n = lib.data.filter((m) => m.monitored && m.state === 'missing').length;
    return n ? (
      <span className="count warn" title={`${formatNumber(n)} missing`}>
        {formatNumber(n)}
      </span>
    ) : null;
  }
  if (entry.id === 'health' && Array.isArray(health.data) && health.data.length) {
    const n = health.data.length;
    return (
      <span className="count warn" title={`${n} ${n === 1 ? 'issue' : 'issues'}`}>
        {n}
      </span>
    );
  }
  return null;
}

function Disk() {
  const roots = useRootFolders();
  const r = roots.data?.find((x) => x.totalSpace);
  if (!r?.totalSpace) return null;
  const used = r.totalSpace - (r.freeSpace ?? 0);
  return (
    <div className="disk">
      <div className="row">
        <span className="truncate">{r.path}</span>
        <span>
          {formatBytes(used).replace(/ [A-Z]+$/, '')} / {formatBytes(r.totalSpace)}
        </span>
      </div>
      <ProgressBar
        value={used / r.totalSpace}
        label={`Disk space: ${formatBytes(used)} of ${formatBytes(r.totalSpace)} used on ${r.path}`}
        size="thin"
        className="disk-bar"
      />
    </div>
  );
}

export function Sidebar() {
  const pathname = useRouterState({ select: (s) => s.location.pathname });
  const active = activeSection(pathname);
  const status = useSystemStatus();
  return (
    <nav className="sidebar" aria-label="Main">
      <Link className="brand" to="/films" aria-label="Gonema, films">
        <Mark />
        <Wordmark />
      </Link>
      {visibleNav().map((section) => (
        <div className="nav-section" key={section.heading}>
          <div className="nav-heading" aria-hidden="true">
            {section.heading}
          </div>
          {section.items.map((entry) => {
            const Icon = entry.icon;
            return (
              <Link
                key={entry.id}
                className="nav-item"
                to={entry.path as never}
                aria-current={active === entry.id ? 'page' : undefined}
              >
                <Icon className="i" aria-hidden="true" />
                <span className="label">{entry.label}</span>
                <Count entry={entry} />
              </Link>
            );
          })}
        </div>
      ))}
      <div className="foot">
        <Disk />
        <div className="user-row">
          <span className="avatar" aria-hidden="true">
            <UserRound className="i i-sm" />
          </span>
          <span className="who">
            This server
            <span>No sign-in yet · v{status.data?.version ?? '…'}</span>
          </span>
        </div>
      </div>
    </nav>
  );
}
