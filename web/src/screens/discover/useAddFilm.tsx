import { type QueryClient, useQueryClient } from '@tanstack/react-query';
import { useNavigate } from '@tanstack/react-router';
import { Plus } from 'lucide-react';
import { useCallback, useState } from 'react';
import { ApiError } from '@/api/client';
import { keys } from '@/api/queries';
import type { LookupResult } from '@/api/schema';
import { addMovie, monitorCollection, removeMovie } from '@/api/screens';
import { toast } from '@/components/ui/Toast';
import { type AddOptions, addBody, saveAddOptions } from './addOptions';

export type AddTarget = { tmdbId: number; title: string; year?: number };

/** Marks a TMDB film as in the library (or not) in every cached list that shows it. */
export function markInLibrary(qc: QueryClient, tmdbId: number, movieId: number | null) {
  const patch = (r: { tmdbId: number; inLibrary?: boolean; movieId?: number | null }) =>
    r.tmdbId === tmdbId ? { ...r, inLibrary: movieId !== null, movieId: movieId ?? undefined } : r;
  qc.setQueriesData<LookupResult[]>({ queryKey: ['lookup'] }, (list) => list?.map(patch) as LookupResult[]);
  qc.setQueriesData<unknown>({ queryKey: ['discover'] }, (page: unknown) => mapDiscover(page, patch as never));
}

function mapDiscover(page: unknown, f: (r: never) => unknown): unknown {
  if (Array.isArray(page)) return page.map((r) => (r && typeof r === 'object' && 'tmdbId' in r ? f(r as never) : r));
  if (page && typeof page === 'object') {
    const o = page as Record<string, unknown>;
    const out: Record<string, unknown> = { ...o };
    for (const k of ['results', 'parts', 'movies', 'items']) if (Array.isArray(o[k])) out[k] = mapDiscover(o[k], f);
    return out;
  }
  return page;
}

function errorText(e: unknown) {
  return e instanceof ApiError ? (e.isNetwork ? 'Gonema could not be reached; nothing was added.' : e.detail) : '';
}

/** Adds a film with options; the toast offers Open and Undo (removes what was added). */
export function useAddFilm() {
  const qc = useQueryClient();
  const navigate = useNavigate();
  const [busy, setBusy] = useState<number | null>(null);

  const add = useCallback(
    async (t: AddTarget, o: AddOptions): Promise<number | null> => {
      saveAddOptions(o);
      setBusy(t.tmdbId);
      try {
        const movie = await addMovie(addBody(t.tmdbId, o));
        markInLibrary(qc, t.tmdbId, movie.id);
        const extra: number[] = [];
        let collectionName = '';
        let collectionId = 0;
        if (o.monitor === 'collection' && movie.collection?.id) {
          collectionId = movie.collection.id;
          collectionName = movie.collection.name;
          try {
            const r = await monitorCollection(collectionId, {
              monitored: true,
              qualityProfileId: o.qualityProfileId,
              rootFolderId: o.rootFolderId,
              minimumAvailability: o.minimumAvailability,
              searchOnAdd: o.searchOnAdd,
            });
            for (const m of r.added ?? []) if (m.id !== movie.id) extra.push(m.id);
          } catch (e) {
            toast.error(`${movie.title} was added, the rest of ${collectionName} was not`, errorText(e));
          }
        }
        void qc.invalidateQueries({ queryKey: keys.library });
        const body = [
          extra.length
            ? `With ${extra.length} more ${extra.length === 1 ? 'film' : 'films'} of ${collectionName}.`
            : '',
          o.monitor === 'none'
            ? 'Not monitored: Gonema will not search for it.'
            : o.searchOnAdd
              ? 'Searching for a release now.'
              : 'Monitored; the next scheduled search picks it up.',
        ]
          .filter(Boolean)
          .join(' ');
        toast.show({
          title: `Added ${movie.title}${movie.year ? ` (${movie.year})` : ''}`,
          body,
          icon: <Plus className="i" />,
          action: {
            label: 'Open',
            onClick: () => void navigate({ to: '/films/$movieId', params: { movieId: movie.id } }),
          },
          undo: () => {
            void (async () => {
              try {
                if (collectionId)
                  await monitorCollection(collectionId, {
                    monitored: false,
                    minimumAvailability: o.minimumAvailability,
                  });
                await Promise.all([movie.id, ...extra].map((id) => removeMovie(id)));
                markInLibrary(qc, t.tmdbId, null);
                toast.show({ title: `Removed ${movie.title} again`, body: 'Nothing else changed.' });
              } catch (e) {
                toast.error('The film could not be removed', errorText(e));
              } finally {
                void qc.invalidateQueries({ queryKey: keys.library });
              }
            })();
          },
        });
        return movie.id;
      } catch (e) {
        toast.error(`${t.title} was not added`, errorText(e));
        return null;
      } finally {
        setBusy(null);
      }
    },
    [qc, navigate],
  );

  return { add, busy };
}
