package mediaserver

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"strings"
	"sync"
	"testing"

	"github.com/fs0ciety7000/gonema/internal/outbound"
)

const (
	plexToken     = "plex-token-s3cr3t-0123"
	jellyfinToken = "0f1e2d3c4b5a69788796a5b4c3d2e1f0"
	kodiPassword  = "kodi-pa55w0rd!"
)

type recorded struct {
	method, path, query, body string
	header                    http.Header
}

// recorder is an httptest server that records requests and answers with fn.
type recorder struct {
	*httptest.Server
	mu   sync.Mutex
	reqs []recorded
}

func newRecorder(t *testing.T, fn func(w http.ResponseWriter, r *http.Request)) *recorder {
	t.Helper()
	rec := &recorder{}
	rec.Server = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		b, _ := io.ReadAll(r.Body)
		rec.mu.Lock()
		rec.reqs = append(rec.reqs, recorded{method: r.Method, path: r.URL.Path, query: r.URL.RawQuery, body: string(b), header: r.Header.Clone()})
		rec.mu.Unlock()
		fn(w, r)
	}))
	t.Cleanup(rec.Close)
	return rec
}

func (r *recorder) requests() []recorded {
	r.mu.Lock()
	defer r.mu.Unlock()
	return append([]recorded(nil), r.reqs...)
}

func fixture(t *testing.T, name string) []byte {
	t.Helper()
	b, err := os.ReadFile("testdata/" + name)
	if err != nil {
		t.Fatal(err)
	}
	return b
}

func mustNew(t *testing.T, cfg Config) Server {
	t.Helper()
	s, err := New(cfg, Options{Version: "1.2.3"})
	if err != nil {
		t.Fatal(err)
	}
	return s
}

func TestPlex(t *testing.T) {
	sections := fixture(t, "plex_sections.json")
	legacyRefresh := false
	rec := newRecorder(t, func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("X-Plex-Token") != plexToken {
			w.WriteHeader(http.StatusUnauthorized)
			return
		}
		switch {
		case r.URL.Path == "/plex/library/sections/all" && r.Method == http.MethodGet:
			if r.Header.Get("Accept") != "application/json" {
				t.Errorf("Accept = %q", r.Header.Get("Accept"))
			}
			_, _ = w.Write(sections)
		case strings.HasSuffix(r.URL.Path, "/refresh") && r.Method == http.MethodPost && legacyRefresh:
			w.WriteHeader(http.StatusMethodNotAllowed)
		case strings.HasSuffix(r.URL.Path, "/refresh"):
			w.WriteHeader(http.StatusOK)
		default:
			w.WriteHeader(http.StatusNotFound)
		}
	})
	srv := mustNew(t, Config{
		Kind: Plex, BaseURL: rec.URL + "/plex/", Token: plexToken,
		Mappings: []ServerPathMapping{{From: "/data/movies", To: "/media/movies"}, {From: "/data/uhd", To: "/mnt/uhd"}},
	})
	ctx := context.Background()
	if err := srv.Test(ctx); err != nil {
		t.Fatalf("Test: %v", err)
	}

	cases := []struct {
		req        RefreshRequest
		key, path  string
		legacyHTTP bool
	}{
		{RefreshRequest{Path: "/data/movies/Halcyon Drift (2024)", Reason: ReasonAdd}, "1", "/media/movies/Halcyon Drift (2024)", false},
		// Nested library: the deepest location wins.
		{RefreshRequest{Path: "/data/movies/4k/Dune (2021)", Reason: ReasonUpgrade}, "7", "/media/movies/4k/Dune (2021)", false},
		{RefreshRequest{Path: "/data/uhd/Heat (1995)", Reason: ReasonRename}, "7", "/mnt/uhd/Heat (1995)", true},
		// Deletion: the parent folder is scanned.
		{RefreshRequest{Path: "/data/movies/Gone (2012)", Reason: ReasonDelete}, "1", "/media/movies", false},
	}
	for _, tc := range cases {
		legacyRefresh = tc.legacyHTTP
		before := len(rec.requests())
		if err := srv.Refresh(ctx, tc.req); err != nil {
			t.Fatalf("Refresh(%+v): %v", tc.req, err)
		}
		reqs := rec.requests()[before:]
		last := reqs[len(reqs)-1]
		wantMethod := http.MethodPost
		if tc.legacyHTTP {
			wantMethod = http.MethodGet
		}
		if last.method != wantMethod || last.path != "/plex/library/sections/"+tc.key+"/refresh" {
			t.Errorf("%s: request = %s %s", tc.req.Path, last.method, last.path)
		}
		if got := mustQuery(t, last.query).Get("path"); got != tc.path {
			t.Errorf("%s: path = %q, want %q", tc.req.Path, got, tc.path)
		}
	}

	err := srv.Refresh(ctx, RefreshRequest{Path: "/elsewhere/Movie (2000)", Reason: ReasonAdd})
	if !errors.Is(err, ErrNoLibrary) || outbound.Temporary(err) || !strings.Contains(err.Error(), "path mapping") {
		t.Errorf("unmapped folder: %v", err)
	}

	bad := mustNew(t, Config{Kind: Plex, BaseURL: rec.URL + "/plex", Token: "wrong-token-value"})
	err = bad.Test(ctx)
	if !errors.Is(err, outbound.ErrAuth) || strings.Contains(err.Error(), "wrong-token-value") {
		t.Errorf("bad token: %v", err)
	}
	if err := mustNew(t, Config{Kind: Plex, BaseURL: rec.URL}).Test(ctx); !errors.Is(err, outbound.ErrAuth) {
		t.Errorf("missing token: %v", err)
	}
}

func mustQuery(t *testing.T, raw string) interface{ Get(string) string } {
	t.Helper()
	q, err := url.ParseQuery(raw)
	if err != nil {
		t.Fatal(err)
	}
	return q
}

func TestJellyfin(t *testing.T) {
	info := fixture(t, "jellyfin_system_info.json")
	noMediaUpdated := false
	rec := newRecorder(t, func(w http.ResponseWriter, r *http.Request) {
		auth := r.Header.Get("Authorization")
		if !strings.HasPrefix(auth, "MediaBrowser ") || !strings.Contains(auth, `Token="`+jellyfinToken+`"`) ||
			!strings.Contains(auth, `Client="Gonema"`) || !strings.Contains(auth, `Version="1.2.3"`) {
			w.WriteHeader(http.StatusUnauthorized)
			return
		}
		switch {
		case r.URL.Path == "/System/Info":
			_, _ = w.Write(info)
		case r.URL.Path == "/Library/Media/Updated" && !noMediaUpdated:
			w.WriteHeader(http.StatusNoContent)
		case r.URL.Path == "/Library/Refresh":
			w.WriteHeader(http.StatusNoContent)
		default:
			w.WriteHeader(http.StatusNotFound)
		}
	})
	srv := mustNew(t, Config{Kind: Jellyfin, BaseURL: rec.URL, Token: jellyfinToken, Mappings: []ServerPathMapping{{From: "/data/movies", To: "/movies"}}})
	ctx := context.Background()
	if err := srv.Test(ctx); err != nil {
		t.Fatalf("Test: %v", err)
	}
	for _, tc := range []struct {
		reason Reason
		typ    string
	}{{ReasonAdd, "Created"}, {ReasonUpgrade, "Modified"}, {ReasonRename, "Modified"}, {ReasonDelete, "Deleted"}} {
		if err := srv.Refresh(ctx, RefreshRequest{Path: "/data/movies/Heat (1995)", Reason: tc.reason}); err != nil {
			t.Fatal(err)
		}
		reqs := rec.requests()
		last := reqs[len(reqs)-1]
		var body struct {
			Updates []struct{ Path, UpdateType string }
		}
		if err := json.Unmarshal([]byte(last.body), &body); err != nil || len(body.Updates) != 1 {
			t.Fatalf("body %q: %v", last.body, err)
		}
		if u := body.Updates[0]; u.Path != "/movies/Heat (1995)" || u.UpdateType != tc.typ || last.header.Get("Content-Type") != "application/json" {
			t.Errorf("%s: update = %+v", tc.reason, u)
		}
	}
	noMediaUpdated = true
	if err := srv.Refresh(ctx, RefreshRequest{Path: "/data/movies/Heat (1995)", Reason: ReasonAdd}); err != nil {
		t.Fatal(err)
	}
	reqs := rec.requests()
	if last := reqs[len(reqs)-1]; last.method != http.MethodPost || last.path != "/Library/Refresh" {
		t.Errorf("fallback = %s %s", last.method, last.path)
	}

	err := mustNew(t, Config{Kind: Jellyfin, BaseURL: rec.URL, Token: "ffffffffffffffffffffffffffffffff"}).Test(ctx)
	if !errors.Is(err, outbound.ErrAuth) || strings.Contains(err.Error(), "ffffffff") {
		t.Errorf("bad key: %v", err)
	}
}

func TestKodi(t *testing.T) {
	var (
		mu    sync.Mutex
		calls []map[string]any
	)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		user, pass, ok := r.BasicAuth()
		if !ok || user != "kodi" || pass != kodiPassword {
			w.WriteHeader(http.StatusUnauthorized)
			return
		}
		if r.URL.Path != "/jsonrpc" || r.Method != http.MethodPost || r.Header.Get("Content-Type") != "application/json" {
			w.WriteHeader(http.StatusNotFound)
			return
		}
		var req map[string]any
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			t.Error(err)
		}
		mu.Lock()
		calls = append(calls, req)
		mu.Unlock()
		id := req["id"]
		var res any = "OK"
		switch req["method"] {
		case "JSONRPC.Ping":
			res = "pong"
		case "VideoLibrary.Clean":
			if p, _ := req["params"].(map[string]any); p["directory"] == "smb://nas/films/Locked (2020)/" {
				_ = json.NewEncoder(w).Encode(map[string]any{"jsonrpc": "2.0", "id": id, "error": map[string]any{"code": -32100, "message": "Failed to execute method."}})
				return
			}
		}
		_ = json.NewEncoder(w).Encode(map[string]any{"jsonrpc": "2.0", "id": id, "result": res})
	}))
	defer srv.Close()
	last := func() map[string]any {
		mu.Lock()
		defer mu.Unlock()
		return calls[len(calls)-1]
	}
	count := func() int {
		mu.Lock()
		defer mu.Unlock()
		return len(calls)
	}
	ctx := context.Background()
	cfg := Config{
		Kind: Kodi, BaseURL: srv.URL, Username: "kodi", Password: kodiPassword,
		Mappings: []ServerPathMapping{{From: "/data/movies", To: "smb://nas/films"}},
	}
	k := mustNew(t, cfg)
	if err := k.Test(ctx); err != nil {
		t.Fatalf("Test: %v", err)
	}
	if m := last(); m["method"] != "JSONRPC.Ping" || m["jsonrpc"] != "2.0" {
		t.Errorf("ping = %v", m)
	}
	if err := k.Refresh(ctx, RefreshRequest{Path: "/data/movies/Heat (1995)", Reason: ReasonAdd}); err != nil {
		t.Fatal(err)
	}
	m := last()
	p, _ := m["params"].(map[string]any)
	if m["method"] != "VideoLibrary.Scan" || p["directory"] != "smb://nas/films/Heat (1995)/" || p["showdialogs"] != false {
		t.Errorf("scan = %v", m)
	}
	// Deletion without clean: nothing is sent.
	n := count()
	if err := k.Refresh(ctx, RefreshRequest{Path: "/data/movies/Heat (1995)", Reason: ReasonDelete}); err != nil || count() != n {
		t.Errorf("delete without clean: %v, calls %d -> %d", err, n, count())
	}
	cfg.CleanOnDelete = true
	k = mustNew(t, cfg)
	if err := k.Refresh(ctx, RefreshRequest{Path: "/data/movies/Heat (1995)", Reason: ReasonDelete}); err != nil {
		t.Fatal(err)
	}
	m = last()
	p, _ = m["params"].(map[string]any)
	if m["method"] != "VideoLibrary.Clean" || p["directory"] != "smb://nas/films/Heat (1995)/" || p["content"] != "movies" {
		t.Errorf("clean = %v", m)
	}
	err := k.Refresh(ctx, RefreshRequest{Path: "/data/movies/Locked (2020)", Reason: ReasonDelete})
	if err == nil || !strings.Contains(err.Error(), "Failed to execute method") || outbound.Temporary(err) {
		t.Errorf("JSON-RPC error: %v", err)
	}

	cfg.Password = "not-the-password"
	err = mustNew(t, cfg).Test(ctx)
	if !errors.Is(err, outbound.ErrAuth) || strings.Contains(err.Error(), "not-the-password") {
		t.Errorf("bad password: %v", err)
	}
}

func TestNewValidates(t *testing.T) {
	for _, cfg := range []Config{
		{Kind: "emby", BaseURL: "http://x"},
		{Kind: Plex, BaseURL: "ftp://x"},
		{Kind: Plex, BaseURL: "http://user:pw@x"},
		{Kind: Kodi, BaseURL: ""},
	} {
		if _, err := New(cfg, Options{}); err == nil {
			t.Errorf("New(%+v) accepted", cfg)
		}
	}
}

func TestMapPath(t *testing.T) {
	m := newMapper([]ServerPathMapping{
		{From: "/data/movies", To: "/media/movies"},
		{From: "/data/movies/uhd", To: `D:\UHD`},
		{From: "/srv/films/", To: "smb://nas/films/"},
	})
	for _, tc := range []struct{ in, want string }{
		{"/data/movies/Heat (1995)", "/media/movies/Heat (1995)"},
		{"/data/movies/uhd/Dune (2021)", `D:\UHD\Dune (2021)`},
		{"/srv/films/Alien (1979)", "smb://nas/films/Alien (1979)"},
		{"/data/moviesX/Heat", "/data/moviesX/Heat"}, // whole components only
		{"/other/Heat", "/other/Heat"},
		{"/data/movies/../etc", "/data/movies/../etc"}, // never climbs out
	} {
		if got := MapPath(m, tc.in); got != tc.want {
			t.Errorf("MapPath(%q) = %q, want %q", tc.in, got, tc.want)
		}
	}
}

func TestPathHelpers(t *testing.T) {
	for _, tc := range []struct {
		child, parent string
		want          bool
	}{
		{"/media/movies/X", "/media/movies", true},
		{"/media/movies", "/media/movies/", true},
		{"/media/moviesX", "/media/movies", false},
		{`D:\Media\Movies\X`, `d:\media\movies`, true},
		{"/Media/Movies/X", "/media/movies", false},
		{"smb://nas/films/X", "smb://nas/films", true},
		{"/x", "", false},
	} {
		if got := under(tc.child, tc.parent); got != tc.want {
			t.Errorf("under(%q, %q) = %v", tc.child, tc.parent, got)
		}
	}
	for in, want := range map[string]string{
		"/media/movies/X": "/media/movies", `D:\M\X`: `D:\M`, "smb://nas/f/X": "smb://nas/f", "/x": "", `C:\X`: "", "smb://nas": "",
	} {
		if got := parentOf(in); got != want {
			t.Errorf("parentOf(%q) = %q, want %q", in, got, want)
		}
	}
	for in, want := range map[string]string{"/a/b": "/a/b/", `D:\a`: `D:\a\`, "smb://n/a/": "smb://n/a/"} {
		if got := withTrailingSep(in); got != want {
			t.Errorf("withTrailingSep(%q) = %q", in, got)
		}
	}
}

func TestMerge(t *testing.T) {
	for _, tc := range []struct{ a, b, want Reason }{
		{"", ReasonRename, ReasonRename},
		{ReasonAdd, ReasonRename, ReasonAdd},
		{ReasonRename, ReasonAdd, ReasonAdd},
		{ReasonDelete, ReasonAdd, ReasonAdd},
		{ReasonDelete, ReasonUpgrade, ReasonUpgrade},
		{ReasonDelete, ReasonRename, ReasonUpgrade},
		{ReasonUpgrade, ReasonRename, ReasonUpgrade},
		{ReasonDelete, ReasonDelete, ReasonDelete},
	} {
		if got := merge(tc.a, tc.b); got != tc.want {
			t.Errorf("merge(%s, %s) = %s, want %s", tc.a, tc.b, got, tc.want)
		}
	}
}
