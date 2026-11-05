package store_test

import (
	"cmp"
	"context"
	"fmt"
	"math"
	"math/rand/v2"
	"slices"
	"testing"

	"github.com/fs0ciety7000/gonema/internal/store"
	"github.com/fs0ciety7000/gonema/internal/store/db"
	"github.com/fs0ciety7000/gonema/internal/store/storetest"
)

// TestKeysetPagination walks every page of both orders, with many duplicate sort
// keys, and compares with the full ordering computed in Go.
func TestKeysetPagination(t *testing.T) {
	ctx := context.Background()
	s := storetest.New(t)
	lib := storetest.SeedLibrary(t, s)
	rng := rand.New(rand.NewPCG(1, 2))

	type key struct {
		id      int64
		title   string
		addedAt int64
	}
	var all []key
	err := s.WithTx(ctx, func(q *db.Queries) error {
		for i := range 250 {
			p := movieParams(lib, int64(i+1), fmt.Sprintf("title %02d", rng.IntN(40)))
			p.AddedAt = int64(rng.IntN(30)) // many ties
			m, err := q.InsertMovie(ctx, p)
			if err != nil {
				return err
			}
			all = append(all, key{m.ID, m.SortTitle, m.AddedAt})
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}

	for _, limit := range []int64{1, 7, 100, 250, 1000} {
		t.Run(fmt.Sprintf("sort_title/limit=%d", limit), func(t *testing.T) {
			want := slices.Clone(all)
			slices.SortFunc(want, func(a, b key) int { return cmp.Or(cmp.Compare(a.title, b.title), cmp.Compare(a.id, b.id)) })
			var got []key
			after, afterID := "", int64(0)
			for {
				page, err := s.Read().ListMoviesBySortTitle(ctx, db.ListMoviesBySortTitleParams{AfterSortTitle: after, AfterID: afterID, Limit: limit})
				if err != nil {
					t.Fatal(err)
				}
				for _, m := range page {
					got = append(got, key{m.ID, m.SortTitle, m.AddedAt})
				}
				if int64(len(page)) < limit {
					break
				}
				after, afterID = page[len(page)-1].SortTitle, page[len(page)-1].ID
			}
			if !slices.Equal(got, want) {
				t.Errorf("pages differ from full ordering: got %d rows, want %d", len(got), len(want))
			}
		})
		t.Run(fmt.Sprintf("added_desc/limit=%d", limit), func(t *testing.T) {
			want := slices.Clone(all)
			slices.SortFunc(want, func(a, b key) int { return cmp.Or(cmp.Compare(b.addedAt, a.addedAt), cmp.Compare(b.id, a.id)) })
			var got []key
			before, beforeID := int64(math.MaxInt64), int64(math.MaxInt64)
			for {
				page, err := s.Read().ListMoviesByAddedDesc(ctx, db.ListMoviesByAddedDescParams{BeforeAddedAt: before, BeforeID: beforeID, Limit: limit})
				if err != nil {
					t.Fatal(err)
				}
				for _, m := range page {
					got = append(got, key{m.ID, m.SortTitle, m.AddedAt})
					if m.State != "missing" {
						t.Fatalf("state = %q, want missing", m.State)
					}
				}
				if int64(len(page)) < limit {
					break
				}
				before, beforeID = page[len(page)-1].AddedAt, page[len(page)-1].ID
			}
			if !slices.Equal(got, want) {
				t.Errorf("pages differ from full ordering: got %d rows, want %d", len(got), len(want))
			}
		})
	}
	_ = store.ErrNotFound
}
