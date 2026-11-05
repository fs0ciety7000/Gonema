package library

import (
	"context"
	"errors"
	"fmt"
	"io/fs"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/fs0ciety7000/gonema/internal/events"
	"github.com/fs0ciety7000/gonema/internal/store"
	"github.com/fs0ciety7000/gonema/internal/store/db"
)

// ErrDiskSpaceUnsupported is returned by DiskSpace on platforms without statfs.
var ErrDiskSpaceUnsupported = errors.New("library: disk space is not available on this platform")

// FS is the file system access of the root folder checks (replaced in tests).
// None of its methods deletes user data.
type FS interface {
	Stat(name string) (fs.FileInfo, error)
	// CheckWritable verifies that files can be created in dir.
	CheckWritable(dir string) error
	// DiskSpace returns the free (available to this user) and total bytes of
	// the file system holding path.
	DiskSpace(path string) (free, total uint64, err error)
}

// OSFS is the real file system.
type OSFS struct{}

// Stat implements FS.
func (OSFS) Stat(name string) (fs.FileInfo, error) { return os.Stat(name) }

// CheckWritable creates then removes a hidden probe file of our own in dir.
func (OSFS) CheckWritable(dir string) error {
	f, err := os.CreateTemp(dir, ".gonema-write-test-*")
	if err != nil {
		return err
	}
	name := f.Name()
	cerr := f.Close()
	// Only the probe file created just above is removed, never user data.
	if err := os.Remove(name); err != nil {
		return err
	}
	return cerr
}

// DiskSpace implements FS (see diskspace_*.go).
func (OSFS) DiskSpace(path string) (free, total uint64, err error) { return diskSpace(path) }

// RootFolder is a root folder with its live state.
type RootFolder struct {
	db.RootFolder
	Accessible bool
	Error      string // why it is not accessible, plain language
}

// cleanRootPath validates and cleans a root folder path.
func cleanRootPath(p string) (string, error) {
	p = strings.TrimSpace(p)
	if p == "" {
		return "", invalid("path", "the folder path is empty")
	}
	if strings.ContainsRune(p, 0) {
		return "", invalid("path", "the folder path contains an invalid character")
	}
	if !filepath.IsAbs(p) {
		return "", invalid("path", "the folder path must be absolute, e.g. /movies or D:\\Movies")
	}
	return filepath.Clean(p), nil
}

// within reports whether p is root or inside it.
func within(p, root string) bool {
	if p == root {
		return true
	}
	rel, err := filepath.Rel(root, p)
	return err == nil && rel != ".." && !strings.HasPrefix(rel, ".."+string(filepath.Separator))
}

// AddRootFolder registers a folder movies are stored in. It must be absolute,
// exist, be a directory, be writable, and not overlap another root folder.
func (s *Service) AddRootFolder(ctx context.Context, path string) (RootFolder, error) {
	p, err := cleanRootPath(path)
	if err != nil {
		return RootFolder{}, err
	}
	fi, err := s.fs.Stat(p)
	switch {
	case errors.Is(err, fs.ErrNotExist):
		return RootFolder{}, invalid("path", "the folder %s does not exist; create it first (or check the volume mapping in Docker)", p)
	case errors.Is(err, fs.ErrPermission):
		return RootFolder{}, invalid("path", "Gonema is not allowed to read %s; check its permissions", p)
	case err != nil:
		return RootFolder{}, invalid("path", "the folder %s cannot be read: %v", p, err)
	case !fi.IsDir():
		return RootFolder{}, invalid("path", "%s is a file, not a folder", p)
	}
	if err := s.fs.CheckWritable(p); err != nil {
		return RootFolder{}, invalid("path", "Gonema cannot write in %s; check its permissions (user and group Gonema runs as)", p)
	}
	existing, err := s.st.Read().ListRootFolders(ctx)
	if err != nil {
		return RootFolder{}, store.MapError(fmt.Errorf("library: list root folders: %w", err))
	}
	for _, rf := range existing {
		if within(p, rf.Path) || within(rf.Path, p) {
			return RootFolder{}, &InputError{Field: "path", Msg: fmt.Sprintf("%s overlaps the root folder %s; root folders must not contain each other", p, rf.Path)}
		}
	}
	row, err := s.st.Write().CreateRootFolder(ctx, p)
	if err != nil {
		return RootFolder{}, store.MapError(fmt.Errorf("library: add root folder: %w", err))
	}
	rf := s.measure(ctx, row)
	s.log.Info("root folder added", slog.Int64("root_folder_id", row.ID), slog.String("path", p))
	s.publish(events.Event{Type: events.RootFolderAdded, Data: map[string]any{"id": row.ID, "path": p}})
	return rf, nil
}

// measure reads the free space of a folder, stores it and returns the live
// state. statfs can hang on a dead network mount: it gets 2 seconds.
func (s *Service) measure(ctx context.Context, rf db.RootFolder) RootFolder {
	type result struct {
		free, total uint64
		err         error
	}
	ch := make(chan result, 1) // buffered: a hung statfs goroutine never blocks
	go func() {
		if _, err := s.fs.Stat(rf.Path); err != nil {
			ch <- result{err: err}
			return
		}
		free, total, err := s.fs.DiskSpace(rf.Path)
		ch <- result{free, total, err}
	}()
	out := RootFolder{RootFolder: rf}
	var r result
	select {
	case r = <-ch:
	case <-time.After(2 * time.Second):
		r.err = errors.New("the file system did not answer within 2 seconds")
	case <-ctx.Done():
		r.err = ctx.Err()
	}
	switch {
	case errors.Is(r.err, ErrDiskSpaceUnsupported):
		out.Accessible = true
		return out
	case r.err != nil:
		out.Error = describeFSError(rf.Path, r.err)
		return out
	}
	out.Accessible = true
	free, total := int64(min(r.free, 1<<62)), int64(min(r.total, 1<<62))
	now := store.Millis(s.now())
	out.FreeSpace, out.TotalSpace, out.CheckedAt = &free, &total, &now
	if err := store.RequireRow(s.st.Write().UpdateRootFolderSpace(ctx, db.UpdateRootFolderSpaceParams{
		ID: rf.ID, FreeSpace: &free, TotalSpace: &total, CheckedAt: now,
	})); err != nil && !errors.Is(err, store.ErrNotFound) {
		s.log.Warn("save root folder space", slog.Int64("root_folder_id", rf.ID), slog.Any("error", err))
	}
	return out
}

func describeFSError(path string, err error) string {
	switch {
	case errors.Is(err, fs.ErrNotExist):
		return fmt.Sprintf("%s no longer exists (unmounted drive or missing volume?)", path)
	case errors.Is(err, fs.ErrPermission):
		return fmt.Sprintf("Gonema is not allowed to read %s", path)
	}
	return fmt.Sprintf("%s cannot be read: %v", path, err)
}

// ListRootFolders returns the root folders with their free space, measured now.
func (s *Service) ListRootFolders(ctx context.Context) ([]RootFolder, error) {
	rows, err := s.st.Read().ListRootFolders(ctx)
	if err != nil {
		return nil, store.MapError(fmt.Errorf("library: list root folders: %w", err))
	}
	out := make([]RootFolder, 0, len(rows))
	for _, rf := range rows {
		out = append(out, s.measure(ctx, rf))
	}
	return out, nil
}

// DeleteRootFolder removes a root folder that no movie uses. The folder on disk
// is not touched.
func (s *Service) DeleteRootFolder(ctx context.Context, id int64) error {
	n, err := s.st.Read().CountMoviesInRootFolder(ctx, id)
	if err != nil {
		return store.MapError(fmt.Errorf("library: count movies of root folder %d: %w", id, err))
	}
	if n > 0 {
		return fmt.Errorf("%w: %w", &InputError{Field: "id", Msg: fmt.Sprintf("%d movies are stored in this root folder; move or remove them first", n)}, store.ErrConflict)
	}
	if err := store.RequireRow(s.st.Write().DeleteRootFolder(ctx, id)); err != nil {
		if errors.Is(err, store.ErrNotFound) {
			return notFound("root folder %d does not exist", id)
		}
		if errors.Is(err, store.ErrConflict) {
			return fmt.Errorf("%w: %w", &InputError{Field: "id", Msg: "this root folder is still used (by a collection or an import list)"}, err)
		}
		return fmt.Errorf("library: delete root folder %d: %w", id, err)
	}
	s.publish(events.Event{Type: events.RootFolderDeleted, Data: map[string]any{"id": id}})
	return nil
}

// HealthEvent is the data of health.changed events.
type HealthEvent struct {
	Source  string `json:"source"` // "rootfolder"
	ID      int64  `json:"id"`
	OK      bool   `json:"ok"`
	Message string `json:"message,omitempty"`
}

// UpdateDiskSpace measures every root folder (the rootfolders.space job) and
// publishes health.changed when a folder becomes unreachable or comes back.
func (s *Service) UpdateDiskSpace(ctx context.Context) error {
	list, err := s.ListRootFolders(ctx)
	if err != nil {
		return err
	}
	for _, rf := range list {
		s.healthMu.Lock()
		prev, seen := s.health[rf.ID]
		s.health[rf.ID] = rf.Error
		s.healthMu.Unlock()
		if (seen && prev == rf.Error) || (!seen && rf.Accessible) {
			continue
		}
		if rf.Accessible {
			s.log.Info("root folder reachable again", slog.Int64("root_folder_id", rf.ID), slog.String("path", rf.Path))
		} else {
			s.log.Warn("root folder unreachable", slog.Int64("root_folder_id", rf.ID), slog.String("reason", rf.Error))
		}
		s.publish(events.Event{Type: events.HealthChanged, Data: HealthEvent{
			Source: "rootfolder", ID: rf.ID, OK: rf.Accessible, Message: rf.Error,
		}})
	}
	return nil
}
