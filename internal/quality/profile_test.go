package quality_test

import (
	"errors"
	"strings"
	"testing"

	"github.com/fs0ciety7000/gonema/internal/quality"
)

func hdProfile() quality.Profile {
	return quality.Profile{
		Name: "HD",
		Items: []quality.Item{
			{Qualities: []quality.ID{quality.Remux1080p}, Allowed: false},
			{Qualities: []quality.ID{quality.Bluray1080p}, Allowed: true},
			{Name: "WEB 1080p", Qualities: []quality.ID{quality.WebDL1080p, quality.WebRip1080p}, Allowed: true},
			{Qualities: []quality.ID{quality.HDTV720p}, Allowed: true},
		},
		Cutoff:         "WEB 1080p",
		UpgradeAllowed: true,
		Languages:      []string{"en", quality.LanguageOriginal},
	}
}

func TestProfile_Rank(t *testing.T) {
	t.Parallel()
	p := hdProfile()
	tests := []struct {
		id          quality.ID
		wantRank    int
		wantAllowed bool
	}{
		{quality.Remux1080p, 4, false},
		{quality.Bluray1080p, 3, true},
		{quality.WebDL1080p, 2, true},
		{quality.WebRip1080p, 2, true},
		{quality.HDTV720p, 1, true},
		{quality.Bluray2160p, 0, false},
	}
	for _, tt := range tests {
		t.Run(string(tt.id), func(t *testing.T) {
			t.Parallel()
			rank, allowed := p.Rank(tt.id)
			if rank != tt.wantRank || allowed != tt.wantAllowed {
				t.Errorf("Rank = %d, %v; want %d, %v", rank, allowed, tt.wantRank, tt.wantAllowed)
			}
			if p.Allowed(tt.id) != tt.wantAllowed {
				t.Errorf("Allowed = %v", !tt.wantAllowed)
			}
		})
	}
}

func TestProfile_cutoffAndItems(t *testing.T) {
	t.Parallel()
	p := hdProfile()
	if got := p.CutoffRank(); got != 2 {
		t.Errorf("CutoffRank = %d, want 2", got)
	}
	if got := p.CutoffLabel(); got != "WEB 1080p" {
		t.Errorf("CutoffLabel = %q", got)
	}
	it, ok := p.ItemOf(quality.WebRip1080p)
	if !ok || it.Name != "WEB 1080p" || !it.IsGroup() {
		t.Errorf("ItemOf(webrip-1080p) = %+v, %v", it, ok)
	}
	if _, ok := p.ItemOf(quality.Cam); ok {
		t.Error("ItemOf(cam) found an item")
	}
	p.Cutoff = string(quality.Bluray1080p)
	if got := p.CutoffLabel(); got != "Bluray-1080p" {
		t.Errorf("CutoffLabel = %q", got)
	}
	p.Cutoff = ""
	if p.CutoffRank() != 0 || p.CutoffLabel() != "" {
		t.Error("empty cutoff should have rank 0")
	}
	p.Cutoff = "nowhere"
	if p.CutoffRank() != 0 || p.CutoffLabel() != "nowhere" {
		t.Error("missing cutoff should have rank 0 and keep its name")
	}
	if p.UpgradeStep() != 1 {
		t.Errorf("UpgradeStep with 0 = %d, want 1", p.UpgradeStep())
	}
	p.MinUpgradeStep = 25
	if p.UpgradeStep() != 25 {
		t.Errorf("UpgradeStep = %d, want 25", p.UpgradeStep())
	}
}

// TestProfile_cutoffMemberOfGroup: a cutoff naming a member quality of a
// group stands for the group (same rank and label as the group name), as the
// database and the movie_state view accept it.
func TestProfile_cutoffMemberOfGroup(t *testing.T) {
	t.Parallel()
	byGroup := hdProfile()
	for _, member := range []quality.ID{quality.WebDL1080p, quality.WebRip1080p} {
		p := hdProfile()
		p.Cutoff = string(member)
		if got, want := p.CutoffRank(), byGroup.CutoffRank(); got != want {
			t.Errorf("CutoffRank(%s) = %d, want the group's %d", member, got, want)
		}
		if got := p.CutoffLabel(); got != "WEB 1080p" {
			t.Errorf("CutoffLabel(%s) = %q, want the group name", member, got)
		}
		if err := p.Validate(nil); err != nil {
			t.Errorf("Validate(%s) = %v", member, err)
		}
	}
}

func TestProfile_WantsAnyLanguage(t *testing.T) {
	t.Parallel()
	for _, tt := range []struct {
		langs []string
		want  bool
	}{
		{nil, true},
		{[]string{"any"}, true},
		{[]string{"en"}, false},
		{[]string{"original"}, false},
	} {
		p := quality.Profile{Languages: tt.langs}
		if got := p.WantsAnyLanguage(); got != tt.want {
			t.Errorf("WantsAnyLanguage(%v) = %v", tt.langs, got)
		}
	}
}

func TestProfile_Validate(t *testing.T) {
	t.Parallel()
	rules := []*quality.Rule{quality.MustRule("Known", "", quality.Condition{Kind: quality.KindCodec, Value: "h265"})}
	tests := []struct {
		name    string
		edit    func(p *quality.Profile)
		wantErr string
	}{
		{"valid", func(*quality.Profile) {}, ""},
		{"empty name", func(p *quality.Profile) { p.Name = " " }, "name is empty"},
		{"no items", func(p *quality.Profile) { p.Items = nil; p.Cutoff = "" }, "has no qualities"},
		{"nothing allowed", func(p *quality.Profile) {
			for i := range p.Items {
				p.Items[i].Allowed = false
			}
		}, "allows no quality"},
		{"cutoff absent", func(p *quality.Profile) { p.Cutoff = string(quality.Cam) }, `cutoff "cam" is not a quality or group of the profile`},
		{"cutoff not allowed", func(p *quality.Profile) { p.Cutoff = string(quality.Remux1080p) }, "cutoff Remux-1080p is not an allowed quality"},
		{"cutoff member of a group", func(p *quality.Profile) { p.Cutoff = string(quality.WebRip1080p) }, ""},
		{"cutoff member of a refused group", func(p *quality.Profile) {
			p.Items[2].Allowed = false
			p.Cutoff = string(quality.WebDL1080p)
		}, "cutoff WEB 1080p is not an allowed quality"},
		{"cutoff is a group-less quality name", func(p *quality.Profile) { p.Cutoff = "Bluray-1080p" }, `cutoff "Bluray-1080p" is not a quality or group of the profile`},
		{"duplicate quality", func(p *quality.Profile) {
			p.Items = append(p.Items, quality.Item{Qualities: []quality.ID{quality.WebDL1080p}})
		}, "quality WEBDL-1080p appears twice (in WEB 1080p and WEBDL-1080p)"},
		{"unknown quality", func(p *quality.Profile) {
			p.Items = append(p.Items, quality.Item{Qualities: []quality.ID{"vhs"}})
		}, `unknown quality "vhs"`},
		{"empty item", func(p *quality.Profile) { p.Items = append(p.Items, quality.Item{Name: "Empty"}) }, `item 5 ("Empty") has no qualities`},
		{"unnamed group", func(p *quality.Profile) {
			p.Items = append(p.Items, quality.Item{Qualities: []quality.ID{quality.Cam, quality.Telesync}})
		}, "item 5 groups 2 qualities but has no group name"},
		{"duplicate group", func(p *quality.Profile) {
			p.Items = append(p.Items, quality.Item{Name: "WEB 1080p", Qualities: []quality.ID{quality.Cam}})
		}, `group name "WEB 1080p" is used twice`},
		{"group named like a quality", func(p *quality.Profile) {
			p.Items = append(p.Items, quality.Item{Name: "cam", Qualities: []quality.ID{quality.Cam}})
		}, `group name "cam" is also the name of a quality`},
		{"group named like a quality display name", func(p *quality.Profile) {
			p.Items = append(p.Items, quality.Item{Name: "bluray-720P", Qualities: []quality.ID{quality.Cam}})
		}, `group name "bluray-720P" is also the name of a quality`},
		{"duplicate group, other case", func(p *quality.Profile) {
			p.Items = append(p.Items, quality.Item{Name: "web 1080P", Qualities: []quality.ID{quality.Cam}})
		}, `group name "web 1080P" is used twice (names are compared without case)`},
		{"blank group name", func(p *quality.Profile) {
			p.Items = append(p.Items, quality.Item{Name: "   ", Qualities: []quality.ID{quality.Cam}})
		}, "item 5 has a blank group name"},
		{"long group name", func(p *quality.Profile) {
			p.Items = append(p.Items, quality.Item{Name: strings.Repeat("g", 101), Qualities: []quality.ID{quality.Cam}})
		}, "is longer than 100 characters"},
		{"one-quality group", func(p *quality.Profile) {
			p.Items = append(p.Items, quality.Item{Name: "Old", Qualities: []quality.ID{quality.Cam}})
		}, ""},
		{"negative step", func(p *quality.Profile) { p.MinUpgradeStep = -1 }, "minimum upgrade step -1 is negative"},
		{"unknown language", func(p *quality.Profile) { p.Languages = []string{"klingon"} }, `unknown language "klingon"`},
		{"duplicate language", func(p *quality.Profile) { p.Languages = []string{"fr", "fr"} }, `language "fr" is listed twice`},
		{"any with others", func(p *quality.Profile) { p.Languages = []string{"any", "fr"} }, `language "any" cannot be combined`},
		{"unknown rule", func(p *quality.Profile) { p.Scores = map[string]int{"Known": 1, "Ghost": 5} }, `score given to unknown scoring rule "Ghost"`},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			p := hdProfile()
			tt.edit(&p)
			err := p.Validate(rules)
			checkErr(t, err, tt.wantErr)
			if err != nil && !errors.Is(err, quality.ErrInvalid) {
				t.Errorf("error does not wrap ErrInvalid: %v", err)
			}
		})
	}
}

func TestProfile_Validate_nilRulesSkipsScores(t *testing.T) {
	t.Parallel()
	p := hdProfile()
	p.Scores = map[string]int{"Ghost": 5}
	if err := p.Validate(nil); err != nil {
		t.Errorf("Validate(nil) = %v", err)
	}
}

func TestItem_Label(t *testing.T) {
	t.Parallel()
	if got := (&quality.Item{}).Label(); got != "" {
		t.Errorf("empty item label = %q", got)
	}
}
