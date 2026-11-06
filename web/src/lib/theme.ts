import { useSyncExternalStore } from 'react';
import { prefersReducedMotion } from './motion';

export type ThemePreference = 'dark' | 'light' | 'system';
export type Theme = 'dark' | 'light';

const KEY = 'gn-theme';
const listeners = new Set<() => void>();
const lightQuery = () => (typeof matchMedia === 'function' ? matchMedia('(prefers-color-scheme: light)') : null);

function readPreference(): ThemePreference {
  try {
    const v = localStorage.getItem(KEY);
    if (v === 'light' || v === 'dark' || v === 'system') return v;
  } catch {
    /* storage unavailable */
  }
  return 'dark';
}

let preference: ThemePreference = readPreference();

export function resolveTheme(p: ThemePreference): Theme {
  if (p === 'system') return lightQuery()?.matches ? 'light' : 'dark';
  return p;
}

function apply() {
  document.documentElement.dataset.theme = resolveTheme(preference);
  for (const l of listeners) l();
}

/** Applies a theme preference. The switch cross-fades with a View Transition (200 ms), none under reduced motion. */
export function setThemePreference(p: ThemePreference) {
  preference = p;
  try {
    localStorage.setItem(KEY, p);
  } catch {
    /* storage unavailable */
  }
  const doc = document as Document & { startViewTransition?: (cb: () => void) => unknown };
  if (doc.startViewTransition && !prefersReducedMotion()) {
    document.documentElement.dataset.themeSwitch = '';
    const t = doc.startViewTransition(apply) as { finished?: Promise<void> };
    t.finished?.finally(() => delete document.documentElement.dataset.themeSwitch);
  } else apply();
}

export function initTheme() {
  const forced = new URLSearchParams(location.search).get('theme');
  if (forced === 'light' || forced === 'dark') preference = forced;
  apply();
  lightQuery()?.addEventListener('change', () => {
    if (preference === 'system') apply();
  });
}

function subscribe(cb: () => void) {
  listeners.add(cb);
  return () => listeners.delete(cb);
}

export function useTheme(): { preference: ThemePreference; theme: Theme } {
  const pref = useSyncExternalStore(subscribe, () => preference);
  return { preference: pref, theme: resolveTheme(pref) };
}
