import '@/styles/v11.css';
import { keepPreviousData, useInfiniteQuery } from '@tanstack/react-query';
import { getRouteApi, Link, useNavigate } from '@tanstack/react-router';
import {
  ArrowUpDown,
  Bookmark,
  BookmarkMinus,
  Calendar,
  CircleCheck,
  CircleDashed,
  Compass,
  EyeOff,
  FolderOpen,
  ListFilter,
} from 'lucide-react';
import { type CSSProperties, memo, useEffect, useMemo, useRef, useState } from 'react';
import { COLLECTION_PAGE, type CollectionListItem, type CollectionSort, fetchCollections, thumbUrl } from '@/api/v11';
import { SearchField, ThemeToggle, Topbar } from '@/components/shell/Topbar';
import { Button, buttonClass, IconButton } from '@/components/ui/Button';
import { Notice, Skeleton } from '@/components/ui/Feedback';
import { Input } from '@/components/ui/Field';
import { Menu, MenuRadioGroup, MenuRadioItem } from '@/components/ui/Menu';
import { PosterArt } from '@/components/ui/PosterArt';
import { Segmented } from '@/components/ui/Segmented';
import { formatNumber } from '@/lib/format';
import { useDelayedFlag } from '@/lib/useDelayedFlag';
import { CollectionDetailView } from './CollectionDetail';
import { missingOf, notInLibrary, pipOf, yearsOf } from './collectionModel';
import { useCollectionActions } from './useCollectionActions';

const route = getRouteApi('/collections');

type CollectionsSearch = import('@/router').CollectionsSearch;

const SORTS: ReadonlyArray<[CollectionSort, string, string]> = [
  ['name', 'Name', 'sorted by name'],
  ['missing', 'Missing films', 'most films missing first'],
  ['year', 'Newest film', 'newest film first'],
  ['added', 'Recently added', 'most recently added first'],
];

export function CollectionsScreen() {
  const search = route.useSearch();
  if (search.id) return <CollectionDetailView id={search.id} />;
  return <CollectionsGrid />;
}

function CollectionsGrid() {
  const search = route.useSearch();
  const navigate = useNavigate();
  const sort: CollectionSort = search.by ?? 'name';
  const show = search.show ?? 'all';
  const [text, setText] = useState(search.q ?? '');
  useEffect(() => {
    document.title = 'Collections · Gonema';
  }, []);
  // the name filter goes to the URL (and the server) 250 ms after typing stops
  useEffect(() => {
    const t = setTimeout(() => {
      if ((search.q ?? '') !== text)
        void navigate({
          from: '/collections',
          to: '/collections',
          search: (s) => ({ ...s, q: text || undefined }),
          replace: true,
        });
    }, 250);
    return () => clearTimeout(t);
  }, [text, search.q, navigate]);

  const query = { sort, show, q: search.q ?? '' } as const;
  const q = useInfiniteQuery({
    queryKey: ['collections', query],
    queryFn: ({ pageParam, signal }) => fetchCollections(query, pageParam, signal),
    initialPageParam: 1,
    getNextPageParam: (last) => (last.page * last.pageSize < last.total ? last.page + 1 : undefined),
    placeholderData: keepPreviousData,
    staleTime: 30_000,
  });
  const items = useMemo(() => (q.data?.pages ?? []).flatMap((p) => p.collections ?? []), [q.data]);
  const first = q.data?.pages[0];
  const totals = first?.totals;
  const total = first?.total ?? 0;
  const skeleton = useDelayedFlag(q.isPending);

  // the next page loads when the end of the grid comes near
  const end = useRef<HTMLParagraphElement>(null);
  const { hasNextPage, isFetchingNextPage, fetchNextPage } = q;
  useEffect(() => {
    const el = end.current;
    if (!el || !hasNextPage) return;
    const io = new IntersectionObserver(
      (e) => {
        if (e[0]?.isIntersecting && !isFetchingNextPage) void fetchNextPage();
      },
      { rootMargin: '600px 0px' },
    );
    io.observe(el);
    return () => io.disconnect();
  }, [hasNextPage, isFetchingNextPage, fetchNextPage]);

  const set = (patch: Partial<CollectionsSearch>) =>
    void navigate({ from: '/collections', to: '/collections', search: (s) => ({ ...s, ...patch }), replace: true });
  const sortLabel = SORTS.find((s) => s[0] === sort)!;
  const empty = totals && totals.collections === 0;

  return (
    <>
      <Topbar>
        <SearchField />
        <div className="spacer" />
        <ThemeToggle />
      </Topbar>
      <main id="main" className="page coll-page" tabIndex={-1}>
        <div className="page-head">
          <div>
            <h1>Collections</h1>
            <p className="sub">
              {!totals
                ? ' '
                : empty
                  ? 'None yet'
                  : [
                      `${formatNumber(totals.collections)} ${totals.collections === 1 ? 'collection' : 'collections'}`,
                      `${formatNumber(totals.films)} films in them`,
                      `${formatNumber(totals.notInLibrary)} not in your library`,
                      `${formatNumber(totals.upcoming)} upcoming`,
                    ].join(' · ')}
            </p>
          </div>
        </div>
        {q.isError && !first ? (
          <Notice
            tone="failed"
            role="alert"
            title="The collections could not be loaded."
            action={
              <Button size="sm" onClick={() => void q.refetch()}>
                Try again
              </Button>
            }
          >
            {(q.error as Error).message}
          </Notice>
        ) : empty ? (
          <EmptyCollections />
        ) : (
          <>
            <div className="filters" role="toolbar" aria-label="Collection filters">
              <Segmented<'all' | 'missing' | 'monitored'>
                label="Show"
                value={show}
                onChange={(v) => set({ show: v === 'all' ? undefined : v })}
                options={[
                  {
                    value: 'all',
                    label: <>All {totals && <span className="n">{formatNumber(totals.collections)}</span>}</>,
                  },
                  {
                    value: 'missing',
                    label: <>Missing films {totals && <span className="n">{formatNumber(totals.withMissing)}</span>}</>,
                  },
                  {
                    value: 'monitored',
                    label: <>Monitored {totals && <span className="n">{formatNumber(totals.monitored)}</span>}</>,
                  },
                ]}
              />
              <Input
                wrapperClassName="find"
                icon={<ListFilter className="i" aria-hidden="true" />}
                type="search"
                placeholder="Filter by name…"
                aria-label="Filter collections by name"
                value={text}
                maxLength={200}
                onChange={(ev) => setText(ev.target.value)}
                onKeyDown={(ev) => {
                  if (ev.key === 'Escape' && text) setText('');
                }}
              />
              <div className="spacer" />
              <Menu
                label="Sort collections"
                align="end"
                trigger={
                  <Button variant="ghost" size="sm" icon={<ArrowUpDown className="i" aria-hidden="true" />}>
                    <span className="t-muted fw-500">Sort</span> <span className="fw-600">{sortLabel[1]}</span>
                  </Button>
                }
              >
                <MenuRadioGroup<CollectionSort>
                  value={sort}
                  onValueChange={(v) => set({ by: v === 'name' ? undefined : v })}
                >
                  {SORTS.map(([v, label]) => (
                    <MenuRadioItem key={v} value={v} selected={v === sort}>
                      {label}
                    </MenuRadioItem>
                  ))}
                </MenuRadioGroup>
              </Menu>
              <Link className={buttonClass('ghost', 'sm')} to="/discover" search={{ tab: 'discover' }}>
                <Compass className="i" aria-hidden="true" />
                Find more on TMDB
              </Link>
            </div>
            {first && !items.length ? (
              <div className="empty">
                <h2>No collection matches.</h2>
                <p>Try another name, or show every collection.</p>
                <div className="actions">
                  <Button
                    size="sm"
                    onClick={() => {
                      setText('');
                      set({ show: undefined, q: undefined });
                    }}
                  >
                    Clear filters
                  </Button>
                </div>
              </div>
            ) : (
              <section
                className="cgrid"
                aria-label={`Collections, ${sortLabel[2]}`}
                aria-busy={q.isPending || q.isFetching || undefined}
              >
                {items.map((c, i) => (
                  <CollectionCard key={c.id} c={c} eager={i < 8} />
                ))}
                {(skeleton || isFetchingNextPage) &&
                  [0, 1, 2, 3].map((i) => (
                    <div className="cc sk" aria-hidden="true" key={`sk-${i}`}>
                      <Skeleton className="cc-frame" delay={i * 60} />
                      <div className="cc-meta">
                        <Skeleton className="sk-line" style={{ width: '70%' }} delay={i * 60} />
                        <Skeleton className="sk-line" style={{ width: '45%', height: 10 }} delay={i * 60} />
                      </div>
                    </div>
                  ))}
              </section>
            )}
            {hasNextPage && (
              <p className="grid-end" ref={end} aria-live="polite">
                Loading {formatNumber(Math.min(COLLECTION_PAGE, total - items.length))} more{' '}
                {total - items.length === 1 ? 'collection' : 'collections'}…
              </p>
            )}
          </>
        )}
      </main>
    </>
  );
}

function EmptyCollections() {
  return (
    <div className="empty-wrap">
      <div className="empty">
        <div className="ghosts" aria-hidden="true">
          <span />
          <span />
          <span />
        </div>
        <h2>No sagas on the bill yet.</h2>
        <p>
          Collections appear on their own when a film in your library belongs to one on TMDB. Add one film of a series
          and the rest of the set shows up here.
        </p>
        <div className="actions">
          <Link className={buttonClass('primary')} to="/discover" search={{ tab: 'discover' }}>
            <Compass className="i" aria-hidden="true" />
            Browse collections
          </Link>
          <Link className={buttonClass('secondary')} to="/discover" search={{ tab: 'import' }}>
            <FolderOpen className="i" aria-hidden="true" />
            Import a folder
          </Link>
        </div>
        <span className="hintline">Collections are read from TMDB when a film is added or refreshed.</span>
      </div>
    </div>
  );
}

/** Card of the grid: the whole frame is the link; the monitor toggle is a sibling (never nested). */
const CollectionCard = memo(function CollectionCard({ c, eager }: { c: CollectionListItem; eager: boolean }) {
  const actions = useCollectionActions();
  const [mon, setMon] = useState(c.monitored);
  useEffect(() => setMon(c.monitored), [c.monitored]);
  const films = c.films ?? [];
  const n = films.length || c.counts.total;
  const lib = c.counts.inLibrary;
  const miss = missingOf(c);
  const soon = c.counts.upcoming;
  const shown = films.slice(0, films.length > 5 ? 4 : 5);
  const lead = films.find((f) => f.inLibrary && f.posterUrl) ?? films.find((f) => f.posterUrl);
  const backdrop = c.backdropUrl ?? lead?.posterUrl ?? null;
  const years = yearsOf(c);
  const titleId = `cc-${c.id}`;
  const toggle = async () => {
    const next = !mon;
    setMon(next);
    if (!(await actions.setMonitored(c, next))) setMon(!next);
  };
  return (
    <article
      className="cc"
      aria-labelledby={titleId}
      style={{ ['--cc-color' as string]: c.dominantColor ?? undefined } as CSSProperties}
    >
      <div className="cc-frame">
        <div className="backdrop-art" aria-hidden="true">
          {backdrop && <img src={backdrop} alt="" loading={eager ? 'eager' : 'lazy'} decoding="async" />}
        </div>
        <Link
          className="cc-link"
          to="/collections"
          search={{ id: c.id }}
          aria-label={`${c.name}, ${lib} of ${n} in library`}
        />
        <div className="cc-top">
          {miss > 0 && (
            <span className="badge on-media" data-status="missing">
              <CircleDashed className="i" aria-hidden="true" />
              {formatNumber(miss)} missing
            </span>
          )}
          {soon > 0 && (
            <span className="badge on-media">
              <Calendar className="i" aria-hidden="true" />
              {formatNumber(soon)} upcoming
            </span>
          )}
          {!miss && !soon && (
            <span className="badge on-media" data-status="available">
              <CircleCheck className="i" aria-hidden="true" />
              Complete
            </span>
          )}
        </div>
        <IconButton
          variant="on-media"
          className="cc-mon"
          aria-pressed={mon}
          label={mon ? `Stop monitoring ${c.name}` : `Monitor ${c.name}`}
          title={mon ? 'Monitored: new films are added automatically' : 'Not monitored'}
          onClick={() => void toggle()}
        >
          {mon ? (
            <Bookmark className="i i-sm" aria-hidden="true" />
          ) : (
            <BookmarkMinus className="i i-sm" aria-hidden="true" />
          )}
        </IconButton>
        <div className="cc-posters" aria-hidden="true">
          {shown.map((f) => (
            <div className={notInLibrary(f) ? 'p out' : 'p'} key={f.tmdbId}>
              <PosterArt src={thumbUrl(f.posterUrl)} title={f.title} color={f.dominantColor} eager={eager} />
            </div>
          ))}
          {films.length > 5 && <span className="more">+{films.length - 4}</span>}
        </div>
      </div>
      <div className="cc-meta">
        <div className="cc-title" id={titleId}>
          {c.name}
        </div>
        <div className="cc-count">
          <span>
            <b>{formatNumber(lib)}</b> of {formatNumber(n)} in library
          </span>
          <span className="pips" aria-hidden="true">
            {films.map((f) => (
              <i key={f.tmdbId} className={pipOf(f) || undefined} />
            ))}
          </span>
        </div>
        <div className="cc-sub">
          {years && (
            <>
              <span>{years}</span>
              <span className="sep" aria-hidden="true" />
            </>
          )}
          {mon ? (
            <span className="status on">
              <Bookmark className="i" aria-hidden="true" />
              Monitored
            </span>
          ) : (
            <span className="status" data-status="unmonitored">
              <EyeOff className="i" aria-hidden="true" />
              Not monitored
            </span>
          )}
        </div>
      </div>
    </article>
  );
});
