import { type ReactNode, Suspense, useState } from 'react';

/** Renders `placeholder` (same look, no logic) until the user shows intent (hover, focus,
 *  press), then the lazily loaded `children`, opened at once when the intent was a press. */
export function Deferred({
  placeholder,
  children,
}: {
  placeholder: (intent: { onPointerEnter: () => void; onFocus: () => void; onClick: () => void }) => ReactNode;
  children: (openNow: boolean) => ReactNode;
}) {
  const [state, setState] = useState<'idle' | 'load' | 'open'>('idle');
  if (state === 'idle')
    return placeholder({
      onPointerEnter: () => setState('load'),
      onFocus: () => setState('load'),
      onClick: () => setState('open'),
    });
  return (
    <Suspense fallback={placeholder({ onPointerEnter() {}, onFocus() {}, onClick() {} })}>
      {children(state === 'open')}
    </Suspense>
  );
}
