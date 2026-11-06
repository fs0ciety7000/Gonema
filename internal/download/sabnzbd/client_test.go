package sabnzbd

import (
	"bytes"
	"context"
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

const testKey = "0123456789abcdef0123456789abcdef"

// fakeSAB emulates the SABnzbd API under the URL base /sabnzbd.
type fakeSAB struct {
	t        *testing.T
	mu       sync.Mutex
	calls    []url.Values // query (or form) of every api call
	file     []byte       // last uploaded nzb
	fileName string
	queueIDs map[string]bool
	histIDs  map[string]bool
	cats     []string
}

func newFakeSAB(t *testing.T) (*fakeSAB, *httptest.Server) {
	t.Helper()
	f := &fakeSAB{
		t:        t,
		queueIDs: map[string]bool{"SABnzbd_nzo_p86tgx": true},
		histIDs:  map[string]bool{"SABnzbd_nzo_sdkoun": true},
		cats:     []string{"*", "movies", "tv"},
	}
	srv := httptest.NewServer(f)
	t.Cleanup(srv.Close)
	return f, srv
}

func (f *fakeSAB) fixture(name string) []byte {
	b, err := os.ReadFile("testdata/" + name)
	if err != nil {
		f.t.Errorf("fixture %s: %v", name, err)
	}
	return b
}

func (f *fakeSAB) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	if r.URL.Path != "/sabnzbd/api" {
		http.NotFound(w, r)
		return
	}
	q := r.URL.Query()
	if r.Method == http.MethodPost {
		if err := r.ParseMultipartForm(1 << 20); err != nil {
			f.t.Errorf("multipart: %v", err)
			return
		}
		for k, v := range r.MultipartForm.Value {
			q[k] = v
		}
		if fh := r.MultipartForm.File["name"]; len(fh) == 1 {
			rd, _ := fh[0].Open()
			b, _ := io.ReadAll(rd)
			_ = rd.Close()
			f.mu.Lock()
			f.file, f.fileName = b, fh[0].Filename
			f.mu.Unlock()
		}
	}
	f.mu.Lock()
	f.calls = append(f.calls, q)
	f.mu.Unlock()
	w.Header().Set("Content-Type", "application/json")
	mode := q.Get("mode")
	if mode != "version" && q.Get("apikey") != testKey {
		_, _ = io.WriteString(w, `{"status": false, "error": "API Key Incorrect"}`)
		return
	}
	switch mode {
	case "version":
		_, _ = io.WriteString(w, `{"version": "4.5.1"}`)
	case "get_cats":
		_, _ = io.WriteString(w, `{"categories": ["`+strings.Join(f.cats, `","`)+`"]}`)
	case "addurl", "addfile":
		_, _ = io.WriteString(w, `{"status": true, "nzo_ids": ["SABnzbd_nzo_kyt1f0"]}`)
	case "queue":
		f.queue(w, q)
	case "history":
		f.history(w, q)
	default:
		_, _ = io.WriteString(w, `{"status": false, "error": "not implemented"}`)
	}
}

func (f *fakeSAB) queue(w http.ResponseWriter, q url.Values) {
	id := q.Get("value")
	switch q.Get("name") {
	case "delete", "pause", "resume":
		f.mu.Lock()
		ok := f.queueIDs[id]
		if ok && q.Get("name") == "delete" {
			delete(f.queueIDs, id)
		}
		f.mu.Unlock()
		if !ok {
			_, _ = io.WriteString(w, `{"status": true, "nzo_ids": []}`)
			return
		}
		_, _ = io.WriteString(w, `{"status": true, "nzo_ids": ["`+id+`"]}`)
	default:
		if ids := q.Get("nzo_ids"); ids != "" {
			f.mu.Lock()
			ok := f.queueIDs[ids]
			f.mu.Unlock()
			if ok {
				_, _ = io.WriteString(w, `{"queue": {"slots": [{"nzo_id": "`+ids+`", "status": "Downloading"}]}}`)
			} else {
				_, _ = io.WriteString(w, `{"queue": {"slots": []}}`)
			}
			return
		}
		_, _ = w.Write(f.fixture("queue.json"))
	}
}

func (f *fakeSAB) history(w http.ResponseWriter, q url.Values) {
	id := q.Get("value")
	switch q.Get("name") {
	case "delete":
		f.mu.Lock()
		delete(f.histIDs, id)
		f.mu.Unlock()
		_, _ = io.WriteString(w, `{"status": true}`)
	default:
		if ids := q.Get("nzo_ids"); ids != "" {
			f.mu.Lock()
			ok := f.histIDs[ids]
			f.mu.Unlock()
			if ok {
				_, _ = io.WriteString(w, `{"history": {"slots": [{"nzo_id": "`+ids+`", "status": "Completed"}]}}`)
			} else {
				_, _ = io.WriteString(w, `{"history": {"slots": []}}`)
			}
			return
		}
		_, _ = w.Write(f.fixture("history.json"))
	}
}

func (f *fakeSAB) lastCall() url.Values {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.calls[len(f.calls)-1]
}

func (f *fakeSAB) modes() []string {
	f.mu.Lock()
	defer f.mu.Unlock()
	var out []string
	for _, c := range f.calls {
		m := c.Get("mode")
		if n := c.Get("name"); n != "" && (m == "queue" || m == "history") && !strings.Contains(n, "://") {
			m += ":" + n
		}
		out = append(out, m)
	}
	return out
}

func clientFor(t *testing.T, srv *httptest.Server, mod func(*download.Config)) *Client {
	t.Helper()
	u, _ := url.Parse(srv.URL)
	port, _ := strconv.Atoi(u.Port())
	cfg := &download.Config{Host: u.Hostname(), Port: port, URLBase: "/sabnzbd/", APIKey: testKey, Category: "movies", HTTPClient: srv.Client()}
	if mod != nil {
		mod(cfg)
	}
	c, err := New(cfg)
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	return c
}

func assertNoSecret(t *testing.T, err error) {
	t.Helper()
	if err != nil && strings.Contains(err.Error(), testKey) {
		t.Fatalf("error leaks the API key: %v", err)
	}
}

func TestNewRequiresKey(t *testing.T) {
	t.Parallel()
	if _, err := New(&download.Config{Host: "localhost"}); !errors.Is(err, download.ErrInvalidRequest) {
		t.Fatalf("err = %v, want ErrInvalidRequest", err)
	}
}

func TestTest(t *testing.T) {
	t.Parallel()
	_, srv := newFakeSAB(t)
	tests := []struct {
		name string
		mod  func(*download.Config)
		want error
	}{
		{"ok", nil, nil},
		{"wrong key", func(c *download.Config) { c.APIKey = "wrong-key-value" }, download.ErrAuth},
		{"wrong url base", func(c *download.Config) { c.URLBase = "" }, download.ErrURLBase},
		{"missing category", func(c *download.Config) { c.Category = "films" }, download.ErrRejected},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			err := clientFor(t, srv, tt.mod).Test(context.Background())
			if !errors.Is(err, tt.want) {
				t.Fatalf("Test() = %v, want %v", err, tt.want)
			}
			assertNoSecret(t, err)
			if err != nil && strings.Contains(err.Error(), "wrong-key-value") {
				t.Fatalf("error leaks the key: %v", err)
			}
		})
	}
}

func TestTestNotSABnzbd(t *testing.T) {
	t.Parallel()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = io.WriteString(w, "<html>router login</html>")
	}))
	t.Cleanup(srv.Close)
	err := clientFor(t, srv, nil).Test(context.Background())
	if !errors.Is(err, download.ErrUnexpected) {
		t.Fatalf("err = %v, want ErrUnexpected", err)
	}
}

func TestUnreachableRedactsKey(t *testing.T) {
	t.Parallel()
	srv := httptest.NewServer(http.NotFoundHandler())
	c := clientFor(t, srv, nil)
	srv.Close()
	err := c.Test(context.Background())
	if !errors.Is(err, download.ErrUnreachable) {
		t.Fatalf("err = %v, want ErrUnreachable", err)
	}
	assertNoSecret(t, err)
	if !strings.Contains(err.Error(), "REDACTED") {
		t.Fatalf("expected the redacted URL in %v", err)
	}
}

func TestAddURL(t *testing.T) {
	t.Parallel()
	f, srv := newFakeSAB(t)
	c := clientFor(t, srv, nil)
	ref, err := c.Add(context.Background(), download.AddRequest{
		Protocol: decision.Usenet, URL: "https://indexer.example/get?id=1&apikey=x", Priority: download.PriorityHigh, Name: "Movie.2016",
	})
	if err != nil || ref != "SABnzbd_nzo_kyt1f0" {
		t.Fatalf("Add = %q, %v", ref, err)
	}
	q := f.lastCall()
	want := map[string]string{"mode": "addurl", "name": "https://indexer.example/get?id=1&apikey=x", "cat": "movies", "priority": "1", "nzbname": "Movie.2016", "output": "json"}
	for k, v := range want {
		if q.Get(k) != v {
			t.Errorf("%s = %q, want %q", k, q.Get(k), v)
		}
	}
}

func TestAddFile(t *testing.T) {
	t.Parallel()
	f, srv := newFakeSAB(t)
	c := clientFor(t, srv, nil)
	nzb := []byte(`<?xml version="1.0"?><nzb></nzb>`)
	ref, err := c.Add(context.Background(), download.AddRequest{
		Protocol: decision.Usenet, Data: nzb, Filename: `..\evil"name.nzb`, Paused: true, Category: "tv",
	})
	if err != nil || ref != "SABnzbd_nzo_kyt1f0" {
		t.Fatalf("Add = %q, %v", ref, err)
	}
	q := f.lastCall()
	if q.Get("mode") != "addfile" || q.Get("priority") != "-2" || q.Get("cat") != "tv" || q.Get("apikey") != testKey {
		t.Fatalf("form = %v", q)
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	if !bytes.Equal(f.file, nzb) || f.fileName != "evilname.nzb" {
		t.Fatalf("file %q named %q", f.file, f.fileName)
	}
}

func TestAddRejectsTorrent(t *testing.T) {
	t.Parallel()
	_, srv := newFakeSAB(t)
	_, err := clientFor(t, srv, nil).Add(context.Background(), download.AddRequest{Protocol: decision.Torrent, URL: "magnet:?xt=urn:btih:abc"})
	if !errors.Is(err, download.ErrInvalidRequest) {
		t.Fatalf("err = %v", err)
	}
}

func TestItems(t *testing.T) {
	t.Parallel()
	f, srv := newFakeSAB(t)
	items, err := clientFor(t, srv, nil).Items(context.Background())
	if err != nil {
		t.Fatalf("Items: %v", err)
	}
	if q := f.lastCall(); q.Get("cat") != "movies" || q.Get("mode") != "history" {
		t.Fatalf("history query = %v", q)
	}
	byRef := map[string]download.Item{}
	for _, it := range items {
		byRef[it.Ref] = it
	}
	if len(byRef) != 9 {
		t.Fatalf("got %d items", len(byRef))
	}
	dl := byRef["SABnzbd_nzo_p86tgx"]
	if dl.Status != download.StatusDownloading || dl.Speed != 1327124 || dl.ETA != 16*time.Minute+44*time.Second ||
		dl.Size != 1339713126 || dl.Name != "Movie.2016.1080p.BluRay.x264-GRP" || dl.Category != "movies" {
		t.Errorf("downloading item = %+v", dl)
	}
	if enc := byRef["SABnzbd_nzo_enc1"]; enc.Status != download.StatusWarning || enc.Speed != 0 || enc.Progress != 0.5 {
		t.Errorf("encrypted item = %+v (speed goes to the first downloading job only)", enc)
	}
	done := byRef["SABnzbd_nzo_sdkoun"]
	if done.Status != download.StatusCompleted || done.Progress != 1 || done.OutputPath != `C:\Users\xxx\Videos\Complete\Done.Movie.2016.720p.BluRay.x264-xHD` {
		t.Errorf("completed item = %+v", done)
	}
	for ref, msg := range map[string]string{
		"SABnzbd_nzo_unpack":    "Unpacking failed, write error or disk is full?",
		"SABnzbd_nzo_retention": "Out of retention",
	} {
		if it := byRef[ref]; it.Status != download.StatusFailed || it.Message != msg {
			t.Errorf("%s = %+v, want failed with %q", ref, it, msg)
		}
	}
	if pp := byRef["SABnzbd_nzo_pp"]; pp.Status != download.StatusDownloading || pp.Progress != 1 || pp.RawState != "Extracting" {
		t.Errorf("post-processing item = %+v", pp)
	}
}

func TestMapQueueStatus(t *testing.T) {
	t.Parallel()
	tests := []struct {
		raw    string
		labels []string
		want   download.Status
	}{
		{"Downloading", nil, download.StatusDownloading},
		{"Fetching", nil, download.StatusDownloading},
		{"Queued", nil, download.StatusQueued},
		{"Grabbing", nil, download.StatusQueued},
		{"Checking", nil, download.StatusQueued},
		{"Propagating", nil, download.StatusQueued},
		{"Paused", []string{"DUPLICATE"}, download.StatusPaused},
		{"Downloading", []string{"ENCRYPTED"}, download.StatusWarning},
		{"Bogus", nil, download.StatusWarning},
	}
	for _, tt := range tests {
		t.Run(tt.raw, func(t *testing.T) {
			t.Parallel()
			if got, _ := mapQueueStatus(tt.raw, tt.labels); got != tt.want {
				t.Fatalf("mapQueueStatus(%q, %v) = %s, want %s", tt.raw, tt.labels, got, tt.want)
			}
		})
	}
}

func TestMapHistoryStatus(t *testing.T) {
	t.Parallel()
	tests := []struct {
		raw, fail string
		want      download.Status
		wantMsg   string
	}{
		{"Completed", "", download.StatusCompleted, ""},
		{"Failed", "Unpacking failed, write error or disk is full?", download.StatusFailed, "Unpacking failed, write error or disk is full?"},
		{"Failed", "Out of retention", download.StatusFailed, "Out of retention"},
		{"Failed", "", download.StatusFailed, "failed in SABnzbd"},
		{"Queued", "", download.StatusDownloading, "post-processing: Queued"},
		{"QuickCheck", "", download.StatusDownloading, "post-processing: QuickCheck"},
		{"Verifying", "", download.StatusDownloading, "post-processing: Verifying"},
		{"Repairing", "", download.StatusDownloading, "post-processing: Repairing"},
		{"Fetching", "", download.StatusDownloading, "post-processing: Fetching"},
		{"Extracting", "", download.StatusDownloading, "post-processing: Extracting"},
		{"Moving", "", download.StatusDownloading, "post-processing: Moving"},
		{"Running", "", download.StatusDownloading, "post-processing: Running"},
		{"Deleted", "", download.StatusFailed, "deleted in SABnzbd"},
		{"Weird", "", download.StatusWarning, `unknown SABnzbd state "Weird"`},
	}
	for _, tt := range tests {
		t.Run(tt.raw+"/"+tt.fail, func(t *testing.T) {
			t.Parallel()
			got, msg := mapHistoryStatus(tt.raw, tt.fail)
			if got != tt.want || msg != tt.wantMsg {
				t.Fatalf("mapHistoryStatus(%q, %q) = %s, %q; want %s, %q", tt.raw, tt.fail, got, msg, tt.want, tt.wantMsg)
			}
		})
	}
}

func FuzzMapStatus(f *testing.F) {
	for _, s := range []string{"Downloading", "Failed", "Completed", "", "Paused"} {
		f.Add(s, "msg")
	}
	valid := map[download.Status]bool{
		download.StatusQueued: true, download.StatusDownloading: true, download.StatusPaused: true,
		download.StatusCompleted: true, download.StatusFailed: true, download.StatusWarning: true,
	}
	f.Fuzz(func(t *testing.T, raw, msg string) {
		if s, _ := mapQueueStatus(raw, []string{msg}); !valid[s] {
			t.Fatalf("queue %q -> %q", raw, s)
		}
		if s, _ := mapHistoryStatus(raw, msg); !valid[s] {
			t.Fatalf("history %q -> %q", raw, s)
		}
		_ = parseTimeLeft(raw)
	})
}

func TestParseTimeLeft(t *testing.T) {
	t.Parallel()
	tests := map[string]time.Duration{
		"0:16:44":    16*time.Minute + 44*time.Second,
		"1:02:03:04": 26*time.Hour + 3*time.Minute + 4*time.Second,
		"5:00":       5 * time.Minute,
		"":           0,
		"a:b:c":      0,
		"1:2:3:4:5":  0,
	}
	for in, want := range tests {
		if got := parseTimeLeft(in); got != want {
			t.Errorf("parseTimeLeft(%q) = %s, want %s", in, got, want)
		}
	}
}

func TestRemove(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name       string
		ref        string
		deleteData bool
		wantMode   string
		wantDel    string
		wantErr    error
	}{
		{"queue keep data", "SABnzbd_nzo_p86tgx", false, "queue:delete", "0", nil},
		{"queue delete data", "SABnzbd_nzo_p86tgx", true, "queue:delete", "1", nil},
		{"history keep data", "SABnzbd_nzo_sdkoun", false, "history:delete", "0", nil},
		{"history delete data", "SABnzbd_nzo_sdkoun", true, "history:delete", "1", nil},
		{"unknown", "SABnzbd_nzo_nope", false, "", "", download.ErrNotFound},
		{"special value", "all", true, "", "", download.ErrInvalidRequest},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			f, srv := newFakeSAB(t)
			err := clientFor(t, srv, nil).Remove(context.Background(), tt.ref, tt.deleteData)
			if !errors.Is(err, tt.wantErr) {
				t.Fatalf("Remove = %v, want %v", err, tt.wantErr)
			}
			if tt.wantErr != nil {
				for _, m := range f.modes() {
					if strings.HasSuffix(m, ":delete") {
						t.Fatalf("a delete was sent: %v", f.modes())
					}
				}
				return
			}
			modes := f.modes()
			if got := modes[len(modes)-1]; got != tt.wantMode {
				t.Fatalf("last call %s, want %s (all: %v)", got, tt.wantMode, modes)
			}
			q := f.lastCall()
			if q.Get("del_files") != tt.wantDel || q.Get("value") != tt.ref {
				t.Fatalf("delete query = %v", q)
			}
			if tt.wantMode == "history:delete" && q.Get("archive") != "0" {
				t.Fatalf("history delete must skip the archive: %v", q)
			}
		})
	}
}

func TestPauseResume(t *testing.T) {
	t.Parallel()
	f, srv := newFakeSAB(t)
	c := clientFor(t, srv, nil)
	ctx := context.Background()
	if err := c.Pause(ctx, "SABnzbd_nzo_p86tgx"); err != nil {
		t.Fatalf("Pause: %v", err)
	}
	if q := f.lastCall(); q.Get("name") != "pause" || q.Get("value") != "SABnzbd_nzo_p86tgx" {
		t.Fatalf("pause query %v", q)
	}
	if err := c.Resume(ctx, "SABnzbd_nzo_p86tgx"); err != nil {
		t.Fatalf("Resume: %v", err)
	}
	if err := c.Pause(ctx, "SABnzbd_nzo_nope"); !errors.Is(err, download.ErrNotFound) {
		t.Fatalf("Pause unknown = %v", err)
	}
}

func TestCategories(t *testing.T) {
	t.Parallel()
	_, srv := newFakeSAB(t)
	cats, err := clientFor(t, srv, nil).Categories(context.Background())
	if err != nil || strings.Join(cats, ",") != "movies,tv" {
		t.Fatalf("Categories = %v, %v", cats, err)
	}
}

func TestCanceledContext(t *testing.T) {
	t.Parallel()
	_, srv := newFakeSAB(t)
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	_, err := clientFor(t, srv, nil).Items(ctx)
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("err = %v, want context.Canceled", err)
	}
}

func TestTimeout(t *testing.T) {
	t.Parallel()
	block := make(chan struct{})
	srv := httptest.NewServer(http.HandlerFunc(func(_ http.ResponseWriter, r *http.Request) {
		select {
		case <-block:
		case <-r.Context().Done():
		}
	}))
	t.Cleanup(func() { close(block); srv.Close() })
	err := clientFor(t, srv, func(c *download.Config) { c.Timeout = 50 * time.Millisecond }).Test(context.Background())
	if !errors.Is(err, download.ErrUnreachable) || !strings.Contains(err.Error(), "no answer within") {
		t.Fatalf("err = %v", err)
	}
	assertNoSecret(t, err)
}
