package jobs

import (
	"context"
	"errors"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/fs0ciety7000/gonema/internal/events"
	"github.com/fs0ciety7000/gonema/internal/store"
	"github.com/fs0ciety7000/gonema/internal/store/db"
	"github.com/fs0ciety7000/gonema/internal/store/storetest"
)

// clock is a manual clock shared by the runner and the test.
type clock struct{ ms atomic.Int64 }

func newClock() *clock {
	c := &clock{}
	c.ms.Store(time.Date(2026, 11, 5, 12, 0, 0, 0, time.UTC).UnixMilli())
	return c
}
func (c *clock) Now() time.Time          { return time.UnixMilli(c.ms.Load()).UTC() }
func (c *clock) Advance(d time.Duration) { c.ms.Add(d.Milliseconds()) }

func newRunner(t *testing.T, st *store.Store, c *clock, bus *events.Bus) *Runner {
	t.Helper()
	return New(st, Options{Workers: 2, PollInterval: 10 * time.Millisecond, Now: c.Now, Bus: bus, Rand: func() float64 { return 0.5 }})
}

func start(t *testing.T, r *Runner) {
	t.Helper()
	if err := r.Start(context.Background()); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		if err := r.Stop(ctx); err != nil {
			t.Error(err)
		}
	})
}

// waitJob polls the job until cond holds.
func waitJob(t *testing.T, st *store.Store, id int64, cond func(db.Job) bool) db.Job {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for {
		j, err := st.Read().GetJob(context.Background(), id)
		if err != nil {
			t.Fatal(err)
		}
		if cond(j) {
			return j
		}
		if time.Now().After(deadline) {
			t.Fatalf("job %d stuck: %+v", id, j)
		}
		time.Sleep(5 * time.Millisecond)
	}
}

func TestRunSuccess(t *testing.T) {
	st := storetest.New(t)
	c := newClock()
	bus := events.New(events.Options{})
	sub := bus.Subscribe(16, nil)
	r := newRunner(t, st, c, bus)
	var got atomic.Value
	r.Handle("echo", func(_ context.Context, j db.Job) error {
		got.Store(j.Payload)
		return nil
	}, HandlerOptions{})
	start(t, r)

	job, created, err := r.Enqueue(context.Background(), "echo", map[string]int{"movieId": 3}, EnqueueOptions{DedupeKey: "echo:3"})
	if err != nil || !created {
		t.Fatalf("Enqueue = %v, %v", created, err)
	}
	waitJob(t, st, job.ID, func(j db.Job) bool { return j.Status == "done" })
	if p := got.Load(); p != `{"movieId":3}` {
		t.Errorf("payload = %v", p)
	}
	var types []events.Type
	for len(types) < 2 {
		select {
		case e := <-sub.C():
			types = append(types, e.Type)
		case <-time.After(5 * time.Second):
			t.Fatalf("events = %v", types)
		}
	}
	if types[0] != events.JobStarted || types[1] != events.JobCompleted {
		t.Errorf("events = %v", types)
	}
}

func TestEnqueueDedupe(t *testing.T) {
	st := storetest.New(t)
	r := newRunner(t, st, newClock(), nil) // not started: jobs stay pending
	ctx := context.Background()
	if _, created, err := r.Enqueue(ctx, "k", nil, EnqueueOptions{DedupeKey: "same"}); err != nil || !created {
		t.Fatalf("first enqueue = %v, %v", created, err)
	}
	if _, created, err := r.Enqueue(ctx, "k", nil, EnqueueOptions{DedupeKey: "same"}); err != nil || created {
		t.Fatalf("second enqueue = %v, %v; want deduplicated", created, err)
	}
}

func TestRetryWithBackoffThenFail(t *testing.T) {
	st := storetest.New(t)
	c := newClock()
	r := newRunner(t, st, c, nil)
	var calls atomic.Int32
	r.Handle("flaky", func(context.Context, db.Job) error {
		calls.Add(1)
		return errors.New("indexer unreachable")
	}, HandlerOptions{})
	start(t, r)

	job, _, err := r.Enqueue(context.Background(), "flaky", nil, EnqueueOptions{MaxAttempts: 3})
	if err != nil {
		t.Fatal(err)
	}
	// Attempt 1 fails: back to pending, retry 30 s later (store.Backoff).
	j := waitJob(t, st, job.ID, func(j db.Job) bool { return j.Attempts == 1 && j.Status == "pending" })
	if want := store.Millis(c.Now().Add(store.Backoff(1))); j.RunAt != want {
		t.Errorf("retry at %d, want %d (now + %s)", j.RunAt, want, store.Backoff(1))
	}
	if j.LastError == nil || *j.LastError != "indexer unreachable" {
		t.Errorf("last_error = %v", j.LastError)
	}
	// Not due yet: no new attempt.
	time.Sleep(50 * time.Millisecond)
	if n := calls.Load(); n != 1 {
		t.Fatalf("calls before backoff elapsed = %d, want 1", n)
	}
	c.Advance(store.Backoff(1))
	r.Wake()
	waitJob(t, st, job.ID, func(j db.Job) bool { return j.Attempts == 2 && j.Status == "pending" })
	c.Advance(store.Backoff(2))
	r.Wake()
	j = waitJob(t, st, job.ID, func(j db.Job) bool { return j.Status == "failed" })
	if j.Attempts != 3 || calls.Load() != 3 {
		t.Errorf("attempts = %d, calls = %d; want 3, 3", j.Attempts, calls.Load())
	}
}

func TestPanicAndUnknownKind(t *testing.T) {
	st := storetest.New(t)
	r := newRunner(t, st, newClock(), nil)
	r.Handle("boom", func(context.Context, db.Job) error { panic("bug") }, HandlerOptions{})
	start(t, r)
	ctx := context.Background()
	boom, _, _ := r.Enqueue(ctx, "boom", nil, EnqueueOptions{MaxAttempts: 1})
	unknown, _, _ := r.Enqueue(ctx, "nobody", nil, EnqueueOptions{MaxAttempts: 1})
	j := waitJob(t, st, boom.ID, func(j db.Job) bool { return j.Status == "failed" })
	if j.LastError == nil || *j.LastError != "jobs: handler panicked: bug" {
		t.Errorf("panic job last_error = %v", j.LastError)
	}
	j = waitJob(t, st, unknown.ID, func(j db.Job) bool { return j.Status == "failed" })
	if j.LastError == nil || *j.LastError != ErrNoHandler.Error() {
		t.Errorf("unknown job last_error = %v", j.LastError)
	}
}

// Stop cancels running handlers; the interrupted job is resumed by the next
// runner (crash recovery), without consuming a failure.
func TestShutdownAndResume(t *testing.T) {
	st := storetest.New(t)
	c := newClock()
	r1 := newRunner(t, st, c, nil)
	started := make(chan struct{})
	var once sync.Once
	r1.Handle("long", func(ctx context.Context, _ db.Job) error {
		once.Do(func() { close(started) })
		<-ctx.Done()
		return ctx.Err()
	}, HandlerOptions{})
	if err := r1.Start(context.Background()); err != nil {
		t.Fatal(err)
	}
	job, _, err := r1.Enqueue(context.Background(), "long", nil, EnqueueOptions{})
	if err != nil {
		t.Fatal(err)
	}
	select {
	case <-started:
	case <-time.After(5 * time.Second):
		t.Fatal("handler did not start")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if err := r1.Stop(ctx); err != nil {
		t.Fatal(err)
	}
	if j, _ := st.Read().GetJob(ctx, job.ID); j.Status != "running" || j.LastError != nil {
		t.Fatalf("after shutdown job = %+v, want running without error", j)
	}

	r2 := newRunner(t, st, c, nil)
	r2.Handle("long", func(context.Context, db.Job) error { return nil }, HandlerOptions{})
	start(t, r2)
	j := waitJob(t, st, job.ID, func(j db.Job) bool { return j.Status == "done" })
	if j.Attempts != 2 {
		t.Errorf("attempts = %d, want 2", j.Attempts)
	}
}

func TestHandlerTimeout(t *testing.T) {
	st := storetest.New(t)
	r := newRunner(t, st, newClock(), nil)
	r.Handle("slow", func(ctx context.Context, _ db.Job) error {
		<-ctx.Done()
		return ctx.Err()
	}, HandlerOptions{Timeout: 20 * time.Millisecond})
	start(t, r)
	job, _, _ := r.Enqueue(context.Background(), "slow", nil, EnqueueOptions{MaxAttempts: 1})
	j := waitJob(t, st, job.ID, func(j db.Job) bool { return j.Status == "failed" })
	if j.LastError == nil || *j.LastError != context.DeadlineExceeded.Error() {
		t.Errorf("last_error = %v", j.LastError)
	}
}

func TestScheduler(t *testing.T) {
	st := storetest.New(t)
	c := newClock()
	ctx := context.Background()
	// Due in the past: enqueued at once, next due time saved.
	if err := store.SetSetting(ctx, st, scheduleKey("tick"), store.Millis(c.Now().Add(-time.Minute))); err != nil {
		t.Fatal(err)
	}
	r := newRunner(t, st, c, nil)
	var runs atomic.Int32
	r.Handle("tick", func(context.Context, db.Job) error { runs.Add(1); return nil }, HandlerOptions{})
	r.Every("tick", time.Hour, 10*time.Minute, nil)
	start(t, r)

	deadline := time.Now().Add(5 * time.Second)
	for runs.Load() == 0 {
		if time.Now().After(deadline) {
			t.Fatal("recurring job never ran")
		}
		time.Sleep(5 * time.Millisecond)
	}
	var next int64
	for {
		var err error
		next, err = store.GetSetting[int64](ctx, st, scheduleKey("tick"))
		if err != nil {
			t.Fatal(err)
		}
		if next > store.Millis(c.Now()) || time.Now().After(deadline) {
			break
		}
		time.Sleep(5 * time.Millisecond)
	}
	// interval + jitter × 0.5
	if want := store.Millis(c.Now().Add(time.Hour + 5*time.Minute)); next != want {
		t.Errorf("next run = %v, want %v", store.FromMillis(next), store.FromMillis(want))
	}
}

func TestListRecent(t *testing.T) {
	st := storetest.New(t)
	r := newRunner(t, st, newClock(), nil)
	ctx := context.Background()
	for _, k := range []string{"a", "b", "c"} {
		if _, _, err := r.Enqueue(ctx, k, nil, EnqueueOptions{}); err != nil {
			t.Fatal(err)
		}
	}
	jobs, err := r.ListRecent(ctx, 2)
	if err != nil {
		t.Fatal(err)
	}
	if len(jobs) != 2 || jobs[0].Kind != "c" || jobs[1].Kind != "b" {
		t.Errorf("recent = %+v", jobs)
	}
}

// waitNextRun polls the stored due time of kind until it equals want.
func waitNextRun(t *testing.T, st *store.Store, kind string, want time.Time) {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for {
		next, err := store.GetSetting[int64](context.Background(), st, scheduleKey(kind))
		if err == nil && next == store.Millis(want) {
			return
		}
		if time.Now().After(deadline) {
			t.Fatalf("next run of %s = %v (err %v), want %v", kind, store.FromMillis(next), err, want)
		}
		time.Sleep(5 * time.Millisecond)
	}
}

// TestReschedule: a changed interval applies at once, from the last run, or
// from now when the job has not run in this process.
func TestReschedule(t *testing.T) {
	st := storetest.New(t)
	c := newClock()
	ctx := context.Background()
	// "idle" is due in 10 h; "busy" is due now.
	if err := store.SetSetting(ctx, st, scheduleKey("idle"), store.Millis(c.Now().Add(10*time.Hour))); err != nil {
		t.Fatal(err)
	}
	if err := store.SetSetting(ctx, st, scheduleKey("busy"), store.Millis(c.Now().Add(-time.Minute))); err != nil {
		t.Fatal(err)
	}
	var idleEvery, busyEvery atomic.Int64
	idleEvery.Store(int64(10 * time.Hour))
	busyEvery.Store(int64(24 * time.Hour))
	r := newRunner(t, st, c, nil)
	ran := make(chan struct{}, 4)
	r.Handle("busy", func(context.Context, db.Job) error { ran <- struct{}{}; return nil }, HandlerOptions{})
	r.Handle("idle", func(context.Context, db.Job) error { return nil }, HandlerOptions{})
	r.EveryFunc("idle", func() time.Duration { return time.Duration(idleEvery.Load()) }, 0, nil)
	r.EveryFunc("busy", func() time.Duration { return time.Duration(busyEvery.Load()) }, 0, nil)
	if r.Reschedule("unknown") {
		t.Error("Reschedule of an unknown kind reported a schedule")
	}
	start(t, r)
	select {
	case <-ran:
	case <-time.After(5 * time.Second):
		t.Fatal("busy never ran")
	}
	start0 := c.Now()
	waitNextRun(t, st, "busy", start0.Add(24*time.Hour))

	idleEvery.Store(int64(time.Hour))
	if !r.Reschedule("idle") {
		t.Fatal("Reschedule(idle) = false")
	}
	waitNextRun(t, st, "idle", start0.Add(time.Hour))

	busyEvery.Store(int64(48 * time.Hour))
	r.Reschedule("busy")
	waitNextRun(t, st, "busy", start0.Add(48*time.Hour))
}

func TestCancel(t *testing.T) {
	st := storetest.New(t)
	c := newClock()
	bus := events.New(events.Options{})
	sub := bus.Subscribe(64, func(e *events.Event) bool { return e.Type == events.JobCancelled })
	r := newRunner(t, st, c, bus)
	started := make(chan struct{})
	var sawCause atomic.Bool
	r.Handle("long", func(ctx context.Context, _ db.Job) error {
		close(started)
		<-ctx.Done()
		sawCause.Store(errors.Is(context.Cause(ctx), ErrCancelled))
		return ctx.Err()
	}, HandlerOptions{})
	ctx := context.Background()

	// A pending job (due later) is cancelled at once.
	later, _, err := r.Enqueue(ctx, "long", nil, EnqueueOptions{RunAt: c.Now().Add(time.Hour)})
	if err != nil {
		t.Fatal(err)
	}
	start(t, r)
	if err := r.Cancel(ctx, later.ID); err != nil {
		t.Fatal(err)
	}
	if j, _ := st.Read().GetJob(ctx, later.ID); j.Status != "cancelled" || j.FinishedAt == nil {
		t.Errorf("pending job after cancel = %+v", j)
	}

	// A running job is interrupted and recorded as cancelled, not failed.
	job, _, err := r.Enqueue(ctx, "long", nil, EnqueueOptions{})
	if err != nil {
		t.Fatal(err)
	}
	select {
	case <-started:
	case <-time.After(5 * time.Second):
		t.Fatal("job did not start")
	}
	if err := r.Cancel(ctx, job.ID); err != nil {
		t.Fatal(err)
	}
	j := waitJob(t, st, job.ID, func(j db.Job) bool { return j.Status != "running" })
	if j.Status != "cancelled" || j.LastError != nil || !sawCause.Load() {
		t.Errorf("running job after cancel = %+v (cause seen %v)", j, sawCause.Load())
	}
	for range 2 {
		select {
		case e := <-sub.C():
			if d, ok := e.Data.(JobEvent); !ok || d.Status != "cancelled" {
				t.Errorf("event data = %+v", e.Data)
			}
		case <-time.After(5 * time.Second):
			t.Fatal("missing job.cancelled event")
		}
	}

	// Finished and unknown jobs.
	if err := r.Cancel(ctx, job.ID); !errors.Is(err, ErrNotCancellable) {
		t.Errorf("cancel finished job = %v", err)
	}
	if err := r.Cancel(ctx, 9999); !errors.Is(err, store.ErrNotFound) {
		t.Errorf("cancel unknown job = %v", err)
	}
}
