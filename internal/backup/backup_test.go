package backup

import (
	"archive/zip"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/fs0ciety7000/gonema/internal/secretbox"
	"github.com/fs0ciety7000/gonema/internal/store"
)

type env struct {
	t    *testing.T
	ctx  context.Context
	dir  string
	st   *store.Store
	key  secretbox.Key
	svc  *Service
	now  time.Time
	open bool
}

func newEnv(t *testing.T) *env {
	t.Helper()
	e := &env{t: t, ctx: context.Background(), dir: t.TempDir(), now: time.Date(2026, 11, 6, 10, 0, 0, 0, time.UTC)}
	k, _, err := secretbox.LoadKey(filepath.Join(e.dir, KeyFile), func(string) string { return "" })
	if err != nil {
		t.Fatal(err)
	}
	e.key = k
	e.openStore()
	t.Cleanup(func() { e.closeStore() })
	return e
}

func (e *env) openStore() {
	e.t.Helper()
	st, err := store.Open(e.ctx, filepath.Join(e.dir, DBFile))
	if err != nil {
		e.t.Fatal(err)
	}
	e.st, e.open = st, true
	e.svc, err = New(Options{DataDir: e.dir, Store: st, Version: "1.2.3", Key: e.key, Now: func() time.Time { return e.now }})
	if err != nil {
		e.t.Fatal(err)
	}
}

func (e *env) closeStore() {
	if e.open {
		if err := e.st.Close(); err != nil {
			e.t.Error(err)
		}
		e.open = false
	}
}

func (e *env) set(k, v string) {
	e.t.Helper()
	if err := store.SetSetting(e.ctx, e.st, k, v); err != nil {
		e.t.Fatal(err)
	}
}

func (e *env) get(k string) string {
	e.t.Helper()
	v, err := store.GetSetting[string](e.ctx, e.st, k)
	if err != nil {
		e.t.Fatal(err)
	}
	return v
}

func zipEntries(t *testing.T, p string) map[string][]byte {
	t.Helper()
	zr, err := zip.OpenReader(p)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = zr.Close() }()
	out := map[string][]byte{}
	for _, f := range zr.File {
		r, err := f.Open()
		if err != nil {
			t.Fatal(err)
		}
		b, err := io.ReadAll(r)
		_ = r.Close()
		if err != nil {
			t.Fatal(err)
		}
		out[f.Name] = b
	}
	return out
}

func TestCreateListOpenDelete(t *testing.T) {
	e := newEnv(t)
	e.set("probe", "v1")
	in, err := e.svc.Create(e.ctx, KindManual, false)
	if err != nil {
		t.Fatal(err)
	}
	if in.Name != "gonema-backup-20261106-100000-manual.zip" || in.IncludesKey || !in.Restorable || in.SchemaVersion != store.MaxSchemaVersion() || in.AppVersion != "1.2.3" {
		t.Errorf("info = %+v", in)
	}
	files := zipEntries(t, filepath.Join(e.svc.Dir(), in.Name))
	if _, ok := files[KeyFile]; ok || len(files) != 2 || files[DBFile] == nil {
		t.Fatalf("archive entries = %d, key included %v", len(files), ok)
	}
	var m Manifest
	if err := json.Unmarshal(files[ManifestFile], &m); err != nil || m.KeyFingerprint != Fingerprint(e.key) || m.DBSize != int64(len(files[DBFile])) {
		t.Errorf("manifest = %+v %v", m, err)
	}
	if fi, err := os.Stat(filepath.Join(e.svc.Dir(), in.Name)); err != nil || fi.Mode().Perm() != 0o600 {
		t.Errorf("archive mode = %v %v", fi, err)
	}

	withKey, err := e.svc.Create(e.ctx, KindManual, true) // same second: another name
	if err != nil {
		t.Fatal(err)
	}
	if withKey.Name != "gonema-backup-20261106-100000-2-manual.zip" || !withKey.IncludesKey {
		t.Errorf("with key = %+v", withKey)
	}
	if _, ok := zipEntries(t, filepath.Join(e.svc.Dir(), withKey.Name))[KeyFile]; !ok {
		t.Error("key not included on request")
	}

	e.now = e.now.Add(time.Hour)
	if err := os.WriteFile(filepath.Join(e.svc.Dir(), "gonema-backup-20261106-120000-manual.zip"), []byte("junk"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(e.svc.Dir(), "notes.txt"), []byte("ignored"), 0o600); err != nil {
		t.Fatal(err)
	}
	list, err := e.svc.List(e.ctx)
	if err != nil {
		t.Fatal(err)
	}
	if len(list) != 3 || list[0].Restorable || list[0].Problem == "" || list[1].Name == list[2].Name ||
		(list[1].Name != withKey.Name && list[1].Name != in.Name) || (list[2].Name != withKey.Name && list[2].Name != in.Name) {
		t.Errorf("list = %+v", list)
	}

	f, size, err := e.svc.Open(in.Name)
	if err != nil {
		t.Fatal(err)
	}
	_ = f.Close()
	if size != in.Size {
		t.Errorf("size %d, want %d", size, in.Size)
	}
	for _, bad := range []string{"../gonema.db", "..", "gonema.db", "gonema-backup-20261106-100000-manual.zip/../../gonema.key",
		"/etc/passwd", "gonema-backup-20261106-100000-evil.zip", "gonema-backup-29991106-100000-manual.zip"} {
		if _, _, err := e.svc.Open(bad); !errors.Is(err, ErrNotFound) {
			t.Errorf("Open(%q) = %v, want not found", bad, err)
		}
	}
	// A symbolic link with a valid name is not followed out of the folder.
	link := "gonema-backup-20261106-130000-manual.zip"
	if err := os.Symlink(filepath.Join(e.dir, KeyFile), filepath.Join(e.svc.Dir(), link)); err == nil {
		if _, _, err := e.svc.Open(link); !errors.Is(err, ErrNotFound) {
			t.Errorf("symlink opened: %v", err)
		}
	}

	if err := e.svc.Delete(in.Name, false); !errors.Is(err, ErrConfirmRequired) {
		t.Errorf("delete without confirm = %v", err)
	}
	if err := e.svc.Delete(in.Name, true); err != nil {
		t.Fatal(err)
	}
	if err := e.svc.Delete(in.Name, true); !errors.Is(err, ErrNotFound) {
		t.Errorf("second delete = %v", err)
	}
	if err := e.svc.Delete("../"+DBFile, true); !errors.Is(err, ErrNotFound) {
		t.Errorf("traversal delete = %v", err)
	}
	if _, err := os.Stat(filepath.Join(e.dir, DBFile)); err != nil {
		t.Errorf("database touched: %v", err)
	}
}

func TestKeyFromEnvCannotBeIncluded(t *testing.T) {
	e := newEnv(t)
	e.svc.keyFromEnv = true
	if _, err := e.svc.Create(e.ctx, KindManual, true); !errors.Is(err, ErrInvalid) {
		t.Errorf("include key from env = %v", err)
	}
}

func TestPrune(t *testing.T) {
	e := newEnv(t)
	for i := range 5 {
		e.now = e.now.Add(time.Duration(i+1) * time.Minute)
		if _, err := e.svc.Create(e.ctx, KindScheduled, false); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := e.svc.Create(e.ctx, KindManual, false); err != nil {
		t.Fatal(err)
	}
	removed, err := e.svc.Prune(e.ctx, 2)
	if err != nil || len(removed) != 3 {
		t.Fatalf("removed %v %v", removed, err)
	}
	list, _ := e.svc.List(e.ctx)
	kinds := map[Kind]int{}
	for _, in := range list {
		kinds[in.Kind]++
	}
	if len(list) != 3 || kinds[KindScheduled] != 2 || kinds[KindManual] != 1 || list[len(list)-1].Name != "gonema-backup-20261106-101000-scheduled.zip" {
		t.Errorf("after prune = %+v", list)
	}
}

func TestStageAndApplyRestore(t *testing.T) {
	e := newEnv(t)
	e.set("probe", "old")
	b, err := e.svc.Create(e.ctx, KindManual, false)
	if err != nil {
		t.Fatal(err)
	}
	e.set("probe", "new")
	e.now = e.now.Add(time.Minute)

	if _, err := e.svc.StageRestore(e.ctx, b.Name, false, false); !errors.Is(err, ErrConfirmRequired) {
		t.Fatalf("restore without confirm = %v", err)
	}
	if _, err := e.svc.StageRestore(e.ctx, b.Name, true, true); !errors.Is(err, ErrInvalid) {
		t.Errorf("restoreKey without a key in the archive = %v", err)
	}
	st, err := e.svc.StageRestore(e.ctx, b.Name, false, true)
	if err != nil {
		t.Fatal(err)
	}
	if !st.RestartRequired || st.PreRestoreBackup.Kind != KindPreRestore || len(st.Warnings) != 0 {
		t.Errorf("staged = %+v", st)
	}
	if p, err := e.svc.PendingRestore(); err != nil || p == nil || p.Name != b.Name {
		t.Errorf("pending = %+v %v", p, err)
	}
	if e.get("probe") != "new" {
		t.Error("the live database changed before the restart")
	}

	e.closeStore() // restart
	p, err := ApplyPending(e.dir, e.now, nil)
	if err != nil || p == nil {
		t.Fatalf("apply = %+v %v", p, err)
	}
	if p, err := ApplyPending(e.dir, e.now, nil); err != nil || p != nil {
		t.Errorf("second apply = %+v %v", p, err)
	}
	e.openStore()
	if got := e.get("probe"); got != "old" {
		t.Errorf("after restore probe = %q, want old", got)
	}
	// The pre-restore backup holds the state before the restore.
	if _, err := e.svc.StageRestore(e.ctx, st.PreRestoreBackup.Name, false, true); err != nil {
		t.Fatal(err)
	}
	e.closeStore()
	if _, err := ApplyPending(e.dir, e.now.Add(time.Minute), nil); err != nil {
		t.Fatal(err)
	}
	e.openStore()
	if got := e.get("probe"); got != "new" {
		t.Errorf("after restoring the pre-restore backup probe = %q, want new", got)
	}
	if _, err := os.Stat(filepath.Join(e.dir, KeyFile)); err != nil {
		t.Errorf("key file: %v", err)
	}
}

func TestRestoreKeyAndCancel(t *testing.T) {
	e := newEnv(t)
	b, err := e.svc.Create(e.ctx, KindManual, true)
	if err != nil {
		t.Fatal(err)
	}
	oldKey, err := os.ReadFile(filepath.Join(e.dir, KeyFile))
	if err != nil {
		t.Fatal(err)
	}
	// The key changes (lost data directory): the archive's secrets need the old one.
	nk, err := secretbox.NewKey()
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(e.dir, KeyFile), []byte(secretbox.EncodeKey(nk)+"\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	e.svc.keyFP = Fingerprint(nk)
	st, err := e.svc.StageRestore(e.ctx, b.Name, false, true)
	if err != nil {
		t.Fatal(err)
	}
	if len(st.Warnings) != 1 || !strings.Contains(st.Warnings[0], "restoreKey") {
		t.Errorf("warnings = %q", st.Warnings)
	}
	if err := e.svc.CancelRestore(); err != nil {
		t.Fatal(err)
	}
	if err := e.svc.CancelRestore(); !errors.Is(err, ErrNotFound) {
		t.Errorf("second cancel = %v", err)
	}
	if _, err := e.svc.StageRestore(e.ctx, b.Name, true, true); err != nil {
		t.Fatal(err)
	}
	e.closeStore()
	if _, err := ApplyPending(e.dir, e.now, nil); err != nil {
		t.Fatal(err)
	}
	got, err := os.ReadFile(filepath.Join(e.dir, KeyFile))
	if err != nil || !bytes.Equal(got, oldKey) {
		t.Errorf("key not restored: %v", err)
	}
	moved, err := filepath.Glob(filepath.Join(e.dir, RestoreDir, replacedDir, "*", KeyFile))
	if err != nil || len(moved) != 1 {
		t.Errorf("replaced key kept: %v %v", moved, err)
	}
	e.openStore()
}

// writeZip writes an archive with the given entries.
func writeZip(t *testing.T, entries map[string][]byte) []byte {
	t.Helper()
	var buf bytes.Buffer
	zw := zip.NewWriter(&buf)
	for name, b := range entries {
		w, err := zw.Create(name)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := w.Write(b); err != nil {
			t.Fatal(err)
		}
	}
	if err := zw.Close(); err != nil {
		t.Fatal(err)
	}
	return buf.Bytes()
}

func TestImportValidates(t *testing.T) {
	e := newEnv(t)
	b, err := e.svc.Create(e.ctx, KindManual, false)
	if err != nil {
		t.Fatal(err)
	}
	good, err := os.ReadFile(filepath.Join(e.svc.Dir(), b.Name))
	if err != nil {
		t.Fatal(err)
	}
	in, err := e.svc.Import(e.ctx, bytes.NewReader(good))
	if err != nil {
		t.Fatal(err)
	}
	if in.Kind != KindManual || !strings.HasSuffix(in.Name, "-uploaded.zip") || !in.Restorable {
		t.Errorf("uploaded = %+v", in)
	}

	entries := zipEntries(t, filepath.Join(e.svc.Dir(), b.Name))
	var m Manifest
	if err := json.Unmarshal(entries[ManifestFile], &m); err != nil {
		t.Fatal(err)
	}
	with := func(edit func(m *Manifest, files map[string][]byte)) []byte {
		mm := m
		files := map[string][]byte{DBFile: entries[DBFile]}
		edit(&mm, files)
		raw, _ := json.Marshal(mm)
		files[ManifestFile] = raw
		return writeZip(t, files)
	}
	bad := map[string][]byte{
		"not a zip":      []byte("hello"),
		"no manifest":    writeZip(t, map[string][]byte{DBFile: entries[DBFile]}),
		"zip slip":       with(func(_ *Manifest, f map[string][]byte) { f["../../evil"] = []byte("x") }),
		"newer schema":   with(func(m *Manifest, _ map[string][]byte) { m.SchemaVersion = store.MaxSchemaVersion() + 1 }),
		"checksum":       with(func(m *Manifest, _ map[string][]byte) { m.DBSHA256 = strings.Repeat("0", 64) }),
		"size":           with(func(m *Manifest, _ map[string][]byte) { m.DBSize++ }),
		"other app":      with(func(m *Manifest, _ map[string][]byte) { m.App = "other" }),
		"schema differs": with(func(m *Manifest, _ map[string][]byte) { m.SchemaVersion-- }),
		"not sqlite": with(func(m *Manifest, f map[string][]byte) {
			f[DBFile] = bytes.Repeat([]byte("x"), 4096)
			m.DBSize = 4096
			m.DBSHA256 = "9e5b3a7ee0e2e9d4c2c5a7c71dbb93f3f0b41ae7d2b17f8f96d2b8f0e9cd9e6c"
		}),
	}
	for name, archive := range bad {
		if _, err := e.svc.Import(e.ctx, bytes.NewReader(archive)); !errors.Is(err, ErrInvalid) {
			t.Errorf("%s: import = %v, want invalid", name, err)
		}
	}
	list, _ := e.svc.List(e.ctx)
	if len(list) != 2 {
		t.Errorf("rejected uploads were kept: %+v", list)
	}
	leftovers, _ := filepath.Glob(filepath.Join(e.svc.Dir(), ".*"))
	if len(leftovers) != 0 {
		t.Errorf("temporary files left: %v", leftovers)
	}
}

func TestApplyPendingResumesAfterCrash(t *testing.T) {
	e := newEnv(t)
	e.set("probe", "old")
	b, err := e.svc.Create(e.ctx, KindManual, false)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := e.svc.StageRestore(e.ctx, b.Name, false, true); err != nil {
		t.Fatal(err)
	}
	e.closeStore()
	// Crash after the staged database was moved into place, before the
	// marker was removed.
	if err := os.Rename(filepath.Join(e.dir, DBFile), filepath.Join(e.t.TempDir(), "old.db")); err != nil {
		t.Fatal(err)
	}
	if err := os.Rename(filepath.Join(e.dir, RestoreDir, stagedDB), filepath.Join(e.dir, DBFile)); err != nil {
		t.Fatal(err)
	}
	p, err := ApplyPending(e.dir, e.now, nil)
	if err != nil || p == nil {
		t.Fatalf("resume = %+v %v", p, err)
	}
	if p, _ := readPending(filepath.Join(e.dir, RestoreDir)); p != nil {
		t.Error("marker left")
	}
	e.openStore()
	if e.get("probe") != "old" {
		t.Error("restored database lost")
	}
}

func TestSettingsValidate(t *testing.T) {
	d := DefaultSettings()
	if err := d.Validate(); err != nil || d.IntervalHours != 168 || !d.Enabled || d.IncludeKey {
		t.Errorf("defaults = %+v %v", d, err)
	}
	for _, s := range []Settings{{IntervalHours: 0, Keep: 1}, {IntervalHours: 721, Keep: 1}, {IntervalHours: 1, Keep: 0}, {IntervalHours: 1, Keep: 101}} {
		if err := s.Validate(); !errors.Is(err, ErrInvalid) {
			t.Errorf("%+v accepted", s)
		}
	}
	e := newEnv(t)
	ss := &SettingsStore{St: e.st}
	if got := ss.Get(e.ctx); got != DefaultSettings() || ss.Interval() != 168*time.Hour {
		t.Errorf("get = %+v %v", got, ss.Interval())
	}
	if _, err := ss.Set(e.ctx, Settings{Enabled: false, IntervalHours: 24, Keep: 3}); err != nil {
		t.Fatal(err)
	}
	if got := ss.Get(e.ctx); got.IntervalHours != 24 || got.Enabled || ss.Interval() != 24*time.Hour {
		t.Errorf("after set = %+v", got)
	}
}

func TestValidName(t *testing.T) {
	for _, n := range []string{"gonema-backup-20261106-100000-manual.zip", "gonema-backup-20261106-100000-12-pre-restore.zip"} {
		if !ValidName(n) {
			t.Errorf("%q rejected", n)
		}
	}
	for _, n := range []string{"", "x.zip", "gonema-backup-20261106-100000-manual.zip.tmp", "../gonema-backup-20261106-100000-manual.zip",
		"gonema-backup-20261106-100000-manual.zip\x00", "gonema-backup-2026110-100000-manual.zip"} {
		if ValidName(n) {
			t.Errorf("%q accepted", n)
		}
	}
}
