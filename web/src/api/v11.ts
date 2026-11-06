/* v1.1 endpoints: collections, the iCal feed, bulk search, folder import and stop/resume retrying.
   Imported by lazy screens only (the first screen's chunk stays as it was). */
import { queryOptions } from '@tanstack/react-query';
import { api, unwrap } from './client';
import type {
  BulkSearchProgress,
  BulkSearchStarted,
  CalendarFeedView,
  CalendarOptions,
  CollectionDetail,
  CollectionFilm,
  CollectionList,
  CollectionListItem,
  LibraryCandidate,
  LibraryFolder,
  LibraryImport,
  LibraryScan,
  LibrarySelection,
  MonitorInputBody,
  MonitorResult,
  QueueItem,
  Subfolder,
} from './schema';

export type {
  BulkSearchProgress,
  CalendarFeedView,
  CollectionDetail,
  CollectionFilm,
  CollectionListItem,
  LibraryCandidate,
  LibraryFolder,
  LibraryImport,
  LibraryScan,
  LibrarySelection,
  Subfolder,
};

export const v11Keys = {
  collections: (q: CollectionsQuery) => ['collections', q] as const,
  collectionsAll: ['collections'] as const,
  calendarFeed: ['settings', 'calendar-feed'] as const,
  scans: ['library-import', 'scans'] as const,
  scan: (id: number) => ['library-import', 'scan', id] as const,
  folders: (root: number, path: string) => ['library-import', 'folders', root, path] as const,
};

// ----------------------------------------------------------------- collections

export type CollectionSort = 'name' | 'missing' | 'year' | 'added';
export type CollectionsQuery = {
  sort: CollectionSort;
  show: 'all' | 'missing' | 'monitored';
  q: string;
};

export const COLLECTION_PAGE = 60;

export async function fetchCollections(
  q: CollectionsQuery,
  page: number,
  signal?: AbortSignal,
): Promise<CollectionList> {
  return unwrap(
    api.GET('/collections', {
      params: {
        query: {
          sort: q.sort,
          ...(q.show === 'missing' ? { missing: true } : {}),
          ...(q.show === 'monitored' ? { monitored: 'true' as const } : {}),
          ...(q.q.trim() ? { q: q.q.trim().slice(0, 200) } : {}),
          page,
          pageSize: COLLECTION_PAGE,
        },
      },
      signal,
    }),
  );
}

/** Same key as keys.collection (live.ts invalidates ['collection'] on collection.* events). */
export const collectionDetailQuery = (id: number) =>
  queryOptions({
    queryKey: ['collection', id] as const,
    queryFn: ({ signal }) =>
      unwrap(api.GET('/collections/{id}', { params: { path: { id } }, signal })) as Promise<CollectionDetail>,
    staleTime: 60_000,
  });

export async function monitorCollection(id: number, body: MonitorInputBody): Promise<MonitorResult> {
  return unwrap(api.PUT('/collections/{id}/monitor', { params: { path: { id } }, body }));
}

export async function addCollectionParts(
  tmdbId: number,
  body: {
    qualityProfileId: number;
    rootFolderId: number;
    minimumAvailability: 'announced' | 'in_cinemas' | 'released';
    monitored?: boolean;
    monitorCollection?: boolean;
    searchOnAdd?: boolean;
  },
) {
  return unwrap(api.POST('/collections/{tmdbId}/add', { params: { path: { tmdbId } }, body }));
}

// ----------------------------------------------------------------- iCal feed

export const calendarFeedQuery = queryOptions({
  queryKey: v11Keys.calendarFeed,
  queryFn: ({ signal }) => unwrap(api.GET('/settings/calendar-feed', { signal })) as Promise<CalendarFeedView>,
  staleTime: 30_000,
});

export async function putCalendarFeed(body: CalendarOptions): Promise<CalendarFeedView> {
  return unwrap(api.PUT('/settings/calendar-feed', { body }));
}

export async function regenerateCalendarFeed(): Promise<CalendarFeedView> {
  return unwrap(api.POST('/settings/calendar-feed/regenerate', {}));
}

export async function disableCalendarFeed() {
  return unwrap(api.DELETE('/settings/calendar-feed', { params: { query: { confirm: true } } }));
}

// ----------------------------------------------------------------- bulk search

export type BulkTarget = { ids: number[] } | { filter: 'missing_released' | 'cutoff_unmet' };

export async function startBulkSearch(target: BulkTarget): Promise<BulkSearchStarted> {
  return unwrap(api.POST('/movies/search', { body: target }));
}

export async function getBulkSearch(jobId: number, signal?: AbortSignal): Promise<BulkSearchProgress> {
  return unwrap(api.GET('/bulk-searches/{jobId}', { params: { path: { jobId } }, signal }));
}

export async function cancelBulkSearch(jobId: number): Promise<BulkSearchProgress> {
  return unwrap(api.DELETE('/bulk-searches/{jobId}', { params: { path: { jobId } } }));
}

// ----------------------------------------------------------------- folder import

export const foldersQuery = (rootFolderId: number, path: string) =>
  queryOptions({
    queryKey: v11Keys.folders(rootFolderId, path),
    queryFn: async ({ signal }) =>
      ((await unwrap(
        api.GET('/library-import/folders', {
          params: { query: { rootFolderId, ...(path ? { path } : {}) } },
          signal,
        }),
      )) ?? []) as Subfolder[],
    staleTime: 30_000,
    enabled: rootFolderId > 0,
  });

export const scansQuery = queryOptions({
  queryKey: v11Keys.scans,
  queryFn: async ({ signal }) => ((await unwrap(api.GET('/library-import/scans', { signal }))) ?? []) as LibraryScan[],
  staleTime: 10_000,
});

export const scanQuery = (id: number) =>
  queryOptions({
    queryKey: v11Keys.scan(id),
    queryFn: ({ signal }) =>
      unwrap(
        api.GET('/library-import/scans/{scanId}', { params: { path: { scanId: id } }, signal }),
      ) as Promise<LibraryScan>,
    enabled: id > 0,
  });

export async function startScan(rootFolderId: number, path: string): Promise<LibraryScan> {
  return unwrap(api.POST('/library-import/scans', { body: { rootFolderId, ...(path ? { path } : {}) } }));
}

export async function cancelScan(id: number): Promise<LibraryScan> {
  return unwrap(api.DELETE('/library-import/scans/{scanId}', { params: { path: { scanId: id } } }));
}

export async function searchCandidates(
  body: { rootFolderId: number; folder: string; query: string; year?: number },
  signal?: AbortSignal,
): Promise<LibraryCandidate[]> {
  return ((await unwrap(api.POST('/library-import/candidates', { body, signal }))) ?? []) as LibraryCandidate[];
}

export async function confirmImport(rootFolderId: number, selections: LibrarySelection[]): Promise<LibraryImport> {
  return unwrap(api.POST('/library-import/confirm', { body: { rootFolderId, selections } }));
}

// ----------------------------------------------------------------- retries

export async function stopRetrying(id: number): Promise<QueueItem> {
  return unwrap(api.POST('/queue/{id}/stop-retrying', { params: { path: { id } } }));
}

export async function resumeRetrying(id: number): Promise<QueueItem> {
  return unwrap(api.POST('/queue/{id}/resume-retrying', { params: { path: { id } } }));
}

export { posterThumb as thumbUrl } from './model';
