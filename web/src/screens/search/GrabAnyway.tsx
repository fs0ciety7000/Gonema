import { Popover as BasePopover } from '@base-ui/react/popover';
import { CircleX, Info, TriangleAlert } from 'lucide-react';
import { useLayoutEffect, useState } from 'react';
import type { SearchResult } from '@/api/schema';
import { Button } from '@/components/ui/Button';
import { overlayRoot } from '@/components/ui/overlayRoot';

/** Anchored confirmation for grabbing a rejected release: recalls why it was rejected; sends override=true. */
export function GrabAnywayPopover({
  r,
  anchor,
  onCancel,
  onConfirm,
}: {
  r: SearchResult;
  anchor: () => HTMLElement | null;
  onCancel: () => void;
  onConfirm: () => void;
}) {
  const [el, setEl] = useState<HTMLElement | null>(null);
  useLayoutEffect(() => setEl(anchor()), [anchor]);
  const reasons = r.rejections ?? [];
  return (
    <BasePopover.Root
      open={!!el}
      onOpenChange={(o) => {
        if (!o) onCancel();
      }}
    >
      <BasePopover.Portal container={overlayRoot()}>
        <BasePopover.Positioner anchor={el} side="bottom" align="end" sideOffset={10} className="menu-positioner">
          <BasePopover.Popup className="popover confirm-pop" finalFocus={false}>
            <BasePopover.Title className="cf-title">
              <TriangleAlert className="i" aria-hidden="true" />
              Grab a rejected release?
            </BasePopover.Title>
            <BasePopover.Description render={<div />} className="cf-body">
              <p>Gonema skipped it for {reasons.length === 1 ? 'this reason' : 'these reasons'}:</p>
              <ul className="quote">
                {reasons.map((x) => (
                  <li key={x.code + x.message}>
                    {x.permanent ? (
                      <CircleX className="i" aria-hidden="true" />
                    ) : (
                      <Info className="i" aria-hidden="true" />
                    )}
                    <span>{x.message}</span>
                  </li>
                ))}
              </ul>
              <p>
                It will be sent to your download client and logged as a manual grab. If it fails, Gonema won’t retry it
                automatically.
              </p>
            </BasePopover.Description>
            <div className="actions">
              <Button variant="ghost" size="sm" onClick={onCancel}>
                Cancel
              </Button>
              <Button variant="primary" size="sm" onClick={onConfirm} autoFocus>
                Grab anyway
              </Button>
            </div>
          </BasePopover.Popup>
        </BasePopover.Positioner>
      </BasePopover.Portal>
    </BasePopover.Root>
  );
}
