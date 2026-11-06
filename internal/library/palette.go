package library

import (
	"cmp"
	"context"
	"slices"
	"strconv"
	"strings"
	"unicode"
)

// Command palette search: fuzzy matching of the library titles, from the
// in-memory index. A request scans the folded titles of every movie (computed
// once per index version), so 10,000 movies answer in about a millisecond.

// MaxPaletteLimit bounds the hits of SearchLibrary.
const MaxPaletteLimit = 50

// Match kinds, best first.
const (
	MatchPrefix    = "prefix"    // the title starts with the query
	MatchWord      = "word"      // the query starts a word of the title
	MatchSubstring = "substring" // the query is inside the title
	MatchWords     = "words"     // every word of the query is in the title, in any order
	MatchFuzzy     = "fuzzy"     // the letters of the query appear in order
)

// PaletteHit is a library movie matching a palette query.
type PaletteHit struct {
	Movie *MovieSummary
	// Field is the matched field: "title" or "originalTitle".
	Field string
	// Kind is how it matched (MatchPrefix...).
	Kind string
	// Ranges are the matched characters of Field, as [start, end) offsets in
	// Unicode code points, ascending and disjoint: what the palette highlights.
	Ranges [][2]int
	score  int
}

// foldTable maps accented Latin letters to their base letter, one rune to one
// rune so that offsets in folded and original titles agree.
var foldTable = map[rune]rune{
	'à': 'a', 'á': 'a', 'â': 'a', 'ã': 'a', 'ä': 'a', 'å': 'a', 'ā': 'a', 'ă': 'a', 'ą': 'a',
	'ç': 'c', 'ć': 'c', 'č': 'c', 'ď': 'd', 'đ': 'd',
	'è': 'e', 'é': 'e', 'ê': 'e', 'ë': 'e', 'ē': 'e', 'ė': 'e', 'ę': 'e', 'ě': 'e',
	'ì': 'i', 'í': 'i', 'î': 'i', 'ï': 'i', 'ī': 'i', 'į': 'i', 'ı': 'i',
	'ł': 'l', 'ñ': 'n', 'ń': 'n', 'ň': 'n',
	'ò': 'o', 'ó': 'o', 'ô': 'o', 'õ': 'o', 'ö': 'o', 'ø': 'o', 'ō': 'o', 'ő': 'o',
	'ř': 'r', 'ś': 's', 'š': 's', 'ş': 's', 'ť': 't', 'ţ': 't',
	'ù': 'u', 'ú': 'u', 'û': 'u', 'ü': 'u', 'ū': 'u', 'ů': 'u', 'ű': 'u', 'ų': 'u',
	'ý': 'y', 'ÿ': 'y', 'ź': 'z', 'ż': 'z', 'ž': 'z',
}

// fold lowercases s and strips the accents of Latin letters, rune by rune.
func fold(s string) []rune {
	out := []rune(s)
	for i, r := range out {
		r = unicode.ToLower(r)
		if f, ok := foldTable[r]; ok {
			r = f
		}
		out[i] = r
	}
	return out
}

// foldedTitles returns the folded title and original title of every row,
// computed once per index version.
func (x *libraryIndex) foldedTitles() (titles, originals [][]rune) {
	x.mu.Lock()
	defer x.mu.Unlock()
	if x.folded == nil {
		x.folded = make([][]rune, len(x.rows))
		x.foldedOrig = make([][]rune, len(x.rows))
		for i := range x.rows {
			x.folded[i] = fold(x.rows[i].Title)
			if o := x.rows[i].OriginalTitle; o != "" && o != x.rows[i].Title {
				x.foldedOrig[i] = fold(o)
			}
		}
	}
	return x.folded, x.foldedOrig
}

func isWordStart(t []rune, i int) bool {
	return i == 0 || !unicode.IsLetter(t[i-1]) && !unicode.IsDigit(t[i-1])
}

// runeIndex returns the first index of sub in t, -1 if absent; with
// wordOnly, only at the start of a word.
func runeIndex(t, sub []rune, wordOnly bool) int {
	for i := 0; i+len(sub) <= len(t); i++ {
		if (!wordOnly || isWordStart(t, i)) && slices.Equal(t[i:i+len(sub)], sub) {
			return i
		}
	}
	return -1
}

// matchTitle matches a folded query against a folded title. words are the
// query words; compact is the query without spaces.
func matchTitle(t, q []rune, words [][]rune, compact []rune) (kind string, score int, ranges [][2]int) {
	if len(t) == 0 {
		return "", 0, nil
	}
	if i := runeIndex(t, q, false); i >= 0 {
		r := [][2]int{{i, i + len(q)}}
		switch {
		case i == 0:
			return MatchPrefix, 4000 + wholeWord(t, i+len(q)) - len(t), r
		case runeIndex(t, q, true) >= 0:
			j := runeIndex(t, q, true)
			return MatchWord, 3000 + wholeWord(t, j+len(q)) - j - len(t), [][2]int{{j, j + len(q)}}
		}
		return MatchSubstring, 2000 - i - len(t), r
	}
	if len(words) > 1 {
		score = 1000 - len(t)
		for _, w := range words {
			j := runeIndex(t, w, true)
			if j < 0 {
				if j = runeIndex(t, w, false); j < 0 {
					ranges = nil
					break
				}
				score -= 10
			}
			ranges = append(ranges, [2]int{j, j + len(w)})
		}
		if ranges != nil {
			return MatchWords, score, mergeRanges(ranges)
		}
	}
	if len(compact) < 2 {
		return "", 0, nil
	}
	// Fuzzy: the letters in order, the first one at the start of a word.
	start := -1
	for i := range t {
		if t[i] == compact[0] && isWordStart(t, i) {
			start = i
			break
		}
	}
	if start < 0 {
		return "", 0, nil
	}
	ranges = [][2]int{{start, start + 1}}
	gaps, pos := 0, start+1
	for _, c := range compact[1:] {
		j := slices.Index(t[pos:], c)
		if j < 0 {
			return "", 0, nil
		}
		if j > 0 {
			gaps++
		}
		pos += j
		if last := &ranges[len(ranges)-1]; last[1] == pos {
			last[1]++
		} else {
			ranges = append(ranges, [2]int{pos, pos + 1})
		}
		pos++
	}
	return MatchFuzzy, 500 - 20*gaps - len(t), ranges
}

// wholeWord is a bonus for a match ending at the end of a word: "hal" ranks
// "Hal and the Cyon" above "Halcyon Drift".
func wholeWord(t []rune, end int) int {
	if end == len(t) || !unicode.IsLetter(t[end]) && !unicode.IsDigit(t[end]) {
		return 100
	}
	return 0
}

func mergeRanges(r [][2]int) [][2]int {
	slices.SortFunc(r, func(a, b [2]int) int { return cmp.Compare(a[0], b[0]) })
	out := r[:1]
	for _, x := range r[1:] {
		if last := &out[len(out)-1]; x[0] <= last[1] {
			last[1] = max(last[1], x[1])
		} else {
			out = append(out, x)
		}
	}
	return out
}

// SearchLibrary matches q against the titles and original titles of the
// library, best first: prefix, word start, substring, all words, then fuzzy
// (letters in order). A word of four digits that is not in the title selects
// the year ("dune 2021"). It returns at most limit hits and the number of
// matching movies.
func (s *Service) SearchLibrary(ctx context.Context, q string, limit int) ([]PaletteHit, int, error) {
	q = strings.TrimSpace(q)
	if len(q) > 200 {
		return nil, 0, invalid("q", "the search text is longer than 200 characters")
	}
	if limit <= 0 || limit > MaxPaletteLimit {
		return nil, 0, invalid("limit", "limit must be between 1 and %d", MaxPaletteLimit)
	}
	hits := []PaletteHit{}
	if q == "" {
		return hits, 0, nil
	}
	idx, err := s.index(ctx)
	if err != nil {
		return nil, 0, err
	}
	titles, originals := idx.foldedTitles()
	var year int64
	var words [][]rune
	for _, w := range strings.Fields(q) {
		if len(w) == 4 && year == 0 {
			if y, err := strconv.Atoi(w); err == nil && y >= 1870 && y <= 2200 {
				year = int64(y)
				continue
			}
		}
		words = append(words, fold(w))
	}
	if len(words) == 0 && year == 0 {
		return hits, 0, nil
	}
	var full, compact []rune
	for i, w := range words {
		if i > 0 {
			full = append(full, ' ')
		}
		full = append(full, w...)
		compact = append(compact, w...)
	}
	yearText := []rune(strconv.FormatInt(year, 10))
	for i := range idx.rows {
		m := &idx.rows[i]
		best := PaletteHit{}
		for _, f := range [...]struct {
			name string
			t    []rune
		}{{"title", titles[i]}, {"originalTitle", originals[i]}} {
			if f.t == nil {
				continue
			}
			kind, score, ranges := "", 0, [][2]int(nil)
			switch {
			case len(words) == 0: // a year alone: the year, or a title holding it ("1917")
				if j := runeIndex(f.t, yearText, true); j >= 0 {
					kind, score, ranges = MatchPrefix, 4000-len(f.t), [][2]int{{j, j + len(yearText)}}
					if j > 0 {
						kind, score = MatchWord, 3000-j-len(f.t)
					}
				} else if m.Year == year {
					kind, score = MatchSubstring, 1000
				} else {
					continue
				}
			default:
				kind, score, ranges = matchTitle(f.t, full, words, compact)
				if kind == "" {
					continue
				}
				if year != 0 && m.Year != year {
					// The year may be part of the title ("1917", "Blade Runner 2049").
					j := runeIndex(f.t, yearText, true)
					if j < 0 {
						continue
					}
					ranges = mergeRanges(append(ranges, [2]int{j, j + len(yearText)}))
				}
			}
			if f.name == "originalTitle" {
				score -= 50 // the displayed title first on ties
			}
			if best.Kind == "" || score > best.score {
				best = PaletteHit{Movie: m, Field: f.name, Kind: kind, Ranges: ranges, score: score}
			}
		}
		if best.Kind != "" {
			hits = append(hits, best)
		}
	}
	slices.SortFunc(hits, func(a, b PaletteHit) int {
		return cmp.Or(cmp.Compare(b.score, a.score), cmp.Compare(a.Movie.SortTitle, b.Movie.SortTitle), cmp.Compare(a.Movie.ID, b.Movie.ID))
	})
	total := len(hits)
	hits = hits[:min(total, limit)]
	for i := range hits {
		if hits[i].Ranges == nil {
			hits[i].Ranges = [][2]int{}
		}
	}
	return hits, total, nil
}
