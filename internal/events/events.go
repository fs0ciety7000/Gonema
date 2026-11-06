// Package events is the in-process event bus of Gonema.
//
// Publishers never block: each subscriber has a bounded buffer and an event that
// does not fit is dropped for that subscriber only (and counted). Subscribers that
// cannot afford losses (the history writer) use a large buffer and log drops.
//
// Every event gets a monotonically increasing ID. The bus keeps the last events
// in a ring so that an SSE client reconnecting with Last-Event-ID can catch up
// (best effort: older events are gone).
//
// Event data must be treated as immutable once published: the same value is
// handed to every subscriber. Publish values or pointers to fresh structs.
package events

import (
	"sync"
	"sync/atomic"
	"time"
)

// Type names an event. Types are part of the API (SSE "event:" field).
type Type string

// Event types.
const (
	MovieAdded   Type = "movie.added"
	MovieUpdated Type = "movie.updated"
	MovieDeleted Type = "movie.deleted"

	// MovieFileRenamed and MovieFileDeleted are published by the operations that
	// rename or recycle movie files; Data is a map with "path" (the movie
	// folder as Gonema sees it, optional). Media servers and notifications
	// follow them.
	MovieFileRenamed Type = "moviefile.renamed"
	MovieFileDeleted Type = "moviefile.deleted"

	CollectionUpdated Type = "collection.updated"
	RootFolderAdded   Type = "rootfolder.added"
	RootFolderDeleted Type = "rootfolder.deleted"
	SettingsChanged   Type = "settings.changed"

	JobStarted   Type = "job.started"
	JobCompleted Type = "job.completed"
	JobFailed    Type = "job.failed"

	HealthChanged Type = "health.changed"

	DownloadProgress Type = "download.progress"
)

// HistoryRecord is the history row an event stands for. Only events that carry
// one are written to the history table (see HistoryWriter).
type HistoryRecord struct {
	Event       string         // history.event enum: "metadata_changed", "grabbed"...
	SourceTitle string         // release or movie title shown in the history list
	Quality     string         // optional quality ID
	DownloadID  int64          // optional
	Data        map[string]any // reasons and details, stored as JSON
}

// Event is one published event.
type Event struct {
	ID      uint64         `json:"id"`
	Type    Type           `json:"type"`
	Time    time.Time      `json:"time"`
	MovieID int64          `json:"movieId,omitempty"`
	Data    any            `json:"data,omitempty"`
	History *HistoryRecord `json:"-"`
}

// Subscription receives events. Read C until it is closed (by Close or by the
// bus closing).
type Subscription struct {
	ch      chan Event
	filter  func(*Event) bool
	dropped atomic.Uint64
	bus     *Bus
	closed  bool // guarded by bus.mu
}

// C returns the channel delivering the events.
func (s *Subscription) C() <-chan Event { return s.ch }

// Dropped returns how many events did not fit in the buffer.
func (s *Subscription) Dropped() uint64 { return s.dropped.Load() }

// Close unsubscribes and closes C. Safe to call more than once.
func (s *Subscription) Close() { s.bus.unsubscribe(s) }

// Options configures a Bus.
type Options struct {
	ReplaySize int              // events kept for Since; default 256
	Now        func() time.Time // default time.Now
}

// Bus fans events out to subscribers. It is safe for concurrent use.
type Bus struct {
	now func() time.Time

	mu      sync.Mutex
	subs    map[*Subscription]struct{}
	nextID  uint64
	ring    []Event // circular, len == cap once full
	ringPos int
	closed  bool

	dropped atomic.Uint64
}

// New returns a bus.
func New(opts Options) *Bus {
	if opts.ReplaySize <= 0 {
		opts.ReplaySize = 256
	}
	if opts.Now == nil {
		opts.Now = time.Now
	}
	return &Bus{
		now:  opts.Now,
		subs: make(map[*Subscription]struct{}),
		ring: make([]Event, 0, opts.ReplaySize),
	}
}

// Subscribe returns a subscription with a buffer of the given size (minimum 1).
// filter, when not nil, selects the events delivered.
func (b *Bus) Subscribe(buffer int, filter func(*Event) bool) *Subscription {
	s := &Subscription{ch: make(chan Event, max(buffer, 1)), filter: filter, bus: b}
	b.mu.Lock()
	defer b.mu.Unlock()
	if b.closed {
		s.closed = true
		close(s.ch)
		return s
	}
	b.subs[s] = struct{}{}
	return s
}

func (b *Bus) unsubscribe(s *Subscription) {
	b.mu.Lock()
	defer b.mu.Unlock()
	if s.closed {
		return
	}
	s.closed = true
	delete(b.subs, s)
	close(s.ch)
}

// Publish assigns an ID and a time to the event and delivers it to every
// subscriber whose buffer has room. It never blocks. It returns the event as
// delivered. Publishing on a closed bus is a no-op.
func (b *Bus) Publish(e Event) Event {
	b.mu.Lock()
	defer b.mu.Unlock()
	if b.closed {
		return e
	}
	b.nextID++
	e.ID = b.nextID
	if e.Time.IsZero() {
		e.Time = b.now().UTC()
	}
	if len(b.ring) < cap(b.ring) {
		b.ring = append(b.ring, e)
	} else {
		b.ring[b.ringPos] = e
		b.ringPos = (b.ringPos + 1) % len(b.ring)
	}
	for s := range b.subs {
		if s.filter != nil && !s.filter(&e) {
			continue
		}
		select {
		case s.ch <- e:
		default:
			s.dropped.Add(1)
			b.dropped.Add(1)
		}
	}
	return e
}

// Since returns the retained events with an ID greater than id, oldest first.
// complete is false when events after id were already evicted (the caller
// missed some and should refetch its state).
func (b *Bus) Since(id uint64) (evs []Event, complete bool) {
	b.mu.Lock()
	defer b.mu.Unlock()
	if id >= b.nextID {
		return nil, true
	}
	n := len(b.ring)
	for i := range n {
		e := b.ring[(b.ringPos+i)%n]
		if e.ID > id {
			evs = append(evs, e)
		}
	}
	complete = len(evs) > 0 && evs[0].ID == id+1
	return evs, complete
}

// LastID returns the ID of the last published event (0 if none).
func (b *Bus) LastID() uint64 {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.nextID
}

// Dropped returns the total number of deliveries dropped, all subscribers.
func (b *Bus) Dropped() uint64 { return b.dropped.Load() }

// Subscribers returns the number of active subscriptions.
func (b *Bus) Subscribers() int {
	b.mu.Lock()
	defer b.mu.Unlock()
	return len(b.subs)
}

// Close closes every subscription; later Publish calls are ignored.
func (b *Bus) Close() {
	b.mu.Lock()
	defer b.mu.Unlock()
	if b.closed {
		return
	}
	b.closed = true
	for s := range b.subs {
		s.closed = true
		close(s.ch)
	}
	clear(b.subs)
}
