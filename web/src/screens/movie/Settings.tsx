import { Check, X } from 'lucide-react';
import { type KeyboardEvent, useId, useState } from 'react';
import type { MoviePatch } from '@/api/queries';
import { useProfiles, useRootFolders } from '@/api/queries';
import { useMovieActions } from '@/components/movie/useMovieActions';
import { Select } from '@/components/ui/Select';
import { Switch } from '@/components/ui/Switch';
import { toast } from '@/components/ui/Toast';
import { formatBytes } from '@/lib/format';
import { qualityName } from '@/lib/quality';

type MovieSettingsValue = {
  id: number;
  title: string;
  monitored: boolean;
  qualityProfileId: number;
  minimumAvailability: 'announced' | 'in_cinemas' | 'released';
  rootFolderId: number;
  tags: string[];
};

const AVAILABILITY = [
  { value: 'announced', label: 'Announced (as soon as it is known)' },
  { value: 'in_cinemas', label: 'In cinemas' },
  { value: 'released', label: 'Released (digital or physical)' },
] as const;

/** Per-film settings: every change is applied at once (optimistic), saved, and offers Undo. */
export function FilmSettings({ movie }: { movie: MovieSettingsValue }) {
  const a = useMovieActions();
  const profiles = useProfiles();
  const roots = useRootFolders();
  const [saved, setSaved] = useState<'idle' | 'saving' | 'saved'>('idle');
  const [tagDraft, setTagDraft] = useState('');
  const [tagError, setTagError] = useState<string | null>(null);
  const ids = { mon: useId(), prof: useId(), avail: useId(), tags: useId(), root: useId() };
  const t = { id: movie.id, title: movie.title };

  const change = async (label: string, patch: MoviePatch, undo: MoviePatch) => {
    setSaved('saving');
    const ok = await a.patch([movie.id], patch);
    setSaved(ok ? 'saved' : 'idle');
    if (ok)
      toast.show({
        title: label,
        icon: <Check className="i" />,
        undo: () => void a.patch([movie.id], undo),
      });
  };

  const profile = profiles.data?.find((p) => p.id === movie.qualityProfileId);
  const root = roots.data?.find((r) => r.id === movie.rootFolderId);

  const addTag = (raw: string) => {
    const tag = raw.trim().toLowerCase().replace(/\s+/g, '-');
    if (!tag) return;
    if (!/^[a-z0-9][a-z0-9_-]{0,39}$/.test(tag)) {
      setTagError('Tags use letters, digits, - and _, up to 40 characters.');
      return;
    }
    if (movie.tags.includes(tag)) {
      setTagDraft('');
      return;
    }
    if (movie.tags.length >= 20) {
      setTagError('A film can carry up to 20 tags.');
      return;
    }
    setTagError(null);
    setTagDraft('');
    void change(`Tagged ${movie.title} “${tag}”`, { tags: [...movie.tags, tag] }, { tags: movie.tags });
  };

  const onTagKey = (ev: KeyboardEvent<HTMLInputElement>) => {
    if (ev.key === 'Enter' || ev.key === ',') {
      ev.preventDefault();
      addTag(tagDraft);
    } else if (ev.key === 'Backspace' && !tagDraft && movie.tags.length) {
      const last = movie.tags[movie.tags.length - 1]!;
      void change(`Removed the tag “${last}”`, { tags: movie.tags.slice(0, -1) }, { tags: movie.tags });
    }
  };

  return (
    <section className="panel" aria-labelledby="set-h">
      <div className="panel-hd">
        <h2 id="set-h">Film settings</h2>
        <span className="saved" role="status">
          {saved === 'saving' ? (
            'Saving…'
          ) : saved === 'saved' ? (
            <>
              <Check className="i i-xs" aria-hidden="true" />
              Saved
            </>
          ) : null}
        </span>
      </div>
      <div className="panel-bd settings">
        <div className="switch-row">
          <div className="t">
            <b id={ids.mon}>Monitored</b>
            <span>Search for it and for upgrades</span>
          </div>
          <Switch
            tint
            checked={movie.monitored}
            aria-labelledby={ids.mon}
            onCheckedChange={(v) => void a.setMonitored([t], v)}
          />
        </div>
        {profiles.data && (
          <div className="field">
            <span className="field-label" id={ids.prof}>
              Quality profile
            </span>
            <Select
              labelledBy={ids.prof}
              value={movie.qualityProfileId}
              options={profiles.data.map((p) => ({ value: p.id, label: p.name }))}
              onValueChange={(id) => {
                const p = profiles.data?.find((x) => x.id === id);
                if (p && id !== movie.qualityProfileId)
                  void a.setProfile([t], p.id, p.name, new Map([[movie.id, movie.qualityProfileId]]));
              }}
            />
            {profile && (
              <span className="field-hint">
                Cutoff {qualityName(profile.cutoffQuality)} · min score {profile.minScore}
                {profile.upgradeAllowed ? '' : ' · no upgrades'}
              </span>
            )}
          </div>
        )}
        <div className="field">
          <span className="field-label" id={ids.avail}>
            Minimum availability
          </span>
          <Select
            labelledBy={ids.avail}
            value={movie.minimumAvailability}
            options={AVAILABILITY}
            onValueChange={(v) =>
              void change(
                `Searching once ${AVAILABILITY.find((x) => x.value === v)
                  ?.label.split(' (')[0]
                  ?.toLowerCase()}`,
                { minimumAvailability: v },
                { minimumAvailability: movie.minimumAvailability },
              )
            }
          />
        </div>
        <div className="field">
          <label className="field-label" htmlFor={ids.tags}>
            Tags
          </label>
          <div className="tags-edit" aria-invalid={tagError ? true : undefined}>
            {movie.tags.map((tag) => (
              <span className="tag" key={tag}>
                {tag}
                <button
                  type="button"
                  className="tag-remove"
                  aria-label={`Remove tag ${tag}`}
                  onClick={() =>
                    void change(
                      `Removed the tag “${tag}”`,
                      { tags: movie.tags.filter((x) => x !== tag) },
                      { tags: movie.tags },
                    )
                  }
                >
                  <X className="i i-xs" aria-hidden="true" />
                </button>
              </span>
            ))}
            <input
              id={ids.tags}
              placeholder="Add tag…"
              value={tagDraft}
              maxLength={40}
              aria-describedby={tagError ? `${ids.tags}-err` : undefined}
              onChange={(ev) => {
                setTagDraft(ev.target.value);
                setTagError(null);
              }}
              onKeyDown={onTagKey}
              onBlur={() => tagDraft && addTag(tagDraft)}
            />
          </div>
          {tagError && (
            <span className="field-error" id={`${ids.tags}-err`}>
              {tagError}
            </span>
          )}
        </div>
        {roots.data && roots.data.length > 0 && (
          <div className="field">
            <span className="field-label" id={ids.root}>
              Root folder
              {root?.freeSpace != null && <span className="t-muted root-free">{formatBytes(root.freeSpace)} free</span>}
            </span>
            <Select
              mono
              labelledBy={ids.root}
              value={movie.rootFolderId}
              options={roots.data.map((r) => ({ value: r.id, label: r.path }))}
              onValueChange={(id) =>
                void change(
                  `Root folder set to ${roots.data?.find((r) => r.id === id)?.path ?? 'the new folder'}`,
                  { rootFolderId: id },
                  { rootFolderId: movie.rootFolderId },
                )
              }
            />
            <span className="field-hint">Changing it does not move files already on disk.</span>
          </div>
        )}
      </div>
    </section>
  );
}
