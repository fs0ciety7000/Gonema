import { useCallback, useEffect, useReducer, useRef } from 'react';
import { ApiError } from '@/api/client';
import { onServerEvent, useConnection } from '@/api/live';
import { getSearch, startInteractiveSearch } from '@/api/screens';
import { initialSearch, type SearchEvent, searchReducer } from './searchModel';

const SEARCH_EVENTS = new Set(['search.batch', 'search.indexer_done', 'search.indexer_failed', 'search.finished']);

/** Starts an interactive search and follows it: events from the shared stream, plus a
 *  snapshot after the start and whenever the stream was interrupted (missed events). */
export function useInteractiveSearch(movieId: number) {
  const [state, dispatch] = useReducer(searchReducer, movieId, initialSearch);
  const conn = useConnection();
  const searchId = useRef<string | null>(null);
  searchId.current = state.searchId;

  useEffect(
    () =>
      onServerEvent((e) => {
        if (!SEARCH_EVENTS.has(e.type) || !e.data || typeof e.data !== 'object') return;
        dispatch({ type: 'event', event: { type: e.type, data: e.data } as SearchEvent });
      }),
    [],
  );

  const syncSnapshot = useCallback(async (id: string) => {
    try {
      dispatch({ type: 'snapshot', snapshot: await getSearch(id) });
    } catch {
      /* the stream keeps the state; the next sync retries */
    }
  }, []);

  const seq = useRef(0);
  const start = useCallback(async () => {
    const n = ++seq.current;
    dispatch({ type: 'start' });
    try {
      const s = await startInteractiveSearch(movieId);
      if (n !== seq.current) return; // a newer search replaced this one
      dispatch({ type: 'started', snapshot: { ...s, id: s.searchId || s.id } });
      void syncSnapshot(s.searchId || s.id);
    } catch (e) {
      if (n !== seq.current) return;
      dispatch({
        type: 'start-failed',
        error: e instanceof ApiError ? e.detail : 'The search could not start. Try again.',
      });
    }
  }, [movieId, syncSnapshot]);

  // biome-ignore lint/correctness/useExhaustiveDependencies: one search per film page
  useEffect(() => {
    void start();
  }, [movieId]);

  // Back after a stream interruption: what was missed is in the snapshot.
  const wasOpen = useRef(true);
  useEffect(() => {
    const open = conn.status === 'open';
    if (open && !wasOpen.current && searchId.current) void syncSnapshot(searchId.current);
    wasOpen.current = open;
  }, [conn.status, syncSnapshot]);

  // While the stream is down, poll the snapshot so results still arrive.
  const running = state.status === 'running';
  useEffect(() => {
    if (!running || conn.status === 'open' || !state.searchId) return;
    const id = state.searchId;
    const t = setInterval(() => void syncSnapshot(id), 2000);
    return () => clearInterval(t);
  }, [running, conn.status, state.searchId, syncSnapshot]);

  return { state, restart: start };
}
