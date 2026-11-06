package api

import (
	"context"
	"net/http"
	"strconv"
	"sync"

	"github.com/danielgtaylor/huma/v2"

	"github.com/fs0ciety7000/gonema/internal/auth"
)

// Caps of the long-lived Server-Sent Events connections (/events and
// /logs/stream together): each one holds a goroutine, a bus subscription and
// its buffer for as long as the client stays.
const (
	maxStreams          = 64 // whole server
	maxStreamsPerCaller = 8  // per session, per API key, per local address
	streamRetryAfter    = 10 // seconds advised with 429
)

// streamLimiter counts the open streams. Safe for concurrent use.
type streamLimiter struct {
	mu       sync.Mutex
	total    int
	max, per int
	open     map[string]int
}

func newStreamLimiter(total, perCaller int) *streamLimiter {
	return &streamLimiter{max: total, per: perCaller, open: make(map[string]int)}
}

// acquire claims a stream for caller; release must be called when it ends.
func (l *streamLimiter) acquire(caller string) (release func(), ok bool) {
	l.mu.Lock()
	defer l.mu.Unlock()
	if l.total >= l.max || l.open[caller] >= l.per {
		return nil, false
	}
	l.total++
	l.open[caller]++
	var once sync.Once
	return func() {
		once.Do(func() {
			l.mu.Lock()
			defer l.mu.Unlock()
			l.total--
			if n := l.open[caller] - 1; n > 0 {
				l.open[caller] = n
			} else {
				delete(l.open, caller)
			}
		})
	}, true
}

// streamCaller identifies the caller of ctx for the per-caller cap.
func streamCaller(ctx context.Context) string {
	p, _ := PrincipalOf(ctx)
	switch p.Method {
	case auth.MethodSession:
		return "session:" + strconv.FormatInt(p.SessionID, 10)
	case auth.MethodAPIKey:
		return "apikey:" + strconv.FormatInt(p.UserID, 10)
	}
	return "addr:" + clientOf(ctx).IP
}

// openStream claims a stream slot for the caller of ctx, or returns a 429.
func (s *Server) openStream(ctx context.Context) (func(), error) {
	release, ok := s.streamSlots.acquire(streamCaller(ctx))
	if !ok {
		return nil, huma.ErrorWithHeaders(
			huma.Error429TooManyRequests("Too many live connections are open (event and log streams): close other Gonema tabs, or try again later."),
			http.Header{"Retry-After": {strconv.Itoa(streamRetryAfter)}})
	}
	return release, nil
}
