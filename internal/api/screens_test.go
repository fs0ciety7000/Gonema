package api

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/fs0ciety7000/gonema/internal/acquire"
	"github.com/fs0ciety7000/gonema/internal/importer"
	"github.com/fs0ciety7000/gonema/internal/metadata"
	"github.com/fs0ciety7000/gonema/internal/parser"
	"github.com/fs0ciety7000/gonema/internal/store"
	"github.com/fs0ciety7000/gonema/internal/store/db"
)

const p1 = "/api/v1"

func (e *testEnv) setKey(t *testing.T) {
	t.Helper()
	if r := e.do(t, "PUT", p1+"/settings/tmdb", `{"key":"`+testKey+`"}`); r.status != 200 {
		t.Fatalf("set key = %d %s", r.status, r.body)
	}
}

// addMovie adds a provider movie through the API.
func (e *testEnv) addMovie(t *testing.T, tmdbID int, title string, year int, rootID int64) Movie {
	t.Helper()
	d := metadata.Date{Year: year, Month: 1, Day: 1}
	e.prov.movies[tmdbID] = &metadata.Movie{TMDBID: tmdbID, Title: title, Year: year, ReleaseDate: d,
		Releases: metadata.ReleaseSelection{Cinema: metadata.DatedRelease{Date: d, Region: "US"}}}
	r := e.do(t, "POST", p1+"/movies", `{"tmdbId":`+itoa(int64(tmdbID))+`,"rootFolderId":`+itoa(rootID)+`,"qualityProfileId":`+itoa(e.profID)+`}`)
	if r.status != 201 {
		t.Fatalf("add %s = %d %s", title, r.status, r.body)
	}
	return decode[Movie](t, r)
}

func TestTagsAndBulkRoutes(t *testing.T) {
	e := newTestEnv(t, "")
	e.setKey(t)
	a := e.addMovie(t, 1, "Alien", 1979, e.rootID)
	h := e.addMovie(t, 2, "Heat", 1995, e.rootID)

	r := e.do(t, "POST", p1+"/tags", `{"label":"4k"}`)
	tag := decode[Tag](t, r)
	if r.status != 201 || tag.Label != "4k" {
		t.Fatalf("create tag = %d %s", r.status, r.body)
	}
	problemOK(t, e.do(t, "POST", p1+"/tags", `{"label":"4k"}`), 409)

	r = e.do(t, "POST", p1+"/movies/bulk", `{"ids":[`+itoa(a.ID)+`,`+itoa(h.ID)+`],"set":{"monitored":false,"addTags":["4k","kids"]}}`)
	res := decode[BulkResult](t, r)
	if r.status != 200 || len(res.Changed) != 2 || len(res.Previous) != 2 || !res.Previous[0].Monitored || len(res.Previous[0].Tags) != 0 || len(res.Moves) != 0 {
		t.Fatalf("bulk = %d %s", r.status, r.body)
	}
	r = e.do(t, "POST", p1+"/movies/"+itoa(h.ID)+"/tags", `{"remove":["kids"]}`)
	if one := decode[BulkResult](t, r); r.status != 200 || !slices.Equal(one.Previous[0].Tags, []string{"4k", "kids"}) {
		t.Fatalf("movie tags = %d %s", r.status, r.body)
	}

	list := decode[[]Tag](t, e.do(t, "GET", p1+"/tags", ""))
	if len(list) != 2 || list[0].Label != "4k" || list[0].Movies != 2 || list[1].Movies != 1 {
		t.Errorf("tags = %+v", list)
	}
	all := decode[MoviePage](t, e.do(t, "GET", p1+"/movies?tags=4k,kids&tagMatch=all", ""))
	if len(all.Movies) != 1 || all.Movies[0].ID != a.ID || len(all.Movies[0].TagIDs) != 2 {
		t.Errorf("tags all = %+v", all.Movies)
	}
	anyOf := decode[MoviePage](t, e.do(t, "GET", p1+"/movies?tags=4k,kids", ""))
	if anyOf.Total != 2 {
		t.Errorf("tags any = %d", anyOf.Total)
	}
	cols := decode[MovieColumns](t, e.do(t, "GET", p1+"/movies/ids?tags=kids", ""))
	if cols.Total != 1 || len(cols.TagIDs[0]) != 2 || cols.SortTitle[0] != "alien" || cols.QualityProfileID[0] != e.profID ||
		cols.MinimumAvailability[0] != "released" || cols.ReleaseCinema[0] == nil || *cols.ReleaseCinema[0] != "1979-01-01" {
		t.Errorf("columns = %+v", cols)
	}

	// Undo of the bulk edit.
	body, _ := json.Marshal(map[string]any{"previous": res.Previous})
	r = e.do(t, "POST", p1+"/movies/bulk/restore", string(body))
	if r.status != 200 {
		t.Fatalf("restore = %d %s", r.status, r.body)
	}
	for _, id := range []int64{a.ID, h.ID} {
		d := decode[MovieDetail](t, e.do(t, "GET", p1+"/movies/"+itoa(id), ""))
		if !d.Monitored || len(d.Tags) != 0 {
			t.Errorf("movie %d after undo: monitored %v tags %v", id, d.Monitored, d.Tags)
		}
	}
	problemOK(t, e.do(t, "POST", p1+"/movies/bulk", `{"ids":[99999],"set":{"monitored":true}}`), 404)
	problemOK(t, e.do(t, "POST", p1+"/movies/bulk", `{"ids":[],"set":{}}`), 422)
	if d := problemOK(t, e.do(t, "POST", p1+"/movies/bulk", `{"ids":[`+itoa(a.ID)+`],"set":{"rootFolderId":1}}`), 503); !strings.Contains(d, "not available") {
		t.Errorf("move without importer = %q", d)
	}

	r = e.do(t, "DELETE", p1+"/tags/"+itoa(tag.ID), "")
	if del := decode[DeletedTag](t, r); r.status != 200 || del.Tag.Label != "4k" {
		t.Errorf("delete tag = %d %s", r.status, r.body)
	}
	problemOK(t, e.do(t, "PUT", p1+"/tags/"+itoa(tag.ID), `{"label":"x"}`), 404)
}

func TestHistoryRoute(t *testing.T) {
	e := newTestEnv(t, "")
	e.setKey(t)
	m := e.addMovie(t, 1, "Alien", 1979, e.rootID)
	ctx := context.Background()
	w := e.st.Write()
	dl := func(retryOf *int64, at int64) int64 {
		d, err := w.InsertDownload(ctx, db.InsertDownloadParams{
			MovieID: m.ID, ReleaseTitle: "Alien.1979.1080p.BluRay.x264-GRP", Protocol: "usenet", Quality: "bluray-1080p",
			Revision: 1, Decision: "{}", RetryOf: retryOf, GrabbedAt: at,
		})
		if err != nil {
			t.Fatal(err)
		}
		return d.ID
	}
	first := dl(nil, 1000)
	second := dl(&first, 2000)
	third := dl(&second, 3000)
	base := time.Date(2026, 11, 1, 0, 0, 0, 0, time.UTC)
	add := func(event string, dlID int64, day int, reason string) {
		q := "bluray-1080p"
		p := db.AppendHistoryParams{MovieID: &m.ID, Event: event, SourceTitle: "Alien.1979", Quality: &q,
			Data: `{"reason":"` + reason + `"}`, CreatedAt: store.Millis(base.AddDate(0, 0, day))}
		if dlID > 0 {
			p.DownloadID = &dlID
		}
		if _, err := w.AppendHistory(ctx, p); err != nil {
			t.Fatal(err)
		}
	}
	add("grabbed", first, 1, "best of 9")
	add("download_failed", first, 2, "CRC error")
	add("grabbed", second, 3, "retry 2 of 6")
	add("download_failed", second, 3, "password")
	add("grabbed", third, 4, "retry 3 of 6")
	add("imported", third, 4, "linked")

	r := e.do(t, "GET", p1+"/history?event=grabbed,imported&movieId="+itoa(m.ID)+"&limit=2", "")
	page := decode[HistoryPage](t, r)
	if r.status != 200 || len(page.Entries) != 2 || page.NextCursor == "" {
		t.Fatalf("page 1 = %d %s", r.status, r.body)
	}
	top := page.Entries[0]
	if top.Event != "imported" || top.Reason != "linked" || top.Movie == nil || top.Movie.Title != "Alien" ||
		top.QualityName == nil || top.ChainID == nil || *top.ChainID != first || top.RetryOf == nil || *top.RetryOf != second {
		t.Errorf("top row = %+v", top)
	}
	r = e.do(t, "GET", p1+"/history?event=grabbed,imported&movieId="+itoa(m.ID)+"&limit=2&cursor="+page.NextCursor, "")
	page2 := decode[HistoryPage](t, r)
	if len(page2.Entries) != 2 || page2.Entries[0].ID >= page.Entries[1].ID || *page2.Entries[1].ChainID != first {
		t.Errorf("page 2 = %s", r.body)
	}
	since := base.AddDate(0, 0, 2).Format(time.RFC3339)
	until := base.AddDate(0, 0, 4).Format(time.RFC3339)
	ranged := decode[HistoryPage](t, e.do(t, "GET", p1+"/history?since="+since+"&until="+until, ""))
	if len(ranged.Entries) != 3 { // day 2 failure, day 3 grab and failure
		t.Errorf("date range = %+v", ranged.Entries)
	}
	byDl := decode[HistoryPage](t, e.do(t, "GET", p1+"/history?downloadId="+itoa(second), ""))
	if len(byDl.Entries) != 2 {
		t.Errorf("by download = %d", len(byDl.Entries))
	}
	problemOK(t, e.do(t, "GET", p1+"/history?event=bogus", ""), 422)
	problemOK(t, e.do(t, "GET", p1+"/history?cursor=abc", ""), 422)
	problemOK(t, e.do(t, "GET", p1+"/history?since="+until+"&until="+since, ""), 422)
}

func TestQueueEntries(t *testing.T) {
	e := newTestEnv(t, "")
	e.setKey(t)
	m := e.addMovie(t, 1, "Alien", 1979, e.rootID)
	ctx := context.Background()
	w := e.st.Write()
	failed, err := w.InsertDownload(ctx, db.InsertDownloadParams{
		MovieID: m.ID, ReleaseTitle: "Alien.1979.1080p.WEB-DL-ZEPHYR", Protocol: "torrent", Quality: "webdl-1080p", Revision: 1,
		Decision: `{"indexer":"Nebula","seeders":112}`, GrabbedAt: store.Millis(time.Now().Add(-time.Hour)),
	})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := w.UpdateDownloadState(ctx, db.UpdateDownloadStateParams{ID: failed.ID, Status: "failed", StatusMessages: `["password-protected"]`}); err != nil {
		t.Fatal(err)
	}
	retry, err := w.InsertDownload(ctx, db.InsertDownloadParams{
		MovieID: m.ID, ReleaseTitle: "Alien.1979.1080p.WEB-DL-LUMEN", Protocol: "torrent", Size: 1000, Quality: "webdl-1080p", Revision: 1,
		Decision: `{"indexer":"AltReel","seeders":40}`, RetryOf: &failed.ID, GrabbedAt: store.Millis(time.Now()),
	})
	if err != nil {
		t.Fatal(err)
	}
	cid := int64(1)
	items := []acquire.QueueItem{{ID: retry.ID, MovieID: m.ID, Protocol: "torrent", Size: 1000, Status: "downloading", Progress: 0.25,
		ClientID: &cid, Client: "qBittorrent", Messages: []string{"stalled"}}}
	got, err := e.srv.queueEntries(ctx, items, 6)
	if err != nil || len(got) != 1 {
		t.Fatalf("entries = %+v, %v", got, err)
	}
	q := got[0]
	if q.Movie.Title != "Alien" || q.Movie.Year != 1979 || q.Indexer != "AltReel" || q.Seeders == nil || *q.Seeders != 40 ||
		q.DownloadedBytes != 250 || q.StatusMessage != "stalled" || !q.Actions.Pause || q.Actions.Resume || q.Actions.RetryImport {
		t.Errorf("entry = %+v", q)
	}
	if q.Retry == nil || q.Retry.Attempt != 2 || q.Retry.Of != 6 || q.Retry.PreviousReason != "password-protected" ||
		q.Retry.PreviousRelease != "Alien.1979.1080p.WEB-DL-ZEPHYR" {
		t.Errorf("retry = %+v", q.Retry)
	}
	done := time.Now()
	items[0].Status, items[0].CompletedAt, items[0].Messages = "warning", &done, []string{"Import failed: disk full"}
	got, _ = e.srv.queueEntries(ctx, items, 6)
	if q := got[0]; !q.ImportFailed || q.FailedReason == nil || !q.Actions.RetryImport || q.Actions.Pause {
		t.Errorf("import failed entry = %+v", q)
	}
}

func TestDiscoverRoutes(t *testing.T) {
	e := newTestEnv(t, "")
	e.setKey(t)
	a := e.addMovie(t, 1, "Alien", 1979, e.rootID)
	e.prov.popular = []metadata.MovieSummary{{TMDBID: 1, Title: "Alien", PosterPath: "/alien.jpg"}, {TMDBID: 2, Title: "New"}}
	r := e.do(t, "GET", p1+"/discover/popular", "")
	page := decode[DiscoverPage](t, r)
	if r.status != 200 || len(page.Results) != 2 {
		t.Fatalf("popular = %d %s", r.status, r.body)
	}
	if m := page.Results[0]; !m.InLibrary || m.MovieID == nil || *m.MovieID != a.ID || m.State == nil || *m.State != "missing" ||
		m.PosterURL == nil || *m.PosterURL != "/api/v1/images/poster/w342/alien.jpg" {
		t.Errorf("in library = %+v", m)
	}
	if m := page.Results[1]; m.InLibrary || m.MovieID != nil || m.State != nil {
		t.Errorf("not in library = %+v", m)
	}
	problemOK(t, e.do(t, "GET", p1+"/discover/top", ""), 422)

	e.prov.collections = map[int]*metadata.Collection{8091: {TMDBID: 8091, Name: "Alien Collection",
		Parts: []metadata.MovieSummary{{TMDBID: 1, Title: "Alien"}, {TMDBID: 3, Title: "Aliens"}}}}
	e.prov.movies[3] = &metadata.Movie{TMDBID: 3, Title: "Aliens", Year: 1986}
	c := decode[DiscoverCollection](t, e.do(t, "GET", p1+"/discover/collection/8091", ""))
	if c.Name != "Alien Collection" || c.InLibrary != 1 || len(c.Parts) != 2 || !c.Parts[0].InLibrary {
		t.Errorf("collection = %+v", c)
	}
	problemOK(t, e.do(t, "GET", p1+"/discover/collection/1", ""), 404)
	r = e.do(t, "POST", p1+"/collections/8091/add", `{"rootFolderId":`+itoa(e.rootID)+`,"qualityProfileId":`+itoa(e.profID)+`,"monitorCollection":true}`)
	added := decode[AddCollectionResult](t, r)
	if r.status != 200 || len(added.Added) != 1 || added.Added[0].Title != "Aliens" || !slices.Equal(added.Existing, []int64{a.ID}) || !added.Collection.Monitored {
		t.Errorf("add collection = %d %s", r.status, r.body)
	}

	e.prov.people = map[int]*metadata.Filmography{42: {PersonID: 42,
		Cast: []metadata.FilmCredit{{Movie: metadata.MovieSummary{TMDBID: 1, Title: "Alien"}, Character: "Ripley"}},
		Crew: []metadata.FilmCredit{{Movie: metadata.MovieSummary{TMDBID: 1, Title: "Alien"}, Job: "Producer", Department: "Production"}},
	}}
	f := decode[Filmography](t, e.do(t, "GET", p1+"/discover/person/42", ""))
	if len(f.Movies) != 1 || !f.Movies[0].InLibrary || !slices.Equal(f.Movies[0].Characters, []string{"Ripley"}) || !slices.Equal(f.Movies[0].Jobs, []string{"Producer"}) {
		t.Errorf("filmography = %+v", f)
	}
	look := decode[[]LookupResult](t, e.do(t, "GET", p1+"/lookup?q=alien", ""))
	for _, l := range look {
		if l.TMDBID == 1 && (!l.InLibrary || l.State == nil || !l.Monitored) {
			t.Errorf("lookup = %+v", l)
		}
	}

	// Wanted: Alien is monitored, missing and released; Aliens has no release
	// date (announced), so it is not wanted yet.
	wanted := decode[MoviePage](t, e.do(t, "GET", p1+"/wanted?kind=missing", ""))
	if wanted.Total != 1 || wanted.Movies[0].ID != a.ID {
		t.Errorf("wanted = %+v", wanted)
	}
	if cu := decode[MoviePage](t, e.do(t, "GET", p1+"/wanted?kind=cutoff_unmet", "")); cu.Total != 0 {
		t.Errorf("cutoff unmet = %+v", cu)
	}
}

// fileEnv is an API with the import routes and a real root folder.
func fileEnv(t *testing.T) (e *testEnv, root string, rootID int64) {
	t.Helper()
	e = newImportEnv(t)
	e.setKey(t)
	root = t.TempDir()
	r := e.do(t, "POST", p1+"/root-folders", `{"path":`+quote(root)+`}`)
	if r.status != 201 {
		t.Fatalf("root folder = %d %s", r.status, r.body)
	}
	return e, root, decode[RootFolder](t, r).ID
}

func (e *testEnv) putFile(t *testing.T, root string, m Movie, rel, scene string) string {
	t.Helper()
	p := filepath.Join(root, m.Path, rel)
	if err := os.MkdirAll(filepath.Dir(p), 0o750); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(p, []byte("video"), 0o600); err != nil {
		t.Fatal(err)
	}
	parsed := parser.Parse(scene)
	raw, _ := json.Marshal(&parsed)
	if _, err := e.st.Write().UpsertMovieFile(context.Background(), db.UpsertMovieFileParams{
		MovieID: m.ID, RelativePath: rel, Size: 5, Quality: "bluray-1080p", Revision: 1, Languages: "[]",
		SceneName: scene, Parsed: string(raw), MediaInfo: "{}", ImportedAt: 1,
	}); err != nil {
		t.Fatal(err)
	}
	return p
}

func fileExists(p string) bool {
	_, err := os.Lstat(p)
	return err == nil
}

func TestFileOperationRoutes(t *testing.T) {
	e, root, rootID := fileEnv(t)
	m := e.addMovie(t, 1, "Alien", 1979, rootID)
	video := e.putFile(t, root, m, "alien.mkv", "Alien.1979.1080p.BluRay.x264-GRP")

	// Detail: score breakdown of the file.
	d := decode[MovieDetail](t, e.do(t, "GET", p1+"/movies/"+itoa(m.ID), ""))
	if d.File == nil || d.File.ScoreBreakdown.Rules == nil || d.Studios == nil || d.ReleaseRegions.Cinema == nil || *d.ReleaseRegions.Cinema != "US" {
		t.Errorf("detail = file %+v studios %v regions %+v", d.File, d.Studios, d.ReleaseRegions)
	}

	r := e.do(t, "GET", p1+"/movies/"+itoa(m.ID)+"/rename/preview", "")
	plan := decode[importer.Plan](t, r)
	if r.status != 200 || plan.FileFrom != "alien.mkv" || !strings.HasPrefix(plan.FileTo, "Alien (1979)") || len(plan.Ops) != 1 {
		t.Fatalf("rename preview = %d %s", r.status, r.body)
	}
	r = e.do(t, "POST", p1+"/movies/"+itoa(m.ID)+"/rename", "")
	if r.status != 200 || fileExists(video) || !fileExists(filepath.Join(root, m.Path, plan.FileTo)) {
		t.Fatalf("rename = %d %s", r.status, r.body)
	}
	r = e.do(t, "POST", p1+"/movies/rename?preview=true", `{"ids":[`+itoa(m.ID)+`]}`)
	if b := decode[BulkFileResult](t, r); r.status != 200 || len(b.Results) != 1 || !b.Results[0].OK || !b.Results[0].Relocation.NoChange {
		t.Errorf("bulk rename preview = %d %s", r.status, r.body)
	}

	// Move to another root folder: preview, then move.
	archive := t.TempDir()
	r = e.do(t, "POST", p1+"/root-folders", `{"path":`+quote(archive)+`}`)
	archiveID := decode[RootFolder](t, r).ID
	moveBody := `{"rootFolderId":` + itoa(archiveID) + `,"moveFiles":true}`
	r = e.do(t, "POST", p1+"/movies/"+itoa(m.ID)+"/move?preview=true", moveBody)
	if rel := decode[importer.Relocation](t, r); r.status != 200 || len(rel.Ops) != 1 || len(rel.Done) != 0 {
		t.Fatalf("move preview = %d %s", r.status, r.body)
	}
	if !fileExists(filepath.Join(root, m.Path, plan.FileTo)) {
		t.Fatal("preview moved the file")
	}
	r = e.do(t, "POST", p1+"/movies/"+itoa(m.ID)+"/move", moveBody)
	moved := filepath.Join(archive, m.Path, plan.FileTo)
	if rel := decode[importer.Relocation](t, r); r.status != 200 || len(rel.Done) != 1 || !fileExists(moved) {
		t.Fatalf("move = %d %s", r.status, r.body)
	}
	if err := os.MkdirAll(filepath.Join(root, m.Path), 0o750); err != nil {
		t.Fatal(err)
	}
	problemOK(t, e.do(t, "POST", p1+"/movies/"+itoa(m.ID)+"/move", `{"rootFolderId":`+itoa(rootID)+`,"moveFiles":true}`), 409)
	r = e.do(t, "POST", p1+"/movies/bulk", `{"ids":[`+itoa(m.ID)+`],"set":{"rootFolderId":`+itoa(rootID)+`,"moveFiles":true}}`)
	if b := decode[BulkResult](t, r); r.status != 200 || len(b.Moves) != 1 || b.Moves[0].OK || !strings.Contains(b.Moves[0].Error, "already exists") ||
		b.Previous[0].RootFolderID != archiveID {
		t.Errorf("bulk move = %d %s", r.status, r.body)
	}

	// Delete the file: refused without confirm, recycled with.
	if dt := problemOK(t, e.do(t, "DELETE", p1+"/movies/"+itoa(m.ID)+"/file", ""), 422); !strings.Contains(dt, "recycle bin") || !strings.Contains(dt, "confirm=true") {
		t.Errorf("detail = %q", dt)
	}
	if !fileExists(moved) {
		t.Fatal("deleted without confirmation")
	}
	r = e.do(t, "DELETE", p1+"/movies/"+itoa(m.ID)+"/file?confirm=true", "")
	del := decode[importer.Deletion](t, r)
	if r.status != 200 || len(del.Recycled) != 1 || fileExists(moved) || !fileExists(filepath.Join(del.Bin, del.Recycled[0])) {
		t.Fatalf("delete file = %d %s", r.status, r.body)
	}
	problemOK(t, e.do(t, "DELETE", p1+"/movies/"+itoa(m.ID)+"/file?confirm=true", ""), 404)

	// Remove the movie with its files.
	m2 := e.addMovie(t, 2, "Heat", 1995, rootID)
	v2 := e.putFile(t, root, m2, "heat.mkv", "Heat.1995.1080p.BluRay.x264-GRP")
	problemOK(t, e.do(t, "DELETE", p1+"/movies/"+itoa(m2.ID)+"?deleteFiles=true", ""), 422)
	if !fileExists(v2) {
		t.Fatal("deleted without confirmation")
	}
	r = e.do(t, "DELETE", p1+"/movies/"+itoa(m2.ID)+"?deleteFiles=true&confirm=true", "")
	if del := decode[importer.Deletion](t, r); r.status != 200 || len(del.Recycled) != 1 || fileExists(v2) || fileExists(filepath.Dir(v2)) {
		t.Fatalf("delete with files = %d %s", r.status, r.body)
	}
	problemOK(t, e.do(t, "GET", p1+"/movies/"+itoa(m2.ID), ""), 404)
	h := decode[HistoryPage](t, e.do(t, "GET", p1+"/history?event=file_deleted,file_renamed", ""))
	var kinds []string
	for _, row := range h.Entries {
		kinds = append(kinds, row.Event)
	}
	if !slices.Equal(kinds, []string{"file_deleted", "file_deleted", "file_renamed", "file_renamed"}) {
		t.Errorf("history = %v", kinds)
	}

	// Default removal keeps the files.
	m3 := e.addMovie(t, 3, "Zodiac", 2007, rootID)
	v3 := e.putFile(t, root, m3, "z.mkv", "Zodiac.2007.1080p.BluRay.x264-GRP")
	if r := e.do(t, "DELETE", p1+"/movies/"+itoa(m3.ID), ""); r.status != 204 || !fileExists(v3) {
		t.Errorf("default delete = %d, file kept %v", r.status, fileExists(v3))
	}
}
