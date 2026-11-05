package api

import (
	"context"
	"errors"
	"fmt"
	"math"
	"net/http"
	"runtime"
	"time"

	"github.com/danielgtaylor/huma/v2"

	"github.com/fs0ciety7000/gonema/internal/jobs"
	"github.com/fs0ciety7000/gonema/internal/library"
	"github.com/fs0ciety7000/gonema/internal/metadata"
	"github.com/fs0ciety7000/gonema/internal/store"
	"github.com/fs0ciety7000/gonema/internal/store/db"
)

// IDParam is the {id} path parameter. Input types embedded in Huma inputs are
// exported: Huma ignores embedded fields of unexported types.
type IDParam struct {
	ID int64 `path:"id" minimum:"1" doc:"Identifier"`
}

// PageParams selects a keyset page of a history list.
type PageParams struct {
	Before int64 `query:"before" minimum:"0" doc:"Return entries with an id lower than this (keyset pagination); 0 = from the newest"`
	Limit  int64 `query:"limit" minimum:"1" maximum:"500" default:"50"`
}

func (p PageParams) beforeID() int64 {
	if p.Before <= 0 {
		return math.MaxInt64
	}
	return p.Before
}

func (s *Server) routes() {
	s.systemRoutes()
	s.movieRoutes()
	s.libraryRoutes()
	s.registerEvents()
}

// ----------------------------------------------------------------- system

// SystemStatus describes the running server.
type SystemStatus struct {
	Version        string    `json:"version"`
	StartedAt      time.Time `json:"startedAt"`
	UptimeSeconds  int64     `json:"uptimeSeconds"`
	SchemaVersion  int64     `json:"schemaVersion" doc:"Database migration version"`
	DataDir        string    `json:"dataDir"`
	BaseURL        string    `json:"baseUrl"`
	GoVersion      string    `json:"goVersion"`
	OS             string    `json:"os"`
	Arch           string    `json:"arch"`
	TMDBConfigured bool      `json:"tmdbConfigured"`
	Movies         int       `json:"movies" doc:"Movies known to the release matcher"`
}

type jobsOutput struct {
	Body []Job
}

type historyOutput struct {
	Body []HistoryEntry
}

func (s *Server) systemRoutes() {
	huma.Register(s.api, huma.Operation{
		OperationID: "get-system-status", Method: http.MethodGet, Path: "/system/status",
		Summary: "Server status", Tags: []string{"System"},
	}, func(ctx context.Context, _ *struct{}) (*struct{ Body SystemStatus }, error) {
		v, err := s.d.Library.SchemaVersion(ctx)
		if err != nil {
			return nil, s.problem(ctx, err, false)
		}
		return &struct{ Body SystemStatus }{SystemStatus{
			Version: s.d.Version, StartedAt: s.d.StartedAt.UTC(), UptimeSeconds: int64(time.Since(s.d.StartedAt).Seconds()),
			SchemaVersion: v, DataDir: s.d.DataDir, BaseURL: s.d.BaseURL, GoVersion: runtime.Version(),
			OS: runtime.GOOS, Arch: runtime.GOARCH, TMDBConfigured: s.d.Library.TMDBConfigured(),
			Movies: s.d.Library.MatcherSize(),
		}}, nil
	})

	huma.Register(s.api, huma.Operation{
		OperationID: "list-jobs", Method: http.MethodGet, Path: "/jobs",
		Summary: "Recent background jobs", Description: "Newest first.", Tags: []string{"System"},
	}, func(ctx context.Context, in *struct {
		Limit int64 `query:"limit" minimum:"1" maximum:"500" default:"50"`
	}) (*jobsOutput, error) {
		rows, err := s.d.Jobs.ListRecent(ctx, in.Limit)
		if err != nil {
			return nil, s.problem(ctx, err, false)
		}
		out := &jobsOutput{Body: make([]Job, 0, len(rows))}
		for i := range rows {
			out.Body = append(out.Body, jobOf(&rows[i]))
		}
		return out, nil
	})

	huma.Register(s.api, huma.Operation{
		OperationID: "list-history", Method: http.MethodGet, Path: "/history",
		Summary: "Recent history", Description: "Every automatic action and its reasons, newest first.", Tags: []string{"History"},
	}, func(ctx context.Context, in *PageParams) (*historyOutput, error) {
		rows, err := s.d.Store.Read().ListRecentHistory(ctx, db.ListRecentHistoryParams{BeforeID: in.beforeID(), Limit: in.Limit})
		if err != nil {
			return nil, s.problem(ctx, store.MapError(err), false)
		}
		return historyPage(rows), nil
	})
}

func historyPage(rows []db.History) *historyOutput {
	out := &historyOutput{Body: make([]HistoryEntry, 0, len(rows))}
	for i := range rows {
		out.Body = append(out.Body, historyOf(&rows[i]))
	}
	return out
}

// ----------------------------------------------------------------- movies

type movieListInput struct {
	Cursor       string `query:"cursor" maxLength:"512" doc:"nextCursor of the previous page"`
	Limit        int    `query:"limit" minimum:"1" maximum:"500" default:"100"`
	Sort         string `query:"sort" enum:"title,year,added" default:"title" doc:"title: A to Z; year: newest first; added: most recent first"`
	Monitored    string `query:"monitored" enum:"true,false" doc:"Only monitored (true) or unmonitored (false) movies"`
	Status       string `query:"status" enum:"announced,in_cinemas,released,deleted"`
	YearFrom     int    `query:"yearFrom" minimum:"0" maximum:"2200"`
	YearTo       int    `query:"yearTo" minimum:"0" maximum:"2200"`
	Genre        string `query:"genre" maxLength:"100"`
	RootFolderID int64  `query:"rootFolderId" minimum:"0"`
}

// MoviePage is a page of movies.
type MoviePage struct {
	Movies     []MovieListItem `json:"movies"`
	NextCursor string          `json:"nextCursor,omitempty" doc:"Pass as cursor to get the next page; absent on the last page"`
}

type addMovieInput struct {
	Body struct {
		TmdbID              int      `json:"tmdbId" minimum:"1"`
		RootFolderID        int64    `json:"rootFolderId" minimum:"1"`
		QualityProfileID    int64    `json:"qualityProfileId" minimum:"1"`
		MinimumAvailability string   `json:"minimumAvailability,omitempty" enum:"announced,in_cinemas,released" default:"released"`
		Monitored           *bool    `json:"monitored,omitempty" doc:"Default true"`
		Tags                []string `json:"tags,omitempty" maxItems:"20"`
		SearchOnAdd         bool     `json:"searchOnAdd,omitempty" doc:"Search for a release right after adding"`
	}
}

type movieOutput struct {
	Status   int
	Location string `header:"Location"`
	Body     Movie
}

type updateMovieInput struct {
	IDParam
	Body struct {
		Monitored           *bool     `json:"monitored,omitempty"`
		QualityProfileID    *int64    `json:"qualityProfileId,omitempty" minimum:"1"`
		MinimumAvailability *string   `json:"minimumAvailability,omitempty" enum:"announced,in_cinemas,released"`
		RootFolderID        *int64    `json:"rootFolderId,omitempty" minimum:"1" doc:"Files are not moved"`
		Tags                *[]string `json:"tags,omitempty" maxItems:"20"`
	}
}

type refreshOutput struct {
	Status int
	Body   struct {
		JobID   int64 `json:"jobId,omitempty"`
		Queued  bool  `json:"queued" doc:"false when a refresh of this movie is already pending"`
		MovieID int64 `json:"movieId"`
	}
}

func (s *Server) movieRoutes() {
	tags := []string{"Movies"}
	huma.Register(s.api, huma.Operation{
		OperationID: "list-movies", Method: http.MethodGet, Path: "/movies", Summary: "List movies", Tags: tags,
	}, func(ctx context.Context, in *movieListInput) (*struct{ Body MoviePage }, error) {
		q := library.ListQuery{Sort: in.Sort, Cursor: in.Cursor, Limit: in.Limit, Filter: library.ListFilter{
			Status: in.Status, YearFrom: in.YearFrom, YearTo: in.YearTo, Genre: in.Genre, RootFolderID: in.RootFolderID,
		}}
		if in.Monitored != "" {
			v := in.Monitored == "true"
			q.Filter.Monitored = &v
		}
		page, err := s.d.Library.ListMovies(ctx, q)
		if err != nil {
			return nil, s.problem(ctx, err, false)
		}
		out := &struct{ Body MoviePage }{MoviePage{Movies: make([]MovieListItem, 0, len(page.Movies)), NextCursor: page.NextCursor}}
		for i := range page.Movies {
			out.Body.Movies = append(out.Body.Movies, movieListItemOf(&page.Movies[i]))
		}
		return out, nil
	})

	huma.Register(s.api, huma.Operation{
		OperationID: "get-movie", Method: http.MethodGet, Path: "/movies/{id}", Summary: "Get a movie", Tags: tags,
	}, func(ctx context.Context, in *IDParam) (*struct{ Body Movie }, error) {
		d, err := s.d.Library.GetMovie(ctx, in.ID)
		if err != nil {
			return nil, s.problem(ctx, err, false)
		}
		return &struct{ Body Movie }{movieDetailsOf(&d)}, nil
	})

	huma.Register(s.api, huma.Operation{
		OperationID: "add-movie", Method: http.MethodPost, Path: "/movies", Summary: "Add a movie from TMDB",
		Description: "Idempotent on tmdbId: when the movie is already in the library it is returned unchanged with status 200; a new movie is returned with 201.",
		Tags:        tags, DefaultStatus: http.StatusCreated,
	}, func(ctx context.Context, in *addMovieInput) (*movieOutput, error) {
		b := in.Body
		monitored := b.Monitored == nil || *b.Monitored
		m, created, err := s.d.Library.AddMovie(ctx, b.TmdbID, library.AddOptions{
			RootFolderID: b.RootFolderID, QualityProfileID: b.QualityProfileID, MinimumAvailability: b.MinimumAvailability,
			Monitored: monitored, Tags: b.Tags, SearchOnAdd: b.SearchOnAdd,
		})
		if err != nil {
			return nil, s.problem(ctx, err, true)
		}
		d, err := s.d.Library.GetMovie(ctx, m.ID)
		if err != nil {
			return nil, s.problem(ctx, err, false)
		}
		out := &movieOutput{Status: http.StatusOK, Body: movieDetailsOf(&d)}
		if created {
			out.Status = http.StatusCreated
			out.Location = fmt.Sprintf("%s%s/movies/%d", s.d.BaseURL, Prefix, m.ID)
		}
		return out, nil
	})

	huma.Register(s.api, huma.Operation{
		OperationID: "update-movie", Method: http.MethodPatch, Path: "/movies/{id}", Summary: "Change movie settings",
		Description: "Only the fields present are changed.", Tags: tags,
	}, func(ctx context.Context, in *updateMovieInput) (*struct{ Body Movie }, error) {
		b := in.Body
		if _, err := s.d.Library.UpdateMovie(ctx, in.ID, library.UpdateOptions{
			Monitored: b.Monitored, QualityProfileID: b.QualityProfileID, MinimumAvailability: b.MinimumAvailability,
			RootFolderID: b.RootFolderID, Tags: b.Tags,
		}); err != nil {
			return nil, s.problem(ctx, err, false)
		}
		d, err := s.d.Library.GetMovie(ctx, in.ID)
		if err != nil {
			return nil, s.problem(ctx, err, false)
		}
		return &struct{ Body Movie }{movieDetailsOf(&d)}, nil
	})

	huma.Register(s.api, huma.Operation{
		OperationID: "delete-movie", Method: http.MethodDelete, Path: "/movies/{id}", Summary: "Remove a movie from the library",
		Description: "Removes the movie from the database only. Its folder and files are never deleted.",
		Tags:        tags, DefaultStatus: http.StatusNoContent,
	}, func(ctx context.Context, in *struct {
		IDParam
		AddExclusion bool `query:"addExclusion" doc:"Keep import lists from adding it again"`
	}) (*struct{}, error) {
		if _, err := s.d.Library.DeleteMovie(ctx, in.ID, library.DeleteOptions{AddExclusion: in.AddExclusion}); err != nil {
			return nil, s.problem(ctx, err, false)
		}
		return nil, nil
	})

	huma.Register(s.api, huma.Operation{
		OperationID: "refresh-movie", Method: http.MethodPost, Path: "/movies/{id}/refresh",
		Summary: "Refresh movie metadata", Description: "Queues a metadata refresh from TMDB.", Tags: tags,
		DefaultStatus: http.StatusAccepted,
	}, func(ctx context.Context, in *IDParam) (*refreshOutput, error) {
		if _, err := s.d.Library.GetMovie(ctx, in.ID); err != nil {
			return nil, s.problem(ctx, err, false)
		}
		job, created, err := s.d.Jobs.Enqueue(ctx, library.JobMovieRefresh, library.MoviePayload{MovieID: in.ID},
			jobs.EnqueueOptions{DedupeKey: fmt.Sprintf("%s:%d", library.JobMovieRefresh, in.ID), Priority: 10, MaxAttempts: 3})
		if err != nil {
			return nil, s.problem(ctx, err, false)
		}
		out := &refreshOutput{Status: http.StatusAccepted}
		out.Body.JobID, out.Body.Queued, out.Body.MovieID = job.ID, created, in.ID
		return out, nil
	})

	huma.Register(s.api, huma.Operation{
		OperationID: "list-movie-history", Method: http.MethodGet, Path: "/movies/{id}/history",
		Summary: "History of a movie", Description: "Newest first; kept after the movie is removed.", Tags: []string{"History"},
	}, func(ctx context.Context, in *struct {
		IDParam
		PageParams
	}) (*historyOutput, error) {
		rows, err := s.d.Store.Read().ListMovieHistory(ctx, db.ListMovieHistoryParams{MovieID: in.ID, BeforeID: in.beforeID(), Limit: in.Limit})
		if err != nil {
			return nil, s.problem(ctx, store.MapError(err), false)
		}
		return historyPage(rows), nil
	})

	huma.Register(s.api, huma.Operation{
		OperationID: "lookup-movies", Method: http.MethodGet, Path: "/lookup", Summary: "Search TMDB",
		Description: "q is a title, tmdb:<id>, or an IMDb id (tt0137523). Results already in the library carry their movieId.",
		Tags:        tags,
	}, func(ctx context.Context, in *struct {
		Q    string `query:"q" required:"true" minLength:"1" maxLength:"200"`
		Year int    `query:"year" minimum:"0" maximum:"2200"`
	}) (*struct{ Body []LookupResult }, error) {
		res, err := s.d.Library.Lookup(ctx, in.Q, in.Year)
		if err != nil {
			return nil, s.problem(ctx, err, true)
		}
		out := &struct{ Body []LookupResult }{make([]LookupResult, 0, len(res))}
		for i := range res {
			r := &res[i]
			out.Body = append(out.Body, LookupResult{MovieSummary: r.MovieSummary, InLibrary: r.MovieID != 0, MovieID: r.MovieID})
		}
		return out, nil
	})
}

// ----------------------------------------------------------------- library

type monitorInput struct {
	IDParam
	Body struct {
		Monitored           bool   `json:"monitored"`
		QualityProfileID    int64  `json:"qualityProfileId,omitempty" minimum:"0" doc:"Required when monitored"`
		RootFolderID        int64  `json:"rootFolderId,omitempty" minimum:"0" doc:"Required when monitored"`
		MinimumAvailability string `json:"minimumAvailability,omitempty" enum:"announced,in_cinemas,released" default:"released"`
		SearchOnAdd         bool   `json:"searchOnAdd,omitempty"`
	}
}

// MonitorResult reports the movies added by monitoring a collection.
type MonitorResult struct {
	Collection Collection            `json:"collection"`
	Added      []Movie               `json:"added"`
	Skipped    []library.SkippedPart `json:"skipped" doc:"Movies not added, with the reason"`
}

// TMDBSetting is the state of the TMDB key. The key itself is never returned.
type TMDBSetting struct {
	Configured bool `json:"configured"`
}

func (s *Server) libraryRoutes() {
	huma.Register(s.api, huma.Operation{
		OperationID: "get-collection", Method: http.MethodGet, Path: "/collections/{id}", Summary: "Get a collection",
		Tags: []string{"Collections"},
	}, func(ctx context.Context, in *IDParam) (*struct{ Body Collection }, error) {
		d, err := s.d.Library.GetCollection(ctx, in.ID)
		if err != nil {
			return nil, s.problem(ctx, err, false)
		}
		c := collectionOf(&d.Collection)
		for i := range d.Movies {
			c.Movies = append(c.Movies, movieOf(&d.Movies[i]))
		}
		c.Parts, c.PartsError = d.Parts, d.PartsError
		return &struct{ Body Collection }{c}, nil
	})

	huma.Register(s.api, huma.Operation{
		OperationID: "monitor-collection", Method: http.MethodPut, Path: "/collections/{id}/monitor",
		Summary:     "Monitor a collection",
		Description: "Saves the settings; when monitored, adds every movie of the collection that is not in the library (excluded movies are skipped).",
		Tags:        []string{"Collections"},
	}, func(ctx context.Context, in *monitorInput) (*struct{ Body MonitorResult }, error) {
		b := in.Body
		res, err := s.d.Library.MonitorCollection(ctx, in.ID, library.MonitorOptions{
			Monitored: b.Monitored, QualityProfileID: b.QualityProfileID, RootFolderID: b.RootFolderID,
			MinimumAvailability: b.MinimumAvailability, SearchOnAdd: b.SearchOnAdd,
		})
		if err != nil {
			return nil, s.problem(ctx, err, true)
		}
		out := MonitorResult{Collection: collectionOf(&res.Collection), Added: []Movie{}, Skipped: res.Skipped}
		if out.Skipped == nil {
			out.Skipped = []library.SkippedPart{}
		}
		for i := range res.Added {
			out.Added = append(out.Added, movieOf(&res.Added[i]))
		}
		return &struct{ Body MonitorResult }{out}, nil
	})

	rfTags := []string{"Root folders"}
	huma.Register(s.api, huma.Operation{
		OperationID: "list-root-folders", Method: http.MethodGet, Path: "/root-folders",
		Summary: "List root folders", Description: "Free space is measured on each call.", Tags: rfTags,
	}, func(ctx context.Context, _ *struct{}) (*struct{ Body []RootFolder }, error) {
		list, err := s.d.Library.ListRootFolders(ctx)
		if err != nil {
			return nil, s.problem(ctx, err, false)
		}
		out := &struct{ Body []RootFolder }{make([]RootFolder, 0, len(list))}
		for i := range list {
			out.Body = append(out.Body, rootFolderOf(&list[i]))
		}
		return out, nil
	})

	huma.Register(s.api, huma.Operation{
		OperationID: "add-root-folder", Method: http.MethodPost, Path: "/root-folders", Summary: "Add a root folder",
		Description: "The folder must be absolute, exist and be writable by Gonema.", Tags: rfTags, DefaultStatus: http.StatusCreated,
	}, func(ctx context.Context, in *struct {
		Body struct {
			Path string `json:"path" minLength:"1" maxLength:"4096"`
		}
	}) (*struct{ Body RootFolder }, error) {
		rf, err := s.d.Library.AddRootFolder(ctx, in.Body.Path)
		if err != nil {
			return nil, s.problem(ctx, err, false)
		}
		return &struct{ Body RootFolder }{rootFolderOf(&rf)}, nil
	})

	huma.Register(s.api, huma.Operation{
		OperationID: "delete-root-folder", Method: http.MethodDelete, Path: "/root-folders/{id}",
		Summary: "Remove an unused root folder", Description: "Refused while movies use it. The folder on disk is not touched.",
		Tags: rfTags, DefaultStatus: http.StatusNoContent,
	}, func(ctx context.Context, in *IDParam) (*struct{}, error) {
		if err := s.d.Library.DeleteRootFolder(ctx, in.ID); err != nil {
			return nil, s.problem(ctx, err, false)
		}
		return nil, nil
	})

	huma.Register(s.api, huma.Operation{
		OperationID: "list-quality-profiles", Method: http.MethodGet, Path: "/quality-profiles",
		Summary: "List quality profiles", Tags: []string{"Quality"},
	}, func(ctx context.Context, _ *struct{}) (*struct{ Body []QualityProfile }, error) {
		list, err := s.d.Library.ListQualityProfiles(ctx)
		if err != nil {
			return nil, s.problem(ctx, err, false)
		}
		out := &struct{ Body []QualityProfile }{make([]QualityProfile, 0, len(list))}
		for i := range list {
			out.Body = append(out.Body, qualityProfileOf(&list[i]))
		}
		return out, nil
	})

	setTags := []string{"Settings"}
	huma.Register(s.api, huma.Operation{
		OperationID: "get-tmdb-setting", Method: http.MethodGet, Path: "/settings/tmdb",
		Summary: "Whether a TMDB key is configured", Description: "The key itself is never returned.", Tags: setTags,
	}, func(context.Context, *struct{}) (*struct{ Body TMDBSetting }, error) {
		return &struct{ Body TMDBSetting }{TMDBSetting{Configured: s.d.Library.TMDBConfigured()}}, nil
	})

	huma.Register(s.api, huma.Operation{
		OperationID: "set-tmdb-key", Method: http.MethodPut, Path: "/settings/tmdb", Summary: "Set the TMDB key",
		Description: "Checks the key with TMDB, then stores it encrypted. Accepts the API Read Access Token or the API Key.",
		Tags:        setTags,
	}, func(ctx context.Context, in *struct {
		Body struct {
			Key string `json:"key" doc:"TMDB API Read Access Token or API Key"`
		}
	}) (*struct{ Body TMDBSetting }, error) {
		err := s.d.Library.SetTMDBKey(ctx, in.Body.Key)
		switch {
		case err == nil:
		case errors.Is(err, metadata.ErrUnauthorized):
			return nil, huma.Error422UnprocessableEntity("TMDB rejected this key. Copy the API Read Access Token or the API Key from https://www.themoviedb.org/settings/api and try again.")
		default:
			return nil, s.problem(ctx, err, true)
		}
		return &struct{ Body TMDBSetting }{TMDBSetting{Configured: true}}, nil
	})
}
