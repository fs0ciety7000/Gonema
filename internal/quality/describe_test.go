package quality_test

import (
	"slices"
	"testing"

	"github.com/fs0ciety7000/gonema/internal/parser"
	"github.com/fs0ciety7000/gonema/internal/quality"
)

func TestDescribe(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name  string
		conds []cond
		want  string
	}{
		{"none", nil, ""},
		{"alternatives and negated required", []cond{
			{Kind: quality.KindHDR, Value: "dv"},
			{Kind: quality.KindHDR, Value: "hdr10"},
			{Kind: quality.KindHDR, Value: "hdr10+"},
			{Kind: quality.KindReleaseGroup, Value: "MiNiPiX", Negate: true, Required: true},
		}, "the release is not from MiNiPiX and is dv, is hdr10 or is hdr10+"},
		{"kinds are and", []cond{
			{Kind: quality.KindCodec, Value: "h265", Required: true},
			{Kind: quality.KindResolution, Value: "720p"},
			{Kind: quality.KindResolution, Value: "1080p"},
		}, "the release is encoded in h265 and is 720p or is 1080p"},
		{"ranges", []cond{
			{Kind: quality.KindSizeRange, Min: 1, Max: 4.5, Required: true},
			{Kind: quality.KindYearRange, Min: 2000, Required: true},
			{Kind: quality.KindSizeRange, Max: 2, Negate: true, Required: true},
		}, "the release weighs 1 to 4.5 GB and is from the years at least 2000 and does not weigh at most 2 GB"},
		{"misc", []cond{
			{Kind: quality.KindLanguage, Value: "multi", Required: true},
			{Kind: quality.KindAudio, Value: "atmos", Required: true},
			{Kind: quality.KindIndexer, Value: "Nebula", Required: true},
			{Kind: quality.KindTitleRegex, Value: `\bimax\b`, Required: true},
		}, `the release has several audio languages and has atmos audio and comes from Nebula and has a title matching /\bimax\b/`},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			if got := quality.Describe(tt.conds); got != tt.want {
				t.Errorf("Describe = %q\nwant       %q", got, tt.want)
			}
		})
	}
}

func TestConditionKinds(t *testing.T) {
	t.Parallel()
	kinds := quality.ConditionKinds()
	if len(kinds) != 13 {
		t.Fatalf("kinds = %d, want 13", len(kinds))
	}
	for _, k := range kinds {
		if !quality.ValidKind(k.Kind) {
			t.Errorf("%s not valid", k.Kind)
		}
		// Every offered value must compile into a rule.
		for _, v := range k.Values {
			if _, err := quality.NewRule("R", "", cond{Kind: k.Kind, Value: v}); err != nil {
				t.Errorf("%s %q: %v", k.Kind, v, err)
			}
		}
		if (k.Input == quality.InputChoice) != (len(k.Values) > 0) {
			t.Errorf("%s: input %s with %d values", k.Kind, k.Input, len(k.Values))
		}
	}
	if quality.ValidKind("color") {
		t.Error("color is valid")
	}
	if !slices.Contains(kinds[1].Values, "remux") || slices.Contains(kinds[2].Values, "unknown") {
		t.Errorf("values = %v %v", kinds[1].Values, kinds[2].Values)
	}
}

func TestIndexerCondition(t *testing.T) {
	t.Parallel()
	r := quality.MustRule("Nebula", "", cond{Kind: quality.KindIndexer, Value: "nebula nzb"})
	p := parser.Parse("Movie.2020.1080p.WEB-DL.H264-GRP")
	tests := []struct {
		indexer string
		want    bool
	}{{"Nebula NZB", true}, {"NEBULA nzb", true}, {"Other", false}, {"", false}}
	for _, tt := range tests {
		if got := r.Matches(&quality.Release{Title: "x", Parsed: &p, Indexer: tt.indexer}); got != tt.want {
			t.Errorf("indexer %q: %v, want %v", tt.indexer, got, tt.want)
		}
	}
}
