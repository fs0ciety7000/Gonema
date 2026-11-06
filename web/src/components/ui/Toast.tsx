/* Public toast API. The store has no dependency; the animated region (Motion)
   is a separate chunk, loaded when the first toast appears (see Toaster.tsx). */
import { lazy, Suspense, useState } from 'react';
import { useToasts } from './toastStore';

export { type CloseReason, TOAST_DURATION, type ToastInput, type ToastItem, toast, useToasts } from './toastStore';

const LazyToaster = lazy(() => import('./Toaster').then((m) => ({ default: m.Toaster })));

export function Toaster() {
  const list = useToasts();
  const [needed, setNeeded] = useState(false);
  if (list.length > 0 && !needed) setNeeded(true);
  if (!needed) return null;
  return (
    <Suspense fallback={null}>
      <LazyToaster />
    </Suspense>
  );
}
