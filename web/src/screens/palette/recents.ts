/* Last places opened from the palette, in this browser only (localStorage, validated on read). */

export type Recent = { kind: 'film'; id: number } | { kind: 'nav'; id: string };

const STORE = 'gn-palette-recent';
const MAX = 5;

export function readRecents(): Recent[] {
  try {
    const raw: unknown = JSON.parse(localStorage.getItem(STORE) ?? '[]');
    if (!Array.isArray(raw)) return [];
    return raw
      .filter(
        (r): r is Recent =>
          !!r &&
          typeof r === 'object' &&
          ((r.kind === 'film' && Number.isInteger(r.id) && r.id > 0) ||
            (r.kind === 'nav' && typeof r.id === 'string' && /^[a-z]{2,16}$/.test(r.id))),
      )
      .slice(0, MAX);
  } catch {
    return [];
  }
}

export function pushRecent(r: Recent) {
  const list = [r, ...readRecents().filter((x) => !(x.kind === r.kind && x.id === r.id))].slice(0, MAX);
  try {
    localStorage.setItem(STORE, JSON.stringify(list));
  } catch {}
}
