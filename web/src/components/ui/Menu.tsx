import { ContextMenu as BaseContextMenu } from '@base-ui/react/context-menu';
import { Menu as BaseMenu } from '@base-ui/react/menu';
import { Check, ChevronRight } from 'lucide-react';
import { type ReactElement, type ReactNode, useState } from 'react';
import { cx } from './cx';
import { overlayRoot } from './overlayRoot';

/* Menus use Base UI for focus, typeahead, keyboard and ARIA; all styling is .menu
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
}: MenuProps) {
  const [instant, setInstant] = useState(false);
  return (
    <BaseMenu.Root
      {...(open !== undefined ? { open } : {})}
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

type ContextMenuProps = {
  /** The element that opens the menu on right click / long press / Shift+F10 (rendered as is). */
  children: ReactElement;
  label: string;
  heading?: ReactNode;
  items: ReactNode;
  onOpenChange?: (open: boolean) => void;
};

export function ContextMenu({ children, label, heading, items, onOpenChange }: ContextMenuProps) {
  return (
    <BaseContextMenu.Root {...(onOpenChange ? { onOpenChange: (o: boolean) => onOpenChange(o) } : {})}>
      <BaseContextMenu.Trigger render={children} />
      <BaseContextMenu.Portal container={overlayRoot()}>
        <BaseContextMenu.Positioner className="menu-positioner">
          <BaseContextMenu.Popup className="menu" aria-label={label}>
            {heading && (
              <div className="menu-heading" aria-hidden="true">
                {heading}
              </div>
            )}
            {items}
          </BaseContextMenu.Popup>
        </BaseContextMenu.Positioner>
      </BaseContextMenu.Portal>
    </BaseContextMenu.Root>
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
