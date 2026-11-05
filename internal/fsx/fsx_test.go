package fsx_test

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"runtime"
	"slices"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/fs0ciety7000/gonema/internal/fsx"
)

// faultFS wraps the real file system and injects failures.
type faultFS struct {
	fsx.OS
	crossDevice bool         // Link and Rename across top-level test folders fail with EXDEV
	failWrite   int64        // > 0: writes of temp files fail after this many bytes
	freeSpace   uint64       // > 0: reported free space
	removed     atomic.Int64 // Remove calls
	renameHook  func() error // optional failure of Rename
	dirOf       func(string) string
}

func (f *faultFS) device(p string) string {
	if f.dirOf != nil {
		return f.dirOf(p)
	}
	return filepath.Dir(p)
}

func (f *faultFS) Link(oldname, newname string) error {
	if f.crossDevice && f.device(oldname) != f.device(newname) {
		return &os.LinkError{Op: "link", Old: oldname, New: newname, Err: errCrossDevice}
	}
	return f.OS.Link(oldname, newname)
}

func (f *faultFS) Rename(oldpath, newpath string) error {
	if f.renameHook != nil {
		if err := f.renameHook(); err != nil {
			return err
		}
	}
	if f.crossDevice && f.device(oldpath) != f.device(newpath) {
		return &os.LinkError{Op: "rename", Old: oldpath, New: newpath, Err: errCrossDevice}
	}
	return f.OS.Rename(oldpath, newpath)
}

func (f *faultFS) Remove(name string) error {
	f.removed.Add(1)
	return f.OS.Remove(name)
}

func (f *faultFS) FreeSpace(path string) (uint64, error) {
	if f.freeSpace > 0 {
		return f.freeSpace, nil
	}
	return f.OS.FreeSpace(path)
}

type failingTemp struct {
	fsx.TempFile
	left int64
}

var errDiskFull = errors.New("simulated write failure")

func (t *failingTemp) Write(p []byte) (int, error) {
	if int64(len(p)) > t.left {
		n, _ := t.TempFile.Write(p[:t.left])
		t.left = 0
		return n, errDiskFull
	}
	t.left -= int64(len(p))
	return t.TempFile.Write(p)
}

func (f *faultFS) CreateTemp(dir, pattern string) (fsx.TempFile, error) {
	tf, err := f.OS.CreateTemp(dir, pattern)
	if err != nil || f.failWrite <= 0 {
		return tf, err
	}
	return &failingTemp{TempFile: tf, left: f.failWrite}, nil
}

// twoDevices lays out two "file systems" (sibling folders) for faultFS.
func twoDevices(t *testing.T) (a, b string, f *faultFS) {
	t.Helper()
	root := t.TempDir()
	a, b = filepath.Join(root, "a"), filepath.Join(root, "b")
	for _, d := range []string{a, b} {
		if err := os.MkdirAll(d, 0o750); err != nil {
			t.Fatal(err)
		}
	}
	f = &faultFS{crossDevice: true, dirOf: func(p string) string {
		rel, err := filepath.Rel(root, p)
		if err != nil {
			return p
		}
		return strings.SplitN(rel, string(filepath.Separator), 2)[0]
	}}
	return a, b, f
}

func write(t *testing.T, path string, size int) []byte {
	t.Helper()
	data := bytes.Repeat([]byte("gonema-"), size/7+1)[:size]
	if err := os.MkdirAll(filepath.Dir(path), 0o750); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, data, 0o640); err != nil {
		t.Fatal(err)
	}
	old := time.Date(2020, 1, 2, 3, 4, 5, 0, time.UTC)
	if err := os.Chtimes(path, old, old); err != nil {
		t.Fatal(err)
	}
	return data
}

func names(t *testing.T, dir string) []string {
	t.Helper()
	es, err := os.ReadDir(dir)
	if err != nil {
		t.Fatal(err)
	}
	var out []string
	for _, e := range es {
		out = append(out, e.Name())
	}
	return out
}

func sameContent(t *testing.T, path string, want []byte) {
	t.Helper()
	got, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(got, want) {
		t.Fatalf("%s: content differs (%d bytes, want %d)", path, len(got), len(want))
	}
}

func TestHardlink(t *testing.T) {
	dir := t.TempDir()
	src, dst := filepath.Join(dir, "src.mkv"), filepath.Join(dir, "lib", "dst.mkv")
	write(t, src, 1000)
	if err := os.MkdirAll(filepath.Dir(dst), 0o750); err != nil {
		t.Fatal(err)
	}
	o := fsx.New(nil, fsx.Options{})
	out, err := o.Transfer(context.Background(), src, dst, fsx.ModeHardlink)
	if err != nil {
		t.Fatal(err)
	}
	if out.Method != fsx.MethodHardlink || !out.SourceKept {
		t.Errorf("outcome = %+v", out)
	}
	a, _ := os.Stat(src)
	b, _ := os.Stat(dst)
	if !os.SameFile(a, b) {
		t.Error("dst is not a hard link of src")
	}
	// Never overwrite.
	if err := o.Hardlink(src, dst); !errors.Is(err, fs.ErrExist) {
		t.Errorf("second link: err = %v, want ErrExist", err)
	}
}

func TestHardlinkCrossDeviceFallsBackToCopy(t *testing.T) {
	a, b, f := twoDevices(t)
	src, dst := filepath.Join(a, "movie.mkv"), filepath.Join(b, "movie.mkv")
	data := write(t, src, 3<<20+17)
	o := fsx.New(f, fsx.Options{})
	if err := o.Hardlink(src, dst); !errors.Is(err, fsx.ErrCrossDevice) {
		t.Fatalf("Hardlink err = %v, want ErrCrossDevice", err)
	}
	out, err := o.Transfer(context.Background(), src, dst, fsx.ModeHardlink)
	if err != nil {
		t.Fatal(err)
	}
	if out.Method != fsx.MethodCopy || !out.SourceKept || !strings.Contains(out.Note, "different file systems") {
		t.Errorf("outcome = %+v", out)
	}
	sameContent(t, dst, data)
	sameContent(t, src, data)
	fi, _ := os.Stat(dst)
	if want := time.Date(2020, 1, 2, 3, 4, 5, 0, time.UTC); !fi.ModTime().Equal(want) {
		t.Errorf("mtime = %v, want %v (preserved)", fi.ModTime(), want)
	}
	if got := names(t, b); !slices.Equal(got, []string{"movie.mkv"}) {
		t.Errorf("destination folder = %v", got)
	}
}

func TestMove(t *testing.T) {
	t.Run("same device renames", func(t *testing.T) {
		dir := t.TempDir()
		src, dst := filepath.Join(dir, "dl", "m.mkv"), filepath.Join(dir, "lib", "m.mkv")
		data := write(t, src, 4096)
		_ = os.MkdirAll(filepath.Dir(dst), 0o750)
		out, err := fsx.New(nil, fsx.Options{}).Transfer(context.Background(), src, dst, fsx.ModeMove)
		if err != nil {
			t.Fatal(err)
		}
		if out.Method != fsx.MethodRename || out.SourceKept {
			t.Errorf("outcome = %+v", out)
		}
		sameContent(t, dst, data)
		if _, err := os.Lstat(src); !errors.Is(err, fs.ErrNotExist) {
			t.Errorf("source still there: %v", err)
		}
	})
	t.Run("cross device copies then removes", func(t *testing.T) {
		a, b, f := twoDevices(t)
		src, dst := filepath.Join(a, "m.mkv"), filepath.Join(b, "m.mkv")
		data := write(t, src, 2<<20+5)
		out, err := fsx.New(f, fsx.Options{}).Move(context.Background(), src, dst)
		if err != nil {
			t.Fatal(err)
		}
		if out.Method != fsx.MethodCopyRemove || out.SourceKept {
			t.Errorf("outcome = %+v", out)
		}
		sameContent(t, dst, data)
		if _, err := os.Lstat(src); !errors.Is(err, fs.ErrNotExist) {
			t.Errorf("source still there: %v", err)
		}
	})
	t.Run("never overwrites", func(t *testing.T) {
		dir := t.TempDir()
		src, dst := filepath.Join(dir, "a.mkv"), filepath.Join(dir, "b.mkv")
		data := write(t, src, 10)
		other := write(t, dst, 20)
		if _, err := fsx.New(nil, fsx.Options{}).Move(context.Background(), src, dst); !errors.Is(err, fs.ErrExist) {
			t.Fatalf("err = %v, want ErrExist", err)
		}
		sameContent(t, src, data)
		sameContent(t, dst, other)
	})
}

func TestCopyFailureLeavesNoPartialFile(t *testing.T) {
	cases := map[string]func(t *testing.T) (*faultFS, context.Context){
		"write fails mid-way": func(*testing.T) (*faultFS, context.Context) {
			return &faultFS{failWrite: 1<<20 + 100}, context.Background()
		},
		"canceled": func(*testing.T) (*faultFS, context.Context) {
			ctx, cancel := context.WithCancel(context.Background())
			cancel()
			return &faultFS{}, ctx
		},
		"rename fails": func(*testing.T) (*faultFS, context.Context) {
			return &faultFS{renameHook: func() error { return errors.New("simulated rename failure") }}, context.Background()
		},
	}
	for name, setup := range cases {
		t.Run(name, func(t *testing.T) {
			dir := t.TempDir()
			src, dstDir := filepath.Join(dir, "src", "m.mkv"), filepath.Join(dir, "dst")
			data := write(t, src, 3<<20)
			_ = os.MkdirAll(dstDir, 0o750)
			f, ctx := setup(t)
			err := fsx.New(f, fsx.Options{}).Copy(ctx, src, filepath.Join(dstDir, "m.mkv"))
			if err == nil {
				t.Fatal("Copy succeeded")
			}
			if got := names(t, dstDir); len(got) != 0 {
				t.Errorf("destination folder not empty after failure: %v", got)
			}
			sameContent(t, src, data)
		})
	}
}

func TestCopyChecksFreeSpace(t *testing.T) {
	dir := t.TempDir()
	src := filepath.Join(dir, "m.mkv")
	write(t, src, 5000)
	err := fsx.New(&faultFS{freeSpace: 100}, fsx.Options{}).Copy(context.Background(), src, filepath.Join(dir, "copy.mkv"))
	if !errors.Is(err, fsx.ErrNoSpace) {
		t.Fatalf("err = %v, want ErrNoSpace", err)
	}
	if got := names(t, dir); !slices.Equal(got, []string{"m.mkv"}) {
		t.Errorf("folder = %v", got)
	}
}

func TestSymlinkSourcesRefused(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("symlinks need privileges on Windows")
	}
	dir := t.TempDir()
	target := filepath.Join(dir, "target.mkv")
	data := write(t, target, 10)
	link := filepath.Join(dir, "link.mkv")
	if err := os.Symlink(target, link); err != nil {
		t.Fatal(err)
	}
	o := fsx.New(nil, fsx.Options{})
	for _, mode := range []fsx.Mode{fsx.ModeHardlink, fsx.ModeCopy, fsx.ModeMove} {
		if _, err := o.Transfer(context.Background(), link, filepath.Join(dir, "out-"+string(mode)), mode); !errors.Is(err, fsx.ErrNotRegular) {
			t.Errorf("%s: err = %v, want ErrNotRegular", mode, err)
		}
	}
	if _, err := o.Recycle(context.Background(), link, filepath.Join(dir, "bin"), fsx.Manifest{}); !errors.Is(err, fsx.ErrNotRegular) {
		t.Errorf("Recycle(symlink): err = %v, want ErrNotRegular", err)
	}
	sameContent(t, target, data)
}

func TestSafeJoin(t *testing.T) {
	root := t.TempDir()
	o := fsx.New(nil, fsx.Options{})
	if err := os.MkdirAll(filepath.Join(root, "Dune (2021)"), 0o750); err != nil {
		t.Fatal(err)
	}
	ok := []string{"Dune (2021)/Dune.mkv", "new/folder/file.mkv", "a..b/c", "Dune (2021)"}
	for _, rel := range ok {
		got, err := o.SafeJoin(root, rel)
		if err != nil {
			t.Errorf("SafeJoin(%q): %v", rel, err)
			continue
		}
		if !fsx.Within(root, got) {
			t.Errorf("SafeJoin(%q) = %q, outside the root", rel, got)
		}
	}
	bad := []string{"", ".", "..", "../x", "a/../../x", "a/../..", "/etc/passwd", `\\server\share`, "a\x00b", `..\x`}
	if runtime.GOOS == "windows" {
		bad = append(bad, `C:\Windows`, `C:x`)
	}
	for _, rel := range bad {
		if got, err := o.SafeJoin(root, rel); !errors.Is(err, fsx.ErrOutsideRoot) {
			t.Errorf("SafeJoin(%q) = %q, %v; want ErrOutsideRoot", rel, got, err)
		}
	}
}

func TestSymlinkEscapeRejected(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("symlinks need privileges on Windows")
	}
	base := t.TempDir()
	root, outside := filepath.Join(base, "root"), filepath.Join(base, "outside")
	for _, d := range []string{root, outside} {
		_ = os.MkdirAll(d, 0o750)
	}
	write(t, filepath.Join(outside, "secret.mkv"), 5)
	if err := os.Symlink(outside, filepath.Join(root, "escape")); err != nil {
		t.Fatal(err)
	}
	inside := filepath.Join(root, "inside")
	_ = os.MkdirAll(inside, 0o750)
	if err := os.Symlink(inside, filepath.Join(root, "alias")); err != nil {
		t.Fatal(err)
	}
	o := fsx.New(nil, fsx.Options{})
	for _, rel := range []string{"escape/secret.mkv", "escape/new/file.mkv", "escape"} {
		if _, err := o.SafeJoin(root, rel); !errors.Is(err, fsx.ErrOutsideRoot) {
			t.Errorf("SafeJoin(%q): err = %v, want ErrOutsideRoot", rel, err)
		}
	}
	if _, err := o.SafeJoin(root, "alias/new.mkv"); err != nil {
		t.Errorf("a link that stays inside is fine: %v", err)
	}
	// Contains resolves both sides.
	if _, _, err := o.Contains([]string{root}, filepath.Join(root, "escape", "secret.mkv")); !errors.Is(err, fsx.ErrOutsideRoot) {
		t.Errorf("Contains(escape) err = %v", err)
	}
	if _, r, err := o.Contains([]string{outside, root}, filepath.Join(root, "inside")); err != nil || r != root {
		t.Errorf("Contains(inside) = %q, %v", r, err)
	}
	if _, _, err := o.Contains([]string{root}, "relative/path"); !errors.Is(err, fsx.ErrOutsideRoot) {
		t.Errorf("Contains(relative) err = %v", err)
	}
}

func TestRecycleListRestorePurge(t *testing.T) {
	root := t.TempDir()
	movieDir := filepath.Join(root, "Dune (2021)")
	old := filepath.Join(movieDir, "Dune (2021) [WEBDL-1080p].mkv")
	data := write(t, old, 2048)
	bin := filepath.Join(root, ".gonema-recycle")
	now := time.Date(2026, 11, 5, 10, 0, 0, 0, time.UTC)
	o := fsx.New(nil, fsx.Options{Now: func() time.Time { return now }})
	ctx := context.Background()

	it, err := o.Recycle(ctx, old, bin, fsx.Manifest{MovieID: 7, Reason: "upgraded to Bluray-2160p"})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := os.Lstat(old); !errors.Is(err, fs.ErrNotExist) {
		t.Fatalf("original still there: %v", err)
	}
	sameContent(t, it.Path, data)
	raw, err := os.ReadFile(it.Path + fsx.ManifestSuffix)
	if err != nil {
		t.Fatal(err)
	}
	var m fsx.Manifest
	if err := json.Unmarshal(raw, &m); err != nil {
		t.Fatal(err)
	}
	if m.OriginalPath != old || m.MovieID != 7 || m.Reason == "" || !m.RecycledAt.Equal(now) || m.Size != 2048 {
		t.Errorf("manifest = %+v", m)
	}

	items, err := o.ListRecycle(bin)
	if err != nil || len(items) != 1 || items[0].Name != it.Name || items[0].Manifest == nil {
		t.Fatalf("ListRecycle = %+v, %v", items, err)
	}
	if missing, err := o.ListRecycle(filepath.Join(root, "nope")); err != nil || len(missing) != 0 {
		t.Errorf("missing bin = %v, %v", missing, err)
	}

	// Purge refuses without confirmation, whatever the age.
	if _, err := o.PurgeRecycle(bin, now.Add(time.Hour), false); !errors.Is(err, fsx.ErrConfirmRequired) {
		t.Fatalf("purge without confirm: err = %v", err)
	}
	sameContent(t, it.Path, data)
	// Too recent: kept.
	if purged, err := o.PurgeRecycle(bin, now.Add(-time.Hour), true); err != nil || len(purged) != 0 {
		t.Fatalf("purge recent = %v, %v", purged, err)
	}

	// Restore refuses invalid names and occupied destinations.
	for _, bad := range []string{"../x", "", it.Name + fsx.ManifestSuffix, "a/b"} {
		if _, err := o.RestoreRecycled(ctx, bin, bad, nil); !errors.Is(err, fsx.ErrInvalidName) {
			t.Errorf("restore %q: err = %v", bad, err)
		}
	}
	if _, err := o.RestoreRecycled(ctx, bin, it.Name, func(string) bool { return false }); !errors.Is(err, fsx.ErrOutsideRoot) {
		t.Errorf("restore refused by caller: err = %v", err)
	}
	other := write(t, old, 10)
	if _, err := o.RestoreRecycled(ctx, bin, it.Name, nil); !errors.Is(err, fs.ErrExist) {
		t.Errorf("restore over a file: err = %v", err)
	}
	sameContent(t, old, other)
	if err := os.Rename(old, old+".other"); err != nil {
		t.Fatal(err)
	}
	got, err := o.RestoreRecycled(ctx, bin, it.Name, nil)
	if err != nil || got != old {
		t.Fatalf("restore = %q, %v", got, err)
	}
	sameContent(t, old, data)
	if left := names(t, bin); len(left) != 0 {
		t.Errorf("bin after restore = %v", left)
	}

	// Recycle again, then purge with confirmation once old enough.
	it2, err := o.Recycle(ctx, old, bin, fsx.Manifest{Reason: "test"})
	if err != nil {
		t.Fatal(err)
	}
	purged, err := o.PurgeRecycle(bin, now.Add(time.Second), true)
	if err != nil || len(purged) != 1 || purged[0].Name != it2.Name {
		t.Fatalf("purge = %+v, %v", purged, err)
	}
	if left := names(t, bin); len(left) != 0 {
		t.Errorf("bin after purge = %v", left)
	}
}

func TestPurgeNeverFollowsSymlinks(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("symlinks need privileges on Windows")
	}
	dir := t.TempDir()
	bin := filepath.Join(dir, "bin")
	_ = os.MkdirAll(bin, 0o750)
	target := filepath.Join(dir, "precious.mkv")
	data := write(t, target, 10)
	if err := os.Symlink(target, filepath.Join(bin, "20200101T000000Z-00000000-precious.mkv")); err != nil {
		t.Fatal(err)
	}
	o := fsx.New(nil, fsx.Options{})
	if _, err := o.PurgeRecycle(bin, time.Now(), true); err != nil {
		t.Fatal(err)
	}
	sameContent(t, target, data)
}

func TestRecycleCrossDevice(t *testing.T) {
	a, b, f := twoDevices(t)
	src := filepath.Join(a, "old.mkv")
	data := write(t, src, 1<<20+3)
	o := fsx.New(f, fsx.Options{})
	it, err := o.Recycle(context.Background(), src, filepath.Join(b, "bin"), fsx.Manifest{Reason: "upgrade"})
	if err != nil {
		t.Fatal(err)
	}
	sameContent(t, it.Path, data)
	if _, err := os.Lstat(src); !errors.Is(err, fs.ErrNotExist) {
		t.Errorf("original still there: %v", err)
	}
}

func TestRevert(t *testing.T) {
	ctx := context.Background()
	for _, mode := range []fsx.Mode{fsx.ModeHardlink, fsx.ModeCopy, fsx.ModeMove} {
		t.Run(string(mode), func(t *testing.T) {
			dir := t.TempDir()
			src, dst := filepath.Join(dir, "src.mkv"), filepath.Join(dir, "dst.mkv")
			data := write(t, src, 100)
			o := fsx.New(nil, fsx.Options{})
			out, err := o.Transfer(ctx, src, dst, mode)
			if err != nil {
				t.Fatal(err)
			}
			if err := o.Revert(ctx, out, src, dst); err != nil {
				t.Fatal(err)
			}
			sameContent(t, src, data)
			if _, err := os.Lstat(dst); !errors.Is(err, fs.ErrNotExist) {
				t.Errorf("dst left after revert: %v", err)
			}
		})
	}
}

func TestFreeSpace(t *testing.T) {
	free, err := fsx.New(nil, fsx.Options{}).FreeSpace(t.TempDir())
	if errors.Is(err, fsx.ErrUnsupported) {
		t.Skip("unsupported platform")
	}
	if err != nil || free == 0 {
		t.Errorf("FreeSpace = %d, %v", free, err)
	}
}

func TestUnknownMode(t *testing.T) {
	dir := t.TempDir()
	write(t, filepath.Join(dir, "a"), 1)
	if _, err := fsx.New(nil, fsx.Options{}).Transfer(context.Background(), filepath.Join(dir, "a"), filepath.Join(dir, "b"), "symlink"); err == nil {
		t.Error("unknown mode accepted")
	}
	if fsx.Mode("symlink").Valid() || !fsx.ModeMove.Valid() {
		t.Error("Valid")
	}
}

var _ io.Writer = (*failingTemp)(nil)
