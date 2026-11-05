package transmission

import (
	"context"
	"encoding/base64"
	"encoding/json"
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
	"testing"
	"time"

	"github.com/fs0ciety7000/gonema/internal/decision"
	"github.com/fs0ciety7000/gonema/internal/download"
)

const (
	testUser = "tr"
	testPass = "tr-Secret-9"
)

type rpcCall struct {
	Method    string         `json:"method"`
	Arguments map[string]any `json:"arguments"`
	Tag       int            `json:"tag"`
}

// fakeTR emulates /transmission/rpc with the session-id handshake.
type fakeTR struct {
	t         *testing.T
	mu        sync.Mutex
	sessionID string
	handshake int // number of 409 answers
	calls     []rpcCall
	duplicate bool
	forbidden bool
}

func newFake(t *testing.T) (*fakeTR, *httptest.Server) {
	t.Helper()
	f := &fakeTR{t: t, sessionID: "sess-1"}
	srv := httptest.NewServer(f)
	t.Cleanup(srv.Close)
	return f, srv
}

func (f *fakeTR) handshakes() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.handshake
}

func (f *fakeTR) rotate(id string) {
	f.mu.Lock()
	f.sessionID = id
	f.mu.Unlock()
}

func (f *fakeTR) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	if r.URL.Path != "/transmission/rpc" {
		http.NotFound(w, r)
		return
	}
	if f.forbidden {
		w.WriteHeader(http.StatusForbidden)
		return
	}
	if u, p, ok := r.BasicAuth(); !ok || u != testUser || p != testPass {
		w.WriteHeader(http.StatusUnauthorized)
		return
	}
	f.mu.Lock()
	sid := f.sessionID
	f.mu.Unlock()
	if r.Header.Get("X-Transmission-Session-Id") != sid {
		f.mu.Lock()
		f.handshake++
		f.mu.Unlock()
		w.Header().Set("X-Transmission-Session-Id", sid)
		w.WriteHeader(http.StatusConflict)
		_, _ = io.WriteString(w, "<h1>409: Conflict</h1>")
		return
	}
	var c rpcCall
	if err := json.NewDecoder(r.Body).Decode(&c); err != nil {
		f.t.Errorf("decode: %v", err)
		return
	}
	f.mu.Lock()
	f.calls = append(f.calls, c)
	f.mu.Unlock()
	switch c.Method {
	case "session-get":
		_, _ = fmt.Fprintf(w, `{"arguments":{"version":"4.0.6 (38c164933e)","rpc-version":17},"result":"success","tag":%d}`, c.Tag)
	case "torrent-add":
		if fn, _ := c.Arguments["filename"].(string); fn == "https://bad.example/x.torrent" {
			_, _ = io.WriteString(w, `{"arguments":{},"result":"invalid or corrupt torrent file"}`)
			return
		}
		key := "torrent-added"
		if f.duplicate {
			key = "torrent-duplicate"
		}
		_, _ = fmt.Fprintf(w, `{"arguments":{%q:{"hashString":"ABCDEF0123456789ABCDEF0123456789ABCDEF01","id":7,"name":"x"}},"result":"success"}`, key)
	case "torrent-get":
		b, err := os.ReadFile("testdata/torrent_get.json")
		if err != nil {
			f.t.Errorf("fixture: %v", err)
		}
		if ids, ok := c.Arguments["ids"].([]any); ok {
			id, _ := ids[0].(string)
			if strings.Contains(string(b), `"hashString": "`+id+`"`) {
				_, _ = fmt.Fprintf(w, `{"arguments":{"torrents":[{"hashString":%q}]},"result":"success"}`, id)
			} else {
				_, _ = io.WriteString(w, `{"arguments":{"torrents":[]},"result":"success"}`)
			}
			return
		}
		_, _ = w.Write(b)
	case "torrent-remove", "torrent-start", "torrent-stop", "torrent-set", "torrent-start-now":
		_, _ = io.WriteString(w, `{"arguments":{},"result":"success"}`)
	default:
		_, _ = io.WriteString(w, `{"arguments":{},"result":"method name not recognized"}`)
	}
}

func (f *fakeTR) last(method string) *rpcCall {
	f.mu.Lock()
	defer f.mu.Unlock()
	for i := len(f.calls) - 1; i >= 0; i-- {
		if f.calls[i].Method == method {
			return &f.calls[i]
		}
	}
	return nil
}

func clientFor(t *testing.T, srv *httptest.Server, mod func(*download.Config)) *Client {
	t.Helper()
	u, _ := url.Parse(srv.URL)
	port, _ := strconv.Atoi(u.Port())
	cfg := &download.Config{Host: u.Hostname(), Port: port, Username: testUser, Password: testPass, Category: "movies", HTTPClient: srv.Client()}
	if mod != nil {
		mod(cfg)
	}
	c, err := New(cfg)
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	return c
}

func TestSessionIDHandshake(t *testing.T) {
	t.Parallel()
	f, srv := newFake(t)
	c := clientFor(t, srv, nil)
	ctx := context.Background()
	if err := c.Test(ctx); err != nil {
		t.Fatal(err)
	}
	if n := f.handshakes(); n != 1 {
		t.Fatalf("handshakes = %d, want 1", n)
	}
	if err := c.Test(ctx); err != nil {
		t.Fatal(err)
	}
	if f.handshakes() != 1 {
		t.Fatal("the session id was not reused")
	}
	f.rotate("sess-2") // Transmission restarted: new id
	if err := c.Test(ctx); err != nil {
		t.Fatal(err)
	}
	if n := f.handshakes(); n != 2 {
		t.Fatalf("handshakes = %d, want 2", n)
	}
}

func TestTestErrors(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name      string
		forbidden bool
		mod       func(*download.Config)
		want      error
	}{
		{"wrong password", false, func(c *download.Config) { c.Password = "bad-pw-x" }, download.ErrAuth},
		{"whitelist", true, nil, download.ErrAPIDisabled},
		{"url base", false, func(c *download.Config) { c.URLBase = "/other/" }, download.ErrURLBase},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			f, srv := newFake(t)
			f.forbidden = tt.forbidden
			err := clientFor(t, srv, tt.mod).Test(context.Background())
			if !errors.Is(err, tt.want) {
				t.Fatalf("Test = %v, want %v", err, tt.want)
			}
			if strings.Contains(err.Error(), testPass) || strings.Contains(err.Error(), "bad-pw-x") {
				t.Fatalf("error leaks the password: %v", err)
			}
		})
	}
}

func TestCustomURLBase(t *testing.T) {
	t.Parallel()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/tr/rpc" {
			http.NotFound(w, r)
			return
		}
		_, _ = io.WriteString(w, `{"arguments":{"rpc-version":17},"result":"success"}`)
	}))
	t.Cleanup(srv.Close)
	if err := clientFor(t, srv, func(c *download.Config) { c.URLBase = "/tr/" }).Test(context.Background()); err != nil {
		t.Fatal(err)
	}
}

func TestAdd(t *testing.T) {
	t.Parallel()
	data := []byte("d4:infod4:name1:xee")
	tests := []struct {
		name    string
		mod     func(*download.Config)
		req     download.AddRequest
		dup     bool
		wantErr error
		check   func(t *testing.T, f *fakeTR)
	}{
		{
			name: "magnet with dir", mod: func(c *download.Config) { c.Directory = "/downloads/complete" },
			req: download.AddRequest{Protocol: decision.Torrent, URL: "magnet:?xt=urn:btih:abc", Paused: true, Priority: download.PriorityLow},
			check: func(t *testing.T, f *fakeTR) {
				t.Helper()
				a := f.last("torrent-add").Arguments
				if a["filename"] != "magnet:?xt=urn:btih:abc" || a["paused"] != true || a["download-dir"] != "/downloads/complete/movies" ||
					a["bandwidthPriority"] != -1.0 || fmt.Sprint(a["labels"]) != "[movies]" {
					t.Fatalf("args = %v", a)
				}
				if f.last("torrent-set") != nil {
					t.Fatal("torrent-set without limits")
				}
			},
		},
		{
			name: "metainfo with limits",
			req:  download.AddRequest{Protocol: decision.Torrent, Data: data, Filename: "x.torrent", SeedRatio: 2, SeedTime: 3 * time.Hour, Priority: download.PriorityForce},
			check: func(t *testing.T, f *fakeTR) {
				t.Helper()
				a := f.last("torrent-add").Arguments
				if a["metainfo"] != base64.StdEncoding.EncodeToString(data) || a["download-dir"] != nil || a["bandwidthPriority"] != 1.0 {
					t.Fatalf("args = %v", a)
				}
				s := f.last("torrent-set").Arguments
				if s["seedRatioLimit"] != 2.0 || s["seedRatioMode"] != 1.0 || s["seedIdleLimit"] != 180.0 || s["seedIdleMode"] != 1.0 {
					t.Fatalf("torrent-set = %v", s)
				}
				if f.last("torrent-start-now") == nil {
					t.Fatal("force did not start the torrent")
				}
			},
		},
		{name: "duplicate", dup: true, req: download.AddRequest{Protocol: decision.Torrent, URL: "https://ok.example/x.torrent"}},
		{name: "corrupt", wantErr: download.ErrRejected, req: download.AddRequest{Protocol: decision.Torrent, URL: "https://bad.example/x.torrent"}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			f, srv := newFake(t)
			f.duplicate = tt.dup
			ref, err := clientFor(t, srv, tt.mod).Add(context.Background(), tt.req)
			if !errors.Is(err, tt.wantErr) {
				t.Fatalf("Add = %q, %v", ref, err)
			}
			if tt.wantErr == nil && ref != "abcdef0123456789abcdef0123456789abcdef01" {
				t.Fatalf("ref = %q", ref)
			}
			if tt.check != nil {
				tt.check(t, f)
			}
		})
	}
}

func TestItems(t *testing.T) {
	t.Parallel()
	_, srv := newFake(t)
	items, err := clientFor(t, srv, nil).Items(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if len(items) != 11 {
		t.Fatalf("%d items, want 11 (tv and unlabelled excluded)", len(items))
	}
	want := []download.Status{
		download.StatusPaused, download.StatusCompleted, download.StatusQueued, download.StatusDownloading,
		download.StatusQueued, download.StatusDownloading, download.StatusCompleted, download.StatusCompleted,
		download.StatusWarning, download.StatusCompleted, download.StatusFailed,
	}
	for i, it := range items {
		if it.Status != want[i] {
			t.Errorf("%d (%s) = %s, want %s", i, it.RawState, it.Status, want[i])
		}
	}
	dl := items[5]
	if dl.Progress != 0.25 || dl.Downloaded != 250 || dl.Speed != 3000 || dl.ETA != time.Minute ||
		dl.OutputPath != "/downloads/complete/movies/Movie.5.1080p-GRP" || dl.Category != "movies" || dl.Ratio != 0 {
		t.Errorf("downloading = %+v", dl)
	}
	if e := items[10]; e.Message != "No data found! Ensure your drives are connected" {
		t.Errorf("local error = %+v", e)
	}
	if w := items[8]; !strings.Contains(w.Message, "404") {
		t.Errorf("tracker error = %+v", w)
	}

	withDir, err := clientFor(t, srv, func(c *download.Config) { c.Directory = "/downloads/complete" }).Items(context.Background())
	if err != nil || len(withDir) != 12 {
		t.Fatalf("with directory: %d items, %v", len(withDir), err)
	}
	all, err := clientFor(t, srv, func(c *download.Config) { c.Category = "" }).Items(context.Background())
	if err != nil || len(all) != 13 || all[11].Category != "tv" {
		t.Fatalf("without category: %d items, %v", len(all), err)
	}
}

func TestMapStatus(t *testing.T) {
	t.Parallel()
	tests := []struct {
		status, errType int
		done            bool
		want            download.Status
	}{
		{0, 0, false, download.StatusPaused},
		{0, 0, true, download.StatusCompleted},
		{1, 0, false, download.StatusQueued},
		{2, 0, false, download.StatusDownloading},
		{3, 0, false, download.StatusQueued},
		{4, 0, false, download.StatusDownloading},
		{5, 0, true, download.StatusCompleted},
		{6, 0, true, download.StatusCompleted},
		{4, 1, false, download.StatusWarning},
		{4, 2, false, download.StatusWarning},
		{6, 2, true, download.StatusCompleted},
		{6, 3, true, download.StatusFailed},
		{4, 3, false, download.StatusFailed},
		{9, 0, false, download.StatusWarning},
	}
	for _, tt := range tests {
		if got, _ := mapStatus(tt.status, tt.errType, "", tt.done); got != tt.want {
			t.Errorf("mapStatus(%d, %d, %v) = %s, want %s", tt.status, tt.errType, tt.done, got, tt.want)
		}
	}
}

func TestRemove(t *testing.T) {
	t.Parallel()
	const ref = "00000000000000000000000000000000000000a0"
	for _, del := range []bool{false, true} {
		t.Run(strconv.FormatBool(del), func(t *testing.T) {
			t.Parallel()
			f, srv := newFake(t)
			if err := clientFor(t, srv, nil).Remove(context.Background(), ref, del); err != nil {
				t.Fatal(err)
			}
			a := f.last("torrent-remove").Arguments
			if a["delete-local-data"] != del || fmt.Sprint(a["ids"]) != "["+ref+"]" {
				t.Fatalf("remove args = %v", a)
			}
		})
	}
	t.Run("unknown", func(t *testing.T) {
		t.Parallel()
		f, srv := newFake(t)
		if err := clientFor(t, srv, nil).Remove(context.Background(), strings.Repeat("9", 40), true); !errors.Is(err, download.ErrNotFound) {
			t.Fatalf("err = %v", err)
		}
		if f.last("torrent-remove") != nil {
			t.Fatal("torrent-remove sent")
		}
	})
}

func TestPauseResume(t *testing.T) {
	t.Parallel()
	const ref = "00000000000000000000000000000000000000a5"
	f, srv := newFake(t)
	c := clientFor(t, srv, nil)
	if err := c.Pause(context.Background(), ref); err != nil {
		t.Fatal(err)
	}
	if err := c.Resume(context.Background(), ref); err != nil {
		t.Fatal(err)
	}
	if f.last("torrent-stop") == nil || f.last("torrent-start") == nil {
		t.Fatal("stop/start not sent")
	}
}

func TestCategoriesUnsupported(t *testing.T) {
	t.Parallel()
	_, srv := newFake(t)
	if _, err := clientFor(t, srv, nil).Categories(context.Background()); !errors.Is(err, download.ErrUnsupported) {
		t.Fatalf("err = %v", err)
	}
}

func FuzzMapStatus(f *testing.F) {
	f.Add(4, 0, "", false)
	f.Fuzz(func(t *testing.T, status, errType int, msg string, done bool) {
		switch s, _ := mapStatus(status, errType, msg, done); s {
		case download.StatusQueued, download.StatusDownloading, download.StatusPaused,
			download.StatusCompleted, download.StatusFailed, download.StatusWarning:
		default:
			t.Fatalf("(%d, %d) -> %q", status, errType, s)
		}
	})
}
