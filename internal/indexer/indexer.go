package indexer

import (
	"context"
	"slices"
	"strings"
	"time"

	"github.com/fs0ciety7000/gonema/internal/decision"
	"github.com/fs0ciety7000/gonema/internal/parser"
)

// Indexer is a release search source. Implementations are safe for concurrent
// use; every method honors ctx cancellation and deadline.
type Indexer interface {
	// Info returns the identity of the indexer, stamped on every Release.
	Info() Info
	// Caps returns the capabilities of the indexer (search modes, supported
	// parameters, categories, limits). Implementations may cache them.
	Caps(ctx context.Context) (Caps, error)
	// Search returns the releases matching a movie query.
	Search(ctx context.Context, q Query) ([]Release, error)
	// RSS returns the latest releases of the movie categories, newest first when
	// the indexer sorts them so.
	RSS(ctx context.Context) ([]Release, error)
	// Test checks that the indexer is reachable, speaks the expected API and
	// accepts the credential. The error text is meant for the user.
	Test(ctx context.Context) error
}

// Info identifies an indexer configured in Gonema (indexers table).
type Info struct {
	ID       int64             `json:"id"`
	Name     string            `json:"name"`
	Priority int               `json:"priority"` // lower is preferred (1..50, default 25)
	Protocol decision.Protocol `json:"protocol"`
}

// DefaultPriority is the priority of an indexer without explicit setting.
const DefaultPriority = 25

// Query is a movie search. At least one of IMDbID, TMDbID and Title is required.
type Query struct {
	IMDbID string // "tt0137523"; the "tt" prefix is optional
	TMDbID int64
	Title  string // used for text search when no id search is possible
	Year   int    // appended to the text search; 0 if unknown

	// Categories restricts the search; empty uses the indexer's configured movie
	// categories.
	Categories []int
	// Limit is the page size; 0 uses the indexer's maximum. It is capped to the
	// maximum announced in the capabilities.
	Limit int
	// Offset is the index of the first result of the first page.
	Offset int
	// MaxPages is the number of pages fetched at most; 0 means 1.
	MaxPages int
}

// IMDbNumber returns the digits of an IMDb id ("tt0137523" -> "0137523"), or ""
// when id is not a valid IMDb title id.
func IMDbNumber(id string) string {
	id = strings.TrimSpace(strings.ToLower(id))
	id = strings.TrimPrefix(id, "tt")
	if len(id) < 1 || len(id) > 10 || strings.Trim(id, "0123456789") != "" {
		return ""
	}
	if len(id) < 7 {
		id = strings.Repeat("0", 7-len(id)) + id
	}
	return id
}

// NormalizeIMDb returns id in canonical "tt" + at least 7 digits form, or "".
func NormalizeIMDb(id string) string {
	if n := IMDbNumber(id); n != "" {
		return "tt" + n
	}
	return ""
}

// Indexer flags reported on releases. Indexers may report other tags verbatim
// (lower case).
const (
	FlagFreeleech    = "freeleech"    // download does not count against the ratio
	FlagHalfleech    = "halfleech"    // download counts partially (0 < factor < 1)
	FlagDoubleUpload = "doubleupload" // upload counts at least twice
	FlagInternal     = "internal"     // release group internal to the tracker
)

// Source is one indexer offering a release. A release found on several indexers
// has several sources.
type Source struct {
	IndexerID       int64  `json:"indexerId"`
	IndexerName     string `json:"indexerName"`
	IndexerPriority int    `json:"indexerPriority"`
	GUID            string `json:"guid"`
	DownloadURL     string `json:"downloadUrl"`
	InfoURL         string `json:"infoUrl,omitempty"`
}

// Release is one search result.
type Release struct {
	Title       string    `json:"title"`
	GUID        string    `json:"guid"` // unique within the indexer
	Size        int64     `json:"size"` // bytes; 0 if unknown
	InfoURL     string    `json:"infoUrl,omitempty"`
	DownloadURL string    `json:"downloadUrl"` // NZB, .torrent or magnet URI
	MagnetURL   string    `json:"magnetUrl,omitempty"`
	PublishDate time.Time `json:"publishDate"` // Usenet post date when known; zero if unknown

	IndexerID       int64             `json:"indexerId"`
	IndexerName     string            `json:"indexerName"`
	IndexerPriority int               `json:"indexerPriority"`
	Protocol        decision.Protocol `json:"protocol"`

	Seeders  int    `json:"seeders"`            // -1 if unknown
	Leechers int    `json:"leechers"`           // -1 if unknown
	Grabs    int    `json:"grabs"`              // -1 if unknown
	InfoHash string `json:"infoHash,omitempty"` // lower-case hexadecimal (torrents)

	// DownloadVolumeFactor and UploadVolumeFactor are the Torznab ratio
	// multipliers; 1 when unknown. Freeleech is a download factor of 0.
	DownloadVolumeFactor float64       `json:"downloadVolumeFactor"`
	UploadVolumeFactor   float64       `json:"uploadVolumeFactor"`
	MinimumRatio         float64       `json:"minimumRatio,omitempty"`
	MinimumSeedTime      time.Duration `json:"minimumSeedTime,omitempty"`

	Flags      []string `json:"flags,omitempty"` // FlagFreeleech, FlagInternal, other tags
	Categories []int    `json:"categories,omitempty"`
	IMDbID     string   `json:"imdbId,omitempty"` // "tt0137523"
	TMDbID     int64    `json:"tmdbId,omitempty"`

	// Sources lists the indexers offering this release when the search merged
	// duplicates (internal/search); empty for a single source.
	Sources []Source `json:"sources,omitempty"`
}

// Source returns the indexer source of r.
func (r *Release) Source() Source {
	return Source{
		IndexerID: r.IndexerID, IndexerName: r.IndexerName, IndexerPriority: r.IndexerPriority,
		GUID: r.GUID, DownloadURL: r.DownloadURL, InfoURL: r.InfoURL,
	}
}

// HasFlag reports whether r carries flag (case-insensitive).
func (r *Release) HasFlag(flag string) bool {
	return slices.ContainsFunc(r.Flags, func(f string) bool { return strings.EqualFold(f, flag) })
}

// Candidate converts r for the decision engine; Title is parsed with
// parser.Parse.
func (r *Release) Candidate() decision.Candidate {
	return decision.Candidate{
		Title:           r.Title,
		Parsed:          parser.Parse(r.Title),
		Size:            r.Size,
		IndexerID:       r.IndexerID,
		IndexerName:     r.IndexerName,
		IndexerPriority: r.IndexerPriority,
		Protocol:        r.Protocol,
		Seeders:         r.Seeders,
		Leechers:        r.Leechers,
		PublishDate:     r.PublishDate,
		IndexerFlags:    slices.Clone(r.Flags),
		DownloadURL:     r.DownloadURL,
		GUID:            r.GUID,
		InfoHash:        r.InfoHash,
	}
}

// SearchMode is one search function of an indexer (search, movie-search).
type SearchMode struct {
	Available bool     `json:"available"`
	Params    []string `json:"params"` // lower case, e.g. "q", "imdbid", "tmdbid"
}

// Supports reports whether the mode is available and accepts param.
func (m SearchMode) Supports(param string) bool {
	return m.Available && slices.Contains(m.Params, strings.ToLower(param))
}

// Category is an indexer category with its subcategories.
type Category struct {
	ID      int        `json:"id"`
	Name    string     `json:"name"`
	Subcats []Category `json:"subcats,omitempty"`
}

// MoviesCategory is the Newznab parent category of movies; 2000-2999 is the
// movie range.
const MoviesCategory = 2000

// IsMovieCategory reports whether id is in the standard Newznab movie range.
func IsMovieCategory(id int) bool { return id >= 2000 && id <= 2999 }

// Caps are the capabilities of an indexer (Newznab t=caps).
type Caps struct {
	ServerTitle   string     `json:"serverTitle,omitempty"`
	Version       string     `json:"version,omitempty"`
	LimitMax      int        `json:"limitMax"`     // 0 if unknown
	LimitDefault  int        `json:"limitDefault"` // 0 if unknown
	RetentionDays int        `json:"retentionDays,omitempty"`
	Search        SearchMode `json:"search"`
	MovieSearch   SearchMode `json:"movieSearch"`
	Categories    []Category `json:"categories,omitempty"`
	Tags          []string   `json:"tags,omitempty"`
}

// MovieCategories returns the ids of the movie categories and subcategories
// announced in the capabilities (standard 2000-2999 range), in announce order.
func (c *Caps) MovieCategories() []int {
	var ids []int
	for _, cat := range c.Categories {
		if IsMovieCategory(cat.ID) {
			ids = append(ids, cat.ID)
		}
		for _, sub := range cat.Subcats {
			if IsMovieCategory(sub.ID) && !slices.Contains(ids, sub.ID) {
				ids = append(ids, sub.ID)
			}
		}
	}
	return ids
}
