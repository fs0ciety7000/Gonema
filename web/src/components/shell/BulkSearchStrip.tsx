import '@/styles/v11.css';
import { Link } from '@tanstack/react-router';
import { Search, X } from 'lucide-react';
import { useEffect, useRef, useState } from 'react';
import { cancelBulkSearch } from '@/api/v11';
import { Button, IconButton } from '@/components/ui/Button';
import { ProgressBar } from '@/components/ui/Progress';
import { toast } from '@/components/ui/Toast';
import { type BulkSearch, bulkSearch, isEnded, useBulkSearch } from '@/lib/bulkSearch';
import { formatClock, formatNumber } from '@/lib/format';

/* Activity strip of the bulk search (DESIGN.md: glass 3, like the bulk-action bar; bottom right so it
   never covers the toasts or the bulk bar). Progress is a progressbar (state changes only are
   announced); the end becomes a toast with the counts. */

const plural = (n: number, w: string) => `${formatNumber(n)} ${n === 1 ? w : `${w}s`}`;

export function bulkTitle(s: Pick<BulkSearch, 'label' | 'total'>): string {
  if (s.label === 'missing_released') return `Searching the ${formatNumber(s.total)} released`;
  if (s.label === 'cutoff_unmet') return `Searching the ${formatNumber(s.total)} below cutoff`;
  return `Searching ${plural(s.total, 'film')}`;
}

/** "12 of 40 · 3 grabbed" (the line under the title). */
export function bulkLine(s: BulkSearch): string {
  if (s.state === 'pending') return `Queued · ${plural(s.total, 'film')}`;
  if (s.state === 'waiting') {
    const until = s.waitingUntil ? Date.parse(s.waitingUntil) : Number.NaN;
    return [
      s.reason || 'Every indexer is resting.',
      Number.isNaN(until) ? '' : `Goes on at ${formatClock(new Date(until))}.`,
    ]
      .filter(Boolean)
      .join(' ');
  }
  return `${formatNumber(s.done)} of ${formatNumber(s.total)} · ${formatNumber(s.grabbed)} grabbed`;
}

/** The toast at the end: what was done, in counts. */
export function bulkSummary(s: BulkSearch): { title: string; body: string } {
  const title =
    s.state === 'cancelled' ? 'Search cancelled' : s.state === 'stopped' ? 'Search stopped' : 'Search finished';
  const parts = [
    `${formatNumber(s.done)} of ${formatNumber(s.total)} searched`,
    `${formatNumber(s.grabbed)} grabbed`,
    s.noResult ? `${formatNumber(s.noResult)} with nothing acceptable` : '',
    s.skipped ? `${formatNumber(s.skipped)} skipped` : '',
    s.failed ? `${formatNumber(s.failed)} failed (see the logs)` : '',
  ].filter(Boolean);
  const tail = s.state === 'cancelled' ? ' Grabs already made stay.' : s.reason ? ` ${s.reason}` : '';
  return { title, body: `${parts.join(' · ')}.${tail}` };
}

export default function BulkSearchStrip() {
  const s = useBulkSearch();
  const [cancelling, setCancelling] = useState(false);
  const [said, setSaid] = useState('');
  const lastState = useRef<string | null>(null);

  useEffect(() => {
    if (!s) return;
    if (s.state === lastState.current) return;
    lastState.current = s.state;
    if (isEnded(s)) {
      const { title, body } = bulkSummary(s);
      toast.show({ title, body, icon: <Search className="i" /> });
      bulkSearch.dismiss();
      setCancelling(false);
      return;
    }
    setSaid(s.state === 'waiting' ? `Bulk search waiting. ${bulkLine(s)}` : `${bulkTitle(s)}.`);
  }, [s]);

  if (!s || isEnded(s)) return null;
  const cancel = async () => {
    setCancelling(true);
    try {
      const p = await cancelBulkSearch(s.jobId);
      bulkSearch.set(p);
    } catch (e) {
      setCancelling(false);
      toast.error('The search was not cancelled', (e as { detail?: string }).detail);
    }
  };
  return (
    <section className="bulk-strip" aria-label="Bulk search">
      <span className="ticon" aria-hidden="true">
        <Search className="i" />
      </span>
      <div className="grow">
        <div className="ttitle">{bulkTitle(s)}</div>
        <div className="tbody">{cancelling ? 'Cancelling after the current film…' : bulkLine(s)}</div>
        <ProgressBar
          size="thin"
          value={s.total ? s.done / s.total : 0}
          label={bulkTitle(s)}
          live={s.state === 'running'}
          state={s.state === 'waiting' ? 'paused' : 'active'}
        />
      </div>
      <div className="tactions">
        <Link className="btn btn-ghost btn-sm" to="/history">
          History
        </Link>
        <Button size="sm" variant="secondary" loading={cancelling} disabled={cancelling} onClick={() => void cancel()}>
          Cancel
        </Button>
        <IconButton size="sm" label="Hide (the search goes on)" title="Hide" onClick={() => bulkSearch.dismiss()}>
          <X className="i i-sm" aria-hidden="true" />
        </IconButton>
      </div>
      <span className="sr-only" role="status">
        {said}
      </span>
    </section>
  );
}
