import { useSyncExternalStore } from 'react';

/* Library dialogs (tags, move, rename, delete file, remove) are opened from menus, the
   bulk bar and the film page. This store is all the library's first load carries: the
   dialogs themselves are a separate chunk, loaded on first use (MovieDialogHost). */

export type DialogTarget = { id: number; title: string };

export type MovieDialog =
  | { kind: 'tags'; targets: DialogTarget[]; onDone?: () => void }
  | { kind: 'move'; targets: DialogTarget[]; onDone?: () => void }
  | { kind: 'rename'; targets: DialogTarget[] }
  | { kind: 'deleteFile'; target: DialogTarget }
  | { kind: 'remove'; targets: DialogTarget[]; onDone?: () => void }
  | { kind: 'tmdbKey'; onDone?: () => void };

let current: MovieDialog | null = null;
let used = false;
const listeners = new Set<() => void>();
const emit = () => {
  for (const l of listeners) l();
};

export function openMovieDialog(d: MovieDialog) {
  current = d;
  used = true;
  emit();
}

export function closeMovieDialog() {
  current = null;
  emit();
}

const subscribe = (cb: () => void) => {
  listeners.add(cb);
  return () => listeners.delete(cb);
};

export function useMovieDialog(): MovieDialog | null {
  return useSyncExternalStore(subscribe, () => current);
}

/** True once any dialog was requested (the host loads its chunk from then on). */
export function useDialogsUsed(): boolean {
  return useSyncExternalStore(subscribe, () => used);
}
