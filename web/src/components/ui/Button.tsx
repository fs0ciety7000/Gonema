import { type ComponentPropsWithRef, forwardRef, type ReactNode } from 'react';
import { cx } from './cx';

export type ButtonVariant = 'primary' | 'secondary' | 'ghost' | 'danger' | 'danger-solid' | 'tint' | 'plain';
export type ButtonSize = 'sm' | 'md' | 'lg';

const variantClass: Record<ButtonVariant, string> = {
  primary: 'btn-primary',
  secondary: 'btn-secondary',
  ghost: 'btn-ghost',
  danger: 'btn-danger',
  'danger-solid': 'btn-danger btn-solid',
  tint: 'btn-tint',
  plain: '',
};

export function buttonClass(variant: ButtonVariant = 'secondary', size: ButtonSize = 'md', extra?: string) {
  return cx('btn', variantClass[variant], size === 'sm' && 'btn-sm', size === 'lg' && 'btn-lg', extra);
}

type ButtonProps = ComponentPropsWithRef<'button'> & {
  variant?: ButtonVariant;
  size?: ButtonSize;
  /** Loading keeps the label (no layout shift) and sweeps a light across the button. Never a spinner. */
  loading?: boolean;
  icon?: ReactNode;
};

export const Button = forwardRef<HTMLButtonElement, ButtonProps>(function Button(
  { variant = 'secondary', size = 'md', loading = false, icon, className, children, type = 'button', ...rest },
  ref,
) {
  return (
    <button
      ref={ref}
      type={type}
      className={buttonClass(variant, size, className)}
      aria-busy={loading || undefined}
      {...rest}
    >
      {icon}
      {children}
    </button>
  );
});

type IconButtonProps = Omit<ComponentPropsWithRef<'button'>, 'aria-label'> & {
  /** Required: icon-only controls always carry a label. */
  label: string;
  variant?: 'ghost' | 'outlined' | 'on-media';
  size?: 'sm' | 'md';
  dot?: boolean;
};

export const IconButton = forwardRef<HTMLButtonElement, IconButtonProps>(function IconButton(
  { label, variant = 'ghost', size = 'md', dot = false, className, children, type = 'button', ...rest },
  ref,
) {
  return (
    <button
      ref={ref}
      type={type}
      aria-label={label}
      className={cx(
        'icon-btn',
        variant === 'outlined' && 'outlined',
        variant === 'on-media' && 'on-media',
        size === 'sm' && 'sm',
        className,
      )}
      {...rest}
    >
      {children}
      {dot && <span className="dot" aria-hidden="true" />}
    </button>
  );
});

export function Kbd({ children }: { children: ReactNode }) {
  return <span className="kbd">{children}</span>;
}
