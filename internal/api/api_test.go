package api

import (
	"bufio"
	"compress/gzip"
	"context"
	"encoding/json"
	"io"
	"log/slog"
	"maps"
	"net/http"
	"net/http/httptest"
	"slices"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/fs0ciety7000/gonema/internal/config"
	"github.com/fs0ciety7000/gonema/internal/events"
	"github.com/fs0ciety7000/gonema/internal/jobs"
	"github.com/fs0ciety7000/gonema/internal/library"
	"github.com/fs0ciety7000/gonema/internal/metadata"
	"github.com/fs0ciety7000/gonema/internal/secretbox"
	"github.com/fs0ciety7000/gonema/internal/store"
	"github.com/fs0ciety7000/gonema/internal/store/storetest"
)

const testKey = "0123456789abcdef0123456789abcdef"

// provider is a minimal metadata provider.
type provider struct {
	metadata.Provider // unused methods panic
	movies            map[int]*metadata.Movie
	popular           []metadata.MovieSummary
	collections       map[int]*metadata.Collection
	people            map[int]*metadata.Filmography
	persons           map[int]*metadata.PersonDetails
	personsErr        error
}

func (p *provider) Person(_ context.Context, id int) (*metadata.PersonDetails, error) {
	if p.personsErr != nil {
		return nil, p.personsErr
	}
	if d, ok := p.persons[id]; ok {
		return d, nil
	}
	return nil, metadata.ErrNotFound
}

func (p *provider) SearchPeople(_ context.Context, query string, page int) (metadata.Page[metadata.PersonSummary], error) {
	if p.personsErr != nil {
		return metadata.Page[metadata.PersonSummary]{}, p.personsErr
	}
	var out []metadata.PersonSummary
	for _, id := range slices.Sorted(maps.Keys(p.persons)) {
		d := p.persons[id]
		if strings.Contains(strings.ToLower(d.Name), strings.ToLower(query)) {
			var known []metadata.MovieSummary
			for _, m := range p.movies {
				known = append(known, metadata.MovieSummary{TMDBID: m.TMDBID, Title: m.Title, Year: m.Year})
			}
			slices.SortFunc(known, func(a, b metadata.MovieSummary) int { return a.TMDBID - b.TMDBID })
			out = append(out, metadata.PersonSummary{TMDBID: d.TMDBID, Name: d.Name, ProfilePath: d.ProfilePath,
				KnownForDepartment: d.KnownForDepartment, KnownFor: known})
		}
	}
	return metadata.Page[metadata.PersonSummary]{Page: max(page, 1), TotalPages: 1, TotalResults: len(out), Results: out}, nil
}

func (p *provider) Popular(context.Context, int) (metadata.Page[metadata.MovieSummary], error) {
	return metadata.Page[metadata.MovieSummary]{Page: 1, TotalPages: 1, TotalResults: len(p.popular), Results: p.popular}, nil
}

func (p *provider) Collection(_ context.Context, id int) (*metadata.Collection, error) {
	if c, ok := p.collections[id]; ok {
		return c, nil
	}
	return nil, metadata.ErrNotFound
}

func (p *provider) PersonFilmography(_ context.Context, id int) (*metadata.Filmography, error) {
	if f, ok := p.people[id]; ok {
		return f, nil
	}
	return nil, metadata.ErrNotFound
}

func (p *provider) Movie(_ context.Context, id int) (*metadata.Movie, error) {
	m, ok := p.movies[id]
	if !ok {
		return nil, metadata.ErrNotFound
	}
	c := *m
	return &c, nil
}

func (p *provider) Search(context.Context, string, int, int) (metadata.Page[metadata.MovieSummary], error) {
	var out []metadata.MovieSummary
	for _, m := range p.movies {
		out = append(out, metadata.MovieSummary{TMDBID: m.TMDBID, Title: m.Title, Year: m.Year})
	}
	return metadata.Page[metadata.MovieSummary]{Page: 1, TotalPages: 1, TotalResults: len(out), Results: out}, nil
}

func (p *provider) ValidateKey(context.Context) error { return nil }

type rejecting struct{ *provider }

func (rejecting) ValidateKey(context.Context) error { return metadata.ErrUnauthorized }

type testEnv struct {
	srv    *Server
	ts     *httptest.Server
	lib    *library.Service
	bus    *events.Bus
	st     *store.Store
	rootID int64
	profID int64
	prov   *provider
	apiKey string // sent by do as X-Api-Key; see withTestAuth
}

func newTestEnv(t *testing.T, baseURL string) *testEnv {
	t.Helper()
	return newTestEnvWith(t, baseURL, nil, true)
}

// newTestEnvWith is newTestEnv with security options; setup creates the
// account (and its API key, sent by do).
func newTestEnvWith(t *testing.T, baseURL string, sec *config.Security, setup bool) *testEnv {
	t.Helper()
	return newTestEnvDeps(t, baseURL, sec, setup, nil)
}

// newTestEnvDeps is newTestEnvWith with a last change of the dependencies.
func newTestEnvDeps(t *testing.T, baseURL string, sec *config.Security, setup bool, mod func(*Deps)) *testEnv {
	t.Helper()
	st := storetest.New(t)
	key, _ := secretbox.NewKey()
	box, _ := secretbox.New(key)
	bus := events.New(events.Options{})
	log := slog.New(slog.DiscardHandler)
	runner := jobs.New(st, jobs.Options{Workers: 1, Bus: bus, Logger: log})
	d := metadata.Date{Year: 1999, Month: 10, Day: 15}
	prov := &provider{movies: map[int]*metadata.Movie{550: {
		TMDBID: 550, Title: "Fight Club", Year: 1999, ReleaseDate: d, IMDbID: "tt0137523",
		Releases: metadata.ReleaseSelection{Cinema: metadata.DatedRelease{Date: d, Region: "US"}},
	}}}
	lib, err := library.New(library.Options{
		Store: st, Box: box, Jobs: runner, Bus: bus, Logger: log,
		NewProvider: func(k string) (library.KeyedProvider, error) {
			if k == "rejected-key-000000000000000000" {
				return rejecting{prov}, nil
			}
			return prov, nil
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	if err := lib.Init(ctx); err != nil {
		t.Fatal(err)
	}
	lib.RegisterJobs(runner)
	if err := runner.Start(ctx); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		sctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		_ = runner.Stop(sctx)
	})
	lib2 := storetest.SeedLibrary(t, st)
	deps := Deps{
		Library: lib, Jobs: runner, Bus: bus, Store: st, Logger: log, Version: "test",
		StartedAt: time.Now(), DataDir: t.TempDir(), BaseURL: baseURL, SSEHeartbeat: 50 * time.Millisecond,
	}
	if sec != nil {
		deps.Security = *sec
	}
	if mod != nil {
		mod(&deps)
	}
	var apiKey string
	if setup {
		apiKey = withTestAuth(t, &deps)
	} else {
		newTestAuth(t, &deps)
	}
	srv := New(deps)
	ts := httptest.NewServer(srv.Handler())
	t.Cleanup(func() {
		srv.Shutdown()
		ts.Close()
	})
	return &testEnv{srv: srv, ts: ts, lib: lib, bus: bus, st: st, rootID: lib2.RootFolderID, profID: lib2.ProfileID, prov: prov, apiKey: apiKey}
}

type response struct {
	status int
	header http.Header
	body   string
}

func (e *testEnv) do(t *testing.T, method, path, body string, header ...string) response {
	t.Helper()
	var r io.Reader
	if body != "" {
		r = strings.NewReader(body)
	}
	req, err := http.NewRequestWithContext(context.Background(), method, e.ts.URL+path, r)
	if err != nil {
		t.Fatal(err)
	}
	if body != "" {
		req.Header.Set("Content-Type", "application/json")
	}
	if e.apiKey != "" {
		req.Header.Set(APIKeyHeader, e.apiKey)
	}
	for i := 0; i+1 < len(header); i += 2 {
		req.Header.Set(header[i], header[i+1])
	}
	resp, err := e.ts.Client().Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	b, err := io.ReadAll(resp.Body)
	if err != nil {
		t.Fatal(err)
	}
	return response{status: resp.StatusCode, header: resp.Header, body: string(b)}
}

func decode[T any](t *testing.T, r response) T {
	t.Helper()
	var v T
	if err := json.Unmarshal([]byte(r.body), &v); err != nil {
		t.Fatalf("decode %q: %v", r.body, err)
	}
	return v
}

// problemOK checks a problem+json response and returns its detail.
func problemOK(t *testing.T, r response, status int) string {
	t.Helper()
	if r.status != status {
		t.Fatalf("status = %d, want %d: %s", r.status, status, r.body)
	}
	if ct := r.header.Get("Content-Type"); ct != "application/problem+json" {
		t.Errorf("Content-Type = %q, want application/problem+json", ct)
	}
	p := decode[struct {
		Status int    `json:"status"`
		Title  string `json:"title"`
		Detail string `json:"detail"`
	}](t, r)
	if p.Status != status || p.Title == "" || p.Detail == "" {
		t.Errorf("problem = %+v", p)
	}
	return p.Detail
}

func TestHealthAndStatus(t *testing.T) {
	e := newTestEnv(t, "")
	if r := e.do(t, "GET", "/health", ""); r.status != 200 || r.body != "{\"status\":\"ok\"}\n" {
		t.Errorf("health = %d %q", r.status, r.body)
	}
	r := e.do(t, "GET", "/api/v1/system/status", "", RequestIDHeader, "abc-123")
	st := decode[SystemStatus](t, r)
	if r.status != 200 || st.Version != "test" || st.SchemaVersion < 1 || st.TMDBConfigured {
		t.Errorf("status = %d %+v", r.status, st)
	}
	if got := r.header.Get(RequestIDHeader); got != "abc-123" {
		t.Errorf("request id = %q", got)
	}
	if r := e.do(t, "GET", "/api/v1/health", "", RequestIDHeader, "bad id!"); r.header.Get(RequestIDHeader) == "bad id!" {
		t.Error("invalid request id echoed")
	}
	problemOK(t, e.do(t, "GET", "/api/v1/does-not-exist", ""), 404)
	if r := e.do(t, "GET", "/api/v1/openapi.json", ""); r.status != 200 || !strings.Contains(r.body, `"/movies/{id}"`) {
		t.Errorf("openapi = %d", r.status)
	}
}

func TestTMDBKeyAndMovies(t *testing.T) {
	e := newTestEnv(t, "")
	add := `{"tmdbId":550,"rootFolderId":` + itoa(e.rootID) + `,"qualityProfileId":` + itoa(e.profID) + `,"tags":["cult"]}`

	// Without a key: plain-language 503.
	if d := problemOK(t, e.do(t, "POST", "/api/v1/movies", add), 503); !strings.Contains(d, "Settings > Metadata") {
		t.Errorf("detail = %q", d)
	}
	if r := e.do(t, "GET", "/api/v1/settings/tmdb", ""); r.body != "{\"configured\":false}\n" {
		t.Errorf("tmdb setting = %s", r.body)
	}
	if d := problemOK(t, e.do(t, "PUT", "/api/v1/settings/tmdb", `{"key":"rejected-key-000000000000000000"}`), 422); !strings.Contains(d, "TMDB rejected this key") {
		t.Errorf("detail = %q", d)
	}
	r := e.do(t, "PUT", "/api/v1/settings/tmdb", `{"key":"`+testKey+`"}`)
	if r.status != 200 || strings.Contains(r.body, testKey) {
		t.Fatalf("set key = %d %s", r.status, r.body)
	}
	if r := e.do(t, "GET", "/api/v1/settings/tmdb", ""); r.body != "{\"configured\":true}\n" {
		t.Errorf("tmdb setting = %s", r.body)
	}

	// Validation errors.
	problemOK(t, e.do(t, "POST", "/api/v1/movies", `{"tmdbId":0,"rootFolderId":1,"qualityProfileId":1}`), 422)
	if d := problemOK(t, e.do(t, "POST", "/api/v1/movies", `{"tmdbId":550,"rootFolderId":999,"qualityProfileId":1}`), 422); d != "root folder 999 does not exist" {
		t.Errorf("detail = %q", d)
	}
	problemOK(t, e.do(t, "POST", "/api/v1/movies", `{"tmdbId":12345,"rootFolderId":`+itoa(e.rootID)+`,"qualityProfileId":`+itoa(e.profID)+`}`), 404)

	// Add: 201 then idempotent 200.
	r = e.do(t, "POST", "/api/v1/movies", add)
	m := decode[Movie](t, r)
	if r.status != 201 || m.ID == 0 || m.Path != "Fight Club (1999)" || !m.Monitored || m.State != "missing" ||
		len(m.Tags) != 1 || r.header.Get("Location") != "/api/v1/movies/"+itoa(m.ID) {
		t.Fatalf("add = %d %s (Location %q)", r.status, r.body, r.header.Get("Location"))
	}
	r = e.do(t, "POST", "/api/v1/movies", add)
	if again := decode[Movie](t, r); r.status != 200 || again.ID != m.ID {
		t.Errorf("second add = %d %s", r.status, r.body)
	}

	// List, get, lookup.
	page := decode[MoviePage](t, e.do(t, "GET", "/api/v1/movies?sort=year&monitored=true", ""))
	if len(page.Movies) != 1 || page.Movies[0].Title != "Fight Club" || page.NextCursor != "" {
		t.Errorf("list = %+v", page)
	}
	problemOK(t, e.do(t, "GET", "/api/v1/movies?sort=rating", ""), 422)
	problemOK(t, e.do(t, "GET", "/api/v1/movies?cursor=garbage", ""), 422)
	lookup := decode[[]LookupResult](t, e.do(t, "GET", "/api/v1/lookup?q=fight", ""))
	if len(lookup) != 1 || !lookup[0].InLibrary || lookup[0].MovieID != m.ID {
		t.Errorf("lookup = %+v", lookup)
	}
	problemOK(t, e.do(t, "GET", "/api/v1/lookup?q=", ""), 422)

	// Update, refresh, delete.
	r = e.do(t, "PATCH", "/api/v1/movies/"+itoa(m.ID), `{"monitored":false,"minimumAvailability":"announced"}`)
	if u := decode[Movie](t, r); r.status != 200 || u.Monitored || u.MinimumAvailability != "announced" || len(u.Tags) != 1 {
		t.Errorf("update = %d %s", r.status, r.body)
	}
	problemOK(t, e.do(t, "PATCH", "/api/v1/movies/"+itoa(m.ID), `{"minimumAvailability":"soon"}`), 422)
	if r := e.do(t, "POST", "/api/v1/movies/"+itoa(m.ID)+"/refresh", ""); r.status != 202 || !strings.Contains(r.body, `"queued":true`) {
		t.Errorf("refresh = %d %s", r.status, r.body)
	}
	if r := e.do(t, "GET", "/api/v1/jobs?limit=10", ""); r.status != 200 || !strings.Contains(r.body, library.JobMovieRefresh) {
		t.Errorf("jobs = %d %s", r.status, r.body)
	}
	if r := e.do(t, "DELETE", "/api/v1/movies/"+itoa(m.ID), ""); r.status != 204 {
		t.Errorf("delete = %d %s", r.status, r.body)
	}
	if d := problemOK(t, e.do(t, "GET", "/api/v1/movies/"+itoa(m.ID), ""), 404); d != "movie "+itoa(m.ID)+" is not in the library" {
		t.Errorf("detail = %q", d)
	}
	problemOK(t, e.do(t, "DELETE", "/api/v1/movies/"+itoa(m.ID), ""), 404)
	if r := e.do(t, "GET", "/api/v1/movies/"+itoa(m.ID)+"/history", ""); r.status != 200 {
		t.Errorf("history = %d %s", r.status, r.body)
	}
	if r := e.do(t, "GET", "/api/v1/history?limit=5", ""); r.status != 200 {
		t.Errorf("recent history = %d %s", r.status, r.body)
	}
}

func TestRootFoldersAndProfiles(t *testing.T) {
	e := newTestEnv(t, "")
	dir := t.TempDir()
	r := e.do(t, "POST", "/api/v1/root-folders", `{"path":`+quote(dir)+`}`)
	rf := decode[RootFolder](t, r)
	if r.status != 201 || rf.Path != dir || !rf.Accessible || rf.FreeSpace == nil {
		t.Fatalf("add root folder = %d %s", r.status, r.body)
	}
	if d := problemOK(t, e.do(t, "POST", "/api/v1/root-folders", `{"path":"relative"}`), 422); !strings.Contains(d, "absolute") {
		t.Errorf("detail = %q", d)
	}
	list := decode[[]RootFolder](t, e.do(t, "GET", "/api/v1/root-folders", ""))
	if len(list) != 2 {
		t.Errorf("root folders = %+v", list)
	}
	if r := e.do(t, "DELETE", "/api/v1/root-folders/"+itoa(rf.ID), ""); r.status != 204 {
		t.Errorf("delete root folder = %d %s", r.status, r.body)
	}
	profiles := decode[[]QualityProfile](t, e.do(t, "GET", "/api/v1/quality-profiles", ""))
	if len(profiles) != 5 || len(profiles[0].Items) == 0 {
		t.Errorf("profiles = %d", len(profiles))
	}
}

func TestGzip(t *testing.T) {
	e := newTestEnv(t, "")
	req, _ := http.NewRequestWithContext(context.Background(), "GET", e.ts.URL+"/api/v1/quality-profiles", http.NoBody)
	req.Header.Set("Accept-Encoding", "gzip")
	req.Header.Set(APIKeyHeader, e.apiKey)
	resp, err := (&http.Transport{DisableCompression: true}).RoundTrip(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if resp.Header.Get("Content-Encoding") != "gzip" {
		t.Fatalf("Content-Encoding = %q", resp.Header.Get("Content-Encoding"))
	}
	zr, err := gzip.NewReader(resp.Body)
	if err != nil {
		t.Fatal(err)
	}
	var profiles []QualityProfile
	if err := json.NewDecoder(zr).Decode(&profiles); err != nil || len(profiles) == 0 {
		t.Errorf("decoded %d profiles: %v", len(profiles), err)
	}
}

func TestBaseURL(t *testing.T) {
	e := newTestEnv(t, "/gonema")
	if r := e.do(t, "GET", "/gonema/api/v1/system/status", ""); r.status != 200 {
		t.Errorf("status under base URL = %d", r.status)
	}
	for _, p := range []string{"/health", "/gonema/health"} {
		if r := e.do(t, "GET", p, ""); r.status != 200 {
			t.Errorf("%s = %d", p, r.status)
		}
	}
	if r := e.do(t, "GET", "/api/v1/system/status", ""); r.status != 404 {
		t.Errorf("status outside base URL = %d", r.status)
	}
	if !strings.Contains(e.do(t, "GET", "/gonema/api/v1/openapi.json", "").body, `"url":"/gonema/api/v1"`) {
		t.Error("OpenAPI server URL lacks the base URL")
	}
}

func TestRecover(t *testing.T) {
	h := withRequestID(withRecover(slog.New(slog.DiscardHandler), http.HandlerFunc(func(http.ResponseWriter, *http.Request) {
		panic("bug")
	})))
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest("GET", "/x", http.NoBody))
	if rec.Code != 500 || rec.Header().Get("Content-Type") != "application/problem+json" || !strings.Contains(rec.Body.String(), "request id") {
		t.Errorf("recover = %d %s", rec.Code, rec.Body.String())
	}
}

// sseClient reads an event stream line by line.
type sseClient struct {
	resp  *http.Response
	lines chan string
}

func openSSE(t *testing.T, e *testEnv, lastID string) *sseClient {
	t.Helper()
	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)
	req, _ := http.NewRequestWithContext(ctx, "GET", e.ts.URL+"/api/v1/events", http.NoBody)
	req.Header.Set("Accept-Encoding", "gzip") // must not be compressed
	req.Header.Set(APIKeyHeader, e.apiKey)
	if lastID != "" {
		req.Header.Set("Last-Event-ID", lastID)
	}
	resp, err := e.ts.Client().Do(req)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { resp.Body.Close() })
	if resp.StatusCode != 200 || resp.Header.Get("Content-Type") != "text/event-stream" || resp.Header.Get("Content-Encoding") != "" {
		t.Fatalf("events = %d %v", resp.StatusCode, resp.Header)
	}
	c := &sseClient{resp: resp, lines: make(chan string, 256)}
	go func() {
		defer close(c.lines)
		sc := bufio.NewScanner(resp.Body)
		for sc.Scan() {
			c.lines <- sc.Text()
		}
	}()
	return c
}

// waitFor returns once a line equal to want is read.
func (c *sseClient) waitFor(t *testing.T, want string) []string {
	t.Helper()
	var seen []string
	timeout := time.After(5 * time.Second)
	for {
		select {
		case l, ok := <-c.lines:
			if !ok {
				t.Fatalf("stream closed before %q; read %q", want, seen)
			}
			seen = append(seen, l)
			if l == want {
				return seen
			}
		case <-timeout:
			t.Fatalf("no %q within 5 s; read %q", want, seen)
		}
	}
}

func TestSSE(t *testing.T) {
	e := newTestEnv(t, "")
	c := openSSE(t, e, "")
	c.waitFor(t, ": connected")
	c.waitFor(t, ": ping") // heartbeat (50 ms in tests)

	if r := e.do(t, "PUT", "/api/v1/settings/tmdb", `{"key":"`+testKey+`"}`); r.status != 200 {
		t.Fatal(r.body)
	}
	add := `{"tmdbId":550,"rootFolderId":` + itoa(e.rootID) + `,"qualityProfileId":` + itoa(e.profID) + `}`
	if r := e.do(t, "POST", "/api/v1/movies", add); r.status != 201 {
		t.Fatal(r.body)
	}
	lines := c.waitFor(t, "event: movie.added")
	idLine := lines[len(lines)-2]
	var data string
	for data == "" {
		if l := <-c.lines; strings.HasPrefix(l, "data: ") {
			data = strings.TrimPrefix(l, "data: ")
		}
	}
	var ev struct {
		ID      uint64 `json:"id"`
		Type    string `json:"type"`
		MovieID int64  `json:"movieId"`
		Data    struct {
			Title string `json:"title"`
		} `json:"data"`
	}
	if err := json.Unmarshal([]byte(data), &ev); err != nil || ev.Type != "movie.added" || ev.MovieID == 0 || ev.Data.Title != "Fight Club" {
		t.Errorf("event = %s (%v)", data, err)
	}
	if idLine != "id: "+itoa(int64(ev.ID)) {
		t.Errorf("id line = %q, event id %d", idLine, ev.ID)
	}

	// Reconnecting with Last-Event-ID replays what came after it.
	c2 := openSSE(t, e, itoa(int64(ev.ID-1)))
	c2.waitFor(t, "event: movie.added")

	// Shutdown ends the streams so that http.Server.Shutdown can complete.
	e.srv.Shutdown()
	for l := range c.lines {
		_ = l // drain until the server closes the stream
	}
}

func itoa(n int64) string { return strconv.FormatInt(n, 10) }

func quote(s string) string {
	b, _ := json.Marshal(s)
	return string(b)
}

// TestSSECaps: the open event streams are capped per caller and in total;
// beyond, 429 with Retry-After. A closed stream frees its slot.
func TestSSECaps(t *testing.T) {
	e := newTestEnvDeps(t, "", nil, true, func(d *Deps) { d.MaxStreams, d.MaxStreamsPerCaller = 3, 2 })
	type stream struct {
		status     int
		retryAfter string
	}
	// open starts a stream and keeps it open until cancel (or the end of the test).
	open := func(header ...string) (stream, context.CancelFunc) {
		t.Helper()
		ctx, cancel := context.WithCancel(context.Background())
		t.Cleanup(cancel)
		req, _ := http.NewRequestWithContext(ctx, "GET", e.ts.URL+"/api/v1/events", http.NoBody)
		for i := 0; i+1 < len(header); i += 2 {
			req.Header.Set(header[i], header[i+1])
		}
		resp, err := http.DefaultTransport.RoundTrip(req) //nolint:bodyclose // closed by the reader goroutine below
		if err != nil {
			t.Fatal(err)
		}
		go func() {
			defer resp.Body.Close()
			_, _ = io.Copy(io.Discard, resp.Body)
		}()
		return stream{resp.StatusCode, resp.Header.Get("Retry-After")}, cancel
	}
	key := []string{APIKeyHeader, e.apiKey}
	first, closeFirst := open(key...)
	second, _ := open(key...)
	third, _ := open(key...)
	if first.status != 200 || second.status != 200 || third.status != 429 || third.retryAfter == "" {
		t.Fatalf("per caller: %d %d %d", first.status, second.status, third.status)
	}
	// Another caller (a browser session) gets the last global slot, then no more.
	b := &browser{t: t, base: e.ts.URL}
	b.do("POST", "/api/v1/auth/login", `{"username":"admin","password":"`+testPassword+`"}`)
	cookie := []string{"Cookie", SessionCookie + "=" + b.cookie}
	if r, _ := open(cookie...); r.status != 200 {
		t.Fatalf("session stream = %d", r.status)
	}
	if r, _ := open(cookie...); r.status != 429 {
		t.Fatalf("over the global cap = %d", r.status)
	}
	closeFirst()
	deadline := time.Now().Add(5 * time.Second)
	for {
		r, cancel := open(cookie...)
		if r.status == 200 {
			break
		}
		cancel()
		if time.Now().After(deadline) {
			t.Fatalf("the closed stream never freed its slot: %d", r.status)
		}
		time.Sleep(20 * time.Millisecond)
	}
}
