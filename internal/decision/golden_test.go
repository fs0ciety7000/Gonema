package decision_test

import (
	"bufio"
	"bytes"
	"encoding/json"
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
	"time"

	"github.com/fs0ciety7000/gonema/internal/decision"
	"github.com/fs0ciety7000/gonema/internal/parser"
	"github.com/fs0ciety7000/gonema/internal/quality"
)

var update = flag.Bool("update", false, "rewrite the golden files in testdata/decision")

var (
	corpusPath = filepath.Join("..", "..", "testdata", "releases", "corpus.jsonl")
	goldenDir  = filepath.Join("..", "..", "testdata", "decision")
)

// derivedNames are corpus names of other movies, retitled "Alien.Romulus.2024" to
// cover what the Alien: Romulus entries lack (remux, WEB-DL, HDR10+, cams, propers,
// hardcoded subtitles, Spanish multi...).
var derivedNames = []string{
	"Dune.Part.Two.2024.2160p.UHD.BluRay.REMUX.DV.HDR.HEVC.TrueHD.Atmos.7.1-GROUP",
	"Oppenheimer.2023.1080p.BluRay.REMUX.AVC.DTS-HD.MA.5.1-GROUP",
	"The.Batman.2022.2160p.HMAX.WEB-DL.DDP5.1.Atmos.DV.HDR10.HEVC-GROUP",
	"Glass.Onion.2022.1080p.NF.WEB-DL.DDP5.1.Atmos.H.264-GROUP",
	"Saltburn.2023.1080p.AMZN.WEBRip.DDP5.1.x264-GROUP",
	"Uncharted.2022.2160p.WEB-DL.DDP5.1.HDR10+.HEVC-GROUP",
	"The.Wailing.2016.KORSUB.720p.HDRip.x264-GROUP",
	"Avengers.Endgame.2019.HDCAM.x264-GROUP",
	"Everything.Everywhere.All.at.Once.2022.PROPER.2160p.UHD.BluRay.H265-GAZPROM",
	"Top.Gun.Maverick.2022.PROPER.SPANiSH.MULTi.1080p.BluRay.x264-CEBRAY",
	"Hereditary.2018.DV.2160p.WEB.H265-HEATHEN",
	"Joker.2019.HC.HDRip.XviD.AC3-GROUP",
}

var titleYear = regexp.MustCompile(`^.*?\.(?:19|20)\d\d\.`)

// searchNames returns the release names of the golden interactive search: every
// corpus entry whose clean title starts with "alien" (Alien: Romulus and the
// unrelated "Aliens..." an indexer returns for the query), then the derived names.
func searchNames(tb testing.TB) []string {
	tb.Helper()
	data, err := os.ReadFile(corpusPath)
	if err != nil {
		tb.Fatalf("read corpus: %v", err)
	}
	inCorpus := map[string]bool{}
	var names []string
	sc := bufio.NewScanner(bytes.NewReader(data))
	for sc.Scan() {
		var e struct {
			Name string `json:"name"`
			Want struct {
				CleanTitle string `json:"cleanTitle"`
			} `json:"want"`
		}
		if err := json.Unmarshal(sc.Bytes(), &e); err != nil {
			tb.Fatalf("corpus: %v", err)
		}
		inCorpus[e.Name] = true
		if strings.HasPrefix(e.Want.CleanTitle, "alien") {
			names = append(names, e.Name)
		}
	}
	if err := sc.Err(); err != nil {
		tb.Fatalf("corpus: %v", err)
	}
	for _, n := range derivedNames {
		if !inCorpus[n] {
			tb.Fatalf("derived name %q is not in the corpus", n)
		}
		names = append(names, titleYear.ReplaceAllString(n, "Alien.Romulus.2024."))
	}
	return names
}

// searchCandidates turns names into search results with deterministic metadata:
// three indexers (two Usenet, one torrent), sizes around the preferred size of
// each quality, staggered publish dates and seeders.
func searchCandidates(tb testing.TB, names []string, runtime int) []decision.Candidate {
	tb.Helper()
	sizes := quality.DefaultSizes()
	factors := []float64{1.0, 0.7, 1.3, 0.4, 2.2}
	out := make([]decision.Candidate, len(names))
	for i, n := range names {
		p := parser.Parse(n)
		mbPerMin := 20.0
		if l, ok := sizes.Limit(quality.FromParsed(&p)); ok && l.PreferredMBPerMin > 0 {
			mbPerMin = l.PreferredMBPerMin
		}
		c := decision.Candidate{
			Title: n, Parsed: p,
			Size:        int64(mbPerMin * float64(runtime) * factors[i%len(factors)] * quality.MB),
			PublishDate: now.Add(-time.Duration(i+1) * 7 * time.Hour),
			GUID:        fmt.Sprintf("guid-%02d", i),
			DownloadURL: fmt.Sprintf("https://indexer.example/get/%02d", i),
			Seeders:     -1,
		}
		switch i % 3 {
		case 0:
			c.IndexerID, c.IndexerName, c.IndexerPriority, c.Protocol = 1, "NZBGeek", 25, decision.Usenet
		case 1:
			c.IndexerID, c.IndexerName, c.IndexerPriority, c.Protocol = 2, "TorrentLeech", 25, decision.Torrent
			c.Seeders, c.Leechers = (i*13)%41, i%5
			c.InfoHash = fmt.Sprintf("%040x", i)
			if i%4 == 1 {
				c.IndexerFlags = []string{"freeleech"}
			}
		default:
			c.IndexerID, c.IndexerName, c.IndexerPriority, c.Protocol = 3, "DrunkenSlug", 10, decision.Usenet
		}
		c.AlreadyGrabbed = i == 5
		out[i] = c
	}
	return out
}

func goldenConfig(tb testing.TB, profile string) decision.Config {
	tb.Helper()
	cfg := decision.Config{
		Movie:     alienRomulus(),
		Profile:   starter(tb, profile),
		Rules:     quality.StarterRules(),
		Sizes:     quality.DefaultSizes(),
		Blocklist: []decision.BlocklistEntry{{Kind: decision.BlockGroup, ReleaseGroup: "FaiLED"}},
		Indexers:  map[int64]decision.IndexerSettings{2: {MinSeeders: 3}},
		Options:   decision.Options{Now: now, Interactive: true, UsenetRetentionDays: 4000},
	}
	return cfg
}

// TestGolden_interactiveSearch snapshots a full ranked interactive search. Run
// with -update to rewrite the golden files after an intended change.
func TestGolden_interactiveSearch(t *testing.T) {
	t.Parallel()
	names := searchNames(t)
	if len(names) < 30 {
		t.Fatalf("search has %d releases, want at least 30", len(names))
	}
	for _, profile := range []string{quality.ProfileHD1080p, quality.ProfileUltraHD} {
		t.Run(profile, func(t *testing.T) {
			t.Parallel()
			cfg := goldenConfig(t, profile)
			e := newEvaluator(t, cfg)
			ranked := e.EvaluateAll(searchCandidates(t, names, cfg.Movie.RuntimeMinutes))
			got := renderSearch(&cfg, ranked)
			path := filepath.Join(goldenDir, strings.ToLower(strings.ReplaceAll(profile, " ", "-"))+".golden")
			if *update {
				if err := os.MkdirAll(goldenDir, 0o755); err != nil {
					t.Fatal(err)
				}
				if err := os.WriteFile(path, []byte(got), 0o644); err != nil {
					t.Fatal(err)
				}
			}
			want, err := os.ReadFile(path)
			if err != nil {
				t.Fatalf("read golden (run with -update to create it): %v", err)
			}
			if got != string(want) {
				t.Errorf("%s differs from the golden file; run go test ./internal/decision -run Golden -update and review the diff.\n%s",
					path, firstDiff(string(want), got))
			}
		})
	}
}

func renderSearch(cfg *decision.Config, ranked []decision.Evaluated) string {
	var b strings.Builder
	m := &cfg.Movie
	fmt.Fprintf(&b, "# Interactive search: %s (%d), %d min, original language %s\n", m.Title, m.Year, m.RuntimeMinutes, m.OriginalLanguage)
	fmt.Fprintf(&b, "# Profile %s, cutoff %s, now %s, %d releases\n\n", cfg.Profile.Name, cfg.Profile.CutoffLabel(),
		cfg.Options.Now.Format(time.DateOnly), len(ranked))
	for i := range ranked {
		c, d := &ranked[i].Candidate, &ranked[i].Decision
		verdict := "REJECT"
		if d.Accepted {
			verdict = "ACCEPT"
		}
		fmt.Fprintf(&b, "%02d %s %s\n", i+1, verdict, c.Title)
		seeders := ""
		if c.Protocol == decision.Torrent {
			seeders = fmt.Sprintf(" %d seeders", c.Seeders)
		}
		fmt.Fprintf(&b, "   %s (rank %d) score %d | %s %s p%d%s | %.1f GB | %s\n",
			d.Quality.Name(), d.QualityRank, d.Score.Total, c.Protocol, c.IndexerName, c.IndexerPriority, seeders,
			float64(c.Size)/quality.GB, c.GUID)
		for _, r := range d.Score.Rules {
			fmt.Fprintf(&b, "   score %+d %s\n", r.Score, r.Rule)
		}
		for _, r := range d.Rejections {
			kind := "temporary"
			if r.Permanent {
				kind = "permanent"
			}
			fmt.Fprintf(&b, "   - %s (%s): %s\n", r.Code, kind, r.Message)
		}
		for _, n := range d.Notes {
			fmt.Fprintf(&b, "   note: %s\n", n)
		}
	}
	return b.String()
}

func firstDiff(want, got string) string {
	w, g := strings.Split(want, "\n"), strings.Split(got, "\n")
	for i := range max(len(w), len(g)) {
		var wl, gl string
		if i < len(w) {
			wl = w[i]
		}
		if i < len(g) {
			gl = g[i]
		}
		if wl != gl {
			return fmt.Sprintf("line %d:\nwant %q\n got %q", i+1, wl, gl)
		}
	}
	return ""
}
