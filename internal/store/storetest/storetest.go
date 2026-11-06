// Package storetest provides test helpers for code that uses the store: a real,
// migrated SQLite database in a temporary directory, and minimal fixtures.
package storetest

import (
	"context"
	"path/filepath"
	"testing"

	"github.com/fs0ciety7000/gonema/internal/store"
	"github.com/fs0ciety7000/gonema/internal/store/db"
)

// New opens a fresh migrated database in tb.TempDir() and closes it at cleanup.
// A file is used rather than ":memory:" because WAL and the reader/writer pools
// need a shared file (each in-memory connection would be a separate database).
func New(tb testing.TB) *store.Store {
	tb.Helper()
	s, err := store.Open(context.Background(), filepath.Join(tb.TempDir(), "gonema.db"))
	if err != nil {
		tb.Fatalf("storetest: open: %v", err)
	}
	tb.Cleanup(func() {
		if err := s.Close(); err != nil {
			tb.Errorf("storetest: close: %v", err)
		}
	})
	return s
}

// Library holds the rows every movie needs (foreign key targets).
type Library struct {
	ProfileID    int64
	RootFolderID int64
}

// SeedLibrary creates a quality profile ("HD", cutoff webdl-1080p) and a root
// folder ("/movies").
func SeedLibrary(tb testing.TB, s *store.Store) Library {
	tb.Helper()
	ctx := context.Background()
	p, err := s.CreateQualityProfile(ctx, db.CreateQualityProfileParams{
		Name: "HD", UpgradeAllowed: true, CutoffQuality: "webdl-1080p", MinUpgradeStep: 1, Languages: "[]",
	}, []store.ProfileItem{
		{Quality: "bluray-1080p", Position: 0, GroupName: "HD-1080p", Allowed: true},
		{Quality: "webdl-1080p", Position: 0, GroupName: "HD-1080p", Allowed: true},
		{Quality: "webdl-720p", Position: 1, Allowed: true},
		{Quality: "dvd", Position: 2, Allowed: false},
	})
	if err != nil {
		tb.Fatalf("storetest: seed profile: %v", err)
	}
	rf, err := s.Write().CreateRootFolder(ctx, "/movies")
	if err != nil {
		tb.Fatalf("storetest: seed root folder: %v", err)
	}
	return Library{ProfileID: p.ID, RootFolderID: rf.ID}
}
