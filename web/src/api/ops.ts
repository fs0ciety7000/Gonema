/* Phase 8: security settings, backups, logs and the import journal. Lazy (settings and health only). */
import { queryOptions } from '@tanstack/react-query';
import { API_ROOT, ApiError, api, unwrap } from './client';
import type { BackupSettings, ImportJournalEntry, LogEntry } from './schema';

export const opsKeys = {
  security: ['settings', 'security'] as const,
  backups: ['backups'] as const,
  backupSettings: ['settings', 'backups'] as const,
  logSettings: ['settings', 'logs'] as const,
  logs: (level: string, q: string) => ['logs', level, q] as const,
  journal: ['imports', 'journal'] as const,
};

export type LogLevel = LogEntry['level'];
export const LOG_LEVELS: readonly LogLevel[] = ['debug', 'info', 'warn', 'error'];

// ----------------------------------------------------------------- security

export const securityQuery = queryOptions({
  queryKey: opsKeys.security,
  queryFn: ({ signal }) => unwrap(api.GET('/settings/security', { signal })),
  staleTime: 10_000,
});

/** Changes the password. Rate limited like sign-in: the Response is returned for Retry-After. */
export async function changePassword(currentPassword: string, newPassword: string) {
  const { data, error, response } = await api.PUT('/auth/password', { body: { currentPassword, newPassword } });
  if (!response.ok) {
    const detail = (error as { detail?: string } | undefined)?.detail ?? `The server answered ${response.status}.`;
    throw Object.assign(new ApiError(response.status, detail), { response });
  }
  return data!;
}

export const regenerateApiKey = () => unwrap(api.POST('/auth/api-key'));

export const revokeSession = (id: number) => unwrap(api.DELETE('/auth/sessions/{id}', { params: { path: { id } } }));

// ----------------------------------------------------------------- backups

export const backupsQuery = queryOptions({
  queryKey: opsKeys.backups,
  queryFn: ({ signal }) => unwrap(api.GET('/backups', { signal })),
  staleTime: 10_000,
});

export const backupSettingsQuery = queryOptions({
  queryKey: opsKeys.backupSettings,
  queryFn: ({ signal }) => unwrap(api.GET('/settings/backups', { signal })),
  staleTime: 60_000,
});

export const createBackup = () => unwrap(api.POST('/backups', { body: { includeKey: false } }));

export const putBackupSettings = (b: BackupSettings) => unwrap(api.PUT('/settings/backups', { body: b }));

export const deleteBackup = (name: string) =>
  unwrap(api.DELETE('/backups/{name}', { params: { path: { name }, query: { confirm: true } } }));

export const restoreBackup = (name: string, restoreKey: boolean) =>
  unwrap(api.POST('/backups/restore', { body: { name, confirm: true, restoreKey } }));

export const cancelRestore = () => unwrap(api.DELETE('/backups/restore'));

/** Sends an archive from this computer (multipart, field "file"). */
export function uploadBackup(file: File) {
  return unwrap(
    api.POST('/backups/upload', {
      body: { file } as never,
      bodySerializer: () => {
        const fd = new FormData();
        fd.append('file', file);
        return fd;
      },
    }),
  );
}

/** Same-origin link: the session cookie authenticates the download. */
export const backupDownloadUrl = (name: string) => `${API_ROOT}/backups/${encodeURIComponent(name)}/download`;

// ----------------------------------------------------------------- logs

export const logSettingsQuery = queryOptions({
  queryKey: opsKeys.logSettings,
  queryFn: ({ signal }) => unwrap(api.GET('/settings/logs', { signal })),
  staleTime: 30_000,
});

export const putLogLevel = (level: LogLevel) => unwrap(api.PUT('/settings/logs', { body: { level } }));

export const logsQuery = (level: LogLevel, q: string, limit = 2000) =>
  queryOptions({
    queryKey: opsKeys.logs(level, q),
    queryFn: ({ signal }) =>
      unwrap(api.GET('/logs', { params: { query: { level, q: q || undefined, limit } }, signal })),
    staleTime: 0,
  });

export const logStreamUrl = (level: LogLevel) => `${API_ROOT}/logs/stream?level=${level}`;
export const logDownloadUrl = `${API_ROOT}/logs/download`;

/** Validates one SSE log entry (external input). */
export function parseLogEntry(raw: unknown): LogEntry | null {
  if (!raw || typeof raw !== 'object') return null;
  const e = raw as Record<string, unknown>;
  if (typeof e.id !== 'number' || typeof e.time !== 'string' || typeof e.message !== 'string') return null;
  if (!LOG_LEVELS.includes(e.level as LogLevel)) return null;
  const attrs: Record<string, string> = {};
  if (e.attrs && typeof e.attrs === 'object')
    for (const [k, v] of Object.entries(e.attrs as Record<string, unknown>)) attrs[k] = String(v);
  return { id: e.id, time: e.time, level: e.level as LogLevel, message: e.message, attrs };
}

// ----------------------------------------------------------------- import journal

export const journalQuery = queryOptions({
  queryKey: opsKeys.journal,
  queryFn: async ({ signal }) =>
    ((await unwrap(api.GET('/imports/journal', { signal }))) ?? []) as ImportJournalEntry[],
  staleTime: 30_000,
});

export const dismissJournal = (id: number) =>
  unwrap(api.POST('/imports/journal/{id}/dismiss', { params: { path: { id }, query: { confirm: true } } }));
