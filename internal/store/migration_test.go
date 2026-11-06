package store

import (
	"context"
	"errors"
	"testing"

	"github.com/fs0ciety7000/gonema/internal/store/db"
)

// TestMigration2HistoryRebuild checks the history table rebuild of migration
// 00002: rows, ids, indexes and append-only triggers survive up and down; the
// new events are accepted after up and dropped by down.
func TestMigration2HistoryRebuild(t *testing.T) {
	ctx := context.Background()
	s := openTest(t)
	if err := s.MigrateDownTo(ctx, 1); err != nil {
		t.Fatal(err)
	}
	if err := s.exec(t, `INSERT INTO history (id, movie_id, event, source_title, data, created_at)
		VALUES (7, 1, 'grabbed', 'Heat.1995.1080p.BluRay.x264-GRP', '{"reasons":["ok"]}', 100)`); err != nil {
		t.Fatal(err)
	}
	if err := s.exec(t, `INSERT INTO history (event, created_at) VALUES ('movie_added', 1)`); !errors.Is(err, ErrInvalid) {
		t.Fatalf("version 1 accepts movie_added: %v", err)
	}

	if err := s.MigrateUp(ctx); err != nil {
		t.Fatal(err)
	}
	rows, err := s.reader.QueryContext(ctx, `SELECT id, event, source_title, data, created_at FROM history ORDER BY id`)
	if err != nil {
		t.Fatal(err)
	}
	var n int
	for rows.Next() {
		var id, created int64
		var ev, title, data string
		if err := rows.Scan(&id, &ev, &title, &data, &created); err != nil {
			t.Fatal(err)
		}
		n++
		if id != 7 || ev != "grabbed" || title != "Heat.1995.1080p.BluRay.x264-GRP" || data != `{"reasons":["ok"]}` || created != 100 {
			t.Errorf("row after rebuild = %d %s %q %s %d", id, ev, title, data, created)
		}
	}
	if err := rows.Err(); err != nil || n != 1 {
		t.Fatalf("%d rows, %v", n, err)
	}
	for _, ev := range []string{"movie_added", "movie_deleted", "grab_overridden", "retry_scheduled", "retries_exhausted", "download_removed"} {
		if _, err := s.Write().AppendHistory(ctx, db.AppendHistoryParams{Event: ev, Data: "{}", CreatedAt: 200}); err != nil {
			t.Errorf("append %s: %v", ev, MapError(err))
		}
	}
	checkHistorySchema(t, s, []string{"history_created_at", "history_download_id", "history_event_id", "history_movie_id_created_at"})

	if err := s.MigrateDownTo(ctx, 1); err != nil {
		t.Fatal(err)
	}
	var count int
	if err := s.reader.QueryRowContext(ctx, `SELECT count(*) FROM history`).Scan(&count); err != nil || count != 1 {
		t.Errorf("rows after down = %d, %v; want the grabbed row only", count, err)
	}
	checkHistorySchema(t, s, []string{"history_created_at", "history_movie_id_created_at"})
	if err := s.MigrateUp(ctx); err != nil {
		t.Fatal(err)
	}
}

func checkHistorySchema(t *testing.T, s *Store, wantIndexes []string) {
	t.Helper()
	ctx := context.Background()
	rows, err := s.reader.QueryContext(ctx, `SELECT type, name FROM sqlite_schema WHERE tbl_name = 'history' AND type IN ('index', 'trigger') ORDER BY type, name`)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = rows.Close() }()
	var idx, trg []string
	for rows.Next() {
		var typ, name string
		if err := rows.Scan(&typ, &name); err != nil {
			t.Fatal(err)
		}
		if typ == "index" {
			idx = append(idx, name)
		} else {
			trg = append(trg, name)
		}
	}
	if len(idx) != len(wantIndexes) {
		t.Errorf("indexes = %v, want %v", idx, wantIndexes)
	} else {
		for i := range idx {
			if idx[i] != wantIndexes[i] {
				t.Errorf("indexes = %v, want %v", idx, wantIndexes)
				break
			}
		}
	}
	if len(trg) != 2 || trg[0] != "history_no_update" || trg[1] != "history_retention_only" {
		t.Errorf("triggers = %v", trg)
	}
	if err := s.exec(t, `UPDATE history SET source_title = 'x'`); !errors.Is(err, ErrImmutable) {
		t.Errorf("UPDATE history err = %v, want ErrImmutable", err)
	}
	if err := s.exec(t, `DELETE FROM history`); !errors.Is(err, ErrImmutable) {
		t.Errorf("DELETE history err = %v, want ErrImmutable", err)
	}
}
