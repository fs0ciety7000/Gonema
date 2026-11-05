package acquire

import (
	"bytes"
	"context"
	"log/slog"
	"strconv"
	"sync"
	"testing"
	"time"

	"github.com/fs0ciety7000/gonema/internal/acquire/acquiretest"
	"github.com/fs0ciety7000/gonema/internal/events"
	"github.com/fs0ciety7000/gonema/internal/library"
	"github.com/fs0ciety7000/gonema/internal/metadata"
	"github.com/fs0ciety7000/gonema/internal/quality"
	"github.com/fs0ciety7000/gonema/internal/search"
	"github.com/fs0ciety7000/gonema/internal/secretbox"
	"github.com/fs0ciety7000/gonema/internal/store"
	"github.com/fs0ciety7000/gonema/internal/store/db"
	"github.com/fs0ciety7000/gonema/internal/store/storetest"
)

var t0 = time.Date(2026, 11, 5, 12, 0, 0, 0, time.UTC)

// Credentials of the fakes: they must never appear in logs or API views.
const (
	trackerKey = "TRACKERSECRETKEY42"
	qbUser     = "admin"
	qbPass     = "QB-PASSWORD-SECRET"
)

// Release titles used across tests (Fight Club, HD-1080p starter profile).
const (
	relBluray = "Fight.Club.1999.1080p.BluRay.x264-GRP"
	relWeb    = "Fight.Club.1999.1080p.WEB-DL.DDP5.1.H.264-OTHER"
	rel720    = "Fight.Club.1999.720p.BluRay.x264-SMALL"
	relCam    = "Fight.Club.1999.CAM.x264-BAD"
)

type clock struct {
	mu sync.Mutex
	t  time.Time
}

func (c *clock) Now() time.Time {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.t
}

func (c *clock) Advance(d time.Duration) {
	c.mu.Lock()
	c.t = c.t.Add(d)
	c.mu.Unlock()
}

type syncBuffer struct {
	mu sync.Mutex
	b  bytes.Buffer
}

func (s *syncBuffer) Write(p []byte) (int, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.b.Write(p)
}

func (s *syncBuffer) String() string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.b.String()
}

// provider is a fake TMDB.
type provider struct {
	metadata.Provider // unused methods panic
	movies            map[int]*metadata.Movie
}

func (p *provider) Movie(_ context.Context, id int) (*metadata.Movie, error) {
	m, ok := p.movies[id]
	if !ok {
		return nil, metadata.ErrNotFound
	}
	c := *m
	return &c, nil
}

func (*provider) ValidateKey(context.Context) error { return nil }

func fightClub() *metadata.Movie {
	d := metadata.Date{Year: 1999, Month: 10, Day: 15}
	return &metadata.Movie{
		TMDBID: 550, IMDbID: "tt0137523", Title: "Fight Club", OriginalTitle: "Fight Club", OriginalLanguage: "en",
		Year: 1999, ReleaseDate: d, RuntimeMin: 139,
		Releases: metadata.ReleaseSelection{Cinema: metadata.DatedRelease{Date: d, Region: "US", Type: metadata.ReleaseTheatrical}},
	}
}

// fakeImporter records import requests and answers with result/err.
type fakeImporter struct {
	mu     sync.Mutex
	reqs   []ImportRequest
	result ImportResult
	err    error
}

func (f *fakeImporter) Import(_ context.Context, req ImportRequest) (ImportResult, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.reqs = append(f.reqs, req)
	return f.result, f.err
}

func (f *fakeImporter) requests() []ImportRequest {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]ImportRequest(nil), f.reqs...)
}

type env struct {
	t       *testing.T
	ctx     context.Context
	st      *store.Store
	box     *secretbox.Box
	bus     *events.Bus
	clk     *clock
	logs    *syncBuffer
	lib     *library.Service
	svc     *Service
	imp     *fakeImporter
	imports chan int64
	rootID  int64
	hdID    int64
}

func newEnv(t *testing.T) *env {
	t.Helper()
	ctx := context.Background()
	e := &env{t: t, ctx: ctx, st: storetest.New(t), bus: events.New(events.Options{}), clk: &clock{t: t0}, logs: &syncBuffer{},
		imp: &fakeImporter{result: ImportResult{Imported: true, FileID: 1}}, imports: make(chan int64, 16)}
	key, err := secretbox.NewKey()
	if err != nil {
		t.Fatal(err)
	}
	if e.box, err = secretbox.New(key); err != nil {
		t.Fatal(err)
	}
	log := slog.New(slog.NewTextHandler(e.logs, &slog.HandlerOptions{Level: slog.LevelDebug}))
	prov := &provider{movies: map[int]*metadata.Movie{550: fightClub()}}
	e.lib, err = library.New(library.Options{
		Store: e.st, Box: e.box, Bus: e.bus, Logger: log, Now: e.clk.Now,
		NewProvider: func(string) (library.KeyedProvider, error) { return prov, nil },
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := e.lib.Init(ctx); err != nil {
		t.Fatal(err)
	}
	if err := e.lib.SetTMDBKey(ctx, "0123456789abcdef0123456789abcdef"); err != nil {
		t.Fatal(err)
	}
	rf, err := e.st.Write().CreateRootFolder(ctx, "/movies")
	if err != nil {
		t.Fatal(err)
	}
	e.rootID = rf.ID
	p, err := e.st.Read().GetQualityProfileByName(ctx, quality.ProfileHD1080p)
	if err != nil {
		t.Fatal(err)
	}
	e.hdID = p.ID
	e.svc = e.newService(log)
	return e
}

func (e *env) newService(log *slog.Logger) *Service {
	e.t.Helper()
	svc, err := New(Options{
		Store: e.st, Box: e.box, Matcher: e.lib, Bus: e.bus, Importer: e.imp, Logger: log, Now: e.clk.Now,
		Search: search.New(search.Options{Timeout: 5 * time.Second, RateLimit: 1000, Burst: 1000, Now: e.clk.Now, Logger: log}),
	})
	if err != nil {
		e.t.Fatal(err)
	}
	svc.onImportDone = func(id int64) { e.imports <- id }
	if err := svc.Init(e.ctx); err != nil {
		e.t.Fatal(err)
	}
	e.t.Cleanup(svc.Close)
	return svc
}

func (e *env) addMovie() db.Movie {
	e.t.Helper()
	m, _, err := e.lib.AddMovie(e.ctx, 550, library.AddOptions{RootFolderID: e.rootID, QualityProfileID: e.hdID, Monitored: true})
	if err != nil {
		e.t.Fatal(err)
	}
	return m
}

func (e *env) addTracker(name string, tr *acquiretest.Tracker) Indexer {
	e.t.Helper()
	key := tr.APIKey
	ix, err := e.svc.CreateIndexer(e.ctx, IndexerInput{Name: name, Kind: "torznab", BaseURL: tr.Srv.URL, APIKey: &key})
	if err != nil {
		e.t.Fatal(err)
	}
	return ix
}

func (e *env) addQB(qb *acquiretest.QBittorrent) DownloadClient {
	e.t.Helper()
	user, pass := qb.User, qb.Password
	c, err := e.svc.CreateDownloadClient(e.ctx, DownloadClientInput{
		Name: "qBittorrent", Kind: "qbittorrent", Host: "127.0.0.1", Port: int64(qb.Port()),
		Username: &user, Password: &pass, Category: "movies",
	})
	if err != nil {
		e.t.Fatal(err)
	}
	return c
}

// standardTrackers returns a slow tracker with three releases (Bluray and WEB
// accepted, 720p rejected by the HD-1080p profile) and a failing one.
func standardTrackers(t *testing.T) (slow, failing *acquiretest.Tracker) {
	slow = acquiretest.NewTracker(t, trackerKey,
		acquiretest.Item{Title: rel720, Size: 4 << 30, Seeders: 80},
		acquiretest.Item{Title: relWeb, Size: 6 << 30, Seeders: 20},
		acquiretest.Item{Title: relBluray, Size: 10 << 30, Seeders: 50, GUIDIsLink: true},
	)
	slow.SetDelay(150 * time.Millisecond)
	failing = acquiretest.NewTracker(t, "OTHERKEY")
	failing.SetFail(500)
	return slow, failing
}

func (e *env) history(movieID int64) []db.History {
	e.t.Helper()
	rows, err := e.st.Read().ListMovieHistory(e.ctx, db.ListMovieHistoryParams{MovieID: movieID, BeforeID: 1 << 62, Limit: 100})
	if err != nil {
		e.t.Fatal(err)
	}
	// oldest first
	for i, j := 0, len(rows)-1; i < j; i, j = i+1, j-1 {
		rows[i], rows[j] = rows[j], rows[i]
	}
	return rows
}

func historyEvents(rows []db.History) []string {
	out := make([]string, 0, len(rows))
	for _, r := range rows {
		out = append(out, r.Event)
	}
	return out
}

func (e *env) downloads(movieID int64) []db.Download {
	e.t.Helper()
	rows, err := e.st.Read().ListMovieDownloads(e.ctx, movieID)
	if err != nil {
		e.t.Fatal(err)
	}
	for i, j := 0, len(rows)-1; i < j; i, j = i+1, j-1 {
		rows[i], rows[j] = rows[j], rows[i]
	}
	return rows
}

func (e *env) waitImport(id int64) {
	e.t.Helper()
	select {
	case got := <-e.imports:
		if got != id {
			e.t.Fatalf("import of download %d done, want %d", got, id)
		}
	case <-time.After(10 * time.Second):
		e.t.Fatalf("import of download %d did not finish", id)
	}
}

func itoa(n int64) string { return strconv.FormatInt(n, 10) }
