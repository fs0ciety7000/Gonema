package acquire

import (
	"context"
	"errors"
	"slices"
	"sync"
	"testing"
	"time"

	"github.com/fs0ciety7000/gonema/internal/acquire/acquiretest"
	"github.com/fs0ciety7000/gonema/internal/events"
	"github.com/fs0ciety7000/gonema/internal/jobs"
	"github.com/fs0ciety7000/gonema/internal/search"
	"github.com/fs0ciety7000/gonema/internal/store"
	"github.com/fs0ciety7000/gonema/internal/store/db"
)

func TestBreakerWait(t *testing.T) {
	now := time.Date(2026, 11, 6, 12, 0, 0, 0, time.UTC)
	open := func(d time.Duration) search.Health {
		return search.Health{State: search.Open, DisabledUntil: now.Add(d)}
	}
	for _, tc := range []struct {
		name  string
		hs    []search.Health
		wait  bool
		until time.Time
	}{
		{"none", nil, false, time.Time{}},
		{"one closed", []search.Health{{}}, false, time.Time{}},
		{"one open, one closed", []search.Health{open(time.Hour), {}}, false, time.Time{}},
		{"all open: earliest", []search.Health{open(time.Hour), open(10 * time.Minute)}, true, now.Add(10 * time.Minute)},
		{"open but expired", []search.Health{open(-time.Minute)}, false, time.Time{}},
		{"half-open probes", []search.Health{{State: search.HalfOpen}}, false, time.Time{}},
	} {
		until, wait := breakerWait(tc.hs, now)
		if wait != tc.wait || !until.Equal(tc.until) {
			t.Errorf("%s: breakerWait = %v, %v; want %v, %v", tc.name, until, wait, tc.until, tc.wait)
		}
	}
}

// fakeQueue is a JobQueue that records enqueued jobs and deduplicates keys.
type fakeQueue struct {
	mu   sync.Mutex
	jobs []db.Job
	keys map[string]bool
}

func (q *fakeQueue) Enqueue(_ context.Context, kind string, _ any, opts jobs.EnqueueOptions) (db.Job, bool, error) {
	q.mu.Lock()
	defer q.mu.Unlock()
	if q.keys == nil {
		q.keys = map[string]bool{}
	}
	if opts.DedupeKey != "" && q.keys[opts.DedupeKey] {
		return db.Job{}, false, nil
	}
	q.keys[opts.DedupeKey] = true
	j := db.Job{ID: int64(len(q.jobs) + 1), Kind: kind}
	q.jobs = append(q.jobs, j)
	return j, true, nil
}

func (q *fakeQueue) Has(kind string) bool { return kind == JobBulkSearch }

func TestStartBulkSearch(t *testing.T) {
	e := newEnv(t)
	movie := e.addMovie()
	// No job queue: refused.
	if _, _, err := e.svc.StartBulkSearch(e.ctx, []int64{movie.ID}, "selection"); !errors.Is(err, ErrUnavailable) {
		t.Errorf("without jobs = %v", err)
	}
	q := &fakeQueue{}
	e.svc.jobs = q
	// No indexer for automatic search: refused before queueing.
	if _, _, err := e.svc.StartBulkSearch(e.ctx, []int64{movie.ID}, "selection"); !errors.Is(err, ErrUnavailable) {
		t.Errorf("without indexer = %v", err)
	}
	slow, _ := standardTrackers(t)
	e.addTracker("Slow", slow)
	for _, ids := range [][]int64{nil, {0, -1}} {
		if _, _, err := e.svc.StartBulkSearch(e.ctx, ids, "selection"); !errors.Is(err, ErrInvalid) {
			t.Errorf("ids %v = %v", ids, err)
		}
	}
	job, n, err := e.svc.StartBulkSearch(e.ctx, []int64{movie.ID, movie.ID, 999}, "selection")
	if err != nil || n != 2 || job.ID != 1 {
		t.Fatalf("start = %+v, %d, %v", job, n, err)
	}
	if p, ok := e.svc.BulkSearch(job.ID); !ok || p.State != "pending" || p.Total != 2 {
		t.Errorf("progress = %+v, %v", p, ok)
	}
	if _, _, err := e.svc.StartBulkSearch(e.ctx, []int64{movie.ID}, "selection"); !errors.Is(err, store.ErrConflict) || !errors.Is(err, ErrBulkSearchActive) {
		t.Errorf("second start = %v, want a conflict", err)
	}
}

func TestRunBulkSearch(t *testing.T) {
	e := newEnv(t)
	movie := e.addMovie()
	slow, _ := standardTrackers(t)
	e.addTracker("Slow", slow)
	qb := acquiretest.NewQBittorrent(t, qbUser, qbPass)
	e.addQB(qb)
	e.svc.bulk.init()
	e.svc.bulk.spacing = 0
	sub := e.bus.Subscribe(256, func(ev *events.Event) bool {
		return ev.Type == EventBulkSearchProgress || ev.Type == EventBulkSearchFinished
	})
	defer sub.Close()

	job := db.Job{ID: 7, Kind: JobBulkSearch, CreatedAt: store.Millis(e.clk.Now())}
	if err := e.svc.runBulkSearch(e.ctx, job, BulkSearchPayload{MovieIDs: []int64{movie.ID, 999}, Label: "missing_released"}); err != nil {
		t.Fatal(err)
	}
	p, ok := e.svc.BulkSearch(7)
	if !ok || p.State != BulkFinished || p.Total != 2 || p.Done != 2 || p.Grabbed != 1 || p.Skipped != 1 || p.Failed != 0 {
		t.Fatalf("progress = %+v", p)
	}
	if got := historyEvents(e.history(movie.ID)); !slices.Equal(got, []string{"grabbed"}) {
		t.Errorf("history = %v", got)
	}
	var last events.Event
	n := 0
	for len(sub.C()) > 0 {
		last = <-sub.C()
		n++
	}
	if n < 3 || last.Type != EventBulkSearchFinished {
		t.Errorf("%d events, last %v", n, last.Type)
	}

	// Resumed after a restart: the movie searched since the job was created
	// is not searched again.
	if err := e.svc.runBulkSearch(e.ctx, job, BulkSearchPayload{MovieIDs: []int64{movie.ID}}); err != nil {
		t.Fatal(err)
	}
	if p, _ := e.svc.BulkSearch(7); p.Skipped != 1 || p.Grabbed != 0 {
		t.Errorf("resumed progress = %+v", p)
	}
	if n := len(e.downloads(movie.ID)); n != 1 {
		t.Errorf("%d downloads, want 1", n)
	}
}

func TestRunBulkSearchCancelled(t *testing.T) {
	e := newEnv(t)
	movie := e.addMovie()
	slow, _ := standardTrackers(t)
	e.addTracker("Slow", slow)
	e.svc.bulk.init()
	ctx, cancel := context.WithCancelCause(e.ctx)
	cancel(jobs.ErrCancelled)
	err := e.svc.runBulkSearch(ctx, db.Job{ID: 3}, BulkSearchPayload{MovieIDs: []int64{movie.ID}})
	if !errors.Is(err, context.Canceled) {
		t.Errorf("err = %v", err)
	}
	if p, _ := e.svc.BulkSearch(3); p.State != BulkCancelled || p.Done != 0 {
		t.Errorf("progress = %+v", p)
	}
}
