import { Popover as BasePopover } from '@base-ui/react/popover';
import type { ReactElement, ReactNode } from 'react';

// ----------------------------------------------------------------- popover

export function Popover({
  trigger,
  children,
  label,
  align = 'start',
  open,
  onOpenChange,
  defaultOpen,
}: {
  trigger: ReactElement;
  children: ReactNode;
  label: string;
  align?: 'start' | 'center' | 'end';
  open?: boolean;
  onOpenChange?: (open: boolean) => void;
  defaultOpen?: boolean;
}) {
  return (
    <BasePopover.Root
      defaultOpen={defaultOpen}
      {...(open !== undefined ? { open } : {})}
      {...(onOpenChange ? { onOpenChange: (o: boolean) => onOpenChange(o) } : {})}
    >
      <BasePopover.Trigger render={trigger} />
      <BasePopover.Portal>
        <BasePopover.Positioner sideOffset={8} align={align} className="menu-positioner">
          <BasePopover.Popup className="popover" aria-label={label}>
            {children}
          </BasePopover.Popup>
        </BasePopover.Positioner>
      </BasePopover.Portal>
    </BasePopover.Root>
  );
}
