package events

import (
	"context"
	"io"
	"log/slog"
	"math"
	"sync"
	"testing"
	"time"

	"github.com/fs0ciety7000/gonema/internal/store/db"
	"github.com/fs0ciety7000/gonema/internal/store/storetest"
)

func TestPublishFanOut(t *testing.T) {
	b := New(Options{})
	a := b.Subscribe(4, nil)
	movies := b.Subscribe(4, func(e *Event) bool { return e.Type == MovieAdded })
	b.Publish(Event{Type: JobStarted})
	b.Publish(Event{Type: MovieAdded, MovieID: 7})

	if e := <-a.C(); e.ID != 1 || e.Type != JobStarted || e.Time.IsZero() {
		t.Errorf("first event = %+v", e)
	}
	if e := <-a.C(); e.ID != 2 {
		t.Errorf("second event id = %d, want 2", e.ID)
	}
	if e := <-movies.C(); e.Type != MovieAdded || e.MovieID != 7 {
		t.Errorf("filtered event = %+v", e)
	}
	select {
	case e := <-movies.C():
		t.Errorf("filter let through %+v", e)
	default:
	}
}

// A subscriber that never reads must not slow down nor block publishers.
func TestSlowSubscriberDoesNotBlock(t *testing.T) {
	b := New(Options{})
	slow := b.Subscribe(2, nil)
	fast := b.Subscribe(1000, nil)

	done := make(chan struct{})
	go func() {
		defer close(done)
		for range 1000 {
			b.Publish(Event{Type: DownloadProgress})
		}
	}()
	select {
	case <-done:
	case <-time.After(5 * time.Second):
		t.Fatal("Publish blocked on a slow subscriber")
	}
	if got := slow.Dropped(); got != 998 {
		t.Errorf("slow dropped = %d, want 998", got)
	}
	if got := fast.Dropped(); got != 0 {
		t.Errorf("fast dropped = %d, want 0", got)
	}
	if got := b.Dropped(); got != 998 {
		t.Errorf("bus dropped = %d, want 998", got)
	}
	if got := len(fast.C()); got != 1000 {
		t.Errorf("fast buffered %d events, want 1000", got)
	}
}

func TestConcurrentPublishSubscribe(t *testing.T) {
	b := New(Options{})
	var wg sync.WaitGroup
	for range 8 {
		wg.Go(func() {
			for range 200 {
				b.Publish(Event{Type: JobCompleted})
			}
		})
		wg.Go(func() {
			for range 50 {
				s := b.Subscribe(1, nil)
				s.Close()
				s.Close() // idempotent
			}
		})
	}
	wg.Wait()
	if got := b.LastID(); got != 1600 {
		t.Errorf("LastID = %d, want 1600", got)
	}
	b.Close()
	b.Publish(Event{Type: JobCompleted}) // ignored, no panic
	s := b.Subscribe(1, nil)
	if _, ok := <-s.C(); ok {
		t.Error("subscription on a closed bus is open")
	}
}

func TestSince(t *testing.T) {
	b := New(Options{ReplaySize: 3})
	for range 5 {
		b.Publish(Event{Type: JobStarted})
	}
	ids := func(evs []Event) []uint64 {
		var out []uint64
		for _, e := range evs {
			out = append(out, e.ID)
		}
		return out
	}
	tests := []struct {
		since    uint64
		want     []uint64
		complete bool
	}{
		{since: 5, want: nil, complete: true},
		{since: 3, want: []uint64{4, 5}, complete: true},
		{since: 2, want: []uint64{3, 4, 5}, complete: true},
		{since: 1, want: []uint64{3, 4, 5}, complete: false},
		{since: 99, want: nil, complete: true},
	}
	for _, tt := range tests {
		evs, complete := b.Since(tt.since)
		got := ids(evs)
		if len(got) != len(tt.want) || complete != tt.complete {
			t.Errorf("Since(%d) = %v, %v; want %v, %v", tt.since, got, complete, tt.want, tt.complete)
			continue
		}
		for i := range got {
			if got[i] != tt.want[i] {
				t.Errorf("Since(%d) = %v, want %v", tt.since, got, tt.want)
			}
		}
	}
}

func TestHistoryWriter(t *testing.T) {
	st := storetest.New(t)
	b := New(Options{})
	w := NewHistoryWriter(b, st, slog.New(slog.NewTextHandler(io.Discard, nil)))

	b.Publish(Event{Type: MovieUpdated, MovieID: 42, History: &HistoryRecord{
		Event: "metadata_changed", SourceTitle: "Fight Club", Data: map[string]any{"changes": []string{"status"}},
	}})
	b.Publish(Event{Type: JobStarted}) // no history
	b.Close()
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if err := w.Wait(ctx); err != nil {
		t.Fatal(err)
	}

	rows, err := st.Read().ListRecentHistory(ctx, db.ListRecentHistoryParams{BeforeID: math.MaxInt64, Limit: 10})
	if err != nil {
		t.Fatal(err)
	}
	if len(rows) != 1 {
		t.Fatalf("history rows = %d, want 1", len(rows))
	}
	r := rows[0]
	if r.Event != "metadata_changed" || r.MovieID == nil || *r.MovieID != 42 || r.SourceTitle != "Fight Club" ||
		r.Data != `{"changes":["status"]}` {
		t.Errorf("row = %+v", r)
	}
}
