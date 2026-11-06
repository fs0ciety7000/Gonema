package mediaserver

import (
	"context"
	"errors"
	"slices"
	"strconv"
	"strings"
	"sync"
	"testing"
	"testing/synctest"
	"time"

	"github.com/fs0ciety7000/gonema/internal/events"
	"github.com/fs0ciety7000/gonema/internal/outbound"
	"github.com/fs0ciety7000/gonema/internal/secretbox"
	"github.com/fs0ciety7000/gonema/internal/store"
	"github.com/fs0ciety7000/gonema/internal/store/db"
	"github.com/fs0ciety7000/gonema/internal/store/storetest"
)

// TestDebounce runs on the fake clock of testing/synctest: no real sleeps.
func TestDebounce(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		type emitted struct {
			t      target
			reason Reason
			at     time.Duration
		}
		var (
			mu  sync.Mutex
			got []emitted
		)
		start := time.Now()
		d := newDebouncer(5*time.Second, 30*time.Second, time.Now, func(tg target, r Reason) {
			mu.Lock()
			got = append(got, emitted{tg, r, time.Since(start)})
			mu.Unlock()
		})
		snapshot := func() []emitted {
			synctest.Wait()
			mu.Lock()
			defer mu.Unlock()
			return slices.Clone(got)
		}
		a := target{server: 1, folder: "/movies/Halcyon Drift (2024)"}
		b := target{server: 2, folder: "/movies/Halcyon Drift (2024)"}

		// An upgrade: the old file is recycled, the new one imported, then renamed.
		d.add(a, ReasonDelete)
		d.add(b, ReasonDelete)
		time.Sleep(2 * time.Second)
		d.add(a, ReasonUpgrade)
		time.Sleep(3 * time.Second)
		d.add(a, ReasonRename)
		time.Sleep(4 * time.Second)
		if g := snapshot(); len(g) != 1 || g[0].t != b || g[0].reason != ReasonDelete || g[0].at != 5*time.Second {
			t.Fatalf("after 9 s: %+v (only b, debounced 5 s after its single request)", g)
		}
		time.Sleep(time.Second)
		g := snapshot()
		if len(g) != 2 || g[1].t != a || g[1].reason != ReasonUpgrade || g[1].at != 10*time.Second {
			t.Fatalf("after 10 s: %+v (a once, as an upgrade, 5 s after the last request)", g)
		}

		// A stream of requests is flushed after maxWait.
		mu.Lock()
		got, start = nil, time.Now()
		mu.Unlock()
		for range 12 {
			d.add(a, ReasonRename)
			time.Sleep(4 * time.Second)
		}
		g = snapshot()
		if len(g) < 1 || g[0].at != 30*time.Second {
			t.Fatalf("max wait: %+v", g)
		}
		if n := d.close(); n != 1 {
			t.Errorf("close dropped %d pending, want 1", n)
		}
		d.add(a, ReasonAdd) // ignored once closed
		time.Sleep(time.Minute)
		if g := snapshot(); len(g) != 1 {
			t.Errorf("after close: %+v (nothing new)", g)
		}
	})
}

// fakeServer records refreshes; fail makes Refresh fail with err.
type fakeServer struct {
	mu   sync.Mutex
	cfgs []Config
	reqs []RefreshRequest
	err  error
	got  chan RefreshRequest
}

func (f *fakeServer) build(cfg Config, _ Options) (Server, error) {
	f.mu.Lock()
	f.cfgs = append(f.cfgs, cfg)
	f.mu.Unlock()
	return f, nil
}

func (f *fakeServer) Test(context.Context) error { return f.error() }

func (f *fakeServer) error() error {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.err
}

func (f *fakeServer) Refresh(_ context.Context, r RefreshRequest) error {
	f.mu.Lock()
	f.reqs = append(f.reqs, r)
	err := f.err
	f.mu.Unlock()
	f.got <- r
	return err
}

type env struct {
	st   *store.Store
	bus  *events.Bus
	svc  *Service
	fake *fakeServer
	lib  storetest.Library
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
	bus := events.New(events.Options{})
	fake := &fakeServer{got: make(chan RefreshRequest, 16)}
	svc, err := NewService(ServiceOptions{
		Store: st, Box: box, Bus: bus, Debounce: 300 * time.Millisecond, MaxWait: 5 * time.Second, NewServer: fake.build,
		Retry: outbound.Retry{Attempts: 2, Sleep: func(context.Context, time.Duration) error { return nil }},
	})
	if err != nil {
		t.Fatal(err)
	}
	svc.Start()
	t.Cleanup(func() {
		svc.Close()
		bus.Close()
	})
	return &env{st: st, bus: bus, svc: svc, fake: fake, lib: storetest.SeedLibrary(t, st)}
}

func (e *env) movie(t *testing.T) db.Movie {
	t.Helper()
	m, err := e.st.Write().InsertMovie(context.Background(), db.InsertMovieParams{
		TmdbID: 42, Title: "Halcyon Drift", OriginalTitle: "Halcyon Drift", SortTitle: "halcyon drift", CleanTitle: "halcyondrift",
		OriginalLanguage: "en", Year: 2024, Status: "released", Genres: "[]", Ratings: "{}", Extra: "{}", Monitored: true,
		QualityProfileID: e.lib.ProfileID, MinimumAvailability: "released", RootFolderID: e.lib.RootFolderID,
		Path: "Halcyon Drift (2024)", AddedAt: 1,
	})
	if err != nil {
		t.Fatal(store.MapError(err))
	}
	return m
}

func ptr[T any](v T) *T { return &v }

func waitRefresh(t *testing.T, f *fakeServer) RefreshRequest {
	t.Helper()
	select {
	case r := <-f.got:
		return r
	case <-time.After(10 * time.Second):
		t.Fatal("no refresh")
	}
	return RefreshRequest{}
}

func TestServiceCRUD(t *testing.T) {
	e := newEnv(t)
	ctx := context.Background()
	in := MediaServerInput{
		Name: "Living room", Kind: Jellyfin, BaseURL: "http://jellyfin:8096/", Token: ptr(jellyfinToken),
		PathMappings: []ServerPathMapping{{From: "/movies", To: "/media/movies"}},
	}
	v, err := e.svc.Create(ctx, in)
	if err != nil {
		t.Fatal(err)
	}
	if !v.TokenSet || v.PasswordSet || v.BaseURL != "http://jellyfin:8096" || len(v.RefreshOn) != 4 || len(v.RefreshMatrix) != 4 || !v.Enabled {
		t.Errorf("created = %+v", v)
	}
	row, err := e.st.Read().GetMediaServer(ctx, v.ID)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(row.Secrets), jellyfinToken) {
		t.Error("token stored in clear")
	}
	if _, err := e.svc.open(row.Secrets, v.ID+1); err == nil {
		t.Error("secrets must be bound to their row (associated data)")
	}

	// Update without token keeps it; refresh_on narrowed.
	in.Token = nil
	in.RefreshOn = []Reason{ReasonUpgrade, ReasonAdd}
	v, err = e.svc.Update(ctx, v.ID, in)
	if err != nil || !v.TokenSet || !slices.Equal(v.RefreshOn, []Reason{ReasonAdd, ReasonUpgrade}) {
		t.Fatalf("update = %+v, %v", v, err)
	}
	if v.RefreshMatrix[2].Reason != ReasonRename || v.RefreshMatrix[2].Enabled {
		t.Errorf("matrix = %+v", v.RefreshMatrix)
	}
	res, err := e.svc.TestConfig(ctx, v.ID, MediaServerInput{Name: "x", Kind: Jellyfin, BaseURL: "http://jellyfin:8096"})
	if err != nil || !res.OK {
		t.Errorf("test config = %+v, %v", res, err)
	}
	if last := e.fake.cfgs[len(e.fake.cfgs)-1]; last.Token != jellyfinToken {
		t.Error("TestConfig must reuse the stored token when omitted")
	}

	for _, bad := range []MediaServerInput{
		{Name: "", Kind: Plex, BaseURL: "http://x"},
		{Name: "a", Kind: "emby", BaseURL: "http://x"},
		{Name: "a", Kind: Plex, BaseURL: "gopher://x"},
		{Name: "a", Kind: Plex, BaseURL: "http://u:p@x"},
		{Name: "a", Kind: Plex, BaseURL: "http://x?a=b"},
		{Name: "a", Kind: Plex, BaseURL: "http://x", RefreshOn: []Reason{"scan"}},
		{Name: "a", Kind: Plex, BaseURL: "http://x", PathMappings: []ServerPathMapping{{From: "/a"}}},
	} {
		if _, err := e.svc.Create(ctx, bad); !errors.Is(err, ErrInvalid) {
			t.Errorf("Create(%+v) = %v, want ErrInvalid", bad, err)
		}
	}
	if _, err := e.svc.Create(ctx, MediaServerInput{Name: "Living room", Kind: Plex, BaseURL: "http://x"}); !errors.Is(err, ErrInvalid) || !errors.Is(err, store.ErrConflict) {
		t.Errorf("duplicate name: %v", err)
	}
	if _, err := e.svc.Get(ctx, 999); !errors.Is(err, ErrNotFound) {
		t.Errorf("get missing: %v", err)
	}
	if err := e.svc.Delete(ctx, v.ID); err != nil {
		t.Fatal(err)
	}
	if err := e.svc.Delete(ctx, v.ID); !errors.Is(err, ErrNotFound) {
		t.Errorf("delete twice: %v", err)
	}
}

func TestRefresherFollowsEvents(t *testing.T) {
	e := newEnv(t)
	ctx := context.Background()
	m := e.movie(t)
	srv, err := e.svc.Create(ctx, MediaServerInput{Name: "Plex", Kind: Plex, BaseURL: "http://plex:32400", Token: ptr(plexToken),
		RefreshOn: []Reason{ReasonAdd, ReasonUpgrade, ReasonDelete}})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := e.svc.Create(ctx, MediaServerInput{Name: "Off", Kind: Kodi, BaseURL: "http://kodi:8080", Enabled: ptr(false)}); err != nil {
		t.Fatal(err)
	}

	// Upgrade: file recycled then new file imported -> one refresh, as an upgrade.
	e.bus.Publish(events.Event{Type: events.MovieFileDeleted, MovieID: m.ID})
	e.bus.Publish(events.Event{Type: EventFileImported, MovieID: m.ID, Data: map[string]any{"upgraded": true, "path": "x.mkv"}})
	r := waitRefresh(t, e.fake)
	if r.Path != "/movies/Halcyon Drift (2024)" || r.Reason != ReasonUpgrade {
		t.Errorf("refresh = %+v", r)
	}
	// Rename is not in refresh_on: nothing.
	e.bus.Publish(events.Event{Type: events.MovieFileRenamed, MovieID: m.ID})
	// A folder given in the event is used as is.
	e.bus.Publish(events.Event{Type: EventFileImported, MovieID: m.ID, Data: map[string]any{"path": "/movies/Other (2001)"}})
	r = waitRefresh(t, e.fake)
	if r.Path != "/movies/Other (2001)" || r.Reason != ReasonAdd {
		t.Errorf("refresh = %+v", r)
	}
	select {
	case r := <-e.fake.got:
		t.Errorf("unexpected refresh %+v", r)
	case <-time.After(time.Second): // 3x the debounce window
	}

	// A failure (after retries) is recorded and raises a health event; the
	// next success publishes the recovery.
	health := e.bus.Subscribe(8, func(ev *events.Event) bool { return ev.Type == events.HealthChanged })
	e.fake.mu.Lock()
	e.fake.err = &outbound.Error{Service: "Plex", Kind: outbound.ErrStatus, Status: 503}
	e.fake.mu.Unlock()
	e.bus.Publish(events.Event{Type: EventFileImported, MovieID: m.ID})
	waitRefresh(t, e.fake)
	waitRefresh(t, e.fake) // retried once (Attempts 2)
	ev := <-health.C()
	he, ok := ev.Data.(HealthEvent)
	if !ok || he.OK || he.ID != srv.ID || he.Source != HealthSource || !strings.Contains(he.Message, "HTTP 503") {
		t.Fatalf("health event = %+v", ev.Data)
	}
	issues, err := e.svc.HealthIssues(ctx)
	if err != nil || len(issues) != 1 || issues[0].ID != "mediaserver.failing:"+itoa(srv.ID) || issues[0].Severity != events.SeverityWarning {
		t.Fatalf("issues = %+v, %v", issues, err)
	}
	if v, _ := e.svc.Get(ctx, srv.ID); v.Health.FailureCount != 1 || !strings.Contains(v.Health.LastError, "503") || v.Health.LastFailedAt == nil {
		t.Errorf("health = %+v", v.Health)
	}
	e.fake.mu.Lock()
	e.fake.err = nil
	e.fake.mu.Unlock()
	e.bus.Publish(events.Event{Type: EventFileImported, MovieID: m.ID})
	waitRefresh(t, e.fake)
	ev = <-health.C()
	if he, _ := ev.Data.(HealthEvent); !he.OK {
		t.Errorf("recovery event = %+v", ev.Data)
	}
	if issues, _ := e.svc.HealthIssues(ctx); len(issues) != 0 {
		t.Errorf("issues after recovery = %+v", issues)
	}
}

func itoa(n int64) string { return strconv.FormatInt(n, 10) }

func TestJoinUnder(t *testing.T) {
	if p, err := joinUnder("/movies", "Heat (1995)"); err != nil || p != "/movies/Heat (1995)" {
		t.Errorf("join = %q, %v", p, err)
	}
	for _, rel := range []string{"", "/etc", "../etc", "a/../../etc"} {
		if _, err := joinUnder("/movies", rel); err == nil {
			t.Errorf("joinUnder(%q) accepted", rel)
		}
	}
}

// TestRefresherMoveRescansBothFolders: a move publishes the folder the movie
// left (oldPath) with the new one; both are rescanned.
func TestRefresherMoveRescansBothFolders(t *testing.T) {
	e := newEnv(t)
	m := e.movie(t)
	if _, err := e.svc.Create(context.Background(), MediaServerInput{Name: "Plex", Kind: Plex, BaseURL: "http://plex:32400",
		Token: ptr(plexToken), RefreshOn: []Reason{ReasonRename}}); err != nil {
		t.Fatal(err)
	}
	e.bus.Publish(events.Event{Type: events.MovieFileRenamed, MovieID: m.ID, Data: map[string]any{
		"path": "/archive/Halcyon Drift (2024)", "oldPath": "/movies/Halcyon Drift (2024)",
	}})
	got := map[string]bool{}
	for range 2 {
		r := waitRefresh(t, e.fake)
		if r.Reason != ReasonRename {
			t.Errorf("refresh = %+v", r)
		}
		got[r.Path] = true
	}
	if !got["/archive/Halcyon Drift (2024)"] || !got["/movies/Halcyon Drift (2024)"] {
		t.Errorf("refreshed folders = %v", got)
	}
}
