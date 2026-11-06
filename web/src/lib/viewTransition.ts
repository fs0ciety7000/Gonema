import { prefersReducedMotion } from './motion';

/* Poster -> detail shared element (motion.md §1).
   With the View Transitions API the clicked poster and the detail hero share
   view-transition-name "poster-<id>" (set on the clicked poster only, just before
   the transition, so names stay unique). Without it, a FLIP fallback animates the
   hero from the poster's rectangle with WAAPI. Reduced motion: a 150 ms
   cross-fade, no travel. A second navigation during a transition skips it. */

type VT = { finished: Promise<void>; updateCallbackDone: Promise<void>; skipTransition: () => void };
type DocVT = Document & { startViewTransition?: (cb: () => Promise<void> | void) => VT };

let active: VT | null = null;
let pendingFlip: { id: number; rect: DOMRect } | null = null;
let lastOpened: number | null = null;

export function supportsViewTransitions(): boolean {
  return typeof document !== 'undefined' && typeof (document as DocVT).startViewTransition === 'function';
}

function waitFor(sel: string, timeout: number): Promise<Element | null> {
  return new Promise((resolve) => {
    const t0 = performance.now();
    const tick = () => {
      const el = document.querySelector(sel);
      if (el || performance.now() - t0 > timeout) resolve(el);
      else requestAnimationFrame(tick);
    };
    tick();
  });
}

function run(kind: 'poster' | 'back' | 'fade', update: () => Promise<void>): Promise<void> {
  active?.skipTransition();
  const doc = document as DocVT;
  const root = document.documentElement;
  root.dataset.vt = kind;
  const vt = doc.startViewTransition!(update);
  active = vt;
  vt.finished
    .catch(() => {})
    .finally(() => {
      if (active === vt) {
        active = null;
        delete root.dataset.vt;
      }
    });
  return vt.updateCallbackDone.catch(() => {});
}

/** Opens a film from a poster (or a table row thumbnail). */
export function openMovieTransition(id: number, source: HTMLElement | null, navigate: () => Promise<unknown>) {
  lastOpened = id;
  const reduce = prefersReducedMotion();
  if (!supportsViewTransitions()) {
    if (source && !reduce) pendingFlip = { id, rect: source.getBoundingClientRect() };
    return navigate();
  }
  if (reduce || !source) {
    return run('fade', async () => {
      await navigate();
      await waitFor(`[data-hero-poster="${id}"]`, 300);
    });
  }
  source.style.viewTransitionName = `poster-${id}`;
  return run('poster', async () => {
    source.style.viewTransitionName = '';
    await navigate();
    await waitFor(`[data-hero-poster="${id}"]`, 400);
  });
}

/** Back to the library: reverse transition (250 ms) onto the poster, focus returns to it. */
export function backToLibraryTransition(id: number, navigate: () => Promise<unknown>) {
  lastOpened = id;
  if (!supportsViewTransitions()) return navigate();
  const reduce = prefersReducedMotion();
  let target: HTMLElement | null = null;
  return run(reduce ? 'fade' : 'back', async () => {
    await navigate();
    if (reduce) return;
    target = (await waitFor(`[data-poster-frame="${id}"]`, 300)) as HTMLElement | null;
    if (target) target.style.viewTransitionName = `poster-${id}`;
  }).then(() => {
    active?.finished.finally(() => {
      if (target) target.style.viewTransitionName = '';
    });
  });
}

/** The film whose poster should get focus back when the library mounts again. */
export function takeLastOpened(): number | null {
  const id = lastOpened;
  lastOpened = null;
  return id;
}

/** FLIP fallback, called by the detail hero once it is laid out. */
export function playPendingFlip(id: number, hero: HTMLElement) {
  const p = pendingFlip;
  pendingFlip = null;
  if (!p || p.id !== id || prefersReducedMotion()) return;
  const to = hero.getBoundingClientRect();
  if (!to.width || !to.height) return;
  const dx = p.rect.left - to.left;
  const dy = p.rect.top - to.top;
  const s = p.rect.width / to.width;
  // transform only (no radius per frame): the 12 -> 16 px radius change is not animated here.
  hero.style.transformOrigin = '0 0';
  hero.animate([{ transform: `translate(${dx}px, ${dy}px) scale(${s})` }, { transform: 'none' }], {
    duration: 300,
    easing: 'cubic-bezier(0.77, 0, 0.175, 1)',
  });
}
