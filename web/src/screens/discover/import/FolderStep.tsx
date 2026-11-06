import { useQuery } from '@tanstack/react-query';
import { Link } from '@tanstack/react-router';
import {
  ChevronRight,
  CornerLeftUp,
  Film,
  Folder,
  FolderOpen,
  ListChecks,
  Plus,
  ScanSearch,
  ShieldCheck,
} from 'lucide-react';
import { useEffect, useMemo, useState } from 'react';
import { ApiError } from '@/api/client';
import { useLibrary, useRootFolders } from '@/api/queries';
import { foldersQuery, type LibraryScan, startScan } from '@/api/v11';
import { Button, buttonClass } from '@/components/ui/Button';
import { Notice, Skeleton } from '@/components/ui/Feedback';
import { formatBytes, formatNumber, formatWhen } from '@/lib/format';
import { scanPath } from './ImportTab';

/** Sub-path typed by the user: relative, no "." / ".." / hidden parts (the server checks it too). */
export function cleanSubPath(v: string): string | null {
  const p = v
    .trim()
    .replace(/\\/g, '/')
    .replace(/^\/+|\/+$/g, '');
  if (!p) return '';
  const parts = p.split('/');
  if (parts.some((x) => !x || x === '.' || x === '..' || x.startsWith('.'))) return null;
  return parts.join('/');
}

const STATUS_WORD: Record<LibraryScan['status'], string> = {
  pending: 'Queued',
  running: 'Reading',
  done: 'Ready to review',
  cancelled: 'Stopped',
  failed: 'Failed',
};

export function FolderStep({
  recent,
  onStarted,
  onOpen,
}: {
  recent: LibraryScan[];
  onStarted: (s: LibraryScan) => void;
  onOpen: (id: number) => void;
}) {
  const roots = useRootFolders();
  const lib = useLibrary();
  const [rootId, setRootId] = useState(0);
  const [sub, setSub] = useState('');
  const [busy, setBusy] = useState(false);
  const [error, setError] = useState<string | null>(null);
  useEffect(() => {
    if (!rootId && roots.data?.[0]) setRootId(roots.data[0].id);
  }, [roots.data, rootId]);
  const root = roots.data?.find((r) => r.id === rootId);
  const counts = useMemo(() => {
    const c = new Map<number, number>();
    for (const m of lib.data ?? []) c.set(m.rootFolderId, (c.get(m.rootFolderId) ?? 0) + 1);
    return c;
  }, [lib.data]);
  const clean = cleanSubPath(sub);
  // the picker lists the folder typed so far (or its parent while it is being typed)
  const [listed, setListed] = useState('');
  useEffect(() => {
    const t = setTimeout(() => clean !== null && setListed(clean), 300);
    return () => clearTimeout(t);
  }, [clean]);
  const folders = useQuery(foldersQuery(rootId, listed));
  const target = scanPath(root?.path, clean ?? '');

  const start = async () => {
    if (!root || clean === null) return;
    setBusy(true);
    setError(null);
    try {
      onStarted(await startScan(root.id, clean));
    } catch (e) {
      setError(e instanceof ApiError ? e.detail : 'Gonema could not be reached.');
    } finally {
      setBusy(false);
    }
  };

  return (
    <div className="pick">
      <section className="panel" aria-labelledby="f-h">
        <div className="panel-hd">
          <h2 id="f-h">Where are your films?</h2>
        </div>
        <div className="panel-bd">
          {roots.isPending ? (
            <Skeleton className="sk-line" />
          ) : !roots.data?.length ? (
            <Notice
              title="No root folder yet."
              action={
                <Link
                  className={buttonClass('secondary', 'sm')}
                  to="/settings/$section/$anchor"
                  params={{ section: 'library', anchor: 'root-folders' }}
                >
                  Add a root folder
                </Link>
              }
            >
              Gonema imports from the folders it manages. Add the one that holds your films first.
            </Notice>
          ) : (
            <fieldset className="roots-set">
              <legend className="field-label">Root folder</legend>
              <div className="roots">
                {roots.data.map((r) => {
                  const n = counts.get(r.id) ?? 0;
                  const used = r.totalSpace ? 1 - (r.freeSpace ?? 0) / r.totalSpace : 0;
                  return (
                    <label key={r.id}>
                      <input
                        type="radio"
                        name="root"
                        checked={r.id === rootId}
                        onChange={() => {
                          setRootId(r.id);
                          setSub('');
                        }}
                      />
                      <b>
                        <span className="truncate">{r.path}</span>
                        <span className="radio" aria-hidden="true" />
                      </b>
                      {r.totalSpace ? (
                        <div className="progress" style={{ ['--p' as string]: used }} aria-hidden="true">
                          <div className="fill" />
                        </div>
                      ) : null}
                      <span>
                        {n ? `${formatNumber(n)} ${n === 1 ? 'film' : 'films'}` : 'No films yet'}
                        {r.freeSpace ? ` · ${formatBytes(r.freeSpace)} free` : ''}
                        {r.accessible === false ? ' · not reachable' : ''}
                      </span>
                    </label>
                  );
                })}
              </div>
              <div>
                <Link
                  className={buttonClass('ghost', 'sm')}
                  to="/settings/$section/$anchor"
                  params={{ section: 'library', anchor: 'root-folders' }}
                >
                  <Plus className="i" aria-hidden="true" />
                  Add a root folder
                </Link>
              </div>
            </fieldset>
          )}

          {root && (
            <div className="field">
              <label className="field-label" htmlFor="inside">
                Only look inside <span className="t-muted fw-400">optional</span>
              </label>
              <span className="input mono inside" aria-invalid={clean === null || undefined}>
                <FolderOpen className="i" aria-hidden="true" />
                <span className="pre">{root.path.replace(/\/+$/, '')}/</span>
                <input
                  id="inside"
                  placeholder="the whole folder"
                  aria-describedby="inside-h"
                  aria-invalid={clean === null || undefined}
                  value={sub}
                  maxLength={1024}
                  spellCheck={false}
                  autoComplete="off"
                  onChange={(ev) => setSub(ev.target.value)}
                />
              </span>
              {clean === null ? (
                <span className="field-error" id="inside-h">
                  Use a folder inside the root folder, like <span className="t-mono-sm">Films A–M</span>: no “..” and no
                  hidden folders.
                </span>
              ) : (
                <span className="field-hint" id="inside-h">
                  Pick a folder below, or type a path. Gonema only reads inside the root folder.
                </span>
              )}
              <ul
                className="tree"
                aria-label={`Folders in ${scanPath(root.path, listed)}`}
                aria-busy={folders.isFetching || undefined}
              >
                {listed && (
                  <li>
                    <button type="button" onClick={() => setSub(listed.split('/').slice(0, -1).join('/'))}>
                      <CornerLeftUp className="i" aria-hidden="true" />
                      <span>Up one level</span>
                      <span className="c" />
                      <span />
                    </button>
                  </li>
                )}
                {folders.isPending ? (
                  <li className="tree-note">
                    <Skeleton className="sk-line" />
                  </li>
                ) : folders.isError ? (
                  <li className="tree-note">{(folders.error as Error).message}</li>
                ) : !folders.data?.length ? (
                  <li className="tree-note">No folder in here.</li>
                ) : (
                  folders.data.map((f) => (
                    <li key={f.path}>
                      <button type="button" aria-pressed={clean === f.path} onClick={() => setSub(f.path)}>
                        {f.movie ? (
                          <Film className="i" aria-hidden="true" />
                        ) : (
                          <Folder className="i" aria-hidden="true" />
                        )}
                        <span className="truncate">{f.name}</span>
                        <span className="c">{f.movie ? 'Film folder' : ''}</span>
                        <ChevronRight className="i" aria-hidden="true" />
                      </button>
                    </li>
                  ))
                )}
              </ul>
            </div>
          )}

          {error && (
            <Notice tone="failed" role="alert" title="The scan did not start.">
              {error}
            </Notice>
          )}
          <div className="foot">
            <span className="t-caption">About a minute for 100 folders. Nothing is changed while scanning.</span>
            <Button
              variant="primary"
              size="lg"
              icon={<ScanSearch className="i" aria-hidden="true" />}
              loading={busy}
              disabled={!root || clean === null || busy}
              onClick={() => void start()}
            >
              <span className="lbl" title={target}>
                Scan {target}
              </span>
            </Button>
          </div>
        </div>
      </section>
      <aside className="side-col">
        <section className="panel promise" aria-labelledby="how-h">
          <div className="panel-hd">
            <h2 id="how-h">How it works</h2>
          </div>
          <div className="panel-bd">
            <ol>
              <li>
                <span className="ic" aria-hidden="true">
                  <ScanSearch className="i" />
                </span>
                <span>
                  <b>Gonema reads the folder</b>Folder and file names, plus each video’s resolution and size. One film
                  per folder.
                </span>
              </li>
              <li>
                <span className="ic" aria-hidden="true">
                  <ListChecks className="i" />
                </span>
                <span>
                  <b>You check every match</b>Each folder gets a TMDB match and a confidence. Nothing is added until you
                  press Import.
                </span>
              </li>
              <li>
                <span className="ic" aria-hidden="true">
                  <Film className="i" />
                </span>
                <span>
                  <b>Films join your library</b>Pointed at their files, with the profile you choose. Gonema upgrades
                  them later if you ask.
                </span>
              </li>
            </ol>
            <div className="safe">
              <ShieldCheck className="i" aria-hidden="true" />
              <span>
                <b>Nothing is deleted.</b> Files stay where they are, unless you choose to move a film into Gonema’s
                naming. Even then, nothing is removed.
              </span>
            </div>
          </div>
        </section>
        {recent.length > 0 && (
          <section className="panel recent-scans" aria-labelledby="recent-h">
            <div className="panel-hd">
              <h2 id="recent-h">Recent scans</h2>
            </div>
            <ul className="panel-bd">
              {recent.map((s) => {
                const r = roots.data?.find((x) => x.id === s.rootFolderId);
                const n = s.counts.new + s.counts.ambiguous + s.counts.noMatch + s.counts.inLibrary;
                return (
                  <li key={s.id}>
                    <button type="button" onClick={() => onOpen(s.id)}>
                      <span className="t-mono-sm truncate">{scanPath(r?.path, s.path)}</span>
                      <small>
                        {STATUS_WORD[s.status]} · {formatNumber(n)} {n === 1 ? 'folder' : 'folders'} ·{' '}
                        {formatWhen(s.updatedAt)}
                      </small>
                    </button>
                  </li>
                );
              })}
            </ul>
          </section>
        )}
      </aside>
    </div>
  );
}
