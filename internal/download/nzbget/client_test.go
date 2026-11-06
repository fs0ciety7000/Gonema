package nzbget

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
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
	testUser = "nzbget"
	testPass = "s3cret-Passw0rd"
)

type call struct {
	Method string            `json:"method"`
	Params []json.RawMessage `json:"params"`
}

// fakeNZBGet emulates the JSON-RPC endpoint /jsonrpc.
type fakeNZBGet struct {
	t       *testing.T
	version string
	mu      sync.Mutex
	calls   []call
}

func newFake(t *testing.T) (*fakeNZBGet, *httptest.Server) {
	t.Helper()
	f := &fakeNZBGet{t: t, version: "24.3"}
	srv := httptest.NewServer(f)
	t.Cleanup(srv.Close)
	return f, srv
}

func (f *fakeNZBGet) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	if r.URL.Path != "/jsonrpc" || r.Method != http.MethodPost {
		http.NotFound(w, r)
		return
	}
	if u, p, ok := r.BasicAuth(); !ok || u != testUser || p != testPass {
		w.WriteHeader(http.StatusUnauthorized)
		return
	}
	var c call
	if err := json.NewDecoder(r.Body).Decode(&c); err != nil {
		f.t.Errorf("decode: %v", err)
		return
	}
	f.mu.Lock()
	f.calls = append(f.calls, c)
	f.mu.Unlock()
	switch c.Method {
	case "version":
		_, _ = io.WriteString(w, `{"version":"1.1","result":"`+f.version+`"}`)
	case "listgroups":
		f.fixture(w, "listgroups.json")
	case "history":
		f.fixture(w, "history.json")
	case "config":
		f.fixture(w, "config.json")
	case "status":
		_, _ = io.WriteString(w, `{"version":"1.1","result":{"DownloadRate":1048576,"DownloadPaused":false}}`)
	case "append":
		var content string
		_ = json.Unmarshal(c.Params[1], &content)
		if content == "" {
			_, _ = io.WriteString(w, `{"version":"1.1","result":0}`)
			return
		}
		_, _ = io.WriteString(w, `{"version":"1.1","result":42}`)
	case "editqueue":
		_, _ = io.WriteString(w, `{"version":"1.1","result":true}`)
	default:
		_, _ = io.WriteString(w, `{"version":"1.1","error":{"name":"JSONRPCError","code":1,"message":"Invalid procedure"}}`)
	}
}

func (f *fakeNZBGet) fixture(w io.Writer, name string) {
	b, err := os.ReadFile("testdata/" + name)
	if err != nil {
		f.t.Errorf("fixture: %v", err)
	}
	_, _ = w.Write(b)
}

func (f *fakeNZBGet) last(method string) *call {
	f.mu.Lock()
	defer f.mu.Unlock()
	for i := len(f.calls) - 1; i >= 0; i-- {
		if f.calls[i].Method == method {
			return &f.calls[i]
		}
	}
	return nil
}

func (f *fakeNZBGet) count(method string) int {
	f.mu.Lock()
	defer f.mu.Unlock()
	n := 0
	for _, c := range f.calls {
		if c.Method == method {
			n++
		}
	}
	return n
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

func param[T any](t *testing.T, c *call, i int) T {
	t.Helper()
	var v T
	if err := json.Unmarshal(c.Params[i], &v); err != nil {
		t.Fatalf("param %d: %v", i, err)
	}
	return v
}

func TestTest(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name    string
		version string
		mod     func(*download.Config)
		want    error
	}{
		{"ok", "24.3", nil, nil},
		{"bad password", "24.3", func(c *download.Config) { c.Password = "wrong-pass" }, download.ErrAuth},
		{"url base", "24.3", func(c *download.Config) { c.URLBase = "/nzbget" }, download.ErrURLBase},
		{"too old", "17.1", nil, download.ErrUnsupported},
		{"missing category", "24.3", func(c *download.Config) { c.Category = "films" }, download.ErrRejected},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			f, srv := newFake(t)
			f.version = tt.version
			err := clientFor(t, srv, tt.mod).Test(context.Background())
			if !errors.Is(err, tt.want) {
				t.Fatalf("Test = %v, want %v", err, tt.want)
			}
			if err != nil && (strings.Contains(err.Error(), testPass) || strings.Contains(err.Error(), "wrong-pass")) {
				t.Fatalf("error leaks the password: %v", err)
			}
		})
	}
}

func TestRestrictedUser(t *testing.T) {
	t.Parallel()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = io.WriteString(w, `{"version":"1.1","error":{"name":"JSONRPCError","code":401,"message":"Access denied"}}`)
	}))
	t.Cleanup(srv.Close)
	_, err := clientFor(t, srv, nil).Items(context.Background())
	if !errors.Is(err, download.ErrAuth) {
		t.Fatalf("err = %v, want ErrAuth", err)
	}
}

func TestAdd(t *testing.T) {
	t.Parallel()
	f, srv := newFake(t)
	c := clientFor(t, srv, nil)
	ctx := context.Background()

	ref, err := c.Add(ctx, download.AddRequest{Protocol: decision.Usenet, URL: "https://indexer.example/get/1", Priority: download.PriorityForce})
	if err != nil || ref != "42" {
		t.Fatalf("Add URL = %q, %v", ref, err)
	}
	a := f.last("append")
	if len(a.Params) != 10 {
		t.Fatalf("append has %d params, want 10", len(a.Params))
	}
	if param[string](t, a, 0) != "" || param[string](t, a, 1) != "https://indexer.example/get/1" ||
		param[string](t, a, 2) != "movies" || param[int](t, a, 3) != 900 || param[bool](t, a, 5) {
		t.Fatalf("append params = %s", a.Params)
	}

	nzb := []byte("<nzb/>")
	ref, err = c.Add(ctx, download.AddRequest{Protocol: decision.Usenet, Data: nzb, Filename: "x.nzb", Paused: true, Name: "Movie.2016", Priority: download.PriorityLow})
	if err != nil || ref != "42" {
		t.Fatalf("Add data = %q, %v", ref, err)
	}
	a = f.last("append")
	if param[string](t, a, 0) != "Movie.2016.nzb" || param[string](t, a, 1) != base64.StdEncoding.EncodeToString(nzb) ||
		param[int](t, a, 3) != -50 || !param[bool](t, a, 5) {
		t.Fatalf("append params = %s", a.Params)
	}
}

func TestItems(t *testing.T) {
	t.Parallel()
	_, srv := newFake(t)
	items, err := clientFor(t, srv, nil).Items(context.Background())
	if err != nil {
		t.Fatalf("Items: %v", err)
	}
	by := map[string]download.Item{}
	for _, it := range items {
		by[it.Ref] = it
	}
	if _, ok := by["14"]; ok {
		t.Error("item of another category returned")
	}
	if _, ok := by["26"]; ok {
		t.Error("hidden DUP history item returned")
	}
	if len(by) != 8 {
		t.Fatalf("got %d items: %v", len(by), by)
	}
	dl := by["11"]
	if dl.Status != download.StatusDownloading || dl.Size != 5343543296 || dl.Downloaded != 4294967296 ||
		dl.Speed != 1048576 || dl.ETA != 1000*time.Second || dl.Progress < 0.80 || dl.Progress > 0.81 {
		t.Errorf("downloading = %+v", dl)
	}
	if p := by["12"]; p.Status != download.StatusPaused {
		t.Errorf("paused = %+v", p)
	}
	if u := by["13"]; u.Status != download.StatusDownloading || u.Message != "Unpacking Unpacking.Movie.2018-GRP" {
		t.Errorf("unpacking = %+v", u)
	}
	if d := by["21"]; d.Status != download.StatusCompleted || d.OutputPath != "/downloads/completed/movies/Done.Movie.2015-GRP" || d.Progress != 1 {
		t.Errorf("done = %+v", d)
	}
	if m := by["22"]; m.OutputPath != "/media/sorted/Moved.Movie (2014)" {
		t.Errorf("FinalDir must win: %+v", m)
	}
	if h := by["23"]; h.Status != download.StatusFailed || !strings.Contains(h.Message, "retention") {
		t.Errorf("health failure = %+v", h)
	}
	if w := by["24"]; w.Status != download.StatusWarning || !strings.Contains(w.Message, "password") {
		t.Errorf("password warning = %+v", w)
	}
	if u := by["25"]; u.Status != download.StatusFailed || u.RawState != "FAILURE/FETCH" {
		t.Errorf("fetch failure = %+v", u)
	}
}

func TestMapGroupStatus(t *testing.T) {
	t.Parallel()
	tests := map[string]download.Status{
		"QUEUED": download.StatusQueued, "FETCHING": download.StatusQueued, "PAUSED": download.StatusPaused,
		"DOWNLOADING": download.StatusDownloading, "PP_QUEUED": download.StatusDownloading,
		"LOADING_PARS": download.StatusDownloading, "VERIFYING_SOURCES": download.StatusDownloading,
		"REPAIRING": download.StatusDownloading, "VERIFYING_REPAIRED": download.StatusDownloading,
		"RENAMING": download.StatusDownloading, "UNPACKING": download.StatusDownloading,
		"MOVING": download.StatusDownloading, "POST_UNPACK_RENAMING": download.StatusDownloading,
		"POST_DOWNLOAD_RENAMING": download.StatusDownloading, "EXECUTING_SCRIPT": download.StatusDownloading,
		"PP_FINISHED": download.StatusDownloading, "SOMETHING_NEW": download.StatusWarning,
	}
	for raw, want := range tests {
		if got, _ := mapGroupStatus(raw); got != want {
			t.Errorf("mapGroupStatus(%q) = %s, want %s", raw, got, want)
		}
	}
}

func TestMapHistoryStatus(t *testing.T) {
	t.Parallel()
	tests := map[string]download.Status{
		"SUCCESS/ALL": download.StatusCompleted, "SUCCESS/UNPACK": download.StatusCompleted,
		"SUCCESS/PAR": download.StatusCompleted, "SUCCESS/HEALTH": download.StatusCompleted,
		"SUCCESS/GOOD": download.StatusCompleted, "SUCCESS/MARK": download.StatusCompleted,
		"WARNING/SCRIPT": download.StatusWarning, "WARNING/SPACE": download.StatusWarning,
		"WARNING/PASSWORD": download.StatusWarning, "WARNING/DAMAGED": download.StatusWarning,
		"WARNING/REPAIRABLE": download.StatusWarning, "WARNING/HEALTH": download.StatusWarning,
		"WARNING/SKIPPED": download.StatusWarning,
		"DELETED/MANUAL":  download.StatusFailed, "DELETED/DUPE": download.StatusFailed,
		"DELETED/COPY": download.StatusFailed, "DELETED/GOOD": download.StatusFailed,
		"FAILURE/PAR": download.StatusFailed, "FAILURE/UNPACK": download.StatusFailed,
		"FAILURE/MOVE": download.StatusFailed, "FAILURE/SCAN": download.StatusFailed,
		"FAILURE/BAD": download.StatusFailed, "FAILURE/HEALTH": download.StatusFailed,
		"FAILURE/FETCH": download.StatusFailed, "FAILURE/NEW": download.StatusFailed,
		"": download.StatusWarning, "OTHER/X": download.StatusWarning,
	}
	for raw, want := range tests {
		got, msg := mapHistoryStatus(raw)
		if got != want {
			t.Errorf("mapHistoryStatus(%q) = %s, want %s", raw, got, want)
		}
		if got != download.StatusCompleted && msg == "" {
			t.Errorf("mapHistoryStatus(%q) has no message", raw)
		}
	}
}

func TestRemove(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name       string
		ref        string
		deleteData bool
		want       string
		wantErr    error
	}{
		{"queue", "11", false, "GroupFinalDelete", nil},
		{"queue with data", "12", true, "GroupFinalDelete", nil},
		{"history", "21", false, "HistoryFinalDelete", nil},
		{"history with data", "23", true, "HistoryFinalDelete", nil},
		{"unknown", "99", false, "", download.ErrNotFound},
		{"invalid", "abc", false, "", download.ErrInvalidRequest},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			f, srv := newFake(t)
			err := clientFor(t, srv, nil).Remove(context.Background(), tt.ref, tt.deleteData)
			if !errors.Is(err, tt.wantErr) {
				t.Fatalf("Remove = %v, want %v", err, tt.wantErr)
			}
			e := f.last("editqueue")
			if tt.wantErr != nil {
				if e != nil {
					t.Fatalf("editqueue sent: %s", e.Params)
				}
				return
			}
			id, _ := strconv.ParseInt(tt.ref, 10, 64)
			if param[string](t, e, 0) != tt.want || param[string](t, e, 1) != "" || param[[]int64](t, e, 2)[0] != id {
				t.Fatalf("editqueue = %s", e.Params)
			}
		})
	}
}

func TestPauseResume(t *testing.T) {
	t.Parallel()
	f, srv := newFake(t)
	c := clientFor(t, srv, nil)
	ctx := context.Background()
	if err := c.Pause(ctx, "11"); err != nil {
		t.Fatal(err)
	}
	if param[string](t, f.last("editqueue"), 0) != "GroupPause" {
		t.Fatal("GroupPause not sent")
	}
	if err := c.Resume(ctx, "12"); err != nil {
		t.Fatal(err)
	}
	if param[string](t, f.last("editqueue"), 0) != "GroupResume" {
		t.Fatal("GroupResume not sent")
	}
	if err := c.Pause(ctx, "21"); !errors.Is(err, download.ErrNotFound) {
		t.Fatalf("pause of a history item = %v", err)
	}
	if n := f.count("editqueue"); n != 2 {
		t.Fatalf("editqueue sent %d times", n)
	}
}

func TestCategories(t *testing.T) {
	t.Parallel()
	_, srv := newFake(t)
	cats, err := clientFor(t, srv, nil).Categories(context.Background())
	if err != nil || strings.Join(cats, ",") != "movies,tv" {
		t.Fatalf("Categories = %v, %v", cats, err)
	}
}

func FuzzMapStatus(f *testing.F) {
	for _, s := range []string{"SUCCESS/ALL", "FAILURE/", "/", "DOWNLOADING", ""} {
		f.Add(s)
	}
	f.Fuzz(func(t *testing.T, raw string) {
		for _, s := range []download.Status{first(mapGroupStatus(raw)), first(mapHistoryStatus(raw))} {
			switch s {
			case download.StatusQueued, download.StatusDownloading, download.StatusPaused,
				download.StatusCompleted, download.StatusFailed, download.StatusWarning:
			default:
				t.Fatalf("%q -> %q", raw, s)
			}
		}
	})
}

func first(s download.Status, _ string) download.Status { return s }
