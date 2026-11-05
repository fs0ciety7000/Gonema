package quality_test

import (
	"testing"

	"github.com/google/go-cmp/cmp"

	"github.com/fs0ciety7000/gonema/internal/quality"
)

func TestScore(t *testing.T) {
	t.Parallel()
	rules := []*quality.Rule{
		quality.MustRule("HEVC", "", cond{Kind: quality.KindCodec, Value: "h265"}),
		quality.MustRule("Unscored", "", cond{Kind: quality.KindSource, Value: "bluray"}),
		quality.MustRule("Bad group", "", cond{Kind: quality.KindReleaseGroup, Value: "EVO"}),
		quality.MustRule("Never", "", cond{Kind: quality.KindResolution, Value: "480p"}),
	}
	p := quality.Profile{Scores: map[string]int{"HEVC": 20, "Bad group": -100, "Never": 1000}}
	tests := []struct {
		name string
		rel  *quality.Release
		want quality.Breakdown
	}{
		{"none", release("Movie.2020.1080p.WEB-DL.H264-GRP", 0), quality.Breakdown{}},
		{
			"several", release("Movie.2020.1080p.BluRay.x265-EVO", 0),
			quality.Breakdown{Total: -80, Rules: []quality.RuleScore{
				{Rule: "HEVC", Score: 20}, {Rule: "Unscored", Score: 0}, {Rule: "Bad group", Score: -100},
			}},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			got := quality.Score(&p, rules, tt.rel)
			if diff := cmp.Diff(tt.want, got); diff != "" {
				t.Errorf("Score mismatch (-want +got):\n%s", diff)
			}
		})
	}
}
