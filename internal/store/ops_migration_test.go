package store

import (
	"context"
	"testing"
)

// TestMigration8OpsUpDown checks migration 00008: the change log triggers,
// their pruning, and a clean down and up.
func TestMigration8OpsUpDown(t *testing.T) {
	ctx := context.Background()
	s := openTest(t)
	count := func(q string) int64 {
		t.Helper()
		var n int64
		if err := s.reader.QueryRowContext(ctx, q).Scan(&n); err != nil {
			t.Fatal(err)
		}
		return n
	}
	if err := s.exec(t, `INSERT INTO quality_profiles (id, name, cutoff_quality, languages) VALUES (1, 'P', 'webdl-1080p', '[]')`); err != nil {
		t.Fatal(err)
	}
	if err := s.exec(t, `INSERT INTO root_folders (id, path) VALUES (1, '/m')`); err != nil {
		t.Fatal(err)
	}
	if err := s.exec(t, `INSERT INTO movies (id, tmdb_id, title, sort_title, clean_title, year, status, quality_profile_id,
		minimum_availability, root_folder_id, path, added_at) VALUES (7, 1, 'A', 'a', 'a', 2000, 'released', 1, 'released', 1, 'A', 0)`); err != nil {
		t.Fatal(err)
	}
	if n := count(`SELECT count(*) FROM library_changes WHERE movie_id = 7`); n != 1 {
		t.Errorf("insert logged %d changes", n)
	}
	if err := s.exec(t, `UPDATE quality_profiles SET name = 'Q' WHERE id = 1`); err != nil {
		t.Fatal(err)
	}
	if n := count(`SELECT count(*) FROM library_changes WHERE movie_id IS NULL`); n != 1 {
		t.Errorf("profile change logged %d global changes", n)
	}
	// Pruning keeps between 2,000 and 3,000 rows.
	if err := s.exec(t, `WITH RECURSIVE n(i) AS (SELECT 1 UNION ALL SELECT i + 1 FROM n WHERE i < 5000)
		INSERT INTO library_changes (movie_id) SELECT 7 FROM n`); err != nil {
		t.Fatal(err)
	}
	if n := count(`SELECT count(*) FROM library_changes`); n < 2000 || n > 3000 {
		t.Errorf("%d changes kept", n)
	}

	if err := s.MigrateDownTo(ctx, 7); err != nil {
		t.Fatal(err)
	}
	if n := count(`SELECT count(*) FROM sqlite_schema WHERE name IN ('import_journal', 'library_changes') OR name LIKE 'library_changes_%'`); n != 0 {
		t.Errorf("%d objects left after down", n)
	}
	if err := s.exec(t, `UPDATE movies SET title = 'B' WHERE id = 7`); err != nil {
		t.Errorf("version 7 schema broken: %v", err)
	}
	if err := s.MigrateUp(ctx); err != nil {
		t.Fatal(err)
	}
	if err := s.exec(t, `INSERT INTO import_journal (movie_id, source, destination, mode, size, created_at, updated_at)
		VALUES (7, '/d/a.mkv', '/m/A/a.mkv', 'move', 1, 0, 0)`); err != nil {
		t.Fatal(err)
	}
	if err := s.exec(t, `INSERT INTO import_journal (movie_id, source, destination, mode, size, created_at, updated_at)
		VALUES (7, '/d/a.mkv', '/m/A/a.mkv', 'teleport', 1, 0, 0)`); err == nil {
		t.Error("invalid mode accepted")
	}
}
