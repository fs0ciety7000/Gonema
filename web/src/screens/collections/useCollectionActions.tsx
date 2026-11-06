import { useQueryClient } from '@tanstack/react-query';
import { Bookmark, BookmarkMinus, Plus } from 'lucide-react';
import { useCallback, useMemo } from 'react';
import { ApiError } from '@/api/client';
import { keys, useLibrary, useProfiles, useRootFolders } from '@/api/queries';
import { addMovie, removeMovie } from '@/api/screens';
import { addCollectionParts, type CollectionListItem, monitorCollection } from '@/api/v11';
import { toast } from '@/components/ui/Toast';
import { formatNumber } from '@/lib/format';
import { addSettingsOf } from './collectionModel';

type Collection = Pick<
  CollectionListItem,
  'id' | 'tmdbId' | 'name' | 'monitored' | 'qualityProfileId' | 'rootFolderId' | 'searchOnAdd' | 'films'
> & { minimumAvailability: string };

const plural = (n: number, w: string) => `${formatNumber(n)} ${n === 1 ? w : `${w}s`}`;
const detailOf = (e: unknown) => (e instanceof ApiError ? e.detail : 'Gonema could not be reached.');

/** Monitor / add actions of a collection, applied at once with Undo (DESIGN.md: Annuler plutôt que confirmer). */
export function useCollectionActions() {
  const qc = useQueryClient();
  const lib = useLibrary();
  const profiles = useProfiles();
  const roots = useRootFolders();
  const byId = useMemo(() => new Map((lib.data ?? []).map((m) => [m.id, m])), [lib.data]);

  const refresh = useCallback(
    (id: number) => {
      void qc.invalidateQueries({ queryKey: ['collections'] });
      void qc.invalidateQueries({ queryKey: keys.collection(id) });
      void qc.invalidateQueries({ queryKey: keys.library });
    },
    [qc],
  );

  const settingsOf = useCallback(
    (c: Collection) => addSettingsOf(c, byId, profiles.data?.[0]?.id, roots.data?.[0]?.id),
    [byId, profiles.data, roots.data],
  );

  const removeAll = useCallback(
    (ids: number[], id: number) =>
      Promise.all(ids.map((m) => removeMovie(m)))
        .catch((e) => toast.error('Some films were not removed', detailOf(e)))
        .finally(() => refresh(id)),
    [refresh],
  );

  /** Turns monitoring on (adds the released films now) or off (keeps every film). */
  const setMonitored = useCallback(
    async (c: Collection, on: boolean, undoable = true): Promise<boolean> => {
      const s = settingsOf(c);
      if (on && !s) {
        toast.error('The collection was not monitored', 'Add a root folder and a quality profile first.');
        return false;
      }
      try {
        const r = await monitorCollection(c.id, { monitored: on, minimumAvailability: 'released', ...(s ?? {}) });
        refresh(c.id);
        const added = (r.added ?? []).map((m) => m.id);
        const upcoming = (r.upcoming ?? []).length;
        if (!undoable) return true;
        toast.show({
          title: on ? `Monitoring ${c.name}` : `Stopped monitoring ${c.name}`,
          body: on
            ? [
                added.length ? `Added ${plural(added.length, 'film')} now.` : 'Every released film is already here.',
                upcoming ? `${plural(upcoming, 'film')} join on their release day.` : '',
              ]
                .filter(Boolean)
                .join(' ')
            : 'Every film you have stays; new ones are no longer added.',
          icon: on ? <Bookmark className="i" /> : <BookmarkMinus className="i" />,
          undo: () =>
            void setMonitored(c, !on, false).then((ok) => {
              if (ok && added.length) void removeAll(added, c.id);
            }),
        });
        return true;
      } catch (e) {
        toast.error('The collection was not changed', detailOf(e));
        refresh(c.id);
        return false;
      }
    },
    [settingsOf, refresh, removeAll],
  );

  /** "Add the N missing": every film not in the library, with the collection's settings. */
  const addMissing = useCallback(
    async (c: Collection) => {
      const s = settingsOf(c);
      if (!s) {
        toast.error('Nothing was added', 'Add a root folder and a quality profile first.');
        return;
      }
      try {
        const r = await addCollectionParts(c.tmdbId, {
          ...s,
          monitored: true,
          monitorCollection: c.monitored,
        });
        refresh(c.id);
        const added = (r.added ?? []).map((m) => m.id);
        const skipped = (r.skipped ?? []).length;
        toast.show({
          title: added.length ? `Added ${plural(added.length, 'film')} of ${c.name}` : 'Nothing to add',
          body: [
            s.searchOnAdd
              ? 'Gonema searches for the released ones now.'
              : 'Gonema searches for them at the next check.',
            skipped ? `${formatNumber(skipped)} skipped (on your exclusion list).` : '',
          ]
            .filter(Boolean)
            .join(' '),
          icon: <Plus className="i" />,
          undo: added.length ? () => void removeAll(added, c.id) : undefined,
        });
      } catch (e) {
        toast.error('Nothing was added', detailOf(e));
      }
    },
    [settingsOf, refresh, removeAll],
  );

  /** Adds one film of the collection (a part not in the library). */
  const addOne = useCallback(
    async (c: Collection, film: { tmdbId: number; title: string }) => {
      const s = settingsOf(c);
      if (!s) {
        toast.error(`${film.title} was not added`, 'Add a root folder and a quality profile first.');
        return;
      }
      try {
        const m = await addMovie({ tmdbId: film.tmdbId, ...s, monitored: true, tags: [] });
        refresh(c.id);
        toast.show({
          title: `Added ${film.title}`,
          body: s.searchOnAdd ? 'Gonema searches for it now.' : 'Gonema searches for it at the next check.',
          icon: <Plus className="i" />,
          undo: () => void removeAll([m.id], c.id),
        });
      } catch (e) {
        toast.error(`${film.title} was not added`, detailOf(e));
      }
    },
    [settingsOf, refresh, removeAll],
  );

  return useMemo(
    () => ({ settingsOf, setMonitored, addMissing, addOne, refresh }),
    [settingsOf, setMonitored, addMissing, addOne, refresh],
  );
}
