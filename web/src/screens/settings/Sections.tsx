import { useMutation, useQuery, useQueryClient } from '@tanstack/react-query';
import { CircleCheck, FolderPlus, KeyRound, Lock, Tag as TagIcon } from 'lucide-react';
import { type FormEvent, type ReactNode, useEffect, useMemo, useState } from 'react';
import {
  acquisitionQuery,
  addRootFolder,
  healthSettingsQuery,
  importSettingsQuery,
  lot3Keys,
  type NamingInput,
  namingQuery,
  pathMappingsQuery,
  previewNaming,
  putAcquisition,
  putHealthSettings,
  putImportSettings,
  putNaming,
  removeRootFolder,
} from '@/api/lot3';
import { keys, rootFoldersQuery, useLibrary } from '@/api/queries';
import type { AcquisitionSettings, ImportSettingsBody, NamingPreview, Settings as HealthSettings } from '@/api/schema';
import { deleteTag, renameTag, screenKeys, setTmdbKey, tagsQuery, tmdbQuery } from '@/api/screens';
import { StatusText } from '@/components/ui/Badges';
import { Button } from '@/components/ui/Button';
import { EmptyState, Skeleton } from '@/components/ui/Feedback';
import { Field, Input } from '@/components/ui/Field';
import { Switch } from '@/components/ui/Switch';
import { toast } from '@/components/ui/Toast';
import { formatBytes, formatNumber } from '@/lib/format';
import { errorText, FormFields, type FieldSpec, toBody, validate, type Values } from './form';

export function Panel({ id, title, aside, children }: { id: string; title: ReactNode; aside?: ReactNode; children: ReactNode }) {
  return (
    <section className="panel" aria-labelledby={id}>
      <div className="panel-hd">
        <h2 id={id}>{title}</h2>
        {aside}
      </div>
      <div className="panel-bd set-bd">{children}</div>
    </section>
  );
}

/** Settings form bound to one GET/PUT pair: edits locally, saves on submit, says when it is saved. */
function SettingsForm<T extends object>({
  id,
  title,
  spec,
  data,
  save,
  queryKey,
  intro,
}: {
  id: string;
  title: string;
  spec: readonly FieldSpec[];
  data: T | undefined;
  save: (b: T) => Promise<T>;
  queryKey: readonly unknown[];
  intro?: ReactNode;
}) {
  const qc = useQueryClient();
  const [v, setV] = useState<Values | null>(null);
  const [errors, setErrors] = useState<Record<string, string>>({});
  const values = v ?? ((data ?? {}) as Values);
  const m = useMutation({
    mutationFn: (b: T) => save(b),
    onSuccess: (r) => {
      qc.setQueryData(queryKey, r);
      setV(null);
      toast.show({ title: `${title} saved.` });
    },
    onError: (e) => toast.error(`${title} not saved`, errorText(e)),
  });
  const submit = (ev: FormEvent) => {
    ev.preventDefault();
    const e = validate(spec, values);
    setErrors(e);
    if (Object.keys(e).length) return;
    m.mutate({ ...(data as object), ...toBody(spec, values) } as T);
  };
  return (
    <Panel id={id} title={title}>
      {!data ? (
        <Skeleton className="sk-wanted" />
      ) : (
        <form onSubmit={submit} noValidate className="set-form">
          {intro}
          <FormFields spec={spec} values={values} onChange={setV} errors={errors} />
          <div className="row">
            <Button type="submit" variant="secondary" loading={m.isPending} disabled={!v}>
              Save
            </Button>
            {v && (
              <Button variant="ghost" onClick={() => setV(null)}>
                Discard changes
              </Button>
            )}
          </div>
        </form>
      )}
    </Panel>
  );
}

// ----------------------------------------------------------------- library: root folders, naming, import

function RootFolders() {
  const qc = useQueryClient();
  const roots = useQuery(rootFoldersQuery);
  const lib = useLibrary();
  const mappings = useQuery(pathMappingsQuery);
  const [path, setPath] = useState('');
  const [error, setError] = useState<string | null>(null);
  const [hidden, setHidden] = useState<ReadonlySet<number>>(new Set());
  const add = useMutation({
    mutationFn: (p: string) => addRootFolder(p),
    onSuccess: (r) => {
      setPath('');
      void qc.invalidateQueries({ queryKey: keys.rootFolders });
      toast.show({ title: `Added ${r.path}`, body: r.accessible ? 'Gonema can read and write in it.' : r.error });
    },
    onError: (e) => setError(errorText(e)),
  });
  const counts = useMemo(() => {
    const c = new Map<number, number>();
    for (const m of lib.data ?? []) c.set(m.rootFolderId, (c.get(m.rootFolderId) ?? 0) + 1);
    return c;
  }, [lib.data]);
  const remove = (id: number, p: string) => {
    setHidden((h) => new Set(h).add(id));
    const restore = () =>
      setHidden((h) => {
        const n = new Set(h);
        n.delete(id);
        return n;
      });
    toast.show({
      title: `Removed ${p} from the root folders`,
      body: 'Nothing on disk is touched.',
      undo: restore,
      onClose: (reason) => {
        if (reason === 'undo') return;
        removeRootFolder(id)
          .then(() => qc.invalidateQueries({ queryKey: keys.rootFolders }))
          .catch((e) => {
            restore();
            toast.error(`${p} is still a root folder`, errorText(e));
          });
      },
    });
  };
  return (
    <Panel id="root-folders" title="Root folders">
      <p className="t-secondary t-body-sm">
        Each film gets its own folder in one of these. Removing a root folder never deletes files.
      </p>
      {roots.isPending ? (
        <Skeleton className="sk-wanted" />
      ) : (
        <ul className="mini-rows">
          {(roots.data ?? [])
            .filter((r) => !hidden.has(r.id))
            .map((r) => {
              const n = counts.get(r.id) ?? 0;
              return (
                <li key={r.id}>
                  <span className="t-mono-sm">
                    {r.path}
                    <small>
                      {[
                        r.freeSpace != null ? `${formatBytes(r.freeSpace)} free` : '',
                        r.totalSpace ? `of ${formatBytes(r.totalSpace)}` : '',
                        `${formatNumber(n)} ${n === 1 ? 'film' : 'films'}`,
                      ]
                        .filter(Boolean)
                        .join(' · ')}
                    </small>
                    {r.error && <small className="err">{r.error}</small>}
                  </span>
                  {r.accessible ? (
                    <StatusText status="available">OK</StatusText>
                  ) : (
                    <StatusText status="failed">Unreachable</StatusText>
                  )}
                  <Button
                    size="sm"
                    variant="ghost"
                    disabled={n > 0}
                    title={n > 0 ? 'Move its films to another folder first' : undefined}
                    onClick={() => remove(r.id, r.path)}
                    aria-label={`Remove root folder ${r.path}`}
                  >
                    Remove
                  </Button>
                </li>
              );
            })}
        </ul>
      )}
      <form
        className="row add-root"
        onSubmit={(ev) => {
          ev.preventDefault();
          const p = path.trim();
          if (!p.startsWith('/') && !/^[A-Za-z]:\\/.test(p)) {
            setError('Use an absolute path as Gonema sees it, for example /data/movies.');
            return;
          }
          setError(null);
          add.mutate(p);
        }}
      >
        <Field label="Add a root folder" error={error}>
          {({ id, describedBy, invalid }) => (
            <Input
              id={id}
              mono
              aria-describedby={describedBy}
              invalid={invalid}
              placeholder="/data/movies"
              value={path}
              onChange={(ev) => setPath(ev.target.value)}
            />
          )}
        </Field>
        <Button type="submit" icon={<FolderPlus className="i" aria-hidden="true" />} loading={add.isPending}>
          Add
        </Button>
      </form>
      {!!mappings.data?.length && (
        <>
          <span className="field-label">Remote path mappings</span>
          <div className="mappings t-mono-sm t-secondary">
            {mappings.data.map((m) => (
              <span key={m.id} className="contents">
                <span className="t-muted">{m.host}</span>
                <span>
                  {m.remotePath} → {m.localPath}
                </span>
              </span>
            ))}
          </div>
        </>
      )}
    </Panel>
  );
}

function Naming() {
  const qc = useQueryClient();
  const naming = useQuery(namingQuery);
  const lib = useLibrary();
  const [draft, setDraft] = useState<NamingInput | null>(null);
  const cur: NamingInput | null = draft ?? (naming.data ? { ...naming.data } : null);
  const sample = useMemo(() => (lib.data ?? []).find((m) => m.quality)?.id, [lib.data]);
  const [preview, setPreview] = useState<NamingPreview | null>(null);
  const [previewError, setPreviewError] = useState<string | null>(null);
  useEffect(() => {
    if (!cur) return;
    const ctl = new AbortController();
    const t = setTimeout(() => {
      previewNaming({ ...cur, movieId: sample }, ctl.signal)
        .then((p) => {
          setPreview(p);
          setPreviewError(null);
        })
        .catch((e) => {
          if ((e as Error).name !== 'AbortError') setPreviewError(errorText(e));
        });
    }, 250);
    return () => {
      clearTimeout(t);
      ctl.abort();
    };
  }, [cur?.fileTemplate, cur?.folderTemplate, cur?.renameFiles, sample]);
  const save = useMutation({
    mutationFn: (b: NamingInput) => putNaming(b),
    onSuccess: (r) => {
      qc.setQueryData(lot3Keys.naming, r);
      setDraft(null);
      toast.show({ title: 'Naming saved.', body: 'New imports use it; existing files keep their names until renamed.' });
    },
    onError: (e) => toast.error('Naming not saved', errorText(e)),
  });
  if (!cur || !naming.data)
    return (
      <Panel id="naming" title="Naming">
        <Skeleton className="sk-wanted" />
      </Panel>
    );
  const tokens = naming.data.tokens ?? [];
  return (
    <Panel id="naming" title="Naming" aside={<span className="t-caption t-muted">Live preview with a film from your library</span>}>
      <form
        className="tmpl"
        onSubmit={(ev) => {
          ev.preventDefault();
          save.mutate(cur);
        }}
      >
        <Field label="Folder">
          {({ id, describedBy }) => (
            <Input
              id={id}
              mono
              aria-describedby={describedBy}
              value={cur.folderTemplate}
              onChange={(ev) => setDraft({ ...cur, folderTemplate: ev.target.value })}
            />
          )}
        </Field>
        <Field
          label="Film file"
          hint={
            tokens.length ? (
              <>
                Tokens:{' '}
                {tokens.map((t) => (
                  <span key={t.name} className="tok" title={`${t.description} (${t.example})`}>
                    {t.name}
                  </span>
                ))}
              </>
            ) : undefined
          }
        >
          {({ id, describedBy }) => (
            <Input
              id={id}
              mono
              aria-describedby={describedBy}
              value={cur.fileTemplate}
              onChange={(ev) => setDraft({ ...cur, fileTemplate: ev.target.value })}
            />
          )}
        </Field>
        <div className="preview" aria-live="polite" aria-label="Preview">
          {previewError ? (
            <span className="err">{previewError}</span>
          ) : preview ? (
            <>
              <span>{preview.folder}/</span>
              {preview.file}
              {preview.example && <span> · example film</span>}
            </>
          ) : (
            ' '
          )}
        </div>
        <div className="row">
          <Switch checked={cur.renameFiles} onCheckedChange={(c) => setDraft({ ...cur, renameFiles: c })}>
            Rename imported files
          </Switch>
          <Button
            variant="ghost"
            size="sm"
            className="ml-auto"
            disabled={!draft}
            onClick={() => setDraft({ ...naming.data.defaults })}
          >
            Reset to defaults
          </Button>
          <Button type="submit" variant="secondary" size="sm" loading={save.isPending} disabled={!draft}>
            Save
          </Button>
        </div>
      </form>
    </Panel>
  );
}

const IMPORT_SPEC: FieldSpec[] = [
  {
    kind: 'select',
    name: 'mode',
    label: 'Usenet downloads',
    options: [
      { value: 'move', label: 'Move into the library' },
      { value: 'copy', label: 'Copy into the library' },
    ],
  },
  {
    kind: 'select',
    name: 'torrentMode',
    label: 'Torrent downloads',
    hint: 'Hardlinks keep seeding without using twice the space (same disk only).',
    options: [
      { value: 'hardlink', label: 'Hardlink (falls back to copy)' },
      { value: 'copy', label: 'Copy' },
    ],
  },
  { kind: 'switch', name: 'importSubtitles', label: 'Import subtitle files' },
  { kind: 'mono', name: 'recycleBin', label: 'Recycle bin', hint: 'Deleted and replaced files go here first.' },
  { kind: 'number', name: 'recycleRetentionDays', label: 'Keep recycled files (days)', min: 0, max: 365 },
];

export function LibrarySection() {
  const imp = useQuery(importSettingsQuery);
  return (
    <div className="set-stack">
      <RootFolders />
      <Naming />
      <SettingsForm<ImportSettingsBody>
        id="import"
        title="Importing"
        spec={IMPORT_SPEC}
        data={imp.data}
        save={putImportSettings}
        queryKey={lot3Keys.importSettings}
      />
    </div>
  );
}

// ----------------------------------------------------------------- acquisition & health

const ACQ_SPEC: FieldSpec[] = [
  { kind: 'number', name: 'rssIntervalMin', label: 'RSS sync every (minutes)', min: 10, max: 120 },
  { kind: 'number', name: 'searchMissingIntervalHours', label: 'Search for missing films every (hours)', min: 1, max: 168 },
  { kind: 'number', name: 'maxAttemptsPerDay', label: 'Attempts per film per day', min: 1, max: 20 },
  {
    kind: 'select',
    name: 'preferredProtocol',
    label: 'Preferred protocol',
    hint: 'Breaks ties between equally good releases.',
    options: [
      { value: 'none', label: 'No preference' },
      { value: 'usenet', label: 'Usenet' },
      { value: 'torrent', label: 'Torrent' },
    ],
  },
  { kind: 'number', name: 'usenetRetentionDays', label: 'Usenet retention (days)', min: 0, hint: '0 for unlimited' },
  { kind: 'number', name: 'usenetMinimumAgeMin', label: 'Usenet minimum age (minutes)', min: 0 },
  { kind: 'switch', name: 'blocklistGroupOnFailure', label: 'Block the whole group when a release fails' },
];

const HEALTH_SPEC: FieldSpec[] = [
  { kind: 'number', name: 'lowSpaceGb', label: 'Warn below (GB free)', min: 0 },
  { kind: 'number', name: 'lowSpacePercent', label: 'Warn below (% free)', min: 0, max: 100 },
  { kind: 'number', name: 'recycleBinMaxGb', label: 'Warn when the recycle bin exceeds (GB)', min: 0 },
];

export function AcquisitionSection() {
  const acq = useQuery(acquisitionQuery);
  const health = useQuery(healthSettingsQuery);
  return (
    <div className="set-stack">
      <SettingsForm<AcquisitionSettings>
        id="acq"
        title="Searching & grabbing"
        spec={ACQ_SPEC}
        data={acq.data}
        save={putAcquisition}
        queryKey={lot3Keys.acquisition}
      />
      <SettingsForm<HealthSettings>
        id="health-set"
        title="Health warnings"
        spec={HEALTH_SPEC}
        data={health.data}
        save={putHealthSettings}
        queryKey={lot3Keys.healthSettings}
      />
    </div>
  );
}

// ----------------------------------------------------------------- metadata (TMDB)

export function MetadataSection() {
  const qc = useQueryClient();
  const tmdb = useQuery(tmdbQuery);
  const [key, setKey] = useState('');
  const [error, setError] = useState<string | null>(null);
  const save = useMutation({
    mutationFn: (k: string) => setTmdbKey(k),
    onSuccess: () => {
      setKey('');
      void qc.invalidateQueries({ queryKey: screenKeys.tmdb });
      void qc.invalidateQueries({ queryKey: keys.status });
      toast.show({ title: 'TMDB key saved.', body: 'Posters, cast and release dates now come from TMDB.' });
    },
    onError: (e) => setError(errorText(e)),
  });
  const configured = !!tmdb.data?.configured;
  return (
    <Panel
      id="tmdb"
      title="The Movie Database"
      aside={
        tmdb.data ? (
          configured ? (
            <StatusText status="available">Connected</StatusText>
          ) : (
            <StatusText status="missing">No key yet</StatusText>
          )
        ) : null
      }
    >
      <p className="t-secondary t-body-sm">
        Gonema uses TMDB for posters, cast, collections and release dates. The key is yours, stays encrypted on this
        server and is never shown again.
      </p>
      <form
        className="row add-root"
        onSubmit={(ev) => {
          ev.preventDefault();
          if (key.trim().length < 16) {
            setError('That is too short for a TMDB key. Paste the API Read Access Token (the long one) or the API Key.');
            return;
          }
          setError(null);
          save.mutate(key.trim());
        }}
      >
        <Field label={configured ? 'Replace the key' : 'API Read Access Token'} error={error}>
          {({ id, describedBy, invalid }) => (
            <Input
              id={id}
              mono
              type="password"
              autoComplete="off"
              icon={<Lock className="i" aria-hidden="true" />}
              aria-describedby={describedBy}
              invalid={invalid}
              placeholder={configured ? 'Saved · paste a new key to replace it' : 'eyJhbGciOi…'}
              value={key}
              onChange={(ev) => setKey(ev.target.value)}
            />
          )}
        </Field>
        <Button type="submit" icon={<KeyRound className="i" aria-hidden="true" />} loading={save.isPending}>
          {configured ? 'Replace' : 'Save key'}
        </Button>
      </form>
      {configured && (
        <span className="ok-line">
          <CircleCheck className="i" aria-hidden="true" />A key is stored and was accepted by TMDB.
        </span>
      )}
    </Panel>
  );
}

// ----------------------------------------------------------------- tags

export function TagsSection() {
  const qc = useQueryClient();
  const tags = useQuery(tagsQuery);
  const [editing, setEditing] = useState<{ id: number; label: string } | null>(null);
  const [hidden, setHidden] = useState<ReadonlySet<number>>(new Set());
  const rename = useMutation({
    mutationFn: ({ id, label }: { id: number; label: string }) => renameTag(id, label),
    onSuccess: () => {
      setEditing(null);
      void qc.invalidateQueries({ queryKey: lot3Keys.tags });
      void qc.invalidateQueries({ queryKey: keys.library });
    },
    onError: (e) => toast.error('Tag not renamed', errorText(e)),
  });
  const del = (id: number, label: string, n: number) => {
    setHidden((h) => new Set(h).add(id));
    const restore = () =>
      setHidden((h) => {
        const x = new Set(h);
        x.delete(id);
        return x;
      });
    toast.show({
      title: `Deleted the tag ${label}`,
      body: n ? `It comes off ${formatNumber(n)} ${n === 1 ? 'film' : 'films'}. The films stay.` : undefined,
      undo: restore,
      onClose: (reason) => {
        if (reason === 'undo') return;
        deleteTag(id)
          .then(() => {
            void qc.invalidateQueries({ queryKey: lot3Keys.tags });
            void qc.invalidateQueries({ queryKey: keys.library });
          })
          .catch((e) => {
            restore();
            toast.error(`${label} was not deleted`, errorText(e));
          });
      },
    });
  };
  const list = (tags.data ?? []).filter((t) => !hidden.has(t.id));
  return (
    <Panel id="tags" title="Tags">
      <p className="t-secondary t-body-sm">
        Tags group films, and tie them to indexers, clients and notifications that carry the same tag. Add them from a
        film or the library’s bulk bar.
      </p>
      {tags.isPending ? (
        <Skeleton className="sk-wanted" />
      ) : list.length ? (
        <ul className="mini-rows">
          {list.map((t) => (
            <li key={t.id}>
              {editing?.id === t.id ? (
                <form
                  className="row"
                  onSubmit={(ev) => {
                    ev.preventDefault();
                    if (editing.label.trim()) rename.mutate({ id: t.id, label: editing.label.trim() });
                  }}
                >
                  <Input
                    aria-label={`New name for ${t.label}`}
                    value={editing.label}
                    autoFocus
                    onChange={(ev) => setEditing({ id: t.id, label: ev.target.value })}
                    onKeyDown={(ev) => {
                      if (ev.key === 'Escape') setEditing(null);
                    }}
                  />
                  <Button type="submit" size="sm" loading={rename.isPending}>
                    Rename
                  </Button>
                </form>
              ) : (
                <span>
                  <TagIcon className="i i-xs" aria-hidden="true" /> {t.label}
                  <small>
                    {formatNumber(t.movies)} {t.movies === 1 ? 'film' : 'films'}
                  </small>
                </span>
              )}
              <span />
              <span className="acts">
                <Button size="sm" variant="ghost" onClick={() => setEditing({ id: t.id, label: t.label })} aria-label={`Rename ${t.label}`}>
                  Rename
                </Button>
                <Button size="sm" variant="ghost" onClick={() => del(t.id, t.label, t.movies)} aria-label={`Delete tag ${t.label}`}>
                  Delete
                </Button>
              </span>
            </li>
          ))}
        </ul>
      ) : (
        <EmptyState title="No tags yet." headingLevel={3}>
          Select films in the library and choose Tags to create the first one.
        </EmptyState>
      )}
    </Panel>
  );
}

