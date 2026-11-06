import { useSyncExternalStore } from 'react';
import type { BulkSearchProgress } from '@/api/schema';
import { toast } from '@/components/ui/toastStore';

/* The bulk search in progress (one at a time on the server). Fed by the bulksearch.* events
   (live.ts) and by the start / cancel calls; shown by the activity strip (BulkSearchStrip, lazy).
   The job id is kept in localStorage so a reload finds a running search again. */

export type BulkSearch = BulkSearchProgress;

const STORE_KEY = 'gn-bulk-search';
const ENDED = new Set(['finished', 'cancelled', 'stopped']);

let current: BulkSearch | null = null;
/** The last job whose end was shown: a late or repeated event of it does not show it again. */
let endedJob = 0;
const listeners = new Set<() => void>();
const emit = () => {
  for (const l of listeners) l();
};

export const isEnded = (s: BulkSearch | null) => !!s && ENDED.has(s.state);

function remember(id: number | null) {
  try {
    if (id) localStorage.setItem(STORE_KEY, String(id));
    else localStorage.removeItem(STORE_KEY);
  } catch {
    // storage unavailable (private mode): the events still drive the strip
  }
}

export const bulkSearch = {
  get: () => current,
  set(p: BulkSearch | null) {
    // events of an older job do not replace a newer one; an end is shown once
    if (p && current && p.jobId < current.jobId && !isEnded(current)) return;
    if (p && p.jobId <= endedJob) return;
    if (p && isEnded(p)) endedJob = p.jobId;
    current = p;
    remember(p && !isEnded(p) ? p.jobId : null);
    emit();
  },
  /** Hides the strip (the search goes on on the server; the next event shows it again). */
  dismiss() {
    if (!current || isEnded(current)) remember(null);
    current = null;
    emit();
  },
  /** Job id remembered by this browser (a search that may still run after a reload). */
  remembered(): number | null {
    try {
      const n = Number(localStorage.getItem(STORE_KEY));
      return Number.isInteger(n) && n > 0 ? n : null;
    } catch {
      return null;
    }
  },
};

export function useBulkSearch(): BulkSearch | null {
  return useSyncExternalStore(
    (cb) => {
      listeners.add(cb);
      return () => listeners.delete(cb);
    },
    () => current,
  );
}

/** Data of a bulksearch.* event, validated (the strip trusts these fields). */
export function parseBulkEvent(data: unknown): BulkSearch | null {
  if (!data || typeof data !== 'object') return null;
  const d = data as Record<string, unknown>;
  const n = (v: unknown) => (typeof v === 'number' && Number.isFinite(v) ? v : 0);
  if (typeof d.jobId !== 'number' || typeof d.state !== 'string') return null;
  return {
    jobId: d.jobId,
    label: typeof d.label === 'string' ? d.label : 'selection',
    state: d.state as BulkSearch['state'],
    total: n(d.total),
    done: n(d.done),
    grabbed: n(d.grabbed),
    noResult: n(d.noResult),
    skipped: n(d.skipped),
    failed: n(d.failed),
    updatedAt: typeof d.updatedAt === 'string' ? d.updatedAt : new Date().toISOString(),
    ...(typeof d.reason === 'string' ? { reason: d.reason } : {}),
    ...(typeof d.waitingUntil === 'string' ? { waitingUntil: d.waitingUntil } : {}),
    ...(typeof d.currentMovieId === 'number' ? { currentMovieId: d.currentMovieId } : {}),
  };
}

export type BulkRequest = { ids: number[] } | { filter: 'missing_released' | 'cutoff_unmet' };

/** Starts a bulk search (the API module is loaded on demand). Errors become a calm toast. */
export async function startBulk(target: BulkRequest): Promise<boolean> {
  const { startBulkSearch } = await import('@/api/v11');
  try {
    const r = await startBulkSearch(target);
    bulkSearch.set({
      jobId: r.jobId,
      label: r.label,
      state: 'pending',
      total: r.total,
      done: 0,
      grabbed: 0,
      noResult: 0,
      skipped: 0,
      failed: 0,
      updatedAt: new Date().toISOString(),
    });
    return true;
  } catch (e) {
    const detail = (e as { detail?: string }).detail ?? 'Gonema could not be reached.';
    toast.error('The search did not start', detail);
    return false;
  }
}
