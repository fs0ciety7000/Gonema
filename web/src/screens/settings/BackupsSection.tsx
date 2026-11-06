import '@/styles/system.css';
import { useMutation, useQuery, useQueryClient } from '@tanstack/react-query';
import {
  ArchiveRestore,
  CalendarClock,
  Check,
  CircleCheck,
  Clock,
  Database,
  Download,
  EyeOff,
  Film,
  Folder,
  HardDrive,
  Info,
  KeyRound,
  Lock,
  type LucideIcon,
  Trash2,
  TriangleAlert,
  Upload,
} from 'lucide-react';
import { type FormEvent, useId, useRef, useState } from 'react';
import {
  backupDownloadUrl,
  backupSettingsQuery,
  backupsQuery,
  cancelRestore,
  createBackup,
  deleteBackup,
  opsKeys,
  putBackupSettings,
  restoreBackup,
  uploadBackup,
} from '@/api/ops';
import type { BackupInfo, BackupList, BackupSettings, RestoreStaged } from '@/api/schema';
import { problemText } from '@/components/auth/AuthParts';
import { StatusText } from '@/components/ui/Badges';
import { Button, IconButton } from '@/components/ui/Button';
import { Checkbox } from '@/components/ui/Checkbox';
import { Chip } from '@/components/ui/Chip';
import { Dialog } from '@/components/ui/Dialog';
import { Skeleton } from '@/components/ui/Feedback';
import { Field, Input } from '@/components/ui/Field';
import { Switch } from '@/components/ui/Switch';
import { toast } from '@/components/ui/Toast';
import { formatBytes, formatClock, formatNumber } from '@/lib/format';
import { HeaderActions } from './headerSlot';
import { Panel } from './Sections';

const KIND: Record<BackupInfo['kind'], { label: string; icon: LucideIcon }> = {
  scheduled: { label: 'Scheduled', icon: CalendarClock },
  manual: { label: 'By hand', icon: HardDrive },
  'pre-restore': { label: 'Before a restore', icon: ArchiveRestore },
  uploaded: { label: 'Uploaded', icon: Upload },
};

/** "Every 7 days", "Every day", "Every 6 hours" */
export function everyLabel(hours: number): string {
  if (hours % 24 === 0) return hours === 24 ? 'Every day' : `Every ${hours / 24} days`;
  return hours === 1 ? 'Every hour' : `Every ${hours} hours`;
}

export const PRESETS = [
  { label: 'Day', hours: 24 },
  { label: 'Week', hours: 168 },
  { label: 'Month', hours: 720 },
] as const;

const dayMonth = new Intl.DateTimeFormat('en-GB', { day: 'numeric', month: 'short' });
const dayMonthYear = new Intl.DateTimeFormat('en-GB', { day: 'numeric', month: 'short', year: 'numeric' });

/** "Today, 09:14", "4 Nov, 22:17", "28 Oct 2025, 18:02" */
export function madeWhen(iso: string, now = new Date()): string {
  const d = new Date(iso);
  if (Number.isNaN(d.getTime())) return '';
  const day =
    d.toDateString() === now.toDateString()
      ? 'Today'
      : d.getFullYear() === now.getFullYear()
        ? dayMonth.format(d)
        : dayMonthYear.format(d);
  return `${day}, ${formatClock(d)}`;
}

/** "today at 09:14", "on 4 Nov at 09:14" */
export function stagedWhen(iso: string, now = new Date()): string {
  const d = new Date(iso);
  if (Number.isNaN(d.getTime())) return '';
  return d.toDateString() === now.toDateString()
    ? `today at ${formatClock(d)}`
    : `on ${(d.getFullYear() === now.getFullYear() ? dayMonth : dayMonthYear).format(d)} at ${formatClock(d)}`;
}

/** Count and total size of the archives in the folder. */
export function folderTotals(list: readonly BackupInfo[]): { count: number; bytes: number } {
  return { count: list.length, bytes: list.reduce((n, b) => n + Math.max(b.size, 0), 0) };
}

function PendingBanner({ data }: { data: BackupList }) {
  const qc = useQueryClient();
  const p = data.pendingRestore;
  const cancel = useMutation({
    mutationFn: cancelRestore,
    onSuccess: () => {
      toast.show({ title: 'Restore canceled.', body: 'The current database stays as it is.' });
      void qc.invalidateQueries({ queryKey: opsKeys.backups });
    },
    onError: (e) => toast.error('The restore is still staged', problemText(e)),
  });
  if (!p) return null;
  const src = data.backups?.find((b) => b.name === p.name);
  return (
    <div className="pending" role="status">
      <span className="ic" aria-hidden="true">
        <ArchiveRestore className="i" />
      </span>
      <div>
        <b>A restore is waiting for a restart.</b>
        <span className="t">
          The backup of <span className="hl">{src ? madeWhen(src.createdAt).replace(/^Today/, 'today') : p.name}</span>{' '}
          replaces the current database when Gonema restarts{p.restoreKey ? ', with its master key' : ''}. Staged{' '}
          {stagedWhen(p.stagedAt)}
          {'; until then nothing has changed.'}
        </span>
      </div>
      <Button size="sm" loading={cancel.isPending} onClick={() => cancel.mutate()}>
        Cancel restore
      </Button>
    </div>
  );
}

function Tiles({ data, settings }: { data: BackupList | undefined; settings: BackupSettings | undefined }) {
  const list = data?.backups ?? [];
  const last = list[0];
  const tot = folderTotals(list);
  return (
    <div className="tiles">
      <div className="tile">
        <span className="lbl">
          <Clock className="i" aria-hidden="true" />
          Last backup
        </span>
        <span className="v">
          {last ? madeWhen(last.createdAt) : 'None yet'}
          {last &&
            (last.restorable ? (
              <StatusText status="available">OK</StatusText>
            ) : (
              <StatusText status="failed">Unusable</StatusText>
            ))}
        </span>
        <span className="s">
          {last
            ? `${KIND[last.kind]?.label ?? last.kind} · ${formatBytes(last.size)}`
            : 'Back up now makes one in seconds'}
        </span>
      </div>
      <div className="tile">
        <span className="lbl">
          <CalendarClock className="i" aria-hidden="true" />
          Schedule
        </span>
        <span className="v">{!settings ? '—' : settings.enabled ? everyLabel(settings.intervalHours) : 'Off'}</span>
        <span className="s">
          {!settings
            ? ''
            : settings.enabled
              ? `Keeps the ${settings.keep} newest scheduled ${settings.keep === 1 ? 'backup' : 'backups'}`
              : 'Backups made by hand still work'}
        </span>
      </div>
      <div className="tile">
        <span className="lbl">
          <Folder className="i" aria-hidden="true" />
          In the folder
        </span>
        <span className="v">
          {formatNumber(tot.count)} {tot.count === 1 ? 'archive' : 'archives'}
          {tot.count ? ` · ${formatBytes(tot.bytes)}` : ''}
        </span>
        <span className="s t-mono-sm">{data?.directory ?? ''}</span>
      </div>
    </div>
  );
}

function ArchiveTable({
  data,
  context,
  onRestore,
  onDelete,
}: {
  data: BackupList;
  context: string | null;
  onRestore: (b: BackupInfo) => void;
  onDelete: (b: BackupInfo) => void;
}) {
  const id = useId();
  const staged = data.pendingRestore?.name;
  const list = data.backups ?? [];
  return (
    <Panel
      id="bk-list"
      title={
        <>
          <Database className="i" aria-hidden="true" />
          Archives <span className="sub">newest first</span>
        </>
      }
    >
      {!list.length ? (
        <p className="explain">No backup yet. “Back up now” makes one in a few seconds, while Gonema keeps running.</p>
      ) : (
        <table className="bk">
          <caption className="sr-only">Backup archives</caption>
          <thead>
            <tr>
              <th scope="col">Made</th>
              <th scope="col">Kind</th>
              <th scope="col" className="r">
                Size
              </th>
              <th scope="col" className="col-schema">
                Schema
              </th>
              <th scope="col">Master key</th>
              <th scope="col">
                <span className="sr-only">Actions</span>
              </th>
            </tr>
          </thead>
          <tbody>
            {list.map((b, i) => {
              const k = KIND[b.kind] ?? { label: b.kind, icon: HardDrive };
              const KI = k.icon;
              const why = !b.restorable ? b.problem || 'This archive cannot be restored.' : '';
              const whyId = `${id}-p${i}`;
              return (
                <tr
                  key={b.name}
                  className={[why ? 'bad' : '', context === b.name ? 'is-context' : ''].join(' ').trim() || undefined}
                >
                  <td>
                    <span className="when">
                      {madeWhen(b.createdAt)}
                      {staged === b.name && (
                        <span className="badge" data-status="missing">
                          <ArchiveRestore className="i" aria-hidden="true" />
                          Staged
                        </span>
                      )}
                    </span>
                    <span className="fn" title={b.name}>
                      {b.name}
                    </span>
                    {why && (
                      <span className="problem" id={whyId}>
                        <TriangleAlert className="i" aria-hidden="true" />
                        {why}
                      </span>
                    )}
                  </td>
                  <td>
                    <span className="kind">
                      <KI className="i" aria-hidden="true" />
                      {k.label}
                    </span>
                  </td>
                  <td className="r">{formatBytes(b.size)}</td>
                  <td className="col-schema">
                    <span className="schema">
                      {b.schemaVersion}
                      <small>Gonema {b.appVersion}</small>
                    </span>
                  </td>
                  <td className="keyc">
                    {b.includesKey ? (
                      <span className="status" data-status="missing">
                        <KeyRound className="i" aria-hidden="true" />
                        Included
                      </span>
                    ) : (
                      <span className="no">Not included</span>
                    )}
                  </td>
                  <td>
                    <div className="acts">
                      <a
                        className="icon-btn sm"
                        href={backupDownloadUrl(b.name)}
                        download={b.name}
                        aria-label={`Download ${b.name}`}
                        title="Download"
                      >
                        <Download className="i i-sm" aria-hidden="true" />
                      </a>
                      <Button
                        size="sm"
                        variant="ghost"
                        disabled={!b.restorable}
                        aria-describedby={why ? whyId : undefined}
                        onClick={() => onRestore(b)}
                        aria-label={`Restore ${b.name}…`}
                      >
                        Restore…
                      </Button>
                      <IconButton size="sm" label={`Delete ${b.name}…`} title="Delete…" onClick={() => onDelete(b)}>
                        <Trash2 className="i i-sm" aria-hidden="true" />
                      </IconButton>
                    </div>
                  </td>
                </tr>
              );
            })}
          </tbody>
        </table>
      )}
      <div className="folder">
        <Folder className="i" aria-hidden="true" />
        <span>
          Stored in <span className="t-mono-sm">{data.directory}</span>. Copy them off this machine now and then: a
          backup on the same disk does not survive the disk.
        </span>
      </div>
    </Panel>
  );
}

type Draft = { intervalHours: string; keep: string };

function SchedulePanel({ settings }: { settings: BackupSettings | undefined }) {
  const qc = useQueryClient();
  const [draft, setDraft] = useState<Draft | null>(null);
  const [errors, setErrors] = useState<{ intervalHours?: string; keep?: string }>({});
  const [saved, setSaved] = useState(false);
  const m = useMutation({
    mutationFn: putBackupSettings,
    onSuccess: (r) => {
      qc.setQueryData(opsKeys.backupSettings, r);
      setDraft(null);
      setSaved(true);
    },
    onError: (e) => toast.error('The schedule was not saved', problemText(e)),
  });
  if (!settings)
    return (
      <Panel id="bk-sched" title="Schedule and retention">
        <Skeleton className="sk-row" />
      </Panel>
    );
  const v: Draft = draft ?? { intervalHours: String(settings.intervalHours), keep: String(settings.keep) };
  const save = (patch: Partial<BackupSettings>) => {
    setSaved(false);
    m.mutate({ ...settings, ...patch });
  };
  const commit = (ev?: FormEvent) => {
    ev?.preventDefault();
    const hours = Number(v.intervalHours);
    const keep = Number(v.keep);
    const e: typeof errors = {};
    if (!Number.isInteger(hours) || hours < 1 || hours > 720) e.intervalHours = 'Use 1 to 720 hours (30 days).';
    if (!Number.isInteger(keep) || keep < 1 || keep > 100) e.keep = 'Keep 1 to 100 scheduled backups.';
    setErrors(e);
    if (e.intervalHours || e.keep) return;
    if (hours !== settings.intervalHours || keep !== settings.keep) save({ intervalHours: hours, keep });
    else setDraft(null);
  };
  const hours = Number(v.intervalHours);
  const shownHours = Number.isInteger(hours) && hours >= 1 ? hours : settings.intervalHours;
  const keep = Number(v.keep) >= 1 ? Number(v.keep) : settings.keep;
  return (
    <Panel
      id="bk-sched"
      title={
        <>
          <CalendarClock className="i" aria-hidden="true" />
          Schedule and retention
        </>
      }
      aside={
        <span className="t-caption t-muted" role="status">
          {m.isPending ? (
            'Saving…'
          ) : saved ? (
            <>
              <Check className="i i-xs" aria-hidden="true" /> Saved
            </>
          ) : (
            ''
          )}
        </span>
      }
    >
      <form className="set-form" onSubmit={commit} noValidate>
        <div className="sw-row">
          <span>
            <b id="bk-sw1">Back up on a schedule</b>
            <small>A failed run raises a health issue.</small>
          </span>
          <Switch
            checked={settings.enabled}
            aria-labelledby="bk-sw1"
            onCheckedChange={(enabled) => save({ enabled })}
          />
        </div>
        <div className="two">
          <Field label="Every" error={errors.intervalHours}>
            {({ id, describedBy, invalid }) => (
              <>
                <Input
                  id={id}
                  mono
                  inputMode="numeric"
                  aria-describedby={describedBy}
                  invalid={invalid}
                  value={v.intervalHours}
                  suffix="hours"
                  onChange={(ev) => setDraft({ ...v, intervalHours: ev.target.value })}
                  onBlur={() => commit()}
                />
                {/* biome-ignore lint/a11y/useSemanticElements: a group of toggle buttons, not a form fieldset */}
                <div className="presets" role="group" aria-label="Presets">
                  {PRESETS.map((p) => (
                    <Chip
                      key={p.label}
                      pressed={shownHours === p.hours}
                      onClick={() => {
                        setErrors({});
                        setDraft({ ...v, intervalHours: String(p.hours) });
                        save({ intervalHours: p.hours, keep });
                      }}
                    >
                      {p.label}
                    </Chip>
                  ))}
                </div>
              </>
            )}
          </Field>
          <Field label="Keep" error={errors.keep} hint="1 to 100. Older scheduled ones are deleted after each run.">
            {({ id, describedBy, invalid }) => (
              <Input
                id={id}
                mono
                inputMode="numeric"
                aria-describedby={describedBy}
                invalid={invalid}
                value={v.keep}
                suffix="scheduled"
                onChange={(ev) => setDraft({ ...v, keep: ev.target.value })}
                onBlur={() => commit()}
              />
            )}
          </Field>
        </div>
        <div className="sw-row">
          <span>
            <b id="bk-sw2">Include the master key</b>
            <small>
              Off is safer: with the key inside, whoever gets the file can read every stored password and API key.
            </small>
          </span>
          <Switch
            checked={settings.includeKey}
            aria-labelledby="bk-sw2"
            onCheckedChange={(includeKey) => save({ includeKey })}
          />
        </div>
        <p className="plain">
          In plain words:{' '}
          {settings.enabled ? (
            <>
              <b>{everyLabel(shownHours).toLowerCase()}</b>, Gonema saves the database and keeps the{' '}
              <b>{keep === 1 ? 'newest' : `${keep} newest`}</b> scheduled {keep === 1 ? 'copy' : 'copies'},{' '}
              <b>{settings.includeKey ? 'with the master key' : 'without the master key'}</b>.
            </>
          ) : (
            <>
              Gonema makes <b>no backup on its own</b>.
            </>
          )}{' '}
          Backups made by hand, uploaded or made before a restore stay until you delete them.
        </p>
      </form>
    </Panel>
  );
}

function MasterKeyPanel() {
  return (
    <Panel
      id="bk-mk"
      title={
        <>
          <KeyRound className="i" aria-hidden="true" />
          The master key
        </>
      }
    >
      <div className="keypanel">
        <div className="keyfig">
          <span className="ic" aria-hidden="true">
            <Lock className="i" />
          </span>
          <span>
            <b className="t-mono-sm">gonema.key</b>
            It unlocks the secrets stored in the database: indexer keys, client passwords, the TMDB key.
          </span>
        </div>
        <ul className="rules">
          <li>
            <Check className="i" aria-hidden="true" />
            <span>
              <b>Back it up once, somewhere else</b> (a password manager, an offline copy). It never changes.
            </span>
          </li>
          <li>
            <EyeOff className="i" aria-hidden="true" />
            <span>
              <b>Left out of archives by default.</b> An archive holding both the database and the key unlocks
              everything.
            </span>
          </li>
          <li>
            <Info className="i" aria-hidden="true" />
            <span>
              A backup restored with another key keeps its films and settings; its stored passwords must be entered
              again.
            </span>
          </li>
        </ul>
      </div>
    </Panel>
  );
}

const longDate = new Intl.DateTimeFormat('en-GB', { day: 'numeric', month: 'short', year: 'numeric' });

function RestoreDialog({ b, onClose }: { b: BackupInfo | null; onClose: () => void }) {
  const qc = useQueryClient();
  const [withKey, setWithKey] = useState(false);
  const [result, setResult] = useState<RestoreStaged | null>(null);
  // keep the archive while the dialog closes (its exit animation)
  const last = useRef<BackupInfo | null>(null);
  if (b) last.current = b;
  const a = b ?? last.current;
  const m = useMutation({
    mutationFn: () => restoreBackup(a!.name, withKey),
    onSuccess: (r) => {
      setResult(r);
      void qc.invalidateQueries({ queryKey: opsKeys.backups });
    },
    onError: (e) => toast.error('Nothing was staged', problemText(e)),
  });
  const close = () => {
    setResult(null);
    setWithKey(false);
    m.reset();
    onClose();
  };
  const d = a ? new Date(a.createdAt) : null;
  const short = d ? dayMonth.format(d) : '';
  return (
    <Dialog
      open={!!b}
      onOpenChange={(o) => !o && close()}
      className="restore-dialog"
      icon={<ArchiveRestore className="i i-lg" aria-hidden="true" />}
      title={result ? 'Restore staged' : `Restore the backup of ${short}?`}
      description={
        result
          ? result.message
          : d &&
            `Films, settings, history and connections go back to how they were on ${longDate.format(d)} at ${formatClock(d)}.`
      }
      actions={
        result ? (
          <Button variant="primary" onClick={close}>
            Done
          </Button>
        ) : (
          <>
            <Button variant="ghost" onClick={close}>
              Cancel
            </Button>
            <Button
              variant="danger"
              loading={m.isPending}
              icon={<ArchiveRestore className="i" aria-hidden="true" />}
              onClick={() => m.mutate()}
            >
              Back up now, then stage the restore
            </Button>
          </>
        )
      }
    >
      {result ? (
        <ul className="restore-done">
          <li>
            The current state was saved first as <span className="t-mono-sm">{result.preRestoreBackup.name}</span>.
          </li>
          <li>Restart Gonema to finish. Until then nothing changes, and you can cancel from this page.</li>
          {(result.warnings ?? []).map((w) => (
            <li key={w}>{w}</li>
          ))}
        </ul>
      ) : (
        a && (
          <>
            <div className="arch">
              <span className="fn">{a.name}</span>
              <span className="ok">
                <CircleCheck className="i" aria-hidden="true" />
                Checked
              </span>
              <span>
                {KIND[a.kind]?.label ?? a.kind} · {formatBytes(a.size)} · schema {a.schemaVersion} · Gonema{' '}
                {a.appVersion}
              </span>
              <span>{a.includesKey ? 'holds the master key' : 'no master key'}</span>
            </div>
            <ol className="steps3">
              <li>
                <span className="n" aria-hidden="true">
                  1
                </span>
                <span>
                  <b>First, a backup of how things are now</b>
                  Kept as “Before a restore”, so you can come back to today.
                </span>
              </li>
              <li>
                <span className="n" aria-hidden="true">
                  2
                </span>
                {a.includesKey ? (
                  <span>
                    <b>The master key: your choice</b>
                    This archive holds one. Left unticked, the current key stays: the archive’s stored passwords then
                    open only if the key has not changed since {short}.
                    <Checkbox
                      checked={withKey}
                      onCheckedChange={setWithKey}
                      label="Also restore the master key from this archive"
                    />
                  </span>
                ) : (
                  <span>
                    <b>The master key stays as it is</b>
                    This archive doesn’t hold one. Its stored passwords open with the key that was in use on {short}:
                    this server’s, unless it changed since.
                  </span>
                )}
              </li>
              <li>
                <span className="n" aria-hidden="true">
                  3
                </span>
                <span>
                  <b>It applies at the next restart</b>
                  Nothing changes until Gonema restarts. Until then you can cancel from this page.
                </span>
              </li>
            </ol>
            <div className="untouched">
              <Film className="i" aria-hidden="true" />
              Film files on disk are not touched.
            </div>
          </>
        )
      )}
    </Dialog>
  );
}

function DeleteDialog({ b, onClose }: { b: BackupInfo | null; onClose: () => void }) {
  const qc = useQueryClient();
  const m = useMutation({
    mutationFn: () => deleteBackup(b!.name),
    onSuccess: () => {
      toast.show({ title: 'Backup deleted.', body: b?.name });
      void qc.invalidateQueries({ queryKey: opsKeys.backups });
      onClose();
    },
    onError: (e) => toast.error('The backup was not deleted', problemText(e)),
  });
  return (
    <Dialog
      open={!!b}
      onOpenChange={(o) => !o && onClose()}
      icon={<Trash2 className="i" aria-hidden="true" />}
      title="Delete this backup?"
      description={
        b && (
          <>
            The file <span className="t-mono-sm">{b.name}</span> ({formatBytes(b.size)}) is removed from the backups
            folder. This cannot be undone.
          </>
        )
      }
      actions={
        <>
          <Button variant="ghost" onClick={onClose}>
            Keep it
          </Button>
          <Button variant="danger-solid" loading={m.isPending} onClick={() => m.mutate()}>
            Delete backup
          </Button>
        </>
      }
    />
  );
}

export function BackupsSection() {
  const qc = useQueryClient();
  const list = useQuery(backupsQuery);
  const settings = useQuery(backupSettingsQuery);
  const [restoring, setRestoring] = useState<BackupInfo | null>(null);
  const [deleting, setDeleting] = useState<BackupInfo | null>(null);
  const fileRef = useRef<HTMLInputElement>(null);
  const refresh = () => void qc.invalidateQueries({ queryKey: opsKeys.backups });
  const create = useMutation({
    mutationFn: createBackup,
    onSuccess: (b) => {
      toast.show({ title: 'Backup made.', body: `${b.name} · ${formatBytes(b.size)}` });
      refresh();
    },
    onError: (e) => toast.error('The backup failed', problemText(e)),
  });
  const upload = useMutation({
    mutationFn: uploadBackup,
    onSuccess: (b) => {
      toast.show({ title: 'Backup added.', body: `${b.name}. Restore it from the list when you are ready.` });
      refresh();
    },
    onError: (e) => toast.error('That archive was not accepted', problemText(e)),
  });
  const data = list.data;
  return (
    <div className="sys-stack">
      <HeaderActions>
        <input
          ref={fileRef}
          type="file"
          accept=".zip,application/zip"
          className="upload-input"
          tabIndex={-1}
          aria-hidden="true"
          onChange={(ev) => {
            const f = ev.target.files?.[0];
            ev.target.value = '';
            if (f) upload.mutate(f);
          }}
        />
        <Button
          icon={<Upload className="i" aria-hidden="true" />}
          loading={upload.isPending}
          onClick={() => fileRef.current?.click()}
        >
          Upload…
        </Button>
        <Button
          variant="primary"
          icon={<HardDrive className="i" aria-hidden="true" />}
          loading={create.isPending}
          onClick={() => create.mutate()}
        >
          Back up now
        </Button>
      </HeaderActions>
      {list.isPending ? (
        <Skeleton className="sk-wanted" />
      ) : !data ? (
        <p className="explain" role="alert">
          The backups could not be listed: {problemText(list.error)}{' '}
          <Button size="sm" onClick={() => void list.refetch()}>
            Try again
          </Button>
        </p>
      ) : (
        <>
          <PendingBanner data={data} />
          <Tiles data={data} settings={settings.data} />
          <ArchiveTable data={data} context={restoring?.name ?? null} onRestore={setRestoring} onDelete={setDeleting} />
        </>
      )}
      <div className="sys-cols bk-cols">
        <SchedulePanel settings={settings.data} />
        <MasterKeyPanel />
      </div>
      <RestoreDialog b={restoring} onClose={() => setRestoring(null)} />
      <DeleteDialog b={deleting} onClose={() => setDeleting(null)} />
    </div>
  );
}

export default BackupsSection;
