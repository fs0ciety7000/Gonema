import '@/styles/phase8.css';
import { useMutation, useQuery, useQueryClient } from '@tanstack/react-query';
import { Link } from '@tanstack/react-router';
import { FileVideo, HardDrive, Lock } from 'lucide-react';
import {
  backupSettingsQuery,
  backupsQuery,
  createBackup,
  LOG_LEVELS,
  logSettingsQuery,
  opsKeys,
  putLogLevel,
  securityQuery,
} from '@/api/ops';
import { problemText } from '@/components/auth/AuthParts';
import { StatusText } from '@/components/ui/Badges';
import { Button, buttonClass } from '@/components/ui/Button';
import { Skeleton } from '@/components/ui/Feedback';
import { Segmented } from '@/components/ui/Segmented';
import { toast } from '@/components/ui/Toast';
import { formatBytes, formatWhen } from '@/lib/format';
import { everyLabel } from './BackupsSection';
import { RecentLogLines } from './LogsSection';

const go = (section: string, label: string) => (
  <Link className={buttonClass('ghost', 'sm')} to="/settings/$section" params={{ section }}>
    {label}
  </Link>
);

/** Settings overview: the Security, Backups and Logs cards of settings-quality.html?section=index. */
export function SystemCards() {
  const qc = useQueryClient();
  const sec = useQuery(securityQuery);
  const backups = useQuery(backupsQuery);
  const schedule = useQuery(backupSettingsQuery);
  const logs = useQuery(logSettingsQuery);
  const create = useMutation({
    mutationFn: createBackup,
    onSuccess: (b) => {
      toast.show({ title: 'Backup made.', body: `${b.name} · ${formatBytes(b.size)}` });
      void qc.invalidateQueries({ queryKey: opsKeys.backups });
    },
    onError: (e) => toast.error('The backup failed', problemText(e)),
  });
  const level = useMutation({
    mutationFn: putLogLevel,
    onSuccess: (r) => qc.setQueryData(opsKeys.logSettings, r),
    onError: (e) => toast.error('The log level did not change', problemText(e)),
  });
  const s = sec.data;
  const others = (s?.sessions ?? []).length;
  const last = backups.data?.backups?.[0];
  return (
    <>
      <section className="panel" aria-labelledby="sys-security">
        <div className="panel-hd">
          <h2 id="sys-security">
            <Lock className="i" aria-hidden="true" />
            Security
          </h2>
          {go('security', 'Manage')}
        </div>
        <div className="panel-bd">
          {!s ? (
            <Skeleton className="sk-row" />
          ) : (
            <ul className="mini-rows">
              <li>
                <span>
                  Sign-in
                  <small>
                    {s.authMode === 'disabled_for_local' ? 'Off for this network' : 'Login page'}
                    {s.user ? ` · ${s.user.username}` : ' · no account yet'}
                  </small>
                </span>
              </li>
              <li>
                <span>
                  API key
                  <small className="t-mono-sm">
                    {s.apiKey.present
                      ? `gnm_••••••••${s.apiKey.last4 ?? ''}${s.apiKey.lastUsedAt ? ` · used ${formatWhen(s.apiKey.lastUsedAt)}` : ''}`
                      : 'none'}
                  </small>
                </span>
              </li>
              <li>
                <span>
                  Signed-in browsers
                  <small>{others === 1 ? '1 browser' : `${others} browsers`}</small>
                </span>
              </li>
            </ul>
          )}
        </div>
      </section>
      <section className="panel" aria-labelledby="sys-backups">
        <div className="panel-hd">
          <h2 id="sys-backups">
            <HardDrive className="i" aria-hidden="true" />
            Backups
          </h2>
          {go('backups', 'Manage')}
        </div>
        <div className="panel-bd">
          {!schedule.data || backups.isPending ? (
            <Skeleton className="sk-row" />
          ) : (
            <ul className="mini-rows">
              <li>
                <span>
                  {schedule.data.enabled ? everyLabel(schedule.data.intervalHours) : 'No schedule'}
                  <small>
                    Keep {schedule.data.keep}
                    {backups.data ? ` · ${backups.data.directory}` : ''}
                  </small>
                </span>
              </li>
              <li>
                <span>
                  Last backup
                  <small>{last ? `${formatWhen(last.createdAt)} · ${formatBytes(last.size)}` : 'None yet'}</small>
                </span>
                {last &&
                  (last.restorable ? (
                    <StatusText status="available">OK</StatusText>
                  ) : (
                    <StatusText status="failed">Unusable</StatusText>
                  ))}
              </li>
            </ul>
          )}
          <div className="row">
            <Button size="sm" loading={create.isPending} onClick={() => create.mutate()}>
              Back up now
            </Button>
            {go('backups', 'Restore…')}
          </div>
          <span className="t-caption t-muted">
            The master key is not in backups. Keep <span className="t-mono-sm">gonema.key</span> somewhere safe.
          </span>
        </div>
      </section>
      <section className="panel span2" aria-labelledby="sys-logs">
        <div className="panel-hd">
          <h2 id="sys-logs">
            <FileVideo className="i" aria-hidden="true" />
            Logs
          </h2>
          <div className="row">
            <span className="t-caption t-muted" aria-hidden="true">
              Level
            </span>
            {logs.data && (
              <Segmented
                label="Server log level"
                value={logs.data.level}
                onChange={(l) => level.mutate(l)}
                options={LOG_LEVELS.map((l) => ({ value: l, label: l[0]!.toUpperCase() + l.slice(1) }))}
              />
            )}
            {go('logs', 'Open')}
          </div>
        </div>
        <div className="panel-bd">
          <RecentLogLines />
        </div>
      </section>
    </>
  );
}

export default SystemCards;
