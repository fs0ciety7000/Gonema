import { useQueryClient } from '@tanstack/react-query';
import { Bookmark, BookmarkMinus, Check, Search, Trash2 } from 'lucide-react';
import { useCallback } from 'react';
import { ApiError } from '@/api/client';
import type { LibraryItem } from '@/api/model';
import {
  applyOptimistic,
  deleteMovie,
  keys,
  type MoviePatch,
  patchMovie,
  refreshMovie,
  restoreSnapshot,
  searchMovie,
} from '@/api/queries';
import { toast } from '@/components/ui/Toast';

type Target = { id: number; title: string };

const plural = (n: number, one: string, many = `${one}s`) => `${n.toLocaleString('en-US')} ${n === 1 ? one : many}`;

function errorText(e: unknown): string {
  if (e instanceof ApiError) return e.isNetwork ? 'Gonema could not be reached; nothing was changed.' : e.detail;
  return 'Something went wrong; nothing was changed.';
}

/** Library actions with optimistic updates, Undo toasts and rollback on failure (motion.md §5). */
export function useMovieActions() {
  const qc = useQueryClient();

  /** Optimistic PATCH of several movies; rolls the caches back and says so when a request fails. */
  const patch = useCallback(
    async (ids: readonly number[], p: MoviePatch): Promise<boolean> => {
      await qc.cancelQueries({ queryKey: keys.library });
      const snap = applyOptimistic(qc, ids, p);
      const results = await Promise.allSettled(ids.map((id) => patchMovie(id, p)));
      const failed = results.filter((r): r is PromiseRejectedResult => r.status === 'rejected');
      results.forEach((r) => {
        if (r.status === 'fulfilled') qc.setQueryData(keys.movie(r.value.id), r.value);
      });
      if (failed.length) {
        restoreSnapshot(qc, snap);
        void qc.invalidateQueries({ queryKey: keys.library });
        toast.error(
          failed.length === ids.length
            ? 'Nothing was changed'
            : `${plural(failed.length, 'film')} could not be changed`,
          errorText(failed[0]?.reason),
        );
        return false;
      }
      return true;
    },
    [qc],
  );

  /** Monitor or stop monitoring; offers Undo, which restores each film's previous value. */
  const setMonitored = useCallback(
    async (targets: readonly Target[], monitored: boolean, previous?: ReadonlyMap<number, boolean>) => {
      if (!targets.length) return;
      const ok = await patch(
        targets.map((t) => t.id),
        { monitored },
      );
      if (!ok) return;
      const one = targets.length === 1 ? targets[0] : undefined;
      const title = monitored
        ? one
          ? `Monitoring ${one.title}`
          : `Monitoring ${plural(targets.length, 'film')}`
        : one
          ? `Stopped monitoring ${one.title}`
          : `Stopped monitoring ${plural(targets.length, 'film')}`;
      toast.show({
        title,
        body: monitored
          ? 'Gonema searches for it and for upgrades.'
          : 'Gonema will not search for it until you monitor it again.',
        icon: monitored ? <Bookmark className="i" /> : <BookmarkMinus className="i" />,
        undo: () => {
          if (previous) {
            const back = targets.filter((t) => previous.get(t.id) !== monitored);
            void patch(
              back.map((t) => t.id),
              { monitored: !monitored },
            );
          } else
            void patch(
              targets.map((t) => t.id),
              { monitored: !monitored },
            );
        },
      });
    },
    [patch],
  );

  /** One transaction for all films (POST /movies/bulk); Undo puts back the previous settings the server returned. */
  const setProfile = useCallback(
    async (
      targets: readonly Target[],
      profileId: number,
      profileName: string,
      previous: ReadonlyMap<number, number>,
    ) => {
      const ids = targets.map((t) => t.id);
      await qc.cancelQueries({ queryKey: keys.library });
      const snap = applyOptimistic(qc, ids, { qualityProfileId: profileId });
      let restore: (() => Promise<unknown>) | null = null;
      try {
        const { bulkUpdate, restoreSettings } = await import('@/api/screens');
        const r = await bulkUpdate(ids, { qualityProfileId: profileId });
        const prev = r.previous ?? [];
        restore = () => restoreSettings(prev);
      } catch (e) {
        if (!(e instanceof ApiError) || e.status !== 404) {
          restoreSnapshot(qc, snap);
          toast.error('Nothing was changed', errorText(e));
          return;
        }
        // Older server without the bulk endpoint: one PATCH per film.
        if (!(await patch(ids, { qualityProfileId: profileId }))) return;
      }
      for (const id of ids) void qc.invalidateQueries({ queryKey: keys.movie(id) });
      toast.show({
        title:
          targets.length === 1
            ? `${targets[0]?.title} now uses ${profileName}`
            : `${plural(targets.length, 'film')} now use ${profileName}`,
        icon: <Check className="i" />,
        undo: () => {
          const byProfile = new Map<number, number[]>();
          for (const t of targets) {
            const p = previous.get(t.id);
            if (p && p !== profileId) byProfile.set(p, [...(byProfile.get(p) ?? []), t.id]);
          }
          for (const [p, pids] of byProfile) applyOptimistic(qc, pids, { qualityProfileId: p });
          const run =
            restore ?? (() => Promise.all([...byProfile].map(([p, pids]) => patch(pids, { qualityProfileId: p }))));
          void run()
            .catch((e) => {
              toast.error('The previous profiles were not restored', errorText(e));
            })
            .finally(() => void qc.invalidateQueries({ queryKey: keys.library }));
        },
      });
    },
    [patch, qc],
  );

  const search = useCallback(async (targets: readonly Target[]) => {
    if (!targets.length) return;
    const results = await Promise.allSettled(targets.map((t) => searchMovie(t.id)));
    const failed = results.filter((r): r is PromiseRejectedResult => r.status === 'rejected');
    if (failed.length === targets.length) {
      toast.error('The search did not start', errorText(failed[0]?.reason));
      return;
    }
    toast.show({
      title:
        targets.length === 1 ? `Searching for ${targets[0]?.title}` : `Searching for ${plural(targets.length, 'film')}`,
      body: 'The best release that fits the profile is grabbed; History shows why.',
      icon: <Search className="i" />,
    });
  }, []);

  const refresh = useCallback(async (t: Target) => {
    try {
      await refreshMovie(t.id);
      toast.show({
        title: `Refreshing ${t.title}`,
        body: 'New details from TMDB appear in a moment.',
        icon: <Check className="i" />,
      });
    } catch (e) {
      toast.error('The refresh did not start', errorText(e));
    }
  }, []);

  const copyPath = useCallback(async (path: string) => {
    try {
      await navigator.clipboard.writeText(path);
      toast.show({
        title: 'Path copied',
        body: <span className="t-mono-sm">{path}</span>,
        icon: <Check className="i" />,
      });
    } catch {
      toast.error('The path could not be copied', 'The browser refused clipboard access.');
    }
  }, []);

  /** Removes films from the library (never their files). Applied at once, committed when the Undo toast goes away. */
  const remove = useCallback(
    (targets: readonly Target[], onRemoved?: () => void) => {
      if (!targets.length) return;
      const ids = new Set(targets.map((t) => t.id));
      const before = qc.getQueryData<LibraryItem[]>(keys.library);
      if (before)
        qc.setQueryData<LibraryItem[]>(
          keys.library,
          before.filter((m) => !ids.has(m.id)),
        );
      onRemoved?.();
      const restore = () => {
        const now = qc.getQueryData<LibraryItem[]>(keys.library) ?? [];
        const back = (before ?? []).filter((m) => ids.has(m.id) && !now.some((n) => n.id === m.id));
        qc.setQueryData<LibraryItem[]>(keys.library, [...now, ...back]);
      };
      toast.show({
        title:
          targets.length === 1
            ? `Removed ${targets[0]?.title} from the library`
            : `Removed ${plural(targets.length, 'film')}`,
        body: 'Files stay on disk.',
        icon: <Trash2 className="i" />,
        undo: restore,
        onClose: (reason) => {
          if (reason === 'undo') return;
          void Promise.allSettled(targets.map((t) => deleteMovie(t.id))).then((rs) => {
            const failed = rs.filter((r) => r.status === 'rejected');
            if (failed.length) {
              restore();
              toast.error(
                `${plural(failed.length, 'film')} could not be removed`,
                errorText((failed[0] as PromiseRejectedResult).reason),
              );
            }
            void qc.invalidateQueries({ queryKey: keys.library });
          });
        },
      });
    },
    [qc],
  );

  return { patch, setMonitored, setProfile, search, refresh, copyPath, remove };
}

export function joinPath(root: string | undefined, rel: string): string {
  if (!root) return rel;
  return `${root.replace(/\/+$/, '')}/${rel.replace(/^\/+/, '')}`;
}
