package library

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"math"
	"slices"
	"strings"

	"github.com/fs0ciety7000/gonema/internal/events"
	"github.com/fs0ciety7000/gonema/internal/jobs"
	"github.com/fs0ciety7000/gonema/internal/match"
	"github.com/fs0ciety7000/gonema/internal/metadata"
	"github.com/fs0ciety7000/gonema/internal/store"
	"github.com/fs0ciety7000/gonema/internal/store/db"
)

// Minimum availability values (movies.minimum_availability).
const (
	AvailAnnounced = "announced"
	AvailInCinemas = "in_cinemas"
	AvailReleased  = "released"
)

func validAvailability(v string) bool {
	return v == AvailAnnounced || v == AvailInCinemas || v == AvailReleased
}

// Job kinds handled by the library (see RegisterJobs).
const (
	JobMovieImages     = "movie.images"
	JobMovieRefresh    = "movie.refresh"
	JobRefreshAll      = "metadata.refresh_all"
	JobRootFolderSpace = "rootfolders.space"
	// JobMovieSearch is handled by the search service; the library only enqueues it.
	JobMovieSearch = "movie.search"
)

// MoviePayload is the payload of per-movie jobs.
type MoviePayload struct {
	MovieID int64 `json:"movieId"`
}

// AddOptions are the user settings of a movie being added.
type AddOptions struct {
	RootFolderID        int64
	QualityProfileID    int64
	MinimumAvailability string // default "released"
	Monitored           bool
	Tags                []string
	SearchOnAdd         bool
}

// MovieEvent is the data of movie.* events.
type MovieEvent struct {
	ID      int64    `json:"id"`
	TmdbID  int64    `json:"tmdbId"`
	Title   string   `json:"title"`
	Year    int64    `json:"year"`
	Status  string   `json:"status"`
	Changes []string `json:"changes,omitempty"`
	Path    string   `json:"path,omitempty"`
	Note    string   `json:"note,omitempty"`
}

func movieEvent(m *db.Movie) MovieEvent {
	return MovieEvent{ID: m.ID, TmdbID: m.TmdbID, Title: m.Title, Year: m.Year, Status: m.Status}
}

// metaFields are the movie columns that come from the metadata provider.
type metaFields struct {
	ImdbID           *string
	Title            string
	OriginalTitle    string
	SortTitle        string
	CleanTitle       string
	OriginalLanguage string
	Year             int64
	Overview         string
	RuntimeMin       int64
	Status           string
	StatusReason     string
	ReleaseCinema    *string
	ReleaseDigital   *string
	ReleasePhysical  *string
	Certification    string
	Genres           string
	Ratings          string
	PosterPath       *string
	BackdropPath     *string
	Extra            string
}

// extraJSON is movies.extra: the display-only metadata plus the reason of the
// derived status, so the UI can explain it.
type extraJSON struct {
	metadata.Extra
	StatusReason string `json:"statusReason,omitempty"`
}

func optString(s string) *string {
	if s == "" {
		return nil
	}
	return &s
}

func dateString(d metadata.Date) *string { return optString(d.String()) }

// fieldsOf maps provider metadata to movie columns. The status is derived again
// with the service clock, so that it does not depend on when the provider
// response was cached.
func (s *Service) fieldsOf(m *metadata.Movie) (metaFields, error) {
	status, reason := metadata.DeriveStatus(m.Releases, m.ReleaseDate, s.now())
	genres := m.Genres
	if genres == nil {
		genres = []string{}
	}
	g, err := json.Marshal(genres)
	if err != nil {
		return metaFields{}, fmt.Errorf("encode genres: %w", err)
	}
	r, err := json.Marshal(m.Ratings)
	if err != nil {
		return metaFields{}, fmt.Errorf("encode ratings: %w", err)
	}
	x, err := json.Marshal(extraJSON{Extra: m.Extra, StatusReason: reason})
	if err != nil {
		return metaFields{}, fmt.Errorf("encode extra: %w", err)
	}
	f := metaFields{
		Title:            strings.TrimSpace(m.Title),
		OriginalTitle:    m.OriginalTitle,
		OriginalLanguage: m.OriginalLanguage,
		Year:             int64(m.Year),
		Overview:         m.Overview,
		RuntimeMin:       int64(max(m.RuntimeMin, 0)),
		Status:           string(status),
		StatusReason:     reason,
		ReleaseCinema:    dateString(m.Releases.Cinema.Date),
		ReleaseDigital:   dateString(m.Releases.Digital.Date),
		ReleasePhysical:  dateString(m.Releases.Physical.Date),
		Certification:    m.Releases.Certification,
		Genres:           string(g),
		Ratings:          string(r),
		PosterPath:       optString(m.PosterPath),
		BackdropPath:     optString(m.BackdropPath),
		Extra:            string(x),
	}
	if f.Title == "" {
		return metaFields{}, fmt.Errorf("TMDB movie %d has no title", m.TMDBID)
	}
	if f.Year != 0 && (f.Year < 1870 || f.Year > 2200) {
		f.Year = 0
	}
	if strings.HasPrefix(m.IMDbID, "tt") {
		f.ImdbID = &m.IMDbID
	}
	f.SortTitle = SortTitle(f.Title)
	f.CleanTitle = match.Key(f.Title)
	if f.CleanTitle == "" {
		f.CleanTitle = f.SortTitle
	}
	return f, nil
}

// normalizeTags trims, deduplicates and validates tag labels.
func normalizeTags(tags []string) ([]string, error) {
	out := make([]string, 0, len(tags))
	for _, t := range tags {
		t = strings.TrimSpace(t)
		if t == "" {
			continue
		}
		if len(t) > 64 {
			return nil, invalid("tags", "the tag %q is longer than 64 characters", t[:20]+"...")
		}
		if !slices.Contains(out, t) {
			out = append(out, t)
		}
	}
	return out, nil
}

func setTags(ctx context.Context, q *db.Queries, movieID int64, tags []string) error {
	if err := q.ClearMovieTags(ctx, movieID); err != nil {
		return fmt.Errorf("clear tags: %w", err)
	}
	for _, label := range tags {
		tag, err := q.GetTagByLabel(ctx, label)
		if errors.Is(store.MapError(err), store.ErrNotFound) {
			tag, err = q.CreateTag(ctx, label)
		}
		if err != nil {
			return fmt.Errorf("tag %q: %w", label, err)
		}
		if err := q.AddMovieTag(ctx, db.AddMovieTagParams{MovieID: movieID, TagID: tag.ID}); err != nil {
			return fmt.Errorf("add tag %q: %w", label, err)
		}
	}
	return nil
}

// insertAlternateTitles replaces the TMDB alternate titles of a movie. Titles
// equal (by matching key) to the title or original title, and duplicates, are
// skipped: they add nothing to matching.
func insertAlternateTitles(ctx context.Context, q *db.Queries, movieID int64, f *metaFields, alts []metadata.AlternateTitle) error {
	if err := q.DeleteAlternateTitles(ctx, db.DeleteAlternateTitlesParams{MovieID: movieID, Source: "tmdb"}); err != nil {
		return fmt.Errorf("delete alternate titles: %w", err)
	}
	seen := map[string]bool{f.CleanTitle: true, match.Key(f.OriginalTitle): true}
	for _, a := range alts {
		title := strings.TrimSpace(a.Title)
		key := match.Key(title)
		if key == "" || seen[key] {
			continue
		}
		seen[key] = true
		if _, err := q.InsertAlternateTitle(ctx, db.InsertAlternateTitleParams{
			MovieID: movieID, Title: title, CleanTitle: key, Country: a.Country, Source: "tmdb",
		}); err != nil {
			return fmt.Errorf("insert alternate title: %w", err)
		}
	}
	return nil
}

func upsertCollection(ctx context.Context, q *db.Queries, c *metadata.CollectionRef, now int64) (*int64, error) {
	if c == nil || c.TMDBID <= 0 || strings.TrimSpace(c.Name) == "" {
		return nil, nil
	}
	row, err := q.UpsertCollection(ctx, db.UpsertCollectionParams{
		TmdbID: int64(c.TMDBID), Name: c.Name, PosterPath: optString(c.PosterPath),
		BackdropPath: optString(c.BackdropPath), RefreshedAt: &now,
	})
	if err != nil {
		return nil, fmt.Errorf("upsert collection %d: %w", c.TMDBID, err)
	}
	return &row.ID, nil
}

// folderTemplate returns the configured folder template, or the default.
func (s *Service) folderTemplate(ctx context.Context) string {
	t, err := store.GetSetting[string](ctx, s.st, SettingFolderTemplate)
	if err != nil || ValidateFolderTemplate(t) != nil {
		if err != nil && !errors.Is(err, store.ErrNotFound) {
			s.log.Warn("folder template setting unreadable, using the default", slog.Any("error", err))
		}
		return DefaultFolderTemplate
	}
	return t
}

// AddMovie adds a movie from TMDB. It is idempotent on the TMDB id: when the
// movie is already in the library, it is returned with created false and
// nothing changes.
func (s *Service) AddMovie(ctx context.Context, tmdbID int, opts AddOptions) (movie db.Movie, created bool, err error) {
	if tmdbID <= 0 {
		return db.Movie{}, false, invalid("tmdbId", "the TMDB id must be a positive number")
	}
	if opts.MinimumAvailability == "" {
		opts.MinimumAvailability = AvailReleased
	}
	if !validAvailability(opts.MinimumAvailability) {
		return db.Movie{}, false, invalid("minimumAvailability", "minimum availability must be announced, in_cinemas or released")
	}
	tags, err := normalizeTags(opts.Tags)
	if err != nil {
		return db.Movie{}, false, err
	}
	if m, err := s.st.Read().GetMovieByTmdbID(ctx, int64(tmdbID)); err == nil {
		return m, false, nil
	} else if err = store.MapError(err); !errors.Is(err, store.ErrNotFound) {
		return db.Movie{}, false, fmt.Errorf("library: look up TMDB id %d: %w", tmdbID, err)
	}
	if err := s.checkRefs(ctx, opts.RootFolderID, opts.QualityProfileID); err != nil {
		return db.Movie{}, false, err
	}
	p, err := s.Provider()
	if err != nil {
		return db.Movie{}, false, err
	}
	meta, err := p.Movie(ctx, tmdbID)
	if errors.Is(err, metadata.ErrNotFound) {
		return db.Movie{}, false, notFound("TMDB has no movie with id %d", tmdbID)
	}
	if err != nil {
		return db.Movie{}, false, fmt.Errorf("library: fetch TMDB movie %d: %w", tmdbID, err)
	}
	f, err := s.fieldsOf(meta)
	if err != nil {
		return db.Movie{}, false, fmt.Errorf("library: %w", err)
	}
	folder := FolderName(s.folderTemplate(ctx), FolderVars{
		Title: f.Title, OriginalTitle: f.OriginalTitle, Year: int(f.Year), TmdbID: int64(tmdbID), ImdbID: meta.IMDbID,
	})
	now := store.Millis(s.now())

	err = s.st.WithTx(ctx, func(q *db.Queries) error {
		// Re-check inside the write transaction: a concurrent add may have won.
		if m, err := q.GetMovieByTmdbID(ctx, int64(tmdbID)); err == nil {
			movie = m
			return nil
		} else if !errors.Is(store.MapError(err), store.ErrNotFound) {
			return err
		}
		collectionID, err := upsertCollection(ctx, q, meta.Collection, now)
		if err != nil {
			return err
		}
		params := db.InsertMovieParams{
			TmdbID: int64(tmdbID), ImdbID: f.ImdbID, Title: f.Title, OriginalTitle: f.OriginalTitle,
			SortTitle: f.SortTitle, CleanTitle: f.CleanTitle, OriginalLanguage: f.OriginalLanguage,
			Year: f.Year, Overview: f.Overview, RuntimeMin: f.RuntimeMin, Status: f.Status,
			ReleaseCinema: f.ReleaseCinema, ReleaseDigital: f.ReleaseDigital, ReleasePhysical: f.ReleasePhysical,
			Certification: f.Certification, Genres: f.Genres, Ratings: f.Ratings,
			PosterPath: f.PosterPath, BackdropPath: f.BackdropPath, Extra: f.Extra,
			CollectionID: collectionID, Monitored: opts.Monitored, QualityProfileID: opts.QualityProfileID,
			MinimumAvailability: opts.MinimumAvailability, RootFolderID: opts.RootFolderID,
			Path: folder, AddedAt: now, MetadataRefreshedAt: &now,
		}
		movie, err = q.InsertMovie(ctx, params)
		if errors.Is(store.MapError(err), store.ErrConflict) {
			// (root_folder_id, path) taken by another movie (same title and year).
			params.Path = CollisionName(folder, int64(tmdbID))
			movie, err = q.InsertMovie(ctx, params)
		}
		if err != nil {
			return fmt.Errorf("insert movie: %w", err)
		}
		created = true
		if err := insertAlternateTitles(ctx, q, movie.ID, &f, meta.AlternateTitles); err != nil {
			return err
		}
		return setTags(ctx, q, movie.ID, tags)
	})
	if err != nil {
		return db.Movie{}, false, fmt.Errorf("library: add movie %d: %w", tmdbID, err)
	}
	if !created {
		return movie, false, nil
	}

	if err := s.refreshMatcherEntry(ctx, movie.ID); err != nil {
		s.log.Error("matcher update after add", slog.Int64("movie_id", movie.ID), slog.Any("error", err))
	}
	s.log.Info("movie added", slog.Int64("movie_id", movie.ID), slog.Int64("tmdb_id", movie.TmdbID),
		slog.String("title", movie.Title), slog.String("path", movie.Path), slog.String("status", movie.Status))
	ev := movieEvent(&movie)
	ev.Path = movie.Path
	s.publish(events.Event{Type: events.MovieAdded, MovieID: movie.ID, Data: ev, History: &events.HistoryRecord{
		Event: "movie_added", SourceTitle: movie.Title,
		Data: map[string]any{
			"tmdbId": movie.TmdbID, "year": movie.Year, "path": movie.Path, "monitored": movie.Monitored,
			"qualityProfileId": movie.QualityProfileID, "minimumAvailability": movie.MinimumAvailability,
			"searchOnAdd": opts.SearchOnAdd && opts.Monitored,
		},
	}})
	s.enqueue(ctx, JobMovieImages, MoviePayload{MovieID: movie.ID}, jobs.EnqueueOptions{
		DedupeKey: fmt.Sprintf("%s:%d", JobMovieImages, movie.ID), Priority: 5,
	})
	if opts.SearchOnAdd && opts.Monitored {
		s.enqueue(ctx, JobMovieSearch, MoviePayload{MovieID: movie.ID}, jobs.EnqueueOptions{
			DedupeKey: fmt.Sprintf("%s:%d", JobMovieSearch, movie.ID),
		})
	}
	return movie, true, nil
}

// checkRefs verifies the root folder and quality profile exist.
func (s *Service) checkRefs(ctx context.Context, rootFolderID, profileID int64) error {
	if _, err := s.st.Read().GetRootFolder(ctx, rootFolderID); err != nil {
		if errors.Is(store.MapError(err), store.ErrNotFound) {
			return invalid("rootFolderId", "root folder %d does not exist", rootFolderID)
		}
		return fmt.Errorf("library: root folder %d: %w", rootFolderID, err)
	}
	if _, err := s.st.Read().GetQualityProfile(ctx, profileID); err != nil {
		if errors.Is(store.MapError(err), store.ErrNotFound) {
			return invalid("qualityProfileId", "quality profile %d does not exist", profileID)
		}
		return fmt.Errorf("library: quality profile %d: %w", profileID, err)
	}
	return nil
}

// MovieDetails is a movie with its derived state, tags, alternate titles,
// current file, collection, settings references and recent history: everything
// the movie page shows, read in one snapshot without network access.
type MovieDetails struct {
	Movie           db.Movie
	State           string
	Tags            []string
	AlternateTitles []db.AlternateTitle

	File             *db.MovieFile // nil without a file
	Collection       *db.Collection
	CollectionMovies []db.ListCollectionMovieStatesRow // library movies of the collection
	ProfileName      string
	RootFolderPath   string
	History          []db.History // newest first, at most RecentHistory entries
	// LastDownloadFailed: the newest download failed after the current file
	// (if any) was imported, and none is active.
	LastDownloadFailed bool
}

// RecentHistory is the number of history entries GetMovie returns.
const RecentHistory = 20

// GetMovie returns a movie and its details.
func (s *Service) GetMovie(ctx context.Context, id int64) (MovieDetails, error) {
	var d MovieDetails
	err := s.st.ReadTx(ctx, func(q *db.Queries) error {
		var err error
		if d.Movie, err = q.GetMovie(ctx, id); err != nil {
			if errors.Is(store.MapError(err), store.ErrNotFound) {
				return notFound("movie %d is not in the library", id)
			}
			return err
		}
		st, err := q.GetMovieState(ctx, id)
		if err != nil {
			return fmt.Errorf("state: %w", err)
		}
		d.State = st.State
		tags, err := q.ListMovieTags(ctx, id)
		if err != nil {
			return fmt.Errorf("tags: %w", err)
		}
		d.Tags = make([]string, 0, len(tags))
		for _, t := range tags {
			d.Tags = append(d.Tags, t.Label)
		}
		if d.AlternateTitles, err = q.ListAlternateTitles(ctx, id); err != nil {
			return fmt.Errorf("alternate titles: %w", err)
		}
		return d.loadPageData(ctx, q)
	})
	if err != nil {
		return MovieDetails{}, fmt.Errorf("library: get movie %d: %w", id, err)
	}
	return d, nil
}

// loadPageData reads the file, collection, settings references and recent
// history of d.Movie.
func (d *MovieDetails) loadPageData(ctx context.Context, q *db.Queries) error {
	m := &d.Movie
	f, err := q.GetCurrentMovieFile(ctx, m.ID)
	switch err = store.MapError(err); {
	case err == nil:
		d.File = &f
	case !errors.Is(err, store.ErrNotFound):
		return fmt.Errorf("file: %w", err)
	}
	if m.CollectionID != nil {
		c, err := q.GetCollection(ctx, *m.CollectionID)
		if err != nil {
			return fmt.Errorf("collection: %w", err)
		}
		d.Collection = &c
		if d.CollectionMovies, err = q.ListCollectionMovieStates(ctx, c.ID); err != nil {
			return fmt.Errorf("collection movies: %w", err)
		}
	}
	p, err := q.GetQualityProfile(ctx, m.QualityProfileID)
	if err != nil {
		return fmt.Errorf("quality profile: %w", err)
	}
	d.ProfileName = p.Name
	rf, err := q.GetRootFolder(ctx, m.RootFolderID)
	if err != nil {
		return fmt.Errorf("root folder: %w", err)
	}
	d.RootFolderPath = rf.Path
	d.History, err = q.ListMovieHistory(ctx, db.ListMovieHistoryParams{MovieID: m.ID, BeforeID: math.MaxInt64, Limit: RecentHistory})
	if err != nil {
		return fmt.Errorf("history: %w", err)
	}
	if d.State != StateDownloading {
		dls, err := q.ListMovieDownloads(ctx, m.ID) // newest first
		if err != nil {
			return fmt.Errorf("downloads: %w", err)
		}
		var importedAt int64
		if d.File != nil {
			importedAt = d.File.ImportedAt
		}
		d.LastDownloadFailed = len(dls) > 0 && dls[0].Status == "failed" && dls[0].GrabbedAt > importedAt
	}
	return nil
}

// UpdateOptions changes movie settings; nil fields are left unchanged.
type UpdateOptions struct {
	Monitored           *bool
	QualityProfileID    *int64
	MinimumAvailability *string
	RootFolderID        *int64
	Tags                *[]string
}

// UpdateMovie changes the user settings of a movie. Changing the root folder
// only changes where Gonema looks for the movie: files are not moved.
func (s *Service) UpdateMovie(ctx context.Context, id int64, u UpdateOptions) (db.Movie, error) {
	var tags []string
	if u.Tags != nil {
		var err error
		if tags, err = normalizeTags(*u.Tags); err != nil {
			return db.Movie{}, err
		}
	}
	if u.MinimumAvailability != nil && !validAvailability(*u.MinimumAvailability) {
		return db.Movie{}, invalid("minimumAvailability", "minimum availability must be announced, in_cinemas or released")
	}
	cur, err := s.st.Read().GetMovie(ctx, id)
	if err != nil {
		if errors.Is(store.MapError(err), store.ErrNotFound) {
			return db.Movie{}, notFound("movie %d is not in the library", id)
		}
		return db.Movie{}, fmt.Errorf("library: get movie %d: %w", id, err)
	}
	p := db.UpdateMovieSettingsParams{
		ID: id, Monitored: cur.Monitored, QualityProfileID: cur.QualityProfileID,
		MinimumAvailability: cur.MinimumAvailability, RootFolderID: cur.RootFolderID, Path: cur.Path,
	}
	var changes []string
	if u.Monitored != nil && *u.Monitored != cur.Monitored {
		p.Monitored = *u.Monitored
		changes = append(changes, "monitored")
	}
	if u.QualityProfileID != nil && *u.QualityProfileID != cur.QualityProfileID {
		p.QualityProfileID = *u.QualityProfileID
		changes = append(changes, "qualityProfileId")
	}
	if u.MinimumAvailability != nil && *u.MinimumAvailability != cur.MinimumAvailability {
		p.MinimumAvailability = *u.MinimumAvailability
		changes = append(changes, "minimumAvailability")
	}
	if u.RootFolderID != nil && *u.RootFolderID != cur.RootFolderID {
		p.RootFolderID = *u.RootFolderID
		changes = append(changes, "rootFolderId")
	}
	if p.QualityProfileID != cur.QualityProfileID || p.RootFolderID != cur.RootFolderID {
		if err := s.checkRefs(ctx, p.RootFolderID, p.QualityProfileID); err != nil {
			return db.Movie{}, err
		}
	}
	var out db.Movie
	err = s.st.WithTx(ctx, func(q *db.Queries) error {
		var err error
		if out, err = q.UpdateMovieSettings(ctx, p); err != nil {
			if errors.Is(store.MapError(err), store.ErrConflict) {
				return &InputError{Field: "rootFolderId", Msg: fmt.Sprintf("another movie already uses the folder %q in that root folder", p.Path)}
			}
			return err
		}
		if u.Tags != nil {
			changes = append(changes, "tags")
			return setTags(ctx, q, id, tags)
		}
		return nil
	})
	if err != nil {
		return db.Movie{}, fmt.Errorf("library: update movie %d: %w", id, err)
	}
	if len(changes) > 0 {
		ev := movieEvent(&out)
		ev.Changes = changes
		s.publish(events.Event{Type: events.MovieUpdated, MovieID: id, Data: ev})
	}
	return out, nil
}

// DeleteOptions configures DeleteMovie.
type DeleteOptions struct {
	// AddExclusion keeps import lists from adding the movie again.
	AddExclusion bool
}

// DeleteMovie removes a movie from the library database (with its alternate
// titles, tags and file records). The movie folder and its files are NOT
// touched: deleting user files is never done here. History rows are kept.
func (s *Service) DeleteMovie(ctx context.Context, id int64, opts DeleteOptions) (db.Movie, error) {
	var m db.Movie
	err := s.st.WithTx(ctx, func(q *db.Queries) error {
		var err error
		if m, err = q.GetMovie(ctx, id); err != nil {
			if errors.Is(store.MapError(err), store.ErrNotFound) {
				return notFound("movie %d is not in the library", id)
			}
			return err
		}
		if opts.AddExclusion {
			if err := q.AddImportListExclusion(ctx, db.AddImportListExclusionParams{
				TmdbID: m.TmdbID, Title: m.Title, Year: m.Year, CreatedAt: store.Millis(s.now()),
			}); err != nil {
				return fmt.Errorf("add exclusion: %w", err)
			}
		}
		return store.RequireRow(q.DeleteMovie(ctx, id))
	})
	if err != nil {
		return db.Movie{}, fmt.Errorf("library: delete movie %d: %w", id, err)
	}
	s.matcher.remove(id)
	s.log.Info("movie removed from the library; its files were kept", slog.Int64("movie_id", id),
		slog.String("title", m.Title), slog.String("path", m.Path))
	ev := movieEvent(&m)
	ev.Path = m.Path
	ev.Note = "removed from the library; the folder and its files were kept on disk"
	s.publish(events.Event{Type: events.MovieDeleted, MovieID: id, Data: ev, History: &events.HistoryRecord{
		Event: "movie_deleted", SourceTitle: m.Title,
		Data: map[string]any{
			"tmdbId": m.TmdbID, "year": m.Year, "path": m.Path, "addExclusion": opts.AddExclusion,
			"reason": ev.Note,
		},
	}})
	return m, nil
}

// RefreshMovie fetches the metadata of a movie again and records what changed.
// A movie TMDB no longer knows is marked deleted (its other data is kept).
func (s *Service) RefreshMovie(ctx context.Context, id int64) (changes []string, err error) {
	p, err := s.Provider()
	if err != nil {
		return nil, err
	}
	cur, err := s.st.Read().GetMovie(ctx, id)
	if err != nil {
		if errors.Is(store.MapError(err), store.ErrNotFound) {
			return nil, notFound("movie %d is not in the library", id)
		}
		return nil, fmt.Errorf("library: get movie %d: %w", id, err)
	}
	meta, err := p.Movie(ctx, int(cur.TmdbID))
	if errors.Is(err, metadata.ErrNotFound) {
		return s.markDeleted(ctx, &cur)
	}
	if err != nil {
		return nil, fmt.Errorf("library: refresh movie %d: %w", id, err)
	}
	f, err := s.fieldsOf(meta)
	if err != nil {
		return nil, fmt.Errorf("library: refresh movie %d: %w", id, err)
	}
	now := store.Millis(s.now())
	var out db.Movie
	err = s.st.WithTx(ctx, func(q *db.Queries) error {
		cur, err := q.GetMovie(ctx, id) // fresh copy inside the transaction
		if err != nil {
			return err
		}
		collectionID, err := upsertCollection(ctx, q, meta.Collection, now)
		if err != nil {
			return err
		}
		changes = diffMovie(&cur, &f, collectionID)
		color := cur.DominantColor
		if !eqPtr(cur.PosterPath, f.PosterPath) {
			color = nil // recomputed by the image job
		}
		out, err = q.UpdateMovieMetadata(ctx, db.UpdateMovieMetadataParams{
			ID: id, ImdbID: f.ImdbID, Title: f.Title, OriginalTitle: f.OriginalTitle,
			SortTitle: f.SortTitle, CleanTitle: f.CleanTitle, OriginalLanguage: f.OriginalLanguage,
			Year: f.Year, Overview: f.Overview, RuntimeMin: f.RuntimeMin, Status: f.Status,
			ReleaseCinema: f.ReleaseCinema, ReleaseDigital: f.ReleaseDigital, ReleasePhysical: f.ReleasePhysical,
			Certification: f.Certification, Genres: f.Genres, Ratings: f.Ratings,
			PosterPath: f.PosterPath, BackdropPath: f.BackdropPath, DominantColor: color, Extra: f.Extra,
			CollectionID: collectionID, MetadataRefreshedAt: now,
		})
		if err != nil {
			return fmt.Errorf("update metadata: %w", err)
		}
		return insertAlternateTitles(ctx, q, id, &f, meta.AlternateTitles)
	})
	if err != nil {
		return nil, fmt.Errorf("library: refresh movie %d: %w", id, err)
	}
	if err := s.refreshMatcherEntry(ctx, id); err != nil {
		s.log.Error("matcher update after refresh", slog.Int64("movie_id", id), slog.Any("error", err))
	}
	if slices.Contains(changes, "poster") || slices.Contains(changes, "backdrop") || out.DominantColor == nil {
		s.enqueue(ctx, JobMovieImages, MoviePayload{MovieID: id}, jobs.EnqueueOptions{
			DedupeKey: fmt.Sprintf("%s:%d", JobMovieImages, id),
		})
	}
	if len(changes) > 0 {
		s.publishChanged(&out, changes, f.StatusReason)
	}
	return changes, nil
}

func (s *Service) markDeleted(ctx context.Context, cur *db.Movie) ([]string, error) {
	if cur.Status == string(metadata.StatusDeleted) {
		return nil, nil
	}
	p := paramsFromMovie(cur)
	p.Status = string(metadata.StatusDeleted)
	p.MetadataRefreshedAt = store.Millis(s.now())
	out, err := s.st.Write().UpdateMovieMetadata(ctx, p)
	if err != nil {
		return nil, store.MapError(fmt.Errorf("library: mark movie %d deleted: %w", cur.ID, err))
	}
	changes := []string{"status"}
	s.publishChanged(&out, changes, "TMDB no longer knows this movie (removed or merged); it stays in the library")
	return changes, nil
}

func (s *Service) publishChanged(m *db.Movie, changes []string, reason string) {
	s.log.Info("movie metadata changed", slog.Int64("movie_id", m.ID), slog.Any("changes", changes))
	ev := movieEvent(m)
	ev.Changes = changes
	s.publish(events.Event{
		Type: events.MovieUpdated, MovieID: m.ID, Data: ev,
		History: &events.HistoryRecord{
			Event: "metadata_changed", SourceTitle: m.Title,
			Data: map[string]any{"changes": changes, "status": m.Status, "reason": reason},
		},
	})
}

// paramsFromMovie copies the metadata columns of m into update parameters.
func paramsFromMovie(m *db.Movie) db.UpdateMovieMetadataParams {
	refreshed := m.AddedAt
	if m.MetadataRefreshedAt != nil {
		refreshed = *m.MetadataRefreshedAt
	}
	return db.UpdateMovieMetadataParams{
		ID: m.ID, ImdbID: m.ImdbID, Title: m.Title, OriginalTitle: m.OriginalTitle,
		SortTitle: m.SortTitle, CleanTitle: m.CleanTitle, OriginalLanguage: m.OriginalLanguage,
		Year: m.Year, Overview: m.Overview, RuntimeMin: m.RuntimeMin, Status: m.Status,
		ReleaseCinema: m.ReleaseCinema, ReleaseDigital: m.ReleaseDigital, ReleasePhysical: m.ReleasePhysical,
		Certification: m.Certification, Genres: m.Genres, Ratings: m.Ratings,
		PosterPath: m.PosterPath, BackdropPath: m.BackdropPath, DominantColor: m.DominantColor,
		Extra: m.Extra, CollectionID: m.CollectionID, MetadataRefreshedAt: refreshed,
	}
}

func eqPtr[T comparable](a, b *T) bool {
	if a == nil || b == nil {
		return a == b
	}
	return *a == *b
}

// diffMovie lists the user-visible fields that differ (ratings and extra are
// not reported: they change all the time and are display only).
func diffMovie(cur *db.Movie, f *metaFields, collectionID *int64) []string {
	var c []string
	add := func(changed bool, name string) {
		if changed {
			c = append(c, name)
		}
	}
	add(cur.Title != f.Title, "title")
	add(cur.OriginalTitle != f.OriginalTitle, "originalTitle")
	add(cur.Year != f.Year, "year")
	add(!eqPtr(cur.ImdbID, f.ImdbID), "imdbId")
	add(cur.Status != f.Status, "status")
	add(!eqPtr(cur.ReleaseCinema, f.ReleaseCinema), "releaseCinema")
	add(!eqPtr(cur.ReleaseDigital, f.ReleaseDigital), "releaseDigital")
	add(!eqPtr(cur.ReleasePhysical, f.ReleasePhysical), "releasePhysical")
	add(cur.RuntimeMin != f.RuntimeMin, "runtime")
	add(cur.Certification != f.Certification, "certification")
	add(cur.Overview != f.Overview, "overview")
	add(cur.Genres != f.Genres, "genres")
	add(!eqPtr(cur.PosterPath, f.PosterPath), "poster")
	add(!eqPtr(cur.BackdropPath, f.BackdropPath), "backdrop")
	add(!eqPtr(cur.CollectionID, collectionID), "collection")
	return c
}
