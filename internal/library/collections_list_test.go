package library

import (
	"context"
	"slices"
	"testing"
	"time"

	"github.com/fs0ciety7000/gonema/internal/metadata"
	"github.com/fs0ciety7000/gonema/internal/store/db"
)

func TestBuildView(t *testing.T) {
	i64 := func(v int64) *int64 { return &v }
	movies := []*MovieSummary{
		{ListLibraryIndexRow: db.ListLibraryIndexRow{ID: 1, TmdbID: 10, Title: "One", Year: 2001, Status: "released", State: StateAvailable, FileSize: i64(5), Monitored: true, AddedAt: 100}},
		{ListLibraryIndexRow: db.ListLibraryIndexRow{ID: 2, TmdbID: 20, Title: "Two", Year: 2004, Status: "released", State: StateMissing, AddedAt: 300}},
		{ListLibraryIndexRow: db.ListLibraryIndexRow{ID: 3, TmdbID: 30, Title: "Three", Year: 2027, Status: "announced", State: StateMissing}},
		{ListLibraryIndexRow: db.ListLibraryIndexRow{ID: 4, TmdbID: 90, Title: "Extra", Year: 1999, Status: "released", State: StateDownloading, AddedAt: 200}},
	}
	parts := []PartSnapshot{
		{TmdbID: 10, Title: "One (TMDB)", ReleaseDate: "2001-03-01", PosterPath: "/one.jpg"},
		{TmdbID: 20, ReleaseDate: "2004-05-01"},
		{TmdbID: 30, ReleaseDate: "2027-01-01"},
		{TmdbID: 40, Title: "Four", Year: 2010, ReleaseDate: "2010-01-01"},
		{TmdbID: 50, Title: "Five", ReleaseDate: "2027-06-01"},
		{TmdbID: 60, Title: "Undated"},
		{TmdbID: 40, Title: "Four again"}, // duplicate: ignored
	}
	v := buildView(db.Collection{ID: 7, Name: "Saga"}, parts, true, movies, "2026-11-06")
	var got []string
	for _, f := range v.Films {
		got = append(got, f.Title+":"+f.State)
	}
	want := []string{"Extra:downloading", "One:on_disk", "Two:wanted", "Four:missing", "Three:upcoming", "Five:not_released", "Undated:not_released"}
	if !slices.Equal(got, want) {
		t.Errorf("films = %v\nwant %v", got, want)
	}
	c := v.Counts
	if c.Total != 7 || c.InLibrary != 4 || c.OnDisk != 1 || c.Downloading != 1 || c.Wanted != 1 || c.NotInLibrary != 3 ||
		c.Missing != 1 || c.Upcoming != 3 || c.SizeOnDisk != 5 || c.YearFrom != 1999 || c.YearTo != 2027 || !c.HasMissing() {
		t.Errorf("counts = %+v", c)
	}
	if v.LastAdded != 300 || v.Films[1].PosterPath != "/one.jpg" {
		t.Errorf("view = %+v", v)
	}
	// Without a snapshot only the library movies are listed.
	if v := buildView(db.Collection{}, nil, false, movies[:1], "2026-11-06"); len(v.Films) != 1 || v.PartsKnown || v.Counts.HasMissing() {
		t.Errorf("no snapshot = %+v", v)
	}
}

func TestReleasedParts(t *testing.T) {
	r, u := releasedParts([]PartSnapshot{{TmdbID: 1, ReleaseDate: "2026-11-06"}, {TmdbID: 2, ReleaseDate: "2026-11-07"}, {TmdbID: 3}}, "2026-11-06")
	if len(r) != 1 || r[0].TmdbID != 1 || len(u) != 2 {
		t.Errorf("released %v, upcoming %v", r, u)
	}
}

// TestListAndRefreshCollections: the list counts from the snapshot; a
// monitored collection adds an upcoming part on its release day.
func TestListAndRefreshCollections(t *testing.T) {
	e := newEnv(t)
	ctx := context.Background()
	day := func(s string) metadata.Date {
		d, err := metadata.ParseDate(s)
		if err != nil {
			t.Fatal(err)
		}
		return d
	}
	ref := &metadata.CollectionRef{TMDBID: 10, Name: "Alien Collection"}
	other := &metadata.CollectionRef{TMDBID: 11, Name: "Blade Runner Collection"}
	for _, m := range []*metadata.Movie{released(1, "Alien", 1979), released(2, "Aliens", 1986), released(5, "Alien: Next", 2026)} {
		m.Collection = ref
		e.prov.set(m)
	}
	br := released(8, "Blade Runner", 1982)
	br.Collection = other
	e.prov.set(br)
	tomorrow := e.now.AddDate(0, 0, 1).Format(time.DateOnly)
	e.prov.collections[10] = &metadata.Collection{TMDBID: 10, Name: "Alien Collection", Parts: []metadata.MovieSummary{
		{TMDBID: 1, Title: "Alien", ReleaseDate: day("1979-05-25")}, {TMDBID: 2, Title: "Aliens", ReleaseDate: day("1986-07-18")},
		{TMDBID: 5, Title: "Alien: Next", ReleaseDate: day(tomorrow)},
	}}
	e.prov.collections[11] = &metadata.Collection{TMDBID: 11, Name: "Blade Runner Collection", Parts: []metadata.MovieSummary{
		{TMDBID: 8, Title: "Blade Runner", ReleaseDate: day("1982-06-25")},
	}}
	first := e.add(t, 1)
	e.add(t, 8)

	// Before any TMDB read of the collection: library movies only.
	page, err := e.svc.ListCollections(ctx, CollectionQuery{})
	if err != nil || page.Total != 2 || page.Collections[0].PartsKnown || page.Totals.Films != 2 {
		t.Fatalf("list before snapshot = %+v, %v", page, err)
	}
	res, err := e.svc.MonitorCollection(ctx, *first.CollectionID, MonitorOptions{
		Monitored: true, QualityProfileID: e.lib.ProfileID, RootFolderID: e.lib.RootFolderID, SearchOnAdd: true,
	})
	if err != nil || len(res.Added) != 1 || len(res.Upcoming) != 1 || !res.Collection.SearchOnAdd {
		t.Fatalf("monitor = %+v, %v", res, err)
	}
	page, err = e.svc.ListCollections(ctx, CollectionQuery{Sort: CollectionSortName})
	if err != nil || page.Total != 2 {
		t.Fatalf("list = %+v, %v", page, err)
	}
	alien := page.Collections[0]
	if alien.Collection.Name != "Alien Collection" || !alien.PartsKnown || alien.Counts.Total != 3 || alien.Counts.InLibrary != 2 ||
		alien.Counts.Upcoming != 1 || alien.Counts.Missing != 0 || alien.Counts.HasMissing() {
		t.Errorf("alien = %+v", alien.Counts)
	}
	tr := page.Totals
	if tr.Collections != 2 || tr.Monitored != 1 || tr.Films != 4 || tr.NotInLibrary != 1 || tr.Upcoming != 1 || tr.WithMissing != 0 {
		t.Errorf("totals = %+v", tr)
	}
	mon := true
	for _, tc := range []struct {
		q    CollectionQuery
		want []string
	}{
		{CollectionQuery{Monitored: &mon}, []string{"Alien Collection"}},
		{CollectionQuery{Query: "blade"}, []string{"Blade Runner Collection"}},
		{CollectionQuery{Missing: true}, nil},
		{CollectionQuery{Sort: CollectionSortYear}, []string{"Alien Collection", "Blade Runner Collection"}},
		{CollectionQuery{PageSize: 1, Page: 2}, []string{"Blade Runner Collection"}},
	} {
		p, err := e.svc.ListCollections(ctx, tc.q)
		var names []string
		for _, c := range p.Collections {
			names = append(names, c.Collection.Name)
		}
		if err != nil || !slices.Equal(names, tc.want) {
			t.Errorf("%+v: %v, %v", tc.q, names, err)
		}
	}
	if _, err := e.svc.ListCollections(ctx, CollectionQuery{Sort: "nope"}); err == nil {
		t.Error("bad sort accepted")
	}

	// Release day: the job adds the part, with the collection as the reason.
	if err := e.svc.RefreshCollections(ctx); err != nil {
		t.Fatal(err)
	}
	if _, err := e.st.Read().GetMovieByTmdbID(ctx, 5); err == nil {
		t.Fatal("upcoming part added before its release day")
	}
	*e.now = e.now.AddDate(0, 0, 1)
	if err := e.svc.RefreshCollections(ctx); err != nil {
		t.Fatal(err)
	}
	m, err := e.st.Read().GetMovieByTmdbID(ctx, 5)
	if err != nil || !m.Monitored || m.QualityProfileID != e.lib.ProfileID {
		t.Fatalf("part added on release = %+v, %v", m, err)
	}
}
