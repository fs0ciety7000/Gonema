package fsx

import (
	"context"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"path/filepath"
)

// Mode is how a file is brought into the library.
type Mode string

// Modes.
const (
	// ModeHardlink links the file (no extra space; a seeding torrent keeps its
	// data) and falls back to a copy when a link is impossible.
	ModeHardlink Mode = "hardlink"
	// ModeCopy copies the file; the source is left as is.
	ModeCopy Mode = "copy"
	// ModeMove renames the file, or copies it then removes the source once the
	// copy is verified when it crosses file systems.
	ModeMove Mode = "move"
)

// Valid reports whether m is a known mode.
func (m Mode) Valid() bool { return m == ModeHardlink || m == ModeCopy || m == ModeMove }

// Method is how a transfer was actually done.
type Method string

// Methods.
const (
	MethodHardlink   Method = "hardlink"
	MethodCopy       Method = "copy"
	MethodRename     Method = "rename"
	MethodCopyRemove Method = "copy+remove" // cross-device move
)

// Outcome describes a completed transfer.
type Outcome struct {
	Method Method `json:"method"`
	// Note explains a fallback ("hard link impossible: copied") or a source that
	// could not be removed after a cross-device move. Plain language.
	Note string `json:"note,omitempty"`
	// SourceKept is true when the source still exists after the transfer.
	SourceKept bool `json:"sourceKept"`
}

// copyBuffer is the chunk size of copies; the context is checked between chunks.
const copyBuffer = 1 << 20

// tempPrefix starts the names of the temporary files of copies.
const tempPrefix = ".gonema-partial-"

// Hardlink creates dst as a hard link to src. src must be a regular file and dst
// must not exist. A link across file systems fails with ErrCrossDevice.
func (o *Ops) Hardlink(src, dst string) error {
	if _, err := o.regular(src); err != nil {
		return err
	}
	if err := o.absent(dst); err != nil {
		return err
	}
	if err := o.fs.Link(src, dst); err != nil {
		if isCrossDevice(err) {
			return fmt.Errorf("%w: %w", ErrCrossDevice, err)
		}
		return err
	}
	return nil
}

// Copy copies the regular file src to dst, which must not exist. The data goes
// to a temporary file in dst's folder, is synced, checked for size, given the
// permissions and modification time of src, then renamed to dst: dst never
// exists half-written. The free space of the destination is checked first.
// On failure or cancellation the temporary file is removed; src is never touched.
func (o *Ops) Copy(ctx context.Context, src, dst string) error {
	fi, err := o.regular(src)
	if err != nil {
		return err
	}
	if err := o.absent(dst); err != nil {
		return err
	}
	dir := filepath.Dir(dst)
	if free, err := o.fs.FreeSpace(dir); err == nil {
		if size := fi.Size(); size > 0 && free < uint64(size) {
			return fmt.Errorf("%w: %s needs %d bytes, %d are free in %s", ErrNoSpace, filepath.Base(src), size, free, dir)
		}
	} else if !errors.Is(err, ErrUnsupported) {
		return fmt.Errorf("fsx: free space of %s: %w", dir, err)
	}

	tmp, err := o.fs.CreateTemp(dir, tempPrefix+"*")
	if err != nil {
		return err
	}
	tmpName := tmp.Name()
	committed := false
	defer func() {
		if !committed {
			_ = o.fs.Remove(tmpName) // our own temporary file
		}
	}()
	if err := o.copyData(ctx, src, tmp); err != nil {
		_ = tmp.Close()
		return err
	}
	if err := tmp.Sync(); err != nil {
		_ = tmp.Close()
		return fmt.Errorf("fsx: sync %s: %w", tmpName, err)
	}
	if err := tmp.Close(); err != nil {
		return fmt.Errorf("fsx: close %s: %w", tmpName, err)
	}
	got, err := o.fs.Lstat(tmpName)
	if err != nil {
		return err
	}
	if got.Size() != fi.Size() {
		return fmt.Errorf("%w: %s has %d bytes, the source %d", ErrSizeMismatch, dst, got.Size(), fi.Size())
	}
	// Best effort: a file system may not support permissions or times.
	_ = o.fs.Chmod(tmpName, fi.Mode().Perm())
	_ = o.fs.Chtimes(tmpName, fi.ModTime(), fi.ModTime())
	if err := o.absent(dst); err != nil { // created meanwhile: never overwrite
		return err
	}
	if err := o.fs.Rename(tmpName, dst); err != nil {
		return err
	}
	committed = true
	return nil
}

func (o *Ops) copyData(ctx context.Context, src string, w io.Writer) error {
	r, err := o.fs.Open(src)
	if err != nil {
		return err
	}
	defer func() { _ = r.Close() }()
	buf := make([]byte, copyBuffer)
	for {
		if err := ctx.Err(); err != nil {
			return err
		}
		n, rerr := r.Read(buf)
		if n > 0 {
			if _, werr := w.Write(buf[:n]); werr != nil {
				return fmt.Errorf("fsx: write copy of %s: %w", filepath.Base(src), werr)
			}
		}
		if errors.Is(rerr, io.EOF) {
			return nil
		}
		if rerr != nil {
			return fmt.Errorf("fsx: read %s: %w", src, rerr)
		}
	}
}

// Move moves the regular file src to dst, which must not exist. On one file
// system it is an atomic rename. Across file systems, src is copied (see Copy)
// and removed only once the copy is verified and src has not changed meanwhile;
// if src cannot be removed the move still succeeds with Outcome.SourceKept.
func (o *Ops) Move(ctx context.Context, src, dst string) (Outcome, error) {
	before, err := o.regular(src)
	if err != nil {
		return Outcome{}, err
	}
	if err := o.absent(dst); err != nil {
		return Outcome{}, err
	}
	err = o.fs.Rename(src, dst)
	if err == nil {
		return Outcome{Method: MethodRename}, nil
	}
	if !isCrossDevice(err) {
		return Outcome{}, err
	}
	if err := o.Copy(ctx, src, dst); err != nil {
		return Outcome{}, err
	}
	out := Outcome{Method: MethodCopyRemove}
	after, err := o.regular(src)
	switch {
	case err != nil:
		out.SourceKept, out.Note = true, fmt.Sprintf("copied; the source could not be checked before removal (%v)", err)
	case after.Size() != before.Size() || !after.ModTime().Equal(before.ModTime()):
		out.SourceKept, out.Note = true, "copied; the source changed during the copy, so it was kept"
	default:
		if err := o.fs.Remove(src); err != nil {
			out.SourceKept, out.Note = true, fmt.Sprintf("copied; the source could not be removed (%v)", err)
		}
	}
	return out, nil
}

// Transfer brings src to dst with mode: a hard link (falling back to a copy when
// the link is impossible: other file system, unsupported), a copy, or a move.
// dst must not exist. On error, src and dst are as before the call.
func (o *Ops) Transfer(ctx context.Context, src, dst string, mode Mode) (Outcome, error) {
	switch mode {
	case ModeHardlink:
		err := o.Hardlink(src, dst)
		if err == nil {
			return Outcome{Method: MethodHardlink, SourceKept: true}, nil
		}
		if errors.Is(err, fs.ErrExist) || errors.Is(err, fs.ErrNotExist) || errors.Is(err, ErrNotRegular) {
			return Outcome{}, err
		}
		if cerr := o.Copy(ctx, src, dst); cerr != nil {
			return Outcome{}, cerr
		}
		reason := "the file system refused it"
		if errors.Is(err, ErrCrossDevice) {
			reason = "the download and the library are on different file systems"
		}
		return Outcome{Method: MethodCopy, SourceKept: true, Note: "hard link impossible (" + reason + "): copied instead"}, nil
	case ModeCopy:
		if err := o.Copy(ctx, src, dst); err != nil {
			return Outcome{}, err
		}
		return Outcome{Method: MethodCopy, SourceKept: true}, nil
	case ModeMove:
		return o.Move(ctx, src, dst)
	default:
		return Outcome{}, fmt.Errorf("fsx: unknown transfer mode %q", mode)
	}
}

// Revert undoes a Transfer whose follow-up failed: a link or copy created at dst
// is removed (it is the file the transfer just created, not user data), a moved
// file is moved back to src. A cross-device move whose source was removed is
// copied back.
func (o *Ops) Revert(ctx context.Context, out Outcome, src, dst string) error {
	switch out.Method {
	case MethodHardlink, MethodCopy:
		if _, err := o.regular(dst); err != nil {
			return err
		}
		return o.fs.Remove(dst)
	case MethodRename:
		if err := o.absent(src); err != nil {
			return err
		}
		return o.fs.Rename(dst, src)
	case MethodCopyRemove:
		if out.SourceKept {
			if _, err := o.regular(dst); err != nil {
				return err
			}
			return o.fs.Remove(dst)
		}
		_, err := o.Move(ctx, dst, src)
		return err
	default:
		return fmt.Errorf("fsx: cannot revert method %q", out.Method)
	}
}
