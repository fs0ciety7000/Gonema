/** Container of every portalled overlay (menus, popovers, tooltips, dialogs): a labelled
 *  region rendered by the app shell, so their content sits in a landmark. */
export function overlayRoot(): HTMLElement | undefined {
  return (typeof document !== 'undefined' && document.getElementById('gn-overlays')) || undefined;
}
