package qbittorrent

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/fs0ciety7000/gonema/internal/decision"
	"github.com/fs0ciety7000/gonema/internal/download"
)

const (
	testUser = "admin"
	testPass = "qb-Secret-Pass"
)

// sampleTorrent is a minimal single-file .torrent.
var sampleTorrent = []byte("d8:announce23:http://tracker/announce4:infod6:lengthi1000e4:name9:movie.mkv12:piece lengthi16384e6:pieces20:aaaaaaaaaaaaaaaaaaaaee")

type request struct {
	path string
	form url.Values
	file []byte
}

// fakeQB emulates the WebUI API v2 under /qb.
type fakeQB struct {
	t          *testing.T
	api        string // webapiVersion answer
	newLogin   bool   // WebAPI >= 2.14: 401 on bad credentials, JSON add answer
	logins     atomic.Int32
	mu         sync.Mutex
	sid        string
	reqs       []request
	addAnswer  string
	addStatus  int
	loginDelay time.Duration
}

func newFake(t *testing.T, api string) (*fakeQB, *httptest.Server) {
	t.Helper()
	f := &fakeQB{t: t, api: api, addAnswer: "Ok."}
	srv := httptest.NewServer(f)
	t.Cleanup(srv.Close)
	return f, srv
}

func (f *fakeQB) expire() {
	f.mu.Lock()
	f.sid = "expired"
	f.mu.Unlock()
}

func (f *fakeQB) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	path, ok := strings.CutPrefix(r.URL.Path, "/qb/api/v2/")
	if !ok {
		http.NotFound(w, r)
		return
	}
	if ref := r.Header.Get("Referer"); !strings.HasPrefix(ref, "http://127.0.0.1:") {
		f.t.Errorf("%s: missing Referer (%q)", path, ref)
	}
	if path == "auth/login" {
		f.serveLogin(w, r)
		return
	}
	ck, err := r.Cookie("SID")
	f.mu.Lock()
	valid := err == nil && ck.Value == f.sid
	f.mu.Unlock()
	if !valid {
		w.WriteHeader(http.StatusForbidden)
		_, _ = io.WriteString(w, "Forbidden")
		return
	}
	rec := request{path: path, form: r.URL.Query()}
	if r.Method == http.MethodPost {
		if strings.HasPrefix(r.Header.Get("Content-Type"), "multipart/") {
			if err := r.ParseMultipartForm(1 << 20); err != nil {
				f.t.Errorf("multipart: %v", err)
			}
			rec.form = url.Values(r.MultipartForm.Value)
			if fh := r.MultipartForm.File["torrents"]; len(fh) == 1 {
				rd, _ := fh[0].Open()
				rec.file, _ = io.ReadAll(rd)
				_ = rd.Close()
			}
		} else {
			_ = r.ParseForm()
			rec.form = r.PostForm
		}
	}
	f.mu.Lock()
	f.reqs = append(f.reqs, rec)
	f.mu.Unlock()
	switch path {
	case "app/webapiVersion":
		_, _ = io.WriteString(w, f.api)
	case "torrents/info":
		f.serveInfo(w, r.URL.Query())
	case "torrents/categories":
		f.fixture(w, "categories.json")
	case "torrents/add":
		if f.addStatus != 0 {
			w.WriteHeader(f.addStatus)
		}
		_, _ = io.WriteString(w, f.addAnswer)
	case "torrents/delete", "torrents/setForceStart":
	case "torrents/stop", "torrents/start":
		if !strings.HasPrefix(f.api, "2.11") {
			http.NotFound(w, r)
		}
	case "torrents/pause", "torrents/resume":
		if strings.HasPrefix(f.api, "2.11") {
			http.NotFound(w, r)
		}
	default:
		http.NotFound(w, r)
	}
}

func (f *fakeQB) serveLogin(w http.ResponseWriter, r *http.Request) {
	time.Sleep(f.loginDelay)
	_ = r.ParseForm()
	if r.PostForm.Get("username") != testUser || r.PostForm.Get("password") != testPass {
		if f.newLogin {
			w.WriteHeader(http.StatusUnauthorized)
		}
		_, _ = io.WriteString(w, "Fails.")
		return
	}
	n := f.logins.Add(1)
	sid := fmt.Sprintf("sid-%d-hBc7TxF76ERhvIw0jQQ4LZ7Z1jQUV0tQ", n)
	f.mu.Lock()
	f.sid = sid
	f.mu.Unlock()
	http.SetCookie(w, &http.Cookie{Name: "SID", Value: sid, Path: "/"})
	_, _ = io.WriteString(w, "Ok.")
}

func (f *fakeQB) serveInfo(w http.ResponseWriter, q url.Values) {
	b, err := os.ReadFile("testdata/torrents.json")
	if err != nil {
		f.t.Errorf("fixture: %v", err)
		return
	}
	if h := q.Get("hashes"); h != "" {
		if strings.Contains(string(b), `"hash": "`+strings.ToLower(h)+`"`) {
			_, _ = fmt.Fprintf(w, `[{"hash": %q, "state": "downloading"}]`, strings.ToLower(h))
		} else {
			_, _ = io.WriteString(w, `[]`)
		}
		return
	}
	if c := q.Get("category"); c != "" && c != "movies" {
		_, _ = io.WriteString(w, `[]`)
		return
	}
	_, _ = w.Write(b)
}

func (f *fakeQB) fixture(w io.Writer, name string) {
	b, err := os.ReadFile("testdata/" + name)
	if err != nil {
		f.t.Errorf("fixture: %v", err)
	}
	_, _ = w.Write(b)
}

func (f *fakeQB) last(path string) *request {
	f.mu.Lock()
	defer f.mu.Unlock()
	for i := len(f.reqs) - 1; i >= 0; i-- {
		if f.reqs[i].path == path {
			return &f.reqs[i]
		}
	}
	return nil
}

func clientFor(t *testing.T, srv *httptest.Server, mod func(*download.Config)) *Client {
	t.Helper()
	u, _ := url.Parse(srv.URL)
	port, _ := strconv.Atoi(u.Port())
	cfg := &download.Config{Host: u.Hostname(), Port: port, URLBase: "qb", Username: testUser, Password: testPass, Category: "movies", HTTPClient: srv.Client()}
	if mod != nil {
		mod(cfg)
	}
	c, err := New(cfg)
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	return c
}

func TestTest(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name     string
		newLogin bool
		mod      func(*download.Config)
		want     error
	}{
		{"ok", false, nil, nil},
		{"wrong password (Fails.)", false, func(c *download.Config) { c.Password = "nope-pass" }, download.ErrAuth},
		{"wrong password (401)", true, func(c *download.Config) { c.Password = "nope-pass" }, download.ErrAuth},
		{"url base", false, func(c *download.Config) { c.URLBase = "" }, download.ErrURLBase},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			f, srv := newFake(t, "2.11.2")
			f.newLogin = tt.newLogin
			err := clientFor(t, srv, tt.mod).Test(context.Background())
			if !errors.Is(err, tt.want) {
				t.Fatalf("Test = %v, want %v", err, tt.want)
			}
			if err != nil && (strings.Contains(err.Error(), testPass) || strings.Contains(err.Error(), "nope-pass")) {
				t.Fatalf("error leaks the password: %v", err)
			}
		})
	}
}

func TestBanned(t *testing.T) {
	t.Parallel()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusForbidden)
	}))
	t.Cleanup(srv.Close)
	err := clientFor(t, srv, nil).Test(context.Background())
	if !errors.Is(err, download.ErrAuth) || !strings.Contains(err.Error(), "banned") {
		t.Fatalf("err = %v", err)
	}
}

func TestReloginOn403(t *testing.T) {
	t.Parallel()
	f, srv := newFake(t, "2.11.2")
	c := clientFor(t, srv, nil)
	ctx := context.Background()
	if _, err := c.Items(ctx); err != nil {
		t.Fatal(err)
	}
	f.expire()
	items, err := c.Items(ctx)
	if err != nil || len(items) == 0 {
		t.Fatalf("Items after expiry = %d, %v", len(items), err)
	}
	if n := f.logins.Load(); n != 2 {
		t.Fatalf("logins = %d, want 2", n)
	}
}

func TestConcurrentReloginIsShared(t *testing.T) {
	t.Parallel()
	f, srv := newFake(t, "2.11.2")
	f.loginDelay = 20 * time.Millisecond
	c := clientFor(t, srv, nil)
	if _, err := c.Items(context.Background()); err != nil {
		t.Fatal(err)
	}
	f.expire()
	var wg sync.WaitGroup
	errs := make(chan error, 8)
	for range 8 {
		wg.Go(func() {
			_, err := c.Items(context.Background())
			errs <- err
		})
	}
	wg.Wait()
	close(errs)
	for err := range errs {
		if err != nil {
			t.Fatal(err)
		}
	}
	if n := f.logins.Load(); n != 2 {
		t.Fatalf("logins = %d, want 2 (one initial, one shared re-login)", n)
	}
}

func TestSessionCookieRedacted(t *testing.T) {
	t.Parallel()
	_, srv := newFake(t, "2.11.2")
	c := clientFor(t, srv, nil)
	if err := c.Test(context.Background()); err != nil {
		t.Fatal(err)
	}
	if got := c.r.Redact("cookie sid-1-hBc7TxF76ERhvIw0jQQ4LZ7Z1jQUV0tQ"); strings.Contains(got, "hBc7") {
		t.Fatalf("session id not redacted: %s", got)
	}
}

func TestAdd(t *testing.T) {
	t.Parallel()
	wantHash, err := download.InfoHash(sampleTorrent)
	if err != nil {
		t.Fatal(err)
	}
	tests := []struct {
		name      string
		req       download.AddRequest
		addAnswer string
		addStatus int
		wantRef   string
		wantErr   error
		check     func(t *testing.T, r *request)
	}{
		{
			name: "bytes", wantRef: wantHash, addAnswer: "Ok.",
			req: download.AddRequest{Protocol: decision.Torrent, Data: sampleTorrent, Filename: "m.torrent", Paused: true, SeedRatio: 1.5, SeedTime: 90 * time.Minute},
			check: func(t *testing.T, r *request) {
				t.Helper()
				if !bytes.Equal(r.file, sampleTorrent) || r.form.Get("category") != "movies" || r.form.Get("paused") != "true" ||
					r.form.Get("stopped") != "true" || r.form.Get("ratioLimit") != "1.50" || r.form.Get("seedingTimeLimit") != "90" {
					t.Fatalf("add form = %v", r.form)
				}
			},
		},
		{
			name: "magnet", wantRef: "c12fe1c06bba254a9dc9f519b335aa7c1367a88a", addAnswer: "Ok.",
			req: download.AddRequest{Protocol: decision.Torrent, URL: "magnet:?xt=urn:btih:C12FE1C06BBA254A9DC9F519B335AA7C1367A88A&dn=x", Category: "tv"},
			check: func(t *testing.T, r *request) {
				t.Helper()
				if r.form.Get("urls") == "" || r.form.Get("category") != "tv" || r.form.Get("paused") != "false" {
					t.Fatalf("add form = %v", r.form)
				}
			},
		},
		{
			name: "http link with ids (2.14+)", wantRef: "0123456789abcdef0123456789abcdef01234567",
			addAnswer: `{"success_count":1,"failure_count":0,"pending_count":0,"added_torrent_ids":["0123456789ABCDEF0123456789ABCDEF01234567"]}`,
			req:       download.AddRequest{Protocol: decision.Torrent, URL: "https://indexer.example/t/1.torrent"},
		},
		{
			name: "http link without ids", addAnswer: "Ok.", wantErr: download.ErrUnknownRef,
			req: download.AddRequest{Protocol: decision.Torrent, URL: "https://indexer.example/t/1.torrent"},
		},
		{
			name: "refused", addAnswer: "Fails.", wantErr: download.ErrRejected,
			req: download.AddRequest{Protocol: decision.Torrent, Data: sampleTorrent, Filename: "m.torrent"},
		},
		{
			name: "invalid file", addAnswer: "", addStatus: http.StatusUnsupportedMediaType, wantErr: download.ErrRejected,
			req: download.AddRequest{Protocol: decision.Torrent, Data: sampleTorrent, Filename: "m.torrent"},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			f, srv := newFake(t, "2.11.2")
			f.addAnswer, f.addStatus = tt.addAnswer, tt.addStatus
			ref, err := clientFor(t, srv, nil).Add(context.Background(), tt.req)
			if !errors.Is(err, tt.wantErr) || ref != tt.wantRef {
				t.Fatalf("Add = %q, %v; want %q, %v", ref, err, tt.wantRef, tt.wantErr)
			}
			if tt.check != nil {
				tt.check(t, f.last("torrents/add"))
			}
		})
	}
}

func TestAddForce(t *testing.T) {
	t.Parallel()
	f, srv := newFake(t, "2.11.2")
	ref, err := clientFor(t, srv, nil).Add(context.Background(), download.AddRequest{
		Protocol: decision.Torrent, Data: sampleTorrent, Filename: "m.torrent", Priority: download.PriorityForce,
	})
	if err != nil {
		t.Fatal(err)
	}
	r := f.last("torrents/setForceStart")
	if r == nil || r.form.Get("hashes") != ref || r.form.Get("value") != "true" {
		t.Fatalf("setForceStart = %+v", r)
	}
}

func TestItems(t *testing.T) {
	t.Parallel()
	f, srv := newFake(t, "2.11.2")
	items, err := clientFor(t, srv, nil).Items(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if r := f.last("torrents/info"); r.form.Get("category") != "movies" {
		t.Fatalf("category filter missing: %v", r.form)
	}
	if len(items) != 22 {
		t.Fatalf("%d items", len(items))
	}
	up := items[2] // uploading
	if up.Status != download.StatusCompleted || up.ETA != 0 || up.Ratio != 1.5 || up.OutputPath != "/downloads/movies/Movie.2" || up.Progress != 1 {
		t.Errorf("uploading = %+v", up)
	}
	dl := items[10] // downloading
	if dl.Status != download.StatusDownloading || dl.ETA != 2*time.Minute || dl.Speed != 2048 || dl.Downloaded != 500 || dl.Size != 1000 {
		t.Errorf("downloading = %+v", dl)
	}
	if items[0].Status != download.StatusFailed || items[0].Message == "" {
		t.Errorf("error = %+v", items[0])
	}
}

func TestMapState(t *testing.T) {
	t.Parallel()
	tests := map[string]download.Status{
		"error": download.StatusFailed, "missingFiles": download.StatusFailed,
		"uploading": download.StatusCompleted, "stalledUP": download.StatusCompleted, "queuedUP": download.StatusCompleted,
		"forcedUP": download.StatusCompleted, "checkingUP": download.StatusCompleted,
		"pausedUP": download.StatusCompleted, "stoppedUP": download.StatusCompleted,
		"downloading": download.StatusDownloading, "forcedDL": download.StatusDownloading, "metaDL": download.StatusDownloading,
		"forcedMetaDL": download.StatusDownloading, "allocating": download.StatusDownloading,
		"checkingDL": download.StatusDownloading, "moving": download.StatusDownloading,
		"queuedDL": download.StatusQueued, "checkingResumeData": download.StatusQueued,
		"pausedDL": download.StatusPaused, "stoppedDL": download.StatusPaused,
		"stalledDL": download.StatusWarning, "unknown": download.StatusWarning, "": download.StatusWarning,
	}
	for raw, want := range tests {
		if got, _ := mapState(raw); got != want {
			t.Errorf("mapState(%q) = %s, want %s", raw, got, want)
		}
	}
}

func TestRemove(t *testing.T) {
	t.Parallel()
	const ref = "0000000000000000000000000000000000000003"
	for _, del := range []bool{false, true} {
		t.Run(strconv.FormatBool(del), func(t *testing.T) {
			t.Parallel()
			f, srv := newFake(t, "2.11.2")
			if err := clientFor(t, srv, nil).Remove(context.Background(), ref, del); err != nil {
				t.Fatal(err)
			}
			r := f.last("torrents/delete")
			if r.form.Get("hashes") != ref || r.form.Get("deleteFiles") != strconv.FormatBool(del) {
				t.Fatalf("delete form = %v", r.form)
			}
		})
	}
	t.Run("unknown", func(t *testing.T) {
		t.Parallel()
		f, srv := newFake(t, "2.11.2")
		err := clientFor(t, srv, nil).Remove(context.Background(), strings.Repeat("f", 40), true)
		if !errors.Is(err, download.ErrNotFound) || f.last("torrents/delete") != nil {
			t.Fatalf("err = %v", err)
		}
	})
	t.Run("invalid", func(t *testing.T) {
		t.Parallel()
		_, srv := newFake(t, "2.11.2")
		if err := clientFor(t, srv, nil).Remove(context.Background(), "all", true); !errors.Is(err, download.ErrInvalidRequest) {
			t.Fatalf("err = %v", err)
		}
	})
}

func TestPauseResumeByVersion(t *testing.T) {
	t.Parallel()
	const ref = "000000000000000000000000000000000000000b"
	tests := []struct{ api, pause, resume string }{
		{"2.11.2", "torrents/stop", "torrents/start"},
		{"2.9.3", "torrents/pause", "torrents/resume"},
	}
	for _, tt := range tests {
		t.Run(tt.api, func(t *testing.T) {
			t.Parallel()
			f, srv := newFake(t, tt.api)
			c := clientFor(t, srv, nil)
			if err := c.Pause(context.Background(), ref); err != nil {
				t.Fatal(err)
			}
			if r := f.last(tt.pause); r == nil || r.form.Get("hashes") != ref {
				t.Fatalf("%s not called", tt.pause)
			}
			if err := c.Resume(context.Background(), ref); err != nil {
				t.Fatal(err)
			}
			if r := f.last(tt.resume); r == nil {
				t.Fatalf("%s not called", tt.resume)
			}
		})
	}
}

func TestCategories(t *testing.T) {
	t.Parallel()
	_, srv := newFake(t, "2.11.2")
	cats, err := clientFor(t, srv, nil).Categories(context.Background())
	if err != nil || strings.Join(cats, ",") != "movies,tv" {
		t.Fatalf("Categories = %v, %v", cats, err)
	}
}

func FuzzMapState(f *testing.F) {
	f.Add("stalledDL")
	f.Fuzz(func(t *testing.T, raw string) {
		switch s, _ := mapState(raw); s {
		case download.StatusQueued, download.StatusDownloading, download.StatusPaused,
			download.StatusCompleted, download.StatusFailed, download.StatusWarning:
		default:
			t.Fatalf("%q -> %q", raw, s)
		}
	})
}
