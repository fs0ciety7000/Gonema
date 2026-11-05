package indexer

import (
	"context"
	"errors"
	"testing"
	"time"
)

func TestNormalizeIMDb(t *testing.T) {
	t.Parallel()
	tests := map[string]string{
		"tt0137523": "tt0137523", "0137523": "tt0137523", "137523": "tt0137523", "TT0137523": "tt0137523",
		"tt10872600": "tt10872600", "": "", "tt": "", "tt12a": "", "nm0000093": "", "12345678901": "",
	}
	for in, want := range tests {
		if got := NormalizeIMDb(in); got != want {
			t.Errorf("NormalizeIMDb(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestErrorMatchesKindAndCause(t *testing.T) {
	t.Parallel()
	err := error(&Error{Kind: ErrTimeout, Indexer: "Example", Message: "no answer within 15s", Err: context.DeadlineExceeded})
	if !errors.Is(err, ErrTimeout) || !errors.Is(err, context.DeadlineExceeded) || errors.Is(err, ErrUnauthorized) {
		t.Errorf("errors.Is mismatch for %v", err)
	}
	if got, want := err.Error(), "Example: the indexer did not answer in time (no answer within 15s)"; got != want {
		t.Errorf("Error() = %q, want %q", got, want)
	}
	rl := &Error{Kind: ErrRateLimited, Code: 429, Message: "Request limit reached", RetryAfter: time.Minute}
	if got, want := rl.Error(), "the indexer request limit is reached (code 429: Request limit reached), retry after 1m0s"; got != want {
		t.Errorf("Error() = %q, want %q", got, want)
	}
	if RetryAfter(rl) != time.Minute || RetryAfter(errors.New("x")) != 0 {
		t.Error("RetryAfter")
	}
}

func TestCapsMovieCategories(t *testing.T) {
	t.Parallel()
	c := Caps{Categories: []Category{
		{ID: 1000, Subcats: []Category{{ID: 2010}}},
		{ID: 2000, Subcats: []Category{{ID: 2010}, {ID: 2040}}},
		{ID: 100002},
	}}
	got := c.MovieCategories()
	if len(got) != 3 || got[0] != 2010 || got[1] != 2000 || got[2] != 2040 {
		t.Errorf("MovieCategories = %v", got)
	}
}
