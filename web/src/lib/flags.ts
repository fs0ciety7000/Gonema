/** Dev-only screens (/__system, placeholders for sections built in later lots). Compiled out of production. */
export const DEV_SCREENS: boolean = __GONEMA_DEV__;

/** Review flags shared with the mockups: ?motion=reduce, ?freeze=1, ?density=compact, ?theme=light|dark. */
export function reviewFlag(name: 'motion' | 'freeze' | 'density' | 'theme'): string | null {
  if (typeof location === 'undefined') return null;
  return new URLSearchParams(location.search).get(name);
}
