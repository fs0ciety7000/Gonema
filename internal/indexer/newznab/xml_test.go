package newznab

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/google/go-cmp/cmp"

	"github.com/fs0ciety7000/gonema/internal/decision"
	"github.com/fs0ciety7000/gonema/internal/indexer"
)

var usenetInfo = indexer.Info{ID: 7, Name: "Example", Priority: 10, Protocol: decision.Usenet}

func fixture(t testing.TB, name string) []byte {
	t.Helper()
	b, err := os.ReadFile(filepath.Join("testdata", name))
	if err != nil {
		t.Fatal(err)
	}
	return b
}

func TestParseFeedMovieSearch(t *testing.T) {
	t.Parallel()
	p, err := ParseFeed(fixture(t, "movie_search.xml"), usenetInfo)
	if err != nil {
		t.Fatal(err)
	}
	if p.Count != 3 || p.Skipped != 1 || p.Total != 3 || p.Offset != 0 || len(p.Releases) != 2 {
		t.Fatalf("page = count %d skipped %d total %d offset %d releases %d", p.Count, p.Skipped, p.Total, p.Offset, len(p.Releases))
	}
	want := indexer.Release{
		Title:                "Fight.Club.1999.1080p.BluRay.x264-GROUP",
		GUID:                 "https://indexer.example/details/e9c515e02346086e3a477a5436d7bc8c",
		Size:                 10737418240,
		InfoURL:              "https://indexer.example/details/e9c515e02346086e3a477a5436d7bc8c#comments",
		DownloadURL:          "https://indexer.example/getnzb/e9c515e02346086e3a477a5436d7bc8c.nzb&i=1&r=REDACTED",
		PublishDate:          time.Date(2021, 6, 5, 10, 0, 0, 0, time.UTC), // usenetdate wins for Usenet
		IndexerID:            7,
		IndexerName:          "Example",
		IndexerPriority:      10,
		Protocol:             decision.Usenet,
		Seeders:              -1,
		Leechers:             -1,
		Grabs:                42,
		DownloadVolumeFactor: 1,
		UploadVolumeFactor:   1,
		Categories:           []int{2000, 2040},
		IMDbID:               "tt0137523",
		TMDbID:               550,
	}
	if d := cmp.Diff(want, p.Releases[0]); d != "" {
		t.Errorf("release 0 (-want +got):\n%s", d)
	}
	r := p.Releases[1]
	if r.GUID != "a1b2c3d4e5f60718293a4b5c6d7e8f90" || r.InfoURL != "" || r.Size != 64424509440 ||
		!r.PublishDate.Equal(time.Date(2021, 6, 7, 8, 0, 0, 0, time.UTC)) || r.IMDbID != "tt0137523" {
		t.Errorf("release 1 = %+v", r)
	}
}

func TestParseFeedEmptySpecExample(t *testing.T) {
	t.Parallel()
	p, err := ParseFeed(fixture(t, "empty.xml"), usenetInfo)
	if err != nil {
		t.Fatal(err)
	}
	if len(p.Releases) != 0 || p.Total != 0 || p.Count != 0 {
		t.Fatalf("page = %+v", p)
	}
}

func TestParseCaps(t *testing.T) {
	t.Parallel()
	c, err := ParseCaps(fixture(t, "caps.xml"))
	if err != nil {
		t.Fatal(err)
	}
	if c.LimitMax != 100 || c.LimitDefault != 50 || c.RetentionDays != 4000 || c.ServerTitle != "Example Usenet Indexer" || c.Version != "1.1" {
		t.Errorf("caps = %+v", c)
	}
	if !c.MovieSearch.Supports("imdbid") || !c.MovieSearch.Supports("q") || c.MovieSearch.Supports("tmdbid") {
		t.Errorf("movie search = %+v", c.MovieSearch)
	}
	if d := cmp.Diff([]int{2000, 2010, 2030, 2040, 2045, 2050}, c.MovieCategories()); d != "" {
		t.Errorf("movie categories (-want +got):\n%s", d)
	}
}

func TestParseCapsLegacyDefaults(t *testing.T) {
	t.Parallel()
	c, err := ParseCaps(fixture(t, "caps_legacy.xml"))
	if err != nil {
		t.Fatal(err)
	}
	if !c.Search.Supports("q") || c.MovieSearch.Available {
		t.Errorf("search = %+v, movie = %+v", c.Search, c.MovieSearch)
	}
	// An element without supportedParams gets the revision 1.1 defaults.
	c2, err := ParseCaps([]byte(`<caps><searching><movie-search available="yes"/></searching></caps>`))
	if err != nil {
		t.Fatal(err)
	}
	if !c2.MovieSearch.Supports("imdbid") || !c2.MovieSearch.Supports("q") || !c2.Search.Supports("q") {
		t.Errorf("defaults: %+v", c2)
	}
}

func TestParseErrors(t *testing.T) {
	t.Parallel()
	tests := []struct {
		file string
		kind error
		code int
	}{
		{"error_100.xml", indexer.ErrUnauthorized, 100},
		{"error_101.xml", indexer.ErrSuspended, 101},
		{"error_203.xml", indexer.ErrUnsupported, 203},
		{"error_429.xml", indexer.ErrRateLimited, 429},
		{"error_500_limit.xml", indexer.ErrRateLimited, 500},
		{"error_910.xml", indexer.ErrDisabled, 910},
		{"html.html", indexer.ErrNotIndexer, 0},
	}
	for _, tt := range tests {
		t.Run(tt.file, func(t *testing.T) {
			t.Parallel()
			body := fixture(t, tt.file)
			for name, parse := range map[string]func([]byte) error{
				"feed": func(b []byte) error { _, err := ParseFeed(b, usenetInfo); return err },
				"caps": func(b []byte) error { _, err := ParseCaps(b); return err },
			} {
				err := parse(body)
				if !errors.Is(err, tt.kind) {
					t.Fatalf("%s: err = %v, want %v", name, err, tt.kind)
				}
				var ie *indexer.Error
				if !errors.As(err, &ie) || ie.Code != tt.code {
					t.Errorf("%s: err = %#v, want code %d", name, err, tt.code)
				}
			}
		})
	}
}

func TestParseGarbage(t *testing.T) {
	t.Parallel()
	for _, body := range []string{
		"", "hello", `{"error":"json"}`, "<html><body>502 Bad Gateway</body></html>",
		`<?xml version="1.0"?><foo/>`, "<rss><channel><item><title>x</title></item>",
	} {
		_, err := ParseFeed([]byte(body), usenetInfo)
		if !errors.Is(err, indexer.ErrNotIndexer) && !errors.Is(err, indexer.ErrBadResponse) {
			t.Errorf("ParseFeed(%q) = %v, want ErrNotIndexer or ErrBadResponse", body, err)
		}
	}
	_, err := ParseFeed(fixture(t, "html.html"), usenetInfo)
	if !errors.Is(err, indexer.ErrNotIndexer) || !strings.Contains(err.Error(), "HTML page") {
		t.Errorf("html: %v", err)
	}
}

func TestParseLatin1(t *testing.T) {
	t.Parallel()
	body := append([]byte(`<?xml version="1.0" encoding="ISO-8859-1"?><rss><channel><item><title>Am`), 0xe9)
	body = append(body, []byte(`lie.2001.1080p.BluRay.x264-GRP</title><link>https://i.example/a.nzb</link></item></channel></rss>`)...)
	p, err := ParseFeed(body, usenetInfo)
	if err != nil {
		t.Fatal(err)
	}
	if got := p.Releases[0].Title; got != "Amélie.2001.1080p.BluRay.x264-GRP" {
		t.Errorf("title = %q", got)
	}
}

func TestParseDate(t *testing.T) {
	t.Parallel()
	want := time.Date(2010, 6, 22, 5, 54, 22, 0, time.UTC)
	for _, s := range []string{
		"Tue, 22 Jun 2010 06:54:22 +0100",
		"Tue, 22 Jun 2010 05:54:22 GMT",
		"Tue, 22 Jun 2010 05:54:22 UTC",
		"22 Jun 2010 06:54:22 +0100",
		"2010-06-22T06:54:22+01:00",
		"2010-06-22 05:54:22",
	} {
		if got := parseDate(s); !got.Equal(want) {
			t.Errorf("parseDate(%q) = %v", s, got)
		}
	}
	for _, s := range []string{"", "yesterday", strings.Repeat("1", 100)} {
		if got := parseDate(s); !got.IsZero() {
			t.Errorf("parseDate(%q) = %v, want zero", s, got)
		}
	}
}

func TestNormalizeHash(t *testing.T) {
	t.Parallel()
	tests := map[string]string{
		"0123456789ABCDEF0123456789ABCDEF01234567": "0123456789abcdef0123456789abcdef01234567",
		"AEBAGBAFAYDQQCIKBMGA2DQPCAIREEYU":         "0102030405060708090a0b0c0d0e0f1011121314",
		"xyz":                                      "",
		"0123456789abcdef0123456789abcdef0123456g": "",
	}
	for in, want := range tests {
		if got := normalizeHash(in); got != want {
			t.Errorf("normalizeHash(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestSearchText(t *testing.T) {
	t.Parallel()
	tests := []struct {
		title string
		year  int
		want  string
	}{
		{"Fight Club", 1999, "Fight Club 1999"},
		{"Ocean's Eleven", 2001, "Oceans Eleven 2001"},
		{"Mission: Impossible – Dead Reckoning Part One", 2023, "Mission Impossible Dead Reckoning Part One 2023"},
		{"Amélie", 0, "Amélie"},
		{"  ", 2020, ""},
		{"WALL·E", 2008, "WALL E 2008"},
	}
	for _, tt := range tests {
		if got := searchText(tt.title, tt.year); got != tt.want {
			t.Errorf("searchText(%q, %d) = %q, want %q", tt.title, tt.year, got, tt.want)
		}
	}
}

// feedXML builds a feed of n generated items starting at offset, announcing total.
func feedXML(offset, n, total int) []byte {
	var b strings.Builder
	b.WriteString(`<?xml version="1.0" encoding="UTF-8"?>` + "\n")
	b.WriteString(`<rss version="2.0" xmlns:newznab="http://www.newznab.com/DTD/2010/feeds/attributes/"><channel><title>gen</title>`)
	fmt.Fprintf(&b, `<newznab:response offset="%d" total="%d"/>`, offset, total)
	for i := offset; i < offset+n; i++ {
		fmt.Fprintf(&b, `<item><title>Some.Movie.%d.2020.1080p.BluRay.x264-GRP</title>`+
			`<guid isPermaLink="false">guid-%d</guid><link>https://i.example/getnzb/%d.nzb</link>`+
			`<comments>https://i.example/details/%d#comments</comments>`+
			`<pubDate>Thu, 05 Nov 2026 09:%02d:00 +0000</pubDate>`+
			`<enclosure url="https://i.example/getnzb/%d.nzb" length="%d" type="application/x-nzb"/>`+
			`<newznab:attr name="category" value="2000"/><newznab:attr name="category" value="2040"/>`+
			`<newznab:attr name="size" value="%d"/><newznab:attr name="grabs" value="%d"/>`+
			`<newznab:attr name="usenetdate" value="Thu, 05 Nov 2026 08:00:00 +0000"/>`+
			`<newznab:attr name="imdb" value="0137523"/></item>`,
			i, i, i, i, i%60, i, 1<<30+i, 1<<30+i, i)
	}
	b.WriteString(`</channel></rss>`)
	return []byte(b.String())
}

func BenchmarkParseFeed100(b *testing.B) {
	body := feedXML(0, 100, 100)
	b.SetBytes(int64(len(body)))
	b.ReportAllocs()
	for b.Loop() {
		p, err := ParseFeed(body, usenetInfo)
		if err != nil || len(p.Releases) != 100 {
			b.Fatal(err, len(p.Releases))
		}
	}
}

func FuzzParseFeed(f *testing.F) {
	for _, name := range []string{"movie_search.xml", "rss.xml", "empty.xml", "error_100.xml", "html.html", "caps.xml"} {
		f.Add(fixture(f, name))
	}
	f.Add(fixture(f, filepath.Join("..", "..", "torznab", "testdata", "movie_search.xml")))
	f.Add(feedXML(0, 3, 3))
	f.Fuzz(func(t *testing.T, body []byte) {
		p, err := ParseFeed(body, usenetInfo)
		if err != nil {
			var ie *indexer.Error
			if !errors.As(err, &ie) {
				t.Fatalf("untyped error %T: %v", err, err)
			}
			return
		}
		for i := range p.Releases {
			r := &p.Releases[i]
			if r.Title == "" || r.DownloadURL == "" || r.GUID == "" || len(r.Title) > maxTextLen {
				t.Fatalf("invalid release %+v", r)
			}
		}
		_, _ = ParseCaps(body)
	})
}
