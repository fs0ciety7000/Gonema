import { useMutation, useQuery, useQueryClient } from '@tanstack/react-query';
import { useCallback, useMemo } from 'react';
import { api, unwrap } from '@/api/client';
import { keys, viewsQuery, type SavedView as WireView } from '@/api/queries';
import { toast } from '@/components/ui/Toast';
import {
  BUILTIN_VIEWS,
  type Density,
  type SavedView,
  type SortDir,
  type SortKey,
  validateLibrarySearch,
} from './model';

/* Saved views of the library: the built-in ones plus the views stored by the
   server (saved_views, shared until users exist). Filters and sort are stored as
   the same JSON the URL uses, validated again when read. */

const PAGE = 'library';

function fromWire(v: WireView): SavedView {
  const f = validateLibrarySearch((v.filters ?? {}) as Record<string, unknown>);
  const sort = (v.sort ?? {}) as { key?: unknown; dir?: unknown };
  const s = validateLibrarySearch({ sort: sort.key, dir: sort.dir });
  const { view: _v, layout: _l, density: _d, sort: _s, dir: _r, group: _g, ...filters } = f;
  return { id: `s${v.id}`, name: v.name, filters, sort: s.sort, dir: s.dir, density: v.density };
}

export function useSavedViews() {
  const qc = useQueryClient();
  const q = useQuery(viewsQuery(PAGE));
  const views = useMemo(() => [...BUILTIN_VIEWS, ...(q.data ?? []).map(fromWire)], [q.data]);

  const create = useMutation({
    mutationFn: (v: { name: string; filters: SavedView['filters']; sort?: SortKey; dir?: SortDir; density: Density }) =>
      unwrap(
        api.POST('/views', {
          body: {
            page: PAGE,
            name: v.name,
            filters: v.filters,
            sort: v.sort ? { key: v.sort, dir: v.dir } : {},
            density: v.density,
            position: (q.data?.length ?? 0) + 1,
          },
        }),
      ) as Promise<WireView>,
    onSuccess: () => void qc.invalidateQueries({ queryKey: keys.views(PAGE) }),
    onError: (e) => toast.error('The view was not saved', e instanceof Error ? e.message : undefined),
  });

  const add = useCallback(
    async (v: Parameters<typeof create.mutateAsync>[0]): Promise<string | null> => {
      try {
        const saved = await create.mutateAsync(v);
        return `s${saved.id}`;
      } catch {
        return null;
      }
    },
    [create],
  );

  return { views, add, saving: create.isPending };
}
