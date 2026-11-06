import { useQuery, useQueryClient } from '@tanstack/react-query';
import { Link, useNavigate } from '@tanstack/react-router';
import {
  ArrowDownToLine,
  Ban,
  CircleCheck,
  Clapperboard,
  Ellipsis,
  Film,
  FolderOpen,
  Hand,
  Play,
  RefreshCw,
  ScanSearch,
  TriangleAlert,
} from 'lucide-react';
import { memo, useMemo, useState } from 'react';
import { ApiError, api, unwrap } from '@/api/client';
import { type Progress, setProgress, useProgress } from '@/api/progress';
import { keys, type QueueItem, queueQuery, useLibrary } from '@/api/queries';
import { pauseDownload, type RemoveOptions, removeDownload, resumeDownload, retryImport } from '@/api/screens';
import { QualityBadge } from '@/components/ui/Badges';
import { Button, IconButton } from '@/components/ui/Button';
import { Checkbox } from '@/components/ui/Checkbox';
import { cx } from '@/components/ui/cx';
import { Notice, Skeleton } from '@/components/ui/Feedback';
import { Menu, MenuItem, MenuSeparator } from '@/components/ui/Menu';
import { Popover } from '@/components/ui/Popover';
import { PosterArt } from '@/components/ui/PosterArt';
import { ProgressBar } from '@/components/ui/Progress';
import { toast } from '@/components/ui/Toast';
import { Tooltip, TooltipProvider } from '@/components/ui/Tooltip';
import { formatBytes, formatClock, formatEta, formatNumber, formatSpeed } from '@/lib/format';
import { qualityName } from '@/lib/quality';
import type { DisplayStatus } from '@/lib/status';

/* Live queue. The list renders again only when items or statuses change: progress,
   speed and time left are read per row from the progress store (download.progress). */

export type QueueStatic = Omit<QueueItem, 'progress' | 'speedBps' | 'etaSeconds' | 'downloadedBytes'>;

const ACTIVE = new Set(['downloading', 'queued']);
const ATTENTION = new Set(['failed', 'warning']);

/** Strips the fields that change every second: structural sharing then keeps the list stable. */
export function staticOf(items: QueueItem[]): QueueStatic[] {
  return items.map(({ progress: _p, speedBps: _s, etaSeconds: _e, downloadedBytes: _d, ...rest }) => rest);
}

export function groupQueue(items: readonly QueueStatic[]) {
  return [
    { id: 'downloading', label: 'Downloading', items: items.filter((q) => ACTIVE.has(q.status)) },
    { id: 'attention', label: 'Needs attention', items: items.filter((q) => ATTENTION.has(q.status)) },
    {
      id: 'waiting',
      label: 'Waiting',
      items: items.filter((q) => !ACTIVE.has(q.status) && !ATTENTION.has(q.status)),
    },
  ].filter((g) => g.items.length);
}

const acquisitionQuery = {
  queryKey: ['settings', 'acquisition'] as const,
  queryFn: async ({ signal }: { signal: AbortSignal }) => {
    try {
      return await unwrap(api.GET('/settings/acquisition', { signal }));
    } catch {
      return null;
    }
  },
  staleTime: 5 * 60_000,
};

const errorText = (e: unknown) => (e instanceof ApiError ? e.detail : 'Gonema could not be reached.');

export function QueuePanel() {
  const queue = useQuery({ ...queueQuery, select: staticOf });
  const acq = useQuery(acquisitionQuery);
  const [hidden, setHidden] = useState<Set<number>>(() => new Set());
  const items = useMemo(
    () => (queue.data ?? []).filter((q) => !hidden.has(q.id) && q.status !== 'removed'),
    [queue.data, hidden],
  );
  const groups = useMemo(() => groupQueue(items), [items]);

  const hide = (id: number, on: boolean) =>
    setHidden((h) => {
      const n = new Set(h);
      if (on) n.add(id);
      else n.delete(id);
      return n;
    });

  if (queue.isPending)
    return (
      <div className="q" aria-hidden="true">
        {[0, 1, 2].map((i) => (
          <div className="qi" key={i}>
            <Skeleton className="qi-sk-poster" delay={i * 60} />
            <div>
              <Skeleton className="sk-line" style={{ width: '40%' }} delay={i * 60} />
              <Skeleton className="sk-line" style={{ width: '70%', marginTop: 8 }} delay={i * 60} />
            </div>
            <Skeleton style={{ height: 6 }} delay={i * 60} />
            <span />
          </div>
        ))}
      </div>
    );
  if (queue.isError)
    return (
      <Notice
        tone="failed"
        role="alert"
        title="The queue could not be loaded."
        action={
          <Button size="sm" onClick={() => void queue.refetch()}>
            Try again
          </Button>
        }
      >
        {queue.error.message} Downloads continue in your clients meanwhile.
      </Notice>
    );
  if (!items.length) return <EmptyQueue every={acq.data?.rssIntervalMin} />;

  return (
    <TooltipProvider>
      <div className="q">
        {groups.map((g) => (
          <section key={g.id} aria-labelledby={`qg-${g.id}`}>
            <h2 className="grp-h" id={`qg-${g.id}`}>
              {g.label} <span className="count">{g.items.length}</span>
            </h2>
            <ul className="q-list">
              {g.items.map((q) => (
                <QueueRow key={q.id} item={q} onHide={hide} />
              ))}
            </ul>
          </section>
        ))}
      </div>
    </TooltipProvider>
  );
}

function EmptyQueue({ every }: { every?: number }) {
  const lib = useLibrary();
  const wanted = (lib.data ?? []).filter((m) => m.monitored && m.state === 'missing').length;
  return (
    <div className="empty">
      <span className="glyph" aria-hidden="true">
        <Clapperboard className="i" />
      </span>
      <h2>Nothing in the projector.</h2>
      <p>No downloads right now.{every ? ` Gonema checks your indexers every ${every} minutes.` : ''}</p>
      {wanted > 0 && (
        <div className="actions">
          <Link className="btn btn-secondary btn-sm" to="/films" search={{ view: 'wanted' }}>
            See the {formatNumber(wanted)} wanted {wanted === 1 ? 'film' : 'films'}
          </Link>
        </div>
      )}
    </div>
  );
}

type StatusView = { s: DisplayStatus; label: string; icon: typeof Hand };

export function statusOf(status: string, progress: number, attempt: number, of: number): StatusView {
  if (status === 'paused') return { s: 'unmonitored', label: 'Paused', icon: Hand };
  if (status === 'failed') return { s: 'failed', label: 'Failed', icon: TriangleAlert };
  if (status === 'warning') return { s: 'missing', label: 'Needs attention', icon: TriangleAlert };
  if (status === 'importing') return { s: 'upgraded', label: 'Importing', icon: FolderOpen };
  if (status === 'completed' || status === 'imported')
    return { s: 'available', label: status === 'imported' ? 'Imported' : 'Completed', icon: CircleCheck };
  if (attempt > 1) return { s: 'downloading', label: `Retry ${attempt} of ${of}`, icon: RefreshCw };
  if (status === 'queued' && progress <= 0) return { s: 'downloading', label: 'Queued', icon: ArrowDownToLine };
  return { s: 'downloading', label: `${Math.round(progress * 100)}%`, icon: ArrowDownToLine };
}

const QueueRow = memo(function QueueRow({
  item: q,
  onHide,
}: {
  item: QueueStatic;
  onHide: (id: number, hidden: boolean) => void;
}) {
  const qc = useQueryClient();
  const navigate = useNavigate();
  const p = useProgress(q.id, { progress: 0, speedBps: 0, etaSeconds: null, status: q.status });
  const status = p.status || q.status;
  const attempt = q.retry?.attempt ?? 1;
  const of = q.retry?.of || 6;
  const st = statusOf(status, p.progress, attempt, of);
  const Icon = st.icon;
  const title = q.movie?.title || q.title;
  // The server says what the download allows now; after an optimistic pause the reverse stays offered.
  const canToggle = q.actions ? q.actions.pause || q.actions.resume : true;
  const showPause = canToggle && (status === 'paused' || ACTIVE.has(status));
  const reason = q.failedReason || q.statusMessage || q.messages?.[0] || '';
  const done = status === 'completed' || status === 'imported';

  const setStatus = (s: QueueItem['status']) => {
    setProgress(q.id, { status: s });
    qc.setQueryData<QueueItem[]>(keys.queue, (prev) => prev?.map((x) => (x.id === q.id ? { ...x, status: s } : x)));
  };
  const pauseResume = async () => {
    const before = status as QueueItem['status'];
    const pausing = status !== 'paused';
    setStatus(pausing ? 'paused' : 'downloading');
    try {
      if (pausing) await pauseDownload(q.id);
      else await resumeDownload(q.id);
    } catch (e) {
      setStatus(before);
      toast.error(pausing ? `${title} was not paused` : `${title} did not resume`, errorText(e));
    }
  };
  const remove = (o: RemoveOptions) => {
    onHide(q.id, true);
    const what = [o.blocklist ? 'blocklisted' : '', o.deleteData ? 'its data deleted' : ''].filter(Boolean).join(', ');
    toast.show({
      title: `${title}: removed${what ? ` and ${what}` : ''}`,
      body: o.retry
        ? 'Searching for the next release.'
        : o.deleteData
          ? `The data is deleted in ${q.client || 'the client'} when this closes.`
          : 'The downloaded data stays in the client.',
      icon: <Ban className="i" />,
      undo: () => onHide(q.id, false),
      onClose: (why) => {
        if (why === 'undo') return;
        removeDownload(q.id, o)
          .then(() => void qc.invalidateQueries({ queryKey: keys.queue }))
          .catch((e) => {
            onHide(q.id, false);
            toast.error(`${title} was not removed`, errorText(e));
          });
      },
    });
  };
  const retry = async () => {
    try {
      await retryImport(q.id);
      setStatus('importing');
      toast.show({
        title: `Importing ${title} again`,
        body: 'History shows the result and why.',
        icon: <FolderOpen className="i" />,
      });
    } catch (e) {
      toast.error('The import did not start', errorText(e));
    }
  };

  return (
    <li className={cx('qi', (status === 'failed' || attempt > 1) && 'failed', status === 'warning' && 'warn')}>
      <PosterArt src={q.movie?.posterUrl ?? null} title={title} color={q.movie?.dominantColor} />
      <div className="qi-main">
        <div className="t">
          <Link to="/films/$movieId" params={{ movieId: q.movieId }}>
            {title}
          </Link>{' '}
          {q.movie?.year ? <span>{q.movie.year}</span> : null}
        </div>
        <div className="rel" title={q.title}>
          {q.title}
        </div>
        <div className="tags">
          <QualityBadge quality={q.quality} />
          <span>
            {[
              q.client,
              q.indexer,
              q.protocol === 'torrent' && q.seeders !== null && q.seeders >= 0 ? `▲ ${q.seeders}` : '',
              q.upgradeFrom ? `upgrade from ${qualityName(q.upgradeFrom)}` : '',
              q.manual ? 'manual grab' : '',
            ]
              .filter(Boolean)
              .join(' · ')}
          </span>
          {attempt > 1 && (
            <span className="attempts">
              <span className="sr-only">
                Attempt {attempt} of {of}
              </span>
              {Array.from({ length: Math.min(of, 12) }, (_, i) => (
                <i
                  // biome-ignore lint/suspicious/noArrayIndexKey: fixed list of attempt slots
                  key={i}
                  className={i < attempt - 1 ? 'x' : i === attempt - 1 ? 'now' : undefined}
                  aria-hidden="true"
                />
              ))}
            </span>
          )}
        </div>
      </div>
      <LiveProgress q={q} p={p} status={status} />
      <div className="acts">
        <span className="status" data-status={st.s}>
          <Icon className="i" aria-hidden="true" />
          {st.label}
        </span>
        {showPause && (
          <Tooltip content={status === 'paused' ? 'Resume' : 'Pause'}>
            <IconButton
              size="sm"
              label={`${status === 'paused' ? 'Resume' : 'Pause'} ${title}`}
              onClick={() => void pauseResume()}
            >
              {status === 'paused' ? (
                <Play className="i i-sm" aria-hidden="true" />
              ) : (
                <Hand className="i i-sm" aria-hidden="true" />
              )}
            </IconButton>
          </Tooltip>
        )}
        {(q.actions?.remove ?? true) && <RemovePopover title={title} client={q.client} done={done} onRemove={remove} />}
        <Menu
          label={`More for ${title}`}
          align="end"
          trigger={
            <IconButton size="sm" label={`More actions for ${title}`}>
              <Ellipsis className="i i-sm" aria-hidden="true" />
            </IconButton>
          }
        >
          <MenuItem
            icon={<Film className="i" aria-hidden="true" />}
            onSelect={() => void navigate({ to: '/films/$movieId', params: { movieId: q.movieId } })}
          >
            Open film
          </MenuItem>
          <MenuItem
            icon={<ScanSearch className="i" aria-hidden="true" />}
            onSelect={() => void navigate({ to: '/films/$movieId/search', params: { movieId: q.movieId } })}
          >
            Interactive search…
          </MenuItem>
          {q.actions?.retryImport && (
            <>
              <MenuSeparator />
              <MenuItem icon={<FolderOpen className="i" aria-hidden="true" />} onSelect={() => void retry()}>
                Retry import
              </MenuItem>
            </>
          )}
        </Menu>
      </div>
      {(status === 'failed' || status === 'warning' || attempt > 1) && (
        <div className="why">
          <TriangleAlert className="i" aria-hidden="true" />
          <span>
            {status === 'failed' ? (
              <>
                <b>The print arrived damaged.</b> {reason || 'The download failed in the client.'}
                {attempt < of ? ' Gonema blocklisted it and tries the next release.' : ''}
              </>
            ) : status === 'warning' ? (
              <>
                <b>Downloaded, but not imported.</b> {reason || 'The import needs a hand.'} Retry the import once it’s
                fixed.
              </>
            ) : (
              <>
                <b>The first print arrived damaged.</b>{' '}
                {q.retry?.previousRelease ? (
                  <span className="t-mono-sm">{q.retry.previousRelease}</span>
                ) : (
                  'The previous release'
                )}{' '}
                {q.retry?.previousReason ? `failed (${q.retry.previousReason.replace(/\.$/, '')})` : 'failed'}, so
                Gonema blocklisted it and is{' '}
                <b>
                  trying release #{attempt} of {of}
                </b>
                .
              </>
            )}
          </span>
          {q.actions?.retryImport && status === 'warning' ? (
            <Button size="sm" variant="secondary" onClick={() => void retry()}>
              Retry import
            </Button>
          ) : (
            <Link className="btn btn-ghost btn-sm" to="/queue/$downloadId" params={{ downloadId: q.id }}>
              Details
            </Link>
          )}
        </div>
      )}
    </li>
  );
});

/** Size, speed, time left and the bar: the only part of a row that follows download.progress. */
function LiveProgress({ q, p, status }: { q: QueueStatic; p: Progress; status: string }) {
  if (status === 'importing')
    return (
      <div className="prog">
        <div className="row">
          <span>Importing · {formatBytes(q.size)}</span>
        </div>
        <Skeleton style={{ height: 6, borderRadius: 9 }} />
      </div>
    );
  if (status === 'completed' || status === 'imported')
    return (
      <div className="prog">
        <div className="row">
          <span>{status === 'imported' ? 'Imported' : 'Downloaded, import next'}</span>
          <span>
            {q.importedAt
              ? formatClock(new Date(q.importedAt))
              : q.completedAt
                ? formatClock(new Date(q.completedAt))
                : ''}
          </span>
        </div>
        <ProgressBar value={1} label={`Download of ${q.title}: complete`} className="done" />
      </div>
    );
  const right =
    status === 'paused'
      ? 'Paused'
      : status === 'failed'
        ? 'Stopped'
        : [formatSpeed(p.speedBps), p.etaSeconds ? formatEta(p.etaSeconds) : ''].filter(Boolean).join(' · ') ||
          (status === 'queued' ? 'Waiting in the client' : '');
  return (
    <div className="prog">
      <div className="row">
        <span>
          {q.size
            ? `${p.progress > 0 ? formatBytes(p.progress * q.size).replace(/ [A-Z]+$/, '') : '0'} of ${formatBytes(q.size)}`
            : `${Math.round(p.progress * 100)}%`}
        </span>
        <span>{right}</span>
      </div>
      <ProgressBar
        value={p.progress}
        label={`Download of ${q.title}`}
        live={status === 'downloading'}
        state={status === 'paused' ? 'paused' : status === 'failed' ? 'failed' : 'active'}
      />
    </div>
  );
}

/** Remove a download: blocklist and search again by default; deleting the data is an explicit choice. */
function RemovePopover({
  title,
  client,
  done,
  onRemove,
}: {
  title: string;
  client: string;
  done: boolean;
  onRemove: (o: RemoveOptions) => void;
}) {
  const [open, setOpen] = useState(false);
  const [o, setO] = useState<RemoveOptions>({ blocklist: !done, retry: !done, deleteData: false });
  return (
    <Popover
      label={`Remove ${title}`}
      align="end"
      open={open}
      onOpenChange={setOpen}
      trigger={
        <IconButton size="sm" label={`Remove ${title}…`}>
          <Ban className="i i-sm" aria-hidden="true" />
        </IconButton>
      }
    >
      <form
        className="remove-pop"
        onSubmit={(ev) => {
          ev.preventDefault();
          setOpen(false);
          onRemove(o);
        }}
      >
        <p className="rp-title">Remove from the queue?</p>
        <Checkbox
          checked={o.blocklist}
          onCheckedChange={(v) => setO({ ...o, blocklist: v })}
          label="Blocklist this release"
        />
        <Checkbox
          checked={o.retry}
          onCheckedChange={(v) => setO({ ...o, retry: v })}
          label="Search for another release"
        />
        <Checkbox
          checked={o.deleteData}
          onCheckedChange={(v) => setO({ ...o, deleteData: v })}
          label={`Also delete the downloaded data in ${client || 'the client'}`}
        />
        <p className="t-caption t-muted">Undo is offered for 8 seconds; nothing is sent before.</p>
        <div className="actions">
          <Button size="sm" variant="ghost" onClick={() => setOpen(false)}>
            Cancel
          </Button>
          <Button size="sm" variant="danger" type="submit">
            Remove
          </Button>
        </div>
      </form>
    </Popover>
  );
}
