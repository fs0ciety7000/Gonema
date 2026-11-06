import { Dialog as BaseDialog } from '@base-ui/react/dialog';
import type { ReactNode } from 'react';
import { cx } from './cx';

// ----------------------------------------------------------------- dialog

type DialogProps = {
  open: boolean;
  onOpenChange: (open: boolean) => void;
  title: ReactNode;
  description?: ReactNode;
  icon?: ReactNode;
  children?: ReactNode;
  actions?: ReactNode;
  className?: string;
  /** Labels the dialog when the title is visually hidden. */
  wide?: boolean;
};

/** Dialog (glass 3). Confirmation dialogs are reserved for deleting files; elsewhere: Undo. */
export function Dialog({
  open,
  onOpenChange,
  title,
  description,
  icon,
  children,
  actions,
  className,
  wide,
}: DialogProps) {
  return (
    <BaseDialog.Root open={open} onOpenChange={(o) => onOpenChange(o)}>
      <BaseDialog.Portal>
        <BaseDialog.Backdrop className="scrim" />
        <BaseDialog.Popup className={cx('dialog dialog-centered', wide && 'dialog-wide', className)}>
          {icon && <div className="icon-head">{icon}</div>}
          <BaseDialog.Title>{title}</BaseDialog.Title>
          {description && <BaseDialog.Description>{description}</BaseDialog.Description>}
          {children}
          {actions && <div className="actions">{actions}</div>}
        </BaseDialog.Popup>
      </BaseDialog.Portal>
    </BaseDialog.Root>
  );
}

export const DialogClose = BaseDialog.Close;
