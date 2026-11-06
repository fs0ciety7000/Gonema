package imagecache

import (
	"bytes"
	"context"
	"errors"
	"image"
	"image/color"
	"image/draw"
	"image/jpeg"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/fs0ciety7000/gonema/internal/netx"
)

type staticBase string

func (s staticBase) ImageBaseURL(context.Context) (string, error) { return string(s), nil }

type imageServer struct {
	srv      *httptest.Server
	requests atomic.Int32
	mu       sync.Mutex
	handler  func(w http.ResponseWriter, r *http.Request) bool
	lastUA   atomic.Value
}

func jpegBytes(t testing.TB, c color.NRGBA) []byte {
	t.Helper()
	img := image.NewNRGBA(image.Rect(0, 0, 342, 513))
	draw.Draw(img, img.Bounds(), &image.Uniform{C: c}, image.Point{}, draw.Src)
	var buf bytes.Buffer
	if err := jpeg.Encode(&buf, img, &jpeg.Options{Quality: 90}); err != nil {
		t.Fatal(err)
	}
	return buf.Bytes()
}

func newImageServer(t *testing.T) *imageServer {
	t.Helper()
	body := jpegBytes(t, color.NRGBA{R: 0x1d, G: 0x4e, B: 0xd8, A: 0xff})
	is := &imageServer{}
	is.srv = httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		is.requests.Add(1)
		is.lastUA.Store(r.Header.Get("User-Agent"))
		is.mu.Lock()
		h := is.handler
		is.mu.Unlock()
		if h != nil && h(w, r) {
			return
		}
		if !strings.HasPrefix(r.URL.Path, "/t/p/") || strings.Contains(r.URL.Path, "missing") {
			http.NotFound(w, r)
			return
		}
		w.Header().Set("Content-Type", "image/jpeg")
		_, _ = w.Write(body)
	}))
	t.Cleanup(is.srv.Close)
	return is
}

func (is *imageServer) setHandler(h func(w http.ResponseWriter, r *http.Request) bool) {
	is.mu.Lock()
	defer is.mu.Unlock()
	is.handler = h
}

func newTestCache(t *testing.T, is *imageServer, opts Options) (c *Cache, dir string) {
	t.Helper()
	dir = t.TempDir()
	opts.HTTPClient = is.srv.Client()
	c, err := New(dir, staticBase(is.srv.URL+"/t/p/"), opts)
	if err != nil {
		t.Fatal(err)
	}
	return c, dir
}

func noTempFiles(t *testing.T, dir string) {
	t.Helper()
	err := filepath.WalkDir(dir, func(p string, _ os.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if strings.HasPrefix(filepath.Base(p), ".tmp-") {
			t.Errorf("temporary file left behind: %s", p)
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
}

func TestFetch(t *testing.T) {
	t.Parallel()
	is := newImageServer(t)
	c, dir := newTestCache(t, is, Options{UserAgent: "Gonema/test"})
	p, err := c.Fetch(t.Context(), Poster, "w342", "/abc123.jpg")
	if err != nil {
		t.Fatal(err)
	}
	if want := filepath.Join(dir, "images", "poster", "w342", "abc123.jpg"); p != want {
		t.Errorf("path = %s, want %s", p, want)
	}
	fi, err := os.Stat(p)
	if err != nil || fi.Size() == 0 || fi.Mode().Perm() != 0o644 {
		t.Fatalf("cached file: %v, %v", fi, err)
	}
	if ua, _ := is.lastUA.Load().(string); ua != "Gonema/test" {
		t.Errorf("User-Agent = %q", ua)
	}
	// Cached: no second request.
	if _, err := c.Fetch(t.Context(), Poster, "w342", "/abc123.jpg"); err != nil {
		t.Fatal(err)
	}
	if n := is.requests.Load(); n != 1 {
		t.Errorf("%d requests, want 1", n)
	}
	for _, k := range []struct {
		kind Kind
		size string
	}{{Poster, "w780"}, {Backdrop, "w1280"}, {Profile, "w185"}} {
		if _, err := c.Fetch(t.Context(), k.kind, k.size, "/abc123.jpg"); err != nil {
			t.Errorf("Fetch %s %s: %v", k.kind, k.size, err)
		}
	}
	noTempFiles(t, dir)
}

func TestFetchInvalidPath(t *testing.T) {
	t.Parallel()
	is := newImageServer(t)
	c, _ := newTestCache(t, is, Options{})
	tests := []struct {
		kind Kind
		size string
		path string
	}{
		{Poster, "w342", "/../../etc/passwd.jpg"},
		{Poster, "w342", "abc.jpg"},
		{Poster, "w342", "/a/b.jpg"},
		{Poster, "w342", "/abc.svg"},
		{Poster, "w342", "/abc"},
		{Poster, "w342", "/.jpg"},
		{Poster, "w342", "/a b.jpg"},
		{Poster, "w500", "/abc.jpg"},
		{Poster, "original", "/abc.jpg"},
		{Backdrop, "w342", "/abc.jpg"},
		{"logo", "w185", "/abc.jpg"},
		{Profile, "w185", "/" + strings.Repeat("a", 200) + ".jpg"},
	}
	for _, tt := range tests {
		if _, err := c.Fetch(t.Context(), tt.kind, tt.size, tt.path); !errors.Is(err, ErrInvalidPath) {
			t.Errorf("Fetch(%s, %s, %q) error = %v, want ErrInvalidPath", tt.kind, tt.size, tt.path, err)
		}
	}
	if n := is.requests.Load(); n != 0 {
		t.Errorf("%d requests for invalid paths", n)
	}
}

func TestFetchRejects(t *testing.T) {
	t.Parallel()
	big := make([]byte, 4096)
	copy(big, jpegBytes(t, color.NRGBA{R: 255, A: 255}))
	tests := []struct {
		name    string
		handler func(w http.ResponseWriter, r *http.Request) bool
		opts    Options
		want    error
	}{
		{"404", func(w http.ResponseWriter, r *http.Request) bool { http.NotFound(w, r); return true }, Options{}, ErrNotFound},
		{"html content type", func(w http.ResponseWriter, _ *http.Request) bool {
			w.Header().Set("Content-Type", "text/html")
			_, _ = w.Write([]byte("<html></html>"))
			return true
		}, Options{}, ErrNotImage},
		{"html body with image content type", func(w http.ResponseWriter, _ *http.Request) bool {
			w.Header().Set("Content-Type", "image/jpeg")
			_, _ = w.Write([]byte("<html><body>not an image</body></html>"))
			return true
		}, Options{}, ErrNotImage},
		{"svg", func(w http.ResponseWriter, _ *http.Request) bool {
			w.Header().Set("Content-Type", "image/svg+xml")
			_, _ = w.Write([]byte("<svg/>"))
			return true
		}, Options{}, ErrNotImage},
		{"declared too large", func(w http.ResponseWriter, _ *http.Request) bool {
			w.Header().Set("Content-Type", "image/jpeg")
			w.Header().Set("Content-Length", "4096")
			_, _ = w.Write(big)
			return true
		}, Options{MaxBytes: 1024}, ErrTooLarge},
		{"streamed too large", func(w http.ResponseWriter, _ *http.Request) bool {
			w.Header().Set("Content-Type", "image/jpeg")
			for range 4 { // no Content-Length: chunked
				_, _ = w.Write(big[:1024])
				w.(http.Flusher).Flush()
			}
			return true
		}, Options{MaxBytes: 2048}, ErrTooLarge},
		{"too many pixels", nil, Options{MaxPixels: 1000}, ErrTooLarge},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			is := newImageServer(t)
			is.setHandler(tt.handler)
			c, dir := newTestCache(t, is, tt.opts)
			_, err := c.Fetch(t.Context(), Poster, "w342", "/x.jpg")
			if !errors.Is(err, tt.want) {
				t.Fatalf("error = %v, want %v", err, tt.want)
			}
			if p, _ := c.Path(Poster, "w342", "/x.jpg"); fileExists(p) {
				t.Error("rejected image was stored")
			}
			noTempFiles(t, dir)
		})
	}
}

func fileExists(p string) bool {
	_, err := os.Stat(p)
	return err == nil
}

func TestFetchRequiresHTTPSBase(t *testing.T) {
	t.Parallel()
	c, err := New(t.TempDir(), staticBase("http://image.tmdb.org/t/p/"), Options{})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := c.Fetch(t.Context(), Poster, "w342", "/x.jpg"); err == nil || !strings.Contains(err.Error(), "https") {
		t.Errorf("error = %v, want an https requirement", err)
	}
}

func TestFetchDedupe(t *testing.T) {
	t.Parallel()
	const callers = 12
	is := newImageServer(t)
	release := make(chan struct{})
	var once sync.Once
	is.setHandler(func(http.ResponseWriter, *http.Request) bool {
		<-release
		return false
	})
	c, dir := newTestCache(t, is, Options{})
	var wg sync.WaitGroup
	for range callers {
		wg.Go(func() {
			if _, err := c.Fetch(t.Context(), Backdrop, "w1280", "/same.jpg"); err != nil {
				t.Error(err)
			}
		})
	}
	deadline := time.Now().Add(5 * time.Second)
	for c.flight.TotalWaiters() != callers {
		if time.Now().After(deadline) {
			once.Do(func() { close(release) })
			t.Fatalf("only %d callers joined", c.flight.TotalWaiters())
		}
		time.Sleep(time.Millisecond)
	}
	once.Do(func() { close(release) })
	wg.Wait()
	if n := is.requests.Load(); n != 1 {
		t.Errorf("%d downloads for %d concurrent fetches, want 1", n, callers)
	}
	noTempFiles(t, dir)
}

func TestPosterColor(t *testing.T) {
	t.Parallel()
	is := newImageServer(t)
	c, _ := newTestCache(t, is, Options{})
	got, err := c.PosterColor(t.Context(), "/poster.jpg")
	if err != nil {
		t.Fatal(err)
	}
	if d := hueDistance(t, got, color.NRGBA{R: 0x1d, G: 0x4e, B: 0xd8, A: 0xff}); d > 5 {
		t.Errorf("PosterColor = %s, want blue", got)
	}
	if !fileExists(filepath.Join(c.root, "poster", ColorSize, "poster.jpg")) {
		t.Error("poster not cached at w342")
	}
	if _, err := c.PosterColor(t.Context(), "/missing.jpg"); !errors.Is(err, ErrNotFound) {
		t.Errorf("missing poster error = %v", err)
	}
}

func TestNewValidates(t *testing.T) {
	t.Parallel()
	if _, err := New("", staticBase("https://x/"), Options{}); err == nil {
		t.Error("New accepted an empty directory")
	}
	if _, err := New(t.TempDir(), nil, Options{}); err == nil {
		t.Error("New accepted a nil base source")
	}
	if got := Sizes(Poster); len(got) != 2 || got[0] != "w342" || got[1] != "w780" {
		t.Errorf("Sizes(Poster) = %v", got)
	}
}

// TestDefaultClientPublicOnly: without an injected client, downloads go
// through netx with the public-only policy (TMDB is on the Internet).
func TestDefaultClientPublicOnly(t *testing.T) {
	is := newImageServer(t)
	c, err := New(t.TempDir(), staticBase(is.srv.URL+"/t/p/"), Options{})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := c.Fetch(t.Context(), Poster, "w342", "/abc123.jpg"); !errors.Is(err, netx.ErrBlocked) {
		t.Fatalf("fetch from loopback = %v, want blocked", err)
	}
}
