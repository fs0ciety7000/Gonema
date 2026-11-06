package api

import (
	"context"
	"net/http"
	"strings"
	"time"

	"github.com/danielgtaylor/huma/v2"

	"github.com/fs0ciety7000/gonema/internal/imagecache"
	"github.com/fs0ciety7000/gonema/internal/library"
	"github.com/fs0ciety7000/gonema/internal/store"
)

// The collections page (GET /collections) and the films of a collection
// (GET /collections/{id}).

// CollectionFilm is a film of a collection, in the library or not.
type CollectionFilm struct {
	TmdbID      int     `json:"tmdbId"`
	Title       string  `json:"title"`
	Year        int     `json:"year" doc:"0 when unknown"`
	ReleaseDate *string `json:"releaseDate" doc:"Release date of the part on TMDB (YYYY-MM-DD); null when unknown"`
	PosterURL   *string `json:"posterUrl" doc:"w342; replace w342 by w92 in the path for a thumbnail"`
	State       string  `json:"state" enum:"on_disk,downloading,wanted,upcoming,missing,not_released" doc:"on_disk, downloading, wanted (no file yet, released or in cinemas) and upcoming (announced) are library movies; missing (released) and not_released are not in the library"`
	InLibrary   bool    `json:"inLibrary"`
	Released    bool    `json:"released"`
	// Library movies only (null otherwise).
	MovieID             *int64       `json:"movieId"`
	Monitored           *bool        `json:"monitored"`
	Status              *string      `json:"status" enum:"announced,in_cinemas,released,deleted" doc:"Release stage of the library movie"`
	MinimumAvailability *string      `json:"minimumAvailability" enum:"announced,in_cinemas,released"`
	ReleaseCinema       *string      `json:"releaseCinema" doc:"YYYY-MM-DD"`
	ReleaseDigital      *string      `json:"releaseDigital"`
	ReleasePhysical     *string      `json:"releasePhysical"`
	Quality             *string      `json:"quality" doc:"Quality id of the file"`
	QualityName         *string      `json:"qualityName"`
	HDR                 *string      `json:"hdr" doc:"Space-separated HDR formats of the file (DV HDR10); null for SDR or no file"`
	SizeOnDisk          *int64       `json:"sizeOnDisk"`
	Progress            *float64     `json:"progress" doc:"Download progress 0 to 1 while downloading"`
	DominantColor       *string      `json:"dominantColor"`
	LastSearchedAt      *time.Time   `json:"lastSearchedAt,omitempty" doc:"Detail only"`
	Excluded            bool         `json:"excluded" doc:"Detail only: not in the library and on the import list exclusions (not added with the collection)"`
	NextRelease         *NextRelease `json:"nextRelease" doc:"Next release from today on; null when none is known"`
}

// NextRelease is the next release date of a film.
type NextRelease struct {
	Date string `json:"date" doc:"YYYY-MM-DD"`
	Type string `json:"type" enum:"cinema,digital,physical" doc:"For a film not in the library, TMDB gives only its first release date, reported as cinema"`
}

// nextRelease is the first release on or after today among the dates.
func nextRelease(today string, dates map[string]*string) *NextRelease {
	var out *NextRelease
	for _, typ := range []string{"cinema", "digital", "physical"} {
		d := dates[typ]
		if d == nil || *d == "" || *d < today {
			continue
		}
		if out == nil || *d < out.Date {
			out = &NextRelease{Date: *d, Type: typ}
		}
	}
	return out
}

// CollectionCounts summarizes the films of a collection.
type CollectionCounts struct {
	Total        int   `json:"total"`
	InLibrary    int   `json:"inLibrary"`
	OnDisk       int   `json:"onDisk"`
	Downloading  int   `json:"downloading"`
	Wanted       int   `json:"wanted" doc:"In the library without a file, released or in cinemas"`
	NotInLibrary int   `json:"notInLibrary" doc:"Every film not in the library, released or not"`
	Missing      int   `json:"missing" doc:"Released films not in the library. An upcoming film is never missing: a monitored collection adds it on its release day"`
	Upcoming     int   `json:"upcoming" doc:"Not released yet, in the library or not"`
	SizeOnDisk   int64 `json:"sizeOnDisk" doc:"Bytes"`
	YearFrom     int   `json:"yearFrom" doc:"0 when unknown"`
	YearTo       int   `json:"yearTo"`
}

// CollectionListItem is a card of the collections page.
type CollectionListItem struct {
	ID                  int64  `json:"id"`
	TmdbID              int64  `json:"tmdbId"`
	Name                string `json:"name"`
	Monitored           bool   `json:"monitored" doc:"New films of the collection are added automatically"`
	QualityProfileID    *int64 `json:"qualityProfileId"`
	RootFolderID        *int64 `json:"rootFolderId"`
	MinimumAvailability string `json:"minimumAvailability" enum:"announced,in_cinemas,released"`
	CollectionExtras
}

// CollectionExtras are the fields of the collections page that the stored
// collection does not hold.
type CollectionExtras struct {
	PosterURL          *string          `json:"posterUrl" doc:"Collection poster, w342"`
	BackdropURL        *string          `json:"backdropUrl" doc:"Collection backdrop, w1280"`
	QualityProfileName *string          `json:"qualityProfileName"`
	SearchOnAdd        bool             `json:"searchOnAdd" doc:"Films added by the collection are searched at once"`
	Counts             CollectionCounts `json:"counts"`
	HasMissing         bool             `json:"hasMissing" doc:"Misses released films (counts.missing > 0): the Missing films filter"`
	DominantColor      *string          `json:"dominantColor" doc:"#rrggbb of the lead film: the first library film, in release order, with a poster color"`
	Complete           bool             `json:"complete" doc:"Every film is on disk"`
	PartsKnown         bool             `json:"partsKnown" doc:"false until Gonema read the collection from TMDB once: only library movies are listed"`
	PartsRefreshedAt   *time.Time       `json:"partsRefreshedAt" doc:"When the films were last read from TMDB"`
	Films              []CollectionFilm `json:"films" doc:"Every film, in release order (undated last)"`
}

// CollectionListTotals are the header and filter counts, filters not applied.
type CollectionListTotals struct {
	Collections  int `json:"collections"`
	Monitored    int `json:"monitored"`
	WithMissing  int `json:"withMissing" doc:"Collections with hasMissing"`
	Films        int `json:"films" doc:"Films in the collections"`
	NotInLibrary int `json:"notInLibrary"`
	Upcoming     int `json:"upcoming"`
}

// CollectionList is a page of the collections page.
type CollectionList struct {
	Collections []CollectionListItem `json:"collections"`
	Total       int                  `json:"total" doc:"Collections matching the filters"`
	Page        int                  `json:"page"`
	PageSize    int                  `json:"pageSize"`
	Totals      CollectionListTotals `json:"totals"`
}

type listCollectionsInput struct {
	Sort      string `query:"sort" enum:"name,missing,year,added" default:"name" doc:"name: A to Z; missing: most films not in the library first; year: newest film first; added: most recently added library movie first"`
	Monitored string `query:"monitored" enum:"true,false" doc:"Only monitored (true) or not monitored (false) collections; absent: all"`
	Missing   bool   `query:"missing" doc:"Only collections with hasMissing"`
	Q         string `query:"q" maxLength:"200" doc:"Part of the name"`
	Page      int    `query:"page" minimum:"1" default:"1"`
	PageSize  int    `query:"pageSize" minimum:"1" maximum:"200" default:"50"`
}

func (s *Server) collectionFilms(v *library.CollectionView, live map[int64]float64) []CollectionFilm {
	today := time.Now().UTC().Format(time.DateOnly)
	out := make([]CollectionFilm, 0, len(v.Films))
	for i := range v.Films {
		f := &v.Films[i]
		cf := CollectionFilm{
			TmdbID: f.TmdbID, Title: f.Title, Year: f.Year, ReleaseDate: optStr(f.ReleaseDate),
			PosterURL: s.imageURLOf(imagecache.Poster, gridPosterSize, f.PosterPath), State: f.State,
			InLibrary: f.Movie != nil, Released: f.Released,
		}
		if m := f.Movie; m != nil {
			id, mon, st, avail := m.ID, m.Monitored, m.Status, m.MinimumAvailability
			cf.MovieID, cf.Monitored, cf.Status, cf.MinimumAvailability = &id, &mon, &st, &avail
			cf.ReleaseCinema, cf.ReleaseDigital, cf.ReleasePhysical = m.ReleaseCinema, m.ReleaseDigital, m.ReleasePhysical
			cf.Quality, cf.QualityName, cf.SizeOnDisk = m.FileQuality, qualityName(m.FileQuality), m.FileSize
			if m.FileHdr != "" {
				h := strings.ToUpper(m.FileHdr)
				cf.HDR = &h
			}
			if f.State == library.PartDownloading {
				p := m.DownloadProgress
				if lp, ok := live[m.ID]; ok {
					p = lp
				}
				cf.Progress = &p
			}
			cf.DominantColor = m.DominantColor
			cf.NextRelease = nextRelease(today, map[string]*string{"cinema": m.ReleaseCinema, "digital": m.ReleaseDigital, "physical": m.ReleasePhysical})
		} else {
			cf.NextRelease = nextRelease(today, map[string]*string{"cinema": cf.ReleaseDate})
		}
		out = append(out, cf)
	}
	return out
}

func countsOf(c *library.CollectionCounts) CollectionCounts {
	return CollectionCounts{
		Total: c.Total, InLibrary: c.InLibrary, OnDisk: c.OnDisk, Downloading: c.Downloading, Wanted: c.Wanted,
		NotInLibrary: c.NotInLibrary, Missing: c.Missing, Upcoming: c.Upcoming, SizeOnDisk: c.SizeOnDisk,
		YearFrom: c.YearFrom, YearTo: c.YearTo,
	}
}

// liveProgress is the live download progress by movie id (empty without the
// acquisition service).
func (s *Server) liveProgress(ctx context.Context) map[int64]float64 {
	out := map[int64]float64{}
	if s.d.Queue == nil {
		return out
	}
	items, err := s.d.Queue.Queue(ctx)
	if err != nil {
		return out
	}
	for i := range items {
		out[items[i].MovieID] = items[i].Progress
	}
	return out
}

func (s *Server) collectionListItem(v *library.CollectionView, profiles map[int64]string, live map[int64]float64) CollectionListItem {
	c := &v.Collection
	return CollectionListItem{
		ID: c.ID, TmdbID: c.TmdbID, Name: c.Name, Monitored: c.Monitored, QualityProfileID: c.QualityProfileID,
		RootFolderID: c.RootFolderID, MinimumAvailability: c.MinimumAvailability, CollectionExtras: s.collectionExtras(v, profiles, live),
	}
}

func (s *Server) collectionExtras(v *library.CollectionView, profiles map[int64]string, live map[int64]float64) CollectionExtras {
	c := &v.Collection
	x := CollectionExtras{
		PosterURL:   s.imageURL(imagecache.Poster, gridPosterSize, c.PosterPath),
		BackdropURL: s.imageURL(imagecache.Backdrop, backdropSize, c.BackdropPath),
		SearchOnAdd: c.SearchOnAdd, Counts: countsOf(&v.Counts),
		HasMissing: v.Counts.HasMissing(), Complete: v.Counts.Total > 0 && v.Counts.OnDisk == v.Counts.Total,
		PartsKnown: v.PartsKnown, PartsRefreshedAt: tsPtr(c.PartsRefreshedAt), Films: s.collectionFilms(v, live),
	}
	if c.QualityProfileID != nil {
		if n, ok := profiles[*c.QualityProfileID]; ok {
			x.QualityProfileName = &n
		}
	}
	for i := range v.Films {
		if m := v.Films[i].Movie; m != nil && m.DominantColor != nil {
			x.DominantColor = m.DominantColor
			break
		}
	}
	return x
}

func (s *Server) profileNames(ctx context.Context) (map[int64]string, error) {
	rows, err := s.d.Store.Read().ListQualityProfiles(ctx)
	if err != nil {
		return nil, store.MapError(err)
	}
	out := make(map[int64]string, len(rows))
	for _, r := range rows {
		out[r.ID] = r.Name
	}
	return out, nil
}

// CollectionDetail is the page of a collection.
type CollectionDetail struct {
	Collection
	CollectionExtras
	RootFolderPath *string `json:"rootFolderPath" doc:"Path of the root folder of the films it adds"`
}

func (s *Server) collectionRoutes() {
	tags := []string{"Collections"}
	huma.Register(s.api, huma.Operation{
		OperationID: "list-collections", Method: http.MethodGet, Path: "/collections", Summary: "Collections of the library",
		Description: "Every collection with at least one library movie, with its films in release order and their state. Library movies " +
			"come from the in-memory index; films not in the library come from the last read of the collection on TMDB (no TMDB call: " +
			"partsKnown is false until it was read once, which the collections refresh job does within 12 hours). totals ignore the filters.",
		Tags: tags,
	}, func(ctx context.Context, in *listCollectionsInput) (*struct{ Body CollectionList }, error) {
		var monitored *bool
		if in.Monitored != "" {
			v := in.Monitored == "true"
			monitored = &v
		}
		page, err := s.d.Library.ListCollections(ctx, library.CollectionQuery{
			Sort: in.Sort, Monitored: monitored, Missing: in.Missing, Query: in.Q, Page: in.Page, PageSize: in.PageSize,
		})
		if err != nil {
			return nil, s.problem(ctx, err, false)
		}
		profiles, err := s.profileNames(ctx)
		if err != nil {
			return nil, s.problem(ctx, err, false)
		}
		live := s.liveProgress(ctx)
		t := page.Totals
		out := CollectionList{
			Collections: make([]CollectionListItem, 0, len(page.Collections)), Total: page.Total, Page: in.Page, PageSize: in.PageSize,
			Totals: CollectionListTotals{
				Collections: t.Collections, Monitored: t.Monitored, WithMissing: t.WithMissing, Films: t.Films,
				NotInLibrary: t.NotInLibrary, Upcoming: t.Upcoming,
			},
		}
		for i := range page.Collections {
			out.Collections = append(out.Collections, s.collectionListItem(&page.Collections[i], profiles, live))
		}
		return &struct{ Body CollectionList }{out}, nil
	})

	huma.Register(s.api, huma.Operation{
		OperationID: "get-collection", Method: http.MethodGet, Path: "/collections/{id}", Summary: "Get a collection",
		Description: "The collection with its settings and every film (films), in release order: library movies with their state, " +
			"file quality and size; the others missing or not released. The films are read from TMDB when it answers (then saved for " +
			"the list), else from the last read (partsError says why). movies and parts are the raw library movies and TMDB parts.",
		Tags: tags,
	}, func(ctx context.Context, in *IDParam) (*struct{ Body CollectionDetail }, error) {
		v, d, err := s.d.Library.CollectionView(ctx, in.ID)
		if err != nil {
			return nil, s.problem(ctx, err, false)
		}
		c := collectionOf(&d.Collection)
		for i := range d.Movies {
			c.Movies = append(c.Movies, movieOf(&d.Movies[i]))
		}
		c.Parts, c.PartsError = d.Parts, d.PartsError
		profiles, err := s.profileNames(ctx)
		if err != nil {
			return nil, s.problem(ctx, err, false)
		}
		out := CollectionDetail{Collection: c, CollectionExtras: s.collectionExtras(&v, profiles, s.liveProgress(ctx))}
		// Detail only: last search of the library movies, exclusions of the others.
		searched := make(map[int64]*int64, len(d.Movies))
		for i := range d.Movies {
			searched[d.Movies[i].ID] = d.Movies[i].LastSearchedAt
		}
		var outside []int
		for i := range out.Films {
			if f := &out.Films[i]; !f.InLibrary {
				outside = append(outside, f.TmdbID)
			}
		}
		flags, err := s.d.Library.FlagMovies(ctx, outside)
		if err != nil {
			return nil, s.problem(ctx, err, false)
		}
		for i := range out.Films {
			f := &out.Films[i]
			if f.MovieID != nil {
				f.LastSearchedAt = tsPtr(searched[*f.MovieID])
			} else {
				f.Excluded = flags[f.TmdbID].Excluded
			}
		}
		if rf := d.Collection.RootFolderID; rf != nil {
			if row, err := s.d.Store.Read().GetRootFolder(ctx, *rf); err == nil {
				out.RootFolderPath = &row.Path
			}
		}
		return &struct{ Body CollectionDetail }{out}, nil
	})
}
