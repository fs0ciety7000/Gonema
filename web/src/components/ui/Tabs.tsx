import { type KeyboardEvent, type ReactNode, useRef } from 'react';
import { cx } from './cx';

export type TabItem<T extends string> = { value: T; label: ReactNode; count?: ReactNode; className?: string };

type TabsProps<T extends string> = {
  value: T;
  items: readonly TabItem<T>[];
  onChange: (value: T) => void;
  label: string;
  /** id of the panel the tabs control (one shared panel: the list below). */
  panelId: string;
  className?: string;
};

/** Tab list with roving focus and automatic activation (WAI-ARIA tabs pattern). Never animated by keyboard. */
export function Tabs<T extends string>({ value, items, onChange, label, panelId, className }: TabsProps<T>) {
  const refs = useRef<Array<HTMLButtonElement | null>>([]);
  const onKeyDown = (ev: KeyboardEvent) => {
    const i = items.findIndex((t) => t.value === value);
    let next = -1;
    if (ev.key === 'ArrowRight') next = (i + 1) % items.length;
    else if (ev.key === 'ArrowLeft') next = (i - 1 + items.length) % items.length;
    else if (ev.key === 'Home') next = 0;
    else if (ev.key === 'End') next = items.length - 1;
    if (next < 0) return;
    ev.preventDefault();
    const t = items[next];
    if (!t) return;
    onChange(t.value);
    refs.current[next]?.focus();
  };
  return (
    <div className={cx('tabs', className)} role="tablist" aria-label={label}>
      {items.map((t, i) => (
        <button
          key={t.value}
          ref={(el) => {
            refs.current[i] = el;
          }}
          type="button"
          role="tab"
          id={`tab-${panelId}-${t.value}`}
          aria-selected={t.value === value}
          aria-controls={panelId}
          tabIndex={t.value === value ? 0 : -1}
          className={t.className}
          onClick={() => onChange(t.value)}
          onKeyDown={onKeyDown}
        >
          {t.label}
          {t.count !== undefined && <span className="count">{t.count}</span>}
        </button>
      ))}
    </div>
  );
}
