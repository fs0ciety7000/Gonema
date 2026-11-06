package store

import (
	"context"
	"database/sql"
	"errors"
	"path/filepath"
	"testing"

	"github.com/fs0ciety7000/gonema/internal/store/db"
)

func openTest(t *testing.T) *Store {
	t.Helper()
	s, err := Open(context.Background(), filepath.Join(t.TempDir(), "gonema.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = s.Close() })
	return s
}

func (s *Store) exec(t *testing.T, query string, args ...any) error {
	t.Helper()
	_, err := s.writer.ExecContext(context.Background(), query, args...)
	return MapError(err)
}

func TestPragmas(t *testing.T) {
	s := openTest(t)
	ctx := context.Background()
	for _, conn := range []struct {
		name string
		db   *sql.DB
	}{{"writer", s.writer}, {"reader", s.reader}} {
		for pragma, want := range map[string]string{
			"journal_mode": "wal", "foreign_keys": "1", "busy_timeout": "5000", "synchronous": "1", "temp_store": "2",
		} {
			var got string
			err := conn.db.QueryRowContext(ctx, "PRAGMA "+pragma).Scan(&got)
			if err != nil || got != want {
				t.Errorf("%s: PRAGMA %s = %q, %v; want %q", conn.name, pragma, got, err, want)
			}
		}
	}
	if st := s.writer.Stats(); st.MaxOpenConnections != 1 {
		t.Errorf("writer MaxOpenConnections = %d, want 1", st.MaxOpenConnections)
	}
}

func TestHistoryUpdateIsRejected(t *testing.T) {
	s := openTest(t)
	if err := s.exec(t, `INSERT INTO history (event, created_at) VALUES ('ignored', 1)`); err != nil {
		t.Fatal(err)
	}
	if err := s.exec(t, `UPDATE history SET source_title = 'x'`); !errors.Is(err, ErrImmutable) {
		t.Errorf("UPDATE history err = %v, want ErrImmutable", err)
	}
	if err := s.exec(t, `DELETE FROM history`); !errors.Is(err, ErrImmutable) {
		t.Errorf("DELETE history err = %v, want ErrImmutable", err)
	}
}

// TestMovieState covers each state of the movie_state view, with the profile
// seeded below: [HD-1080p group {bluray-1080p, webdl-1080p}] > webdl-720p > dvd,
// cutoff = webdl-1080p (the group), cutoff_score = 10.
func TestMovieState(t *testing.T) {
	ctx := context.Background()
	s := openTest(t)
	var profileID int64
	err := s.WithTx(ctx, func(q *db.Queries) error {
		p, err := q.CreateQualityProfile(ctx, db.CreateQualityProfileParams{
			Name: "HD", UpgradeAllowed: true, CutoffQuality: "webdl-1080p", CutoffScore: 10, Languages: "[]",
		})
		if err != nil {
			return err
		}
		profileID = p.ID
		return ReplaceQualityProfileItems(ctx, q, p.ID, []ProfileItem{
			{Quality: "bluray-1080p", Position: 0, GroupName: "HD-1080p", Allowed: true},
			{Quality: "webdl-1080p", Position: 0, GroupName: "HD-1080p", Allowed: true},
			{Quality: "webdl-720p", Position: 1, Allowed: true},
			{Quality: "dvd", Position: 2},
		})
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := s.exec(t, `INSERT INTO root_folders (id, path) VALUES (1, '/m')`); err != nil {
		t.Fatal(err)
	}
	if err := s.exec(t, `INSERT INTO download_clients (id, name, kind, protocol, host, port) VALUES (1, 'sab', 'sabnzbd', 'usenet', 'h', 8080)`); err != nil {
		t.Fatal(err)
	}

	tests := []struct {
		name     string
		file     string // quality, "" for no file
		score    int
		download string // download status, "" for none
		want     string
	}{
		{"no file", "", 0, "", "missing"},
		{"no file, failed download", "", 0, "failed", "missing"},
		{"no file, queued", "", 0, "queued", "downloading"},
		{"file, downloading upgrade", "webdl-1080p", 50, "downloading", "downloading"},
		{"cutoff met", "webdl-1080p", 10, "", "available"},
		{"above cutoff, same group", "bluray-1080p", 10, "imported", "available"},
		{"below cutoff quality", "webdl-720p", 50, "", "upgradable"},
		{"cutoff quality, low score", "bluray-1080p", 9, "", "upgradable"},
		{"quality absent from profile", "remux-2160p", 50, "", "upgradable"},
	}
	for i, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			id := int64(i + 1)
			if err := s.exec(t, `INSERT INTO movies (id, tmdb_id, title, sort_title, clean_title, quality_profile_id, root_folder_id, path, added_at)
				VALUES (?, ?, 't', 't', 't', ?, 1, ?, 0)`, id, id, profileID, tt.name); err != nil {
				t.Fatal(err)
			}
			if tt.file != "" {
				if err := s.exec(t, `INSERT INTO movie_files (movie_id, relative_path, size, quality, custom_score, imported_at)
					VALUES (?, 'f.mkv', 1, ?, ?, 0)`, id, tt.file, tt.score); err != nil {
					t.Fatal(err)
				}
			}
			if tt.download != "" {
				if err := s.exec(t, `INSERT INTO downloads (movie_id, client_id, client_ref, release_title, protocol, quality, status, grabbed_at)
					VALUES (?, 1, ?, 'r', 'usenet', 'webdl-1080p', ?, 0)`, id, tt.name, tt.download); err != nil {
					t.Fatal(err)
				}
			}
			st, err := s.Read().GetMovieState(ctx, id)
			if err != nil {
				t.Fatal(err)
			}
			if st.State != tt.want {
				t.Errorf("state = %q, want %q", st.State, tt.want)
			}
		})
	}

	// Upgrades disabled: never upgradable.
	if err := s.exec(t, `UPDATE quality_profiles SET upgrade_allowed = 0`); err != nil {
		t.Fatal(err)
	}
	if st, err := s.Read().GetMovieState(ctx, 7); err != nil || st.State != "available" {
		t.Errorf("upgrades disabled: state = %q, %v; want available", st.State, err)
	}
}
