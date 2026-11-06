// Package flight collapses concurrent calls with the same key into one execution
// (singleflight), with per-caller cancellation.
package flight

import (
	"context"
	"sync"
)

// Group runs at most one call per key at a time. The zero value is ready to use.
//
// Unlike a plain singleflight, the shared call runs on its own context, derived
// from the first caller's values but not its cancellation: a caller that gives up
// does not fail the others. The shared call is canceled only when every caller
// waiting for it has left; it is then forgotten so that a new caller starts a
// fresh one. fn must return in bounded time once its context is canceled.
type Group[V any] struct {
	mu sync.Mutex
	m  map[string]*call[V]
}

type call[V any] struct {
	done    chan struct{}
	val     V
	err     error
	waiters int
	cancel  context.CancelFunc
}

// Do runs fn once for all concurrent callers of key and returns its result to
// each of them. shared reports whether the caller joined a call started by
// another one. When ctx ends first, Do returns ctx.Err().
func (g *Group[V]) Do(ctx context.Context, key string, fn func(context.Context) (V, error)) (val V, shared bool, err error) {
	g.mu.Lock()
	if g.m == nil {
		g.m = make(map[string]*call[V])
	}
	if c, ok := g.m[key]; ok {
		c.waiters++
		g.mu.Unlock()
		val, err = g.wait(ctx, key, c)
		return val, true, err
	}
	fctx, cancel := context.WithCancel(context.WithoutCancel(ctx))
	c := &call[V]{done: make(chan struct{}), waiters: 1, cancel: cancel}
	g.m[key] = c
	g.mu.Unlock()

	go func() {
		defer cancel()
		v, e := fn(fctx)
		g.mu.Lock()
		if g.m[key] == c {
			delete(g.m, key)
		}
		g.mu.Unlock()
		c.val, c.err = v, e
		close(c.done)
	}()
	val, err = g.wait(ctx, key, c)
	return val, false, err
}

func (g *Group[V]) wait(ctx context.Context, key string, c *call[V]) (V, error) {
	select {
	case <-c.done:
		return c.val, c.err
	case <-ctx.Done():
		g.mu.Lock()
		c.waiters--
		if c.waiters == 0 {
			if g.m[key] == c {
				delete(g.m, key)
			}
			c.cancel()
		}
		g.mu.Unlock()
		var zero V
		return zero, ctx.Err()
	}
}

// Waiters returns the number of callers waiting on key (0 when no call runs).
func (g *Group[V]) Waiters(key string) int {
	g.mu.Lock()
	defer g.mu.Unlock()
	if c, ok := g.m[key]; ok {
		return c.waiters
	}
	return 0
}

// TotalWaiters returns the number of callers waiting on any key.
func (g *Group[V]) TotalWaiters() int {
	g.mu.Lock()
	defer g.mu.Unlock()
	n := 0
	for _, c := range g.m {
		n += c.waiters
	}
	return n
}
