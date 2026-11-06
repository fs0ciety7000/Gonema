import { type KeyboardEvent, type ReactNode, useRef } from 'react';
import { cx } from './cx';

export type SegmentedOption<T extends string> = {
  value: T;
  label: ReactNode;
  /** Accessible name when the label is an icon. */
  ariaLabel?: string;
  title?: string;
  disabled?: boolean;
};

type SegmentedProps<T extends string> = {
  value: T;
  options: readonly SegmentedOption<T>[];
  onChange: (value: T) => void;
  label: string;
  iconOnly?: boolean;
  className?: string;
};

const STEP: Record<string, number> = { ArrowRight: 1, ArrowDown: 1, ArrowLeft: -1, ArrowUp: -1 };

/** Radio group with roving focus (arrow keys move and select, as native radios do). */
export function Segmented<T extends string>({
  value,
  options,
  onChange,
  label,
  iconOnly,
  className,
}: SegmentedProps<T>) {
  const refs = useRef<Array<HTMLButtonElement | null>>([]);
  const enabled = options.filter((o) => !o.disabled);
  const onKeyDown = (ev: KeyboardEvent) => {
    const dir = STEP[ev.key] ?? 0;
    if (!dir) return;
    ev.preventDefault();
    const i = enabled.findIndex((o) => o.value === value);
    const next = enabled[(i + dir + enabled.length) % enabled.length];
    if (!next) return;
    onChange(next.value);
    refs.current[options.indexOf(next)]?.focus();
  };
  return (
    <div className={cx('segmented', iconOnly && 'icon-only', className)} role="radiogroup" aria-label={label}>
      {options.map((o, i) => (
        // biome-ignore lint/a11y/useSemanticElements: segmented control of the design system (button radios, roving focus)
        <button
          key={o.value}
          ref={(el) => {
            refs.current[i] = el;
          }}
          type="button"
          role="radio"
          aria-checked={o.value === value}
          aria-label={o.ariaLabel}
          title={o.title}
          disabled={o.disabled}
          tabIndex={o.value === value ? 0 : -1}
          onClick={() => onChange(o.value)}
          onKeyDown={onKeyDown}
        >
          {o.label}
        </button>
      ))}
    </div>
  );
}
