import { useInfiniteQuery, useQueries, useQuery } from '@tanstack/react-query';
import { getRouteApi, Link, useNavigate } from '@tanstack/react-router';
import { Check, ChevronRight, CircleDashed, Plus, UserRound } from 'lucide-react';
import { type FormEvent, useMemo, useState } from 'react';
import { ApiError, api, unwrap } from '@/api/client';
import { collectionQuery, useLibrary } from '@/api/queries';
import type { DiscoverMovie } from '@/api/schema';
import { imageUrl, screenKeys } from '@/api/screens';
import { Button, buttonClass, IconButton } from '@/components/ui/Button';
import { Chip } from '@/components/ui/Chip';
import { Dialog } from '@/components/ui/Dialog';
import { Notice, Skeleton } from '@/components/ui/Feedback';
import { Input } from '@/components/ui/Field';
import { PosterArt } from '@/components/ui/PosterArt';
import { Segmented } from '@/components/ui/Segmented';
import { formatNumber } from '@/lib/format';
import { useDelayedFlag } from '@/lib/useDelayedFlag';
import { NoTmdbKey, useAddOptionsState } from './AddFilmScreen';
import { AddOptionsForm } from './AddOptionsForm';
import type { AddOptions } from './addOptions';
import { useAddFilm } from './useAddFilm';

const route = getRouteApi('/discover');

type ListId = 'popular' | 'trending' | 'upcoming' | 'now_playing';
type View = 'all' | ListId | 'collections' | 'people';
const VIEWS: ReadonlyArray<readonly [View, string]> = [
  ['all', 'All lists'],
  ['popular', 'Popular'],
  ['trending', 'Trending'],
  ['upcoming', 'Upcoming'],
  ['now_playing', 'Now playing'],
  ['collections', 'Collections'],
  ['people', 'People'],
];
const TITLES: Record<ListId, string> = {
  popular: 'Popular',
  trending: 'Trending',
  upcoming: 'Upcoming',
  now_playing: 'Now playing',
};
const SHELF = 7;

export type Card = Pick<
  DiscoverMovie,
  'tmdbId' | 'title' | 'year' | 'posterUrl' | 'inLibrary' | 'movieId' | 'state' | 'monitored' | 'releaseDate'
>;

const dayMonth = new Intl.DateTimeFormat('en-GB', { day: 'numeric', month: 'short' });
function subOf(m: Card, list?: ListId) {
  if (list === 'upcoming' && m.releaseDate) {
    const d = new Date(`${m.releaseDate}T00:00:00`);
    if (!Number.isNaN(d.getTime()))
      return d.getFullYear() === new Date().getFullYear() ? dayMonth.format(d) : String(d.getFullYear());
  }
  return m.year ? String(m.year) : 'Date unknown';
}

function useList(list: ListId, window: 'day' | 'week', enabled = true) {
  return useInfiniteQuery({
    queryKey: screenKeys.discover(list, list === 'trending' ? window : ''),
    initialPageParam: 1,
    enabled,
    queryFn: ({ pageParam, signal }) =>
      unwrap(
        api.GET('/discover/{list}', {
          params: { path: { list }, query: { page: pageParam, window: list === 'trending' ? window : undefined } },
          signal,
        }),
      ),
    getNextPageParam: (p) => (p.page < p.totalPages ? p.page + 1 : undefined),
    staleTime: 10 * 60_000,
    retry: false,
  });
}

export function DiscoverTab({ onNeedKey }: { onNeedKey: () => void }) {
  const search = route.useSearch();
  const navigate = useNavigate();
  const view = (VIEWS.some((v) => v[0] === search.list) ? search.list : 'all') as View;
  const [window, setWindow] = useState<'day' | 'week'>('week');
  const [hideOwned, setHideOwned] = useState(false);
  const [options, setOptions] = useAddOptionsState();
  const [picked, setPicked] = useState<Card | null>(null);
  const { add, busy } = useAddFilm();
  const setView = (v: View) =>
    void navigate({ to: '/discover', search: (s) => ({ ...s, list: v === 'all' ? undefined : v }), replace: true });

  const quickAdd = (m: Card) => options && void add({ tmdbId: m.tmdbId, title: m.title, year: m.year }, options);
  const common = { hideOwned, onPick: setPicked, onQuickAdd: quickAdd, busy, ready: !!options, onNeedKey };

  return (
    <div className="discover">
      <div className="disc-bar">
        {/* biome-ignore lint/a11y/useSemanticElements: a labelled group of toggle chips, not a form */}
        <div className="row chips" role="group" aria-label="Lists">
          {VIEWS.map(([v, l]) => (
            <Chip key={v} pressed={view === v} onClick={() => setView(v)}>
              {l}
            </Chip>
          ))}
        </div>
        <span className="t-caption t-muted disc-hide">
          Films already in your library are marked ·{' '}
          <button type="button" className="link-btn" aria-pressed={hideOwned} onClick={() => setHideOwned((h) => !h)}>
            {hideOwned ? 'show them' : 'hide them'}
          </button>
        </span>
      </div>

      {view === 'all' && (
        <>
          <ListShelf
            list="trending"
            window={window}
            onWindow={setWindow}
            limit={SHELF}
            onSeeAll={() => setView('trending')}
            {...common}
          />
          <ListShelf list="upcoming" window={window} limit={SHELF} onSeeAll={() => setView('upcoming')} {...common} />
          <div className="disc-split">
            <CollectionsShelf limit={2} onPick={setPicked} onSeeAll={() => setView('collections')} />
            <PersonShelf compact {...common} />
          </div>
          <ListShelf list="popular" window={window} limit={SHELF} onSeeAll={() => setView('popular')} {...common} />
          <ListShelf
            list="now_playing"
            window={window}
            limit={SHELF}
            onSeeAll={() => setView('now_playing')}
            {...common}
          />
        </>
      )}
      {(view === 'popular' || view === 'trending' || view === 'upcoming' || view === 'now_playing') && (
        <ListShelf list={view} window={window} onWindow={setWindow} {...common} />
      )}
      {view === 'collections' && <CollectionsShelf onPick={setPicked} />}
      {view === 'people' && <PersonShelf {...common} />}

      <Dialog
        open={!!picked && !!options}
        onOpenChange={(o) => !o && setPicked(null)}
        wide
        className="add-dialog"
        title={picked ? `Add ${picked.title}${picked.year ? ` (${picked.year})` : ''}` : ''}
        description="Choose where it goes and how Gonema looks after it. These choices are remembered."
      >
        {picked && options && (
          <AddOptionsForm
            options={options}
            onChange={setOptions as (o: AddOptions) => void}
            busy={busy === picked.tmdbId}
            showKbd={false}
            folderName={`${picked.title}${picked.year ? ` (${picked.year})` : ''}`}
            onSubmit={() =>
              void add({ tmdbId: picked.tmdbId, title: picked.title, year: picked.year }, options).then(
                (id) => id && setPicked(null),
              )
            }
          />
        )}
      </Dialog>
    </div>
  );
}

type Common = {
  hideOwned: boolean;
  onPick: (m: Card) => void;
  onQuickAdd: (m: Card) => void;
  busy: number | null;
  ready: boolean;
  onNeedKey: () => void;
};

function ListShelf({
  list,
  window,
  onWindow,
  limit,
  onSeeAll,
  ...c
}: Common & {
  list: ListId;
  window: 'day' | 'week';
  onWindow?: (w: 'day' | 'week') => void;
  limit?: number;
  onSeeAll?: () => void;
}) {
  const q = useList(list, window);
  const all = useMemo(() => (q.data?.pages ?? []).flatMap((p) => p.results ?? []), [q.data]);
  const shown = (c.hideOwned ? all.filter((m) => !m.inLibrary) : all).slice(0, limit ?? Number.POSITIVE_INFINITY);
  const total = q.data?.pages[0]?.totalResults;
  const skeleton = useDelayedFlag(q.isPending);
  const id = `sh-${list}`;
  const needKey = q.error instanceof ApiError && q.error.status === 503;
  if (needKey) return limit ? null : <NoTmdbKey onConnect={c.onNeedKey} />;
  return (
    <section className="shelf" aria-labelledby={id}>
      <div className="shelf-hd">
        <h2 id={id}>
          {list === 'trending' ? `Trending ${window === 'day' ? 'today' : 'this week'}` : TITLES[list]}{' '}
          <span className="t-caption">{list === 'upcoming' ? 'coming soon' : 'TMDB'}</span>
        </h2>
        <div className="row">
          {list === 'trending' && onWindow && (
            <Segmented
              label="Trending period"
              value={window}
              onChange={onWindow}
              options={[
                { value: 'day', label: 'Today' },
                { value: 'week', label: 'This week' },
              ]}
            />
          )}
          {onSeeAll && (
            <Button variant="ghost" size="sm" onClick={onSeeAll}>
              See all
              <ChevronRight className="i" aria-hidden="true" />
            </Button>
          )}
        </div>
      </div>
      {q.isError ? (
        <Notice
          tone="failed"
          title={`TMDB did not send the ${TITLES[list].toLowerCase()} list.`}
          action={
            <Button size="sm" onClick={() => void q.refetch()}>
              Try again
            </Button>
          }
        >
          {q.error.message}
        </Notice>
      ) : q.isPending ? (
        skeleton && <ShelfSkeleton n={limit ?? 14} />
      ) : shown.length === 0 ? (
        <p className="t-secondary">
          {all.length ? 'Every film of this list is already in your library.' : 'TMDB has nothing here right now.'}
        </p>
      ) : (
        <ul className={limit ? 'rowp' : 'rowp grid'}>
          {shown.map((m) => (
            <DiscoverPoster key={m.tmdbId} m={m} sub={subOf(m, list)} {...c} />
          ))}
        </ul>
      )}
      {!limit && q.hasNextPage && (
        <div className="show-more">
          <Button variant="ghost" size="sm" loading={q.isFetchingNextPage} onClick={() => void q.fetchNextPage()}>
            More{total ? ` · ${formatNumber(all.length)} of ${formatNumber(total)}` : ''}
          </Button>
        </div>
      )}
    </section>
  );
}

function ShelfSkeleton({ n }: { n: number }) {
  return (
    <ul className="rowp" aria-hidden="true">
      {Array.from({ length: n }, (_, i) => (
        // biome-ignore lint/suspicious/noArrayIndexKey: static placeholders
        <li className="pc" key={i}>
          <Skeleton className="pc-sk" delay={i * 60} />
          <Skeleton className="sk-line" style={{ width: '70%', marginTop: 10 }} delay={i * 60} />
        </li>
      ))}
    </ul>
  );
}

export function DiscoverPoster({ m, sub, onPick, onQuickAdd, busy, ready }: Common & { m: Card; sub: string }) {
  const wanted = m.inLibrary && m.state === 'missing' && m.monitored;
  const label = `${m.title}${m.year ? ` (${m.year})` : ''}`;
  return (
    <li className="pc">
      <div className="pc-frame">
        {m.inLibrary && m.movieId ? (
          <Link className="pc-link" to="/films/$movieId" params={{ movieId: m.movieId }}>
            <PosterArt src={m.posterUrl} title={m.title} year={m.year} />
            <span className="sr-only">{label}, in library</span>
          </Link>
        ) : (
          <button type="button" className="pc-link" aria-haspopup="dialog" onClick={() => onPick(m)}>
            <PosterArt src={m.posterUrl} title={m.title} year={m.year} />
            <span className="sr-only">{label}: add with options</span>
          </button>
        )}
        {m.inLibrary && (
          <span className="lib-badge badge on-media" data-status={wanted ? 'missing' : 'available'}>
            {wanted ? <CircleDashed className="i" aria-hidden="true" /> : <Check className="i" aria-hidden="true" />}
            {wanted ? 'Wanted' : 'In library'}
          </span>
        )}
        {!m.inLibrary && (
          <div className="pc-bottom">
            <div className="pc-actions">
              <IconButton
                variant="on-media"
                label={`Add ${label} with your last options`}
                disabled={!ready || busy === m.tmdbId}
                aria-busy={busy === m.tmdbId || undefined}
                onClick={() => onQuickAdd(m)}
              >
                <Plus className="i i-sm" aria-hidden="true" />
              </IconButton>
            </div>
          </div>
        )}
      </div>
      <div className="pc-meta">
        <div className="pc-title">{m.title}</div>
        <div className="pc-sub">{sub}</div>
      </div>
    </li>
  );
}

/** Collections with at least one film in the library, the most started first. */
function CollectionsShelf({
  limit,
  onSeeAll,
  onPick,
}: {
  limit?: number;
  onSeeAll?: () => void;
  onPick: (m: Card) => void;
}) {
  const lib = useLibrary();
  const ids = useMemo(() => {
    const n = new Map<number, number>();
    for (const m of lib.data ?? []) if (m.collectionId) n.set(m.collectionId, (n.get(m.collectionId) ?? 0) + 1);
    return [...n.entries()]
      .sort((a, b) => b[1] - a[1])
      .slice(0, limit ?? 12)
      .map(([id]) => id);
  }, [lib.data, limit]);
  const cols = useQueries({ queries: ids.map((id) => collectionQuery(id)) });
  return (
    <section className="shelf" aria-labelledby="sh-coll">
      <div className="shelf-hd">
        <h2 id="sh-coll">Collections you’ve started</h2>
        {onSeeAll && ids.length > 0 && (
          <Button variant="ghost" size="sm" onClick={onSeeAll}>
            See all
            <ChevronRight className="i" aria-hidden="true" />
          </Button>
        )}
      </div>
      {lib.isSuccess && ids.length === 0 ? (
        <p className="t-secondary">
          None yet: films that belong to a TMDB collection gather here once one is in your library.
        </p>
      ) : (
        <ul className="coll">
          {cols.map((c, i) => {
            const d = c.data;
            if (!d) return <li key={ids[i]} className="panel coll-sk" aria-hidden="true" />;
            const parts = d.parts ?? [];
            const owned = new Set((d.movies ?? []).map((m) => m.tmdbId));
            const missing = parts.filter((p) => !owned.has(p.tmdbId));
            return (
              <li key={d.id} className="panel">
                <div className="stackp" aria-hidden="true">
                  {(d.movies ?? []).slice(0, 2).map((m, k) => (
                    <PosterArt
                      key={m.id}
                      src={imageUrl('poster', 'w342', m.posterPath)}
                      title={m.title}
                      className={k ? 'p2' : 'p1'}
                    />
                  ))}
                </div>
                <div>
                  <h3>{d.name}</h3>
                  <p>
                    {parts.length ? `${parts.length} films · ` : ''}
                    {(d.movies ?? []).length} in library
                    {d.partsError ? ' · TMDB did not answer for the rest' : ''}
                  </p>
                  {missing.length === 1 ? (
                    <Button
                      size="sm"
                      onClick={() =>
                        onPick({
                          tmdbId: missing[0]!.tmdbId,
                          title: missing[0]!.title,
                          year: missing[0]!.year ?? 0,
                          posterUrl: imageUrl('poster', 'w342', missing[0]!.posterPath),
                          inLibrary: false,
                          movieId: null,
                          state: null,
                          monitored: false,
                          releaseDate: missing[0]!.releaseDate ?? null,
                        })
                      }
                    >
                      Add the missing one
                    </Button>
                  ) : (d.movies ?? [])[0] ? (
                    <Link
                      className={buttonClass('secondary', 'sm')}
                      to="/films/$movieId"
                      params={{ movieId: d.movies![0]!.id }}
                      hash="collection"
                    >
                      {d.monitored
                        ? 'Open collection'
                        : missing.length
                          ? `Monitor · ${missing.length} missing`
                          : 'Open'}
                    </Link>
                  ) : null}
                </div>
              </li>
            );
          })}
        </ul>
      )}
    </section>
  );
}

/** A person's TMDB id from "12345", "tmdb:12345" or a themoviedb.org/person/12345-name link. */
export function personIdOf(raw: string): number | null {
  const m = /(?:person\/|tmdb:|^)(\d{1,9})(?:\b|-|$)/.exec(raw.trim());
  const id = m ? Number(m[1]) : Number.NaN;
  return Number.isInteger(id) && id > 0 ? id : null;
}

function PersonShelf({ compact, ...c }: Common & { compact?: boolean }) {
  const [raw, setRaw] = useState('');
  const [id, setId] = useState<number | null>(null);
  const [error, setError] = useState<string | null>(null);
  const q = useQuery({
    queryKey: screenKeys.discover('person', String(id ?? 0)),
    enabled: !!id,
    queryFn: ({ signal }) =>
      unwrap(api.GET('/discover/person/{tmdbId}', { params: { path: { tmdbId: id! } }, signal })),
    staleTime: 30 * 60_000,
    retry: false,
  });
  const submit = (ev: FormEvent) => {
    ev.preventDefault();
    const n = personIdOf(raw);
    setError(n ? null : 'Paste a TMDB person link (themoviedb.org/person/…) or the number in it.');
    if (n) setId(n);
  };
  const movies = (q.data?.movies ?? []).filter((m) => !c.hideOwned || !m.inLibrary);
  return (
    <section className="shelf" aria-labelledby="sh-person">
      <div className="shelf-hd">
        <div className="person-head">
          <span className="avatar" aria-hidden="true">
            <UserRound className="i i-sm" />
          </span>
          <h2 id="sh-person">
            Filmography {q.data && <span className="t-caption">{formatNumber(q.data.movies?.length ?? 0)} films</span>}
          </h2>
        </div>
      </div>
      <form className="person-form" onSubmit={submit}>
        <Input
          wrapperClassName="person-input"
          aria-label="TMDB person link or id"
          placeholder="Paste a TMDB person link…"
          value={raw}
          invalid={!!error}
          aria-describedby={error ? 'person-err' : 'person-hint'}
          onChange={(ev) => setRaw(ev.target.value)}
        />
        <Button type="submit" size="md" loading={q.isFetching}>
          Show films
        </Button>
      </form>
      {error ? (
        <p className="field-error" id="person-err">
          {error}
        </p>
      ) : (
        <p className="field-hint" id="person-hint">
          Searching people by name needs TMDB person search, which this server does not offer yet: paste the person’s
          TMDB link.
        </p>
      )}
      {q.isError && (
        <Notice tone="failed" title="That filmography could not be loaded.">
          {q.error.message}
        </Notice>
      )}
      {q.data && (
        <ul className={compact ? 'rowp four' : 'rowp grid'}>
          {movies.slice(0, compact ? 4 : undefined).map((m) => (
            <DiscoverPoster
              key={m.tmdbId}
              m={m}
              sub={[m.year || '', (m.jobs ?? [])[0] ?? ((m.characters ?? []).length ? 'cast' : '')]
                .filter(Boolean)
                .join(' · ')}
              {...c}
            />
          ))}
        </ul>
      )}
    </section>
  );
}
