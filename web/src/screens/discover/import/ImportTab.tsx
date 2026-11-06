import '@/styles/v11.css';
import { useQuery, useQueryClient } from '@tanstack/react-query';
import { useNavigate } from '@tanstack/react-router';
import { Check } from 'lucide-react';
import { useEffect, useMemo, useState } from 'react';
import { onServerEvent } from '@/api/live';
import { useRootFolders } from '@/api/queries';
import type { LibraryFolder, LibraryImport, LibraryScan } from '@/api/v11';
import { scanQuery, scansQuery, v11Keys } from '@/api/v11';
import { Button } from '@/components/ui/Button';
import { Notice, Skeleton } from '@/components/ui/Feedback';
import { formatNumber } from '@/lib/format';
import { DoneStep } from './DoneStep';
import { FolderStep } from './FolderStep';
import { isRunning, selectionSummary, withFolder } from './importModel';
import { ReviewStep, useChoices } from './ReviewStep';
import { ScanStep } from './ScanStep';

/* Add & discover › Import folder (library-import.html): folder → scan (streams, goes on when the page
   is left) → review (every match, keep or move) → done. The scan being looked at is in the URL
   (?tab=import&scan=ID), so a reload or a later visit comes back to it. Nothing is deleted. */

type Step = 'folder' | 'scan' | 'review' | 'done';
const STEPS: ReadonlyArray<[Step, string]> = [
  ['folder', 'Folder'],
  ['scan', 'Scan'],
  ['review', 'Review'],
  ['done', 'Done'],
];

/** Absolute path of what a scan reads. */
export function scanPath(rootPath: string | undefined, sub: string): string {
  const root = (rootPath ?? '').replace(/\/+$/, '');
  return sub ? `${root}/${sub}` : root || '/';
}

export function ImportTab({ scanId }: { scanId?: number }) {
  const navigate = useNavigate();
  const qc = useQueryClient();
  const roots = useRootFolders();
  const scans = useQuery({ ...scansQuery, refetchOnMount: 'always' });
  const scan = useQuery({
    ...scanQuery(scanId ?? 0),
    // safety net under the events: a running scan is asked again every 4 s
    refetchInterval: (q) => (isRunning(q.state.data) ? 4000 : false),
  });
  const [view, setView] = useState<'review' | 'done' | null>(null);
  const [result, setResult] = useState<LibraryImport | null>(null);
  const [imported, setImported] = useState<ReadonlySet<string>>(new Set());
  const [arrived, setArrived] = useState<string[]>([]);
  const [startedAt, setStartedAt] = useState(0);
  const [took, setTook] = useState(0);

  const open = (id: number | undefined, replace = false) => {
    setView(null);
    setResult(null);
    setImported(new Set());
    setArrived([]);
    void navigate({ to: '/discover', search: (s) => ({ ...s, tab: 'import', scan: id }), replace });
  };

  useEffect(() => {
    document.title = 'Import a folder · Gonema';
  }, []);

  // coming back to the tab: a scan still running is shown again
  const running = scans.data?.find((s) => isRunning(s))?.id;
  // biome-ignore lint/correctness/useExhaustiveDependencies: only when the list arrives (open is stable enough)
  useEffect(() => {
    if (!scanId && running) open(running, true);
  }, [scanId, running]);

  // the folders as they are described (libraryimport.folder), and the scan's progress
  useEffect(() => {
    if (!scanId) return;
    return onServerEvent((e) => {
      if (!e.type.startsWith('libraryimport.')) return;
      const d = e.data as { scanId?: number; id?: number; done?: number; total?: number; folder?: LibraryFolder };
      if (e.type === 'libraryimport.folder') {
        if (d?.scanId !== scanId || !d.folder) return;
        const ev = { done: d.done ?? 0, total: d.total ?? 0, folder: d.folder };
        setArrived((a) => [...a.slice(-200), ev.folder.folder]);
        qc.setQueryData<LibraryScan>(v11Keys.scan(scanId), (prev) => (prev ? withFolder(prev, ev) : prev));
        return;
      }
      if ((d as LibraryScan | undefined)?.id !== scanId) return;
      const s = d as LibraryScan;
      qc.setQueryData<LibraryScan>(v11Keys.scan(scanId), (prev) =>
        prev ? { ...prev, ...s, folders: prev.folders, counts: prev.folders ? prev.counts : s.counts } : prev,
      );
      if (e.type === 'libraryimport.finished') void qc.invalidateQueries({ queryKey: v11Keys.scan(scanId) });
    });
  }, [scanId, qc]);

  const s = scan.data;
  const root = roots.data?.find((r) => r.id === s?.rootFolderId);
  const folders = useMemo(() => (s?.folders ?? []).filter((f) => !imported.has(f.folder)), [s?.folders, imported]);
  const choices = useChoices(folders);

  const step: Step = !scanId
    ? 'folder'
    : view === 'done' && result
      ? 'done'
      : view === 'review' || (s && !isRunning(s))
        ? 'review'
        : 'scan';
  const idx = STEPS.findIndex(([k]) => k === step);
  const sel = selectionSummary(folders, choices.choices);
  const sub = (k: Step, i: number): string => {
    if (i > idx) return ['', 'Streams as it reads', 'Check every match', 'Summary'][i]!;
    if (k === 'folder') return s ? scanPath(root?.path, s.path) : 'Pick a root folder';
    if (k === 'scan')
      return s
        ? isRunning(s)
          ? `${formatNumber(s.done)} of ${s.total ? formatNumber(s.total) : '…'} folders`
          : `${formatNumber(s.folders?.length ?? s.done)} folders`
        : '';
    if (k === 'review')
      return step === 'done' && result
        ? `${formatNumber(result.summary.imported)} imported`
        : `${formatNumber(sel.count)} selected`;
    return result ? `${formatNumber(result.summary.imported)} imported` : 'Summary';
  };

  return (
    <div className="imp-page">
      <ol className="stepper" aria-label="Import steps">
        {STEPS.map(([k, label], i) => {
          const done = i < idx || step === 'done';
          return (
            <li
              key={k}
              className={done ? 'done' : undefined}
              aria-current={i === idx && step !== 'done' ? 'step' : undefined}
            >
              <span className="n" aria-hidden="true">
                {done ? <Check className="i" /> : i + 1}
              </span>
              <span>
                <b>
                  {label}
                  {done && <span className="sr-only"> (done)</span>}
                </b>
                <small>{sub(k, i)}</small>
              </span>
            </li>
          );
        })}
      </ol>

      {scanId && scan.isError ? (
        <Notice
          tone="failed"
          role="alert"
          title="This scan could not be loaded."
          action={
            <Button size="sm" onClick={() => open(undefined)}>
              Import a folder
            </Button>
          }
        >
          {(scan.error as Error).message} It may be older than the last 20 scans Gonema keeps.
        </Notice>
      ) : step === 'folder' ? (
        <FolderStep
          recent={(scans.data ?? []).filter((x) => !isRunning(x)).slice(0, 5)}
          onStarted={(sc) => {
            setStartedAt(Date.now());
            qc.setQueryData(v11Keys.scan(sc.id), { ...sc, folders: [] });
            void qc.invalidateQueries({ queryKey: v11Keys.scans });
            open(sc.id);
          }}
          onOpen={(id) => open(id)}
        />
      ) : !s ? (
        <Skeleton className="imp-sk" />
      ) : step === 'scan' ? (
        <ScanStep
          scan={s}
          path={scanPath(root?.path, s.path)}
          arrived={arrived}
          startedAt={startedAt}
          onReview={() => setView('review')}
        />
      ) : step === 'review' ? (
        <ReviewStep
          scan={s}
          folders={folders}
          rootPath={root?.path ?? ''}
          choices={choices}
          onBack={() => open(undefined)}
          onScanAgain={(sc) => {
            qc.setQueryData(v11Keys.scan(sc.id), { ...sc, folders: [] });
            setStartedAt(Date.now());
            open(sc.id);
          }}
          onImported={(r, done, ms) => {
            setResult(r);
            setTook(ms);
            setImported((prev) => new Set([...prev, ...done]));
            setView('done');
          }}
        />
      ) : result ? (
        <DoneStep
          result={result}
          took={took}
          leftCounts={folders}
          rootFolderId={s.rootFolderId}
          onReviewLeft={() => setView('review')}
          onAnother={() => open(undefined)}
          onScanFolder={(sc) => {
            qc.setQueryData(v11Keys.scan(sc.id), { ...sc, folders: [] });
            setStartedAt(Date.now());
            open(sc.id);
          }}
        />
      ) : null}
    </div>
  );
}
