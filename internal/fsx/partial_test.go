package fsx

import (
	"os"
	"path/filepath"
	"slices"
	"testing"
)

func TestRemovePartials(t *testing.T) {
	dir := t.TempDir()
	for _, n := range []string{".gonema-partial-1", ".gonema-partial-2", "movie.mkv", ".gonema-import-x.mkv"} {
		if err := os.WriteFile(filepath.Join(dir, n), []byte("x"), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	if err := os.Mkdir(filepath.Join(dir, ".gonema-partial-dir"), 0o750); err != nil {
		t.Fatal(err)
	}
	o := New(nil, Options{})
	removed, err := o.RemovePartials(dir)
	if err != nil || !slices.Equal(removed, []string{".gonema-partial-1", ".gonema-partial-2"}) {
		t.Fatalf("removed %v %v", removed, err)
	}
	left, _ := os.ReadDir(dir)
	if len(left) != 3 {
		t.Errorf("left %d entries, want the movie, the staging file and the folder", len(left))
	}
	if removed, err := o.RemovePartials(filepath.Join(dir, "missing")); err != nil || removed != nil {
		t.Errorf("missing dir = %v %v", removed, err)
	}
}
