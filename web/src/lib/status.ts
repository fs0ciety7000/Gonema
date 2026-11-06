import {
  ArrowDownToLine,
  CircleArrowUp,
  CircleCheck,
  CircleDashed,
  EyeOff,
  type LucideIcon,
  TriangleAlert,
} from 'lucide-react';
import type { LibraryItem } from '@/api/model';
import { formatDate } from './format';
import { parseQuality } from './quality';

/** Display statuses of DESIGN.md (never colour only: icon + label). */
export type DisplayStatus = 'available' | 'missing' | 'downloading' | 'failed' | 'upgraded' | 'unmonitored';

export const STATUS: Record<DisplayStatus, { icon: LucideIcon; label: string }> = {
  available: { icon: CircleCheck, label: 'Available' },
  missing: { icon: CircleDashed, label: 'Missing' },
  downloading: { icon: ArrowDownToLine, label: 'Downloading' },
  failed: { icon: TriangleAlert, label: 'Failed' },
  upgraded: { icon: CircleArrowUp, label: 'Upgraded' },
  unmonitored: { icon: EyeOff, label: 'Not monitored' },
};

export function displayStatus(m: Pick<LibraryItem, 'state' | 'monitored' | 'failed'>): DisplayStatus {
  if (m.state === 'downloading') return 'downloading';
  if (m.failed) return 'failed';
  if (!m.monitored) return 'unmonitored';
  if (m.state === 'missing') return 'missing';
  return 'available';
}

const dayMonth = new Intl.DateTimeFormat('en-GB', { day: 'numeric', month: 'short' });

/** Short honest note for a missing film: why it is not there and what happens next. */
export function missingNote(
  m: Pick<LibraryItem, 'status' | 'releaseDigital' | 'releasePhysical' | 'monitored'>,
  now = Date.now(),
): string {
  const next = [m.releaseDigital, m.releasePhysical]
    .filter((d): d is string => !!d)
    .map((d) => new Date(`${d}T00:00:00`))
    .filter((d) => d.getTime() > now)
    .sort((a, b) => a.getTime() - b.getTime())[0];
  if (next)
    return `Out ${next.getFullYear() === new Date(now).getFullYear() ? dayMonth.format(next) : formatDate(next)}`;
  if (m.status === 'in_cinemas') return 'In cinemas';
  if (m.status === 'announced') return 'Announced';
  return m.monitored ? 'Wanted' : 'Missing';
}

/** Text of the status line under a poster / in a table cell. */
export function statusText(m: LibraryItem, s: DisplayStatus): string {
  switch (s) {
    case 'available':
    case 'upgraded':
      return parseQuality(m.quality)?.res ?? STATUS[s].label;
    case 'downloading':
      return m.progress > 0 ? `${Math.round(m.progress * 100)}%` : 'Downloading';
    case 'failed':
      return m.quality ? 'Upgrade failed' : 'Download failed';
    case 'missing':
      return missingNote(m);
    case 'unmonitored':
      return 'Not monitored';
  }
}
