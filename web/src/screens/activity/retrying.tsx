import { useQueryClient } from '@tanstack/react-query';
import { CircleStop, RefreshCw } from 'lucide-react';
import { useCallback } from 'react';
import { ApiError } from '@/api/client';
import { keys, type QueueItem } from '@/api/queries';
import { resumeRetrying, stopRetrying } from '@/api/v11';
import { toast } from '@/components/ui/Toast';

/** Stop / resume the automatic next-release retries of a download's chain, applied at once with
 *  Undo (the opposite call). The current download is not touched. */
export function useRetrying() {
  const qc = useQueryClient();
  const patch = useCallback(
    (id: number, stopped: boolean) =>
      qc.setQueryData<QueueItem[]>(keys.queue, (prev) =>
        prev?.map((q) =>
          q.id === id
            ? {
                ...q,
                retryStopped: stopped,
                actions: { ...q.actions, stopRetrying: !stopped, resumeRetrying: stopped },
              }
            : q,
        ),
      ),
    [qc],
  );
  const set = useCallback(
    async (id: number, title: string, stop: boolean, undoable = true) => {
      patch(id, stop);
      try {
        if (stop) await stopRetrying(id);
        else await resumeRetrying(id);
        void qc.invalidateQueries({ queryKey: keys.queue });
        void qc.invalidateQueries({ queryKey: ['history', 'all'] });
        if (!undoable) return;
        toast.show({
          title: stop ? `Stopped retrying ${title}` : `Retrying ${title} again`,
          body: stop
            ? 'If this download fails, Gonema won’t grab the next release on its own. RSS and scheduled searches still run.'
            : 'If a download fails, Gonema grabs the next best release on its own.',
          icon: stop ? <CircleStop className="i" /> : <RefreshCw className="i" />,
          undo: () => void set(id, title, !stop, false),
        });
      } catch (e) {
        patch(id, !stop);
        toast.error(
          stop ? `Gonema still retries ${title}` : `Retries of ${title} are still stopped`,
          e instanceof ApiError ? e.detail : 'Gonema could not be reached.',
        );
      }
    },
    [patch, qc],
  );
  return set;
}
