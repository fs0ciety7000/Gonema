package quality_test

import (
	"testing"

	"github.com/fs0ciety7000/gonema/internal/quality"
)

func TestStarterProfiles_valid(t *testing.T) {
	t.Parallel()
	rules := quality.StarterRules()
	if err := quality.ValidateRules(rules); err != nil {
		t.Fatalf("starter rules: %v", err)
	}
	profiles := quality.StarterProfiles()
	names := map[string]bool{}
	for i := range profiles {
		p := &profiles[i]
		names[p.Name] = true
		if err := p.Validate(rules); err != nil {
			t.Errorf("starter profile %s: %v", p.Name, err)
		}
		// Every catalog quality is listed so the user can enable it.
		for _, q := range quality.All() {
			if _, ok := p.ItemOf(q.ID); !ok {
				t.Errorf("%s does not list %s", p.Name, q.ID)
			}
		}
	}
	for _, n := range []string{quality.ProfileAny, quality.ProfileHD1080p, quality.ProfileUltraHD, quality.ProfileRemux2160} {
		if !names[n] {
			t.Errorf("missing starter profile %s", n)
		}
	}
}

func TestStarterProfiles_content(t *testing.T) {
	t.Parallel()
	hd, ok := quality.StarterProfile(quality.ProfileHD1080p)
	if !ok {
		t.Fatal("no HD-1080p")
	}
	for id, want := range map[quality.ID]bool{
		quality.Remux1080p: true, quality.Bluray1080p: true, quality.WebDL1080p: true, quality.WebRip1080p: true,
		quality.HDTV1080p: true, quality.WebDL720p: false, quality.Bluray2160p: false, quality.BRDisk1080p: false,
	} {
		if hd.Allowed(id) != want {
			t.Errorf("HD-1080p allows %s = %v, want %v", id, !want, want)
		}
	}
	web, _ := hd.Rank(quality.WebDL1080p)
	rip, _ := hd.Rank(quality.WebRip1080p)
	br, _ := hd.Rank(quality.Bluray1080p)
	if web != rip || br <= web {
		t.Errorf("HD-1080p ranks: webdl %d, webrip %d, bluray %d", web, rip, br)
	}
	anyP, _ := quality.StarterProfile(quality.ProfileAny)
	if anyP.Allowed(quality.BRDisk2160p) || anyP.Allowed(quality.Unknown) || !anyP.Allowed(quality.Cam) {
		t.Error("Any profile allows the wrong qualities")
	}
	if _, ok := quality.StarterProfile("Nope"); ok {
		t.Error("StarterProfile found an unknown name")
	}
	// Fresh values on each call.
	hd.Scores[quality.RuleFreeleech] = 999
	again, _ := quality.StarterProfile(quality.ProfileHD1080p)
	if again.Scores[quality.RuleFreeleech] == 999 {
		t.Error("StarterProfile shares state between calls")
	}
}

func TestStarterRules_match(t *testing.T) {
	t.Parallel()
	rules := map[string]*quality.Rule{}
	for _, r := range quality.StarterRules() {
		rules[r.Name()] = r
	}
	tests := []struct {
		rule string
		rel  *quality.Release
		want bool
	}{
		{quality.RuleHDR10Plus, release("Movie.2020.2160p.WEB-DL.DDP5.1.HDR10+.H.265-GRP", 0), true},
		{quality.RuleDolbyVision, release("Movie.2020.2160p.WEB-DL.DV.H.265-GRP", 0), true},
		{quality.RuleDolbyVision, release("Movie.2020.2160p.WEB-DL.HDR.H.265-GRP", 0), false},
		{quality.RuleX265HD, release("Movie.2020.1080p.BluRay.x265-GRP", 0), true},
		{quality.RuleX265HD, release("Movie.2020.720p.BluRay.x265-GRP", 0), true},
		{quality.RuleX265HD, release("Movie.2020.2160p.BluRay.x265-GRP", 0), false},
		{quality.RuleHardcodedSubs, release("Movie.2020.1080p.WEB.H264.KORSUB-GRP", 0), true},
		{quality.RuleHardcodedSubs, release("Movie.2020.1080p.HC.WEBRip.x264-GRP", 0), true},
		{quality.RuleHardcodedSubs, release("Movie_2020_1080p_HC_WEBRip_x264-GRP", 0), true},
		{quality.RuleHardcodedSubs, release("Movie.2020.1080p.WEBRip.x264-HCGRP", 0), false},
		{quality.RuleLowQuality, release("Movie.2020.1080p.BluRay.x264-YIFY", 0), true},
		{quality.RuleLowQuality, release("Movie.2020.1080p.BluRay.x264-SPARKS", 0), false},
		{quality.RuleUpscaled, release("Movie.2020.2160p.UpScaled.WEB.x265-GRP", 0), true},
		{quality.Rule3D, release("Movie.2020.1080p.3D.HSBS.BluRay.x264-GRP", 0), true},
		{quality.Rule3D, release("Movie.3Day.2020.1080p.BluRay.x264-GRP", 0), false},
		{quality.RuleFreeleech, release("Movie.2020.1080p.BluRay.x264-GRP", 0, "freeleech"), true},
	}
	for _, tt := range tests {
		t.Run(tt.rule+"/"+tt.rel.Title, func(t *testing.T) {
			t.Parallel()
			if got := rules[tt.rule].Matches(tt.rel); got != tt.want {
				t.Errorf("Matches = %v, want %v", got, tt.want)
			}
		})
	}
}
