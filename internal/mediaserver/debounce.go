package mediaserver

import (
	"sync"
	"time"
)

// target is what a refresh is about: one folder on one server.
type target struct {
	server int64
	folder string
}

type pendingRefresh struct {
	reason Reason
	first  time.Time
	timer  *time.Timer
}

// debouncer coalesces the refreshes of a target: emit runs once, window after
// the last request (trailing edge), and never later than maxWait after the
// first one, with the merged reason. An upgrade (file recycled, new file
// imported, maybe renamed) therefore costs one refresh.
type debouncer struct {
	window  time.Duration
	maxWait time.Duration
	now     func() time.Time
	emit    func(t target, r Reason)

	mu      sync.Mutex
	pending map[target]*pendingRefresh
	closed  bool
}

func newDebouncer(window, maxWait time.Duration, now func() time.Time, emit func(target, Reason)) *debouncer {
	return &debouncer{
		window: window, maxWait: max(maxWait, window), now: now, emit: emit,
		pending: make(map[target]*pendingRefresh),
	}
}

// add requests a refresh of t for r.
func (d *debouncer) add(t target, r Reason) {
	d.mu.Lock()
	defer d.mu.Unlock()
	if d.closed {
		return
	}
	now := d.now()
	p := d.pending[t]
	if p == nil {
		p = &pendingRefresh{reason: r, first: now}
		d.pending[t] = p
		p.timer = time.AfterFunc(d.window, func() { d.fire(t, p) })
		return
	}
	p.reason = merge(p.reason, r)
	wait := min(d.window, p.first.Add(d.maxWait).Sub(now))
	p.timer.Reset(max(wait, 0))
}

func (d *debouncer) fire(t target, p *pendingRefresh) {
	d.mu.Lock()
	if d.closed || d.pending[t] != p {
		d.mu.Unlock()
		return // closed, or a re-armed timer of an entry already emitted
	}
	delete(d.pending, t)
	reason := p.reason
	d.mu.Unlock()
	d.emit(t, reason)
}

// close stops the timers; pending refreshes are dropped and returned.
func (d *debouncer) close() int {
	d.mu.Lock()
	defer d.mu.Unlock()
	d.closed = true
	n := len(d.pending)
	for _, p := range d.pending {
		p.timer.Stop()
	}
	clear(d.pending)
	return n
}
