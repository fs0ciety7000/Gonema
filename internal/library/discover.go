package library

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"slices"

	"github.com/fs0ciety7000/gonema/internal/metadata"
	"github.com/fs0ciety7000/gonema/internal/store"
	"github.com/fs0ciety7000/gonema/internal/store/db"
)

// Discover lists of the provider.
const (
	ListPopular    = "popular"
	ListTrending   = "trending"
	ListUpcoming   = "upcoming"
	ListNowPlaying = "now_playing"
)

// MaxDiscoverPage is the last page TMDB serves.
const MaxDiscoverPage = 500

// Flags say whether a provider movie is already in the library.
type Flags struct {
	MovieID   int64  // library movie id, 0 when not in the library
	Monitored bool   // library movies only
	State     string // library state, "" when not in the library
	Excluded  bool   // on the import list exclusions
}

// DiscoverItem is a provider movie with its library flags.
type DiscoverItem struct {
	metadata.MovieSummary
	Flags
}

// DiscoverPage is a page of a discover list.
type DiscoverPage struct {
	Page, TotalPages, TotalResults int
	Results                        []DiscoverItem
}

// Discover returns a page of a provider list (ListPopular...) with library
// flags. window is the trending period (day or week, default week). Adult
// movies are dropped.
func (s *Service) Discover(ctx context.Context, list, window string, page int) (DiscoverPage, error) {
	if page == 0 {
		page = 1
	}
	if page < 1 || page > MaxDiscoverPage {
		return DiscoverPage{}, invalid("page", "page must be between 1 and %d", MaxDiscoverPage)
	}
	p, err := s.Provider()
	if err != nil {
		return DiscoverPage{}, err
	}
	var res metadata.Page[metadata.MovieSummary]
	switch list {
	case ListPopular:
		res, err = p.Popular(ctx, page)
	case ListTrending:
		w := metadata.TrendingWeek
		switch window {
		case "", string(metadata.TrendingWeek):
		case string(metadata.TrendingDay):
			w = metadata.TrendingDay
		default:
			return DiscoverPage{}, invalid("window", "the trending window must be day or week")
		}
		res, err = p.Trending(ctx, w, page)
	case ListUpcoming:
		res, err = p.Upcoming(ctx, page)
	case ListNowPlaying:
		res, err = p.NowPlaying(ctx, page)
	default:
		return DiscoverPage{}, invalid("list", "the list must be popular, trending, upcoming or now_playing")
	}
	if err != nil {
		return DiscoverPage{}, fmt.Errorf("library: discover %s: %w", list, err)
	}
	items, err := s.flagSummaries(ctx, res.Results)
	if err != nil {
		return DiscoverPage{}, err
	}
	return DiscoverPage{Page: res.Page, TotalPages: res.TotalPages, TotalResults: res.TotalResults, Results: items}, nil
}

// FlagMovies returns the library flags of provider movies, by TMDB id. Movies
// absent from the result are not in the library and not excluded.
func (s *Service) FlagMovies(ctx context.Context, tmdbIDs []int) (map[int]Flags, error) {
	out := make(map[int]Flags, len(tmdbIDs))
	if len(tmdbIDs) == 0 {
		return out, nil
	}
	raw, err := json.Marshal(tmdbIDs)
	if err != nil {
		return nil, fmt.Errorf("library: encode ids: %w", err)
	}
	err = s.st.ReadTx(ctx, func(q *db.Queries) error {
		rows, err := q.ListMoviesByTmdbIDs(ctx, string(raw))
		if err != nil {
			return err
		}
		for _, r := range rows {
			out[int(r.TmdbID)] = Flags{MovieID: r.ID, Monitored: r.Monitored, State: r.State}
		}
		excluded, err := q.ListExcludedTmdbIDs(ctx, string(raw))
		if err != nil {
			return err
		}
		for _, id := range excluded {
			f := out[int(id)]
			f.Excluded = true
			out[int(id)] = f
		}
		return nil
	})
	if err != nil {
		return nil, store.MapError(fmt.Errorf("library: flag movies: %w", err))
	}
	return out, nil
}

func (s *Service) flagSummaries(ctx context.Context, list []metadata.MovieSummary) ([]DiscoverItem, error) {
	ids := make([]int, 0, len(list))
	for i := range list {
		if !list[i].Adult {
			ids = append(ids, list[i].TMDBID)
		}
	}
	flags, err := s.FlagMovies(ctx, ids)
	if err != nil {
		return nil, err
	}
	out := make([]DiscoverItem, 0, len(ids))
	for i := range list {
		if list[i].Adult {
			continue
		}
		out = append(out, DiscoverItem{MovieSummary: list[i], Flags: flags[list[i].TMDBID]})
	}
	return out, nil
}

// DiscoverCollection is a provider collection with library flags on its parts.
type DiscoverCollection struct {
	TMDBID       int
	Name         string
	Overview     string
	PosterPath   string
	BackdropPath string
	// CollectionID is the library collection (0 when none of its movies was
	// added yet); Monitored its setting.
	CollectionID int64
	Monitored    bool
	Parts        []DiscoverItem // by release date, undated last
}

// CollectionByTMDB returns a provider collection with library flags.
func (s *Service) CollectionByTMDB(ctx context.Context, tmdbID int) (DiscoverCollection, error) {
	if tmdbID <= 0 {
		return DiscoverCollection{}, invalid("tmdbId", "the TMDB id must be a positive number")
	}
	p, err := s.Provider()
	if err != nil {
		return DiscoverCollection{}, err
	}
	c, err := p.Collection(ctx, tmdbID)
	if errors.Is(err, metadata.ErrNotFound) {
		return DiscoverCollection{}, notFound("TMDB has no collection with id %d", tmdbID)
	}
	if err != nil {
		return DiscoverCollection{}, fmt.Errorf("library: collection %d: %w", tmdbID, err)
	}
	out := DiscoverCollection{
		TMDBID: c.TMDBID, Name: c.Name, Overview: c.Overview, PosterPath: c.PosterPath, BackdropPath: c.BackdropPath,
	}
	row, err := s.st.Read().GetCollectionByTmdbID(ctx, int64(tmdbID))
	switch err = store.MapError(err); {
	case err == nil:
		out.CollectionID, out.Monitored = row.ID, row.Monitored
		s.saveParts(ctx, row.ID, c.Parts)
	case !errors.Is(err, store.ErrNotFound):
		return DiscoverCollection{}, fmt.Errorf("library: collection %d: %w", tmdbID, err)
	}
	if out.Parts, err = s.flagSummaries(ctx, c.Parts); err != nil {
		return DiscoverCollection{}, err
	}
	return out, nil
}

// PersonMovie is one movie of a filmography: the person's roles merged (a
// director who also acts has one entry).
type PersonMovie struct {
	DiscoverItem
	Characters  []string // cast roles
	Jobs        []string // crew jobs
	Departments []string // crew departments
}

// Filmography returns the movies of a person, cast and crew merged by movie,
// upcoming (undated) first then newest first, with library flags. Adult movies
// are dropped. TMDB does not return the person's name with the credits.
func (s *Service) Filmography(ctx context.Context, personID int) ([]PersonMovie, error) {
	if personID <= 0 {
		return nil, invalid("tmdbId", "the TMDB id must be a positive number")
	}
	p, err := s.Provider()
	if err != nil {
		return nil, err
	}
	f, err := p.PersonFilmography(ctx, personID)
	if errors.Is(err, metadata.ErrNotFound) {
		return nil, notFound("TMDB has no person with id %d", personID)
	}
	if err != nil {
		return nil, fmt.Errorf("library: filmography of %d: %w", personID, err)
	}
	return s.mergeFilmography(ctx, f)
}

func (s *Service) mergeFilmography(ctx context.Context, f *metadata.Filmography) ([]PersonMovie, error) {
	byID := map[int]*PersonMovie{}
	var order []int
	add := func(c *metadata.FilmCredit) *PersonMovie {
		if c.Movie.Adult || c.Movie.TMDBID <= 0 {
			return nil
		}
		pm, ok := byID[c.Movie.TMDBID]
		if !ok {
			pm = &PersonMovie{DiscoverItem: DiscoverItem{MovieSummary: c.Movie}, Characters: []string{}, Jobs: []string{}, Departments: []string{}}
			byID[c.Movie.TMDBID] = pm
			order = append(order, c.Movie.TMDBID)
		}
		return pm
	}
	appendNew := func(list []string, v string) []string {
		if v == "" || slices.Contains(list, v) {
			return list
		}
		return append(list, v)
	}
	for i := range f.Cast {
		if pm := add(&f.Cast[i]); pm != nil {
			pm.Characters = appendNew(pm.Characters, f.Cast[i].Character)
		}
	}
	for i := range f.Crew {
		if pm := add(&f.Crew[i]); pm != nil {
			pm.Jobs = appendNew(pm.Jobs, f.Crew[i].Job)
			pm.Departments = appendNew(pm.Departments, f.Crew[i].Department)
		}
	}
	flags, err := s.FlagMovies(ctx, order)
	if err != nil {
		return nil, err
	}
	out := make([]PersonMovie, 0, len(order))
	for _, id := range order {
		pm := byID[id]
		pm.Flags = flags[id]
		out = append(out, *pm)
	}
	slices.SortStableFunc(out, func(a, b PersonMovie) int {
		da, dB := a.ReleaseDate, b.ReleaseDate
		switch {
		case da.IsZero() != dB.IsZero():
			if da.IsZero() {
				return -1
			}
			return 1
		case da != dB:
			return dB.Compare(da)
		}
		return a.TMDBID - b.TMDBID
	})
	return out, nil
}

// AddCollectionOptions are the settings of the movies added from a collection.
type AddCollectionOptions struct {
	AddOptions
	// MonitorCollection also monitors the collection: its future movies are
	// added automatically with the same settings (movies are then monitored).
	MonitorCollection bool
}

// AddCollectionResult reports what AddCollection did.
type AddCollectionResult struct {
	Collection db.Collection
	Added      []db.Movie
	Existing   []int64 // library ids of the parts already in the library
	Skipped    []SkippedPart
}

// AddCollection adds every movie of a provider collection that is not in the
// library yet (excluded movies are skipped), in one request: "monitor the whole
// saga in one click".
func (s *Service) AddCollection(ctx context.Context, tmdbID int, opts AddCollectionOptions) (AddCollectionResult, error) {
	if tmdbID <= 0 {
		return AddCollectionResult{}, invalid("tmdbId", "the TMDB id must be a positive number")
	}
	if opts.MinimumAvailability == "" {
		opts.MinimumAvailability = AvailReleased
	}
	if !validAvailability(opts.MinimumAvailability) {
		return AddCollectionResult{}, invalid("minimumAvailability", "minimum availability must be announced, in_cinemas or released")
	}
	if err := s.checkRefs(ctx, opts.RootFolderID, opts.QualityProfileID); err != nil {
		return AddCollectionResult{}, err
	}
	if _, err := normalizeTags(opts.Tags); err != nil {
		return AddCollectionResult{}, err
	}
	p, err := s.Provider()
	if err != nil {
		return AddCollectionResult{}, err
	}
	c, err := p.Collection(ctx, tmdbID)
	if errors.Is(err, metadata.ErrNotFound) {
		return AddCollectionResult{}, notFound("TMDB has no collection with id %d", tmdbID)
	}
	if err != nil {
		return AddCollectionResult{}, fmt.Errorf("library: collection %d: %w", tmdbID, err)
	}
	now := store.Millis(s.now())
	var res AddCollectionResult
	res.Collection, err = s.st.Write().UpsertCollection(ctx, db.UpsertCollectionParams{
		TmdbID: int64(c.TMDBID), Name: c.Name, Overview: c.Overview, PosterPath: optString(c.PosterPath),
		BackdropPath: optString(c.BackdropPath), RefreshedAt: &now,
	})
	if err != nil {
		return AddCollectionResult{}, store.MapError(fmt.Errorf("library: save collection %d: %w", tmdbID, err))
	}
	s.saveParts(ctx, res.Collection.ID, c.Parts)
	if opts.MonitorCollection {
		opts.Monitored = true
		if res.Collection, err = s.st.Write().UpdateCollectionSettings(ctx, db.UpdateCollectionSettingsParams{
			ID: res.Collection.ID, Monitored: true, QualityProfileID: &opts.QualityProfileID,
			RootFolderID: &opts.RootFolderID, MinimumAvailability: opts.MinimumAvailability, SearchOnAdd: opts.SearchOnAdd,
		}); err != nil {
			return AddCollectionResult{}, store.MapError(fmt.Errorf("library: monitor collection %d: %w", tmdbID, err))
		}
	}
	flags, err := s.FlagMovies(ctx, partIDs(c.Parts))
	if err != nil {
		return AddCollectionResult{}, err
	}
	for i := range c.Parts {
		part := &c.Parts[i]
		f := flags[part.TMDBID]
		switch {
		case part.Adult:
			continue
		case f.MovieID != 0:
			res.Existing = append(res.Existing, f.MovieID)
			continue
		case f.Excluded:
			res.Skipped = append(res.Skipped, SkippedPart{TmdbID: part.TMDBID, Title: part.Title, Reason: "on the exclusion list"})
			continue
		}
		m, created, err := s.AddMovie(ctx, part.TMDBID, opts.AddOptions)
		switch {
		case err != nil:
			if ctx.Err() != nil {
				return res, ctx.Err()
			}
			s.log.Warn("collection part not added", slog.Int("tmdb_id", part.TMDBID), slog.Any("error", err))
			res.Skipped = append(res.Skipped, SkippedPart{TmdbID: part.TMDBID, Title: part.Title, Reason: err.Error()})
		case created:
			res.Added = append(res.Added, m)
		default:
			res.Existing = append(res.Existing, m.ID)
		}
	}
	return res, nil
}

func partIDs(parts []metadata.MovieSummary) []int {
	out := make([]int, 0, len(parts))
	for i := range parts {
		out = append(out, parts[i].TMDBID)
	}
	return out
}
