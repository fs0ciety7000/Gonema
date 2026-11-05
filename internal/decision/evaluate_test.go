package decision_test

import (
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/google/go-cmp/cmp"

	"github.com/fs0ciety7000/gonema/internal/decision"
	"github.com/fs0ciety7000/gonema/internal/parser"
	"github.com/fs0ciety7000/gonema/internal/quality"
)

const good = "Alien.Romulus.2024.1080p.BluRay.x264-SPARKS"

func TestNewEvaluator_errors(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name string
		edit func(*decision.Config)
		want string
	}{
		{"no title", func(c *decision.Config) { c.Movie.Title = "" }, "movie title is empty"},
		{"no clock", func(c *decision.Config) { c.Options.Now = time.Time{} }, "Options.Now is not set"},
		{"bad profile", func(c *decision.Config) { c.Profile.Cutoff = "vhs" }, `cutoff "vhs" is not a quality or group`},
		{"duplicate rules", func(c *decision.Config) { c.Rules = append(c.Rules, c.Rules[0]) }, "is used twice"},
		{"bad sizes", func(c *decision.Config) { c.Sizes[quality.Cam] = quality.SizeLimit{MinMBPerMin: -1} }, "must not be negative"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			cfg := baseConfig(t)
			tt.edit(&cfg)
			e, err := decision.NewEvaluator(cfg)
			if e != nil || !errors.Is(err, decision.ErrInvalidConfig) || !strings.Contains(err.Error(), tt.want) {
				t.Errorf("NewEvaluator = %v, %v; want ErrInvalidConfig containing %q", e, err, tt.want)
			}
		})
	}
}

func TestEvaluate_accepted(t *testing.T) {
	t.Parallel()
	e := newEvaluator(t, baseConfig(t))
	c := usenet(good, 9*gb)
	d := e.Evaluate(&c)
	if !d.Accepted || d.Overridden || len(d.Rejections) != 0 {
		t.Fatalf("decision = %+v, want accepted", d)
	}
	if d.Quality != quality.Bluray1080p || d.QualityRank == 0 || d.Upgrade != nil {
		t.Errorf("decision = %+v", d)
	}
	_, pref, _ := quality.DefaultSizes()[quality.Bluray1080p].Bytes(119)
	if want := 9*gb - pref; d.SizeDelta != want {
		t.Errorf("SizeDelta = %d, want %d", d.SizeDelta, want)
	}
	if !d.Grabbable() || d.Has(decision.CodeBlocklisted) {
		t.Error("Grabbable/Has wrong")
	}
	if e.Profile().Name != quality.ProfileHD1080p {
		t.Error("Profile accessor")
	}
}

// TestEvaluate_rejections has one case per rejection code, with the exact message
// locked for most of them.
func TestEvaluate_rejections(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name      string
		edit      func(*decision.Config)
		cand      decision.Candidate
		code      decision.Code
		message   string // exact, if set
		permanent bool
	}{
		{
			name: "title", cand: usenet("Aliens.1986.1080p.BluRay.x264-SPARKS", 9*gb), code: decision.CodeTitleMismatch,
			message: `Title "Aliens" does not match "Alien: Romulus"`, permanent: true,
		},
		{
			name: "no title", cand: usenet("a8f7c6d5e4b3a29182736455463728ab", 9*gb), code: decision.CodeTitleMismatch,
			message: "Could not read a movie title from the release name", permanent: true,
		},
		{
			name: "year", cand: usenet("Alien.Romulus.2021.1080p.BluRay.x264-SPARKS", 9*gb), code: decision.CodeYearMismatch,
			message: "Year 2021 does not match 2024 (±1 tolerated)", permanent: true,
		},
		{
			name: "unknown quality", cand: usenet("Alien.Romulus.2024.1080p.x264-SPARKS", 9*gb), code: decision.CodeUnknownQuality,
			message: "Quality could not be determined from the release name", permanent: true,
		},
		{
			name: "quality not allowed", cand: usenet("Alien.Romulus.2024.720p.WEB-DL.DDP5.1.H.264-NTG", 3*gb), code: decision.CodeQualityNotAllowed,
			message: "WEBDL-720p is not allowed by profile HD-1080p", permanent: true,
		},
		{
			name: "language", cand: usenet("Alien.Romulus.2024.POLISH.1080p.WEB.H264-FLAME", 5*gb), code: decision.CodeWrongLanguage,
			message: "Language Polish is not wanted by profile HD-1080p (wanted: original (English))", permanent: true,
		},
		{
			name: "hardcoded subs", cand: usenet("Alien.Romulus.2024.1080p.WEB.H264.KORSUB-GRP", 5*gb), code: decision.CodeHardcodedSubs,
			message: "Release has hardcoded subtitles (KORSUB)", permanent: true,
		},
		{
			name: "min score", cand: usenet("Alien.Romulus.2024.1080p.BluRay.x264-YIFY", 9*gb), code: decision.CodeBelowMinScore,
			message: "Custom score -1000 is below the minimum of -50 for profile HD-1080p", permanent: true,
		},
		{
			name: "too small", cand: usenet(good, gb), code: decision.CodeSizeTooSmall,
			message: "Size 1.0 GB is below the 1.7 GB minimum for Bluray-1080p at 119 min", permanent: true,
		},
		{
			name: "too large", edit: func(c *decision.Config) { c.Movie.RuntimeMinutes = 132 }, cand: usenet(good, 41*gb+gb/5), code: decision.CodeSizeTooLarge,
			message: "Size 41.2 GB is above the 24.5 GB maximum for Bluray-1080p at 132 min", permanent: true,
		},
		{
			name: "blocklisted title",
			edit: func(c *decision.Config) {
				c.Blocklist = []decision.BlocklistEntry{{Kind: decision.BlockRelease, SourceTitle: strings.ToUpper(good)}}
			},
			cand: usenet(good, 9*gb), code: decision.CodeBlocklisted, message: "Release is blocklisted", permanent: true,
		},
		{
			name: "not monitored", edit: func(c *decision.Config) { c.Movie.Monitored = false },
			cand: usenet(good, 9*gb), code: decision.CodeNotMonitored, message: "Movie is not monitored", permanent: true,
		},
		{
			name: "not available",
			edit: func(c *decision.Config) {
				c.Movie.Status = decision.InCinemas
				c.Movie.DigitalRelease = time.Date(2026, 12, 1, 0, 0, 0, 0, time.UTC)
			},
			cand: usenet(good, 9*gb), code: decision.CodeNotAvailable,
			message: `Movie is not available yet: minimum availability is "released", expected 2026-12-01`,
		},
		{
			name: "seeders", cand: torrent(good, 9*gb, 0), code: decision.CodeNotEnoughSeeders,
			message: "0 seeders, indexer TorrentLeech requires at least 1",
		},
		{
			name: "minimum age", edit: func(c *decision.Config) { c.Options.UsenetMinimumAge = 30 * time.Minute },
			cand: func() decision.Candidate {
				c := usenet(good, 9*gb)
				c.PublishDate = now.Add(-12 * time.Minute)
				return c
			}(),
			code: decision.CodeMinimumAge, message: "Posted 12 min ago, waiting for the 30 min minimum age",
		},
		{
			name: "retention", edit: func(c *decision.Config) { c.Options.UsenetRetentionDays = 1000 },
			cand: func() decision.Candidate {
				c := usenet(good, 9*gb)
				c.PublishDate = now.Add(-1200 * 24 * time.Hour)
				return c
			}(),
			code: decision.CodeRetention, message: "Posted 1200 days ago, beyond the 1000-day Usenet retention", permanent: true,
		},
		{
			name: "already grabbed",
			cand: func() decision.Candidate {
				c := usenet(good, 9*gb)
				c.AlreadyGrabbed = true
				return c
			}(),
			code: decision.CodeAlreadyGrabbed, message: "Release is already in the download queue or was already grabbed",
		},
		{
			name: "upgrades disabled",
			edit: func(c *decision.Config) {
				c.Profile.UpgradeAllowed = false
				c.Current = &decision.CurrentFile{FileQuality: decision.FileQuality{Quality: quality.WebDL1080p, Revision: parser.Revision{Version: 1}}}
			},
			cand: usenet(good, 9*gb), code: decision.CodeUpgradesDisabled,
			message: "Movie already has a WEBDL-1080p file and profile HD-1080p does not allow upgrades", permanent: true,
		},
		{
			name: "cutoff met",
			edit: func(c *decision.Config) {
				c.Current = &decision.CurrentFile{FileQuality: decision.FileQuality{Quality: quality.Bluray1080p, Revision: parser.Revision{Version: 1}}}
			},
			cand: usenet("Alien.Romulus.2024.1080p.BluRay.REMUX.AVC.DTS-HD.MA.5.1-FGT", 30*gb), code: decision.CodeCutoffMet,
			message: "Existing Bluray-1080p file (score 0) already meets the cutoff of profile HD-1080p (Bluray-1080p, score 0)", permanent: true,
		},
		{
			name: "lower rank",
			edit: func(c *decision.Config) {
				c.Profile.Cutoff = string(quality.Remux1080p)
				c.Current = &decision.CurrentFile{FileQuality: decision.FileQuality{Quality: quality.Bluray1080p, Revision: parser.Revision{Version: 1}}}
			},
			cand: usenet("Alien.Romulus.2024.1080p.WEB-DL.DDP5.1.H.264-NTG", 6*gb), code: decision.CodeNotUpgrade,
			message: "WEBDL-1080p ranks below the existing Bluray-1080p file in profile HD-1080p", permanent: true,
		},
		{
			name: "older revision",
			edit: func(c *decision.Config) {
				c.Profile.Cutoff = string(quality.Remux1080p)
				c.Current = &decision.CurrentFile{FileQuality: decision.FileQuality{Quality: quality.Bluray1080p, Revision: parser.Revision{Version: 2}}}
			},
			cand: usenet(good, 9*gb), code: decision.CodeNotUpgrade,
			message: "Revision v1 is older than the existing file's v2", permanent: true,
		},
		{
			name: "score step",
			edit: func(c *decision.Config) {
				c.Profile.Cutoff = string(quality.Remux1080p)
				c.Current = &decision.CurrentFile{FileQuality: decision.FileQuality{Quality: quality.Bluray1080p, Revision: parser.Revision{Version: 1}}}
			},
			cand: func() decision.Candidate {
				c := usenet(good, 9*gb)
				c.IndexerFlags = []string{"freeleech"}
				return c
			}(),
			code:    decision.CodeNotUpgrade,
			message: "Custom score 5 does not beat the existing Bluray-1080p file's 0 by the minimum step of 10", permanent: true,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			cfg := baseConfig(t)
			if tt.edit != nil {
				tt.edit(&cfg)
			}
			e := newEvaluator(t, cfg)
			d := e.Evaluate(&tt.cand)
			if d.Accepted {
				t.Fatalf("accepted, want %s", tt.code)
			}
			var got *decision.Rejection
			for i := range d.Rejections {
				if d.Rejections[i].Code == tt.code {
					got = &d.Rejections[i]
				}
			}
			if got == nil {
				t.Fatalf("rejections %v lack %s", d.Rejections, tt.code)
			}
			if tt.message != "" && got.Message != tt.message {
				t.Errorf("message:\n got %q\nwant %q", got.Message, tt.message)
			}
			if got.Permanent != tt.permanent {
				t.Errorf("permanent = %v, want %v", got.Permanent, tt.permanent)
			}
		})
	}
}

// TestEvaluate_collectsAll checks that rejections are not short-circuited and come
// in a deterministic order.
func TestEvaluate_collectsAll(t *testing.T) {
	t.Parallel()
	cfg := baseConfig(t)
	cfg.Movie.Monitored = false
	cfg.Blocklist = []decision.BlocklistEntry{{Kind: decision.BlockGroup, ReleaseGroup: "yify"}}
	e := newEvaluator(t, cfg)
	c := torrent("Aliens.1986.720p.BluRay.x264.KORSUB-YIFY", 50*gb, 0)
	c.AlreadyGrabbed = true
	d := e.Evaluate(&c)
	want := []decision.Code{
		decision.CodeTitleMismatch, decision.CodeYearMismatch, decision.CodeQualityNotAllowed,
		decision.CodeHardcodedSubs, decision.CodeBelowMinScore, decision.CodeSizeTooLarge, decision.CodeBlocklisted,
		decision.CodeNotMonitored, decision.CodeNotEnoughSeeders, decision.CodeAlreadyGrabbed,
	}
	if diff := cmp.Diff(want, codes(&d)); diff != "" {
		t.Errorf("codes (-want +got):\n%s", diff)
	}
	if got := d.Rejections[6].Message; got != "Release group YIFY is blocklisted" {
		t.Errorf("group message = %q", got)
	}
	again := e.Evaluate(&c)
	if diff := cmp.Diff(d, again); diff != "" {
		t.Errorf("Evaluate is not deterministic:\n%s", diff)
	}
}

func TestEvaluate_override(t *testing.T) {
	t.Parallel()
	cfg := baseConfig(t)
	cfg.Options.Override = true
	cfg.Options.Interactive = true
	e := newEvaluator(t, cfg)
	c := usenet("Alien.Romulus.2024.720p.WEB-DL.DDP5.1.H.264-NTG", 3*gb)
	d := e.Evaluate(&c)
	if d.Accepted || !d.Overridden || !d.Grabbable() || !d.Has(decision.CodeQualityNotAllowed) {
		t.Errorf("override decision = %+v", d)
	}
	ok := usenet(good, 9*gb)
	if d := e.Evaluate(&ok); !d.Accepted || d.Overridden {
		t.Errorf("accepted release marked overridden: %+v", d)
	}
}

func TestEvaluate_titleMatching(t *testing.T) {
	t.Parallel()
	cfg := baseConfig(t)
	cfg.Movie.AlternateTitles = []string{"Alien: Romulus", "Чужой: Ромул", "  "}
	cfg.Movie.CleanTitle = ""
	e := newEvaluator(t, cfg)
	tests := []struct {
		name string
		want bool
	}{
		{"Alien.Romulus.2024.1080p.BluRay.x264-SPARKS", true},
		{"ALIEN.ROMULUS.2025.1080p.BluRay.x264-SPARKS", true}, // year +1 tolerated
		{"Alien.Romulus.2023.1080p.BluRay.x264-SPARKS", true}, // year -1 tolerated
		{"Чужой.Ромул.2024.1080p.BluRay.x264-SPARKS", true},   // alternate title
		{"Xenomorph.AKA.Alien.Romulus.2024.1080p.BluRay.x264-SPARKS", true},
		{"Alien.2024.1080p.BluRay.x264-SPARKS", false},
		{"Alien.Romulus.Extended.Cut.Of.Something.2024.1080p.BluRay.x264-SPARKS", false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			c := usenet(tt.name, 9*gb)
			d := e.Evaluate(&c)
			got := !d.Has(decision.CodeTitleMismatch) && !d.Has(decision.CodeYearMismatch)
			if got != tt.want {
				t.Errorf("title/year match = %v, want %v (%v)", got, tt.want, d.Rejections)
			}
		})
	}
}

func TestEvaluate_notes(t *testing.T) {
	t.Parallel()
	cfg := baseConfig(t)
	cfg.Movie.RuntimeMinutes = 0
	e := newEvaluator(t, cfg)
	c := usenet("Alien.Romulus.1080p.BluRay.x264-SPARKS", 1)
	d := e.Evaluate(&c)
	want := []string{"release has no year: year not checked", "runtime unknown: size not checked"}
	if diff := cmp.Diff(want, d.Notes); diff != "" || !d.Accepted || d.SizeDelta != -1 {
		t.Errorf("notes (-want +got):\n%s\n%+v", diff, d)
	}
	c = usenet(good, 0)
	if d := e.Evaluate(&c); len(d.Notes) != 1 || d.Notes[0] != "size unknown: size not checked" {
		t.Errorf("notes = %v", d.Notes)
	}
	// No size limit configured for the quality: nothing to check, no note.
	cfg = baseConfig(t)
	cfg.Sizes = nil
	e = newEvaluator(t, cfg)
	c = usenet(good, 100*gb)
	if d := e.Evaluate(&c); !d.Accepted || len(d.Notes) != 0 {
		t.Errorf("without sizes: %+v", d)
	}
}

func TestEvaluate_languages(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name     string
		langs    []string
		original parser.Language
		release  string
		want     bool
	}{
		{"untagged is original", []string{"original"}, parser.LanguageEnglish, "Alien.Romulus.2024.1080p.BluRay.x264-SPARKS", true},
		{"tagged foreign", []string{"original"}, parser.LanguageEnglish, "Alien.Romulus.2024.TRUEFRENCH.1080p.BluRay.x264-Ulysse", false},
		{"multi carries original", []string{"original"}, parser.LanguageEnglish, "Alien.Romulus.2024.MULTi.TRUEFRENCH.1080p.BluRay.x264-Ulysse", true},
		{"german DL carries original", []string{"original"}, parser.LanguageEnglish, "Alien.Romulus.2024.German.DL.1080p.BluRay.x264-GMA", true},
		{"explicit french", []string{"fr"}, parser.LanguageEnglish, "Alien.Romulus.2024.TRUEFRENCH.1080p.BluRay.x264-Ulysse", true},
		{"explicit french, untagged", []string{"fr"}, parser.LanguageEnglish, "Alien.Romulus.2024.1080p.BluRay.x264-SPARKS", false},
		{"any", []string{"any"}, parser.LanguageEnglish, "Alien.Romulus.2024.POLISH.1080p.WEB.H264-FLAME", true},
		{"empty means any", nil, parser.LanguageEnglish, "Alien.Romulus.2024.POLISH.1080p.WEB.H264-FLAME", true},
		{"original unknown, untagged", []string{"original"}, 0, "Alien.Romulus.2024.1080p.BluRay.x264-SPARKS", true},
		{"original unknown, multi", []string{"original"}, 0, "Alien.Romulus.2024.MULTi.TRUEFRENCH.1080p.BluRay.x264-Ulysse", true},
		{"original unknown, tagged", []string{"original"}, 0, "Alien.Romulus.2024.POLISH.1080p.WEB.H264-FLAME", false},
		{"english wanted, original unknown, untagged", []string{"en"}, 0, "Alien.Romulus.2024.1080p.BluRay.x264-SPARKS", true},
		{"japanese original untagged", []string{"en"}, parser.LanguageJapanese, "Alien.Romulus.2024.1080p.BluRay.x264-SPARKS", false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			cfg := baseConfig(t)
			cfg.Profile.Languages = tt.langs
			cfg.Movie.OriginalLanguage = tt.original
			e := newEvaluator(t, cfg)
			c := usenet(tt.release, 9*gb)
			d := e.Evaluate(&c)
			if got := !d.Has(decision.CodeWrongLanguage); got != tt.want {
				t.Errorf("language ok = %v, want %v (%v)", got, tt.want, d.Rejections)
			}
		})
	}
}

func TestEvaluate_languageMessages(t *testing.T) {
	t.Parallel()
	cfg := baseConfig(t)
	cfg.Profile.Languages = []string{"fr", "original"}
	cfg.Movie.OriginalLanguage = parser.LanguageJapanese
	e := newEvaluator(t, cfg)
	tests := []struct{ release, want string }{
		{"Alien.Romulus.2024.German.ITALIAN.1080p.BluRay.x264-GRP",
			"Languages German, Italian are not wanted by profile HD-1080p (wanted: French, original (Japanese))"},
	}
	for _, tt := range tests {
		c := usenet(tt.release, 9*gb)
		d := e.Evaluate(&c)
		if len(d.Rejections) != 1 || d.Rejections[0].Message != tt.want {
			t.Errorf("%s: rejections = %v\nwant %q", tt.release, d.Rejections, tt.want)
		}
	}
	cfg.Profile.Languages = []string{"fr"}
	e = newEvaluator(t, cfg)
	c := usenet("Alien.Romulus.2024.1080p.BluRay.x264-SPARKS", 9*gb)
	d := e.Evaluate(&c)
	if want := "Language Japanese (assumed: untagged) is not wanted by profile HD-1080p (wanted: French)"; len(d.Rejections) != 1 || d.Rejections[0].Message != want {
		t.Errorf("rejections = %v, want %q", d.Rejections, want)
	}
	cfg.Profile.Languages = []string{"original"}
	cfg.Movie.OriginalLanguage = 0
	e = newEvaluator(t, cfg)
	c = usenet("Alien.Romulus.2024.POLISH.1080p.WEB.H264-FLAME", 5*gb)
	d = e.Evaluate(&c)
	if want := "Language Polish is not wanted by profile HD-1080p (wanted: original)"; len(d.Rejections) != 1 || d.Rejections[0].Message != want {
		t.Errorf("rejections = %v, want %q", d.Rejections, want)
	}
}

func TestEvaluate_hardcodedSubsAllowed(t *testing.T) {
	t.Parallel()
	cfg := baseConfig(t)
	cfg.Options.AllowHardcodedSubs = true
	e := newEvaluator(t, cfg)
	c := usenet("Alien.Romulus.2024.1080p.WEB.H264.KORSUB-GRP", 5*gb)
	d := e.Evaluate(&c)
	if !d.Accepted || d.Score.Total != -20 {
		t.Errorf("allowed hardcoded subs: accepted %v score %d (%v)", d.Accepted, d.Score.Total, d.Rejections)
	}
	// Hardcoded subs detected without a tag text.
	cfg.Options.AllowHardcodedSubs = false
	e = newEvaluator(t, cfg)
	c.Parsed.HardcodedSubsTag = ""
	if d := e.Evaluate(&c); !d.Has(decision.CodeHardcodedSubs) || d.Rejections[0].Message != "Release has hardcoded subtitles" {
		t.Errorf("rejections = %v", d.Rejections)
	}
}

func TestEvaluate_blocklist(t *testing.T) {
	t.Parallel()
	c := torrent(good, 9*gb, 50)
	tests := []struct {
		name  string
		entry decision.BlocklistEntry
		want  string // empty: not blocklisted
	}{
		{"hash", decision.BlocklistEntry{Kind: decision.BlockRelease, InfoHash: strings.ToLower(c.InfoHash)}, "Torrent hash abcdef0123456789abcdef0123456789abcdef01 is blocklisted"},
		{"guid", decision.BlocklistEntry{Kind: decision.BlockRelease, GUID: c.GUID}, "Release is blocklisted (same indexer GUID)"},
		{"title", decision.BlocklistEntry{Kind: decision.BlockRelease, SourceTitle: " " + good + " "}, "Release is blocklisted"},
		{"group", decision.BlocklistEntry{Kind: decision.BlockGroup, ReleaseGroup: "sparks"}, "Release group SPARKS is blocklisted"},
		{"other release", decision.BlocklistEntry{Kind: decision.BlockRelease, SourceTitle: "Other", GUID: "x", InfoHash: "y"}, ""},
		{"group entry ignores title", decision.BlocklistEntry{Kind: decision.BlockGroup, SourceTitle: good}, ""},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			cfg := baseConfig(t)
			cfg.Blocklist = []decision.BlocklistEntry{tt.entry}
			d := newEvaluator(t, cfg).Evaluate(&c)
			switch {
			case tt.want == "" && !d.Accepted:
				t.Errorf("rejected: %v", d.Rejections)
			case tt.want != "" && (len(d.Rejections) != 1 || d.Rejections[0].Message != tt.want):
				t.Errorf("rejections = %v, want %q", d.Rejections, tt.want)
			}
		})
	}
}

func TestEvaluate_seeders(t *testing.T) {
	t.Parallel()
	cfg := baseConfig(t)
	cfg.Indexers = map[int64]decision.IndexerSettings{2: {MinSeeders: 5}}
	e := newEvaluator(t, cfg)
	tests := []struct {
		seeders int
		want    string
	}{
		{-1, ""},
		{5, ""},
		{4, "4 seeders, indexer TorrentLeech requires at least 5"},
		{1, "1 seeder, indexer TorrentLeech requires at least 5"},
	}
	for _, tt := range tests {
		c := torrent(good, 9*gb, tt.seeders)
		d := e.Evaluate(&c)
		got := ""
		if len(d.Rejections) > 0 {
			got = d.Rejections[0].Message
		}
		if got != tt.want {
			t.Errorf("seeders %d: %q, want %q", tt.seeders, got, tt.want)
		}
	}
	c := torrent(good, 9*gb, 0)
	c.IndexerID, c.IndexerName = 9, ""
	if d := e.Evaluate(&c); len(d.Rejections) != 1 || d.Rejections[0].Message != "0 seeders, indexer #9 requires at least 1" {
		t.Errorf("default min seeders: %v", d.Rejections)
	}
}

func TestEvaluate_usenetAge(t *testing.T) {
	t.Parallel()
	cfg := baseConfig(t)
	cfg.Options.UsenetMinimumAge = 2 * time.Hour
	cfg.Options.UsenetRetentionDays = 100
	e := newEvaluator(t, cfg)
	c := usenet(good, 9*gb)
	c.PublishDate = now.Add(-90 * time.Minute)
	if d := e.Evaluate(&c); len(d.Rejections) != 1 || d.Rejections[0].Message != "Posted 1.5 h ago, waiting for the 2.0 h minimum age" {
		t.Errorf("rejections = %v", d.Rejections)
	}
	c.PublishDate = time.Time{}
	if d := e.Evaluate(&c); !d.Accepted {
		t.Errorf("unknown publish date rejected: %v", d.Rejections)
	}
	tc := torrent(good, 9*gb, 10)
	tc.PublishDate = now.Add(-1000 * 24 * time.Hour)
	if d := e.Evaluate(&tc); !d.Accepted {
		t.Errorf("retention applied to a torrent: %v", d.Rejections)
	}
}

func TestEvaluate_interactiveIgnoresMonitored(t *testing.T) {
	t.Parallel()
	cfg := baseConfig(t)
	cfg.Movie.Monitored = false
	cfg.Options.Interactive = true
	c := usenet(good, 9*gb)
	if d := newEvaluator(t, cfg).Evaluate(&c); !d.Accepted {
		t.Errorf("interactive search rejected an unmonitored movie: %v", d.Rejections)
	}
}

func TestEvaluate_availabilityNoDate(t *testing.T) {
	t.Parallel()
	cfg := baseConfig(t)
	cfg.Movie.Status = decision.Announced
	cfg.Movie.InCinemas, cfg.Movie.DigitalRelease = time.Time{}, time.Time{}
	c := usenet(good, 9*gb)
	d := newEvaluator(t, cfg).Evaluate(&c)
	want := `Movie is not available yet: minimum availability is "released" and no date is known`
	if len(d.Rejections) != 1 || d.Rejections[0].Message != want || d.Rejections[0].Permanent {
		t.Errorf("rejections = %v", d.Rejections)
	}
}

func TestAvailable(t *testing.T) {
	t.Parallel()
	day := func(y int, m time.Month, d int) time.Time { return time.Date(y, m, d, 0, 0, 0, 0, time.UTC) }
	tests := []struct {
		name   string
		movie  decision.Movie
		delay  time.Duration
		want   bool
		wantAt time.Time
	}{
		{"status reached", decision.Movie{Status: decision.Released, MinimumAvailability: decision.Released}, 0, true, time.Time{}},
		{"announced minimum", decision.Movie{Status: decision.Announced, MinimumAvailability: decision.Announced}, 0, true, time.Time{}},
		{"in cinemas by date", decision.Movie{MinimumAvailability: decision.InCinemas, InCinemas: day(2026, 11, 1)}, 0, true, day(2026, 11, 1)},
		{"in cinemas future", decision.Movie{MinimumAvailability: decision.InCinemas, InCinemas: day(2026, 12, 1)}, 0, false, day(2026, 12, 1)},
		{"released earliest of digital and physical", decision.Movie{
			MinimumAvailability: decision.Released, DigitalRelease: day(2026, 12, 20), PhysicalRelease: day(2026, 11, 1),
		}, 0, true, day(2026, 11, 1)},
		{"released digital only", decision.Movie{MinimumAvailability: decision.Released, DigitalRelease: day(2026, 11, 1)}, 0, true, day(2026, 11, 1)},
		{"released physical later", decision.Movie{
			MinimumAvailability: decision.Released, DigitalRelease: day(2026, 11, 20), PhysicalRelease: day(2026, 12, 1),
		}, 0, false, day(2026, 11, 20)},
		{"released fallback", decision.Movie{MinimumAvailability: decision.Released, InCinemas: day(2026, 8, 1)}, 0, true, day(2026, 10, 30)},
		{"released fallback future", decision.Movie{MinimumAvailability: decision.Released, InCinemas: day(2026, 9, 1)}, 0, false, day(2026, 11, 30)},
		{"delay", decision.Movie{MinimumAvailability: decision.InCinemas, InCinemas: day(2026, 11, 1)}, 7 * 24 * time.Hour, false, day(2026, 11, 8)},
		{"no date", decision.Movie{MinimumAvailability: decision.Released}, 0, false, time.Time{}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			got, at := decision.Available(&tt.movie, now, tt.delay)
			if got != tt.want || !at.Equal(tt.wantAt) {
				t.Errorf("Available = %v, %v; want %v, %v", got, at, tt.want, tt.wantAt)
			}
		})
	}
}

func TestAvailability_String(t *testing.T) {
	t.Parallel()
	if decision.InCinemas.String() != "in cinemas" || decision.Availability(9).String() != "Availability(9)" {
		t.Error("Availability.String")
	}
}
