package decision_test

import (
	"fmt"
	"testing"

	"github.com/fs0ciety7000/gonema/internal/decision"
	"github.com/fs0ciety7000/gonema/internal/parser"
	"github.com/fs0ciety7000/gonema/internal/quality"
)

// BenchmarkEvaluate measures one decision on an accepted release, with the starter
// rules (three of them are regexes) and a current file to compare against.
func BenchmarkEvaluate(b *testing.B) {
	cfg := goldenConfig(b, quality.ProfileHD1080p)
	cfg.Current = &decision.CurrentFile{FileQuality: decision.FileQuality{
		Quality: quality.WebDL1080p, Revision: parser.Revision{Version: 1},
	}}
	e := newEvaluator(b, cfg)
	c := usenet(good, 9*gb)
	b.ReportAllocs()
	for b.Loop() {
		e.Evaluate(&c)
	}
}

// BenchmarkSearch200 measures an interactive search end to end: parse 200 release
// names, evaluate them and rank them.
func BenchmarkSearch200(b *testing.B) {
	base := searchNames(b)
	names := make([]string, 200)
	for i := range names {
		names[i] = base[i%len(base)]
	}
	cands := searchCandidates(b, names, 119)
	for i := range cands {
		cands[i].GUID = fmt.Sprintf("guid-%03d", i)
	}
	cfg := goldenConfig(b, quality.ProfileHD1080p)
	b.ReportAllocs()
	for b.Loop() {
		e, err := decision.NewEvaluator(cfg)
		if err != nil {
			b.Fatal(err)
		}
		for i := range cands {
			cands[i].Parsed = parser.Parse(cands[i].Title)
		}
		e.EvaluateAll(cands)
	}
}
