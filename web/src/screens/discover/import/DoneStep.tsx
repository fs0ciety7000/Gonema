import { Link } from '@tanstack/react-router';
import {
  ChevronRight,
  CircleArrowUp,
  CircleCheck,
  History,
  Layers,
  Minus,
  ScanSearch,
  TriangleAlert,
} from 'lucide-react';
import { useState } from 'react';
import { ApiError } from '@/api/client';
import { useLibrary } from '@/api/queries';
import { type LibraryFolder, type LibraryImport, type LibraryScan, startScan } from '@/api/v11';
import { Button, buttonClass } from '@/components/ui/Button';
import { PosterArt } from '@/components/ui/PosterArt';
import { toast } from '@/components/ui/Toast';
import { formatNumber } from '@/lib/format';
import { doneSummary, formatSeconds, listWords, tabCounts } from './importModel';

const plural = (n: number, w: string, ws = `${w}s`) => `${formatNumber(n)} ${n === 1 ? w : ws}`;

export function DoneStep({
  result,
  took,
  leftCounts,
  rootFolderId,
  onReviewLeft,
  onAnother,
  onScanFolder,
}: {
  result: LibraryImport;
  took: number;
  /** Folders of the scan not imported (the "left for later"). */
  leftCounts: readonly LibraryFolder[];
  rootFolderId: number;
  onReviewLeft: () => void;
  onAnother: () => void;
  onScanFolder: (s: LibraryScan) => void;
}) {
  const s = doneSummary(result);
  const left = tabCounts(leftCounts);
  const failedNames = new Set(s.failures.map((f) => f.folder));
  const leftNew = leftCounts.filter((f) => f.status === 'new' && !failedNames.has(f.folder)).length;
  const [busy, setBusy] = useState<string | null>(null);
  const rescan = async (folder: string) => {
    setBusy(folder);
    try {
      onScanFolder(await startScan(rootFolderId, folder));
    } catch (e) {
      toast.error('The scan did not start', e instanceof ApiError ? e.detail : undefined);
    } finally {
      setBusy(null);
    }
  };
  const ambiguous = leftCounts.filter((f) => f.status === 'ambiguous').length;
  const noMatch = leftCounts.filter((f) => f.status === 'no_match').length;
  const firstFailure = s.failures[0];
  const lib = useLibrary();
  const ids = (result.results ?? []).filter((r) => r.movieId && (r.outcome === 'kept' || r.outcome === 'moved'));
  const byId = new Map((lib.data ?? []).map((m) => [m.id, m]));
  const strip = ids.map((r) => byId.get(r.movieId!)).filter((m) => !!m);
  return (
    <div className="done-wrap">
      <div>
        <span className="t-overline">Import finished · {formatSeconds(took / 1000)}</span>
        <h2 className="big">
          {s.imported
            ? `${plural(s.imported, 'film is', 'films are')} in the house.`
            : s.added
              ? `${plural(s.added, 'film is', 'films are')} in the library.`
              : 'Nothing was imported.'}
        </h2>
        <p className="lead">
          {s.imported
            ? `They’re in your library, pointed at their files. ${formatNumber(s.kept)} stayed where ${s.kept === 1 ? 'it was' : 'they were'} and ${formatNumber(s.moved)} ${s.moved === 1 ? 'was' : 'were'} moved into Gonema’s naming. Nothing was deleted.`
            : 'Nothing was deleted or moved. The reasons are below.'}
        </p>
        {strip.length > 0 && (
          <div className="strip" aria-hidden="true">
            {strip.slice(0, 7).map((m) => (
              <PosterArt key={m.id} src={m.posterUrl} title={m.title} year={m.year} color={m.dominantColor} />
            ))}
            {ids.length > 7 && <span className="more">+{formatNumber(ids.length - 7)}</span>}
          </div>
        )}
        <div className="results">
          <div>
            <span className="status" data-status="available">
              <CircleCheck className="i" aria-hidden="true" />
              Imported
            </span>
            <b>{formatNumber(s.imported)}</b>
            <span className="t-caption">
              {formatNumber(s.kept)} kept in place · {formatNumber(s.moved)} moved
              {s.added ? ` · ${formatNumber(s.added)} without a file` : ''}
            </span>
          </div>
          <div>
            <span className="status" data-status="unmonitored">
              <Minus className="i" aria-hidden="true" />
              Left for later
            </span>
            <b>{formatNumber(leftCounts.length)}</b>
            <span className="t-caption">
              {[
                ambiguous ? `${formatNumber(ambiguous)} ambiguous` : '',
                noMatch ? `${formatNumber(noMatch)} no match` : '',
                left.library ? `${formatNumber(left.library)} in library` : '',
                leftNew ? `${formatNumber(leftNew)} not selected` : '',
                s.failed ? `${formatNumber(s.failed)} failed` : '',
              ]
                .filter(Boolean)
                .join(' · ') || 'None'}
            </span>
          </div>
          <div>
            <span className="status" data-status="failed">
              <TriangleAlert className="i" aria-hidden="true" />
              Couldn’t import
            </span>
            <b>{formatNumber(s.failed)}</b>
            <span className="t-caption">{firstFailure ? `${firstFailure.folder} · see below` : 'None'}</span>
          </div>
        </div>
        {s.failures.slice(0, 5).map((f) => (
          <div className="notice" data-tone="failed" key={f.folder}>
            <TriangleAlert className="i" aria-hidden="true" />
            <div className="grow">
              <div className="ntitle">{f.folder} couldn’t be imported</div>
              <div className="nbody">{f.error} Gonema left it untouched.</div>
            </div>
            <Button size="sm" loading={busy === f.folder} disabled={!!busy} onClick={() => void rescan(f.folder)}>
              Scan {f.folder} again
            </Button>
          </div>
        ))}
        <div className="done-actions">
          <Link className={buttonClass('primary', 'lg')} to="/films">
            Open the library
            <ChevronRight className="i" aria-hidden="true" />
          </Link>
          {leftCounts.length > 0 && (
            <Button size="lg" onClick={onReviewLeft}>
              Review the {formatNumber(leftCounts.length)} left
            </Button>
          )}
          <Button variant="ghost" size="lg" onClick={onAnother}>
            Import another folder
          </Button>
        </div>
      </div>
      <aside className="panel" aria-labelledby="next-h">
        <div className="panel-hd">
          <h2 id="next-h">What Gonema does next</h2>
        </div>
        <div className="panel-bd">
          <ul className="next">
            {s.belowCutoff > 0 && (
              <li>
                <span className="ic" aria-hidden="true">
                  <CircleArrowUp className="i" />
                </span>
                <span>
                  <b>{plural(s.belowCutoff, 'film is', 'films are')} below their profile.</b> Gonema looks for better
                  copies at the next RSS check, for the monitored ones. The files you have stay until a better one has
                  arrived.
                </span>
              </li>
            )}
            {s.collections.length > 0 && (
              <li>
                <span className="ic" aria-hidden="true">
                  <Layers className="i" />
                </span>
                <span>
                  <b>{plural(s.collections.length, 'collection')} grew.</b> {listWords(s.collections)}{' '}
                  {s.collections.length === 1 ? 'has' : 'have'} more films on disk.
                </span>
              </li>
            )}
            {leftCounts.length > 0 && (
              <li>
                <span className="ic" aria-hidden="true">
                  <ScanSearch className="i" />
                </span>
                <span>
                  <b>{plural(leftCounts.length, 'folder is', 'folders are')} kept for later.</b> Their matches are
                  saved; come back to them anytime from this tab.
                </span>
              </li>
            )}
            <li>
              <span className="ic" aria-hidden="true">
                <History className="i" />
              </span>
              <span>
                <b>{plural(s.imported + s.added, 'entry', 'entries')} in History,</b> one per film, with the folder it
                came from.
              </span>
            </li>
          </ul>
        </div>
      </aside>
    </div>
  );
}
