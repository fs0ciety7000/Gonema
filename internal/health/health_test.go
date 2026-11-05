package health

import (
	"context"
	"errors"
	"slices"
	"sync"
	"testing"
	"time"

	"github.com/fs0ciety7000/gonema/internal/events"
)

var t0 = time.Date(2026, 11, 5, 20, 50, 0, 0, time.UTC)

type fakeClock struct {
	mu sync.Mutex
	t  time.Time
}

func newClock() *fakeClock { return &fakeClock{t: t0} }

func (c *fakeClock) Now() time.Time {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.t
}

func (c *fakeClock) Advance(d time.Duration) {
	c.mu.Lock()
	c.t = c.t.Add(d)
	c.mu.Unlock()
}

// scripted is a check whose answers the test sets.
type scripted struct {
	mu     sync.Mutex
	issues []Issue
	err    error
	panics bool
	runs   int
}

func (s *scripted) set(err error, issues ...Issue) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.issues, s.err = issues, err
}

func (s *scripted) count() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.runs
}

func (s *scripted) run(context.Context) ([]Issue, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.runs++
	if s.panics {
		panic("boom")
	}
	return slices.Clone(s.issues), s.err
}

func issue(id string, sev Severity) Issue {
	return Issue{ID: id, Severity: sev, Title: id, Message: "m"}
}

// drain returns the health events published so far.
func drain(sub *events.Subscription) (raised, resolved []string) {
	for {
		select {
		case e := <-sub.C():
			id := e.Data.(events.HealthIssueEvent).Issue.ID
			if e.Type == events.HealthIssueRaised {
				raised = append(raised, id)
			} else {
				resolved = append(resolved, id)
			}
		default:
			return raised, resolved
		}
	}
}

func ids(list []Issue) []string {
	out := make([]string, 0, len(list))
	for i := range list {
		out = append(out, list[i].ID)
	}
	return out
}

func TestSchedulerRaisesAndResolves(t *testing.T) {
	clk := newClock()
	bus := events.New(events.Options{Now: clk.Now})
	sub := bus.Subscribe(64, func(e *events.Event) bool {
		return e.Type == events.HealthIssueRaised || e.Type == events.HealthIssueResolved
	})
	s := New(Options{Bus: bus, Now: clk.Now})
	a, b := &scripted{}, &scripted{}
	s.Register(Check{ID: "a", Run: a.run})
	s.Register(Check{ID: "b", Run: b.run, Interval: time.Hour})
	ctx := context.Background()

	a.set(nil, issue("a.one", Warning), issue("a.two", Info))
	b.set(nil, issue("b.one", Error))
	got := s.RunAll(ctx)
	if want := []string{"b.one", "a.one", "a.two"}; !slices.Equal(ids(got), want) {
		t.Fatalf("issues = %v, want %v (most urgent first)", ids(got), want)
	}
	for _, is := range got {
		if !is.Since.Equal(t0) || is.Source == "" || is.Fix == nil {
			t.Errorf("issue %s: since %v source %q fix %v", is.ID, is.Since, is.Source, is.Fix)
		}
	}
	raised, resolved := drain(sub)
	slices.Sort(raised)
	if !slices.Equal(raised, []string{"a.one", "a.two", "b.one"}) || len(resolved) != 0 {
		t.Errorf("events = %v / %v", raised, resolved)
	}

	// Still present: not raised again, Since kept. Severity change: raised again.
	clk.Advance(10 * time.Minute)
	a.set(nil, issue("a.one", Error))
	s.RunAll(ctx)
	raised, resolved = drain(sub)
	if !slices.Equal(raised, []string{"a.one"}) || !slices.Equal(resolved, []string{"a.two"}) {
		t.Errorf("events = %v / %v", raised, resolved)
	}
	if got := s.Issues(); got[0].ID != "a.one" && got[1].ID != "a.one" || !got[0].Since.Equal(t0) {
		t.Errorf("issues = %+v", got)
	}

	// A check that cannot look keeps its findings, so nothing flaps.
	a.set(errors.New("database is locked"))
	s.RunAll(ctx)
	if raised, resolved = drain(sub); len(raised)+len(resolved) != 0 {
		t.Errorf("events after a failed run = %v / %v", raised, resolved)
	}
	if len(s.Issues()) != 2 {
		t.Errorf("issues after a failed run = %v", ids(s.Issues()))
	}
	st := s.Checks()
	if st[0].ID != "a" || st[0].LastError != "database is locked" || st[0].Issues != 1 {
		t.Errorf("check status = %+v", st[0])
	}

	// A panicking check is contained the same way.
	a.mu.Lock()
	a.panics = true
	a.mu.Unlock()
	s.RunAll(ctx)
	if len(s.Issues()) != 2 {
		t.Errorf("issues after a panic = %v", ids(s.Issues()))
	}
	a.mu.Lock()
	a.panics = false
	a.mu.Unlock()

	// A check-provided Since wins.
	since := t0.Add(-time.Hour)
	b.set(nil, Issue{ID: "b.one", Severity: Error, Since: since})
	a.set(nil)
	s.RunAll(ctx)
	got = s.Issues()
	if len(got) != 1 || !got[0].Since.Equal(since) {
		t.Errorf("issues = %+v", got)
	}
	if _, resolved = drain(sub); !slices.Equal(resolved, []string{"a.one"}) {
		t.Errorf("resolved = %v", resolved)
	}
}

func TestSchedulerIntervalsAndTriggers(t *testing.T) {
	clk := newClock()
	s := New(Options{Now: clk.Now, Debounce: 10 * time.Second})
	fast, slow := &scripted{}, &scripted{}
	s.Register(Check{ID: "fast", Run: fast.run, Interval: time.Minute})
	s.Register(Check{ID: "slow", Run: slow.run, Interval: time.Hour, Triggers: []events.Type{"thing.changed"}})
	ctx := context.Background()

	s.RunDue(ctx)
	if fast.count()+slow.count() != 0 {
		t.Fatal("checks ran during the start delay")
	}
	clk.Advance(startDelay)
	s.RunDue(ctx)
	if fast.count() != 1 || slow.count() != 1 {
		t.Fatalf("first round: fast %d slow %d", fast.count(), slow.count())
	}
	clk.Advance(time.Minute)
	s.RunDue(ctx)
	if fast.count() != 2 || slow.count() != 1 {
		t.Fatalf("after a minute: fast %d slow %d", fast.count(), slow.count())
	}
	// A burst of events runs the interested check once, after the debounce.
	for range 5 {
		s.Trigger("thing.changed")
	}
	s.Trigger("other.event")
	s.RunDue(ctx)
	if slow.count() != 1 {
		t.Fatal("triggered check ran before the debounce")
	}
	clk.Advance(10 * time.Second)
	s.RunDue(ctx)
	s.RunDue(ctx)
	if slow.count() != 2 {
		t.Fatalf("triggered check ran %d times, want 2", slow.count())
	}
	if fast.count() != 2 {
		t.Errorf("untriggered check ran: %d", fast.count())
	}
}

func TestSchedulerRunLoop(t *testing.T) {
	clk := newClock()
	bus := events.New(events.Options{Now: clk.Now})
	ticks := make(chan time.Time)
	s := New(Options{Bus: bus, Now: clk.Now, Ticks: ticks, Debounce: time.Second})
	c := &scripted{}
	c.set(nil, issue("x.down", Warning))
	s.Register(Check{ID: "x", Run: c.run, Interval: time.Hour, Triggers: []events.Type{events.HealthChanged}})
	out := bus.Subscribe(8, func(e *events.Event) bool {
		return e.Type == events.HealthIssueRaised || e.Type == events.HealthIssueResolved
	})
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() {
		defer close(done)
		s.Run(ctx)
	}()
	defer func() {
		cancel()
		<-done
	}()
	next := func() events.Event {
		t.Helper()
		select {
		case e := <-out.C():
			return e
		case <-time.After(5 * time.Second):
			t.Fatal("no health event")
		}
		return events.Event{}
	}

	clk.Advance(startDelay)
	ticks <- clk.Now()
	if e := next(); e.Type != events.HealthIssueRaised {
		t.Fatalf("event = %s", e.Type)
	}
	// A bus event triggers the check, which runs at the next tick past the debounce.
	c.set(nil)
	bus.Publish(events.Event{Type: events.HealthChanged})
	deadline := time.Now().Add(5 * time.Second)
	for c.count() < 2 {
		clk.Advance(time.Second)
		select {
		case ticks <- clk.Now():
		case <-time.After(time.Until(deadline)):
			t.Fatal("loop stuck")
		}
		if time.Now().After(deadline) {
			t.Fatal("triggered check never ran")
		}
	}
	if e := next(); e.Type != events.HealthIssueResolved {
		t.Fatalf("event = %s", e.Type)
	}
}

func TestRegisterPanics(t *testing.T) {
	s := New(Options{})
	s.Register(Check{ID: "a", Run: (&scripted{}).run})
	for name, c := range map[string]Check{"duplicate": {ID: "a", Run: (&scripted{}).run}, "no run": {ID: "b"}, "no id": {Run: (&scripted{}).run}} {
		func() {
			defer func() {
				if recover() == nil {
					t.Errorf("%s: no panic", name)
				}
			}()
			s.Register(c)
		}()
	}
}
