import '@/styles/lot2.css';
import { keepPreviousData, useQuery } from '@tanstack/react-query';
import { getRouteApi, Link, useNavigate } from '@tanstack/react-router';
import { Check, ChevronRight, CircleDashed, KeyRound, Plus, Search, SearchX } from 'lucide-react';
import { lazy, Suspense, useEffect, useMemo, useRef, useState } from 'react';
import { ApiError } from '@/api/client';
import { useLibrary, useProfiles, useRootFolders } from '@/api/queries';
import type { LookupResult } from '@/api/schema';
import { imageUrl, lookupQuery, tmdbQuery } from '@/api/screens';
import { ThemeToggle, Topbar } from '@/components/shell/Topbar';
import { Button, IconButton } from '@/components/ui/Button';
import { cx } from '@/components/ui/cx';
import { Notice, Skeleton } from '@/components/ui/Feedback';
import { PosterArt } from '@/components/ui/PosterArt';
import { Tabs } from '@/components/ui/Tabs';
import { formatDate, formatNumber } from '@/lib/format';
import { useDelayedFlag } from '@/lib/useDelayedFlag';
import { AddOptionsForm } from './AddOptionsForm';
import { type AddOptions, readAddOptions } from './addOptions';
import { TmdbKeyDialog } from './TmdbKeyDialog';
import { useAddFilm } from './useAddFilm';

const DiscoverTab = lazy(() => import('./DiscoverTab').then((m) => ({ default: m.DiscoverTab })));
const ImportTab = lazy(() => import('./import/ImportTab').then((m) => ({ default: m.ImportTab })));

const route = getRouteApi('/discover');
const DEBOUNCE_MS = 250;

const languageName = (() => {
  try {
    const dn = new Intl.DisplayNames(['en'], { type: 'language' });
    return (code: string) => (code ? (dn.of(code) ?? code) : '');
  } catch {
    return (code: string) => code;
  }
})();

/** Options remembered from the last add, validated against the folders and profiles that exist now. */
export function useAddOptionsState() {
  const roots = useRootFolders();
  const profiles = useProfiles();
  const ready = roots.isSuccess && profiles.isSuccess;
  const [options, setOptions] = useState<AddOptions | null>(null);
  useEffect(() => {
    if (ready && !options)
      setOptions(
        readAddOptions(
          (roots.data ?? []).map((r) => r.id),
          (profiles.data ?? []).map((p) => p.id),
        ),
      );
  }, [ready, options, roots.data, profiles.data]);
  return [options, setOptions] as const;
}

/** No TMDB key: nothing here works without one, so say so and offer the form. */
export function NoTmdbKey({ onConnect }: { onConnect: () => void }) {
  return (
    <div className="empty no-key">
      <span className="glyph" aria-hidden="true">
        <KeyRound className="i" />
      </span>
      <h2>Film metadata isn’t connected yet.</h2>
      <p>
        Searching, discovering and adding films all come from The Movie Database. A free key takes two minutes and stays
        encrypted on this server.
      </p>
      <div className="actions">
        <Button variant="primary" icon={<KeyRound className="i" aria-hidden="true" />} onClick={onConnect}>
          Add a TMDB key
        </Button>
      </div>
    </div>
  );
}

export function AddFilmScreen() {
  const search = route.useSearch();
  const navigate = useNavigate();
  const tab = search.tab ?? 'search';
  const tmdb = useQuery(tmdbQuery);
  const [keyOpen, setKeyOpen] = useState(false);
  const noKey = tmdb.data?.configured === false;

  return (
    <>
      <Topbar>
        <nav className="crumbs" aria-label="Breadcrumb">
          {tab === 'import' ? (
            <>
              <Link to="/discover">Add &amp; discover</Link>
              <ChevronRight className="i" aria-hidden="true" />
              <span aria-current="page">Import a folder</span>
            </>
          ) : (
            <span aria-current="page">Add &amp; discover</span>
          )}
        </nav>
        <div className="spacer" />
        <ThemeToggle />
      </Topbar>
      <main id="main" className="page add-page" tabIndex={-1}>
        <div className="add-head">
          <h1>{tab === 'import' ? 'Import a folder' : 'Add a film'}</h1>
          <Tabs
            label="Add, discover or import"
            panelId="add-panel"
            className="tabs-plain"
            value={tab}
            onChange={(t) =>
              void navigate({
                to: '/discover',
                search: (s) => ({ ...s, tab: t === 'search' ? undefined : t, scan: undefined }),
              })
            }
            items={[
              { value: 'search', label: 'Search' },
              { value: 'discover', label: 'Discover' },
              { value: 'import', label: 'Import folder' },
            ]}
          />
        </div>
        <div id="add-panel" role="tabpanel" aria-labelledby={`tab-add-panel-${tab}`}>
          {noKey ? (
            <NoTmdbKey onConnect={() => setKeyOpen(true)} />
          ) : tab === 'search' ? (
            <SearchTab initial={search.q ?? ''} onNeedKey={() => setKeyOpen(true)} />
          ) : tab === 'import' ? (
            <Suspense fallback={null}>
              <ImportTab scanId={search.scan} />
            </Suspense>
          ) : (
            <Suspense fallback={null}>
              <DiscoverTab onNeedKey={() => setKeyOpen(true)} />
            </Suspense>
          )}
        </div>
      </main>
      <TmdbKeyDialog open={keyOpen} onOpenChange={setKeyOpen} />
    </>
  );
}

function useDebounced<T>(value: T, ms: number): T {
  const [v, setV] = useState(value);
  useEffect(() => {
    const t = setTimeout(() => setV(value), ms);
    return () => clearTimeout(t);
  }, [value, ms]);
  return v;
}

function SearchTab({ initial, onNeedKey }: { initial: string; onNeedKey: () => void }) {
  const navigate = useNavigate();
  const [text, setText] = useState(initial);
  const q = useDebounced(text.trim(), DEBOUNCE_MS);
  const lookup = useQuery({ ...lookupQuery(q), placeholderData: keepPreviousData });
  const lib = useLibrary();
  const [active, setActive] = useState(0);
  const [options, setOptions] = useAddOptionsState();
  const { add, busy } = useAddFilm();
  const input = useRef<HTMLInputElement>(null);

  // Keep ?q= in the URL (shareable, survives reload) without stacking history entries.
  useEffect(() => {
    void navigate({ to: '/discover', search: (s) => ({ ...s, q: q || undefined }), replace: true });
  }, [q, navigate]);
  useEffect(() => {
    input.current?.focus();
  }, []);

  const results = q ? (lookup.data ?? []) : [];
  const stale = lookup.isPlaceholderData;
  // biome-ignore lint/correctness/useExhaustiveDependencies: a new result list starts at its first film
  useEffect(() => setActive(0), [lookup.data]);
  const selected: LookupResult | undefined = results[Math.min(active, results.length - 1)];
  const libState = useMemo(() => new Map((lib.data ?? []).map((m) => [m.id, m])), [lib.data]);
  const showSkeleton = useDelayedFlag(!!q && lookup.isFetching && !lookup.data);
  const needsKey = lookup.error instanceof ApiError && lookup.error.status === 503;

  const addSelected = (r: LookupResult | undefined) => {
    if (!r || !options) return;
    if (r.inLibrary && r.movieId) {
      void navigate({ to: '/films/$movieId', params: { movieId: r.movieId } });
      return;
    }
    void add({ tmdbId: r.tmdbId, title: r.title, year: r.year }, options);
  };

  return (
    <>
      <label className="input big-search">
        <Search className="i" aria-hidden="true" />
        <input
          ref={input}
          type="search"
          value={text}
          aria-label="Search TMDB by title, or paste an IMDb or TMDB link"
          aria-controls="add-results"
          aria-describedby="add-count"
          placeholder="Search The Movie Database…"
          onChange={(ev) => setText(ev.target.value)}
          onKeyDown={(ev) => {
            if (ev.key === 'ArrowDown' || ev.key === 'ArrowUp') {
              if (!results.length) return;
              ev.preventDefault();
              const d = ev.key === 'ArrowDown' ? 1 : -1;
              setActive((i) => (i + d + results.length) % results.length);
            } else if (ev.key === 'Enter') {
              ev.preventDefault();
              addSelected(selected);
            } else if (ev.key === 'Escape' && text) {
              ev.preventDefault();
              setText('');
            }
          }}
        />
        <span className="t-caption t-muted hide-tablet" aria-hidden="true">
          Title, imdb:tt…, or a TMDB link
        </span>
      </label>

      {needsKey ? (
        <NoTmdbKey onConnect={onNeedKey} />
      ) : !q ? (
        <div className="empty add-empty">
          <span className="glyph" aria-hidden="true">
            <Search className="i" />
          </span>
          <h2>Which film tonight?</h2>
          <p>Type a title, an IMDb id (tt0137523) or paste a TMDB link. Results arrive as you type.</p>
          <span className="hintline">
            Tip: press <span className="kbd">A</span> anywhere to come back here.
          </span>
        </div>
      ) : (
        <div className="add-split">
          <div>
            <p className="count-line" id="add-count" aria-live="polite">
              {lookup.isError
                ? ' '
                : lookup.data && !stale
                  ? results.length
                    ? `${formatNumber(results.length)} ${results.length === 1 ? 'film' : 'films'} on TMDB · ↑↓ to move, ↵ to add with these options`
                    : ' '
                  : 'Searching TMDB…'}
            </p>
            {lookup.isError ? (
              <Notice
                tone="failed"
                role="alert"
                title="TMDB did not answer."
                action={
                  <Button size="sm" onClick={() => void lookup.refetch()}>
                    Try again
                  </Button>
                }
              >
                {lookup.error.message} Your library is not affected.
              </Notice>
            ) : showSkeleton ? (
              <ul className="results" aria-hidden="true">
                {[0, 1, 2, 3].map((i) => (
                  <li className="res" key={i}>
                    <Skeleton className="res-sk-poster" delay={i * 60} />
                    <div>
                      <Skeleton className="sk-line" style={{ width: '40%' }} delay={i * 60} />
                      <Skeleton className="sk-line" style={{ width: '85%', marginTop: 10 }} delay={i * 60} />
                    </div>
                  </li>
                ))}
              </ul>
            ) : results.length === 0 && lookup.data && !stale ? (
              <div className="empty add-empty">
                <span className="glyph" aria-hidden="true">
                  <SearchX className="i" />
                </span>
                <h2>No film by that name on TMDB.</h2>
                <p>Check the spelling, try the original title, or paste the film’s IMDb id.</p>
              </div>
            ) : (
              <ul className={cx('results', stale && 'is-stale')} id="add-results" aria-label="Search results">
                {results.map((r, i) => {
                  const m = r.movieId ? libState.get(r.movieId) : undefined;
                  const wanted = m && m.state === 'missing' && m.monitored;
                  const isSel = i === active;
                  const facts = [
                    r.releaseDate
                      ? `Released ${formatDate(r.releaseDate)}`
                      : r.year
                        ? String(r.year)
                        : 'No release date',
                    r.originalTitle && r.originalTitle !== r.title ? r.originalTitle : '',
                    r.originalLanguage ? languageName(r.originalLanguage) : '',
                    r.rating?.value ? `TMDB ${r.rating.value.toFixed(1)}` : '',
                  ].filter(Boolean);
                  return (
                    // biome-ignore lint/a11y/useKeyWithClickEvents: the title button selects by keyboard; the row is a larger pointer target
                    // biome-ignore lint/a11y/noNoninteractiveElementInteractions: same (pointer convenience only)
                    <li
                      key={r.tmdbId}
                      className={cx('res', r.inLibrary && 'in-lib')}
                      aria-current={isSel || undefined}
                      onClick={() => setActive(i)}
                    >
                      <PosterArt src={imageUrl('poster', 'w342', r.posterPath)} title={r.title} year={r.year} />
                      <div className="res-body">
                        <div className="rt">
                          <button type="button" className="res-title" aria-pressed={isSel} onClick={() => setActive(i)}>
                            {r.title}
                          </button>
                          {r.year ? <span>{r.year}</span> : null}
                        </div>
                        {r.overview && <p>{r.overview}</p>}
                        <div className="meta">{facts.join(' · ')}</div>
                      </div>
                      {r.inLibrary && r.movieId ? (
                        <Link
                          to="/films/$movieId"
                          params={{ movieId: r.movieId }}
                          className="badge"
                          data-status={wanted ? 'missing' : 'available'}
                        >
                          {wanted ? (
                            <CircleDashed className="i" aria-hidden="true" />
                          ) : (
                            <Check className="i" aria-hidden="true" />
                          )}
                          {wanted ? 'In library · wanted' : 'In library'}
                          <span className="sr-only">: open {r.title}</span>
                        </Link>
                      ) : (
                        <IconButton
                          variant="outlined"
                          label={`Add ${r.title}${r.year ? ` (${r.year})` : ''} with these options`}
                          disabled={!options || busy === r.tmdbId}
                          aria-busy={busy === r.tmdbId || undefined}
                          onClick={(ev) => {
                            ev.stopPropagation();
                            addSelected(r);
                          }}
                        >
                          <Plus className="i" aria-hidden="true" />
                        </IconButton>
                      )}
                    </li>
                  );
                })}
              </ul>
            )}
          </div>
          {selected && options && (
            <aside className="panel add-opt" aria-labelledby="opt-h">
              <div className="hero">
                {selected.backdropPath ? (
                  <img src={imageUrl('backdrop', 'w1280', selected.backdropPath) ?? ''} alt="" decoding="async" />
                ) : (
                  <PosterArt src={null} title={selected.title} kind="backdrop" />
                )}
              </div>
              <div className="id">
                <PosterArt
                  src={imageUrl('poster', 'w342', selected.posterPath)}
                  title={selected.title}
                  year={selected.year}
                />
                <div>
                  <h2 id="opt-h">{selected.title}</h2>
                  <span className="t-caption">
                    {[selected.year, selected.originalLanguage ? languageName(selected.originalLanguage) : '']
                      .filter(Boolean)
                      .join(' · ')}
                  </span>
                </div>
              </div>
              {selected.inLibrary && selected.movieId ? (
                <div className="add-in-lib">
                  <p className="t-secondary">This film is already in your library.</p>
                  <Link className="btn btn-secondary" to="/films/$movieId" params={{ movieId: selected.movieId }}>
                    Open {selected.title}
                  </Link>
                </div>
              ) : (
                <AddOptionsForm
                  options={options}
                  onChange={setOptions}
                  busy={busy === selected.tmdbId}
                  folderName={`${selected.title}${selected.year ? ` (${selected.year})` : ''}`}
                  onSubmit={() => addSelected(selected)}
                />
              )}
            </aside>
          )}
        </div>
      )}
    </>
  );
}
