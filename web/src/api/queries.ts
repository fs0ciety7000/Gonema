import { type QueryClient, queryOptions, useMutation, useQuery, useQueryClient } from '@tanstack/react-query';
import { api, unwrap } from './client';
import { fromColumns, type LibraryItem, type MovieColumnsWire } from './model';
import { seedProgress } from './progress';
import type {
  HistoryEntry,
  MovieDetail,
  QualityProfileDetail as QualityProfile,
  QueueEntry as QueueItem,
  RootFolder,
  SavedView,
} from './schema';

type Movie = MovieDetail;

export const keys = {
  status: ['status'] as const,
  library: ['library'] as const,
  movie: (id: number) => ['movie', id] as const,
  history: (id: number) => ['movie', id, 'history'] as const,
  collection: (id: number) => ['collection', id] as const,
  profiles: ['quality-profiles'] as const,
  rootFolders: ['root-folders'] as const,
  queue: ['queue'] as const,
  health: ['health'] as const,
  views: (page: string) => ['views', page] as const,
};

/** Every movie of the library in one columnar response (GET /movies/ids): sorting and
 *  filtering run client-side, which is instant for 10,000 rows. */
export async function fetchLibrary(signal?: AbortSignal): Promise<LibraryItem[]> {
  const cols = await unwrap(api.GET('/movies/ids', { params: { query: { sort: 'added', order: 'desc' } }, signal }));
  return fromColumns(cols as unknown as MovieColumnsWire);
}

export const statusQuery = queryOptions({
  queryKey: keys.status,
  queryFn: ({ signal }) => unwrap(api.GET('/system/status', { signal })),
  staleTime: 60_000,
});

export const libraryQuery = queryOptions({
  queryKey: keys.library,
  queryFn: ({ signal }) => fetchLibrary(signal),
  staleTime: 30_000,
});

export const movieQuery = (id: number) =>
  queryOptions({
    queryKey: keys.movie(id),
    queryFn: ({ signal }) => unwrap(api.GET('/movies/{id}', { params: { path: { id } }, signal })),
    staleTime: 30_000,
  });

export const historyQuery = (id: number) =>
  queryOptions({
    queryKey: keys.history(id),
    queryFn: ({ signal }) =>
      unwrap(api.GET('/movies/{id}/history', { params: { path: { id }, query: { limit: 100 } }, signal })),
  });

export const collectionQuery = (id: number) =>
  queryOptions({
    queryKey: keys.collection(id),
    queryFn: ({ signal }) => unwrap(api.GET('/collections/{id}', { params: { path: { id } }, signal })),
    staleTime: 5 * 60_000,
  });

export const profilesQuery = queryOptions({
  queryKey: keys.profiles,
  queryFn: ({ signal }) => unwrap(api.GET('/quality-profiles', { signal })) as Promise<QualityProfile[]>,
  staleTime: 5 * 60_000,
});

export const rootFoldersQuery = queryOptions({
  queryKey: keys.rootFolders,
  queryFn: ({ signal }) => unwrap(api.GET('/root-folders', { signal })) as Promise<RootFolder[]>,
  staleTime: 60_000,
});

export const queueQuery = queryOptions({
  queryKey: keys.queue,
  queryFn: async ({ signal }) => {
    try {
      const items = ((await unwrap(api.GET('/queue', { signal }))) ?? []) as QueueItem[];
      seedProgress(items);
      return items;
    } catch (e) {
      // The acquisition endpoints are absent when the server runs without them.
      if ((e as { status?: number }).status === 404) return [] as QueueItem[];
      throw e;
    }
  },
  staleTime: 10_000,
});

export const healthQuery = queryOptions({
  queryKey: keys.health,
  queryFn: ({ signal }) => unwrap(api.GET('/health/issues', { signal })),
  staleTime: 60_000,
});

export const viewsQuery = (page: string) =>
  queryOptions({
    queryKey: keys.views(page),
    queryFn: async ({ signal }) =>
      ((await unwrap(api.GET('/views', { params: { query: { page } }, signal }))) ?? []) as SavedView[],
    staleTime: 5 * 60_000,
  });

export const useLibrary = () => useQuery(libraryQuery);
export const useMovie = (id: number) => useQuery(movieQuery(id));
export const useHistory = (id: number) => useQuery(historyQuery(id));
export const useQueue = () => useQuery(queueQuery);
export const useProfiles = () => useQuery(profilesQuery);
export const useRootFolders = () => useQuery(rootFoldersQuery);
export const useSystemStatus = () => useQuery(statusQuery);

// ----------------------------------------------------------------- mutations

export type MoviePatch = {
  monitored?: boolean;
  qualityProfileId?: number;
  minimumAvailability?: 'announced' | 'in_cinemas' | 'released';
  rootFolderId?: number;
  tags?: string[];
};

type Snapshot = { library?: LibraryItem[]; movies: Map<number, Movie | undefined> };

/** Applies a patch to the cached library and movie details; returns what to restore on failure. */
export function applyOptimistic(qc: QueryClient, ids: readonly number[], patch: MoviePatch): Snapshot {
  const set = new Set(ids);
  const snap: Snapshot = { library: qc.getQueryData<LibraryItem[]>(keys.library), movies: new Map() };
  if (snap.library) {
    qc.setQueryData<LibraryItem[]>(
      keys.library,
      snap.library.map((m) => (set.has(m.id) ? { ...m, ...patch } : m)),
    );
  }
  for (const id of ids) {
    const prev = qc.getQueryData<Movie>(keys.movie(id));
    snap.movies.set(id, prev);
    if (prev) qc.setQueryData<Movie>(keys.movie(id), { ...prev, ...patch } as Movie);
  }
  return snap;
}

export function restoreSnapshot(qc: QueryClient, snap: Snapshot) {
  if (snap.library) qc.setQueryData(keys.library, snap.library);
  for (const [id, m] of snap.movies) if (m) qc.setQueryData(keys.movie(id), m);
}

export async function patchMovie(id: number, patch: MoviePatch): Promise<Movie> {
  return unwrap(api.PATCH('/movies/{id}', { params: { path: { id } }, body: patch }));
}

/** Optimistic PATCH of one or more movies; rolls back the caches when any request fails. */
export function useUpdateMovies() {
  const qc = useQueryClient();
  return useMutation({
    mutationFn: async ({ ids, patch }: { ids: readonly number[]; patch: MoviePatch }) => {
      const results = await Promise.allSettled(ids.map((id) => patchMovie(id, patch)));
      const failed = results.find((r): r is PromiseRejectedResult => r.status === 'rejected');
      if (failed) throw failed.reason;
      return results.map((r) => (r as PromiseFulfilledResult<Movie>).value);
    },
    onMutate: async ({ ids, patch }) => {
      await qc.cancelQueries({ queryKey: keys.library });
      return applyOptimistic(qc, ids, patch);
    },
    onError: (_e, _v, snap) => {
      if (snap) restoreSnapshot(qc, snap);
    },
    onSuccess: (movies) => {
      for (const m of movies) qc.setQueryData(keys.movie(m.id), m);
    },
  });
}

export async function searchMovie(id: number) {
  return unwrap(api.POST('/movies/{id}/search', { params: { path: { id } } }));
}

export async function refreshMovie(id: number) {
  return unwrap(api.POST('/movies/{id}/refresh', { params: { path: { id } } }));
}

export async function deleteMovie(id: number) {
  return unwrap(api.DELETE('/movies/{id}', { params: { path: { id } } }));
}

export type { HistoryEntry, Movie, QueueItem, SavedView };
