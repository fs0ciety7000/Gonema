import '@/styles/lot3.css';
import { Dialog as BaseDialog } from '@base-ui/react/dialog';
import { keepPreviousData, useQuery, useQueryClient } from '@tanstack/react-query';
import { useNavigate } from '@tanstack/react-router';
import { Command } from 'cmdk';
import {
  ArrowDownToLine,
  CirclePause,
  CirclePlay,
  Moon,
  Plus,
  ScanSearch,
  Search,
  Sun,
  UserRound,
  X,
} from 'lucide-react';
import { type ReactNode, useDeferredValue, useEffect, useMemo, useState } from 'react';
import { paletteSearchQuery, queueControl } from '@/api/lot3';
import type { LibraryItem } from '@/api/model';
import { keys, useLibrary, useQueue } from '@/api/queries';
import type { PaletteFilm } from '@/api/schema';
import { tmdbQuery } from '@/api/screens';
import { StatusText } from '@/components/ui/Badges';
import { overlayRoot } from '@/components/ui/overlayRoot';
import { PosterArt } from '@/components/ui/PosterArt';
import { toast } from '@/components/ui/Toast';
import { ALL_SECTIONS, type NavEntry, visibleNav } from '@/lib/nav';
import { palette, usePalette } from '@/lib/palette';
import { qualityName } from '@/lib/quality';
import { displayStatus, statusText } from '@/lib/status';
import { setThemePreference, useTheme } from '@/lib/theme';
import { pushRecent, type Recent, readRecents } from './recents';

type Scope = 'all' | 'films' | 'people' | 'actions';

/** Query prefixes of the mockup: ">" actions, "@" people. */
export function parseQuery(raw: string, scope: Scope): { q: string; scope: Scope } {
  if (raw.startsWith('>')) return { q: raw.slice(1).trim(), scope: 'actions' };
  if (raw.startsWith('@')) return { q: raw.slice(1).trim(), scope: 'people' };
  return { q: raw.trim(), scope };
}

const norm = (s: string) => s.toLowerCase().normalize('NFD').replace(/[̀-ͯ]/g, '');

/** Films whose title matches: prefix first, then word start, then anywhere; at most `limit`. */
export function matchFilms(library: readonly LibraryItem[], q: string, limit = 6): LibraryItem[] {
  const n = norm(q);
  if (!n) return [];
  const scored: Array<[number, LibraryItem]> = [];
  for (const m of library) {
    const t = norm(m.title);
    const i = t.indexOf(n);
    if (i < 0) continue;
    const s = i === 0 ? 0 : t[i - 1] === ' ' ? 1 : 2;
    scored.push([s, m]);
  }
  scored.sort((a, b) => a[0] - b[0] || a[1].title.length - b[1].title.length);
  return scored.slice(0, limit).map((x) => x[1]);
}

function Mark({ text, q }: { text: string; q: string }) {
  const i = q ? norm(text).indexOf(norm(q)) : -1;
  if (i < 0) return <>{text}</>;
  return (
    <>
      {text.slice(0, i)}
      <mark>{text.slice(i, i + q.length)}</mark>
      {text.slice(i + q.length)}
    </>
  );
}

function Opt({
  value,
  onSelect,
  lead,
  title,
  sub,
  side,
}: {
  value: string;
  onSelect: () => void;
  lead: ReactNode;
  title: ReactNode;
  sub?: ReactNode;
  side?: ReactNode;
}) {
  return (
    <Command.Item value={value} onSelect={onSelect} className="opt">
      <span className="lead" aria-hidden="true">
        {lead}
      </span>
      <span className="tx">
        <b>{title}</b>
        {sub && <span>{sub}</span>}
      </span>
      <span className="side">{side}</span>
    </Command.Item>
  );
}

const Kbd = ({ k }: { k: string }) => (
  <span className="kbd" aria-hidden="true">
    {k}
  </span>
);

export default function CommandPalette() {
  const { open, query: initial } = usePalette();
  const navigate = useNavigate();
  const qc = useQueryClient();
  const lib = useLibrary();
  const queue = useQueue();
  const { theme } = useTheme();
  const tmdb = useQuery(tmdbQuery);
  const [raw, setRaw] = useState('');
  const [scope, setScope] = useState<Scope>('all');
  const [recents, setRecents] = useState<Recent[]>([]);
  useEffect(() => {
    if (open) {
      setRaw(initial);
      setScope('all');
      setRecents(readRecents());
    }
  }, [open, initial]);
  const parsed = parseQuery(raw, scope);
  const q = useDeferredValue(parsed.q);
  const s = parsed.scope;
  // Library films from the server's index on every keystroke; TMDB films and people once typing pauses.
  const [remoteQ, setRemoteQ] = useState('');
  useEffect(() => {
    const t = setTimeout(() => setRemoteQ(q.length >= 3 ? q : ''), 350);
    return () => clearTimeout(t);
  }, [q]);
  const wantFilms = s === 'all' || s === 'films';
  const local = useQuery({
    ...paletteSearchQuery(q, false),
    enabled: open && !!q && (wantFilms || s === 'people'),
    placeholderData: keepPreviousData,
  });
  const remote = useQuery({
    ...paletteSearchQuery(remoteQ, true),
    enabled: open && !!tmdb.data?.configured && !!remoteQ && remoteQ === q && s !== 'actions',
  });
  const byId = useMemo(() => new Map((lib.data ?? []).map((m) => [m.id, m])), [lib.data]);
  const films: PaletteFilm[] = useMemo(() => {
    if (!q || !wantFilms) return [];
    if (local.data) return (local.data.films ?? []).slice(0, s === 'films' ? 12 : 6);
    if (local.isError && lib.data)
      return matchFilms(lib.data, q, 6).map((m) => ({
        id: m.id,
        title: m.title,
        originalTitle: '',
        field: 'title' as const,
        match: 'substring' as const,
        ranges: null,
        year: m.year,
        posterUrl: m.posterUrl,
        dominantColor: m.dominantColor,
        monitored: m.monitored,
        quality: m.quality,
        qualityName: null,
        state: m.state,
        tmdbId: m.tmdbId,
      }));
    return [];
  }, [q, wantFilms, s, local.data, local.isError, lib.data]);
  const remoteData = remote.data?.query === q ? remote.data.remote : undefined;
  const remoteBusy =
    !!tmdb.data?.configured && !!q && q.length >= 3 && s !== 'actions' && (remoteQ !== q || remote.isFetching);

  const [selected, setSelected] = useState('');
  const firstValue = films[0] ? `film-${films[0].id}` : '';
  useEffect(() => {
    if (firstValue) setSelected(firstValue);
  }, [firstValue]);

  const close = () => palette.close();
  const go = (to: string, search?: Record<string, unknown>, recent?: Recent) => {
    if (recent) pushRecent(recent);
    close();
    void navigate({ to: to as never, search: search as never });
  };
  const openFilm = (m: { id: number }) => go(`/films/${m.id}`, undefined, { kind: 'film', id: m.id });

  const active = (queue.data ?? []).filter((x) => x.status === 'downloading' || x.status === 'queued').length;
  const paused = (queue.data ?? []).filter((x) => x.status === 'paused').length;
  const control = async (what: 'pause' | 'resume') => {
    close();
    try {
      await queueControl(what);
      void qc.invalidateQueries({ queryKey: keys.queue });
      toast.show({
        title: what === 'pause' ? 'Queue paused.' : 'Queue resumed.',
        body: what === 'pause' ? 'Nothing downloads until you resume.' : 'Downloads continue where they stopped.',
      });
    } catch (e) {
      toast.error(what === 'pause' ? 'The queue was not paused' : 'The queue was not resumed', (e as Error).message);
    }
  };

  const nav = visibleNav().flatMap((g) => g.items);
  const actions: Array<{ id: string; title: string; sub: string; icon: ReactNode; run: () => void; kbd?: string[] }> = [
    {
      id: 'add',
      title: 'Add film…',
      sub: 'Search TMDB',
      icon: <Plus className="i" />,
      run: () => go('/discover', q ? { q } : undefined),
      kbd: ['A'],
    },
    ...(active
      ? [
          {
            id: 'pause',
            title: 'Pause the queue',
            sub: `${active} downloading`,
            icon: <CirclePause className="i" />,
            run: () => void control('pause'),
          },
        ]
      : []),
    ...(paused
      ? [
          {
            id: 'resume',
            title: 'Resume the queue',
            sub: `${paused} paused`,
            icon: <CirclePlay className="i" />,
            run: () => void control('resume'),
          },
        ]
      : []),
    {
      id: 'theme',
      title: theme === 'light' ? 'Switch to dark theme' : 'Switch to light theme',
      sub: 'Appearance',
      icon: theme === 'light' ? <Moon className="i" /> : <Sun className="i" />,
      run: () => {
        setThemePreference(theme === 'light' ? 'dark' : 'light');
        close();
      },
    },
    ...nav.map((n: NavEntry) => {
      const Icon = n.icon;
      return {
        id: `nav-${n.id}`,
        title: `Go to ${n.label}`,
        sub: n.id === 'queue' && active ? `${active} downloading` : 'Navigation',
        icon: <Icon className="i" />,
        run: () => go(n.path, undefined, { kind: 'nav', id: n.id }),
      };
    }),
  ];
  const nq = norm(q);
  const shownActions =
    s === 'all' || s === 'actions'
      ? actions
          .filter((a) => !nq || norm(`${a.title} ${a.sub}`).includes(nq))
          .slice(0, s === 'actions' ? 20 : q ? 4 : 6)
      : [];
  const top = films[0];
  const peopleList = s === 'all' || s === 'people' ? (remoteData?.people ?? []).slice(0, 4) : [];
  const lookupList = wantFilms ? (remoteData?.movies ?? []).filter((r) => !r.inLibrary).slice(0, 4) : [];
  const nothing =
    !!q &&
    !films.length &&
    !shownActions.length &&
    !peopleList.length &&
    !lookupList.length &&
    !remoteBusy &&
    !local.isFetching;

  return (
    <BaseDialog.Root open={open} onOpenChange={(o) => !o && close()}>
      <BaseDialog.Portal container={overlayRoot()}>
        <BaseDialog.Backdrop className="scrim palette-scrim" />
        <BaseDialog.Popup className="palette" aria-label="Command palette">
          <BaseDialog.Title className="sr-only">Command palette</BaseDialog.Title>
          <Command
            shouldFilter={false}
            loop
            value={selected}
            onValueChange={setSelected}
            label="Search films, people, or type > for actions"
          >
            <div className="p-in">
              <Search className="i" aria-hidden="true" />
              <Command.Input
                value={raw}
                onValueChange={setRaw}
                autoFocus
                placeholder="Search films, people… or type > for actions"
                onKeyDown={(ev) => {
                  if (ev.key === 'Tab') {
                    ev.preventDefault();
                    const order: Scope[] = ['all', 'films', 'people', 'actions'];
                    const i = order.indexOf(scope);
                    setScope(order[(i + (ev.shiftKey ? order.length - 1 : 1)) % order.length]!);
                  }
                  if (ev.key === 'Backspace' && (ev.metaKey || ev.ctrlKey)) {
                    ev.preventDefault();
                    setRaw('');
                  }
                }}
              />
              <span className="kbd" aria-hidden="true">
                Esc
              </span>
            </div>
            {/* biome-ignore lint/a11y/useSemanticElements: a labelled group of toggle chips, not a form */}
            <div className="scopes" role="group" aria-label="Scope">
              {(
                [
                  ['all', 'All', ''],
                  ['films', 'Films', ''],
                  ['people', 'People', '@'],
                  ['actions', 'Actions', '>'],
                ] as const
              ).map(([v, label, k]) => (
                <button
                  key={v}
                  type="button"
                  className={s === v ? 'chip is-on' : 'chip'}
                  aria-pressed={s === v}
                  tabIndex={-1}
                  onClick={() => {
                    setScope(v);
                    setRaw(parsed.q);
                  }}
                >
                  {label}
                  {k && <span className="k">{k}</span>}
                </button>
              ))}
            </div>
            <Command.List className="p-body" label="Results">
              {remoteData?.error && q.length >= 3 && (
                <p className="p-note t-caption t-muted" role="status">
                  TMDB didn’t answer ({remoteData.error}); showing your library only.
                </p>
              )}
              {nothing && (
                <div className="none" role="presentation">
                  <span className="ic" aria-hidden="true">
                    <ScanSearch className="i" />
                  </span>
                  <b>Nothing called “{q}” in the booth.</b>
                  <p>No film, person or action matches{tmdb.data?.configured ? ', here or on TMDB' : ''}.</p>
                </div>
              )}
              {!q && s === 'all' && recents.length > 0 && (
                <Command.Group heading="Recent">
                  {recents.map((r) => {
                    if (r.kind === 'film') {
                      const m = byId.get(r.id);
                      if (!m) return null;
                      const st = displayStatus(m);
                      return (
                        <Opt
                          key={`r-f-${r.id}`}
                          value={`recent-film-${r.id}`}
                          onSelect={() => openFilm(m)}
                          lead={
                            <PosterArt className="poster" src={m.posterUrl} title={m.title} color={m.dominantColor} />
                          }
                          title={m.title}
                          sub={`Film${m.year ? ` · ${m.year}` : ''} · ${statusText(m, st)}`}
                        />
                      );
                    }
                    const n = ALL_SECTIONS.find((x) => x.id === r.id);
                    if (!n) return null;
                    const Icon = n.icon;
                    return (
                      <Opt
                        key={`r-n-${r.id}`}
                        value={`recent-nav-${r.id}`}
                        onSelect={() => go(n.path, undefined, r)}
                        lead={<Icon className="i" />}
                        title={n.label}
                        sub="Navigation"
                      />
                    );
                  })}
                </Command.Group>
              )}
              {films.length > 0 && (
                <Command.Group heading="Films">
                  {films.map((f, i) => {
                    const m = byId.get(f.id);
                    const st = displayStatus({ state: f.state, monitored: f.monitored, failed: m?.failed ?? false });
                    return (
                      <Opt
                        key={f.id}
                        value={`film-${f.id}`}
                        onSelect={() => openFilm(f)}
                        lead={
                          <PosterArt className="poster" src={f.posterUrl} title={f.title} color={f.dominantColor} />
                        }
                        title={<Mark text={f.title} q={q} />}
                        sub={
                          <>
                            {f.year ? `${f.year} · ` : ''}
                            {f.field === 'originalTitle' && f.originalTitle ? `${f.originalTitle} · ` : ''}
                            <StatusText status={st}>
                              {m ? statusText(m, st) : (f.qualityName ?? qualityName(f.quality) ?? '')}
                            </StatusText>
                          </>
                        }
                        side={i === 0 ? <Kbd k="↵" /> : undefined}
                      />
                    );
                  })}
                </Command.Group>
              )}
              {peopleList.length > 0 && (
                <Command.Group heading="People">
                  {peopleList.map((p) => (
                    <Opt
                      key={p.tmdbId}
                      value={`person-${p.tmdbId}`}
                      onSelect={() => go('/discover', { tab: 'discover', list: 'people', person: p.tmdbId })}
                      lead={<UserRound className="i" />}
                      title={<Mark text={p.name} q={q} />}
                      sub={[
                        'Person',
                        p.knownForDepartment,
                        (p.knownFor ?? [])
                          .slice(0, 2)
                          .map((k) => k.title)
                          .join(', '),
                      ]
                        .filter(Boolean)
                        .join(' · ')}
                    />
                  ))}
                </Command.Group>
              )}
              {lookupList.length > 0 && (
                <Command.Group heading="On TMDB">
                  {lookupList.map((r) => (
                    <Opt
                      key={r.tmdbId}
                      value={`tmdb-${r.tmdbId}`}
                      onSelect={() => go('/discover', { q: r.title })}
                      lead={<Plus className="i" />}
                      title={<Mark text={r.title} q={q} />}
                      sub={`${r.year ? `${r.year} · ` : ''}not in your library`}
                    />
                  ))}
                </Command.Group>
              )}
              {(shownActions.length > 0 || (q && s !== 'people')) && (
                <Command.Group heading="Actions">
                  {top && s !== 'actions' && (
                    <Opt
                      value={`search-${top.id}`}
                      onSelect={() => go(`/films/${top.id}/search`, undefined, { kind: 'film', id: top.id })}
                      lead={<ScanSearch className="i" />}
                      title={`Interactive search: ${top.title}`}
                      sub="Every release the indexers have, with the reasons"
                    />
                  )}
                  {shownActions.map((a) => (
                    <Opt
                      key={a.id}
                      value={`action-${a.id}`}
                      onSelect={a.run}
                      lead={a.icon}
                      title={a.title}
                      sub={a.sub}
                      side={a.kbd?.map((k) => <Kbd key={k} k={k} />)}
                    />
                  ))}
                  {q && s !== 'actions' && (
                    <Opt
                      value="tmdb-search"
                      onSelect={() => go('/discover', { q })}
                      lead={<Plus className="i" />}
                      title={`Search TMDB for “${q}”`}
                      sub="Find films not in your library yet"
                    />
                  )}
                  {q && (
                    <Opt
                      value="clear"
                      onSelect={() => setRaw('')}
                      lead={<X className="i" />}
                      title="Clear the search"
                      sub="Back to recent items"
                      side={
                        <>
                          <Kbd k="Ctrl" />
                          <Kbd k="⌫" />
                        </>
                      }
                    />
                  )}
                </Command.Group>
              )}
              {!q && s === 'all' && (
                <Command.Group heading="Queue">
                  <Opt
                    value="nav-queue-now"
                    onSelect={() => go('/queue', undefined, { kind: 'nav', id: 'queue' })}
                    lead={<ArrowDownToLine className="i" />}
                    title="Open the queue"
                    sub={active ? `${active} downloading` : 'Nothing downloading'}
                  />
                </Command.Group>
              )}
            </Command.List>
          </Command>
          <div className="p-foot" aria-hidden="true">
            <span>
              <Kbd k="↑" />
              <Kbd k="↓" />
              move
            </span>
            <span>
              <Kbd k="↵" />
              open
            </span>
            <span>
              <Kbd k="Tab" />
              scope
            </span>
            <span className="grow" />
            <span>
              Type <Kbd k=">" /> for actions, <Kbd k="@" /> for people
            </span>
          </div>
        </BaseDialog.Popup>
      </BaseDialog.Portal>
    </BaseDialog.Root>
  );
}
