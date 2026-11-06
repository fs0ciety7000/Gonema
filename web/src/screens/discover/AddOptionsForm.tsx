import { Plus } from 'lucide-react';
import { useId } from 'react';
import { useProfiles, useRootFolders } from '@/api/queries';
import { Button } from '@/components/ui/Button';
import { Checkbox } from '@/components/ui/Checkbox';
import { Segmented } from '@/components/ui/Segmented';
import { Select } from '@/components/ui/Select';
import { TagsInput } from '@/components/ui/TagsInput';
import { formatBytes } from '@/lib/format';
import { type AddOptions, AVAILABILITY_OPTIONS, type Availability, type MonitorChoice } from './addOptions';

/** The add options (root folder, profile, availability, monitor, tags, search on add). */
export function AddOptionsForm({
  options,
  onChange,
  onSubmit,
  busy,
  disabled,
  folderName,
  collection,
  submitLabel = 'Add film',
  showKbd = true,
}: {
  options: AddOptions;
  onChange: (o: AddOptions) => void;
  onSubmit: () => void;
  busy?: boolean;
  disabled?: boolean;
  /** "Title (Year)": shown under the root folder as the folder Gonema creates. */
  folderName?: string;
  /** Name of the film's collection when known; null when it has none; undefined when unknown until added. */
  collection?: string | null;
  submitLabel?: string;
  showKbd?: boolean;
}) {
  const id = useId();
  const roots = useRootFolders();
  const profiles = useProfiles();
  const root = roots.data?.find((r) => r.id === options.rootFolderId);
  const set = (p: Partial<AddOptions>) => onChange({ ...options, ...p });
  const noRoots = roots.isSuccess && !roots.data.length;
  const noProfiles = profiles.isSuccess && !profiles.data.length;
  const monitorHint =
    options.monitor === 'collection'
      ? collection === null
        ? 'Not part of a collection on TMDB: only this film is added.'
        : collection
          ? `Adds the other films of ${collection} with these options, and new ones when announced.`
          : 'If the film belongs to a collection, its other films are added with these options.'
      : options.monitor === 'none'
        ? 'Added to the library only: Gonema will not search for it.'
        : collection
          ? `Part of ${collection}.`
          : collection === null
            ? 'Not part of a collection on TMDB.'
            : 'Gonema searches for it and for upgrades.';
  return (
    <form
      className="add-form"
      onSubmit={(ev) => {
        ev.preventDefault();
        onSubmit();
      }}
    >
      <div className="field">
        <span className="field-label" id={`${id}-rf`}>
          <span>Root folder</span>
          {root?.freeSpace ? <span className="t-muted fw-400">{formatBytes(root.freeSpace)} free</span> : null}
        </span>
        <Select
          value={options.rootFolderId || null}
          labelledBy={`${id}-rf`}
          mono
          placeholder={noRoots ? 'No root folder yet' : 'Choose a folder…'}
          disabled={noRoots}
          options={(roots.data ?? []).map((r) => ({ value: r.id, label: r.path }))}
          onValueChange={(v) => set({ rootFolderId: v })}
        />
        {root && folderName && (
          <span className="field-hint t-mono-sm">
            → {root.path.replace(/\/+$/, '')}/{folderName}/
          </span>
        )}
        {noRoots && (
          <span className="field-error">Add a root folder in Settings first (lot 3 brings that screen).</span>
        )}
      </div>
      <div className="two">
        <div className="field">
          <span className="field-label" id={`${id}-qp`}>
            Quality profile
          </span>
          <Select
            value={options.qualityProfileId || null}
            labelledBy={`${id}-qp`}
            disabled={noProfiles}
            options={(profiles.data ?? []).map((p) => ({ value: p.id, label: p.name }))}
            onValueChange={(v) => set({ qualityProfileId: v })}
          />
        </div>
        <div className="field">
          <span className="field-label" id={`${id}-ma`}>
            Minimum availability
          </span>
          <Select<Availability>
            value={options.minimumAvailability}
            labelledBy={`${id}-ma`}
            options={AVAILABILITY_OPTIONS}
            onValueChange={(v) => set({ minimumAvailability: v })}
          />
        </div>
      </div>
      <div className="field monitor">
        <span className="field-label" id={`${id}-mon`}>
          Monitor
        </span>
        <Segmented<MonitorChoice>
          label="Monitor"
          value={options.monitor}
          onChange={(v) => set({ monitor: v })}
          options={[
            { value: 'film', label: 'This film' },
            { value: 'collection', label: 'Whole collection', disabled: collection === null },
            { value: 'none', label: 'Don’t monitor' },
          ]}
        />
        <span className="field-hint">{monitorHint}</span>
      </div>
      <div className="field">
        <label className="field-label" htmlFor={`${id}-tags`}>
          Tags
        </label>
        <TagsInput id={`${id}-tags`} value={options.tags} onChange={(tags) => set({ tags })} />
      </div>
      <div className="actions">
        <Checkbox
          labelClassName="add-search"
          checked={options.searchOnAdd && options.monitor !== 'none'}
          disabled={options.monitor === 'none'}
          onCheckedChange={(v) => set({ searchOnAdd: v })}
          label="Start searching now"
        />
        <Button
          type="submit"
          variant="primary"
          loading={busy}
          disabled={disabled || noRoots || noProfiles || !options.rootFolderId || !options.qualityProfileId}
          icon={<Plus className="i" aria-hidden="true" />}
        >
          {submitLabel}
          {showKbd && (
            <span className="kbd kbd-on-btn" aria-hidden="true">
              ↵
            </span>
          )}
        </Button>
      </div>
    </form>
  );
}
