package library

import (
	"context"
	"encoding/json"
	"errors"
	"math"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/fs0ciety7000/gonema/internal/events"
	"github.com/fs0ciety7000/gonema/internal/metadata"
	"github.com/fs0ciety7000/gonema/internal/parser"
	"github.com/fs0ciety7000/gonema/internal/quality"
	"github.com/fs0ciety7000/gonema/internal/store"
	"github.com/fs0ciety7000/gonema/internal/store/db"
)

func TestAddMovie(t *testing.T) {
	e := newEnv(t)
	sub := e.bus.Subscribe(16, func(ev *events.Event) bool { return ev.Type == events.MovieAdded })
	m := released(550, "Fight Club", 1999)
	m.Collection = &metadata.CollectionRef{TMDBID: 10, Name: "Fight Club Collection"}
	m.Releases.Certification = "R"
	e.prov.set(m)

	ctx := context.Background()
	got, created, err := e.svc.AddMovie(ctx, 550, AddOptions{
		RootFolderID: e.lib.RootFolderID, QualityProfileID: e.lib.ProfileID, Monitored: true,
		Tags: []string{" 4k ", "kids", "4k"}, SearchOnAdd: true,
	})
	if err != nil || !created {
		t.Fatalf("AddMovie = %v, %v", created, err)
	}
	if got.Path != "Fight Club (1999)" || got.Status != "released" || got.SortTitle != "fight club" ||
		got.CleanTitle != "fightclub" || got.MinimumAvailability != "released" || got.Certification != "R" ||
		got.CollectionID == nil || got.ImdbID == nil || *got.ImdbID != "tt0000550" || got.Genres != `["Drama"]` {
		t.Errorf("movie = %+v", got)
	}
	var extra map[string]any
	if err := json.Unmarshal([]byte(got.Extra), &extra); err != nil || extra["statusReason"] == "" {
		t.Errorf("extra = %s (%v)", got.Extra, err)
	}
	d, err := e.svc.GetMovie(ctx, got.ID)
	if err != nil {
		t.Fatal(err)
	}
	if !slices.Equal(d.Tags, []string{"4k", "kids"}) || d.State != "missing" {
		t.Errorf("details = tags %v, state %q", d.Tags, d.State)
	}
	if k := e.jobs.enqueued(); !slices.Equal(k, []string{JobMovieImages, JobMovieSearch}) {
		t.Errorf("enqueued = %v", k)
	}
	select {
	case ev := <-sub.C():
		if ev.MovieID != got.ID {
			t.Errorf("event = %+v", ev)
		}
	default:
		t.Error("no movie.added event")
	}

	// Idempotent: same movie, no provider call, no change.
	again, created, err := e.svc.AddMovie(ctx, 550, AddOptions{RootFolderID: 999, QualityProfileID: 999})
	if err != nil || created || again.ID != got.ID {
		t.Fatalf("second AddMovie = %d, %v, %v; want existing %d", again.ID, created, err, got.ID)
	}
	if n := e.prov.count("Movie"); n != 1 {
		t.Errorf("provider called %d times, want 1", n)
	}
}

func TestAddMovieErrors(t *testing.T) {
	e := newEnv(t)
	e.prov.set(released(1, "Alien", 1979))
	ctx := context.Background()
	ok := AddOptions{RootFolderID: e.lib.RootFolderID, QualityProfileID: e.lib.ProfileID}
	tests := []struct {
		name string
		id   int
		opts AddOptions
		is   error
	}{
		{"bad id", 0, ok, ErrInvalid},
		{"unknown root folder", 1, AddOptions{RootFolderID: 99, QualityProfileID: e.lib.ProfileID}, ErrInvalid},
		{"unknown profile", 1, AddOptions{RootFolderID: e.lib.RootFolderID, QualityProfileID: 99}, ErrInvalid},
		{"bad availability", 1, AddOptions{RootFolderID: e.lib.RootFolderID, QualityProfileID: e.lib.ProfileID, MinimumAvailability: "soon"}, ErrInvalid},
		{"not on TMDB", 2, ok, store.ErrNotFound},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if _, _, err := e.svc.AddMovie(ctx, tt.id, tt.opts); !errors.Is(err, tt.is) {
				t.Errorf("err = %v, want %v", err, tt.is)
			}
		})
	}
}

func TestAddMovieWithoutProvider(t *testing.T) {
	e := newEnv(t)
	e.svc.provider = nil
	_, _, err := e.svc.AddMovie(context.Background(), 1, AddOptions{RootFolderID: e.lib.RootFolderID, QualityProfileID: e.lib.ProfileID})
	if !errors.Is(err, ErrNoProvider) {
		t.Errorf("err = %v, want ErrNoProvider", err)
	}
}

func TestAddMoviePathCollision(t *testing.T) {
	e := newEnv(t)
	e.prov.set(released(1, "Dune: Part One?", 2021))
	e.prov.set(released(2, "Dune: Part One?", 2021)) // homonym, same year
	a, b := e.add(t, 1), e.add(t, 2)
	if a.Path != "Dune - Part One (2021)" {
		t.Errorf("first path = %q", a.Path)
	}
	if b.Path != "Dune - Part One (2021) {tmdb-2}" {
		t.Errorf("second path = %q", b.Path)
	}
}

func TestAddMovieFolderTemplateSetting(t *testing.T) {
	e := newEnv(t)
	if err := store.SetSetting(context.Background(), e.st, SettingFolderTemplate, "{Title} [{Year}] [imdbid-{ImdbId}]"); err != nil {
		t.Fatal(err)
	}
	m := released(3, "Amélie", 2001)
	m.IMDbID = ""
	e.prov.set(m)
	if got := e.add(t, 3).Path; got != "Amélie [2001]" {
		t.Errorf("path = %q", got)
	}
}

func TestAlternateTitlesFeedMatcher(t *testing.T) {
	e := newEnv(t)
	m := released(194, "Amélie", 2001)
	m.OriginalTitle = "Le Fabuleux Destin d'Amélie Poulain"
	m.AlternateTitles = []metadata.AlternateTitle{
		{Title: "Die fabelhafte Welt der Amélie", Country: "DE"},
		{Title: "amelie", Country: "US"}, // same key as the title: skipped
	}
	e.prov.set(m)
	movie := e.add(t, 194)

	d, err := e.svc.GetMovie(context.Background(), movie.ID)
	if err != nil {
		t.Fatal(err)
	}
	if len(d.AlternateTitles) != 1 || d.AlternateTitles[0].Country != "DE" {
		t.Errorf("alternate titles = %+v", d.AlternateTitles)
	}
	for _, name := range []string{
		"Die.fabelhafte.Welt.der.Amelie.2001.German.1080p.BluRay.x264-GRP",
		"Le.Fabuleux.Destin.d.Amelie.Poulain.2001.FRENCH.1080p.BluRay.x264-GRP",
		"Amelie.2001.1080p.BluRay.x264-GRP",
	} {
		c := e.svc.MatchRelease(parser.Parse(name))
		if len(c) == 0 || c[0].MovieID != movie.ID || !c[0].Confident() {
			t.Errorf("MatchRelease(%s) = %+v", name, c)
		}
	}
	if c := e.svc.MatchIMDb("tt0000194"); len(c) != 1 || c[0].MovieID != movie.ID {
		t.Errorf("MatchIMDb = %+v", c)
	}

	// A rebuilt matcher (startup) gives the same answer.
	if err := e.svc.RebuildMatcher(context.Background()); err != nil {
		t.Fatal(err)
	}
	if e.svc.MatcherSize() != 1 {
		t.Errorf("matcher size = %d", e.svc.MatcherSize())
	}
	if c := e.svc.MatchRelease(parser.Parse("Die.fabelhafte.Welt.der.Amelie.2001.1080p.WEB-DL-GRP")); len(c) == 0 || c[0].MovieID != movie.ID {
		t.Errorf("after rebuild: %+v", c)
	}
}

func TestRefreshMovieChangesStatus(t *testing.T) {
	e := newEnv(t)
	hist := events.NewHistoryWriter(e.bus, e.st, e.svc.log)
	future := metadata.Date{Year: 2027, Month: 3, Day: 1}
	m := &metadata.Movie{
		TMDBID: 42, Title: "Upcoming", Year: 2027, ReleaseDate: future, PosterPath: "/a.jpg",
		Releases: metadata.ReleaseSelection{Cinema: metadata.DatedRelease{Date: future, Region: "US", Type: metadata.ReleaseTheatrical}},
	}
	e.prov.set(m)
	movie := e.add(t, 42)
	if movie.Status != "announced" {
		t.Fatalf("status = %q, want announced", movie.Status)
	}
	sub := e.bus.Subscribe(8, func(ev *events.Event) bool { return ev.Type == events.MovieUpdated })

	// Nothing changed: no event, no history.
	ctx := context.Background()
	if changes, err := e.svc.RefreshMovie(ctx, movie.ID); err != nil || len(changes) != 0 {
		t.Fatalf("refresh without change = %v, %v", changes, err)
	}

	// Time passes and a digital release is announced, then reached.
	*e.now = time.Date(2027, 6, 1, 0, 0, 0, 0, time.UTC)
	m2 := *m
	m2.Releases.Digital = metadata.DatedRelease{Date: metadata.Date{Year: 2027, Month: 5, Day: 20}, Region: "US", Type: metadata.ReleaseDigital}
	m2.PosterPath = "/b.jpg"
	e.prov.set(&m2)
	changes, err := e.svc.RefreshMovie(ctx, movie.ID)
	if err != nil {
		t.Fatal(err)
	}
	if !slices.Contains(changes, "status") || !slices.Contains(changes, "releaseDigital") || !slices.Contains(changes, "poster") {
		t.Errorf("changes = %v", changes)
	}
	got, _ := e.svc.GetMovie(ctx, movie.ID)
	if got.Movie.Status != "released" || got.Movie.ReleaseDigital == nil || *got.Movie.ReleaseDigital != "2027-05-20" {
		t.Errorf("after refresh = %+v", got.Movie)
	}
	select {
	case ev := <-sub.C():
		if ev.History == nil || ev.History.Event != "metadata_changed" {
			t.Errorf("event = %+v", ev)
		}
	default:
		t.Error("no movie.updated event")
	}

	// TMDB drops the movie: status deleted, the rest kept.
	e.prov.mu.Lock()
	delete(e.prov.movies, 42)
	e.prov.mu.Unlock()
	if changes, err := e.svc.RefreshMovie(ctx, movie.ID); err != nil || !slices.Equal(changes, []string{"status"}) {
		t.Fatalf("refresh of a removed movie = %v, %v", changes, err)
	}
	got, _ = e.svc.GetMovie(ctx, movie.ID)
	if got.Movie.Status != "deleted" || got.Movie.Title != "Upcoming" {
		t.Errorf("after TMDB removal = %+v", got.Movie)
	}

	e.bus.Close()
	wctx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	if err := hist.Wait(wctx); err != nil {
		t.Fatal(err)
	}
	rows, err := e.st.Read().ListMovieHistory(ctx, db.ListMovieHistoryParams{MovieID: movie.ID, BeforeID: math.MaxInt64, Limit: 10})
	if err != nil {
		t.Fatal(err)
	}
	// Newest first: two metadata changes, then the addition.
	if len(rows) != 3 || rows[0].Event != "metadata_changed" || !strings.Contains(rows[0].Data, "TMDB no longer knows") ||
		rows[2].Event != "movie_added" {
		t.Errorf("history = %+v", rows)
	}
}

// Deleting a movie removes database rows only: its folder and files stay.
func TestDeleteMovieKeepsFiles(t *testing.T) {
	e := newEnv(t)
	root := t.TempDir()
	rf, err := e.st.Write().CreateRootFolder(context.Background(), root)
	if err != nil {
		t.Fatal(err)
	}
	e.prov.set(released(603, "The Matrix", 1999))
	ctx := context.Background()
	m, _, err := e.svc.AddMovie(ctx, 603, AddOptions{RootFolderID: rf.ID, QualityProfileID: e.lib.ProfileID, Monitored: true})
	if err != nil {
		t.Fatal(err)
	}
	file := filepath.Join(root, m.Path, "The.Matrix.1999.1080p.mkv")
	if err := os.MkdirAll(filepath.Dir(file), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(file, []byte("video"), 0o644); err != nil {
		t.Fatal(err)
	}
	sub := e.bus.Subscribe(4, func(ev *events.Event) bool { return ev.Type == events.MovieDeleted })

	if _, err := e.svc.DeleteMovie(ctx, m.ID, DeleteOptions{AddExclusion: true}); err != nil {
		t.Fatal(err)
	}
	if b, err := os.ReadFile(file); err != nil || string(b) != "video" {
		t.Fatalf("movie file after delete: %q, %v", b, err)
	}
	if _, err := e.svc.GetMovie(ctx, m.ID); !errors.Is(err, store.ErrNotFound) {
		t.Errorf("GetMovie after delete: %v", err)
	}
	if c := e.svc.MatchRelease(parser.Parse("The.Matrix.1999.1080p.BluRay.x264-GRP")); len(c) != 0 {
		t.Errorf("matcher still knows the movie: %+v", c)
	}
	if ex, _ := e.st.Read().IsImportListExcluded(ctx, 603); !ex {
		t.Error("exclusion not added")
	}
	select {
	case ev := <-sub.C():
		if !strings.Contains(ev.Data.(MovieEvent).Note, "files were kept") {
			t.Errorf("event = %+v", ev)
		}
	default:
		t.Error("no movie.deleted event")
	}
	if _, err := e.svc.DeleteMovie(ctx, m.ID, DeleteOptions{}); !errors.Is(err, store.ErrNotFound) {
		t.Errorf("second delete: %v", err)
	}
}

func TestUpdateMovie(t *testing.T) {
	e := newEnv(t)
	e.prov.set(released(1, "Alien", 1979))
	m := e.add(t, 1)
	ctx := context.Background()
	off, avail, tags := false, "in_cinemas", []string{"sci-fi"}
	got, err := e.svc.UpdateMovie(ctx, m.ID, UpdateOptions{Monitored: &off, MinimumAvailability: &avail, Tags: &tags})
	if err != nil {
		t.Fatal(err)
	}
	if got.Monitored || got.MinimumAvailability != "in_cinemas" || got.Path != m.Path {
		t.Errorf("updated = %+v", got)
	}
	bad := int64(999)
	if _, err := e.svc.UpdateMovie(ctx, m.ID, UpdateOptions{QualityProfileID: &bad}); !errors.Is(err, ErrInvalid) {
		t.Errorf("unknown profile: %v", err)
	}
	if _, err := e.svc.UpdateMovie(ctx, 12345, UpdateOptions{Monitored: &off}); !errors.Is(err, store.ErrNotFound) {
		t.Errorf("unknown movie: %v", err)
	}
}

func TestListMovies(t *testing.T) {
	e := newEnv(t)
	titles := map[int]struct {
		title string
		year  int
	}{1: {"Alien", 1979}, 2: {"The Thing", 1982}, 3: {"Heat", 1995}, 4: {"Arrival", 2016}, 5: {"Zodiac", 2007}}
	ctx := context.Background()
	for id := 1; id <= 5; id++ {
		m := released(id, titles[id].title, titles[id].year)
		if id == 3 {
			m.Genres = []string{"Crime"}
		}
		e.prov.set(m)
		*e.now = e.now.Add(time.Minute)
		e.add(t, id)
	}
	off := false
	if _, err := e.svc.UpdateMovie(ctx, 5, UpdateOptions{Monitored: &off}); err != nil {
		t.Fatal(err)
	}

	collect := func(q ListQuery) []string {
		t.Helper()
		var out []string
		for range 10 {
			p, err := e.svc.ListMovies(ctx, q)
			if err != nil {
				t.Fatal(err)
			}
			for _, m := range p.Movies {
				out = append(out, m.Title)
			}
			if p.NextCursor == "" {
				return out
			}
			q.Cursor = p.NextCursor
		}
		t.Fatal("pagination does not end")
		return nil
	}
	on := true
	tests := []struct {
		name string
		q    ListQuery
		want []string
	}{
		{"title pages of 2", ListQuery{Limit: 2}, []string{"Alien", "Arrival", "Heat", "The Thing", "Zodiac"}},
		{"year desc", ListQuery{Sort: SortYearDesc, Limit: 2}, []string{"Arrival", "Zodiac", "Heat", "The Thing", "Alien"}},
		{"added desc", ListQuery{Sort: SortAdded, Limit: 3}, []string{"Zodiac", "Arrival", "Heat", "The Thing", "Alien"}},
		{"monitored", ListQuery{Filter: ListFilter{Monitored: &on}}, []string{"Alien", "Arrival", "Heat", "The Thing"}},
		{"years", ListQuery{Filter: ListFilter{YearFrom: 1980, YearTo: 2010}}, []string{"Heat", "The Thing", "Zodiac"}},
		{"genre", ListQuery{Filter: ListFilter{Genre: "Crime"}}, []string{"Heat"}},
		{"status", ListQuery{Filter: ListFilter{Status: "announced"}}, nil},
		{"root folder", ListQuery{Filter: ListFilter{RootFolderID: e.lib.RootFolderID}, Limit: 1}, []string{"Alien", "Arrival", "Heat", "The Thing", "Zodiac"}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := collect(tt.q); !slices.Equal(got, tt.want) {
				t.Errorf("got %v, want %v", got, tt.want)
			}
		})
	}

	for _, bad := range []ListQuery{
		{Sort: "rating"}, {Limit: 501}, {Filter: ListFilter{Status: "x"}},
		{Filter: ListFilter{YearFrom: 2000, YearTo: 1990}}, {Cursor: "!!!"},
	} {
		if _, err := e.svc.ListMovies(ctx, bad); !errors.Is(err, ErrInvalid) {
			t.Errorf("ListMovies(%+v) err = %v, want ErrInvalid", bad, err)
		}
	}
	// A cursor of another sort order is refused.
	p, _ := e.svc.ListMovies(ctx, ListQuery{Limit: 1})
	if _, err := e.svc.ListMovies(ctx, ListQuery{Sort: SortAdded, Cursor: p.NextCursor}); !errors.Is(err, ErrInvalid) {
		t.Errorf("foreign cursor: %v", err)
	}
}

func TestTMDBKey(t *testing.T) {
	e := newEnv(t) // SetTMDBKey succeeded in newEnv
	ctx := context.Background()
	raw, err := e.st.Read().GetSetting(ctx, SettingTMDBKey)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(raw, "0123456789abcdef") {
		t.Fatal("the TMDB key is stored in clear")
	}

	// Rejected key: nothing saved, provider unchanged.
	e.prov.validateErr = metadata.ErrUnauthorized
	if err := e.svc.SetTMDBKey(ctx, "fedcba9876543210fedcba9876543210"); !errors.Is(err, metadata.ErrUnauthorized) {
		t.Errorf("rejected key: %v", err)
	}
	if after, _ := e.st.Read().GetSetting(ctx, SettingTMDBKey); after != raw {
		t.Error("a rejected key replaced the stored one")
	}
	if err := e.svc.SetTMDBKey(ctx, "bad-format"); !errors.Is(err, ErrInvalid) {
		t.Errorf("malformed key: %v", err)
	}
	if err := e.svc.SetTMDBKey(ctx, "  "); !errors.Is(err, ErrInvalid) {
		t.Errorf("empty key: %v", err)
	}

	// A new service over the same database loads the stored key.
	var gotKey string
	svc2, err := New(Options{Store: e.st, Box: e.svc.box, NewProvider: func(k string) (KeyedProvider, error) {
		gotKey = k
		return e.prov, nil
	}})
	if err != nil {
		t.Fatal(err)
	}
	if svc2.TMDBConfigured() {
		t.Error("configured before Init")
	}
	if err := svc2.Init(ctx); err != nil {
		t.Fatal(err)
	}
	if !svc2.TMDBConfigured() || gotKey != "0123456789abcdef0123456789abcdef" {
		t.Errorf("after Init: configured %v, key %q", svc2.TMDBConfigured(), gotKey)
	}
}

func TestSeedQualityProfiles(t *testing.T) {
	e := newEnv(t) // Init seeded once
	ctx := context.Background()
	profiles, err := e.svc.ListQualityProfiles(ctx)
	if err != nil {
		t.Fatal(err)
	}
	// 4 starter profiles + "HD" from storetest.SeedLibrary.
	if len(profiles) != len(quality.StarterProfiles())+1 {
		t.Fatalf("profiles = %d", len(profiles))
	}
	for _, p := range profiles {
		if p.Name == quality.ProfileHD1080p {
			if p.CutoffQuality != "bluray-1080p" || len(p.Items) != len(quality.All())-1 && len(p.Items) != len(quality.All()) {
				t.Errorf("HD-1080p = %+v (%d items)", p.QualityProfile, len(p.Items))
			}
			if p.Items[0].Quality != "remux-1080p" || !p.Items[0].Allowed {
				t.Errorf("first item = %+v", p.Items[0])
			}
		}
	}
	if n, err := e.svc.SeedQualityProfiles(ctx); err != nil || n != 0 {
		t.Errorf("second seed = %d, %v", n, err)
	}
}

func TestRootFolders(t *testing.T) {
	e := newEnv(t)
	ctx := context.Background()
	e.fs.dirs["file.txt"] = nil
	e.fs.dirs["srv/media"] = nil
	delete(e.fs.dirs, "srv/media")
	e.fs.dirs["srv/media"] = e.fs.dirs["movies"]
	e.fs.dirs["ro"] = e.fs.dirs["movies"]
	e.fs.readOnly["/ro"] = true

	tests := []struct {
		path string
		want string // substring of the error; "" = success
	}{
		{"relative/path", "absolute"},
		{"/missing", "does not exist"},
		{"/ro", "cannot write"},
		{"/movies/sub", "overlaps"}, // /movies is a root folder (storetest)
		{"/", "overlaps"},
		{"/srv/media/", ""},
	}
	e.fs.dirs["movies/sub"] = e.fs.dirs["movies"]
	for _, tt := range tests {
		rf, err := e.svc.AddRootFolder(ctx, tt.path)
		switch {
		case tt.want == "" && err != nil:
			t.Errorf("AddRootFolder(%q): %v", tt.path, err)
		case tt.want == "" && (rf.Path != "/srv/media" || !rf.Accessible || rf.FreeSpace == nil || *rf.FreeSpace != 500<<30):
			t.Errorf("AddRootFolder(%q) = %+v", tt.path, rf)
		case tt.want != "" && (err == nil || !strings.Contains(err.Error(), tt.want) || !errors.Is(err, ErrInvalid)):
			t.Errorf("AddRootFolder(%q) err = %v, want %q", tt.path, err, tt.want)
		}
	}

	list, err := e.svc.ListRootFolders(ctx)
	if err != nil || len(list) != 2 {
		t.Fatalf("list = %+v, %v", list, err)
	}
	// /movies is used by a movie: it cannot be removed.
	e.prov.set(released(1, "Alien", 1979))
	e.add(t, 1)
	if err := e.svc.DeleteRootFolder(ctx, e.lib.RootFolderID); !errors.Is(err, store.ErrConflict) || !strings.Contains(err.Error(), "1 movies") {
		t.Errorf("delete used folder: %v", err)
	}
	if err := e.svc.DeleteRootFolder(ctx, list[1].ID); err != nil {
		t.Errorf("delete unused folder: %v", err)
	}
	if err := e.svc.DeleteRootFolder(ctx, 999); !errors.Is(err, store.ErrNotFound) {
		t.Errorf("delete unknown folder: %v", err)
	}
}

func TestUpdateDiskSpaceHealth(t *testing.T) {
	e := newEnv(t)
	sub := e.bus.Subscribe(8, func(ev *events.Event) bool { return ev.Type == events.HealthChanged })
	ctx := context.Background()
	if err := e.svc.UpdateDiskSpace(ctx); err != nil { // /movies ok: no event
		t.Fatal(err)
	}
	delete(e.fs.dirs, "movies") // drive unmounted
	if err := e.svc.UpdateDiskSpace(ctx); err != nil {
		t.Fatal(err)
	}
	if err := e.svc.UpdateDiskSpace(ctx); err != nil { // unchanged: no second event
		t.Fatal(err)
	}
	ev := <-sub.C()
	if h := ev.Data.(HealthEvent); h.OK || !strings.Contains(h.Message, "no longer exists") {
		t.Errorf("health = %+v", h)
	}
	if len(sub.C()) != 0 {
		t.Error("health event repeated")
	}
}

func TestCollectionMonitor(t *testing.T) {
	e := newEnv(t)
	ref := &metadata.CollectionRef{TMDBID: 10, Name: "Alien Collection"}
	for _, m := range []*metadata.Movie{released(1, "Alien", 1979), released(2, "Aliens", 1986), released(3, "Alien 3", 1992)} {
		m.Collection = ref
		e.prov.set(m)
	}
	e.prov.collections[10] = &metadata.Collection{TMDBID: 10, Name: "Alien Collection", Parts: []metadata.MovieSummary{
		{TMDBID: 1, Title: "Alien"}, {TMDBID: 2, Title: "Aliens"}, {TMDBID: 3, Title: "Alien 3"}, {TMDBID: 4, Title: "Gone"},
	}}
	first := e.add(t, 1)
	ctx := context.Background()
	if err := e.st.Write().AddImportListExclusion(ctx, db.AddImportListExclusionParams{TmdbID: 3, Title: "Alien 3", Year: 1992}); err != nil {
		t.Fatal(err)
	}
	res, err := e.svc.MonitorCollection(ctx, *first.CollectionID, MonitorOptions{
		Monitored: true, QualityProfileID: e.lib.ProfileID, RootFolderID: e.lib.RootFolderID,
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(res.Added) != 1 || res.Added[0].TmdbID != 2 || !res.Collection.Monitored {
		t.Errorf("added = %+v", res.Added)
	}
	if len(res.Skipped) != 2 || res.Skipped[0].TmdbID != 3 || res.Skipped[1].TmdbID != 4 {
		t.Errorf("skipped = %+v", res.Skipped)
	}
	d, err := e.svc.GetCollection(ctx, *first.CollectionID)
	if err != nil || len(d.Movies) != 2 || len(d.Parts) != 4 {
		t.Errorf("collection = %+v, %v", d, err)
	}
}

func TestLookup(t *testing.T) {
	e := newEnv(t)
	e.prov.set(released(550, "Fight Club", 1999))
	e.prov.set(released(551, "Other", 2000))
	in := e.add(t, 550)
	ctx := context.Background()
	for _, q := range []string{"Fight Club", "tmdb:550", "tt0000550", "imdb:TT0000550"} {
		res, err := e.svc.Lookup(ctx, q, 0)
		if err != nil || len(res) != 1 || res[0].TMDBID != 550 || res[0].MovieID != in.ID {
			t.Errorf("Lookup(%q) = %+v, %v", q, res, err)
		}
	}
	if res, err := e.svc.Lookup(ctx, "tmdb:999", 0); err != nil || len(res) != 0 {
		t.Errorf("unknown tmdb id = %+v, %v", res, err)
	}
	for _, q := range []string{"", "tmdb:x", "imdb:x", strings.Repeat("a", 201)} {
		if _, err := e.svc.Lookup(ctx, q, 0); !errors.Is(err, ErrInvalid) {
			t.Errorf("Lookup(%q) err = %v", q, err)
		}
	}
}

func TestRefreshDue(t *testing.T) {
	now := testNow
	ago := func(d time.Duration) *int64 { v := store.Millis(now.Add(-d)); return &v }
	date := func(s string) *string { return &s }
	day := 24 * time.Hour
	tests := []struct {
		name     string
		status   string
		releases []*string
		at       *int64
		want     bool
	}{
		{"never refreshed", "released", nil, nil, true},
		{"announced, yesterday", "announced", nil, ago(day), true},
		{"announced, an hour ago", "announced", nil, ago(time.Hour), false},
		{"recent release, 2 days", "released", []*string{date("2026-09-01")}, ago(2 * day), false},
		{"recent release, 4 days", "released", []*string{date("2026-09-01")}, ago(4 * day), true},
		{"old release, 10 days", "released", []*string{date("1999-10-15")}, ago(10 * day), false},
		{"old release, 31 days", "released", []*string{date("1999-10-15")}, ago(31 * day), true},
		{"deleted, 10 days", "deleted", nil, ago(10 * day), false},
	}
	for _, tt := range tests {
		if got := RefreshDue(tt.status, tt.releases, tt.at, now); got != tt.want {
			t.Errorf("%s: RefreshDue = %v, want %v", tt.name, got, tt.want)
		}
	}
}
