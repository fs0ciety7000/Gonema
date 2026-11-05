package events

import (
	"context"
	"encoding/json"
	"log/slog"
	"time"

	"github.com/fs0ciety7000/gonema/internal/store"
	"github.com/fs0ciety7000/gonema/internal/store/db"
)

// HistoryBuffer is the buffer of the history writer subscription. History is an
// audit trail: the buffer is large so that a burst never loses rows; a drop is
// logged at error level.
const HistoryBuffer = 4096

// HistoryWriter persists the events that carry a HistoryRecord into the history
// table. It runs until the bus is closed, then writes what is left in its buffer.
type HistoryWriter struct {
	st   *store.Store
	log  *slog.Logger
	sub  *Subscription
	done chan struct{}
}

// NewHistoryWriter subscribes to the bus and starts writing in a goroutine.
// Call Wait after closing the bus to let it finish.
func NewHistoryWriter(bus *Bus, st *store.Store, log *slog.Logger) *HistoryWriter {
	w := &HistoryWriter{
		st:   st,
		log:  log,
		sub:  bus.Subscribe(HistoryBuffer, func(e *Event) bool { return e.History != nil }),
		done: make(chan struct{}),
	}
	go w.run()
	return w
}

func (w *HistoryWriter) run() {
	defer close(w.done)
	var reported uint64
	for e := range w.sub.C() {
		if d := w.sub.Dropped(); d > reported {
			w.log.Error("history writer fell behind: events were not recorded", slog.Uint64("dropped", d-reported))
			reported = d
		}
		w.write(&e)
	}
}

func (w *HistoryWriter) write(e *Event) {
	h := e.History
	data := h.Data
	if data == nil {
		data = map[string]any{}
	}
	raw, err := json.Marshal(data)
	if err != nil {
		w.log.Error("history: encode data", slog.String("event", h.Event), slog.Any("error", err))
		raw = []byte("{}")
	}
	p := db.AppendHistoryParams{
		Event:       h.Event,
		SourceTitle: h.SourceTitle,
		Data:        string(raw),
		CreatedAt:   store.Millis(e.Time),
	}
	if e.MovieID != 0 {
		p.MovieID = &e.MovieID
	}
	if h.Quality != "" {
		p.Quality = &h.Quality
	}
	if h.DownloadID != 0 {
		p.DownloadID = &h.DownloadID
	}
	// Not tied to a request: the write must survive the publisher's context.
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	if _, err := w.st.Write().AppendHistory(ctx, p); err != nil {
		w.log.Error("history: append", slog.String("event", h.Event), slog.Int64("movie_id", e.MovieID),
			slog.Any("error", store.MapError(err)))
	}
}

// Wait blocks until the writer has drained its buffer after the bus was closed,
// or ctx is done.
func (w *HistoryWriter) Wait(ctx context.Context) error {
	select {
	case <-w.done:
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}
