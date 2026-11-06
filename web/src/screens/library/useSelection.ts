import { useCallback, useMemo, useRef, useState } from 'react';

export type SelectionApi = {
  selected: ReadonlySet<number>;
  isSelected: (id: number) => boolean;
  toggle: (id: number) => void;
  /** Selects from the anchor (last toggled) to this id, in list order. */
  range: (id: number) => void;
  set: (ids: Iterable<number>) => void;
  selectAll: () => void;
  clear: () => void;
  /** Keeps only ids that still exist (after a filter or a removal). */
  prune: (ids: ReadonlySet<number>) => void;
};

/** Multi-selection over an ordered list: click toggles, Shift extends from the anchor, Cmd/Ctrl adds. */
export function useSelection(order: readonly number[]): SelectionApi {
  const [selected, setSelected] = useState<ReadonlySet<number>>(() => new Set());
  const anchor = useRef<number | null>(null);
  const orderRef = useRef(order);
  orderRef.current = order;

  const toggle = useCallback((id: number) => {
    anchor.current = id;
    setSelected((prev) => {
      const next = new Set(prev);
      if (next.has(id)) next.delete(id);
      else next.add(id);
      return next;
    });
  }, []);

  const range = useCallback((id: number) => {
    const list = orderRef.current;
    const from = anchor.current === null ? -1 : list.indexOf(anchor.current);
    const to = list.indexOf(id);
    if (to < 0) return;
    if (from < 0) {
      anchor.current = id;
      setSelected((prev) => new Set(prev).add(id));
      return;
    }
    const [a, b] = from < to ? [from, to] : [to, from];
    setSelected((prev) => {
      const next = new Set(prev);
      for (let i = a; i <= b; i++) next.add(list[i]!);
      return next;
    });
  }, []);

  const set = useCallback((ids: Iterable<number>) => setSelected(new Set(ids)), []);
  const selectAll = useCallback(() => setSelected(new Set(orderRef.current)), []);
  const clear = useCallback(() => {
    anchor.current = null;
    setSelected((prev) => (prev.size ? new Set() : prev));
  }, []);
  const prune = useCallback((ids: ReadonlySet<number>) => {
    setSelected((prev) => {
      let changed = false;
      const next = new Set<number>();
      for (const id of prev) {
        if (ids.has(id)) next.add(id);
        else changed = true;
      }
      return changed ? next : prev;
    });
  }, []);

  return useMemo(
    () => ({ selected, isSelected: (id: number) => selected.has(id), toggle, range, set, selectAll, clear, prune }),
    [selected, toggle, range, set, selectAll, clear, prune],
  );
}
