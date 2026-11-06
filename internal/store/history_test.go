package store_test

import (
	"context"
	"errors"
	"math"
	"testing"
	"time"

	"github.com/fs0ciety7000/gonema/internal/store"
	"github.com/fs0ciety7000/gonema/internal/store/db"
	"github.com/fs0ciety7000/gonema/internal/store/storetest"
)

func TestHistoryAppendOnly(t *testing.T) {
	ctx := context.Background()
	s := storetest.New(t)
	lib := storetest.SeedLibrary(t, s)
	m := insertMovie(t, s, movieParams(lib, 1, "heat"))
	w, r := s.Write(), s.Read()

	old := store.Millis(time.Now().Add(-100 * 24 * time.Hour))
	recent := store.Millis(time.Now().Add(-time.Hour))
	var ids []int64
	for i, created := range []int64{old, old + 1, recent} {
		h, err := w.AppendHistory(ctx, db.AppendHistoryParams{
			MovieID: &m.ID, Event: "grabbed", SourceTitle: "Heat.1995.1080p.BluRay.x264-GRP",
			Quality: ptr("bluray-1080p"), CustomScore: ptr[int64](int64(i)), Data: `{"reasons":[]}`, CreatedAt: created,
		})
		if err != nil {
			t.Fatal(err)
		}
		ids = append(ids, h.ID)
	}
	if _, err := w.AppendHistory(ctx, db.AppendHistoryParams{Event: "exploded", Data: "{}", CreatedAt: 1}); !errors.Is(store.MapError(err), store.ErrInvalid) {
		t.Errorf("bad event err = %v, want ErrInvalid", err)
	}
	if _, err := w.AppendHistory(ctx, db.AppendHistoryParams{Event: "ignored", Data: "[]", CreatedAt: 1}); !errors.Is(store.MapError(err), store.ErrInvalid) {
		t.Errorf("data not an object err = %v, want ErrInvalid", err)
	}

	page, err := r.ListMovieHistory(ctx, db.ListMovieHistoryParams{MovieID: m.ID, BeforeID: math.MaxInt64, Limit: 2})
	if err != nil || len(page) != 2 || page[0].ID != ids[2] {
		t.Fatalf("ListMovieHistory page 1 = %+v, %v", page, err)
	}
	page, err = r.ListMovieHistory(ctx, db.ListMovieHistoryParams{MovieID: m.ID, BeforeID: page[1].ID, Limit: 2})
	if err != nil || len(page) != 1 || page[0].ID != ids[0] {
		t.Fatalf("ListMovieHistory page 2 = %+v, %v", page, err)
	}
	if rec, err := r.ListRecentHistory(ctx, db.ListRecentHistoryParams{BeforeID: math.MaxInt64, Limit: 10}); err != nil || len(rec) != 3 {
		t.Errorf("ListRecentHistory = %d rows, %v", len(rec), err)
	}

	// Immutable: no update, no delete outside the retention purge.
	err = s.WithTx(ctx, func(q *db.Queries) error {
		_, err := q.PurgeHistory(ctx, math.MaxInt64)
		return err
	})
	if !errors.Is(err, store.ErrImmutable) {
		t.Errorf("delete without retention setting err = %v, want ErrImmutable", err)
	}
	if err := store.SetSetting(ctx, s, "history.retention_days", 30); err != nil {
		t.Fatal(err)
	}
	if _, err := w.PurgeHistory(ctx, math.MaxInt64); !errors.Is(store.MapError(err), store.ErrImmutable) {
		t.Errorf("purge including recent rows err = %v, want ErrImmutable", err)
	}
	n, err := w.PurgeHistory(ctx, store.Millis(time.Now().Add(-30*24*time.Hour)))
	if err != nil || n != 2 {
		t.Fatalf("PurgeHistory(older than retention) = %d, %v; want 2", n, store.MapError(err))
	}
	if err := store.SetSetting(ctx, s, "history.retention_days", "30"); err != nil {
		t.Fatal(err)
	}
	if _, err := w.PurgeHistory(ctx, math.MaxInt64); !errors.Is(store.MapError(err), store.ErrImmutable) {
		t.Errorf("non-integer retention must disable purge, err = %v", err)
	}

	// Deleting the movie keeps its history (soft reference).
	if err := store.RequireRow(w.DeleteMovie(ctx, m.ID)); err != nil {
		t.Fatal(err)
	}
	if rec, _ := r.ListMovieHistory(ctx, db.ListMovieHistoryParams{MovieID: m.ID, BeforeID: math.MaxInt64, Limit: 10}); len(rec) != 1 {
		t.Errorf("history after movie delete = %d rows, want 1", len(rec))
	}
}
