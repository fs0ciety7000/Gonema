package quality

import (
	"slices"

	"github.com/fs0ciety7000/gonema/internal/parser"
)

// Starter profile names.
const (
	ProfileAny       = "Any"
	ProfileHD1080p   = "HD-1080p"
	ProfileUltraHD   = "Ultra-HD"
	ProfileRemux2160 = "Remux 2160p"
)

// Starter rule names.
const (
	RuleHDR10Plus     = "HDR10+"
	RuleDolbyVision   = "Dolby Vision"
	RuleX265HD        = "x265 (HD)"
	RuleHardcodedSubs = "Hardcoded subs"
	RuleLowQuality    = "Low-quality groups"
	RuleUpscaled      = "Upscaled"
	Rule3D            = "3D"
	RuleFreeleech     = "Freeleech"
)

// Word boundaries that also treat "_" as a separator (\b does not).
const (
	wordStart = `(?:^|[^a-z0-9])`
	wordEnd   = `(?:$|[^a-z0-9])`
)

// StarterRules returns the default scoring rules, seeded into a new database and
// editable afterwards. They carry no score: the starter profiles score them.
//
//   - HDR10+ and Dolby Vision: bonuses for UHD profiles.
//   - x265 (HD): HEVC at 720p/1080p. Most HD x265 releases are re-encodes of
//     re-encodes, so HD profiles give it a small penalty.
//   - Hardcoded subs: a small penalty, only relevant when hardcoded subtitles are
//     allowed at all (they are rejected by default; see package decision).
//   - Low-quality groups, Upscaled, 3D: known-bad releases, scored far below the
//     minimum score so that they are rejected.
//   - Freeleech: a tie-breaker bonus for torrents that do not count against ratio.
func StarterRules() []*Rule {
	return []*Rule{
		MustRule(RuleHDR10Plus, "HDR10+ dynamic metadata.",
			Condition{Kind: KindHDR, Value: "hdr10+"}),
		MustRule(RuleDolbyVision, "Dolby Vision.",
			Condition{Kind: KindHDR, Value: "dv"}),
		MustRule(RuleX265HD, "HEVC encode at 720p or 1080p.",
			Condition{Kind: KindCodec, Value: "h265", Required: true},
			Condition{Kind: KindResolution, Value: "720p"},
			Condition{Kind: KindResolution, Value: "1080p"}),
		MustRule(RuleHardcodedSubs, "Subtitles burned into the picture (HC, HARDSUB, KORSUB...).",
			Condition{Kind: KindTitleRegex, Value: wordStart +
				`(?:hc|hc[ ._-]?subs?|hard[ ._-]?subs?|subfrench|(?:kor|swe|fin|nor|dan|ger|ita|spa|chi|ara|heb|pol|dut)subs?)` + wordEnd}),
		MustRule(RuleLowQuality, "Groups known for low-quality encodes.",
			Condition{Kind: KindReleaseGroup, Value: "aXXo"},
			Condition{Kind: KindReleaseGroup, Value: "EVO"},
			Condition{Kind: KindReleaseGroup, Value: "nHD"},
			Condition{Kind: KindReleaseGroup, Value: "nSD"},
			Condition{Kind: KindReleaseGroup, Value: "STUTTERSHIT"},
			Condition{Kind: KindReleaseGroup, Value: "YIFY"},
			Condition{Kind: KindReleaseGroup, Value: "YTS"},
			Condition{Kind: KindReleaseGroup, Value: "YTS.MX"}),
		MustRule(RuleUpscaled, "Upscaled from a lower resolution.",
			Condition{Kind: KindTitleRegex, Value: wordStart + `(?:upscaled?|ai[ ._-]?upscaled?)` + wordEnd}),
		MustRule(Rule3D, "Stereoscopic 3D.",
			Condition{Kind: KindTitleRegex, Value: wordStart + `(?:3d|h-?sbs|h-?ou|half[ ._-]?sbs)` + wordEnd}),
		MustRule(RuleFreeleech, "Torrent marked freeleech by the indexer.",
			Condition{Kind: KindIndexerFlag, Value: "freeleech"}),
	}
}

// starterScores are the scores shared by every starter profile.
func starterScores(extra map[string]int) map[string]int {
	s := map[string]int{
		RuleHardcodedSubs: -20,
		RuleLowQuality:    -1000,
		RuleUpscaled:      -1000,
		Rule3D:            -1000,
		RuleFreeleech:     5,
	}
	for k, v := range extra {
		s[k] = v
	}
	return s
}

// buildItems lists the allowed items first, in order, then every other catalog
// quality as a disallowed single, best first, so the user can enable them.
func buildItems(allowed ...Item) []Item {
	items := slices.Clone(allowed)
	used := make(map[ID]bool)
	for i := range items {
		items[i].Allowed = true
		for _, q := range items[i].Qualities {
			used[q] = true
		}
	}
	for i := len(catalog) - 1; i >= 0; i-- {
		if q := catalog[i].ID; !used[q] {
			items = append(items, Item{Qualities: []ID{q}})
		}
	}
	return items
}

func single(id ID) Item { return Item{Qualities: []ID{id}} }

// StarterProfiles returns the default profiles, seeded into a new database and
// editable afterwards. Each call returns fresh values.
//
//   - Any: every real quality except full discs and Raw-HD, best first; upgrades
//     until Bluray-1080p.
//   - HD-1080p: Remux-1080p > Bluray-1080p > WEB 1080p (WEB-DL = WEBRip) >
//     HDTV-1080p; cutoff Bluray-1080p.
//   - Ultra-HD: Remux-2160p > Bluray-2160p > WEB 2160p > HDTV-2160p; cutoff
//     Remux-2160p and score 100 (Dolby Vision).
//   - Remux 2160p: Remux-2160p only; upgrades for score (DV, HDR10+) and propers.
//
// All want the original language except Any. MinScore is -50: mild penalties pass,
// known-bad rules (-1000) are rejected. MinUpgradeStep is 10, so the freeleech
// bonus alone never triggers an upgrade.
func StarterProfiles() []Profile {
	var anyItems []Item
	for i := len(catalog) - 1; i >= 0; i-- {
		switch q := catalog[i]; {
		case q.ID == Unknown, q.Modifier == parser.ModifierBRDisk, q.ID == RawHD:
		default:
			anyItems = append(anyItems, single(q.ID))
		}
	}
	return []Profile{
		{
			Name: ProfileAny, Items: buildItems(anyItems...), Cutoff: string(Bluray1080p),
			UpgradeAllowed: true, MinScore: -50, MinUpgradeStep: 10,
			Languages: []string{LanguageAny},
			Scores:    starterScores(map[string]int{RuleX265HD: -10}),
		},
		{
			Name: ProfileHD1080p,
			Items: buildItems(
				single(Remux1080p),
				single(Bluray1080p),
				Item{Name: "WEB 1080p", Qualities: []ID{WebDL1080p, WebRip1080p}},
				single(HDTV1080p),
			),
			Cutoff: string(Bluray1080p), UpgradeAllowed: true, MinScore: -50, MinUpgradeStep: 10,
			Languages: []string{LanguageOriginal},
			Scores:    starterScores(map[string]int{RuleX265HD: -10}),
		},
		{
			Name: ProfileUltraHD,
			Items: buildItems(
				single(Remux2160p),
				single(Bluray2160p),
				Item{Name: "WEB 2160p", Qualities: []ID{WebDL2160p, WebRip2160p}},
				single(HDTV2160p),
			),
			Cutoff: string(Remux2160p), UpgradeAllowed: true, MinScore: -50, CutoffScore: 100, MinUpgradeStep: 10,
			Languages: []string{LanguageOriginal},
			Scores:    starterScores(map[string]int{RuleHDR10Plus: 50, RuleDolbyVision: 100}),
		},
		{
			Name:   ProfileRemux2160,
			Items:  buildItems(single(Remux2160p)),
			Cutoff: string(Remux2160p), UpgradeAllowed: true, MinScore: -50, CutoffScore: 100, MinUpgradeStep: 10,
			Languages: []string{LanguageOriginal},
			Scores:    starterScores(map[string]int{RuleHDR10Plus: 50, RuleDolbyVision: 100}),
		},
	}
}

// StarterProfile returns the starter profile with that name.
func StarterProfile(name string) (Profile, bool) {
	for _, p := range StarterProfiles() {
		if p.Name == name {
			return p, true
		}
	}
	return Profile{}, false
}
