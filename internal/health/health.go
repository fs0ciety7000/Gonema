// Package health runs the health checks of Gonema: each check looks at one
// area (metadata key, root folders, indexers, download clients, downloads,
// recycle bin, database) and reports the problems it finds in plain language,
// with what Gonema does about them and how to fix them.
//
// The Scheduler runs every check periodically and again shortly after a
// related event (debounced), keeps the current issues in memory, and publishes
// health.issue_raised and health.issue_resolved when the set changes. Checks
// only look: they never change settings nor touch user files.
package health

import (
	"cmp"
	"context"
	"fmt"
	"log/slog"
	"maps"
	"slices"
	"sync"
	"time"

	"github.com/fs0ciety7000/gonema/internal/events"
)

// Issue is a problem found by a check (see events.HealthIssue).
type Issue = events.HealthIssue

// Severity is the urgency of an issue.
type Severity = events.HealthSeverity

// Severities.
const (
	Info    = events.SeverityInfo
	Warning = events.SeverityWarning
	Error   = events.SeverityError
)

// Defaults of the scheduler.
const (
	DefaultInterval = 15 * time.Minute
	DefaultTimeout  = 30 * time.Second
	DefaultDebounce = 10 * time.Second
	DefaultTick     = 5 * time.Second
	// startDelay keeps the first checks out of the startup path.
	startDelay = 5 * time.Second
	// eventBuffer is the buffer of the scheduler's bus subscription; a dropped
	// event only delays a check until its next periodic run.
	eventBuffer = 64
)

// Check is one health check.
type Check struct {
	// ID names the check; it is the Source of its issues ("indexers").
	ID string
	// Interval between two periodic runs; default 15 minutes.
	Interval time.Duration
	// Timeout of one run; default 30 seconds.
	Timeout time.Duration
	// Triggers are the event types after which the check runs again (debounced).
	Triggers []events.Type
	// Run returns the current issues of the check. An error means the check
	// could not look (database busy, canceled): its previous issues are kept.
	Run func(ctx context.Context) ([]Issue, error)
}

// Options configures a Scheduler.
type Options struct {
	Bus      *events.Bus      // optional: events in (triggers) and out (issues)
	Logger   *slog.Logger     // default discards
	Now      func() time.Time // default time.Now
	Debounce time.Duration    // default 10 s
	// Ticks drives the loop of Run; default a ticker of 5 seconds. Tests send
	// on it to step the loop.
	Ticks <-chan time.Time
}

type checkState struct {
	next    time.Time
	issues  map[string]Issue
	lastRun time.Time
	lastErr string
}

// Scheduler runs the checks and keeps their issues. It is safe for concurrent
// use; register the checks before Run.
type Scheduler struct {
	bus      *events.Bus
	log      *slog.Logger
	now      func() time.Time
	debounce time.Duration
	ticks    <-chan time.Time

	mu     sync.Mutex
	checks []Check
	state  map[string]*checkState

	runMu sync.Mutex // one round of checks at a time
}

// New returns a scheduler without checks.
func New(opts Options) *Scheduler {
	s := &Scheduler{
		bus: opts.Bus, log: opts.Logger, now: opts.Now, debounce: opts.Debounce, ticks: opts.Ticks,
		state: make(map[string]*checkState),
	}
	if s.log == nil {
		s.log = slog.New(slog.DiscardHandler)
	}
	if s.now == nil {
		s.now = time.Now
	}
	if s.debounce <= 0 {
		s.debounce = DefaultDebounce
	}
	return s
}

// Register adds a check; its first run is a few seconds after Run starts (or
// at the first RunDue past that time). It panics on a duplicate or empty ID
// (programming error).
func (s *Scheduler) Register(c Check) {
	if c.ID == "" || c.Run == nil {
		panic("health: a check needs an ID and a Run function")
	}
	if c.Interval <= 0 {
		c.Interval = DefaultInterval
	}
	if c.Timeout <= 0 {
		c.Timeout = DefaultTimeout
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if _, dup := s.state[c.ID]; dup {
		panic("health: check " + c.ID + " registered twice")
	}
	s.checks = append(s.checks, c)
	s.state[c.ID] = &checkState{next: s.now().Add(startDelay), issues: map[string]Issue{}}
}

// Issues returns the current issues, most urgent first, then oldest first.
func (s *Scheduler) Issues() []Issue {
	s.mu.Lock()
	out := []Issue{}
	for _, st := range s.state {
		out = slices.AppendSeq(out, maps.Values(st.issues))
	}
	s.mu.Unlock()
	sortIssues(out)
	return out
}

func sortIssues(list []Issue) {
	slices.SortFunc(list, func(a, b Issue) int {
		if c := cmp.Compare(b.Severity.Rank(), a.Severity.Rank()); c != 0 {
			return c
		}
		if c := a.Since.Compare(b.Since); c != 0 {
			return c
		}
		return cmp.Compare(a.ID, b.ID)
	})
}

// RunAll runs every check now and returns the issues.
func (s *Scheduler) RunAll(ctx context.Context) []Issue {
	s.run(ctx, func(*checkState, time.Time) bool { return true })
	return s.Issues()
}

// RunDue runs the checks whose time has come.
func (s *Scheduler) RunDue(ctx context.Context) {
	s.run(ctx, func(st *checkState, now time.Time) bool { return !now.Before(st.next) })
}

// Trigger makes the checks interested in event type t run within the
// debounce delay: a burst of events causes one run.
func (s *Scheduler) Trigger(t events.Type) {
	s.mu.Lock()
	defer s.mu.Unlock()
	at := s.now().Add(s.debounce)
	for i := range s.checks {
		if slices.Contains(s.checks[i].Triggers, t) {
			if st := s.state[s.checks[i].ID]; at.Before(st.next) {
				st.next = at
			}
		}
	}
}

// Run drives the checks until ctx ends: due checks at each tick, and the
// triggers of the bus events.
func (s *Scheduler) Run(ctx context.Context) {
	ticks := s.ticks
	if ticks == nil {
		t := time.NewTicker(DefaultTick)
		defer t.Stop()
		ticks = t.C
	}
	var evs <-chan events.Event
	if s.bus != nil {
		triggers := s.triggerTypes()
		sub := s.bus.Subscribe(eventBuffer, func(e *events.Event) bool { return triggers[e.Type] })
		defer sub.Close()
		evs = sub.C()
	}
	for {
		select {
		case <-ctx.Done():
			return
		case e, ok := <-evs:
			if !ok {
				evs = nil
				continue
			}
			s.Trigger(e.Type)
		case <-ticks:
			s.RunDue(ctx)
		}
	}
}

func (s *Scheduler) triggerTypes() map[events.Type]bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	m := make(map[events.Type]bool)
	for i := range s.checks {
		for _, t := range s.checks[i].Triggers {
			m[t] = true
		}
	}
	return m
}

// run runs the selected checks concurrently, each with its timeout, and
// applies their results.
func (s *Scheduler) run(ctx context.Context, selected func(st *checkState, now time.Time) bool) {
	s.runMu.Lock()
	defer s.runMu.Unlock()
	now := s.now()
	s.mu.Lock()
	var due []Check
	for _, c := range s.checks {
		if st := s.state[c.ID]; selected(st, now) {
			st.next = now.Add(c.Interval)
			due = append(due, c)
		}
	}
	s.mu.Unlock()
	var wg sync.WaitGroup
	for _, c := range due {
		wg.Go(func() {
			issues, err := s.runOne(ctx, c)
			s.apply(c, issues, err)
		})
	}
	wg.Wait()
}

func (s *Scheduler) runOne(ctx context.Context, c Check) (issues []Issue, err error) {
	cctx, cancel := context.WithTimeout(ctx, c.Timeout)
	defer cancel()
	defer func() {
		if p := recover(); p != nil {
			err = fmt.Errorf("health: check %s panicked: %v", c.ID, p)
		}
	}()
	return c.Run(cctx)
}

// apply replaces the issues of a check and publishes the changes.
func (s *Scheduler) apply(c Check, found []Issue, err error) {
	now := s.now()
	s.mu.Lock()
	st := s.state[c.ID]
	st.lastRun = now
	if err != nil {
		st.lastErr = err.Error()
		s.mu.Unlock()
		s.log.Warn("health check could not run; its previous findings are kept", slog.String("check", c.ID), slog.Any("error", err))
		return
	}
	st.lastErr = ""
	next := make(map[string]Issue, len(found))
	var raised, resolved []Issue
	for i := range found {
		is := &found[i]
		is.Source = c.ID
		if is.Fix == nil {
			is.Fix = []string{}
		}
		prev, known := st.issues[is.ID]
		if is.Since.IsZero() {
			is.Since = now
			if known {
				is.Since = prev.Since
			}
		}
		if !known || prev.Severity != is.Severity {
			raised = append(raised, *is)
		}
		next[is.ID] = *is
	}
	for id := range st.issues {
		if _, still := next[id]; !still {
			resolved = append(resolved, st.issues[id])
		}
	}
	st.issues = next
	s.mu.Unlock()

	sortIssues(raised)
	sortIssues(resolved)
	for i := range resolved {
		s.log.Info("health issue resolved", slog.String("issue", resolved[i].ID))
		s.publish(events.HealthIssueResolved, resolved[i])
	}
	for i := range raised {
		is := &raised[i]
		lvl := slog.LevelInfo
		if is.Severity != Info {
			lvl = slog.LevelWarn
		}
		s.log.Log(context.Background(), lvl, "health issue", slog.String("issue", is.ID), slog.String("severity", string(is.Severity)),
			slog.String("title", is.Title))
		s.publish(events.HealthIssueRaised, *is)
	}
}

func (s *Scheduler) publish(t events.Type, is Issue) {
	if s.bus != nil {
		s.bus.Publish(events.Event{Type: t, Data: events.HealthIssueEvent{Issue: is}})
	}
}

// CheckStatus is the last run of a check.
type CheckStatus struct {
	ID        string    `json:"id"`
	LastRun   time.Time `json:"lastRun"`
	NextRun   time.Time `json:"nextRun"`
	LastError string    `json:"lastError,omitempty" doc:"Why the last run could not look; its previous findings are kept"`
	Issues    int       `json:"issues"`
}

// Checks returns the status of every check, in registration order.
func (s *Scheduler) Checks() []CheckStatus {
	s.mu.Lock()
	defer s.mu.Unlock()
	out := make([]CheckStatus, 0, len(s.checks))
	for _, c := range s.checks {
		st := s.state[c.ID]
		out = append(out, CheckStatus{ID: c.ID, LastRun: st.lastRun, NextRun: st.next, LastError: st.lastErr, Issues: len(st.issues)})
	}
	return out
}
