import '@/styles/v11.css';
import { useMutation, useQuery, useQueryClient } from '@tanstack/react-query';
import { Link } from '@tanstack/react-router';
import { Calendar, CalendarSync, Disc3, EyeOff, MonitorPlay, Ticket, TriangleAlert } from 'lucide-react';
import { useEffect, useState } from 'react';
import { ApiError } from '@/api/client';
import {
  type CalendarFeedView,
  calendarFeedQuery,
  disableCalendarFeed,
  putCalendarFeed,
  regenerateCalendarFeed,
  v11Keys,
} from '@/api/v11';
import { ApiKeyReveal, problemText } from '@/components/auth/AuthParts';
import { Button, buttonClass } from '@/components/ui/Button';
import { Checkbox } from '@/components/ui/Checkbox';
import { Chip } from '@/components/ui/Chip';
import { Dialog } from '@/components/ui/Dialog';
import { Notice, Skeleton } from '@/components/ui/Feedback';
import { Field, Input } from '@/components/ui/Field';
import { toast } from '@/components/ui/Toast';
import { ago } from '@/screens/collections/collectionModel';

/* iCal feed (no mockup: DESIGN.md patterns, see e2e/fidelity.md). A dialog from the calendar's
   "iCal feed" button: turn the feed on (a read-only key, URL shown once), copy it, set the options
   (saved at once), make a new URL or turn it off, each after an explicit confirmation in place. */

const TYPES = [
  { id: 'cinema', label: 'In cinemas', icon: Ticket },
  { id: 'digital', label: 'Digital', icon: MonitorPlay },
  { id: 'physical', label: 'Physical', icon: Disc3 },
] as const;

type Options = Pick<CalendarFeedView, 'pastDays' | 'futureDays' | 'unmonitored' | 'releaseTypes'>;
const MAX_DAYS = 3650;

/** Days back / ahead: whole numbers 0…3650; null when the text is not one. */
export function parseDays(v: string): number | null {
  if (!/^\d{1,4}$/.test(v.trim())) return null;
  const n = Number(v);
  return n <= MAX_DAYS ? n : null;
}

/** The URL to subscribe to: the server's, else this origin + the path it gave. */
export function feedUrl(v: Pick<CalendarFeedView, 'url' | 'path'>, base = document.baseURI): string | null {
  if (v.url) return v.url;
  if (v.path) return new URL(v.path, new URL(base).origin).href;
  return null;
}

export default function CalendarFeedDialog({
  open,
  onOpenChange,
}: {
  open: boolean;
  onOpenChange: (o: boolean) => void;
}) {
  const qc = useQueryClient();
  const q = useQuery({ ...calendarFeedQuery, enabled: open });
  const feed = q.data;
  // the URL with its key: kept in this component only, gone when the dialog closes
  const [revealed, setRevealed] = useState<string | null>(null);
  const [confirm, setConfirm] = useState<'regenerate' | 'disable' | null>(null);
  useEffect(() => {
    if (!open) {
      setRevealed(null);
      setConfirm(null);
    }
  }, [open]);

  const set = (v: CalendarFeedView) =>
    qc.setQueryData(v11Keys.calendarFeed, { ...v, key: undefined, url: undefined, path: undefined });
  const regen = useMutation({
    mutationFn: regenerateCalendarFeed,
    onSuccess: (v) => {
      setRevealed(feedUrl(v));
      set(v);
      setConfirm(null);
    },
    onError: (e) => toast.error('No feed URL was made', problemText(e)),
  });
  const disable = useMutation({
    mutationFn: disableCalendarFeed,
    onSuccess: () => {
      setRevealed(null);
      setConfirm(null);
      void qc.invalidateQueries({ queryKey: v11Keys.calendarFeed });
      toast.show({
        title: 'iCal feed turned off',
        body: 'Subscribed calendars stop updating. Your options are kept for next time.',
        icon: <Calendar className="i" />,
      });
    },
    onError: (e) => toast.error('The feed is still on', problemText(e)),
  });

  return (
    <Dialog
      open={open}
      onOpenChange={onOpenChange}
      wide
      className="feed-dialog"
      icon={<CalendarSync className="i" aria-hidden="true" />}
      title="iCal feed"
      description="Release dates of your films in any calendar app: Apple Calendar, Google Calendar, Outlook, Thunderbird. Read only; nothing in Gonema can be changed through it."
      actions={
        <Button variant="ghost" onClick={() => onOpenChange(false)}>
          Done
        </Button>
      }
    >
      {q.isPending ? (
        <Skeleton className="sk-line" />
      ) : q.isError && q.error instanceof ApiError && q.error.status === 409 ? (
        <Notice
          title="The feed belongs to an account."
          action={
            <Link className={buttonClass('secondary', 'sm')} to="/setup" search={{ step: 'account' }}>
              Create the account
            </Link>
          }
        >
          {problemText(q.error)} The local access has none: its key would have no owner. Create the account, then come
          back here.
        </Notice>
      ) : q.isError ? (
        <Notice tone="failed" role="alert" title="The feed settings could not be loaded.">
          {problemText(q.error)}
        </Notice>
      ) : feed ? (
        <div className="feed-body">
          {revealed ? (
            <ApiKeyReveal
              apiKey={revealed}
              title="Your feed URL"
              label="iCal feed URL"
              icon={<CalendarSync className="i" />}
              intro="Subscribe to it from your calendar app (“Add calendar from URL”)."
              once={
                <>
                  <b>You won’t see it again.</b> It holds a read-only key. Copy it now; you can make a new one here
                  later. Behind a reverse proxy, check that the host name is the public one.
                </>
              }
            />
          ) : feed.enabled ? (
            <div className="feed-state">
              <span className="status" data-status="available">
                <Calendar className="i" aria-hidden="true" />
                On
              </span>
              <span>
                Key ending in <code className="t-mono-sm">{feed.last4 ?? '····'}</code>
                {' · '}
                {feed.lastUsedAt ? `last read by a calendar ${ago(feed.lastUsedAt)}` : 'not read by a calendar yet'}
              </span>
            </div>
          ) : (
            <div className="feed-state">
              <span className="status" data-status="unmonitored">
                <EyeOff className="i" aria-hidden="true" />
                Off
              </span>
              <span>Turn it on to get a private URL. The URL is shown once.</span>
            </div>
          )}

          <FeedOptions feed={feed} />

          {confirm === 'regenerate' ? (
            <Notice
              tone="missing"
              role="alert"
              icon={<TriangleAlert className="i" aria-hidden="true" />}
              title="Make a new feed URL?"
              action={
                <div className="row">
                  <Button size="sm" variant="ghost" onClick={() => setConfirm(null)}>
                    Cancel
                  </Button>
                  <Button size="sm" variant="danger" loading={regen.isPending} onClick={() => regen.mutate()}>
                    Make a new URL
                  </Button>
                </div>
              }
            >
              The current URL stops working at once: calendars subscribed with it stop updating until you give them the
              new one.
            </Notice>
          ) : confirm === 'disable' ? (
            <Notice
              tone="missing"
              role="alert"
              icon={<TriangleAlert className="i" aria-hidden="true" />}
              title="Turn the feed off?"
              action={
                <div className="row">
                  <Button size="sm" variant="ghost" onClick={() => setConfirm(null)}>
                    Cancel
                  </Button>
                  <Button size="sm" variant="danger" loading={disable.isPending} onClick={() => disable.mutate()}>
                    Turn off
                  </Button>
                </div>
              }
            >
              The key is forgotten and subscribed calendars stop updating. Turning it on again makes a new URL.
            </Notice>
          ) : (
            <div className="feed-actions">
              {feed.enabled ? (
                <>
                  <Button onClick={() => setConfirm('regenerate')}>New URL…</Button>
                  <Button variant="ghost" className="feed-off" onClick={() => setConfirm('disable')}>
                    Turn off…
                  </Button>
                </>
              ) : (
                <Button variant="primary" loading={regen.isPending} onClick={() => regen.mutate()}>
                  Turn on the feed
                </Button>
              )}
            </div>
          )}
        </div>
      ) : null}
    </Dialog>
  );
}

/** Options: saved at once (PUT keeps the key and the on/off state). */
function FeedOptions({ feed }: { feed: CalendarFeedView }) {
  const qc = useQueryClient();
  const [past, setPast] = useState(String(feed.pastDays));
  const [future, setFuture] = useState(String(feed.futureDays));
  useEffect(() => setPast(String(feed.pastDays)), [feed.pastDays]);
  useEffect(() => setFuture(String(feed.futureDays)), [feed.futureDays]);
  const types = new Set(feed.releaseTypes ?? []);
  const save = useMutation({
    mutationFn: (o: Options) => putCalendarFeed({ ...o, releaseTypes: o.releaseTypes ?? [] }),
    onSuccess: (v) => qc.setQueryData(v11Keys.calendarFeed, v),
    onError: (e) => {
      toast.error('The feed options were not saved', problemText(e));
      void qc.invalidateQueries({ queryKey: v11Keys.calendarFeed });
    },
  });
  const base: Options = {
    pastDays: feed.pastDays,
    futureDays: feed.futureDays,
    unmonitored: feed.unmonitored,
    releaseTypes: feed.releaseTypes ?? [],
  };
  const pastN = parseDays(past);
  const futureN = parseDays(future);
  const toggle = (t: string) => {
    const n = new Set(types);
    if (n.has(t)) n.delete(t);
    else n.add(t);
    if (!n.size) return; // at least one type
    save.mutate({ ...base, releaseTypes: TYPES.map((x) => x.id).filter((x) => n.has(x)) });
  };
  return (
    <fieldset className="feed-opts">
      <legend className="field-label">What the feed holds</legend>
      <div className="feed-days">
        <Field label="Days back" error={pastN === null ? `A whole number of days, 0 to ${MAX_DAYS}.` : undefined}>
          {({ id, describedBy, invalid }) => (
            <Input
              id={id}
              inputMode="numeric"
              mono
              value={past}
              invalid={invalid}
              aria-describedby={describedBy}
              onChange={(ev) => setPast(ev.target.value)}
              onBlur={() => pastN !== null && pastN !== feed.pastDays && save.mutate({ ...base, pastDays: pastN })}
            />
          )}
        </Field>
        <Field label="Days ahead" error={futureN === null ? `A whole number of days, 0 to ${MAX_DAYS}.` : undefined}>
          {({ id, describedBy, invalid }) => (
            <Input
              id={id}
              inputMode="numeric"
              mono
              value={future}
              invalid={invalid}
              aria-describedby={describedBy}
              onChange={(ev) => setFuture(ev.target.value)}
              onBlur={() =>
                futureN !== null && futureN !== feed.futureDays && save.mutate({ ...base, futureDays: futureN })
              }
            />
          )}
        </Field>
      </div>
      {/* biome-ignore lint/a11y/useSemanticElements: a labelled group of toggle chips, not a form */}
      <div className="legend" role="group" aria-label="Release types in the feed">
        {TYPES.map((t) => (
          <Chip key={t.id} pressed={types.has(t.id)} onClick={() => toggle(t.id)}>
            <t.icon className="i" aria-hidden="true" />
            {t.label}
          </Chip>
        ))}
      </div>
      <Checkbox
        checked={feed.unmonitored}
        onCheckedChange={(v) => save.mutate({ ...base, unmonitored: v })}
        label="Also announced films you don’t monitor (as the calendar’s filter)"
        labelClassName="t-body-sm"
      />
      <span className="t-caption t-muted" aria-live="polite">
        {save.isPending ? 'Saving…' : save.isSuccess ? 'Saved' : 'Saved as you change them.'}
      </span>
    </fieldset>
  );
}
