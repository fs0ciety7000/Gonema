package store

import (
	"context"
	"os"
	"path/filepath"
	"testing"
)

func TestBackupToAndInspect(t *testing.T) {
	ctx := context.Background()
	s := openTest(t)
	if err := SetSetting(ctx, s, "backup.test", "kept"); err != nil {
		t.Fatal(err)
	}
	dst := filepath.Join(t.TempDir(), "copy.db")
	if err := s.BackupTo(ctx, dst); err != nil {
		t.Fatal(err)
	}
	if err := s.BackupTo(ctx, dst); err == nil {
		t.Error("backup over an existing file succeeded")
	}
	fi, err := InspectFile(ctx, dst)
	if err != nil {
		t.Fatal(err)
	}
	want, err := s.SchemaVersion(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if fi.SchemaVersion != want || want != MaxSchemaVersion() || len(fi.Problems) != 0 {
		t.Errorf("inspect = %+v, schema %d, max %d", fi, want, MaxSchemaVersion())
	}
	c, err := Open(ctx, dst)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = c.Close() }()
	if v, err := GetSetting[string](ctx, c, "backup.test"); err != nil || v != "kept" {
		t.Errorf("setting in the copy = %q %v", v, err)
	}

	junk := filepath.Join(t.TempDir(), "junk.db")
	if err := os.WriteFile(junk, []byte("not a database at all, really not"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := InspectFile(ctx, junk); err == nil {
		t.Error("junk file inspected without error")
	}
}
