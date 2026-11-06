package tmdb

import (
	"os"
	"testing"
	"time"

	"github.com/fs0ciety7000/gonema/internal/metadata"
)

// TestLiveTMDB runs against the real API. Opt-in: GONEMA_TMDB_LIVE=1 and the
// credential in GONEMA_TMDB_KEY (e.g. through `kit secrets run`). The key is
// never printed.
func TestLiveTMDB(t *testing.T) {
	if os.Getenv("GONEMA_TMDB_LIVE") != "1" {
		t.Skip("live TMDB test: set GONEMA_TMDB_LIVE=1 and GONEMA_TMDB_KEY")
	}
	key := os.Getenv("GONEMA_TMDB_KEY")
	if key == "" {
		t.Fatal("GONEMA_TMDB_KEY is not set")
	}
	c, err := New(key, Options{Region: "FR", RateLimit: 4, Burst: 2, Timeout: 15 * time.Second})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	ctx := t.Context()
	if err := c.ValidateKey(ctx); err != nil {
		t.Fatalf("ValidateKey: %v", err)
	}
	m, err := c.Movie(ctx, 550)
	if err != nil {
		t.Fatalf("Movie(550): %v", err)
	}
	if m.IMDbID != "tt0137523" || m.Year != 1999 || m.Status != metadata.StatusReleased || len(m.Extra.Cast) == 0 {
		t.Errorf("Movie(550) = %s %d %s, %d cast", m.IMDbID, m.Year, m.Status, len(m.Extra.Cast))
	}
	if _, err := c.FindByIMDb(ctx, "tt0137523"); err != nil {
		t.Errorf("FindByIMDb: %v", err)
	}
	if p, err := c.Search(ctx, "Fight Club", 1999, 1); err != nil || len(p.Results) == 0 {
		t.Errorf("Search: %v (%d results)", err, len(p.Results))
	}
	if base, err := c.ImageBaseURL(ctx); err != nil || base == "" {
		t.Errorf("ImageBaseURL: %q, %v", base, err)
	}
}
