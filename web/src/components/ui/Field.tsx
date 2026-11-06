import { TriangleAlert } from 'lucide-react';
import { type ComponentPropsWithRef, forwardRef, type ReactNode, useId } from 'react';
import { cx } from './cx';

type InputProps = Omit<ComponentPropsWithRef<'input'>, 'size'> & {
  icon?: ReactNode;
  suffix?: ReactNode;
  invalid?: boolean;
  mono?: boolean;
  wrapperClassName?: string;
};

/** Text field: optional leading icon and suffix; the whole frame shows hover, focus and error. */
export const Input = forwardRef<HTMLInputElement, InputProps>(function Input(
  { icon, suffix, invalid, mono, wrapperClassName, disabled, ...rest },
  ref,
) {
  return (
    <span
      className={cx('input', mono && 'mono', disabled && 'is-disabled', wrapperClassName)}
      aria-invalid={invalid || undefined}
    >
      {icon}
      <input ref={ref} disabled={disabled} aria-invalid={invalid || undefined} {...rest} />
      {suffix && <span className="suffix">{suffix}</span>}
    </span>
  );
});

type FieldProps = {
  label: ReactNode;
  hint?: ReactNode;
  /** Says what is wrong and how to fix it. */
  error?: ReactNode;
  children: (ids: { id: string; describedBy: string | undefined; invalid: boolean }) => ReactNode;
  aside?: ReactNode;
};

export function Field({ label, hint, error, children, aside }: FieldProps) {
  const id = useId();
  const hintId = `${id}-hint`;
  const errId = `${id}-err`;
  const describedBy = [error ? errId : null, hint ? hintId : null].filter(Boolean).join(' ') || undefined;
  return (
    <div className="field">
      <label className="field-label" htmlFor={id}>
        <span>{label}</span>
        {aside}
      </label>
      {children({ id, describedBy, invalid: !!error })}
      {error && (
        <span className="field-error" id={errId}>
          <TriangleAlert className="i i-xs" aria-hidden="true" />
          {error}
        </span>
      )}
      {hint && (
        <span className="field-hint" id={hintId}>
          {hint}
        </span>
      )}
    </div>
  );
}
