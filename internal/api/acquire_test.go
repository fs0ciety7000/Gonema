package api

import (
	"bytes"
	"context"
	"fmt"
	"log/slog"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/fs0ciety7000/gonema/internal/acquire"
	"github.com/fs0ciety7000/gonema/internal/acquire/acquiretest"
	"github.com/fs0ciety7000/gonema/internal/events"
	"github.com/fs0ciety7000/gonema/internal/jobs"
	"github.com/fs0ciety7000/gonema/internal/library"
	"github.com/fs0ciety7000/gonema/internal/metadata"
	"github.com/fs0ciety7000/gonema/internal/secretbox"
	"github.com/fs0ciety7000/gonema/internal/store/storetest"
)

const (
	acqTrackerKey = "API-TRACKER-SECRET"
	acqQBPass     = "API-QB-SECRET"
)

type lockedBuffer struct {
	mu sync.Mutex
	b  bytes.Buffer
}

func (l *lockedBuffer) Write(p []byte) (int, error) {
	l.mu.Lock()
	defer l.mu.Unlock()
	return l.b.Write(p)
}

func (l *lockedBuffer) String() string {
	l.mu.Lock()
	defer l.mu.Unlock()
	return l.b.String()
}

type acqEnv struct {
	*testEnv
	acq     *acquire.Service
	logs    *lockedBuffer
	movieID int64
}

func newAcquireEnv(t *testing.T) *acqEnv {
	t.Helper()
	st := storetest.New(t)
	key, _ := secretbox.NewKey()
	box, _ := secretbox.New(key)
	bus := events.New(events.Options{})
	logs := &lockedBuffer{}
	t.Cleanup(func() {
		if t.Failed() {
			t.Log(logs.String())
		}
	})
	log := slog.New(slog.NewTextHandler(logs, &slog.HandlerOptions{Level: slog.LevelDebug}))
	runner := jobs.New(st, jobs.Options{Workers: 1, Bus: bus, Logger: log})
	d := metadata.Date{Year: 1999, Month: 10, Day: 15}
	prov := &provider{movies: map[int]*metadata.Movie{550: {
		TMDBID: 550, Title: "Fight Club", Year: 1999, ReleaseDate: d, IMDbID: "tt0137523", OriginalLanguage: "en",
		Releases: metadata.ReleaseSelection{Cinema: metadata.DatedRelease{Date: d, Region: "US"}},
	}}}
	lib, err := library.New(library.Options{
		Store: st, Box: box, Jobs: runner, Bus: bus, Logger: log,
		NewProvider: func(string) (library.KeyedProvider, error) { return prov, nil },
	})
	if err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	if err := lib.Init(ctx); err != nil {
		t.Fatal(err)
	}
	if err := lib.SetTMDBKey(ctx, testKey); err != nil {
		t.Fatal(err)
	}
	acq, err := acquire.New(acquire.Options{Store: st, Box: box, Matcher: lib, Bus: bus, Logger: log})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(acq.Close)
	if err := acq.Init(ctx); err != nil {
		t.Fatal(err)
	}
	lib.RegisterJobs(runner)
	acq.RegisterJobs(ctx, runner)
	seed := storetest.SeedLibrary(t, st)
	m, _, err := lib.AddMovie(ctx, 550, library.AddOptions{RootFolderID: seed.RootFolderID, QualityProfileID: seed.ProfileID, Monitored: true})
	if err != nil {
		t.Fatal(err)
	}
	srv := New(Deps{
		Library: lib, Acquire: acq, Jobs: runner, Bus: bus, Store: st, Logger: log, Version: "test",
		StartedAt: time.Now(), DataDir: t.TempDir(), SSEHeartbeat: 50 * time.Millisecond,
	})
	ts := httptest.NewServer(srv.Handler())
	t.Cleanup(func() {
		srv.Shutdown()
		ts.Close()
	})
	return &acqEnv{
		testEnv: &testEnv{srv: srv, ts: ts, lib: lib, bus: bus, rootID: seed.RootFolderID, profID: seed.ProfileID},
		acq:     acq, logs: logs, movieID: m.ID,
	}
}

func noLeak(t *testing.T, where, s string, tr *acquiretest.Tracker) {
	t.Helper()
	for _, secret := range []string{acqTrackerKey, acqQBPass, "/dl/", "magnet:?", tr.Srv.URL + "/dl"} {
		if strings.Contains(s, secret) {
			t.Errorf("%s leaks %q", where, secret)
		}
	}
}

func TestAcquisitionAPI(t *testing.T) {
	e := newAcquireEnv(t)
	tr := acquiretest.NewTracker(t, acqTrackerKey,
		acquiretest.Item{Title: "Fight.Club.1999.720p.BluRay.x264-SMALL", Size: 4 << 30, Seeders: 80},
		acquiretest.Item{Title: "Fight.Club.1999.1080p.BluRay.x264-GRP", Size: 10 << 30, Seeders: 50, GUIDIsLink: true},
	)
	qb := acquiretest.NewQBittorrent(t, "admin", acqQBPass)
	var all strings.Builder
	call := func(method, path, body string) response {
		t.Helper()
		r := e.do(t, method, path, body)
		all.WriteString(r.body)
		return r
	}

	// Indexers.
	ixBody := fmt.Sprintf(`{"name":"Tracker","kind":"torznab","baseUrl":%q,"apiKey":%q}`, tr.Srv.URL, acqTrackerKey)
	r := call("POST", "/api/v1/indexers", ixBody)
	ix := decode[acquire.Indexer](t, r)
	if r.status != 201 || !ix.APIKeySet || ix.ID == 0 {
		t.Fatalf("create indexer = %d %s", r.status, r.body)
	}
	problemOK(t, call("POST", "/api/v1/indexers", ixBody), 409)
	problemOK(t, call("POST", "/api/v1/indexers", `{"name":"x","kind":"rss","baseUrl":"https://x.example"}`), 422)
	problemOK(t, call("POST", "/api/v1/indexers", `{"name":"x","kind":"newznab","baseUrl":"https://u:p@x.example"}`), 422)
	if r := call("POST", fmt.Sprintf("/api/v1/indexers/%d/test", ix.ID), ""); r.status != 200 || !decode[acquire.TestResult](t, r).OK {
		t.Errorf("test saved indexer = %d %s", r.status, r.body)
	}
	r = call("POST", "/api/v1/indexers/test", fmt.Sprintf(`{"name":"x","kind":"torznab","baseUrl":%q,"apiKey":"WRONGKEY"}`, tr.Srv.URL))
	if res := decode[acquire.TestResult](t, r); r.status != 200 || res.OK || res.Message == "" || strings.Contains(r.body, "WRONGKEY") {
		t.Errorf("test unsaved indexer = %d %s", r.status, r.body)
	}
	r = call("PUT", fmt.Sprintf("/api/v1/indexers/%d", ix.ID), fmt.Sprintf(`{"name":"Tracker","kind":"torznab","baseUrl":%q,"priority":5}`, tr.Srv.URL))
	if got := decode[acquire.Indexer](t, r); r.status != 200 || !got.APIKeySet || got.Priority != 5 {
		t.Errorf("update indexer = %d %s", r.status, r.body)
	}
	if r := call("GET", "/api/v1/indexers", ""); r.status != 200 || len(decode[[]acquire.Indexer](t, r)) != 1 {
		t.Errorf("list indexers = %d %s", r.status, r.body)
	}
	problemOK(t, call("GET", "/api/v1/indexers/999", ""), 404)

	// Download client and path mapping.
	r = call("POST", "/api/v1/download-clients", fmt.Sprintf(`{"name":"qB","kind":"qbittorrent","host":"127.0.0.1","port":%d,"username":"admin","password":%q,"category":"movies"}`, qb.Port(), acqQBPass))
	dc := decode[acquire.DownloadClient](t, r)
	if r.status != 201 || !dc.PasswordSet || dc.Username != "admin" {
		t.Fatalf("create client = %d %s", r.status, r.body)
	}
	if r := call("POST", fmt.Sprintf("/api/v1/download-clients/%d/test", dc.ID), ""); !decode[acquire.TestResult](t, r).OK {
		t.Errorf("test client = %s", r.body)
	}
	r = call("POST", "/api/v1/remote-path-mappings", `{"host":"127.0.0.1","remotePath":"/downloads","localPath":"/mnt/dl"}`)
	pm := decode[acquire.RemotePathMapping](t, r)
	if r.status != 201 {
		t.Fatalf("create mapping = %d %s", r.status, r.body)
	}
	if r := call("PUT", fmt.Sprintf("/api/v1/remote-path-mappings/%d", pm.ID), `{"host":"127.0.0.1","remotePath":"/data","localPath":"/mnt/data"}`); r.status != 200 {
		t.Errorf("update mapping = %d %s", r.status, r.body)
	}
	if r := call("GET", "/api/v1/remote-path-mappings", ""); len(decode[[]acquire.RemotePathMapping](t, r)) != 1 {
		t.Errorf("list mappings = %s", r.body)
	}

	// Interactive search.
	r = call("POST", fmt.Sprintf("/api/v1/movies/%d/search/interactive", e.movieID), "")
	started := decode[SearchStarted](t, r)
	if r.status != 202 || len(started.SearchID) != 32 || !strings.HasSuffix(started.Location, "/searches/"+started.SearchID) {
		t.Fatalf("start search = %d %s", r.status, r.body)
	}
	var snap acquire.SearchSnapshot
	acquiretest.Wait(t, "search finished", func() bool {
		snap = decode[acquire.SearchSnapshot](t, call("GET", "/api/v1/searches/"+started.SearchID, ""))
		return snap.Status == "finished"
	})
	if len(snap.Results) != 2 || !snap.Results[0].Accepted || snap.Results[1].Accepted || snap.Results[1].Rejections[0].Code != "quality_not_allowed" {
		t.Fatalf("results = %+v", snap.Results)
	}
	problemOK(t, call("GET", "/api/v1/searches/"+strings.Repeat("a", 32), ""), 404)
	grabURL := "/api/v1/searches/" + started.SearchID + "/grab"
	detail := problemOK(t, call("POST", grabURL, fmt.Sprintf(`{"key":%q}`, snap.Results[1].Key)), 422)
	if !strings.Contains(detail, "not allowed") || !strings.Contains(detail, "override") {
		t.Errorf("refusal detail = %q", detail)
	}
	r = call("POST", grabURL, fmt.Sprintf(`{"key":%q,"override":true}`, snap.Results[1].Key))
	item := decode[acquire.QueueItem](t, r)
	if r.status != 201 || !item.Manual || item.Status != "queued" {
		t.Fatalf("override grab = %d %s", r.status, r.body)
	}

	// Queue.
	if q := decode[[]acquire.QueueItem](t, call("GET", "/api/v1/queue", "")); len(q) != 1 || q[0].ID != item.ID {
		t.Errorf("queue = %+v", q)
	}
	if r := call("POST", fmt.Sprintf("/api/v1/queue/%d/pause", item.ID), ""); r.status != 200 || decode[acquire.QueueItem](t, r).Status != "paused" {
		t.Errorf("pause = %d %s", r.status, r.body)
	}
	if r := call("POST", fmt.Sprintf("/api/v1/queue/%d/resume", item.ID), ""); r.status != 200 {
		t.Errorf("resume = %d %s", r.status, r.body)
	}
	hash := qb.Torrents()[0].Hash
	if r := call("DELETE", fmt.Sprintf("/api/v1/queue/%d?blocklist=true", item.ID), ""); r.status != 204 {
		t.Fatalf("remove = %d %s", r.status, r.body)
	}
	if del, files := qb.Deleted(hash); !del || files {
		t.Errorf("client delete = %v, files %v", del, files)
	}
	problemOK(t, call("POST", fmt.Sprintf("/api/v1/queue/%d/pause", item.ID), ""), 404)

	// Blocklist.
	bl := decode[[]acquire.BlocklistItem](t, call("GET", fmt.Sprintf("/api/v1/blocklist?movieId=%d", e.movieID), ""))
	if len(bl) != 1 || bl[0].SourceTitle != "Fight.Club.1999.720p.BluRay.x264-SMALL" {
		t.Fatalf("blocklist = %+v", bl)
	}
	if r := call("DELETE", fmt.Sprintf("/api/v1/blocklist/%d", bl[0].ID), ""); r.status != 204 {
		t.Errorf("unblock = %d %s", r.status, r.body)
	}
	if r := call("POST", "/api/v1/blocklist/groups", fmt.Sprintf(`{"group":"EVO","movieId":%d}`, e.movieID)); r.status != 201 {
		t.Errorf("block group = %d %s", r.status, r.body)
	}
	r = call("DELETE", fmt.Sprintf("/api/v1/blocklist?movieId=%d", e.movieID), "")
	if r.status != 200 || !strings.Contains(r.body, `"removed":1`) {
		t.Errorf("clear = %d %s", r.status, r.body)
	}

	// History tells the story.
	h := call("GET", fmt.Sprintf("/api/v1/movies/%d/history", e.movieID), "").body
	for _, ev := range []string{"grab_overridden", "download_removed", "blocklisted"} {
		if !strings.Contains(h, `"`+ev+`"`) {
			t.Errorf("history lacks %s: %s", ev, h)
		}
	}
	if r := call("POST", fmt.Sprintf("/api/v1/movies/%d/search", e.movieID), ""); r.status != 202 {
		t.Errorf("search now = %d %s", r.status, r.body)
	}
	if r := call("DELETE", fmt.Sprintf("/api/v1/download-clients/%d", dc.ID), ""); r.status != 204 {
		t.Errorf("delete client = %d", r.status)
	}
	if r := call("DELETE", fmt.Sprintf("/api/v1/indexers/%d", ix.ID), ""); r.status != 204 {
		t.Errorf("delete indexer = %d", r.status)
	}
	if r := call("GET", "/api/v1/openapi.json", ""); !strings.Contains(r.body, `"/searches/{searchId}/grab"`) {
		t.Error("openapi lacks the acquisition routes")
	}
	noLeak(t, "API responses", all.String(), tr)
	noLeak(t, "logs", e.logs.String(), tr)
}
