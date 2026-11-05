package acquire

import (
	"encoding/json"
	"errors"
	"slices"
	"testing"

	"github.com/fs0ciety7000/gonema/internal/acquire/acquiretest"
	"github.com/fs0ciety7000/gonema/internal/library"
	"github.com/fs0ciety7000/gonema/internal/quality"
	"github.com/fs0ciety7000/gonema/internal/store"
	"github.com/fs0ciety7000/gonema/internal/store/db"
)

// failedImportEnv grabs the Bluray release of Fight Club, completes it in
// qBittorrent at /data/x and lets the import fail: the download waits in
// warning.
func failedImportEnv(t *testing.T) (*env, db.Movie, db.Download) {
	t.Helper()
	e := newEnv(t)
	movie := e.addMovie()
	slow, _ := standardTrackers(t)
	e.addTracker("Slow", slow)
	qb := acquiretest.NewQBittorrent(t, qbUser, qbPass)
	e.addQB(qb)
	e.imp.mu.Lock()
	e.imp.err = errString("/data/x does not exist")
	e.imp.mu.Unlock()
	if _, err := e.svc.SearchMovie(e.ctx, movie.ID); err != nil {
		t.Fatal(err)
	}
	d := e.downloads(movie.ID)[0]
	qb.Set(deref(d.ClientRef), "uploading", 1, "/data/x")
	if _, err := e.svc.TrackOnce(e.ctx); err != nil {
		t.Fatal(err)
	}
	e.waitImport(d.ID)
	got, err := e.st.Read().GetDownload(e.ctx, d.ID)
	if err != nil || got.Status != "warning" {
		t.Fatalf("download = %+v, %v", got, err)
	}
	return e, movie, got
}

func TestFailedImportIsNotRetriedAutomatically(t *testing.T) {
	e, movie, d := failedImportEnv(t)
	for range 3 {
		if _, err := e.svc.TrackOnce(e.ctx); err != nil {
			t.Fatal(err)
		}
	}
	if n := len(e.imp.requests()); n != 1 {
		t.Errorf("%d import attempts, want 1: a failed import waits for the user", n)
	}
	if got, _ := e.st.Read().GetDownload(e.ctx, d.ID); got.Status != "warning" {
		t.Errorf("status = %s", got.Status)
	}
	if ev := historyEvents(e.history(movie.ID)); !slices.Equal(ev, []string{"grabbed", "import_failed"}) {
		t.Errorf("history = %v", ev)
	}
}

func TestRetryImportAfterPathMapping(t *testing.T) {
	e, movie, d := failedImportEnv(t)
	if _, err := e.svc.CreateRemotePathMapping(e.ctx, RemotePathMappingInput{Host: "127.0.0.1", RemotePath: "/data/", LocalPath: "/mnt/nas/data/"}); err != nil {
		t.Fatal(err)
	}
	e.imp.mu.Lock()
	e.imp.err = nil
	e.imp.mu.Unlock()

	item, err := e.svc.RetryImport(e.ctx, d.ID, 0)
	if err != nil {
		t.Fatal(err)
	}
	if item.Status != "importing" || item.MovieID != movie.ID {
		t.Errorf("item = %+v", item)
	}
	e.waitImport(d.ID)
	reqs := e.imp.requests()
	if last := reqs[len(reqs)-1]; last.LocalPath != "/mnt/nas/data/x" || last.MovieID != movie.ID || last.DownloadID != d.ID {
		t.Errorf("import request = %+v, want the newly mapped path", last)
	}
	if got, _ := e.st.Read().GetDownload(e.ctx, d.ID); got.Status != "imported" || deref(got.OutputPath) != "/mnt/nas/data/x" {
		t.Errorf("download = %s %v", got.Status, deref(got.OutputPath))
	}
	rows := e.history(movie.ID)
	if ev := historyEvents(rows); !slices.Equal(ev, []string{"grabbed", "import_failed", HistoryRetryScheduled, "imported"}) {
		t.Fatalf("history = %v", ev)
	}
	var data map[string]any
	if err := json.Unmarshal([]byte(rows[2].Data), &data); err != nil {
		t.Fatal(err)
	}
	if data["manual"] != true || data["kind"] != "import" || data["reason"] != "Import retried by the user." || data["path"] != "/mnt/nas/data/x" {
		t.Errorf("retry history data = %v", data)
	}
	if deref(rows[2].DownloadID) != d.ID {
		t.Errorf("retry history download = %v", rows[2].DownloadID)
	}
}

func TestRetryImportForAnotherMovie(t *testing.T) {
	e, movie, d := failedImportEnv(t)
	other := *fightClub()
	other.TMDBID, other.IMDbID, other.Title, other.OriginalTitle = 551, "tt0000551", "Fight Club Redux", "Fight Club Redux"
	e.prov.movies[551] = &other
	m2, _, err := e.lib.AddMovie(e.ctx, 551, library.AddOptions{RootFolderID: e.rootID, QualityProfileID: e.hdID, Monitored: true})
	if err != nil {
		t.Fatal(err)
	}
	e.imp.mu.Lock()
	e.imp.err = nil
	e.imp.mu.Unlock()

	if _, err := e.svc.RetryImport(e.ctx, d.ID, 99999); !errors.Is(err, store.ErrNotFound) {
		t.Fatalf("unknown movie = %v", err)
	}
	item, err := e.svc.RetryImport(e.ctx, d.ID, m2.ID)
	if err != nil {
		t.Fatal(err)
	}
	if item.MovieID != m2.ID {
		t.Errorf("item movie = %d", item.MovieID)
	}
	e.waitImport(d.ID)
	reqs := e.imp.requests()
	if last := reqs[len(reqs)-1]; last.MovieID != m2.ID {
		t.Errorf("import request movie = %d, want %d", last.MovieID, m2.ID)
	}
	if got, _ := e.st.Read().GetDownload(e.ctx, d.ID); got.MovieID != m2.ID || got.Status != "imported" {
		t.Errorf("download = movie %d %s", got.MovieID, got.Status)
	}
	rows := e.history(m2.ID)
	if ev := historyEvents(rows); len(ev) == 0 || ev[len(ev)-2] != HistoryRetryScheduled {
		t.Fatalf("history of the new movie = %v", ev)
	}
	var data map[string]any
	if err := json.Unmarshal([]byte(rows[len(rows)-2].Data), &data); err != nil {
		t.Fatal(err)
	}
	if data["previousMovieId"] != float64(movie.ID) {
		t.Errorf("retry data = %v", data)
	}
}

func TestRetryImportRefusals(t *testing.T) {
	e, qb, movie, _ := grabbedEnv(t)
	_ = qb
	d := e.downloads(movie.ID)[0]
	if _, err := e.svc.RetryImport(e.ctx, d.ID, 0); !errors.Is(err, ErrInvalid) {
		t.Errorf("retry of a download in progress = %v", err)
	}
	if _, err := e.svc.RetryImport(e.ctx, 424242, 0); !errors.Is(err, store.ErrNotFound) {
		t.Errorf("retry of an unknown download = %v", err)
	}
	e.svc.importer = nil
	if _, err := e.svc.RetryImport(e.ctx, d.ID, 0); !errors.Is(err, ErrUnavailable) {
		t.Errorf("retry without importer = %v", err)
	}
}

// TestCutoffInGroupLoadsForSearch: a profile whose cutoff is stored as a
// member of a group is usable by the searches, with the group's rank.
func TestCutoffInGroupLoadsForSearch(t *testing.T) {
	e := newEnv(t)
	p, err := e.st.CreateQualityProfile(e.ctx, db.CreateQualityProfileParams{
		Name: "Grouped", UpgradeAllowed: true, CutoffQuality: string(quality.WebRip1080p), MinUpgradeStep: 1, Languages: "[]",
	}, []store.ProfileItem{
		{Quality: string(quality.Bluray1080p), Position: 0, Allowed: true},
		{Quality: string(quality.WebDL1080p), Position: 1, GroupName: "WEB 1080p", Allowed: true},
		{Quality: string(quality.WebRip1080p), Position: 1, GroupName: "WEB 1080p", Allowed: true},
	})
	if err != nil {
		t.Fatal(err)
	}
	m, _, err := e.lib.AddMovie(e.ctx, 550, library.AddOptions{RootFolderID: e.rootID, QualityProfileID: p.ID, Monitored: true})
	if err != nil {
		t.Fatal(err)
	}
	mc, err := e.svc.loadMovie(e.ctx, m.ID, nil, e.svc.searchOptions(e.ctx, false))
	if err != nil {
		t.Fatal(err)
	}
	if mc.cfg.Profile.Cutoff != string(quality.WebRip1080p) || mc.cfg.Profile.CutoffRank() != 1 || mc.cfg.Profile.CutoffLabel() != "WEB 1080p" {
		t.Errorf("profile cutoff %q rank %d label %q", mc.cfg.Profile.Cutoff, mc.cfg.Profile.CutoffRank(), mc.cfg.Profile.CutoffLabel())
	}
	slow, _ := standardTrackers(t)
	e.addTracker("Slow", slow)
	e.addQB(acquiretest.NewQBittorrent(t, qbUser, qbPass))
	out, err := e.svc.SearchMovie(e.ctx, m.ID)
	if err != nil {
		t.Fatal(err)
	}
	if out.Grabbed == nil || out.Grabbed.Title != relBluray {
		t.Errorf("search = %+v, want the Bluray release grabbed", out)
	}
}
