package library

import (
	"cmp"
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"slices"
	"strings"
	"time"

	"github.com/fs0ciety7000/gonema/internal/metadata"
	"github.com/fs0ciety7000/gonema/internal/store"
	"github.com/fs0ciety7000/gonema/internal/store/db"
)

// The collections page: every collection with at least one library movie,
// with its films in release order and what is missing. Library movies come
// from the in-memory index; the films of a collection that are not in the
// library come from the snapshot of its TMDB parts (collections.parts),
// refreshed each time Gonema reads the collection from TMDB (detail page,
// discover, add, monitor, and the collections refresh job): the list never
// calls TMDB.

// PartSnapshot is one film of a collection as last read from TMDB.
type PartSnapshot struct {
	TmdbID      int    `json:"tmdbId"`
	Title       string `json:"title"`
	Year        int    `json:"year,omitempty"`
	ReleaseDate string `json:"releaseDate,omitempty"` // YYYY-MM-DD
	PosterPath  string `json:"posterPath,omitempty"`
}

// snapshotOf keeps the parts worth listing (adult titles are dropped).
func snapshotOf(parts []metadata.MovieSummary) []PartSnapshot {
	out := make([]PartSnapshot, 0, len(parts))
	for i := range parts {
		p := &parts[i]
		if p.Adult || p.TMDBID <= 0 {
			continue
		}
		ps := PartSnapshot{TmdbID: p.TMDBID, Title: p.Title, Year: p.Year, PosterPath: p.PosterPath}
		if !p.ReleaseDate.IsZero() {
			ps.ReleaseDate = p.ReleaseDate.String()
		}
		out = append(out, ps)
	}
	return out
}

// saveParts stores the parts snapshot of a collection. Failures are logged:
// the snapshot only serves the list.
func (s *Service) saveParts(ctx context.Context, collectionID int64, parts []metadata.MovieSummary) {
	raw, err := json.Marshal(snapshotOf(parts))
	if err != nil {
		s.log.Error("encode collection parts", slog.Int64("collection_id", collectionID), slog.Any("error", err))
		return
	}
	now := store.Millis(s.now())
	if err := s.st.Write().SetCollectionParts(context.WithoutCancel(ctx), db.SetCollectionPartsParams{
		ID: collectionID, Parts: ptr(string(raw)), PartsRefreshedAt: &now,
	}); err != nil {
		s.log.Error("save collection parts", slog.Int64("collection_id", collectionID), slog.Any("error", store.MapError(err)))
	}
}

func ptr[T any](v T) *T { return &v }

// decodeParts reads a parts snapshot; ok is false when there is none.
func decodeParts(raw *string) (parts []PartSnapshot, ok bool) {
	if raw == nil {
		return nil, false
	}
	if err := json.Unmarshal([]byte(*raw), &parts); err != nil {
		return nil, false
	}
	return parts, true
}

// Film states of a collection.
const (
	PartOnDisk      = "on_disk"      // in the library, with a file
	PartDownloading = "downloading"  // in the library, downloading
	PartWanted      = "wanted"       // in the library, no file, released (or in cinemas)
	PartUpcoming    = "upcoming"     // in the library, announced
	PartMissing     = "missing"      // not in the library, released
	PartNotReleased = "not_released" // not in the library, not released yet
)

// CollectionFilm is one film of a collection.
type CollectionFilm struct {
	TmdbID      int
	Title       string
	Year        int
	ReleaseDate string // TMDB release date of the part, YYYY-MM-DD; "" when unknown
	PosterPath  string
	State       string // PartOnDisk...
	Released    bool
	// Movie is the library movie (shared with the in-memory index, read
	// only); nil when the film is not in the library.
	Movie *MovieSummary
}

// CollectionCounts summarizes the films of a collection.
type CollectionCounts struct {
	Total, InLibrary, OnDisk, Downloading, Wanted int
	// NotInLibrary: every film not in the library; Missing: of those, the
	// released ones (an upcoming film is never "missing": a monitored
	// collection adds it on release day).
	NotInLibrary, Missing int
	// Upcoming: not released yet, in the library or not.
	Upcoming         int
	SizeOnDisk       int64
	YearFrom, YearTo int // 0 when unknown
}

// HasMissing reports whether the collection misses released films.
func (c *CollectionCounts) HasMissing() bool { return c.Missing > 0 }

// CollectionView is a collection with its films.
type CollectionView struct {
	Collection db.Collection
	Films      []CollectionFilm // release order, undated last
	// PartsKnown: the TMDB parts are known (snapshot or live); without them
	// only the library movies are listed.
	PartsKnown bool
	Counts     CollectionCounts
	LastAdded  int64 // added_at of the newest library movie
}

// buildView merges the TMDB parts and the library movies of a collection.
// today is YYYY-MM-DD. Pure.
func buildView(c db.Collection, parts []PartSnapshot, known bool, movies []*MovieSummary, today string) CollectionView {
	v := CollectionView{Collection: c, PartsKnown: known}
	byTmdb := make(map[int64]*MovieSummary, len(movies))
	for _, m := range movies {
		byTmdb[m.TmdbID] = m
		v.LastAdded = max(v.LastAdded, m.AddedAt)
	}
	seen := make(map[int]bool, len(parts))
	for _, p := range parts {
		if seen[p.TmdbID] {
			continue
		}
		seen[p.TmdbID] = true
		f := CollectionFilm{TmdbID: p.TmdbID, Title: p.Title, Year: p.Year, ReleaseDate: p.ReleaseDate, PosterPath: p.PosterPath}
		f.Movie = byTmdb[int64(p.TmdbID)]
		v.Films = append(v.Films, f)
	}
	for _, m := range movies { // library movies the snapshot does not list
		if !seen[int(m.TmdbID)] {
			f := CollectionFilm{TmdbID: int(m.TmdbID), Title: m.Title, Year: int(m.Year), Movie: m}
			if m.ReleaseCinema != nil {
				f.ReleaseDate = *m.ReleaseCinema
			}
			v.Films = append(v.Films, f)
		}
	}
	for i := range v.Films {
		f := &v.Films[i]
		if m := f.Movie; m != nil {
			f.Title, f.Year = m.Title, int(m.Year)
			if f.PosterPath == "" && m.PosterPath != nil {
				f.PosterPath = *m.PosterPath
			}
			f.Released = m.Status != "announced" && m.Status != "deleted"
			switch {
			case m.FileSize != nil:
				f.State = PartOnDisk
			case m.State == StateDownloading:
				f.State = PartDownloading
			case m.Status == "announced":
				f.State = PartUpcoming
			default:
				f.State = PartWanted
			}
		} else {
			f.Released = f.ReleaseDate != "" && f.ReleaseDate <= today
			f.State = PartNotReleased
			if f.Released {
				f.State = PartMissing
			}
		}
		v.Counts.add(f)
	}
	slices.SortStableFunc(v.Films, func(a, b CollectionFilm) int {
		ka, kb := filmKey(&a), filmKey(&b)
		switch {
		case ka == "" && kb != "":
			return 1
		case kb == "" && ka != "":
			return -1
		}
		return cmp.Compare(ka, kb)
	})
	return v
}

// filmKey orders the films: release date, else year; "" sorts last.
func filmKey(f *CollectionFilm) string {
	if f.ReleaseDate != "" {
		return f.ReleaseDate
	}
	if f.Year > 0 {
		return fmt.Sprintf("%04d-99", f.Year)
	}
	return ""
}

func (c *CollectionCounts) add(f *CollectionFilm) {
	c.Total++
	if f.Movie != nil {
		c.InLibrary++
		if f.Movie.FileSize != nil {
			c.SizeOnDisk += *f.Movie.FileSize
		}
	} else {
		c.NotInLibrary++
		if f.Released {
			c.Missing++
		}
	}
	switch f.State {
	case PartOnDisk:
		c.OnDisk++
	case PartDownloading:
		c.Downloading++
	case PartWanted:
		c.Wanted++
	}
	if !f.Released {
		c.Upcoming++
	}
	if f.Year > 0 {
		if c.YearFrom == 0 || f.Year < c.YearFrom {
			c.YearFrom = f.Year
		}
		c.YearTo = max(c.YearTo, f.Year)
	}
}

// Sort keys of ListCollections.
const (
	CollectionSortName    = "name"    // A to Z
	CollectionSortMissing = "missing" // most missing films first
	CollectionSortYear    = "year"    // newest film first
	CollectionSortAdded   = "added"   // most recently added library movie first
)

// MaxCollectionPage is the largest page of ListCollections.
const MaxCollectionPage = 200

// CollectionQuery selects collections.
type CollectionQuery struct {
	Sort      string // CollectionSort*, default name
	Monitored *bool
	Missing   bool   // only collections that miss films (CollectionCounts.HasMissing)
	Query     string // substring of the name, case-insensitive (ASCII)
	Page      int    // 1-based, default 1
	PageSize  int    // default 50, at most MaxCollectionPage
}

// CollectionTotals are the counts of the collections page header and filters,
// over every collection with a library movie (filters not applied).
type CollectionTotals struct {
	Collections, Monitored, WithMissing int
	Films, NotInLibrary, Upcoming       int
}

// CollectionPage is a page of collections.
type CollectionPage struct {
	Collections []CollectionView
	Total       int // collections matching the filters
	Totals      CollectionTotals
}

// ListCollections lists the collections that have at least one library movie.
func (s *Service) ListCollections(ctx context.Context, q CollectionQuery) (CollectionPage, error) {
	switch q.Sort {
	case "":
		q.Sort = CollectionSortName
	case CollectionSortName, CollectionSortMissing, CollectionSortYear, CollectionSortAdded:
	default:
		return CollectionPage{}, invalid("sort", "sort must be name, missing, year or added")
	}
	if q.Page <= 0 {
		q.Page = 1
	}
	if q.PageSize <= 0 {
		q.PageSize = 50
	}
	if q.PageSize > MaxCollectionPage {
		return CollectionPage{}, invalid("pageSize", "pageSize must be %d or less", MaxCollectionPage)
	}
	text := asciiLower(strings.TrimSpace(q.Query))
	if len(text) > 200 {
		return CollectionPage{}, invalid("q", "the search text is longer than 200 characters")
	}
	idx, err := s.index(ctx)
	if err != nil {
		return CollectionPage{}, err
	}
	byCollection := map[int64][]*MovieSummary{}
	for i := range idx.rows {
		if m := &idx.rows[i]; m.CollectionID != nil {
			byCollection[*m.CollectionID] = append(byCollection[*m.CollectionID], m)
		}
	}
	page := CollectionPage{Collections: []CollectionView{}}
	if len(byCollection) == 0 {
		return page, nil
	}
	rows, err := s.st.Read().ListCollections(ctx)
	if err != nil {
		return CollectionPage{}, store.MapError(fmt.Errorf("library: list collections: %w", err))
	}
	today := s.now().Format(dateLayout)
	var sel []CollectionView
	for _, c := range rows {
		movies := byCollection[c.ID]
		if len(movies) == 0 {
			continue
		}
		parts, known := decodeParts(c.Parts)
		v := buildView(c, parts, known, movies, today)
		t := &page.Totals
		t.Collections++
		t.Films += v.Counts.Total
		t.NotInLibrary += v.Counts.NotInLibrary
		t.Upcoming += v.Counts.Upcoming
		if c.Monitored {
			t.Monitored++
		}
		missing := v.Counts.HasMissing()
		if missing {
			t.WithMissing++
		}
		if q.Monitored != nil && c.Monitored != *q.Monitored || q.Missing && !missing ||
			text != "" && !strings.Contains(asciiLower(c.Name), text) {
			continue
		}
		sel = append(sel, v)
	}
	slices.SortStableFunc(sel, func(a, b CollectionView) int {
		byName := cmp.Or(cmp.Compare(asciiLower(a.Collection.Name), asciiLower(b.Collection.Name)), cmp.Compare(a.Collection.ID, b.Collection.ID))
		switch q.Sort {
		case CollectionSortMissing:
			return cmp.Or(cmp.Compare(b.Counts.Missing, a.Counts.Missing), byName)
		case CollectionSortYear:
			return cmp.Or(cmp.Compare(b.Counts.YearTo, a.Counts.YearTo), byName)
		case CollectionSortAdded:
			return cmp.Or(cmp.Compare(b.LastAdded, a.LastAdded), byName)
		}
		return byName
	})
	page.Total = len(sel)
	start := min((q.Page-1)*q.PageSize, len(sel))
	end := min(start+q.PageSize, len(sel))
	page.Collections = append(page.Collections, sel[start:end]...)
	return page, nil
}

// CollectionView returns one collection with its films. The parts are read
// from TMDB when a key is configured (and saved as the snapshot), else from
// the snapshot; PartsError says why TMDB was not used.
func (s *Service) CollectionView(ctx context.Context, id int64) (CollectionView, CollectionDetails, error) {
	d, err := s.GetCollection(ctx, id)
	if err != nil {
		return CollectionView{}, CollectionDetails{}, err
	}
	idx, err := s.index(ctx)
	if err != nil {
		return CollectionView{}, CollectionDetails{}, err
	}
	var movies []*MovieSummary
	for i := range idx.rows {
		if m := &idx.rows[i]; m.CollectionID != nil && *m.CollectionID == id {
			movies = append(movies, m)
		}
	}
	parts, known := decodeParts(d.Collection.Parts)
	if d.Parts != nil {
		parts, known = snapshotOf(d.Parts), true
	}
	return buildView(d.Collection, parts, known, movies, s.now().Format(dateLayout)), d, nil
}

// Collections job: refreshes the TMDB parts snapshots and adds the released
// films of monitored collections (a monitored collection adds an upcoming
// film on its release day, not before).
const (
	// JobCollectionsRefresh is the job kind.
	JobCollectionsRefresh = "collections.refresh"
	// CollectionsRefreshInterval is its period: a film released today is
	// added within this delay.
	CollectionsRefreshInterval = 6 * time.Hour
	// collectionsStaleAfter: a snapshot older than this is read again.
	collectionsStaleAfter = 24 * time.Hour
	// collectionsPerRun bounds the TMDB calls of one run.
	collectionsPerRun = 100
)

// RefreshCollections reads again the oldest parts snapshots from TMDB (at
// most collectionsPerRun per run), then adds to the library, for each
// monitored collection, its released films that are not in it yet (excluded
// films are skipped), with the collection's settings. Each addition is
// written to history (movie_added, with the collection as the reason).
func (s *Service) RefreshCollections(ctx context.Context) error {
	if p, err := s.Provider(); err == nil {
		rows, err := s.st.Read().ListCollectionsNeedingParts(ctx, db.ListCollectionsNeedingPartsParams{
			StaleBefore: ptr(store.Millis(s.now().Add(-collectionsStaleAfter))), Limit: collectionsPerRun,
		})
		if err != nil {
			return store.MapError(fmt.Errorf("library: collections to refresh: %w", err))
		}
		for _, r := range rows {
			if err := ctx.Err(); err != nil {
				return err
			}
			c, err := p.Collection(ctx, int(r.TmdbID))
			if err != nil {
				s.log.Warn("collection refresh failed", slog.Int64("collection_id", r.ID), slog.Any("error", err))
				continue
			}
			s.saveParts(ctx, r.ID, c.Parts)
		}
	}
	rows, err := s.st.Read().ListCollections(ctx)
	if err != nil {
		return store.MapError(fmt.Errorf("library: list collections: %w", err))
	}
	added := 0
	for i := range rows {
		c := &rows[i]
		if !c.Monitored || c.QualityProfileID == nil || c.RootFolderID == nil {
			continue
		}
		parts, ok := decodeParts(c.Parts)
		if !ok {
			continue
		}
		n, err := s.addReleasedParts(ctx, c, parts, fmt.Sprintf("Added automatically on release: the collection %s is monitored.", c.Name))
		added += n
		if err != nil {
			return err
		}
	}
	if added > 0 {
		s.log.Info("released films of monitored collections added", slog.Int("movies_added", added))
	}
	return nil
}

// releasedParts keeps the parts released on or before today (YYYY-MM-DD). Pure.
func releasedParts(parts []PartSnapshot, today string) (released, upcoming []PartSnapshot) {
	for _, p := range parts {
		if p.ReleaseDate != "" && p.ReleaseDate <= today {
			released = append(released, p)
		} else {
			upcoming = append(upcoming, p)
		}
	}
	return released, upcoming
}

// addReleasedParts adds the released parts of a monitored collection that are
// not in the library and not excluded; it returns how many were added.
func (s *Service) addReleasedParts(ctx context.Context, c *db.Collection, parts []PartSnapshot, reason string) (int, error) {
	released, _ := releasedParts(parts, s.now().Format(dateLayout))
	ids := make([]int, 0, len(released))
	for _, p := range released {
		ids = append(ids, p.TmdbID)
	}
	flags, err := s.FlagMovies(ctx, ids)
	if err != nil {
		return 0, err
	}
	added := 0
	for _, p := range released {
		if f := flags[p.TmdbID]; f.MovieID != 0 || f.Excluded {
			continue
		}
		_, created, err := s.AddMovie(ctx, p.TmdbID, AddOptions{
			RootFolderID: *c.RootFolderID, QualityProfileID: *c.QualityProfileID, MinimumAvailability: c.MinimumAvailability,
			Monitored: true, SearchOnAdd: c.SearchOnAdd, Reason: reason,
		})
		switch {
		case err != nil && ctx.Err() != nil:
			return added, ctx.Err()
		case err != nil:
			s.log.Warn("collection part not added", slog.Int64("collection_id", c.ID), slog.Int("tmdb_id", p.TmdbID), slog.Any("error", err))
		case created:
			added++
		}
	}
	return added, nil
}
