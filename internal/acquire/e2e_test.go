package acquire

import (
	"encoding/json"
	"slices"
	"strings"
	"testing"

	"github.com/fs0ciety7000/gonema/internal/acquire/acquiretest"
	"github.com/fs0ciety7000/gonema/internal/store/db"
)

// TestEndToEndGrabAndImport: add a movie (fake TMDB), search two indexers (one
// slow, one failing), evaluate, grab the best release to a fake qBittorrent,
// see it complete, import it through the remote path mapping.
func TestEndToEndGrabAndImport(t *testing.T) {
	e := newEnv(t)
	movie := e.addMovie()
	slow, failing := standardTrackers(t)
	e.addTracker("Slow", slow)
	bad := e.addTracker("Broken", failing)
	qb := acquiretest.NewQBittorrent(t, qbUser, qbPass)
	e.addQB(qb)
	if _, err := e.svc.CreateRemotePathMapping(e.ctx, RemotePathMappingInput{Host: "127.0.0.1", RemotePath: "/downloads", LocalPath: "/mnt/nas/downloads"}); err != nil {
		t.Fatal(err)
	}

	out, err := e.svc.SearchMovie(e.ctx, movie.ID)
	if err != nil {
		t.Fatal(err)
	}
	if !out.Searched || out.Results != 3 || out.Accepted != 2 || out.Grabbed == nil || out.Grabbed.Title != relBluray {
		t.Fatalf("search outcome = %+v", out)
	}
	if _, downloads := slow.Counts(); downloads != 1 {
		t.Errorf(".torrent fetched %d times, want 1 (server side)", downloads)
	}
	ts := qb.Torrents()
	want := slow.Find(relBluray).Hash()
	if len(ts) != 1 || ts[0].Hash != want || ts[0].Category != "movies" || ts[0].Name != "file" {
		t.Fatalf("qBittorrent torrents = %+v, want the Bluray file (%s)", ts, want)
	}
	dls := e.downloads(movie.ID)
	if len(dls) != 1 || dls[0].Status != "queued" || deref(dls[0].ClientRef) != want || dls[0].Quality != "bluray-1080p" || dls[0].Manual {
		t.Fatalf("downloads = %+v", dls)
	}
	var snap map[string]any
	if err := json.Unmarshal([]byte(dls[0].Decision), &snap); err != nil || snap["accepted"] != true || snap["indexer"] != "Slow" {
		t.Errorf("decision snapshot = %s (%v)", dls[0].Decision, err)
	}
	// The failing indexer is recorded, not fatal.
	ix, err := e.svc.GetIndexer(e.ctx, bad.ID)
	if err != nil || ix.Health.FailureCount != 1 || ix.Health.LastError == "" || ix.Health.State != "failing" {
		t.Errorf("failing indexer health = %+v, %v", ix.Health, err)
	}

	// Downloading, then completed in the client.
	if n, err := e.svc.TrackOnce(e.ctx); err != nil || n != 1 {
		t.Fatalf("TrackOnce = %d, %v", n, err)
	}
	if q, _ := e.svc.Queue(e.ctx); len(q) != 1 || q[0].Status != "downloading" || q[0].SpeedBps == 0 || q[0].Client != "qBittorrent" {
		t.Errorf("queue = %+v", q)
	}
	qb.Set(want, "uploading", 1, "/downloads/movies/"+relBluray)
	if _, err := e.svc.TrackOnce(e.ctx); err != nil {
		t.Fatal(err)
	}
	e.waitImport(dls[0].ID)
	reqs := e.imp.requests()
	if len(reqs) != 1 || reqs[0].LocalPath != "/mnt/nas/downloads/movies/"+relBluray || !reqs[0].ClientSeeding ||
		reqs[0].MovieID != movie.ID || reqs[0].DownloadID != dls[0].ID || reqs[0].ReleaseTitle != relBluray {
		t.Fatalf("import requests = %+v", reqs)
	}
	d, err := e.st.Read().GetDownload(e.ctx, dls[0].ID)
	if err != nil || d.Status != "imported" || d.ImportedAt == nil || deref(d.OutputPath) != reqs[0].LocalPath || d.Progress != 1 {
		t.Fatalf("download after import = %+v, %v", d, err)
	}
	if q, _ := e.svc.Queue(e.ctx); len(q) != 0 {
		t.Errorf("queue after import = %+v", q)
	}
	if got := historyEvents(e.history(movie.ID)); !slices.Equal(got, []string{"grabbed", "imported"}) {
		t.Errorf("history = %v, want grabbed then imported", got)
	}
	assertNoSecrets(t, e.logs.String(), slow)
}

// TestFailureBlocklistAndRetry: the client reports the download failed; the
// release is blocklisted, the next best is grabbed with retry history; when no
// other release is left, retries stop with a reason.
func TestFailureBlocklistAndRetry(t *testing.T) {
	e := newEnv(t)
	movie := e.addMovie()
	slow, failing := standardTrackers(t)
	e.addTracker("Slow", slow)
	e.addTracker("Broken", failing)
	qb := acquiretest.NewQBittorrent(t, qbUser, qbPass)
	e.addQB(qb)

	if _, err := e.svc.SearchMovie(e.ctx, movie.ID); err != nil {
		t.Fatal(err)
	}
	first := e.downloads(movie.ID)[0]
	qb.Set(deref(first.ClientRef), "error", 0.4, "")
	if _, err := e.svc.TrackOnce(e.ctx); err != nil {
		t.Fatal(err)
	}

	dls := e.downloads(movie.ID)
	if len(dls) != 2 || dls[0].Status != "failed" || dls[1].ReleaseTitle != relWeb || deref(dls[1].RetryOf) != first.ID || dls[1].Status != "queued" {
		t.Fatalf("downloads after failure = %+v", dls)
	}
	if del, files := qb.Deleted(deref(first.ClientRef)); !del || files {
		t.Errorf("failed torrent removed from client = %v (with files %v), want removed without files", del, files)
	}
	bl, err := e.svc.ListBlocklist(e.ctx, BlocklistFilter{MovieID: movie.ID})
	if err != nil || len(bl) != 1 || bl[0].SourceTitle != relBluray || bl[0].Kind != "release" || deref(bl[0].InfoHash) != deref(first.ClientRef) ||
		deref(bl[0].ReleaseGroup) != "GRP" || !strings.Contains(bl[0].Reason, "qBittorrent reported an error") {
		t.Fatalf("blocklist = %+v, %v", bl, err)
	}
	hist := e.history(movie.ID)
	if got := historyEvents(hist); !slices.Equal(got, []string{"grabbed", "download_failed", "blocklisted", "retry_scheduled", "grabbed"}) {
		t.Fatalf("history = %v", got)
	}
	if !strings.Contains(hist[3].Data, "Trying release #2 of 6") || hist[3].SourceTitle != relWeb {
		t.Errorf("retry history = %s %s", hist[3].SourceTitle, hist[3].Data)
	}
	if hist[4].DownloadID == nil || *hist[4].DownloadID != dls[1].ID || !strings.Contains(hist[4].Data, "Trying release #2 of 6 after the failure of "+relBluray) {
		t.Errorf("retry grab history = %+v", hist[4])
	}

	// The second release fails too: no accepted release is left.
	qb.Set(deref(dls[1].ClientRef), "missingFiles", 0.1, "")
	if _, err := e.svc.TrackOnce(e.ctx); err != nil {
		t.Fatal(err)
	}
	hist = e.history(movie.ID)
	got := historyEvents(hist)
	if !slices.Equal(got[5:], []string{"download_failed", "blocklisted", "retry_scheduled", "retries_exhausted"}) {
		t.Fatalf("history after second failure = %v", got)
	}
	if last := hist[len(hist)-1]; !strings.Contains(last.Data, "No other acceptable release found") {
		t.Errorf("exhausted reason = %s", last.Data)
	}
	if n := len(e.downloads(movie.ID)); n != 2 {
		t.Errorf("%d downloads, want 2 (nothing left to try)", n)
	}
	assertNoSecrets(t, e.logs.String(), slow)
}

func TestRetryBudget(t *testing.T) {
	e := newEnv(t)
	movie := e.addMovie()
	slow, _ := standardTrackers(t)
	e.addTracker("Slow", slow)
	qb := acquiretest.NewQBittorrent(t, qbUser, qbPass)
	e.addQB(qb)
	if err := setSetting(e, SettingMaxAttempts, 1); err != nil {
		t.Fatal(err)
	}
	if _, err := e.svc.SearchMovie(e.ctx, movie.ID); err != nil {
		t.Fatal(err)
	}
	first := e.downloads(movie.ID)[0]
	qb.Set(deref(first.ClientRef), "error", 0, "")
	if _, err := e.svc.TrackOnce(e.ctx); err != nil {
		t.Fatal(err)
	}
	hist := e.history(movie.ID)
	if last := hist[len(hist)-1]; last.Event != "retries_exhausted" || !strings.Contains(last.Data, "Tried 1 releases in the last 24 hours") {
		t.Fatalf("last history = %s %s", last.Event, last.Data)
	}
	// A day later the budget is back.
	e.clk.Advance(25 * 60 * 60 * 1e9)
	if err := e.svc.Retry(e.ctx, first.ID); err != nil {
		t.Fatal(err)
	}
	if dls := e.downloads(movie.ID); len(dls) != 2 || dls[1].ReleaseTitle != relWeb {
		t.Fatalf("downloads = %+v", dls)
	}
}

// TestImportRefusedIsAFailure: the importer refuses the content (fake): the
// release is blocklisted (group too, per setting) and the next one grabbed.
func TestImportRefusedIsAFailure(t *testing.T) {
	e := newEnv(t)
	movie := e.addMovie()
	slow, _ := standardTrackers(t)
	e.addTracker("Slow", slow)
	qb := acquiretest.NewQBittorrent(t, qbUser, qbPass)
	e.addQB(qb)
	if err := setSetting(e, SettingBlocklistGroup, true); err != nil {
		t.Fatal(err)
	}
	e.imp.result = ImportResult{Reason: "the file is a fake (sample of 12 MB)"}
	if _, err := e.svc.SearchMovie(e.ctx, movie.ID); err != nil {
		t.Fatal(err)
	}
	first := e.downloads(movie.ID)[0]
	qb.Set(deref(first.ClientRef), "stalledUP", 1, "/downloads/movies/x")
	if _, err := e.svc.TrackOnce(e.ctx); err != nil {
		t.Fatal(err)
	}
	e.waitImport(first.ID)
	dls := e.downloads(movie.ID)
	if len(dls) != 2 || dls[0].Status != "failed" || dls[1].ReleaseTitle != relWeb {
		t.Fatalf("downloads = %+v", dls)
	}
	bl, _ := e.svc.ListBlocklist(e.ctx, BlocklistFilter{MovieID: movie.ID})
	kinds := []string{}
	for _, b := range bl {
		kinds = append(kinds, b.Kind)
	}
	if !slices.Equal(kinds, []string{"group", "release"}) || !strings.Contains(bl[1].Reason, "fake") {
		t.Errorf("blocklist = %+v", bl)
	}
}

func TestImportErrorKeepsDownloadWithWarning(t *testing.T) {
	e := newEnv(t)
	movie := e.addMovie()
	slow, _ := standardTrackers(t)
	e.addTracker("Slow", slow)
	qb := acquiretest.NewQBittorrent(t, qbUser, qbPass)
	e.addQB(qb)
	e.imp.err = errString("permission denied on /movies")
	if _, err := e.svc.SearchMovie(e.ctx, movie.ID); err != nil {
		t.Fatal(err)
	}
	d := e.downloads(movie.ID)[0]
	qb.Set(deref(d.ClientRef), "uploading", 1, "/data/x")
	if _, err := e.svc.TrackOnce(e.ctx); err != nil {
		t.Fatal(err)
	}
	e.waitImport(d.ID)
	got, _ := e.st.Read().GetDownload(e.ctx, d.ID)
	if got.Status != "warning" || !strings.Contains(got.StatusMessages, "permission denied") {
		t.Errorf("download = %s %s", got.Status, got.StatusMessages)
	}
	if ev := historyEvents(e.history(movie.ID)); !slices.Equal(ev, []string{"grabbed", "import_failed"}) {
		t.Errorf("history = %v", ev)
	}
	if n := len(e.downloads(movie.ID)); n != 1 {
		t.Errorf("an import error must not trigger a retry: %d downloads", n)
	}
}

type errString string

func (e errString) Error() string { return string(e) }

func setSetting(e *env, key string, v any) error {
	raw, err := json.Marshal(v)
	if err != nil {
		return err
	}
	return e.st.Write().SetSetting(e.ctx, db.SetSettingParams{Key: key, Value: string(raw)})
}

// assertNoSecrets fails when s contains a credential or a download link.
func assertNoSecrets(t *testing.T, s string, trackers ...*acquiretest.Tracker) {
	t.Helper()
	for _, secret := range []string{trackerKey, "OTHERKEY", qbPass, "/dl/", "magnet:?", "session-" + qbUser} {
		if strings.Contains(s, secret) {
			i := strings.Index(s, secret)
			t.Errorf("leak of %q: ...%s...", secret, s[max(0, i-120):min(len(s), i+80)])
		}
	}
	for _, tr := range trackers {
		if tr != nil && strings.Contains(s, tr.DownloadURL(0)) {
			t.Errorf("download link leaked")
		}
	}
}

// TestImporterWritesHistory: with an importer that records its own outcome,
// the tracker does not duplicate the imported row.
func TestImporterWritesHistory(t *testing.T) {
	e := newEnv(t)
	e.svc.impHist = true
	movie := e.addMovie()
	slow, _ := standardTrackers(t)
	e.addTracker("Slow", slow)
	qb := acquiretest.NewQBittorrent(t, qbUser, qbPass)
	e.addQB(qb)
	if _, err := e.svc.SearchMovie(e.ctx, movie.ID); err != nil {
		t.Fatal(err)
	}
	d := e.downloads(movie.ID)[0]
	qb.Set(deref(d.ClientRef), "uploading", 1, "/data/x")
	if _, err := e.svc.TrackOnce(e.ctx); err != nil {
		t.Fatal(err)
	}
	e.waitImport(d.ID)
	if got, _ := e.st.Read().GetDownload(e.ctx, d.ID); got.Status != "imported" {
		t.Errorf("status = %s", got.Status)
	}
	if ev := historyEvents(e.history(movie.ID)); !slices.Equal(ev, []string{"grabbed"}) {
		t.Errorf("history = %v, want the importer's row only (none from the fake)", ev)
	}
}
