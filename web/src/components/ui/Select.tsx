import { Select as BaseSelect } from '@base-ui/react/select';
import { Check, ChevronsUpDown } from 'lucide-react';
import { cx } from './cx';

// ----------------------------------------------------------------- select

export type SelectOption<T extends string | number> = { value: T; label: string; hint?: string };

type SelectProps<T extends string | number> = {
  value: T | null;
  options: readonly SelectOption<T>[];
  onValueChange: (value: T) => void;
  /** id of the visible label (the trigger is labelled by it and by its value). */
  labelledBy?: string;
  ariaLabel?: string;
  placeholder?: string;
  mono?: boolean;
  disabled?: boolean;
  id?: string;
};

/** Button that opens a listbox (Base UI Select), styled as .input.select. */
export function Select<T extends string | number>({
  value,
  options,
  onValueChange,
  labelledBy,
  ariaLabel,
  placeholder = 'Choose…',
  mono,
  disabled,
  id,
}: SelectProps<T>) {
  const current = options.find((o) => o.value === value);
  return (
    <BaseSelect.Root
      value={value}
      onValueChange={(v) => {
        if (v !== null) onValueChange(v as T);
      }}
      disabled={disabled}
    >
      <BaseSelect.Trigger
        className={cx('input select', mono && 'mono', disabled && 'is-disabled')}
        aria-labelledby={labelledBy}
        aria-label={ariaLabel}
        id={id}
      >
        <span className={cx('value', mono && 't-mono')}>{current?.label ?? placeholder}</span>
        <ChevronsUpDown className="i" aria-hidden="true" />
      </BaseSelect.Trigger>
      <BaseSelect.Portal>
        <BaseSelect.Positioner sideOffset={6} alignItemWithTrigger={false} className="menu-positioner">
          <BaseSelect.Popup className="menu select-popup">
            <BaseSelect.List>
              {options.map((o) => (
                <BaseSelect.Item key={String(o.value)} value={o.value} role="option">
                  <span className="check-mark">
                    <BaseSelect.ItemIndicator>
                      <Check className="i i-sm" aria-hidden="true" />
                    </BaseSelect.ItemIndicator>
                  </span>
                  <BaseSelect.ItemText className="label">{o.label}</BaseSelect.ItemText>
                  {o.hint && <span className="kbd-hint">{o.hint}</span>}
                </BaseSelect.Item>
              ))}
            </BaseSelect.List>
          </BaseSelect.Popup>
        </BaseSelect.Positioner>
      </BaseSelect.Portal>
    </BaseSelect.Root>
  );
}
