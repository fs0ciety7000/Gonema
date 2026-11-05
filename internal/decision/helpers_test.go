package decision_test

import (
	"testing"
	"time"

	"github.com/fs0ciety7000/gonema/internal/decision"
	"github.com/fs0ciety7000/gonema/internal/parser"
	"github.com/fs0ciety7000/gonema/internal/quality"
)

// now is the fixed clock of every test.
var now = time.Date(2026, 11, 5, 12, 0, 0, 0, time.UTC)

const gb = quality.GB

func alienRomulus() decision.Movie {
	return decision.Movie{
		ID:                  1,
		Title:               "Alien: Romulus",
		Year:                2024,
		AlternateTitles:     []string{"Alien Romulus", "Alien : Romulus"},
		RuntimeMinutes:      119,
		OriginalLanguage:    parser.LanguageEnglish,
		Status:              decision.Released,
		MinimumAvailability: decision.Released,
		InCinemas:           time.Date(2024, 8, 16, 0, 0, 0, 0, time.UTC),
		DigitalRelease:      time.Date(2024, 10, 15, 0, 0, 0, 0, time.UTC),
		Monitored:           true,
	}
}

// fataler is what helpers need from *testing.T, *testing.B and *rapid.T.
type fataler interface {
	Helper()
	Fatalf(format string, args ...any)
}

func starter(t fataler, name string) quality.Profile {
	t.Helper()
	p, ok := quality.StarterProfile(name)
	if !ok {
		t.Fatalf("no starter profile %q", name)
	}
	return p
}

// baseConfig is an automatic search for Alien: Romulus with the HD-1080p profile.
func baseConfig(t testing.TB) decision.Config {
	t.Helper()
	return decision.Config{
		Movie:   alienRomulus(),
		Profile: starter(t, quality.ProfileHD1080p),
		Rules:   quality.StarterRules(),
		Sizes:   quality.DefaultSizes(),
		Options: decision.Options{Now: now},
	}
}

func newEvaluator(t testing.TB, cfg decision.Config) *decision.Evaluator {
	t.Helper()
	e, err := decision.NewEvaluator(cfg)
	if err != nil {
		t.Fatalf("NewEvaluator: %v", err)
	}
	return e
}

// usenet builds a Usenet candidate of size bytes, posted a day ago.
func usenet(name string, size int64) decision.Candidate {
	return decision.Candidate{
		Title: name, Parsed: parser.Parse(name), Size: size,
		IndexerID: 1, IndexerName: "NZBGeek", IndexerPriority: 25, Protocol: decision.Usenet,
		Seeders: -1, PublishDate: now.Add(-24 * time.Hour),
		DownloadURL: "https://nzb.example/get/" + name, GUID: "nzb-" + name,
	}
}

// torrent builds a torrent candidate.
func torrent(name string, size int64, seeders int) decision.Candidate {
	return decision.Candidate{
		Title: name, Parsed: parser.Parse(name), Size: size,
		IndexerID: 2, IndexerName: "TorrentLeech", IndexerPriority: 25, Protocol: decision.Torrent,
		Seeders: seeders, Leechers: 3, PublishDate: now.Add(-48 * time.Hour),
		DownloadURL: "magnet:?dn=" + name, GUID: "tor-" + name, InfoHash: "ABCDEF0123456789ABCDEF0123456789ABCDEF01",
	}
}

func codes(d *decision.Decision) []decision.Code {
	out := make([]decision.Code, len(d.Rejections))
	for i, r := range d.Rejections {
		out[i] = r.Code
	}
	return out
}
