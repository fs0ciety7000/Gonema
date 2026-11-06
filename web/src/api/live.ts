import { type QueryClient, useQueryClient } from '@tanstack/react-query';
import { useEffect, useSyncExternalStore } from 'react';
import { API_ROOT } from './client';
import { type ConnectionState, EventStream, type ServerEvent } from './events';
import { setProgress } from './progress';
import { keys, type QueueItem } from './queries';

/* One event stream per tab. Events keep the query cache fresh:
   - download.progress patches the queue in place (no refetch every second);
   - other events invalidate what they touch, library refetches are coalesced;
   - "resync" (missed events) invalidates everything. */

let state: ConnectionState = { status: 'connecting', attempt: 0, nextRetryAt: null, lostAt: null };
const listeners = new Set<() => void>();
let stream: EventStream | null = null;

function setState(s: ConnectionState) {
  state = s;
  for (const l of listeners) l();
}

export function useConnection(): ConnectionState {
  return useSyncExternalStore(
    (cb) => {
      listeners.add(cb);
      return () => listeners.delete(cb);
    },
    () => state,
  );
}

export function retryConnectionNow() {
  stream?.retryNow();
}

const LIBRARY_DEBOUNCE_MS = 1000;

/* Screens that follow a stream of their own (interactive search) subscribe here. */
const eventListeners = new Set<(e: ServerEvent) => void>();

export function onServerEvent(cb: (e: ServerEvent) => void): () => void {
  eventListeners.add(cb);
  return () => eventListeners.delete(cb);
}

export function emitServerEvent(e: ServerEvent) {
  for (const l of eventListeners) l(e);
}

type ProgressWire = Partial<QueueItem> & { id: number };

export function handleServerEvent(qc: QueryClient, e: ServerEvent, scheduleLibrary: () => void) {
  const movieId = e.movieId;
  if (e.type === 'download.progress') {
    // Compact payload {id, progress, speedBps, etaSeconds, status} or a full queue item.
    const p = e.data as ProgressWire | undefined;
    if (!p || typeof p.id !== 'number') return;
    setProgress(p.id, p);
    let unknown = false;
    let statusChanged = false;
    qc.setQueryData<QueueItem[]>(keys.queue, (prev) => {
      if (!prev) return prev;
      const i = prev.findIndex((q) => q.id === p.id);
      if (i < 0) {
        // A download the list does not know yet: fetch the list (the event may be compact).
        unknown = true;
        return prev;
      }
      const old = prev[i]!;
      statusChanged = p.status !== undefined && p.status !== old.status;
      const next = prev.slice();
      next[i] = { ...old, ...p };
      return next;
    });
    if (unknown) void qc.invalidateQueries({ queryKey: keys.queue });
    if (statusChanged) scheduleLibrary();
    return;
  }
  if (e.type.startsWith('download.')) {
    void qc.invalidateQueries({ queryKey: keys.queue });
    void qc.invalidateQueries({ queryKey: ['history', 'all'] });
    scheduleLibrary();
    if (movieId) {
      void qc.invalidateQueries({ queryKey: keys.movie(movieId) });
    }
    return;
  }
  if (e.type.startsWith('movie.')) {
    scheduleLibrary();
    if (movieId) {
      if (e.type === 'movie.deleted') qc.removeQueries({ queryKey: keys.movie(movieId) });
      else void qc.invalidateQueries({ queryKey: keys.movie(movieId) });
    }
    return;
  }
  if (e.type.startsWith('moviefile.')) {
    scheduleLibrary();
    if (movieId) void qc.invalidateQueries({ queryKey: keys.movie(movieId) });
    return;
  }
  if (e.type === 'tags.changed') {
    void qc.invalidateQueries({ queryKey: ['tags'] });
    scheduleLibrary();
    return;
  }
  if (e.type === 'health.changed') {
    void qc.invalidateQueries({ queryKey: keys.health });
    return;
  }
  if (e.type.startsWith('collection.')) {
    void qc.invalidateQueries({ queryKey: ['collection'] });
    return;
  }
  if (e.type.startsWith('job.') && movieId) {
    void qc.invalidateQueries({ queryKey: keys.history(movieId) });
  }
}

/** Mount once (in the app shell). */
export function useServerEvents() {
  const qc = useQueryClient();
  useEffect(() => {
    let timer: ReturnType<typeof setTimeout> | null = null;
    const scheduleLibrary = () => {
      if (timer) return;
      timer = setTimeout(() => {
        timer = null;
        void qc.invalidateQueries({ queryKey: keys.library });
      }, LIBRARY_DEBOUNCE_MS);
    };
    let wasDown = false;
    const s = new EventStream({
      url: `${API_ROOT}/events`,
      onEvent: (e) => {
        handleServerEvent(qc, e, scheduleLibrary);
        emitServerEvent(e);
      },
      onResync: () => void qc.invalidateQueries(),
      onState: (c) => {
        // Back after an outage: whatever happened meanwhile is refetched.
        if (c.status === 'open' && wasDown) void qc.invalidateQueries();
        wasDown = c.status === 'reconnecting';
        setState(c);
      },
    });
    stream = s;
    s.start();
    return () => {
      if (timer) clearTimeout(timer);
      s.stop();
      if (stream === s) stream = null;
    };
  }, [qc]);
}
