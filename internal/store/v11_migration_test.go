package store

import (
	"context"
	"testing"
)

// TestMigration9V11UpDown checks migration 00009: the new history events and
// columns, the calendar feed constraints, and a clean down and up that keeps
// the history rows of the older events.
func TestMigration9V11UpDown(t *testing.T) {
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
	for _, q := range []string{
		`INSERT INTO history (movie_id, event, created_at) VALUES (1, 'grabbed', 1)`,
		`INSERT INTO history (movie_id, event, created_at) VALUES (1, 'retries_stopped', 2)`,
		`INSERT INTO history (movie_id, event, created_at) VALUES (1, 'retries_resumed', 3)`,
		`INSERT INTO collections (id, tmdb_id, name, parts, search_on_add) VALUES (1, 10, 'Saga', '[{"tmdbId":1}]', 1)`,
		`INSERT INTO users (id, username, password_hash, role, created_at) VALUES (1, 'a', 'x', 'admin', 1)`,
		`INSERT INTO calendar_feeds (user_id, past_days, future_days) VALUES (1, 30, 180)`,
	} {
		if err := s.exec(t, q); err != nil {
			t.Fatalf("%s: %v", q, err)
		}
	}
	for _, bad := range []string{
		`INSERT INTO history (movie_id, event, created_at) VALUES (1, 'nope', 4)`,
		`UPDATE history SET event = 'grabbed' WHERE id = 2`,
		`UPDATE collections SET parts = '{}' WHERE id = 1`,
		`UPDATE calendar_feeds SET last4 = 'abcd' WHERE user_id = 1`, // last4 without a key
		`UPDATE calendar_feeds SET future_days = 0 WHERE user_id = 1`,
		`INSERT INTO sessions (token_hash, user_id, created_at, last_seen_at, expires_at, via) VALUES (randomblob(32), 1, 1, 1, 2, 'vpn')`,
	} {
		if err := s.exec(t, bad); err == nil {
			t.Errorf("accepted: %s", bad)
		}
	}
	if err := s.MigrateDownTo(ctx, 8); err != nil {
		t.Fatal(err)
	}
	if n := count(`SELECT count(*) FROM history`); n != 1 {
		t.Errorf("%d history rows after down, want 1 (new events dropped)", n)
	}
	if n := count(`SELECT count(*) FROM sqlite_schema WHERE name IN ('calendar_feeds', 'library_import_scans')`); n != 0 {
		t.Errorf("%d tables left after down", n)
	}
	if err := s.exec(t, `UPDATE history SET event = 'x' WHERE id = 1`); err == nil {
		t.Error("append-only trigger lost by the down migration")
	}
	if err := s.MigrateUp(ctx); err != nil {
		t.Fatal(err)
	}
	if n := count(`SELECT count(*) FROM sqlite_schema WHERE name = 'history_event_id'`); n != 1 {
		t.Error("history_event_id index lost")
	}
}
