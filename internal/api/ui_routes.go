package api

import (
	"context"
	"encoding/json"
	"errors"
	"log/slog"
	"net/http"
	"os"
	"path"
	"strconv"

	"github.com/danielgtaylor/huma/v2"
	"github.com/danielgtaylor/huma/v2/adapters/humago"

	"github.com/fs0ciety7000/gonema/internal/acquire"
	"github.com/fs0ciety7000/gonema/internal/imagecache"
	"github.com/fs0ciety7000/gonema/internal/library"
	"github.com/fs0ciety7000/gonema/internal/store/db"
)

// QueueReader reads the download queue with its live progress
// (*acquire.Service implements it).
type QueueReader interface {
	Queue(ctx context.Context) ([]acquire.QueueItem, error)
}

// ImageStore serves cached TMDB images, downloading them on a miss
// (*imagecache.Cache implements it).
type ImageStore interface {
	Path(kind imagecache.Kind, size, tmdbPath string) (string, error)
	Fetch(ctx context.Context, kind imagecache.Kind, size, tmdbPath string) (string, error)
}

// maxImageFetches bounds the concurrent downloads from TMDB triggered by image
// requests: a grid scrolled on a cold cache must not open hundreds of
// connections at once.
const maxImageFetches = 8

func (s *Server) queueReader() QueueReader {
	if s.d.Queue != nil {
		return s.d.Queue
	}
	if s.d.Acquire != nil {
		return s.d.Acquire
	}
	return nil
}

// liveDownloads returns the newest active download of each movie. Errors are
// logged: the lists then fall back to the persisted progress.
func (s *Server) liveDownloads(ctx context.Context) map[int64]*acquire.QueueItem {
	qr := s.queueReader()
	if qr == nil {
		return nil
	}
	items, err := qr.Queue(ctx)
	if err != nil {
		s.log.Warn("queue unavailable for the movie list", slog.String("request_id", RequestID(ctx)), slog.Any("error", err))
		return nil
	}
	out := make(map[int64]*acquire.QueueItem, len(items))
	for i := range items { // oldest first: the newest wins
		out[items[i].MovieID] = &items[i]
	}
	return out
}

// movieDetail reads the movie page data; errors are already API errors.
func (s *Server) movieDetail(ctx context.Context, id int64) (MovieDetail, error) {
	d, err := s.d.Library.GetMovie(ctx, id)
	if err != nil {
		return MovieDetail{}, s.problem(ctx, err, false)
	}
	var live map[int64]*acquire.QueueItem
	if d.State == library.StateDownloading {
		live = s.liveDownloads(ctx)
	}
	return s.movieDetailOf(&d, live), nil
}

// MovieFilterParams selects and orders movies (list and columns endpoints).
type MovieFilterParams struct {
	Sort         string   `query:"sort" enum:"title,year,added,size,score,quality" default:"title" doc:"size, score and quality are those of the current file; quality uses the catalog rank"`
	Order        string   `query:"order" enum:"asc,desc" doc:"Default: asc for title, desc for the other sorts"`
	Monitored    string   `query:"monitored" enum:"true,false" doc:"Only monitored (true) or unmonitored (false) movies"`
	Status       string   `query:"status" enum:"announced,in_cinemas,released,deleted" doc:"Release stage"`
	State        []string `query:"state" enum:"available,missing,downloading,upgradable,unmonitored,failed" maxItems:"6" doc:"Library states, comma-separated: movies in any of them. unmonitored = monitored false; failed = the newest download failed"`
	Quality      string   `query:"quality" maxLength:"32" doc:"Quality id of the current file (bluray-1080p...)"`
	YearFrom     int      `query:"yearFrom" minimum:"0" maximum:"2200"`
	YearTo       int      `query:"yearTo" minimum:"0" maximum:"2200"`
	Genre        string   `query:"genre" maxLength:"100"`
	RootFolderID int64    `query:"rootFolderId" minimum:"0"`
	Tag          string   `query:"tag" maxLength:"64" doc:"Tag label (same as a one-label tags)"`
	Tags         []string `query:"tags" maxItems:"20" doc:"Tag labels, comma-separated: movies with any of them (tagMatch=any) or all (tagMatch=all)"`
	TagMatch     string   `query:"tagMatch" enum:"any,all" default:"any"`
	Q            string   `query:"q" maxLength:"200" doc:"Text in the title, original title or an alternate title (case-insensitive)"`
}

func (p *MovieFilterParams) query() library.ListQuery {
	q := library.ListQuery{Sort: p.Sort, Order: p.Order, Filter: library.ListFilter{
		Status: p.Status, States: p.State, Quality: p.Quality, YearFrom: p.YearFrom, YearTo: p.YearTo,
		Genre: p.Genre, RootFolderID: p.RootFolderID, Tag: p.Tag, Tags: p.Tags, TagMatch: p.TagMatch, Query: p.Q,
	}}
	if p.Monitored != "" {
		v := p.Monitored == "true"
		q.Filter.Monitored = &v
	}
	return q
}

type movieListInput struct {
	MovieFilterParams
	Cursor string `query:"cursor" maxLength:"512" doc:"nextCursor of the previous page"`
	Limit  int    `query:"limit" minimum:"1" maximum:"500" default:"100"`
}

// MoviePage is a page of movies.
type MoviePage struct {
	Movies     []MovieListItem `json:"movies"`
	NextCursor string          `json:"nextCursor,omitempty" doc:"Pass as cursor to get the next page; absent on the last page"`
	Total      int             `json:"total" doc:"Movies matching the filters, all pages included"`
}

// LibraryStats are the counts of the library header and tabs.
type LibraryStats struct {
	Total       int64 `json:"total" doc:"Movies in the library"`
	OnDisk      int64 `json:"onDisk" doc:"Movies with a file"`
	TotalSize   int64 `json:"totalSize" doc:"Bytes of all current files"`
	Monitored   int64 `json:"monitored"`
	Unmonitored int64 `json:"unmonitored"`
	Available   int64 `json:"available" doc:"State available: file at or above the cutoff"`
	Missing     int64 `json:"missing" doc:"State missing: no file"`
	Downloading int64 `json:"downloading"`
	Upgradable  int64 `json:"upgradable" doc:"State upgradable: file below the cutoff"`
	Wanted      int64 `json:"wanted" doc:"Monitored, missing and available for release according to the minimum availability"`
	CutoffUnmet int64 `json:"cutoffUnmet" doc:"Monitored and upgradable"`
}

// QueueSummary is the download activity of the top bar.
type QueueSummary struct {
	Count         int    `json:"count" doc:"Active queue entries"`
	Downloading   int    `json:"downloading" doc:"Entries transferring now"`
	Paused        int    `json:"paused"`
	Warnings      int    `json:"warnings" doc:"Entries with a warning"`
	TotalSpeedBps int64  `json:"totalSpeedBps"`
	EtaSeconds    *int64 `json:"etaSeconds" doc:"Longest remaining time among the downloading entries"`
}

// SavedView is a named set of filters, sort and columns of a UI page.
type SavedView struct {
	ID       int64           `json:"id"`
	Page     string          `json:"page" doc:"UI page, e.g. library"`
	Name     string          `json:"name"`
	Filters  json.RawMessage `json:"filters" doc:"JSON object, opaque to the server"`
	Sort     json.RawMessage `json:"sort" doc:"JSON object, opaque to the server"`
	Columns  []string        `json:"columns"`
	Density  string          `json:"density" enum:"airy,compact"`
	Position int64           `json:"position"`
}

func savedViewOf(v *db.SavedView) SavedView {
	out := SavedView{
		ID: v.ID, Page: v.Page, Name: v.Name, Filters: raw(v.Filters), Sort: raw(v.Sort),
		Columns: []string{}, Density: v.Density, Position: v.Position,
	}
	if err := json.Unmarshal([]byte(v.Columns), &out.Columns); err != nil || out.Columns == nil {
		out.Columns = []string{}
	}
	return out
}

// SavedViewInput is a saved view to create or replace.
type SavedViewInput struct {
	Page     string          `json:"page" minLength:"1" maxLength:"32" pattern:"^[a-z0-9_-]+$"`
	Name     string          `json:"name" minLength:"1" maxLength:"64"`
	Filters  json.RawMessage `json:"filters,omitempty" doc:"JSON object, at most 8 KiB"`
	Sort     json.RawMessage `json:"sort,omitempty" doc:"JSON object, at most 8 KiB"`
	Columns  []string        `json:"columns,omitempty" maxItems:"40"`
	Density  string          `json:"density,omitempty" enum:"airy,compact" default:"airy"`
	Position int64           `json:"position,omitempty"`
}

func (in *SavedViewInput) view() library.ViewInput {
	return library.ViewInput{
		Page: in.Page, Name: in.Name, Filters: in.Filters, Sort: in.Sort, Columns: in.Columns,
		Density: in.Density, Position: in.Position,
	}
}

type imageInput struct {
	Kind string `path:"kind" enum:"poster,backdrop,profile"`
	Size string `path:"size" enum:"w185,w342,w780,w1280" doc:"poster: w342, w780; backdrop: w1280; profile: w185"`
	File string `path:"file" maxLength:"127" pattern:"^[A-Za-z0-9_-]+\\.(jpg|jpeg|png|webp)$" doc:"TMDB file name"`
}

func (s *Server) uiRoutes() {
	tags := []string{"Movies"}
	huma.Register(s.api, huma.Operation{
		OperationID: "list-movies", Method: http.MethodGet, Path: "/movies", Summary: "List movies",
		Description: "One page of the library grid and table, with everything both screens show. " +
			"Keyset pagination: pass nextCursor back with the same filters and sort.",
		Tags: tags,
	}, func(ctx context.Context, in *movieListInput) (*struct{ Body MoviePage }, error) {
		q := in.query()
		q.Cursor, q.Limit = in.Cursor, in.Limit
		page, err := s.d.Library.ListMovies(ctx, q)
		if err != nil {
			return nil, s.problem(ctx, err, false)
		}
		live := s.liveIfDownloading(ctx, page.Movies)
		out := &struct{ Body MoviePage }{MoviePage{
			Movies: make([]MovieListItem, 0, len(page.Movies)), NextCursor: page.NextCursor, Total: page.Total,
		}}
		for _, m := range page.Movies {
			out.Body.Movies = append(out.Body.Movies, s.movieListItemOf(m, live))
		}
		return out, nil
	})

	huma.Register(s.api, huma.Operation{
		OperationID: "list-movie-columns", Method: http.MethodGet, Path: "/movies/ids",
		Summary: "Every matching movie, in columns",
		Description: "All the movies matching the filters, in the requested order, as parallel arrays (entry i of " +
			"each array is the same movie). One request feeds a virtualised grid or table of the whole library.",
		Tags: tags,
	}, func(ctx context.Context, in *struct{ MovieFilterParams }) (*struct{ Body MovieColumns }, error) {
		q := in.query()
		q.All = true
		page, err := s.d.Library.ListMovies(ctx, q)
		if err != nil {
			return nil, s.problem(ctx, err, false)
		}
		return &struct{ Body MovieColumns }{s.movieColumnsOf(page.Movies, s.liveIfDownloading(ctx, page.Movies))}, nil
	})

	huma.Register(s.api, huma.Operation{
		OperationID: "get-library-stats", Method: http.MethodGet, Path: "/library/stats",
		Summary: "Library counts", Description: "Totals of the library header and counts of the tabs.", Tags: tags,
	}, func(ctx context.Context, _ *struct{}) (*struct{ Body LibraryStats }, error) {
		st, err := s.d.Library.Stats(ctx)
		if err != nil {
			return nil, s.problem(ctx, err, false)
		}
		return &struct{ Body LibraryStats }{LibraryStats{
			Total: st.Total, OnDisk: st.OnDisk, TotalSize: st.TotalSize, Monitored: st.Monitored, Unmonitored: st.Unmonitored,
			Available: st.Available, Missing: st.Missing, Downloading: st.Downloading, Upgradable: st.Upgradable,
			Wanted: st.Wanted, CutoffUnmet: st.CutoffUnmet,
		}}, nil
	})

	huma.Register(s.api, huma.Operation{
		OperationID: "get-queue-summary", Method: http.MethodGet, Path: "/queue/summary",
		Summary: "Download activity", Description: "Count and total speed of the active downloads, for the top bar.",
		Tags: []string{"Queue"},
	}, func(ctx context.Context, _ *struct{}) (*struct{ Body QueueSummary }, error) {
		var out QueueSummary
		if qr := s.queueReader(); qr != nil {
			items, err := qr.Queue(ctx)
			if err != nil {
				return nil, s.problem(ctx, err, false)
			}
			out = summarize(items)
		}
		return &struct{ Body QueueSummary }{out}, nil
	})

	s.viewRoutes()
	s.imageRoute()
	s.tagRoutes()
	s.bulkRoutes()
	s.historyRoutes()
	s.discoverRoutes()
	s.wantedRoute()
}

// liveIfDownloading reads the live queue only when a row is downloading.
func (s *Server) liveIfDownloading(ctx context.Context, rows []*library.MovieSummary) map[int64]*acquire.QueueItem {
	for _, m := range rows {
		if m.State == library.StateDownloading {
			return s.liveDownloads(ctx)
		}
	}
	return nil
}

func summarize(items []acquire.QueueItem) QueueSummary {
	out := QueueSummary{Count: len(items)}
	for i := range items {
		it := &items[i]
		switch it.Status {
		case "downloading":
			out.Downloading++
			if it.EtaSeconds != nil && (out.EtaSeconds == nil || *it.EtaSeconds > *out.EtaSeconds) {
				eta := *it.EtaSeconds
				out.EtaSeconds = &eta
			}
		case "paused":
			out.Paused++
		case "warning":
			out.Warnings++
		}
		out.TotalSpeedBps += it.SpeedBps
	}
	return out
}

func (s *Server) viewRoutes() {
	tags := []string{"Saved views"}
	huma.Register(s.api, huma.Operation{
		OperationID: "list-views", Method: http.MethodGet, Path: "/views", Summary: "List saved views",
		Description: "Ordered by page then position. Views are shared until user accounts exist.", Tags: tags,
	}, func(ctx context.Context, in *struct {
		Page string `query:"page" maxLength:"32" doc:"Only the views of this page"`
	}) (*struct{ Body []SavedView }, error) {
		rows, err := s.d.Library.ListViews(ctx, in.Page)
		if err != nil {
			return nil, s.problem(ctx, err, false)
		}
		out := &struct{ Body []SavedView }{make([]SavedView, 0, len(rows))}
		for i := range rows {
			out.Body = append(out.Body, savedViewOf(&rows[i]))
		}
		return out, nil
	})

	huma.Register(s.api, huma.Operation{
		OperationID: "get-view", Method: http.MethodGet, Path: "/views/{id}", Summary: "Get a saved view", Tags: tags,
	}, func(ctx context.Context, in *IDParam) (*struct{ Body SavedView }, error) {
		v, err := s.d.Library.GetView(ctx, in.ID)
		if err != nil {
			return nil, s.problem(ctx, err, false)
		}
		return &struct{ Body SavedView }{savedViewOf(&v)}, nil
	})

	huma.Register(s.api, huma.Operation{
		OperationID: "create-view", Method: http.MethodPost, Path: "/views", Summary: "Save a view",
		Description: "Names are unique per page (409 otherwise).", Tags: tags, DefaultStatus: http.StatusCreated,
	}, func(ctx context.Context, in *struct{ Body SavedViewInput }) (*struct{ Body SavedView }, error) {
		v, err := s.d.Library.CreateView(ctx, in.Body.view())
		if err != nil {
			return nil, s.problem(ctx, err, false)
		}
		return &struct{ Body SavedView }{savedViewOf(&v)}, nil
	})

	huma.Register(s.api, huma.Operation{
		OperationID: "update-view", Method: http.MethodPut, Path: "/views/{id}", Summary: "Replace a saved view", Tags: tags,
	}, func(ctx context.Context, in *struct {
		IDParam
		Body SavedViewInput
	}) (*struct{ Body SavedView }, error) {
		v, err := s.d.Library.UpdateView(ctx, in.ID, in.Body.view())
		if err != nil {
			return nil, s.problem(ctx, err, false)
		}
		return &struct{ Body SavedView }{savedViewOf(&v)}, nil
	})

	huma.Register(s.api, huma.Operation{
		OperationID: "delete-view", Method: http.MethodDelete, Path: "/views/{id}", Summary: "Delete a saved view",
		Tags: tags, DefaultStatus: http.StatusNoContent,
	}, func(ctx context.Context, in *IDParam) (*struct{}, error) {
		if err := s.d.Library.DeleteView(ctx, in.ID); err != nil {
			return nil, s.problem(ctx, err, false)
		}
		return nil, nil
	})
}

// imageRoute serves the image cache. Only TMDB-style file names of the known
// kinds and sizes are accepted, and the upstream is always the TMDB image base
// URL: the endpoint cannot be used to fetch arbitrary URLs.
func (s *Server) imageRoute() {
	sem := make(chan struct{}, maxImageFetches)
	huma.Register(s.api, huma.Operation{
		OperationID: "get-image", Method: http.MethodGet, Path: "/images/{kind}/{size}/{file}",
		Summary: "A poster, backdrop or portrait", Tags: []string{"Images"},
		Description: "Served from the local cache, downloaded from TMDB on the first request. " +
			"Images never change for a given name: they are cached by browsers for a year (ETag, immutable).",
		Responses: map[string]*huma.Response{
			"200": {Description: "The image", Content: map[string]*huma.MediaType{
				"image/jpeg": {Schema: &huma.Schema{Type: huma.TypeString, Format: "binary"}},
				"image/png":  {Schema: &huma.Schema{Type: huma.TypeString, Format: "binary"}},
				"image/webp": {Schema: &huma.Schema{Type: huma.TypeString, Format: "binary"}},
			}},
			"304": {Description: "Not modified (If-None-Match)"},
		},
	}, func(ctx context.Context, in *imageInput) (*huma.StreamResponse, error) {
		if s.d.Images == nil {
			return nil, huma.Error404NotFound("Images are not available on this server.")
		}
		kind, file := imagecache.Kind(in.Kind), "/"+in.File
		local, err := s.d.Images.Path(kind, in.Size, file)
		if err != nil {
			return nil, huma.Error404NotFound("There is no " + in.Size + " size for " + in.Kind + " images.")
		}
		if fi, err := os.Stat(local); err != nil || !fi.Mode().IsRegular() || fi.Size() == 0 {
			select {
			case sem <- struct{}{}:
			case <-ctx.Done():
				return nil, huma.Error503ServiceUnavailable("The request was canceled.")
			}
			local, err = s.d.Images.Fetch(ctx, kind, in.Size, file)
			<-sem
			if err != nil {
				return nil, s.imageProblem(ctx, err)
			}
		}
		return &huma.StreamResponse{Body: func(hctx huma.Context) {
			r, w := humago.Unwrap(hctx)
			serveImage(w, r, local, in.Kind, in.Size, in.File)
		}}, nil
	})
}

func (s *Server) imageProblem(ctx context.Context, err error) error {
	switch {
	case errors.Is(err, imagecache.ErrNotFound):
		return huma.Error404NotFound("TMDB has no such image.")
	case errors.Is(err, imagecache.ErrInvalidPath):
		return huma.Error404NotFound("Not an image name Gonema serves.")
	case errors.Is(err, library.ErrNoProvider):
		return huma.Error404NotFound("This image is not cached, and no TMDB key is configured to download it.")
	case errors.Is(err, imagecache.ErrNotImage), errors.Is(err, imagecache.ErrTooLarge):
		s.log.Warn("TMDB image rejected", slog.String("request_id", RequestID(ctx)), slog.Any("error", err))
		return huma.Error502BadGateway("TMDB returned something that is not a usable image.")
	case errors.Is(err, context.DeadlineExceeded):
		return huma.Error504GatewayTimeout("TMDB did not send the image in time; try again later.")
	case errors.Is(err, context.Canceled):
		return huma.Error503ServiceUnavailable("The request was canceled.")
	}
	s.log.Warn("image download failed", slog.String("request_id", RequestID(ctx)), slog.Any("error", err))
	return huma.Error502BadGateway("The image could not be downloaded from TMDB; try again later.")
}

// serveImage writes a cached image with a strong ETag (TMDB names are content
// addressed; size and modification time cover a re-download) and immutable
// caching. http.ServeContent answers If-None-Match with 304 and ranges.
func serveImage(w http.ResponseWriter, r *http.Request, local, kind, size, file string) {
	f, err := os.Open(local)
	if err != nil {
		writeProblem(w, r, http.StatusNotFound, "This image is no longer in the cache; reload the page.")
		return
	}
	defer f.Close()
	fi, err := f.Stat()
	if err != nil {
		writeProblem(w, r, http.StatusInternalServerError, "The cached image could not be read.")
		return
	}
	h := w.Header()
	h.Set("ETag", `"`+kind+"-"+size+"-"+file+"-"+strconv.FormatInt(fi.Size(), 36)+"-"+strconv.FormatInt(fi.ModTime().UnixNano(), 36)+`"`)
	h.Set("Cache-Control", "public, max-age=31536000, immutable")
	h.Set("X-Content-Type-Options", "nosniff")
	switch path.Ext(file) {
	case ".png":
		h.Set("Content-Type", "image/png")
	case ".webp":
		h.Set("Content-Type", "image/webp")
	default:
		h.Set("Content-Type", "image/jpeg")
	}
	http.ServeContent(w, r, "", fi.ModTime(), f)
}
