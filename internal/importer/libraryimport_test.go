package importer_test

import (
	"context"
	"os"
	"path/filepath"
	"slices"
	"testing"
	"time"

	"github.com/fs0ciety7000/gonema/internal/events"
	"github.com/fs0ciety7000/gonema/internal/fsx"
	"github.com/fs0ciety7000/gonema/internal/importer"
	"github.com/fs0ciety7000/gonema/internal/jobs"
	"github.com/fs0ciety7000/gonema/internal/library"
	"github.com/fs0ciety7000/gonema/internal/metadata"
)

// libraryTree lays out a sub-folder "A-M" with a new, an ambiguous, an
// unknown and an already managed movie.
func libraryTree(e *env) {
	e.file(filepath.Join(e.root, "A-M", "Inception (2010)", "Inception.2010.2160p.UHD.BluRay.HDR10.x265-GRP.mkv"), movieSize)
	e.file(filepath.Join(e.root, "A-M", "Twin (2020)", "twin.mkv"), movieSize)
	e.file(filepath.Join(e.root, "A-M", "Zzqx", "zzqx.mkv"), movieSize)
	e.file(filepath.Join(e.root, "A-M", "Managed Copy (2000)", "Managed.2000.1080p.WEB-DL.H264-GRP.mkv"), movieSize)
	e.file(filepath.Join(e.root, "N-Z", "Other (2001)", "other.mkv"), movieSize)
	e.lib.results["Inception"] = []library.LookupResult{
		{MovieSummary: metadata.MovieSummary{TMDBID: 27205, Title: "Inception", Year: 2010, PosterPath: "/inc.jpg"}},
	}
	e.lib.results["Twin"] = []library.LookupResult{
		{MovieSummary: metadata.MovieSummary{TMDBID: 501, Title: "Twin", Year: 2020}},
		{MovieSummary: metadata.MovieSummary{TMDBID: 502, Title: "Twin", Year: 2020}},
	}
}

func TestLibraryScanSubPath(t *testing.T) {
	e := newEnv(t)
	libraryTree(e)
	managed := e.movie(1, "Managed", 2000)
	e.file(filepath.Join(e.root, "Managed (2000)", "managed.mkv"), movieSize)
	e.lib.results["Managed"] = []library.LookupResult{
		{MovieSummary: metadata.MovieSummary{TMDBID: 1, Title: "Managed", Year: 2000}, MovieID: managed.ID},
	}
	e.svc.SetImageURL(func(p string) *string { u := "/api/v1/images/poster/w342" + p; return &u })

	subs, err := e.svc.Subfolders(e.ctx, e.rootID, "")
	if err != nil || len(subs) != 3 || subs[0].Path != "A-M" || subs[0].Movie {
		t.Fatalf("subfolders = %+v, %v", subs, err)
	}
	if subs, err = e.svc.Subfolders(e.ctx, e.rootID, "A-M"); err != nil || len(subs) != 4 || !subs[0].Movie || subs[0].Path != "A-M/Inception (2010)" {
		t.Fatalf("subfolders of A-M = %+v, %v", subs, err)
	}
	for _, bad := range []string{"../x", "A-M/../../etc", ".hidden", "missing"} {
		if _, err := e.svc.Subfolders(e.ctx, e.rootID, bad); err == nil {
			t.Errorf("subfolders %q accepted", bad)
		}
	}

	var seen []string
	folders, err := e.svc.ScanLibraryPath(e.ctx, e.rootID, "A-M", func(done, total int, f *importer.LibraryFolder) {
		seen = append(seen, f.Folder)
		if total != 4 || done < 1 {
			t.Errorf("progress %d/%d", done, total)
		}
	})
	if err != nil || len(folders) != 4 || len(seen) != 4 {
		t.Fatalf("scan = %+v, %v (seen %v)", folders, err, seen)
	}
	by := map[string]importer.LibraryFolder{}
	for _, f := range folders {
		by[f.Name] = f
	}
	inc := by["Inception (2010)"]
	if inc.Status != importer.FolderNew || inc.Folder != "A-M/Inception (2010)" || inc.Quality != "bluray-2160p" ||
		!slices.Equal(inc.HDR, []string{"hdr10"}) || inc.Candidates[0].PosterURL == nil || *inc.Candidates[0].PosterURL != "/api/v1/images/poster/w342/inc.jpg" {
		t.Errorf("inception = %+v", inc)
	}
	if tw := by["Twin (2020)"]; tw.Status != importer.FolderAmbiguous || len(tw.Candidates) != 2 {
		t.Errorf("twin = %+v", tw)
	}
	if z := by["Zzqx"]; z.Status != importer.FolderNoMatch || len(z.Rejections) == 0 {
		t.Errorf("unknown = %+v", z)
	}
	if mc := by["Managed Copy (2000)"]; mc.Status != importer.FolderInLibrary || mc.MovieID != managed.ID ||
		mc.LibraryPath != filepath.Join(e.root, "Managed (2000)") {
		t.Errorf("managed copy = %+v", mc)
	}
	// A movie folder given as the path is scanned as one folder.
	one, err := e.svc.ScanLibraryPath(e.ctx, e.rootID, "A-M/Inception (2010)", nil)
	if err != nil || len(one) != 1 || one[0].Folder != "A-M/Inception (2010)" {
		t.Errorf("single folder = %+v, %v", one, err)
	}
	// Change match: the alternatives are scored against the folder.
	e.lib.results["Twin 2"] = []library.LookupResult{{MovieSummary: metadata.MovieSummary{TMDBID: 503, Title: "Twin", Year: 2020}},
		{MovieSummary: metadata.MovieSummary{TMDBID: 504, Title: "Unrelated", Year: 1950}}}
	cands, err := e.svc.MatchCandidates(e.ctx, e.rootID, "A-M/Twin (2020)", "Twin 2", 0)
	if err != nil || len(cands) != 2 || cands[0].TmdbID != 503 || cands[0].Confidence <= cands[1].Confidence {
		t.Errorf("candidates = %+v, %v", cands, err)
	}
}

func TestImportLibraryKeepAndMove(t *testing.T) {
	e := newEnv(t)
	libraryTree(e)
	src := filepath.Join(e.root, "A-M", "Inception (2010)", "Inception.2010.2160p.UHD.BluRay.HDR10.x265-GRP.mkv")
	out, err := e.svc.ImportLibrary(e.ctx, e.rootID, []importer.LibrarySelection{
		{Folder: "A-M/Inception (2010)", TmdbID: 27205, QualityProfileID: e.profID, Monitored: true, Mode: importer.LibraryMove},
		{Folder: "A-M/Twin (2020)", TmdbID: 501, QualityProfileID: e.profID, Monitored: true},
		{Folder: "A-M/Twin (2020)", TmdbID: 501, QualityProfileID: e.profID, Monitored: true},
		{Folder: "A-M/../..", TmdbID: 9, QualityProfileID: e.profID},
		{Folder: "N-Z/Other (2001)", TmdbID: 7, QualityProfileID: e.profID, Mode: "copy"},
	})
	if err != nil {
		t.Fatal(err)
	}
	r := out.Results
	if len(r) != 5 || r[0].Outcome != importer.OutcomeMoved || r[1].Outcome != importer.OutcomeKept ||
		r[2].Outcome != importer.OutcomeSkipped || r[3].Outcome != importer.OutcomeFailed || r[4].Outcome != importer.OutcomeFailed {
		t.Fatalf("results = %+v", r)
	}
	s := out.Summary
	if s.Imported != 2 || s.Moved != 1 || s.Kept != 1 || s.Skipped != 1 || s.Failed != 2 || s.CollectionsCompleted == nil {
		t.Errorf("summary = %+v", s)
	}
	// Moved into the naming templates, not deleted.
	m, err := e.st.Read().GetMovie(e.ctx, r[0].MovieID)
	if err != nil || m.Path == "A-M/Inception (2010)" {
		t.Fatalf("moved movie = %+v, %v", m, err)
	}
	f := e.currentFile(m.ID)
	if f == nil {
		t.Fatal("no file recorded")
	}
	if _, err := os.Stat(filepath.Join(e.root, m.Path, f.RelativePath)); err != nil {
		t.Errorf("moved file missing: %v", err)
	}
	if _, err := os.Stat(src); !os.IsNotExist(err) {
		t.Errorf("source still there after a move: %v", err)
	}
	// Kept in place, nested path.
	tw, _ := e.st.Read().GetMovie(e.ctx, r[1].MovieID)
	if tw.Path != "A-M/Twin (2020)" {
		t.Errorf("kept path = %q", tw.Path)
	}
	if _, err := os.Stat(filepath.Join(e.root, "A-M", "Twin (2020)", "twin.mkv")); err != nil {
		t.Errorf("kept file: %v", err)
	}
}

// TestLibraryScanJob runs a scan through the job queue: folders stream as
// events and the results stay for a later review.
func TestLibraryScanJob(t *testing.T) {
	e := newEnv(t)
	libraryTree(e)
	runner := jobs.New(e.st, jobs.Options{Workers: 1, PollInterval: 10 * time.Millisecond, Bus: e.bus})
	svc, err := importer.New(importer.Options{
		Store: e.st, Bus: e.bus, Library: e.lib, Jobs: runner, Now: func() time.Time { return e.now },
		FS: fsx.New(nil, fsx.Options{Now: func() time.Time { return e.now }}),
	})
	if err != nil {
		t.Fatal(err)
	}
	svc.RegisterJobs(runner)
	sub := e.bus.Subscribe(64, func(ev *events.Event) bool {
		return ev.Type == importer.EventScanFolder || ev.Type == importer.EventScanFinished
	})
	defer sub.Close()
	if err := runner.Start(context.Background()); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		_ = runner.Stop(ctx)
	})
	if _, err := svc.StartLibraryScan(e.ctx, e.rootID, "../x"); err == nil {
		t.Error("scan outside the root folder accepted")
	}
	sc, err := svc.StartLibraryScan(e.ctx, e.rootID, "A-M")
	if err != nil || sc.Status != importer.ScanPending || sc.JobID == nil || sc.Path != "A-M" {
		t.Fatalf("start = %+v, %v", sc, err)
	}
	folders := 0
	deadline := time.After(10 * time.Second)
wait:
	for {
		select {
		case ev := <-sub.C():
			if ev.Type == importer.EventScanFolder {
				folders++
				continue
			}
			break wait
		case <-deadline:
			t.Fatal("scan did not finish")
		}
	}
	got, err := svc.LibraryScanByID(e.ctx, sc.ID)
	if err != nil || got.Status != importer.ScanDone || got.Total != 4 || len(got.Folders) != 4 || folders != 4 ||
		got.Counts.New != 1 || got.Counts.Ambiguous != 1 || got.Counts.NoMatch != 2 {
		t.Fatalf("scan = %+v (%d folder events), %v", got, folders, err)
	}
	list, err := svc.LibraryScans(e.ctx)
	if err != nil || len(list) != 1 || list[0].Folders != nil {
		t.Errorf("list = %+v, %v", list, err)
	}
}
