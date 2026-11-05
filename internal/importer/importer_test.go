package importer_test

import (
	"context"
	"errors"
	"io/fs"
	"math"
	"os"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/fs0ciety7000/gonema/internal/decision"
	"github.com/fs0ciety7000/gonema/internal/events"
	"github.com/fs0ciety7000/gonema/internal/fsx"
	"github.com/fs0ciety7000/gonema/internal/importer"
	"github.com/fs0ciety7000/gonema/internal/library"
	"github.com/fs0ciety7000/gonema/internal/match"
	"github.com/fs0ciety7000/gonema/internal/metadata"
	"github.com/fs0ciety7000/gonema/internal/parser"
	"github.com/fs0ciety7000/gonema/internal/store"
	"github.com/fs0ciety7000/gonema/internal/store/db"
	"github.com/fs0ciety7000/gonema/internal/store/storetest"
)

const movieSize = 30 << 20 // above the 20 MiB "too small" limit

type env struct {
	t       *testing.T
	ctx     context.Context
	st      *store.Store
	svc     *importer.Service
	bus     *events.Bus
	root    string
	rootID  int64
	dl      string
	profID  int64
	now     time.Time
	matcher *indexMatcher
	lib     *fakeLibrary
}

type indexMatcher struct{ entries []match.Entry }

func (m *indexMatcher) MatchRelease(p parser.ParsedRelease) []match.Candidate {
	return match.NewIndex(m.entries).Match(p)
}

type fakeLibrary struct {
	e       *env
	results map[string][]library.LookupResult
	added   []int
}

func (f *fakeLibrary) Lookup(_ context.Context, q string, _ int) ([]library.LookupResult, error) {
	return f.results[q], nil
}

func (f *fakeLibrary) AddMovie(_ context.Context, tmdbID int, opts library.AddOptions) (db.Movie, bool, error) {
	if m, err := f.e.st.Read().GetMovieByTmdbID(f.e.ctx, int64(tmdbID)); err == nil {
		return m, false, nil
	}
	f.added = append(f.added, tmdbID)
	title := "Movie " + itoa(int64(tmdbID))
	for _, rs := range f.results {
		for i := range rs {
			if rs[i].TMDBID == tmdbID {
				title = rs[i].Title
			}
		}
	}
	m := f.e.insertMovie(int64(tmdbID), title, 2010, title+" (2010)", opts.QualityProfileID)
	return m, true, nil
}

func itoa(n int64) string { return strconv.FormatInt(n, 10) }

func newEnv(t *testing.T) *env {
	t.Helper()
	ctx := context.Background()
	st := storetest.New(t)
	base := t.TempDir()
	e := &env{t: t, ctx: ctx, st: st, root: filepath.Join(base, "movies"), dl: filepath.Join(base, "downloads"),
		now: time.Date(2026, 11, 5, 12, 0, 0, 0, time.UTC), matcher: &indexMatcher{}}
	for _, d := range []string{e.root, e.dl} {
		if err := os.MkdirAll(d, 0o750); err != nil {
			t.Fatal(err)
		}
	}
	p, err := st.CreateQualityProfile(ctx, db.CreateQualityProfileParams{
		Name: "Test", UpgradeAllowed: true, CutoffQuality: "bluray-2160p", MinUpgradeStep: 1, Languages: "[]",
	}, []store.ProfileItem{
		{Quality: "bluray-2160p", Position: 0, Allowed: true},
		{Quality: "bluray-1080p", Position: 1, Allowed: true},
		{Quality: "webdl-1080p", Position: 2, Allowed: true},
		{Quality: "webdl-720p", Position: 3, Allowed: true},
		{Quality: "dvd", Position: 4, Allowed: false},
	})
	if err != nil {
		t.Fatal(err)
	}
	e.profID = p.ID
	rf, err := st.Write().CreateRootFolder(ctx, e.root)
	if err != nil {
		t.Fatal(err)
	}
	e.rootID = rf.ID
	e.bus = events.New(events.Options{})
	t.Cleanup(e.bus.Close)
	e.lib = &fakeLibrary{e: e, results: map[string][]library.LookupResult{}}
	e.svc, err = importer.New(importer.Options{
		Store: st, Bus: e.bus, Matcher: e.matcher, Library: e.lib, Now: func() time.Time { return e.now },
		FS: fsx.New(nil, fsx.Options{Now: func() time.Time { return e.now }}),
	})
	if err != nil {
		t.Fatal(err)
	}
	set := importer.DefaultSettings()
	set.AllowedRoots = []string{e.dl}
	if _, err := e.svc.SetSettings(ctx, set); err != nil {
		t.Fatal(err)
	}
	return e
}

func (e *env) insertMovie(tmdb int64, title string, year int64, folder string, profileID int64) db.Movie {
	e.t.Helper()
	m, err := e.st.Write().InsertMovie(e.ctx, db.InsertMovieParams{
		TmdbID: tmdb, Title: title, SortTitle: strings.ToLower(title), CleanTitle: parser.CleanTitle(title),
		Year: year, Status: "released", Genres: "[]", Ratings: "{}", Extra: "{}", Monitored: true,
		QualityProfileID: profileID, MinimumAvailability: "released", RootFolderID: e.rootID, Path: folder,
		AddedAt: store.Millis(e.now),
	})
	if err != nil {
		e.t.Fatalf("insert movie: %v", store.MapError(err))
	}
	e.matcher.entries = append(e.matcher.entries, match.Entry{MovieID: m.ID, Title: title, Year: int(year)})
	return m
}

func (e *env) movie(tmdb int64, title string, year int64) db.Movie {
	return e.insertMovie(tmdb, title, year, title+" ("+itoa(year)+")", e.profID)
}

// file creates a sparse file of size bytes (fast, no disk use).
func (e *env) file(path string, size int64) string {
	e.t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o750); err != nil {
		e.t.Fatal(err)
	}
	f, err := os.Create(path)
	if err != nil {
		e.t.Fatal(err)
	}
	if _, err := f.WriteString(filepath.Base(path)); err != nil {
		e.t.Fatal(err)
	}
	if err := f.Truncate(size); err != nil {
		e.t.Fatal(err)
	}
	if err := f.Close(); err != nil {
		e.t.Fatal(err)
	}
	return path
}

func (e *env) currentFile(movieID int64) *db.MovieFile {
	e.t.Helper()
	f, err := e.st.Read().GetCurrentMovieFile(e.ctx, movieID)
	if errors.Is(store.MapError(err), store.ErrNotFound) {
		return nil
	}
	if err != nil {
		e.t.Fatal(err)
	}
	return &f
}

func (e *env) history(movieID int64) []db.History {
	e.t.Helper()
	rows, err := e.st.Read().ListMovieHistory(e.ctx, db.ListMovieHistoryParams{MovieID: movieID, BeforeID: math.MaxInt64, Limit: 50})
	if err != nil {
		e.t.Fatal(err)
	}
	return rows
}

func (e *env) dirNames(dir string) []string {
	e.t.Helper()
	es, err := os.ReadDir(dir)
	if errors.Is(err, fs.ErrNotExist) {
		return nil
	}
	if err != nil {
		e.t.Fatal(err)
	}
	var out []string
	for _, x := range es {
		out = append(out, x.Name())
	}
	return out
}

func exists(t *testing.T, p string) bool {
	t.Helper()
	_, err := os.Lstat(p)
	if err != nil && !errors.Is(err, fs.ErrNotExist) {
		t.Fatal(err)
	}
	return err == nil
}

func TestImportTorrentSeedingHardlinks(t *testing.T) {
	e := newEnv(t)
	m := e.movie(438631, "Dune", 2021)
	rel := "Dune.2021.1080p.BluRay.DTS-HD.MA.5.1.x264-GRP"
	dir := filepath.Join(e.dl, rel)
	src := e.file(filepath.Join(dir, rel+".mkv"), movieSize)
	e.file(filepath.Join(dir, "Sample", "sample.mkv"), 5<<20)
	e.file(filepath.Join(dir, "Subs", "2_English.srt"), 100)
	e.file(filepath.Join(dir, "Subs", "3_French.forced.srt"), 100)
	e.file(filepath.Join(dir, rel+".nfo"), 10)

	sub := e.bus.Subscribe(16, nil)
	defer sub.Close()
	res, err := e.svc.Import(e.ctx, importer.Request{
		MovieID: m.ID, LocalPath: dir, ReleaseTitle: rel, Protocol: decision.Torrent, ClientSeeding: true,
	})
	if err != nil {
		t.Fatal(err)
	}
	if !res.Imported || res.Upgraded || res.FileID == 0 {
		t.Fatalf("result = %+v", res)
	}
	want := "Dune (2021) [Bluray-1080p][DTS-HD MA 5.1]-GRP.mkv"
	if res.Path != want {
		t.Errorf("path = %q, want %q", res.Path, want)
	}
	movieDir := filepath.Join(e.root, "Dune (2021)")
	dst := filepath.Join(movieDir, want)
	a, _ := os.Stat(src)
	b, err := os.Stat(dst)
	if err != nil || !os.SameFile(a, b) {
		t.Fatalf("destination is not a hard link of the source: %v", err)
	}
	if !exists(t, src) {
		t.Error("the seeding source was removed")
	}
	got := e.dirNames(movieDir)
	wantFiles := []string{want, "Dune (2021) [Bluray-1080p][DTS-HD MA 5.1]-GRP.en.srt", "Dune (2021) [Bluray-1080p][DTS-HD MA 5.1]-GRP.fr.forced.srt"}
	slices.Sort(wantFiles)
	if !slices.Equal(got, wantFiles) {
		t.Errorf("movie folder = %q\nwant %q", got, wantFiles)
	}
	f := e.currentFile(m.ID)
	if f == nil || f.RelativePath != want || f.Quality != "bluray-1080p" || f.ReleaseGroup != "GRP" || f.SceneName != rel || f.Size != movieSize {
		t.Errorf("movie file = %+v", f)
	}
	h := e.history(m.ID)
	if len(h) != 1 || h[0].Event != "imported" || !strings.Contains(h[0].Data, "hardlink") {
		t.Errorf("history = %+v", h)
	}
	select {
	case ev := <-sub.C():
		if ev.Type != importer.EventFileImported || ev.MovieID != m.ID {
			t.Errorf("event = %+v", ev)
		}
	case <-time.After(time.Second):
		t.Error("no event")
	}
}

func TestImportUsenetMoves(t *testing.T) {
	e := newEnv(t)
	m := e.movie(550, "Fight Club", 1999)
	dir := filepath.Join(e.dl, "Fight.Club.1999.1080p.WEB-DL.DD5.1.H264-GRP")
	src := e.file(filepath.Join(dir, "a1b2c3d4e5f6.mkv"), movieSize) // obfuscated file name: the folder tells
	res, err := e.svc.Import(e.ctx, importer.Request{MovieID: m.ID, LocalPath: dir, Protocol: decision.Usenet})
	if err != nil {
		t.Fatal(err)
	}
	if !res.Imported {
		t.Fatalf("result = %+v", res)
	}
	if exists(t, src) {
		t.Error("the Usenet source is still there (should be moved)")
	}
	if !exists(t, filepath.Join(e.root, "Fight Club (1999)", res.Path)) || !strings.Contains(res.Path, "WEBDL-1080p") {
		t.Errorf("moved file missing: %+v", res)
	}
	if !strings.Contains(res.Reason, "move") {
		t.Errorf("reason = %q", res.Reason)
	}
}

func TestImportUpgradeRecyclesOldFile(t *testing.T) {
	e := newEnv(t)
	m := e.movie(603, "The Matrix", 1999)
	old := "The.Matrix.1999.720p.WEB-DL.DD5.1.H264-OLD"
	if res, err := e.svc.Import(e.ctx, importer.Request{MovieID: m.ID, LocalPath: e.file(filepath.Join(e.dl, old+".mkv"), movieSize), Protocol: decision.Usenet}); err != nil || !res.Imported {
		t.Fatalf("first import = %+v, %v", res, err)
	}
	first := e.currentFile(m.ID)
	movieDir := filepath.Join(e.root, "The Matrix (1999)")
	e.file(filepath.Join(movieDir, strings.TrimSuffix(first.RelativePath, ".mkv")+".en.srt"), 50) // subtitle of the old file

	e.now = e.now.Add(time.Hour)
	rel := "The.Matrix.1999.1080p.BluRay.x264-NEW"
	res, err := e.svc.Import(e.ctx, importer.Request{MovieID: m.ID, LocalPath: e.file(filepath.Join(e.dl, rel, rel+".mkv"), movieSize+1), Protocol: decision.Usenet})
	if err != nil {
		t.Fatal(err)
	}
	if !res.Imported || !res.Upgraded || !strings.Contains(res.Reason, "Upgraded from WEBDL-720p to Bluray-1080p") || !strings.Contains(res.Reason, "recycle bin") {
		t.Fatalf("result = %+v", res)
	}
	cur := e.currentFile(m.ID)
	if cur.ID != first.ID || cur.Quality != "bluray-1080p" || cur.RelativePath != res.Path || cur.Size != movieSize+1 {
		t.Errorf("movie file = %+v (first id %d)", cur, first.ID)
	}
	if exists(t, filepath.Join(movieDir, first.RelativePath)) {
		t.Error("old file still in the movie folder")
	}
	bin := filepath.Join(e.root, importer.RecycleDirName)
	items, err := e.svc.ListRecycle(e.ctx)
	if err != nil {
		t.Fatal(err)
	}
	if len(items) != 2 {
		t.Fatalf("recycle bin = %+v", items)
	}
	var movieItem *importer.RecycleEntry
	for i := range items {
		if strings.HasSuffix(items[i].Name, ".mkv") {
			movieItem = &items[i]
		}
	}
	if movieItem == nil || movieItem.Manifest == nil || movieItem.Manifest.MovieID != m.ID ||
		movieItem.Manifest.OriginalPath != filepath.Join(movieDir, first.RelativePath) || movieItem.Bin != bin ||
		!strings.Contains(movieItem.Manifest.Reason, "Bluray-1080p") {
		t.Fatalf("recycled item = %+v", movieItem)
	}
	if !exists(t, filepath.Join(bin, movieItem.Name+fsx.ManifestSuffix)) {
		t.Error("manifest missing")
	}
	h := e.history(m.ID)
	if len(h) != 2 || h[0].Event != "upgraded" || !strings.Contains(h[0].Data, `"recycled"`) || !strings.Contains(h[0].Data, "upgradeReason") {
		t.Errorf("history = %+v", h)
	}

	// Purge refuses without confirmation; restore brings the old file back.
	if _, err := e.svc.PurgeRecycle(e.ctx, 0, false); !errors.Is(err, importer.ErrConfirmRequired) {
		t.Fatalf("purge without confirm: %v", err)
	}
	if purged, err := e.svc.PurgeRecycle(e.ctx, -1, true); err != nil || len(purged) != 0 {
		t.Fatalf("purge of recent files = %+v, %v (retention is 30 days)", purged, err)
	}
	restored, err := e.svc.RestoreRecycled(e.ctx, movieItem.Name)
	if err != nil || restored != movieItem.Manifest.OriginalPath || !exists(t, restored) {
		t.Fatalf("restore = %q, %v", restored, err)
	}
	if _, err := e.svc.RestoreRecycled(e.ctx, "../../etc/passwd"); err == nil {
		t.Error("restore accepted a traversal")
	}
	e.now = e.now.Add(31 * 24 * time.Hour)
	purged, err := e.svc.PurgeRecycle(e.ctx, -1, true)
	if err != nil || len(purged) != 1 || !strings.HasSuffix(purged[0].Name, ".en.srt") {
		t.Fatalf("purge = %+v, %v", purged, err)
	}
	h = e.history(m.ID)
	if h[0].Event != "file_deleted" {
		t.Errorf("purge not in history: %+v", h[0])
	}
}

func TestImportNotAnUpgradeChangesNothing(t *testing.T) {
	e := newEnv(t)
	m := e.movie(680, "Pulp Fiction", 1994)
	good := "Pulp.Fiction.1994.1080p.BluRay.x264-GRP"
	if res, err := e.svc.Import(e.ctx, importer.Request{MovieID: m.ID, LocalPath: e.file(filepath.Join(e.dl, good+".mkv"), movieSize), Protocol: decision.Usenet}); err != nil || !res.Imported {
		t.Fatalf("first import = %+v, %v", res, err)
	}
	before := e.currentFile(m.ID)
	movieDir := filepath.Join(e.root, "Pulp Fiction (1994)")
	filesBefore := e.dirNames(movieDir)

	worse := "Pulp.Fiction.1994.720p.WEB-DL.H264-GRP"
	src := e.file(filepath.Join(e.dl, worse+".mkv"), movieSize)
	res, err := e.svc.Import(e.ctx, importer.Request{MovieID: m.ID, LocalPath: src, Protocol: decision.Usenet})
	if err != nil {
		t.Fatal(err)
	}
	if res.Imported || !strings.HasPrefix(res.Reason, "Not an upgrade: the current file is Bluray-1080p") {
		t.Fatalf("result = %+v", res)
	}
	if !exists(t, src) {
		t.Error("source touched")
	}
	if got := e.dirNames(movieDir); !slices.Equal(got, filesBefore) {
		t.Errorf("movie folder changed: %v -> %v", filesBefore, got)
	}
	if after := e.currentFile(m.ID); *after != *before {
		t.Errorf("movie file changed: %+v -> %+v", before, after)
	}
	if e.dirNames(filepath.Join(e.root, importer.RecycleDirName)) != nil {
		t.Error("something was recycled")
	}
	if h := e.history(m.ID); h[0].Event != "import_failed" || !strings.Contains(h[0].Data, "Not an upgrade") {
		t.Errorf("history = %+v", h[0])
	}
}

func TestImportRejections(t *testing.T) {
	e := newEnv(t)
	m := e.movie(13, "Forrest Gump", 1994)
	e.movie(14, "American Beauty", 1999)
	cases := []struct {
		name  string
		files map[string]int64
		path  string
		title string
		want  string
	}{
		{"sample only", map[string]int64{"Forrest.Gump.1994.1080p.BluRay.x264-GRP/forrest.gump.1994.1080p.bluray.x264-grp-sample.mkv": 40 << 20},
			"Forrest.Gump.1994.1080p.BluRay.x264-GRP", "", "Only a sample was found"},
		{"wrong movie", map[string]int64{"American.Beauty.1999.1080p.BluRay.x264-GRP.mkv": movieSize},
			"American.Beauty.1999.1080p.BluRay.x264-GRP.mkv", "", "Wrong movie"},
		{"unrelated title", map[string]int64{"Totally.Different.2001.1080p.BluRay.x264-GRP.mkv": movieSize},
			"Totally.Different.2001.1080p.BluRay.x264-GRP.mkv", "", "Wrong movie"},
		{"multi part", map[string]int64{"FG/Forrest.Gump.1994.DVDRip.XviD.CD1-GRP.avi": movieSize, "FG/Forrest.Gump.1994.DVDRip.XviD.CD2-GRP.avi": movieSize},
			"FG", "Forrest.Gump.1994.DVDRip.XviD-GRP", "multi-part"},
		{"no video", map[string]int64{"Empty/readme.nfo": 10}, "Empty", "", "No video file"},
		{"too small", map[string]int64{"Forrest.Gump.1994.1080p.BluRay.x264-SMALL.mkv": 1 << 20},
			"Forrest.Gump.1994.1080p.BluRay.x264-SMALL.mkv", "", "No video file is the movie"},
		{"disallowed quality", map[string]int64{"Forrest.Gump.1994.DVDRip.XviD-GRP.avi": movieSize},
			"Forrest.Gump.1994.DVDRip.XviD-GRP.avi", "", "not wanted by the quality profile"},
		{"bonus release", map[string]int64{"Forrest.Gump.1994.EXTRAS.1080p.BluRay.x264-GRP.mkv": movieSize},
			"Forrest.Gump.1994.EXTRAS.1080p.BluRay.x264-GRP.mkv", "Forrest.Gump.1994.EXTRAS.1080p.BluRay.x264-GRP", "extras"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			for rel, size := range c.files {
				e.file(filepath.Join(e.dl, rel), size)
			}
			res, err := e.svc.Import(e.ctx, importer.Request{MovieID: m.ID, LocalPath: filepath.Join(e.dl, c.path), ReleaseTitle: c.title, Protocol: decision.Usenet})
			if err != nil {
				t.Fatal(err)
			}
			if res.Imported || !strings.Contains(res.Reason, c.want) {
				t.Errorf("result = %+v, want reason containing %q", res, c.want)
			}
			for rel := range c.files {
				if !exists(t, filepath.Join(e.dl, rel)) {
					t.Errorf("%s was touched", rel)
				}
			}
		})
	}
	if e.currentFile(m.ID) != nil {
		t.Error("a file was recorded")
	}
	if e.dirNames(filepath.Join(e.root, "Forrest Gump (1994)")) != nil {
		t.Error("the movie folder was populated")
	}
}

func TestImportPathChecks(t *testing.T) {
	e := newEnv(t)
	m := e.movie(11, "Star Wars", 1977)
	outside := e.file(filepath.Join(t.TempDir(), "Star.Wars.1977.1080p.BluRay.x264-GRP.mkv"), movieSize)
	_, err := e.svc.Import(e.ctx, importer.Request{MovieID: m.ID, LocalPath: outside})
	if !errors.Is(err, importer.ErrOutsideRoots) {
		t.Errorf("outside: err = %v", err)
	}
	if _, err := e.svc.Import(e.ctx, importer.Request{MovieID: m.ID, LocalPath: filepath.Join(e.dl, "..", "movies")}); !errors.Is(err, importer.ErrOutsideRoots) {
		t.Errorf("traversal: err = %v", err)
	}
	if _, err := e.svc.Import(e.ctx, importer.Request{MovieID: m.ID, LocalPath: "relative/path"}); !errors.Is(err, importer.ErrInvalid) {
		t.Errorf("relative: err = %v", err)
	}
	if _, err := e.svc.Import(e.ctx, importer.Request{MovieID: 999, LocalPath: e.dl}); !errors.Is(err, importer.ErrNotFound) {
		t.Errorf("unknown movie: err = %v", err)
	}
	link := filepath.Join(e.dl, "escape")
	if err := os.Symlink(filepath.Dir(outside), link); err == nil {
		if _, err := e.svc.Import(e.ctx, importer.Request{MovieID: m.ID, LocalPath: link}); !errors.Is(err, importer.ErrOutsideRoots) {
			t.Errorf("symlink escape: err = %v", err)
		}
	}
	if !exists(t, outside) {
		t.Error("outside file touched")
	}
}

func TestImportSameNameStagesAndRecycles(t *testing.T) {
	e := newEnv(t)
	if _, err := e.svc.SetNaming(e.ctx, importer.NamingSettings{FolderTemplate: library.DefaultFolderTemplate, FileTemplate: "{Movie Title}", RenameFiles: true}); err != nil {
		t.Fatal(err)
	}
	m := e.movie(949, "Heat", 1995)
	if res, err := e.svc.Import(e.ctx, importer.Request{MovieID: m.ID, LocalPath: e.file(filepath.Join(e.dl, "Heat.1995.1080p.BluRay.x264-GRP.mkv"), movieSize), Protocol: decision.Usenet}); err != nil || res.Path != "Heat.mkv" {
		t.Fatalf("first = %+v, %v", res, err)
	}
	res, err := e.svc.Import(e.ctx, importer.Request{MovieID: m.ID, LocalPath: e.file(filepath.Join(e.dl, "Heat.1995.PROPER.1080p.BluRay.x264-GRP.mkv"), movieSize+7), Protocol: decision.Usenet})
	if err != nil || !res.Upgraded || res.Path != "Heat.mkv" || !strings.Contains(res.Reason, "PROPER") {
		t.Fatalf("proper = %+v, %v", res, err)
	}
	dir := filepath.Join(e.root, "Heat (1995)")
	if got := e.dirNames(dir); !slices.Equal(got, []string{"Heat.mkv"}) {
		t.Errorf("folder = %v (no staging file may remain)", got)
	}
	fi, _ := os.Stat(filepath.Join(dir, "Heat.mkv"))
	if fi.Size() != movieSize+7 {
		t.Error("the new file is not in place")
	}
	if items, _ := e.svc.ListRecycle(e.ctx); len(items) != 1 {
		t.Errorf("bin = %+v", items)
	}
}

func TestManualImport(t *testing.T) {
	e := newEnv(t)
	m := e.movie(78, "Blade Runner", 1982)
	dir := filepath.Join(e.dl, "batch")
	main := e.file(filepath.Join(dir, "Blade.Runner.1982.Final.Cut.1080p.BluRay.x264-GRP.mkv"), movieSize)
	e.file(filepath.Join(dir, "Blade.Runner.1982.Final.Cut.1080p.BluRay.x264-GRP.en.srt"), 10)
	e.file(filepath.Join(dir, "weird.name.mkv"), movieSize)
	e.file(filepath.Join(dir, "sample.mkv"), 1<<20)

	cands, err := e.svc.Scan(e.ctx, dir)
	if err != nil {
		t.Fatal(err)
	}
	if len(cands) != 3 {
		t.Fatalf("candidates = %+v", cands)
	}
	byName := map[string]importer.ScanCandidate{}
	for _, c := range cands {
		byName[c.Name] = c
	}
	br := byName["Blade.Runner.1982.Final.Cut.1080p.BluRay.x264-GRP.mkv"]
	if br.Movie == nil || br.Movie.MovieID != m.ID || !br.Movie.Confident() || br.Quality != "bluray-1080p" || len(br.Rejections) != 0 {
		t.Errorf("blade runner candidate = %+v", br)
	}
	if w := byName["weird.name.mkv"]; w.Movie != nil || len(w.Rejections) == 0 {
		t.Errorf("weird candidate = %+v", w)
	}
	if s := byName["sample.mkv"]; len(s.Rejections) == 0 {
		t.Errorf("sample candidate = %+v", s)
	}
	if _, err := e.svc.Scan(e.ctx, t.TempDir()); !errors.Is(err, importer.ErrOutsideRoots) {
		t.Errorf("scan outside: %v", err)
	}

	out, err := e.svc.ImportManual(e.ctx, []importer.Selection{
		{Path: main, MovieID: m.ID, Mode: fsx.ModeCopy},
		{Path: filepath.Join(dir, "weird.name.mkv"), MovieID: m.ID},                   // quality unknown
		{Path: filepath.Join(dir, "weird.name.mkv"), MovieID: m.ID, Quality: "dvd"},   // not an upgrade
		{Path: filepath.Join(e.root, "x.mkv"), MovieID: m.ID},                         // outside
		{Path: filepath.Join(dir, "weird.name.mkv"), MovieID: m.ID, Quality: "bogus"}, // invalid
	})
	if err != nil {
		t.Fatal(err)
	}
	if !out[0].Imported || !exists(t, main) || len(out[0].Subtitles) != 1 {
		t.Errorf("manual copy = %+v", out[0])
	}
	if out[1].Imported || !strings.Contains(out[1].Reason, "quality") {
		t.Errorf("unknown quality = %+v", out[1])
	}
	if out[2].Imported || !strings.Contains(out[2].Reason, "Not an upgrade") {
		t.Errorf("worse quality = %+v", out[2])
	}
	if out[3].Error == "" || out[4].Error == "" {
		t.Errorf("errors = %q, %q", out[3].Error, out[4].Error)
	}
	// Replace forces it (old file recycled).
	out, err = e.svc.ImportManual(e.ctx, []importer.Selection{{Path: filepath.Join(dir, "weird.name.mkv"), MovieID: m.ID, Quality: "dvd", Replace: true}})
	if err != nil || !out[0].Imported || !out[0].Upgraded {
		t.Fatalf("replace = %+v, %v", out, err)
	}
	if f := e.currentFile(m.ID); f.Quality != "dvd" {
		t.Errorf("file = %+v", f)
	}
}

func TestLibraryImport(t *testing.T) {
	e := newEnv(t)
	e.file(filepath.Join(e.root, "Inception (2010)", "Inception.2010.1080p.BluRay.x264-GRP.mkv"), movieSize)
	e.file(filepath.Join(e.root, "Arrival {tmdb-329865}", "arrival.mkv"), movieSize)
	e.file(filepath.Join(e.root, "Empty Folder", "notes.txt"), 10)
	e.file(filepath.Join(e.root, importer.RecycleDirName, "x.mkv"), movieSize)
	existing := e.movie(1, "Managed", 2000)
	e.file(filepath.Join(e.root, "Managed (2000)", "Managed.2000.720p.WEB-DL.H264-GRP.mkv"), movieSize)
	e.lib.results["Inception"] = []library.LookupResult{
		{MovieSummary: metadata.MovieSummary{TMDBID: 27205, Title: "Inception", Year: 2010}},
		{MovieSummary: metadata.MovieSummary{TMDBID: 99999, Title: "Inception: The Cobol Job", Year: 2010}},
	}
	e.lib.results["tmdb:329865"] = []library.LookupResult{{MovieSummary: metadata.MovieSummary{TMDBID: 329865, Title: "Arrival", Year: 2016}}}

	folders, err := e.svc.ScanLibrary(e.ctx, e.rootID)
	if err != nil {
		t.Fatal(err)
	}
	byName := map[string]importer.LibraryFolder{}
	for _, f := range folders {
		byName[f.Folder] = f
	}
	if len(folders) != 4 {
		t.Fatalf("folders = %+v", folders)
	}
	inc := byName["Inception (2010)"]
	if len(inc.Candidates) == 0 || inc.Candidates[0].TmdbID != 27205 || inc.Candidates[0].Confidence < 0.75 || inc.Quality != "bluray-1080p" {
		t.Errorf("inception = %+v", inc)
	}
	if arr := byName["Arrival {tmdb-329865}"]; len(arr.Candidates) != 1 || arr.Candidates[0].TmdbID != 329865 || arr.Candidates[0].Confidence != 1 {
		t.Errorf("arrival = %+v", arr)
	}
	if mg := byName["Managed (2000)"]; mg.MovieID != existing.ID {
		t.Errorf("managed = %+v", mg)
	}
	if em := byName["Empty Folder"]; len(em.Rejections) == 0 {
		t.Errorf("empty = %+v", em)
	}

	res, err := e.svc.ConfirmLibrary(e.ctx, e.rootID, []importer.LibrarySelection{
		{Folder: "Inception (2010)", TmdbID: 27205, QualityProfileID: e.profID, Monitored: true},
		{Folder: "Managed (2000)"},
		{Folder: "../outside", TmdbID: 1, QualityProfileID: e.profID},
		{Folder: "Missing", TmdbID: 2, QualityProfileID: e.profID},
	})
	if err != nil {
		t.Fatal(err)
	}
	if res[0].Error != "" || res[0].FileID == 0 {
		t.Fatalf("inception = %+v", res[0])
	}
	mv, err := e.st.Read().GetMovie(e.ctx, res[0].MovieID)
	if err != nil || mv.Path != "Inception (2010)" {
		t.Errorf("movie path = %q, %v", mv.Path, err)
	}
	f := e.currentFile(res[0].MovieID)
	if f == nil || f.RelativePath != "Inception.2010.1080p.BluRay.x264-GRP.mkv" || f.Quality != "bluray-1080p" {
		t.Errorf("file = %+v", f)
	}
	if !exists(t, filepath.Join(e.root, "Inception (2010)", "Inception.2010.1080p.BluRay.x264-GRP.mkv")) {
		t.Error("the file moved")
	}
	if res[1].Error != "" || res[1].MovieID != existing.ID || res[1].FileID == 0 {
		t.Errorf("managed = %+v", res[1])
	}
	if res[2].Error == "" || res[3].Error == "" {
		t.Errorf("invalid folders = %+v / %+v", res[2], res[3])
	}
	if h := e.history(res[0].MovieID); len(h) != 1 || h[0].Event != "imported" || !strings.Contains(h[0].Data, "left in place") {
		t.Errorf("history = %+v", h)
	}
}

func TestNamingSettings(t *testing.T) {
	e := newEnv(t)
	n, err := e.svc.Naming(e.ctx)
	if err != nil || n != importer.DefaultNaming() {
		t.Fatalf("defaults = %+v, %v", n, err)
	}
	if _, err := e.svc.SetNaming(e.ctx, importer.NamingSettings{FolderTemplate: "{Nope}", FileTemplate: "{Bad}"}); !errors.Is(err, importer.ErrInvalid) ||
		!strings.Contains(err.Error(), "{Nope}") || !strings.Contains(err.Error(), "{Bad}") {
		t.Errorf("invalid: %v", err)
	}
	p, err := e.svc.PreviewNaming(e.ctx, importer.DefaultNaming(), 0)
	if err != nil || !p.Example || p.Folder != "Dune - Part Two (2024)" || !strings.HasSuffix(p.File, "-FLUX.mkv") {
		t.Errorf("example preview = %+v, %v", p, err)
	}
	m := e.movie(105, "Back to the Future", 1985)
	if _, err := e.svc.Import(e.ctx, importer.Request{MovieID: m.ID, LocalPath: e.file(filepath.Join(e.dl, "Back.to.the.Future.1985.2160p.UHD.BluRay.HDR10.x265-GRP.mkv"), movieSize), Protocol: decision.Usenet}); err != nil {
		t.Fatal(err)
	}
	p, err = e.svc.PreviewNaming(e.ctx, importer.NamingSettings{FolderTemplate: "{Title} [{Year}]", FileTemplate: "{Movie.Title}.{Release.Year}.{Quality.Title}", RenameFiles: true}, m.ID)
	if err != nil || p.Example || p.Folder != "Back to the Future [1985]" || p.File != "Back.to.the.Future.1985.Bluray-2160p.mkv" {
		t.Errorf("movie preview = %+v, %v", p, err)
	}
	want := importer.NamingSettings{FolderTemplate: "{Title} ({Year})", FileTemplate: "{Movie Title} {[Quality Full]}", RenameFiles: false}
	if _, err := e.svc.SetNaming(e.ctx, want); err != nil {
		t.Fatal(err)
	}
	if got, _ := e.svc.Naming(e.ctx); got != want {
		t.Errorf("stored = %+v", got)
	}
}

func TestSettingsValidation(t *testing.T) {
	e := newEnv(t)
	bad := []importer.Settings{
		{Mode: "hardlink", TorrentMode: fsx.ModeHardlink, RecycleRetentionDays: 30},
		{Mode: fsx.ModeMove, TorrentMode: fsx.ModeMove, RecycleRetentionDays: 30},
		{Mode: fsx.ModeMove, TorrentMode: fsx.ModeCopy, RecycleRetentionDays: 0},
		{Mode: fsx.ModeMove, TorrentMode: fsx.ModeCopy, RecycleRetentionDays: 30, AllowedRoots: []string{"relative"}},
		{Mode: fsx.ModeMove, TorrentMode: fsx.ModeCopy, RecycleRetentionDays: 30, AllowedRoots: []string{"/does/not/exist/gonema"}},
		{Mode: fsx.ModeMove, TorrentMode: fsx.ModeCopy, RecycleRetentionDays: 30, RecycleBin: "bin"},
	}
	for i, s := range bad {
		if _, err := e.svc.SetSettings(e.ctx, s); !errors.Is(err, importer.ErrInvalid) {
			t.Errorf("case %d: err = %v", i, err)
		}
	}
	got, err := e.svc.Settings(e.ctx)
	if err != nil || len(got.AllowedRoots) != 1 || got.Mode != fsx.ModeMove {
		t.Errorf("settings = %+v, %v", got, err)
	}
}

func TestSingleRecycleBin(t *testing.T) {
	e := newEnv(t)
	set, _ := e.svc.Settings(e.ctx)
	set.RecycleBin = filepath.Join(t.TempDir(), "bin")
	if _, err := e.svc.SetSettings(e.ctx, set); err != nil {
		t.Fatal(err)
	}
	m := e.movie(120, "The Lord of the Rings", 2001)
	for _, rel := range []string{"The.Lord.of.the.Rings.2001.720p.WEB-DL.H264-A.mkv", "The.Lord.of.the.Rings.2001.1080p.BluRay.x264-B.mkv"} {
		if res, err := e.svc.Import(e.ctx, importer.Request{MovieID: m.ID, LocalPath: e.file(filepath.Join(e.dl, rel), movieSize), Protocol: decision.Usenet}); err != nil || !res.Imported {
			t.Fatalf("import %s = %+v, %v", rel, res, err)
		}
	}
	items, err := e.svc.ListRecycle(e.ctx)
	if err != nil || len(items) != 1 || items[0].Bin != set.RecycleBin || items[0].RootFolderID != 0 {
		t.Fatalf("items = %+v, %v", items, err)
	}
}
