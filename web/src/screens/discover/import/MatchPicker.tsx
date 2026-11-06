import { useQuery } from '@tanstack/react-query';
import { Ban, Check, Search } from 'lucide-react';
import { type KeyboardEvent, useEffect, useId, useState } from 'react';
import { type LibraryCandidate, type LibraryFolder, searchCandidates, thumbUrl } from '@/api/v11';
import { Skeleton } from '@/components/ui/Feedback';
import { PosterArt } from '@/components/ui/PosterArt';
import { confidenceLevel } from './importModel';

/** Confidence as four bars and a percentage (never colour alone: the number is there, low is amber). */
export function Confidence({ value }: { value: number }) {
  const c = confidenceLevel(value);
  return (
    <span className="conf" data-l={c.level} role="img" aria-label={`Confidence ${c.pct}%, ${c.word}`}>
      <i />
      <i />
      <i />
      <i />
      <b aria-hidden="true">{c.pct}%</b>
    </span>
  );
}

function useDebounced<T>(v: T, ms: number): T {
  const [d, setD] = useState(v);
  useEffect(() => {
    const t = setTimeout(() => setD(v), ms);
    return () => clearTimeout(t);
  }, [v, ms]);
  return d;
}

/** "Change match" (library-import.html `&match=open`): a search field (combobox) over the candidates,
 *  TMDB searched as you type (a title, tmdb:<id> or an IMDb id), and "Not a film, skip this folder". */
export function MatchPicker({
  folder,
  rootFolderId,
  current,
  onPick,
  onSkip,
}: {
  folder: LibraryFolder;
  rootFolderId: number;
  current: number | null;
  onPick: (c: LibraryCandidate) => void;
  onSkip: () => void;
}) {
  const id = useId();
  const initial = folder.title || folder.name;
  const [query, setQuery] = useState(initial);
  const q = useDebounced(query.trim(), 350);
  const searching = q !== initial.trim() && q.length > 0;
  const found = useQuery({
    queryKey: ['library-import', 'candidates', rootFolderId, folder.folder, q],
    queryFn: ({ signal }) => searchCandidates({ rootFolderId, folder: folder.folder, query: q.slice(0, 200) }, signal),
    enabled: searching,
    staleTime: 5 * 60_000,
  });
  const list: LibraryCandidate[] = searching ? (found.data ?? []) : (folder.candidates ?? []);
  const [active, setActive] = useState(0);
  // biome-ignore lint/correctness/useExhaustiveDependencies: back to the first option when the list changes
  useEffect(() => setActive(0), [list.length, q]);
  const optId = (i: number) => `${id}-o${i}`;
  const onKey = (ev: KeyboardEvent) => {
    if (ev.key === 'ArrowDown' || ev.key === 'ArrowUp') {
      ev.preventDefault();
      if (!list.length) return;
      setActive((a) => (a + (ev.key === 'ArrowDown' ? 1 : -1) + list.length) % list.length);
    } else if (ev.key === 'Enter') {
      ev.preventDefault();
      const c = list[active];
      if (c) onPick(c);
    }
  };
  return (
    <div className="matchbox">
      <label className="input">
        <Search className="i" aria-hidden="true" />
        <input
          type="search"
          // biome-ignore lint/a11y/noAutofocus: the picker opens to search
          autoFocus
          value={query}
          maxLength={200}
          aria-label="Search TMDB for this folder"
          role="combobox"
          aria-expanded="true"
          aria-controls={`${id}-list`}
          aria-autocomplete="list"
          aria-activedescendant={list[active] ? optId(active) : undefined}
          onChange={(ev) => setQuery(ev.target.value)}
          onKeyDown={onKey}
        />
      </label>
      <div className="menu-heading" aria-hidden="true">
        <span>{searching ? 'Results' : 'Candidates'}</span>
        <span>Confidence</span>
      </div>
      <div
        role="listbox"
        id={`${id}-list`}
        aria-label={`Candidates for ${folder.name}`}
        aria-busy={found.isFetching || undefined}
      >
        {searching && found.isPending ? (
          <Skeleton className="sk-line" />
        ) : found.isError && searching ? (
          <p className="mb-note">{(found.error as Error).message}</p>
        ) : !list.length ? (
          <p className="mb-note">No film found. Try the title as on TMDB, a year, or tmdb:12345.</p>
        ) : (
          list.map((c, i) => (
            // biome-ignore lint/a11y/useKeyWithClickEvents: the combobox field handles the keys (aria-activedescendant)
            // biome-ignore lint/a11y/useFocusableInteractive: options of a combobox stay unfocusable (aria-activedescendant)
            <div
              key={c.tmdbId}
              id={optId(i)}
              role="option"
              aria-selected={c.tmdbId === current}
              className={i === active ? 'is-active' : undefined}
              onMouseEnter={() => setActive(i)}
              onClick={() => onPick(c)}
            >
              <span className="th">
                <PosterArt src={thumbUrl(c.posterUrl)} title={c.title} />
              </span>
              <span>
                <b>
                  {c.title} {c.year ? <em>{c.year}</em> : null}
                  {c.movieId ? <span className="tag plain">In library</span> : null}
                </b>
                <small>{c.reason}</small>
              </span>
              <Confidence value={c.confidence} />
              <span className="check-mark">
                {c.tmdbId === current && <Check className="i i-sm" aria-hidden="true" />}
              </span>
            </div>
          ))
        )}
      </div>
      <div className="mb-sep" />
      <button type="button" className="mb-skip" onClick={onSkip}>
        <Ban className="i" aria-hidden="true" />
        Not a film, skip this folder
      </button>
    </div>
  );
}
