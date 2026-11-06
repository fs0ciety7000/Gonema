import { useQuery, useQueryClient } from '@tanstack/react-query';
import { Link } from '@tanstack/react-router';
import { Ban, Users } from 'lucide-react';
import { useMemo, useState } from 'react';
import { ApiError } from '@/api/client';
import { blocklistQuery, deleteBlocklistEntry, lot3Keys } from '@/api/lot3';
import { useLibrary } from '@/api/queries';
import type { BlocklistItem } from '@/api/schema';
import { Button } from '@/components/ui/Button';
import { EmptyState, Notice, Skeleton } from '@/components/ui/Feedback';
import { PosterArt } from '@/components/ui/PosterArt';
import { toast } from '@/components/ui/Toast';
import { formatWhen } from '@/lib/format';

/** Releases and groups Gonema will not grab again. Removing an entry applies at once with Undo:
 *  the DELETE is sent when the toast closes (nothing on disk is touched). */
export function BlocklistPanel() {
  const qc = useQueryClient();
  const list = useQuery(blocklistQuery);
  const lib = useLibrary();
  const [hidden, setHidden] = useState<ReadonlySet<number>>(new Set());
  const byId = useMemo(() => new Map((lib.data ?? []).map((m) => [m.id, m])), [lib.data]);
  const rows = (list.data ?? []).filter((b) => !hidden.has(b.id));

  const remove = (b: BlocklistItem) => {
    setHidden((h) => new Set(h).add(b.id));
    const restore = () =>
      setHidden((h) => {
        const n = new Set(h);
        n.delete(b.id);
        return n;
      });
    toast.show({
      title: b.kind === 'group' ? `Unblocked the group ${b.releaseGroup ?? ''}` : 'Removed from the blocklist',
      body: 'Gonema may grab it again if it is the best release.',
      icon: <Ban className="i" />,
      undo: restore,
      onClose: (reason) => {
        if (reason === 'undo') return;
        deleteBlocklistEntry(b.id)
          .then(() => qc.invalidateQueries({ queryKey: lot3Keys.blocklist }))
          .catch((e) => {
            restore();
            toast.error('Still blocklisted', e instanceof ApiError ? e.detail : 'Gonema could not be reached.');
          });
      },
    });
  };

  if (list.isError)
    return (
      <Notice
        tone="failed"
        role="alert"
        title="The blocklist could not be loaded."
        action={
          <Button size="sm" onClick={() => void list.refetch()}>
            Try again
          </Button>
        }
      >
        {(list.error as Error).message}
      </Notice>
    );
  if (list.isPending)
    return (
      <div className="hist" aria-hidden="true">
        {[0, 1, 2].map((i) => (
          <Skeleton key={i} className="sk-wanted" delay={i * 24} />
        ))}
      </div>
    );
  if (!rows.length)
    return (
      <EmptyState title="Nothing blocklisted.">
        When a download fails or a release turns out wrong, Gonema blocklists it here so it is never grabbed again.
      </EmptyState>
    );
  return (
    <div className="hist blocklist">
      <table className="tbl">
        <caption className="sr-only">Blocklisted releases and groups</caption>
        <thead>
          <tr>
            <th scope="col" style={{ width: '40%' }}>
              Release
            </th>
            <th scope="col">Film</th>
            <th scope="col">Reason</th>
            <th scope="col" style={{ width: 130 }}>
              Added
            </th>
            <th scope="col" style={{ width: 110 }}>
              <span className="sr-only">Actions</span>
            </th>
          </tr>
        </thead>
        <tbody>
          {rows.map((b) => {
            const m = b.movieId ? byId.get(b.movieId) : undefined;
            return (
              <tr key={b.id}>
                <td>
                  {b.kind === 'group' ? (
                    <span className="bl-rel">
                      <Users className="i i-sm" aria-hidden="true" />
                      <span>
                        Every release from <b className="t-mono-sm">{b.releaseGroup}</b>
                      </span>
                    </span>
                  ) : (
                    <span className="bl-rel t-mono-sm" title={b.sourceTitle}>
                      {b.sourceTitle}
                    </span>
                  )}
                  <span className="t-caption t-muted bl-sub">
                    {[b.kind === 'group' ? 'Whole group' : 'This release only', b.protocol].filter(Boolean).join(' · ')}
                  </span>
                </td>
                <td>
                  {b.movieId ? (
                    <Link className="film" to="/films/$movieId" params={{ movieId: b.movieId }}>
                      <PosterArt
                        className="thumb"
                        src={m?.posterUrl ?? null}
                        title={m?.title ?? ''}
                        color={m?.dominantColor}
                      />
                      <span>{m?.title ?? `Film #${b.movieId}`}</span>
                    </Link>
                  ) : (
                    <span className="t-muted">Any film</span>
                  )}
                </td>
                <td className="reason">{b.reason || '—'}</td>
                <td className="t-secondary tnum">{formatWhen(b.createdAt)}</td>
                <td className="ta-r">
                  <Button
                    size="sm"
                    variant="ghost"
                    onClick={() => remove(b)}
                    aria-label={`Remove ${b.kind === 'group' ? `group ${b.releaseGroup}` : b.sourceTitle} from the blocklist`}
                  >
                    Remove
                  </Button>
                </td>
              </tr>
            );
          })}
        </tbody>
      </table>
    </div>
  );
}
