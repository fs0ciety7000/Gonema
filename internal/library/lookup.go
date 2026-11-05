package library

import (
	"context"
	"errors"
	"fmt"
	"strconv"
	"strings"
	"unicode/utf8"

	"github.com/fs0ciety7000/gonema/internal/metadata"
	"github.com/fs0ciety7000/gonema/internal/store"
)

// LookupResult is a provider search result, flagged when already in the library.
type LookupResult struct {
	metadata.MovieSummary
	MovieID int64 // library movie id, 0 when not in the library
}

// Lookup searches the provider. The query is a title, "tmdb:<id>", or an IMDb
// id ("tt0137523", "imdb:tt0137523"). year (0 = any) narrows a title search.
func (s *Service) Lookup(ctx context.Context, query string, year int) ([]LookupResult, error) {
	query = strings.TrimSpace(query)
	if query == "" {
		return nil, invalid("q", "type a title, a TMDB id (tmdb:550) or an IMDb id (tt0137523)")
	}
	if utf8.RuneCountInString(query) > 200 {
		return nil, invalid("q", "the search is too long (200 characters at most)")
	}
	if year != 0 && (year < 1870 || year > 2200) {
		return nil, invalid("year", "the year must be between 1870 and 2200")
	}
	p, err := s.Provider()
	if err != nil {
		return nil, err
	}
	lower := strings.ToLower(query)
	var found []metadata.MovieSummary
	switch {
	case strings.HasPrefix(lower, "tmdb:"):
		id, err := strconv.Atoi(strings.TrimSpace(query[5:]))
		if err != nil || id <= 0 {
			return nil, invalid("q", "a TMDB id is a positive number, e.g. tmdb:550")
		}
		m, err := p.Movie(ctx, id)
		if errors.Is(err, metadata.ErrNotFound) {
			return []LookupResult{}, nil
		}
		if err != nil {
			return nil, fmt.Errorf("library: lookup: %w", err)
		}
		found = []metadata.MovieSummary{summaryOf(m)}
	case strings.HasPrefix(lower, "imdb:") || isIMDbID(lower):
		id := strings.TrimSpace(strings.TrimPrefix(lower, "imdb:"))
		if !isIMDbID(id) {
			return nil, invalid("q", "an IMDb id looks like tt0137523")
		}
		m, err := p.FindByIMDb(ctx, id)
		if errors.Is(err, metadata.ErrNotFound) {
			return []LookupResult{}, nil
		}
		if err != nil {
			return nil, fmt.Errorf("library: lookup: %w", err)
		}
		found = []metadata.MovieSummary{m}
	default:
		page, err := p.Search(ctx, query, year, 1)
		if err != nil {
			return nil, fmt.Errorf("library: lookup: %w", err)
		}
		found = page.Results
	}
	out := make([]LookupResult, 0, len(found))
	for i := range found {
		m := &found[i]
		r := LookupResult{MovieSummary: *m}
		if row, err := s.st.Read().GetMovieByTmdbID(ctx, int64(m.TMDBID)); err == nil {
			r.MovieID = row.ID
		} else if !errors.Is(store.MapError(err), store.ErrNotFound) {
			return nil, store.MapError(fmt.Errorf("library: lookup: %w", err))
		}
		out = append(out, r)
	}
	return out, nil
}

func isIMDbID(s string) bool {
	if len(s) < 9 || len(s) > 12 || !strings.HasPrefix(s, "tt") {
		return false
	}
	for _, c := range s[2:] {
		if c < '0' || c > '9' {
			return false
		}
	}
	return true
}

func summaryOf(m *metadata.Movie) metadata.MovieSummary {
	s := metadata.MovieSummary{
		TMDBID: m.TMDBID, Title: m.Title, OriginalTitle: m.OriginalTitle, OriginalLanguage: m.OriginalLanguage,
		Year: m.Year, ReleaseDate: m.ReleaseDate, Overview: m.Overview, PosterPath: m.PosterPath,
		BackdropPath: m.BackdropPath, Popularity: m.Popularity, Adult: m.Adult,
	}
	if m.Ratings.TMDB != nil {
		r := *m.Ratings.TMDB
		s.Rating = &r
	}
	return s
}
