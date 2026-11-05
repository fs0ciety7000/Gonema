package ratelimit

import (
	"context"
	"errors"
	"slices"
	"sync"
	"testing"
	"time"
)

// fakeClock is a manual clock; sleep advances it when advance is true.
type fakeClock struct {
	mu      sync.Mutex
	t       time.Time
	advance bool
	waits   []time.Duration
}

func (c *fakeClock) now() time.Time {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.t
}

func (c *fakeClock) sleep(ctx context.Context, d time.Duration) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.waits = append(c.waits, d)
	if c.advance {
		c.t = c.t.Add(d)
	}
	return ctx.Err()
}

func TestWaitBurstThenRate(t *testing.T) {
	t.Parallel()
	c := &fakeClock{t: time.Unix(1_000_000, 0)}
	l := newLimiter(10, 5, c.now, c.sleep)
	for range 25 {
		if err := l.Wait(t.Context()); err != nil {
			t.Fatal(err)
		}
	}
	// 5 free tokens, then the 20 next reservations wait 100 ms more each.
	if len(c.waits) != 20 {
		t.Fatalf("got %d waits, want 20", len(c.waits))
	}
	for i, w := range c.waits {
		want := time.Duration(i+1) * 100 * time.Millisecond
		if d := w - want; d < -time.Millisecond || d > time.Millisecond {
			t.Errorf("wait %d = %s, want %s", i, w, want)
		}
	}
}

// TestWaitWindow drives the limiter with a clock that only moves when a caller
// sleeps, and checks that no 1 s window ever holds more than burst+rate grants.
func TestWaitWindow(t *testing.T) {
	t.Parallel()
	const rate, burst = 20, 20
	c := &fakeClock{t: time.Unix(1_000_000, 0), advance: true}
	l := newLimiter(rate, burst, c.now, c.sleep)
	var grants []time.Time
	for range 500 {
		if err := l.Wait(t.Context()); err != nil {
			t.Fatal(err)
		}
		grants = append(grants, c.now())
	}
	for i := range grants {
		end := grants[i].Add(time.Second)
		n := 0
		for j := i; j < len(grants) && grants[j].Before(end); j++ {
			n++
		}
		if n > rate+burst {
			t.Fatalf("%d grants in the window starting at grant %d, want <= %d", n, i, rate+burst)
		}
	}
	// Over the run the average rate must be the configured one.
	span := grants[len(grants)-1].Sub(grants[0]).Seconds()
	if got := float64(len(grants)-burst) / span; got > rate*1.01 {
		t.Errorf("average rate %.2f/s, want <= %d/s", got, rate)
	}
}

func TestWaitConcurrentReservations(t *testing.T) {
	t.Parallel()
	const rate, burst, n = 10, 3, 40
	c := &fakeClock{t: time.Unix(1_000_000, 0)}
	l := newLimiter(rate, burst, c.now, c.sleep)
	var wg sync.WaitGroup
	for range n {
		wg.Go(func() {
			if err := l.Wait(t.Context()); err != nil {
				t.Error(err)
			}
		})
	}
	wg.Wait()
	waits := slices.Clone(c.waits)
	slices.Sort(waits)
	if len(waits) != n-burst {
		t.Fatalf("got %d waits, want %d", len(waits), n-burst)
	}
	// Each goroutine got a distinct slot 100 ms after the previous one.
	for i, w := range waits {
		want := time.Duration(i+1) * time.Second / rate
		if d := w - want; d < -time.Millisecond || d > time.Millisecond {
			t.Errorf("slot %d waits %s, want %s", i, w, want)
		}
	}
}

func TestWaitCanceledReturnsToken(t *testing.T) {
	t.Parallel()
	c := &fakeClock{t: time.Unix(1_000_000, 0)}
	l := newLimiter(1, 1, c.now, c.sleep)
	if err := l.Wait(t.Context()); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	err := l.Wait(ctx)
	if !errors.Is(err, ErrCanceled) || !errors.Is(err, context.Canceled) {
		t.Fatalf("Wait on canceled ctx = %v, want ErrCanceled and context.Canceled", err)
	}
	// The bucket is still at 0 tokens: the next wait is 1 s, not 2 s.
	c.waits = nil
	if err := l.Wait(t.Context()); err != nil {
		t.Fatal(err)
	}
	if len(c.waits) != 1 || c.waits[0] != time.Second {
		t.Fatalf("waits = %v, want [1s]", c.waits)
	}
}

// TestWaitRealClock uses the real clock with a generous margin.
func TestWaitRealClock(t *testing.T) {
	t.Parallel()
	l := New(200, 1) // 5 ms per token after the first
	start := time.Now()
	for range 21 {
		if err := l.Wait(t.Context()); err != nil {
			t.Fatal(err)
		}
	}
	if el := time.Since(start); el < 90*time.Millisecond {
		t.Fatalf("21 waits took %s, want >= 100 ms (rate 200/s, burst 1)", el)
	}
}
