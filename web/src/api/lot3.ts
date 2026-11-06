/* API calls of the screens built in lot 3 (home, calendar, wanted, blocklist, health, settings,
   onboarding, command palette). Kept apart from queries.ts so the library's first load does not carry them. */
import { queryOptions } from '@tanstack/react-query';
import { api, unwrap } from './client';
import { keys } from './queries';
import type {
  AcquisitionSettings,
  BlocklistItem,
  CheckStatus,
  DownloadClient,
  DownloadClientInput,
  HealthIssue,
  ImportSettingsBody,
  Indexer,
  IndexerInput,
  LibraryStats,
  MediaServer,
  MediaServerInput,
  MoviePage,
  NamingSettingsView,
  Notification,
  NotificationInput,
  RemotePathMapping,
  RootFolder,
  Settings as HealthSettings,
  TestResult,
  UiPreferences,
  PreviewInput,
  QualityProfileInput,
  ScoringCondition,
  ScoringRuleInput,
} from './schema';

/** Lists answered with `null` when empty. */
const arr = <T>(v: T[] | null | undefined): T[] => v ?? [];

/** Absent endpoint (server built without the feature) → empty value instead of an error screen. */
async function orEmpty<T>(p: Promise<T>, empty: T): Promise<T> {
  try {
    return await p;
  } catch (e) {
    if ((e as { status?: number }).status === 404) return empty;
    throw e;
  }
}

export const lot3Keys = {
  stats: ['library', 'stats'] as const,
  wanted: (kind: string) => ['wanted', kind] as const,
  blocklist: ['blocklist'] as const,
  healthChecks: ['health', 'checks'] as const,
  indexers: ['indexers'] as const,
  clients: ['download-clients'] as const,
  mediaServers: ['media-servers'] as const,
  notifications: ['notifications'] as const,
  naming: ['settings', 'naming'] as const,
  acquisition: ['settings', 'acquisition'] as const,
  healthSettings: ['settings', 'health'] as const,
  importSettings: ['settings', 'import'] as const,
  pathMappings: ['remote-path-mappings'] as const,
  tags: ['tags'] as const,
  jobs: ['jobs'] as const,
};

export const statsQuery = queryOptions({
  queryKey: lot3Keys.stats,
  queryFn: ({ signal }) => unwrap(api.GET('/library/stats', { signal })) as Promise<LibraryStats>,
  staleTime: 30_000,
});

// ----------------------------------------------------------------- wanted

export type WantedKind = 'missing' | 'cutoff_unmet';

export async function fetchWanted(kind: WantedKind, cursor: string | undefined, signal?: AbortSignal) {
  return unwrap(
    api.GET('/wanted', { params: { query: { kind, cursor, limit: 100, sort: 'added', order: 'desc' } }, signal }),
  ) as Promise<MoviePage>;
}

// ----------------------------------------------------------------- blocklist

export const blocklistQuery = queryOptions({
  queryKey: lot3Keys.blocklist,
  queryFn: async ({ signal }) =>
    orEmpty(
      unwrap(api.GET('/blocklist', { params: { query: { limit: 500 } }, signal })).then((r) =>
        arr(r as BlocklistItem[] | null),
      ),
      [] as BlocklistItem[],
    ),
  staleTime: 30_000,
});

export async function deleteBlocklistEntry(id: number) {
  return unwrap(api.DELETE('/blocklist/{id}', { params: { path: { id } } }));
}

export async function blockGroup(body: { group: string; movieId?: number; reason?: string }) {
  return unwrap(api.POST('/blocklist/groups', { body })) as Promise<BlocklistItem>;
}

// ----------------------------------------------------------------- health

export const healthIssuesQuery = queryOptions({
  queryKey: keys.health,
  queryFn: async ({ signal }) =>
    orEmpty(
      unwrap(api.GET('/health/issues', { signal })).then((r) => arr(r as HealthIssue[] | null)),
      [] as HealthIssue[],
    ),
  staleTime: 60_000,
});

export const healthChecksQuery = queryOptions({
  queryKey: lot3Keys.healthChecks,
  queryFn: async ({ signal }) =>
    orEmpty(
      unwrap(api.GET('/health/checks', { signal })).then((r) => arr(r as CheckStatus[] | null)),
      [] as CheckStatus[],
    ),
  staleTime: 60_000,
});

export async function runHealthChecks() {
  return arr((await unwrap(api.POST('/health/run', {}))) as HealthIssue[] | null);
}

// ----------------------------------------------------------------- connections

export const indexersQuery = queryOptions({
  queryKey: lot3Keys.indexers,
  queryFn: async ({ signal }) =>
    orEmpty(unwrap(api.GET('/indexers', { signal })).then((r) => arr(r as Indexer[] | null)), [] as Indexer[]),
  staleTime: 30_000,
});

export const clientsQuery = queryOptions({
  queryKey: lot3Keys.clients,
  queryFn: async ({ signal }) =>
    orEmpty(
      unwrap(api.GET('/download-clients', { signal })).then((r) => arr(r as DownloadClient[] | null)),
      [] as DownloadClient[],
    ),
  staleTime: 30_000,
});

export const mediaServersQuery = queryOptions({
  queryKey: lot3Keys.mediaServers,
  queryFn: async ({ signal }) =>
    orEmpty(
      unwrap(api.GET('/media-servers', { signal })).then((r) => arr(r as MediaServer[] | null)),
      [] as MediaServer[],
    ),
  staleTime: 30_000,
});

export const notificationsQuery = queryOptions({
  queryKey: lot3Keys.notifications,
  queryFn: async ({ signal }) =>
    orEmpty(
      unwrap(api.GET('/notifications', { signal })).then((r) => arr(r as Notification[] | null)),
      [] as Notification[],
    ),
  staleTime: 30_000,
});

export const pathMappingsQuery = queryOptions({
  queryKey: lot3Keys.pathMappings,
  queryFn: async ({ signal }) =>
    orEmpty(
      unwrap(api.GET('/remote-path-mappings', { signal })).then((r) => arr(r as RemotePathMapping[] | null)),
      [] as RemotePathMapping[],
    ),
  staleTime: 60_000,
});

export const indexerApi = {
  create: (b: IndexerInput) => unwrap(api.POST('/indexers', { body: b })) as Promise<Indexer>,
  update: (id: number, b: IndexerInput) =>
    unwrap(api.PUT('/indexers/{id}', { params: { path: { id } }, body: b })) as Promise<Indexer>,
  remove: (id: number) => unwrap(api.DELETE('/indexers/{id}', { params: { path: { id } } })),
  /** Tests the saved indexer with its stored secrets. */
  testSaved: (id: number) =>
    unwrap(api.POST('/indexers/{id}/test', { params: { path: { id } } })) as Promise<TestResult>,
  /** Tests unsaved settings; with an id, blank secrets fall back to the stored ones. */
  test: (b: IndexerInput, id?: number) =>
    unwrap(api.POST('/indexers/test', { params: { query: { id } }, body: b })) as Promise<TestResult>,
};

export const clientApi = {
  create: (b: DownloadClientInput) => unwrap(api.POST('/download-clients', { body: b })) as Promise<DownloadClient>,
  update: (id: number, b: DownloadClientInput) =>
    unwrap(api.PUT('/download-clients/{id}', { params: { path: { id } }, body: b })) as Promise<DownloadClient>,
  remove: (id: number) => unwrap(api.DELETE('/download-clients/{id}', { params: { path: { id } } })),
  testSaved: (id: number) =>
    unwrap(api.POST('/download-clients/{id}/test', { params: { path: { id } } })) as Promise<TestResult>,
  test: (b: DownloadClientInput, id?: number) =>
    unwrap(
      api.POST('/download-clients/test', { params: { query: { id } }, body: b }),
    ) as Promise<TestResult>,
};

export const mediaServerApi = {
  create: (b: MediaServerInput) => unwrap(api.POST('/media-servers', { body: b })) as Promise<MediaServer>,
  update: (id: number, b: MediaServerInput) =>
    unwrap(api.PUT('/media-servers/{id}', { params: { path: { id } }, body: b })) as Promise<MediaServer>,
  remove: (id: number) => unwrap(api.DELETE('/media-servers/{id}', { params: { path: { id } } })),
  test: (b: MediaServerInput, id?: number) =>
    unwrap(api.POST('/media-servers/test', { params: { query: { id } }, body: b })) as Promise<TestResult>,
};

export const notificationApi = {
  create: (b: NotificationInput) => unwrap(api.POST('/notifications', { body: b })) as Promise<Notification>,
  update: (id: number, b: NotificationInput) =>
    unwrap(api.PUT('/notifications/{id}', { params: { path: { id } }, body: b })) as Promise<Notification>,
  remove: (id: number) => unwrap(api.DELETE('/notifications/{id}', { params: { path: { id } } })),
  test: (b: NotificationInput, id?: number) =>
    unwrap(api.POST('/notifications/test', { params: { query: { id } }, body: b })) as Promise<TestResult>,
};

// ----------------------------------------------------------------- library settings

export async function addRootFolder(path: string) {
  return unwrap(api.POST('/root-folders', { body: { path } })) as Promise<RootFolder>;
}

export async function removeRootFolder(id: number) {
  return unwrap(api.DELETE('/root-folders/{id}', { params: { path: { id } } }));
}

export const namingQuery = queryOptions({
  queryKey: lot3Keys.naming,
  queryFn: ({ signal }) => unwrap(api.GET('/settings/naming', { signal })) as Promise<NamingSettingsView>,
  staleTime: 60_000,
});

export type NamingInput = { fileTemplate: string; folderTemplate: string; renameFiles: boolean };

export async function putNaming(b: NamingInput) {
  return unwrap(api.PUT('/settings/naming', { body: b })) as Promise<NamingSettingsView>;
}

export async function previewNaming(b: NamingInput & { movieId?: number }, signal?: AbortSignal) {
  return unwrap(api.POST('/settings/naming/preview', { body: b, signal }));
}

export const importSettingsQuery = queryOptions({
  queryKey: lot3Keys.importSettings,
  queryFn: ({ signal }) => unwrap(api.GET('/import/settings', { signal })) as Promise<ImportSettingsBody>,
  staleTime: 60_000,
});

export async function putImportSettings(b: ImportSettingsBody) {
  return unwrap(api.PUT('/import/settings', { body: b })) as Promise<ImportSettingsBody>;
}

export const acquisitionQuery = queryOptions({
  queryKey: lot3Keys.acquisition,
  queryFn: ({ signal }) => unwrap(api.GET('/settings/acquisition', { signal })) as Promise<AcquisitionSettings>,
  staleTime: 60_000,
});

export async function putAcquisition(b: AcquisitionSettings) {
  return unwrap(api.PUT('/settings/acquisition', { body: b })) as Promise<AcquisitionSettings>;
}

export const healthSettingsQuery = queryOptions({
  queryKey: lot3Keys.healthSettings,
  queryFn: ({ signal }) => unwrap(api.GET('/settings/health', { signal })) as Promise<HealthSettings>,
  staleTime: 60_000,
});

export async function putHealthSettings(b: HealthSettings) {
  return unwrap(api.PUT('/settings/health', { body: b })) as Promise<HealthSettings>;
}

// ----------------------------------------------------------------- general settings (UI preferences)

export const generalSettingsQuery = queryOptions({
  queryKey: ['settings', 'general'] as const,
  queryFn: ({ signal }) => unwrap(api.GET('/settings/general', { signal })),
  staleTime: 60_000,
});

export async function putUiPreferences(b: UiPreferences) {
  return unwrap(api.PUT('/settings/ui', { body: b }));
}

// ----------------------------------------------------------------- queue (all downloads)

/** Pauses or resumes every Gonema download in its client; the result says what each client did. */
export async function queueControl(what: 'pause' | 'resume') {
  return what === 'pause' ? unwrap(api.POST('/queue/pause', {})) : unwrap(api.POST('/queue/resume', {}));
}

// ----------------------------------------------------------------- dashboard, calendar

export const dashboardQuery = queryOptions({
  queryKey: ['dashboard'] as const,
  queryFn: ({ signal }) => unwrap(api.GET('/dashboard', { signal })),
  staleTime: 15_000,
});

export const calendarQuery = (start: string, end: string, unmonitored: boolean) =>
  queryOptions({
    queryKey: ['calendar', start, end, unmonitored] as const,
    queryFn: async ({ signal }) =>
      unwrap(api.GET('/calendar', { params: { query: { start, end, unmonitored: unmonitored || undefined } }, signal })),
    staleTime: 60_000,
  });

// ----------------------------------------------------------------- palette search

/** Library films (fast, every keystroke); with remote, TMDB films and people too (debounced). */
export const paletteSearchQuery = (q: string, remote: boolean) =>
  queryOptions({
    queryKey: ['palette-search', q, remote] as const,
    queryFn: ({ signal }) => unwrap(api.GET('/search', { params: { query: { q, limit: 6, remote: remote || undefined } }, signal })),
    staleTime: 30_000,
    retry: false,
  });

// ----------------------------------------------------------------- people (Discover)

export const peopleSearchQuery = (query: string) =>
  queryOptions({
    queryKey: ['discover', 'people', query] as const,
    queryFn: ({ signal }) => unwrap(api.GET('/discover/people', { params: { query: { query } }, signal })),
    enabled: query.trim().length >= 2,
    staleTime: 5 * 60_000,
    retry: false,
  });

// ----------------------------------------------------------------- quality profiles & scoring rules

export const qualitiesQuery = queryOptions({
  queryKey: ['qualities'] as const,
  queryFn: async ({ signal }) => (await unwrap(api.GET('/qualities', { signal }))) ?? [],
  staleTime: Number.POSITIVE_INFINITY,
});

export const scoringRulesQuery = queryOptions({
  queryKey: ['scoring-rules'] as const,
  queryFn: async ({ signal }) => (await unwrap(api.GET('/scoring-rules', { signal }))) ?? [],
  staleTime: 60_000,
});

export const conditionKindsQuery = queryOptions({
  queryKey: ['scoring-rules', 'kinds'] as const,
  queryFn: async ({ signal }) => (await unwrap(api.GET('/scoring-rules/kinds', { signal }))) ?? [],
  staleTime: Number.POSITIVE_INFINITY,
});

export const profileApi = {
  create: (b: QualityProfileInput) => unwrap(api.POST('/quality-profiles', { body: b })),
  update: (id: number, b: QualityProfileInput) =>
    unwrap(api.PUT('/quality-profiles/{id}', { params: { path: { id } }, body: b })),
  remove: (id: number) => unwrap(api.DELETE('/quality-profiles/{id}', { params: { path: { id } } })),
};

export const ruleApi = {
  create: (b: ScoringRuleInput) => unwrap(api.POST('/scoring-rules', { body: b })),
  update: (id: number, b: ScoringRuleInput) =>
    unwrap(api.PUT('/scoring-rules/{id}', { params: { path: { id } }, body: b })),
  remove: (id: number) => unwrap(api.DELETE('/scoring-rules/{id}', { params: { path: { id } } })),
  matches: (conditions: ScoringCondition[], signal?: AbortSignal) =>
    unwrap(api.POST('/scoring-rules/matches', { body: { conditions, limit: 6 }, signal })),
};

export async function previewRelease(b: PreviewInput, signal?: AbortSignal) {
  return unwrap(api.POST('/quality/preview', { body: b, signal }));
}
