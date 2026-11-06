import { useSyncExternalStore } from 'react';

/* Command palette state. Kept tiny: the palette itself (cmdk) is a lazy chunk loaded on first open. */

type State = { open: boolean; used: boolean; query: string };

let state: State = { open: false, used: false, query: '' };
const listeners = new Set<() => void>();
const set = (next: Partial<State>) => {
  state = { ...state, ...next };
  for (const l of listeners) l();
};

/** Element focused before opening: focus returns to it when the palette closes. */
let opener: HTMLElement | null = null;

export const palette = {
  open(query = '') {
    if (state.open) return;
    opener = document.activeElement instanceof HTMLElement ? document.activeElement : null;
    set({ open: true, used: true, query });
  },
  close() {
    if (!state.open) return;
    set({ open: false });
    const el = opener;
    opener = null;
    if (el?.isConnected) requestAnimationFrame(() => el.focus({ preventScroll: true }));
  },
  toggle() {
    if (state.open) palette.close();
    else palette.open();
  },
  get: () => state,
};

const subscribe = (l: () => void) => {
  listeners.add(l);
  return () => listeners.delete(l);
};

export const usePalette = () => useSyncExternalStore(subscribe, palette.get, palette.get);

/** ⌘K on Apple systems, Ctrl+K elsewhere (both are accepted everywhere). */
export const isPaletteShortcut = (ev: KeyboardEvent) =>
  (ev.metaKey || ev.ctrlKey) && !ev.altKey && !ev.shiftKey && ev.key.toLowerCase() === 'k';
