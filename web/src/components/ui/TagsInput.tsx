import { X } from 'lucide-react';
import { type KeyboardEvent, useId, useState } from 'react';

/** Normalises a tag label the way the server stores it (trimmed, lower case, inner spaces as dashes). */
export function normaliseTag(raw: string): string {
  return raw
    .trim()
    .toLowerCase()
    .replace(/\s+/g, '-')
    .replace(/[^a-z0-9._-]/g, '')
    .slice(0, 64);
}

/** Tag editor: Enter or comma adds, Backspace on an empty field removes the last tag. */
export function TagsInput({
  value,
  onChange,
  id,
  suggestions = [],
  placeholder = 'Add tag…',
  describedBy,
}: {
  value: readonly string[];
  onChange: (tags: string[]) => void;
  id?: string;
  suggestions?: readonly string[];
  placeholder?: string;
  describedBy?: string;
}) {
  const [draft, setDraft] = useState('');
  const listId = useId();
  const add = (raw: string) => {
    const t = normaliseTag(raw);
    if (t && !value.includes(t)) onChange([...value, t]);
    setDraft('');
  };
  const onKeyDown = (ev: KeyboardEvent<HTMLInputElement>) => {
    if (ev.key === 'Enter' || ev.key === ',') {
      if (!draft.trim()) return;
      ev.preventDefault();
      ev.stopPropagation();
      add(draft);
    } else if (ev.key === 'Backspace' && !draft && value.length) {
      onChange(value.slice(0, -1));
    }
  };
  const free = suggestions.filter((s) => !value.includes(s));
  return (
    <div className="tags-edit">
      {value.map((t) => (
        <span className="tag" key={t}>
          {t}
          <button
            type="button"
            className="tag-x"
            aria-label={`Remove tag ${t}`}
            onClick={() => onChange(value.filter((x) => x !== t))}
          >
            <X className="i i-xs" aria-hidden="true" />
          </button>
        </span>
      ))}
      <input
        id={id}
        value={draft}
        placeholder={placeholder}
        list={free.length ? listId : undefined}
        aria-describedby={describedBy}
        onChange={(ev) => {
          const v = ev.target.value;
          if (v.endsWith(',')) add(v.slice(0, -1));
          else setDraft(v);
        }}
        onKeyDown={onKeyDown}
        onBlur={() => draft.trim() && add(draft)}
      />
      {free.length > 0 && (
        <datalist id={listId}>
          {free.map((s) => (
            <option key={s} value={s} />
          ))}
        </datalist>
      )}
    </div>
  );
}
