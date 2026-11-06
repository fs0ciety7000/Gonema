package webui

import (
	"crypto/sha256"
	"encoding/base64"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"testing/fstest"
)

const indexHTML = `<!doctype html><html><head><meta charset="utf-8"><script>document.documentElement.dataset.theme='dark';</script>` +
	`<script type="module" src="./assets/index-abc123.js"></script></head><body><div id="root"></div></body></html>`

func testFS() fstest.MapFS {
	return fstest.MapFS{
		"index.html":                {Data: []byte(indexHTML)},
		"assets/index-abc123.js":    {Data: []byte("console.log('app')")},
		"assets/index-abc123.js.br": {Data: []byte("BR")},
		"assets/index-abc123.js.gz": {Data: []byte("GZ")},
		"assets/app-1.css":          {Data: []byte("body{}")},
		"favicon.svg":               {Data: []byte("<svg/>")},
		"site.webmanifest":          {Data: []byte("{}")},
		".gitkeep":                  {Data: nil},
	}
}

func get(t *testing.T, h http.Handler, target string, hdr ...string) *httptest.ResponseRecorder {
	t.Helper()
	req := httptest.NewRequest(http.MethodGet, target, http.NoBody)
	for i := 0; i+1 < len(hdr); i += 2 {
		req.Header.Set(hdr[i], hdr[i+1])
	}
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	return rec
}

func TestServing(t *testing.T) {
	h, err := New(Options{FS: testFS()})
	if err != nil {
		t.Fatal(err)
	}
	if !h.Built() {
		t.Error("Built = false with an index.html")
	}
	tests := []struct {
		name, target, status, ctype, cache, encoding string
		hdr                                          []string
		bodyHas                                      string
	}{
		{"root", "/", "200", "text/html; charset=utf-8", cacheNone, "", nil, `id="root"`},
		{"client route", "/movies/12", "200", "text/html; charset=utf-8", cacheNone, "", nil, `id="root"`},
		{"index file", "/index.html", "200", "text/html; charset=utf-8", cacheNone, "", nil, "__GONEMA__"},
		{"hashed asset", "/assets/app-1.css", "200", "text/css; charset=utf-8", cacheImmutable, "", nil, "body{}"},
		{"brotli", "/assets/index-abc123.js", "200", "text/javascript; charset=utf-8", cacheImmutable, "br", []string{"Accept-Encoding", "gzip, br"}, "BR"},
		{"gzip", "/assets/index-abc123.js", "200", "text/javascript; charset=utf-8", cacheImmutable, "gzip", []string{"Accept-Encoding", "gzip, br;q=0"}, "GZ"},
		{"identity", "/assets/index-abc123.js", "200", "text/javascript; charset=utf-8", cacheImmutable, "", nil, "console.log"},
		{"public file", "/favicon.svg", "200", "image/svg+xml", cacheShort, "", nil, "<svg/>"},
		{"manifest", "/site.webmanifest", "200", "application/manifest+json", cacheShort, "", nil, "{}"},
		{"missing asset", "/assets/gone-1.js", "404", "", "", "", nil, ""},
		{"precompressed file itself", "/assets/index-abc123.js.br", "404", "", "", "", nil, ""},
		{"hidden file", "/.gitkeep", "404", "", "", "", nil, ""},
		{"api is not the SPA", "/api", "404", "", "", "", nil, ""},
		{"api subpath", "/api/v2/x", "404", "", "", "", nil, ""},
		{"health", "/health", "404", "", "", "", nil, ""},
		{"traversal", "/../../etc/passwd.txt", "404", "", "", "", nil, ""},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			rec := get(t, h, tt.target, tt.hdr...)
			if got := rec.Code; got != atoi(tt.status) {
				t.Fatalf("status = %d, want %s", got, tt.status)
			}
			if rec.Header().Get("X-Content-Type-Options") != "nosniff" || !strings.Contains(rec.Header().Get("Content-Security-Policy"), "frame-ancestors 'none'") {
				t.Errorf("security headers missing: %v", rec.Header())
			}
			if rec.Code != 200 {
				return
			}
			if got := rec.Header().Get("Content-Type"); got != tt.ctype {
				t.Errorf("Content-Type = %q, want %q", got, tt.ctype)
			}
			if got := rec.Header().Get("Cache-Control"); got != tt.cache {
				t.Errorf("Cache-Control = %q, want %q", got, tt.cache)
			}
			if got := rec.Header().Get("Content-Encoding"); got != tt.encoding {
				t.Errorf("Content-Encoding = %q, want %q", got, tt.encoding)
			}
			if !strings.Contains(rec.Body.String(), tt.bodyHas) {
				t.Errorf("body %q does not contain %q", rec.Body.String(), tt.bodyHas)
			}
			if rec.Header().Get("ETag") == "" {
				t.Error("no ETag")
			}
		})
	}

	t.Run("etag 304", func(t *testing.T) {
		first := get(t, h, "/assets/app-1.css")
		rec := get(t, h, "/assets/app-1.css", "If-None-Match", first.Header().Get("ETag"))
		if rec.Code != http.StatusNotModified {
			t.Errorf("status = %d, want 304", rec.Code)
		}
		idx := get(t, h, "/")
		if rec := get(t, h, "/library", "If-None-Match", idx.Header().Get("ETag")); rec.Code != http.StatusNotModified {
			t.Errorf("index revalidation = %d, want 304", rec.Code)
		}
	})
	t.Run("encodings have their own etag", func(t *testing.T) {
		br := get(t, h, "/assets/index-abc123.js", "Accept-Encoding", "br")
		plain := get(t, h, "/assets/index-abc123.js")
		if br.Header().Get("ETag") == plain.Header().Get("ETag") || br.Header().Get("Vary") != "Accept-Encoding" {
			t.Errorf("br %q plain %q vary %q", br.Header().Get("ETag"), plain.Header().Get("ETag"), br.Header().Get("Vary"))
		}
	})
	t.Run("methods", func(t *testing.T) {
		rec := httptest.NewRecorder()
		h.ServeHTTP(rec, httptest.NewRequest(http.MethodPost, "/", http.NoBody))
		if rec.Code != http.StatusMethodNotAllowed {
			t.Errorf("POST = %d", rec.Code)
		}
	})
}

func TestBaseURLInjectionAndCSP(t *testing.T) {
	for _, base := range []string{"", "/gonema"} {
		h, err := New(Options{FS: testFS(), BaseURL: base})
		if err != nil {
			t.Fatal(err)
		}
		rec := get(t, h, "/movies/1")
		body := rec.Body.String()
		wantBase := `<head><base href="` + base + `/"><script>window.__GONEMA__={"baseUrl":"` + base + `"};</script><meta`
		if !strings.Contains(body, wantBase) {
			t.Errorf("base %q: body %q lacks %q", base, body, wantBase)
		}
		policy := rec.Header().Get("Content-Security-Policy")
		for _, script := range []string{`window.__GONEMA__={"baseUrl":"` + base + `"};`, "document.documentElement.dataset.theme='dark';"} {
			sum := sha256.Sum256([]byte(script))
			if h := "'sha256-" + base64.StdEncoding.EncodeToString(sum[:]) + "'"; !strings.Contains(policy, h) {
				t.Errorf("CSP %q lacks the hash of %q", policy, script)
			}
		}
		for _, d := range []string{"default-src 'self'", "img-src 'self' data: blob:", "connect-src 'self'", "object-src 'none'", "base-uri 'self'"} {
			if !strings.Contains(policy, d) {
				t.Errorf("CSP lacks %q", d)
			}
		}
		if strings.Contains(policy, "unsafe-eval") || strings.Contains(policy, "script-src 'self' 'unsafe-inline'") {
			t.Errorf("CSP too permissive: %q", policy)
		}
	}
}

func TestInjectionEscapes(t *testing.T) {
	page, _, err := injectConfig([]byte("<html><head></head></html>"), "/a</script><b>")
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(page), "</script><b>") {
		t.Errorf("unescaped base URL: %s", page)
	}
	if _, _, err := injectConfig([]byte("<html><body></body></html>"), ""); err == nil {
		t.Error("no <head>: want an error")
	}
}

func TestPlaceholder(t *testing.T) {
	h, err := New(Options{FS: fstest.MapFS{}})
	if err != nil {
		t.Fatal(err)
	}
	if h.Built() {
		t.Error("Built = true without index.html")
	}
	if rec := get(t, h, "/"); rec.Code != 200 || !strings.Contains(rec.Body.String(), "Gonema UI not built") {
		t.Errorf("placeholder = %d %q", rec.Code, rec.Body.String())
	}
	// The embedded build (placeholder or real) always loads.
	if _, err := New(Options{}); err != nil {
		t.Fatalf("embedded: %v", err)
	}
}

func atoi(s string) int {
	n := 0
	for _, c := range s {
		n = n*10 + int(c-'0')
	}
	return n
}
