package deluge

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

const testPass = "deluge-Web-Pass"

type rpcCall struct {
	Method string            `json:"method"`
	Params []json.RawMessage `json:"params"`
}

// fakeDeluge emulates the Web UI JSON-RPC endpoint /json.
type fakeDeluge struct {
	t         *testing.T
	mu        sync.Mutex
	sid       string
	logins    int
	connected bool
	hosts     bool
	label     bool
	labels    []string
	calls     []rpcCall
}

func newFake(t *testing.T) (*fakeDeluge, *httptest.Server) {
	t.Helper()
	f := &fakeDeluge{t: t, hosts: true, label: true, labels: []string{"tv"}}
	srv := httptest.NewServer(f)
	t.Cleanup(srv.Close)
	return f, srv
}

func (f *fakeDeluge) reply(w io.Writer, result any) {
	_ = json.NewEncoder(w).Encode(map[string]any{"result": result, "error": nil, "id": 1})
}

func (f *fakeDeluge) fail(w io.Writer, code int, msg string) {
	_ = json.NewEncoder(w).Encode(map[string]any{"result": nil, "error": map[string]any{"message": msg, "code": code}, "id": 1})
}

func (f *fakeDeluge) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	if r.URL.Path != "/json" || r.Method != http.MethodPost {
		http.NotFound(w, r)
		return
	}
	if r.Header.Get("Content-Type") != "application/json" {
		f.t.Errorf("content type %q", r.Header.Get("Content-Type"))
	}
	var c rpcCall
	if err := json.NewDecoder(r.Body).Decode(&c); err != nil {
		f.t.Errorf("decode: %v", err)
		return
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	f.calls = append(f.calls, c)
	if c.Method == "auth.login" {
		var pass string
		_ = json.Unmarshal(c.Params[0], &pass)
		if pass != testPass {
			f.reply(w, false)
			return
		}
		f.logins++
		f.sid = fmt.Sprintf("deadbeefcafe%d", f.logins)
		http.SetCookie(w, &http.Cookie{Name: "_session_id", Value: f.sid})
		f.reply(w, true)
		return
	}
	if ck, err := r.Cookie("_session_id"); err != nil || ck.Value != f.sid {
		f.fail(w, 1, "Not authenticated")
		return
	}
	switch {
	case c.Method == "web.connected":
		f.reply(w, f.connected)
		return
	case c.Method == "web.get_hosts":
		if !f.hosts {
			f.reply(w, []any{})
			return
		}
		f.reply(w, []any{[]any{"c0ffee", "127.0.0.1", 58846, "localclient"}})
		return
	case c.Method == "web.connect":
		f.connected = true
		f.reply(w, []string{"core.add_torrent_url"})
		return
	case !f.connected:
		f.fail(w, 2, "Unknown method")
		return
	case strings.HasPrefix(c.Method, "label.") && !f.label:
		f.fail(w, 2, "Unknown method")
		return
	}
	f.core(w, &c)
}

func (f *fakeDeluge) core(w io.Writer, c *rpcCall) {
	switch c.Method {
	case "core.get_enabled_plugins":
		if f.label {
			f.reply(w, []string{"Label"})
		} else {
			f.reply(w, []string{})
		}
	case "core.add_torrent_magnet", "core.add_torrent_url", "core.add_torrent_file":
		f.reply(w, "ABCDEF0123456789ABCDEF0123456789ABCDEF01")
	case "core.get_torrents_status":
		b, err := os.ReadFile("testdata/torrents_status.json")
		if err != nil {
			f.t.Errorf("fixture: %v", err)
		}
		_, _ = w.Write(b)
	case "core.get_torrent_status":
		var id string
		_ = json.Unmarshal(c.Params[0], &id)
		if strings.HasPrefix(id, strings.Repeat("0", 38)) {
			f.reply(w, map[string]any{"hash": id})
		} else {
			f.reply(w, map[string]any{})
		}
	case "core.remove_torrent":
		f.reply(w, true)
	case "core.pause_torrents", "core.resume_torrents", "core.queue_top", "label.set_torrent", "label.add":
		f.reply(w, nil)
	case "label.get_labels":
		f.reply(w, f.labels)
	default:
		f.fail(w, 2, "Unknown method")
	}
}

func (f *fakeDeluge) last(method string) *rpcCall {
	f.mu.Lock()
	defer f.mu.Unlock()
	for i := len(f.calls) - 1; i >= 0; i-- {
		if f.calls[i].Method == method {
			return &f.calls[i]
		}
	}
	return nil
}

func (f *fakeDeluge) methods() []string {
	f.mu.Lock()
	defer f.mu.Unlock()
	out := make([]string, len(f.calls))
	for i, c := range f.calls {
		out[i] = c.Method
	}
	return out
}

func param[T any](t *testing.T, c *rpcCall, i int) T {
	t.Helper()
	var v T
	if err := json.Unmarshal(c.Params[i], &v); err != nil {
		t.Fatalf("param %d of %s: %v", i, c.Method, err)
	}
	return v
}

func clientFor(t *testing.T, srv *httptest.Server, mod func(*download.Config)) *Client {
	t.Helper()
	u, _ := url.Parse(srv.URL)
	port, _ := strconv.Atoi(u.Port())
	cfg := &download.Config{Host: u.Hostname(), Port: port, Password: testPass, Category: "Movies", HTTPClient: srv.Client()}
	if mod != nil {
		mod(cfg)
	}
	c, err := New(cfg)
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	return c
}

func TestConnectFlow(t *testing.T) {
	t.Parallel()
	f, srv := newFake(t)
	if err := clientFor(t, srv, nil).Test(context.Background()); err != nil {
		t.Fatal(err)
	}
	want := "auth.login,web.connected,web.get_hosts,web.connect,web.connected,core.get_enabled_plugins"
	if got := strings.Join(f.methods(), ","); got != want {
		t.Fatalf("calls = %s\nwant    %s", got, want)
	}
	if id := param[string](t, f.last("web.connect"), 0); id != "c0ffee" {
		t.Fatalf("connected to %q", id)
	}
}

func TestAlreadyConnected(t *testing.T) {
	t.Parallel()
	f, srv := newFake(t)
	f.connected = true
	if err := clientFor(t, srv, nil).Test(context.Background()); err != nil {
		t.Fatal(err)
	}
	if f.last("web.connect") != nil {
		t.Fatal("web.connect called while connected")
	}
}

func TestTestErrors(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name  string
		setup func(*fakeDeluge)
		mod   func(*download.Config)
		want  error
	}{
		{"wrong password", nil, func(c *download.Config) { c.Password = "bad-pass-x" }, download.ErrAuth},
		{"no daemon", func(f *fakeDeluge) { f.hosts = false }, nil, download.ErrAPIDisabled},
		{"no label plugin", func(f *fakeDeluge) { f.label = false }, nil, download.ErrUnsupported},
		{"no label plugin, no category", func(f *fakeDeluge) { f.label = false }, func(c *download.Config) { c.Category = "" }, nil},
		{"url base", nil, func(c *download.Config) { c.URLBase = "/deluge" }, download.ErrURLBase},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			f, srv := newFake(t)
			if tt.setup != nil {
				tt.setup(f)
			}
			err := clientFor(t, srv, tt.mod).Test(context.Background())
			if !errors.Is(err, tt.want) {
				t.Fatalf("Test = %v, want %v", err, tt.want)
			}
			if err != nil && (strings.Contains(err.Error(), testPass) || strings.Contains(err.Error(), "bad-pass-x")) {
				t.Fatalf("error leaks the password: %v", err)
			}
		})
	}
}

func TestReconnectAfterExpiryAndDisconnect(t *testing.T) {
	t.Parallel()
	f, srv := newFake(t)
	c := clientFor(t, srv, nil)
	ctx := context.Background()
	if _, err := c.Items(ctx); err != nil {
		t.Fatal(err)
	}
	f.mu.Lock()
	f.sid = "expired"
	f.mu.Unlock()
	if _, err := c.Items(ctx); err != nil {
		t.Fatalf("after session expiry: %v", err)
	}
	f.mu.Lock()
	f.connected = false
	f.mu.Unlock()
	if _, err := c.Items(ctx); err != nil {
		t.Fatalf("after daemon disconnect: %v", err)
	}
	f.mu.Lock()
	logins := f.logins
	f.mu.Unlock()
	if logins != 3 {
		t.Fatalf("logins = %d, want 3", logins)
	}
}

func TestAdd(t *testing.T) {
	t.Parallel()
	data := []byte("d4:infod4:name1:xee")
	tests := []struct {
		name   string
		req    download.AddRequest
		method string
		check  func(t *testing.T, c *rpcCall)
	}{
		{
			name: "magnet", method: "core.add_torrent_magnet",
			req: download.AddRequest{Protocol: decision.Torrent, URL: "magnet:?xt=urn:btih:abc", Paused: true, SeedRatio: 2},
			check: func(t *testing.T, c *rpcCall) {
				t.Helper()
				opts := param[map[string]any](t, c, 1)
				if opts["add_paused"] != true || opts["stop_at_ratio"] != true || opts["stop_ratio"] != 2.0 {
					t.Fatalf("options = %v", opts)
				}
			},
		},
		{
			name: "url", method: "core.add_torrent_url",
			req: download.AddRequest{Protocol: decision.Torrent, URL: "https://indexer.example/t.torrent"},
			check: func(t *testing.T, c *rpcCall) {
				t.Helper()
				if param[string](t, c, 0) != "https://indexer.example/t.torrent" || len(c.Params) != 3 {
					t.Fatalf("params = %s", c.Params)
				}
			},
		},
		{
			name: "file", method: "core.add_torrent_file",
			req: download.AddRequest{Protocol: decision.Torrent, Data: data, Filename: "x.torrent", Priority: download.PriorityHigh},
			check: func(t *testing.T, c *rpcCall) {
				t.Helper()
				if param[string](t, c, 0) != "x.torrent" || param[string](t, c, 1) != base64.StdEncoding.EncodeToString(data) {
					t.Fatalf("params = %s", c.Params)
				}
			},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			f, srv := newFake(t)
			ref, err := clientFor(t, srv, nil).Add(context.Background(), tt.req)
			if err != nil || ref != "abcdef0123456789abcdef0123456789abcdef01" {
				t.Fatalf("Add = %q, %v", ref, err)
			}
			tt.check(t, f.last(tt.method))
			lbl := f.last("label.set_torrent")
			if lbl == nil || param[string](t, lbl, 1) != "movies" {
				t.Fatalf("label not set: %+v", lbl)
			}
			if a := f.last("label.add"); a == nil || param[string](t, a, 0) != "movies" {
				t.Fatal("missing label not created")
			}
			if (tt.req.Priority == download.PriorityHigh) != (f.last("core.queue_top") != nil) {
				t.Fatal("queue_top mismatch")
			}
		})
	}
}

func TestItems(t *testing.T) {
	t.Parallel()
	f, srv := newFake(t)
	items, err := clientFor(t, srv, nil).Items(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if filter := param[map[string]any](t, f.last("core.get_torrents_status"), 0); filter["label"] != "movies" {
		t.Fatalf("filter = %v", filter)
	}
	if len(items) != 10 {
		t.Fatalf("%d items", len(items))
	}
	want := []download.Status{
		download.StatusDownloading, download.StatusCompleted, download.StatusPaused, download.StatusCompleted,
		download.StatusQueued, download.StatusCompleted, download.StatusDownloading, download.StatusQueued,
		download.StatusDownloading, download.StatusFailed,
	}
	for i, it := range items {
		if it.Status != want[i] {
			t.Errorf("%s (%s) = %s, want %s", it.Ref, it.RawState, it.Status, want[i])
		}
	}
	dl := items[0]
	if dl.Progress != 0.425 || dl.Speed != 4096 || dl.ETA != 5*time.Minute || dl.Size != 2000 || dl.Downloaded != 850 ||
		dl.OutputPath != "/downloads/movies/Movie.0.1080p-GRP" || dl.Category != "movies" {
		t.Errorf("downloading = %+v", dl)
	}
	if e := items[9]; e.Message != "Error: No space left on device" {
		t.Errorf("error message = %q", e.Message)
	}
}

func TestMapState(t *testing.T) {
	t.Parallel()
	tests := []struct {
		raw      string
		finished bool
		want     download.Status
	}{
		{"Downloading", false, download.StatusDownloading},
		{"Checking", false, download.StatusDownloading},
		{"Moving", true, download.StatusDownloading},
		{"Allocating", false, download.StatusQueued},
		{"Queued", false, download.StatusQueued},
		{"Queued", true, download.StatusCompleted},
		{"Paused", false, download.StatusPaused},
		{"Paused", true, download.StatusCompleted},
		{"Seeding", true, download.StatusCompleted},
		{"Error", false, download.StatusFailed},
		{"Nope", false, download.StatusWarning},
	}
	for _, tt := range tests {
		if got, _ := mapState(tt.raw, tt.finished, ""); got != tt.want {
			t.Errorf("mapState(%q, %v) = %s, want %s", tt.raw, tt.finished, got, tt.want)
		}
	}
}

func TestRemove(t *testing.T) {
	t.Parallel()
	const ref = "00000000000000000000000000000000000000d1"
	for _, del := range []bool{false, true} {
		t.Run(strconv.FormatBool(del), func(t *testing.T) {
			t.Parallel()
			f, srv := newFake(t)
			if err := clientFor(t, srv, nil).Remove(context.Background(), ref, del); err != nil {
				t.Fatal(err)
			}
			c := f.last("core.remove_torrent")
			if param[string](t, c, 0) != ref || param[bool](t, c, 1) != del {
				t.Fatalf("remove params = %s", c.Params)
			}
		})
	}
	t.Run("unknown", func(t *testing.T) {
		t.Parallel()
		f, srv := newFake(t)
		err := clientFor(t, srv, nil).Remove(context.Background(), strings.Repeat("e", 40), true)
		if !errors.Is(err, download.ErrNotFound) || f.last("core.remove_torrent") != nil {
			t.Fatalf("err = %v", err)
		}
	})
}

func TestPauseResume(t *testing.T) {
	t.Parallel()
	const ref = "00000000000000000000000000000000000000d0"
	f, srv := newFake(t)
	c := clientFor(t, srv, nil)
	if err := c.Pause(context.Background(), ref); err != nil {
		t.Fatal(err)
	}
	if ids := param[[]string](t, f.last("core.pause_torrents"), 0); len(ids) != 1 || ids[0] != ref {
		t.Fatalf("pause ids = %v", ids)
	}
	if err := c.Resume(context.Background(), ref); err != nil {
		t.Fatal(err)
	}
	if f.last("core.resume_torrents") == nil {
		t.Fatal("resume not sent")
	}
}

func TestCategories(t *testing.T) {
	t.Parallel()
	f, srv := newFake(t)
	cats, err := clientFor(t, srv, nil).Categories(context.Background())
	if err != nil || strings.Join(cats, ",") != "tv" {
		t.Fatalf("Categories = %v, %v", cats, err)
	}
	f.mu.Lock()
	f.label = false
	f.mu.Unlock()
	if _, err := clientFor(t, srv, nil).Categories(context.Background()); !errors.Is(err, download.ErrUnsupported) {
		t.Fatalf("without plugin: %v", err)
	}
}

func TestSessionCookieRedacted(t *testing.T) {
	t.Parallel()
	_, srv := newFake(t)
	c := clientFor(t, srv, nil)
	if err := c.Test(context.Background()); err != nil {
		t.Fatal(err)
	}
	if got := c.r.Redact("id deadbeefcafe1"); strings.Contains(got, "deadbeefcafe1") {
		t.Fatalf("not redacted: %s", got)
	}
}
