import { useMutation, useQuery, useQueryClient } from '@tanstack/react-query';
import { ArchiveRestore, Download, HardDrive, Trash2, TriangleAlert, Upload } from 'lucide-react';
import { useRef, useState } from 'react';
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
import type { BackupInfo, BackupSettings, RestoreStaged } from '@/api/schema';
import { problemText } from '@/components/auth/AuthParts';
import { Button, buttonClass } from '@/components/ui/Button';
import { Checkbox } from '@/components/ui/Checkbox';
import { Dialog } from '@/components/ui/Dialog';
import { Notice, Skeleton } from '@/components/ui/Feedback';
import { toast } from '@/components/ui/Toast';
import { formatBytes, formatWhen } from '@/lib/format';
import type { FieldSpec } from './form';
import { Panel, SettingsForm } from './Sections';

const KIND: Record<BackupInfo['kind'], string> = {
  manual: 'Made by hand',
  scheduled: 'Scheduled',
  'pre-restore': 'Before a restore',
  uploaded: 'Uploaded',
};

/** "Every 7 days", "Every day", "Every 6 hours" */
export function everyLabel(hours: number): string {
  if (hours % 24 === 0) return hours === 24 ? 'Every day' : `Every ${hours / 24} days`;
  return hours === 1 ? 'Every hour' : `Every ${hours} hours`;
}

const SCHEDULE: readonly FieldSpec[] = [
  { kind: 'switch', name: 'enabled', label: 'Back up on a schedule' },
  {
    kind: 'number',
    name: 'intervalHours',
    label: 'Every (hours)',
    min: 1,
    max: 720,
    hint: '24 = daily, 168 = weekly. Up to 30 days.',
  },
  {
    kind: 'number',
    name: 'keep',
    label: 'Scheduled backups kept',
    min: 1,
    max: 100,
    hint: 'Older scheduled backups are deleted; backups made by hand are kept until you delete them.',
  },
  {
    kind: 'switch',
    name: 'includeKey',
    label: 'Include the master key in scheduled backups',
    hint: 'Off is safer: with the key inside, whoever gets the file can read every stored password and API key.',
  },
];

function RestoreDialog({ b, onClose }: { b: BackupInfo | null; onClose: () => void }) {
  const qc = useQueryClient();
  const [withKey, setWithKey] = useState(false);
  const [result, setResult] = useState<RestoreStaged | null>(null);
  const m = useMutation({
    mutationFn: () => restoreBackup(b!.name, withKey),
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
  return (
    <Dialog
      open={!!b}
      onOpenChange={(o) => !o && close()}
      icon={<ArchiveRestore className="i" aria-hidden="true" />}
      title={result ? 'Restore staged' : 'Restore this backup?'}
      description={
        result
          ? result.message
          : b && (
              <>
                <span className="t-mono-sm">{b.name}</span> ({formatWhen(b.createdAt)}, Gonema {b.appVersion}) replaces
                the current database when Gonema restarts.
              </>
            )
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
            <Button variant="danger" loading={m.isPending} onClick={() => m.mutate()}>
              Back up now, then stage the restore
            </Button>
          </>
        )
      }
    >
      {result ? (
        <div className="restore-opts">
          <ul>
            <li>
              The current state was saved first as <span className="t-mono-sm">{result.preRestoreBackup.name}</span>.
            </li>
            <li>Restart Gonema to finish. Until then nothing changes, and you can cancel from this page.</li>
            {(result.warnings ?? []).map((w) => (
              <li key={w}>{w}</li>
            ))}
          </ul>
        </div>
      ) : (
        b && (
          <div className="restore-opts">
            <ul>
              <li>
                <strong>A backup of the current state is made first</strong> (kept as “Before a restore”), so you can
                come back to it.
              </li>
              <li>
                Films, settings, history and connections become those of the backup. Files on disk are not touched.
              </li>
              <li>
                <strong>The master key is not included</strong> unless the archive says so: keep{' '}
                <span className="t-mono-sm">gonema.key</span> from this server. Passwords and API keys saved in the
                backup can only be read with the key that was in use when it was made.
              </li>
            </ul>
            {b.includesKey && (
              <Checkbox
                checked={withKey}
                onCheckedChange={setWithKey}
                label="Also restore the master key from this archive (it holds one)"
              />
            )}
          </div>
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
  const cancel = useMutation({
    mutationFn: cancelRestore,
    onSuccess: () => {
      toast.show({ title: 'Restore canceled.', body: 'The current database stays as it is.' });
      refresh();
    },
    onError: (e) => toast.error('The restore is still staged', problemText(e)),
  });
  const data = list.data;
  const pending = data?.pendingRestore;
  return (
    <>
      {pending && (
        <Notice
          tone="missing"
          icon={<ArchiveRestore className="i" aria-hidden="true" />}
          title="A restore is waiting for a restart."
          action={
            <Button size="sm" loading={cancel.isPending} onClick={() => cancel.mutate()}>
              Cancel restore
            </Button>
          }
        >
          <span className="t-mono-sm">{pending.name}</span> replaces the current database when Gonema restarts (staged{' '}
          {formatWhen(pending.stagedAt)}
          {pending.restoreKey ? ', with its master key' : ''}).
        </Notice>
      )}
      <Panel
        id="bk-list"
        title={
          <>
            <HardDrive className="i" aria-hidden="true" />
            Backups
          </>
        }
        aside={
          <div className="row">
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
              size="sm"
              variant="ghost"
              icon={<Upload className="i" aria-hidden="true" />}
              loading={upload.isPending}
              onClick={() => fileRef.current?.click()}
            >
              Upload…
            </Button>
            <Button size="sm" loading={create.isPending} onClick={() => create.mutate()}>
              Back up now
            </Button>
          </div>
        }
      >
        {list.isPending ? (
          <Skeleton className="sk-wanted" />
        ) : !data ? (
          <p className="explain" role="alert">
            The backups could not be listed: {problemText(list.error)}
          </p>
        ) : !data.backups?.length ? (
          <p className="explain">
            No backup yet. “Back up now” makes one in a few seconds, while Gonema keeps running.
          </p>
        ) : (
          <ul className="mini-rows backups">
            {data.backups.map((b) => (
              <li key={b.name}>
                <span>
                  <span className="bname">{b.name}</span>
                  <small>
                    {[
                      KIND[b.kind] ?? b.kind,
                      formatWhen(b.createdAt),
                      formatBytes(b.size),
                      `Gonema ${b.appVersion}`,
                    ].join(' · ')}
                  </small>
                  {b.includesKey && (
                    <small className="key-warn">
                      <TriangleAlert className="i i-xs" aria-hidden="true" /> Holds the master key: keep this file
                      private.
                    </small>
                  )}
                  {!b.restorable && b.problem && <small className="err">{b.problem}</small>}
                </span>
                <a
                  className={buttonClass('ghost', 'sm')}
                  href={backupDownloadUrl(b.name)}
                  download={b.name}
                  aria-label={`Download ${b.name}`}
                >
                  <Download className="i" aria-hidden="true" />
                  Download
                </a>
                <Button
                  size="sm"
                  variant="ghost"
                  disabled={!b.restorable}
                  title={b.restorable ? undefined : b.problem}
                  onClick={() => setRestoring(b)}
                  aria-label={`Restore ${b.name}…`}
                >
                  Restore…
                </Button>
                <Button size="sm" variant="ghost" onClick={() => setDeleting(b)} aria-label={`Delete ${b.name}…`}>
                  Delete…
                </Button>
              </li>
            ))}
          </ul>
        )}
        {data && (
          <p className="t-caption t-muted">
            Stored in <span className="t-mono-sm">{data.directory}</span>.{' '}
            {data.keyNotice ||
              'The master key is not in backups unless you ask for it. Keep gonema.key somewhere safe.'}
          </p>
        )}
      </Panel>
      <SettingsForm<BackupSettings>
        id="bk-schedule"
        title="Schedule"
        spec={SCHEDULE}
        data={settings.data}
        save={putBackupSettings}
        queryKey={opsKeys.backupSettings}
        intro={
          settings.data && (
            <p className="explain">
              {settings.data.enabled
                ? `${everyLabel(settings.data.intervalHours)}, keeping the last ${settings.data.keep}.`
                : 'Scheduled backups are off. Backups made by hand still work.'}
            </p>
          )
        }
      />
      <RestoreDialog b={restoring} onClose={() => setRestoring(null)} />
      <DeleteDialog b={deleting} onClose={() => setDeleting(null)} />
    </>
  );
}

export default BackupsSection;
