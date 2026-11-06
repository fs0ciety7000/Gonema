import { useQuery, useQueryClient } from '@tanstack/react-query';
import { Link } from '@tanstack/react-router';
import {
  ArrowDownToLine,
  Bookmark,
  BookmarkMinus,
  Calendar,
  ChevronRight,
  CircleCheck,
  CircleDashed,
  Ellipsis,
  ExternalLink,
  EyeOff,
  Plus,
  Search,
} from 'lucide-react';
import { type CSSProperties, type ReactNode, useEffect, useState } from 'react';
import { ApiError } from '@/api/client';
import { keys, useProfiles, useRootFolders, useUpdateMovies } from '@/api/queries';
import {
  type CollectionDetail,
  type CollectionFilm,
  collectionDetailQuery,
  monitorCollection,
  thumbUrl,
} from '@/api/v11';
import { useMovieActions } from '@/components/movie/useMovieActions';
import { NotFound } from '@/components/shell/NotFound';
import { ThemeToggle, Topbar } from '@/components/shell/Topbar';
import { HdrFlag, QualityBadge } from '@/components/ui/Badges';
import { Button, IconButton } from '@/components/ui/Button';
import { Checkbox } from '@/components/ui/Checkbox';
import { Notice, Skeleton } from '@/components/ui/Feedback';
import { Menu, MenuItem } from '@/components/ui/Menu';
import { PosterArt } from '@/components/ui/PosterArt';
import { Select } from '@/components/ui/Select';
import { Switch } from '@/components/ui/Switch';
import { toast } from '@/components/ui/Toast';
import { startBulk } from '@/lib/bulkSearch';
import { formatBytes, formatNumber } from '@/lib/format';
import { addableOf, ago, missingOf, notInLibrary, releaseLabel, yearsOf } from './collectionModel';
import { useCollectionActions } from './useCollectionActions';

const AVAILABILITY = [
  { value: 'announced', label: 'Announced' },
  { value: 'in_cinemas', label: 'In cinemas' },
  { value: 'released', label: 'Released' },
] as const;
type Availability = (typeof AVAILABILITY)[number]['value'];

export function CollectionDetailView({ id }: { id: number }) {
  const q = useQuery(collectionDetailQuery(id));
  const c = q.data;
  const actions = useCollectionActions();
  const movieActions = useMovieActions();
  const [mon, setMon] = useState<boolean | null>(null);
  useEffect(() => setMon(c?.monitored ?? null), [c?.monitored]);
  useEffect(() => {
    if (c?.name) document.title = `${c.name} · Collections · Gonema`;
  }, [c?.name]);

  if (q.isError && q.error instanceof ApiError && q.error.status === 404) return <NotFound />;
  const films = c?.films ?? [];
  const lead = films.find((f) => f.inLibrary && f.posterUrl) ?? films.find((f) => f.posterUrl);
  const color = c?.dominantColor ?? lead?.dominantColor ?? null;
  const backdrop = c?.backdropUrl ?? lead?.posterUrl ?? null;

  return (
    <div
      className="movie-scope detail coll-detail"
      style={{ ['--movie-dominant' as string]: color ?? undefined } as CSSProperties}
    >
      <div className="atmos" aria-hidden="true">
        {backdrop && (
          <>
            <div className="backdrop-art soft">
              <img src={backdrop} alt="" decoding="async" />
            </div>
            {c?.backdropUrl && (
              <div className="backdrop-art sharp">
                <img src={c.backdropUrl} alt="" decoding="async" />
              </div>
            )}
          </>
        )}
      </div>
      <Topbar overMedia>
        <nav className="crumbs" aria-label="Breadcrumb">
          <Link to="/collections">Collections</Link>
          <ChevronRight className="i" aria-hidden="true" />
          <span aria-current="page">{c?.name ?? '…'}</span>
        </nav>
        <div className="spacer" />
        <ThemeToggle />
      </Topbar>
      <main id="main" tabIndex={-1} aria-labelledby="c-title" aria-busy={q.isPending || undefined}>
        {q.isError ? (
          <div className="page">
            <Notice
              tone="failed"
              role="alert"
              title="The collection could not be loaded."
              action={
                <Button size="sm" onClick={() => void q.refetch()}>
                  Try again
                </Button>
              }
            >
              {q.error.message}
            </Notice>
          </div>
        ) : !c ? (
          <section className="chero">
            <Skeleton className="fan-sk" />
            <div className="cinfo">
              <Skeleton className="sk-line" style={{ width: '30%' }} />
              <h1 id="c-title">
                <Skeleton className="sk-line sk-title" />
              </h1>
              <Skeleton className="sk-line" style={{ width: '50%' }} />
            </div>
          </section>
        ) : (
          <Loaded
            c={c}
            mon={mon ?? c.monitored}
            onMonitor={async (v) => {
              setMon(v);
              if (!(await actions.setMonitored(c, v))) setMon(!v);
            }}
            onAddMissing={() => void actions.addMissing(c)}
            onAddOne={(f) => void actions.addOne(c, f)}
            onSearch={(targets) => {
              if (targets.length > 1) void startBulk({ ids: targets.map((t) => t.id) });
              else void movieActions.search(targets);
            }}
          />
        )}
      </main>
    </div>
  );
}

type Target = { id: number; title: string };

function Loaded({
  c,
  mon,
  onMonitor,
  onAddMissing,
  onAddOne,
  onSearch,
}: {
  c: CollectionDetail;
  mon: boolean;
  onMonitor: (v: boolean) => void;
  onAddMissing: () => void;
  onAddOne: (f: CollectionFilm) => void;
  onSearch: (t: Target[]) => void;
}) {
  const films = c.films ?? [];
  const n = films.length || c.counts.total;
  const view = { ...c, monitored: mon };
  const miss = missingOf(view);
  const addable = addableOf(view);
  const outSoon = mon ? 0 : films.filter((f) => f.state === 'not_released' && !f.excluded).length;
  const wanted = films.filter((f) => f.state === 'wanted' && f.movieId);
  // three posters fanned out: the first film, the last one and one from the middle (library films first)
  const withArt = films.filter((f) => f.posterUrl);
  const pool = withArt.length >= 3 ? withArt : films;
  const fan = [pool[0], pool[pool.length - 1], pool[Math.floor(pool.length / 2)]]
    .filter((f): f is CollectionFilm => !!f)
    .filter((f, i, a) => a.findIndex((x) => x.tmdbId === f.tmdbId) === i);
  const years = yearsOf(c);
  const unmon = films.filter((f) => f.inLibrary && f.monitored === false).map((f) => f.title);
  const outNames = addable.map((f) => f.title);

  return (
    <>
      <section className="chero">
        <div className="fan" aria-hidden="true">
          {fan.map((f) => (
            <PosterArt key={f.tmdbId} src={f.posterUrl} title={f.title} year={f.year} color={f.dominantColor} eager />
          ))}
        </div>
        <div className="cinfo">
          <p className="t-overline">
            Collection · {formatNumber(n)} {n === 1 ? 'film' : 'films'}
            {years ? ` · ${years}` : ''}
          </p>
          <h1 id="c-title">{c.name}</h1>
          <div className="facts">
            <span>
              <b>
                {formatNumber(c.counts.inLibrary)} of {formatNumber(n)}
              </b>{' '}
              in library
            </span>
            <span className="sep" aria-hidden="true" />
            <span>{formatNumber(c.counts.onDisk)} on disk</span>
            {miss > 0 && (
              <>
                <span className="sep" aria-hidden="true" />
                <span className="status" data-status="missing">
                  <CircleDashed className="i" aria-hidden="true" />
                  {formatNumber(miss)} missing
                  {outSoon ? `, ${formatNumber(outSoon)} not out yet` : ''}
                </span>
              </>
            )}
            {c.counts.sizeOnDisk > 0 && (
              <>
                <span className="sep" aria-hidden="true" />
                <span>{formatBytes(c.counts.sizeOnDisk)}</span>
              </>
            )}
          </div>
          {c.overview && <p className="overview">{c.overview}</p>}
          <div className="cactions">
            {addable.length > 0 && (
              <Button
                variant="primary"
                size="lg"
                icon={<Plus className="i" aria-hidden="true" />}
                onClick={onAddMissing}
              >
                Add the {formatNumber(addable.length)} missing
              </Button>
            )}
            {wanted.length > 0 && (
              <Button
                size="lg"
                icon={<Search className="i" aria-hidden="true" />}
                onClick={() => onSearch(wanted.map((f) => ({ id: f.movieId!, title: f.title })))}
              >
                {wanted.length === 1
                  ? `Search ${wanted[0]!.title}`
                  : `Search the ${formatNumber(wanted.length)} wanted`}
              </Button>
            )}
            <Switch checked={mon} onCheckedChange={onMonitor}>
              Monitor collection
            </Switch>
            <Menu
              label={`More actions for ${c.name}`}
              align="end"
              trigger={
                <IconButton label="More actions for this collection" className="more">
                  <Ellipsis className="i" aria-hidden="true" />
                </IconButton>
              }
            >
              <MenuItem
                icon={<ExternalLink className="i" aria-hidden="true" />}
                onSelect={() =>
                  window.open(`https://www.themoviedb.org/collection/${c.tmdbId}`, '_blank', 'noopener,noreferrer')
                }
              >
                Open on TMDB
              </MenuItem>
            </Menu>
          </div>
        </div>
      </section>
      <div className="cbody">
        <section aria-labelledby="parts-h">
          <div className="sec-hd">
            <h2 id="parts-h">Films, in release order</h2>
            <span className="t-caption">
              {c.partsKnown
                ? `From TMDB${c.partsRefreshedAt ? ` · refreshed ${ago(c.partsRefreshedAt)}` : ''}`
                : 'Your library only · TMDB not read yet'}
            </span>
          </div>
          {c.partsError && (
            <Notice title="TMDB did not answer.">The list is the one Gonema read last. {c.partsError}</Notice>
          )}
          <ol className="parts">
            {films.map((f, i) => (
              <PartRow
                key={f.tmdbId}
                f={f}
                n={i + 1}
                monitoredCollection={mon}
                onAdd={() => onAddOne(f)}
                onSearch={() => f.movieId && onSearch([{ id: f.movieId, title: f.title }])}
              />
            ))}
          </ol>
        </section>
        <aside className="side" aria-label="Collection settings">
          <SettingsPanel c={c} mon={mon} />
          <section className="panel" aria-labelledby="why-h">
            <div className="panel-hd">
              <h2 id="why-h">What monitoring does</h2>
            </div>
            <div className="panel-bd why">
              {mon ? (
                <>
                  <p>
                    Monitored. Films TMDB adds to this collection later join your library with the settings above, and
                    Gonema grabs them on release.
                  </p>
                  <p className="t-caption t-muted">
                    Turning it off keeps every film you have; it only stops new ones from being added.
                  </p>
                </>
              ) : (
                <>
                  <p>
                    Not monitored. Turn it on to add{' '}
                    {outNames.length ? (
                      <>
                        <Names list={outNames} /> now, and
                      </>
                    ) : null}{' '}
                    any film TMDB adds to the collection later.
                  </p>
                  {unmon.length > 0 && (
                    <p>
                      Films already in your library keep their own monitoring: <Names list={unmon} sep=", " />{' '}
                      {unmon.length === 1 ? 'stays' : 'stay'} unmonitored until you change it.
                    </p>
                  )}
                  <p className="t-caption t-muted">
                    Nothing is downloaded until a film is released and meets its profile.
                  </p>
                </>
              )}
            </div>
          </section>
        </aside>
      </div>
    </>
  );
}

function Names({ list, sep = ' and ' }: { list: string[]; sep?: string }) {
  return (
    <>
      {list.map((t, i) => (
        <span key={t}>
          {i > 0 && (i === list.length - 1 && sep === ' and ' ? ' and ' : sep)}
          <b>{t}</b>
        </span>
      ))}
    </>
  );
}

function PartRow({
  f,
  n,
  monitoredCollection,
  onAdd,
  onSearch,
}: {
  f: CollectionFilm;
  n: number;
  monitoredCollection: boolean;
  onAdd: () => void;
  onSearch: () => void;
}) {
  const update = useUpdateMovies();
  const qc = useQueryClient();
  const [monitored, setMonitored] = useState(f.monitored ?? false);
  useEffect(() => setMonitored(f.monitored ?? false), [f.monitored]);
  const out = notInLibrary(f);
  const unmon = f.inLibrary && !monitored;
  const qline =
    f.state === 'on_disk' ? (
      <span className="q">
        <QualityBadge quality={f.quality} />
        {(f.hdr ?? '')
          .split(' ')
          .filter(Boolean)
          .map((h) => (
            <HdrFlag key={h}>{h}</HdrFlag>
          ))}
        {f.sizeOnDisk ? formatBytes(f.sizeOnDisk) : ''}
      </span>
    ) : null;
  let state: ReactNode = null;
  let sub = f.year ? String(f.year) : '';
  let act: ReactNode = null;
  const join = (...p: string[]) => p.filter(Boolean).join(' · ');
  switch (f.state) {
    case 'on_disk':
      state = unmon ? (
        <span className="status" data-status="unmonitored">
          <EyeOff className="i" aria-hidden="true" />
          Not monitored
        </span>
      ) : (
        <span className="status" data-status="available">
          <CircleCheck className="i" aria-hidden="true" />
          On disk
        </span>
      );
      break;
    case 'wanted':
      state = (
        <span className="badge" data-status="missing">
          <CircleDashed className="i" aria-hidden="true" />
          Wanted
        </span>
      );
      sub = join(
        sub,
        f.status === 'in_cinemas'
          ? 'In cinemas · waiting for a digital release'
          : f.lastSearchedAt
            ? `Last searched ${ago(f.lastSearchedAt)}`
            : 'Not searched yet',
      );
      if (monitored)
        act = (
          <Button variant="ghost" size="sm" icon={<Search className="i" aria-hidden="true" />} onClick={onSearch}>
            Search
          </Button>
        );
      break;
    case 'downloading':
      state = (
        <span className="badge" data-status="downloading">
          <ArrowDownToLine className="i" aria-hidden="true" />
          Downloading
        </span>
      );
      sub = join(sub, f.progress != null ? `${Math.round(f.progress * 100)}%` : '');
      break;
    case 'missing':
      state = (
        <span className="badge" data-status="missing">
          <CircleDashed className="i" aria-hidden="true" />
          Missing
        </span>
      );
      sub = join(sub, f.excluded ? 'On your exclusion list' : 'Not in your library');
      if (!f.excluded)
        act = (
          <Button size="sm" icon={<Plus className="i" aria-hidden="true" />} onClick={onAdd}>
            Add
          </Button>
        );
      break;
    case 'not_released':
      state = (
        <span className="badge">
          <Calendar className="i" aria-hidden="true" />
          Upcoming
        </span>
      );
      sub = join(
        releaseLabel(f),
        monitoredCollection && !f.excluded ? 'Added automatically when it comes out' : 'Not in your library',
      );
      if (!monitoredCollection && !f.excluded)
        act = (
          <Button size="sm" icon={<Plus className="i" aria-hidden="true" />} onClick={onAdd}>
            Add
          </Button>
        );
      break;
    case 'upcoming':
      state = (
        <span className="badge">
          <Calendar className="i" aria-hidden="true" />
          Upcoming
        </span>
      );
      sub = join(releaseLabel(f), 'In library, grabbed on release');
      break;
  }
  const toggle = () => {
    if (!f.movieId) return;
    const next = !monitored;
    setMonitored(next);
    update.mutate(
      { ids: [f.movieId], patch: { monitored: next } },
      {
        onSuccess: () => void qc.invalidateQueries({ queryKey: ['collection'] }),
        onError: (e) => {
          setMonitored(!next);
          toast.error(`${f.title} was not changed`, e instanceof ApiError ? e.detail : undefined);
        },
      },
    );
  };
  return (
    <li className={['part', out && 'out', unmon && 'unmon'].filter(Boolean).join(' ')}>
      <span className="n" aria-hidden="true">
        {n}
      </span>
      <div className="th">
        <PosterArt src={thumbUrl(f.posterUrl)} title={f.title} color={f.dominantColor} />
      </div>
      <div className="tt">
        {f.movieId ? (
          <Link to="/films/$movieId" params={{ movieId: f.movieId }}>
            {f.title}
          </Link>
        ) : (
          <Link to="/discover" search={{ q: f.title }}>
            {f.title}
          </Link>
        )}
        <span>{sub}</span>
      </div>
      <div className="st">
        {state}
        {qline}
      </div>
      {f.inLibrary && f.movieId ? (
        <IconButton
          size="sm"
          aria-pressed={monitored}
          label={monitored ? `Stop monitoring ${f.title}` : `Monitor ${f.title}`}
          title={monitored ? 'Monitored' : 'Not monitored'}
          onClick={toggle}
        >
          {monitored ? (
            <Bookmark className="i i-sm" aria-hidden="true" />
          ) : (
            <BookmarkMinus className="i i-sm" aria-hidden="true" />
          )}
        </IconButton>
      ) : (
        <span />
      )}
      <div className="act">{act}</div>
    </li>
  );
}

/** "When Gonema adds a film": saved at once (PUT /collections/{id}/monitor keeps the monitoring as it is). */
function SettingsPanel({ c, mon }: { c: CollectionDetail; mon: boolean }) {
  const qc = useQueryClient();
  const profiles = useProfiles();
  const roots = useRootFolders();
  const actions = useCollectionActions();
  const base = actions.settingsOf(c);
  const [s, setS] = useState(base);
  // back to the server's values when they change (primitives: no loop on every render)
  // biome-ignore lint/correctness/useExhaustiveDependencies: base is rebuilt from these values
  useEffect(
    () => setS(base),
    [base?.qualityProfileId, base?.rootFolderId, base?.minimumAvailability, base?.searchOnAdd],
  );
  const save = async (patch: Partial<NonNullable<typeof s>>) => {
    if (!s) return;
    const next = { ...s, ...patch };
    const prev = s;
    setS(next);
    try {
      await monitorCollection(c.id, { monitored: mon, ...next });
      void qc.invalidateQueries({ queryKey: keys.collection(c.id) });
      void qc.invalidateQueries({ queryKey: ['collections'] });
    } catch (e) {
      setS(prev);
      toast.error('The settings were not saved', e instanceof ApiError ? e.detail : undefined);
    }
  };
  const profile = profiles.data?.find((p) => p.id === s?.qualityProfileId);
  const root = roots.data?.find((r) => r.id === s?.rootFolderId);
  return (
    <section className="panel" aria-labelledby="set-h">
      <div className="panel-hd">
        <h2 id="set-h">When Gonema adds a film</h2>
      </div>
      <div className="panel-bd">
        {!s ? (
          <p className="t-secondary">Add a root folder and a quality profile in Settings first.</p>
        ) : (
          <>
            <div className="field">
              <span className="field-label" id="qp-l">
                Quality profile
              </span>
              <Select<number>
                labelledBy="qp-l"
                value={s.qualityProfileId}
                options={(profiles.data ?? []).map((p) => ({ value: p.id, label: p.name }))}
                onValueChange={(v) => void save({ qualityProfileId: v })}
              />
              <span className="field-hint">
                {profile
                  ? profile.upgradeAllowed
                    ? `Upgrades until ${profile.cutoffLabel || profile.cutoffQuality}. `
                    : 'Keeps the first good copy. '
                  : ''}
                Films already in your library keep their own profile.
              </span>
            </div>
            <div className="field">
              <span className="field-label" id="rf-l">
                Root folder{' '}
                {root?.freeSpace ? <span className="t-muted fw-400">{formatBytes(root.freeSpace)} free</span> : null}
              </span>
              <Select<number>
                labelledBy="rf-l"
                mono
                value={s.rootFolderId}
                options={(roots.data ?? []).map((r) => ({ value: r.id, label: r.path }))}
                onValueChange={(v) => void save({ rootFolderId: v })}
              />
            </div>
            <div className="field">
              <span className="field-label" id="ma-l">
                Minimum availability
              </span>
              <Select<Availability>
                labelledBy="ma-l"
                value={s.minimumAvailability}
                options={AVAILABILITY}
                onValueChange={(v) => void save({ minimumAvailability: v })}
              />
            </div>
            <Checkbox
              checked={s.searchOnAdd}
              onCheckedChange={(v) => void save({ searchOnAdd: v })}
              label="Search as soon as a film is added"
              labelClassName="t-body-sm"
            />
          </>
        )}
      </div>
    </section>
  );
}
