package acquire

import (
	"errors"
	"testing"
	"time"

	"github.com/fs0ciety7000/gonema/internal/acquire/acquiretest"
	"github.com/fs0ciety7000/gonema/internal/download"
	"github.com/fs0ciety7000/gonema/internal/search"
)

func TestIndexerStates(t *testing.T) {
	e := newEnv(t)
	movie := e.addMovie()
	slow, failing := standardTrackers(t)
	ok := e.addTracker("Slow", slow)
	bad := e.addTracker("Failing", failing)
	off := false
	quiet, err := e.svc.CreateIndexer(e.ctx, IndexerInput{Name: "Quiet", Kind: "torznab", BaseURL: slow.Srv.URL,
		EnableRSS: &off, EnableAutoSearch: &off, EnableInteractive: &off})
	if err != nil {
		t.Fatal(err)
	}
	for range 3 { // the breaker opens after 3 failures
		if _, err := e.svc.SearchMovie(e.ctx, movie.ID); err != nil && !errors.Is(err, ErrUnavailable) {
			t.Fatal(err)
		}
		e.clk.Advance(13 * time.Hour)
	}
	states, err := e.svc.IndexerStates(e.ctx)
	if err != nil {
		t.Fatal(err)
	}
	got := map[int64]IndexerState{}
	for _, s := range states {
		got[s.ID] = s
	}
	if s := got[ok.ID]; !s.Enabled || s.Health.State != search.Closed || s.Health.ConsecutiveFailures != 0 {
		t.Errorf("working indexer = %+v", s)
	}
	if s := got[bad.ID]; !s.Enabled || s.Health.State != search.Open || s.Health.DisabledUntil.IsZero() {
		t.Errorf("failing indexer = %+v", s)
	}
	if s := got[quiet.ID]; s.Enabled {
		t.Errorf("indexer with everything off = %+v", s)
	}

	// A new service (restart) reports the persisted state.
	again := e.newService(nil)
	states, err = again.IndexerStates(e.ctx)
	if err != nil {
		t.Fatal(err)
	}
	for _, s := range states {
		if s.ID == bad.ID && (s.Health.State != search.Open || s.Health.ConsecutiveFailures < 3) {
			t.Errorf("restored failing indexer = %+v", s)
		}
	}
}

func TestProbeClients(t *testing.T) {
	e := newEnv(t)
	good := e.addQB(acquiretest.NewQBittorrent(t, qbUser, qbPass))
	other := acquiretest.NewQBittorrent(t, qbUser, "ANOTHER-PASSWORD")
	user, pass := qbUser, qbPass
	wrong, err := e.svc.CreateDownloadClient(e.ctx, DownloadClientInput{
		Name: "Wrong login", Kind: "qbittorrent", Host: "127.0.0.1", Port: int64(other.Port()), Username: &user, Password: &pass,
	})
	if err != nil {
		t.Fatal(err)
	}
	off := false
	disabled, err := e.svc.CreateDownloadClient(e.ctx, DownloadClientInput{
		Name: "Off", Kind: "qbittorrent", Host: "127.0.0.1", Port: 1, Enabled: &off,
	})
	if err != nil {
		t.Fatal(err)
	}
	states, err := e.svc.ProbeClients(e.ctx)
	if err != nil {
		t.Fatal(err)
	}
	got := map[int64]ClientState{}
	for _, s := range states {
		got[s.ID] = s
	}
	if s := got[good.ID]; s.Err != nil || !s.Enabled || s.Host != "127.0.0.1" || s.Protocol != "torrent" {
		t.Errorf("working client = %+v", s)
	}
	if s := got[wrong.ID]; !errors.Is(s.Err, download.ErrAuth) {
		t.Errorf("wrong login = %v", s.Err)
	}
	if s := got[disabled.ID]; s.Enabled || s.Err != nil {
		t.Errorf("disabled client = %+v", s)
	}
	c, _ := e.svc.GetDownloadClient(e.ctx, wrong.ID)
	if c.Health.FailureCount != 1 {
		t.Errorf("probe not recorded in the client health: %+v", c.Health)
	}
	assertNoSecrets(t, e.logs.String())
}
