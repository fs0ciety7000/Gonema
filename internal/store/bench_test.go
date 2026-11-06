package store_test

import (
	"context"
	"fmt"
	"testing"

	"github.com/fs0ciety7000/gonema/internal/store"
	"github.com/fs0ciety7000/gonema/internal/store/db"
	"github.com/fs0ciety7000/gonema/internal/store/storetest"
)

const benchMovies = 10_000

// seedMovies inserts n movies with 3 alternate titles each, in one transaction.
func seedMovies(b *testing.B, s *store.Store, lib storetest.Library, n, altsPerMovie int) {
	b.Helper()
	ctx := context.Background()
	err := s.WithTx(ctx, func(q *db.Queries) error {
		for i := range n {
			m, err := q.InsertMovie(ctx, movieParams(lib, int64(i+1), fmt.Sprintf("movie title %05d", i)))
			if err != nil {
				return err
			}
			for a := range altsPerMovie {
				if _, err := q.InsertAlternateTitle(ctx, db.InsertAlternateTitleParams{
					MovieID: m.ID, Title: "alt", CleanTitle: fmt.Sprintf("alt%d movie %05d", a, i), Source: "tmdb",
				}); err != nil {
					return err
				}
			}
		}
		return nil
	})
	if err != nil {
		b.Fatal(err)
	}
}

// BenchmarkInsertMovies10kTx: 10,000 movie inserts in one transaction (one op).
func BenchmarkInsertMovies10kTx(b *testing.B) {
	for b.Loop() {
		b.StopTimer()
		s := storetest.New(b)
		lib := storetest.SeedLibrary(b, s)
		b.StartTimer()
		seedMovies(b, s, lib, benchMovies, 0)
	}
}

// BenchmarkListPage100: one page of 100 movies sorted by sort_title, in the middle
// of a 10k library, with the derived state.
func BenchmarkListPage100(b *testing.B) {
	s := storetest.New(b)
	seedMovies(b, s, storetest.SeedLibrary(b, s), benchMovies, 0)
	ctx := context.Background()
	p := db.ListMoviesBySortTitleParams{AfterSortTitle: "movie title 05000", AfterID: 0, Limit: 100}
	for b.Loop() {
		page, err := s.Read().ListMoviesBySortTitle(ctx, p)
		if err != nil || len(page) != 100 {
			b.Fatal(len(page), err)
		}
	}
}

// BenchmarkLoadMatcherIndex: every clean title of 10k movies + 30k alternate titles.
func BenchmarkLoadMatcherIndex(b *testing.B) {
	s := storetest.New(b)
	seedMovies(b, s, storetest.SeedLibrary(b, s), benchMovies, 3)
	ctx := context.Background()
	for b.Loop() {
		titles, err := s.Read().ListMatcherTitles(ctx)
		if err != nil || len(titles) != benchMovies {
			b.Fatal(len(titles), err)
		}
		alts, err := s.Read().ListMatcherAlternateTitles(ctx)
		if err != nil || len(alts) != 3*benchMovies {
			b.Fatal(len(alts), err)
		}
	}
}
