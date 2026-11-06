import '@/styles/lot2.css';
import '@/styles/lot3.css';
import { useQuery, useQueryClient } from '@tanstack/react-query';
import { Link, useParams } from '@tanstack/react-router';
import { ArrowDownToLine, ChevronRight, CircleX, Info, ScanSearch } from 'lucide-react';
import { useEffect, useMemo, useState } from 'react';
import { blockGroup, blocklistQuery, deleteBlocklistEntry, lot3Keys } from '@/api/lot3';
import { historyQuery, useMovie, useProfiles, useQueue } from '@/api/queries';
import type { HistoryEntry } from '@/api/schema';
import { ThemeToggle, Topbar } from '@/components/shell/Topbar';
import { ScorePill, StatusBadge, StatusText } from '@/components/ui/Badges';
import { Button, buttonClass } from '@/components/ui/Button';
import { EmptyState, Skeleton } from '@/components/ui/Feedback';
import { toast } from '@/components/ui/Toast';
import { formatBytes, formatWhen } from '@/lib/format';
import { qualityName } from '@/lib/quality';
import { errorText } from '@/screens/settings/form';
import mascotFailed from '../../../../docs/brand/kit/mascot/kernel-failed-download.svg?url';

type Attempt = {
  n: number;
  release: string;
  score: number | null;
  sub: string;
  state: 'failed' | 'now' | 'done';
  reason?: string;
};

const FAILED = new Set(['download_failed', 'grab_failed', 'import_failed']);
const str = (v: unknown) => (typeof v === 'string' && v ? v : null);

/** Releases grabbed for the film, oldest first, with how each one ended (from the film's history). */
export function attemptsOf(history: readonly HistoryEntry[], currentDownloadId: number | null): Attempt[] {
  const sorted = [...history].sort((a, b) => a.createdAt.localeCompare(b.createdAt));
  const out: Attempt[] = [];
  for (const e of sorted) {
    if (e.event !== 'grabbed' && e.event !== 'grab_overridden') continue;
    const data = (e.data ?? {}) as Record<string, unknown>;
    const failure = sorted.find(
      (f) => FAILED.has(f.event) && ((e.downloadId && f.downloadId === e.downloadId) || f.sourceTitle === e.sourceTitle),
    );
    const reason = failure ? (str((failure.data as Record<string, unknown> | null)?.reason) ?? 'failed') : undefined;
    out.push({
      n: out.length + 1,
      release: e.sourceTitle,
      score: e.customScore,
      sub: [str(data.protocol), str(data.indexer), typeof data.size === 'number' ? formatBytes(data.size) : null, reason ? `blocklisted: ${reason}` : null]
        .filter(Boolean)
        .join(' · '),
      state: failure ? 'failed' : e.downloadId === currentDownloadId ? 'now' : 'done',
      reason,
    });
  }
  return out;
}

export function FailedDownloadScreen() {
  const { downloadId } = useParams({ from: '/queue/$downloadId' });
  const qc = useQueryClient();
  const queue = useQueue();
  const item = queue.data?.find((q) => q.id === downloadId);
  const movieId = item?.movieId ?? 0;
  const movie = useMovie(movieId);
  const history = useQuery({ ...historyQuery(movieId), enabled: movieId > 0 });
  const profiles = useProfiles();
  const blocklist = useQuery(blocklistQuery);
  const [hidden, setHidden] = useState<ReadonlySet<number>>(new Set());
  useEffect(() => {
    document.title = `${item?.movie?.title ?? 'Download'} · failed download · Gonema`;
  }, [item?.movie?.title]);
  const attempts = useMemo(() => attemptsOf(history.data ?? [], item?.id ?? null), [history.data, item?.id]);
  const failedRelease = item?.status === 'failed' ? item.title : (item?.retry?.previousRelease ?? attempts.find((a) => a.state === 'failed')?.release);
  const entry = (blocklist.data ?? []).find(
    (b) => !hidden.has(b.id) && b.movieId === movieId && (b.sourceTitle === failedRelease || b.kind === 'group'),
  );
  const group = /-([A-Za-z0-9]+)$/.exec(failedRelease ?? '')?.[1];
  const profile = profiles.data?.find((p) => p.id === movie.data?.qualityProfileId);
  const allowed = (profile?.items ?? []).filter((i) => i.allowed);

  if (queue.isPending)
    return (
      <main id="main" className="page" tabIndex={-1} aria-busy="true">
        <Skeleton className="sk-wanted" />
      </main>
    );
  if (!item)
    return (
      <>
        <Topbar>
          <div className="spacer" />
          <ThemeToggle />
        </Topbar>
        <main id="main" className="page" tabIndex={-1}>
          <EmptyState
            title="This download has left the queue."
            action={
              <Link className={buttonClass('secondary', 'sm')} to="/history">
                Open history
              </Link>
            }
          >
            It was imported, removed or replaced by the next release. Its whole story is in the history.
          </EmptyState>
        </main>
      </>
    );

  const title = item.movie?.title ?? movie.data?.title ?? item.title;
  const failed = item.status === 'failed';
  const attempt = item.retry?.attempt ?? 1;
  const of = item.retry?.of ?? 6;
  const reason = item.failedReason || item.retry?.previousReason || item.statusMessage;
  const lead = failed
    ? attempt < of
      ? `Blocklisted it and moving on to the next release (${attempt + 1} of ${of}). Nothing for you to do unless you want to choose yourself.`
      : `That was the last of ${of} automatic tries. Choose a release yourself, or Gonema tries again at the next search.`
    : `Blocklisted it and moved on. ${title} is downloading again (release ${attempt} of ${of}). Nothing for you to do unless you want to choose yourself.`;

  const removeEntry = () => {
    if (!entry) return;
    setHidden((h) => new Set(h).add(entry.id));
    const restore = () =>
      setHidden((h) => {
        const n = new Set(h);
        n.delete(entry.id);
        return n;
      });
    toast.show({
      title: 'Removed from the blocklist',
      body: 'Gonema may grab this release again if it is the best one.',
      undo: restore,
      onClose: (r) => {
        if (r === 'undo') return;
        deleteBlocklistEntry(entry.id)
          .then(() => qc.invalidateQueries({ queryKey: lot3Keys.blocklist }))
          .catch((e) => {
            restore();
            toast.error('Still blocklisted', errorText(e));
          });
      },
    });
  };
  const blockAll = async () => {
    if (!group) return;
    try {
      await blockGroup({ group, movieId, reason: reason || undefined });
      void qc.invalidateQueries({ queryKey: lot3Keys.blocklist });
      toast.show({ title: `Blocked every release from ${group}`, body: 'For this film. Remove it from the blocklist to undo.' });
    } catch (e) {
      toast.error(`${group} was not blocked`, errorText(e));
    }
  };

  return (
    <>
      <Topbar>
        <nav className="crumbs" aria-label="Breadcrumb">
          <Link to="/queue">Activity</Link>
          <ChevronRight className="i" aria-hidden="true" />
          <Link to="/queue">Queue</Link>
          <ChevronRight className="i" aria-hidden="true" />
          <span aria-current="page">{title} · failed download</span>
        </nav>
        <div className="spacer" />
        <ThemeToggle />
      </Topbar>
      <main id="main" className="page fd-page" tabIndex={-1}>
        <div className="fd">
          <div>
            <div className="hero">
              <img src={mascotFailed} alt="Kernel holding a torn strip of film" />
              <div>
                <StatusBadge status="failed" lg label={`Failed${item.completedAt ? ` · ${formatWhen(item.completedAt)}` : ''}`} />
                <h1>The print arrived damaged.</h1>
                <p>
                  {reason ? `${reason.replace(/\.$/, '')}. ` : ''}
                  {lead}
                </p>
                <div className="row">
                  <Link className={buttonClass('secondary')} to="/films/$movieId/search" params={{ movieId }}>
                    <ScanSearch className="i" aria-hidden="true" />
                    Choose another release
                  </Link>
                </div>
              </div>
            </div>

            <h2 className="sec-h">Releases tried, in order</h2>
            {history.isPending ? (
              <Skeleton className="sk-wanted" />
            ) : attempts.length ? (
              <ol className="attempts">
                {attempts.map((a) => (
                  <li key={`${a.n}-${a.release}`} className={a.state === 'failed' ? 'att x' : a.state === 'now' ? 'att now' : 'att'}>
                    <span className="n" aria-hidden="true">
                      {a.n}
                    </span>
                    <div className="min-w-0">
                      <div className="rel" title={a.release}>
                        {a.release}
                      </div>
                      <div className="sub">{a.sub || '—'}</div>
                    </div>
                    {a.score != null ? <ScorePill score={a.score} /> : <span />}
                    {a.state === 'failed' ? (
                      <span className="status" data-status="failed">
                        <CircleX className="i" aria-hidden="true" />
                        Failed
                      </span>
                    ) : a.state === 'now' ? (
                      <StatusText status="downloading">Now</StatusText>
                    ) : (
                      <span className="t-caption t-muted">Grabbed</span>
                    )}
                  </li>
                ))}
              </ol>
            ) : (
              <p className="t-secondary">No grab is recorded for this film yet.</p>
            )}

            {!!item.messages?.length && (
              <>
                <h2 className="sec-h">What the client said</h2>
                <div className="log" role="log" aria-label={`${item.client} messages`}>
                  {item.messages.map((m, i) => (
                    <div key={`${i}-${m}`} className={/fail|error|encrypt|corrupt|missing/i.test(m) ? 'e' : undefined}>
                      {m}
                    </div>
                  ))}
                </div>
              </>
            )}
          </div>

          <aside aria-label="Details">
            <section className="panel" aria-labelledby="f-h">
              <div className="panel-hd">
                <h2 id="f-h">Film</h2>
                <Link className={buttonClass('ghost', 'sm')} to="/films/$movieId" params={{ movieId }}>
                  Open
                </Link>
              </div>
              <div className="panel-bd">
                <dl className="dl">
                  <dt>Title</dt>
                  <dd>
                    {title}
                    {movie.data?.year ? ` (${movie.data.year})` : ''}
                  </dd>
                  <dt>Profile</dt>
                  <dd>{profile?.name ?? '—'}</dd>
                  <dt>Wanted</dt>
                  <dd>{allowed.length ? `${qualityName(allowed.at(-1)!.quality)} or better` : '—'}</dd>
                  <dt>Cutoff</dt>
                  <dd>{profile?.cutoffQuality ? qualityName(profile.cutoffQuality) : '—'}</dd>
                </dl>
              </div>
            </section>
            <section className="panel" aria-labelledby="b-h">
              <div className="panel-hd">
                <h2 id="b-h">Blocklist entry</h2>
              </div>
              <div className="panel-bd">
                {entry ? (
                  <>
                    <dl className="dl">
                      <dt>Scope</dt>
                      <dd>{entry.kind === 'group' ? `Every release from ${entry.releaseGroup}` : 'This release only'}</dd>
                      <dt>Reason</dt>
                      <dd>{entry.reason || '—'}</dd>
                      <dt>Added</dt>
                      <dd>{formatWhen(entry.createdAt)}</dd>
                    </dl>
                    <div className="row fd-acts">
                      {entry.kind === 'release' && group && (
                        <Button size="sm" onClick={() => void blockAll()}>
                          Block the whole group
                        </Button>
                      )}
                      <Button size="sm" variant="ghost" onClick={removeEntry}>
                        Remove from blocklist
                      </Button>
                    </div>
                  </>
                ) : (
                  <p className="t-secondary t-body-sm">
                    {blocklist.isPending ? ' ' : 'Not on the blocklist (yet): Gonema adds it when it gives up on the release.'}
                  </p>
                )}
              </div>
            </section>
            <div className="notice" data-tone="info">
              <Info className="i" aria-hidden="true" />
              <div className="grow">
                <div className="ntitle">Why the next one?</div>
                <div className="nbody">
                  Releases are tried best first: quality in your profile, then score, then protocol priority and size.
                  Gonema stops after {of} attempts and tells you.
                </div>
              </div>
            </div>
            {!failed && (
              <p className="t-caption t-muted fd-now">
                <ArrowDownToLine className="i i-xs" aria-hidden="true" /> Now downloading: <span className="t-mono-sm">{item.title}</span>
              </p>
            )}
          </aside>
        </div>
      </main>
    </>
  );
}

