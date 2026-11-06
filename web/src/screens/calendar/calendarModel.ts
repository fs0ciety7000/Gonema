import type { CalendarEvent, CalendarMovie } from '@/api/schema';

export type ReleaseType = 'cinema' | 'digital' | 'physical';
export type CalStatus = 'available' | 'missing' | 'downloading' | 'upgraded' | 'unmonitored';

export type CalEvent = {
  key: string;
  movie: CalendarMovie;
  type: ReleaseType;
  /** Local midnight of the release day. */
  date: Date;
  /** "YYYY-MM-DD" (local). */
  day: string;
  status: CalStatus;
  note: string;
};

const pad = (n: number) => String(n).padStart(2, '0');
export const dayKey = (d: Date) => `${d.getFullYear()}-${pad(d.getMonth() + 1)}-${pad(d.getDate())}`;
export const monthKey = (d: Date) => `${d.getFullYear()}-${pad(d.getMonth() + 1)}`;

export function parseMonth(m: string | undefined, now: Date): Date {
  if (m && /^\d{4}-\d{2}$/.test(m)) {
    const [y, mo] = m.split('-').map(Number) as [number, number];
    return new Date(y, mo - 1, 1);
  }
  return new Date(now.getFullYear(), now.getMonth(), 1);
}

export const addMonths = (d: Date, n: number) => new Date(d.getFullYear(), d.getMonth() + n, 1);

/** Monday-first grid covering the month: whole weeks, 5 or 6 rows. */
export function monthGrid(month: Date): Date[] {
  const first = new Date(month.getFullYear(), month.getMonth(), 1);
  const offset = (first.getDay() + 6) % 7;
  const start = new Date(first.getFullYear(), first.getMonth(), 1 - offset);
  const last = new Date(month.getFullYear(), month.getMonth() + 1, 0);
  const cells = Math.ceil((offset + last.getDate()) / 7) * 7;
  return Array.from({ length: cells }, (_, i) => new Date(start.getFullYear(), start.getMonth(), start.getDate() + i));
}

type ServerMovie = Pick<CalendarMovie, 'state' | 'monitored'>;

/** Status shown on a release: on disk, wanted, downloading, upgrade due (a disc for a file below
 *  its cutoff) or not monitored. */
export function statusOf(m: ServerMovie, type: ReleaseType): CalStatus {
  if (!m.monitored) return 'unmonitored';
  if (m.state === 'downloading') return 'downloading';
  if (m.state === 'upgradable' && type === 'physical') return 'upgraded';
  if (m.state === 'available' || m.state === 'upgradable') return 'available';
  return 'missing';
}

const TYPE_LABEL: Record<ReleaseType, string> = { cinema: 'In cinemas', digital: 'Digital', physical: 'Physical' };
export const typeLabel = (t: ReleaseType) => TYPE_LABEL[t];

/** GET /calendar events → events of the grid (filtered by release type, soonest first). */
export function toEvents(list: readonly CalendarEvent[], types: ReadonlySet<ReleaseType>): CalEvent[] {
  const out: CalEvent[] = [];
  for (const e of list) {
    if (!types.has(e.type)) continue;
    const date = new Date(`${e.date.slice(0, 10)}T00:00:00`);
    if (Number.isNaN(date.getTime())) continue;
    out.push({
      key: `${e.movie.id}-${e.type}`,
      movie: e.movie,
      type: e.type,
      date,
      day: dayKey(date),
      status: statusOf(e.movie, e.type),
      note: e.note,
    });
  }
  return out.sort((a, b) => a.date.getTime() - b.date.getTime() || a.movie.title.localeCompare(b.movie.title));
}

export function groupByDay(events: readonly CalEvent[]): Map<string, CalEvent[]> {
  const map = new Map<string, CalEvent[]>();
  for (const e of events) {
    const list = map.get(e.day);
    if (list) list.push(e);
    else map.set(e.day, [e]);
  }
  return map;
}

/** Monday of the week containing d. */
export function weekStart(d: Date): Date {
  return new Date(d.getFullYear(), d.getMonth(), d.getDate() - ((d.getDay() + 6) % 7));
}
