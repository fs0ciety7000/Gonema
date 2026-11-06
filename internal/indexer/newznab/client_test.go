package newznab

import (
	"context"
	"errors"
	"io"
	"log"
	"net"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/fs0ciety7000/gonema/internal/indexer"
)

const testKey = "s3cr3t+key/with=chars"

// fakeServer answers t=caps with capsFile and other functions with handle.
type fakeServer struct {
	t        testing.TB
	capsFile string
	handle   func(w http.ResponseWriter, r *http.Request)

	mu        sync.Mutex
	requests  []url.Values
	capsCalls atomic.Int32
}

func (s *fakeServer) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	if r.URL.Path != "/api" {
		http.NotFound(w, r)
		return
	}
	q := r.URL.Query()
	s.mu.Lock()
	s.requests = append(s.requests, q)
	s.mu.Unlock()
	if q.Get("t") == "caps" {
		s.capsCalls.Add(1)
		_, _ = w.Write(fixture(s.t, s.capsFile))
		return
	}
	s.handle(w, r)
}

func (s *fakeServer) last() url.Values {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.requests[len(s.requests)-1]
}

func serveFile(t testing.TB, name string) func(http.ResponseWriter, *http.Request) {
	return func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/rss+xml")
		_, _ = w.Write(fixture(t, name))
	}
}

func newTestClient(t *testing.T, fs *fakeServer, mutate ...func(*Options)) *Client {
	t.Helper()
	srv := httptest.NewServer(fs)
	t.Cleanup(srv.Close)
	o := Options{Info: indexer.Info{ID: 1, Name: "Example"}, BaseURL: srv.URL, APIKey: testKey}
	for _, m := range mutate {
		m(&o)
	}
	c, err := New(o)
	if err != nil {
		t.Fatal(err)
	}
	return c
}

func TestNewValidatesURL(t *testing.T) {
	t.Parallel()
	for _, u := range []string{"", "indexer.example", "ftp://indexer.example", "http://", "https://user:pw@indexer.example", "https://i.example/?apikey=x"} {
		if _, err := New(Options{BaseURL: u}); !errors.Is(err, indexer.ErrBadURL) {
			t.Errorf("New(%q) = %v, want ErrBadURL", u, err)
		}
	}
	c, err := New(Options{BaseURL: "https://indexer.example/", APIPath: "api"})
	if err != nil {
		t.Fatal(err)
	}
	if c.endpoint != "https://indexer.example/api" || c.Info().Protocol != "usenet" || c.Info().Priority != indexer.DefaultPriority {
		t.Errorf("endpoint %q info %+v", c.endpoint, c.Info())
	}
}

func TestSearchByIMDb(t *testing.T) {
	t.Parallel()
	fs := &fakeServer{t: t, capsFile: "caps.xml", handle: serveFile(t, "movie_search.xml")}
	c := newTestClient(t, fs)
	rels, err := c.Search(t.Context(), indexer.Query{IMDbID: "tt0137523", TMDbID: 550, Title: "Fight Club", Year: 1999})
	if err != nil {
		t.Fatal(err)
	}
	if len(rels) != 2 || rels[0].IndexerName != "Example" {
		t.Fatalf("releases = %+v", rels)
	}
	q := fs.last()
	want := url.Values{
		"t": {"movie"}, "imdbid": {"0137523"}, "apikey": {testKey}, "cat": {"2000,2010,2030,2040,2045,2050"},
		"offset": {"0"}, "limit": {"100"},
	}
	if q.Encode() != want.Encode() {
		t.Errorf("query = %s\nwant    %s", q.Encode(), want.Encode())
	}
	// Caps are cached.
	if _, err := c.Search(t.Context(), indexer.Query{IMDbID: "tt0137523"}); err != nil {
		t.Fatal(err)
	}
	if n := fs.capsCalls.Load(); n != 1 {
		t.Errorf("caps fetched %d times, want 1", n)
	}
}

func TestSearchModeSelection(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name, caps string
		q          indexer.Query
		want       url.Values
	}{
		{"tmdb only, not supported: text on movie", "caps.xml",
			indexer.Query{TMDbID: 550, Title: "Fight Club", Year: 1999},
			url.Values{"t": {"movie"}, "q": {"Fight Club 1999"}}},
		{"legacy: t=search", "caps_legacy.xml",
			indexer.Query{IMDbID: "tt0137523", Title: "Fight Club", Year: 1999},
			url.Values{"t": {"search"}, "q": {"Fight Club 1999"}}},
		{"torznab: both ids", "../../torznab/testdata/caps.xml",
			indexer.Query{IMDbID: "tt0137523", TMDbID: 550, Title: "Fight Club"},
			url.Values{"t": {"movie"}, "imdbid": {"0137523"}, "tmdbid": {"550"}}},
		{"torznab: tmdb only", "../../torznab/testdata/caps.xml",
			indexer.Query{TMDbID: 550},
			url.Values{"t": {"movie"}, "tmdbid": {"550"}}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			fs := &fakeServer{t: t, capsFile: tt.caps, handle: serveFile(t, "empty.xml")}
			c := newTestClient(t, fs)
			if _, err := c.Search(t.Context(), tt.q); err != nil {
				t.Fatal(err)
			}
			q := fs.last()
			for k, v := range tt.want {
				if q.Get(k) != v[0] {
					t.Errorf("%s = %q, want %q (query %s)", k, q.Get(k), v[0], q.Encode())
				}
			}
			for _, k := range []string{"imdbid", "tmdbid", "q"} {
				if _, ok := tt.want[k]; !ok && q.Has(k) {
					t.Errorf("unexpected %s in %s", k, q.Encode())
				}
			}
		})
	}
}

func TestSearchNoUsableQuery(t *testing.T) {
	t.Parallel()
	fs := &fakeServer{t: t, capsFile: "caps_legacy.xml", handle: serveFile(t, "empty.xml")}
	c := newTestClient(t, fs)
	_, err := c.Search(t.Context(), indexer.Query{TMDbID: 550})
	if !errors.Is(err, indexer.ErrBadRequest) {
		t.Fatalf("err = %v, want ErrBadRequest", err)
	}
}

func TestSearchPagination(t *testing.T) {
	t.Parallel()
	const total = 250
	fs := &fakeServer{t: t, capsFile: "caps.xml"}
	fs.handle = func(w http.ResponseWriter, r *http.Request) {
		off, _ := strconv.Atoi(r.URL.Query().Get("offset"))
		lim, _ := strconv.Atoi(r.URL.Query().Get("limit"))
		n := max(0, min(lim, total-off))
		_, _ = w.Write(feedXML(off, n, total))
	}
	c := newTestClient(t, fs)
	tests := []struct{ offset, pages, want, requests int }{
		{0, 5, 250, 3},
		{0, 2, 200, 2},
		{0, 0, 100, 1},
		{200, 3, 50, 1},
		{300, 3, 0, 1},
	}
	for _, tt := range tests {
		fs.mu.Lock()
		fs.requests = nil
		fs.mu.Unlock()
		rels, err := c.Search(t.Context(), indexer.Query{IMDbID: "tt0137523", Offset: tt.offset, MaxPages: tt.pages})
		if err != nil {
			t.Fatal(err)
		}
		fs.mu.Lock()
		n := 0
		for _, q := range fs.requests {
			if q.Get("t") != "caps" {
				n++
			}
		}
		fs.mu.Unlock()
		if len(rels) != tt.want || n != tt.requests {
			t.Errorf("offset %d pages %d: %d releases in %d requests, want %d in %d", tt.offset, tt.pages, len(rels), n, tt.want, tt.requests)
		}
	}
}

func TestRSS(t *testing.T) {
	t.Parallel()
	fs := &fakeServer{t: t, capsFile: "caps.xml", handle: serveFile(t, "rss.xml")}
	c := newTestClient(t, fs, func(o *Options) { o.Categories = []int{2040, 2045} })
	rels, err := c.RSS(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	if len(rels) != 4 || rels[0].GUID != "guid-4" {
		t.Fatalf("releases = %+v", rels)
	}
	q := fs.last()
	if q.Get("t") != "search" || q.Has("q") || q.Get("cat") != "2040,2045" || q.Get("limit") != "100" {
		t.Errorf("query = %s", q.Encode())
	}
}

func TestErrorsAreTypedAndRedacted(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name       string
		handle     func(w http.ResponseWriter, r *http.Request)
		kind       error
		retryAfter time.Duration
	}{
		{"error 100 echoing key", func(w http.ResponseWriter, _ *http.Request) {
			_, _ = w.Write([]byte(`<error code="100" description="Incorrect user credentials: apikey=` + testKey + `"/>`))
		}, indexer.ErrUnauthorized, 0},
		{"error 101", serveFile(t, "error_101.xml"), indexer.ErrSuspended, 0},
		{"error 429 with Retry-After", func(w http.ResponseWriter, _ *http.Request) {
			w.Header().Set("Retry-After", "120")
			w.WriteHeader(http.StatusTooManyRequests)
			_, _ = w.Write(fixture(t, "error_429.xml"))
		}, indexer.ErrRateLimited, 2 * time.Minute},
		{"error 500 limit", serveFile(t, "error_500_limit.xml"), indexer.ErrRateLimited, 0},
		{"HTTP 429 no body", func(w http.ResponseWriter, _ *http.Request) {
			w.Header().Set("Retry-After", "30")
			w.WriteHeader(http.StatusTooManyRequests)
		}, indexer.ErrRateLimited, 30 * time.Second},
		{"HTTP 401", func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(http.StatusUnauthorized) }, indexer.ErrUnauthorized, 0},
		{"HTTP 502 html", func(w http.ResponseWriter, _ *http.Request) {
			w.WriteHeader(http.StatusBadGateway)
			_, _ = w.Write([]byte("<html><body>Bad Gateway apikey=" + url.QueryEscape(testKey) + "</body></html>"))
		}, indexer.ErrServer, 0},
		{"HTML login page", serveFile(t, "html.html"), indexer.ErrNotIndexer, 0},
		{"garbage", func(w http.ResponseWriter, _ *http.Request) { _, _ = w.Write([]byte("\x00\x01garbage")) }, indexer.ErrNotIndexer, 0},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			fs := &fakeServer{t: t, capsFile: "caps.xml", handle: tt.handle}
			c := newTestClient(t, fs)
			_, err := c.Search(t.Context(), indexer.Query{IMDbID: "tt0137523"})
			if !errors.Is(err, tt.kind) {
				t.Fatalf("err = %v, want %v", err, tt.kind)
			}
			if got := indexer.RetryAfter(err); got != tt.retryAfter {
				t.Errorf("RetryAfter = %s, want %s", got, tt.retryAfter)
			}
			assertNoKey(t, err)
			if !strings.HasPrefix(err.Error(), "Example: ") {
				t.Errorf("error lacks the indexer name: %v", err)
			}
		})
	}
}

func assertNoKey(t *testing.T, err error) {
	t.Helper()
	msg := err.Error()
	for _, leak := range []string{testKey, url.QueryEscape(testKey), "s3cr3t"} {
		if strings.Contains(msg, leak) {
			t.Fatalf("error leaks the API key: %q", msg)
		}
	}
	for e := errors.Unwrap(err); e != nil; e = errors.Unwrap(e) {
		if strings.Contains(e.Error(), "s3cr3t") {
			t.Fatalf("wrapped error leaks the API key: %q", e.Error())
		}
	}
}

func TestTransportErrorsRedacted(t *testing.T) {
	t.Parallel()
	// A closed port: connection refused, the URL (with the key) is in url.Error.
	l, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	addr := l.Addr().String()
	_ = l.Close()
	c, err := New(Options{Info: indexer.Info{Name: "Down"}, BaseURL: "http://" + addr, APIKey: testKey})
	if err != nil {
		t.Fatal(err)
	}
	err = c.Test(t.Context())
	if !errors.Is(err, indexer.ErrUnreachable) {
		t.Fatalf("err = %v, want ErrUnreachable", err)
	}
	assertNoKey(t, err)
}

func TestRedactParam(t *testing.T) {
	t.Parallel()
	c := &Client{apiKey: "abc"}
	tests := map[string]string{
		`Get "http://h/api?apikey=zzz&t=caps": EOF`: `Get "http://h/api?apikey=REDACTED&t=caps": EOF`,
		"key abc in text": "key REDACTED in text",
		"apikey=":         "apikey=",
		"x apikey=q#frag": "x apikey=REDACTED#frag",
	}
	for in, want := range tests {
		if got := c.redact(in); got != want {
			t.Errorf("redact(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestResponseTooLarge(t *testing.T) {
	t.Parallel()
	big := strings.Repeat("<item><title>x</title></item>", (2<<20)/29+1)
	for _, chunked := range []bool{false, true} {
		fs := &fakeServer{t: t, capsFile: "caps.xml", handle: func(w http.ResponseWriter, _ *http.Request) {
			if !chunked {
				w.Header().Set("Content-Length", strconv.Itoa(len(big)+len("<rss><channel></channel></rss>")))
			}
			_, _ = w.Write([]byte("<rss><channel>" + big + "</channel></rss>"))
		}}
		c := newTestClient(t, fs, func(o *Options) { o.MaxBodyBytes = 1 << 20 })
		_, err := c.Search(t.Context(), indexer.Query{IMDbID: "tt0137523"})
		if !errors.Is(err, indexer.ErrBadResponse) || !strings.Contains(err.Error(), "larger than 1 MiB") {
			t.Errorf("chunked=%v: err = %v", chunked, err)
		}
	}
}

func TestSlowResponseTimesOut(t *testing.T) {
	t.Parallel()
	fs := &fakeServer{t: t, capsFile: "caps.xml", handle: func(w http.ResponseWriter, r *http.Request) {
		select {
		case <-time.After(5 * time.Second):
		case <-r.Context().Done():
		}
		_, _ = w.Write(fixture(t, "empty.xml"))
	}}
	c := newTestClient(t, fs, func(o *Options) { o.Timeout = 100 * time.Millisecond })
	start := time.Now()
	_, err := c.Search(t.Context(), indexer.Query{IMDbID: "tt0137523"})
	if !errors.Is(err, indexer.ErrTimeout) {
		t.Fatalf("err = %v, want ErrTimeout", err)
	}
	if d := time.Since(start); d > 2*time.Second {
		t.Errorf("took %s", d)
	}

	// The caller's own cancellation is not an indexer failure.
	ctx, cancel := context.WithCancel(t.Context())
	time.AfterFunc(50*time.Millisecond, cancel)
	c2 := newTestClient(t, fs)
	_, err = c2.Search(ctx, indexer.Query{IMDbID: "tt0137523"})
	var ie *indexer.Error
	if !errors.Is(err, context.Canceled) || errors.As(err, &ie) {
		t.Errorf("canceled: err = %v", err)
	}
}

func TestTestFunction(t *testing.T) {
	t.Parallel()
	t.Run("ok", func(t *testing.T) {
		t.Parallel()
		fs := &fakeServer{t: t, capsFile: "caps.xml", handle: serveFile(t, "empty.xml")}
		c := newTestClient(t, fs)
		if err := c.Test(t.Context()); err != nil {
			t.Fatal(err)
		}
		q := fs.last()
		if q.Get("t") != "search" || q.Get("limit") != "1" || q.Get("apikey") != testKey {
			t.Errorf("query = %v", q)
		}
	})
	t.Run("caps without key, search rejects it", func(t *testing.T) {
		t.Parallel()
		fs := &fakeServer{t: t, capsFile: "caps.xml", handle: serveFile(t, "error_100.xml")}
		c := newTestClient(t, fs)
		err := c.Test(t.Context())
		if !errors.Is(err, indexer.ErrUnauthorized) {
			t.Fatalf("err = %v", err)
		}
		if !strings.Contains(err.Error(), "rejected the API key") {
			t.Errorf("message not plain: %v", err)
		}
	})
	t.Run("wrong API path", func(t *testing.T) {
		t.Parallel()
		fs := &fakeServer{t: t, capsFile: "caps.xml", handle: serveFile(t, "empty.xml")}
		c := newTestClient(t, fs, func(o *Options) { o.APIPath = "/wrong" })
		if err := c.Test(t.Context()); !errors.Is(err, indexer.ErrNotIndexer) {
			t.Fatalf("err = %v", err)
		}
	})
	t.Run("not an indexer", func(t *testing.T) {
		t.Parallel()
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
			_, _ = w.Write(fixture(t, "html.html"))
		}))
		t.Cleanup(srv.Close)
		c, err := New(Options{BaseURL: srv.URL, APIKey: testKey})
		if err != nil {
			t.Fatal(err)
		}
		if err := c.Test(t.Context()); !errors.Is(err, indexer.ErrNotIndexer) {
			t.Fatalf("err = %v", err)
		}
	})
	t.Run("TLS", func(t *testing.T) {
		t.Parallel()
		srv := httptest.NewUnstartedServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
			_, _ = w.Write(fixture(t, "caps.xml"))
		}))
		srv.Config.ErrorLog = log.New(io.Discard, "", 0) // the expected handshake failure
		srv.StartTLS()
		t.Cleanup(srv.Close)
		c, err := New(Options{BaseURL: srv.URL, APIKey: testKey})
		if err != nil {
			t.Fatal(err)
		}
		err = c.Test(t.Context())
		if !errors.Is(err, indexer.ErrTLS) {
			t.Fatalf("err = %v, want ErrTLS", err)
		}
		assertNoKey(t, err)
	})
	t.Run("HTTPS to plain HTTP port", func(t *testing.T) {
		t.Parallel()
		srv := httptest.NewServer(http.NotFoundHandler())
		t.Cleanup(srv.Close)
		c, err := New(Options{BaseURL: strings.Replace(srv.URL, "http://", "https://", 1), APIKey: testKey})
		if err != nil {
			t.Fatal(err)
		}
		if err := c.Test(t.Context()); !errors.Is(err, indexer.ErrTLS) {
			t.Fatalf("err = %v, want ErrTLS", err)
		}
	})
}
