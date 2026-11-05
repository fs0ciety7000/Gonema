package match_test

import (
	"strings"
	"testing"

	"github.com/google/go-cmp/cmp"

	"github.com/fs0ciety7000/gonema/internal/match"
	"github.com/fs0ciety7000/gonema/internal/parser"
)

// FuzzMatch checks invariants for any release name: no panic, determinism,
// confidences in (0, 1], candidates ordered and unique, keys without spaces, and
// a title always matching itself. (Key is not idempotent by design: words are
// joined, so Key("I I") = "ii" while Key("ii") = "2".)
func FuzzMatch(f *testing.F) {
	idx := match.NewIndex(library)
	for _, s := range []string{
		"", "'", "l'", "L'", "qu'x", "d'''", "Part", "Part.II", "vs", "Le", "Les.Les.Les",
		"Dune.Part.Two.2024.2160p", "Amelie.AKA.Le.Fabuleux.Destin.D.Amelie.Poulain.2001",
		"The.Thing.1080p", "Twins.2023", "\xff\xfe", "Ⱥ.2019", "ǅ & ß", "part part part 2", "x x x x x x x x x x x x x x x x x x x x x x x x x x x",
	} {
		f.Add(s)
	}
	f.Fuzz(func(t *testing.T, in string) {
		k := match.Key(in)
		if strings.ContainsRune(k, ' ') {
			t.Errorf("Key(%q) = %q contains a space", in, k)
		}
		p := parser.Parse(in)
		got := idx.Match(p)
		if again := idx.Match(p); !cmp.Equal(got, again) {
			t.Fatalf("Match(%q) not deterministic", in)
		}
		seen := map[int64]bool{}
		for i, c := range got {
			if c.Confidence <= 0 || c.Confidence > 1 || c.Reason == "" {
				t.Errorf("Match(%q)[%d] = %+v", in, i, c)
			}
			if i > 0 && got[i-1].Confidence < c.Confidence {
				t.Errorf("Match(%q) not ordered by confidence", in)
			}
			if seen[c.MovieID] {
				t.Errorf("Match(%q) returns movie %d twice", in, c.MovieID)
			}
			seen[c.MovieID] = true
		}
		// The same text as a library title always finds that movie.
		if k != "" {
			one := match.NewIndex([]match.Entry{{MovieID: 1, Title: in, Year: 2000}})
			if c := one.Match(parser.ParsedRelease{Title: in, Year: 2000}); len(c) != 1 || !c[0].Confident() {
				t.Errorf("title %q does not match itself: %+v", in, c)
			}
		}
	})
}
