import type { LibraryItem } from '@/api/model';
import type { CollectionFilm, CollectionListItem } from '@/api/v11';

type Availability = 'announced' | 'in_cinemas' | 'released';

/** Pip of a film in the card's progress line (collections.html): on disk, downloading, wanted,
 *  upcoming in the library; empty for a film not in the library. */
export function pipOf(f: Pick<CollectionFilm, 'state'>): 'd' | 'l' | 'w' | 'u' | '' {
  switch (f.state) {
    case 'on_disk':
      return 'd';
    case 'downloading':
      return 'l';
    case 'wanted':
      return 'w';
    case 'upcoming':
      return 'u';
    default:
      return '';
  }
}

export const notInLibrary = (f: Pick<CollectionFilm, 'state'>) => f.state === 'missing' || f.state === 'not_released';

/** Films "missing" as the mockup counts them: released and not in the library, plus the announced ones
 *  when the collection is not monitored (a monitored one adds them on their release day). */
export function missingOf(c: Pick<CollectionListItem, 'monitored' | 'counts' | 'films'>): number {
  const notOut = (c.films ?? []).filter((f) => f.state === 'not_released' && !f.excluded).length;
  return c.counts.missing + (c.monitored ? 0 : notOut);
}

/** Films that "Add the N missing" adds: every film not in the library that is not excluded. */
export function addableOf(c: Pick<CollectionListItem, 'monitored' | 'films'>): CollectionFilm[] {
  return (c.films ?? []).filter((f) => notInLibrary(f) && !f.excluded && (f.state === 'missing' || !c.monitored));
}

export function yearsOf(c: Pick<CollectionListItem, 'counts'>): string {
  const { yearFrom: a, yearTo: b } = c.counts;
  if (!a && !b) return '';
  if (!a || !b || a === b) return String(a || b);
  return `${a}–${b}`;
}

const dayMonth = new Intl.DateTimeFormat('en-GB', { day: 'numeric', month: 'short' });
const dayMonthYear = new Intl.DateTimeFormat('en-GB', { day: 'numeric', month: 'short', year: 'numeric' });

function shortDate(iso: string, now: number): string {
  const d = new Date(`${iso}T00:00:00`);
  if (Number.isNaN(d.getTime())) return iso;
  return d.getFullYear() === new Date(now).getFullYear() ? dayMonth.format(d) : dayMonthYear.format(d);
}

/** "In cinemas 27 Nov", "Out 14 Nov · physical", "Announced for 2027". */
export function releaseLabel(f: Pick<CollectionFilm, 'nextRelease' | 'year'>, now = Date.now()): string {
  const n = f.nextRelease;
  if (n?.date) {
    const d = shortDate(n.date, now);
    if (n.type === 'cinema') return `In cinemas ${d}`;
    return `Out ${d} · ${n.type}`;
  }
  return f.year ? `Announced for ${f.year}` : 'Date not announced';
}

/** Settings used for the films a collection adds: its own, else those of its first library film,
 *  else the first profile and root folder. Null when nothing can be chosen (no profile or folder). */
export function addSettingsOf(
  c: Pick<CollectionListItem, 'qualityProfileId' | 'rootFolderId' | 'searchOnAdd' | 'films'> & {
    minimumAvailability: string;
  },
  library: ReadonlyMap<number, Pick<LibraryItem, 'qualityProfileId' | 'rootFolderId'>>,
  firstProfile: number | undefined,
  firstRoot: number | undefined,
): { qualityProfileId: number; rootFolderId: number; minimumAvailability: Availability; searchOnAdd: boolean } | null {
  const lead = (c.films ?? []).map((f) => (f.movieId ? library.get(f.movieId) : undefined)).find(Boolean);
  const qualityProfileId = c.qualityProfileId ?? lead?.qualityProfileId ?? firstProfile;
  const rootFolderId = c.rootFolderId ?? lead?.rootFolderId ?? firstRoot;
  if (!qualityProfileId || !rootFolderId) return null;
  const ma = c.minimumAvailability as string;
  return {
    qualityProfileId,
    rootFolderId,
    minimumAvailability: ma === 'announced' || ma === 'in_cinemas' ? ma : 'released',
    searchOnAdd: c.searchOnAdd,
  };
}

const rel = new Intl.RelativeTimeFormat('en', { numeric: 'auto' });

/** "3 h ago", "yesterday". */
export function ago(iso: string | null | undefined, now = Date.now()): string {
  if (!iso) return '';
  const t = Date.parse(iso);
  if (Number.isNaN(t)) return '';
  const min = Math.round((now - t) / 60_000);
  if (min < 1) return 'just now';
  if (min < 60) return `${min} min ago`;
  const h = Math.round(min / 60);
  if (h < 24) return `${h} h ago`;
  return rel.format(-Math.round(h / 24), 'day');
}
