import type { ComponentPropsWithRef, ReactNode } from 'react';
import { cx } from './cx';

type SwitchProps = Omit<ComponentPropsWithRef<'button'>, 'onChange'> & {
  checked: boolean;
  onCheckedChange: (checked: boolean) => void;
  /** Movie tint (only on the movie page). */
  tint?: boolean;
  children?: ReactNode;
};

/** role="switch" button: the knob slides with --spring-pop. */
export function Switch({ checked, onCheckedChange, tint, className, children, ...rest }: SwitchProps) {
  return (
    <button
      type="button"
      role="switch"
      aria-checked={checked}
      className={cx('switch', tint && 'tint', className)}
      onClick={() => onCheckedChange(!checked)}
      {...rest}
    >
      <span className="track" aria-hidden="true" />
      {children}
    </button>
  );
}
