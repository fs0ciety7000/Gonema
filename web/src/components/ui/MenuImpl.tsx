import { Menu as BaseMenu } from '@base-ui/react/menu';
import { Check, ChevronRight } from 'lucide-react';
import { type ReactElement, type ReactNode, type RefObject, useMemo, useState } from 'react';
import { cx } from './cx';
import { overlayRoot } from './overlayRoot';

/* Base UI implementation of the menus, loaded on demand through the facade in Menu.tsx
   (Base UI and Floating UI stay out of the first screen's chunk).

   Menus use Base UI for focus, typeahead, keyboard and ARIA; all styling is .menu
   from the design system. Pointer-opened menus pop in from their trigger
   (transform-origin = Base UI's --transform-origin); keyboard-opened ones appear
   without animation (motion.md §7). */

type MenuProps = {
  trigger: ReactElement;
  label: string;
  heading?: ReactNode;
  children: ReactNode;
  align?: 'start' | 'center' | 'end';
  side?: 'top' | 'bottom' | 'left' | 'right';
  open?: boolean;
  onOpenChange?: (open: boolean) => void;
  className?: string;
  /** Mounted already open (the facade loaded this module on the trigger's first click). */
  defaultOpen?: boolean;
};

function isKeyboard(ev: Event | undefined) {
  return !!ev && ev.type.startsWith('key');
}

export function Menu({
  trigger,
  label,
  heading,
  children,
  align = 'start',
  side = 'bottom',
  open,
  onOpenChange,
  className,
  defaultOpen,
}: MenuProps) {
  const [instant, setInstant] = useState(false);
  return (
    <BaseMenu.Root
      {...(open !== undefined ? { open } : { defaultOpen })}
      onOpenChange={(o, details) => {
        if (o) setInstant(isKeyboard(details.event));
        onOpenChange?.(o);
      }}
    >
      <BaseMenu.Trigger render={trigger} />
      <BaseMenu.Portal container={overlayRoot()}>
        <BaseMenu.Positioner sideOffset={6} align={align} side={side} className="menu-positioner">
          <BaseMenu.Popup className={cx('menu', className)} aria-label={label} data-instant={instant || undefined}>
            {heading && (
              <div className="menu-heading" aria-hidden="true">
                {heading}
              </div>
            )}
            {children}
          </BaseMenu.Popup>
        </BaseMenu.Positioner>
      </BaseMenu.Portal>
    </BaseMenu.Root>
  );
}

export type ListMenuProps = {
  open: boolean;
  onOpenChange: (open: boolean) => void;
  /** Where the menu opens: the pointer, or under the row's "more" button. */
  point: { x: number; y: number };
  label: string;
  heading?: ReactNode;
  children: ReactNode;
  /** Gets the focus back when the menu closes (the card or row it was opened from). */
  returnFocus: RefObject<HTMLElement | null>;
};

/** One context menu for a whole list (grid, table), anchored at a point: lists of thousands
 *  of items keep a single menu, and the list itself is never wrapped by Base UI. */
export function ListMenu({ open, onOpenChange, point, label, heading, children, returnFocus }: ListMenuProps) {
  const anchor = useMemo(
    () => ({ getBoundingClientRect: () => DOMRect.fromRect({ x: point.x, y: point.y, width: 0, height: 0 }) }),
    [point],
  );
  return (
    <BaseMenu.Root open={open} onOpenChange={(o) => onOpenChange(o)}>
      <BaseMenu.Portal container={overlayRoot()}>
        <BaseMenu.Positioner anchor={anchor} side="bottom" align="start" className="menu-positioner">
          <BaseMenu.Popup className="menu ctx-menu" aria-label={label} finalFocus={returnFocus}>
            {heading && (
              <div className="menu-heading" aria-hidden="true">
                {heading}
              </div>
            )}
            {children}
          </BaseMenu.Popup>
        </BaseMenu.Positioner>
      </BaseMenu.Portal>
    </BaseMenu.Root>
  );
}

type ItemProps = {
  icon?: ReactNode;
  hint?: ReactNode;
  danger?: boolean;
  disabled?: boolean;
  onSelect?: () => void;
  children: ReactNode;
  closeOnClick?: boolean;
};

export function MenuItem({ icon, hint, danger, disabled, onSelect, children, closeOnClick = true }: ItemProps) {
  return (
    <BaseMenu.Item
      className={cx(danger && 'danger')}
      disabled={disabled}
      aria-disabled={disabled || undefined}
      closeOnClick={closeOnClick}
      onClick={onSelect}
    >
      {icon}
      <span className="label">{children}</span>
      {hint && <span className="kbd-hint">{hint}</span>}
    </BaseMenu.Item>
  );
}

export function MenuCheckboxItem({
  icon,
  checked,
  onCheckedChange,
  children,
  disabled,
}: {
  icon?: ReactNode;
  checked: boolean;
  onCheckedChange: (checked: boolean) => void;
  children: ReactNode;
  disabled?: boolean;
}) {
  return (
    <BaseMenu.CheckboxItem checked={checked} onCheckedChange={onCheckedChange} disabled={disabled} closeOnClick={false}>
      {icon}
      <span className="label">{children}</span>
      <span className="check-mark">{checked && <Check className="i i-sm" aria-hidden="true" />}</span>
    </BaseMenu.CheckboxItem>
  );
}

export function MenuRadioGroup<T extends string | number>({
  value,
  onValueChange,
  children,
}: {
  value: T;
  onValueChange: (v: T) => void;
  children: ReactNode;
}) {
  return (
    <BaseMenu.RadioGroup value={value} onValueChange={(v) => onValueChange(v as T)}>
      {children}
    </BaseMenu.RadioGroup>
  );
}

export function MenuRadioItem({
  value,
  children,
  selected,
}: {
  value: string | number;
  children: ReactNode;
  selected: boolean;
}) {
  return (
    <BaseMenu.RadioItem value={value} closeOnClick>
      <span className="check-mark">{selected && <Check className="i i-sm" aria-hidden="true" />}</span>
      <span className="label">{children}</span>
    </BaseMenu.RadioItem>
  );
}

export function MenuSeparator() {
  return <BaseMenu.Separator />;
}

export function MenuGroupLabel({ children }: { children: ReactNode }) {
  return <div className="menu-heading">{children}</div>;
}

export function Submenu({
  icon,
  label,
  children,
  disabled,
}: {
  icon?: ReactNode;
  label: ReactNode;
  children: ReactNode;
  disabled?: boolean;
}) {
  return (
    <BaseMenu.SubmenuRoot>
      <BaseMenu.SubmenuTrigger disabled={disabled}>
        {icon}
        <span className="label">{label}</span>
        <ChevronRight className="i i-sm" aria-hidden="true" />
      </BaseMenu.SubmenuTrigger>
      <BaseMenu.Portal container={overlayRoot()}>
        <BaseMenu.Positioner sideOffset={4} alignOffset={-6} className="menu-positioner">
          <BaseMenu.Popup className="menu submenu-popup">{children}</BaseMenu.Popup>
        </BaseMenu.Positioner>
      </BaseMenu.Portal>
    </BaseMenu.SubmenuRoot>
  );
}
