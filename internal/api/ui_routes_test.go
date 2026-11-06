package api

import (
	"bytes"
	"cmp"
	"compress/gzip"
	"context"
	"encoding/json"
	"image"
	"image/color"
	"image/png"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"net/url"
	"path/filepath"
	"slices"
	"strings"
	"sync/atomic"
	"testing"
	"testing/fstest"
	"time"

	"github.com/fs0ciety7000/gonema/internal/acquire"
	"github.com/fs0ciety7000/gonema/internal/demo"
	"github.com/fs0ciety7000/gonema/internal/events"
	"github.com/fs0ciety7000/gonema/internal/imagecache"
	"github.com/fs0ciety7000/gonema/internal/jobs"
	"github.com/fs0ciety7000/gonema/internal/library"
	"github.com/fs0ciety7000/gonema/internal/quality"
	"github.com/fs0ciety7000/gonema/internal/secretbox"
	"github.com/fs0ciety7000/gonema/internal/store"
	"github.com/fs0ciety7000/gonema/internal/store/db"
	"github.com/fs0ciety7000/gonema/internal/webui"
)

var demoNow = time.Date(2026, 11, 6, 12, 0, 0, 0, time.UTC)

// newDemoEnv serves a demo library of films fictional movies (with files,
// downloads, failures, collections and tags).
func newDemoEnv(tb testing.TB, films int, baseURL string, tweak func(*Deps)) *testEnv {
	tb.Helper()
	ctx := context.Background()
	dir := tb.TempDir()
	if _, err := demo.Seed(ctx, demo.Options{DataDir: dir, Films: films, Seed: 42, Now: demoNow}); err != nil {
		tb.Fatal(err)
	}
	st, err := store.Open(ctx, filepath.Join(dir, demo.DBFile))
	if err != nil {
		tb.Fatal(err)
	}
	tb.Cleanup(func() { _ = st.Close() })
	key, _ := secretbox.NewKey()
	box, _ := secretbox.New(key)
	bus := events.New(events.Options{})
	log := slog.New(slog.DiscardHandler)
	runner := jobs.New(st, jobs.Options{Workers: 1, Bus: bus, Logger: log})
	lib, err := library.New(library.Options{Store: st, Box: box, Jobs: runner, Bus: bus, Logger: log,
		NewProvider: func(string) (library.KeyedProvider, error) { return &provider{}, nil }})
	if err != nil {
		tb.Fatal(err)
	}
	if err := lib.Init(ctx); err != nil {
		tb.Fatal(err)
	}
	d := Deps{Library: lib, Jobs: runner, Bus: bus, Store: st, Logger: log, Version: "test",
		StartedAt: time.Now(), DataDir: dir, BaseURL: baseURL}
	if tweak != nil {
		tweak(&d)
	}
	srv := New(d)
	ts := httptest.NewServer(srv.Handler())
	tb.Cleanup(func() {
		srv.Shutdown()
		ts.Close()
	})
	return &testEnv{srv: srv, ts: ts, lib: lib, bus: bus, st: st}
}

func (e *testEnv) columns(t *testing.T, query string) MovieColumns {
	t.Helper()
	r := e.do(t, "GET", "/api/v1/movies/ids?"+query, "")
	if r.status != 200 {
		t.Fatalf("columns %q = %d %s", query, r.status, r.body)
	}
	return decode[MovieColumns](t, r)
}

// pages walks every page of /movies and returns the items.
func (e *testEnv) pages(t *testing.T, query string, limit int) []MovieListItem {
	t.Helper()
	var out []MovieListItem
	cursor := ""
	for range 1000 {
		q := query + "&limit=" + itoa(int64(limit))
		if cursor != "" {
			q += "&cursor=" + url.QueryEscape(cursor)
		}
		r := e.do(t, "GET", "/api/v1/movies?"+q, "")
		if r.status != 200 {
			t.Fatalf("list %q = %d %s", q, r.status, r.body)
		}
		p := decode[MoviePage](t, r)
		out = append(out, p.Movies...)
		if p.NextCursor == "" {
			if p.Total != len(out) {
				t.Errorf("%q: total %d, got %d movies", query, p.Total, len(out))
			}
			return out
		}
		cursor = p.NextCursor
	}
	t.Fatal("pagination does not end")
	return nil
}

func TestMovieListAndColumns(t *testing.T) {
	e := newDemoEnv(t, 300, "", nil)

	all := e.pages(t, "sort=title", 500)
	if len(all) != 300 {
		t.Fatalf("%d movies, want 300", len(all))
	}
	var withFile, downloading, failed int
	for _, m := range all {
		if m.PosterURL == nil || !strings.HasPrefix(*m.PosterURL, "/api/v1/images/poster/w342/demo-p-") ||
			m.DominantColor == nil || m.Title == "" || m.SortTitle == "" || m.Path == "" || m.RootFolderID == 0 ||
			len(m.Genres.Strings()) == 0 || !strings.Contains(string(m.Ratings), `"tmdb"`) || m.AddedAt.IsZero() {
			t.Fatalf("incomplete item %+v", m)
		}
		if m.Quality != nil {
			withFile++
			if m.QualityName == nil || *m.QualityName != quality.ID(*m.Quality).Name() || m.SizeOnDisk == nil || *m.SizeOnDisk <= 0 || m.CustomScore == nil {
				t.Errorf("file fields of %d: %+v", m.ID, m)
			}
		} else if m.SizeOnDisk != nil || m.CustomScore != nil || len(m.HDR) != 0 {
			t.Errorf("file fields without a file: %+v", m)
		}
		if m.State == "downloading" {
			downloading++
			if m.Download == nil || m.Download.Progress < 0 || m.Download.Progress > 1 {
				t.Errorf("download of %d = %+v", m.ID, m.Download)
			}
		} else if m.Download != nil {
			t.Errorf("download on a %s movie", m.State)
		}
		if m.Failed {
			failed++
		}
	}
	if withFile == 0 || downloading == 0 || failed == 0 {
		t.Errorf("demo mix: %d with file, %d downloading, %d failed", withFile, downloading, failed)
	}

	// Every sort and order pages the same way as the columns endpoint, without
	// duplicates, and in order.
	for _, sort := range []string{"title", "year", "added", "size", "score", "quality"} {
		for _, order := range []string{"", "asc", "desc"} {
			q := "sort=" + sort
			if order != "" {
				q += "&order=" + order
			}
			paged := e.pages(t, q, 37)
			cols := e.columns(t, q)
			ids := make([]int64, len(paged))
			for i, m := range paged {
				ids[i] = m.ID
			}
			if !slices.Equal(ids, cols.ID) || cols.Total != 300 {
				t.Fatalf("%s: pages and columns disagree (%d vs %d ids)", q, len(ids), len(cols.ID))
			}
			if len(slices.Compact(slices.Sorted(slices.Values(ids)))) != 300 {
				t.Fatalf("%s: duplicates", q)
			}
			desc := order == "desc" || order == "" && sort != "title"
			checkOrder(t, q, paged, sort, desc)
		}
	}

	// Filters.
	for _, tc := range []struct {
		query string
		ok    func(MovieListItem) bool
	}{
		{"state=missing", func(m MovieListItem) bool { return m.State == "missing" }},
		{"state=available,upgradable", func(m MovieListItem) bool { return m.State == "available" || m.State == "upgradable" }},
		{"state=unmonitored", func(m MovieListItem) bool { return !m.Monitored }},
		{"state=failed", func(m MovieListItem) bool { return m.Failed }},
		{"state=downloading", func(m MovieListItem) bool { return m.Download != nil }},
		{"monitored=true", func(m MovieListItem) bool { return m.Monitored }},
		{"status=released", func(m MovieListItem) bool { return m.Status == "released" }},
		{"quality=webdl-1080p", func(m MovieListItem) bool { return m.Quality != nil && *m.Quality == "webdl-1080p" }},
		{"yearFrom=2000&yearTo=2010", func(m MovieListItem) bool { return m.Year >= 2000 && m.Year <= 2010 }},
		{"genre=Drama", func(m MovieListItem) bool { return slices.Contains(m.Genres.Strings(), "Drama") }},
		{"q=the", func(m MovieListItem) bool { return strings.Contains(strings.ToLower(m.Title), "the") }},
		{"rootFolderId=" + itoa(all[0].RootFolderID), func(m MovieListItem) bool { return m.RootFolderID == all[0].RootFolderID }},
	} {
		got := e.pages(t, tc.query, 500)
		if len(got) == 0 || len(got) == 300 {
			t.Errorf("%s: %d movies, want a strict subset", tc.query, len(got))
		}
		for _, m := range got {
			if !tc.ok(m) {
				t.Errorf("%s: unexpected %+v", tc.query, m)
				break
			}
		}
	}
	tagged := e.pages(t, "tag=favorites", 500)
	if len(tagged) == 0 || len(tagged) > 150 {
		t.Errorf("tag filter: %d movies", len(tagged))
	}
	if got := e.pages(t, "q=zzzz-no-such-title", 10); len(got) != 0 {
		t.Errorf("unmatched text: %d movies", len(got))
	}

	// Columns carry the same data as the rows.
	cols := e.columns(t, "sort=title")
	byID := map[int64]MovieListItem{}
	for _, m := range all {
		byID[m.ID] = m
	}
	for i, id := range cols.ID {
		m := byID[id]
		if cols.Title[i] != m.Title || cols.Year[i] != m.Year || cols.State[i] != m.State || cols.Monitored[i] != m.Monitored ||
			*cols.PosterURL[i] != *m.PosterURL || !eqp(cols.Quality[i], m.Quality) || !eqp(cols.SizeOnDisk[i], m.SizeOnDisk) ||
			cols.HDR[i] != strings.Join(m.HDR, " ") || cols.AddedAt[i] != m.AddedAt.UnixMilli() || cols.Path[i] != m.Path {
			t.Fatalf("column %d disagrees with row %+v", i, m)
		}
		if m.Quality != nil && cols.QualityNames[*m.Quality] != *m.QualityName {
			t.Errorf("quality name of %s", *m.Quality)
		}
		if (cols.Progress[i] != nil) != (m.State == "downloading") {
			t.Errorf("progress of %d", id)
		}
	}

	// Invalid inputs are 422 problems.
	for _, q := range []string{"sort=rating", "order=up", "state=gone", "quality=8k-holo", "cursor=garbage", "limit=501",
		"yearFrom=2010&yearTo=2000"} {
		problemOK(t, e.do(t, "GET", "/api/v1/movies?"+q, ""), 422)
	}
	first := decode[MoviePage](t, e.do(t, "GET", "/api/v1/movies?sort=year&limit=5", ""))
	problemOK(t, e.do(t, "GET", "/api/v1/movies?sort=title&cursor="+first.NextCursor, ""), 422)
}

func eqp[T comparable](a, b *T) bool {
	if a == nil || b == nil {
		return a == b
	}
	return *a == *b
}

func checkOrder(t *testing.T, q string, items []MovieListItem, sort string, desc bool) {
	t.Helper()
	key := func(m MovieListItem) int64 {
		switch sort {
		case "year":
			return m.Year
		case "added":
			return m.AddedAt.UnixMilli()
		case "size":
			if m.SizeOnDisk == nil {
				return -1
			}
			return *m.SizeOnDisk
		case "score":
			if m.CustomScore == nil {
				return -1 << 62
			}
			return *m.CustomScore
		case "quality":
			return library.QualityRank(m.Quality)
		}
		return 0
	}
	for i := 1; i < len(items); i++ {
		a, b := items[i-1], items[i]
		var c int
		if sort == "title" {
			c = strings.Compare(a.SortTitle, b.SortTitle)
		} else {
			c = cmp.Compare(key(a), key(b))
		}
		if c == 0 {
			c = cmp.Compare(a.ID, b.ID)
		}
		if desc {
			c = -c
		}
		if c >= 0 {
			t.Fatalf("%s: %d then %d out of order", q, a.ID, b.ID)
		}
	}
}

// fakeQueue is a live queue.
type fakeQueue struct{ items []acquire.QueueItem }

func (f *fakeQueue) Queue(context.Context) ([]acquire.QueueItem, error) { return f.items, nil }

func TestMovieDetailStatsAndQueue(t *testing.T) {
	q := &fakeQueue{}
	e := newDemoEnv(t, 300, "", func(d *Deps) { d.Queue = q })
	cols := e.columns(t, "sort=title")

	// A movie with a file and a collection.
	var withFile, inCollection, dl int64
	for _, m := range e.pages(t, "sort=title", 500) {
		if m.Quality != nil && withFile == 0 {
			withFile = m.ID
		}
		if m.CollectionID != nil && inCollection == 0 {
			inCollection = m.ID
		}
		if m.State == "downloading" && dl == 0 {
			dl = m.ID
		}
	}
	d := decode[MovieDetail](t, e.do(t, "GET", "/api/v1/movies/"+itoa(withFile), ""))
	if d.File == nil || d.File.FullPath == "" || !strings.HasPrefix(d.File.FullPath, d.RootFolderPath) || d.File.Size <= 0 ||
		d.File.QualityName == "" || d.File.Media.Resolution == "" || d.File.SceneName == "" || len(d.File.Languages.Strings()) != 1 {
		t.Errorf("file = %+v", d.File)
	}
	if d.QualityProfileName == "" || d.RootFolderPath == "" || len(d.Cast) != 8 || len(d.Crew) != 2 || d.Cast[0].Name == "" ||
		d.Cast[0].Character == "" || len(d.History) < 2 || d.StatusReason == "" || d.Tagline == "" || d.Certification == "" ||
		d.RuntimeMin == 0 || d.ReleaseCinema == nil || d.ReleaseDigital == nil || d.ReleasePhysical == nil {
		t.Errorf("detail = %+v", d)
	}
	if d.PosterURL == nil || !strings.Contains(*d.PosterURL, "/w342/") || d.PosterURLLarge == nil || !strings.Contains(*d.PosterURLLarge, "/w780/") ||
		d.BackdropURL == nil || !strings.Contains(*d.BackdropURL, "/backdrop/w1280/demo-b-") {
		t.Errorf("image URLs = %v %v %v", d.PosterURL, d.PosterURLLarge, d.BackdropURL)
	}
	if d.Videos == nil || d.Keywords == nil || d.AlternateTitles == nil {
		t.Error("nil lists in the detail")
	}
	c := decode[MovieDetail](t, e.do(t, "GET", "/api/v1/movies/"+itoa(inCollection), ""))
	if c.Collection == nil || c.Collection.Name == "" || !slices.ContainsFunc(c.Collection.InLibrary, func(p CollectionPart) bool { return p.ID == inCollection }) {
		t.Errorf("collection = %+v", c.Collection)
	}

	// Live progress replaces the persisted one.
	eta := int64(90)
	q.items = []acquire.QueueItem{
		{ID: 7, MovieID: dl, Status: "downloading", Progress: 0.66, SpeedBps: 5 << 20, EtaSeconds: &eta},
		{ID: 8, MovieID: 999999, Status: "paused", Progress: 0.1},
		{ID: 9, MovieID: 999998, Status: "warning", SpeedBps: 1 << 20},
	}
	dd := decode[MovieDetail](t, e.do(t, "GET", "/api/v1/movies/"+itoa(dl), ""))
	if dd.Download == nil || dd.Download.ID != 7 || dd.Download.Progress != 0.66 || dd.File != nil {
		t.Errorf("download = %+v", dd.Download)
	}
	page := decode[MoviePage](t, e.do(t, "GET", "/api/v1/movies?state=downloading&limit=500", ""))
	for _, m := range page.Movies {
		if m.ID == dl && (m.Download == nil || m.Download.Progress != 0.66) {
			t.Errorf("list download = %+v", m.Download)
		}
	}
	sum := decode[QueueSummary](t, e.do(t, "GET", "/api/v1/queue/summary", ""))
	if sum.Count != 3 || sum.Downloading != 1 || sum.Paused != 1 || sum.Warnings != 1 || sum.TotalSpeedBps != 6<<20 ||
		sum.EtaSeconds == nil || *sum.EtaSeconds != 90 {
		t.Errorf("summary = %+v", sum)
	}

	// Stats agree with the columns.
	st := decode[LibraryStats](t, e.do(t, "GET", "/api/v1/library/stats", ""))
	count := func(f func(i int) bool) int64 {
		var n int64
		for i := range cols.ID {
			if f(i) {
				n++
			}
		}
		return n
	}
	var size int64
	for _, s := range cols.SizeOnDisk {
		if s != nil {
			size += *s
		}
	}
	want := LibraryStats{
		Total: 300, OnDisk: count(func(i int) bool { return cols.Quality[i] != nil }), TotalSize: size,
		Monitored: count(func(i int) bool { return cols.Monitored[i] }), Unmonitored: count(func(i int) bool { return !cols.Monitored[i] }),
		Available: count(func(i int) bool { return cols.State[i] == "available" }), Missing: count(func(i int) bool { return cols.State[i] == "missing" }),
		Downloading: count(func(i int) bool { return cols.State[i] == "downloading" }), Upgradable: count(func(i int) bool { return cols.State[i] == "upgradable" }),
		CutoffUnmet: count(func(i int) bool { return cols.Monitored[i] && cols.State[i] == "upgradable" }),
		Wanted: count(func(i int) bool {
			return cols.Monitored[i] && cols.State[i] == "missing" && cols.Status[i] == "released"
		}),
	}
	if st != want {
		t.Errorf("stats = %+v\nwant    %+v", st, want)
	}
	// Demo flag.
	if s := decode[SystemStatus](t, e.do(t, "GET", "/api/v1/system/status", "")); !s.Demo {
		t.Error("system/status demo = false on a demo database")
	}
	problemOK(t, e.do(t, "GET", "/api/v1/movies/999999", ""), 404)
}

func TestSavedViews(t *testing.T) {
	e := newTestEnv(t, "")
	if r := e.do(t, "GET", "/api/v1/views", ""); r.status != 200 || strings.TrimSpace(r.body) != "[]" {
		t.Fatalf("empty list = %d %s", r.status, r.body)
	}
	r := e.do(t, "POST", "/api/v1/views", `{"page":"library","name":"4K HDR","filters":{"quality":["remux-2160p"],"hdr":true},"sort":{"by":"added","order":"desc"},"columns":["title","quality","size"],"density":"compact","position":2}`)
	if r.status != 201 {
		t.Fatalf("create = %d %s", r.status, r.body)
	}
	v := decode[SavedView](t, r)
	if v.ID == 0 || v.Name != "4K HDR" || v.Density != "compact" || len(v.Columns) != 3 || !strings.Contains(string(v.Filters), "remux-2160p") || v.Position != 2 {
		t.Errorf("created = %+v", v)
	}
	second := decode[SavedView](t, e.do(t, "POST", "/api/v1/views", `{"page":"library","name":"Wanted"}`))
	if second.Density != "airy" || string(second.Filters) != "{}" || string(second.Sort) != "{}" || len(second.Columns) != 0 {
		t.Errorf("defaults = %+v", second)
	}
	problemOK(t, e.do(t, "POST", "/api/v1/views", `{"page":"library","name":"Wanted"}`), 409)
	if r := e.do(t, "POST", "/api/v1/views", `{"page":"calendar","name":"Wanted"}`); r.status != 201 {
		t.Errorf("same name on another page = %d", r.status)
	}
	problemOK(t, e.do(t, "POST", "/api/v1/views", `{"page":"library","name":"x","filters":[1,2]}`), 422)
	problemOK(t, e.do(t, "POST", "/api/v1/views", `{"page":"Library!","name":"x"}`), 422)
	problemOK(t, e.do(t, "POST", "/api/v1/views", `{"page":"library","name":"x","density":"cozy"}`), 422)
	problemOK(t, e.do(t, "POST", "/api/v1/views", `{"page":"library","name":"x","filters":{"q":"`+strings.Repeat("a", 9000)+`"}}`), 422)

	list := decode[[]SavedView](t, e.do(t, "GET", "/api/v1/views?page=library", ""))
	if len(list) != 2 || list[0].Name != "Wanted" || list[1].Name != "4K HDR" { // position order
		t.Errorf("list = %+v", list)
	}
	r = e.do(t, "PUT", "/api/v1/views/"+itoa(v.ID), `{"page":"library","name":"4K","density":"airy","columns":["title"]}`)
	if u := decode[SavedView](t, r); r.status != 200 || u.Name != "4K" || u.Density != "airy" || len(u.Columns) != 1 || string(u.Filters) != "{}" {
		t.Errorf("update = %d %+v", r.status, u)
	}
	problemOK(t, e.do(t, "PUT", "/api/v1/views/"+itoa(v.ID), `{"page":"library","name":"Wanted"}`), 409)
	if g := decode[SavedView](t, e.do(t, "GET", "/api/v1/views/"+itoa(v.ID), "")); g.Name != "4K" {
		t.Errorf("get = %+v", g)
	}
	if r := e.do(t, "DELETE", "/api/v1/views/"+itoa(v.ID), ""); r.status != 204 {
		t.Errorf("delete = %d", r.status)
	}
	problemOK(t, e.do(t, "DELETE", "/api/v1/views/"+itoa(v.ID), ""), 404)
	problemOK(t, e.do(t, "GET", "/api/v1/views/"+itoa(v.ID), ""), 404)
	problemOK(t, e.do(t, "PUT", "/api/v1/views/999", `{"page":"library","name":"n"}`), 404)
}

type staticBase string

func (b staticBase) ImageBaseURL(context.Context) (string, error) { return string(b), nil }

func pngBytes(t *testing.T) []byte {
	t.Helper()
	img := image.NewRGBA(image.Rect(0, 0, 4, 6))
	for i := range 24 {
		img.Set(i%4, i/4, color.RGBA{200, 80, 40, 255})
	}
	var buf bytes.Buffer
	if err := png.Encode(&buf, img); err != nil {
		t.Fatal(err)
	}
	return buf.Bytes()
}

func TestImages(t *testing.T) {
	data := pngBytes(t)
	var hits atomic.Int64
	tmdb := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		hits.Add(1)
		if r.URL.Path != "/t/p/w342/abc123.png" {
			http.NotFound(w, r)
			return
		}
		w.Header().Set("Content-Type", "image/png")
		_, _ = w.Write(data)
	}))
	defer tmdb.Close()
	cache, err := imagecache.New(t.TempDir(), staticBase(tmdb.URL+"/t/p/"), imagecache.Options{HTTPClient: tmdb.Client()})
	if err != nil {
		t.Fatal(err)
	}
	e := newDemoEnv(t, 5, "/gonema", func(d *Deps) { d.Images = cache })

	r := e.do(t, "GET", "/gonema/api/v1/images/poster/w342/abc123.png", "")
	if r.status != 200 || r.body != string(data) || r.header.Get("Content-Type") != "image/png" ||
		r.header.Get("Cache-Control") != "public, max-age=31536000, immutable" || r.header.Get("ETag") == "" {
		t.Fatalf("image = %d %v", r.status, r.header)
	}
	r2 := e.do(t, "GET", "/gonema/api/v1/images/poster/w342/abc123.png", "")
	if r2.status != 200 || hits.Load() != 1 || r2.header.Get("ETag") != r.header.Get("ETag") {
		t.Errorf("second request: status %d, upstream hits %d", r2.status, hits.Load())
	}
	if r := e.do(t, "GET", "/gonema/api/v1/images/poster/w342/abc123.png", "", "If-None-Match", r.header.Get("ETag")); r.status != 304 || r.body != "" {
		t.Errorf("revalidation = %d %q", r.status, r.body)
	}
	if r := e.do(t, "GET", "/gonema/api/v1/images/poster/w342/abc123.png", "", "Accept-Encoding", "gzip"); r.header.Get("Content-Encoding") != "" {
		t.Error("images must not be recompressed")
	}

	problemOK(t, e.do(t, "GET", "/gonema/api/v1/images/poster/w342/missing1.png", ""), 404) // TMDB 404
	problemOK(t, e.do(t, "GET", "/gonema/api/v1/images/poster/w1280/abc123.png", ""), 404)  // size of another kind
	problemOK(t, e.do(t, "GET", "/gonema/api/v1/images/avatar/w342/abc123.png", ""), 422)   // unknown kind
	problemOK(t, e.do(t, "GET", "/gonema/api/v1/images/poster/w342/abc.gif", ""), 422)      // not an image name
	problemOK(t, e.do(t, "GET", "/gonema/api/v1/images/poster/w342/a%2E%2E%2Fb.png", ""), 422)
	problemOK(t, e.do(t, "GET", "/gonema/api/v1/images/poster/w342/https%3A%2F%2Fevil.example%2Fx.png", ""), 422)
	if hits.Load() != 2 {
		t.Errorf("upstream hits = %d, want 2 (invalid names never leave the server)", hits.Load())
	}

	// Demo image store: rendered locally.
	e2 := newDemoEnv(t, 3, "", func(d *Deps) { d.Images = demo.NewImages(cache, d.Store) })
	if r := e2.do(t, "GET", "/api/v1/images/poster/w780/demo-p-000001.jpg", ""); r.status != 200 || r.header.Get("Content-Type") != "image/jpeg" {
		t.Errorf("demo image = %d %v", r.status, r.header)
	}
	// Without an image store the endpoint answers 404.
	e3 := newTestEnv(t, "")
	problemOK(t, e3.do(t, "GET", "/api/v1/images/poster/w342/abc123.png", ""), 404)
}

func TestUIMount(t *testing.T) {
	ui, err := webui.New(webui.Options{BaseURL: "/gonema", FS: fstest.MapFS{
		"index.html":    {Data: []byte("<html><head></head><body>app</body></html>")},
		"assets/a-1.js": {Data: []byte("x")},
	}})
	if err != nil {
		t.Fatal(err)
	}
	e := newDemoEnv(t, 2, "/gonema", func(d *Deps) { d.UI = ui })
	r := e.do(t, "GET", "/gonema/movies/1", "")
	if r.status != 200 || !strings.Contains(r.body, `<base href="/gonema/">`) || r.header.Get("Content-Security-Policy") == "" {
		t.Errorf("SPA route = %d %q", r.status, r.body)
	}
	if r := e.do(t, "GET", "/gonema/assets/a-1.js", ""); r.status != 200 || !strings.Contains(r.header.Get("Cache-Control"), "immutable") {
		t.Errorf("asset = %d %v", r.status, r.header)
	}
	problemOK(t, e.do(t, "GET", "/gonema/api/v1/nothing", ""), 404) // the API keeps its 404s
	if r := e.do(t, "GET", "/gonema/api/v1/system/status", ""); r.status != 200 {
		t.Errorf("api = %d", r.status)
	}
	if r := e.do(t, "GET", "/health", ""); r.status != 200 || !strings.Contains(r.body, "ok") {
		t.Errorf("health = %d", r.status)
	}
}

// TestLibrary10kBudget measures the columns endpoint on 10,000 movies: server
// time under 50 ms and gzipped body under 1.5 MB (docs/architecture.md budget
// for the library screens).
func TestLibrary10kBudget(t *testing.T) {
	if testing.Short() || raceEnabled {
		t.Skip("performance budget: skipped with -short and -race")
	}
	e := newDemoEnv(t, 10_000, "", nil)
	h := e.srv.Handler()
	measure := func(target string) (time.Duration, int, int) {
		best := time.Hour
		var gz, raw int
		for range 5 {
			req := httptest.NewRequest(http.MethodGet, target, http.NoBody)
			req.Header.Set("Accept-Encoding", "gzip")
			rec := httptest.NewRecorder()
			start := time.Now()
			h.ServeHTTP(rec, req)
			best = min(best, time.Since(start))
			if rec.Code != 200 {
				t.Fatalf("%s = %d %s", target, rec.Code, rec.Body.String())
			}
			gz = rec.Body.Len()
			zr, err := gzip.NewReader(bytes.NewReader(rec.Body.Bytes()))
			if err != nil {
				t.Fatal(err)
			}
			b, _ := io.ReadAll(zr)
			raw = len(b)
		}
		return best, gz, raw
	}
	d, gz, raw := measure("/api/v1/movies/ids?sort=title")
	t.Logf("columns, 10,000 movies: %s server time, %d KB gzipped (%d KB raw)", d.Round(time.Millisecond/10), gz>>10, raw>>10)
	if d > 50*time.Millisecond || gz > 1_500_000 {
		t.Errorf("budget exceeded: %s (50 ms), %d bytes (1.5 MB)", d, gz)
	}
	d, gz, raw = measure("/api/v1/movies/ids?sort=quality&state=available,upgradable")
	t.Logf("columns, filtered + quality sort: %s, %d KB gzipped (%d KB raw)", d.Round(time.Millisecond/10), gz>>10, raw>>10)
	d, gz, raw = measure("/api/v1/movies?limit=500&sort=added")
	t.Logf("rows, page of 500 of 10,000: %s, %d KB gzipped (%d KB raw)", d.Round(time.Millisecond/10), gz>>10, raw>>10)
	// After a change the index is reloaded once (the cost of reading every movie).
	if _, err := e.st.Write().SetMovieMonitored(context.Background(), db.SetMovieMonitoredParams{ID: 1, Monitored: false}); err != nil {
		t.Fatal(err)
	}
	req0 := httptest.NewRequest(http.MethodGet, "/api/v1/movies?limit=1", http.NoBody)
	start := time.Now()
	h.ServeHTTP(httptest.NewRecorder(), req0)
	t.Logf("index reload after a change, 10,000 movies: %s", time.Since(start).Round(time.Millisecond))
	var cols MovieColumns
	req := httptest.NewRequest(http.MethodGet, "/api/v1/movies/ids", http.NoBody)
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	if err := json.Unmarshal(rec.Body.Bytes(), &cols); err != nil || cols.Total != 10_000 || len(cols.ID) != 10_000 {
		t.Fatalf("columns: %v, total %d", err, cols.Total)
	}
}

func BenchmarkMovieColumns10k(b *testing.B) {
	e := newDemoEnv(b, 10_000, "", nil)
	h := e.srv.Handler()
	h.ServeHTTP(httptest.NewRecorder(), httptest.NewRequest(http.MethodGet, "/api/v1/movies/ids?sort=title", http.NoBody)) // load the index
	b.ReportAllocs()
	var size int
	for b.Loop() {
		req := httptest.NewRequest(http.MethodGet, "/api/v1/movies/ids?sort=title", http.NoBody)
		req.Header.Set("Accept-Encoding", "gzip")
		rec := httptest.NewRecorder()
		h.ServeHTTP(rec, req)
		if rec.Code != 200 {
			b.Fatal(rec.Code)
		}
		size = rec.Body.Len()
	}
	b.ReportMetric(float64(size), "gzip-bytes")
}

func BenchmarkMovieListPage10k(b *testing.B) {
	e := newDemoEnv(b, 10_000, "", nil)
	h := e.srv.Handler()
	h.ServeHTTP(httptest.NewRecorder(), httptest.NewRequest(http.MethodGet, "/api/v1/movies?sort=title", http.NoBody)) // load the index
	b.ReportAllocs()
	for b.Loop() {
		req := httptest.NewRequest(http.MethodGet, "/api/v1/movies?limit=100&sort=title", http.NoBody)
		req.Header.Set("Accept-Encoding", "gzip")
		rec := httptest.NewRecorder()
		h.ServeHTTP(rec, req)
		if rec.Code != 200 {
			b.Fatal(rec.Code)
		}
	}
}

// TestIndexInvalidation checks that the in-memory index follows every change
// it shows, and ignores the progress the tracker persists.
func TestIndexInvalidation(t *testing.T) {
	e := newDemoEnv(t, 60, "", nil)
	ctx := context.Background()
	version := func() int64 {
		v, err := e.st.Read().GetLibraryVersion(ctx)
		if err != nil {
			t.Fatal(err)
		}
		return v
	}
	cols := e.columns(t, "")
	id := cols.ID[0]

	// A settings change through the API.
	if r := e.do(t, "PATCH", "/api/v1/movies/"+itoa(id), `{"monitored":false,"tags":["fresh-tag"]}`); r.status != 200 {
		t.Fatalf("patch = %d %s", r.status, r.body)
	}
	got := e.columns(t, "state=unmonitored&tag=fresh-tag")
	if !slices.Contains(got.ID, id) || len(got.ID) != 1 {
		t.Errorf("after the change: %v", got.ID)
	}

	// Tracker progress does not invalidate; a status change does.
	var dl int64
	for i, st := range cols.State {
		if st == "downloading" {
			dl = cols.ID[i]
		}
	}
	if dl == 0 {
		t.Fatal("no downloading movie in the demo")
	}
	dls, err := e.st.Read().ListMovieDownloads(ctx, dl)
	if err != nil {
		t.Fatal(err)
	}
	d := dls[0]
	v := version()
	p := db.UpdateDownloadStateParams{ID: d.ID, Status: d.Status, Progress: 0.99, SpeedBps: 1, StatusMessages: "[]"}
	if _, err := e.st.Write().UpdateDownloadState(ctx, p); err != nil {
		t.Fatal(err)
	}
	if version() != v {
		t.Error("a progress update invalidated the index")
	}
	if _, err := e.st.Write().SetMovieLastSearched(ctx, db.SetMovieLastSearchedParams{ID: dl, LastSearchedAt: 1}); err != nil {
		t.Fatal(err)
	}
	if version() != v {
		t.Error("last_searched_at invalidated the index")
	}
	p.Status = "failed"
	if _, err := e.st.Write().UpdateDownloadState(ctx, p); err != nil {
		t.Fatal(err)
	}
	if version() == v {
		t.Error("a status change did not invalidate the index")
	}
	after := e.columns(t, "state=failed")
	if !slices.Contains(after.ID, dl) {
		t.Errorf("movie %d not failed after its download failed", dl)
	}
	// Deleting a movie removes it.
	if r := e.do(t, "DELETE", "/api/v1/movies/"+itoa(id), ""); r.status != 204 {
		t.Fatalf("delete = %d", r.status)
	}
	if got := e.columns(t, ""); slices.Contains(got.ID, id) || got.Total != 59 {
		t.Errorf("after delete: total %d", got.Total)
	}
}
