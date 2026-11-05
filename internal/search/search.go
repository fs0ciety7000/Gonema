package search

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"sync"
	"time"

	"github.com/fs0ciety7000/gonema/internal/indexer"
	"github.com/fs0ciety7000/gonema/internal/ratelimit"
)

// Defaults of Options.
const (
	DefaultTimeout   = 15 * time.Second
	DefaultRateLimit = 1.0 // requests per second per indexer
	DefaultBurst     = 3
)

// ErrCircuitOpen is the failure reported for an indexer skipped by its breaker.
var ErrCircuitOpen = errors.New("the indexer is paused after repeated failures")

// CircuitOpenError tells that an indexer was skipped; it matches ErrCircuitOpen.
type CircuitOpenError struct {
	Indexer string
	Until   time.Time // zero while a probe request is in flight
}

func (e *CircuitOpenError) Error() string {
	if e.Until.IsZero() {
		return fmt.Sprintf("%s: %s, a test request is in progress", e.Indexer, ErrCircuitOpen)
	}
	return fmt.Sprintf("%s: %s, next try at %s", e.Indexer, ErrCircuitOpen, e.Until.UTC().Format(time.RFC3339))
}

// Is reports whether target is ErrCircuitOpen.
func (e *CircuitOpenError) Is(target error) bool { return target == ErrCircuitOpen }

// Options configures an Orchestrator. Zero fields select the defaults.
type Options struct {
	// Timeout bounds each indexer call (rate-limit wait included); default 15 s.
	Timeout time.Duration
	// RateLimit and Burst set the token bucket of each indexer; default 1
	// request/s with bursts of 3.
	RateLimit float64
	Burst     int
	Breaker   BreakerPolicy
	Now       func() time.Time // breaker and RSS clock; default time.Now
	Logger    *slog.Logger     // failures at warn level; default discards
}

// Orchestrator fans searches out to indexers with per-indexer timeout, rate
// limit and circuit breaker. Breakers and limiters are keyed by indexer id and
// live as long as the Orchestrator. It is safe for concurrent use.
type Orchestrator struct {
	timeout time.Duration
	rate    float64
	burst   int
	policy  BreakerPolicy
	now     func() time.Time
	log     *slog.Logger

	mu       sync.Mutex
	breakers map[int64]*Breaker
	limiters map[int64]*ratelimit.Limiter
}

// New returns an Orchestrator.
func New(o Options) *Orchestrator {
	s := &Orchestrator{
		timeout:  o.Timeout,
		rate:     o.RateLimit,
		burst:    o.Burst,
		policy:   o.Breaker.withDefaults(),
		now:      o.Now,
		log:      o.Logger,
		breakers: make(map[int64]*Breaker),
		limiters: make(map[int64]*ratelimit.Limiter),
	}
	if s.timeout <= 0 {
		s.timeout = DefaultTimeout
	}
	if s.rate <= 0 {
		s.rate = DefaultRateLimit
	}
	if s.burst <= 0 {
		s.burst = DefaultBurst
	}
	if s.now == nil {
		s.now = time.Now
	}
	if s.log == nil {
		s.log = slog.New(slog.DiscardHandler)
	}
	return s
}

// Health returns the breaker state of an indexer; ok is false when the indexer
// was never called nor restored.
func (s *Orchestrator) Health(id int64) (h Health, ok bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	b, ok := s.breakers[id]
	if !ok {
		return Health{}, false
	}
	return b.Health(), true
}

// Restore loads a persisted breaker state, e.g. at startup.
func (s *Orchestrator) Restore(id int64, h Health) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.breakers[id] = RestoreBreaker(s.policy, h)
}

// Reset closes the breaker of an indexer, e.g. after a successful manual test
// or a change of its settings.
func (s *Orchestrator) Reset(id int64) {
	s.mu.Lock()
	defer s.mu.Unlock()
	delete(s.breakers, id)
}

func (s *Orchestrator) breaker(id int64) *Breaker {
	b, ok := s.breakers[id]
	if !ok {
		b = NewBreaker(s.policy)
		s.breakers[id] = b
	}
	return b
}

func (s *Orchestrator) limiter(id int64) *ratelimit.Limiter {
	s.mu.Lock()
	defer s.mu.Unlock()
	l, ok := s.limiters[id]
	if !ok {
		l = ratelimit.New(s.rate, s.burst)
		s.limiters[id] = l
	}
	return l
}

// Event is sent on the channel returned by Search: *ResultBatch, *IndexerDone,
// *IndexerFailed and, last, *Finished.
type Event interface{ event() }

// ResultBatch carries the releases of one indexer not seen before in this
// search, and the releases already sent by another indexer (Duplicates).
type ResultBatch struct {
	Indexer    indexer.Info
	Releases   []indexer.Release
	Duplicates []Duplicate
}

// Duplicate tells that a release already sent (same Key) is also offered by
// another indexer.
type Duplicate struct {
	Key    string
	Source indexer.Source
}

// IndexerDone tells that an indexer answered. Count includes duplicates.
type IndexerDone struct {
	Indexer  indexer.Info
	Count    int
	Duration time.Duration
}

// IndexerFailed tells that an indexer failed or was skipped by its breaker.
// WillRetryAt is the end of the breaker's open period, zero when the next search
// will try the indexer again. Releases of pages fetched before the failure are
// sent in a ResultBatch first.
type IndexerFailed struct {
	Indexer     indexer.Info
	Err         error
	WillRetryAt time.Time
	Duration    time.Duration
}

// Finished is the last event of a search that was not canceled. Results holds
// every release once, merged across indexers: a release offered by several
// indexers is represented by the copy of the preferred indexer (lowest priority
// value, then most seeders, then first seen), with all of them in Sources.
type Finished struct {
	Results   []indexer.Release
	Succeeded int
	Failed    int
	Duration  time.Duration
}

func (*ResultBatch) event()   {}
func (*IndexerDone) event()   {}
func (*IndexerFailed) event() {}
func (*Finished) event()      {}

// Key identifies a release across indexers: its info hash when known, else its
// GUID.
func Key(r *indexer.Release) string {
	if r.InfoHash != "" {
		return "btih:" + r.InfoHash
	}
	return "guid:" + r.GUID
}

// outcome is the result of one indexer call.
type outcome struct {
	info        indexer.Info
	releases    []indexer.Release
	err         error
	willRetryAt time.Time
	duration    time.Duration
}

// Search queries every indexer in parallel and streams events as they come: an
// indexer's batch is sent as soon as it answers, whatever the others do. The
// channel is always closed: after Finished, or soon after ctx ends (then
// without Finished). Canceling ctx cancels the requests in flight. The caller
// must read the channel until it is closed or cancel ctx.
func (s *Orchestrator) Search(ctx context.Context, q indexer.Query, ixs []indexer.Indexer) <-chan Event {
	out := make(chan Event, 2*len(ixs)+1)
	results := make(chan outcome, len(ixs)) // never blocks a worker
	start := time.Now()
	for _, ix := range ixs {
		go func() {
			results <- s.call(ctx, ix.Info(), func(ctx context.Context) ([]indexer.Release, error) {
				return ix.Search(ctx, q)
			})
		}()
	}
	go s.collect(ctx, len(ixs), results, out, start)
	return out
}

func (s *Orchestrator) collect(ctx context.Context, n int, results <-chan outcome, out chan<- Event, start time.Time) {
	defer close(out)
	send := func(ev Event) bool {
		select {
		case out <- ev:
			return true
		case <-ctx.Done():
			return false
		}
	}
	d := newDeduper()
	fin := &Finished{}
	for range n {
		var oc outcome
		select {
		case oc = <-results:
		case <-ctx.Done():
			return
		}
		if ctx.Err() != nil {
			return
		}
		if b := d.add(oc.info, oc.releases); len(b.Releases) > 0 || len(b.Duplicates) > 0 {
			if !send(b) {
				return
			}
		}
		var ev Event
		if oc.err != nil {
			fin.Failed++
			ev = &IndexerFailed{Indexer: oc.info, Err: oc.err, WillRetryAt: oc.willRetryAt, Duration: oc.duration}
		} else {
			fin.Succeeded++
			ev = &IndexerDone{Indexer: oc.info, Count: len(oc.releases), Duration: oc.duration}
		}
		if !send(ev) {
			return
		}
	}
	fin.Results = d.results()
	fin.Duration = time.Since(start)
	send(fin)
}

// call runs fn for one indexer under its breaker, rate limit and timeout. fn
// runs in its own goroutine so that an implementation ignoring its context
// cannot hold the search past the timeout.
func (s *Orchestrator) call(ctx context.Context, info indexer.Info, fn func(context.Context) ([]indexer.Release, error)) outcome {
	start := time.Now()
	oc := outcome{info: info}
	s.mu.Lock()
	ok, retryAt := s.breaker(info.ID).Allow(s.now())
	s.mu.Unlock()
	if !ok {
		oc.err = &CircuitOpenError{Indexer: info.Name, Until: retryAt}
		oc.willRetryAt = retryAt
		return oc
	}

	cctx, cancel := context.WithTimeout(ctx, s.timeout)
	defer cancel()
	type result struct {
		releases []indexer.Release
		err      error
	}
	var r result
	throttled := false
	if err := s.limiter(info.ID).Wait(cctx); err != nil {
		r.err, throttled = err, true
	} else {
		ch := make(chan result, 1)
		go func() {
			rels, err := fn(cctx)
			ch <- result{rels, err}
		}()
		select {
		case r = <-ch:
		case <-cctx.Done():
			r.err = cctx.Err()
		}
	}
	oc.releases, oc.err, oc.duration = r.releases, r.err, time.Since(start)

	switch {
	case ctx.Err() != nil:
		// Canceled by the caller: no verdict on the indexer.
		oc.err = ctx.Err()
		s.mu.Lock()
		s.breaker(info.ID).Abandon()
		s.mu.Unlock()
	case oc.err == nil:
		s.mu.Lock()
		s.breaker(info.ID).Success(s.now())
		s.mu.Unlock()
	case throttled || errors.Is(oc.err, indexer.ErrBadRequest):
		// Our own rate limit or the query, not the indexer, is at fault.
		if throttled {
			oc.err = &indexer.Error{Kind: indexer.ErrTimeout, Indexer: info.Name,
				Message: "too many searches in a row: the rate limit of the indexer delayed this one past the timeout", Err: oc.err}
		}
		s.mu.Lock()
		s.breaker(info.ID).Abandon()
		s.mu.Unlock()
	default:
		if errors.Is(oc.err, context.DeadlineExceeded) && !errors.Is(oc.err, indexer.ErrTimeout) {
			oc.err = &indexer.Error{Kind: indexer.ErrTimeout, Indexer: info.Name,
				Message: fmt.Sprintf("no answer within %s", s.timeout), Err: oc.err}
		}
		s.mu.Lock()
		oc.willRetryAt = s.breaker(info.ID).Failure(s.now(), oc.err)
		s.mu.Unlock()
		s.log.WarnContext(ctx, "indexer call failed", "indexer", info.Name, "error", oc.err.Error(), "disabled_until", oc.willRetryAt)
	}
	return oc
}

// deduper merges releases across indexers. Not safe for concurrent use: only
// the collector goroutine uses it.
type deduper struct {
	index  map[string]int
	groups [][]indexer.Release // first element: copy sent first
}

func newDeduper() *deduper { return &deduper{index: make(map[string]int)} }

func (d *deduper) add(info indexer.Info, rels []indexer.Release) *ResultBatch {
	b := &ResultBatch{Indexer: info}
	for i := range rels {
		k := Key(&rels[i])
		if g, ok := d.index[k]; ok {
			if !hasIndexer(d.groups[g], rels[i].IndexerID) {
				d.groups[g] = append(d.groups[g], rels[i])
				b.Duplicates = append(b.Duplicates, Duplicate{Key: k, Source: rels[i].Source()})
			}
			continue
		}
		d.index[k] = len(d.groups)
		d.groups = append(d.groups, []indexer.Release{rels[i]})
		b.Releases = append(b.Releases, rels[i])
	}
	return b
}

func hasIndexer(g []indexer.Release, id int64) bool {
	for i := range g {
		if g[i].IndexerID == id {
			return true
		}
	}
	return false
}

func (d *deduper) results() []indexer.Release {
	out := make([]indexer.Release, 0, len(d.groups))
	for _, g := range d.groups {
		if len(g) == 1 {
			out = append(out, g[0])
			continue
		}
		best := 0
		for i := 1; i < len(g); i++ {
			if g[i].IndexerPriority < g[best].IndexerPriority ||
				(g[i].IndexerPriority == g[best].IndexerPriority && g[i].Seeders > g[best].Seeders) {
				best = i
			}
		}
		r := g[best]
		r.Sources = make([]indexer.Source, len(g))
		for i := range g {
			r.Sources[i] = g[i].Source()
		}
		out = append(out, r)
	}
	return out
}
