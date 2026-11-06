package decision_test

import (
	"testing"

	"pgregory.net/rapid"

	"github.com/fs0ciety7000/gonema/internal/decision"
	"github.com/fs0ciety7000/gonema/internal/parser"
	"github.com/fs0ciety7000/gonema/internal/quality"
)

func rev(v int, isReal bool) parser.Revision { return parser.Revision{Version: v, Real: isReal} }

func fq(id quality.ID, v, score int) decision.FileQuality {
	return decision.FileQuality{Quality: id, Revision: rev(v, false), CustomScore: score}
}

func TestCompareRevision(t *testing.T) {
	t.Parallel()
	tests := []struct {
		a, b parser.Revision
		want int
	}{
		{rev(1, false), rev(1, false), 0},
		{rev(2, false), rev(1, false), 1},
		{rev(1, false), rev(2, false), -1},
		{rev(2, true), rev(2, false), 1},
		{rev(2, false), rev(2, true), -1},
		{rev(2, true), rev(3, false), -1},
		{rev(2, true), rev(2, true), 0},
	}
	for _, tt := range tests {
		if got := decision.CompareRevision(tt.a, tt.b); got != tt.want {
			t.Errorf("CompareRevision(%+v, %+v) = %d, want %d", tt.a, tt.b, got, tt.want)
		}
	}
}

// upgradeProfile: Remux-1080p > Bluray-1080p > WEB 1080p (WEB-DL = WEBRip) > HDTV-1080p,
// cutoff Bluray-1080p, cutoff score 50, step 10.
func upgradeProfile(t fataler) quality.Profile {
	t.Helper()
	p := starter(t, quality.ProfileHD1080p)
	p.CutoffScore = 50
	return p
}

func TestCutoffMet(t *testing.T) {
	t.Parallel()
	p := upgradeProfile(t)
	tests := []struct {
		name string
		cur  decision.FileQuality
		want bool
	}{
		{"below cutoff quality", fq(quality.WebDL1080p, 1, 500), false},
		{"cutoff quality, score below", fq(quality.Bluray1080p, 1, 49), false},
		{"cutoff quality and score", fq(quality.Bluray1080p, 1, 50), true},
		{"above cutoff, score below", fq(quality.Remux1080p, 1, 0), false},
		{"above cutoff and score", fq(quality.Remux1080p, 1, 60), true},
		{"not in profile", fq("bogus", 1, 100), false},
		{"disallowed quality ranked above", fq(quality.Bluray2160p, 1, 100), false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			if got := decision.CutoffMet(tt.cur, &p); got != tt.want {
				t.Errorf("CutoffMet = %v, want %v", got, tt.want)
			}
		})
	}
}

// TestIsUpgrade is the upgrade matrix.
func TestIsUpgrade(t *testing.T) {
	t.Parallel()
	p := upgradeProfile(t)
	disabled := upgradeProfile(t)
	disabled.UpgradeAllowed = false
	tests := []struct {
		name       string
		profile    *quality.Profile
		cur, cand  decision.FileQuality
		wantReason decision.UpgradeReason
	}{
		{"better quality", &p, fq(quality.WebDL1080p, 1, 0), fq(quality.Bluray1080p, 1, 0), decision.UpgradeQuality},
		{"better quality, lower score", &p, fq(quality.WebDL1080p, 1, 40), fq(quality.Bluray1080p, 1, -40), decision.UpgradeQuality},
		{"better quality past cutoff", &p, fq(quality.Bluray1080p, 1, 0), fq(quality.Remux1080p, 1, 0), decision.UpgradeQuality},
		{"better quality, cutoff met", &p, fq(quality.Bluray1080p, 1, 50), fq(quality.Remux1080p, 1, 100), ""},
		{"worse quality, higher score", &p, fq(quality.Bluray1080p, 1, 0), fq(quality.WebDL1080p, 1, 500), ""},
		{"proper same quality", &p, fq(quality.WebDL1080p, 1, 0), fq(quality.WebDL1080p, 2, 0), decision.UpgradeRevision},
		{"proper past cutoff", &p, fq(quality.Bluray1080p, 1, 50), fq(quality.Bluray1080p, 2, 50), decision.UpgradeRevision},
		{"real proper", &p, fq(quality.Bluray1080p, 2, 50), decision.FileQuality{Quality: quality.Bluray1080p, Revision: rev(2, true), CustomScore: 50}, decision.UpgradeRevision},
		{"older revision, higher score", &p, fq(quality.WebDL1080p, 2, 0), fq(quality.WebDL1080p, 1, 100), ""},
		{"proper of a group sibling is not a revision upgrade", &p, fq(quality.WebDL1080p, 1, 0), fq(quality.WebRip1080p, 2, 0), ""},
		{"group sibling with better score", &p, fq(quality.WebDL1080p, 1, 0), fq(quality.WebRip1080p, 1, 10), decision.UpgradeScore},
		{"score below step", &p, fq(quality.WebDL1080p, 1, 0), fq(quality.WebDL1080p, 1, 9), ""},
		{"score at step", &p, fq(quality.WebDL1080p, 1, 0), fq(quality.WebDL1080p, 1, 10), decision.UpgradeScore},
		{"score at cutoff quality, below cutoff score", &p, fq(quality.Bluray1080p, 1, 20), fq(quality.Bluray1080p, 1, 30), decision.UpgradeScore},
		{"score, cutoff met", &p, fq(quality.Bluray1080p, 1, 50), fq(quality.Bluray1080p, 1, 500), ""},
		{"identical", &p, fq(quality.WebDL1080p, 1, 0), fq(quality.WebDL1080p, 1, 0), ""},
		{"disabled: better quality", &disabled, fq(quality.WebDL1080p, 1, 0), fq(quality.Bluray1080p, 1, 0), ""},
		{"disabled: proper", &disabled, fq(quality.WebDL1080p, 1, 0), fq(quality.WebDL1080p, 2, 0), ""},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			reason, ok := decision.IsUpgrade(tt.cur, tt.cand, tt.profile)
			if reason != tt.wantReason || ok != (tt.wantReason != "") {
				t.Errorf("IsUpgrade = %q, %v; want %q", reason, ok, tt.wantReason)
			}
		})
	}
}

func genFileQuality(t *rapid.T, label string) decision.FileQuality {
	all := quality.All()
	return decision.FileQuality{
		Quality:     all[rapid.IntRange(0, len(all)-1).Draw(t, label+".quality")].ID,
		Revision:    rev(rapid.IntRange(1, 3).Draw(t, label+".version"), rapid.Bool().Draw(t, label+".real")),
		CustomScore: rapid.IntRange(-200, 200).Draw(t, label+".score"),
	}
}

func genProfile(t *rapid.T) quality.Profile {
	names := []string{quality.ProfileAny, quality.ProfileHD1080p, quality.ProfileUltraHD, quality.ProfileRemux2160}
	p := starter(t, rapid.SampledFrom(names).Draw(t, "profile"))
	p.UpgradeAllowed = rapid.Bool().Draw(t, "upgradeAllowed")
	p.CutoffScore = rapid.IntRange(-100, 100).Draw(t, "cutoffScore")
	p.MinUpgradeStep = rapid.IntRange(0, 50).Draw(t, "step")
	return p
}

// TestIsUpgrade_properties checks invariants over random files and profiles.
func TestIsUpgrade_properties(t *testing.T) {
	t.Parallel()
	rapid.Check(t, func(t *rapid.T) {
		p := genProfile(t)
		cur := genFileQuality(t, "cur")
		cand := genFileQuality(t, "cand")
		if _, ok := decision.IsUpgrade(cur, cur, &p); ok {
			t.Fatalf("IsUpgrade is reflexive for %+v", cur)
		}
		reason, ok := decision.IsUpgrade(cur, cand, &p)
		if !p.UpgradeAllowed && ok {
			t.Fatalf("upgrade %q with upgrades disabled", reason)
		}
		if ok && reason != decision.UpgradeRevision && decision.CutoffMet(cur, &p) {
			t.Fatalf("upgrade %q after cutoff met", reason)
		}
		// Antisymmetric: a and b cannot upgrade each other.
		if _, back := decision.IsUpgrade(cand, cur, &p); ok && back {
			t.Fatalf("%+v and %+v upgrade each other", cur, cand)
		}
		rc, _ := p.Rank(cur.Quality)
		rn, _ := p.Rank(cand.Quality)
		if ok && rn < rc {
			t.Fatalf("upgrade to a lower rank: %d -> %d", rc, rn)
		}
	})
}

// TestEvaluate_upgrades checks how the evaluator reports upgrades.
func TestEvaluate_upgrades(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name    string
		current decision.CurrentFile
		release string
		want    decision.UpgradeReason
	}{
		{"quality", decision.CurrentFile{FileQuality: fq(quality.WebDL1080p, 1, 0)}, good, decision.UpgradeQuality},
		{"proper", decision.CurrentFile{FileQuality: fq(quality.Bluray1080p, 1, 0)}, "Alien.Romulus.2024.PROPER.1080p.BluRay.x264-SPARKS", decision.UpgradeRevision},
		{
			"language", decision.CurrentFile{FileQuality: fq(quality.Remux1080p, 1, 0), Languages: []parser.Language{parser.LanguagePolish}},
			"Alien.Romulus.2024.1080p.WEB-DL.DDP5.1.H.264-NTG", decision.UpgradeLanguage,
		},
		{
			"file with wanted language is not replaced for language", decision.CurrentFile{
				FileQuality: fq(quality.Remux1080p, 1, 0), Languages: []parser.Language{parser.LanguageEnglish},
			},
			"Alien.Romulus.2024.1080p.WEB-DL.DDP5.1.H.264-NTG", "",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			cfg := baseConfig(t)
			cfg.Current = &tt.current
			c := usenet(tt.release, 7*gb)
			d := newEvaluator(t, cfg).Evaluate(&c)
			switch {
			case tt.want == "" && (d.Accepted || d.Upgrade != nil):
				t.Errorf("accepted as upgrade: %+v", d.Upgrade)
			case tt.want != "" && (!d.Accepted || d.Upgrade == nil || d.Upgrade.Reason != tt.want || d.Upgrade.From != tt.current.FileQuality):
				t.Errorf("decision = %+v, upgrade %+v; want reason %q", d.Rejections, d.Upgrade, tt.want)
			}
		})
	}
}

// TestCutoffMemberOfGroup: a cutoff naming a member of a group behaves exactly
// like the group name (the database stores either), for CutoffMet, IsUpgrade
// and the evaluator, which accepts the profile.
func TestCutoffMemberOfGroup(t *testing.T) {
	t.Parallel()
	byGroup := upgradeProfile(t)
	byGroup.Cutoff = "WEB 1080p"
	ids := []quality.ID{quality.HDTV1080p, quality.WebDL1080p, quality.WebRip1080p, quality.Bluray1080p, quality.Remux1080p}
	for _, member := range []quality.ID{quality.WebDL1080p, quality.WebRip1080p} {
		p := upgradeProfile(t)
		p.Cutoff = string(member)
		for _, cur := range ids {
			for _, score := range []int{0, 50, 60} {
				c := fq(cur, 1, score)
				if got, want := decision.CutoffMet(c, &p), decision.CutoffMet(c, &byGroup); got != want {
					t.Errorf("cutoff %s: CutoffMet(%s, %d) = %v, group gives %v", member, cur, score, got, want)
				}
				for _, cand := range ids {
					n := fq(cand, 1, score+10)
					gr, gok := decision.IsUpgrade(c, n, &p)
					wr, wok := decision.IsUpgrade(c, n, &byGroup)
					if gr != wr || gok != wok {
						t.Errorf("cutoff %s: IsUpgrade(%s→%s) = %v %v, group gives %v %v", member, cur, cand, gr, gok, wr, wok)
					}
				}
			}
		}
		cfg := baseConfig(t)
		cfg.Profile = p
		if _, err := decision.NewEvaluator(cfg); err != nil {
			t.Errorf("cutoff %s: NewEvaluator = %v", member, err)
		}
	}
}
