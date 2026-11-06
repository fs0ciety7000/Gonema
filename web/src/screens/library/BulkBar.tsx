import { Bookmark, BookmarkMinus, Search, SlidersHorizontal, Trash2, X } from 'lucide-react';
import { useEffect } from 'react';
import type { LibraryItem } from '@/api/model';
import { useProfiles } from '@/api/queries';
import { useMovieActions } from '@/components/movie/useMovieActions';
import { Button, IconButton } from '@/components/ui/Button';
import { Menu, MenuRadioGroup, MenuRadioItem } from '@/components/ui/Menu';
import { Tooltip } from '@/components/ui/Tooltip';
import { isTypingTarget } from '@/lib/keys';

/** Floating glass bar for the selection. Tags, Move and Delete files come with their dialogs in lot 2. */
export function BulkBar({ selected, onClear }: { selected: readonly LibraryItem[]; onClear: () => void }) {
  const a = useMovieActions();
  const profiles = useProfiles();
  const n = selected.length;

  useEffect(() => {
    document.documentElement.toggleAttribute('data-bulkbar', n > 0);
    return () => document.documentElement.removeAttribute('data-bulkbar');
  }, [n]);

  useEffect(() => {
    const onKey = (ev: KeyboardEvent) => {
      if (ev.key !== 'Escape' || ev.defaultPrevented) return;
      if (isTypingTarget(ev.target, '[role="menu"], [role="dialog"], [role="listbox"]')) return;
      onClear();
    };
    window.addEventListener('keydown', onKey);
    return () => window.removeEventListener('keydown', onKey);
  }, [onClear]);

  if (!n) return null;
  const targets = selected.map((m) => ({ id: m.id, title: m.title }));
  const allMonitored = selected.every((m) => m.monitored);
  const prevMonitored = new Map(selected.map((m) => [m.id, m.monitored]));
  const prevProfile = new Map(selected.map((m) => [m.id, m.qualityProfileId]));
  const common = selected.every((m) => m.qualityProfileId === selected[0]?.qualityProfileId)
    ? selected[0]?.qualityProfileId
    : -1;

  return (
    <section aria-label="Selection">
      <div
        className="bulkbar"
        role="toolbar"
        aria-label={`Bulk actions for ${n} selected ${n === 1 ? 'film' : 'films'}`}
      >
        <span className="count">
          <span className="n">{n.toLocaleString('en-US')}</span>selected
        </span>
        <span className="divider" aria-hidden="true" />
        {allMonitored ? (
          <Button
            variant="ghost"
            size="sm"
            icon={<BookmarkMinus className="i" aria-hidden="true" />}
            onClick={() => void a.setMonitored(targets, false, prevMonitored)}
          >
            Unmonitor
          </Button>
        ) : (
          <Button
            variant="ghost"
            size="sm"
            icon={<Bookmark className="i" aria-hidden="true" />}
            onClick={() => void a.setMonitored(targets, true, prevMonitored)}
          >
            Monitor
          </Button>
        )}
        {profiles.data && profiles.data.length > 0 && (
          <Menu
            label="Quality profile"
            side="top"
            trigger={
              <Button variant="ghost" size="sm" icon={<SlidersHorizontal className="i" aria-hidden="true" />}>
                Quality profile
              </Button>
            }
          >
            <MenuRadioGroup
              value={common ?? -1}
              onValueChange={(id) => {
                const p = profiles.data?.find((x) => x.id === id);
                if (p) void a.setProfile(targets, p.id, p.name, prevProfile);
              }}
            >
              {profiles.data.map((p) => (
                <MenuRadioItem key={p.id} value={p.id} selected={p.id === common}>
                  {p.name}
                </MenuRadioItem>
              ))}
            </MenuRadioGroup>
          </Menu>
        )}
        <Button
          variant="ghost"
          size="sm"
          icon={<Search className="i" aria-hidden="true" />}
          onClick={() => void a.search(targets)}
        >
          Search
        </Button>
        <span className="divider" aria-hidden="true" />
        <Button
          variant="ghost"
          size="sm"
          className="bulk-danger"
          icon={<Trash2 className="i" aria-hidden="true" />}
          onClick={() => a.remove(targets, onClear)}
        >
          Remove
        </Button>
        <Tooltip content="Clear selection" shortcut="Esc">
          <IconButton size="sm" label="Clear selection (Esc)" onClick={onClear}>
            <X className="i" aria-hidden="true" />
          </IconButton>
        </Tooltip>
      </div>
    </section>
  );
}
