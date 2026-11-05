package tmdb

import (
	"context"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"sync"
	"testing"
	"time"
)

const (
	testAPIKey = "0123456789abcdef0123456789abcdef"
	// testToken has the shape of a TMDB API Read Access Token (not a real one).
	testToken = "eyJhbGciOiJIUzI1NiJ9.eyJhdWQiOiJ0ZXN0IiwibmJmIjowfQ.c2lnbmF0dXJlLW5vdC1yZWFsLWF0LWFsbA"
)

// fixtureServer serves testdata files by path and records every request.
type fixtureServer struct {
	t      *testing.T
	srv    *httptest.Server
	routes map[string]string // path -> fixture file

	mu       sync.Mutex
	requests []*http.Request
	// handle, when set, takes over for matching paths; return false to fall back.
	handle func(w http.ResponseWriter, r *http.Request, n int) bool
}

func newFixtureServer(t *testing.T) *fixtureServer {
	t.Helper()
	fs := &fixtureServer{t: t, routes: map[string]string{
		"/configuration":           "configuration.json",
		"/authentication":          "authentication.json",
		"/search/movie":            "search_fight_club.json",
		"/movie/550":               "movie_550.json",
		"/find/tt0137523":          "find_tt0137523.json",
		"/find/tt9999999":          "find_empty.json",
		"/collection/10":           "collection_10.json",
		"/person/31/movie_credits": "person_31_movie_credits.json",
		"/movie/popular":           "popular.json",
		"/movie/now_playing":       "now_playing.json",
		"/movie/upcoming":          "upcoming.json",
		"/trending/movie/week":     "trending_week.json",
		"/trending/movie/day":      "trending_week.json",
		"/movie/404":               "error_404.json",
	}}
	fs.srv = httptest.NewServer(http.HandlerFunc(fs.serve))
	t.Cleanup(fs.srv.Close)
	return fs
}

func (fs *fixtureServer) serve(w http.ResponseWriter, r *http.Request) {
	fs.mu.Lock()
	fs.requests = append(fs.requests, r.Clone(context.Background()))
	n := len(fs.requests)
	h := fs.handle
	fs.mu.Unlock()
	if h != nil && h(w, r, n) {
		return
	}
	file, ok := fs.routes[r.URL.Path]
	if !ok {
		writeFixture(fs.t, w, http.StatusNotFound, "error_404.json")
		return
	}
	status := http.StatusOK
	if r.URL.Path == "/movie/404" {
		status = http.StatusNotFound
	}
	writeFixture(fs.t, w, status, file)
}

func writeFixture(t *testing.T, w http.ResponseWriter, status int, file string) {
	t.Helper()
	b, err := os.ReadFile(filepath.Join("testdata", file))
	if err != nil {
		t.Errorf("read fixture: %v", err)
		w.WriteHeader(http.StatusInternalServerError)
		return
	}
	w.Header().Set("Content-Type", "application/json;charset=utf-8")
	w.WriteHeader(status)
	_, _ = w.Write(b)
}

func (fs *fixtureServer) count() int {
	fs.mu.Lock()
	defer fs.mu.Unlock()
	return len(fs.requests)
}

func (fs *fixtureServer) last() *http.Request {
	fs.mu.Lock()
	defer fs.mu.Unlock()
	if len(fs.requests) == 0 {
		return nil
	}
	return fs.requests[len(fs.requests)-1]
}

func (fs *fixtureServer) setHandler(h func(w http.ResponseWriter, r *http.Request, n int) bool) {
	fs.mu.Lock()
	defer fs.mu.Unlock()
	fs.handle = h
}

// sleepRecorder replaces the client's sleep: it records the delays and returns at once.
type sleepRecorder struct {
	mu     sync.Mutex
	delays []time.Duration
}

func (s *sleepRecorder) sleep(ctx context.Context, d time.Duration) error {
	s.mu.Lock()
	s.delays = append(s.delays, d)
	s.mu.Unlock()
	return ctx.Err()
}

func (s *sleepRecorder) get() []time.Duration {
	s.mu.Lock()
	defer s.mu.Unlock()
	return append([]time.Duration(nil), s.delays...)
}

// newTestClient returns a client against fs with fast, recorded retries.
func newTestClient(t *testing.T, fs *fixtureServer, key string, opts Options) (*Client, *sleepRecorder) {
	t.Helper()
	opts.BaseURL = fs.srv.URL
	opts.HTTPClient = fs.srv.Client()
	if opts.RateLimit == 0 {
		opts.RateLimit = 10_000
		opts.Burst = 10_000
	}
	c, err := New(key, opts)
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	sr := &sleepRecorder{}
	c.sleep = sr.sleep
	return c, sr
}
