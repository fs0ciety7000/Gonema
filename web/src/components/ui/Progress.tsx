import type { CSSProperties } from 'react';
import { cx } from './cx';

type ProgressProps = {
  /** 0..1 */
  value: number;
  label: string;
  /** Active download: the sheen "breathes" across the filled part (none under reduced motion). */
  live?: boolean;
  state?: 'active' | 'paused' | 'failed';
  size?: 'thin' | 'md' | 'lg';
  className?: string;
  style?: CSSProperties;
};

/** role="progressbar"; the value glides (transform: scaleX) over 900 ms. Screen readers get state changes, not every percent. */
export function ProgressBar({ value, label, live, state = 'active', size = 'md', className, style }: ProgressProps) {
  const p = Math.max(0, Math.min(1, Number.isFinite(value) ? value : 0));
  const pct = Math.round(p * 100);
  return (
    <div
      className={cx(
        'progress',
        live && state === 'active' && 'live',
        state === 'paused' && 'paused',
        state === 'failed' && 'failed',
        size === 'thin' && 'thin',
        size === 'lg' && 'lg',
        className,
      )}
      role="progressbar"
      aria-label={label}
      aria-valuemin={0}
      aria-valuemax={100}
      aria-valuenow={pct}
      aria-valuetext={state === 'failed' ? `Failed at ${pct}%` : state === 'paused' ? `Paused at ${pct}%` : `${pct}%`}
      style={{ ...style, ['--p' as string]: p }}
    >
      <div className="fill" />
    </div>
  );
}
