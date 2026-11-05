package search

import (
	"errors"
	"fmt"
	"time"

	"github.com/fs0ciety7000/gonema/internal/indexer"
)

// BreakerState is the state of an indexer circuit breaker.
type BreakerState uint8

// Breaker states.
const (
	Closed   BreakerState = iota // requests flow
	Open                         // requests are refused until DisabledUntil
	HalfOpen                     // one probe request decides
)

var stateNames = [...]string{"closed", "open", "half-open"}

func (s BreakerState) String() string {
	if int(s) < len(stateNames) {
		return stateNames[s]
	}
	return fmt.Sprintf("BreakerState(%d)", uint8(s))
}

// BreakerPolicy tunes a breaker. Zero fields select the defaults.
type BreakerPolicy struct {
	// Threshold is the number of consecutive failures that opens the breaker;
	// default 3. A rate-limit answer opens it at once.
	Threshold int
	// BaseCooldown is the first open period; each consecutive open doubles it.
	// Default 5 min.
	BaseCooldown time.Duration
	// MaxCooldown caps the open period; default 6 h.
	MaxCooldown time.Duration
}

func (p BreakerPolicy) withDefaults() BreakerPolicy {
	if p.Threshold <= 0 {
		p.Threshold = 3
	}
	if p.BaseCooldown <= 0 {
		p.BaseCooldown = 5 * time.Minute
	}
	if p.MaxCooldown < p.BaseCooldown {
		p.MaxCooldown = max(6*time.Hour, p.BaseCooldown)
	}
	return p
}

// cooldown returns the open period after the n-th consecutive open (n >= 1):
// BaseCooldown * 2^(n-1), capped at MaxCooldown.
func (p BreakerPolicy) cooldown(n int) time.Duration {
	d := p.BaseCooldown
	for i := 1; i < n && d < p.MaxCooldown; i++ {
		d *= 2
	}
	return min(d, p.MaxCooldown)
}

// Health is the persisted and displayed state of a breaker (indexers columns
// failure_count, disabled_until, last_error, last_ok_at).
type Health struct {
	State               BreakerState `json:"state"`
	ConsecutiveFailures int          `json:"consecutiveFailures"`
	// Opens counts the consecutive opens without a success: it sets the
	// escalation step of the cooldown.
	Opens         int       `json:"opens"`
	LastError     string    `json:"lastError,omitempty"`
	LastFailureAt time.Time `json:"lastFailureAt"`
	LastOKAt      time.Time `json:"lastOkAt"`
	// DisabledUntil is the end of the open period; zero when closed.
	DisabledUntil time.Time `json:"disabledUntil"`
}

// Breaker is a circuit breaker for one indexer. It is a pure state machine: the
// current time is a parameter of every transition. It is not safe for concurrent
// use; the Orchestrator guards it.
type Breaker struct {
	policy  BreakerPolicy
	h       Health
	probing bool // a half-open probe is in flight
}

// NewBreaker returns a closed breaker.
func NewBreaker(p BreakerPolicy) *Breaker { return &Breaker{policy: p.withDefaults()} }

// RestoreBreaker returns a breaker in a persisted state. A half-open state is
// restored as open with an elapsed period, so the next request probes.
func RestoreBreaker(p BreakerPolicy, h Health) *Breaker {
	b := &Breaker{policy: p.withDefaults(), h: h}
	switch {
	case h.State == HalfOpen:
		b.h.State = Open
	case h.State == Open && h.DisabledUntil.IsZero():
		b.h.State = Closed
	}
	return b
}

// Health returns a snapshot of the state.
func (b *Breaker) Health() Health { return b.h }

// Allow reports whether a request may be sent at now. When the open period has
// elapsed, the breaker turns half-open and the caller becomes the probe; the
// caller must then report Success, Failure or Abandon. When refused, retryAt is
// the end of the open period, or zero while a probe is in flight.
func (b *Breaker) Allow(now time.Time) (ok bool, retryAt time.Time) {
	switch b.h.State {
	case Open:
		if now.Before(b.h.DisabledUntil) {
			return false, b.h.DisabledUntil
		}
		b.h.State = HalfOpen
		b.probing = true
		return true, time.Time{}
	case HalfOpen:
		if b.probing {
			return false, time.Time{}
		}
		b.probing = true
		return true, time.Time{}
	}
	return true, time.Time{}
}

// Success records a successful request: the breaker closes and the escalation
// resets.
func (b *Breaker) Success(now time.Time) {
	b.h.State = Closed
	b.h.ConsecutiveFailures = 0
	b.h.Opens = 0
	b.h.DisabledUntil = time.Time{}
	b.h.LastOKAt = now
	b.probing = false
}

// Failure records a failed request and returns the end of the open period, or
// zero when the breaker stays closed. A failed probe reopens with the next
// cooldown step; a rate-limit error opens at once for at least its RetryAfter.
func (b *Breaker) Failure(now time.Time, err error) (disabledUntil time.Time) {
	b.h.ConsecutiveFailures++
	b.h.LastFailureAt = now
	if err != nil {
		b.h.LastError = err.Error()
	}
	retryAfter := indexer.RetryAfter(err)
	limited := errors.Is(err, indexer.ErrRateLimited)
	switch b.h.State {
	case Closed:
		if b.h.ConsecutiveFailures >= b.policy.Threshold || limited {
			b.open(now, retryAfter)
		}
	case HalfOpen:
		b.probing = false
		b.open(now, retryAfter)
	case Open:
		// A request sent before the breaker opened: only extend for a longer
		// advised delay.
		if until := now.Add(retryAfter); retryAfter > 0 && until.After(b.h.DisabledUntil) {
			b.h.DisabledUntil = until
		}
	}
	return b.h.DisabledUntil
}

// Abandon records a probe that ended without verdict (caller canceled, request
// invalid): the next Allow probes again.
func (b *Breaker) Abandon() {
	b.probing = false
}

func (b *Breaker) open(now time.Time, retryAfter time.Duration) {
	b.h.Opens++
	d := max(b.policy.cooldown(b.h.Opens), retryAfter)
	b.h.State = Open
	b.h.DisabledUntil = now.Add(d)
}
