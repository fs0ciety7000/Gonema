import '@/styles/lot3.css';
import { useQuery } from '@tanstack/react-query';
import { getRouteApi, Link, useNavigate } from '@tanstack/react-router';
import {
  ArrowDownToLine,
  Calendar,
  ChevronLeft,
  ChevronRight,
  CircleArrowUp,
  CircleCheck,
  CircleDashed,
  Disc3,
  EyeOff,
  type LucideIcon,
  MonitorPlay,
  Ticket,
} from 'lucide-react';
import { lazy, Suspense, useEffect, useMemo, useState } from 'react';
import { calendarQuery } from '@/api/lot3';
import { SearchField, ThemeToggle, Topbar } from '@/components/shell/Topbar';
import { Button, IconButton } from '@/components/ui/Button';
import { Chip } from '@/components/ui/Chip';
import { cx } from '@/components/ui/cx';
import { EmptyState, Notice, Skeleton } from '@/components/ui/Feedback';
import { PosterArt } from '@/components/ui/PosterArt';
import { Segmented } from '@/components/ui/Segmented';
import { formatNumber } from '@/lib/format';
import {
  addMonths,
  type CalEvent,
  type CalStatus,
  dayKey,
  groupByDay,
  monthGrid,
  monthKey,
  parseMonth,
  type ReleaseType,
  toEvents,
  typeLabel,
  weekStart,
} from './calendarModel';

const route = getRouteApi('/calendar');

const TYPE_ICON: Record<ReleaseType, LucideIcon> = { cinema: Ticket, digital: MonitorPlay, physical: Disc3 };
const STATUS: Record<CalStatus, { icon: LucideIcon; label: string }> = {
  available: { icon: CircleCheck, label: 'on disk' },
  missing: { icon: CircleDashed, label: 'wanted' },
  downloading: { icon: ArrowDownToLine, label: 'downloading' },
  upgraded: { icon: CircleArrowUp, label: 'upgrade due' },
  unmonitored: { icon: EyeOff, label: 'announced, not monitored' },
};
const ALL_TYPES: readonly ReleaseType[] = ['cinema', 'digital', 'physical'];

const monthTitle = new Intl.DateTimeFormat('en-GB', { month: 'long', year: 'numeric' });
const dayMonth = new Intl.DateTimeFormat('en-GB', { day: 'numeric', month: 'short' });
const dayName = new Intl.DateTimeFormat('en-GB', { weekday: 'short' });
const dayLong = new Intl.DateTimeFormat('en-GB', { day: 'numeric', month: 'long' });

function Ev({ e, rich }: { e: CalEvent; rich: boolean }) {
  const S = STATUS[e.status];
  const T = TYPE_ICON[e.type];
  const SIcon = S.icon;
  return (
    <Link
      className={cx('ev', rich && 'with-poster')}
      data-status={e.status}
      to="/films/$movieId"
      params={{ movieId: e.movie.id }}
    >
      {rich ? (
        <PosterArt className="pp" src={e.movie.posterUrl} title={e.movie.title} color={e.movie.dominantColor} />
      ) : (
        <T className="i" aria-hidden="true" />
      )}
      <span className="nm">
        {e.movie.title}
        {rich ? (
          <span className="sub">{typeLabel(e.type)}</span>
        ) : (
          <span className="sr-only">, {typeLabel(e.type)}</span>
        )}
        <span className="sr-only">, {S.label}</span>
      </span>
      <span className="s" aria-hidden="true">
        <SIcon className="i" />
      </span>
    </Link>
  );
}

function Month({ month, byDay, today }: { month: Date; byDay: Map<string, CalEvent[]>; today: Date }) {
  const cells = monthGrid(month);
  const todayKey = dayKey(today);
  return (
    // biome-ignore lint/a11y/useSemanticElements: the month grid is a named landmark around day groups
    <div className="month" role="region" aria-label={monthTitle.format(month)}>
      {['Mon', 'Tue', 'Wed', 'Thu', 'Fri', 'Sat', 'Sun'].map((d) => (
        <div key={d} className="dow" aria-hidden="true">
          {d}
        </div>
      ))}
      {cells.map((d) => {
        const k = dayKey(d);
        const out = d.getMonth() !== month.getMonth();
        const evs = byDay.get(k) ?? [];
        const isToday = k === todayKey;
        const shown = evs.slice(0, 4);
        return (
          // biome-ignore lint/a11y/useSemanticElements: a calendar day is a labelled group of release links
          <div
            key={k}
            className={cx('day', out && 'out', isToday && 'today')}
            role="group"
            aria-label={`${dayLong.format(d)}${evs.length ? `, ${evs.length} ${evs.length === 1 ? 'release' : 'releases'}` : ''}`}
            aria-current={isToday ? 'date' : undefined}
          >
            <span className="n" aria-hidden="true">
              {d.getDate()}
            </span>
            {shown.map((e) => (
              <Ev key={e.key} e={e} rich={evs.length === 1 && !out} />
            ))}
            {evs.length > shown.length && (
              <span className="more t-caption t-muted">+{evs.length - shown.length} more</span>
            )}
          </div>
        );
      })}
    </div>
  );
}

function Agenda({ events, today, from }: { events: CalEvent[]; today: Date; from: Date }) {
  const todayKey = dayKey(today);
  const thisWeek = weekStart(today).getTime();
  const weeks = new Map<number, CalEvent[]>();
  for (const e of events) {
    const w = weekStart(e.date).getTime();
    const l = weeks.get(w);
    if (l) l.push(e);
    else weeks.set(w, [e]);
  }
  if (!weeks.size)
    return (
      <EmptyState title="A quiet month." headingLevel={2}>
        No release dates for the films shown from {dayLong.format(from)}. Try the next month, or show announced films
        too.
      </EmptyState>
    );
  return (
    <div className="agenda">
      {[...weeks.entries()].map(([w, list]) => {
        const start = new Date(w);
        const end = new Date(start.getFullYear(), start.getMonth(), start.getDate() + 6);
        const range = `${start.getDate()}–${dayMonth.format(end)}`;
        const label =
          w === thisWeek ? `This week · ${range}` : w === thisWeek + 7 * 86_400_000 ? `Next week · ${range}` : range;
        return (
          <section className="wk" key={w} aria-label={label}>
            <h2>{label}</h2>
            <ul>
              {list.map((e) => {
                const S = STATUS[e.status];
                const SIcon = S.icon;
                const T = TYPE_ICON[e.type];
                const isToday = e.day === todayKey;
                return (
                  <li key={e.key} className={isToday ? 'today' : undefined}>
                    <span className="date">
                      {dayMonth.format(e.date)}
                      <small>{isToday ? 'Today' : dayName.format(e.date)}</small>
                    </span>
                    <PosterArt
                      className="pp"
                      src={e.movie.posterUrl}
                      title={e.movie.title}
                      color={e.movie.dominantColor}
                    />
                    <span className="min-w-0">
                      <Link className="b" to="/films/$movieId" params={{ movieId: e.movie.id }}>
                        {e.movie.title}
                      </Link>
                      <span className="note">{e.note}</span>
                    </span>
                    <span className="type">
                      <T className="i" aria-hidden="true" />
                      {typeLabel(e.type)}
                    </span>
                    <span className="status" data-status={e.status === 'upgraded' ? 'upgraded' : e.status}>
                      <SIcon className="i" aria-hidden="true" />
                      {e.status === 'unmonitored' ? 'Announced' : S.label.charAt(0).toUpperCase() + S.label.slice(1)}
                    </span>
                  </li>
                );
              })}
            </ul>
          </section>
        );
      })}
    </div>
  );
}

const CalendarFeedDialog = lazy(() => import('./CalendarFeedDialog'));

export function CalendarScreen() {
  const search = route.useSearch();
  const navigate = useNavigate();
  const now = useMemo(() => new Date(), []);
  const today = new Date(now.getFullYear(), now.getMonth(), now.getDate());
  const month = parseMonth(search.month, now);
  const view = search.view === 'agenda' ? 'agenda' : 'month';
  const [types, setTypes] = useState<ReadonlySet<ReleaseType>>(new Set(ALL_TYPES));
  const [feedOpen, setFeedOpen] = useState(false);
  const set = (p: Record<string, unknown>) =>
    void navigate({
      to: '/calendar',
      search: (s: Record<string, unknown>) => ({ ...s, ...p }) as never,
      replace: true,
    });
  const title = monthTitle.format(month);
  useEffect(() => {
    document.title = `Calendar · ${title} · Gonema`;
  }, [title]);
  const isCurrent = monthKey(month) === monthKey(now);
  const grid = monthGrid(month);
  const range =
    view === 'month'
      ? {
          from: grid[0]!,
          to: new Date(grid.at(-1)!.getFullYear(), grid.at(-1)!.getMonth(), grid.at(-1)!.getDate() + 1),
        }
      : { from: isCurrent ? weekStart(today) : month, to: addMonths(month, 1) };
  const last = new Date(range.to.getFullYear(), range.to.getMonth(), range.to.getDate() - 1);
  const cal = useQuery({
    ...calendarQuery(dayKey(range.from), dayKey(last), !!search.all),
    placeholderData: (prev) => prev,
  });
  const events = useMemo(() => (cal.data ? toEvents(cal.data.events ?? [], types) : null), [cal.data, types]);
  const byDay = useMemo(() => groupByDay(events ?? []), [events]);
  const inMonth = (events ?? []).filter((e) => e.date.getMonth() === month.getMonth()).length;
  const toggleType = (t: ReleaseType) => {
    const n = new Set(types);
    if (n.has(t)) n.delete(t);
    else n.add(t);
    setTypes(n);
  };
  return (
    <>
      <Topbar>
        <SearchField />
        <div className="spacer" />
        <ThemeToggle />
      </Topbar>
      <main id="main" className="page calendar-page" tabIndex={-1}>
        <div className="head">
          <h1 aria-live="polite">{monthTitle.format(month)}</h1>
          <div className="row">
            <IconButton
              label="Previous month"
              variant="outlined"
              onClick={() => set({ month: monthKey(addMonths(month, -1)) })}
            >
              <ChevronLeft className="i" aria-hidden="true" />
            </IconButton>
            <Button size="sm" disabled={isCurrent} onClick={() => set({ month: undefined })}>
              Today
            </Button>
            <IconButton
              label="Next month"
              variant="outlined"
              onClick={() => set({ month: monthKey(addMonths(month, 1)) })}
            >
              <ChevronRight className="i" aria-hidden="true" />
            </IconButton>
          </div>
          <div className="spacer" />
          <Segmented<'month' | 'agenda'>
            label="View"
            value={view}
            onChange={(v) => set({ view: v === 'agenda' ? 'agenda' : undefined })}
            options={[
              { value: 'month', label: 'Month' },
              { value: 'agenda', label: 'Agenda' },
            ]}
          />
          <Button
            variant="ghost"
            size="sm"
            icon={<Calendar className="i" aria-hidden="true" />}
            aria-haspopup="dialog"
            onClick={() => setFeedOpen(true)}
          >
            iCal feed
          </Button>
        </div>
        {/* biome-ignore lint/a11y/useSemanticElements: a labelled group of toggle chips, not a form */}
        <div className="legend" role="group" aria-label="Release types">
          {ALL_TYPES.map((t) => {
            const Icon = TYPE_ICON[t];
            return (
              <Chip key={t} pressed={types.has(t)} onClick={() => toggleType(t)}>
                <Icon className="i" aria-hidden="true" />
                {typeLabel(t)}
              </Chip>
            );
          })}
          <Chip pressed={!!search.all} onClick={() => set({ all: search.all ? undefined : '1' })}>
            <EyeOff className="i" aria-hidden="true" />
            Announced &amp; unmonitored
          </Chip>
          <span className="t-caption legend-key">
            {events ? `${formatNumber(inMonth)} ${inMonth === 1 ? 'release' : 'releases'} · ` : ''}
            Status:{' '}
            {(['available', 'missing', 'downloading', 'upgraded'] as const).map((s, i) => {
              const Icon = STATUS[s].icon;
              return (
                <span key={s}>
                  {i > 0 && ' · '}
                  <span className="status" data-status={s}>
                    <Icon className="i" aria-hidden="true" />
                    {STATUS[s].label}
                  </span>
                </span>
              );
            })}
          </span>
        </div>
        {cal.isError ? (
          <Notice
            tone="failed"
            role="alert"
            title="The calendar could not be loaded."
            action={
              <Button size="sm" onClick={() => void cal.refetch()}>
                Try again
              </Button>
            }
          >
            {(cal.error as Error).message}
          </Notice>
        ) : !events ? (
          <Skeleton className="sk-month" />
        ) : view === 'month' ? (
          <Month month={month} byDay={byDay} today={today} />
        ) : (
          <Agenda events={events} today={today} from={range.from} />
        )}
      </main>
      {feedOpen && (
        <Suspense fallback={null}>
          <CalendarFeedDialog open={feedOpen} onOpenChange={setFeedOpen} />
        </Suspense>
      )}
    </>
  );
}
