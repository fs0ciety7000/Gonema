/* The order of the list the user opened a film from, so J / K on the film page
   walk the same list (sorted and filtered as it was). */
let order: number[] = [];

export function setBrowseOrder(ids: readonly number[]) {
  order = ids.slice();
}

export function neighbours(id: number, fallback: readonly number[]): { prev: number | null; next: number | null } {
  const list = order.includes(id) ? order : fallback;
  const i = list.indexOf(id);
  if (i < 0) return { prev: null, next: null };
  return { prev: list[i - 1] ?? null, next: list[i + 1] ?? null };
}
