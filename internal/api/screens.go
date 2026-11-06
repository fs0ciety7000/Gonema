package api

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"math"
	"net/http"
	"strconv"
	"time"

	"github.com/danielgtaylor/huma/v2"

	"github.com/fs0ciety7000/gonema/internal/imagecache"
	"github.com/fs0ciety7000/gonema/internal/importer"
	"github.com/fs0ciety7000/gonema/internal/library"
	"github.com/fs0ciety7000/gonema/internal/metadata"
	"github.com/fs0ciety7000/gonema/internal/store"
	"github.com/fs0ciety7000/gonema/internal/store/db"
)

// Endpoints of the web UI screens of phase 7 lot 2: tags, bulk edits (with
// the values to undo them), history, discover lists and the Wanted lists.

// ----------------------------------------------------------------- tags

// Tag is a tag with its usage.
type Tag struct {
	ID     int64  `json:"id"`
	Label  string `json:"label"`
	Movies int64  `json:"movies" doc:"Movies carrying the tag"`
}

func tagOf(t *library.TagUsage) Tag { return Tag{ID: t.ID, Label: t.Label, Movies: t.Movies} }

type tagLabelBody struct {
	Label string `json:"label" minLength:"1" maxLength:"64"`
}

// DeletedTag is a deleted tag with the movies that carried it: to undo,
// create the tag again and add it to those movies (POST /movies/bulk).
type DeletedTag struct {
	Tag      Tag     `json:"tag"`
	MovieIDs []int64 `json:"movieIds"`
}

func (s *Server) tagRoutes() {
	tags := []string{"Tags"}
	huma.Register(s.api, huma.Operation{
		OperationID: "list-tags", Method: http.MethodGet, Path: "/tags", Summary: "List tags",
		Description: "By label, with the number of movies carrying each.", Tags: tags,
	}, func(ctx context.Context, _ *struct{}) (*struct{ Body []Tag }, error) {
		list, err := s.d.Library.ListTags(ctx)
		if err != nil {
			return nil, s.problem(ctx, err, false)
		}
		out := &struct{ Body []Tag }{make([]Tag, 0, len(list))}
		for i := range list {
			out.Body = append(out.Body, tagOf(&list[i]))
		}
		return out, nil
	})
	huma.Register(s.api, huma.Operation{
		OperationID: "create-tag", Method: http.MethodPost, Path: "/tags", Summary: "Create a tag",
		Description: "Labels are unique (409 otherwise).", Tags: tags, DefaultStatus: http.StatusCreated,
	}, func(ctx context.Context, in *struct{ Body tagLabelBody }) (*struct{ Body Tag }, error) {
		t, err := s.d.Library.CreateTag(ctx, in.Body.Label)
		if err != nil {
			return nil, s.problem(ctx, err, false)
		}
		return &struct{ Body Tag }{tagOf(&t)}, nil
	})
	huma.Register(s.api, huma.Operation{
		OperationID: "rename-tag", Method: http.MethodPut, Path: "/tags/{id}", Summary: "Rename a tag",
		Description: "The movies keep the tag. Labels are unique (409 otherwise).", Tags: tags,
	}, func(ctx context.Context, in *struct {
		IDParam
		Body tagLabelBody
	}) (*struct{ Body Tag }, error) {
		t, err := s.d.Library.RenameTag(ctx, in.ID, in.Body.Label)
		if err != nil {
			return nil, s.problem(ctx, err, false)
		}
		return &struct{ Body Tag }{tagOf(&t)}, nil
	})
	huma.Register(s.api, huma.Operation{
		OperationID: "delete-tag", Method: http.MethodDelete, Path: "/tags/{id}", Summary: "Delete a tag",
		Description: "Removes it from every movie. The answer lists those movies, for Undo.", Tags: tags,
	}, func(ctx context.Context, in *IDParam) (*struct{ Body DeletedTag }, error) {
		t, movies, err := s.d.Library.DeleteTag(ctx, in.ID)
		if err != nil {
			return nil, s.problem(ctx, err, false)
		}
		if movies == nil {
			movies = []int64{}
		}
		return &struct{ Body DeletedTag }{DeletedTag{Tag: tagOf(&t), MovieIDs: movies}}, nil
	})
}

// ----------------------------------------------------------------- bulk edits

// BulkSet are the settings to change; absent fields are left unchanged.
type BulkSet struct {
	Monitored           *bool    `json:"monitored,omitempty"`
	QualityProfileID    *int64   `json:"qualityProfileId,omitempty" minimum:"1"`
	MinimumAvailability *string  `json:"minimumAvailability,omitempty" enum:"announced,in_cinemas,released"`
	RootFolderID        *int64   `json:"rootFolderId,omitempty" minimum:"1" doc:"Move to this root folder (see POST /movies/{id}/move)"`
	MoveFiles           bool     `json:"moveFiles,omitempty" doc:"With rootFolderId: move the files too, into folders named with the template"`
	AddTags             []string `json:"addTags,omitempty" maxItems:"20" doc:"Labels; missing tags are created"`
	RemoveTags          []string `json:"removeTags,omitempty" maxItems:"20"`
}

// BulkResult is the outcome of a bulk edit.
type BulkResult struct {
	Changed  []int64                 `json:"changed" doc:"Movies whose settings changed"`
	Previous []library.MovieSettings `json:"previous" doc:"Settings before the edit, one per movie: POST them to /movies/bulk/restore to undo (root folders: move back with /movies/move)"`
	Moves    []MoveOutcome           `json:"moves" doc:"One per movie when rootFolderId was set"`
}

// MoveOutcome is the move of one movie of a bulk request.
type MoveOutcome struct {
	MovieID    int64                `json:"movieId"`
	OK         bool                 `json:"ok"`
	Error      string               `json:"error,omitempty" doc:"Why the movie was not moved"`
	Relocation *importer.Relocation `json:"relocation,omitempty"`
}

// maxBulkMoves bounds the movies one request moves (each moves files).
const maxBulkMoves = 1000

type bulkInput struct {
	Body struct {
		IDs []int64 `json:"ids" minItems:"1" maxItems:"10000"`
		Set BulkSet `json:"set"`
	}
}

type movieTagsInput struct {
	IDParam
	Body struct {
		Add    []string `json:"add,omitempty" maxItems:"20" doc:"Labels; missing tags are created"`
		Remove []string `json:"remove,omitempty" maxItems:"20"`
	}
}

// RestoreResult is the outcome of an Undo.
type RestoreResult struct {
	Changed []int64 `json:"changed"`
}

func (s *Server) bulkRoutes() {
	tags := []string{"Movies"}
	huma.Register(s.api, huma.Operation{
		OperationID: "bulk-update-movies", Method: http.MethodPost, Path: "/movies/bulk", Summary: "Change several movies at once",
		Description: "Settings and tags change in one transaction (all movies or none). The previous settings come back " +
			"for Undo. With rootFolderId, each movie is then moved (one result per movie in moves).",
		Tags: tags,
	}, func(ctx context.Context, in *bulkInput) (*struct{ Body BulkResult }, error) {
		set := in.Body.Set
		if set.RootFolderID != nil && s.files == nil {
			return nil, huma.Error503ServiceUnavailable("Moving movies is not available on this server.")
		}
		if set.RootFolderID != nil && len(in.Body.IDs) > maxBulkMoves {
			return nil, huma.Error422UnprocessableEntity(fmt.Sprintf("At most %d movies can be moved at once; select fewer.", maxBulkMoves))
		}
		prev, changed, err := s.d.Library.BulkUpdate(ctx, in.Body.IDs, library.BulkSet{
			Monitored: set.Monitored, QualityProfileID: set.QualityProfileID, MinimumAvailability: set.MinimumAvailability,
			AddTags: set.AddTags, RemoveTags: set.RemoveTags,
		})
		if err != nil {
			return nil, s.problem(ctx, err, false)
		}
		out := BulkResult{Changed: orEmpty(changed), Previous: prev, Moves: []MoveOutcome{}}
		if set.RootFolderID != nil {
			for _, p := range prev {
				out.Moves = append(out.Moves, s.moveOne(ctx, p.ID, *set.RootFolderID, set.MoveFiles, false))
				if ctx.Err() != nil {
					break
				}
			}
		}
		return &struct{ Body BulkResult }{out}, nil
	})
	huma.Register(s.api, huma.Operation{
		OperationID: "restore-movie-settings", Method: http.MethodPost, Path: "/movies/bulk/restore", Summary: "Undo a bulk edit",
		Description: "Puts movies back to the given settings (monitored, quality profile, minimum availability, exact tags), " +
			"as returned in previous by a bulk edit. Root folders are not changed.",
		Tags: tags,
	}, func(ctx context.Context, in *struct {
		Body struct {
			Previous []library.MovieSettings `json:"previous" minItems:"1" maxItems:"10000"`
		}
	}) (*struct{ Body RestoreResult }, error) {
		changed, err := s.d.Library.RestoreSettings(ctx, in.Body.Previous)
		if err != nil {
			return nil, s.problem(ctx, err, false)
		}
		return &struct{ Body RestoreResult }{RestoreResult{Changed: orEmpty(changed)}}, nil
	})
	huma.Register(s.api, huma.Operation{
		OperationID: "update-movie-tags", Method: http.MethodPost, Path: "/movies/{id}/tags", Summary: "Add or remove tags of a movie",
		Description: "The previous settings come back for Undo (POST /movies/bulk/restore).", Tags: tags,
	}, func(ctx context.Context, in *movieTagsInput) (*struct{ Body BulkResult }, error) {
		prev, changed, err := s.d.Library.BulkUpdate(ctx, []int64{in.ID}, library.BulkSet{AddTags: in.Body.Add, RemoveTags: in.Body.Remove})
		if err != nil {
			return nil, s.problem(ctx, err, false)
		}
		return &struct{ Body BulkResult }{BulkResult{Changed: orEmpty(changed), Previous: prev, Moves: []MoveOutcome{}}}, nil
	})
}

func orEmpty[T any](s []T) []T {
	if s == nil {
		return []T{}
	}
	return s
}

// moveOne moves one movie of a bulk request; failures are reported, not
// returned.
func (s *Server) moveOne(ctx context.Context, id, rootFolderID int64, moveFiles, preview bool) MoveOutcome {
	out := MoveOutcome{MovieID: id}
	var rel importer.Relocation
	var err error
	if preview {
		var p importer.Plan
		p, err = s.files.PlanMove(ctx, id, rootFolderID, moveFiles)
		rel = importer.Relocation{Plan: p, Done: []importer.FileOp{}}
		if err == nil && len(p.Problems) > 0 {
			out.Relocation, out.Error = &rel, p.Problems[0]
			return out
		}
	} else {
		rel, err = s.files.Move(ctx, id, rootFolderID, moveFiles)
	}
	if err != nil {
		out.Error = errText(err)
		if out.Error == "" {
			s.log.Error("bulk move failed", slog.String("request_id", RequestID(ctx)), slog.Int64("movie_id", id), slog.Any("error", err))
			out.Error = "the move failed; the logs have the details (request id " + RequestID(ctx) + ")"
		}
		if rel.MovieID != 0 {
			out.Relocation = &rel
		}
		return out
	}
	out.OK, out.Relocation = true, &rel
	return out
}

// ----------------------------------------------------------------- history

// HistoryFilterParams are the filters of the history list and its export.
type HistoryFilterParams struct {
	Event      []string  `query:"event" maxItems:"17" enum:"grabbed,grab_failed,grab_overridden,download_failed,download_removed,imported,upgraded,import_failed,file_deleted,file_renamed,blocklisted,ignored,metadata_changed,movie_added,movie_deleted,retry_scheduled,retries_exhausted" doc:"Event kinds, comma-separated; all when absent"`
	MovieID    int64     `query:"movieId" minimum:"0" doc:"Only this movie (also after its removal)"`
	DownloadID int64     `query:"downloadId" minimum:"0" doc:"Only the entries of this download"`
	Since      time.Time `query:"since" doc:"Entries at or after this time (RFC 3339)"`
	Until      time.Time `query:"until" doc:"Entries before this time (RFC 3339)"`
}

// params returns the query parameters of the filters, newest first from
// beforeID; errors are API errors.
func (f *HistoryFilterParams) params(beforeID, limit int64) (db.ListHistoryFilteredParams, error) {
	p := db.ListHistoryFilteredParams{BeforeID: beforeID, Limit: limit}
	if len(f.Event) > 0 {
		raw, err := json.Marshal(f.Event)
		if err != nil {
			return p, huma.Error422UnprocessableEntity("The event filter is invalid.")
		}
		ev := string(raw)
		p.Events = &ev
	}
	if f.MovieID > 0 {
		p.MovieID = &f.MovieID
	}
	if f.DownloadID > 0 {
		p.DownloadID = &f.DownloadID
	}
	if !f.Since.IsZero() {
		v := store.Millis(f.Since)
		p.Since = &v
	}
	if !f.Until.IsZero() {
		v := store.Millis(f.Until)
		p.Until = &v
	}
	if p.Since != nil && p.Until != nil && *p.Since >= *p.Until {
		return p, huma.Error422UnprocessableEntity("The date range is empty: since must be before until.")
	}
	return p, nil
}

type historyListInput struct {
	HistoryFilterParams
	Cursor string `query:"cursor" maxLength:"20" doc:"nextCursor of the previous page"`
	Limit  int64  `query:"limit" minimum:"1" maximum:"500" default:"50"`
}

// HistoryMovie is the movie of a history row.
type HistoryMovie struct {
	ID            int64   `json:"id"`
	Title         string  `json:"title"`
	Year          int64   `json:"year"`
	PosterURL     *string `json:"posterUrl" doc:"w342"`
	DominantColor *string `json:"dominantColor"`
}

// HistoryRow is a row of the history screen.
type HistoryRow struct {
	HistoryEntry
	QualityName *string       `json:"qualityName"`
	Reason      string        `json:"reason" doc:"Plain-language reason (data.reason); empty when the event has none"`
	Movie       *HistoryMovie `json:"movie" doc:"null when the movie is no longer in the library (sourceTitle remains)"`
	RetryOf     *int64        `json:"retryOf" doc:"The failed download this entry's download replaced"`
	ChainID     *int64        `json:"chainId" doc:"First download of the retry chain: rows with one chainId are one acquisition (grabbed, failed, retried, imported, upgraded)"`
}

// HistoryPage is one page of history.
type HistoryPage struct {
	Entries    []HistoryRow `json:"entries"`
	NextCursor string       `json:"nextCursor,omitempty" doc:"Pass as cursor for older entries; absent on the last page"`
}

func (s *Server) historyRoutes() {
	huma.Register(s.api, huma.Operation{
		OperationID: "list-history", Method: http.MethodGet, Path: "/history", Summary: "History",
		Description: "Every automatic action and user file operation with its reasons, newest first, with filters. " +
			"Rows of one download chain (grab, failure, retry, import, upgrade) share chainId.",
		Tags: []string{"History"},
	}, func(ctx context.Context, in *historyListInput) (*struct{ Body HistoryPage }, error) {
		before := int64(math.MaxInt64)
		if in.Cursor != "" {
			id, err := strconv.ParseInt(in.Cursor, 10, 64)
			if err != nil || id <= 0 {
				return nil, huma.Error422UnprocessableEntity("The page cursor is invalid; start again from the first page.")
			}
			before = id
		}
		p, err := in.params(before, in.Limit+1)
		if err != nil {
			return nil, err
		}
		rows, err := s.d.Store.Read().ListHistoryFiltered(ctx, p)
		if err != nil {
			return nil, s.problem(ctx, store.MapError(err), false)
		}
		page := HistoryPage{Entries: make([]HistoryRow, 0, min(len(rows), int(in.Limit)))}
		if int64(len(rows)) > in.Limit {
			rows = rows[:in.Limit]
			page.NextCursor = strconv.FormatInt(rows[len(rows)-1].ID, 10)
		}
		chains, err := s.downloadChains(ctx, rows)
		if err != nil {
			return nil, s.problem(ctx, err, false)
		}
		for i := range rows {
			page.Entries = append(page.Entries, s.historyRowOf(&rows[i], chains))
		}
		return &struct{ Body HistoryPage }{page}, nil
	})
}

func (s *Server) historyRowOf(r *db.ListHistoryFilteredRow, chains map[int64]int64) HistoryRow {
	h := db.History{
		ID: r.ID, MovieID: r.MovieID, Event: r.Event, SourceTitle: r.SourceTitle, Quality: r.Quality,
		CustomScore: r.CustomScore, DownloadID: r.DownloadID, Data: r.Data, CreatedAt: r.CreatedAt,
	}
	out := HistoryRow{HistoryEntry: historyOf(&h), QualityName: qualityName(r.Quality), RetryOf: r.DownloadRetryOf}
	var data struct {
		Reason string `json:"reason"`
	}
	if json.Unmarshal([]byte(r.Data), &data) == nil {
		out.Reason = data.Reason
	}
	if r.MovieID != nil && r.MovieTitle != nil {
		out.Movie = &HistoryMovie{
			ID: *r.MovieID, Title: *r.MovieTitle, Year: deref(r.MovieYear),
			PosterURL: s.imageURL(imagecache.Poster, gridPosterSize, r.MoviePosterPath), DominantColor: r.MovieDominantColor,
		}
	}
	if r.DownloadID != nil {
		root := chains[*r.DownloadID]
		if root == 0 {
			root = *r.DownloadID
		}
		out.ChainID = &root
	}
	return out
}

func deref[T any](p *T) T {
	var zero T
	if p == nil {
		return zero
	}
	return *p
}

// maxChainDepth bounds the walk up a retry chain (a movie gets a few attempts
// a day).
const maxChainDepth = 50

// downloadChains maps each download of the rows to the first download of its
// retry chain.
func (s *Server) downloadChains(ctx context.Context, rows []db.ListHistoryFilteredRow) (map[int64]int64, error) {
	parent := map[int64]*int64{}
	var pending []int64
	for i := range rows {
		if id := rows[i].DownloadID; id != nil {
			if _, ok := parent[*id]; !ok {
				parent[*id] = rows[i].DownloadRetryOf
				if rows[i].DownloadRetryOf != nil {
					pending = append(pending, *rows[i].DownloadRetryOf)
				}
			}
		}
	}
	for depth := 0; len(pending) > 0 && depth < maxChainDepth; depth++ {
		var ask []int64
		for _, id := range pending {
			if _, ok := parent[id]; !ok {
				ask = append(ask, id)
			}
		}
		if len(ask) == 0 {
			break
		}
		raw, err := json.Marshal(ask)
		if err != nil {
			return nil, fmt.Errorf("api: encode ids: %w", err)
		}
		links, err := s.d.Store.Read().ListDownloadLinks(ctx, string(raw))
		if err != nil {
			return nil, store.MapError(fmt.Errorf("api: download links: %w", err))
		}
		pending = pending[:0]
		for _, id := range ask {
			parent[id] = nil // deleted downloads end the chain
		}
		for _, l := range links {
			parent[l.ID] = l.RetryOf
			if l.RetryOf != nil {
				pending = append(pending, *l.RetryOf)
			}
		}
	}
	roots := make(map[int64]int64, len(parent))
	for id := range parent {
		cur := id
		for range maxChainDepth {
			p := parent[cur]
			if p == nil {
				break
			}
			if _, known := parent[*p]; !known {
				break
			}
			cur = *p
		}
		roots[id] = cur
	}
	return roots, nil
}

// ----------------------------------------------------------------- discover

// DiscoverMovie is a TMDB movie with its library flags.
type DiscoverMovie struct {
	TmdbID           int              `json:"tmdbId"`
	Title            string           `json:"title"`
	OriginalTitle    string           `json:"originalTitle,omitempty"`
	OriginalLanguage string           `json:"originalLanguage,omitempty"`
	Year             int              `json:"year" doc:"0 when unknown"`
	ReleaseDate      *string          `json:"releaseDate" doc:"YYYY-MM-DD"`
	Overview         string           `json:"overview"`
	PosterURL        *string          `json:"posterUrl" doc:"w342, through the image cache"`
	BackdropURL      *string          `json:"backdropUrl" doc:"w1280"`
	Popularity       float64          `json:"popularity"`
	Rating           *metadata.Rating `json:"rating"`
	InLibrary        bool             `json:"inLibrary"`
	MovieID          *int64           `json:"movieId" doc:"Library movie id when inLibrary"`
	Monitored        bool             `json:"monitored" doc:"Library movies only"`
	State            *string          `json:"state" enum:"available,missing,downloading,upgradable" doc:"Library state; null when not in the library"`
	Excluded         bool             `json:"excluded" doc:"On the import list exclusions"`
}

func (s *Server) discoverMovieOf(m *metadata.MovieSummary, f library.Flags) DiscoverMovie {
	out := DiscoverMovie{
		TmdbID: m.TMDBID, Title: m.Title, OriginalTitle: m.OriginalTitle, OriginalLanguage: m.OriginalLanguage,
		Year: m.Year, ReleaseDate: optStr(m.ReleaseDate.String()), Overview: m.Overview,
		PosterURL: s.imageURLOf(imagecache.Poster, gridPosterSize, m.PosterPath), BackdropURL: s.imageURLOf(imagecache.Backdrop, backdropSize, m.BackdropPath),
		Popularity: m.Popularity, Rating: m.Rating, InLibrary: f.MovieID != 0, Monitored: f.Monitored, State: optStr(f.State),
		Excluded: f.Excluded,
	}
	if f.MovieID != 0 {
		id := f.MovieID
		out.MovieID = &id
	}
	return out
}

// DiscoverPage is a page of a TMDB list.
type DiscoverPage struct {
	Page         int             `json:"page"`
	TotalPages   int             `json:"totalPages"`
	TotalResults int             `json:"totalResults"`
	Results      []DiscoverMovie `json:"results"`
}

// DiscoverCollection is a TMDB collection with every part flagged.
type DiscoverCollection struct {
	TmdbID       int             `json:"tmdbId"`
	Name         string          `json:"name"`
	Overview     string          `json:"overview"`
	PosterURL    *string         `json:"posterUrl"`
	BackdropURL  *string         `json:"backdropUrl"`
	CollectionID *int64          `json:"collectionId" doc:"Library collection (GET /collections/{id}); null when none of its movies was added"`
	Monitored    bool            `json:"monitored" doc:"New movies of the collection are added automatically"`
	InLibrary    int             `json:"inLibrary" doc:"Parts already in the library"`
	Parts        []DiscoverMovie `json:"parts" doc:"By release date, undated last; part n is entry n-1"`
}

// PersonCredit is a movie of a filmography, roles merged.
type PersonCredit struct {
	DiscoverMovie
	Characters  []string `json:"characters" doc:"Cast roles"`
	Jobs        []string `json:"jobs" doc:"Crew jobs (Director, Writer...)"`
	Departments []string `json:"departments"`
}

// Filmography is the movies of a person.
type Filmography struct {
	TmdbID int            `json:"tmdbId"`
	Person *PersonProfile `json:"person" doc:"Name, portrait and biography; null when TMDB could not give them (the movies are still listed)"`
	Movies []PersonCredit `json:"movies" doc:"Cast and crew merged by movie, deduplicated; upcoming (undated) first, then newest first"`
}

type addCollectionInput struct {
	TmdbID int `path:"tmdbId" minimum:"1" doc:"TMDB collection id"`
	Body   struct {
		RootFolderID        int64    `json:"rootFolderId" minimum:"1"`
		QualityProfileID    int64    `json:"qualityProfileId" minimum:"1"`
		MinimumAvailability string   `json:"minimumAvailability,omitempty" enum:"announced,in_cinemas,released" default:"released"`
		Monitored           *bool    `json:"monitored,omitempty" doc:"Monitor the added movies; default true"`
		MonitorCollection   bool     `json:"monitorCollection,omitempty" doc:"Also add the collection's future movies automatically (the movies are then monitored)"`
		SearchOnAdd         bool     `json:"searchOnAdd,omitempty"`
		Tags                []string `json:"tags,omitempty" maxItems:"20"`
	}
}

// AddCollectionResult reports the movies added from a collection.
type AddCollectionResult struct {
	Collection Collection            `json:"collection"`
	Added      []Movie               `json:"added"`
	Existing   []int64               `json:"existing" doc:"Library ids of the parts already in the library (left unchanged)"`
	Skipped    []library.SkippedPart `json:"skipped" doc:"Parts not added, with the reason"`
}

func (s *Server) discoverRoutes() {
	tags := []string{"Discover"}
	huma.Register(s.api, huma.Operation{
		OperationID: "discover-list", Method: http.MethodGet, Path: "/discover/{list}", Summary: "A TMDB list",
		Description: "Popular, trending, upcoming and now playing movies (upcoming and now playing for the configured region), " +
			"flagged when already in the library. Adult movies are left out.",
		Tags: tags,
	}, func(ctx context.Context, in *struct {
		List   string `path:"list" enum:"popular,trending,upcoming,now_playing"`
		Window string `query:"window" enum:"day,week" default:"week" doc:"Trending period"`
		Page   int    `query:"page" minimum:"1" maximum:"500" default:"1"`
	}) (*struct{ Body DiscoverPage }, error) {
		p, err := s.d.Library.Discover(ctx, in.List, in.Window, in.Page)
		if err != nil {
			return nil, s.problem(ctx, err, true)
		}
		out := DiscoverPage{Page: p.Page, TotalPages: p.TotalPages, TotalResults: p.TotalResults, Results: make([]DiscoverMovie, 0, len(p.Results))}
		for i := range p.Results {
			out.Results = append(out.Results, s.discoverMovieOf(&p.Results[i].MovieSummary, p.Results[i].Flags))
		}
		return &struct{ Body DiscoverPage }{out}, nil
	})
	huma.Register(s.api, huma.Operation{
		OperationID: "discover-collection", Method: http.MethodGet, Path: "/discover/collection/{tmdbId}",
		Summary: "A TMDB collection", Description: "Every part, flagged when already in the library.", Tags: tags,
	}, func(ctx context.Context, in *struct {
		TmdbID int `path:"tmdbId" minimum:"1"`
	}) (*struct{ Body DiscoverCollection }, error) {
		c, err := s.d.Library.CollectionByTMDB(ctx, in.TmdbID)
		if err != nil {
			return nil, s.problem(ctx, err, true)
		}
		out := DiscoverCollection{
			TmdbID: c.TMDBID, Name: c.Name, Overview: c.Overview, Monitored: c.Monitored,
			PosterURL:   s.imageURLOf(imagecache.Poster, gridPosterSize, c.PosterPath),
			BackdropURL: s.imageURLOf(imagecache.Backdrop, backdropSize, c.BackdropPath),
			Parts:       make([]DiscoverMovie, 0, len(c.Parts)),
		}
		if c.CollectionID != 0 {
			id := c.CollectionID
			out.CollectionID = &id
		}
		for i := range c.Parts {
			if c.Parts[i].MovieID != 0 {
				out.InLibrary++
			}
			out.Parts = append(out.Parts, s.discoverMovieOf(&c.Parts[i].MovieSummary, c.Parts[i].Flags))
		}
		return &struct{ Body DiscoverCollection }{out}, nil
	})
	huma.Register(s.api, huma.Operation{
		OperationID: "discover-person", Method: http.MethodGet, Path: "/discover/person/{tmdbId}",
		Summary: "Filmography of a person",
		Description: "Cast and crew credits merged by movie, flagged when in the library, with the person's profile (name, portrait, biography) " +
			"from a second cached TMDB call made in parallel; person is null when only that call failed.",
		Tags: tags,
	}, func(ctx context.Context, in *struct {
		TmdbID int `path:"tmdbId" minimum:"1" doc:"TMDB person id"`
	}) (*struct{ Body Filmography }, error) {
		var person *metadata.PersonDetails
		var personErr error
		done := make(chan struct{})
		go func() {
			defer close(done)
			person, personErr = s.d.Library.Person(ctx, in.TmdbID)
		}()
		movies, err := s.d.Library.Filmography(ctx, in.TmdbID)
		<-done
		if err != nil {
			return nil, s.problem(ctx, err, true)
		}
		out := Filmography{TmdbID: in.TmdbID, Movies: make([]PersonCredit, 0, len(movies))}
		if personErr == nil {
			out.Person = s.personProfileOf(person)
		} else {
			s.log.Warn("person profile unavailable", slog.String("request_id", RequestID(ctx)), slog.Int("tmdb_id", in.TmdbID), slog.Any("error", personErr))
		}
		for i := range movies {
			m := &movies[i]
			out.Movies = append(out.Movies, PersonCredit{
				DiscoverMovie: s.discoverMovieOf(&m.MovieSummary, m.Flags),
				Characters:    m.Characters, Jobs: m.Jobs, Departments: m.Departments,
			})
		}
		return &struct{ Body Filmography }{out}, nil
	})
	huma.Register(s.api, huma.Operation{
		OperationID: "add-collection", Method: http.MethodPost, Path: "/collections/{tmdbId}/add",
		Summary:     "Add every movie of a TMDB collection",
		Description: "Adds the parts that are not in the library (excluded ones are skipped) with the given settings; with monitorCollection, its future movies are added too.",
		Tags:        []string{"Collections"},
	}, func(ctx context.Context, in *addCollectionInput) (*struct{ Body AddCollectionResult }, error) {
		b := in.Body
		res, err := s.d.Library.AddCollection(ctx, in.TmdbID, library.AddCollectionOptions{
			AddOptions: library.AddOptions{
				RootFolderID: b.RootFolderID, QualityProfileID: b.QualityProfileID, MinimumAvailability: b.MinimumAvailability,
				Monitored: b.Monitored == nil || *b.Monitored, Tags: b.Tags, SearchOnAdd: b.SearchOnAdd,
			},
			MonitorCollection: b.MonitorCollection,
		})
		if err != nil {
			return nil, s.problem(ctx, err, true)
		}
		out := AddCollectionResult{Collection: collectionOf(&res.Collection), Added: []Movie{}, Existing: orEmpty(res.Existing), Skipped: orEmpty(res.Skipped)}
		for i := range res.Added {
			out.Added = append(out.Added, movieOf(&res.Added[i]))
		}
		return &struct{ Body AddCollectionResult }{out}, nil
	})
}

// ----------------------------------------------------------------- wanted

func (s *Server) wantedRoute() {
	huma.Register(s.api, huma.Operation{
		OperationID: "list-wanted", Method: http.MethodGet, Path: "/wanted", Summary: "Wanted movies",
		Description: "missing: monitored, no file, released enough for the minimum availability. " +
			"cutoff_unmet: monitored, the file is below the profile's cutoff. Same rows and paging as GET /movies.",
		Tags: []string{"Movies"},
	}, func(ctx context.Context, in *struct {
		Kind   string `query:"kind" enum:"missing,cutoff_unmet" default:"missing"`
		Sort   string `query:"sort" enum:"title,year,added,size,score,quality" default:"title"`
		Order  string `query:"order" enum:"asc,desc"`
		Cursor string `query:"cursor" maxLength:"512"`
		Limit  int    `query:"limit" minimum:"1" maximum:"500" default:"100"`
	}) (*struct{ Body MoviePage }, error) {
		page, err := s.d.Library.ListMovies(ctx, library.ListQuery{
			Sort: in.Sort, Order: in.Order, Cursor: in.Cursor, Limit: in.Limit, Filter: library.ListFilter{Wanted: in.Kind},
		})
		if err != nil {
			return nil, s.problem(ctx, err, false)
		}
		live := s.liveIfDownloading(ctx, page.Movies)
		out := MoviePage{Movies: make([]MovieListItem, 0, len(page.Movies)), NextCursor: page.NextCursor, Total: page.Total}
		for _, m := range page.Movies {
			out.Movies = append(out.Movies, s.movieListItemOf(m, live))
		}
		return &struct{ Body MoviePage }{out}, nil
	})
}
