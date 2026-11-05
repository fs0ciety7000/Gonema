package fsx

import (
	"cmp"
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"path/filepath"
	"slices"
	"strings"
	"time"
	"unicode/utf8"
)

// ManifestSuffix ends the name of the sidecar manifest of a recycled file.
const ManifestSuffix = ".gonema.json"

// recycleTimeLayout prefixes recycled file names: sortable, portable.
const recycleTimeLayout = "20060102T150405Z"

// maxManifestBytes bounds a manifest read from disk.
const maxManifestBytes = 64 << 10

// Manifest records where a recycled file came from. It is stored as JSON next to
// the file, in "<name>.gonema.json".
type Manifest struct {
	OriginalPath string    `json:"originalPath"`
	MovieID      int64     `json:"movieId,omitempty"`
	Reason       string    `json:"reason"`
	RecycledAt   time.Time `json:"recycledAt"`
	Size         int64     `json:"size"`
}

// RecycledItem is a file in a recycle bin.
type RecycledItem struct {
	// Name is the file name in the bin; it identifies the item.
	Name string `json:"name"`
	Path string `json:"path"`
	Size int64  `json:"size"`
	// Manifest is nil for a file without a readable manifest: it cannot be
	// restored automatically.
	Manifest *Manifest `json:"manifest,omitempty"`
	// RecycledAt comes from the manifest, or from the name prefix.
	RecycledAt time.Time `json:"recycledAt"`
}

// Recycle moves the regular file path into binDir (created if needed) under a
// unique timestamped name, with a manifest recording its origin. m.OriginalPath,
// m.RecycledAt and m.Size are filled in. Keep binDir on the file system of path
// (e.g. a folder of the same root folder) so that the move is a rename.
func (o *Ops) Recycle(ctx context.Context, path, binDir string, m Manifest) (RecycledItem, error) {
	fi, err := o.regular(path)
	if err != nil {
		return RecycledItem{}, err
	}
	if !filepath.IsAbs(binDir) {
		return RecycledItem{}, fmt.Errorf("fsx: recycle bin %q is not absolute", binDir)
	}
	if err := o.fs.MkdirAll(binDir, 0o750); err != nil {
		return RecycledItem{}, fmt.Errorf("fsx: create recycle bin: %w", err)
	}
	now := o.now().UTC()
	var rnd [4]byte
	_, _ = rand.Read(rnd[:]) // never fails (crypto/rand panics instead)
	base := filepath.Base(path)
	// Keep "<name>.gonema.json" within 255 bytes.
	const maxBase = 255 - len(recycleTimeLayout) - 10 - len(ManifestSuffix)
	if len(base) > maxBase {
		ext := filepath.Ext(base)
		if len(ext) > 16 {
			ext = ""
		}
		base = truncateUTF8(strings.TrimSuffix(base, filepath.Ext(base)), maxBase-len(ext)) + ext
	}
	name := now.Format(recycleTimeLayout) + "-" + hex.EncodeToString(rnd[:]) + "-" + base
	dst := filepath.Join(binDir, name)

	m.OriginalPath = filepath.Clean(path)
	m.RecycledAt = now
	m.Size = fi.Size()
	manifestPath := dst + ManifestSuffix
	if err := o.writeJSON(manifestPath, &m); err != nil {
		return RecycledItem{}, err
	}
	out, err := o.Move(ctx, path, dst)
	if err != nil {
		_ = o.fs.Remove(manifestPath) // our own manifest, written just above
		return RecycledItem{}, fmt.Errorf("fsx: move %s to the recycle bin: %w", base, err)
	}
	if out.SourceKept {
		// The copy is in the bin but the original is still in place: the caller
		// would believe it gone. Undo the copy and report.
		_ = o.fs.Remove(dst)
		_ = o.fs.Remove(manifestPath)
		return RecycledItem{}, fmt.Errorf("fsx: move %s to the recycle bin: %s", base, out.Note)
	}
	return RecycledItem{Name: name, Path: dst, Size: fi.Size(), Manifest: &m, RecycledAt: now}, nil
}

// writeJSON writes v atomically to name, which must not exist.
func (o *Ops) writeJSON(name string, v any) error {
	data, err := json.MarshalIndent(v, "", "  ")
	if err != nil {
		return fmt.Errorf("fsx: encode %s: %w", filepath.Base(name), err)
	}
	if err := o.absent(name); err != nil {
		return err
	}
	tmp, err := o.fs.CreateTemp(filepath.Dir(name), tempPrefix+"*")
	if err != nil {
		return err
	}
	tmpName := tmp.Name()
	_, werr := tmp.Write(append(data, '\n'))
	serr := tmp.Sync()
	cerr := tmp.Close()
	if err := errors.Join(werr, serr, cerr); err != nil {
		_ = o.fs.Remove(tmpName)
		return fmt.Errorf("fsx: write %s: %w", filepath.Base(name), err)
	}
	if err := o.fs.Rename(tmpName, name); err != nil {
		_ = o.fs.Remove(tmpName)
		return err
	}
	return nil
}

func (o *Ops) readManifest(name string) (*Manifest, error) {
	r, err := o.fs.Open(name)
	if err != nil {
		return nil, err
	}
	defer func() { _ = r.Close() }()
	data, err := io.ReadAll(io.LimitReader(r, maxManifestBytes+1))
	if err != nil {
		return nil, err
	}
	if len(data) > maxManifestBytes {
		return nil, fmt.Errorf("fsx: manifest %s is too large", filepath.Base(name))
	}
	var m Manifest
	if err := json.Unmarshal(data, &m); err != nil {
		return nil, fmt.Errorf("fsx: decode manifest %s: %w", filepath.Base(name), err)
	}
	if m.OriginalPath == "" || !filepath.IsAbs(m.OriginalPath) {
		return nil, fmt.Errorf("fsx: manifest %s has no absolute original path", filepath.Base(name))
	}
	return &m, nil
}

// ListRecycle returns the items of a recycle bin, oldest first. A missing bin is
// empty. Manifests and temporary files are not items.
func (o *Ops) ListRecycle(binDir string) ([]RecycledItem, error) {
	entries, err := o.fs.ReadDir(binDir)
	if errors.Is(err, fs.ErrNotExist) {
		return []RecycledItem{}, nil
	}
	if err != nil {
		return nil, err
	}
	out := make([]RecycledItem, 0, len(entries))
	for _, e := range entries {
		name := e.Name()
		if strings.HasSuffix(name, ManifestSuffix) || strings.HasPrefix(name, tempPrefix) {
			continue
		}
		if !e.Type().IsRegular() {
			continue
		}
		fi, err := e.Info()
		if err != nil {
			continue // removed meanwhile
		}
		it := RecycledItem{Name: name, Path: filepath.Join(binDir, name), Size: fi.Size()}
		if m, err := o.readManifest(it.Path + ManifestSuffix); err == nil {
			it.Manifest = m
			it.RecycledAt = m.RecycledAt
		} else if t, ok := nameTime(name); ok {
			it.RecycledAt = t
		} else {
			it.RecycledAt = fi.ModTime().UTC()
		}
		out = append(out, it)
	}
	slices.SortStableFunc(out, func(a, b RecycledItem) int {
		return cmp.Or(a.RecycledAt.Compare(b.RecycledAt), cmp.Compare(a.Name, b.Name))
	})
	return out, nil
}

// nameTime reads the timestamp prefix of a recycled file name.
func nameTime(name string) (time.Time, bool) {
	if len(name) < len(recycleTimeLayout) {
		return time.Time{}, false
	}
	t, err := time.Parse(recycleTimeLayout, name[:len(recycleTimeLayout)])
	return t, err == nil
}

// checkItemName accepts plain file names only.
func checkItemName(name string) error {
	if name == "" || name == "." || name == ".." || strings.ContainsAny(name, `/\`+"\x00") ||
		filepath.Base(name) != name || strings.HasSuffix(name, ManifestSuffix) || strings.HasPrefix(name, tempPrefix) {
		return fmt.Errorf("%w: %q", ErrInvalidName, name)
	}
	return nil
}

// RestoreRecycled moves an item of binDir back to its original path, which must
// be free, and removes its manifest. It returns the restored path. allowed, when
// not nil, must accept the original path (e.g. "inside a root folder").
func (o *Ops) RestoreRecycled(ctx context.Context, binDir, name string, allowed func(string) bool) (string, error) {
	if err := checkItemName(name); err != nil {
		return "", err
	}
	src := filepath.Join(binDir, name)
	if _, err := o.regular(src); err != nil {
		return "", err
	}
	m, err := o.readManifest(src + ManifestSuffix)
	if err != nil {
		return "", fmt.Errorf("fsx: %s has no usable manifest, its original location is unknown: %w", name, err)
	}
	dst := filepath.Clean(m.OriginalPath)
	if allowed != nil && !allowed(dst) {
		return "", fmt.Errorf("%w: original location %s", ErrOutsideRoot, dst)
	}
	if err := o.absent(dst); err != nil {
		return "", err
	}
	if err := o.fs.MkdirAll(filepath.Dir(dst), 0o750); err != nil {
		return "", err
	}
	out, err := o.Move(ctx, src, dst)
	if err != nil {
		return "", err
	}
	if !out.SourceKept {
		_ = o.fs.Remove(src + ManifestSuffix) // the item is gone: its manifest too
	}
	return dst, nil
}

// PurgeRecycle permanently deletes the items of binDir recycled before
// olderThan, with their manifests. It refuses to run unless confirm is true: it
// is the only operation of Gonema that deletes user data, and it runs only on an
// explicit request of the user. Non-regular entries are never removed. It returns
// the deleted items; on error, the items deleted so far.
func (o *Ops) PurgeRecycle(binDir string, olderThan time.Time, confirm bool) ([]RecycledItem, error) {
	if !confirm {
		return nil, fmt.Errorf("%w: purging the recycle bin deletes files for good", ErrConfirmRequired)
	}
	items, err := o.ListRecycle(binDir)
	if err != nil {
		return nil, err
	}
	var purged []RecycledItem
	for _, it := range items {
		if !it.RecycledAt.Before(olderThan) {
			continue
		}
		if _, err := o.regular(it.Path); err != nil {
			continue
		}
		if err := o.fs.Remove(it.Path); err != nil {
			return purged, fmt.Errorf("fsx: purge %s: %w", it.Name, err)
		}
		if err := o.fs.Remove(it.Path + ManifestSuffix); err != nil && !errors.Is(err, fs.ErrNotExist) {
			return purged, fmt.Errorf("fsx: purge manifest of %s: %w", it.Name, err)
		}
		purged = append(purged, it)
	}
	return purged, nil
}

// truncateUTF8 cuts s to at most n bytes without splitting a character.
func truncateUTF8(s string, n int) string {
	if len(s) <= n {
		return s
	}
	for n > 0 && !utf8.RuneStart(s[n]) {
		n--
	}
	return s[:n]
}
