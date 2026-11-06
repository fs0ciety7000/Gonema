import '@/styles/lot2.css';
import { useQuery, useQueryClient } from '@tanstack/react-query';
import { ArrowRight, FileX2, FolderInput, Pencil, Tag as TagIcon, Trash2, TriangleAlert } from 'lucide-react';
import { type ReactNode, useEffect, useId, useMemo, useState } from 'react';
import { ApiError } from '@/api/client';
import type { LibraryItem } from '@/api/model';
import { keys, useRootFolders } from '@/api/queries';
import type { MoveOutcome, Tag } from '@/api/schema';
import {
  bulkUpdate,
  createTag,
  deleteMovieFile,
  deleteTag,
  importSettingsQuery,
  planMove,
  planRename,
  removeMovieWithFiles,
  renameTag,
  restoreRecycled,
  restoreSettings,
  tagsQuery,
} from '@/api/screens';
import { Button, IconButton } from '@/components/ui/Button';
import { Checkbox } from '@/components/ui/Checkbox';
import { cx } from '@/components/ui/cx';
import { Dialog } from '@/components/ui/Dialog';
import { Notice, Skeleton } from '@/components/ui/Feedback';
import { Input } from '@/components/ui/Field';
import { Segmented } from '@/components/ui/Segmented';
import { Select } from '@/components/ui/Select';
import { Switch } from '@/components/ui/Switch';
import { normaliseTag } from '@/components/ui/TagsInput';
import { toast } from '@/components/ui/Toast';
import { TmdbKeyDialog } from '@/screens/discover/TmdbKeyDialog';
import { closeMovieDialog, type DialogTarget, type MovieDialog, useMovieDialog } from '../dialogStore';
import { useMovieActions } from '../useMovieActions';

const plural = (n: number, one: string, many = `${one}s`) => `${n.toLocaleString('en-US')} ${n === 1 ? one : many}`;
const errorText = (e: unknown) =>
  e instanceof ApiError ? (e.isNetwork ? 'Gonema could not be reached; nothing was changed.' : e.detail) : '';
const nameOf = (t: readonly DialogTarget[]) => (t.length === 1 ? t[0]!.title : plural(t.length, 'film'));

/** Every library dialog, mounted once (lazy chunk) and driven by dialogStore. */
export default function MovieDialogs() {
  const d = useMovieDialog();
  // Keep the last dialog rendered while it animates out.
  const [last, setLast] = useState<MovieDialog | null>(d);
  useEffect(() => {
    if (d) setLast(d);
  }, [d]);
  const shown = d ?? last;
  if (!shown) return null;
  const open = !!d;
  const close = (o: boolean) => {
    if (!o) closeMovieDialog();
  };
  const key =
    shown.kind +
    ('targets' in shown ? shown.targets.map((t) => t.id).join(',') : 'target' in shown ? shown.target.id : '');
  switch (shown.kind) {
    case 'tags':
      return <TagsDialog key={key} open={open} onOpenChange={close} d={shown} />;
    case 'move':
      return <MoveDialog key={key} open={open} onOpenChange={close} d={shown} />;
    case 'rename':
      return <RenameDialog key={key} open={open} onOpenChange={close} targets={shown.targets} />;
    case 'deleteFile':
      return <DeleteFileDialog key={key} open={open} onOpenChange={close} target={shown.target} />;
    case 'remove':
      return <RemoveDialog key={key} open={open} onOpenChange={close} d={shown} />;
    case 'tmdbKey':
      return <TmdbKeyDialog open={open} onOpenChange={close} onSaved={shown.onDone} />;
  }
}

type Props<T> = { open: boolean; onOpenChange: (o: boolean) => void; d: T };

// ----------------------------------------------------------------- tags

type TriState = 'all' | 'some' | 'none';

export function tagStates(
  targets: readonly number[],
  library: readonly Pick<LibraryItem, 'id' | 'tagIds'>[] | undefined,
  tags: readonly Tag[],
): Map<number, TriState> {
  const byId = new Map((library ?? []).map((m) => [m.id, new Set(m.tagIds)]));
  const out = new Map<number, TriState>();
  for (const t of tags) {
    let n = 0;
    for (const id of targets) if (byId.get(id)?.has(t.id)) n++;
    out.set(t.id, n === 0 ? 'none' : n === targets.length ? 'all' : 'some');
  }
  return out;
}

function TagsDialog({ open, onOpenChange, d }: Props<Extract<MovieDialog, { kind: 'tags' }>>) {
  const qc = useQueryClient();
  const tags = useQuery(tagsQuery);
  const lib = qc.getQueryData<LibraryItem[]>(keys.library);
  const ids = useMemo(() => d.targets.map((t) => t.id), [d.targets]);
  const initial = useMemo(() => tagStates(ids, lib, tags.data ?? []), [ids, lib, tags.data]);
  const [wanted, setWanted] = useState<Map<number, TriState>>(new Map());
  const [newTags, setNewTags] = useState<string[]>([]);
  const [draft, setDraft] = useState('');
  const [busy, setBusy] = useState(false);
  const [tab, setTab] = useState<'assign' | 'manage'>('assign');
  const inputId = useId();

  const state = (id: number) => wanted.get(id) ?? initial.get(id) ?? 'none';
  const changed = [...wanted.entries()].filter(([id, s]) => s !== initial.get(id));
  const addLabels = [
    ...changed.filter(([, s]) => s === 'all').map(([id]) => tags.data?.find((t) => t.id === id)?.label ?? ''),
    ...newTags,
  ].filter(Boolean);
  const removeLabels = changed
    .filter(([, s]) => s === 'none')
    .map(([id]) => tags.data?.find((t) => t.id === id)?.label ?? '')
    .filter(Boolean);

  const addDraft = () => {
    const t = normaliseTag(draft);
    setDraft('');
    if (!t) return;
    const existing = tags.data?.find((x) => x.label === t);
    if (existing) setWanted((w) => new Map(w).set(existing.id, 'all'));
    else if (!newTags.includes(t)) setNewTags((n) => [...n, t]);
  };

  const apply = async () => {
    if (!addLabels.length && !removeLabels.length) {
      onOpenChange(false);
      return;
    }
    setBusy(true);
    try {
      const r = await bulkUpdate(ids, { addTags: addLabels, removeTags: removeLabels });
      void qc.invalidateQueries({ queryKey: keys.library });
      void qc.invalidateQueries({ queryKey: ['tags'] });
      for (const id of ids) void qc.invalidateQueries({ queryKey: keys.movie(id) });
      onOpenChange(false);
      d.onDone?.();
      const previous = r.previous ?? [];
      toast.show({
        title: `Tags updated on ${nameOf(d.targets)}`,
        body: [
          addLabels.length ? `Added ${addLabels.join(', ')}` : '',
          removeLabels.length ? `removed ${removeLabels.join(', ')}` : '',
        ]
          .filter(Boolean)
          .join('; '),
        icon: <TagIcon className="i" />,
        undo: previous.length
          ? () =>
              void restoreSettings(previous)
                .then(() => {
                  void qc.invalidateQueries({ queryKey: keys.library });
                  void qc.invalidateQueries({ queryKey: ['tags'] });
                })
                .catch((e) => toast.error('The tags were not restored', errorText(e)))
          : undefined,
      });
    } catch (e) {
      toast.error('No tag was changed', errorText(e));
    } finally {
      setBusy(false);
    }
  };

  return (
    <Dialog
      open={open}
      onOpenChange={onOpenChange}
      wide
      icon={<TagIcon className="i" aria-hidden="true" />}
      title={`Tags · ${nameOf(d.targets)}`}
      description="Tick a tag to add it to every selected film, clear it to remove it. A dash means some films have it."
      actions={
        tab === 'assign' ? (
          <>
            <Button variant="ghost" onClick={() => onOpenChange(false)}>
              Cancel
            </Button>
            <Button variant="primary" loading={busy} onClick={() => void apply()}>
              {addLabels.length || removeLabels.length
                ? `Apply ${plural(addLabels.length + removeLabels.length, 'change')}`
                : 'Done'}
            </Button>
          </>
        ) : (
          <Button variant="secondary" onClick={() => setTab('assign')}>
            Back to the films
          </Button>
        )
      }
    >
      <Segmented
        label="Tags view"
        value={tab}
        onChange={setTab}
        options={[
          { value: 'assign', label: 'Assign' },
          { value: 'manage', label: 'Manage all tags' },
        ]}
      />
      {tags.isPending ? (
        <div className="tag-list" aria-hidden="true">
          <Skeleton className="sk-line" />
          <Skeleton className="sk-line" style={{ width: '60%', marginTop: 10 }} />
        </div>
      ) : tags.isError ? (
        <Notice tone="failed" title="Tags could not be loaded.">
          {tags.error.message}
        </Notice>
      ) : tab === 'assign' ? (
        <>
          <ul className="tag-list">
            {(tags.data ?? []).map((t) => {
              const s = state(t.id);
              return (
                <li key={t.id}>
                  <Checkbox
                    checked={s === 'all'}
                    indeterminate={s === 'some'}
                    onCheckedChange={(v) => setWanted((w) => new Map(w).set(t.id, v ? 'all' : 'none'))}
                    label={
                      <span className="tag-row">
                        <span className="tag">{t.label}</span>
                        <span className="t-caption t-muted">{plural(t.movies, 'film')}</span>
                      </span>
                    }
                  />
                </li>
              );
            })}
            {newTags.map((t) => (
              <li key={`new-${t}`}>
                <Checkbox
                  checked
                  onCheckedChange={() => setNewTags((n) => n.filter((x) => x !== t))}
                  label={
                    <span className="tag-row">
                      <span className="tag">{t}</span>
                      <span className="t-caption t-muted">new</span>
                    </span>
                  }
                />
              </li>
            ))}
            {!tags.data?.length && !newTags.length && (
              <li className="t-secondary">No tags yet. Create the first one below.</li>
            )}
          </ul>
          <form
            className="tag-new"
            onSubmit={(ev) => {
              ev.preventDefault();
              addDraft();
            }}
          >
            <label className="sr-only" htmlFor={inputId}>
              New tag
            </label>
            <Input id={inputId} placeholder="New tag…" value={draft} onChange={(ev) => setDraft(ev.target.value)} />
            <Button type="submit" size="md" disabled={!normaliseTag(draft)}>
              Add tag
            </Button>
          </form>
        </>
      ) : (
        <ManageTags tags={tags.data ?? []} />
      )}
    </Dialog>
  );
}

function ManageTags({ tags }: { tags: readonly Tag[] }) {
  const qc = useQueryClient();
  const [editing, setEditing] = useState<number | null>(null);
  const [label, setLabel] = useState('');
  const refresh = () => {
    void qc.invalidateQueries({ queryKey: ['tags'] });
    void qc.invalidateQueries({ queryKey: keys.library });
  };
  const save = async (t: Tag) => {
    const l = normaliseTag(label);
    setEditing(null);
    if (!l || l === t.label) return;
    try {
      await renameTag(t.id, l);
      refresh();
      toast.show({
        title: `Renamed ${t.label} to ${l}`,
        icon: <Pencil className="i" />,
        undo: () => void renameTag(t.id, t.label).then(refresh),
      });
    } catch (e) {
      toast.error('The tag was not renamed', errorText(e));
    }
  };
  const remove = async (t: Tag) => {
    try {
      const r = await deleteTag(t.id);
      refresh();
      const movies = r.movieIds ?? [];
      toast.show({
        title: `Deleted the tag ${t.label}`,
        body: movies.length ? `Removed from ${plural(movies.length, 'film')}.` : 'No film had it.',
        icon: <Trash2 className="i" />,
        undo: () =>
          void (async () => {
            try {
              if (movies.length) await bulkUpdate(movies, { addTags: [t.label] });
              else await createTag(t.label);
              refresh();
            } catch (e) {
              toast.error('The tag was not restored', errorText(e));
            }
          })(),
      });
    } catch (e) {
      toast.error('The tag was not deleted', errorText(e));
    }
  };
  if (!tags.length) return <p className="t-secondary tag-list">No tags yet.</p>;
  return (
    <ul className="tag-list manage">
      {tags.map((t) => (
        <li key={t.id}>
          {editing === t.id ? (
            <form
              className="tag-edit"
              onSubmit={(ev) => {
                ev.preventDefault();
                void save(t);
              }}
            >
              <Input
                aria-label={`New name for ${t.label}`}
                value={label}
                autoFocus
                onChange={(ev) => setLabel(ev.target.value)}
                onKeyDown={(ev) => {
                  if (ev.key === 'Escape') {
                    ev.stopPropagation();
                    setEditing(null);
                  }
                }}
              />
              <Button type="submit" size="sm">
                Save
              </Button>
            </form>
          ) : (
            <span className="tag-row">
              <span className="tag">{t.label}</span>
              <span className="t-caption t-muted">{plural(t.movies, 'film')}</span>
            </span>
          )}
          {editing !== t.id && (
            <span className="tag-acts">
              <IconButton
                size="sm"
                label={`Rename ${t.label}`}
                onClick={() => {
                  setEditing(t.id);
                  setLabel(t.label);
                }}
              >
                <Pencil className="i i-sm" aria-hidden="true" />
              </IconButton>
              <IconButton size="sm" label={`Delete the tag ${t.label}`} onClick={() => void remove(t)}>
                <Trash2 className="i i-sm" aria-hidden="true" />
              </IconButton>
            </span>
          )}
        </li>
      ))}
    </ul>
  );
}

// ----------------------------------------------------------------- move and rename plans

function OutcomeList({
  outcomes,
  titleOf,
  empty,
}: {
  outcomes: readonly MoveOutcome[];
  titleOf: (id: number) => string;
  empty: string;
}) {
  const changes = outcomes.filter((o) => !o.ok || !o.relocation?.noChange);
  if (!changes.length) return <p className="t-secondary plan-empty">{empty}</p>;
  return (
    <ul className="plan">
      {changes.map((o) => {
        const r = o.relocation;
        const problems = [...(o.error ? [o.error] : []), ...(r?.problems ?? [])];
        const ops = r?.ops ?? [];
        return (
          <li key={o.movieId} className={cx(problems.length > 0 && 'has-problem')}>
            <b>{titleOf(o.movieId)}</b>
            {r && (
              <div className="plan-paths">
                <span className="from" title={r.kind === 'move' ? r.fromPath : (r.fileFrom ?? r.fromPath)}>
                  {r.kind === 'move' ? r.fromPath : (r.fileFrom ?? r.fromPath)}
                </span>
                <ArrowRight className="i i-sm" aria-hidden="true" />
                <span className="sr-only">becomes</span>
                <span className="to" title={r.kind === 'move' ? r.toPath : (r.fileTo ?? r.toPath)}>
                  {r.kind === 'move' ? r.toPath : (r.fileTo ?? r.toPath)}
                </span>
              </div>
            )}
            {ops.length > 1 && (
              <p className="t-caption t-muted">{plural(ops.length, 'file')} in the folder move with it.</p>
            )}
            {problems.map((p) => (
              <p className="plan-problem" key={p}>
                <TriangleAlert className="i i-sm" aria-hidden="true" />
                {p}
              </p>
            ))}
            {(r?.notes ?? []).map((n) => (
              <p className="t-caption t-muted" key={n}>
                {n}
              </p>
            ))}
          </li>
        );
      })}
    </ul>
  );
}

function useTitles(targets: readonly DialogTarget[]) {
  return useMemo(() => {
    const m = new Map(targets.map((t) => [t.id, t.title]));
    return (id: number) => m.get(id) ?? `Film #${id}`;
  }, [targets]);
}

function MoveDialog({ open, onOpenChange, d }: Props<Extract<MovieDialog, { kind: 'move' }>>) {
  const qc = useQueryClient();
  const roots = useRootFolders();
  const ids = useMemo(() => d.targets.map((t) => t.id), [d.targets]);
  const lib = qc.getQueryData<LibraryItem[]>(keys.library);
  const current = lib?.find((m) => m.id === ids[0])?.rootFolderId;
  const [root, setRoot] = useState<number | null>(null);
  const [moveFiles, setMoveFiles] = useState(true);
  const [busy, setBusy] = useState(false);
  const titleOf = useTitles(d.targets);
  const rootId = root ?? roots.data?.find((r) => r.id !== current)?.id ?? roots.data?.[0]?.id ?? null;
  const preview = useQuery({
    queryKey: ['move-plan', ids.join(','), rootId, moveFiles],
    enabled: open && !!rootId,
    queryFn: () => planMove(ids, rootId!, moveFiles, true),
    staleTime: 0,
    gcTime: 0,
    retry: false,
  });
  const outcomes = preview.data?.results ?? [];
  const blocked = outcomes.filter((o) => !o.ok || (o.relocation?.problems ?? []).length > 0).length;
  const rootPath = roots.data?.find((r) => r.id === rootId)?.path ?? '';

  const apply = async () => {
    if (!rootId) return;
    setBusy(true);
    try {
      const r = await planMove(ids, rootId, moveFiles, false);
      const failed = (r.results ?? []).filter((o) => !o.ok);
      void qc.invalidateQueries({ queryKey: keys.library });
      for (const id of ids) void qc.invalidateQueries({ queryKey: keys.movie(id) });
      onOpenChange(false);
      d.onDone?.();
      if (failed.length)
        toast.error(
          `${plural(failed.length, 'film')} could not be moved`,
          `${failed[0]?.error ?? ''}${failed.length > 1 ? ` (and ${failed.length - 1} more)` : ''}`,
        );
      const ok = ids.length - failed.length;
      if (ok)
        toast.show({
          title: `Moved ${ok === ids.length ? nameOf(d.targets) : plural(ok, 'film')} to ${rootPath}`,
          body: moveFiles
            ? 'Files and folders moved; History has each operation.'
            : 'Only the library entry changed; files stayed where they were.',
          icon: <FolderInput className="i" />,
        });
    } catch (e) {
      toast.error('Nothing was moved', errorText(e));
    } finally {
      setBusy(false);
    }
  };

  return (
    <Dialog
      open={open}
      onOpenChange={onOpenChange}
      wide
      icon={<FolderInput className="i" aria-hidden="true" />}
      title={`Move ${nameOf(d.targets)}`}
      description="Choose the new root folder. Gonema shows every operation before doing anything."
      actions={
        <>
          <Button variant="ghost" onClick={() => onOpenChange(false)}>
            Cancel
          </Button>
          <Button
            variant="primary"
            loading={busy}
            disabled={!rootId || preview.isPending || preview.isError || blocked === outcomes.length}
            onClick={() => void apply()}
          >
            {moveFiles ? 'Move' : 'Change folder'}
            {blocked > 0 && blocked < outcomes.length ? ` ${plural(outcomes.length - blocked, 'film')}` : ''}
          </Button>
        </>
      }
    >
      <div className="move-opts">
        <div className="field">
          <span className="field-label" id="mv-root">
            Root folder
          </span>
          <Select
            value={rootId}
            labelledBy="mv-root"
            mono
            options={(roots.data ?? []).map((r) => ({
              value: r.id,
              label: r.path,
              hint: r.id === current ? 'current' : undefined,
            }))}
            onValueChange={(v) => setRoot(v)}
          />
        </div>
        <Switch checked={moveFiles} onCheckedChange={setMoveFiles}>
          Move the files too
        </Switch>
      </div>
      <p className="field-hint">
        {moveFiles
          ? 'Each film folder moves into the new root folder, renamed with your folder template.'
          : 'Only the library changes: the files stay where they are until you move them yourself.'}
      </p>
      <h3 className="plan-h">Planned operations</h3>
      {preview.isPending ? (
        <div className="plan" aria-hidden="true">
          <Skeleton className="sk-line" />
          <Skeleton className="sk-line" style={{ width: '70%', marginTop: 10 }} />
        </div>
      ) : preview.isError ? (
        <Notice tone="failed" title="The move could not be planned.">
          {errorText(preview.error)}
        </Notice>
      ) : (
        <OutcomeList outcomes={outcomes} titleOf={titleOf} empty="Nothing to move: already in that folder." />
      )}
    </Dialog>
  );
}

function RenameDialog({
  open,
  onOpenChange,
  targets,
}: {
  open: boolean;
  onOpenChange: (o: boolean) => void;
  targets: DialogTarget[];
}) {
  const qc = useQueryClient();
  const ids = useMemo(() => targets.map((t) => t.id), [targets]);
  const titleOf = useTitles(targets);
  const [busy, setBusy] = useState(false);
  const preview = useQuery({
    queryKey: ['rename-plan', ids.join(',')],
    enabled: open,
    queryFn: () => planRename(ids, true),
    staleTime: 0,
    gcTime: 0,
    retry: false,
  });
  const outcomes = preview.data?.results ?? [];
  const todo = outcomes.filter((o) => o.ok && !o.relocation?.noChange && !(o.relocation?.problems ?? []).length);
  const apply = async () => {
    setBusy(true);
    try {
      const r = await planRename(
        todo.map((o) => o.movieId),
        false,
      );
      const failed = (r.results ?? []).filter((o) => !o.ok);
      void qc.invalidateQueries({ queryKey: keys.library });
      for (const id of ids) void qc.invalidateQueries({ queryKey: keys.movie(id) });
      onOpenChange(false);
      if (failed.length) toast.error(`${plural(failed.length, 'film')} could not be renamed`, failed[0]?.error);
      const ok = todo.length - failed.length;
      if (ok)
        toast.show({
          title: `Renamed ${plural(ok, 'film')}`,
          body: 'Names follow your templates; History has the old names.',
          icon: <Pencil className="i" />,
        });
    } catch (e) {
      toast.error('Nothing was renamed', errorText(e));
    } finally {
      setBusy(false);
    }
  };
  return (
    <Dialog
      open={open}
      onOpenChange={onOpenChange}
      wide
      icon={<Pencil className="i" aria-hidden="true" />}
      title={`Rename ${nameOf(targets)}`}
      description="Files and folders are renamed to your naming templates. Current name on the left, new name on the right."
      actions={
        <>
          <Button variant="ghost" onClick={() => onOpenChange(false)}>
            Cancel
          </Button>
          <Button variant="primary" loading={busy} disabled={!todo.length} onClick={() => void apply()}>
            {todo.length ? `Rename ${plural(todo.length, 'film')}` : 'Nothing to rename'}
          </Button>
        </>
      }
    >
      {preview.isPending ? (
        <div className="plan" aria-hidden="true">
          <Skeleton className="sk-line" />
          <Skeleton className="sk-line" style={{ width: '70%', marginTop: 10 }} />
        </div>
      ) : preview.isError ? (
        <Notice tone="failed" title="The rename could not be planned.">
          {errorText(preview.error)}
        </Notice>
      ) : (
        <OutcomeList outcomes={outcomes} titleOf={titleOf} empty="Every name already follows your templates." />
      )}
    </Dialog>
  );
}

// ----------------------------------------------------------------- deletion (always confirmed)

function useRetentionDays() {
  const s = useQuery(importSettingsQuery);
  return (s.data as { recycleRetentionDays?: number } | null | undefined)?.recycleRetentionDays ?? 30;
}

function RecycleNote({ days, children }: { days: number; children?: ReactNode }) {
  return (
    <div className="recycle-note">
      <Trash2 className="i" aria-hidden="true" />
      <p>
        {children} It goes to the <b>recycle bin</b>, where it stays for {days} days and can be restored. Nothing is
        deleted for good now.
      </p>
    </div>
  );
}

function restoreAction(names: readonly string[], qc: ReturnType<typeof useQueryClient>, ids: readonly number[]) {
  if (!names.length) return undefined;
  return {
    label: 'Restore',
    onClick: () =>
      void Promise.all(names.map((n) => restoreRecycled(n)))
        .then(() => {
          toast.show({ title: 'Restored from the recycle bin', body: 'Refresh the film to import it again.' });
          void qc.invalidateQueries({ queryKey: keys.library });
          for (const id of ids) void qc.invalidateQueries({ queryKey: keys.movie(id) });
        })
        .catch((e) => toast.error('Nothing was restored', errorText(e))),
  };
}

function DeleteFileDialog({
  open,
  onOpenChange,
  target,
}: {
  open: boolean;
  onOpenChange: (o: boolean) => void;
  target: DialogTarget;
}) {
  const qc = useQueryClient();
  const days = useRetentionDays();
  const [busy, setBusy] = useState(false);
  const confirm = async () => {
    setBusy(true);
    try {
      const r = await deleteMovieFile(target.id);
      void qc.invalidateQueries({ queryKey: keys.library });
      void qc.invalidateQueries({ queryKey: keys.movie(target.id) });
      onOpenChange(false);
      toast.show({
        title: `${target.title}: file moved to the recycle bin`,
        body: `The film is missing again; kept ${days} days in ${r.bin || 'the recycle bin'}.`,
        icon: <FileX2 className="i" />,
        action: restoreAction(r.recycled ?? [], qc, [target.id]),
      });
    } catch (e) {
      toast.error('The file was not deleted', errorText(e));
    } finally {
      setBusy(false);
    }
  };
  return (
    <Dialog
      open={open}
      onOpenChange={onOpenChange}
      icon={<FileX2 className="i" aria-hidden="true" />}
      className="danger-dialog"
      title={`Delete the file of ${target.title}?`}
      actions={
        <>
          <Button variant="ghost" onClick={() => onOpenChange(false)} autoFocus>
            Cancel
          </Button>
          <Button variant="danger-solid" loading={busy} onClick={() => void confirm()}>
            Move file to recycle bin
          </Button>
        </>
      }
    >
      <RecycleNote days={days}>The film file and its subtitles leave the library folder.</RecycleNote>
      <p className="t-secondary">
        The film stays in the library as missing; if it is monitored, Gonema will look for a new copy.
      </p>
    </Dialog>
  );
}

function RemoveDialog({ open, onOpenChange, d }: Props<Extract<MovieDialog, { kind: 'remove' }>>) {
  const qc = useQueryClient();
  const actions = useMovieActions();
  const days = useRetentionDays();
  const [withFiles, setWithFiles] = useState(false);
  const [busy, setBusy] = useState(false);
  const confirm = async () => {
    if (!withFiles) {
      onOpenChange(false);
      actions.remove(d.targets, d.onDone);
      return;
    }
    setBusy(true);
    const rs = await Promise.allSettled(d.targets.map((t) => removeMovieWithFiles(t.id)));
    setBusy(false);
    const failed = rs.filter((r): r is PromiseRejectedResult => r.status === 'rejected');
    void qc.invalidateQueries({ queryKey: keys.library });
    onOpenChange(false);
    d.onDone?.();
    if (failed.length)
      toast.error(`${plural(failed.length, 'film')} could not be removed`, errorText(failed[0]?.reason));
    const ok = d.targets.length - failed.length;
    if (ok)
      toast.show({
        title: `Removed ${ok === d.targets.length ? nameOf(d.targets) : plural(ok, 'film')} and moved the files to the recycle bin`,
        body: `Files are kept ${days} days and can be restored from the recycle bin.`,
        icon: <Trash2 className="i" />,
      });
  };
  return (
    <Dialog
      open={open}
      onOpenChange={onOpenChange}
      icon={<Trash2 className="i" aria-hidden="true" />}
      className={withFiles ? 'danger-dialog' : undefined}
      title={`Remove ${nameOf(d.targets)} from the library?`}
      actions={
        <>
          <Button variant="ghost" onClick={() => onOpenChange(false)}>
            Cancel
          </Button>
          <Button variant={withFiles ? 'danger-solid' : 'danger'} loading={busy} onClick={() => void confirm()}>
            {withFiles ? 'Remove and move files to recycle bin' : 'Remove from library'}
          </Button>
        </>
      }
    >
      <Segmented
        label="Files"
        value={withFiles ? 'bin' : 'keep'}
        onChange={(v) => setWithFiles(v === 'bin')}
        options={[
          { value: 'keep', label: 'Keep the files' },
          { value: 'bin', label: 'Also move files to recycle bin' },
        ]}
      />
      {withFiles ? (
        <RecycleNote days={days}>The films leave the library and their folders leave the disk.</RecycleNote>
      ) : (
        <p className="t-secondary">Files stay on disk. You can undo for 8 seconds after removing.</p>
      )}
    </Dialog>
  );
}
