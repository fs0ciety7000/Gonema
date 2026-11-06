import { useQuery, useQueryClient } from '@tanstack/react-query';
import { Link, useNavigate, useRouterState } from '@tanstack/react-router';
import { LogOut, UserRound } from 'lucide-react';
import { useState } from 'react';
import { authStatusQuery, signOut } from '@/api/auth';
import { healthQuery, useLibrary, useQueue, useRootFolders, useSystemStatus } from '@/api/queries';
import { IconButton } from '@/components/ui/Button';
import { ProgressBar } from '@/components/ui/Progress';
import { formatBytes, formatNumber } from '@/lib/format';
import { type NavEntry, visibleNav } from '@/lib/nav';
import { Mark, Wordmark } from './Logo';

function activeSection(pathname: string): string {
  // /system/health: the server answers GET /health itself (liveness probe), so the screen lives one level down
  if (pathname.startsWith('/system/health')) return 'health';
  return pathname.split('/').filter(Boolean)[0] ?? 'home';
}

function Count({ entry }: { entry: NavEntry }) {
  const lib = useLibrary();
  const queue = useQueue();
  const health = useQuery(healthQuery);
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

/** Who is signed in, and the way out. In the local mode (GONEMA_AUTH=disabled_for_local) there is no session to end. */
function UserRow() {
  const status = useSystemStatus();
  const auth = useQuery(authStatusQuery);
  const qc = useQueryClient();
  const navigate = useNavigate();
  const [leaving, setLeaving] = useState(false);
  const a = auth.data;
  const local = a?.method === 'local';
  const name = a?.username ?? (local ? 'Local access' : '…');
  const role = local ? (a?.username ? 'Local access' : 'No sign-in on this network') : 'Admin';
  return (
    <div className="user-row">
      <span className="avatar" aria-hidden="true">
        <UserRound className="i i-sm" />
      </span>
      <span className="who">
        <span className="name truncate">{name}</span>
        <span>
          {role} · v{status.data?.version ?? '…'}
        </span>
      </span>
      {a?.method === 'session' && (
        <IconButton
          size="sm"
          className="sign-out"
          label="Sign out"
          title="Sign out"
          disabled={leaving}
          onClick={() => {
            setLeaving(true);
            void signOut(qc).finally(() => navigate({ to: '/login', replace: true }));
          }}
        >
          <LogOut className="i i-sm" aria-hidden="true" />
        </IconButton>
      )}
    </div>
  );
}

export function Sidebar() {
  const pathname = useRouterState({ select: (s) => s.location.pathname });
  const active = activeSection(pathname);
  return (
    <nav className="sidebar" aria-label="Main">
      <Link className="brand" to="/" aria-label="Gonema, home">
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
        <UserRow />
      </div>
    </nav>
  );
}
