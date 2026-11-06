import { type ChangeEvent, type ComponentPropsWithRef, type ReactNode, useEffect, useRef } from 'react';
import { cx } from './cx';

type CheckboxProps = Omit<ComponentPropsWithRef<'input'>, 'type' | 'onChange' | 'checked'> & {
  checked: boolean;
  indeterminate?: boolean;
  onCheckedChange?: (checked: boolean, event: ChangeEvent<HTMLInputElement>) => void;
  onMedia?: boolean;
  label?: ReactNode;
  labelClassName?: string;
};

/** Native checkbox (keyboard and forms for free), styled by .check; the mark scales in with --spring-pop. */
export function Checkbox({
  checked,
  indeterminate = false,
  onCheckedChange,
  onMedia,
  label,
  labelClassName,
  className,
  ...rest
}: CheckboxProps) {
  const ref = useRef<HTMLInputElement>(null);
  useEffect(() => {
    if (ref.current) ref.current.indeterminate = indeterminate;
  }, [indeterminate]);
  return (
    <label className={cx('check', onMedia && 'on-media', labelClassName)}>
      <input
        ref={ref}
        type="checkbox"
        className={className}
        checked={checked}
        onChange={(ev) => onCheckedChange?.(ev.target.checked, ev)}
        {...rest}
      />
      {label}
    </label>
  );
}
