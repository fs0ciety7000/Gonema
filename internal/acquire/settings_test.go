package acquire

import (
	"encoding/json"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/fs0ciety7000/gonema/internal/acquire/acquiretest"
	"github.com/fs0ciety7000/gonema/internal/search"
	"github.com/fs0ciety7000/gonema/internal/store"
)

func TestIndexerSettings(t *testing.T) {
	e := newEnv(t)
	tr := acquiretest.NewTracker(t, trackerKey)
	key := trackerKey
	ix, err := e.svc.CreateIndexer(e.ctx, IndexerInput{
		Name: " Fake ", Kind: "torznab", BaseURL: tr.Srv.URL + "/", APIKey: &key, Tags: []string{"4K", " kids", "4k"},
		Categories: []int{2000, 2040},
	})
	if err != nil {
		t.Fatal(err)
	}
	if ix.Name != "Fake" || ix.Protocol != "torrent" || ix.APIPath != "/api" || ix.Priority != 25 || !ix.APIKeySet ||
		!ix.EnableRSS || !ix.EnableAutoSearch || !ix.EnableInteractive || ix.BaseURL != tr.Srv.URL ||
		strings.Join(ix.Tags, ",") != "4k,kids" || ix.Health.State != "ok" {
		t.Errorf("created = %+v", ix)
	}
	raw, _ := json.Marshal(ix)
	if strings.Contains(string(raw), trackerKey) {
		t.Fatalf("view leaks the key: %s", raw)
	}

	// Encrypted at rest, bound to the row.
	row, err := e.st.Read().GetIndexer(e.ctx, ix.ID)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(row.Secrets), trackerKey) {
		t.Fatal("API key stored in clear")
	}
	if _, err := e.box.Open(row.Secrets, []byte("indexers/999")); err == nil {
		t.Error("secret opens with another row's associated data")
	}
	if plain, err := e.box.Open(row.Secrets, []byte("indexers/"+itoa(ix.ID))); err != nil || !strings.Contains(string(plain), trackerKey) {
		t.Errorf("open with the row's AD = %v", err)
	}

	// Saved and unsaved tests.
	if r, err := e.svc.TestIndexer(e.ctx, ix.ID); err != nil || !r.OK {
		t.Errorf("TestIndexer = %+v, %v", r, err)
	}
	wrong := "WRONG"
	r, err := e.svc.TestIndexerConfig(e.ctx, 0, IndexerInput{Name: "x", Kind: "torznab", BaseURL: tr.Srv.URL, APIKey: &wrong})
	if err != nil || r.OK || !strings.Contains(r.Message, "API key") || strings.Contains(r.Message, "WRONG") {
		t.Errorf("unsaved test with a wrong key = %+v, %v", r, err)
	}
	if r, err := e.svc.TestIndexerConfig(e.ctx, ix.ID, IndexerInput{Name: "x", Kind: "torznab", BaseURL: tr.Srv.URL}); err != nil || !r.OK {
		t.Errorf("unsaved test reusing the stored key = %+v, %v", r, err)
	}

	// Update without key keeps it; with "" removes it.
	f := false
	ix, err = e.svc.UpdateIndexer(e.ctx, ix.ID, IndexerInput{Name: "Fake", Kind: "torznab", BaseURL: tr.Srv.URL, EnableRSS: &f, Priority: 3})
	if err != nil || !ix.APIKeySet || ix.EnableRSS || ix.Priority != 3 {
		t.Fatalf("update = %+v, %v", ix, err)
	}
	empty := ""
	if ix, err = e.svc.UpdateIndexer(e.ctx, ix.ID, IndexerInput{Name: "Fake", Kind: "torznab", BaseURL: tr.Srv.URL, APIKey: &empty}); err != nil || ix.APIKeySet {
		t.Fatalf("update with empty key = %+v, %v", ix, err)
	}

	// Validation and conflicts.
	for name, in := range map[string]IndexerInput{
		"kind":       {Name: "a", Kind: "rss", BaseURL: tr.Srv.URL},
		"url":        {Name: "a", Kind: "newznab", BaseURL: "ftp://x"},
		"creds":      {Name: "a", Kind: "newznab", BaseURL: "https://user:pw@x.example"},
		"query":      {Name: "a", Kind: "newznab", BaseURL: "https://x.example/?apikey=1"},
		"name":       {Name: " ", Kind: "newznab", BaseURL: "https://x.example"},
		"priority":   {Name: "a", Kind: "newznab", BaseURL: "https://x.example", Priority: 51},
		"categories": {Name: "a", Kind: "newznab", BaseURL: "https://x.example", Categories: []int{-1}},
	} {
		if _, err := e.svc.CreateIndexer(e.ctx, in); !errors.Is(err, ErrInvalid) {
			t.Errorf("%s: err = %v, want ErrInvalid", name, err)
		}
	}
	if _, err := e.svc.CreateIndexer(e.ctx, IndexerInput{Name: "Fake", Kind: "newznab", BaseURL: "https://x.example"}); !errors.Is(err, store.ErrConflict) {
		t.Errorf("duplicate name err = %v", err)
	}
	if _, err := e.svc.GetIndexer(e.ctx, 999); !errors.Is(err, store.ErrNotFound) {
		t.Errorf("get unknown = %v", err)
	}
	if err := e.svc.DeleteIndexer(e.ctx, ix.ID); err != nil {
		t.Fatal(err)
	}
	if err := e.svc.DeleteIndexer(e.ctx, ix.ID); !errors.Is(err, store.ErrNotFound) {
		t.Errorf("second delete = %v", err)
	}
}

func TestDownloadClientSettings(t *testing.T) {
	e := newEnv(t)
	qb := acquiretest.NewQBittorrent(t, qbUser, qbPass)
	c := e.addQB(qb)
	if c.Username != qbUser || !c.PasswordSet || c.APIKeySet || c.Protocol != "torrent" || !c.Enabled || !c.RemoveFailed || c.Priority != 1 {
		t.Errorf("created = %+v", c)
	}
	raw, _ := json.Marshal(c)
	if strings.Contains(string(raw), qbPass) {
		t.Fatalf("view leaks the password: %s", raw)
	}
	row, _ := e.st.Read().GetDownloadClient(e.ctx, c.ID)
	if strings.Contains(string(row.Secrets), qbPass) {
		t.Fatal("password stored in clear")
	}
	if _, err := e.box.Open(row.Secrets, []byte("download-clients/"+itoa(c.ID))); err != nil {
		t.Errorf("open with the row's AD: %v", err)
	}
	if r, err := e.svc.TestDownloadClient(e.ctx, c.ID); err != nil || !r.OK {
		t.Errorf("test saved = %+v, %v", r, err)
	}
	bad := "nope"
	in := DownloadClientInput{Name: "q", Kind: "qbittorrent", Host: "127.0.0.1", Port: int64(qb.Port()), Password: &bad}
	if r, err := e.svc.TestDownloadClientConfig(e.ctx, c.ID, in); err != nil || r.OK || !strings.Contains(r.Message, "password") || strings.Contains(r.Message, "nope") {
		t.Errorf("test unsaved with a wrong password = %+v, %v", r, err)
	}
	in.Password = nil // stored credentials
	if r, err := e.svc.TestDownloadClientConfig(e.ctx, c.ID, in); err != nil || !r.OK {
		t.Errorf("test unsaved with stored credentials = %+v, %v", r, err)
	}
	// Update keeps the password when omitted.
	off := false
	c, err := e.svc.UpdateDownloadClient(e.ctx, c.ID, DownloadClientInput{Name: "qB", Kind: "qbittorrent", Host: "127.0.0.1", Port: int64(qb.Port()), Enabled: &off})
	if err != nil || !c.PasswordSet || c.Username != qbUser || c.Enabled || c.Name != "qB" {
		t.Fatalf("update = %+v, %v", c, err)
	}
	if r, _ := e.svc.TestDownloadClient(e.ctx, c.ID); !r.OK {
		t.Errorf("test after update = %+v", r)
	}
	// Default port per kind; validation.
	sab, err := e.svc.CreateDownloadClient(e.ctx, DownloadClientInput{Name: "sab", Kind: "sabnzbd", Host: "nas.local"})
	if err != nil || sab.Port != 8080 || sab.Protocol != "usenet" {
		t.Errorf("sab = %+v, %v", sab, err)
	}
	for name, in := range map[string]DownloadClientInput{
		"kind":    {Name: "x", Kind: "utorrent", Host: "h"},
		"host":    {Name: "x", Kind: "deluge", Host: "http://h"},
		"port":    {Name: "x", Kind: "deluge", Host: "h", Port: 70000},
		"urlbase": {Name: "x", Kind: "deluge", Host: "h", URLBase: "a/../b"},
	} {
		if _, err := e.svc.CreateDownloadClient(e.ctx, in); !errors.Is(err, ErrInvalid) {
			t.Errorf("%s: err = %v, want ErrInvalid", name, err)
		}
	}
	if err := e.svc.DeleteDownloadClient(e.ctx, sab.ID); err != nil {
		t.Fatal(err)
	}
}

func TestRemotePathMappings(t *testing.T) {
	e := newEnv(t)
	m, err := e.svc.CreateRemotePathMapping(e.ctx, RemotePathMappingInput{Host: " nas ", RemotePath: `D:\Downloads`, LocalPath: "/mnt/d"})
	if err != nil || m.Host != "nas" {
		t.Fatalf("create = %+v, %v", m, err)
	}
	if _, err := e.svc.CreateRemotePathMapping(e.ctx, RemotePathMappingInput{Host: "nas", RemotePath: `D:\Downloads`, LocalPath: "/x"}); !errors.Is(err, store.ErrConflict) {
		t.Errorf("duplicate = %v", err)
	}
	for _, in := range []RemotePathMappingInput{
		{Host: "nas", RemotePath: "relative", LocalPath: "/x"},
		{Host: "nas", RemotePath: "/a", LocalPath: "relative"},
		{Host: "", RemotePath: "/a", LocalPath: "/x"},
		{Host: "nas", RemotePath: "/a/../b", LocalPath: "/x"},
	} {
		if _, err := e.svc.CreateRemotePathMapping(e.ctx, in); !errors.Is(err, ErrInvalid) {
			t.Errorf("%+v: err = %v", in, err)
		}
	}
	if m, err = e.svc.UpdateRemotePathMapping(e.ctx, m.ID, RemotePathMappingInput{Host: "nas", RemotePath: "/downloads", LocalPath: "/mnt/dl"}); err != nil || m.RemotePath != "/downloads" {
		t.Fatalf("update = %+v, %v", m, err)
	}
	mapper, err := e.svc.pathMapper(e.ctx)
	if err != nil {
		t.Fatal(err)
	}
	if got, ok := mapper.Map("NAS", "/downloads/movies/x.mkv"); !ok || got != "/mnt/dl/movies/x.mkv" {
		t.Errorf("map = %q %v", got, ok)
	}
	if list, _ := e.svc.ListRemotePathMappings(e.ctx); len(list) != 1 {
		t.Errorf("list = %+v", list)
	}
	if err := e.svc.DeleteRemotePathMapping(e.ctx, m.ID); err != nil {
		t.Fatal(err)
	}
	if err := e.svc.DeleteRemotePathMapping(e.ctx, m.ID); !errors.Is(err, store.ErrNotFound) {
		t.Errorf("second delete = %v", err)
	}
}

// TestIndexerHealthPersistence: repeated failures open the breaker; the state
// is persisted to the indexer row and restored by a new service (restart).
func TestIndexerHealthPersistence(t *testing.T) {
	e := newEnv(t)
	movie := e.addMovie()
	failing := acquiretest.NewTracker(t, trackerKey)
	failing.SetFail(500)
	ix := e.addTracker("Broken", failing)
	for range 3 {
		if _, err := e.svc.SearchMovie(e.ctx, movie.ID); err != nil {
			t.Fatal(err)
		}
	}
	got, _ := e.svc.GetIndexer(e.ctx, ix.ID)
	if got.Health.State != "disabled" || got.Health.FailureCount != 3 || got.Health.DisabledUntil == nil ||
		!got.Health.DisabledUntil.Equal(t0.Add(5*time.Minute)) || !strings.Contains(got.Health.LastError, "HTTP 500") {
		t.Fatalf("health = %+v", got.Health)
	}
	// Restart: the breaker is open again without any request.
	svc2 := e.newService(e.svc.log)
	h, ok := svc2.Search().Health(ix.ID)
	if !ok || h.State != search.Open || !h.DisabledUntil.Equal(t0.Add(5*time.Minute)) || h.ConsecutiveFailures != 3 {
		t.Fatalf("restored = %+v, %v", h, ok)
	}
	before, _ := failing.Counts()
	if _, err := svc2.SearchMovie(e.ctx, movie.ID); err != nil {
		t.Fatal(err)
	}
	if after, _ := failing.Counts(); after != before {
		t.Errorf("a paused indexer was called (%d -> %d)", before, after)
	}
	// After the pause and a successful test, it is healthy again.
	failing.SetFail(0)
	e.clk.Advance(6 * time.Minute)
	if r, err := svc2.TestIndexer(e.ctx, ix.ID); err != nil || !r.OK {
		t.Fatalf("test = %+v, %v", r, err)
	}
	got, _ = svc2.GetIndexer(e.ctx, ix.ID)
	if got.Health.State != "ok" || got.Health.FailureCount != 0 || got.Health.LastOKAt == nil {
		t.Errorf("health after test = %+v", got.Health)
	}
}
