import { useSyncExternalStore } from 'react';

/* Live download progress, one entry per queue item. download.progress events land
   here and only the row that shows the item re-renders (per-row subscriptions):
   the queue list itself renders again only when items or statuses change. */

export type Progress = {
  progress: number;
  speedBps: number;
  etaSeconds: number | null;
  status: string;
};

const values = new Map<number, Progress>();
const subs = new Map<number, Set<() => void>>();

function notify(id: number) {
  const s = subs.get(id);
  if (s) for (const cb of s) cb();
}

/** Merges a (possibly partial) update; ignores values that are not numbers. */
export function setProgress(
  id: number,
  p: { progress?: number; speedBps?: number; etaSeconds?: number | null; status?: string },
) {
  const prev = values.get(id);
  const next: Progress = {
    progress: typeof p.progress === 'number' && Number.isFinite(p.progress) ? p.progress : (prev?.progress ?? 0),
    speedBps: typeof p.speedBps === 'number' ? p.speedBps : (prev?.speedBps ?? 0),
    etaSeconds: p.etaSeconds !== undefined ? p.etaSeconds : (prev?.etaSeconds ?? null),
    status: p.status ?? prev?.status ?? 'queued',
  };
  if (
    prev &&
    prev.progress === next.progress &&
    prev.speedBps === next.speedBps &&
    prev.etaSeconds === next.etaSeconds &&
    prev.status === next.status
  )
    return;
  values.set(id, next);
  notify(id);
}

/** Seeds the store from a fresh GET /queue (the list is authoritative when it arrives). */
export function seedProgress(items: ReadonlyArray<{ id: number } & Partial<Progress>>) {
  for (const it of items) setProgress(it.id, it);
}

export function getProgress(id: number): Progress | undefined {
  return values.get(id);
}

export function resetProgress() {
  values.clear();
}

export function useProgress(id: number, fallback: Progress): Progress {
  return useSyncExternalStore(
    (cb) => {
      let s = subs.get(id);
      if (!s) {
        s = new Set();
        subs.set(id, s);
      }
      s.add(cb);
      return () => {
        s.delete(cb);
        if (!s.size) subs.delete(id);
      };
    },
    () => values.get(id) ?? fallback,
  );
}
