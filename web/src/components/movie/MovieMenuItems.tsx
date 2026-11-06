import { useNavigate } from '@tanstack/react-router';
import {
  Bookmark,
  Copy,
  FileX2,
  Film,
  FolderInput,
  Pencil,
  RefreshCw,
  ScanSearch,
  Search,
  SlidersHorizontal,
  Tag,
  Trash2,
} from 'lucide-react';
import type { LibraryItem } from '@/api/model';
import { useProfiles, useRootFolders } from '@/api/queries';
import {
  MenuCheckboxItem,
  MenuItem,
  MenuRadioGroup,
  MenuRadioItem,
  MenuSeparator,
  Submenu,
} from '@/components/ui/Menu';
import { openMovieDialog } from './dialogStore';
import { joinPath, useMovieActions } from './useMovieActions';

type Props = {
  movie: Pick<LibraryItem, 'id' | 'title' | 'monitored' | 'qualityProfileId' | 'rootFolderId' | 'path'> & {
    /** Whether the film has a file on disk (rename and delete file need one). */
    hasFile?: boolean;
  };
  onOpen?: () => void;
};

/** Actions of one film, shared by the poster menu, the table row menu and the context menu. */
export function MovieMenuItems({ movie, onOpen }: Props) {
  const a = useMovieActions();
  const navigate = useNavigate();
  const profiles = useProfiles();
  const roots = useRootFolders();
  const t = { id: movie.id, title: movie.title };
  const root = roots.data?.find((r) => r.id === movie.rootFolderId)?.path;
  return (
    <>
      {onOpen && (
        <MenuItem icon={<Film className="i" aria-hidden="true" />} hint="↵" onSelect={onOpen}>
          Open
        </MenuItem>
      )}
      <MenuItem icon={<Search className="i" aria-hidden="true" />} hint="S" onSelect={() => void a.search([t])}>
        Search now
      </MenuItem>
      <MenuItem
        icon={<ScanSearch className="i" aria-hidden="true" />}
        hint="I"
        onSelect={() => void navigate({ to: '/films/$movieId/search', params: { movieId: movie.id } })}
      >
        Interactive search…
      </MenuItem>
      <MenuSeparator />
      <MenuCheckboxItem
        icon={<Bookmark className="i" aria-hidden="true" />}
        checked={movie.monitored}
        onCheckedChange={(v) => void a.setMonitored([t], v)}
      >
        Monitored
      </MenuCheckboxItem>
      {profiles.data && profiles.data.length > 0 && (
        <Submenu icon={<SlidersHorizontal className="i" aria-hidden="true" />} label="Quality profile">
          <MenuRadioGroup
            value={movie.qualityProfileId}
            onValueChange={(id) => {
              const p = profiles.data?.find((x) => x.id === id);
              if (p && id !== movie.qualityProfileId)
                void a.setProfile([t], p.id, p.name, new Map([[movie.id, movie.qualityProfileId]]));
            }}
          >
            {profiles.data.map((p) => (
              <MenuRadioItem key={p.id} value={p.id} selected={p.id === movie.qualityProfileId}>
                {p.name}
              </MenuRadioItem>
            ))}
          </MenuRadioGroup>
        </Submenu>
      )}
      <MenuItem
        icon={<Tag className="i" aria-hidden="true" />}
        hint="⇧T"
        onSelect={() => openMovieDialog({ kind: 'tags', targets: [t] })}
      >
        Edit tags…
      </MenuItem>
      <MenuItem
        icon={<FolderInput className="i" aria-hidden="true" />}
        onSelect={() => openMovieDialog({ kind: 'move', targets: [t] })}
      >
        Move…
      </MenuItem>
      {movie.hasFile !== false && (
        <MenuItem
          icon={<Pencil className="i" aria-hidden="true" />}
          onSelect={() => openMovieDialog({ kind: 'rename', targets: [t] })}
        >
          Rename files…
        </MenuItem>
      )}
      <MenuItem
        icon={<Copy className="i" aria-hidden="true" />}
        onSelect={() => void a.copyPath(joinPath(root, movie.path))}
      >
        Copy path
      </MenuItem>
      <MenuItem icon={<RefreshCw className="i" aria-hidden="true" />} onSelect={() => void a.refresh(t)}>
        Refresh metadata
      </MenuItem>
      <MenuSeparator />
      <MenuItem danger icon={<Trash2 className="i" aria-hidden="true" />} hint="⌫" onSelect={() => a.remove([t])}>
        Remove from library
      </MenuItem>
      {movie.hasFile !== false && (
        <MenuItem
          danger
          icon={<FileX2 className="i" aria-hidden="true" />}
          onSelect={() => openMovieDialog({ kind: 'deleteFile', target: t })}
        >
          Delete file…
        </MenuItem>
      )}
    </>
  );
}
