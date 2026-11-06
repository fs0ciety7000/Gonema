import { useQuery, useQueryClient } from '@tanstack/react-query';
import { Link } from '@tanstack/react-router';
import { Layers, Plus } from 'lucide-react';
import { useState } from 'react';
import { ApiError, api, unwrap } from '@/api/client';
import { collectionQuery, keys } from '@/api/queries';
import { imageUrl } from '@/api/screens';
import { StatusText } from '@/components/ui/Badges';
import { Button } from '@/components/ui/Button';
import { Checkbox } from '@/components/ui/Checkbox';
import { Dialog } from '@/components/ui/Dialog';
import { Notice, Skeleton } from '@/components/ui/Feedback';
import { PosterArt } from '@/components/ui/PosterArt';
import { toast } from '@/components/ui/Toast';
import { displayStatus } from '@/lib/status';

type Defaults = {
  qualityProfileId: number;
  rootFolderId: number;
  minimumAvailability: 'announced' | 'in_cinemas' | 'released';
};

/** The whole collection: every TMDB part, which ones are in the library, and adding the missing ones. */
export default function CollectionDialog({
  id,
  name,
  currentId,
  open,
  onOpenChange,
  defaults,
}: {
  id: number;
  name: string;
  currentId: number;
  open: boolean;
  onOpenChange: (o: boolean) => void;
  defaults: Defaults;
}) {
  const qc = useQueryClient();
  const c = useQuery(collectionQuery(id));
  const [monitor, setMonitor] = useState(true);
  const [busy, setBusy] = useState(false);
  const d = c.data;
  const owned = new Map((d?.movies ?? []).map((m) => [m.tmdbId, m]));
  const missing = (d?.parts ?? []).filter((p) => !owned.has(p.tmdbId));

  const addMissing = async () => {
    if (!d) return;
    setBusy(true);
    try {
      const r = await unwrap(
        api.POST('/collections/{tmdbId}/add', {
          params: { path: { tmdbId: d.tmdbId } },
          body: { ...defaults, monitored: true, monitorCollection: monitor, searchOnAdd: true },
        }),
      );
      const added = r.added ?? [];
      void qc.invalidateQueries({ queryKey: keys.library });
      void qc.invalidateQueries({ queryKey: keys.collection(id) });
      void qc.invalidateQueries({ queryKey: keys.movie(currentId) });
      toast.show({
        title: added.length
          ? `Added ${added.length} ${added.length === 1 ? 'film' : 'films'} of ${d.name}`
          : 'Nothing to add',
        body: [
          monitor ? 'The collection is monitored: new films are added when announced.' : '',
          (r.skipped ?? []).length ? `${(r.skipped ?? []).length} skipped (on your exclusion list).` : '',
        ]
          .filter(Boolean)
          .join(' '),
        icon: <Plus className="i" />,
        undo: added.length
          ? () =>
              void Promise.all(added.map((m) => unwrap(api.DELETE('/movies/{id}', { params: { path: { id: m.id } } }))))
                .then(() => void qc.invalidateQueries({ queryKey: keys.library }))
                .catch((e) => toast.error('The films were not removed', e instanceof ApiError ? e.detail : undefined))
          : undefined,
      });
    } catch (e) {
      toast.error('Nothing was added', e instanceof ApiError ? e.detail : undefined);
    } finally {
      setBusy(false);
    }
  };

  return (
    <Dialog
      open={open}
      onOpenChange={onOpenChange}
      wide
      icon={<Layers className="i" aria-hidden="true" />}
      title={d?.name ?? name}
      description={d?.overview || undefined}
      actions={
        <>
          <Button variant="ghost" onClick={() => onOpenChange(false)}>
            Close
          </Button>
          {missing.length > 0 && (
            <Button variant="primary" loading={busy} onClick={() => void addMissing()}>
              Add the {missing.length} missing
            </Button>
          )}
        </>
      }
    >
      {c.isPending ? (
        <Skeleton className="sk-line" />
      ) : c.isError ? (
        <Notice tone="failed" title="The collection could not be loaded.">
          {c.error.message}
        </Notice>
      ) : (
        <>
          {d?.partsError && (
            <Notice title="TMDB did not answer.">
              Only the films already in your library are listed. {d.partsError}
            </Notice>
          )}
          <ul className="coll-parts">
            {(d?.parts ?? d?.movies ?? []).map((p) => {
              const m = owned.get(p.tmdbId);
              const st = m
                ? displayStatus({ state: m.state as never, monitored: m.monitored, failed: false })
                : 'missing';
              return (
                <li key={p.tmdbId}>
                  <PosterArt
                    src={imageUrl('poster', 'w342', p.posterPath)}
                    title={p.title}
                    year={p.year ?? undefined}
                  />
                  <div>
                    {m ? (
                      <Link
                        to="/films/$movieId"
                        params={{ movieId: m.id }}
                        aria-current={m.id === currentId ? 'page' : undefined}
                      >
                        {p.title}
                      </Link>
                    ) : (
                      <b>{p.title}</b>
                    )}
                    <StatusText status={st}>
                      {p.year || 'Date unknown'}
                      {m ? '' : ' · not in library'}
                    </StatusText>
                  </div>
                </li>
              );
            })}
          </ul>
          {missing.length > 0 && (
            <Checkbox
              checked={monitor}
              onCheckedChange={setMonitor}
              label="Also monitor the collection: add its future films automatically"
            />
          )}
        </>
      )}
    </Dialog>
  );
}
