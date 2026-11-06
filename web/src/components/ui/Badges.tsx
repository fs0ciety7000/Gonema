import type { ReactNode } from 'react';
import { formatScore } from '@/lib/format';
import { parseQuality } from '@/lib/quality';
import { type DisplayStatus, STATUS } from '@/lib/status';
import { cx } from './cx';

/** Status badge: icon + label, always (never colour only). */
export function StatusBadge({
  status,
  label,
  onMedia,
  lg,
}: {
  status: DisplayStatus;
  label?: ReactNode;
  onMedia?: boolean;
  lg?: boolean;
}) {
  const s = STATUS[status];
  const Icon = s.icon;
  return (
    <span className={cx('badge', onMedia && 'on-media', lg && 'lg')} data-status={status}>
      <Icon className="i" aria-hidden="true" />
      {label ?? s.label}
    </span>
  );
}

/** Inline status (tables, meta lines). `srPrefix` names the status when the visible text is a value (e.g. "2160p"). */
export function StatusText({
  status,
  children,
  srPrefix,
  className,
}: {
  status: DisplayStatus;
  children?: ReactNode;
  srPrefix?: string;
  className?: string;
}) {
  const s = STATUS[status];
  const Icon = s.icon;
  return (
    <span className={cx('status', className)} data-status={status}>
      <Icon className="i" aria-hidden="true" />
      {srPrefix && <span className="sr-only">{srPrefix}, </span>}
      {children ?? s.label}
    </span>
  );
}

/** Resolution + source; 2160p is inverted. */
export function QualityBadge({ quality, dim }: { quality: string | null | undefined; dim?: boolean }) {
  const q = parseQuality(quality);
  if (!q) return null;
  return (
    <span className={cx('qbadge', q.uhd && 'uhd', dim && 'dim')}>
      <b>{q.res}</b>
      {q.source && <span>{q.source}</span>}
    </span>
  );
}

export function HdrFlag({ children }: { children: ReactNode }) {
  return <span className="hdr-flag">{children}</span>;
}

/** +/- score. Clickable when onClick is set (opens the score breakdown). */
export function ScorePill({ score, onClick, label }: { score: number; onClick?: () => void; label?: string }) {
  const cls = cx('score', score > 0 && 'pos', score < 0 && 'neg');
  if (onClick) {
    return (
      <button
        type="button"
        className={cls}
        onClick={onClick}
        aria-label={label ?? `Score ${formatScore(score)}, show details`}
      >
        {formatScore(score)}
      </button>
    );
  }
  return <span className={cls}>{formatScore(score)}</span>;
}
