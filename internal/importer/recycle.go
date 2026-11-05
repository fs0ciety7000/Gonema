package importer

import (
	"context"
	"errors"
	"fmt"
	"io/fs"
	"log/slog"
	"path/filepath"
	"time"

	"github.com/fs0ciety7000/gonema/internal/events"
	"github.com/fs0ciety7000/gonema/internal/fsx"
	"github.com/fs0ciety7000/gonema/internal/store"
)

// RecycleEntry is a file in a recycle bin.
type RecycleEntry struct {
	fsx.RecycledItem
	Bin string `json:"bin"`
	// RootFolderID is the root folder of a per-root-folder bin, 0 for the single
	// configured bin.
	RootFolderID int64 `json:"rootFolderId,omitempty"`
}

type bin struct {
	dir    string
	rootID int64
}

// bins returns the recycle bins in use: the configured one, or one per root
// folder.
func (s *Service) bins(ctx context.Context, set *Settings) ([]bin, error) {
	if set.RecycleBin != "" {
		return []bin{{dir: set.RecycleBin}}, nil
	}
	rfs, err := s.st.Read().ListRootFolders(ctx)
	if err != nil {
		return nil, store.MapError(fmt.Errorf("importer: list root folders: %w", err))
	}
	out := make([]bin, 0, len(rfs))
	for _, rf := range rfs {
		out = append(out, bin{dir: binDir(set, rf.Path), rootID: rf.ID})
	}
	return out, nil
}

// ListRecycle returns the files of every recycle bin, oldest first per bin.
func (s *Service) ListRecycle(ctx context.Context) ([]RecycleEntry, error) {
	set, err := s.Settings(ctx)
	if err != nil {
		return nil, err
	}
	bins, err := s.bins(ctx, &set)
	if err != nil {
		return nil, err
	}
	out := []RecycleEntry{}
	for _, b := range bins {
		items, err := s.fs.ListRecycle(b.dir)
		if err != nil {
			if errors.Is(err, fs.ErrPermission) {
				s.log.Warn("recycle bin unreadable", slog.String("bin", b.dir), slog.Any("error", err))
				continue
			}
			return nil, fsErr(err, "cannot list the recycle bin %s", b.dir)
		}
		for _, it := range items {
			out = append(out, RecycleEntry{RecycledItem: it, Bin: b.dir, RootFolderID: b.rootID})
		}
	}
	return out, nil
}

// RestoreRecycled moves a recycled file back to where it was. The original
// location must be inside a root folder and free. The file is not registered
// again: the movie keeps its current file.
func (s *Service) RestoreRecycled(ctx context.Context, name string) (string, error) {
	set, err := s.Settings(ctx)
	if err != nil {
		return "", err
	}
	bins, err := s.bins(ctx, &set)
	if err != nil {
		return "", err
	}
	rfs, err := s.st.Read().ListRootFolders(ctx)
	if err != nil {
		return "", store.MapError(fmt.Errorf("importer: list root folders: %w", err))
	}
	inRoot := func(p string) bool {
		for _, rf := range rfs {
			rel, err := filepath.Rel(rf.Path, p)
			if err != nil || !fsx.Within(rf.Path, p) || rel == "." {
				continue
			}
			if _, err := s.fs.SafeJoin(rf.Path, rel); err == nil {
				return true
			}
		}
		return false
	}
	for _, b := range bins {
		if _, err := s.fs.Lstat(filepath.Join(b.dir, filepath.Base(name))); err != nil {
			continue
		}
		dst, err := s.fs.RestoreRecycled(ctx, b.dir, name, inRoot)
		switch {
		case errors.Is(err, fsx.ErrInvalidName):
			return "", userErr(ErrInvalid, err, "%q is not an item of the recycle bin", name)
		case errors.Is(err, fsx.ErrOutsideRoot):
			return "", userErr(ErrInvalid, err, "the original location of %s is not inside a root folder; move it by hand", name)
		case errors.Is(err, fs.ErrExist):
			return "", userErr(ErrInvalid, err, "a file already exists where %s was; move or rename it first", name)
		case err != nil:
			return "", fsErr(err, "cannot restore %s", name)
		}
		s.log.Info("recycled file restored", slog.String("item", name), slog.String("path", dst))
		s.publish(events.Event{Type: EventRecycleChanged, Data: map[string]any{"restored": name, "path": dst}})
		return dst, nil
	}
	return "", userErr(ErrNotFound, nil, "%q is not in the recycle bin", name)
}

// PurgeRecycle permanently deletes the recycled files older than olderThanDays
// (negative: the retention of the settings; 0: every file). It refuses unless
// confirm is true. It is never called automatically. Each deletion is written
// to history.
func (s *Service) PurgeRecycle(ctx context.Context, olderThanDays int, confirm bool) ([]RecycleEntry, error) {
	if !confirm {
		return nil, userErr(ErrConfirmRequired, nil, "purging the recycle bin deletes the files for good: confirm it (confirm=true)")
	}
	set, err := s.Settings(ctx)
	if err != nil {
		return nil, err
	}
	if olderThanDays < 0 {
		olderThanDays = set.RecycleRetentionDays
	}
	if olderThanDays > 36500 {
		return nil, userErr(ErrInvalid, nil, "the age must be at most 36500 days")
	}
	cutoff := s.now().Add(-time.Duration(olderThanDays) * 24 * time.Hour)
	if olderThanDays == 0 {
		cutoff = s.now().Add(time.Minute) // everything, including what was recycled just now
	}
	bins, err := s.bins(ctx, &set)
	if err != nil {
		return nil, err
	}
	out := []RecycleEntry{}
	for _, b := range bins {
		purged, err := s.fs.PurgeRecycle(b.dir, cutoff, true)
		for _, it := range purged {
			out = append(out, RecycleEntry{RecycledItem: it, Bin: b.dir, RootFolderID: b.rootID})
			s.recordPurge(ctx, &it)
		}
		if err != nil {
			return out, fsErr(err, "the purge of %s stopped", b.dir)
		}
	}
	s.log.Info("recycle bin purged on request", slog.Int("files", len(out)), slog.Int("older_than_days", olderThanDays))
	if len(out) > 0 {
		s.publish(events.Event{Type: EventRecycleChanged, Data: map[string]any{"purged": len(out)}})
	}
	return out, nil
}

func (s *Service) recordPurge(ctx context.Context, it *fsx.RecycledItem) {
	h := &historyRow{event: historyFileDeleted, title: it.Name, data: map[string]any{
		"reason": "deleted for good: the recycle bin was purged on request", "bin": filepath.Dir(it.Path), "size": it.Size,
	}}
	if it.Manifest != nil {
		h.movieID = it.Manifest.MovieID
		h.data["originalPath"] = it.Manifest.OriginalPath
		h.data["recycledBecause"] = it.Manifest.Reason
	}
	if err := s.appendHistory(ctx, s.st.Write(), h); err != nil {
		s.log.Error("history: recycle purge", slog.String("item", it.Name), slog.Any("error", err))
	}
}
