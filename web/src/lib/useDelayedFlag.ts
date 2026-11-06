import { useEffect, useRef, useState } from 'react';

/** Skeleton timing (motion.md §6): shown only after `delay` ms of waiting, then kept at least `min` ms. */
export function useDelayedFlag(active: boolean, delay = 150, min = 300): boolean {
  const [shown, setShown] = useState(false);
  const shownAt = useRef(0);
  useEffect(() => {
    if (active) {
      if (shown) return;
      const t = setTimeout(() => {
        shownAt.current = Date.now();
        setShown(true);
      }, delay);
      return () => clearTimeout(t);
    }
    if (!shown) return;
    const left = min - (Date.now() - shownAt.current);
    if (left <= 0) {
      setShown(false);
      return;
    }
    const t = setTimeout(() => setShown(false), left);
    return () => clearTimeout(t);
  }, [active, shown, delay, min]);
  return shown;
}
