package library

import (
	"context"
	"fmt"
	"sync"
	"sync/atomic"

	"github.com/fs0ciety7000/gonema/internal/match"
	"github.com/fs0ciety7000/gonema/internal/parser"
	"github.com/fs0ciety7000/gonema/internal/store"
	"github.com/fs0ciety7000/gonema/internal/store/db"
)

// matcher keeps a match.Index in sync with the library. Readers load the
// current immutable index without locking; writers update the entry set under
// a mutex and publish a rebuilt index (a few milliseconds for 10 000 movies).
type matcher struct {
	mu      sync.Mutex
	entries map[int64]match.Entry
	idx     atomic.Pointer[match.Index]
}

func (m *matcher) index() *match.Index {
	if idx := m.idx.Load(); idx != nil {
		return idx
	}
	return match.NewIndex(nil)
}

// rebuildLocked publishes a new index from the entries. m.mu must be held.
func (m *matcher) rebuildLocked() {
	list := make([]match.Entry, 0, len(m.entries))
	for _, e := range m.entries {
		list = append(list, e)
	}
	m.idx.Store(match.NewIndex(list))
}

func (m *matcher) replaceAll(entries map[int64]match.Entry) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.entries = entries
	m.rebuildLocked()
}

func (m *matcher) put(e match.Entry) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.entries == nil {
		m.entries = make(map[int64]match.Entry)
	}
	m.entries[e.MovieID] = e
	m.rebuildLocked()
}

func (m *matcher) remove(movieID int64) {
	m.mu.Lock()
	defer m.mu.Unlock()
	delete(m.entries, movieID)
	m.rebuildLocked()
}

// RebuildMatcher reloads the release matcher from the database.
func (s *Service) RebuildMatcher(ctx context.Context) error {
	entries := make(map[int64]match.Entry)
	err := s.st.ReadTx(ctx, func(q *db.Queries) error {
		movies, err := q.ListMatcherMovies(ctx)
		if err != nil {
			return fmt.Errorf("list matcher movies: %w", err)
		}
		for _, m := range movies {
			e := match.Entry{MovieID: m.ID, Title: m.Title, OriginalTitle: m.OriginalTitle, Year: int(m.Year)}
			if m.ImdbID != nil {
				e.IMDbID = *m.ImdbID
			}
			entries[m.ID] = e
		}
		alts, err := q.ListMatcherAlternateTitleRows(ctx)
		if err != nil {
			return fmt.Errorf("list matcher alternate titles: %w", err)
		}
		for _, a := range alts {
			if e, ok := entries[a.MovieID]; ok {
				e.AlternateTitles = append(e.AlternateTitles, a.Title)
				entries[a.MovieID] = e
			}
		}
		return nil
	})
	if err != nil {
		return fmt.Errorf("library: rebuild matcher: %w", err)
	}
	s.matcher.replaceAll(entries)
	return nil
}

// refreshMatcherEntry reloads one movie into the matcher.
func (s *Service) refreshMatcherEntry(ctx context.Context, movieID int64) error {
	m, err := s.st.Read().GetMovie(ctx, movieID)
	if err != nil {
		return store.MapError(fmt.Errorf("library: matcher entry %d: %w", movieID, err))
	}
	alts, err := s.st.Read().ListAlternateTitles(ctx, movieID)
	if err != nil {
		return store.MapError(fmt.Errorf("library: matcher alternate titles %d: %w", movieID, err))
	}
	e := match.Entry{MovieID: m.ID, Title: m.Title, OriginalTitle: m.OriginalTitle, Year: int(m.Year)}
	if m.ImdbID != nil {
		e.IMDbID = *m.ImdbID
	}
	for _, a := range alts {
		e.AlternateTitles = append(e.AlternateTitles, a.Title)
	}
	s.matcher.put(e)
	return nil
}

// MatchRelease returns the library movies a parsed release may be, best first
// (see match.Index.Match). Only Confident candidates may be grabbed without
// confirmation.
func (s *Service) MatchRelease(p parser.ParsedRelease) []match.Candidate {
	return s.matcher.index().Match(p)
}

// MatchIMDb returns the library movie with this IMDb id, if any.
func (s *Service) MatchIMDb(imdbID string) []match.Candidate {
	return s.matcher.index().MatchIMDb(imdbID)
}

// MatcherSize returns the number of movies in the matcher.
func (s *Service) MatcherSize() int { return s.matcher.index().Len() }
