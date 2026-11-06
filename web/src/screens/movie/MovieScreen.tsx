import '@/styles/lot2.css';
import { useInfiniteQuery, useQuery, useQueryClient } from '@tanstack/react-query';
import { getRouteApi, Link, useNavigate } from '@tanstack/react-router';
import { ChevronDown, ChevronRight, ChevronUp, Ellipsis, Play, ScanSearch, Search } from 'lucide-react';
import { type CSSProperties, useEffect, useMemo, useRef, useState } from 'react';
import { ApiError, api, unwrap } from '@/api/client';
import type { LibraryItem } from '@/api/model';
import { collectionQuery, keys, useLibrary, useMovie, useProfiles } from '@/api/queries';
import type { HistoryEntry } from '@/api/schema';
import { openMovieDialog } from '@/components/movie/dialogStore';
import { MovieMenuItems } from '@/components/movie/MovieMenuItems';
import { useMovieActions } from '@/components/movie/useMovieActions';
import { NotFound } from '@/components/shell/NotFound';
import { SearchField, ThemeToggle, Topbar } from '@/components/shell/Topbar';
import { StatusBadge, StatusText } from '@/components/ui/Badges';
import { Button, buttonClass, IconButton } from '@/components/ui/Button';
import { Notice, Skeleton } from '@/components/ui/Feedback';
import { Menu } from '@/components/ui/Menu';
import { PosterArt } from '@/components/ui/PosterArt';
import { Switch } from '@/components/ui/Switch';
import { toast } from '@/components/ui/Toast';
import { Tooltip, TooltipProvider } from '@/components/ui/Tooltip';
import { neighbours } from '@/lib/browseOrder';
import { formatEta, formatRuntime } from '@/lib/format';
import { isTypingTarget } from '@/lib/keys';
import { qualityName } from '@/lib/quality';
import { type DisplayStatus, displayStatus, missingNote } from '@/lib/status';
import { backToLibraryTransition, playPendingFlip } from '@/lib/viewTransition';
import {
  CastDialog,
  FilePanel,
  HeroPoster,
  HistoryTimeline,
  type PersonView,
  Portrait,
  RatingRing,
  ReleaseDates,
  TrailerDialog,
} from './parts';
import { FilmSettings } from './Settings';

const route = getRouteApi('/films/$movieId');
const languageName = (() => {
  try {
    const dn = new Intl.DisplayNames(['en'], { type: 'language' });
    return (code: string) => (code ? (dn.of(code) ?? code) : '');
  } catch {
    return (code: string) => code;
  }
})();

type Ratings = { tmdb?: { value: number; votes: number } | null; imdb?: { value: number; votes: number } | null };

export function MovieScreen() {
  const { movieId } = route.useParams();
  const navigate = useNavigate();
  const qc = useQueryClient();
  const lib = useLibrary();
  const q = useMovie(movieId);
  const profiles = useProfiles();
  const actions = useMovieActions();
  const [trailer, setTrailer] = useState(false);
  const [castOpen, setCastOpen] = useState(false);
  const h1 = useRef<HTMLHeadingElement>(null);

  const item: LibraryItem | undefined = useMemo(() => lib.data?.find((m) => m.id === movieId), [lib.data, movieId]);
  const m = q.data;
  const title = m?.title ?? item?.title ?? '';
  const year = m?.year ?? item?.year ?? 0;
  const color = m?.dominantColor ?? item?.dominantColor ?? null;

  useEffect(() => {
    if (title) document.title = `${title}${year ? ` (${year})` : ''} · Gonema`;
  }, [title, year]);

  // Focus moves to the title once the page is there (after the transition).
  // biome-ignore lint/correctness/useExhaustiveDependencies: once per film
  useEffect(() => {
    const t = setTimeout(() => h1.current?.focus({ preventScroll: true }), 320);
    return () => clearTimeout(t);
  }, [movieId]);

  // J / K: next / previous film of the list it was opened from. Not animated (repeated keyboard action).
  const order = useMemo(() => (lib.data ?? []).map((x) => x.id), [lib.data]);
  const { prev, next } = neighbours(movieId, order);
  useEffect(() => {
    const onKey = (ev: KeyboardEvent) => {
      if (ev.metaKey || ev.ctrlKey || ev.altKey) return;
      if (isTypingTarget(ev.target, '[role="menu"], [role="listbox"], [role="dialog"]')) return;
      const to = ev.key === 'j' || ev.key === 'J' ? next : ev.key === 'k' || ev.key === 'K' ? prev : null;
      if (to) {
        ev.preventDefault();
        void navigate({ to: '/films/$movieId', params: { movieId: to } });
      }
    };
    window.addEventListener('keydown', onKey);
    return () => window.removeEventListener('keydown', onKey);
  }, [next, prev, navigate]);

  const history = useInfiniteQuery({
    queryKey: [...keys.history(movieId), 'pages'],
    enabled: !!m,
    initialPageParam: 0,
    queryFn: async ({ pageParam, signal }) => {
      if (pageParam === 0) return (m?.history ?? []) as HistoryEntry[];
      return ((await unwrap(
        api.GET('/movies/{id}/history', {
          params: { path: { id: movieId }, query: { before: pageParam, limit: 50 } },
          signal,
        }),
      )) ?? []) as HistoryEntry[];
    },
    getNextPageParam: (last, pages) => {
      const size = pages.length === 1 ? 20 : 50;
      return last.length >= size ? last[last.length - 1]?.id : undefined;
    },
  });
  // biome-ignore lint/correctness/useExhaustiveDependencies: the first page is the detail's history
  useEffect(() => {
    void qc.resetQueries({ queryKey: [...keys.history(movieId), 'pages'] });
  }, [m?.history]);

  const collection = useQuery({ ...collectionQuery(m?.collection?.id ?? 0), enabled: !!m?.collection?.id });

  if (q.isError && q.error instanceof ApiError && q.error.status === 404) return <NotFound />;

  const status: DisplayStatus = m
    ? displayStatus({ state: m.state as LibraryItem['state'], monitored: m.monitored, failed: m.failed })
    : item
      ? displayStatus(item)
      : 'missing';
  const lastUpgrade = (m?.history ?? []).find((e) => e.event === 'upgraded' || e.event === 'imported');
  const shownStatus: DisplayStatus = status === 'available' && lastUpgrade?.event === 'upgraded' ? 'upgraded' : status;
  const profile = profiles.data?.find((p) => p.id === m?.qualityProfileId);
  const ratings = (m?.ratings ?? {}) as Ratings;
  const cast: PersonView[] = (m?.cast ?? []) as PersonView[];
  const crew: PersonView[] = (m?.crew ?? []) as PersonView[];
  const directors = crew.filter((p) => p.job === 'Director').map((p) => p.name);
  const writers = [...new Set(crew.filter((p) => p.department === 'Writing').map((p) => p.name))].slice(0, 3);
  const studios = (m?.studios ?? []).map((s) => s.name).slice(0, 3);
  const video = (m?.videos ?? [])[0];
  const file = m?.file && (m.file as { id?: number }).id ? m.file : null;
  const download = m?.download && (m.download as { status?: string }).status ? m.download : null;

  const stateLine = (() => {
    if (!m) return null;
    const onDisk = file ? qualityName(file.quality) : null;
    const cutoff = profile ? qualityName(profile.cutoffQuality) : null;
    if (status === 'downloading' && download)
      return `Downloading · ${Math.round(download.progress * 100)}%${download.etaSeconds ? ` · ${formatEta(download.etaSeconds)} left` : ''}`;
    if (status === 'failed') return 'The last download failed: it was blocklisted and Gonema tries the next release.';
    if (status === 'unmonitored') return onDisk ? `${onDisk} on disk · not searched for upgrades` : 'Not searched for';
    if (status === 'missing')
      return `Not on disk yet · ${missingNote(m).toLowerCase()}${m.statusReason ? ` · ${m.statusReason}` : ''}`;
    if (m.state === 'upgradable')
      return `${onDisk} on disk${cutoff ? ` · cutoff ${cutoff}` : ''} · still looking for upgrades`;
    return `${onDisk ?? 'On disk'}${cutoff ? ` · cutoff ${cutoff} met` : ''}`;
  })();

  const tint = { ['--movie-dominant' as string]: color ?? undefined } as CSSProperties;
  const back = () =>
    void backToLibraryTransition(movieId, () => navigate({ to: '/films', search: (s: Record<string, unknown>) => s }));

  return (
    <div className="movie-scope detail" style={tint}>
      <div className="atmos" aria-hidden="true">
        {(m?.backdropUrl || m?.posterUrl || item?.posterUrl) && (
          <>
            <div className="backdrop-art soft">
              <img src={m?.backdropUrl ?? m?.posterUrl ?? item?.posterUrl ?? ''} alt="" decoding="async" />
            </div>
            {m?.backdropUrl && (
              <div className="backdrop-art sharp">
                <img src={m.backdropUrl} alt="" decoding="async" />
              </div>
            )}
          </>
        )}
        <div className="grain" />
      </div>

      <TooltipProvider>
        <Topbar overMedia>
          <nav className="crumbs" aria-label="Breadcrumb">
            <Link
              to="/films"
              onClick={(ev) => {
                if (ev.button !== 0 || ev.metaKey || ev.ctrlKey || ev.shiftKey) return;
                ev.preventDefault();
                back();
              }}
            >
              Films
            </Link>
            <ChevronRight className="i" aria-hidden="true" />
            <span aria-current="page">{title || '…'}</span>
          </nav>
          <div className="spacer" />
          <div className="detail-search">
            <SearchField
              compact
              placeholder="Search…"
              onSearch={(v) => void navigate({ to: '/films', search: { q: v || undefined } })}
            />
          </div>
          <Tooltip content="Previous film" shortcut="K">
            <IconButton
              label="Previous film (K)"
              disabled={!prev}
              onClick={() => prev && void navigate({ to: '/films/$movieId', params: { movieId: prev } })}
            >
              <ChevronUp className="i" aria-hidden="true" />
            </IconButton>
          </Tooltip>
          <Tooltip content="Next film" shortcut="J">
            <IconButton
              label="Next film (J)"
              disabled={!next}
              onClick={() => next && void navigate({ to: '/films/$movieId', params: { movieId: next } })}
            >
              <ChevronDown className="i" aria-hidden="true" />
            </IconButton>
          </Tooltip>
          <ThemeToggle />
        </Topbar>
      </TooltipProvider>

      <main id="main">
        <section className="hero" aria-labelledby="film-title">
          <HeroPoster
            id={movieId}
            small={m?.posterUrl ?? item?.posterUrl ?? null}
            large={m?.posterUrlLarge ?? null}
            title={title}
            color={color}
            onReady={(el) => playPendingFlip(movieId, el)}
          />
          <div className="hero-info">
            <p className="t-overline">
              {(m?.genres ?? item?.genres ?? []).slice(0, 3).join(' · ')}
              {m?.collection?.name && (
                <>
                  {(m.genres ?? []).length > 0 && ' · '}
                  <a href="#collection">{m.collection.name}</a>
                </>
              )}
            </p>
            <h1 id="film-title" ref={h1} tabIndex={-1}>
              {title || <Skeleton className="sk-line sk-title" />}
            </h1>
            <div className="meta-row">
              {year > 0 && <span>{year}</span>}
              {m?.runtimeMin ? (
                <>
                  <span className="sep" aria-hidden="true" />
                  <span>{formatRuntime(m.runtimeMin)}</span>
                </>
              ) : null}
              {m?.certification && (
                <>
                  <span className="sep" aria-hidden="true" />
                  <span className="cert" role="img" aria-label={`Rated ${m.certification}`}>
                    {m.certification}
                  </span>
                </>
              )}
              {m?.originalLanguage && (
                <>
                  <span className="sep" aria-hidden="true" />
                  <span>{languageName(m.originalLanguage)}</span>
                </>
              )}
            </div>
            <div className="ratings">
              <RatingRing source="TMDB" rating={ratings.tmdb} />
              <RatingRing source="IMDb" rating={ratings.imdb} />
              {m && (
                <div className="state-line">
                  <StatusBadge status={shownStatus} lg />
                  <span>{stateLine}</span>
                </div>
              )}
            </div>
            {m?.tagline && <p className="tagline">{m.tagline}</p>}
            {m ? (
              m.overview && <p className="synopsis">{m.overview}</p>
            ) : (
              <div className="synopsis" aria-hidden="true">
                <Skeleton className="sk-line" style={{ width: '90%' }} />
                <Skeleton className="sk-line" style={{ width: '80%', marginTop: 10 }} />
              </div>
            )}
            {(directors.length > 0 || writers.length > 0 || studios.length > 0) && (
              <dl className="credits">
                {directors.length > 0 && (
                  <div>
                    <dt>{directors.length > 1 ? 'Directors' : 'Director'}</dt>
                    <dd>{directors.join(', ')}</dd>
                  </div>
                )}
                {writers.length > 0 && (
                  <div>
                    <dt>{writers.length > 1 ? 'Writers' : 'Writer'}</dt>
                    <dd>{writers.join(', ')}</dd>
                  </div>
                )}
                {studios.length > 0 && (
                  <div>
                    <dt>{studios.length > 1 ? 'Studios' : 'Studio'}</dt>
                    <dd>{studios.join(', ')}</dd>
                  </div>
                )}
              </dl>
            )}
            {m && (
              <div className="actions">
                <Button
                  variant="tint"
                  size="lg"
                  icon={<Search className="i" aria-hidden="true" />}
                  onClick={() => void actions.search([{ id: m.id, title: m.title }])}
                >
                  Search now
                </Button>
                <Link className={buttonClass('secondary', 'lg')} to="/films/$movieId/search" params={{ movieId: m.id }}>
                  <ScanSearch className="i" aria-hidden="true" />
                  Interactive search
                </Link>
                {video && (
                  <Button
                    variant="secondary"
                    size="lg"
                    icon={<Play className="i" aria-hidden="true" />}
                    onClick={() => setTrailer(true)}
                  >
                    Trailer
                  </Button>
                )}
                <Switch
                  tint
                  checked={m.monitored}
                  onCheckedChange={(v) => void actions.setMonitored([{ id: m.id, title: m.title }], v)}
                >
                  Monitored
                </Switch>
                <Menu
                  label={`Actions for ${m.title}`}
                  align="end"
                  trigger={
                    <IconButton label="More actions" className="more">
                      <Ellipsis className="i" aria-hidden="true" />
                    </IconButton>
                  }
                >
                  <MovieMenuItems
                    movie={{
                      id: m.id,
                      title: m.title,
                      monitored: m.monitored,
                      qualityProfileId: m.qualityProfileId,
                      rootFolderId: m.rootFolderId,
                      path: m.path,
                      hasFile: !!file,
                    }}
                  />
                </Menu>
              </div>
            )}
          </div>
        </section>

        {q.isError && !(q.error instanceof ApiError && q.error.status === 404) && (
          <div className="body">
            <Notice
              tone="failed"
              role="alert"
              title="This film could not be loaded."
              action={
                <Button size="sm" onClick={() => void q.refetch()}>
                  Try again
                </Button>
              }
            >
              {q.error.message}
            </Notice>
          </div>
        )}

        {m && (
          <div className="body">
            <div className="col">
              {cast.length > 0 && (
                <section aria-labelledby="cast-h">
                  <div className="sec-hd">
                    <h2 id="cast-h">Cast</h2>
                    <button type="button" className="link-btn t-caption" onClick={() => setCastOpen(true)}>
                      Full cast &amp; crew
                    </button>
                  </div>
                  <ul className="cast">
                    {cast.slice(0, 6).map((p) => (
                      <li className="person" key={`${p.tmdbId}-${p.character}`}>
                        <Portrait p={p} />
                        <b>{p.name}</b>
                        <span>{p.character}</span>
                      </li>
                    ))}
                    {cast.length > 6 && (
                      <li className="person more">
                        <button
                          type="button"
                          className="ph"
                          onClick={() => setCastOpen(true)}
                          aria-label={`${cast.length - 6} more cast members`}
                        >
                          +{cast.length - 6}
                        </button>
                        <b>Full cast</b>
                        <span>&nbsp;</span>
                      </li>
                    )}
                  </ul>
                </section>
              )}

              {m.collection?.name && (
                <section id="collection" className="panel collection" aria-labelledby="coll-h">
                  <ul className="films">
                    {(m.collection.inLibrary ?? []).map((p) => {
                      const st = displayStatus({
                        state: p.state as LibraryItem['state'],
                        monitored: p.monitored,
                        failed: false,
                      });
                      return (
                        <li
                          key={p.id}
                          className={`mini${p.id === m.id ? ' current' : ''}${st === 'missing' ? ' missing' : ''}`}
                        >
                          <Link
                            to="/films/$movieId"
                            params={{ movieId: p.id }}
                            className="f"
                            aria-label={`${p.title} (${p.year})`}
                            aria-current={p.id === m.id ? 'page' : undefined}
                          >
                            <PosterArt src={p.posterUrl} title={p.title} color={p.dominantColor} />
                          </Link>
                          <b>{p.title}</b>
                          <StatusText status={st}>{p.year || '—'}</StatusText>
                        </li>
                      );
                    })}
                  </ul>
                  <div className="info">
                    <div>
                      <p className="t-overline">Collection</p>
                      <h2 id="coll-h" className="t-title-lg">
                        {m.collection.name}
                      </h2>
                      <p>
                        {collection.data?.parts
                          ? `${collection.data.parts.length} films · ${(m.collection.inLibrary ?? []).length} in your library.`
                          : `${(m.collection.inLibrary ?? []).length} in your library.`}
                        {m.collection.monitored ? ' Gonema adds the others as they are announced.' : ''}
                      </p>
                    </div>
                    <div className="row coll-row">
                      <CollectionSwitch
                        id={m.collection.id}
                        monitored={m.collection.monitored}
                        movieId={m.id}
                        defaults={{
                          qualityProfileId: m.qualityProfileId,
                          rootFolderId: m.rootFolderId,
                          minimumAvailability: m.minimumAvailability,
                        }}
                      />
                      <Link className={buttonClass('ghost', 'sm')} to="/collections" search={{ id: m.collection.id }}>
                        Open
                        <ChevronRight className="i" aria-hidden="true" />
                      </Link>
                    </div>
                  </div>
                </section>
              )}

              <HistoryTimeline
                entries={(history.data?.pages ?? [m.history ?? []]).flat() as HistoryEntry[]}
                canLoadMore={!!history.hasNextPage}
                loadingMore={history.isFetchingNextPage}
                onLoadMore={() => void history.fetchNextPage()}
              />
            </div>

            <aside className="col" aria-label="File and settings">
              {file ? (
                <FilePanel
                  file={file as never}
                  runtimeMin={m.runtimeMin}
                  onRename={() => openMovieDialog({ kind: 'rename', targets: [{ id: m.id, title: m.title }] })}
                  onDelete={() => openMovieDialog({ kind: 'deleteFile', target: { id: m.id, title: m.title } })}
                />
              ) : (
                <section className="panel" aria-labelledby="file-h">
                  <div className="panel-hd">
                    <h2 id="file-h">File</h2>
                  </div>
                  <div className="panel-bd">
                    <p className="t-secondary">
                      No file yet.{' '}
                      {m.monitored
                        ? 'Gonema searches for a release that fits the profile.'
                        : 'Monitor the film to let Gonema search for it.'}
                    </p>
                  </div>
                </section>
              )}
              <ReleaseDates
                cinema={m.releaseCinema}
                digital={m.releaseDigital}
                physical={m.releasePhysical}
                regions={m.releaseRegions}
              />
              <FilmSettings
                movie={{
                  id: m.id,
                  title: m.title,
                  monitored: m.monitored,
                  qualityProfileId: m.qualityProfileId,
                  minimumAvailability: m.minimumAvailability,
                  rootFolderId: m.rootFolderId,
                  tags: m.tags ?? [],
                }}
              />
            </aside>
          </div>
        )}
      </main>

      {video && m && (
        <TrailerDialog open={trailer} onOpenChange={setTrailer} youtubeKey={video.youtubeKey} title={m.title} />
      )}
      {m && <CastDialog open={castOpen} onOpenChange={setCastOpen} cast={cast} crew={crew} title={m.title} />}
    </div>
  );
}

function CollectionSwitch({
  id,
  monitored,
  movieId,
  defaults,
}: {
  id: number;
  monitored: boolean;
  movieId: number;
  /** New films of the saga get this film's profile, folder and availability. */
  defaults: {
    qualityProfileId: number;
    rootFolderId: number;
    minimumAvailability: 'announced' | 'in_cinemas' | 'released';
  };
}) {
  const qc = useQueryClient();
  const [value, setValue] = useState(monitored);
  useEffect(() => setValue(monitored), [monitored]);
  const set = async (v: boolean, undoable = true) => {
    setValue(v);
    try {
      await unwrap(
        api.PUT('/collections/{id}/monitor', { params: { path: { id } }, body: { monitored: v, ...defaults } }),
      );
      void qc.invalidateQueries({ queryKey: keys.movie(movieId) });
      if (undoable)
        toast.show({
          title: v ? 'Monitoring the collection' : 'Stopped monitoring the collection',
          body: v ? 'New films of the saga are added when announced.' : 'Films already added stay in the library.',
          undo: () => void set(!v, false),
        });
    } catch (e) {
      setValue(!v);
      toast.error('The collection was not changed', e instanceof Error ? e.message : undefined);
    }
  };
  return (
    <Switch tint checked={value} onCheckedChange={(v) => void set(v)}>
      Monitor collection
    </Switch>
  );
}
