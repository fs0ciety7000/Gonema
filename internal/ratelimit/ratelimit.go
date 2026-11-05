// Package ratelimit provides a token bucket shared by goroutines, used to stay
// under the request rate of external APIs.
package ratelimit

import (
	"context"
	"errors"
	"sync"
	"time"
)

// Limiter is a token bucket: it holds up to Burst tokens and refills at Rate
// tokens per second. Wait takes one token, waiting for it if needed. Waiters are
// served in arrival order: each one reserves its token (the bucket may go
// negative) and sleeps until the refill covers it. Safe for concurrent use.
type Limiter struct {
	rate  float64 // tokens per second
	burst float64

	mu     sync.Mutex
	tokens float64
	last   time.Time

	now   func() time.Time
	sleep func(ctx context.Context, d time.Duration) error
}

// New returns a limiter allowing rate requests per second with bursts of burst
// requests. rate must be > 0; burst < 1 is treated as 1. The bucket starts full.
func New(rate float64, burst int) *Limiter {
	return newLimiter(rate, burst, time.Now, sleepCtx)
}

func newLimiter(rate float64, burst int, now func() time.Time, sleep func(context.Context, time.Duration) error) *Limiter {
	if rate <= 0 {
		rate = 1
	}
	if burst < 1 {
		burst = 1
	}
	return &Limiter{rate: rate, burst: float64(burst), tokens: float64(burst), last: now(), now: now, sleep: sleep}
}

// ErrCanceled wraps the context error when Wait gives up.
var ErrCanceled = errors.New("ratelimit: wait canceled")

// Wait blocks until a token is available or ctx is done. When ctx ends first the
// reserved token is given back and the error wraps both ErrCanceled and ctx.Err().
func (l *Limiter) Wait(ctx context.Context) error {
	if err := ctx.Err(); err != nil {
		return errors.Join(ErrCanceled, err)
	}
	l.mu.Lock()
	now := l.now()
	if elapsed := now.Sub(l.last).Seconds(); elapsed > 0 {
		l.tokens = min(l.burst, l.tokens+elapsed*l.rate)
		l.last = now
	}
	l.tokens--
	deficit := -l.tokens
	l.mu.Unlock()
	if deficit <= 0 {
		return nil
	}
	wait := time.Duration(deficit / l.rate * float64(time.Second))
	if err := l.sleep(ctx, wait); err != nil {
		l.mu.Lock()
		l.tokens = min(l.burst, l.tokens+1)
		l.mu.Unlock()
		return errors.Join(ErrCanceled, err)
	}
	return nil
}

func sleepCtx(ctx context.Context, d time.Duration) error {
	t := time.NewTimer(d)
	defer t.Stop()
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-t.C:
		return nil
	}
}
