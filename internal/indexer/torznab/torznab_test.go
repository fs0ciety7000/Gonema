package torznab

import (
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/google/go-cmp/cmp"

	"github.com/fs0ciety7000/gonema/internal/decision"
	"github.com/fs0ciety7000/gonema/internal/indexer"
)

func fixture(t *testing.T, name string) []byte {
	t.Helper()
	b, err := os.ReadFile(filepath.Join("testdata", name))
	if err != nil {
		t.Fatal(err)
	}
	return b
}

func TestSearchTorznab(t *testing.T) {
	t.Parallel()
	var got http.Header
	var query string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Query().Get("t") == "caps" {
			_, _ = w.Write(fixture(t, "caps.xml"))
			return
		}
		got, query = r.Header.Clone(), r.URL.RawQuery
		_, _ = w.Write(fixture(t, "movie_search.xml"))
	}))
	t.Cleanup(srv.Close)
	c, err := New(Options{Info: indexer.Info{ID: 3, Name: "Tracker", Priority: 5, Protocol: decision.Usenet}, BaseURL: srv.URL, APIKey: "k"})
	if err != nil {
		t.Fatal(err)
	}
	if c.Info().Protocol != decision.Torrent {
		t.Fatalf("protocol = %q", c.Info().Protocol)
	}
	rels, err := c.Search(t.Context(), indexer.Query{IMDbID: "tt0137523", TMDbID: 550})
	if err != nil {
		t.Fatal(err)
	}
	if got.Get("User-Agent") == "" || query == "" {
		t.Fatal("no search request")
	}
	if len(rels) != 2 {
		t.Fatalf("got %d releases, want 2 (the item without link is skipped)", len(rels))
	}
	want := indexer.Release{
		Title:                "Fight.Club.1999.REPACK.1080p.BluRay.x264-INTGRP",
		GUID:                 "https://tracker.example/torrents/1001",
		Size:                 12884901888,
		InfoURL:              "https://tracker.example/torrents/1001",
		DownloadURL:          "https://tracker.example/download/1001.torrent?passkey=PASSKEY",
		PublishDate:          time.Date(2021, 6, 1, 18, 15, 0, 0, time.UTC),
		IndexerID:            3,
		IndexerName:          "Tracker",
		IndexerPriority:      5,
		Protocol:             decision.Torrent,
		Seeders:              57,
		Leechers:             4, // peers - seeders
		Grabs:                -1,
		InfoHash:             "0123456789abcdef0123456789abcdef01234567",
		DownloadVolumeFactor: 0,
		UploadVolumeFactor:   2,
		MinimumRatio:         1,
		MinimumSeedTime:      48 * time.Hour,
		Flags:                []string{indexer.FlagFreeleech, indexer.FlagDoubleUpload, indexer.FlagInternal},
		Categories:           []int{2040, 100001},
		IMDbID:               "tt0137523",
		TMDbID:               550,
	}
	if d := cmp.Diff(want, rels[0]); d != "" {
		t.Errorf("release 0 (-want +got):\n%s", d)
	}
	m := rels[1]
	if m.DownloadURL != m.MagnetURL || m.InfoHash != "0102030405060708090a0b0c0d0e0f1011121314" ||
		m.Seeders != 3 || m.Leechers != 1 || m.UploadVolumeFactor != 1 || m.DownloadVolumeFactor != 0.5 ||
		!m.HasFlag(indexer.FlagHalfleech) || m.Size != 4294967296 {
		t.Errorf("magnet release = %+v", m)
	}

	cand := rels[0].Candidate()
	if cand.Protocol != decision.Torrent || cand.Seeders != 57 || cand.InfoHash != want.InfoHash ||
		cand.Parsed.ReleaseGroup != "INTGRP" || len(cand.IndexerFlags) != 3 {
		t.Errorf("candidate = %+v", cand)
	}
}

func TestCapsTorznab(t *testing.T) {
	t.Parallel()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write(fixture(t, "caps.xml"))
	}))
	t.Cleanup(srv.Close)
	c, err := New(Options{BaseURL: srv.URL})
	if err != nil {
		t.Fatal(err)
	}
	caps, err := c.Caps(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	if !caps.MovieSearch.Supports("tmdbid") || caps.RetentionDays != 0 || len(caps.Tags) != 2 {
		t.Errorf("caps = %+v", caps)
	}
	// Site-specific categories (100000+) are not guessed as movie categories.
	if d := cmp.Diff([]int{2000, 2040, 2045}, caps.MovieCategories()); d != "" {
		t.Errorf("movie categories (-want +got):\n%s", d)
	}
}
