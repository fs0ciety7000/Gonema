import { ListFilter, Plus } from 'lucide-react';
import { useId, useState } from 'react';
import { Button } from '@/components/ui/Button';
import { Chip } from '@/components/ui/Chip';
import { Input } from '@/components/ui/Field';
import { Popover } from '@/components/ui/Popover';
import { Segmented } from '@/components/ui/Segmented';
import { Select } from '@/components/ui/Select';
import { STATUS } from '@/lib/status';
import { type Filters, RESOLUTIONS, type Resolution, STATUSES } from './model';

const RES_LABEL: Record<Resolution, string> = { '2160p': '4K', '1080p': '1080p', '720p': '720p', sd: 'SD' };
const ADDED: ReadonlyArray<{ value: number; label: string }> = [
  { value: 7, label: 'Last 7 days' },
  { value: 30, label: 'Last 30 days' },
  { value: 90, label: 'Last 90 days' },
  { value: 365, label: 'Last year' },
];

export function countFilters(f: Filters): number {
  let n = 0;
  if (f.monitored !== undefined) n++;
  if (f.status?.length) n++;
  if (f.yearFrom || f.yearTo) n++;
  if (f.genre) n++;
  if (f.resolution) n++;
  if (f.added) n++;
  return n;
}

export function FiltersButton({
  filters,
  genres,
  onChange,
  defaultOpen = false,
}: {
  filters: Filters;
  genres: readonly string[];
  onChange: (f: Partial<Filters>) => void;
  defaultOpen?: boolean;
}) {
  const n = countFilters(filters);
  const yearId = useId();
  const genreId = useId();
  const addedId = useId();
  return (
    <Popover
      defaultOpen={defaultOpen}
      label="Filters"
      align="end"
      trigger={
        <Button
          variant="ghost"
          size="sm"
          icon={<ListFilter className="i" aria-hidden="true" />}
          aria-label={n ? `Filters ${n} active` : 'Filters'}
        >
          Filters
          {n > 0 && (
            <span className="badge badge-count" aria-hidden="true">
              {n}
            </span>
          )}
        </Button>
      }
    >
      <div className="filters-pop">
        <div className="fp-row">
          <span className="fp-label" id={`${yearId}-mon`}>
            Monitored
          </span>
          <Segmented
            label="Monitored"
            value={filters.monitored === undefined ? 'any' : filters.monitored ? 'yes' : 'no'}
            options={[
              { value: 'any', label: 'Any' },
              { value: 'yes', label: 'Monitored' },
              { value: 'no', label: 'Not monitored' },
            ]}
            onChange={(v) => onChange({ monitored: v === 'any' ? undefined : v === 'yes' })}
          />
        </div>
        <fieldset className="fp-row">
          <legend className="fp-label">Status</legend>
          <div className="fp-chips">
            {STATUSES.filter((s) => s !== 'upgraded').map((s) => {
              const on = filters.status?.includes(s) ?? false;
              const Icon = STATUS[s].icon;
              return (
                <Chip
                  key={s}
                  pressed={on}
                  onClick={() => {
                    const set = new Set(filters.status ?? []);
                    if (on) set.delete(s);
                    else set.add(s);
                    onChange({ status: set.size ? [...set] : undefined });
                  }}
                >
                  <Icon className="i" aria-hidden="true" />
                  {STATUS[s].label}
                </Chip>
              );
            })}
          </div>
        </fieldset>
        <div className="fp-row">
          <span className="fp-label">Resolution</span>
          <Segmented
            label="Resolution"
            value={filters.resolution ?? 'any'}
            options={[{ value: 'any', label: 'Any' }, ...RESOLUTIONS.map((r) => ({ value: r, label: RES_LABEL[r] }))]}
            onChange={(v) => onChange({ resolution: v === 'any' ? undefined : (v as Resolution) })}
          />
        </div>
        <div className="fp-row">
          <label className="fp-label" htmlFor={yearId}>
            Year
          </label>
          <div className="fp-years">
            <Input
              id={yearId}
              inputMode="numeric"
              placeholder="From"
              aria-label="From year"
              value={filters.yearFrom ?? ''}
              onChange={(ev) => {
                const v = Number.parseInt(ev.target.value, 10);
                onChange({ yearFrom: Number.isFinite(v) && v > 1800 ? v : undefined });
              }}
            />
            <span className="t-muted" aria-hidden="true">
              –
            </span>
            <Input
              inputMode="numeric"
              placeholder="To"
              aria-label="To year"
              value={filters.yearTo ?? ''}
              onChange={(ev) => {
                const v = Number.parseInt(ev.target.value, 10);
                onChange({ yearTo: Number.isFinite(v) && v > 1800 ? v : undefined });
              }}
            />
          </div>
        </div>
        {genres.length > 0 && (
          <div className="fp-row">
            <span className="fp-label" id={genreId}>
              Genre
            </span>
            <Select
              labelledBy={genreId}
              value={filters.genre ?? ''}
              options={[{ value: '', label: 'Any genre' }, ...genres.map((g) => ({ value: g, label: g }))]}
              onValueChange={(g) => onChange({ genre: g || undefined })}
            />
          </div>
        )}
        <div className="fp-row">
          <span className="fp-label" id={addedId}>
            Added
          </span>
          <Select
            labelledBy={addedId}
            value={filters.added ?? 0}
            options={[{ value: 0, label: 'Any time' }, ...ADDED]}
            onValueChange={(d) => onChange({ added: d || undefined })}
          />
        </div>
      </div>
    </Popover>
  );
}

/** "+ View": names the current filters and sort, stored on the server for everyone. */
export function SaveViewButton({
  onSave,
  saving,
  defaultOpen = false,
}: {
  onSave: (name: string) => Promise<boolean>;
  saving: boolean;
  defaultOpen?: boolean;
}) {
  const [open, setOpen] = useState(defaultOpen);
  const [name, setName] = useState('');
  const [error, setError] = useState<string | null>(null);
  const id = useId();
  return (
    <Popover
      label="Save as a view"
      open={open}
      onOpenChange={(o) => {
        setOpen(o);
        if (o) {
          setName('');
          setError(null);
        }
      }}
      trigger={
        <Button
          variant="ghost"
          size="sm"
          icon={<Plus className="i" aria-hidden="true" />}
          aria-label="Save current filters as a view"
        >
          View
        </Button>
      }
    >
      <form
        className="save-view"
        onSubmit={async (ev) => {
          ev.preventDefault();
          const n = name.trim();
          if (!n) {
            setError('Give the view a name, for example “4K to watch”.');
            return;
          }
          if (await onSave(n.slice(0, 60))) setOpen(false);
        }}
      >
        <label className="field-label" htmlFor={id}>
          Name of the view
        </label>
        <Input
          id={id}
          value={name}
          maxLength={60}
          invalid={!!error}
          aria-describedby={error ? `${id}-err` : undefined}
          onChange={(ev) => setName(ev.target.value)}
          placeholder="4K to watch"
          autoFocus
        />
        {error && (
          <span className="field-error" id={`${id}-err`}>
            {error}
          </span>
        )}
        <p className="field-hint">Saves the current filters and sort. Everyone using this server sees it.</p>
        <div className="row save-view-actions">
          <Button type="submit" variant="secondary" size="sm" loading={saving}>
            Save view
          </Button>
        </div>
      </form>
    </Popover>
  );
}
