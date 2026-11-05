package parser_test

import (
	"testing"

	"github.com/fs0ciety7000/gonema/internal/parser"
)

// parseBudget is the maximum mean parse time per name (docs/architecture.md, "Mesure des budgets").
const parseBudgetNs = 50_000

var typicalNames = map[string]string{
	"scene_bluray": "The.Dark.Knight.2008.1080p.BluRay.H264-PRiSTiNE",
	"p2p_remux":    "Dune.Part.Two.2024.2160p.UHD.BluRay.REMUX.DV.HDR.HEVC.TrueHD.Atmos.7.1-GROUP",
	"web_dl":       "Movie.2019.1080p.AMZN.WEB-DL.DDP5.1.H.264-NTG",
	"french_multi": "Dune.Part.Two.2024.MULTi.TRUEFRENCH.2160p.UHD.BluRay.x265-SESKAPiLE",
	"spaced_yts":   "The Matrix (1999) [1080p] [BluRay] [5.1] [YTS.MX]",
	"obfuscated":   "a8f7c6d5e4b3a29182736455463728ab.mkv",
}

func BenchmarkParse(b *testing.B) {
	entries := loadCorpus(b)
	names := make([]string, len(entries))
	for i := range entries {
		names[i] = entries[i].Name
	}
	b.Run("corpus", func(b *testing.B) {
		b.ReportAllocs()
		for b.Loop() {
			for _, n := range names {
				parser.Parse(n)
			}
		}
		b.ReportMetric(float64(b.Elapsed().Nanoseconds())/float64(b.N*len(names)), "ns/name")
	})
	for label, name := range typicalNames {
		b.Run(label, func(b *testing.B) {
			b.ReportAllocs()
			for b.Loop() {
				parser.Parse(name)
			}
		})
	}
}

// TestParseBudget fails if the mean parse time over the corpus exceeds the 50 µs budget.
// Timing is meaningless under the race detector or in -short runs, so it is skipped there.
func TestParseBudget(t *testing.T) {
	if testing.Short() {
		t.Skip("timing budget skipped in -short mode")
	}
	if raceEnabled {
		t.Skip("timing budget skipped under the race detector")
	}
	entries := loadCorpus(t)
	res := testing.Benchmark(func(b *testing.B) {
		for b.Loop() {
			for i := range entries {
				parser.Parse(entries[i].Name)
			}
		}
	})
	mean := res.NsPerOp() / int64(len(entries))
	t.Logf("mean parse time over %d names: %d ns", len(entries), mean)
	if mean > parseBudgetNs {
		t.Errorf("mean parse time %d ns exceeds budget %d ns", mean, parseBudgetNs)
	}
}
