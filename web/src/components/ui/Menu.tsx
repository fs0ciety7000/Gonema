import {
  type ComponentProps,
  cloneElement,
  createElement,
  type KeyboardEvent,
  type MouseEvent,
  type ReactElement,
  type ReactNode,
  useEffect,
  useRef,
  useState,
} from 'react';
import type * as Impl from './MenuImpl';

/* Menus are a facade over MenuImpl.tsx (Base UI + Floating UI, ~40 KB gzip), which is
   loaded on demand: when the browser is idle after the first screen, on hover or focus of
   a trigger, or at the latest on the first click. Items only render inside an open popup,
   so by then the implementation is always there. */

type ImplModule = typeof Impl;
let impl: ImplModule | null = null;
let loading: Promise<ImplModule> | null = null;

/** Loads the menu implementation (once). */
export function loadMenus(): Promise<ImplModule> {
  loading ??= import('./MenuImpl').then((m) => {
    impl = m;
    return m;
  });
  return loading;
}

/** Loads the menus once the browser is idle (the first screen has painted). */
export function preloadMenusWhenIdle() {
  if (impl || loading) return;
  const idle = (globalThis as { requestIdleCallback?: (cb: () => void, o?: { timeout: number }) => number })
    .requestIdleCallback;
  if (idle) idle(() => void loadMenus(), { timeout: 3000 });
  else setTimeout(() => void loadMenus(), 1500);
}

type MenuProps = ComponentProps<ImplModule['Menu']>;

type TriggerProps = {
  onClick?: (ev: MouseEvent<HTMLElement>) => void;
  onKeyDown?: (ev: KeyboardEvent<HTMLElement>) => void;
  onPointerEnter?: (ev: unknown) => void;
  onFocus?: (ev: unknown) => void;
};

/** Dropdown menu. Before the implementation is loaded, the trigger is rendered as is; its first
 *  activation loads the module and mounts the real menu already open. */
export function Menu(props: MenuProps) {
  const [ready, setReady] = useState(() => impl !== null);
  const [openNow, setOpenNow] = useState(false);
  const controlledOpen = props.open;
  useEffect(() => {
    if (!ready && controlledOpen) void loadMenus().then(() => setReady(true));
  }, [ready, controlledOpen]);
  if (ready && impl) return createElement(impl.Menu, { ...props, defaultOpen: openNow });
  const trigger = props.trigger as ReactElement<TriggerProps>;
  const open = () =>
    void loadMenus().then(() => {
      setOpenNow(true);
      setReady(true);
      props.onOpenChange?.(true);
    });
  return cloneElement(trigger, {
    'aria-haspopup': 'menu',
    'aria-expanded': false,
    onClick: (ev: MouseEvent<HTMLElement>) => {
      trigger.props.onClick?.(ev);
      if (!ev.defaultPrevented) open();
    },
    onKeyDown: (ev: KeyboardEvent<HTMLElement>) => {
      trigger.props.onKeyDown?.(ev);
      if (ev.key === 'ArrowDown' && !ev.defaultPrevented) {
        ev.preventDefault();
        open();
      }
    },
    onPointerEnter: (ev: unknown) => {
      trigger.props.onPointerEnter?.(ev);
      void loadMenus();
    },
    onFocus: (ev: unknown) => {
      trigger.props.onFocus?.(ev);
      void loadMenus();
    },
  } as Partial<TriggerProps> & Record<string, unknown>);
}

type ListMenuProps = ComponentProps<ImplModule['ListMenu']>;

/** Context menu of a list, anchored at a point (see MenuImpl.ListMenu). Renders nothing until
 *  it is first opened. */
export function ListMenu(props: ListMenuProps) {
  const [ready, setReady] = useState(() => impl !== null);
  useEffect(() => {
    if (!ready && props.open) void loadMenus().then(() => setReady(true));
  }, [ready, props.open]);
  return ready && impl ? createElement(impl.ListMenu, props) : null;
}

export function MenuItem(p: ComponentProps<ImplModule['MenuItem']>) {
  return impl ? <impl.MenuItem {...p} /> : null;
}

export function MenuCheckboxItem(p: ComponentProps<ImplModule['MenuCheckboxItem']>) {
  return impl ? <impl.MenuCheckboxItem {...p} /> : null;
}

export function MenuRadioItem(p: ComponentProps<ImplModule['MenuRadioItem']>) {
  return impl ? <impl.MenuRadioItem {...p} /> : null;
}

export function MenuSeparator() {
  return impl ? <impl.MenuSeparator /> : null;
}

export function Submenu(p: ComponentProps<ImplModule['Submenu']>) {
  return impl ? <impl.Submenu {...p} /> : null;
}

export function MenuRadioGroup<T extends string | number>(p: {
  value: T;
  onValueChange: (v: T) => void;
  children: ReactNode;
}) {
  const Group = impl?.MenuRadioGroup<T>;
  return Group ? <Group {...p} /> : null;
}

export function MenuGroupLabel({ children }: { children: ReactNode }) {
  return <div className="menu-heading">{children}</div>;
}

/** Opens the context menu that owns `el` (one shared menu per list), anchored under the
 *  button: lists of thousands of items keep a single menu instead of one per row. */
export function openContextMenuFrom(el: HTMLElement) {
  const r = el.getBoundingClientRect();
  el.dispatchEvent(
    new MouseEvent('contextmenu', {
      bubbles: true,
      cancelable: true,
      clientX: r.left,
      clientY: r.bottom + 4,
      button: 2,
    }),
  );
}

/** State of a list's shared context menu: open it from a right click (or Shift+F10, or a row's
 *  "more" button through openContextMenuFrom) on one of the list's items. */
export function useListMenu<T>(find: (target: HTMLElement) => T | undefined) {
  const [at, setAt] = useState<{ item: T; point: { x: number; y: number } } | null>(null);
  const [open, setOpen] = useState(false);
  const returnFocus = useRef<HTMLElement | null>(null);
  const onContextMenu = (ev: MouseEvent<HTMLElement>) => {
    const item = find(ev.target as HTMLElement);
    if (item === undefined) return; // outside the items: the browser's own menu
    ev.preventDefault();
    const active = document.activeElement;
    returnFocus.current = active instanceof HTMLElement && ev.currentTarget.contains(active) ? active : null;
    setAt({ item, point: { x: ev.clientX, y: ev.clientY } });
    setOpen(true);
    void loadMenus();
  };
  return { item: at?.item ?? null, point: at?.point ?? { x: 0, y: 0 }, open, setOpen, returnFocus, onContextMenu };
}
