import { useSyncExternalStore } from 'react';

/* Reduced motion is tiered (docs/design/motion.md): travel and scale are removed,
   fades stay (<= 150 ms), essential feedback stays. CSS reads the media query;
   JS reads the same query here (plus ?motion=reduce, as in the mockups). */

const reduceQuery = () => (typeof matchMedia === 'function' ? matchMedia('(prefers-reduced-motion: reduce)') : null);

export function prefersReducedMotion(): boolean {
  if (typeof document !== 'undefined' && document.documentElement.dataset.motion === 'reduce') return true;
  return reduceQuery()?.matches ?? false;
}

function subscribe(cb: () => void) {
  const q = reduceQuery();
  q?.addEventListener('change', cb);
  return () => q?.removeEventListener('change', cb);
}

export function useReducedMotion(): boolean {
  return useSyncExternalStore(subscribe, prefersReducedMotion, () => false);
}

/** Durations from the tokens, in ms (for JS-driven animation). */
export const DUR = { instant: 100, fast: 150, base: 200, slow: 250, slower: 300 } as const;
export const EASE_OUT = [0.23, 1, 0.32, 1] as const;
export const EASE_IN_OUT = [0.77, 0, 0.175, 1] as const;
