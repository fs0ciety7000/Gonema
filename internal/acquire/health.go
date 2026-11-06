package acquire

import (
	"context"
	"fmt"
	"log/slog"

	"github.com/fs0ciety7000/gonema/internal/events"
	"github.com/fs0ciety7000/gonema/internal/search"
	"github.com/fs0ciety7000/gonema/internal/store"
	"github.com/fs0ciety7000/gonema/internal/store/db"
)

// HealthEvent is the data of health.changed events about indexers and
// download clients.
type HealthEvent struct {
	Source        string `json:"source" enum:"indexer,downloadClient"`
	ID            int64  `json:"id"`
	Name          string `json:"name"`
	OK            bool   `json:"ok"`
	Message       string `json:"message,omitempty"`
	DisabledUntil string `json:"disabledUntil,omitempty"`
}

// restoreHealth loads the persisted breaker states into the orchestrator.
func (s *Service) restoreHealth(ctx context.Context) error {
	rows, err := s.st.Read().ListIndexers(ctx)
	if err != nil {
		return store.MapError(fmt.Errorf("acquire: restore indexer health: %w", err))
	}
	s.healthMu.Lock()
	defer s.healthMu.Unlock()
	for i := range rows {
		r := &rows[i]
		h := healthOf(r)
		s.health[r.ID] = h
		if r.FailureCount == 0 && r.DisabledUntil == nil {
			continue
		}
		s.orch.Restore(r.ID, h)
		s.log.Info("indexer health restored", slog.String("indexer", r.Name), slog.Int64("failures", r.FailureCount),
			slog.Any("disabled_until", timePtr(r.DisabledUntil)))
	}
	return nil
}

// healthOf converts the persisted columns into a breaker state. The open
// escalation step is not persisted: a restored open breaker counts as the
// first open.
func healthOf(r *db.Indexer) search.Health {
	h := search.Health{ConsecutiveFailures: int(r.FailureCount), LastError: deref(r.LastError)}
	if r.LastOkAt != nil {
		h.LastOKAt = store.FromMillis(*r.LastOkAt)
	}
	if r.DisabledUntil != nil {
		h.State, h.Opens, h.DisabledUntil = search.Open, 1, store.FromMillis(*r.DisabledUntil)
	}
	return h
}

// persistHealth writes the breaker state of the indexers to their columns when
// it changed, and publishes health.changed when an indexer is paused or back.
func (s *Service) persistHealth(ctx context.Context, rows []db.Indexer) {
	ctx = context.WithoutCancel(ctx)
	for i := range rows {
		r := &rows[i]
		h, ok := s.orch.Health(r.ID)
		if !ok {
			continue
		}
		s.healthMu.Lock()
		prev, known := s.health[r.ID]
		same := known && prev.ConsecutiveFailures == h.ConsecutiveFailures && prev.DisabledUntil.Equal(h.DisabledUntil) &&
			prev.LastError == h.LastError && prev.LastOKAt.Equal(h.LastOKAt)
		if !same {
			s.health[r.ID] = h
		}
		s.healthMu.Unlock()
		if same {
			continue
		}
		p := db.UpdateIndexerHealthParams{
			ID: r.ID, FailureCount: int64(h.ConsecutiveFailures), DisabledUntil: millisPtr(h.DisabledUntil),
			LastOkAt: millisPtr(h.LastOKAt),
		}
		if h.LastError != "" && h.ConsecutiveFailures > 0 {
			p.LastError = &h.LastError
		}
		if h.State == search.Closed {
			p.DisabledUntil = nil
		}
		if err := store.RequireRow(s.st.Write().UpdateIndexerHealth(ctx, p)); err != nil {
			s.log.Error("persist indexer health", slog.String("indexer", r.Name), slog.Any("error", err))
			continue
		}
		wasDown, isDown := known && !prev.DisabledUntil.IsZero() && prev.State != search.Closed, p.DisabledUntil != nil
		if wasDown != isDown || (!known && isDown) {
			ev := HealthEvent{Source: "indexer", ID: r.ID, Name: r.Name, OK: !isDown}
			if isDown {
				ev.Message = h.LastError
				ev.DisabledUntil = h.DisabledUntil.UTC().Format("2006-01-02T15:04:05Z")
			}
			s.publish(events.Event{Type: events.HealthChanged, Data: ev})
		}
	}
}

func (s *Service) publishClientHealth(r *db.DownloadClient, err error) {
	ev := HealthEvent{Source: "downloadClient", ID: r.ID, Name: r.Name, OK: err == nil}
	if err != nil {
		ev.Message = err.Error()
	}
	s.publish(events.Event{Type: events.HealthChanged, Data: ev})
}
