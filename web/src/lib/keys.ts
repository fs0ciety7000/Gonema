/** True when a key event targets a text field (global shortcuts must not fire there). */
export function isTypingTarget(target: EventTarget | null, extra = ''): boolean {
  if (!(target instanceof Element)) return false;
  return !!target.closest(`input, textarea, select, [contenteditable="true"]${extra ? `, ${extra}` : ''}`);
}
