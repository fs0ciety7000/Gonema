// Package match finds which library movie a release name refers to.
//
// The package is pure: no I/O, no clock, no global mutable state. An Index is
// built once from the library (title, original title, alternate titles, year,
// IMDb id) and is then safe for concurrent use; rebuild it when the library
// changes.
//
// Titles are compared through Key (see normalize.go). Candidates carry a
// confidence in [0, 1] and a plain-language reason. The caller must not grab
// automatically unless Candidate.Confident is true: ambiguous candidates (same
// title and year, or no year to tell remakes apart) are returned with a low
// confidence for a human to choose.
package match

import (
	"fmt"
	"slices"
	"strings"

	"github.com/fs0ciety7000/gonema/internal/parser"
)

// AutoThreshold is the lowest confidence at which a candidate may be used
// without confirmation.
const AutoThreshold = 0.75

// Confidence scores. Penalties are subtracted from the title score.
const (
	scorePrimary     = 1.0  // the movie's title
	scoreOriginal    = 0.97 // its original title
	scoreAlternate   = 0.9  // one of its alternate titles
	penaltyAKA       = 0.05 // the match comes from the AKA part of the release name
	penaltyArticle   = 0.05 // equal only once a leading foreign article is dropped
	penaltyYearOff   = 0.15 // years differ by one
	scoreTitleUnique = 0.7  // no year to compare, only one movie has this title (× title score)
	maxAmbiguous     = 0.4  // cap for candidates that cannot be told apart
)

// Entry is one library movie.
type Entry struct {
	MovieID         int64
	Title           string
	OriginalTitle   string
	Year            int // 0 if unknown
	AlternateTitles []string
	IMDbID          string // "tt0137523", optional
}

// TitleKind says which title of the movie matched.
type TitleKind uint8

// Title kinds, best first.
const (
	TitlePrimary TitleKind = iota + 1
	TitleOriginal
	TitleAlternate
)

func (k TitleKind) String() string {
	switch k {
	case TitlePrimary:
		return "title"
	case TitleOriginal:
		return "original title"
	case TitleAlternate:
		return "alternate title"
	}
	return "unknown"
}

// Candidate is a library movie a release may be.
type Candidate struct {
	MovieID      int64     `json:"movieId"`
	Title        string    `json:"title"` // the movie's title
	Year         int       `json:"year,omitempty"`
	MatchedTitle string    `json:"matchedTitle"` // the title of the movie that matched
	Via          TitleKind `json:"via"`
	Confidence   float64   `json:"confidence"`
	Ambiguous    bool      `json:"ambiguous,omitempty"`
	Reason       string    `json:"reason"`
}

// Confident reports whether c may be used without confirmation.
func (c *Candidate) Confident() bool { return !c.Ambiguous && c.Confidence >= AutoThreshold }

type posting struct {
	entry int32
	kind  TitleKind
	full  string // key before the foreign article was dropped
	title string // the title as written in the library
}

type movie struct {
	id    int64
	title string
	year  int
}

// Index is an immutable lookup structure over the library.
type Index struct {
	movies []movie
	byKey  map[string][]posting
	byIMDb map[string]int32
}

// NewIndex builds an index. Entries with an empty title are skipped; for each
// movie and key only the best title kind is kept.
func NewIndex(entries []Entry) *Index {
	idx := &Index{
		movies: make([]movie, 0, len(entries)),
		byKey:  make(map[string][]posting, len(entries)*2),
		byIMDb: make(map[string]int32),
	}
	for i := range entries {
		e := &entries[i]
		if strings.TrimSpace(e.Title) == "" {
			continue
		}
		n := int32(len(idx.movies))
		idx.movies = append(idx.movies, movie{id: e.MovieID, title: e.Title, year: e.Year})
		if e.IMDbID != "" {
			idx.byIMDb[strings.ToLower(e.IMDbID)] = n
		}
		idx.add(n, TitlePrimary, e.Title)
		idx.add(n, TitleOriginal, e.OriginalTitle)
		for _, t := range e.AlternateTitles {
			idx.add(n, TitleAlternate, t)
		}
	}
	return idx
}

func (idx *Index) add(n int32, kind TitleKind, title string) {
	full, short := keys(title)
	if short == "" {
		return
	}
	// Index both keys so that a release with or without the leading foreign
	// article finds the movie ("Les Misérables" vs "Miserables", "L'Auberge"
	// vs "LAuberge").
	idx.addKey(short, posting{entry: n, kind: kind, full: full, title: title})
	if full != short {
		idx.addKey(full, posting{entry: n, kind: kind, full: full, title: title})
	}
}

func (idx *Index) addKey(key string, p posting) {
	ps := idx.byKey[key]
	for i := range ps {
		if ps[i].entry == p.entry {
			if p.kind < ps[i].kind {
				ps[i] = p
			}
			return
		}
	}
	idx.byKey[key] = append(ps, p)
}

// Len returns the number of indexed movies.
func (idx *Index) Len() int { return len(idx.movies) }

// MatchIMDb returns the movie with this IMDb id (given by some indexers), with
// full confidence.
func (idx *Index) MatchIMDb(imdbID string) []Candidate {
	n, ok := idx.byIMDb[strings.ToLower(strings.TrimSpace(imdbID))]
	if !ok {
		return nil
	}
	m := idx.movies[n]
	return []Candidate{{
		MovieID: m.id, Title: m.title, Year: m.year, MatchedTitle: m.title, Via: TitlePrimary,
		Confidence: 1, Reason: "IMDb id " + imdbID + " given by the indexer",
	}}
}

// scored is a candidate under construction.
type scored struct {
	entry     int32
	title     float64 // title score after penalties
	yearDiff  int     // -1: no year to compare
	p         posting
	fromAKA   bool
	articleOK bool // keys equal without dropping an article
}

// Match returns the library movies the release may be, best first (confidence,
// then movie id). It returns nil when nothing matches.
//
// Rules: the release title (and each AKA title) is looked up by key. With a year
// on both sides, the year must be equal (full score) or off by one (penalty);
// other years are rejected, which separates remakes. Without a year, a title
// shared by a single movie is accepted with a reduced confidence; a title shared
// by several movies yields all of them, ambiguous. Candidates tied at the top
// (same title and year) are all marked ambiguous with a capped confidence.
func (idx *Index) Match(p parser.ParsedRelease) []Candidate {
	var buf [8]scored
	found := buf[:0]
	title := p.Title
	if title == "" {
		title = p.CleanTitle
	}
	found = idx.lookup(found, title, p.Year, false)
	for _, aka := range p.AlternateTitles {
		found = idx.lookup(found, aka, p.Year, true)
	}
	if len(found) == 0 {
		return nil
	}

	out := make([]Candidate, 0, len(found))
	noYear := 0
	for i := range found {
		if found[i].yearDiff < 0 {
			noYear++
		}
	}
	for i := range found {
		s := &found[i]
		m := idx.movies[s.entry]
		c := Candidate{MovieID: m.id, Title: m.title, Year: m.year, MatchedTitle: s.p.title, Via: s.p.kind}
		var why []string
		switch {
		case s.yearDiff == 0:
			c.Confidence = s.title
			why = append(why, fmt.Sprintf("%s and year %d match", s.p.kind, m.year))
		case s.yearDiff == 1:
			c.Confidence = s.title - penaltyYearOff
			why = append(why, fmt.Sprintf("%s matches, year off by one (release %d, movie %d)", s.p.kind, p.Year, m.year))
		case noYear == 1 && len(found) == 1:
			c.Confidence = s.title * scoreTitleUnique
			why = append(why, fmt.Sprintf("%s matches, no year to compare; the only movie with this title", s.p.kind))
		default:
			c.Confidence = maxAmbiguous * s.title
			c.Ambiguous = true
			why = append(why, fmt.Sprintf("%s matches, no year to compare; %d movies share this title", s.p.kind, len(found)))
		}
		if s.p.kind != TitlePrimary {
			why = append(why, fmt.Sprintf("matched %q", s.p.title))
		}
		if s.fromAKA {
			why = append(why, "via the AKA part of the release name")
		}
		if !s.articleOK {
			why = append(why, "ignoring a leading article")
		}
		c.Reason = strings.Join(why, "; ")
		out = append(out, c)
	}
	slices.SortStableFunc(out, func(a, b Candidate) int {
		switch {
		case a.Confidence > b.Confidence:
			return -1
		case a.Confidence < b.Confidence:
			return 1
		case a.MovieID < b.MovieID:
			return -1
		case a.MovieID > b.MovieID:
			return 1
		}
		return 0
	})
	// Ties at the top cannot be decided automatically.
	if len(out) > 1 && !out[0].Ambiguous && out[1].Confidence == out[0].Confidence {
		top := out[0].Confidence
		n := 0
		for i := range out {
			if out[i].Confidence == top {
				n++
			}
		}
		for i := range n {
			out[i].Ambiguous = true
			out[i].Confidence = min(out[i].Confidence, maxAmbiguous)
			out[i].Reason += fmt.Sprintf("; %d movies match equally well, choose manually", n)
		}
	}
	for i := range out {
		out[i].Confidence = round2(out[i].Confidence)
	}
	return out
}

func round2(f float64) float64 { return float64(int(f*100+0.5)) / 100 }

// lookup appends the movies whose titles match title, keeping the best score per movie.
func (idx *Index) lookup(found []scored, title string, year int, fromAKA bool) []scored {
	full, short := keys(title)
	if short == "" {
		return found
	}
	found = idx.lookupKey(found, short, full, year, fromAKA)
	if full != short {
		found = idx.lookupKey(found, full, full, year, fromAKA)
	}
	return found
}

func (idx *Index) lookupKey(found []scored, key, full string, year int, fromAKA bool) []scored {
	for _, ps := range idx.byKey[key] {
		m := idx.movies[ps.entry]
		diff := -1
		if year > 0 && m.year > 0 {
			diff = year - m.year
			if diff < 0 {
				diff = -diff
			}
			if diff > 1 {
				continue // another movie with the same title (remake, homonym)
			}
		}
		s := scored{entry: ps.entry, yearDiff: diff, p: ps, fromAKA: fromAKA, articleOK: full == ps.full}
		switch ps.kind {
		case TitlePrimary:
			s.title = scorePrimary
		case TitleOriginal:
			s.title = scoreOriginal
		default:
			s.title = scoreAlternate
		}
		if fromAKA {
			s.title -= penaltyAKA
		}
		if !s.articleOK {
			s.title -= penaltyArticle
		}
		replaced := false
		for i := range found {
			if found[i].entry == s.entry {
				if better(s, found[i]) {
					found[i] = s
				}
				replaced = true
				break
			}
		}
		if !replaced {
			found = append(found, s)
		}
	}
	return found
}

func better(a, b scored) bool {
	if (a.yearDiff == 0) != (b.yearDiff == 0) {
		return a.yearDiff == 0
	}
	return a.title > b.title
}
