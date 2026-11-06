package health

import (
	"fmt"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/fs0ciety7000/gonema/internal/store"
	"github.com/fs0ciety7000/gonema/internal/store/db"
	"github.com/fs0ciety7000/gonema/internal/store/storetest"
)

type storeEnv struct {
	st  *store.Store
	lib storetest.Library
}

func newStoreEnv(t *testing.T) *storeEnv {
	t.Helper()
	st := storetest.New(t)
	return &storeEnv{st: st, lib: storetest.SeedLibrary(t, st)}
}

func (e *storeEnv) movie(t *testing.T, tmdbID int64) db.Movie {
	t.Helper()
	title := fmt.Sprintf("Movie %d", tmdbID)
	m, err := e.st.Write().InsertMovie(t.Context(), db.InsertMovieParams{
		TmdbID: tmdbID, Title: title, OriginalTitle: title, SortTitle: title, CleanTitle: title, OriginalLanguage: "en",
		Year: 2001, Status: "released", Genres: "[]", Ratings: "{}", Extra: "{}", Monitored: true,
		QualityProfileID: e.lib.ProfileID, MinimumAvailability: "released", RootFolderID: e.lib.RootFolderID,
		Path: title, AddedAt: 1_700_000_000_000,
	})
	if err != nil {
		t.Fatalf("insert movie: %v", store.MapError(err))
	}
	return m
}

func (e *storeEnv) movieWithFile(t *testing.T, tmdbID, size int64) {
	t.Helper()
	m := e.movie(t, tmdbID)
	if _, err := e.st.Write().UpsertMovieFile(t.Context(), db.UpsertMovieFileParams{
		MovieID: m.ID, RelativePath: "f.mkv", Size: size, Quality: "bluray-1080p", Revision: 1, Languages: "[]",
		Parsed: "{}", MediaInfo: "{}", ImportedAt: 1,
	}); err != nil {
		t.Fatalf("insert file: %v", err)
	}
}

// download inserts a download in the given state.
func (e *storeEnv) download(t *testing.T, movieID, clientID int64, title, status, path string, completed bool, msg string) db.Download {
	t.Helper()
	ctx := t.Context()
	ref := strings.ToLower(title)
	d, err := e.st.Write().InsertDownload(ctx, db.InsertDownloadParams{
		MovieID: movieID, ClientID: &clientID, ClientRef: &ref, ReleaseTitle: title, Protocol: "torrent",
		Quality: "bluray-1080p", Revision: 1, Decision: "{}", GrabbedAt: store.Millis(t0.Add(-5 * time.Hour)),
	})
	if err != nil {
		t.Fatal(err)
	}
	p := db.UpdateDownloadStateParams{ID: d.ID, Status: status, StatusMessages: "[]"}
	if path != "" {
		p.OutputPath = &path
	}
	if completed {
		at := store.Millis(t0.Add(-4 * time.Hour))
		p.CompletedAt, p.Progress = &at, 1
	}
	if msg != "" {
		p.StatusMessages = fmt.Sprintf("[%q]", msg)
	}
	if d, err = e.st.Write().UpdateDownloadState(ctx, p); err != nil {
		t.Fatal(err)
	}
	return d
}

func (e *storeEnv) importFailed(t *testing.T, d db.Download, at time.Time) {
	t.Helper()
	if _, err := e.st.Write().AppendHistory(t.Context(), db.AppendHistoryParams{
		MovieID: &d.MovieID, Event: "import_failed", SourceTitle: d.ReleaseTitle, DownloadID: &d.ID, Data: "{}", CreatedAt: store.Millis(at),
	}); err != nil {
		t.Fatal(err)
	}
}

func TestDownloadsCheck(t *testing.T) {
	e := newStoreEnv(t)
	ctx := t.Context()
	qb, err := e.st.Write().CreateDownloadClient(ctx, db.CreateDownloadClientParams{
		Name: "qBittorrent", Kind: "qbittorrent", Protocol: "torrent", Host: "qbit", Port: 8080, Enabled: true, Tags: "[]", Priority: 1,
	})
	if err != nil {
		t.Fatal(err)
	}
	sab, err := e.st.Write().CreateDownloadClient(ctx, db.CreateDownloadClientParams{
		Name: "SABnzbd", Kind: "sabnzbd", Protocol: "usenet", Host: "sab", Port: 8080, Enabled: true, Tags: "[]", Priority: 1,
	})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := e.st.Write().CreateRemotePathMapping(ctx, db.CreateRemotePathMappingParams{Host: "sab", RemotePath: "/complete/", LocalPath: "/mnt/sab/"}); err != nil {
		t.Fatal(err)
	}
	m := e.movie(t, 1)

	// qBittorrent: two finished downloads at paths Gonema can't see, no mapping.
	e.download(t, m.ID, qb.ID, "Fight.Club.1999.1080p.BluRay.x264-GRP", "completed", "/downloads/complete/Fight.Club.1999.1080p.BluRay.x264-GRP", true, "")
	lost := e.download(t, m.ID, qb.ID, "Heat.1995.1080p.BluRay.x264-GRP", "warning", "/downloads/complete/Heat.1995", true, "Import failed: no such file")
	e.importFailed(t, lost, t0.Add(-3*time.Hour))
	// SABnzbd: a mapping applies but its local side is missing.
	e.download(t, m.ID, sab.ID, "Alien.1979.1080p.WEB-DL", "importing", "/mnt/sab/Alien.1979", true, "")
	// A visible download whose import failed 2 hours ago: stuck.
	stuck := e.download(t, m.ID, qb.ID, "Up.2009.1080p.BluRay.x264-GRP", "warning", "/data/Up.2009", true,
		"Import failed: permission denied on /movies/Up (2009).")
	e.importFailed(t, stuck, t0.Add(-2*time.Hour))
	// A visible download whose import failed 10 minutes ago: not yet.
	fresh := e.download(t, m.ID, qb.ID, "Big.Fish.2003.1080p", "warning", "/data/Big.Fish", true, "Import failed: disk full")
	e.importFailed(t, fresh, t0.Add(-10*time.Minute))
	// Still downloading (client warning, not completed): ignored.
	e.download(t, m.ID, qb.ID, "Stalled.2020.1080p", "warning", "/downloads/incomplete/Stalled", false, "stalled: no connection to peers")

	fsys := &fakeFS{stat: map[string]error{"/data/Up.2009": nil, "/data/Big.Fish": nil}}
	got, err := DownloadsCheck(e.st, fsys, fixed(t0)).Run(ctx)
	if err != nil {
		t.Fatal(err)
	}
	want := []string{
		fmt.Sprintf("download.path_unreachable:%d", qb.ID), fmt.Sprintf("download.path_unreachable:%d", sab.ID),
		fmt.Sprintf("download.import_stuck:%d", stuck.ID),
	}
	if !slices.Equal(ids(got), want) {
		t.Fatalf("issues = %v\nwant %v", ids(got), want)
	}
	wellFormed(t, got)

	q := got[0]
	wantMsg := "qBittorrent finished Fight.Club.1999.1080p.BluRay.x264-GRP in /downloads/complete/Fight.Club.1999.1080p.BluRay.x264-GRP, " +
		"but that path doesn't exist for Gonema (2 downloads are waiting), so it can't be imported. Gonema and qBittorrent probably " +
		"run in separate containers or machines that see the same folder under different paths. The downloads are kept until then."
	if q.Message != wantMsg || q.Severity != Error || q.Title != "Gonema can't see qBittorrent's finished downloads" {
		t.Errorf("path issue:\n got %q\nwant %q", q.Message, wantMsg)
	}
	if !strings.Contains(q.Fix[0], "host qbit with remote path /downloads/complete/") || q.Link != "/settings/download-clients/remote-path-mappings" {
		t.Errorf("suggestion = %q (%s)", q.Fix[0], q.Link)
	}
	s := got[1]
	if !strings.Contains(s.Message, "The remote path mapping /complete/ → /mnt/sab/ applies") || !strings.Contains(s.Fix[0], "/mnt/sab/ exists") {
		t.Errorf("mapped path issue = %q / %v", s.Message, s.Fix)
	}
	st := got[2]
	wantStuck := "Its import failed 2 hours ago: permission denied on /movies/Up (2009). The download stays in the queue; " +
		"Gonema doesn't retry on its own, so nothing is repeated or lost."
	if st.Message != wantStuck || st.Severity != Warning || st.Title != "Up.2009.1080p.BluRay.x264-GRP is waiting to be imported" ||
		!st.Since.Equal(t0.Add(-2*time.Hour)) || st.Link != "/queue" {
		t.Errorf("stuck:\n got %q (%s, %v)\nwant %q", st.Message, st.Severity, st.Since, wantStuck)
	}

	// Once Gonema sees the folders, only the stuck imports remain.
	fsys.stat["/downloads/complete/Fight.Club.1999.1080p.BluRay.x264-GRP"] = nil
	fsys.stat["/downloads/complete/Heat.1995"] = nil
	fsys.stat["/mnt/sab/Alien.1979"] = nil
	got, err = DownloadsCheck(e.st, fsys, fixed(t0)).Run(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if want := []string{fmt.Sprintf("download.import_stuck:%d", lost.ID), fmt.Sprintf("download.import_stuck:%d", stuck.ID)}; !slices.Equal(ids(got), want) {
		t.Errorf("issues = %v, want %v", ids(got), want)
	}
}
