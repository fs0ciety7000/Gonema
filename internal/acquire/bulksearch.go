package acquire

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"sync"
	"time"

	"github.com/fs0ciety7000/gonema/internal/events"
	"github.com/fs0ciety7000/gonema/internal/jobs"
	"github.com/fs0ciety7000/gonema/internal/search"
	"github.com/fs0ciety7000/gonema/internal/store"
	"github.com/fs0ciety7000/gonema/internal/store/db"
)

// Bulk search: automatic searches for a list of movies ("Search the 12
// released" on the dashboard, the Wanted lists, a selection of the library),
// run as one persistent job. Movies are searched one at a time, spaced out,
// and the job waits while every indexer is paused by its circuit breaker:
// the per-indexer rate limits of the orchestrator then never see a burst.
// Grabs are written to history by the grab itself; progress streams as
// bulksearch.progress events; the job can be cancelled (jobs.Runner.Cancel).

// JobBulkSearch is the job kind of bulk searches.
const JobBulkSearch = "movies.bulk_search"

// Bulk search events.
const (
	EventBulkSearchProgress events.Type = "bulksearch.progress"
	EventBulkSearchFinished events.Type = "bulksearch.finished"
)

// Bulk search limits.
const (
	// MaxBulkSearch bounds the movies of one bulk search.
	MaxBulkSearch = 5000
	// bulkSpacing is the default pause between two movie searches.
	bulkSpacing = 3 * time.Second
	// maxBreakerWait bounds one wait for an indexer to come back.
	maxBreakerWait = 30 * time.Minute
	// bulkSearchTimeout bounds one run of the job.
	bulkSearchTimeout = 12 * time.Hour
)

// Bulk search states.
const (
	BulkRunning   = "running"
	BulkWaiting   = "waiting" // every indexer is paused by its circuit breaker
	BulkFinished  = "finished"
	BulkCancelled = "cancelled"
	BulkStopped   = "stopped" // could not go on (no indexer...): Reason says why
)

// BulkSearchPayload is the payload of JobBulkSearch.
type BulkSearchPayload struct {
	MovieIDs []int64 `json:"movieIds"`
	Label    string  `json:"label,omitempty"` // what was asked: "missing_released", "cutoff_unmet" or "selection"
}

// BulkSearchProgress is the state of a bulk search (bulksearch.* events and
// GET /bulk-searches/{jobId}).
type BulkSearchProgress struct {
	JobID     int64  `json:"jobId"`
	Label     string `json:"label" doc:"missing_released, cutoff_unmet or selection"`
	State     string `json:"state" enum:"pending,running,waiting,finished,cancelled,stopped"`
	Total     int    `json:"total"`
	Done      int    `json:"done" doc:"Movies handled, whatever the outcome"`
	Grabbed   int    `json:"grabbed" doc:"Movies for which a release was grabbed (written to history)"`
	NoResult  int    `json:"noResult" doc:"Searched, nothing acceptable to grab"`
	Skipped   int    `json:"skipped" doc:"Not searched: unmonitored, not available yet, removed, or already searched by this job before a restart"`
	Failed    int    `json:"failed" doc:"The search failed (see the logs)"`
	CurrentID int64  `json:"currentMovieId,omitempty" doc:"Movie being searched"`
	// WaitingUntil is set while every indexer is paused by its breaker.
	WaitingUntil *time.Time `json:"waitingUntil,omitempty"`
	Reason       string     `json:"reason,omitempty" doc:"Why the search stopped or waits, plain language"`
	UpdatedAt    time.Time  `json:"updatedAt"`
}

// bulkState keeps the progress of the bulk searches of this process.
type bulkState struct {
	mu      sync.Mutex
	m       map[int64]*BulkSearchProgress
	spacing time.Duration // default bulkSpacing; tests set 0
	sleep   func(ctx context.Context, d time.Duration) error
}

func (b *bulkState) init() {
	if b.m == nil {
		b.m = map[int64]*BulkSearchProgress{}
		b.spacing = bulkSpacing
		b.sleep = sleepCtx
	}
}

func sleepCtx(ctx context.Context, d time.Duration) error {
	if d <= 0 {
		return ctx.Err()
	}
	t := time.NewTimer(d)
	defer t.Stop()
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-t.C:
		return nil
	}
}

// ErrBulkSearchActive is wrapped by StartBulkSearch when a bulk search is
// already pending or running.
var ErrBulkSearchActive = errors.New("acquire: a bulk search is already running")

// StartBulkSearch queues a bulk search of the movies (order kept, duplicates
// dropped). It returns the job; a conflict error names the active one.
func (s *Service) StartBulkSearch(ctx context.Context, ids []int64, label string) (db.Job, int, error) {
	if s.jobs == nil || !s.jobs.Has(JobBulkSearch) {
		return db.Job{}, 0, newError(ErrUnavailable, nil, "Bulk searches are not available on this server.")
	}
	seen := make(map[int64]bool, len(ids))
	uniq := make([]int64, 0, len(ids))
	for _, id := range ids {
		if id > 0 && !seen[id] {
			seen[id] = true
			uniq = append(uniq, id)
		}
	}
	switch {
	case len(uniq) == 0:
		return db.Job{}, 0, invalid("There is no movie to search.")
	case len(uniq) > MaxBulkSearch:
		return db.Job{}, 0, invalid("Search at most %d movies at once.", MaxBulkSearch)
	}
	rows, err := s.st.Read().ListIndexers(ctx)
	if err != nil {
		return db.Job{}, 0, store.MapError(fmt.Errorf("acquire: list indexers: %w", err))
	}
	if autoIndexers(rows) == 0 {
		return db.Job{}, 0, newError(ErrUnavailable, nil, "No indexer is enabled for automatic search: add or enable one in Settings > Indexers.")
	}
	job, created, err := s.jobs.Enqueue(ctx, JobBulkSearch, BulkSearchPayload{MovieIDs: uniq, Label: label},
		jobs.EnqueueOptions{DedupeKey: JobBulkSearch, Priority: -1, MaxAttempts: 3})
	if err != nil {
		return db.Job{}, 0, fmt.Errorf("acquire: enqueue bulk search: %w", err)
	}
	if !created {
		return db.Job{}, 0, &Error{Kinds: []error{ErrInvalid, store.ErrConflict, ErrBulkSearchActive},
			Msg: "A bulk search is already running: wait for it to finish or cancel it (GET /jobs lists it)."}
	}
	p := &BulkSearchProgress{JobID: job.ID, Label: label, State: "pending", Total: len(uniq), UpdatedAt: s.now().UTC()}
	s.bulk.mu.Lock()
	s.bulk.init()
	s.bulk.m[job.ID] = p
	s.bulk.mu.Unlock()
	s.searchLog.Info("bulk search queued", slog.Int64("job_id", job.ID), slog.Int("movies", len(uniq)), slog.String("label", label))
	return job, len(uniq), nil
}

func autoIndexers(rows []db.Indexer) int {
	n := 0
	for i := range rows {
		if rows[i].EnableAutoSearch {
			n++
		}
	}
	return n
}

// BulkSearch returns the progress of a bulk search of this process; ok is
// false when it is unknown here (finished before a restart, or another job).
func (s *Service) BulkSearch(jobID int64) (BulkSearchProgress, bool) {
	s.bulk.mu.Lock()
	defer s.bulk.mu.Unlock()
	s.bulk.init()
	p, ok := s.bulk.m[jobID]
	if !ok {
		return BulkSearchProgress{}, false
	}
	return *p, true
}

// update changes the progress of a job and publishes it.
func (s *Service) bulkUpdate(jobID int64, fn func(p *BulkSearchProgress)) BulkSearchProgress {
	s.bulk.mu.Lock()
	s.bulk.init()
	p, ok := s.bulk.m[jobID]
	if !ok {
		p = &BulkSearchProgress{JobID: jobID}
		s.bulk.m[jobID] = p
	}
	fn(p)
	p.UpdatedAt = s.now().UTC()
	out := *p
	s.bulk.mu.Unlock()
	typ := EventBulkSearchProgress
	switch out.State {
	case BulkFinished, BulkCancelled, BulkStopped:
		typ = EventBulkSearchFinished
	}
	s.publish(events.Event{Type: typ, Data: out})
	return out
}

func (s *Service) handleBulkSearch(ctx context.Context, job db.Job) error {
	var p BulkSearchPayload
	if err := json.Unmarshal([]byte(job.Payload), &p); err != nil {
		return fmt.Errorf("acquire: invalid %s payload", job.Kind)
	}
	return s.runBulkSearch(ctx, job, p)
}

// runBulkSearch searches the movies one at a time. A movie already searched
// since the job was created (a restart resumed the job) is skipped.
func (s *Service) runBulkSearch(ctx context.Context, job db.Job, p BulkSearchPayload) error {
	s.bulkUpdate(job.ID, func(pr *BulkSearchProgress) {
		*pr = BulkSearchProgress{JobID: job.ID, Label: p.Label, State: BulkRunning, Total: len(p.MovieIDs)}
	})
	log := s.searchLog.With(slog.Int64("job_id", job.ID))
	log.Info("bulk search started", slog.Int("movies", len(p.MovieIDs)))
	var last time.Time
	for _, id := range p.MovieIDs {
		if err := ctx.Err(); err != nil {
			return s.bulkEnd(ctx, job.ID, err)
		}
		m, err := s.st.Read().GetMovie(ctx, id)
		switch {
		case errors.Is(store.MapError(err), store.ErrNotFound):
			s.bulkUpdate(job.ID, func(pr *BulkSearchProgress) { pr.Done++; pr.Skipped++ })
			continue
		case err != nil:
			return store.MapError(fmt.Errorf("acquire: movie %d: %w", id, err))
		case m.LastSearchedAt != nil && *m.LastSearchedAt >= job.CreatedAt:
			s.bulkUpdate(job.ID, func(pr *BulkSearchProgress) { pr.Done++; pr.Skipped++ })
			continue
		}
		if err := s.bulkThrottle(ctx, job.ID, last); err != nil {
			if errors.Is(err, errNoIndexer) {
				s.bulkUpdate(job.ID, func(pr *BulkSearchProgress) {
					pr.State, pr.CurrentID, pr.Reason = BulkStopped, 0, "No indexer is enabled for automatic search any more."
				})
				log.Warn("bulk search stopped: no indexer enabled for automatic search")
				return nil
			}
			return s.bulkEnd(ctx, job.ID, err)
		}
		s.bulkUpdate(job.ID, func(pr *BulkSearchProgress) {
			pr.State, pr.CurrentID, pr.WaitingUntil, pr.Reason = BulkRunning, id, nil, ""
		})
		out, err := s.SearchMovie(ctx, id)
		last = s.now()
		if err != nil && ctx.Err() != nil {
			return s.bulkEnd(ctx, job.ID, ctx.Err())
		}
		s.bulkUpdate(job.ID, func(pr *BulkSearchProgress) {
			pr.Done++
			switch {
			case err != nil && (errors.Is(err, store.ErrNotFound) || errors.Is(err, ErrInvalid)):
				pr.Skipped++
			case err != nil:
				pr.Failed++
				log.Warn("bulk search: movie search failed", slog.Int64("movie_id", id), slog.Any("error", err))
			case out.Grabbed != nil:
				pr.Grabbed++
			case out.Searched:
				pr.NoResult++
			default:
				pr.Skipped++
			}
		})
	}
	final := s.bulkUpdate(job.ID, func(pr *BulkSearchProgress) { pr.State, pr.CurrentID = BulkFinished, 0 })
	log.Info("bulk search finished", slog.Int("total", final.Total), slog.Int("grabbed", final.Grabbed),
		slog.Int("no_result", final.NoResult), slog.Int("skipped", final.Skipped), slog.Int("failed", final.Failed))
	return nil
}

// bulkEnd records the end of an interrupted bulk search: cancelled by the
// user, or stopped by shutdown (the job resumes at the next start).
func (s *Service) bulkEnd(ctx context.Context, jobID int64, err error) error {
	if errors.Is(context.Cause(ctx), jobs.ErrCancelled) {
		s.bulkUpdate(jobID, func(pr *BulkSearchProgress) {
			pr.State, pr.CurrentID, pr.WaitingUntil, pr.Reason = BulkCancelled, 0, nil, "Cancelled by the user."
		})
		s.searchLog.Info("bulk search cancelled", slog.Int64("job_id", jobID))
	}
	return err
}

var errNoIndexer = errors.New("acquire: no indexer for automatic search")

// bulkThrottle waits before the next search: the spacing after the previous
// one, and while every automatic search indexer is paused by its circuit
// breaker (rate limited or failing), until the first one is back.
func (s *Service) bulkThrottle(ctx context.Context, jobID int64, last time.Time) error {
	s.bulk.mu.Lock()
	s.bulk.init()
	spacing, sleep := s.bulk.spacing, s.bulk.sleep
	s.bulk.mu.Unlock()
	if !last.IsZero() {
		if err := sleep(ctx, spacing-s.now().Sub(last)); err != nil {
			return err
		}
	}
	for {
		rows, err := s.st.Read().ListIndexers(ctx)
		if err != nil {
			return store.MapError(fmt.Errorf("acquire: list indexers: %w", err))
		}
		var healths []search.Health
		for i := range rows {
			if rows[i].EnableAutoSearch {
				h, _ := s.orch.Health(rows[i].ID)
				healths = append(healths, h)
			}
		}
		if len(healths) == 0 {
			return errNoIndexer
		}
		now := s.now()
		until, wait := breakerWait(healths, now)
		if !wait {
			return nil
		}
		w := min(until.Sub(now), maxBreakerWait)
		at := now.Add(w).UTC()
		s.bulkUpdate(jobID, func(pr *BulkSearchProgress) {
			pr.State, pr.CurrentID, pr.WaitingUntil = BulkWaiting, 0, &at
			pr.Reason = "Every indexer is paused (rate limit or repeated errors): the search goes on when the first one is back."
		})
		if err := sleep(ctx, w); err != nil {
			return err
		}
	}
}

// breakerWait reports whether every indexer is paused by its breaker at now,
// and until when the first one is (pure).
func breakerWait(hs []search.Health, now time.Time) (until time.Time, wait bool) {
	for _, h := range hs {
		if h.State != search.Open || !h.DisabledUntil.After(now) {
			return time.Time{}, false
		}
		if until.IsZero() || h.DisabledUntil.Before(until) {
			until = h.DisabledUntil
		}
	}
	return until, len(hs) > 0
}
