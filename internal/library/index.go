package library

import (
	"cmp"
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"slices"
	"strconv"
	"strings"
	"sync"

	"github.com/fs0ciety7000/gonema/internal/metadata"
	"github.com/fs0ciety7000/gonema/internal/quality"
	"github.com/fs0ciety7000/gonema/internal/store"
	"github.com/fs0ciety7000/gonema/internal/store/db"
)

// Sort keys of ListMovies.
const (
	SortTitleAsc = "title"   // sort title, A to Z by default
	SortYearDesc = "year"    // newest first by default
	SortAdded    = "added"   // most recently added first by default
	SortSize     = "size"    // largest file first by default; no file counts as smallest
	SortScore    = "score"   // best custom score first by default; no file counts as lowest
	SortQuality  = "quality" // best file quality (catalog rank) first by default; no file lowest
)

// Sort directions.
const (
	OrderAsc  = "asc"
	OrderDesc = "desc"
)

// Library states of the list filters: the movie_state values plus two derived
// ones.
const (
	StateAvailable   = "available"
	StateMissing     = "missing"
	StateDownloading = "downloading"
	StateUpgradable  = "upgradable"
	StateUnmonitored = "unmonitored" // monitored = false, whatever the file state
	StateFailed      = "failed"      // the newest download failed after the current file (if any)
)

// MaxListLimit is the largest page of ListMovies.
const MaxListLimit = 500

func defaultOrder(sort string) string {
	if sort == SortTitleAsc {
		return OrderAsc
	}
	return OrderDesc
}

func validSort(s string) bool {
	switch s {
	case SortTitleAsc, SortYearDesc, SortAdded, SortSize, SortScore, SortQuality:
		return true
	}
	return false
}

func validState(s string) bool {
	switch s {
	case StateAvailable, StateMissing, StateDownloading, StateUpgradable, StateUnmonitored, StateFailed:
		return true
	}
	return false
}

// ListFilter selects movies; zero fields do not filter.
type ListFilter struct {
	Monitored    *bool
	Status       string   // release stage: announced, in_cinemas, released, deleted
	States       []string // library states, any of (Available, Missing...)
	Quality      string   // quality ID of the current file
	YearFrom     int
	YearTo       int
	Genre        string
	RootFolderID int64
	Tag          string // tag label
	Query        string // substring of the title, original title or an alternate title
}

// ListQuery is a list request. Cursor is the opaque NextCursor of the previous
// page ("" for the first page).
type ListQuery struct {
	Filter ListFilter
	Sort   string // SortTitleAsc (default), SortYearDesc, SortAdded, SortSize, SortScore, SortQuality
	Order  string // OrderAsc or OrderDesc; "" = the default of Sort
	Cursor string
	Limit  int  // 1-500, default 100; ignored when All
	All    bool // every matching movie in one page, no cursor
}

// MovieSummary is a movie row of a list.
type MovieSummary struct {
	db.ListLibraryIndexRow
	// LastDownloadFailed: the newest download failed after the current file
	// (if any) was imported, and none is active.
	LastDownloadFailed bool
}

// MoviePage is one page of movies. The rows are shared with the in-memory
// index: callers must not modify them.
type MoviePage struct {
	Movies     []*MovieSummary
	NextCursor string // "" on the last page
	Total      int    // movies matching the filter, all pages included
}

// cursor is the keyset position, encoded as JSON then base64 (opaque to clients).
type cursor struct {
	Sort  string `json:"s"`
	Order string `json:"o"`
	Title string `json:"t,omitempty"`
	Num   int64  `json:"n,omitempty"` // year, added_at, size, score or rank
	ID    int64  `json:"i"`
}

func encodeCursor(c cursor) string {
	raw, err := json.Marshal(c)
	if err != nil { // cannot happen: plain struct
		return ""
	}
	return base64.RawURLEncoding.EncodeToString(raw)
}

// decodeCursor parses a page cursor; "" is the first page.
func decodeCursor(s, sort, order string) (cursor, bool, error) {
	if s == "" {
		return cursor{}, false, nil
	}
	bad := invalid("cursor", "the page cursor is invalid or belongs to another sort order; start again from the first page")
	if len(s) > 512 {
		return cursor{}, false, bad
	}
	raw, err := base64.RawURLEncoding.DecodeString(s)
	if err != nil {
		return cursor{}, false, bad
	}
	var c cursor
	if err := json.Unmarshal(raw, &c); err != nil || c.Sort != sort || c.Order != order || c.ID <= 0 {
		return cursor{}, false, bad
	}
	return c, true, nil
}

// QualityRank is the catalog rank of a quality ID, -1 when unknown or empty.
func QualityRank(id *string) int64 {
	if id == nil {
		return -1
	}
	q, ok := quality.Lookup(quality.ID(*id))
	if !ok {
		return -1
	}
	return int64(q.Rank)
}

// sortKey is the numeric key of a row for the numeric sorts.
func sortKey(sort string, m *MovieSummary) int64 {
	switch sort {
	case SortYearDesc:
		return m.Year
	case SortAdded:
		return m.AddedAt
	case SortSize:
		if m.FileSize == nil {
			return -1
		}
		return *m.FileSize
	case SortScore:
		if m.FileScore == nil {
			return math.MinInt64
		}
		return *m.FileScore
	case SortQuality:
		return QualityRank(m.FileQuality)
	}
	return 0
}

// compareKey orders a row against a key in ascending order (title, num, id).
func compareKey(sort string, m *MovieSummary, title string, num, id int64) int {
	if sort == SortTitleAsc {
		if c := strings.Compare(m.SortTitle, title); c != 0 {
			return c
		}
	} else if c := cmp.Compare(sortKey(sort, m), num); c != 0 {
		return c
	}
	return cmp.Compare(m.ID, id)
}

// filter is a validated ListFilter, ready to test cached rows.
type filter struct {
	monitored        *bool
	status           string
	states           []string
	quality          string
	yearMin, yearMax int64
	genre            string // JSON-encoded, quotes included: matched in the genres array text
	rootFolderID     int64
	tag              string
	query            string // lower case
}

func (fl *ListFilter) compile() (filter, error) {
	f := filter{monitored: fl.Monitored, yearMin: 0, yearMax: 9999, rootFolderID: fl.RootFolderID}
	switch fl.Status {
	case "", "announced", "in_cinemas", "released", "deleted":
		f.status = fl.Status
	default:
		return f, invalid("status", "status must be announced, in_cinemas, released or deleted")
	}
	for _, st := range fl.States {
		if !validState(st) {
			return f, invalid("state", "state must be available, missing, downloading, upgradable, unmonitored or failed")
		}
	}
	f.states = fl.States
	if fl.Quality != "" && !quality.ID(fl.Quality).Valid() {
		return f, invalid("quality", "%q is not a known quality", fl.Quality)
	}
	f.quality = fl.Quality
	if fl.YearFrom > 0 {
		f.yearMin = int64(fl.YearFrom)
	}
	if fl.YearTo > 0 {
		f.yearMax = int64(fl.YearTo)
	}
	if f.yearMin > f.yearMax {
		return f, invalid("yearFrom", "the year range is empty: yearFrom is after yearTo")
	}
	if fl.Genre != "" {
		g, err := json.Marshal(fl.Genre)
		if err != nil {
			return f, invalid("genre", "the genre is not valid text")
		}
		f.genre = string(g)
	}
	f.tag = strings.TrimSpace(fl.Tag)
	if q := strings.TrimSpace(fl.Query); q != "" {
		if len(q) > 200 {
			return f, invalid("q", "the search text is longer than 200 characters")
		}
		f.query = asciiLower(q)
	}
	return f, nil
}

// asciiLower folds ASCII letters only, like SQLite lower(), so that Go and SQL
// matching agree.
func asciiLower(s string) string {
	return strings.Map(func(r rune) rune {
		if 'A' <= r && r <= 'Z' {
			return r + ('a' - 'A')
		}
		return r
	}, s)
}

// matchesStates reports whether a row has one of the states (none = all).
func matchesStates(m *MovieSummary, states []string) bool {
	if len(states) == 0 {
		return true
	}
	for _, st := range states {
		switch st {
		case StateUnmonitored:
			if !m.Monitored {
				return true
			}
		case StateFailed:
			if m.LastDownloadFailed {
				return true
			}
		default:
			if m.State == st {
				return true
			}
		}
	}
	return false
}

// libraryIndex is the in-memory index of every movie, valid for one value of
// library_version.
type libraryIndex struct {
	version int64
	rows    []MovieSummary    // id order; shared, never modified
	tags    map[int64][]int64 // movie id -> tag ids

	mu     sync.Mutex         // guards orders
	orders map[string][]int32 // sort+order -> every row index, sorted; computed on first use
}

// ordered returns the indexes of every row in the sort order, computing it
// once per index version: a request then only filters.
func (x *libraryIndex) ordered(sort string, sign int) []int32 {
	k := sort + strconv.Itoa(sign)
	x.mu.Lock()
	defer x.mu.Unlock()
	if o, ok := x.orders[k]; ok {
		return o
	}
	o := make([]int32, len(x.rows))
	for i := range o {
		o[i] = int32(i)
	}
	sortIndex(x.rows, o, sort, sign)
	if x.orders == nil {
		x.orders = map[string][]int32{}
	}
	x.orders[k] = o
	return o
}

// index returns the current index, reloading it when library_version moved.
// The version check is one tiny query; the reload reads every movie.
func (s *Service) index(ctx context.Context) (*libraryIndex, error) {
	v, err := s.st.Read().GetLibraryVersion(ctx)
	if err != nil {
		return nil, store.MapError(fmt.Errorf("library: index version: %w", err))
	}
	s.indexMu.Lock()
	defer s.indexMu.Unlock()
	if s.idx != nil && s.idx.version == v {
		return s.idx, nil
	}
	idx := &libraryIndex{tags: map[int64][]int64{}}
	err = s.st.ReadTx(ctx, func(q *db.Queries) error {
		var err error
		if idx.version, err = q.GetLibraryVersion(ctx); err != nil {
			return fmt.Errorf("version: %w", err)
		}
		rows, err := q.ListLibraryIndex(ctx)
		if err != nil {
			return fmt.Errorf("rows: %w", err)
		}
		failed, err := q.ListLastFailedDownloads(ctx)
		if err != nil {
			return fmt.Errorf("failed downloads: %w", err)
		}
		failedAt := make(map[int64]int64, len(failed))
		for _, f := range failed {
			failedAt[f.MovieID] = f.GrabbedAt
		}
		idx.rows = make([]MovieSummary, len(rows))
		for i := range rows {
			r := &idx.rows[i]
			r.ListLibraryIndexRow = rows[i]
			if at, ok := failedAt[r.ID]; ok && r.State != StateDownloading {
				r.LastDownloadFailed = r.FileImportedAt == nil || at > *r.FileImportedAt
			}
		}
		tags, err := q.ListAllMovieTags(ctx)
		if err != nil {
			return fmt.Errorf("tags: %w", err)
		}
		for _, t := range tags {
			idx.tags[t.MovieID] = append(idx.tags[t.MovieID], t.TagID)
		}
		return nil
	})
	if err != nil {
		return nil, store.MapError(fmt.Errorf("library: load the movie index: %w", err))
	}
	s.idx = idx
	return idx, nil
}

// ListMovies returns one page of movies, or every movie with All. Rows come
// from the in-memory index (see index); filters, sort and keyset pagination run
// in memory, so every sort order (quality rank included) pages the same way and
// stays stable under inserts.
func (s *Service) ListMovies(ctx context.Context, lq ListQuery) (MoviePage, error) {
	if lq.Sort == "" {
		lq.Sort = SortTitleAsc
	}
	if !validSort(lq.Sort) {
		return MoviePage{}, invalid("sort", "sort must be title, year, added, size, score or quality")
	}
	if lq.Order == "" {
		lq.Order = defaultOrder(lq.Sort)
	}
	if lq.Order != OrderAsc && lq.Order != OrderDesc {
		return MoviePage{}, invalid("order", "order must be asc or desc")
	}
	if lq.Limit <= 0 {
		lq.Limit = 100
	}
	if lq.Limit > MaxListLimit && !lq.All {
		return MoviePage{}, invalid("limit", "limit must be %d or less", MaxListLimit)
	}
	f, err := lq.Filter.compile()
	if err != nil {
		return MoviePage{}, err
	}
	c, hasCursor, err := decodeCursor(lq.Cursor, lq.Sort, lq.Order)
	if err != nil {
		return MoviePage{}, err
	}
	if lq.All && hasCursor {
		return MoviePage{}, invalid("cursor", "a cursor cannot be combined with the full list")
	}
	idx, err := s.index(ctx)
	if err != nil {
		return MoviePage{}, err
	}
	var tagID int64 = -1
	if f.tag != "" {
		t, err := s.st.Read().GetTagByLabel(ctx, f.tag)
		switch err = store.MapError(err); {
		case err == nil:
			tagID = t.ID
		case errors.Is(err, store.ErrNotFound):
			return MoviePage{Movies: []*MovieSummary{}}, nil
		default:
			return MoviePage{}, fmt.Errorf("library: tag %q: %w", f.tag, err)
		}
	}
	var altMatches map[int64]bool
	if f.query != "" {
		ids, err := s.st.Read().SearchAlternateTitleMovies(ctx, f.query)
		if err != nil {
			return MoviePage{}, store.MapError(fmt.Errorf("library: search alternate titles: %w", err))
		}
		altMatches = make(map[int64]bool, len(ids))
		for _, id := range ids {
			altMatches[id] = true
		}
	}

	sign := 1
	if lq.Order == OrderDesc {
		sign = -1
	}
	sel := make([]int32, 0, len(idx.rows))
	for _, i := range idx.ordered(lq.Sort, sign) {
		m := &idx.rows[i]
		if f.monitored != nil && m.Monitored != *f.monitored ||
			f.status != "" && m.Status != f.status ||
			f.rootFolderID > 0 && m.RootFolderID != f.rootFolderID ||
			m.Year < f.yearMin || m.Year > f.yearMax ||
			f.quality != "" && (m.FileQuality == nil || *m.FileQuality != f.quality) ||
			f.genre != "" && !strings.Contains(m.Genres, f.genre) ||
			!matchesStates(m, f.states) ||
			tagID >= 0 && !slices.Contains(idx.tags[m.ID], tagID) {
			continue
		}
		if f.query != "" && !altMatches[m.ID] &&
			!strings.Contains(asciiLower(m.Title), f.query) && !strings.Contains(asciiLower(m.OriginalTitle), f.query) {
			continue
		}
		sel = append(sel, i)
	}

	page := MoviePage{Total: len(sel)}
	start, end := 0, len(sel)
	if !lq.All {
		if hasCursor {
			// First row strictly after the cursor in the sort order.
			start, _ = slices.BinarySearchFunc(sel, c, func(i int32, c cursor) int {
				if sign*compareKey(lq.Sort, &idx.rows[i], c.Title, c.Num, c.ID) <= 0 {
					return -1
				}
				return 1
			})
		}
		end = min(start+lq.Limit, len(sel))
		if end < len(sel) {
			last := &idx.rows[sel[end-1]]
			next := cursor{Sort: lq.Sort, Order: lq.Order, ID: last.ID}
			if lq.Sort == SortTitleAsc {
				next.Title = last.SortTitle
			} else {
				next.Num = sortKey(lq.Sort, last)
			}
			page.NextCursor = encodeCursor(next)
		}
	}
	page.Movies = make([]*MovieSummary, end-start)
	for i, j := range sel[start:end] {
		page.Movies[i] = &idx.rows[j]
	}
	return page, nil
}

// sortIndex sorts the selected row indexes by key then id, ascending (sign 1)
// or descending (-1). Keys are computed once (the quality rank is a catalog
// lookup).
func sortIndex(rows []MovieSummary, sel []int32, sort string, sign int) {
	type entry struct {
		title string
		num   int64
		id    int64
		idx   int32
	}
	perm := make([]entry, len(sel))
	for i, j := range sel {
		m := &rows[j]
		perm[i] = entry{id: m.ID, idx: j}
		if sort == SortTitleAsc {
			perm[i].title = m.SortTitle
		} else {
			perm[i].num = sortKey(sort, m)
		}
	}
	slices.SortFunc(perm, func(a, b entry) int {
		c := strings.Compare(a.title, b.title)
		if c == 0 {
			c = cmp.Compare(a.num, b.num)
		}
		if c == 0 {
			c = cmp.Compare(a.id, b.id)
		}
		return sign * c
	})
	for i, e := range perm {
		sel[i] = e.idx
	}
}

// Counts are the counts of the library header and tabs.
type Counts struct {
	Total, OnDisk, TotalSize, Monitored, Unmonitored int64
	Available, Missing, Downloading, Upgradable      int64
	// Wanted: monitored and missing, and released enough for the minimum
	// availability (the release stage is derived at metadata refresh; release
	// delays are not applied).
	Wanted int64
	// CutoffUnmet: monitored and upgradable.
	CutoffUnmet int64
}

// meetsAvailability reports whether a release stage satisfies a minimum availability.
func meetsAvailability(status, minimum string) bool {
	switch minimum {
	case AvailAnnounced:
		return status != string(metadata.StatusDeleted)
	case AvailInCinemas:
		return status == string(metadata.StatusInCinemas) || status == string(metadata.StatusReleased)
	}
	return status == string(metadata.StatusReleased)
}

// Stats returns the library counts, from the in-memory index.
func (s *Service) Stats(ctx context.Context) (Counts, error) {
	idx, err := s.index(ctx)
	if err != nil {
		return Counts{}, err
	}
	var c Counts
	for i := range idx.rows {
		m := &idx.rows[i]
		c.Total++
		if m.FileSize != nil {
			c.OnDisk++
			c.TotalSize += *m.FileSize
		}
		if m.Monitored {
			c.Monitored++
		} else {
			c.Unmonitored++
		}
		switch m.State {
		case StateAvailable:
			c.Available++
		case StateMissing:
			c.Missing++
			if m.Monitored && meetsAvailability(m.Status, m.MinimumAvailability) {
				c.Wanted++
			}
		case StateDownloading:
			c.Downloading++
		case StateUpgradable:
			c.Upgradable++
			if m.Monitored {
				c.CutoffUnmet++
			}
		}
	}
	return c, nil
}
