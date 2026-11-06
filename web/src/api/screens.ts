/* API calls of the screens built in lot 2 (add & discover, interactive search, activity,
   library dialogs). Kept apart from queries.ts so the library's first load does not carry them. */
import { queryOptions } from '@tanstack/react-query';
import { API_ROOT, api, unwrap } from './client';
import { keys } from './queries';
import type {
  BulkResult,
  BulkSet,
  HistoryPage,
  LookupResult,
  MovieDetail,
  MovieSettings,
  paths,
  QueueItem,
  SearchStarted,
  Tag,
} from './schema';

export const screenKeys = {
  tmdb: ['settings', 'tmdb'] as const,
  lookup: (q: string) => ['lookup', q] as const,
  historyAll: (filter: string) => ['history', filter] as const,
  discover: (list: string, extra = '') => ['discover', list, extra] as const,
};

/** TMDB file name ("/abc.jpg" or "abc.jpg") → URL of the server's image cache. */
export function imageUrl(kind: 'poster' | 'backdrop' | 'profile', size: string, path: string | null | undefined) {
  if (!path) return null;
  const file = path.replace(/^\/+/, '');
  if (!/^[A-Za-z0-9_-]+\.(jpg|jpeg|png|webp)$/.test(file)) return null;
  return `${API_ROOT}/images/${kind}/${size}/${file}`;
}

export const tmdbQuery = queryOptions({
  queryKey: screenKeys.tmdb,
  queryFn: ({ signal }) => unwrap(api.GET('/settings/tmdb', { signal })),
  staleTime: 60_000,
});

export async function setTmdbKey(key: string) {
  return unwrap(api.PUT('/settings/tmdb', { body: { key } }));
}

export const lookupQuery = (q: string) =>
  queryOptions({
    queryKey: screenKeys.lookup(q),
    queryFn: async ({ signal }) =>
      ((await unwrap(api.GET('/lookup', { params: { query: { q } }, signal }))) ?? []) as LookupResult[],
    enabled: q.trim().length > 0,
    staleTime: 5 * 60_000,
    retry: false,
  });

export type AddInput = {
  tmdbId: number;
  rootFolderId: number;
  qualityProfileId: number;
  minimumAvailability: 'announced' | 'in_cinemas' | 'released';
  monitored: boolean;
  searchOnAdd: boolean;
  tags: string[];
};

export async function addMovie(input: AddInput): Promise<MovieDetail> {
  return unwrap(api.POST('/movies', { body: input }));
}

export async function monitorCollection(
  id: number,
  body: {
    monitored: boolean;
    qualityProfileId?: number;
    rootFolderId?: number;
    minimumAvailability: AddInput['minimumAvailability'];
    searchOnAdd?: boolean;
  },
) {
  return unwrap(api.PUT('/collections/{id}/monitor', { params: { path: { id } }, body }));
}

export async function removeMovie(id: number) {
  return unwrap(api.DELETE('/movies/{id}', { params: { path: { id } } }));
}

// ----------------------------------------------------------------- interactive search

export async function startInteractiveSearch(movieId: number): Promise<SearchStarted> {
  return unwrap(api.POST('/movies/{id}/search/interactive', { params: { path: { id: movieId } } }));
}

export async function getSearch(searchId: string, signal?: AbortSignal) {
  return unwrap(api.GET('/searches/{searchId}', { params: { path: { searchId } }, signal }));
}

export async function grabResult(searchId: string, key: string, override = false): Promise<QueueItem> {
  return unwrap(
    api.POST('/searches/{searchId}/grab', {
      params: { path: { searchId } },
      body: { key, override: override || undefined },
    }),
  );
}

/** Asks one failed indexer of an interactive search again (force: even while it is paused). */
export async function retrySearchIndexer(searchId: string, indexerId: number, force = true) {
  return unwrap(
    api.POST('/searches/{searchId}/indexers/{indexerId}/retry', {
      params: { path: { searchId, indexerId }, query: { force } },
    }),
  );
}

// ----------------------------------------------------------------- queue

export async function pauseDownload(id: number) {
  return unwrap(api.POST('/queue/{id}/pause', { params: { path: { id } } }));
}

export async function resumeDownload(id: number) {
  return unwrap(api.POST('/queue/{id}/resume', { params: { path: { id } } }));
}

export async function retryImport(id: number) {
  return unwrap(api.POST('/queue/{id}/import', { params: { path: { id } } }));
}

export type RemoveOptions = { blocklist: boolean; retry: boolean; deleteData: boolean };

export async function removeDownload(id: number, o: RemoveOptions) {
  return unwrap(
    api.DELETE('/queue/{id}', {
      params: {
        path: { id },
        query: {
          blocklist: o.blocklist || undefined,
          retry: o.retry || undefined,
          deleteData: o.deleteData || undefined,
        },
      },
    }),
  );
}

export const queueSummaryQuery = queryOptions({
  queryKey: [...keys.queue, 'summary'] as const,
  queryFn: async ({ signal }) => {
    try {
      return await unwrap(api.GET('/queue/summary', { signal }));
    } catch (e) {
      if ((e as { status?: number }).status === 404) return null;
      throw e;
    }
  },
  staleTime: 10_000,
});

// ----------------------------------------------------------------- history

export type HistoryEvent = NonNullable<NonNullable<paths['/history']['get']['parameters']['query']>['event']>;

export type HistoryQuery = {
  events?: HistoryEvent;
  movieId?: number;
  since?: string;
  cursor?: string;
  limit: number;
};

/** One page of GET /history (newest first, server-side filters, keyset cursor). */
export async function fetchHistory(q: HistoryQuery, signal?: AbortSignal): Promise<HistoryPage> {
  return unwrap(
    api.GET('/history', {
      params: {
        query: {
          event: q.events?.length ? q.events : undefined,
          movieId: q.movieId,
          since: q.since,
          cursor: q.cursor,
          limit: q.limit,
        },
      },
      // event is comma-separated on the server (explode: false)
      querySerializer: { array: { style: 'form', explode: false } },
      signal,
    }),
  );
}

// ----------------------------------------------------------------- library dialogs

export const tagsQuery = queryOptions({
  queryKey: ['tags'] as const,
  queryFn: async ({ signal }) => ((await unwrap(api.GET('/tags', { signal }))) ?? []) as Tag[],
  staleTime: 60_000,
});

export async function createTag(label: string): Promise<Tag> {
  return unwrap(api.POST('/tags', { body: { label } }));
}

export async function renameTag(id: number, label: string): Promise<Tag> {
  return unwrap(api.PUT('/tags/{id}', { params: { path: { id } }, body: { label } }));
}

export async function deleteTag(id: number) {
  return unwrap(api.DELETE('/tags/{id}', { params: { path: { id } } }));
}

/** One transaction for every film; the response carries the previous settings for Undo. */
export async function bulkUpdate(ids: number[], set: BulkSet): Promise<BulkResult> {
  return unwrap(api.POST('/movies/bulk', { body: { ids, set } }));
}

export async function restoreSettings(previous: MovieSettings[]) {
  return unwrap(api.POST('/movies/bulk/restore', { body: { previous } }));
}

export async function planMove(ids: number[], rootFolderId: number, moveFiles: boolean, preview: boolean) {
  return unwrap(
    api.POST('/movies/move', {
      params: { query: { preview: preview || undefined } },
      body: { ids, rootFolderId, moveFiles },
    }),
  );
}

export async function planRename(ids: number[], preview: boolean) {
  return unwrap(api.POST('/movies/rename', { params: { query: { preview: preview || undefined } }, body: { ids } }));
}

export async function deleteMovieFile(id: number) {
  return unwrap(api.DELETE('/movies/{id}/file', { params: { path: { id }, query: { confirm: true } } }));
}

/** Removes the film from the library and moves its files to the recycle bin (explicitly confirmed). */
export async function removeMovieWithFiles(id: number) {
  return unwrap(api.DELETE('/movies/{id}', { params: { path: { id }, query: { deleteFiles: true, confirm: true } } }));
}

export async function restoreRecycled(name: string) {
  return unwrap(api.POST('/recycle-bin/restore', { body: { name } }));
}

export const importSettingsQuery = queryOptions({
  queryKey: ['settings', 'import'] as const,
  queryFn: async ({ signal }) => {
    try {
      return await unwrap(api.GET('/import/settings', { signal }));
    } catch {
      return null;
    }
  },
  staleTime: 5 * 60_000,
});
