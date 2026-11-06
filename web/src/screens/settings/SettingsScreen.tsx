import '@/styles/lot2.css';
import '@/styles/lot3.css';
import { useQuery } from '@tanstack/react-query';
import { Link, useParams } from '@tanstack/react-router';
import {
  ArrowDownToLine,
  Bell,
  ChevronRight,
  FileVideo,
  Film,
  Gauge,
  Globe,
  HardDrive,
  Info,
  Lock,
  type LucideIcon,
  MonitorPlay,
  Radar,
  Search,
  SlidersHorizontal,
  Tag,
} from 'lucide-react';
import { lazy, Suspense, useEffect, useMemo, useState } from 'react';
import {
  clientsQuery,
  indexersQuery,
  mediaServersQuery,
  namingQuery,
  notificationsQuery,
  pathMappingsQuery,
} from '@/api/lot3';
import { rootFoldersQuery } from '@/api/queries';
import { tmdbQuery } from '@/api/screens';
import { NotFound } from '@/components/shell/NotFound';
import { ThemeToggle, Topbar } from '@/components/shell/Topbar';
import { StatusText } from '@/components/ui/Badges';
import { buttonClass } from '@/components/ui/Button';
import { Skeleton } from '@/components/ui/Feedback';
import { formatBytes } from '@/lib/format';
import { ClientsSection, EVENTS, IndexersSection, MediaServersSection, NotificationsSection } from './Connections';
import { HeaderSlot } from './headerSlot';
import { AcquisitionSection, LibrarySection, MetadataSection, TagsSection } from './Sections';

const QualitySection = lazy(() => import('./QualitySection'));
const SecuritySection = lazy(() => import('./SecuritySection'));
const BackupsSection = lazy(() => import('./BackupsSection'));
const LogsSection = lazy(() => import('./LogsSection'));
const AboutSection = lazy(() => import('./AboutSection'));
const SystemCards = lazy(() => import('./SystemCards'));

type SectionDef = {
  id: string;
  label: string;
  icon: LucideIcon;
  title: string;
  intro: string;
  keywords: string;
};

export const SECTIONS: ReadonlyArray<{ heading: string; items: readonly SectionDef[] }> = [
  {
    heading: 'General',
    items: [
      {
        id: 'overview',
        label: 'Overview',
        icon: Gauge,
        title: 'Settings',
        intro:
          'Everything that connects Gonema to the outside world. Changes save when you press Save; removals offer Undo.',
        keywords: '',
      },
      {
        id: 'library',
        label: 'Library & naming',
        icon: Film,
        title: 'Library & naming',
        intro: 'Where films live, how their folders and files are named, and how downloads are brought in.',
        keywords: 'root folder path naming rename template hardlink copy move recycle bin subtitles import',
      },
      {
        id: 'quality',
        label: 'Quality & scoring',
        icon: SlidersHorizontal,
        title: 'Quality profiles & scoring',
        intro:
          'A profile says which copies are acceptable and when to stop upgrading. Scoring rules add or remove points for what you care about.',
        keywords: 'profile cutoff upgrade score rule custom format size',
      },
      {
        id: 'acquisition',
        label: 'Searching & health',
        icon: Radar,
        title: 'Searching & health',
        intro: 'How often Gonema looks for releases, how it retries, and when it warns you.',
        keywords: 'rss interval retention protocol blocklist retries disk space warning',
      },
      {
        id: 'metadata',
        label: 'Metadata',
        icon: Globe,
        title: 'Metadata',
        intro: 'Film details, posters and release dates.',
        keywords: 'tmdb key api token posters',
      },
      {
        id: 'tags',
        label: 'Tags',
        icon: Tag,
        title: 'Tags',
        intro: 'Labels for films, and the connections they apply to.',
        keywords: 'tag label',
      },
    ],
  },
  {
    heading: 'Connections',
    items: [
      {
        id: 'indexers',
        label: 'Indexers',
        icon: Search,
        title: 'Indexers',
        intro: 'Where Gonema looks for releases: Newznab (Usenet) and Torznab (torrent) indexers.',
        keywords: 'newznab torznab api key categories seeders',
      },
      {
        id: 'download-clients',
        label: 'Download clients',
        icon: ArrowDownToLine,
        title: 'Download clients',
        intro:
          'Where releases are sent. Same priority: Gonema alternates. Failed downloads are removed from the client after blocklisting.',
        keywords: 'sabnzbd nzbget qbittorrent deluge transmission category password',
      },
      {
        id: 'media-servers',
        label: 'Media servers',
        icon: MonitorPlay,
        title: 'Media servers',
        intro: 'Libraries Gonema refreshes when a film arrives, is upgraded, renamed or removed.',
        keywords: 'plex jellyfin kodi refresh token',
      },
      {
        id: 'notifications',
        label: 'Notifications',
        icon: Bell,
        title: 'Notifications',
        intro: 'Short notes when something happens, with the reason behind it.',
        keywords: 'discord slack telegram pushover ntfy gotify email webhook',
      },
    ],
  },
  {
    heading: 'System',
    items: [
      {
        id: 'security',
        label: 'Security',
        icon: Lock,
        title: 'Security',
        intro:
          'Who can sign in, the browsers signed in right now, the API key, and where Gonema is allowed to connect.',
        keywords: 'password sign in login session api key logout ssrf outbound network auth',
      },
      {
        id: 'backups',
        label: 'Backups',
        icon: HardDrive,
        title: 'Backups',
        intro:
          'Copies of the database, made on a schedule or by hand while Gonema keeps running. A restore takes effect at the next restart.',
        keywords: 'backup restore archive schedule retention master key download upload',
      },
      {
        id: 'logs',
        label: 'Logs',
        icon: FileVideo,
        title: 'Logs',
        intro:
          'What Gonema did and why, as it happens. Passwords, keys and tokens are removed before anything is written.',
        keywords: 'log level debug error warning tail download',
      },
      {
        id: 'about',
        label: 'About',
        icon: Info,
        title: 'About Gonema',
        intro: 'Version, source code and license.',
        keywords: 'version license gpl source credits tmdb',
      },
    ],
  },
];

const ALL = SECTIONS.flatMap((s) => s.items);

function Overview() {
  const indexers = useQuery(indexersQuery);
  const clients = useQuery(clientsQuery);
  const servers = useQuery(mediaServersQuery);
  const naming = useQuery(namingQuery);
  const roots = useQuery(rootFoldersQuery);
  const mappings = useQuery(pathMappingsQuery);
  const notifs = useQuery(notificationsQuery);
  const tmdb = useQuery(tmdbQuery);
  const manage = (section: string, label = 'Manage') => (
    <Link className={buttonClass('ghost', 'sm')} to="/settings/$section" params={{ section }}>
      {label}
    </Link>
  );
  const none = (t: string) => <p className="t-secondary t-body-sm">{t}</p>;
  return (
    <div className="idx">
      <section className="panel" aria-labelledby="i1">
        <div className="panel-hd">
          <h2 id="i1">
            <Search className="i" aria-hidden="true" />
            Indexers
          </h2>
          {manage('indexers')}
        </div>
        <div className="panel-bd">
          {indexers.isPending ? (
            <Skeleton className="sk-row" />
          ) : indexers.data?.length ? (
            <ul className="mini-rows">
              {indexers.data.map((x) => (
                <li key={x.id}>
                  <span>
                    {x.name}
                    <small>
                      {x.kind === 'torznab' ? 'Torznab' : 'Newznab'} · priority {x.priority}
                    </small>
                  </span>
                  {x.health?.state === 'ok' || !x.health ? (
                    <StatusText status="available">OK</StatusText>
                  ) : x.health.state === 'disabled' ? (
                    <StatusText status="missing">Paused</StatusText>
                  ) : (
                    <StatusText status="failed">Failing</StatusText>
                  )}
                </li>
              ))}
            </ul>
          ) : (
            none('No indexer yet.')
          )}
        </div>
      </section>
      <section className="panel" aria-labelledby="i2">
        <div className="panel-hd">
          <h2 id="i2">
            <ArrowDownToLine className="i" aria-hidden="true" />
            Download clients
          </h2>
          {manage('download-clients')}
        </div>
        <div className="panel-bd">
          {clients.isPending ? (
            <Skeleton className="sk-row" />
          ) : clients.data?.length ? (
            <ul className="mini-rows">
              {clients.data.map((c) => (
                <li key={c.id}>
                  <span>
                    {c.name}
                    <small>
                      {c.protocol === 'usenet' ? 'Usenet' : 'Torrent'}
                      {c.category ? ` · category ${c.category}` : ''} · priority {c.priority}
                      {!c.enabled && ' · disabled'}
                    </small>
                  </span>
                  {!c.enabled ? (
                    <StatusText status="unmonitored">Off</StatusText>
                  ) : (c.health?.failureCount ?? 0) > 0 ? (
                    <StatusText status="failed">Failing</StatusText>
                  ) : (
                    <StatusText status="available">OK</StatusText>
                  )}
                </li>
              ))}
            </ul>
          ) : (
            none('No download client yet.')
          )}
        </div>
      </section>
      <section className="panel" aria-labelledby="i3">
        <div className="panel-hd">
          <h2 id="i3">
            <MonitorPlay className="i" aria-hidden="true" />
            Media servers
          </h2>
          {manage('media-servers')}
        </div>
        <div className="panel-bd">
          {servers.isPending ? (
            <Skeleton className="sk-row" />
          ) : servers.data?.length ? (
            <ul className="mini-rows">
              {servers.data.map((s) => (
                <li key={s.id}>
                  <span>
                    {s.name}
                    <small>{s.kind}</small>
                  </span>
                  {s.enabled ? (
                    <StatusText status="available">On</StatusText>
                  ) : (
                    <StatusText status="unmonitored">Off</StatusText>
                  )}
                </li>
              ))}
            </ul>
          ) : (
            none('No media server yet.')
          )}
        </div>
      </section>
      <section className="panel span2" aria-labelledby="i4">
        <div className="panel-hd">
          <h2 id="i4">
            <Film className="i" aria-hidden="true" />
            Naming
          </h2>
          {manage('library', 'Edit')}
        </div>
        <div className="panel-bd tmpl">
          {naming.data ? (
            <>
              <span className="field-label">Film file</span>
              <code className="preview">
                <span>{naming.data.folderTemplate}/</span>
                {naming.data.fileTemplate}
              </code>
              <span className="t-caption t-muted">
                {naming.data.renameFiles ? 'Imported files are renamed.' : 'Imported files keep their original names.'}
              </span>
            </>
          ) : (
            <Skeleton className="sk-row" />
          )}
        </div>
      </section>
      <section className="panel" aria-labelledby="i5">
        <div className="panel-hd">
          <h2 id="i5">Paths</h2>
          {manage('library')}
        </div>
        <div className="panel-bd">
          <ul className="mini-rows">
            {(roots.data ?? []).map((r) => (
              <li key={r.id}>
                <span className="t-mono-sm">
                  {r.path}
                  <small>{r.freeSpace != null ? `${formatBytes(r.freeSpace)} free` : r.error || '—'}</small>
                </span>
              </li>
            ))}
          </ul>
          {!roots.isPending && !roots.data?.length && none('No root folder yet.')}
          {!!mappings.data?.length && (
            <>
              <span className="field-label">Remote path mappings</span>
              <div className="mappings t-mono-sm t-secondary">
                {mappings.data.map((m) => (
                  <span key={m.id} className="contents">
                    <span className="t-muted">{m.host}</span>
                    <span>
                      {m.remotePath} → {m.localPath}
                    </span>
                  </span>
                ))}
              </div>
            </>
          )}
        </div>
      </section>
      <section className="panel span2" aria-labelledby="i6">
        <div className="panel-hd">
          <h2 id="i6">
            <Bell className="i" aria-hidden="true" />
            Notifications
          </h2>
          {manage('notifications', 'Add channel')}
        </div>
        <div className="panel-bd">
          {notifs.data?.length ? (
            <table className="matrix" aria-label="Notification events per channel">
              <thead>
                <tr>
                  <th scope="col">Channel</th>
                  {EVENTS.map((e) => (
                    <th scope="col" key={e.value}>
                      {e.label}
                    </th>
                  ))}
                </tr>
              </thead>
              <tbody>
                {notifs.data.map((n) => (
                  <tr key={n.id}>
                    <th scope="row">{n.name}</th>
                    {EVENTS.map((e) => {
                      const on = (n.events ?? []).includes(e.value);
                      return (
                        <td key={e.value}>
                          {on ? (
                            <span className="on" aria-hidden="true">
                              ✓
                            </span>
                          ) : (
                            <span className="off" aria-hidden="true">
                              ·
                            </span>
                          )}
                          <span className="sr-only">{on ? 'on' : 'off'}</span>
                        </td>
                      );
                    })}
                  </tr>
                ))}
              </tbody>
            </table>
          ) : (
            none(
              notifs.isPending
                ? ' '
                : 'No channel yet. Gonema can send a note when a film arrives or something needs a look.',
            )
          )}
        </div>
      </section>
      <section className="panel" aria-labelledby="i7">
        <div className="panel-hd">
          <h2 id="i7">
            <Globe className="i" aria-hidden="true" />
            Metadata
          </h2>
          {manage('metadata', tmdb.data?.configured ? 'Manage' : 'Add key')}
        </div>
        <div className="panel-bd">
          <ul className="mini-rows">
            <li>
              <span>
                TMDB
                <small>Posters, cast, collections, release dates</small>
              </span>
              {tmdb.data?.configured ? (
                <StatusText status="available">Connected</StatusText>
              ) : (
                <StatusText status="missing">No key</StatusText>
              )}
            </li>
          </ul>
        </div>
      </section>
      <Suspense fallback={null}>
        <SystemCards />
      </Suspense>
    </div>
  );
}

function Body({ id }: { id: string }) {
  switch (id) {
    case 'overview':
      return <Overview />;
    case 'library':
      return <LibrarySection />;
    case 'quality':
      return (
        <Suspense fallback={<Skeleton className="sk-wanted" />}>
          <QualitySection />
        </Suspense>
      );
    case 'acquisition':
      return <AcquisitionSection />;
    case 'metadata':
      return <MetadataSection />;
    case 'tags':
      return <TagsSection />;
    case 'indexers':
      return <IndexersSection />;
    case 'download-clients':
      return <ClientsSection />;
    case 'media-servers':
      return <MediaServersSection />;
    case 'notifications':
      return <NotificationsSection />;
    case 'security':
    case 'backups':
    case 'logs':
    case 'about': {
      const C = { security: SecuritySection, backups: BackupsSection, logs: LogsSection, about: AboutSection }[id];
      return (
        <Suspense fallback={<Skeleton className="sk-wanted" />}>
          <C />
        </Suspense>
      );
    }
    default:
      return null;
  }
}

export function SettingsScreen() {
  const params = useParams({ strict: false }) as { section?: string; anchor?: string };
  const id = params.section ?? 'overview';
  const def = ALL.find((s) => s.id === id);
  const [find, setFind] = useState('');
  const [slot, setSlot] = useState<HTMLDivElement | null>(null);
  useEffect(() => {
    document.title = def ? `${def.id === 'overview' ? 'Settings' : `${def.label} · Settings`} · Gonema` : 'Gonema';
  }, [def]);
  useEffect(() => {
    if (params.anchor) requestAnimationFrame(() => document.getElementById(params.anchor!)?.scrollIntoView());
  }, [params.anchor]);
  const matches = useMemo(() => {
    const q = find.trim().toLowerCase();
    if (!q) return null;
    return new Set(
      ALL.filter((s) => `${s.label} ${s.title} ${s.intro} ${s.keywords}`.toLowerCase().includes(q)).map((s) => s.id),
    );
  }, [find]);
  if (!def) return <NotFound />;
  return (
    <>
      <Topbar>
        <nav className="crumbs" aria-label="Breadcrumb">
          {def.id === 'overview' ? (
            <span aria-current="page">Settings</span>
          ) : (
            <>
              <Link to="/settings">Settings</Link>
              <ChevronRight className="i" aria-hidden="true" />
              <span aria-current="page">{def.label}</span>
            </>
          )}
        </nav>
        <div className="spacer" />
        <label className="input search set-find">
          <Search className="i" aria-hidden="true" />
          <input
            type="search"
            placeholder="Find a setting…"
            aria-label="Find a setting"
            value={find}
            onChange={(ev) => setFind(ev.target.value)}
            onKeyDown={(ev) => {
              if (ev.key === 'Escape') setFind('');
            }}
          />
        </label>
        <ThemeToggle />
      </Topbar>
      <main id="main" className="page settings-page" tabIndex={-1}>
        <div className="set">
          <nav className="snav" aria-label="Settings sections">
            {SECTIONS.map((g) => {
              const items = g.items.filter((s) => !matches || matches.has(s.id));
              if (!items.length) return null;
              return (
                <div key={g.heading} className="snav-group">
                  <h2>{g.heading}</h2>
                  {items.map((s) => {
                    const Icon = s.icon;
                    return (
                      <Link
                        key={s.id}
                        to={s.id === 'overview' ? '/settings' : '/settings/$section'}
                        params={s.id === 'overview' ? undefined : ({ section: s.id } as never)}
                        activeOptions={{ exact: true }}
                        aria-current={s.id === id ? 'page' : undefined}
                      >
                        <Icon className="i" aria-hidden="true" />
                        {s.label}
                      </Link>
                    );
                  })}
                </div>
              );
            })}
            {matches && !matches.size && <p className="t-caption t-muted snav-none">No setting matches “{find}”.</p>}
          </nav>
          <div className="set-main">
            <div className="shead">
              <div>
                <h1>{def.title}</h1>
                <p>{def.intro}</p>
              </div>
              <div className="shead-actions" ref={setSlot} />
            </div>
            <HeaderSlot.Provider value={slot}>
              <Body id={def.id} />
            </HeaderSlot.Provider>
          </div>
        </div>
      </main>
    </>
  );
}
