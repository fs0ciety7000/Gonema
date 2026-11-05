package outbound

import (
	"context"
	"time"
)

// Retry runs a call again after temporary failures (see Temporary), waiting
// the longer of its backoff step and the Retry-After the server asked for.
type Retry struct {
	// Attempts is the total number of calls (minimum 1).
	Attempts int
	// Backoff is the wait before the second, third... call; the last value is
	// reused when there are more attempts than steps.
	Backoff []time.Duration
	// Sleep waits d or until ctx is done; default SleepContext. Tests replace it.
	Sleep func(ctx context.Context, d time.Duration) error
}

// Do calls fn until it succeeds, fails for good, or the attempts run out. It
// returns the last error.
func (r Retry) Do(ctx context.Context, fn func(ctx context.Context) error) error {
	sleep := r.Sleep
	if sleep == nil {
		sleep = SleepContext
	}
	attempts := max(r.Attempts, 1)
	var err error
	for i := range attempts {
		if err = fn(ctx); err == nil || !Temporary(err) || i == attempts-1 {
			return err
		}
		wait := time.Second
		if len(r.Backoff) > 0 {
			wait = r.Backoff[min(i, len(r.Backoff)-1)]
		}
		wait = max(wait, RetryAfterOf(err))
		if serr := sleep(ctx, wait); serr != nil {
			return err
		}
	}
	return err
}

// SleepContext waits d, or returns ctx.Err() when ctx is done first.
func SleepContext(ctx context.Context, d time.Duration) error {
	t := time.NewTimer(d)
	defer t.Stop()
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-t.C:
		return nil
	}
}
