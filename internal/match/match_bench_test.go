package match_test

import (
	"fmt"
	"testing"

	"github.com/fs0ciety7000/gonema/internal/match"
	"github.com/fs0ciety7000/gonema/internal/parser"
)

// matchBudgetNs is the target for one Match on a 10,000-movie library.
const matchBudgetNs = 10_000

var benchWords = []string{
	"night", "dark", "river", "last", "house", "city", "storm", "king", "blood", "silent",
	"love", "war", "ghost", "summer", "winter", "road", "lost", "black", "golden", "iron",
	"le", "la", "les", "de", "der", "das", "el", "amour", "mort", "nuit", "part", "ii", "iii", "vs", "and",
}

// syntheticLibrary returns n movies with three alternate titles each,
// deterministically (a linear congruential sequence, no math/rand).
func syntheticLibrary(n int) []match.Entry {
	seed := uint32(1)
	next := func(m int) int {
		seed = seed*1664525 + 1013904223
		return int(seed>>8) % m
	}
	title := func() string {
		words := 2 + next(4)
		s := ""
		for i := range words {
			if i > 0 {
				s += " "
			}
			s += benchWords[next(len(benchWords))]
		}
		return s
	}
	lib := make([]match.Entry, n)
	for i := range lib {
		lib[i] = match.Entry{
			MovieID:         int64(i + 1),
			Title:           fmt.Sprintf("%s %d", title(), i),
			OriginalTitle:   title(),
			Year:            1950 + next(75),
			AlternateTitles: []string{title(), title() + ": " + title(), fmt.Sprintf("%s %d", title(), i)},
			IMDbID:          fmt.Sprintf("tt%07d", i+1),
		}
	}
	return lib
}

func BenchmarkNewIndex(b *testing.B) {
	lib := syntheticLibrary(10_000) // 10,000 movies, 30,000 alternate titles
	b.ReportAllocs()
	for b.Loop() {
		_ = match.NewIndex(lib)
	}
}

func BenchmarkMatch(b *testing.B) {
	lib := append(syntheticLibrary(10_000), library...)
	idx := match.NewIndex(lib)
	names := []string{
		"The.Dark.Knight.2008.1080p.BluRay.x264-GRP",
		"Dune.Part.Two.2024.2160p.UHD.BluRay.REMUX.DV.HDR.HEVC.TrueHD.Atmos.7.1-GRP",
		"Le.Fabuleux.Destin.d.Amelie.Poulain.2001.FRENCH.1080p.BluRay.x264-GRP",
		"Amelie.AKA.Le.Fabuleux.Destin.D.Amelie.Poulain.2001.1080p.BluRay.x264-GRP",
		"The.Thing.1080p.BluRay.x264-GRP",
		"Some.Unknown.Movie.2020.1080p.WEB-DL.DDP5.1.H.264-GRP",
		fmt.Sprintf("%s.%d", lib[4242].Title, lib[4242].Year),
	}
	parsed := make([]parser.ParsedRelease, len(names))
	for i, n := range names {
		parsed[i] = parser.Parse(n)
	}
	b.ReportAllocs()
	for b.Loop() {
		for i := range parsed {
			_ = idx.Match(parsed[i])
		}
	}
	perMatch := b.Elapsed().Nanoseconds() / int64(b.N) / int64(len(parsed))
	b.ReportMetric(float64(perMatch), "ns/match")
	if perMatch > matchBudgetNs {
		b.Errorf("Match takes %d ns, budget %d ns", perMatch, matchBudgetNs)
	}
}
