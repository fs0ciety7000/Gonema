package library

import (
	"context"
	"encoding/json"
	"errors"
	"slices"
	"strings"
	"testing"

	"github.com/fs0ciety7000/gonema/internal/events"
	"github.com/fs0ciety7000/gonema/internal/metadata"
	"github.com/fs0ciety7000/gonema/internal/parser"
	"github.com/fs0ciety7000/gonema/internal/quality"
	"github.com/fs0ciety7000/gonema/internal/store"
	"github.com/fs0ciety7000/gonema/internal/store/db"
)

func titles(p MoviePage) []string {
	out := make([]string, 0, len(p.Movies))
	for _, m := range p.Movies {
		out = append(out, m.Title)
	}
	return out
}

func TestTagsCRUD(t *testing.T) {
	e := newEnv(t)
	ctx := context.Background()
	e.prov.set(released(1, "Alien", 1979))
	m := e.add(t, 1)

	tag, err := e.svc.CreateTag(ctx, "  4k  ")
	if err != nil || tag.Label != "4k" || tag.Movies != 0 {
		t.Fatalf("create = %+v, %v", tag, err)
	}
	if _, err := e.svc.CreateTag(ctx, "4k"); !errors.Is(err, store.ErrConflict) || !errors.Is(err, ErrInvalid) {
		t.Errorf("duplicate = %v", err)
	}
	if _, err := e.svc.CreateTag(ctx, " "); !errors.Is(err, ErrInvalid) {
		t.Errorf("empty = %v", err)
	}
	if _, _, err := e.svc.BulkUpdate(ctx, []int64{m.ID}, BulkSet{AddTags: []string{"4k"}}); err != nil {
		t.Fatal(err)
	}
	other, _ := e.svc.CreateTag(ctx, "kids")
	if _, err := e.svc.RenameTag(ctx, other.ID, "4k"); !errors.Is(err, store.ErrConflict) {
		t.Errorf("rename onto a used label = %v", err)
	}
	renamed, err := e.svc.RenameTag(ctx, tag.ID, "UHD")
	if err != nil || renamed.Label != "UHD" || renamed.Movies != 1 {
		t.Fatalf("rename = %+v, %v", renamed, err)
	}
	list, err := e.svc.ListTags(ctx)
	if err != nil || len(list) != 2 || list[0].Label != "UHD" || list[0].Movies != 1 || list[1].Movies != 0 {
		t.Fatalf("list = %+v, %v", list, err)
	}
	sub := e.bus.Subscribe(8, func(ev *events.Event) bool { return ev.Type == events.MovieUpdated })
	defer sub.Close()
	del, movies, err := e.svc.DeleteTag(ctx, tag.ID)
	if err != nil || del.Label != "UHD" || !slices.Equal(movies, []int64{m.ID}) {
		t.Fatalf("delete = %+v %v, %v", del, movies, err)
	}
	if ev := <-sub.C(); ev.MovieID != m.ID {
		t.Errorf("event = %+v", ev)
	}
	if _, _, err := e.svc.DeleteTag(ctx, tag.ID); !errors.Is(err, store.ErrNotFound) {
		t.Errorf("second delete = %v", err)
	}
	d, _ := e.svc.GetMovie(ctx, m.ID)
	if len(d.Tags) != 0 {
		t.Errorf("tags after delete = %v", d.Tags)
	}
}

func TestBulkUpdateAndRestore(t *testing.T) {
	e := newEnv(t)
	ctx := context.Background()
	for i, title := range []string{"Alien", "Heat", "Zodiac"} {
		e.prov.set(released(i+1, title, 1980+i))
	}
	a, h, z := e.add(t, 1), e.add(t, 2), e.add(t, 3)
	tags := []string{"keep"}
	if _, err := e.svc.UpdateMovie(ctx, h.ID, UpdateOptions{Tags: &tags}); err != nil {
		t.Fatal(err)
	}
	sub := e.bus.Subscribe(16, func(ev *events.Event) bool { return ev.Type == events.MovieUpdated })
	defer sub.Close()

	off, avail := false, AvailInCinemas
	prev, changed, err := e.svc.BulkUpdate(ctx, []int64{a.ID, h.ID, a.ID}, BulkSet{
		Monitored: &off, MinimumAvailability: &avail, AddTags: []string{"kids", "new"}, RemoveTags: []string{"keep", "new"},
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(prev) != 2 || prev[0].ID != a.ID || !prev[0].Monitored || prev[0].MinimumAvailability != AvailReleased ||
		len(prev[0].Tags) != 0 || !slices.Equal(prev[1].Tags, []string{"keep"}) {
		t.Fatalf("previous = %+v", prev)
	}
	if !slices.Equal(changed, []int64{a.ID, h.ID}) {
		t.Errorf("changed = %v", changed)
	}
	for _, id := range []int64{a.ID, h.ID} {
		d, _ := e.svc.GetMovie(ctx, id)
		if d.Movie.Monitored || d.Movie.MinimumAvailability != AvailInCinemas || !slices.Equal(d.Tags, []string{"kids", "new"}) {
			t.Errorf("movie %d after bulk = monitored %v %s %v", id, d.Movie.Monitored, d.Movie.MinimumAvailability, d.Tags)
		}
	}
	if n := len(sub.C()); n != 2 {
		t.Errorf("%d movie.updated events, want 2", n)
	}
	if d, _ := e.svc.GetMovie(ctx, z.ID); !d.Movie.Monitored {
		t.Error("an unselected movie changed")
	}

	// Undo restores exactly what was there.
	restored, err := e.svc.RestoreSettings(ctx, prev)
	if err != nil || !slices.Equal(restored, []int64{a.ID, h.ID}) {
		t.Fatalf("restore = %v, %v", restored, err)
	}
	for _, p := range prev {
		got, err := e.svc.MovieSettingsOf(ctx, p.ID)
		if err != nil {
			t.Fatal(err)
		}
		if got.Monitored != p.Monitored || got.MinimumAvailability != p.MinimumAvailability || !sameLabels(got.Tags, p.Tags) {
			t.Errorf("after undo = %+v, want %+v", got, p)
		}
	}

	// All or nothing: an unknown movie stops the whole edit.
	on := true
	if _, _, err := e.svc.BulkUpdate(ctx, []int64{z.ID, 9999}, BulkSet{Monitored: &off}); !errors.Is(err, store.ErrNotFound) {
		t.Fatalf("unknown movie = %v", err)
	}
	if d, _ := e.svc.GetMovie(ctx, z.ID); d.Movie.Monitored != on {
		t.Error("a failed bulk edit changed a movie")
	}
	bad := int64(999)
	if _, _, err := e.svc.BulkUpdate(ctx, []int64{z.ID}, BulkSet{QualityProfileID: &bad}); !errors.Is(err, ErrInvalid) {
		t.Errorf("unknown profile = %v", err)
	}
	if _, _, err := e.svc.BulkUpdate(ctx, nil, BulkSet{}); !errors.Is(err, ErrInvalid) {
		t.Errorf("no ids = %v", err)
	}
}

func TestListMoviesTagsAndWanted(t *testing.T) {
	e := newEnv(t)
	ctx := context.Background()
	for i, title := range []string{"Alien", "Heat", "Zodiac"} {
		e.prov.set(released(i+1, title, 1980+i))
	}
	a, h, z := e.add(t, 1), e.add(t, 2), e.add(t, 3)
	set := func(id int64, tags ...string) {
		if _, err := e.svc.UpdateMovie(ctx, id, UpdateOptions{Tags: &tags}); err != nil {
			t.Fatal(err)
		}
	}
	set(a.ID, "4k", "kids")
	set(h.ID, "4k")
	set(z.ID, "kids")
	for _, tc := range []struct {
		name string
		f    ListFilter
		want []string
	}{
		{"any", ListFilter{Tags: []string{"4k", "kids"}}, []string{"Alien", "Heat", "Zodiac"}},
		{"all", ListFilter{Tags: []string{"4k", "kids"}, TagMatch: TagMatchAll}, []string{"Alien"}},
		{"legacy tag", ListFilter{Tag: "kids"}, []string{"Alien", "Zodiac"}},
		{"unknown in all", ListFilter{Tags: []string{"4k", "nope"}, TagMatch: TagMatchAll}, nil},
		{"unknown in any", ListFilter{Tags: []string{"4k", "nope"}}, []string{"Alien", "Heat"}},
	} {
		page, err := e.svc.ListMovies(ctx, ListQuery{Filter: tc.f})
		if err != nil {
			t.Fatalf("%s: %v", tc.name, err)
		}
		if got := titles(page); !slices.Equal(got, tc.want) && len(got)+len(tc.want) > 0 {
			t.Errorf("%s = %v, want %v", tc.name, got, tc.want)
		}
	}
	page, _ := e.svc.ListMovies(ctx, ListQuery{})
	for _, m := range page.Movies {
		if m.Title == "Alien" && len(m.TagIDs) != 2 {
			t.Errorf("Alien tag ids = %v", m.TagIDs)
		}
	}
	if _, err := e.svc.ListMovies(ctx, ListQuery{Filter: ListFilter{TagMatch: "some"}}); !errors.Is(err, ErrInvalid) {
		t.Errorf("bad tagMatch = %v", err)
	}

	// Wanted: monitored, missing, released. Heat is unmonitored; Zodiac has a
	// file below the cutoff (webdl-720p < webdl-1080p).
	off := false
	if _, err := e.svc.UpdateMovie(ctx, h.ID, UpdateOptions{Monitored: &off}); err != nil {
		t.Fatal(err)
	}
	if _, err := e.st.Write().UpsertMovieFile(ctx, db.UpsertMovieFileParams{
		MovieID: z.ID, RelativePath: "z.mkv", Size: 1, Quality: "webdl-720p", Revision: 1, Languages: "[]",
		Parsed: "{}", MediaInfo: "{}", ImportedAt: 1,
	}); err != nil {
		t.Fatal(err)
	}
	missing, err := e.svc.ListMovies(ctx, ListQuery{Filter: ListFilter{Wanted: WantedMissing}})
	if err != nil || !slices.Equal(titles(missing), []string{"Alien"}) {
		t.Errorf("missing = %v, %v", titles(missing), err)
	}
	cutoff, err := e.svc.ListMovies(ctx, ListQuery{Filter: ListFilter{Wanted: WantedCutoffUnmet}})
	if err != nil || !slices.Equal(titles(cutoff), []string{"Zodiac"}) {
		t.Errorf("cutoff unmet = %v, %v", titles(cutoff), err)
	}
	st, _ := e.svc.Stats(ctx)
	if st.Wanted != int64(missing.Total) || st.CutoffUnmet != int64(cutoff.Total) {
		t.Errorf("stats %+v disagree with the lists", st)
	}
	if _, err := e.svc.ListMovies(ctx, ListQuery{Filter: ListFilter{Wanted: "all"}}); !errors.Is(err, ErrInvalid) {
		t.Errorf("bad wanted = %v", err)
	}
}

func TestDiscoverFlags(t *testing.T) {
	e := newEnv(t)
	ctx := context.Background()
	e.prov.set(released(1, "Alien", 1979))
	alien := e.add(t, 1)
	if err := e.st.Write().AddImportListExclusion(ctx, db.AddImportListExclusionParams{TmdbID: 3, Title: "Excluded", CreatedAt: 1}); err != nil {
		t.Fatal(err)
	}
	list := []metadata.MovieSummary{
		{TMDBID: 1, Title: "Alien", PosterPath: "/a.jpg"}, {TMDBID: 2, Title: "New"},
		{TMDBID: 3, Title: "Excluded"}, {TMDBID: 4, Title: "Adult", Adult: true},
	}
	e.prov.lists["popular"], e.prov.lists["trending-day"] = list, list[:1]

	p, err := e.svc.Discover(ctx, ListPopular, "", 1)
	if err != nil {
		t.Fatal(err)
	}
	if len(p.Results) != 3 {
		t.Fatalf("results = %+v (adult movies must be dropped)", p.Results)
	}
	if r := p.Results[0]; r.MovieID != alien.ID || !r.Monitored || r.State != StateMissing {
		t.Errorf("in library = %+v", r.Flags)
	}
	if r := p.Results[1]; r.MovieID != 0 || r.State != "" || r.Excluded {
		t.Errorf("not in library = %+v", r.Flags)
	}
	if r := p.Results[2]; !r.Excluded || r.MovieID != 0 {
		t.Errorf("excluded = %+v", r.Flags)
	}
	if p, err := e.svc.Discover(ctx, ListTrending, "day", 2); err != nil || len(p.Results) != 1 || p.Page != 2 {
		t.Errorf("trending day = %+v, %v", p, err)
	}
	for _, bad := range []struct {
		list, window string
		page         int
	}{{"top", "", 1}, {ListTrending, "month", 1}, {ListPopular, "", 501}} {
		if _, err := e.svc.Discover(ctx, bad.list, bad.window, bad.page); !errors.Is(err, ErrInvalid) {
			t.Errorf("Discover(%v) = %v", bad, err)
		}
	}

	// Lookup gets the same flags.
	res, err := e.svc.Lookup(ctx, "Alien", 0)
	if err != nil || len(res) != 1 || res[0].MovieID != alien.ID || res[0].State != StateMissing || !res[0].Monitored {
		t.Errorf("lookup = %+v, %v", res, err)
	}
}

func TestFilmographyMergesRoles(t *testing.T) {
	e := newEnv(t)
	ctx := context.Background()
	e.prov.set(released(1, "Alien", 1979))
	alien := e.add(t, 1)
	d := func(y int) metadata.Date { return metadata.Date{Year: y, Month: 1, Day: 1} }
	e.prov.people[42] = &metadata.Filmography{
		PersonID: 42,
		Cast: []metadata.FilmCredit{
			{Movie: metadata.MovieSummary{TMDBID: 1, Title: "Alien", ReleaseDate: d(1979)}, Character: "Cameo"},
			{Movie: metadata.MovieSummary{TMDBID: 5, Title: "Soon"}, Character: "Lead"},
		},
		Crew: []metadata.FilmCredit{
			{Movie: metadata.MovieSummary{TMDBID: 1, Title: "Alien", ReleaseDate: d(1979)}, Job: "Director", Department: "Directing"},
			{Movie: metadata.MovieSummary{TMDBID: 1, Title: "Alien", ReleaseDate: d(1979)}, Job: "Producer", Department: "Production"},
			{Movie: metadata.MovieSummary{TMDBID: 7, Title: "Later", ReleaseDate: d(1990)}, Job: "Writer", Department: "Writing"},
			{Movie: metadata.MovieSummary{TMDBID: 8, Title: "X", Adult: true}, Job: "Writer"},
		},
	}
	movies, err := e.svc.Filmography(ctx, 42)
	if err != nil {
		t.Fatal(err)
	}
	var got []string
	for _, m := range movies {
		got = append(got, m.Title)
	}
	if !slices.Equal(got, []string{"Soon", "Later", "Alien"}) {
		t.Fatalf("order = %v (upcoming first, then newest first, deduplicated)", got)
	}
	a := movies[2]
	if a.MovieID != alien.ID || !slices.Equal(a.Characters, []string{"Cameo"}) || !slices.Equal(a.Jobs, []string{"Director", "Producer"}) {
		t.Errorf("Alien = %+v", a)
	}
	if _, err := e.svc.Filmography(ctx, 43); !errors.Is(err, store.ErrNotFound) {
		t.Errorf("unknown person = %v", err)
	}
}

func TestAddCollection(t *testing.T) {
	e := newEnv(t)
	ctx := context.Background()
	for i, title := range []string{"Alien", "Aliens", "Alien 3", "Resurrection"} {
		m := released(i+1, title, 1979+i)
		m.Collection = &metadata.CollectionRef{TMDBID: 8091, Name: "Alien Collection"}
		e.prov.set(m)
	}
	alien := e.add(t, 1)
	if err := e.st.Write().AddImportListExclusion(ctx, db.AddImportListExclusionParams{TmdbID: 4, Title: "Resurrection", CreatedAt: 1}); err != nil {
		t.Fatal(err)
	}
	e.prov.collections[8091] = &metadata.Collection{TMDBID: 8091, Name: "Alien Collection", Parts: []metadata.MovieSummary{
		{TMDBID: 1, Title: "Alien"}, {TMDBID: 2, Title: "Aliens"}, {TMDBID: 3, Title: "Alien 3"}, {TMDBID: 4, Title: "Resurrection"},
	}}
	c, err := e.svc.CollectionByTMDB(ctx, 8091)
	if err != nil || c.CollectionID == 0 || len(c.Parts) != 4 || c.Parts[0].MovieID != alien.ID || !c.Parts[3].Excluded {
		t.Fatalf("collection = %+v, %v", c, err)
	}
	res, err := e.svc.AddCollection(ctx, 8091, AddCollectionOptions{
		AddOptions:        AddOptions{RootFolderID: e.lib.RootFolderID, QualityProfileID: e.lib.ProfileID, Monitored: false},
		MonitorCollection: true,
	})
	if err != nil {
		t.Fatal(err)
	}
	var added []string
	for _, m := range res.Added {
		added = append(added, m.Title)
		if !m.Monitored {
			t.Errorf("%s not monitored although the collection is", m.Title)
		}
	}
	if !slices.Equal(added, []string{"Aliens", "Alien 3"}) || !slices.Equal(res.Existing, []int64{alien.ID}) ||
		len(res.Skipped) != 1 || res.Skipped[0].TmdbID != 4 || !res.Collection.Monitored {
		t.Fatalf("result = added %v existing %v skipped %+v monitored %v", added, res.Existing, res.Skipped, res.Collection.Monitored)
	}
	if _, err := e.svc.AddCollection(ctx, 1, AddCollectionOptions{AddOptions: AddOptions{RootFolderID: e.lib.RootFolderID, QualityProfileID: e.lib.ProfileID}}); !errors.Is(err, store.ErrNotFound) {
		t.Errorf("unknown collection = %v", err)
	}
	if _, err := e.svc.AddCollection(ctx, 8091, AddCollectionOptions{}); !errors.Is(err, ErrInvalid) {
		t.Errorf("no root folder = %v", err)
	}
}

func TestMovieDetailScoreAndExtra(t *testing.T) {
	e := newEnv(t)
	ctx := context.Background()
	m := released(1, "Heat", 1995)
	m.Releases.Digital = metadata.DatedRelease{Date: metadata.Date{Year: 1996, Month: 1, Day: 1}, Region: "FR"}
	m.Releases.CertificationRegion = "US"
	m.Extra.Companies = []metadata.Company{{TMDBID: 508, Name: "Regency Enterprises", Country: "US"}}
	e.prov.set(m)
	movie := e.add(t, 1)

	rule, err := e.st.Write().CreateScoringRule(ctx, db.CreateScoringRuleParams{Name: "Trusted group"})
	if err != nil {
		t.Fatal(err)
	}
	if err := e.st.Write().InsertScoringCondition(ctx, db.InsertScoringConditionParams{RuleID: rule.ID, Kind: "release_group", Value: `{"value":"GRP"}`}); err != nil {
		t.Fatal(err)
	}
	if err := e.st.Write().SetProfileScore(ctx, db.SetProfileScoreParams{ProfileID: e.lib.ProfileID, RuleID: rule.ID, Score: 20}); err != nil {
		t.Fatal(err)
	}
	scene := "Heat.1995.1080p.BluRay.x264-GRP"
	parsed := parser.Parse(scene)
	raw, _ := json.Marshal(&parsed)
	if _, err := e.st.Write().UpsertMovieFile(ctx, db.UpsertMovieFileParams{
		MovieID: movie.ID, RelativePath: "Heat.mkv", Size: 8 << 30, Quality: "bluray-1080p", Revision: 1, Languages: "[]",
		SceneName: scene, Parsed: string(raw), MediaInfo: "{}", CustomScore: 5, ImportedAt: 1,
	}); err != nil {
		t.Fatal(err)
	}
	d, err := e.svc.GetMovie(ctx, movie.ID)
	if err != nil {
		t.Fatal(err)
	}
	b := d.FileScore
	if b == nil || !slices.Contains(b.Rules, quality.RuleScore{Rule: "Trusted group", Score: 20}) {
		t.Fatalf("breakdown = %+v", b)
	}
	sum := 0
	for _, r := range b.Rules {
		sum += r.Score
	}
	if sum != b.Total {
		t.Errorf("total %d != sum of rules %d", b.Total, sum)
	}
	var x extraJSON
	if err := json.Unmarshal([]byte(d.Movie.Extra), &x); err != nil {
		t.Fatal(err)
	}
	if x.ReleaseRegions == nil || x.ReleaseRegions.Cinema != "US" || x.ReleaseRegions.Digital != "FR" || x.ReleaseRegions.Certification != "US" {
		t.Errorf("release regions = %+v", x.ReleaseRegions)
	}
	if len(x.Companies) != 1 || x.Companies[0].Name != "Regency Enterprises" {
		t.Errorf("companies = %+v", x.Companies)
	}
	if !strings.Contains(d.Movie.Extra, `"releaseRegions"`) {
		t.Errorf("extra = %s", d.Movie.Extra)
	}
}
