package logbuf

import (
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"strconv"
	"sync"
)

// RotatingFile is an io.Writer appending to a log file that is rotated by
// size: when a write would make it larger than MaxBytes, gonema.log becomes
// gonema.log.1 (gonema.log.1 becomes .2, and so on) and a new file starts.
// Only Keep rotated files are kept: the oldest is removed (these files are
// Gonema's own logs). It is safe for concurrent use.
type RotatingFile struct {
	path     string
	maxBytes int64
	keep     int

	mu   sync.Mutex
	f    *os.File
	size int64
}

// OpenRotatingFile opens (creating it and its folder) the log file at path.
// maxBytes is at least 1 MiB; keep is the number of rotated files kept (0-100).
func OpenRotatingFile(path string, maxBytes int64, keep int) (*RotatingFile, error) {
	if path == "" {
		return nil, errors.New("logbuf: empty log file path")
	}
	if keep < 0 || keep > 100 {
		return nil, fmt.Errorf("logbuf: keep %d: want 0 to 100", keep)
	}
	r := &RotatingFile{path: filepath.Clean(path), maxBytes: max(maxBytes, 1<<20), keep: keep}
	if err := os.MkdirAll(filepath.Dir(r.path), 0o750); err != nil {
		return nil, fmt.Errorf("logbuf: create the log folder: %w", err)
	}
	if err := r.open(); err != nil {
		return nil, err
	}
	return r, nil
}

func (r *RotatingFile) open() error {
	f, err := os.OpenFile(r.path, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o640)
	if err != nil {
		return fmt.Errorf("logbuf: open the log file: %w", err)
	}
	fi, err := f.Stat()
	if err != nil {
		_ = f.Close()
		return fmt.Errorf("logbuf: stat the log file: %w", err)
	}
	r.f, r.size = f, fi.Size()
	return nil
}

// Path returns the path of the current log file.
func (r *RotatingFile) Path() string { return r.path }

// MaxBytes returns the rotation size.
func (r *RotatingFile) MaxBytes() int64 { return r.maxBytes }

// Keep returns the number of rotated files kept.
func (r *RotatingFile) Keep() int { return r.keep }

// Write implements io.Writer. A record is never split across two files.
func (r *RotatingFile) Write(p []byte) (int, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.f == nil {
		return 0, errors.New("logbuf: log file closed")
	}
	if r.size > 0 && r.size+int64(len(p)) > r.maxBytes {
		if err := r.rotate(); err != nil {
			return 0, err
		}
	}
	n, err := r.f.Write(p)
	r.size += int64(n)
	return n, err
}

// rotate shifts the files and opens a new one. Callers hold r.mu.
func (r *RotatingFile) rotate() error {
	if err := r.f.Close(); err != nil {
		return fmt.Errorf("logbuf: close the log file: %w", err)
	}
	r.f = nil
	if r.keep == 0 {
		if err := os.Remove(r.path); err != nil && !errors.Is(err, fs.ErrNotExist) {
			return fmt.Errorf("logbuf: rotate: %w", err)
		}
		return r.open()
	}
	if err := os.Remove(r.rotated(r.keep)); err != nil && !errors.Is(err, fs.ErrNotExist) {
		return fmt.Errorf("logbuf: rotate: %w", err)
	}
	for i := r.keep - 1; i >= 1; i-- {
		if err := os.Rename(r.rotated(i), r.rotated(i+1)); err != nil && !errors.Is(err, fs.ErrNotExist) {
			return fmt.Errorf("logbuf: rotate: %w", err)
		}
	}
	if err := os.Rename(r.path, r.rotated(1)); err != nil && !errors.Is(err, fs.ErrNotExist) {
		return fmt.Errorf("logbuf: rotate: %w", err)
	}
	return r.open()
}

func (r *RotatingFile) rotated(i int) string { return r.path + "." + strconv.Itoa(i) }

// WriteAll copies the rotated files (oldest first) then the current file to w.
// It does not hold the write lock (a slow reader must not stall logging): a
// rotation during the copy may repeat or skip a part.
func (r *RotatingFile) WriteAll(w io.Writer) error {
	for i := r.keep; i >= 0; i-- {
		name := r.path
		if i > 0 {
			name = r.rotated(i)
		}
		f, err := os.Open(name)
		if errors.Is(err, fs.ErrNotExist) {
			continue
		}
		if err != nil {
			return fmt.Errorf("logbuf: open %s: %w", filepath.Base(name), err)
		}
		_, err = io.Copy(w, f)
		_ = f.Close()
		if err != nil {
			return err
		}
	}
	return nil
}

// Close closes the current file.
func (r *RotatingFile) Close() error {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.f == nil {
		return nil
	}
	err := r.f.Close()
	r.f = nil
	return err
}

// TeeWriter writes to every writer; a failing writer does not stop the others
// (a full disk must not silence stderr). The first error is returned.
type TeeWriter []io.Writer

// Write implements io.Writer.
func (t TeeWriter) Write(p []byte) (int, error) {
	var first error
	for _, w := range t {
		if _, err := w.Write(p); err != nil && first == nil {
			first = err
		}
	}
	return len(p), first
}
