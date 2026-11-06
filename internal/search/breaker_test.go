package search

import (
	"errors"
	"testing"
	"time"

	"github.com/fs0ciety7000/gonema/internal/indexer"
)

var t0 = time.Date(2026, 11, 5, 12, 0, 0, 0, time.UTC)

type step struct {
	at    time.Duration // offset from t0
	op    string        // "allow", "ok", "fail", "limit", "abandon"
	allow bool          // for "allow": expected verdict
	state BreakerState  // state after the step
	until time.Duration // expected DisabledUntil offset; -1 for zero
	fails int           // expected consecutive failures
}

var errBoom = &indexer.Error{Kind: indexer.ErrServer, Indexer: "x", Message: "boom"}

func TestBreakerStateMachine(t *testing.T) {
	t.Parallel()
	p := BreakerPolicy{Threshold: 3, BaseCooldown: time.Minute, MaxCooldown: 5 * time.Minute}
	tests := []struct {
		name  string
		steps []step
	}{
		{"opens after threshold", []step{
			{0, "fail", false, Closed, -1, 1},
			{0, "fail", false, Closed, -1, 2},
			{0, "allow", true, Closed, -1, 2},
			{0, "fail", false, Open, time.Minute, 3},
			{30 * time.Second, "allow", false, Open, time.Minute, 3},
		}},
		{"success resets the count", []step{
			{0, "fail", false, Closed, -1, 1},
			{0, "fail", false, Closed, -1, 2},
			{0, "ok", false, Closed, -1, 0},
			{0, "fail", false, Closed, -1, 1},
		}},
		{"half-open probe success closes", []step{
			{0, "fail", false, Closed, -1, 1}, {0, "fail", false, Closed, -1, 2}, {0, "fail", false, Open, time.Minute, 3},
			{time.Minute, "allow", true, HalfOpen, time.Minute, 3},
			{time.Minute, "allow", false, HalfOpen, time.Minute, 3}, // one probe at a time
			{time.Minute + time.Second, "ok", false, Closed, -1, 0},
			{time.Minute + time.Second, "allow", true, Closed, -1, 0},
		}},
		{"failed probes escalate then cap", []step{
			{0, "fail", false, Closed, -1, 1}, {0, "fail", false, Closed, -1, 2},
			{0, "fail", false, Open, time.Minute, 3},
			{time.Minute, "allow", true, HalfOpen, time.Minute, 3},
			{time.Minute, "fail", false, Open, 3 * time.Minute, 4}, // 2 min
			{3 * time.Minute, "allow", true, HalfOpen, 3 * time.Minute, 4},
			{3 * time.Minute, "fail", false, Open, 7 * time.Minute, 5}, // 4 min
			{7 * time.Minute, "allow", true, HalfOpen, 7 * time.Minute, 5},
			{7 * time.Minute, "fail", false, Open, 12 * time.Minute, 6}, // capped at 5 min
			{12 * time.Minute, "allow", true, HalfOpen, 12 * time.Minute, 6},
			{12 * time.Minute, "ok", false, Closed, -1, 0},
			// Escalation restarts after a success.
			{13 * time.Minute, "fail", false, Closed, -1, 1}, {13 * time.Minute, "fail", false, Closed, -1, 2},
			{13 * time.Minute, "fail", false, Open, 14 * time.Minute, 3},
		}},
		{"rate limit opens at once for RetryAfter", []step{
			{0, "limit", false, Open, 10 * time.Minute, 1},
			{9 * time.Minute, "allow", false, Open, 10 * time.Minute, 1},
			{10 * time.Minute, "allow", true, HalfOpen, 10 * time.Minute, 1},
		}},
		{"late failure while open only extends for RetryAfter", []step{
			{0, "fail", false, Closed, -1, 1}, {0, "fail", false, Closed, -1, 2}, {0, "fail", false, Open, time.Minute, 3},
			{time.Second, "fail", false, Open, time.Minute, 4},
			{time.Second, "limit", false, Open, 10*time.Minute + time.Second, 5},
		}},
		{"abandoned probe lets the next caller probe", []step{
			{0, "limit", false, Open, 10 * time.Minute, 1},
			{10 * time.Minute, "allow", true, HalfOpen, 10 * time.Minute, 1},
			{10 * time.Minute, "abandon", false, HalfOpen, 10 * time.Minute, 1},
			{10 * time.Minute, "allow", true, HalfOpen, 10 * time.Minute, 1},
		}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			b := NewBreaker(p)
			for i, s := range tt.steps {
				now := t0.Add(s.at)
				switch s.op {
				case "allow":
					ok, retryAt := b.Allow(now)
					if ok != s.allow {
						t.Fatalf("step %d: Allow = %v, want %v", i, ok, s.allow)
					}
					if !ok && b.Health().State == Open && !retryAt.Equal(b.Health().DisabledUntil) {
						t.Errorf("step %d: retryAt %v, want %v", i, retryAt, b.Health().DisabledUntil)
					}
				case "ok":
					b.Success(now)
				case "fail":
					b.Failure(now, errBoom)
				case "limit":
					b.Failure(now, &indexer.Error{Kind: indexer.ErrRateLimited, RetryAfter: 10 * time.Minute})
				case "abandon":
					b.Abandon()
				}
				h := b.Health()
				wantUntil := time.Time{}
				if s.until >= 0 {
					wantUntil = t0.Add(s.until)
				}
				if h.State != s.state || !h.DisabledUntil.Equal(wantUntil) || h.ConsecutiveFailures != s.fails {
					t.Fatalf("step %d (%s at %s): state %s until %v fails %d, want %s %v %d",
						i, s.op, s.at, h.State, h.DisabledUntil, h.ConsecutiveFailures, s.state, wantUntil, s.fails)
				}
			}
		})
	}
}

func TestBreakerHealthFields(t *testing.T) {
	t.Parallel()
	b := NewBreaker(BreakerPolicy{})
	b.Success(t0)
	b.Failure(t0.Add(time.Second), errBoom)
	h := b.Health()
	if !h.LastOKAt.Equal(t0) || !h.LastFailureAt.Equal(t0.Add(time.Second)) || h.LastError != errBoom.Error() {
		t.Errorf("health = %+v", h)
	}
	// Defaults: 3 failures, 5 min.
	b.Failure(t0, errBoom)
	if until := b.Failure(t0, errors.New("x")); !until.Equal(t0.Add(5 * time.Minute)) {
		t.Errorf("until = %v", until)
	}
}

func TestRestoreBreaker(t *testing.T) {
	t.Parallel()
	b := RestoreBreaker(BreakerPolicy{}, Health{State: Open, ConsecutiveFailures: 5, Opens: 2, DisabledUntil: t0.Add(time.Hour)})
	if ok, at := b.Allow(t0); ok || !at.Equal(t0.Add(time.Hour)) {
		t.Errorf("Allow = %v %v", ok, at)
	}
	if ok, _ := b.Allow(t0.Add(time.Hour)); !ok || b.Health().State != HalfOpen {
		t.Errorf("probe refused: %+v", b.Health())
	}
	// Failed probe escalates from the restored step: 5 min * 2^2.
	if until := b.Failure(t0.Add(time.Hour), errBoom); !until.Equal(t0.Add(time.Hour + 20*time.Minute)) {
		t.Errorf("until = %v", until)
	}
	if b := RestoreBreaker(BreakerPolicy{}, Health{State: HalfOpen, DisabledUntil: t0}); b.Health().State != Open {
		t.Errorf("half-open restored as %s", b.Health().State)
	}
	if b := RestoreBreaker(BreakerPolicy{}, Health{State: Open}); b.Health().State != Closed {
		t.Errorf("open without until restored as %s", b.Health().State)
	}
}

func TestCooldown(t *testing.T) {
	t.Parallel()
	p := BreakerPolicy{}.withDefaults()
	want := []time.Duration{5 * time.Minute, 10 * time.Minute, 20 * time.Minute, 40 * time.Minute, 80 * time.Minute, 160 * time.Minute, 320 * time.Minute, 6 * time.Hour, 6 * time.Hour}
	for i, w := range want {
		if got := p.cooldown(i + 1); got != w {
			t.Errorf("cooldown(%d) = %s, want %s", i+1, got, w)
		}
	}
	if got := p.cooldown(1000); got != 6*time.Hour {
		t.Errorf("cooldown(1000) = %s", got)
	}
}
