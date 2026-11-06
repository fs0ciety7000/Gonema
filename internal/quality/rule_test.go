package quality_test

import (
	"errors"
	"testing"

	"github.com/fs0ciety7000/gonema/internal/parser"
	"github.com/fs0ciety7000/gonema/internal/quality"
)

type cond = quality.Condition

func release(name string, size int64, flags ...string) *quality.Release {
	p := parser.Parse(name)
	return &quality.Release{Title: name, Parsed: &p, Size: size, IndexerFlags: flags}
}

func TestNewRule_errors(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name    string
		rule    string
		conds   []cond
		wantErr string
	}{
		{"empty name", "", []cond{{Kind: quality.KindCodec, Value: "h265"}}, "scoring rule name is empty"},
		{"no condition", "R", nil, `scoring rule "R" has no condition`},
		{"bad regex", "R", []cond{{Kind: quality.KindTitleRegex, Value: "(x265"}},
			`scoring rule "R", condition 1 (title_regex): invalid regular expression "(x265": missing closing )`},
		{"long regex", "R", []cond{{Kind: quality.KindTitleRegex, Value: string(make([]byte, 1001))}}, "pattern is longer than 1000 characters"},
		{"empty value", "R", []cond{{Kind: quality.KindReleaseGroup, Value: "  "}}, "value is empty"},
		{"unknown kind", "R", []cond{{Kind: "color", Value: "red"}}, `unknown condition kind "color"`},
		{"unknown source", "R", []cond{{Kind: quality.KindSource, Value: "vhs"}}, `unknown source "vhs"`},
		{"modifier none", "R", []cond{{Kind: quality.KindSource, Value: "none"}}, `unknown source "none"`},
		{"unknown resolution", "R", []cond{{Kind: quality.KindResolution, Value: "4320p"}}, `unknown resolution "4320p"`},
		{"unknown codec", "R", []cond{{Kind: quality.KindCodec, Value: "x266"}}, `unknown codec "x266"`},
		{"unknown hdr", "R", []cond{{Kind: quality.KindHDR, Value: "hdr11"}}, `unknown HDR format "hdr11"`},
		{"unknown audio", "R", []cond{{Kind: quality.KindAudio, Value: "mono"}}, `unknown audio format "mono"`},
		{"unknown edition", "R", []cond{{Kind: quality.KindEdition, Value: "lost"}}, `unknown edition "lost"`},
		{"empty edition list", "R", []cond{{Kind: quality.KindEdition, Value: ","}}, `unknown edition ","`},
		{"unknown language", "R", []cond{{Kind: quality.KindLanguage, Value: "elvish"}}, `unknown language "elvish"`},
		{"negative range", "R", []cond{{Kind: quality.KindSizeRange, Min: -1}}, "must not be negative"},
		{"inverted range", "R", []cond{{Kind: quality.KindSizeRange, Min: 10, Max: 2}}, "range maximum 2 is below the minimum 10"},
		{"fractional year", "R", []cond{{Kind: quality.KindYearRange, Min: 1999.5}}, "years must be whole numbers"},
		{"several errors", "R", []cond{{Kind: quality.KindCodec, Value: "x266"}, {Kind: quality.KindCodec, Value: "h265"}, {Kind: quality.KindHDR, Value: "x"}},
			`condition 3 (hdr): unknown HDR format "x"`},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			r, err := quality.NewRule(tt.rule, "", tt.conds...)
			checkErr(t, err, tt.wantErr)
			if r != nil || !errors.Is(err, quality.ErrInvalid) {
				t.Errorf("NewRule = %v, %v; want nil and ErrInvalid", r, err)
			}
		})
	}
}

func TestNewRule_nonFiniteRange(t *testing.T) {
	t.Parallel()
	zero := 0.0
	_, err := quality.NewRule("R", "", cond{Kind: quality.KindSizeRange, Min: zero / zero})
	checkErr(t, err, "finite")
}

func TestMustRule_panics(t *testing.T) {
	t.Parallel()
	defer func() {
		if recover() == nil {
			t.Error("MustRule did not panic on an invalid rule")
		}
	}()
	quality.MustRule("", "")
}

func TestRule_accessors(t *testing.T) {
	t.Parallel()
	c := cond{Kind: quality.KindCodec, Value: "h265", Required: true}
	r := quality.MustRule("HEVC", "desc", c)
	if r.Name() != "HEVC" || r.Description() != "desc" {
		t.Errorf("accessors: %q %q", r.Name(), r.Description())
	}
	got := r.Conditions()
	if len(got) != 1 || got[0] != c {
		t.Errorf("Conditions = %+v", got)
	}
	got[0].Value = "h264"
	if r.Conditions()[0].Value != "h265" {
		t.Error("Conditions returned internal state")
	}
}

// TestRule_Matches_kinds covers every condition kind, matched and not matched.
func TestRule_Matches_kinds(t *testing.T) {
	t.Parallel()
	uhd := release("Dune.Part.Two.2024.2160p.UHD.BluRay.REMUX.DV.HDR10+.HEVC.TrueHD.Atmos.7.1-FraMeSToR", 60*quality.GB, "Freeleech")
	web := release("Dune.Part.Two.2024.MULTi.1080p.WEB-DL.DDP5.1.H.264.Extended-NTG", 0)
	tests := []struct {
		name string
		c    cond
		rel  *quality.Release
		want bool
	}{
		{"regex hit", cond{Kind: quality.KindTitleRegex, Value: `remux`}, uhd, true},
		{"regex miss", cond{Kind: quality.KindTitleRegex, Value: `\bx265\b`}, uhd, false},
		{"group case-insensitive", cond{Kind: quality.KindReleaseGroup, Value: "framestor"}, uhd, true},
		{"group miss", cond{Kind: quality.KindReleaseGroup, Value: "NTG"}, uhd, false},
		{"source", cond{Kind: quality.KindSource, Value: "bluray"}, uhd, true},
		{"source miss", cond{Kind: quality.KindSource, Value: "webdl"}, uhd, false},
		{"modifier", cond{Kind: quality.KindSource, Value: "remux"}, uhd, true},
		{"modifier miss", cond{Kind: quality.KindSource, Value: "remux"}, web, false},
		{"resolution", cond{Kind: quality.KindResolution, Value: "2160p"}, uhd, true},
		{"codec", cond{Kind: quality.KindCodec, Value: "h264"}, web, true},
		{"hdr any of", cond{Kind: quality.KindHDR, Value: "hdr10+"}, uhd, true},
		{"hdr miss", cond{Kind: quality.KindHDR, Value: "dv"}, web, false},
		{"audio", cond{Kind: quality.KindAudio, Value: "truehd"}, uhd, true},
		{"audio miss", cond{Kind: quality.KindAudio, Value: "dts-x"}, uhd, false},
		{"atmos", cond{Kind: quality.KindAudio, Value: "Atmos"}, uhd, true},
		{"atmos miss", cond{Kind: quality.KindAudio, Value: "atmos"}, web, false},
		{"edition", cond{Kind: quality.KindEdition, Value: "extended"}, web, true},
		{"edition miss", cond{Kind: quality.KindEdition, Value: "imax"}, web, false},
		{"language multi", cond{Kind: quality.KindLanguage, Value: "multi"}, web, true},
		{"language miss", cond{Kind: quality.KindLanguage, Value: "fr"}, web, false},
		{"indexer flag", cond{Kind: quality.KindIndexerFlag, Value: "freeleech"}, uhd, true},
		{"indexer flag miss", cond{Kind: quality.KindIndexerFlag, Value: "internal"}, uhd, false},
		{"size in range", cond{Kind: quality.KindSizeRange, Min: 40, Max: 80}, uhd, true},
		{"size open max", cond{Kind: quality.KindSizeRange, Min: 40}, uhd, true},
		{"size above", cond{Kind: quality.KindSizeRange, Max: 50}, uhd, false},
		{"size unknown", cond{Kind: quality.KindSizeRange}, web, false},
		{"year in range", cond{Kind: quality.KindYearRange, Min: 2020, Max: 2024}, uhd, true},
		{"year below", cond{Kind: quality.KindYearRange, Min: 2025}, uhd, false},
		{"negate", cond{Kind: quality.KindCodec, Value: "h264", Negate: true}, uhd, true},
		{"negate miss", cond{Kind: quality.KindCodec, Value: "h264", Negate: true}, web, false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			r := quality.MustRule("R", "", tt.c)
			if got := r.Matches(tt.rel); got != tt.want {
				t.Errorf("Matches = %v, want %v", got, tt.want)
			}
		})
	}
}

func TestRule_Matches_yearUnknown(t *testing.T) {
	t.Parallel()
	r := quality.MustRule("R", "", cond{Kind: quality.KindYearRange})
	if r.Matches(release("Some.Movie.1080p.BluRay.x264-GRP", 0)) {
		t.Error("year_range matched a release without a year")
	}
}

// TestRule_Matches_semantics locks the combination rules: required conditions are
// ANDed, non-required ones are ORed within a kind and ANDed across kinds.
func TestRule_Matches_semantics(t *testing.T) {
	t.Parallel()
	x265HD := release("Movie.2020.1080p.BluRay.x265-GRP", 0)
	x265UHD := release("Movie.2020.2160p.BluRay.x265-GRP", 0)
	x264HD := release("Movie.2020.720p.BluRay.x264-GRP", 0)
	rule := quality.MustRule("x265 HD", "",
		cond{Kind: quality.KindCodec, Value: "h265", Required: true},
		cond{Kind: quality.KindResolution, Value: "720p"},
		cond{Kind: quality.KindResolution, Value: "1080p"},
	)
	anyOf := quality.MustRule("any of", "",
		cond{Kind: quality.KindResolution, Value: "720p"},
		cond{Kind: quality.KindResolution, Value: "2160p"},
	)
	acrossKinds := quality.MustRule("across", "",
		cond{Kind: quality.KindResolution, Value: "1080p"},
		cond{Kind: quality.KindReleaseGroup, Value: "OTHER"},
	)
	onlyRequired := quality.MustRule("required", "",
		cond{Kind: quality.KindCodec, Value: "h265", Required: true},
		cond{Kind: quality.KindSource, Value: "bluray", Required: true},
	)
	requiredNegated := quality.MustRule("not 2160p", "",
		cond{Kind: quality.KindResolution, Value: "2160p", Required: true, Negate: true},
		cond{Kind: quality.KindCodec, Value: "h265"},
	)
	tests := []struct {
		name string
		rule *quality.Rule
		rel  *quality.Release
		want bool
	}{
		{"required + one of kind", rule, x265HD, true},
		{"required + none of kind", rule, x265UHD, false},
		{"required fails", rule, x264HD, false},
		{"OR within a kind (first)", anyOf, x264HD, true},
		{"OR within a kind (second)", anyOf, x265UHD, true},
		{"OR within a kind (none)", anyOf, x265HD, false},
		{"AND across kinds", acrossKinds, x265HD, false},
		{"only required, all hold", onlyRequired, x265HD, true},
		{"only required, one fails", onlyRequired, x264HD, false},
		{"negated required", requiredNegated, x265HD, true},
		{"negated required fails", requiredNegated, x265UHD, false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			if got := tt.rule.Matches(tt.rel); got != tt.want {
				t.Errorf("%s.Matches(%s) = %v, want %v", tt.rule.Name(), tt.rel.Title, got, tt.want)
			}
		})
	}
}

func TestValidateRules(t *testing.T) {
	t.Parallel()
	a := quality.MustRule("A", "", cond{Kind: quality.KindCodec, Value: "h265"})
	b := quality.MustRule("A", "", cond{Kind: quality.KindCodec, Value: "h264"})
	if err := quality.ValidateRules([]*quality.Rule{a}); err != nil {
		t.Errorf("ValidateRules(valid) = %v", err)
	}
	checkErr(t, quality.ValidateRules([]*quality.Rule{a, b}), `scoring rule name "A" is used twice`)
	checkErr(t, quality.ValidateRules([]*quality.Rule{a, nil}), "scoring rule 2 is nil")
}
