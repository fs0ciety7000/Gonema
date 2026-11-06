import { useQueryClient } from '@tanstack/react-query';
import { ChevronRight, CircleCheck, CircleX, FileVideo, Folder, Library, ScanSearch } from 'lucide-react';
import { useEffect, useState } from 'react';
import { ApiError } from '@/api/client';
import { cancelScan, type LibraryFolder, type LibraryScan, thumbUrl, v11Keys } from '@/api/v11';
import { Button } from '@/components/ui/Button';
import { Skeleton } from '@/components/ui/Feedback';
import { PosterArt } from '@/components/ui/PosterArt';
import { ProgressBar } from '@/components/ui/Progress';
import { toast } from '@/components/ui/Toast';
import { formatClock, formatNumber } from '@/lib/format';
import { feedOf, formatSeconds, secondsLeft } from './importModel';

export function FolderBadge({ status }: { status: LibraryFolder['status'] }) {
  switch (status) {
    case 'new':
      return (
        <span className="badge" data-status="available">
          <CircleCheck className="i" aria-hidden="true" />
          New
        </span>
      );
    case 'ambiguous':
      return (
        <span className="badge" data-status="missing">
          <ScanSearch className="i" aria-hidden="true" />
          Ambiguous
        </span>
      );
    case 'no_match':
      return (
        <span className="badge">
          <CircleX className="i" aria-hidden="true" />
          No match
        </span>
      );
    default:
      return (
        <span className="badge">
          <Library className="i" aria-hidden="true" />
          In library
        </span>
      );
  }
}

function useNow(every: number, on: boolean) {
  const [now, setNow] = useState(() => Date.now());
  useEffect(() => {
    if (!on) return;
    const t = setInterval(() => setNow(Date.now()), every);
    return () => clearInterval(t);
  }, [every, on]);
  return now;
}

export function ScanStep({
  scan,
  path,
  arrived,
  startedAt,
  onReview,
}: {
  scan: LibraryScan;
  path: string;
  arrived: readonly string[];
  startedAt: number;
  onReview: () => void;
}) {
  const qc = useQueryClient();
  const [stopping, setStopping] = useState(false);
  const now = useNow(1000, true);
  const started = Date.parse(scan.createdAt) || startedAt || now;
  const elapsed = Math.max(0, (now - started) / 1000);
  const left = secondsLeft(scan, now);
  const found = scan.folders?.length ?? 0;
  const feed = feedOf(scan, arrived);
  const stop = async () => {
    setStopping(true);
    try {
      const s = await cancelScan(scan.id);
      qc.setQueryData<LibraryScan>(v11Keys.scan(scan.id), (p) => (p ? { ...p, ...s, folders: p.folders } : p));
      void qc.invalidateQueries({ queryKey: v11Keys.scan(scan.id) });
    } catch (e) {
      setStopping(false);
      toast.error('The scan goes on', e instanceof ApiError ? e.detail : undefined);
    }
  };
  return (
    <div className="scan">
      <section className="panel" aria-labelledby="s-h">
        <div className="panel-hd">
          <h2 id="s-h">
            Reading <span className="t-mono">{path}</span>
          </h2>
          <span className="t-caption t-muted tnum">
            Started {formatClock(new Date(started))} · {formatSeconds(elapsed)}
          </span>
        </div>
        <div className="panel-bd">
          <div>
            <ProgressBar size="lg" live value={scan.total ? scan.done / scan.total : 0} label="Scan progress" />
            <div className="progress-meta">
              <span>
                {scan.total
                  ? `${formatNumber(scan.done)} of ${formatNumber(scan.total)} folders`
                  : 'Listing the folders…'}
              </span>
              <span>{left !== null ? `About ${formatSeconds(left)} left` : ''}</span>
            </div>
          </div>
          <ul className="tally" aria-label="Found so far">
            <li>
              <span className="status" data-status="available">
                <CircleCheck className="i" aria-hidden="true" />
                New
              </span>
              <b>{formatNumber(scan.counts.new)}</b>
            </li>
            <li>
              <span className="status" data-status="missing">
                <ScanSearch className="i" aria-hidden="true" />
                Ambiguous
              </span>
              <b>{formatNumber(scan.counts.ambiguous)}</b>
            </li>
            <li>
              <span className="status">
                <CircleX className="i" aria-hidden="true" />
                No match
              </span>
              <b>{formatNumber(scan.counts.noMatch)}</b>
            </li>
            <li>
              <span className="status" data-status="unmonitored">
                <Library className="i" aria-hidden="true" />
                In library
              </span>
              <b>{formatNumber(scan.counts.inLibrary)}</b>
            </li>
          </ul>
          <div>
            <div className="row feed-hd">
              <h3 className="t-title-md">Found so far</h3>
              <span className="t-caption t-muted">Newest first</span>
            </div>
            <div role="log" aria-live="polite" aria-label="Folders found">
              <ul className="feed">
                <li className="reading">
                  <span className="fold">{scan.status === 'pending' ? 'Waiting for its turn…' : 'Next folder'}</span>
                  <ChevronRight className="i arrow" aria-hidden="true" />
                  <span className="m">
                    <Skeleton className="th" />
                    <Skeleton className="sk-line" />
                  </span>
                  <span className="t-caption t-muted">Reading…</span>
                </li>
                {feed.map((f) => {
                  const c = f.candidates?.[0];
                  return (
                    <li key={f.folder}>
                      <span className="fold" title={f.folder}>
                        {f.name}
                      </span>
                      <ChevronRight className="i arrow" aria-hidden="true" />
                      <span className="m">
                        {c && f.status !== 'no_match' ? (
                          <>
                            <span className="th">
                              <PosterArt src={thumbUrl(c.posterUrl)} title={c.title} />
                            </span>
                            <span>
                              {c.title} {c.year ? <em>{c.year}</em> : null}
                            </span>
                          </>
                        ) : (
                          <span className="t-muted fw-400">No film found</span>
                        )}
                      </span>
                      <FolderBadge status={f.status} />
                    </li>
                  );
                })}
              </ul>
            </div>
          </div>
          <div className="foot">
            <p>
              Nothing on disk changes during a scan. You can leave this page: Gonema keeps reading and tells you when
              the folders are ready to review.
            </p>
            <Button variant="ghost" loading={stopping} disabled={stopping} onClick={() => void stop()}>
              Stop scan
            </Button>
            <Button disabled={!found} onClick={onReview}>
              Review the {formatNumber(found)} found so far
            </Button>
          </div>
        </div>
      </section>
      <aside className="panel promise" aria-labelledby="how2-h">
        <div className="panel-hd">
          <h2 id="how2-h">What Gonema looks at</h2>
        </div>
        <div className="panel-bd">
          <ol>
            <li>
              <span className="ic" aria-hidden="true">
                <Folder className="i" />
              </span>
              <span>
                <b>The folder name</b>Title and year, like <span className="t-mono-sm">Marrow (2025)</span>. Release
                names work too.
              </span>
            </li>
            <li>
              <span className="ic" aria-hidden="true">
                <FileVideo className="i" />
              </span>
              <span>
                <b>The largest video file</b>Resolution, source, HDR and size. Samples under 300 MB are ignored.
              </span>
            </li>
            <li>
              <span className="ic" aria-hidden="true">
                <Library className="i" />
              </span>
              <span>
                <b>Your library</b>A film you already have is flagged, never imported twice.
              </span>
            </li>
          </ol>
        </div>
      </aside>
    </div>
  );
}
