import { Tooltip as BaseTooltip } from '@base-ui/react/tooltip';
import type { ReactElement, ReactNode } from 'react';
import { overlayRoot } from './overlayRoot';

// ----------------------------------------------------------------- tooltip

/** 500 ms delay; once one tooltip is open the next ones open instantly (Base UI sets data-instant). */
export function TooltipProvider({ children }: { children: ReactNode }) {
  return (
    <BaseTooltip.Provider delay={500} timeout={400}>
      {children}
    </BaseTooltip.Provider>
  );
}

export function Tooltip({
  content,
  children,
  shortcut,
}: {
  content: ReactNode;
  children: ReactElement;
  shortcut?: string;
}) {
  return (
    <BaseTooltip.Root>
      <BaseTooltip.Trigger render={children} />
      <BaseTooltip.Portal container={overlayRoot()}>
        <BaseTooltip.Positioner sideOffset={8} className="tooltip-positioner">
          <BaseTooltip.Popup className="tooltip">
            {content}
            {shortcut && <span className="kbd">{shortcut}</span>}
          </BaseTooltip.Popup>
        </BaseTooltip.Positioner>
      </BaseTooltip.Portal>
    </BaseTooltip.Root>
  );
}
