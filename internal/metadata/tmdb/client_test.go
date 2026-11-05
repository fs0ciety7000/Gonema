package tmdb

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/google/go-cmp/cmp"

	"github.com/fs0ciety7000/gonema/internal/metadata"
)

func TestParseCredential(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name string
		in   string
		want credKind
	}{
		{name: "api key", in: testAPIKey, want: credAPIKey},
		{name: "api key upper and spaces", in: "  0123456789ABCDEF0123456789ABCDEF\n", want: credAPIKey},
		{name: "read access token", in: testToken, want: credBearer},
		{name: "token with bearer prefix", in: "Bearer " + testToken, want: credBearer},
		{name: "empty", in: ""},
		{name: "short hex", in: "0123456789abcdef"},
		{name: "33 hex", in: testAPIKey + "0"},
		{name: "non hex 32", in: "0123456789abcdef0123456789abcdeg"},
		{name: "jwt without eyJ", in: "abc.def.ghi" + strings.Repeat("x", 40)},
		{name: "jwt with spaces", in: "eyJhbGciOiJIUzI1NiJ9.eyJ hdWQiOiJ0ZXN0Iiwib.c2lnbmF0dXJl"},
		{name: "password", in: "hunter2-my-secret-password"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			got, err := parseCredential(tt.in)
			if tt.want == 0 {
				if !errors.Is(err, metadata.ErrUnauthorized) || !errors.Is(err, ErrKeyFormat) {
					t.Fatalf("parseCredential error = %v, want ErrKeyFormat matching ErrUnauthorized", err)
				}
				if s := strings.TrimSpace(tt.in); s != "" && strings.Contains(err.Error(), s) {
					t.Fatalf("error echoes the credential: %v", err)
				}
				return
			}
			if err != nil {
				t.Fatalf("parseCredential: %v", err)
			}
			if got.kind != tt.want {
				t.Fatalf("kind = %d, want %d", got.kind, tt.want)
			}
		})
	}
}

func TestCredentialTransport(t *testing.T) {
	t.Parallel()
	t.Run("bearer", func(t *testing.T) {
		t.Parallel()
		fs := newFixtureServer(t)
		c, _ := newTestClient(t, fs, testToken, Options{Version: "1.2.3"})
		if _, err := c.Popular(t.Context(), 1); err != nil {
			t.Fatal(err)
		}
		r := fs.last()
		if got := r.Header.Get("Authorization"); got != "Bearer "+testToken {
			t.Errorf("Authorization = %q", got)
		}
		if r.URL.Query().Has("api_key") {
			t.Error("api_key sent with a bearer token")
		}
		if got, want := r.Header.Get("User-Agent"), "Gonema/1.2.3 (+https://github.com/fs0ciety7000/gonema)"; got != want {
			t.Errorf("User-Agent = %q, want %q", got, want)
		}
		if got := r.Header.Get("Accept"); got != "application/json" {
			t.Errorf("Accept = %q", got)
		}
	})
	t.Run("api key", func(t *testing.T) {
		t.Parallel()
		fs := newFixtureServer(t)
		c, _ := newTestClient(t, fs, testAPIKey, Options{})
		if _, err := c.Popular(t.Context(), 1); err != nil {
			t.Fatal(err)
		}
		r := fs.last()
		if got := r.URL.Query().Get("api_key"); got != testAPIKey {
			t.Errorf("api_key = %q", got)
		}
		if r.Header.Get("Authorization") != "" {
			t.Error("Authorization sent with an API key")
		}
		if got := r.Header.Get("User-Agent"); got != "Gonema/dev (+https://github.com/fs0ciety7000/gonema)" {
			t.Errorf("User-Agent = %q", got)
		}
	})
}

func TestNewRejectsBadBaseURL(t *testing.T) {
	t.Parallel()
	for _, base := range []string{"ftp://x", "not a url", "http://"} {
		if _, err := New(testAPIKey, Options{BaseURL: base}); err == nil {
			t.Errorf("New(BaseURL %q) succeeded", base)
		}
	}
}

func TestSearch(t *testing.T) {
	t.Parallel()
	fs := newFixtureServer(t)
	c, _ := newTestClient(t, fs, testToken, Options{Language: "fr-FR"})
	page, err := c.Search(t.Context(), "  Fight Club ", 1999, 2)
	if err != nil {
		t.Fatal(err)
	}
	q := fs.last().URL.Query()
	for k, want := range map[string]string{"query": "Fight Club", "year": "1999", "page": "2", "language": "fr-FR", "include_adult": "false"} {
		if got := q.Get(k); got != want {
			t.Errorf("query %s = %q, want %q", k, got, want)
		}
	}
	if page.Page != 1 || page.TotalResults == 0 || len(page.Results) != 5 {
		t.Fatalf("page = %d total %d len %d", page.Page, page.TotalResults, len(page.Results))
	}
	want := metadata.MovieSummary{
		TMDBID: 550, Title: "Fight Club", OriginalTitle: "Fight Club", OriginalLanguage: "en", Year: 1999,
		ReleaseDate: metadata.Date{Year: 1999, Month: 10, Day: 15},
		PosterPath:  "/pB8BM7pdSp6B6Ih7QZ4DrQ3PmJK.jpg", BackdropPath: "/hZkgoQYus5vegHoetLkCJzb17zJ.jpg",
		Popularity: 73.433, Rating: &metadata.Rating{Value: 8.433, Votes: 26279}, GenreIDs: []int{18, 53, 35},
	}
	got := page.Results[0]
	got.Overview = ""
	if d := cmp.Diff(want, got); d != "" {
		t.Errorf("first result (-want +got):\n%s", d)
	}

	t.Run("no year", func(t *testing.T) {
		if _, err := c.Search(t.Context(), "Heat", 0, 0); err != nil {
			t.Fatal(err)
		}
		q := fs.last().URL.Query()
		if q.Has("year") || q.Get("page") != "1" {
			t.Errorf("query = %v, want no year and page 1", q)
		}
	})
	t.Run("invalid", func(t *testing.T) {
		n := fs.count()
		for _, query := range []string{"", "   ", strings.Repeat("a", 201)} {
			if _, err := c.Search(t.Context(), query, 0, 1); !errors.Is(err, ErrInvalidArgument) {
				t.Errorf("Search(%q) error = %v, want ErrInvalidArgument", query, err)
			}
		}
		if _, err := c.Search(t.Context(), "x", 0, 501); !errors.Is(err, ErrInvalidArgument) {
			t.Errorf("page 501 error = %v", err)
		}
		if fs.count() != n {
			t.Error("invalid arguments reached the server")
		}
	})
}

func TestMovieDetails(t *testing.T) {
	t.Parallel()
	fs := newFixtureServer(t)
	c, _ := newTestClient(t, fs, testToken, Options{})
	c.now = func() time.Time { return time.Date(2026, 11, 5, 12, 0, 0, 0, time.UTC) }
	m, err := c.Movie(t.Context(), 550)
	if err != nil {
		t.Fatal(err)
	}
	q := fs.last().URL.Query()
	if got := q.Get("append_to_response"); got != "credits,videos,alternative_titles,release_dates,external_ids,keywords" {
		t.Errorf("append_to_response = %q", got)
	}
	if q.Get("language") != "en-US" {
		t.Errorf("language = %q", q.Get("language"))
	}

	d := func(y, mo, da int) metadata.Date { return metadata.Date{Year: y, Month: time.Month(mo), Day: da} }
	checks := []struct {
		name      string
		got, want any
	}{
		{"TMDBID", m.TMDBID, 550},
		{"IMDbID", m.IMDbID, "tt0137523"},
		{"Title", m.Title, "Fight Club"},
		{"OriginalLanguage", m.OriginalLanguage, "en"},
		{"Year", m.Year, 1999},
		{"RuntimeMin", m.RuntimeMin, 139},
		{"Genres", m.Genres, []string{"Drama", "Thriller", "Comedy"}},
		{"ProviderStatus", m.ProviderStatus, "Released"},
		{"Status", m.Status, metadata.StatusReleased},
		{"Cinema", m.Releases.Cinema, metadata.DatedRelease{Date: d(1999, 10, 15), Region: "US", Type: metadata.ReleaseTheatrical}},
		// No US digital/physical date in the fixture: earliest of any region.
		{"Digital", m.Releases.Digital, metadata.DatedRelease{Date: d(1999, 10, 15), Region: "IN", Type: metadata.ReleaseDigital}},
		{"Physical", m.Releases.Physical, metadata.DatedRelease{Date: d(2000, 11, 15), Region: "FR", Type: metadata.ReleasePhysical}},
		{"Certification", m.Releases.Certification, "R"},
		{"CertificationRegion", m.Releases.CertificationRegion, "US"},
		{"Ratings.TMDB", *m.Ratings.TMDB, metadata.Rating{Value: 8.433, Votes: 26279}},
		{"Ratings.IMDb", m.Ratings.IMDb == nil, true},
		{"Collection", m.Collection == nil, true},
		{"AlternateTitles", len(m.AlternateTitles), 12},
		{"AlternateTitles[0]", m.AlternateTitles[0], metadata.AlternateTitle{Title: "Borilački klub", Country: "RS"}},
		{"Cast", len(m.Extra.Cast), 6},
		{"Cast[1]", m.Extra.Cast[1], metadata.Credit{
			Person:    metadata.Person{TMDBID: 287, Name: "Brad Pitt", ProfilePath: "/huV2cdcolEUwJy37QvH914vup7d.jpg", KnownForDepartment: "Acting"},
			Character: "Tyler Durden", Order: 1,
		}},
		{"Crew[0].Job", m.Extra.Crew[0].Job, "Executive Producer"},
		{"Keywords", len(m.Extra.Keywords), 15},
		{"Keywords[0]", m.Extra.Keywords[0], "based on novel or book"},
		{"Tagline", m.Extra.Tagline, "Mischief. Mayhem. Soap."},
	}
	for _, c := range checks {
		if diff := cmp.Diff(c.want, c.got); diff != "" {
			t.Errorf("%s (-want +got):\n%s", c.name, diff)
		}
	}
	if !strings.Contains(m.StatusReason, "digital release on 1999-10-15 (IN, digital)") {
		t.Errorf("StatusReason = %q", m.StatusReason)
	}
	var keys []string
	for _, v := range m.Extra.Videos {
		keys = append(keys, string(v.Type)+":"+v.Key)
	}
	// Official 1080p trailer, then the unofficial one, then the teaser; the
	// featurette and the Vimeo trailer are dropped.
	if diff := cmp.Diff([]string{"trailer:BdJKm16Co6M", "trailer:O-b2VfmmbyA", "teaser:TEASER00001"}, keys); diff != "" {
		t.Errorf("videos (-want +got):\n%s", diff)
	}
	if tr, ok := m.Trailer(); !ok || tr.URL() != "https://www.youtube.com/watch?v=BdJKm16Co6M" {
		t.Errorf("Trailer() = %v, %v", tr, ok)
	}
	if len(m.ReleaseDates) == 0 || m.ReleaseDates[0].Region > m.ReleaseDates[len(m.ReleaseDates)-1].Region {
		t.Error("ReleaseDates not sorted by region")
	}
}

func TestMovieRegion(t *testing.T) {
	t.Parallel()
	fs := newFixtureServer(t)
	c, _ := newTestClient(t, fs, testToken, Options{Region: "fr"})
	m, err := c.Movie(t.Context(), 550)
	if err != nil {
		t.Fatal(err)
	}
	if got := m.Releases.Digital; got.Region != "FR" || got.Date.String() != "2011-09-08" {
		t.Errorf("Digital = %+v, want FR 2011-09-08", got)
	}
	if got := m.Releases.Physical; got.Region != "FR" || got.Date.String() != "2000-11-15" {
		t.Errorf("Physical = %+v, want FR 2000-11-15", got)
	}
	if m.Releases.CertificationRegion != "FR" || m.Releases.Certification == "" {
		t.Errorf("certification = %q (%s), want a FR one", m.Releases.Certification, m.Releases.CertificationRegion)
	}
	if _, err := c.Movie(t.Context(), 0); !errors.Is(err, ErrInvalidArgument) {
		t.Errorf("Movie(0) error = %v", err)
	}
}

func TestFindByIMDb(t *testing.T) {
	t.Parallel()
	fs := newFixtureServer(t)
	c, _ := newTestClient(t, fs, testToken, Options{})
	got, err := c.FindByIMDb(t.Context(), "tt0137523")
	if err != nil {
		t.Fatal(err)
	}
	if got.TMDBID != 934433 || got.Title != "Scream VI" || got.Year != 2023 {
		t.Errorf("FindByIMDb = %+v", got)
	}
	if q := fs.last().URL.Query(); q.Get("external_source") != "imdb_id" {
		t.Errorf("external_source = %q", q.Get("external_source"))
	}
	if _, err := c.FindByIMDb(t.Context(), "tt9999999"); !errors.Is(err, metadata.ErrNotFound) {
		t.Errorf("unknown id error = %v, want ErrNotFound", err)
	}
	n := fs.count()
	for _, id := range []string{"", "tt12", "nm0000093", "tt0137523/../x", "tt01375230000"} {
		if _, err := c.FindByIMDb(t.Context(), id); !errors.Is(err, ErrInvalidArgument) {
			t.Errorf("FindByIMDb(%q) error = %v, want ErrInvalidArgument", id, err)
		}
	}
	if fs.count() != n {
		t.Error("invalid ids reached the server")
	}
}

func TestCollection(t *testing.T) {
	t.Parallel()
	fs := newFixtureServer(t)
	c, _ := newTestClient(t, fs, testToken, Options{})
	col, err := c.Collection(t.Context(), 10)
	if err != nil {
		t.Fatal(err)
	}
	if col.TMDBID != 10 || col.Name != "Star Wars Collection" || col.PosterPath == "" {
		t.Errorf("collection = %+v", col)
	}
	var titles []string
	for _, p := range col.Parts {
		titles = append(titles, fmt.Sprintf("%d %s", p.Year, p.Title))
	}
	want := []string{
		"1977 Star Wars", "1980 The Empire Strikes Back", "1983 Return of the Jedi",
		"1999 Star Wars: Episode I - The Phantom Menace", "2002 Star Wars: Episode II - Attack of the Clones",
		"2005 Star Wars: Episode III - Revenge of the Sith", "2015 Star Wars: The Force Awakens",
		"2017 Star Wars: The Last Jedi", "2019 Star Wars: The Rise of Skywalker",
	}
	if d := cmp.Diff(want, titles); d != "" {
		t.Errorf("parts (-want +got):\n%s", d)
	}
}

func TestPersonFilmography(t *testing.T) {
	t.Parallel()
	fs := newFixtureServer(t)
	c, _ := newTestClient(t, fs, testToken, Options{})
	f, err := c.PersonFilmography(t.Context(), 31)
	if err != nil {
		t.Fatal(err)
	}
	if f.PersonID != 31 || len(f.Cast) != 8 || len(f.Crew) != 6 {
		t.Fatalf("filmography: id %d cast %d crew %d", f.PersonID, len(f.Cast), len(f.Crew))
	}
	if f.Cast[0].Movie.Year != 2006 || f.Cast[len(f.Cast)-1].Movie.Year != 1993 {
		t.Errorf("cast not newest first: %d .. %d", f.Cast[0].Movie.Year, f.Cast[len(f.Cast)-1].Movie.Year)
	}
	for _, cr := range f.Cast {
		if cr.Movie.Title == "Forrest Gump" && cr.Character == "" {
			t.Error("Forrest Gump has no character")
		}
	}
	if f.Crew[0].Job == "" {
		t.Error("crew without job")
	}
}

func TestLists(t *testing.T) {
	t.Parallel()
	fs := newFixtureServer(t)
	c, _ := newTestClient(t, fs, testToken, Options{Region: "gb"})
	ctx := t.Context()
	tests := []struct {
		name   string
		call   func() (metadata.Page[metadata.MovieSummary], error)
		path   string
		region string
	}{
		{"popular", func() (metadata.Page[metadata.MovieSummary], error) { return c.Popular(ctx, 3) }, "/movie/popular", ""},
		{"trending day", func() (metadata.Page[metadata.MovieSummary], error) { return c.Trending(ctx, metadata.TrendingDay, 3) }, "/trending/movie/day", ""},
		{"trending week", func() (metadata.Page[metadata.MovieSummary], error) { return c.Trending(ctx, metadata.TrendingWeek, 3) }, "/trending/movie/week", ""},
		{"upcoming", func() (metadata.Page[metadata.MovieSummary], error) { return c.Upcoming(ctx, 3) }, "/movie/upcoming", "GB"},
		{"now playing", func() (metadata.Page[metadata.MovieSummary], error) { return c.NowPlaying(ctx, 3) }, "/movie/now_playing", "GB"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			p, err := tt.call()
			if err != nil {
				t.Fatal(err)
			}
			r := fs.last()
			if r.URL.Path != tt.path {
				t.Errorf("path = %s, want %s", r.URL.Path, tt.path)
			}
			q := r.URL.Query()
			if q.Get("page") != "3" || q.Get("region") != tt.region {
				t.Errorf("query = %v", q)
			}
			if len(p.Results) != 4 || p.TotalPages == 0 || p.Results[0].TMDBID == 0 || p.Results[0].Title == "" {
				t.Errorf("page = %+v", p)
			}
		})
	}
	if _, err := c.Trending(ctx, "month", 1); !errors.Is(err, ErrInvalidArgument) {
		t.Errorf("Trending(month) error = %v", err)
	}
}

func TestConfigurationCached(t *testing.T) {
	t.Parallel()
	fs := newFixtureServer(t)
	c, _ := newTestClient(t, fs, testToken, Options{CacheTTL: -1}) // general cache off; configuration keeps its own TTL
	for range 3 {
		base, err := c.ImageBaseURL(t.Context())
		if err != nil {
			t.Fatal(err)
		}
		if base != "https://image.tmdb.org/t/p/" {
			t.Fatalf("base = %q", base)
		}
	}
	cfg, err := c.Configuration(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	if !contains(cfg.PosterSizes, "w342") || !contains(cfg.PosterSizes, "w780") || !contains(cfg.BackdropSizes, "w1280") || !contains(cfg.ProfileSizes, "w185") {
		t.Errorf("sizes = %+v", cfg)
	}
	if n := fs.count(); n != 1 {
		t.Errorf("%d requests, want 1", n)
	}
}

func contains(s []string, v string) bool {
	for _, x := range s {
		if x == v {
			return true
		}
	}
	return false
}

func TestValidateKey(t *testing.T) {
	t.Parallel()
	fs := newFixtureServer(t)
	c, _ := newTestClient(t, fs, testToken, Options{})
	for range 2 {
		if err := c.ValidateKey(t.Context()); err != nil {
			t.Fatalf("ValidateKey: %v", err)
		}
	}
	if fs.count() != 2 {
		t.Errorf("ValidateKey was cached: %d requests", fs.count())
	}
	fs.setHandler(func(w http.ResponseWriter, _ *http.Request, _ int) bool {
		writeFixture(t, w, http.StatusUnauthorized, "error_401.json")
		return true
	})
	err := c.ValidateKey(t.Context())
	if !errors.Is(err, metadata.ErrUnauthorized) {
		t.Fatalf("ValidateKey error = %v, want ErrUnauthorized", err)
	}
	for _, want := range []string{"themoviedb.org/settings/api", "Invalid API key"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("error %q lacks %q", err, want)
		}
	}
}

func TestStatusErrors(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name     string
		status   int
		file     string
		want     error
		requests int
	}{
		{"401 not retried", http.StatusUnauthorized, "error_401.json", metadata.ErrUnauthorized, 1},
		{"404 not retried", http.StatusNotFound, "error_404.json", metadata.ErrNotFound, 1},
		{"400 not retried", http.StatusBadRequest, "error_404.json", nil, 1},
		{"500 retried to the limit", http.StatusInternalServerError, "error_404.json", nil, 3},
		{"503 retried to the limit", http.StatusServiceUnavailable, "error_404.json", nil, 3},
		{"429 without Retry-After", http.StatusTooManyRequests, "error_429.json", metadata.ErrRateLimited, 3},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			fs := newFixtureServer(t)
			fs.setHandler(func(w http.ResponseWriter, _ *http.Request, _ int) bool {
				writeFixture(t, w, tt.status, tt.file)
				return true
			})
			c, sr := newTestClient(t, fs, testToken, Options{MaxAttempts: 3})
			_, err := c.Movie(t.Context(), 550)
			if err == nil {
				t.Fatal("no error")
			}
			if tt.want != nil && !errors.Is(err, tt.want) {
				t.Errorf("error = %v, want %v", err, tt.want)
			}
			var ae *metadata.APIError
			if tt.status != http.StatusTooManyRequests && (!errors.As(err, &ae) || ae.HTTPStatus != tt.status) {
				t.Errorf("error = %v, want APIError with status %d", err, tt.status)
			}
			if got := fs.count(); got != tt.requests {
				t.Errorf("%d requests, want %d", got, tt.requests)
			}
			// Backoff: [250ms, 500ms) then [500ms, 1s) with the default base.
			for i, d := range sr.get() {
				lo := (500 * time.Millisecond << i) / 2
				if d < lo || d > 2*lo {
					t.Errorf("backoff %d = %s, want in [%s, %s]", i, d, lo, 2*lo)
				}
			}
			if !strings.HasPrefix(err.Error(), "tmdb: GET /movie/550: ") {
				t.Errorf("error = %q, want the request path as context", err)
			}
		})
	}
}

func TestRetryAfter(t *testing.T) {
	t.Parallel()
	t.Run("honoured then success", func(t *testing.T) {
		t.Parallel()
		fs := newFixtureServer(t)
		fs.setHandler(func(w http.ResponseWriter, _ *http.Request, n int) bool {
			if n == 1 {
				w.Header().Set("Retry-After", "2")
				writeFixture(t, w, http.StatusTooManyRequests, "error_429.json")
				return true
			}
			return false
		})
		c, sr := newTestClient(t, fs, testToken, Options{})
		if _, err := c.Movie(t.Context(), 550); err != nil {
			t.Fatal(err)
		}
		if d := cmp.Diff([]time.Duration{2 * time.Second}, sr.get()); d != "" {
			t.Errorf("sleeps (-want +got):\n%s", d)
		}
		if fs.count() != 2 {
			t.Errorf("%d requests, want 2", fs.count())
		}
	})
	t.Run("too long", func(t *testing.T) {
		t.Parallel()
		fs := newFixtureServer(t)
		fs.setHandler(func(w http.ResponseWriter, _ *http.Request, _ int) bool {
			w.Header().Set("Retry-After", "120")
			writeFixture(t, w, http.StatusTooManyRequests, "error_429.json")
			return true
		})
		c, sr := newTestClient(t, fs, testToken, Options{})
		_, err := c.Movie(t.Context(), 550)
		var rl *metadata.RateLimitError
		if !errors.As(err, &rl) || rl.RetryAfter != 2*time.Minute || !errors.Is(err, metadata.ErrRateLimited) {
			t.Fatalf("error = %v, want RateLimitError{2m}", err)
		}
		if fs.count() != 1 || len(sr.get()) != 0 {
			t.Errorf("%d requests, %d sleeps; want 1 and 0", fs.count(), len(sr.get()))
		}
	})
}

func TestParseRetryAfter(t *testing.T) {
	t.Parallel()
	now := time.Date(2026, 11, 5, 12, 0, 0, 0, time.UTC)
	tests := []struct {
		in   string
		want time.Duration
	}{
		{"", 0}, {"3", 3 * time.Second}, {" 10 ", 10 * time.Second}, {"-1", 0}, {"abc", 0},
		{"Thu, 05 Nov 2026 12:00:30 GMT", 30 * time.Second},
		{"Thu, 05 Nov 2026 11:00:00 GMT", 0},
		{"999999999", 24 * time.Hour},
	}
	for _, tt := range tests {
		if got := parseRetryAfter(tt.in, now); got != tt.want {
			t.Errorf("parseRetryAfter(%q) = %s, want %s", tt.in, got, tt.want)
		}
	}
}

func TestTimeoutRetried(t *testing.T) {
	t.Parallel()
	fs := newFixtureServer(t)
	release := make(chan struct{})
	t.Cleanup(func() { close(release) })
	fs.setHandler(func(_ http.ResponseWriter, r *http.Request, n int) bool {
		if n == 1 {
			select { // hang until the client gives up on this attempt
			case <-r.Context().Done():
			case <-release:
			}
			return true
		}
		return false
	})
	c, sr := newTestClient(t, fs, testToken, Options{Timeout: 100 * time.Millisecond})
	if _, err := c.Movie(t.Context(), 550); err != nil {
		t.Fatalf("Movie after a timeout: %v", err)
	}
	if fs.count() != 2 || len(sr.get()) != 1 {
		t.Errorf("%d requests, %d sleeps; want 2 and 1", fs.count(), len(sr.get()))
	}
}

func TestCallerCancelNotRetried(t *testing.T) {
	t.Parallel()
	fs := newFixtureServer(t)
	ctx, cancel := context.WithCancel(t.Context())
	fs.setHandler(func(_ http.ResponseWriter, r *http.Request, _ int) bool {
		cancel()
		<-r.Context().Done()
		return true
	})
	c, sr := newTestClient(t, fs, testToken, Options{})
	_, err := c.Movie(ctx, 550)
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("error = %v, want context.Canceled", err)
	}
	if len(sr.get()) != 0 {
		t.Errorf("retried after the caller canceled: %v", sr.get())
	}
}

func TestCache(t *testing.T) {
	t.Parallel()
	fs := newFixtureServer(t)
	c, _ := newTestClient(t, fs, testAPIKey, Options{CacheTTL: time.Minute})
	now := time.Date(2026, 11, 5, 12, 0, 0, 0, time.UTC)
	var mu sync.Mutex
	c.now = func() time.Time { mu.Lock(); defer mu.Unlock(); return now }

	for range 3 {
		if _, err := c.Movie(t.Context(), 550); err != nil {
			t.Fatal(err)
		}
	}
	if fs.count() != 1 {
		t.Fatalf("%d requests for 3 identical calls, want 1", fs.count())
	}
	if _, err := c.Search(t.Context(), "fight club", 0, 1); err != nil {
		t.Fatal(err)
	}
	if fs.count() != 2 {
		t.Fatalf("a different request was served from cache")
	}
	for key := range c.cache.m {
		if strings.Contains(key, testAPIKey) || strings.Contains(key, "api_key") {
			t.Errorf("cache key %q contains the credential", key)
		}
	}
	mu.Lock()
	now = now.Add(61 * time.Second)
	mu.Unlock()
	if _, err := c.Movie(t.Context(), 550); err != nil {
		t.Fatal(err)
	}
	if fs.count() != 3 {
		t.Errorf("expired entry not refetched: %d requests", fs.count())
	}
	// Errors are not cached.
	if _, err := c.Movie(t.Context(), 404); !errors.Is(err, metadata.ErrNotFound) {
		t.Fatal(err)
	}
	if _, err := c.Movie(t.Context(), 404); !errors.Is(err, metadata.ErrNotFound) {
		t.Fatal(err)
	}
	if fs.count() != 5 {
		t.Errorf("errors were cached: %d requests, want 5", fs.count())
	}
}

func TestRespCacheBounds(t *testing.T) {
	t.Parallel()
	now := time.Unix(0, 0)
	c := newRespCache(3, 10)
	c.put("a", []byte("1234"), time.Minute, now)
	c.put("b", []byte("1234"), time.Minute, now)
	c.put("c", []byte("1234"), time.Minute, now) // 12 bytes > 10: evicts a
	if _, ok := c.get("a", now); ok {
		t.Error("a not evicted by the byte bound")
	}
	c.get("b", now) // b becomes most recent
	c.put("d", []byte("1"), time.Minute, now)
	c.put("e", []byte("1"), time.Minute, now) // 4 entries > 3: evicts c (least recent)
	if _, ok := c.get("c", now); ok {
		t.Error("c not evicted by the entry bound")
	}
	if _, ok := c.get("b", now); !ok {
		t.Error("recently used b evicted")
	}
	c.put("big", make([]byte, 11), time.Minute, now)
	if _, ok := c.get("big", now); ok {
		t.Error("value larger than the cache was stored")
	}
	if _, ok := c.get("d", now.Add(time.Minute)); ok {
		t.Error("expired entry served")
	}
	if c.len() != 2 || c.bytes != 5 {
		t.Errorf("len %d bytes %d, want 2 and 5", c.len(), c.bytes)
	}
}

// TestSingleflight checks that concurrent identical requests share one HTTP call.
func TestSingleflight(t *testing.T) {
	t.Parallel()
	const callers = 20
	fs := newFixtureServer(t)
	release := make(chan struct{})
	fs.setHandler(func(_ http.ResponseWriter, _ *http.Request, _ int) bool {
		<-release
		return false
	})
	c, _ := newTestClient(t, fs, testToken, Options{})
	var wg sync.WaitGroup
	errs := make(chan error, callers)
	for range callers {
		wg.Go(func() {
			m, err := c.Movie(t.Context(), 550)
			if err == nil && m.TMDBID != 550 {
				err = fmt.Errorf("got movie %d", m.TMDBID)
			}
			errs <- err
		})
	}
	waitFor(t, func() bool { return flightWaiters(c) == callers })
	close(release)
	wg.Wait()
	close(errs)
	for err := range errs {
		if err != nil {
			t.Error(err)
		}
	}
	if fs.count() != 1 {
		t.Errorf("%d requests for %d concurrent callers, want 1", fs.count(), callers)
	}
}

// TestSingleflightLeaderCancel checks that the first caller giving up does not
// fail the others.
func TestSingleflightLeaderCancel(t *testing.T) {
	t.Parallel()
	fs := newFixtureServer(t)
	release := make(chan struct{})
	fs.setHandler(func(_ http.ResponseWriter, _ *http.Request, _ int) bool {
		<-release
		return false
	})
	c, _ := newTestClient(t, fs, testToken, Options{})
	leaderCtx, cancelLeader := context.WithCancel(t.Context())
	leaderErr := make(chan error, 1)
	go func() {
		_, err := c.Movie(leaderCtx, 550)
		leaderErr <- err
	}()
	waitFor(t, func() bool { return flightWaiters(c) == 1 })
	followerErr := make(chan error, 1)
	go func() {
		_, err := c.Movie(t.Context(), 550)
		followerErr <- err
	}()
	waitFor(t, func() bool { return flightWaiters(c) == 2 })
	cancelLeader()
	if err := <-leaderErr; !errors.Is(err, context.Canceled) {
		t.Errorf("leader error = %v, want context.Canceled", err)
	}
	close(release)
	if err := <-followerErr; err != nil {
		t.Errorf("follower failed after the leader left: %v", err)
	}
}

func flightWaiters(c *Client) int { return c.flight.TotalWaiters() }

// waitFor polls cond with a generous deadline (no fixed sleeps in assertions).
func waitFor(t *testing.T, cond func() bool) {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for !cond() {
		if time.Now().After(deadline) {
			t.Fatal("condition not reached within 5s")
		}
		time.Sleep(time.Millisecond)
	}
}

func TestRateLimitShared(t *testing.T) {
	t.Parallel()
	fs := newFixtureServer(t)
	c, _ := newTestClient(t, fs, testToken, Options{RateLimit: 50, Burst: 1, CacheTTL: -1})
	start := time.Now()
	var wg sync.WaitGroup
	for i := range 11 {
		wg.Go(func() {
			if _, err := c.Popular(t.Context(), i+1); err != nil {
				t.Error(err)
			}
		})
	}
	wg.Wait()
	// 1 immediate + 10 at 20 ms intervals: at least 200 ms (margin for timer slack).
	if el := time.Since(start); el < 180*time.Millisecond {
		t.Errorf("11 requests at 50/s took %s, want >= 200ms", el)
	}
}

func TestRedaction(t *testing.T) {
	t.Parallel()
	t.Run("transport error and logs", func(t *testing.T) {
		t.Parallel()
		fs := newFixtureServer(t)
		var logs bytes.Buffer
		c, _ := newTestClient(t, fs, testAPIKey, Options{
			MaxAttempts: 2,
			Logger:      slog.New(slog.NewTextHandler(&logs, &slog.HandlerOptions{Level: slog.LevelDebug})),
		})
		fs.srv.Close() // connection refused: the url.Error carries the full URL
		_, err := c.Movie(t.Context(), 550)
		if err == nil {
			t.Fatal("no error from a closed server")
		}
		for name, s := range map[string]string{"error": err.Error(), "logs": logs.String()} {
			if strings.Contains(s, testAPIKey) {
				t.Errorf("%s leaks the API key: %s", name, s)
			}
		}
		if !strings.Contains(err.Error(), "api_key=REDACTED") {
			t.Errorf("error = %q, want the redacted URL", err)
		}
		if !strings.Contains(logs.String(), "tmdb request retry") {
			t.Errorf("retry not logged: %s", logs.String())
		}
	})
	t.Run("echo in provider message", func(t *testing.T) {
		t.Parallel()
		fs := newFixtureServer(t)
		fs.setHandler(func(w http.ResponseWriter, _ *http.Request, _ int) bool {
			w.WriteHeader(http.StatusUnauthorized)
			fmt.Fprintf(w, `{"status_code":7,"status_message":"bad key %s"}`, testToken)
			return true
		})
		c, _ := newTestClient(t, fs, testToken, Options{})
		err := c.ValidateKey(t.Context())
		if err == nil || strings.Contains(err.Error(), testToken) || !strings.Contains(err.Error(), "bad key REDACTED") {
			t.Errorf("error = %v, want the token redacted", err)
		}
	})
	t.Run("param", func(t *testing.T) {
		t.Parallel()
		tests := map[string]string{
			"https://x/3/movie/1?api_key=abc&language=en": "https://x/3/movie/1?api_key=REDACTED&language=en",
			"a api_key=abc":                      "a api_key=REDACTED",
			`Get "http://h/?api_key=k": refused`: `Get "http://h/?api_key=REDACTED": refused`,
			"api_key=REDACTED":                   "api_key=REDACTED",
			"none":                               "none",
		}
		for in, want := range tests {
			if got := redactAPIKeyParam(in); got != want {
				t.Errorf("redactAPIKeyParam(%q) = %q, want %q", in, got, want)
			}
		}
	})
}
