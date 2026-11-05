package flight

import (
	"context"
	"errors"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

func waitFor(t *testing.T, cond func() bool) {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for !cond() {
		if time.Now().After(deadline) {
			t.Fatal("condition not reached within 5s")
		}
		time.Sleep(time.Millisecond)
	}
}

func TestDoShares(t *testing.T) {
	t.Parallel()
	var g Group[int]
	var runs atomic.Int32
	release := make(chan struct{})
	fn := func(context.Context) (int, error) {
		runs.Add(1)
		<-release
		return 42, nil
	}
	var wg sync.WaitGroup
	var shared atomic.Int32
	for range 10 {
		wg.Go(func() {
			v, s, err := g.Do(t.Context(), "k", fn)
			if err != nil || v != 42 {
				t.Errorf("Do = %d, %v", v, err)
			}
			if s {
				shared.Add(1)
			}
		})
	}
	waitFor(t, func() bool { return g.Waiters("k") == 10 })
	close(release)
	wg.Wait()
	if runs.Load() != 1 || shared.Load() != 9 {
		t.Errorf("runs %d shared %d, want 1 and 9", runs.Load(), shared.Load())
	}
	if g.TotalWaiters() != 0 {
		t.Error("call not forgotten")
	}
}

func TestDoErrorShared(t *testing.T) {
	t.Parallel()
	var g Group[string]
	boom := errors.New("boom")
	_, _, err := g.Do(t.Context(), "k", func(context.Context) (string, error) { return "", boom })
	if !errors.Is(err, boom) {
		t.Fatalf("err = %v", err)
	}
}

func TestLastWaiterCancels(t *testing.T) {
	t.Parallel()
	var g Group[int]
	canceled := make(chan struct{})
	ctx, cancel := context.WithCancel(t.Context())
	done := make(chan error, 1)
	go func() {
		_, _, err := g.Do(ctx, "k", func(fctx context.Context) (int, error) {
			<-fctx.Done()
			close(canceled)
			return 0, fctx.Err()
		})
		done <- err
	}()
	waitFor(t, func() bool { return g.Waiters("k") == 1 })
	cancel()
	if err := <-done; !errors.Is(err, context.Canceled) {
		t.Fatalf("err = %v", err)
	}
	select {
	case <-canceled:
	case <-time.After(5 * time.Second):
		t.Fatal("shared call not canceled when its last waiter left")
	}
	// A new caller starts a fresh call.
	v, shared, err := g.Do(t.Context(), "k", func(context.Context) (int, error) { return 7, nil })
	if v != 7 || shared || err != nil {
		t.Errorf("fresh Do = %d %v %v", v, shared, err)
	}
}
