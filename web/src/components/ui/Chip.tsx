import { X } from 'lucide-react';
import type { ComponentPropsWithRef, ReactNode } from 'react';
import { cx } from './cx';

type ChipProps = ComponentPropsWithRef<'button'> & { pressed?: boolean };

/** Toggleable chip (aria-pressed). */
export function Chip({ pressed, className, children, type = 'button', ...rest }: ChipProps) {
  return (
    <button type={type} className={cx('chip', className)} aria-pressed={pressed} {...rest}>
      {children}
    </button>
  );
}

/** Active filter: key, value and a remove button (the chip itself is not interactive). */
export function FilterChip({ k, value, onRemove }: { k: string; value: ReactNode; onRemove: () => void }) {
  return (
    <span className="chip is-on">
      <span className="k">{k}</span> {value}
      <button className="remove" type="button" aria-label={`Remove filter ${k}`} onClick={onRemove}>
        <X className="i i-xs" aria-hidden="true" />
      </button>
    </span>
  );
}

export function Tag({ children, plain }: { children: ReactNode; plain?: boolean }) {
  return <span className={cx('tag', plain && 'plain')}>{children}</span>;
}
