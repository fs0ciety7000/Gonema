// Package fsx is the only package of Gonema that changes user files. Every
// import, rename, upgrade and recycle goes through it.
//
// Guarantees:
//
//   - nothing is overwritten: every operation refuses an existing destination
//     (fs.ErrExist);
//   - copies are atomic: data is written to a temporary file in the destination
//     folder, synced, checked for size, then renamed into place; a failure or a
//     cancellation removes the temporary file and nothing else;
//   - a source is only removed by Move, after a verified copy, when the source and
//     destination are on different file systems (a same-device move is a rename);
//   - files that are replaced go to a recycle bin with a manifest (Recycle); the
//     bin is only emptied by PurgeRecycle with an explicit confirmation;
//   - symbolic links are never followed for a removal or a rename: sources must be
//     regular files (ErrNotRegular otherwise);
//   - SafeJoin rejects relative paths that escape their root, through ".." or a
//     symbolic link.
//
// The low-level calls go through the FS interface: OS in production, wrappers that
// inject faults (cross-device links, full disks, failing writes) in tests.
package fsx

import (
	"errors"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"time"
)

// Errors, matched with errors.Is. Errors returned by the package also wrap the
// underlying *fs.PathError when there is one (fs.ErrExist, fs.ErrNotExist...).
var (
	// ErrOutsideRoot: a path escapes the folder it must stay in.
	ErrOutsideRoot = errors.New("fsx: path is outside the allowed folder")
	// ErrCrossDevice: a hard link or rename crosses file systems.
	ErrCrossDevice = errors.New("fsx: source and destination are on different file systems")
	// ErrNoSpace: the destination file system lacks the space for a copy.
	ErrNoSpace = errors.New("fsx: not enough free space at the destination")
	// ErrSizeMismatch: a copy does not have the size of its source.
	ErrSizeMismatch = errors.New("fsx: the copy does not match the source size")
	// ErrNotRegular: the source is a directory, a symbolic link or a device.
	ErrNotRegular = errors.New("fsx: not a regular file")
	// ErrConfirmRequired: a destructive operation was called without confirmation.
	ErrConfirmRequired = errors.New("fsx: explicit confirmation required")
	// ErrUnsupported: the platform cannot answer (e.g. free space on plan9).
	ErrUnsupported = errors.New("fsx: not supported on this platform")
	// ErrInvalidName: a recycle bin item name is not a plain file name.
	ErrInvalidName = errors.New("fsx: invalid item name")
)

// TempFile is a file being written by Copy.
type TempFile interface {
	io.Writer
	Name() string
	Sync() error
	Close() error
}

// FS is the low-level file system used by Ops. Implementations must not follow
// symbolic links in Lstat, Remove and Rename (as package os does).
type FS interface {
	Lstat(name string) (fs.FileInfo, error)
	Stat(name string) (fs.FileInfo, error)
	ReadDir(name string) ([]fs.DirEntry, error)
	EvalSymlinks(path string) (string, error)
	MkdirAll(path string, perm fs.FileMode) error
	Open(name string) (io.ReadCloser, error)
	CreateTemp(dir, pattern string) (TempFile, error)
	Link(oldname, newname string) error
	Rename(oldpath, newpath string) error
	Remove(name string) error
	Chtimes(name string, atime, mtime time.Time) error
	Chmod(name string, mode fs.FileMode) error
	// FreeSpace returns the bytes available to this process on the file system
	// holding path, or ErrUnsupported.
	FreeSpace(path string) (uint64, error)
}

// OS is the real file system.
type OS struct{}

var _ FS = OS{}

// Lstat implements FS.
func (OS) Lstat(name string) (fs.FileInfo, error) { return os.Lstat(name) }

// Stat implements FS.
func (OS) Stat(name string) (fs.FileInfo, error) { return os.Stat(name) }

// ReadDir implements FS.
func (OS) ReadDir(name string) ([]fs.DirEntry, error) { return os.ReadDir(name) }

// EvalSymlinks implements FS.
func (OS) EvalSymlinks(path string) (string, error) { return filepath.EvalSymlinks(path) }

// MkdirAll implements FS.
func (OS) MkdirAll(path string, perm fs.FileMode) error { return os.MkdirAll(path, perm) }

// Open implements FS.
func (OS) Open(name string) (io.ReadCloser, error) { return os.Open(name) } //nolint:gosec // paths are checked by the callers

// CreateTemp implements FS.
func (OS) CreateTemp(dir, pattern string) (TempFile, error) { return os.CreateTemp(dir, pattern) }

// Link implements FS.
func (OS) Link(oldname, newname string) error { return os.Link(oldname, newname) }

// Rename implements FS.
func (OS) Rename(oldpath, newpath string) error { return os.Rename(oldpath, newpath) }

// Remove implements FS.
func (OS) Remove(name string) error { return os.Remove(name) }

// Chtimes implements FS.
func (OS) Chtimes(name string, atime, mtime time.Time) error { return os.Chtimes(name, atime, mtime) }

// Chmod implements FS.
func (OS) Chmod(name string, mode fs.FileMode) error { return os.Chmod(name, mode) }

// FreeSpace implements FS (see space_*.go).
func (OS) FreeSpace(path string) (uint64, error) { return freeSpace(path) }

// Options configures Ops.
type Options struct {
	Now func() time.Time // default time.Now; used for recycle bin timestamps
}

// Ops performs the file operations of Gonema on an FS. It is safe for
// concurrent use (it holds no mutable state).
type Ops struct {
	fs  FS
	now func() time.Time
}

// New returns Ops over fsys (OS{} when nil).
func New(fsys FS, opts Options) *Ops {
	if fsys == nil {
		fsys = OS{}
	}
	if opts.Now == nil {
		opts.Now = time.Now
	}
	return &Ops{fs: fsys, now: opts.Now}
}

// Lstat returns the file info of name without following a final symbolic link.
func (o *Ops) Lstat(name string) (fs.FileInfo, error) { return o.fs.Lstat(name) }

// Stat returns the file info of name.
func (o *Ops) Stat(name string) (fs.FileInfo, error) { return o.fs.Stat(name) }

// ReadDir lists a directory, sorted by name.
func (o *Ops) ReadDir(name string) ([]fs.DirEntry, error) { return o.fs.ReadDir(name) }

// MkdirAll creates a directory and its parents (0750).
func (o *Ops) MkdirAll(path string) error { return o.fs.MkdirAll(path, 0o750) }

// EvalSymlinks resolves the symbolic links of path (which must exist).
func (o *Ops) EvalSymlinks(path string) (string, error) { return o.fs.EvalSymlinks(path) }

// FreeSpace returns the bytes available on the file system holding path, or
// ErrUnsupported.
func (o *Ops) FreeSpace(path string) (uint64, error) { return o.fs.FreeSpace(path) }

// regular returns the info of a regular file, without following links.
func (o *Ops) regular(name string) (fs.FileInfo, error) {
	fi, err := o.fs.Lstat(name)
	if err != nil {
		return nil, err
	}
	if !fi.Mode().IsRegular() {
		return nil, &fs.PathError{Op: "lstat", Path: name, Err: ErrNotRegular}
	}
	return fi, nil
}

// absent returns nil when nothing exists at name, an error wrapping fs.ErrExist
// otherwise.
func (o *Ops) absent(name string) error {
	_, err := o.fs.Lstat(name)
	switch {
	case err == nil:
		return &fs.PathError{Op: "create", Path: name, Err: fs.ErrExist}
	case errors.Is(err, fs.ErrNotExist):
		return nil
	default:
		return err
	}
}
