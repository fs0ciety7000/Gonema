import type { CSSProperties, ReactNode } from 'react';
import { cx } from './cx';

/** Skeleton block: reproduces the final layout; the shimmer is removed under reduced motion. */
export function Skeleton({
  className,
  style,
  delay = 0,
}: {
  className?: string;
  style?: CSSProperties;
  delay?: number;
}) {
  return (
    <div
      className={cx('skeleton', className)}
      style={{ ...style, ['--shimmer-delay' as string]: `${delay}ms` }}
      aria-hidden="true"
    />
  );
}

/** Empty block: short title, one sentence, one action (Kernel only where DESIGN.md allows it). */
export function EmptyState({
  title,
  children,
  action,
  mascot,
  headingLevel = 2,
}: {
  title: ReactNode;
  children?: ReactNode;
  action?: ReactNode;
  mascot?: string;
  headingLevel?: 2 | 3;
}) {
  const H = headingLevel === 2 ? 'h2' : 'h3';
  return (
    <div className="empty">
      {mascot && <img className="mascot" src={mascot} alt="" />}
      <H>{title}</H>
      {children && <p>{children}</p>}
      {action && <div className="actions">{action}</div>}
    </div>
  );
}

/** Calm inline message: what happened, what Gonema does next, one action. */
export function Notice({
  tone,
  icon,
  title,
  children,
  action,
  role,
}: {
  tone?: 'failed' | 'missing' | 'info';
  icon?: ReactNode;
  title: ReactNode;
  children?: ReactNode;
  action?: ReactNode;
  role?: 'status' | 'alert';
}) {
  return (
    <div className="notice" data-tone={tone} role={role}>
      {icon}
      <div className="grow">
        <div className="ntitle">{title}</div>
        {children && <div className="nbody">{children}</div>}
      </div>
      {action}
    </div>
  );
}
