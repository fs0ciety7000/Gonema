package auth

import (
	"sync"
	"time"
)

// limiter counts failures per key (client IP, account) in a sliding window.
// When a key reaches max failures, further attempts are refused until the
// oldest failure leaves the window. It is a throttle, not a lockout: the
// legitimate user waits at most one window. Safe for concurrent use.
type limiter struct {
	window time.Duration
	max    int

	mu       sync.Mutex
	hits     map[string][]time.Time
	inflight map[string]int // attempts reserved and not yet ended
}

// maxKeys bounds the memory of the limiter (an attacker rotating IPv6
// addresses must not grow it without bound).
const maxKeys = 10_000

func newLimiter(limit int, window time.Duration) *limiter {
	return &limiter{window: window, max: limit, hits: make(map[string][]time.Time), inflight: make(map[string]int)}
}

func (l *limiter) live(key string, now time.Time) []time.Time {
	h := l.hits[key]
	i := 0
	for i < len(h) && now.Sub(h[i]) >= l.window {
		i++
	}
	if i == len(h) {
		delete(l.hits, key)
		return nil
	}
	if i > 0 {
		h = h[i:]
		l.hits[key] = h
	}
	return h
}

// wait returns how long key must wait before its next attempt (0: allowed).
func (l *limiter) wait(key string, now time.Time) time.Duration {
	l.mu.Lock()
	defer l.mu.Unlock()
	h := l.live(key, now)
	if len(h) < l.max {
		return 0
	}
	return h[len(h)-l.max].Add(l.window).Sub(now)
}

// inflightRetry is the Retry-After of an attempt refused because others of
// the same key are still being checked.
const inflightRetry = time.Second

// reserve claims an attempt for key before the password is checked. It is
// refused (wait > 0) when the failures of the window plus the attempts in
// flight reach the limit: concurrent attempts cannot exceed it. A reserved
// attempt must be ended with end.
func (l *limiter) reserve(key string, now time.Time) time.Duration {
	l.mu.Lock()
	defer l.mu.Unlock()
	h := l.live(key, now)
	if len(h)+l.inflight[key] >= l.max {
		if len(h) >= l.max {
			return h[len(h)-l.max].Add(l.window).Sub(now)
		}
		return inflightRetry
	}
	l.inflight[key]++
	return 0
}

// outcome ends a reserved attempt.
type outcome int

const (
	aborted   outcome = iota // not a password check (error, busy server): nothing recorded
	failed                   // wrong credentials: a failure is recorded
	succeeded                // right credentials: the failures of the key are forgotten
)

// end ends an attempt reserved with reserve.
func (l *limiter) end(key string, now time.Time, o outcome) {
	l.mu.Lock()
	if n := l.inflight[key] - 1; n > 0 {
		l.inflight[key] = n
	} else {
		delete(l.inflight, key)
	}
	l.mu.Unlock()
	switch o {
	case failed:
		l.fail(key, now)
	case succeeded:
		l.reset(key)
	}
}

// fail records a failure of key.
func (l *limiter) fail(key string, now time.Time) {
	l.mu.Lock()
	defer l.mu.Unlock()
	if len(l.hits) >= maxKeys {
		for k := range l.hits {
			l.live(k, now)
		}
		for k := range l.hits { // still full: forget arbitrary keys
			if len(l.hits) < maxKeys {
				break
			}
			delete(l.hits, k)
		}
	}
	h := append(l.live(key, now), now)
	if len(h) > l.max {
		h = h[len(h)-l.max:]
	}
	l.hits[key] = h
}

// reset forgets the failures of key (after a success).
func (l *limiter) reset(key string) {
	l.mu.Lock()
	defer l.mu.Unlock()
	delete(l.hits, key)
}
