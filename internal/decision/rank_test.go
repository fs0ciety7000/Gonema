package decision_test

import (
	"testing"
	"time"

	"github.com/google/go-cmp/cmp"
	"pgregory.net/rapid"

	"github.com/fs0ciety7000/gonema/internal/decision"
	"github.com/fs0ciety7000/gonema/internal/parser"
	"github.com/fs0ciety7000/gonema/internal/quality"
)

// ev is an accepted, otherwise neutral, evaluated release.
func ev(guid string) decision.Evaluated {
	return decision.Evaluated{
		Candidate: decision.Candidate{
			Title: "T", GUID: guid, Protocol: decision.Usenet, IndexerPriority: 25,
			Parsed: parser.ParsedRelease{Revision: rev(1, false)}, PublishDate: now,
		},
		Decision: decision.Decision{Accepted: true, QualityRank: 3, Quality: quality.Bluray1080p, SizeDelta: 100},
	}
}

func guids(evs []decision.Evaluated) []string {
	out := make([]string, len(evs))
	for i := range evs {
		out[i] = evs[i].Candidate.GUID
	}
	return out
}

// TestRank_keys checks each ranking key in isolation: "better" must rank before
// "worse" whatever the input order.
func TestRank_keys(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name   string
		prefer decision.Protocol
		better func(*decision.Evaluated)
		worse  func(*decision.Evaluated)
	}{
		{"accepted first", "", func(*decision.Evaluated) {}, func(e *decision.Evaluated) { e.Decision.Accepted = false; e.Decision.QualityRank = 9 }},
		{"quality rank", "", func(e *decision.Evaluated) { e.Decision.QualityRank = 4 }, func(*decision.Evaluated) {}},
		{"score", "", func(e *decision.Evaluated) { e.Decision.Score.Total = 10 }, func(e *decision.Evaluated) { e.Candidate.Parsed.Revision = rev(3, false) }},
		{"revision", "", func(e *decision.Evaluated) { e.Candidate.Parsed.Revision = rev(2, false) }, func(e *decision.Evaluated) { e.Candidate.IndexerPriority = 1 }},
		{"real revision", "", func(e *decision.Evaluated) { e.Candidate.Parsed.Revision = rev(1, true) }, func(*decision.Evaluated) {}},
		{"preferred protocol", decision.Torrent, func(e *decision.Evaluated) { e.Candidate.Protocol = decision.Torrent; e.Candidate.IndexerPriority = 50 }, func(*decision.Evaluated) {}},
		{"no protocol preference: usenet before torrent", "", func(*decision.Evaluated) {}, func(e *decision.Evaluated) { e.Candidate.Protocol = decision.Torrent; e.Candidate.Seeders = 1000 }},
		{"indexer priority", "", func(e *decision.Evaluated) { e.Candidate.IndexerPriority = 10 }, func(e *decision.Evaluated) { e.Decision.SizeDelta = 0 }},
		{"seeders", "", func(e *decision.Evaluated) { e.Candidate.Protocol = decision.Torrent; e.Candidate.Seeders = 50 }, func(e *decision.Evaluated) {
			e.Candidate.Protocol = decision.Torrent
			e.Candidate.Seeders = 5
			e.Decision.SizeDelta = 0
		}},
		{"unknown seeders count as zero", "", func(e *decision.Evaluated) { e.Candidate.Protocol = decision.Torrent; e.Candidate.Seeders = 1 }, func(e *decision.Evaluated) {
			e.Candidate.Protocol = decision.Torrent
			e.Candidate.Seeders = -1
		}},
		{"size closeness", "", func(e *decision.Evaluated) { e.Decision.SizeDelta = 10 }, func(e *decision.Evaluated) { e.Candidate.PublishDate = now.Add(time.Hour) }},
		{"unknown size last", "", func(*decision.Evaluated) {}, func(e *decision.Evaluated) { e.Decision.SizeDelta = -1 }},
		{"newer first", "", func(e *decision.Evaluated) { e.Candidate.PublishDate = now.Add(time.Hour) }, func(e *decision.Evaluated) { e.Candidate.GUID = "a" }},
		{"unknown date last", "", func(*decision.Evaluated) {}, func(e *decision.Evaluated) { e.Candidate.PublishDate = time.Time{}; e.Candidate.GUID = "a" }},
		{"guid tiebreak", "", func(e *decision.Evaluated) { e.Candidate.GUID = "a" }, func(e *decision.Evaluated) { e.Candidate.GUID = "b" }},
		{"title tiebreak", "", func(e *decision.Evaluated) { e.Candidate.Title = "A" }, func(e *decision.Evaluated) { e.Candidate.Title = "B" }},
		{"indexer tiebreak", "", func(e *decision.Evaluated) { e.Candidate.IndexerID = 1 }, func(e *decision.Evaluated) { e.Candidate.IndexerID = 2 }},
		{"url tiebreak", "", func(e *decision.Evaluated) { e.Candidate.DownloadURL = "a" }, func(e *decision.Evaluated) { e.Candidate.DownloadURL = "b" }},
		{"hash tiebreak", "", func(e *decision.Evaluated) { e.Candidate.InfoHash = "a" }, func(e *decision.Evaluated) { e.Candidate.InfoHash = "b" }},
		{"size tiebreak", "", func(e *decision.Evaluated) { e.Candidate.Size = 1 }, func(e *decision.Evaluated) { e.Candidate.Size = 2 }},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			b, w := ev("same"), ev("same")
			tt.better(&b)
			tt.worse(&w)
			for _, in := range [][]decision.Evaluated{{b, w}, {w, b}} {
				got := decision.Rank(in, tt.prefer)
				if diff := cmp.Diff([]decision.Evaluated{b, w}, got); diff != "" {
					t.Errorf("Rank order (-want +got):\n%s", diff)
				}
			}
		})
	}
}

func TestRank_doesNotModifyInput(t *testing.T) {
	t.Parallel()
	in := []decision.Evaluated{ev("b"), ev("a")}
	_ = decision.Rank(in, "")
	if in[0].Candidate.GUID != "b" {
		t.Error("Rank sorted its input in place")
	}
	if got := decision.Rank(nil, ""); len(got) != 0 {
		t.Errorf("Rank(nil) = %v", got)
	}
}

// genEvaluated draws releases whose compared fields come from small sets (to force
// ties on every key) and whose other fields are constant.
func genEvaluated(t *rapid.T, i int) decision.Evaluated {
	l := func(s string) string { return s + "[" + string(rune('a'+i%26)) + "]" }
	e := ev(rapid.SampledFrom([]string{"g1", "g2", "g3"}).Draw(t, l("guid")))
	e.Decision.Accepted = rapid.Bool().Draw(t, l("accepted"))
	e.Decision.QualityRank = rapid.IntRange(0, 3).Draw(t, l("rank"))
	e.Decision.Score.Total = rapid.IntRange(-1, 1).Draw(t, l("score"))
	e.Decision.SizeDelta = rapid.Int64Range(-1, 2).Draw(t, l("sizeDelta"))
	e.Candidate.Parsed.Revision = rev(rapid.IntRange(1, 2).Draw(t, l("version")), rapid.Bool().Draw(t, l("real")))
	e.Candidate.Protocol = rapid.SampledFrom([]decision.Protocol{decision.Usenet, decision.Torrent, ""}).Draw(t, l("protocol"))
	e.Candidate.IndexerPriority = rapid.IntRange(1, 2).Draw(t, l("priority"))
	e.Candidate.Seeders = rapid.IntRange(-1, 2).Draw(t, l("seeders"))
	e.Candidate.PublishDate = rapid.SampledFrom([]time.Time{{}, now, now.Add(time.Hour)}).Draw(t, l("date"))
	e.Candidate.Title = rapid.SampledFrom([]string{"A", "B"}).Draw(t, l("title"))
	e.Candidate.IndexerID = rapid.Int64Range(1, 2).Draw(t, l("indexer"))
	e.Candidate.DownloadURL = rapid.SampledFrom([]string{"u1", "u2"}).Draw(t, l("url"))
	e.Candidate.InfoHash = rapid.SampledFrom([]string{"", "h"}).Draw(t, l("hash"))
	e.Candidate.Size = rapid.Int64Range(1, 2).Draw(t, l("size"))
	return e
}

// TestRank_totalOrder: Rank is deterministic, idempotent and permutation-invariant,
// which holds only if the comparison is a total order.
func TestRank_totalOrder(t *testing.T) {
	t.Parallel()
	rapid.Check(t, func(t *rapid.T) {
		n := rapid.IntRange(0, 12).Draw(t, "n")
		evs := make([]decision.Evaluated, n)
		for i := range evs {
			evs[i] = genEvaluated(t, i)
		}
		prefer := rapid.SampledFrom([]decision.Protocol{"", decision.Usenet, decision.Torrent}).Draw(t, "prefer")
		want := decision.Rank(evs, prefer)
		perm := rapid.Permutation(evs).Draw(t, "perm")
		if diff := cmp.Diff(want, decision.Rank(perm, prefer)); diff != "" {
			t.Fatalf("Rank depends on input order (-sorted +permuted):\n%s", diff)
		}
		if diff := cmp.Diff(want, decision.Rank(want, prefer)); diff != "" {
			t.Fatalf("Rank is not idempotent:\n%s", diff)
		}
		for i := 1; i < len(want); i++ {
			if !want[i-1].Decision.Accepted && want[i].Decision.Accepted {
				t.Fatalf("rejected release at %d ranks before an accepted one", i-1)
			}
		}
	})
}

func TestNextBest(t *testing.T) {
	t.Parallel()
	rejected := ev("r")
	rejected.Decision.Accepted = false
	first := ev("first")
	first.Candidate.InfoHash = "HASH1"
	second := ev("second")
	second.Candidate.Parsed.ReleaseGroup = "GRP"
	third := ev("third")
	third.Candidate.Title = "Third.Title"
	ranked := []decision.Evaluated{rejected, first, second, third}
	tests := []struct {
		name   string
		failed []decision.BlocklistEntry
		want   string
	}{
		{"none failed", nil, "first"},
		{"by hash", []decision.BlocklistEntry{{Kind: decision.BlockRelease, InfoHash: "hash1"}}, "second"},
		{"by guid and group", []decision.BlocklistEntry{
			{Kind: decision.BlockRelease, GUID: "first"},
			{Kind: decision.BlockGroup, ReleaseGroup: "grp"},
		}, "third"},
		{"all failed", []decision.BlocklistEntry{
			{Kind: decision.BlockRelease, GUID: "first"},
			{Kind: decision.BlockRelease, GUID: "second"},
			{Kind: decision.BlockRelease, SourceTitle: "third.title"},
		}, ""},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			got, ok := decision.NextBest(ranked, tt.failed)
			if ok != (tt.want != "") || got.Candidate.GUID != tt.want {
				t.Errorf("NextBest = %q, %v; want %q", got.Candidate.GUID, ok, tt.want)
			}
		})
	}
}

func TestEvaluator_EvaluateAll(t *testing.T) {
	t.Parallel()
	cfg := baseConfig(t)
	cfg.Options.PreferredProtocol = decision.Torrent
	e := newEvaluator(t, cfg)
	cands := []decision.Candidate{
		usenet("Alien.Romulus.2024.720p.WEB-DL.DDP5.1.H.264-NTG", 3*gb),
		usenet(good, 9*gb),
		torrent(good, 9*gb, 20),
		usenet("Alien.Romulus.2024.1080p.WEB-DL.DDP5.1.H.264-NTG", 6*gb),
	}
	got := guids(e.EvaluateAll(cands))
	want := []string{"tor-" + good, "nzb-" + good, "nzb-Alien.Romulus.2024.1080p.WEB-DL.DDP5.1.H.264-NTG", "nzb-Alien.Romulus.2024.720p.WEB-DL.DDP5.1.H.264-NTG"}
	if diff := cmp.Diff(want, got); diff != "" {
		t.Errorf("EvaluateAll order (-want +got):\n%s", diff)
	}
}
