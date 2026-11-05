package notify

import (
	"context"
	"errors"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/fs0ciety7000/gonema/internal/events"
	"github.com/fs0ciety7000/gonema/internal/outbound"
	"github.com/fs0ciety7000/gonema/internal/secretbox"
	"github.com/fs0ciety7000/gonema/internal/store"
	"github.com/fs0ciety7000/gonema/internal/store/db"
	"github.com/fs0ciety7000/gonema/internal/store/storetest"
)

// sent is one delivery seen by a fake notifier.
type sent struct {
	url string // Settings.URL of the notification: identifies it
	msg *Message
}

// fakes builds fake notifiers; every Send goes to out (after gate, when set).
type fakes struct {
	out  chan sent
	gate chan struct{} // nil: no wait

	mu    sync.Mutex
	errs  []error // returned by the next Sends, in order
	calls int
}

func (f *fakes) build(cfg Config, _ Options) (Notifier, error) {
	if cfg.Kind == Webhook && cfg.Settings.URL == "" {
		return nil, configErr("the webhook URL is not valid: the URL is empty")
	}
	return &fakeNotifier{f: f, url: cfg.Settings.URL}, nil
}

type fakeNotifier struct {
	f   *fakes
	url string
}

func (n *fakeNotifier) Test(ctx context.Context) error { return n.Send(ctx, TestMessage(time.Now())) }

func (n *fakeNotifier) Send(ctx context.Context, m *Message) error {
	if n.f.gate != nil {
		select {
		case <-n.f.gate:
		case <-ctx.Done():
			return ctx.Err()
		}
	}
	n.f.mu.Lock()
	n.f.calls++
	var err error
	if len(n.f.errs) > 0 {
		err, n.f.errs = n.f.errs[0], n.f.errs[1:]
	}
	n.f.mu.Unlock()
	select {
	case n.f.out <- sent{url: n.url, msg: m}:
	default:
	}
	return err
}

type images struct{}

func (images) ImageBaseURL(context.Context) (string, error) {
	return "https://image.tmdb.org/t/p/", nil
}

type env struct {
	st    *store.Store
	bus   *events.Bus
	svc   *Service
	fakes *fakes
	lib   storetest.Library
	slept chan time.Duration
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
	f := &fakes{out: make(chan sent, 64)}
	slept := make(chan time.Duration, 64)
	svc, err := NewService(ServiceOptions{
		Store: st, Box: box, Bus: bus, Images: images{}, Workers: 1, NewNotifier: f.build,
		Retry: outbound.Retry{Attempts: 2, Backoff: []time.Duration{time.Second}, Sleep: func(_ context.Context, d time.Duration) error {
			slept <- d
			return nil
		}},
	})
	if err != nil {
		t.Fatal(err)
	}
	svc.Start()
	t.Cleanup(func() {
		svc.Close()
		bus.Close()
	})
	return &env{st: st, bus: bus, svc: svc, fakes: f, lib: storetest.SeedLibrary(t, st), slept: slept}
}

func (e *env) movie(t *testing.T, tmdbID int64, title string, year int64, tags ...string) db.Movie {
	t.Helper()
	ctx := context.Background()
	poster := "/halcyon.jpg"
	m, err := e.st.Write().InsertMovie(ctx, db.InsertMovieParams{
		TmdbID: tmdbID, Title: title, OriginalTitle: title, SortTitle: title, CleanTitle: strings.ToLower(title), OriginalLanguage: "en",
		Year: year, Status: "released", Genres: "[]", Ratings: "{}", Extra: "{}", Monitored: true, PosterPath: &poster,
		QualityProfileID: e.lib.ProfileID, MinimumAvailability: "released", RootFolderID: e.lib.RootFolderID,
		Path: title + " (" + strconv.FormatInt(year, 10) + ")", AddedAt: 1,
	})
	if err != nil {
		t.Fatal(store.MapError(err))
	}
	for _, label := range tags {
		tag, err := e.st.Write().GetTagByLabel(ctx, label)
		if err != nil {
			if tag, err = e.st.Write().CreateTag(ctx, label); err != nil {
				t.Fatal(err)
			}
		}
		if err := e.st.Write().AddMovieTag(ctx, db.AddMovieTagParams{MovieID: m.ID, TagID: tag.ID}); err != nil {
			t.Fatal(err)
		}
	}
	return m
}

func (e *env) notification(t *testing.T, name string, evs []EventKind, tags ...string) Notification {
	t.Helper()
	n, err := e.svc.Create(context.Background(), NotificationInput{
		Name: name, Kind: Webhook, Settings: ServiceSettings{URL: "http://hooks.example.lan/" + name}, Events: evs, Tags: tags,
	})
	if err != nil {
		t.Fatal(err)
	}
	return n
}

func (e *env) next(t *testing.T) sent {
	t.Helper()
	select {
	case s := <-e.fakes.out:
		return s
	case <-time.After(10 * time.Second):
		t.Fatal("no delivery")
	}
	return sent{}
}

func grabData(dl db.Download, client string) map[string]any {
	return map[string]any{
		"id": dl.ID, "movieId": dl.MovieID, "title": dl.ReleaseTitle, "protocol": dl.Protocol, "size": dl.Size,
		"quality": dl.Quality, "qualityName": "Remux-2160p", "status": "queued", "client": client, "indexerId": dl.IndexerID,
		"messages": []string{},
	}
}

func (e *env) download(t *testing.T, m db.Movie, grabbedAt time.Time) db.Download {
	t.Helper()
	ctx := context.Background()
	ix, err := e.st.Write().GetIndexer(ctx, 1)
	if err != nil {
		ix, err = e.st.Write().CreateIndexer(ctx, db.CreateIndexerParams{
			Name: "Nebula NZB", Kind: "newznab", Protocol: "usenet", BaseUrl: "https://nebula.example", ApiPath: "/api",
			Categories: "[]", Priority: 25, Tags: "[]",
		})
		if err != nil {
			t.Fatal(store.MapError(err))
		}
	}
	dl, err := e.st.Write().InsertDownload(ctx, db.InsertDownloadParams{
		MovieID: m.ID, IndexerID: &ix.ID, ReleaseTitle: "Halcyon.Drift.2024.2160p.UHD.BluRay.REMUX-GRP", ReleaseGuid: "g" + strconv.FormatInt(grabbedAt.UnixNano(), 10),
		Protocol: "usenet", Size: 62_706_909_594, Quality: "remux-2160p", Revision: 1, Decision: "{}", GrabbedAt: store.Millis(grabbedAt),
	})
	if err != nil {
		t.Fatal(store.MapError(err))
	}
	return dl
}

// TestMessagesCopy checks the copy examples of the brief, built from events
// and the database.
func TestMessagesCopy(t *testing.T) {
	e := newEnv(t)
	ctx := context.Background()
	if _, err := e.svc.SetAppURL(ctx, "https://gonema.example.lan/"); err != nil {
		t.Fatal(err)
	}
	m := e.movie(t, 900001, "Halcyon Drift", 2024)
	dl := e.download(t, m, time.Now())
	b := e.svc.b

	grab := b.build(ctx, &events.Event{Type: EventDownloadGrabbed, MovieID: m.ID, Time: time.Now(), Data: grabData(dl, "SABnzbd")})
	if grab == nil || grab.msg.Summary != "Grabbed — Halcyon Drift (2024) · Remux-2160p · 58.4 GB from Nebula NZB" {
		t.Fatalf("grab = %+v", grab)
	}
	if grab.msg.PosterURL != "https://image.tmdb.org/t/p/w342/halcyon.jpg" || grab.msg.Link != "https://gonema.example.lan/movies/"+strconv.FormatInt(m.ID, 10) {
		t.Errorf("poster %q link %q", grab.msg.PosterURL, grab.msg.Link)
	}
	if grab.msg.Body != "Remux-2160p · 58.4 GB from Nebula NZB. Sent to SABnzbd." {
		t.Errorf("grab body = %q", grab.msg.Body)
	}

	if _, err := e.st.Write().AppendHistory(ctx, db.AppendHistoryParams{
		MovieID: &m.ID, Event: "upgraded", SourceTitle: dl.ReleaseTitle,
		Data: `{"previous":{"quality":"webdl-2160p","path":"old.mkv"}}`, CreatedAt: 1,
	}); err != nil {
		t.Fatal(err)
	}
	up := b.build(ctx, &events.Event{Type: EventFileImported, MovieID: m.ID, Time: time.Now(), Data: map[string]any{
		"fileId": 1, "path": "x.mkv", "quality": "remux-2160p", "upgraded": true, "downloadId": dl.ID,
	}})
	if up == nil || up.msg.Event != EventUpgrade || up.msg.Summary != "Upgraded — Halcyon Drift (2024): WEBDL-2160p → Remux-2160p" {
		t.Fatalf("upgrade = %+v", up)
	}

	failed := grabData(dl, "SABnzbd")
	failed["status"] = "failed"
	failed["messages"] = []string{"unpacking failed: CRC error"}
	f := b.build(ctx, &events.Event{Type: EventDownloadFailed, MovieID: m.ID, Time: time.Now(), Data: failed})
	if f == nil || !f.msg.Problem || f.msg.Summary != "Download failed — Halcyon Drift (2024). Blocklisted it, trying release #2 of 6." {
		t.Fatalf("failure = %+v", f)
	}
	if f.msg.Body != "Unpacking failed: CRC error. Blocklisted it, trying release #2 of 6." {
		t.Errorf("failure body = %q", f.msg.Body)
	}
	// Daily budget used: retries stop until tomorrow.
	if err := store.SetSetting(ctx, e.st, SettingMaxAttempts, 1); err != nil {
		t.Fatal(err)
	}
	f = b.build(ctx, &events.Event{Type: EventDownloadFailed, MovieID: m.ID, Time: time.Now(), Data: failed})
	if !strings.Contains(f.msg.Body, "release 1 of 1 today: automatic retries stop until tomorrow") {
		t.Errorf("exhausted = %q", f.msg.Body)
	}

	warn := grabData(dl, "SABnzbd")
	warn["status"] = "warning"
	warn["messages"] = []string{"Import failed: no video file in the download"}
	ev := &events.Event{Type: EventDownloadUpdated, MovieID: m.ID, Time: time.Now(), Data: warn}
	if s := b.build(ctx, ev); s == nil || s.msg.Title != "Import failed — Halcyon Drift (2024)" || !s.msg.Problem {
		t.Fatalf("import failed = %+v", s)
	}
	if s := b.build(ctx, ev); s != nil {
		t.Error("the same import failure must be notified once")
	}
	warn["status"] = "downloading"
	if s := b.build(ctx, &events.Event{Type: EventDownloadUpdated, MovieID: m.ID, Data: warn}); s != nil {
		t.Error("progress updates are not notified")
	}

	issue := events.HealthIssueEvent{Issue: events.HealthIssue{
		ID: "indexer.unreachable:3", Severity: events.SeverityWarning, Title: "Nebula NZB is not answering",
		Message: "Nebula NZB hasn't answered for 10 minutes. Others keep searching.", Fix: []string{"Check the indexer URL."},
		Link: "/settings/indexers/3", Source: "indexers",
	}}
	h := b.build(ctx, &events.Event{Type: events.HealthIssueRaised, Data: issue})
	if h == nil || h.msg.Title != "Health issue — Nebula NZB is not answering" || !h.msg.Problem || h.msg.HealthID != 3 ||
		h.msg.Link != "https://gonema.example.lan/settings/indexers/3" {
		t.Fatalf("health = %+v", h)
	}
	if h := b.build(ctx, &events.Event{Type: events.HealthIssueResolved, Data: issue}); h == nil || h.msg.Problem || h.msg.Title != "Resolved — Nebula NZB is not answering" {
		t.Errorf("resolved = %+v", h)
	}

	del := b.build(ctx, &events.Event{Type: events.MovieDeleted, MovieID: 99, Data: map[string]any{
		"id": 99, "title": "Gone Girl", "year": 2014, "note": "removed from the library; the folder and its files were kept on disk",
	}})
	if del == nil || del.msg.Summary != "Removed — Gone Girl (2014). Removed from the library; the folder and its files were kept on disk." {
		t.Errorf("delete = %+v", del)
	}
	if s := b.build(ctx, &events.Event{Type: events.MovieUpdated, MovieID: m.ID}); s != nil {
		t.Error("movie.updated is not notified")
	}
}

// TestDispatchSelection: event selection and tag filtering. One worker: the
// deliveries come out in the order they were queued, so a final message to
// "all" proves the earlier ones were (or were not) sent.
func TestDispatchSelection(t *testing.T) {
	e := newEnv(t)
	kids := e.movie(t, 1, "Paddington", 2014, "kids")
	adult := e.movie(t, 2, "Heat", 1995, "crime")
	e.notification(t, "all", []EventKind{EventGrab, EventHealth})
	e.notification(t, "kids-imports", []EventKind{EventImport}, "Kids")
	e.notification(t, "kids-grabs", []EventKind{EventGrab}, "kids")
	off := e.notification(t, "off", nil)
	if _, err := e.svc.Update(context.Background(), off.ID, NotificationInput{Name: "off", Kind: Webhook, Settings: ServiceSettings{URL: "http://x/off"}, Enabled: ptr(false)}); err != nil {
		t.Fatal(err)
	}
	dlAdult, dlKids := e.download(t, adult, time.Now()), e.download(t, kids, time.Now().Add(time.Second))

	e.bus.Publish(events.Event{Type: EventDownloadGrabbed, MovieID: adult.ID, Data: grabData(dlAdult, "SAB")})
	e.bus.Publish(events.Event{Type: EventDownloadGrabbed, MovieID: kids.ID, Data: grabData(dlKids, "SAB")})
	e.bus.Publish(events.Event{Type: EventFileImported, MovieID: adult.ID, Data: map[string]any{"quality": "remux-2160p"}})
	e.bus.Publish(events.Event{Type: EventFileImported, MovieID: kids.ID, Data: map[string]any{"quality": "remux-2160p"}})
	e.bus.Publish(events.Event{Type: events.HealthIssueRaised, Data: events.HealthIssueEvent{Issue: events.HealthIssue{
		ID: "x", Title: "End", Message: "sentinel", Source: "test", Severity: events.SeverityInfo,
	}}})

	var got []string
	for {
		s := e.next(t)
		got = append(got, strings.TrimPrefix(s.url, "http://hooks.example.lan/")+":"+string(s.msg.Event)+":"+movieName(s.msg))
		if s.msg.Event == EventHealth {
			break
		}
	}
	want := "all:grab:Heat|all:grab:Paddington|kids-grabs:grab:Paddington|kids-imports:import:Paddington|all:health:"
	if strings.Join(got, "|") != want {
		t.Errorf("deliveries:\n got %s\nwant %s", strings.Join(got, "|"), want)
	}
}

func movieName(m *Message) string {
	if m.Movie == nil {
		return ""
	}
	return m.Movie.Title
}

func ptr[T any](v T) *T { return &v }

// TestDispatchNeverBlocks: deliveries stuck on a slow service neither block
// the bus nor the dispatcher's subscription; the overflow is dropped.
func TestDispatchNeverBlocks(t *testing.T) {
	e := newEnv(t)
	e.fakes.gate = make(chan struct{})
	m := e.movie(t, 1, "Heat", 1995)
	dl := e.download(t, m, time.Now())
	e.notification(t, "slow", []EventKind{EventGrab})
	other := e.bus.Subscribe(4096, nil)
	defer other.Close()

	const n = 600 // more than the queue (256) + the worker
	done := make(chan struct{})
	go func() {
		defer close(done)
		for range n {
			e.bus.Publish(events.Event{Type: EventDownloadGrabbed, MovieID: m.ID, Data: grabData(dl, "SAB")})
		}
	}()
	select {
	case <-done:
	case <-time.After(10 * time.Second):
		t.Fatal("Publish blocked behind a slow notification")
	}
	if got := len(other.C()); got != n {
		t.Errorf("other subscriber got %d events, want %d", got, n)
	}
	deadline := time.Now().Add(10 * time.Second)
	for e.svc.Dropped() == 0 {
		if time.Now().After(deadline) {
			t.Fatal("the dispatcher never dropped: its listener is stuck behind the worker")
		}
		time.Sleep(5 * time.Millisecond)
	}
	if e.svc.sub.Dropped() != 0 {
		t.Errorf("the bus dropped %d events for the dispatcher: its listener waited on deliveries", e.svc.sub.Dropped())
	}
	close(e.fakes.gate)
	e.next(t)
}

func TestDispatchRetryAndHealth(t *testing.T) {
	e := newEnv(t)
	m := e.movie(t, 1, "Heat", 1995)
	dl := e.download(t, m, time.Now())
	a := e.notification(t, "a", []EventKind{EventGrab, EventHealth})
	e.notification(t, "b", []EventKind{EventHealth})
	health := e.bus.Subscribe(8, func(ev *events.Event) bool { return ev.Type == events.HealthChanged })
	grab := events.Event{Type: EventDownloadGrabbed, MovieID: m.ID, Data: grabData(dl, "SAB")}

	// 429 with Retry-After: waited, then delivered.
	e.fakes.mu.Lock()
	e.fakes.errs = []error{&outbound.Error{Service: "Webhook", Kind: outbound.ErrStatus, Status: 429, RetryAfter: 7 * time.Second}}
	e.fakes.mu.Unlock()
	e.bus.Publish(grab)
	e.next(t)
	e.next(t)
	if d := <-e.slept; d != 7*time.Second {
		t.Errorf("waited %s, want the 7 s of Retry-After", d)
	}

	// Three failed deliveries (each retried once) raise a health event.
	fail := &outbound.Error{Service: "Webhook", Kind: outbound.ErrStatus, Status: 503}
	e.fakes.mu.Lock()
	e.fakes.errs = []error{fail, fail, fail, fail, fail, fail}
	e.fakes.mu.Unlock()
	for range FailuresBeforeHealth {
		e.bus.Publish(grab)
		e.next(t)
		e.next(t)
	}
	ev := <-health.C()
	he, ok := ev.Data.(HealthEvent)
	if !ok || he.OK || he.ID != a.ID || he.Source != HealthSource || !strings.Contains(he.Message, "HTTP 503") {
		t.Fatalf("health event = %+v", ev.Data)
	}
	v, _ := e.svc.Get(context.Background(), a.ID)
	if v.Health.FailureCount != FailuresBeforeHealth || !strings.Contains(v.Health.LastError, "503") {
		t.Errorf("health = %+v", v.Health)
	}
	issues, err := e.svc.HealthIssues(context.Background())
	if err != nil || len(issues) != 1 || issues[0].ID != "notification.failing:"+strconv.FormatInt(a.ID, 10) {
		t.Fatalf("issues = %+v, %v", issues, err)
	}

	// The issue (published by the health checks) reaches b, never a.
	e.bus.Publish(events.Event{Type: events.HealthIssueRaised, Data: events.HealthIssueEvent{Issue: issues[0]}})
	if s := e.next(t); !strings.HasSuffix(s.url, "/b") || s.msg.Event != EventHealth {
		t.Errorf("health issue went to %s", s.url)
	}

	// Recovery.
	e.bus.Publish(grab)
	e.next(t)
	ev = <-health.C()
	if he, _ := ev.Data.(HealthEvent); !he.OK || he.ID != a.ID {
		t.Errorf("recovery = %+v", ev.Data)
	}
}

func TestServiceCRUD(t *testing.T) {
	st := storetest.New(t)
	key, _ := secretbox.NewKey()
	box, _ := secretbox.New(key)
	svc, err := NewService(ServiceOptions{Store: st, Box: box})
	if err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	const token = "123456789:AAHsecretBotTokenValue_xyz-0123456789"
	n, err := svc.Create(ctx, NotificationInput{
		Name: "Phone", Kind: Telegram, Settings: ServiceSettings{ChatID: "42"}, Secrets: &NotificationSecretsInput{Token: ptr(token)},
		Tags: []string{" Kids ", "kids", "Crime"},
	})
	if err != nil {
		t.Fatal(err)
	}
	if !n.SecretsSet.Token || n.SecretsSet.WebhookURL || len(n.Events) != len(DefaultEvents) || len(n.EventMatrix) != len(EventKinds) ||
		strings.Join(n.Tags, ",") != "crime,kids" || !n.Enabled {
		t.Errorf("created = %+v", n)
	}
	row, _ := st.Read().GetNotification(ctx, n.ID)
	if strings.Contains(string(row.Secrets), "AAHsecret") || strings.Contains(row.Settings, "AAHsecret") {
		t.Error("token stored in clear")
	}
	if _, err := svc.open(row.Secrets, n.ID+1); err == nil {
		t.Error("secrets must be bound to their row")
	}

	// Update without secrets keeps the token; events in canonical order.
	n, err = svc.Update(ctx, n.ID, NotificationInput{Name: "Phone", Kind: Telegram, Settings: ServiceSettings{ChatID: "43"},
		Events: []EventKind{EventDelete, EventGrab}})
	if err != nil || !n.SecretsSet.Token || n.Settings.ChatID != "43" || len(n.Events) != 2 || n.Events[0] != EventGrab {
		t.Fatalf("update = %+v, %v", n, err)
	}
	// Another kind: the old secrets do not carry over (and Discord needs its URL).
	if _, err := svc.Update(ctx, n.ID, NotificationInput{Name: "Phone", Kind: Discord}); !errors.Is(err, ErrInvalid) || !strings.Contains(err.Error(), "Discord webhook URL") {
		t.Errorf("kind change: %v", err)
	}
	for _, bad := range []NotificationInput{
		{Name: "", Kind: Slack},
		{Name: "x", Kind: "sms"},
		{Name: "x", Kind: Telegram, Settings: ServiceSettings{ChatID: "1"}, Secrets: &NotificationSecretsInput{Token: ptr(token)}, Events: []EventKind{"test"}},
		{Name: "x", Kind: Webhook, Settings: ServiceSettings{URL: "http://a:b@host"}},
		{Name: "x", Kind: Telegram, Settings: ServiceSettings{ChatID: "1"}, Secrets: &NotificationSecretsInput{Token: ptr(token)}, Tags: []string{strings.Repeat("t", 65)}},
	} {
		if _, err := svc.Create(ctx, bad); !errors.Is(err, ErrInvalid) {
			t.Errorf("Create(%+v) = %v", bad, err)
		}
	}
	if _, err := svc.Create(ctx, NotificationInput{Name: "Phone", Kind: Slack, Secrets: &NotificationSecretsInput{WebhookURL: ptr("https://hooks.slack.com/services/T/B/X")}}); !errors.Is(err, store.ErrConflict) {
		t.Errorf("duplicate: %v", err)
	}
	// TestConfig reuses the stored token; a malformed setting is refused first.
	res, err := svc.TestConfig(ctx, n.ID, NotificationInput{Name: "Phone", Kind: Telegram, Settings: ServiceSettings{ChatID: "43", ServerURL: "http://127.0.0.1:1"}})
	if err != nil || res.OK || strings.Contains(res.Message, "AAHsecret") || !strings.Contains(res.Message, "Telegram") {
		t.Errorf("test config = %+v, %v", res, err)
	}
	if _, err := svc.SetAppURL(ctx, "ftp://x"); !errors.Is(err, ErrInvalid) {
		t.Errorf("app url: %v", err)
	}
	if u, err := svc.SetAppURL(ctx, "https://gonema.lan/base/"); err != nil || u != "https://gonema.lan/base" || svc.AppURL(ctx) != u {
		t.Errorf("app url = %q, %v", u, err)
	}
	if err := svc.Delete(ctx, n.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := svc.Get(ctx, n.ID); !errors.Is(err, ErrNotFound) || !errors.Is(err, store.ErrNotFound) {
		t.Errorf("get deleted: %v", err)
	}
}
