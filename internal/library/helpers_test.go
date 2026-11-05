package library

import (
	"context"
	"errors"
	"io/fs"
	"maps"
	"slices"
	"sync"
	"testing"
	"testing/fstest"
	"time"

	"github.com/fs0ciety7000/gonema/internal/events"
	"github.com/fs0ciety7000/gonema/internal/jobs"
	"github.com/fs0ciety7000/gonema/internal/metadata"
	"github.com/fs0ciety7000/gonema/internal/secretbox"
	"github.com/fs0ciety7000/gonema/internal/store"
	"github.com/fs0ciety7000/gonema/internal/store/db"
	"github.com/fs0ciety7000/gonema/internal/store/storetest"
)

var testNow = time.Date(2026, 11, 5, 12, 0, 0, 0, time.UTC)

// fakeProvider is an in-memory metadata.Provider.
type fakeProvider struct {
	mu          sync.Mutex
	movies      map[int]*metadata.Movie
	collections map[int]*metadata.Collection
	calls       map[string]int
	validateErr error
}

func newFakeProvider() *fakeProvider {
	return &fakeProvider{movies: map[int]*metadata.Movie{}, collections: map[int]*metadata.Collection{}, calls: map[string]int{}}
}

func (p *fakeProvider) set(m *metadata.Movie) {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.movies[m.TMDBID] = m
}

func (p *fakeProvider) count(name string) int {
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.calls[name]
}

func (p *fakeProvider) Movie(_ context.Context, id int) (*metadata.Movie, error) {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.calls["Movie"]++
	m, ok := p.movies[id]
	if !ok {
		return nil, metadata.ErrNotFound
	}
	c := *m
	return &c, nil
}

func (p *fakeProvider) Search(_ context.Context, query string, _, _ int) (metadata.Page[metadata.MovieSummary], error) {
	p.mu.Lock()
	defer p.mu.Unlock()
	var out []metadata.MovieSummary
	for _, id := range sortedKeys(p.movies) {
		if m := p.movies[id]; m.Title == query {
			out = append(out, metadata.MovieSummary{TMDBID: m.TMDBID, Title: m.Title, Year: m.Year})
		}
	}
	return metadata.Page[metadata.MovieSummary]{Page: 1, TotalPages: 1, TotalResults: len(out), Results: out}, nil
}

func sortedKeys(m map[int]*metadata.Movie) []int { return slices.Sorted(maps.Keys(m)) }

func (p *fakeProvider) FindByIMDb(_ context.Context, imdbID string) (metadata.MovieSummary, error) {
	p.mu.Lock()
	defer p.mu.Unlock()
	for _, m := range p.movies {
		if m.IMDbID == imdbID {
			return metadata.MovieSummary{TMDBID: m.TMDBID, Title: m.Title, Year: m.Year}, nil
		}
	}
	return metadata.MovieSummary{}, metadata.ErrNotFound
}

func (p *fakeProvider) Collection(_ context.Context, id int) (*metadata.Collection, error) {
	p.mu.Lock()
	defer p.mu.Unlock()
	c, ok := p.collections[id]
	if !ok {
		return nil, metadata.ErrNotFound
	}
	return c, nil
}

func (p *fakeProvider) PersonFilmography(context.Context, int) (*metadata.Filmography, error) {
	return nil, metadata.ErrNotFound
}

func (p *fakeProvider) Popular(context.Context, int) (metadata.Page[metadata.MovieSummary], error) {
	return metadata.Page[metadata.MovieSummary]{}, nil
}

func (p *fakeProvider) Trending(context.Context, metadata.TrendingWindow, int) (metadata.Page[metadata.MovieSummary], error) {
	return metadata.Page[metadata.MovieSummary]{}, nil
}

func (p *fakeProvider) Upcoming(context.Context, int) (metadata.Page[metadata.MovieSummary], error) {
	return metadata.Page[metadata.MovieSummary]{}, nil
}

func (p *fakeProvider) NowPlaying(context.Context, int) (metadata.Page[metadata.MovieSummary], error) {
	return metadata.Page[metadata.MovieSummary]{}, nil
}

func (p *fakeProvider) ValidateKey(context.Context) error { return p.validateErr }

// released returns a movie released long ago.
func released(id int, title string, year int) *metadata.Movie {
	d := metadata.Date{Year: year, Month: 6, Day: 1}
	return &metadata.Movie{
		TMDBID: id, Title: title, OriginalTitle: title, Year: year, ReleaseDate: d,
		IMDbID:   "tt" + itoa7(id),
		Releases: metadata.ReleaseSelection{Cinema: metadata.DatedRelease{Date: d, Region: "US", Type: metadata.ReleaseTheatrical}},
		Genres:   []string{"Drama"}, PosterPath: "/poster" + itoa7(id) + ".jpg",
	}
}

func itoa7(n int) string {
	b := []byte("0000000")
	for i := 6; i >= 0 && n > 0; i-- {
		b[i] = byte('0' + n%10)
		n /= 10
	}
	return string(b)
}

// memFS is an FS backed by a map of directories.
type memFS struct {
	dirs      fstest.MapFS
	readOnly  map[string]bool
	free      uint64
	statCalls int
}

func newMemFS(dirs ...string) *memFS {
	m := &memFS{dirs: fstest.MapFS{}, readOnly: map[string]bool{}, free: 500 << 30}
	for _, d := range dirs {
		m.dirs[d[1:]] = &fstest.MapFile{Mode: fs.ModeDir | 0o755}
	}
	return m
}

func (m *memFS) Stat(name string) (fs.FileInfo, error) {
	m.statCalls++
	if name == "/" {
		return fs.Stat(m.dirs, ".")
	}
	return fs.Stat(m.dirs, name[1:])
}

func (m *memFS) CheckWritable(dir string) error {
	if m.readOnly[dir] {
		return fs.ErrPermission
	}
	return nil
}

func (m *memFS) DiskSpace(string) (free, total uint64, err error) { return m.free, 1 << 40, nil }

// fakeJobs records enqueued jobs.
type fakeJobs struct {
	mu    sync.Mutex
	kinds []string
}

func (f *fakeJobs) Enqueue(_ context.Context, kind string, _ any, _ jobs.EnqueueOptions) (db.Job, bool, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.kinds = append(f.kinds, kind)
	return db.Job{Kind: kind}, true, nil
}

func (*fakeJobs) Has(string) bool { return true }

func (f *fakeJobs) enqueued() []string {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]string(nil), f.kinds...)
}

type env struct {
	svc  *Service
	st   *store.Store
	prov *fakeProvider
	fs   *memFS
	jobs *fakeJobs
	bus  *events.Bus
	lib  storetest.Library
	now  *time.Time
}

func newEnv(t *testing.T) *env {
	t.Helper()
	st := storetest.New(t)
	key, err := secretbox.NewKey()
	if err != nil {
		t.Fatal(err)
	}
	box, err := secretbox.New(key)
	if err != nil {
		t.Fatal(err)
	}
	e := &env{st: st, prov: newFakeProvider(), fs: newMemFS("/movies", "/other"), jobs: &fakeJobs{}, bus: events.New(events.Options{})}
	now := testNow
	e.now = &now
	e.svc, err = New(Options{
		Store: st, Box: box, Jobs: e.jobs, Bus: e.bus, FS: e.fs,
		NewProvider: func(key string) (KeyedProvider, error) {
			if key == "bad-format" {
				return nil, errors.New("format")
			}
			return e.prov, nil
		},
		Now: func() time.Time { return *e.now },
	})
	if err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	if err := e.svc.Init(ctx); err != nil {
		t.Fatal(err)
	}
	if err := e.svc.SetTMDBKey(ctx, "0123456789abcdef0123456789abcdef"); err != nil {
		t.Fatal(err)
	}
	e.lib = storetest.SeedLibrary(t, st) // profile "HD" and root folder "/movies"
	return e
}

func (e *env) add(t *testing.T, tmdbID int) db.Movie {
	t.Helper()
	m, _, err := e.svc.AddMovie(context.Background(), tmdbID, AddOptions{
		RootFolderID: e.lib.RootFolderID, QualityProfileID: e.lib.ProfileID, Monitored: true,
	})
	if err != nil {
		t.Fatalf("AddMovie(%d): %v", tmdbID, err)
	}
	return m
}
