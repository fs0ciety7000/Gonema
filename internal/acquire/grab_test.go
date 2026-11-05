package acquire

import (
	"bytes"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/fs0ciety7000/gonema/internal/acquire/acquiretest"
	"github.com/fs0ciety7000/gonema/internal/decision"
	"github.com/fs0ciety7000/gonema/internal/events"
	"github.com/fs0ciety7000/gonema/internal/indexer"
)

func TestRedactURL(t *testing.T) {
	for _, tc := range []struct {
		in, want string
		opaque   bool
	}{
		{"https://idx.example/details/1?apikey=SECRET&id=3", "https://idx.example/details/1?apikey=REDACTED&id=3", false},
		{"https://u:p@idx.example/x?passkey=ABC", "https://idx.example/x?passkey=REDACTED", false},
		{"https://t.example/rss/0123456789abcdef0123456789abcdef/dl", "https://t.example/rss/REDACTED/dl", false},
		{"magnet:?xt=urn:btih:abc", "", true},
		{"abc-123", "abc-123", true},
		{"abc-123", "", false},
		{"x?apikey=1", "", true},
		{"", "", true},
	} {
		if got := RedactURL(tc.in, tc.opaque); got != tc.want {
			t.Errorf("RedactURL(%q, %v) = %q, want %q", tc.in, tc.opaque, got, tc.want)
		}
	}
}

func TestFetchRelease(t *testing.T) {
	e := newEnv(t)
	torrent, hash := acquiretest.MakeTorrent("x")
	nzb := []byte(`<?xml version="1.0"?><!DOCTYPE nzb><nzb xmlns="http://www.newzbin.com/DTD/2003/nzb"><file/></nzb>`)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Query().Get("apikey") != "K3Y" {
			w.WriteHeader(http.StatusUnauthorized)
			return
		}
		switch r.URL.Path {
		case "/t":
			_, _ = w.Write(torrent)
		case "/n":
			_, _ = w.Write(nzb)
		case "/html":
			_, _ = w.Write([]byte("<!DOCTYPE html><html><body>Please log in</body></html>"))
		case "/magnet":
			http.Redirect(w, r, "magnet:?xt=urn:btih:"+hash, http.StatusFound)
		case "/big":
			_, _ = w.Write(bytes.Repeat([]byte("d"), maxTorrentBytes+10))
		}
	}))
	t.Cleanup(srv.Close)
	rel := func(p decision.Protocol, path string) *indexer.Release {
		return &indexer.Release{Title: "Some/Movie: 2020", Protocol: p, IndexerName: "Idx", DownloadURL: srv.URL + path + "?apikey=K3Y"}
	}

	if pl, err := e.svc.fetchRelease(e.ctx, rel(decision.Torrent, "/t")); err != nil || pl.infoHash != hash || pl.filename != "Some_Movie_ 2020.torrent" || pl.magnet != "" {
		t.Errorf("torrent = %+v, %v", pl, err)
	}
	if pl, err := e.svc.fetchRelease(e.ctx, rel(decision.Usenet, "/n")); err != nil || !bytes.Equal(pl.data, nzb) || !strings.HasSuffix(pl.filename, ".nzb") {
		t.Errorf("nzb = %+v, %v", pl, err)
	}
	if pl, err := e.svc.fetchRelease(e.ctx, rel(decision.Torrent, "/magnet")); err != nil || pl.magnet == "" || pl.infoHash != hash {
		t.Errorf("redirect to magnet = %+v, %v", pl, err)
	}
	m := &indexer.Release{Title: "M", Protocol: decision.Torrent, DownloadURL: "magnet:?xt=urn:btih:" + hash}
	if pl, err := e.svc.fetchRelease(e.ctx, m); err != nil || pl.magnet != m.DownloadURL || pl.infoHash != hash {
		t.Errorf("magnet passthrough = %+v, %v", pl, err)
	}
	// A broken .torrent link falls back to the magnet of the release.
	fb := rel(decision.Torrent, "/html")
	fb.MagnetURL = m.DownloadURL
	if pl, err := e.svc.fetchRelease(e.ctx, fb); err != nil || pl.magnet != m.DownloadURL {
		t.Errorf("magnet fallback = %+v, %v", pl, err)
	}
	for name, r := range map[string]*indexer.Release{
		"html as nzb":     rel(decision.Usenet, "/html"),
		"html as torrent": rel(decision.Torrent, "/html"),
		"too large":       rel(decision.Torrent, "/big"),
		"unauthorized":    {Title: "x", Protocol: decision.Usenet, IndexerName: "Idx", DownloadURL: srv.URL + "/n?apikey=WRONG"},
		"magnet for nzb":  rel(decision.Usenet, "/magnet"),
		"unreachable":     {Title: "x", Protocol: decision.Usenet, IndexerName: "Idx", DownloadURL: "http://127.0.0.1:1/x?apikey=K3Y"},
	} {
		_, err := e.svc.fetchRelease(e.ctx, r)
		if !errors.Is(err, ErrUpstream) {
			t.Errorf("%s: err = %v, want ErrUpstream", name, err)
			continue
		}
		if strings.Contains(err.Error(), "K3Y") || strings.Contains(err.Error(), "WRONG") || strings.Contains(err.Error(), srv.URL) {
			t.Errorf("%s: error leaks the link: %v", name, err)
		}
	}
}

func TestGrabRefusesRejectedUnlessManual(t *testing.T) {
	e := newEnv(t)
	movie := e.addMovie()
	tr := acquiretest.NewTracker(t, trackerKey, acquiretest.Item{Title: relCam, Size: 1 << 30, Seeders: 5})
	e.addTracker("T", tr)
	qb := acquiretest.NewQBittorrent(t, qbUser, qbPass)
	e.addQB(qb)
	rel := indexer.Release{Title: relCam, Protocol: decision.Torrent, IndexerName: "T", DownloadURL: tr.DownloadURL(0)}
	dec := decision.Decision{Quality: "cam", Rejections: []decision.Rejection{{Code: decision.CodeQualityNotAllowed, Message: "CAM is not allowed by profile HD-1080p", Permanent: true}}}

	_, err := e.svc.Grab(e.ctx, movie.ID, rel, dec, false)
	var refused *RefusedError
	if !errors.As(err, &refused) || !errors.Is(err, ErrRefused) || !strings.Contains(refused.UserMessage(), "CAM is not allowed") {
		t.Fatalf("automatic grab of a rejected release = %v", err)
	}
	if len(qb.Torrents()) != 0 {
		t.Fatal("refused release reached the client")
	}
	item, err := e.svc.Grab(e.ctx, movie.ID, rel, dec, true)
	if err != nil || !item.Manual || item.Status != "queued" {
		t.Fatalf("override = %+v, %v", item, err)
	}
	h := e.history(movie.ID)
	if len(h) != 1 || h[0].Event != "grab_overridden" || !strings.Contains(h[0].Data, "despite: CAM is not allowed") {
		t.Errorf("history = %+v", h)
	}
	d := e.downloads(movie.ID)[0]
	if !strings.Contains(d.Decision, `"overridden":true`) {
		t.Errorf("decision snapshot = %s", d.Decision)
	}
	// No client for Usenet: refused with a plain message and a grab_failed row.
	_, err = e.svc.Grab(e.ctx, movie.ID, indexer.Release{Title: relWeb, Protocol: decision.Usenet}, decision.Decision{Accepted: true}, false)
	if !errors.Is(err, ErrUnavailable) {
		t.Errorf("no usenet client = %v", err)
	}
	if ev := historyEvents(e.history(movie.ID)); ev[len(ev)-1] != "grab_failed" {
		t.Errorf("history = %v", ev)
	}
}

// TestInteractiveSearch: results stream as events and are kept, ranked, with
// decisions; links never leave the server; rejected releases need override.
func TestInteractiveSearch(t *testing.T) {
	e := newEnv(t)
	movie := e.addMovie()
	slow, failing := standardTrackers(t)
	e.addTracker("Slow", slow)
	e.addTracker("Broken", failing)
	qb := acquiretest.NewQBittorrent(t, qbUser, qbPass)
	e.addQB(qb)
	sub := e.bus.Subscribe(64, func(ev *events.Event) bool { return strings.HasPrefix(string(ev.Type), "search.") })
	defer sub.Close()

	snap, err := e.svc.StartInteractive(e.ctx, movie.ID)
	if err != nil {
		t.Fatal(err)
	}
	if len(snap.ID) != 32 || snap.Status != "running" || len(snap.Indexers) != 2 {
		t.Fatalf("start = %+v", snap)
	}
	var types []string
	var all bytes.Buffer
	timeout := time.After(10 * time.Second)
	for done := false; !done; {
		select {
		case ev := <-sub.C():
			types = append(types, string(ev.Type))
			raw, _ := json.Marshal(ev)
			all.Write(raw)
			done = ev.Type == EventSearchFinished
		case <-timeout:
			t.Fatalf("no search.finished; got %v", types)
		}
	}
	slices.Sort(types)
	if !slices.Equal(types, []string{"search.batch", "search.finished", "search.indexer_done", "search.indexer_failed"}) {
		t.Errorf("events = %v", types)
	}
	snap, err = e.svc.GetSearch(snap.ID)
	if err != nil {
		t.Fatal(err)
	}
	if snap.Status != "finished" || snap.FinishedAt == nil || len(snap.Results) != 3 {
		t.Fatalf("snapshot = %+v", snap)
	}
	r0, r2 := snap.Results[0], snap.Results[2]
	if r0.Title != relBluray || r0.Rank != 1 || !r0.Accepted || r0.Quality != "bluray-1080p" || r0.Score.Total != 0 ||
		r2.Title != rel720 || r2.Accepted || len(r2.Rejections) == 0 || r2.Rejections[0].Code != decision.CodeQualityNotAllowed {
		t.Errorf("results = %+v", snap.Results)
	}
	if !strings.Contains(r0.InfoURL, "apikey=REDACTED") {
		t.Errorf("info URL = %q", r0.InfoURL)
	}
	failed := 0
	for _, ix := range snap.Indexers {
		if ix.Status == "failed" && ix.Name == "Broken" && ix.Error != "" {
			failed++
		}
	}
	if failed != 1 {
		t.Errorf("indexers = %+v", snap.Indexers)
	}
	raw, _ := json.Marshal(snap)
	all.Write(raw)
	assertNoSecrets(t, all.String(), slow)

	// Rejected without override: refused; with override: grabbed.
	if _, err := e.svc.GrabFromSearch(e.ctx, snap.ID, r2.Key, false); !errors.Is(err, ErrRefused) {
		t.Fatalf("grab rejected = %v", err)
	}
	item, err := e.svc.GrabFromSearch(e.ctx, snap.ID, r2.Key, true)
	if err != nil || item.Title != rel720 || !item.Manual {
		t.Fatalf("override grab = %+v, %v", item, err)
	}
	if _, err := e.svc.GrabFromSearch(e.ctx, snap.ID, "nope", false); err == nil {
		t.Error("unknown key accepted")
	}
	if _, err := e.svc.GrabFromSearch(e.ctx, snap.ID, r0.Key, false); err != nil {
		t.Errorf("grab accepted = %v", err)
	}
	if ev := historyEvents(e.history(movie.ID)); !slices.Equal(ev, []string{"grab_overridden", "grabbed"}) {
		t.Errorf("history = %v", ev)
	}
	// Expiry.
	e.clk.Advance(16 * time.Minute)
	if _, err := e.svc.GetSearch(snap.ID); err == nil {
		t.Error("search did not expire")
	}
	assertNoSecrets(t, e.logs.String(), slow)
}

func TestRSSSync(t *testing.T) {
	e := newEnv(t)
	movie := e.addMovie()
	tr := acquiretest.NewTracker(t, trackerKey,
		acquiretest.Item{Title: "Unrelated.Movie.2020.1080p.BluRay.x264-GRP", Size: 9 << 30, Seeders: 9},
		acquiretest.Item{Title: relWeb, Size: 6 << 30, Seeders: 20},
	)
	ix := e.addTracker("T", tr)
	qb := acquiretest.NewQBittorrent(t, qbUser, qbPass)
	e.addQB(qb)

	if err := e.svc.SyncRSS(e.ctx); err != nil {
		t.Fatal(err)
	}
	dls := e.downloads(movie.ID)
	if len(dls) != 1 || dls[0].ReleaseTitle != relWeb {
		t.Fatalf("downloads = %+v", dls)
	}
	h := e.history(movie.ID)
	if len(h) != 1 || !strings.Contains(h[0].Data, "New RSS release from T") {
		t.Errorf("history = %+v", h)
	}
	st, err := e.st.Read().GetRSSState(e.ctx, ix.ID)
	if err != nil || st.LastGuid == nil || st.LastSyncAt == nil {
		t.Fatalf("rss state = %+v, %v", st, err)
	}
	// Same feed: nothing new. A better release appears: grabbed (upgrade of
	// the queued one).
	if err := e.svc.SyncRSS(e.ctx); err != nil {
		t.Fatal(err)
	}
	if n := len(e.downloads(movie.ID)); n != 1 {
		t.Errorf("%d downloads after an unchanged feed", n)
	}
	tr.Add(acquiretest.Item{Title: relBluray, Size: 10 << 30, Seeders: 30, Pub: time.Date(2026, 11, 2, 0, 0, 0, 0, time.UTC)})
	if err := e.svc.SyncRSS(e.ctx); err != nil {
		t.Fatal(err)
	}
	if dls := e.downloads(movie.ID); len(dls) != 2 || dls[1].ReleaseTitle != relBluray {
		t.Errorf("downloads = %+v", dls)
	}
	// A worse one is ignored: the queued Bluray covers it.
	tr.Add(acquiretest.Item{Title: "Fight.Club.1999.1080p.WEBRip.x264-LESSER", Size: 5 << 30, Seeders: 30, Pub: time.Date(2026, 11, 3, 0, 0, 0, 0, time.UTC)})
	if err := e.svc.SyncRSS(e.ctx); err != nil {
		t.Fatal(err)
	}
	if n := len(e.downloads(movie.ID)); n != 2 {
		t.Errorf("%d downloads after a worse release", n)
	}
}

func TestSearchMissingAndSkips(t *testing.T) {
	e := newEnv(t)
	movie := e.addMovie()
	tr := acquiretest.NewTracker(t, trackerKey, acquiretest.Item{Title: relWeb, Size: 6 << 30, Seeders: 20})
	e.addTracker("T", tr)
	qb := acquiretest.NewQBittorrent(t, qbUser, qbPass)
	e.addQB(qb)
	if err := e.svc.SearchMissing(e.ctx); err != nil {
		t.Fatal(err)
	}
	if n := len(e.downloads(movie.ID)); n != 1 {
		t.Fatalf("%d downloads after the missing search", n)
	}
	// Downloading now: not missing anymore, no new search.
	before, _ := tr.Counts()
	if err := e.svc.SearchMissing(e.ctx); err != nil {
		t.Fatal(err)
	}
	if after, _ := tr.Counts(); after != before {
		t.Errorf("searched again (%d -> %d)", before, after)
	}
	// An equal release already queued: an automatic search grabs nothing.
	out, err := e.svc.SearchMovie(e.ctx, movie.ID)
	if err != nil || out.Grabbed != nil || out.Accepted != 0 {
		t.Errorf("second search = %+v, %v", out, err)
	}
	// Not monitored: no search at all.
	if _, err := e.st.Write().SetMovieMonitored(e.ctx, dbMonitored(movie.ID, false)); err != nil {
		t.Fatal(err)
	}
	if out, err := e.svc.SearchMovie(e.ctx, movie.ID); err != nil || out.Searched {
		t.Errorf("unmonitored search = %+v, %v", out, err)
	}
}
