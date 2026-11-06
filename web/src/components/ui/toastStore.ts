import { type ReactNode, useSyncExternalStore } from 'react';

/* Toasts (motion.md §5): reversible actions are applied optimistically and offer
   Undo for 8 s. The timer drains linearly and pauses on hover, on focus within
   the toast and while the window is hidden. Cmd/Ctrl+Z triggers the newest Undo;
   F6 moves focus to the newest toast. Max 3, newest at the bottom (entry point). */

export type ToastInput = {
  title: ReactNode;
  body?: ReactNode;
  icon?: ReactNode;
  /** Errors are announced assertively (role="alert"). */
  tone?: 'neutral' | 'error';
  undo?: () => void;
  action?: { label: string; onClick: () => void };
  duration?: number;
  /** Called once when the toast goes away, with why. A deferred action commits on 'timeout' / 'dismiss'. */
  onClose?: (reason: CloseReason) => void;
};

export type CloseReason = 'timeout' | 'dismiss' | 'undo' | 'action' | 'replaced';

export type ToastItem = ToastInput & { id: number; createdAt: number };

export const TOAST_DURATION = 8000;
const MAX = 3;

let items: ToastItem[] = [];
let seq = 0;
const listeners = new Set<() => void>();
const emit = () => {
  for (const l of listeners) l();
};

export const toast = {
  show(t: ToastInput): number {
    const id = ++seq;
    const next = [...items, { ...t, id, createdAt: Date.now() }];
    const dropped = next.slice(0, Math.max(0, next.length - MAX));
    items = next.slice(-MAX);
    emit();
    for (const d of dropped) d.onClose?.('replaced');
    return id;
  },
  error(title: ReactNode, body?: ReactNode) {
    return toast.show({ title, body, tone: 'error' });
  },
  dismiss(id: number, reason: CloseReason = 'dismiss') {
    const t = items.find((x) => x.id === id);
    if (!t) return;
    items = items.filter((x) => x.id !== id);
    emit();
    t.onClose?.(reason);
  },
  clear() {
    const old = items;
    items = [];
    emit();
    for (const t of old) t.onClose?.('dismiss');
  },
  /** Runs the newest Undo (Cmd/Ctrl+Z). Returns false when there is none. */
  undoLatest(): boolean {
    const t = [...items].reverse().find((x) => x.undo);
    if (!t?.undo) return false;
    t.undo();
    toast.dismiss(t.id, 'undo');
    return true;
  },
};

export function useToasts(): ToastItem[] {
  return useSyncExternalStore(
    (cb) => {
      listeners.add(cb);
      return () => listeners.delete(cb);
    },
    () => items,
  );
}

/** Current toasts (for keyboard handlers outside React state). */
export const currentToasts = (): readonly ToastItem[] => items;
