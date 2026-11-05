package store_test

import (
	"context"
	"errors"
	"fmt"
	"path/filepath"
	"testing"
	"time"

	"github.com/fs0ciety7000/gonema/internal/store"
	"github.com/fs0ciety7000/gonema/internal/store/db"
	"github.com/fs0ciety7000/gonema/internal/store/storetest"
)

func ptr[T any](v T) *T { return &v }

// movieParams returns valid insert parameters for a movie.
func movieParams(lib storetest.Library, tmdbID int64, title string) db.InsertMovieParams {
	return db.InsertMovieParams{
		TmdbID:              tmdbID,
		ImdbID:              ptr(fmt.Sprintf("tt%07d", tmdbID)),
		Title:               title,
		OriginalTitle:       title,
		SortTitle:           title,
		CleanTitle:          title,
		OriginalLanguage:    "en",
		Year:                2000 + tmdbID%25,
		Status:              "released",
		ReleaseCinema:       ptr("2001-02-03"),
		Genres:              `["Drama"]`,
		Ratings:             `{}`,
		Extra:               `{}`,
		Monitored:           true,
		QualityProfileID:    lib.ProfileID,
		MinimumAvailability: "released",
		RootFolderID:        lib.RootFolderID,
		Path:                fmt.Sprintf("%s (%d)", title, tmdbID),
		AddedAt:             1_700_000_000_000 + tmdbID,
	}
}

func insertMovie(t testing.TB, s *store.Store, p db.InsertMovieParams) db.Movie {
	t.Helper()
	m, err := s.Write().InsertMovie(context.Background(), p)
	if err != nil {
		t.Fatalf("InsertMovie(%d): %v", p.TmdbID, store.MapError(err))
	}
	return m
}

func TestOpenRejectsBadPath(t *testing.T) {
	for _, p := range []string{"", "a?b.db", "a#b.db"} {
		if s, err := store.Open(context.Background(), p); err == nil {
			_ = s.Close()
			t.Errorf("Open(%q): want error", p)
		}
	}
}

func TestReaderIsQueryOnly(t *testing.T) {
	s := storetest.New(t)
	_, err := s.Read().CreateRootFolder(context.Background(), "/x")
	if err == nil {
		t.Fatal("write on the reader pool succeeded, want query_only error")
	}
}

func TestReopenKeepsDataAndSchema(t *testing.T) {
	ctx := context.Background()
	path := filepath.Join(t.TempDir(), "gonema.db")
	s, err := store.Open(ctx, path)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := s.Write().CreateRootFolder(ctx, "/movies"); err != nil {
		t.Fatal(err)
	}
	if err := s.Close(); err != nil {
		t.Fatal(err)
	}
	s, err = store.Open(ctx, path) // migrations already applied: no-op
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	rfs, err := s.Read().ListRootFolders(ctx)
	if err != nil || len(rfs) != 1 {
		t.Fatalf("ListRootFolders = %v, %v; want 1 row", rfs, err)
	}
}

func TestMigrationsUpDownUp(t *testing.T) {
	ctx := context.Background()
	s := storetest.New(t)
	v, err := s.SchemaVersion(ctx)
	if err != nil || v != 1 {
		t.Fatalf("SchemaVersion = %d, %v; want 1", v, err)
	}
	if err := s.MigrateDownTo(ctx, 0); err != nil {
		t.Fatal(err)
	}
	if v, err := s.SchemaVersion(ctx); err != nil || v != 0 {
		t.Fatalf("after down: SchemaVersion = %d, %v; want 0", v, err)
	}
	if _, err := s.Read().ListRootFolders(ctx); err == nil {
		t.Fatal("root_folders still exists after down")
	}
	if err := s.MigrateUp(ctx); err != nil {
		t.Fatal(err)
	}
	lib := storetest.SeedLibrary(t, s)
	insertMovie(t, s, movieParams(lib, 1, "alien"))
}

func TestMovieCRUD(t *testing.T) {
	ctx := context.Background()
	s := storetest.New(t)
	lib := storetest.SeedLibrary(t, s)
	r, w := s.Read(), s.Write()

	m := insertMovie(t, s, movieParams(lib, 603, "the matrix"))
	if m.ID == 0 || !m.Monitored || m.Genres != `["Drama"]` {
		t.Fatalf("InsertMovie returned %+v", m)
	}
	for name, get := range map[string]func() (db.Movie, error){
		"id":   func() (db.Movie, error) { return r.GetMovie(ctx, m.ID) },
		"tmdb": func() (db.Movie, error) { return r.GetMovieByTmdbID(ctx, 603) },
		"imdb": func() (db.Movie, error) { return r.GetMovieByImdbID(ctx, "tt0000603") },
	} {
		got, err := get()
		if err != nil || got.ID != m.ID {
			t.Errorf("get by %s = %d, %v; want %d", name, got.ID, err, m.ID)
		}
	}
	if _, err := r.GetMovie(ctx, 9999); !errors.Is(store.MapError(err), store.ErrNotFound) {
		t.Errorf("GetMovie(missing) err = %v, want ErrNotFound", err)
	}

	// Settings changed by the user survive a metadata upsert.
	if _, err := w.UpdateMovieSettings(ctx, db.UpdateMovieSettingsParams{
		ID: m.ID, Monitored: false, QualityProfileID: lib.ProfileID, MinimumAvailability: "in_cinemas",
		RootFolderID: lib.RootFolderID, Path: "The Matrix (1999)",
	}); err != nil {
		t.Fatal(err)
	}
	up := db.UpsertMovieParams(movieParams(lib, 603, "the matrix reloaded"))
	up.Monitored = true
	up.Path = "ignored"
	got, err := w.UpsertMovie(ctx, up)
	if err != nil {
		t.Fatal(err)
	}
	if got.ID != m.ID || got.Title != "the matrix reloaded" || got.Monitored ||
		got.Path != "The Matrix (1999)" || got.MinimumAvailability != "in_cinemas" {
		t.Errorf("upsert: got %+v", got)
	}
	// Upsert of a new tmdb_id inserts.
	n, err := w.UpsertMovie(ctx, db.UpsertMovieParams(movieParams(lib, 604, "new")))
	if err != nil || n.ID == m.ID {
		t.Fatalf("upsert insert: %+v, %v", n, err)
	}

	md, err := w.UpdateMovieMetadata(ctx, db.UpdateMovieMetadataParams{
		ID: m.ID, Title: "The Matrix", SortTitle: "matrix", CleanTitle: "thematrix", Year: 1999,
		Status: "released", Genres: `["Action","Science Fiction"]`, Ratings: `{"tmdb":{"value":8.2,"votes":1}}`,
		Extra: `{}`, DominantColor: ptr("#1a2b3c"), MetadataRefreshedAt: 42,
	})
	if err != nil {
		t.Fatal(err)
	}
	if md.Year != 1999 || md.MetadataRefreshedAt == nil || *md.MetadataRefreshedAt != 42 || md.ImdbID != nil {
		t.Errorf("UpdateMovieMetadata = %+v", md)
	}
	if _, err := w.UpdateMovieMetadata(ctx, db.UpdateMovieMetadataParams{ID: 9999, Title: "x", Status: "released", Genres: "[]", Ratings: "{}", Extra: "{}"}); !errors.Is(store.MapError(err), store.ErrNotFound) {
		t.Errorf("UpdateMovieMetadata(missing) err = %v", err)
	}
	if err := store.RequireRow(w.SetMovieMonitored(ctx, db.SetMovieMonitoredParams{ID: m.ID, Monitored: true})); err != nil {
		t.Error(err)
	}
	if err := store.RequireRow(w.SetMovieLastSearched(ctx, db.SetMovieLastSearchedParams{ID: m.ID, LastSearchedAt: 7})); err != nil {
		t.Error(err)
	}
	if c, err := r.CountMovies(ctx); err != nil || c != 2 {
		t.Errorf("CountMovies = %d, %v", c, err)
	}

	// Delete cascades to owned rows.
	if err := s.ReplaceAlternateTitles(ctx, m.ID, "tmdb", []store.AlternateTitle{{Title: "Matrix", CleanTitle: "matrix"}}); err != nil {
		t.Fatal(err)
	}
	tag, err := w.CreateTag(ctx, "4k")
	if err != nil {
		t.Fatal(err)
	}
	if err := w.AddMovieTag(ctx, db.AddMovieTagParams{MovieID: m.ID, TagID: tag.ID}); err != nil {
		t.Fatal(err)
	}
	if err := store.RequireRow(w.DeleteMovie(ctx, m.ID)); err != nil {
		t.Fatal(err)
	}
	if err := store.RequireRow(w.DeleteMovie(ctx, m.ID)); !errors.Is(err, store.ErrNotFound) {
		t.Errorf("second DeleteMovie err = %v, want ErrNotFound", err)
	}
	if alts, _ := r.ListAlternateTitles(ctx, m.ID); len(alts) != 0 {
		t.Errorf("alternate titles not cascaded: %v", alts)
	}
	if ids, _ := r.ListMovieIDsByTag(ctx, tag.ID); len(ids) != 0 {
		t.Errorf("movie_tags not cascaded: %v", ids)
	}
}

func TestConstraints(t *testing.T) {
	ctx := context.Background()
	s := storetest.New(t)
	lib := storetest.SeedLibrary(t, s)
	insertMovie(t, s, movieParams(lib, 1, "first"))

	tests := []struct {
		name   string
		modify func(p *db.InsertMovieParams)
		want   error
	}{
		{"bad status enum", func(p *db.InsertMovieParams) { p.Status = "downloading" }, store.ErrInvalid},
		{"bad availability enum", func(p *db.InsertMovieParams) { p.MinimumAvailability = "soon" }, store.ErrInvalid},
		{"invalid JSON", func(p *db.InsertMovieParams) { p.Genres = `["Drama"` }, store.ErrInvalid},
		{"JSON not an array", func(p *db.InsertMovieParams) { p.Genres = `{"a":1}` }, store.ErrInvalid},
		{"bad date", func(p *db.InsertMovieParams) { p.ReleaseCinema = ptr("2021-02-30") }, store.ErrInvalid},
		{"bad color", func(p *db.InsertMovieParams) { p.DominantColor = ptr("red") }, store.ErrInvalid},
		{"bad imdb id", func(p *db.InsertMovieParams) { p.ImdbID = ptr("nm123") }, store.ErrInvalid},
		{"empty title", func(p *db.InsertMovieParams) { p.Title = "" }, store.ErrInvalid},
		{"duplicate tmdb_id", func(p *db.InsertMovieParams) { p.TmdbID = 1 }, store.ErrConflict},
		{"duplicate path", func(p *db.InsertMovieParams) { p.Path = "first (1)" }, store.ErrConflict},
		{"missing profile FK", func(p *db.InsertMovieParams) { p.QualityProfileID = 999 }, store.ErrConflict},
		{"missing root folder FK", func(p *db.InsertMovieParams) { p.RootFolderID = 999 }, store.ErrConflict},
		{"missing collection FK", func(p *db.InsertMovieParams) { p.CollectionID = ptr[int64](999) }, store.ErrConflict},
	}
	for i, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			p := movieParams(lib, int64(100+i), fmt.Sprintf("m%d", i))
			tt.modify(&p)
			_, err := s.Write().InsertMovie(ctx, p)
			if !errors.Is(store.MapError(err), tt.want) {
				t.Errorf("err = %v, want %v", err, tt.want)
			}
		})
	}

	t.Run("profile in use is RESTRICTed", func(t *testing.T) {
		err := store.RequireRow(s.Write().DeleteQualityProfile(ctx, lib.ProfileID))
		if !errors.Is(err, store.ErrConflict) {
			t.Errorf("err = %v, want ErrConflict", err)
		}
	})
	t.Run("root folder in use is RESTRICTed", func(t *testing.T) {
		err := store.RequireRow(s.Write().DeleteRootFolder(ctx, lib.RootFolderID))
		if !errors.Is(err, store.ErrConflict) {
			t.Errorf("err = %v, want ErrConflict", err)
		}
	})
	t.Run("invalid setting JSON", func(t *testing.T) {
		err := s.Write().SetSetting(ctx, db.SetSettingParams{Key: "k", Value: "not json"})
		if !errors.Is(store.MapError(err), store.ErrInvalid) {
			t.Errorf("err = %v, want ErrInvalid", err)
		}
	})
	t.Run("job bad status via cancel of unknown id", func(t *testing.T) {
		err := store.RequireRow(s.Write().CancelJob(ctx, db.CancelJobParams{ID: 42, Now: 1}))
		if !errors.Is(err, store.ErrNotFound) {
			t.Errorf("err = %v, want ErrNotFound", err)
		}
	})
}

func TestWithTxRollsBack(t *testing.T) {
	ctx := context.Background()
	s := storetest.New(t)
	boom := errors.New("boom")
	err := s.WithTx(ctx, func(q *db.Queries) error {
		if _, err := q.CreateRootFolder(ctx, "/a"); err != nil {
			return err
		}
		return boom
	})
	if !errors.Is(err, boom) {
		t.Fatalf("err = %v, want boom", err)
	}
	if rfs, _ := s.Read().ListRootFolders(ctx); len(rfs) != 0 {
		t.Errorf("rollback failed: %v", rfs)
	}
	// Constraint errors inside a transaction are mapped.
	err = s.WithTx(ctx, func(q *db.Queries) error {
		_, err := q.CreateTag(ctx, "")
		return err
	})
	if !errors.Is(err, store.ErrInvalid) {
		t.Errorf("err = %v, want ErrInvalid", err)
	}
}

func TestAlternateTitlesAndMatcherIndex(t *testing.T) {
	ctx := context.Background()
	s := storetest.New(t)
	lib := storetest.SeedLibrary(t, s)
	m := insertMovie(t, s, movieParams(lib, 1, "amelie"))
	insertMovie(t, s, movieParams(lib, 2, "heat"))

	if err := s.ReplaceAlternateTitles(ctx, m.ID, "user", []store.AlternateTitle{{Title: "Amélie (user)", CleanTitle: "amelieuser"}}); err != nil {
		t.Fatal(err)
	}
	for _, set := range [][]store.AlternateTitle{
		{{Title: "Le Fabuleux Destin", CleanTitle: "lefabuleuxdestin", Country: "FR"}, {Title: "Die fabelhafte Welt", CleanTitle: "diefabelhaftewelt", Country: "DE"}},
		{{Title: "Le Fabuleux Destin d'Amélie Poulain", CleanTitle: "lefabuleuxdestindameliepoulain", Country: "FR"}},
	} {
		if err := s.ReplaceAlternateTitles(ctx, m.ID, "tmdb", set); err != nil {
			t.Fatal(err)
		}
	}
	alts, err := s.Read().ListAlternateTitles(ctx, m.ID)
	if err != nil || len(alts) != 2 {
		t.Fatalf("ListAlternateTitles = %v, %v; want user + 1 tmdb", alts, err)
	}
	if err := s.ReplaceAlternateTitles(ctx, m.ID, "robot", nil); err != nil {
		t.Fatalf("empty replace with unknown source should be a no-op, got %v", err)
	}
	if err := s.ReplaceAlternateTitles(ctx, m.ID, "robot", []store.AlternateTitle{{Title: "x", CleanTitle: "x"}}); !errors.Is(err, store.ErrInvalid) {
		t.Errorf("bad source err = %v, want ErrInvalid", err)
	}

	titles, err := s.Read().ListMatcherTitles(ctx)
	if err != nil || len(titles) != 2 || titles[0].CleanTitle != "amelie" {
		t.Errorf("ListMatcherTitles = %+v, %v", titles, err)
	}
	mAlts, err := s.Read().ListMatcherAlternateTitles(ctx)
	if err != nil || len(mAlts) != 2 || mAlts[0].MovieID != m.ID || mAlts[0].Year != m.Year {
		t.Errorf("ListMatcherAlternateTitles = %+v, %v", mAlts, err)
	}
}

func TestCollections(t *testing.T) {
	ctx := context.Background()
	s := storetest.New(t)
	lib := storetest.SeedLibrary(t, s)
	w, r := s.Write(), s.Read()

	c, err := w.UpsertCollection(ctx, db.UpsertCollectionParams{TmdbID: 10, Name: "Alien Collection", RefreshedAt: ptr[int64](1)})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := w.UpdateCollectionSettings(ctx, db.UpdateCollectionSettingsParams{ID: c.ID, Monitored: true, MinimumAvailability: "released"}); !errors.Is(store.MapError(err), store.ErrInvalid) {
		t.Errorf("monitored without profile: err = %v, want ErrInvalid", err)
	}
	if _, err := w.UpdateCollectionSettings(ctx, db.UpdateCollectionSettingsParams{
		ID: c.ID, Monitored: true, QualityProfileID: &lib.ProfileID, RootFolderID: &lib.RootFolderID, MinimumAvailability: "released",
	}); err != nil {
		t.Fatal(err)
	}
	c2, err := w.UpsertCollection(ctx, db.UpsertCollectionParams{TmdbID: 10, Name: "Alien - Collection", Overview: "o", RefreshedAt: ptr[int64](2)})
	if err != nil || c2.ID != c.ID || !c2.Monitored || c2.Name != "Alien - Collection" {
		t.Fatalf("re-upsert = %+v, %v", c2, err)
	}
	for i, title := range []string{"aliens", "alien"} {
		p := movieParams(lib, int64(i+1), title)
		p.CollectionID = &c.ID
		p.Year = int64(1986 - 7*i)
		insertMovie(t, s, p)
	}
	ms, err := r.ListCollectionMovies(ctx, c.ID)
	if err != nil || len(ms) != 2 || ms[0].Title != "alien" {
		t.Errorf("ListCollectionMovies = %v, %v", ms, err)
	}
	if got, err := r.GetCollectionByTmdbID(ctx, 10); err != nil || got.ID != c.ID {
		t.Errorf("GetCollectionByTmdbID = %v, %v", got, err)
	}
	if all, err := r.ListCollections(ctx); err != nil || len(all) != 1 {
		t.Errorf("ListCollections = %v, %v", all, err)
	}
	// Deleting the collection keeps the movies (SET NULL).
	if err := store.RequireRow(w.DeleteCollection(ctx, c.ID)); err != nil {
		t.Fatal(err)
	}
	m, err := r.GetMovieByTmdbID(ctx, 1)
	if err != nil || m.CollectionID != nil {
		t.Errorf("movie after collection delete = %+v, %v", m, err)
	}
}

func TestTagsRootFoldersProfilesSettingsExclusions(t *testing.T) {
	ctx := context.Background()
	s := storetest.New(t)
	lib := storetest.SeedLibrary(t, s)
	w, r := s.Write(), s.Read()
	m := insertMovie(t, s, movieParams(lib, 1, "heat"))

	// Tags.
	tag, err := w.CreateTag(ctx, "kids")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := w.CreateTag(ctx, "kids"); !errors.Is(store.MapError(err), store.ErrConflict) {
		t.Errorf("duplicate tag err = %v", err)
	}
	for range 2 { // idempotent
		if err := w.AddMovieTag(ctx, db.AddMovieTagParams{MovieID: m.ID, TagID: tag.ID}); err != nil {
			t.Fatal(err)
		}
	}
	if tags, err := r.ListMovieTags(ctx, m.ID); err != nil || len(tags) != 1 || tags[0].Label != "kids" {
		t.Errorf("ListMovieTags = %v, %v", tags, err)
	}
	if got, err := r.GetTagByLabel(ctx, "kids"); err != nil || got.ID != tag.ID {
		t.Errorf("GetTagByLabel = %v, %v", got, err)
	}
	if err := store.RequireRow(w.RemoveMovieTag(ctx, db.RemoveMovieTagParams{MovieID: m.ID, TagID: tag.ID})); err != nil {
		t.Error(err)
	}
	if err := w.AddMovieTag(ctx, db.AddMovieTagParams{MovieID: m.ID, TagID: 999}); !errors.Is(store.MapError(err), store.ErrConflict) {
		t.Errorf("tag FK err = %v", err)
	}
	if err := w.ClearMovieTags(ctx, m.ID); err != nil {
		t.Error(err)
	}
	if all, err := r.ListTags(ctx); err != nil || len(all) != 1 {
		t.Errorf("ListTags = %v, %v", all, err)
	}
	if err := store.RequireRow(w.DeleteTag(ctx, tag.ID)); err != nil {
		t.Error(err)
	}

	// Root folders.
	rf, err := w.CreateRootFolder(ctx, "/data/films")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := w.CreateRootFolder(ctx, "/data/films"); !errors.Is(store.MapError(err), store.ErrConflict) {
		t.Errorf("duplicate root folder err = %v", err)
	}
	if err := store.RequireRow(w.UpdateRootFolderSpace(ctx, db.UpdateRootFolderSpaceParams{ID: rf.ID, FreeSpace: ptr[int64](10), TotalSpace: ptr[int64](20), CheckedAt: 5})); err != nil {
		t.Fatal(err)
	}
	if got, err := w.UpdateRootFolderPath(ctx, db.UpdateRootFolderPathParams{ID: rf.ID, Path: "/data/movies"}); err != nil || got.Path != "/data/movies" || *got.FreeSpace != 10 {
		t.Errorf("UpdateRootFolderPath = %+v, %v", got, err)
	}
	if got, err := r.GetRootFolder(ctx, rf.ID); err != nil || got.CheckedAt == nil || *got.CheckedAt != 5 {
		t.Errorf("GetRootFolder = %+v, %v", got, err)
	}
	if err := store.RequireRow(w.DeleteRootFolder(ctx, rf.ID)); err != nil {
		t.Error(err)
	}

	// Quality profiles.
	p, err := r.GetQualityProfile(ctx, lib.ProfileID)
	if err != nil {
		t.Fatal(err)
	}
	items, err := r.ListQualityProfileItems(ctx, p.ID)
	if err != nil || len(items) != 4 || items[0].Position != 0 || items[3].Allowed {
		t.Errorf("ListQualityProfileItems = %+v, %v", items, err)
	}
	err = s.WithTx(ctx, func(q *db.Queries) error {
		if _, err := q.UpdateQualityProfile(ctx, db.UpdateQualityProfileParams{
			ID: p.ID, Name: "HD+", CutoffQuality: "webdl-720p", MinUpgradeStep: 1, Languages: `["fr"]`,
		}); err != nil {
			return err
		}
		return store.ReplaceQualityProfileItems(ctx, q, p.ID, []store.ProfileItem{{Quality: "webdl-720p", Allowed: true}})
	})
	if err != nil {
		t.Fatal(err)
	}
	if got, err := r.GetQualityProfileByName(ctx, "HD+"); err != nil || got.Languages != `["fr"]` || got.UpgradeAllowed {
		t.Errorf("GetQualityProfileByName = %+v, %v", got, err)
	}
	if _, err := s.CreateQualityProfile(ctx, db.CreateQualityProfileParams{Name: "dup", CutoffQuality: "x", Languages: "[]"},
		[]store.ProfileItem{{Quality: "x"}, {Quality: "x"}}); !errors.Is(err, store.ErrConflict) {
		t.Errorf("duplicate item err = %v, want ErrConflict", err)
	}
	if _, err := r.GetQualityProfileByName(ctx, "dup"); !errors.Is(store.MapError(err), store.ErrNotFound) {
		t.Errorf("failed CreateQualityProfile left a row: %v", err)
	}
	empty, err := s.CreateQualityProfile(ctx, db.CreateQualityProfileParams{Name: "unused", CutoffQuality: "dvd", Languages: "[]"}, nil)
	if err != nil {
		t.Fatal(err)
	}
	if all, err := r.ListQualityProfiles(ctx); err != nil || len(all) != 2 {
		t.Errorf("ListQualityProfiles = %v, %v", all, err)
	}
	if err := store.RequireRow(w.DeleteQualityProfile(ctx, empty.ID)); err != nil {
		t.Error(err)
	}

	// Settings.
	type naming struct {
		Template string `json:"template"`
	}
	if _, err := store.GetSetting[naming](ctx, s, "naming"); !errors.Is(err, store.ErrNotFound) {
		t.Errorf("GetSetting(unset) err = %v", err)
	}
	for _, tpl := range []string{"{Title} ({Year})", "{Title}"} {
		if err := store.SetSetting(ctx, s, "naming", naming{tpl}); err != nil {
			t.Fatal(err)
		}
	}
	if got, err := store.GetSetting[naming](ctx, s, "naming"); err != nil || got.Template != "{Title}" {
		t.Errorf("GetSetting = %+v, %v", got, err)
	}
	if all, err := r.ListSettings(ctx); err != nil || len(all) != 1 {
		t.Errorf("ListSettings = %v, %v", all, err)
	}
	if err := store.RequireRow(w.DeleteSetting(ctx, "naming")); err != nil {
		t.Error(err)
	}

	// Import list exclusions.
	for range 2 {
		if err := w.AddImportListExclusion(ctx, db.AddImportListExclusionParams{TmdbID: 77, Title: "Bad", Year: 2001, CreatedAt: 1}); err != nil {
			t.Fatal(err)
		}
	}
	if ex, err := r.IsImportListExcluded(ctx, 77); err != nil || !ex {
		t.Errorf("IsImportListExcluded(77) = %v, %v", ex, err)
	}
	if ex, err := r.IsImportListExcluded(ctx, 78); err != nil || ex {
		t.Errorf("IsImportListExcluded(78) = %v, %v", ex, err)
	}
	if all, err := r.ListImportListExclusions(ctx); err != nil || len(all) != 1 {
		t.Errorf("ListImportListExclusions = %v, %v", all, err)
	}
	if err := store.RequireRow(w.DeleteImportListExclusion(ctx, 77)); err != nil {
		t.Error(err)
	}
}

func TestMillis(t *testing.T) {
	now := time.Date(2026, 11, 5, 12, 0, 0, 123_000_000, time.UTC)
	if got := store.FromMillis(store.Millis(now)); !got.Equal(now) {
		t.Errorf("round trip = %v, want %v", got, now)
	}
}
