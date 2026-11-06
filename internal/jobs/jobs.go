// Package jobs runs the background jobs persisted in the store's job queue.
//
// A Runner has N workers that claim due jobs (highest priority first), run the
// handler registered for the job kind and record the outcome: done, or failed
// with the store's exponential backoff until max_attempts is reached. Jobs left
// running by a previous process are requeued at Start, so nothing is lost on a
// crash or restart.
//
// A scheduler enqueues recurring jobs (Every) with a dedupe key, so a slow job
// never piles up behind itself. Their next due time is kept in the settings
// table: a restart does not rerun a daily job that ran an hour ago.
//
// Every run is logged with slog and published on the event bus (job.started,
// job.completed, job.failed).
package jobs

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"math/rand/v2"
	"runtime/debug"
	"sync"
	"time"

	"github.com/fs0ciety7000/gonema/internal/events"
	"github.com/fs0ciety7000/gonema/internal/store"
	"github.com/fs0ciety7000/gonema/internal/store/db"
)

// Handler runs one job. A nil error marks the job done; an error schedules a
// retry (or fails the job once its attempts are exhausted). The context is
// canceled when the runner stops or the handler's timeout elapses.
type Handler func(ctx context.Context, job db.Job) error

// HandlerOptions configures a job kind.
type HandlerOptions struct {
	Timeout time.Duration // per run; default 10 minutes
}

// EnqueueOptions configures one enqueued job. Zero values select defaults.
type EnqueueOptions struct {
	DedupeKey   string    // when an active job holds it, nothing is enqueued
	Priority    int64     // higher runs first
	RunAt       time.Time // default now
	MaxAttempts int64     // default 5
}

// Options configures a Runner.
type Options struct {
	Workers      int           // default 4
	PollInterval time.Duration // idle polling period; default 1 s (enqueues also wake workers)
	Logger       *slog.Logger  // default discards
	Bus          *events.Bus   // optional
	Now          func() time.Time
	// Rand returns a float in [0, 1) used for schedule jitter; default math/rand/v2.
	Rand func() float64
}

// ErrNoHandler is the error recorded for a job whose kind has no handler.
var ErrNoHandler = errors.New("jobs: no handler registered for this kind")

// ErrCancelled is the cause of the context of a job cancelled by Cancel.
// Handlers that loop should return when their context is done.
var ErrCancelled = errors.New("jobs: cancelled by the user")

// ErrNotCancellable is returned by Cancel for a job that is not pending or
// running (finished, or running in another process).
var ErrNotCancellable = errors.New("jobs: the job is not pending or running")

type handler struct {
	fn      Handler
	timeout time.Duration
}

type schedule struct {
	kind     string
	interval func() time.Duration // read before each due time is computed
	jitter   time.Duration
	payload  any
	resched  chan struct{} // Reschedule signal, buffered 1
}

// every returns the current interval, at least a second (a zero or negative
// interval from a misconfigured source must not spin).
func (s *schedule) every() time.Duration { return max(s.interval(), time.Second) }

// Runner executes jobs. Register handlers and schedules before Start.
type Runner struct {
	st      *store.Store
	log     *slog.Logger
	bus     *events.Bus
	workers int
	poll    time.Duration
	now     func() time.Time
	rand    func() float64

	mu        sync.RWMutex
	handlers  map[string]handler
	schedules []schedule
	started   bool
	running   map[int64]context.CancelCauseFunc // jobs running in this process

	wake   chan struct{}
	cancel context.CancelFunc
	wg     sync.WaitGroup
}

// New returns a runner over the store's job queue.
func New(st *store.Store, opts Options) *Runner {
	r := &Runner{
		st:       st,
		log:      opts.Logger,
		bus:      opts.Bus,
		workers:  opts.Workers,
		poll:     opts.PollInterval,
		now:      opts.Now,
		rand:     opts.Rand,
		handlers: make(map[string]handler),
		running:  make(map[int64]context.CancelCauseFunc),
		wake:     make(chan struct{}, 1),
	}
	if r.log == nil {
		r.log = slog.New(slog.DiscardHandler)
	}
	if r.workers <= 0 {
		r.workers = 4
	}
	if r.poll <= 0 {
		r.poll = time.Second
	}
	if r.now == nil {
		r.now = time.Now
	}
	if r.rand == nil {
		r.rand = rand.Float64
	}
	return r
}

// Handle registers the handler of a job kind. It panics if called twice for the
// same kind or after Start (programming errors).
func (r *Runner) Handle(kind string, fn Handler, opts HandlerOptions) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.started {
		panic("jobs: Handle after Start")
	}
	if _, dup := r.handlers[kind]; dup {
		panic("jobs: duplicate handler for " + kind)
	}
	if opts.Timeout <= 0 {
		opts.Timeout = 10 * time.Minute
	}
	r.handlers[kind] = handler{fn: fn, timeout: opts.Timeout}
}

// Has reports whether a handler is registered for kind.
func (r *Runner) Has(kind string) bool {
	r.mu.RLock()
	defer r.mu.RUnlock()
	_, ok := r.handlers[kind]
	return ok
}

// Every schedules kind to be enqueued every interval, plus a random delay up to
// jitter (spreads load, avoids synchronized bursts). Call before Start.
func (r *Runner) Every(kind string, interval, jitter time.Duration, payload any) {
	r.EveryFunc(kind, func() time.Duration { return interval }, jitter, payload)
}

// EveryFunc is Every with an interval read from a setting: interval is called
// each time the next due time is computed, and again by Reschedule. It must be
// fast and safe for concurrent use. Call before Start.
func (r *Runner) EveryFunc(kind string, interval func() time.Duration, jitter time.Duration, payload any) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.started {
		panic("jobs: Every after Start")
	}
	r.schedules = append(r.schedules, schedule{
		kind: kind, interval: interval, jitter: jitter, payload: payload, resched: make(chan struct{}, 1),
	})
}

// Reschedule applies a changed interval of a recurring job at once: the next
// run becomes the last run of this process plus the new interval (or, before
// any run, the earlier of the stored due time and now plus the interval). It
// reports whether kind has a schedule. Safe to call before Start (no effect
// then: the interval is read when the scheduler starts).
func (r *Runner) Reschedule(kind string) bool {
	r.mu.RLock()
	defer r.mu.RUnlock()
	for i := range r.schedules {
		if s := &r.schedules[i]; s.kind == kind {
			select {
			case s.resched <- struct{}{}:
			default:
			}
			return true
		}
	}
	return false
}

// Enqueue adds a job. created is false when an active job already holds the
// dedupe key. Workers are woken immediately.
func (r *Runner) Enqueue(ctx context.Context, kind string, payload any, opts EnqueueOptions) (job db.Job, created bool, err error) {
	raw := []byte("{}")
	if payload != nil {
		if raw, err = json.Marshal(payload); err != nil {
			return db.Job{}, false, fmt.Errorf("jobs: encode %s payload: %w", kind, err)
		}
	}
	now := r.now()
	p := db.EnqueueJobParams{
		Kind: kind, Payload: string(raw), Priority: opts.Priority,
		RunAt: store.Millis(now), MaxAttempts: opts.MaxAttempts, CreatedAt: store.Millis(now),
	}
	if !opts.RunAt.IsZero() {
		p.RunAt = store.Millis(opts.RunAt)
	}
	if p.MaxAttempts <= 0 {
		p.MaxAttempts = 5
	}
	if opts.DedupeKey != "" {
		p.DedupeKey = &opts.DedupeKey
	}
	job, created, err = r.st.EnqueueJob(ctx, p)
	if err != nil {
		return db.Job{}, false, fmt.Errorf("jobs: %w", err)
	}
	if created {
		r.Wake()
	}
	return job, created, nil
}

// Wake makes an idle worker look for due jobs now.
func (r *Runner) Wake() {
	select {
	case r.wake <- struct{}{}:
	default:
	}
}

// Start requeues jobs left running by a previous process, then starts the
// workers and the scheduler. They run until Stop or until ctx is canceled.
func (r *Runner) Start(ctx context.Context) error {
	r.mu.Lock()
	if r.started {
		r.mu.Unlock()
		return errors.New("jobs: already started")
	}
	r.started = true
	r.mu.Unlock()

	n, err := r.st.ResetRunningJobs(ctx)
	if err != nil {
		return fmt.Errorf("jobs: %w", err)
	}
	if n > 0 {
		r.log.Info("requeued jobs interrupted by the previous shutdown", slog.Int64("count", n))
	}
	ctx, r.cancel = context.WithCancel(ctx)
	for i := range r.workers {
		r.wg.Go(func() { r.worker(ctx, i) })
	}
	for _, s := range r.schedules {
		r.wg.Go(func() { r.scheduler(ctx, s) })
	}
	return nil
}

// Stop cancels running handlers and waits for the workers to return, or for ctx
// to end. Interrupted jobs stay running in the store and are requeued at the
// next Start.
func (r *Runner) Stop(ctx context.Context) error {
	if r.cancel == nil {
		return nil
	}
	r.cancel()
	done := make(chan struct{})
	go func() {
		r.wg.Wait()
		close(done)
	}()
	select {
	case <-done:
		return nil
	case <-ctx.Done():
		return fmt.Errorf("jobs: workers did not stop in time: %w", ctx.Err())
	}
}

func (r *Runner) worker(ctx context.Context, n int) {
	timer := time.NewTimer(0)
	defer timer.Stop()
	for {
		// Drain the queue, then sleep until woken or polled.
		for ctx.Err() == nil {
			job, ok, err := r.st.ClaimJob(ctx, r.now())
			if err != nil {
				if ctx.Err() == nil {
					r.log.Error("jobs: claim", slog.Int("worker", n), slog.Any("error", err))
				}
				break
			}
			if !ok {
				break
			}
			r.run(ctx, job)
		}
		timer.Reset(r.poll)
		select {
		case <-ctx.Done():
			return
		case <-r.wake:
		case <-timer.C:
		}
	}
}

// run executes one claimed job and records its outcome.
func (r *Runner) run(ctx context.Context, job db.Job) {
	r.mu.RLock()
	h, ok := r.handlers[job.Kind]
	r.mu.RUnlock()

	log := r.log.With(slog.Int64("job_id", job.ID), slog.String("kind", job.Kind), slog.Int64("attempt", job.Attempts))
	start := r.now()
	r.publish(events.JobStarted, job, 0, "")
	log.Debug("job started")

	jctx, jcancel := context.WithCancelCause(ctx)
	r.mu.Lock()
	r.running[job.ID] = jcancel
	r.mu.Unlock()
	defer jcancel(nil)

	var err error
	if !ok {
		err = ErrNoHandler
	} else {
		err = r.call(jctx, h, job)
	}
	// Leave the running set before recording the outcome: once the job reads
	// as finished, Cancel must answer ErrNotCancellable, not cancel a context
	// that no longer runs anything.
	r.mu.Lock()
	delete(r.running, job.ID)
	r.mu.Unlock()
	elapsed := r.now().Sub(start)

	if errors.Is(context.Cause(jctx), ErrCancelled) && ctx.Err() == nil {
		octx, cancel := context.WithTimeout(context.WithoutCancel(ctx), 10*time.Second)
		defer cancel()
		if _, cerr := r.st.Write().CancelRunningJob(octx, db.CancelRunningJobParams{ID: job.ID, Now: store.Millis(r.now())}); cerr != nil {
			log.Error("jobs: record cancellation", slog.Any("error", cerr))
		}
		log.Info("job cancelled", slog.Duration("elapsed", elapsed))
		job.Status = "cancelled"
		r.publish(events.JobCancelled, job, elapsed, "")
		return
	}

	if err != nil && ctx.Err() != nil {
		// Shutdown: leave the job running; Start requeues it next time.
		log.Info("job interrupted by shutdown, will resume at next start", slog.Duration("elapsed", elapsed))
		return
	}
	// The outcome must be recorded even if ctx was canceled meanwhile.
	octx, cancel := context.WithTimeout(context.WithoutCancel(ctx), 10*time.Second)
	defer cancel()
	if err == nil {
		if cerr := r.st.CompleteJob(octx, job.ID, r.now()); cerr != nil {
			log.Error("jobs: record completion", slog.Any("error", cerr))
		}
		log.Info("job done", slog.Duration("elapsed", elapsed))
		r.publish(events.JobCompleted, job, elapsed, "")
		return
	}
	updated, ferr := r.st.FailJob(octx, job.ID, job.Attempts, r.now(), err.Error())
	if ferr != nil {
		log.Error("jobs: record failure", slog.Any("error", ferr), slog.Any("job_error", err))
		return
	}
	if updated.Status == "failed" {
		log.Error("job failed, no attempt left", slog.Duration("elapsed", elapsed), slog.Any("error", err))
	} else {
		log.Warn("job failed, will retry", slog.Duration("elapsed", elapsed),
			slog.Time("retry_at", store.FromMillis(updated.RunAt)), slog.Any("error", err))
	}
	job.Status = updated.Status
	r.publish(events.JobFailed, job, elapsed, err.Error())
}

// call runs the handler with its timeout and turns a panic into an error.
func (r *Runner) call(ctx context.Context, h handler, job db.Job) (err error) {
	hctx, cancel := context.WithTimeout(ctx, h.timeout)
	defer cancel()
	defer func() {
		if p := recover(); p != nil {
			r.log.Error("jobs: handler panicked", slog.String("kind", job.Kind), slog.Any("panic", p),
				slog.String("stack", string(debug.Stack())))
			err = fmt.Errorf("jobs: handler panicked: %v", p)
		}
	}()
	return h.fn(hctx, job)
}

// JobEvent is the data of job.* events.
type JobEvent struct {
	ID        int64  `json:"id"`
	Kind      string `json:"kind"`
	Status    string `json:"status"`
	Attempt   int64  `json:"attempt"`
	ElapsedMS int64  `json:"elapsedMs,omitempty"`
	Error     string `json:"error,omitempty"`
}

func (r *Runner) publish(t events.Type, job db.Job, elapsed time.Duration, msg string) {
	if r.bus == nil {
		return
	}
	status := job.Status
	switch t {
	case events.JobCompleted:
		status = "done"
	case events.JobStarted:
		status = "running"
	case events.JobCancelled:
		status = "cancelled"
	}
	r.bus.Publish(events.Event{Type: t, Data: JobEvent{
		ID: job.ID, Kind: job.Kind, Status: status, Attempt: job.Attempts,
		ElapsedMS: elapsed.Milliseconds(), Error: msg,
	}})
}

// scheduleKey is the settings key holding the next due time of a recurring job.
func scheduleKey(kind string) string { return "jobs.next_run." + kind }

func (r *Runner) scheduler(ctx context.Context, s schedule) {
	next, err := store.GetSetting[int64](ctx, r.st, scheduleKey(s.kind))
	switch {
	case err == nil:
	case errors.Is(err, store.ErrNotFound):
		// First run ever: soon, with jitter, not in the startup path.
		next = store.Millis(r.now().Add(r.jitter(min(s.jitter, time.Minute)) + 30*time.Second))
	default:
		r.log.Error("jobs: read schedule", slog.String("kind", s.kind), slog.Any("error", err))
		next = store.Millis(r.now().Add(s.every()))
	}
	timer := time.NewTimer(0)
	defer timer.Stop()
	var last time.Time // last enqueue by this process
	for {
		wait := store.FromMillis(next).Sub(r.now())
		if wait <= 0 {
			last = r.now()
			_, created, err := r.Enqueue(ctx, s.kind, s.payload, EnqueueOptions{DedupeKey: "recurring:" + s.kind})
			if err != nil {
				if ctx.Err() != nil {
					return
				}
				r.log.Error("jobs: enqueue recurring job", slog.String("kind", s.kind), slog.Any("error", err))
			} else if !created {
				r.log.Debug("recurring job still pending, not enqueued again", slog.String("kind", s.kind))
			}
			next = store.Millis(r.now().Add(s.every() + r.jitter(s.jitter)))
			r.saveNext(ctx, s.kind, next)
			continue
		}
		timer.Reset(wait)
		select {
		case <-ctx.Done():
			return
		case <-timer.C:
		case <-s.resched:
			iv := s.every()
			if !last.IsZero() {
				next = store.Millis(last.Add(iv))
			} else {
				next = min(next, store.Millis(r.now().Add(iv)))
			}
			r.log.Info("recurring job rescheduled", slog.String("kind", s.kind), slog.Duration("interval", iv),
				slog.Time("next_run", store.FromMillis(next)))
			r.saveNext(ctx, s.kind, next)
		}
	}
}

func (r *Runner) saveNext(ctx context.Context, kind string, next int64) {
	if err := store.SetSetting(ctx, r.st, scheduleKey(kind), next); err != nil && ctx.Err() == nil {
		r.log.Error("jobs: save schedule", slog.String("kind", kind), slog.Any("error", err))
	}
}

func (r *Runner) jitter(upTo time.Duration) time.Duration {
	if upTo <= 0 {
		return 0
	}
	return time.Duration(r.rand() * float64(upTo))
}

// NextRun returns the next due time of a recurring job (Every, EveryFunc), as
// last saved by its scheduler; ok is false before the first schedule.
func (r *Runner) NextRun(ctx context.Context, kind string) (time.Time, bool) {
	ms, err := store.GetSetting[int64](ctx, r.st, scheduleKey(kind))
	if err != nil {
		if !errors.Is(err, store.ErrNotFound) {
			r.log.Warn("jobs: read schedule", slog.String("kind", kind), slog.Any("error", err))
		}
		return time.Time{}, false
	}
	return store.FromMillis(ms), true
}

// Cancel cancels a job: a pending job is marked cancelled at once; a job
// running in this process has its context canceled (cause ErrCancelled) and is
// marked cancelled when its handler returns. It returns ErrNotCancellable for a
// finished job and store.ErrNotFound for an unknown one.
func (r *Runner) Cancel(ctx context.Context, id int64) error {
	n, err := r.st.Write().CancelJob(ctx, db.CancelJobParams{ID: id, Now: store.Millis(r.now())})
	if err != nil {
		return store.MapError(fmt.Errorf("jobs: cancel %d: %w", id, err))
	}
	if n > 0 {
		job, err := r.st.Read().GetJob(ctx, id)
		if err == nil {
			r.log.Info("pending job cancelled", slog.Int64("job_id", id), slog.String("kind", job.Kind))
			r.publish(events.JobCancelled, job, 0, "")
		}
		return nil
	}
	r.mu.RLock()
	cancel, ok := r.running[id]
	r.mu.RUnlock()
	if ok {
		cancel(ErrCancelled)
		return nil
	}
	if _, err := r.st.Read().GetJob(ctx, id); err != nil {
		return store.MapError(fmt.Errorf("jobs: cancel %d: %w", id, err))
	}
	return ErrNotCancellable
}

// ListRecent returns the most recent jobs, newest first.
func (r *Runner) ListRecent(ctx context.Context, limit int64) ([]db.Job, error) {
	rows, err := r.st.Read().ListRecentJobs(ctx, db.ListRecentJobsParams{BeforeID: 1<<63 - 1, Limit: limit})
	if err != nil {
		return nil, store.MapError(fmt.Errorf("jobs: list recent: %w", err))
	}
	return rows, nil
}
