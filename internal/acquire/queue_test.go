package acquire

import (
	"errors"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/fs0ciety7000/gonema/internal/acquire/acquiretest"
	"github.com/fs0ciety7000/gonema/internal/decision"
	"github.com/fs0ciety7000/gonema/internal/jobs"
	"github.com/fs0ciety7000/gonema/internal/library"
	"github.com/fs0ciety7000/gonema/internal/store"
	"github.com/fs0ciety7000/gonema/internal/store/db"
)

func dbMonitored(id int64, v bool) db.SetMovieMonitoredParams {
	return db.SetMovieMonitoredParams{ID: id, Monitored: v}
}

// grabbedEnv is an env with one movie whose Bluray release was grabbed.
func grabbedEnv(t *testing.T) (*env, *acquiretest.QBittorrent, db.Movie, db.Download) {
	e := newEnv(t)
	movie := e.addMovie()
	slow, _ := standardTrackers(t)
	e.addTracker("Slow", slow)
	qb := acquiretest.NewQBittorrent(t, qbUser, qbPass)
	e.addQB(qb)
	if _, err := e.svc.SearchMovie(e.ctx, movie.ID); err != nil {
		t.Fatal(err)
	}
	return e, qb, movie, e.downloads(movie.ID)[0]
}

func TestQueueActions(t *testing.T) {
	e, qb, movie, d := grabbedEnv(t)
	hash := deref(d.ClientRef)

	item, err := e.svc.SetPaused(e.ctx, d.ID, true)
	if err != nil || item.Status != "paused" || !qb.Paused(hash) {
		t.Fatalf("pause = %+v, %v", item, err)
	}
	if item, err = e.svc.SetPaused(e.ctx, d.ID, false); err != nil || item.Status != "downloading" || qb.Paused(hash) {
		t.Fatalf("resume = %+v, %v", item, err)
	}
	if _, err := e.svc.SetPaused(e.ctx, 999, true); !errors.Is(err, store.ErrNotFound) {
		t.Errorf("pause unknown = %v", err)
	}

	// Remove with blocklist and retry; data kept by default.
	if err := e.svc.RemoveFromQueue(e.ctx, d.ID, RemoveOptions{Blocklist: true, Retry: true}); err != nil {
		t.Fatal(err)
	}
	if del, files := qb.Deleted(hash); !del || files {
		t.Errorf("client remove = %v, files %v", del, files)
	}
	dls := e.downloads(movie.ID)
	if len(dls) != 2 || dls[0].Status != "removed" || dls[1].ReleaseTitle != relWeb || deref(dls[1].RetryOf) != d.ID {
		t.Fatalf("downloads = %+v", dls)
	}
	if ev := historyEvents(e.history(movie.ID)); !slices.Equal(ev, []string{"grabbed", "download_removed", "blocklisted", "retry_scheduled", "grabbed"}) {
		t.Errorf("history = %v", ev)
	}
	if err := e.svc.RemoveFromQueue(e.ctx, d.ID, RemoveOptions{}); !errors.Is(err, store.ErrNotFound) {
		t.Errorf("remove twice = %v", err)
	}
	// deleteData is passed through only when asked.
	if err := e.svc.RemoveFromQueue(e.ctx, dls[1].ID, RemoveOptions{DeleteData: true}); err != nil {
		t.Fatal(err)
	}
	if _, files := qb.Deleted(deref(dls[1].ClientRef)); !files {
		t.Error("deleteData not passed to the client")
	}
	if q, _ := e.svc.Queue(e.ctx); len(q) != 0 {
		t.Errorf("queue = %+v", q)
	}
}

func TestTrackerClientUnreachable(t *testing.T) {
	e, qb, _, d := grabbedEnv(t)
	qb.Srv.Close()
	if n, err := e.svc.TrackOnce(e.ctx); err != nil || n != 1 {
		t.Fatalf("TrackOnce = %d, %v", n, err)
	}
	got, _ := e.st.Read().GetDownload(e.ctx, d.ID)
	if got.Status != "queued" {
		t.Errorf("status = %s, want unchanged", got.Status)
	}
	q, _ := e.svc.Queue(e.ctx)
	if len(q) != 1 || len(q[0].Messages) == 0 || !strings.Contains(q[0].Messages[len(q[0].Messages)-1], "cannot be reached") {
		t.Errorf("queue = %+v", q)
	}
	c, _ := e.svc.GetDownloadClient(e.ctx, deref(d.ClientID))
	if c.Health.FailureCount != 1 || c.Health.LastError == "" {
		t.Errorf("client health = %+v", c.Health)
	}
	assertNoSecrets(t, e.logs.String())
}

func TestTrackerVanishedDownload(t *testing.T) {
	e, qb, movie, d := grabbedEnv(t)
	qb.Drop(deref(d.ClientRef))
	for range 3 { // within the grace period: kept
		if _, err := e.svc.TrackOnce(e.ctx); err != nil {
			t.Fatal(err)
		}
	}
	if got, _ := e.st.Read().GetDownload(e.ctx, d.ID); got.Status != "queued" {
		t.Fatalf("status within grace = %s", got.Status)
	}
	e.clk.Advance(3 * time.Minute)
	if _, err := e.svc.TrackOnce(e.ctx); err != nil {
		t.Fatal(err)
	}
	got, _ := e.st.Read().GetDownload(e.ctx, d.ID)
	if got.Status != "removed" || !strings.Contains(got.StatusMessages, "disappeared from qBittorrent") {
		t.Errorf("download = %s %s", got.Status, got.StatusMessages)
	}
	if ev := historyEvents(e.history(movie.ID)); !slices.Equal(ev, []string{"grabbed", "download_removed"}) {
		t.Errorf("history = %v", ev)
	}
}

func TestTrackerProgressEvents(t *testing.T) {
	e, qb, _, d := grabbedEnv(t)
	sub := e.bus.Subscribe(16, nil)
	defer sub.Close()
	qb.Set(deref(d.ClientRef), "downloading", 0.5, "")
	if _, err := e.svc.TrackOnce(e.ctx); err != nil {
		t.Fatal(err)
	}
	var progress *QueueItem
	for len(sub.C()) > 0 {
		ev := <-sub.C()
		if it, ok := ev.Data.(QueueItem); ok && ev.Type == "download.progress" {
			progress = &it
		}
	}
	if progress == nil || progress.Progress != 0.5 || progress.Status != "downloading" || progress.EtaSeconds == nil {
		t.Fatalf("progress event = %+v", progress)
	}
	// Progress is in memory; the status change was persisted.
	got, _ := e.st.Read().GetDownload(e.ctx, d.ID)
	if got.Status != "downloading" {
		t.Errorf("persisted status = %s", got.Status)
	}
}

func TestBlocklistService(t *testing.T) {
	e := newEnv(t)
	movie := e.addMovie()
	g, err := e.svc.BlockGroup(e.ctx, "EVO", movie.ID, "")
	if err != nil || g.Kind != "group" || deref(g.MovieID) != movie.ID {
		t.Fatalf("block group = %+v, %v", g, err)
	}
	global, err := e.svc.BlockGroup(e.ctx, "YIFY", 0, "bad encodes")
	if err != nil || global.MovieID != nil {
		t.Fatalf("global = %+v, %v", global, err)
	}
	if _, err := e.svc.BlockGroup(e.ctx, " ", 0, ""); !errors.Is(err, ErrInvalid) {
		t.Errorf("empty group = %v", err)
	}
	if _, err := e.svc.BlockGroup(e.ctx, "X", 999, ""); !errors.Is(err, store.ErrNotFound) {
		t.Errorf("unknown movie = %v", err)
	}
	all, _ := e.svc.ListBlocklist(e.ctx, BlocklistFilter{})
	mine, _ := e.svc.ListBlocklist(e.ctx, BlocklistFilter{MovieID: movie.ID, Kind: "group"})
	if len(all) != 2 || all[0].ID != global.ID || len(mine) != 1 {
		t.Errorf("all = %+v, mine = %+v", all, mine)
	}
	if page, _ := e.svc.ListBlocklist(e.ctx, BlocklistFilter{Before: global.ID, Limit: 10}); len(page) != 1 || page[0].ID != g.ID {
		t.Errorf("page = %+v", page)
	}
	// Both apply to decisions on the movie.
	mc, err := e.svc.loadMovie(e.ctx, movie.ID, nil, decisionOptions())
	if err != nil || len(mc.cfg.Blocklist) != 2 {
		t.Fatalf("decision blocklist = %+v, %v", mc.cfg.Blocklist, err)
	}
	if n, err := e.svc.ClearMovieBlocklist(e.ctx, movie.ID); err != nil || n != 1 {
		t.Errorf("clear = %d, %v", n, err)
	}
	if err := e.svc.DeleteBlocklist(e.ctx, global.ID); err != nil {
		t.Fatal(err)
	}
	if err := e.svc.DeleteBlocklist(e.ctx, global.ID); !errors.Is(err, store.ErrNotFound) {
		t.Errorf("second delete = %v", err)
	}
	if ev := historyEvents(e.history(movie.ID)); !slices.Equal(ev, []string{"blocklisted"}) {
		t.Errorf("history = %v", ev)
	}
}

func decisionOptions() decision.Options { return decision.Options{} }

// TestTrackerDoesNotResurrect: a tracker update computed before the user
// removed the download must not bring it back.
func TestTrackerDoesNotResurrect(t *testing.T) {
	e, _, _, d := grabbedEnv(t)
	stale := d
	if err := e.svc.RemoveFromQueue(e.ctx, d.ID, RemoveOptions{}); err != nil {
		t.Fatal(err)
	}
	e.svc.persistState(e.ctx, &stale, "downloading", e.svc.track.get(d.ID), "", true)
	if got, _ := e.st.Read().GetDownload(e.ctx, d.ID); got.Status != "removed" {
		t.Errorf("status = %s, want removed", got.Status)
	}
}

type recordingRunner struct {
	handled   []string
	recurring map[string]time.Duration
}

func (r *recordingRunner) Handle(kind string, _ jobs.Handler, _ jobs.HandlerOptions) {
	r.handled = append(r.handled, kind)
}

func (r *recordingRunner) Every(kind string, interval, _ time.Duration, _ any) {
	r.recurring[kind] = interval
}

func TestRegisterJobs(t *testing.T) {
	e := newEnv(t)
	if JobMovieSearch != library.JobMovieSearch {
		t.Fatalf("search-on-add job kind %q is not handled (%q)", library.JobMovieSearch, JobMovieSearch)
	}
	if err := setSetting(e, SettingRSSInterval, 5); err != nil {
		t.Fatal(err)
	}
	r := &recordingRunner{recurring: map[string]time.Duration{}}
	e.svc.RegisterJobs(e.ctx, r)
	slices.Sort(r.handled)
	if !slices.Equal(r.handled, []string{JobDownloadRetry, JobMovieSearch, JobSearchMissing, JobRSSSync}) {
		t.Errorf("handled = %v", r.handled)
	}
	if r.recurring[JobRSSSync] != 10*time.Minute || r.recurring[JobSearchMissing] != 24*time.Hour {
		t.Errorf("recurring = %v (RSS interval clamped to 10 min)", r.recurring)
	}
}
