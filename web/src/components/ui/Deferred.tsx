import { type ReactNode, Suspense, useEffect, useState } from 'react';

/** Renders `placeholder` (same look, no logic) until the browser is idle after the first paint or the
 *  user shows intent (hover, focus, press), then the lazily loaded `children`, opened at once when the intent was a press. */
export function Deferred({
  placeholder,
  children,
}: {
  placeholder: (intent: { onPointerEnter: () => void; onFocus: () => void; onClick: () => void }) => ReactNode;
  children: (openNow: boolean) => ReactNode;
}) {
  const [state, setState] = useState<'idle' | 'load' | 'open'>('idle');
  // Also load when the browser is idle after the first paint, so the swap happens before any press.
  useEffect(() => {
    if (state !== 'idle') return;
    const w = window as Window & { requestIdleCallback?: (cb: () => void, o?: { timeout: number }) => number };
    if (w.requestIdleCallback) {
      const id = w.requestIdleCallback(() => setState((s) => (s === 'idle' ? 'load' : s)), { timeout: 2000 });
      return () => window.cancelIdleCallback?.(id);
    }
    const t = setTimeout(() => setState((s) => (s === 'idle' ? 'load' : s)), 1500);
    return () => clearTimeout(t);
  }, [state]);
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
