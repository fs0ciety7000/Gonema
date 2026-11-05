package notify

import (
	"context"
	"fmt"
	"log/slog"
	"slices"
	"time"

	"github.com/fs0ciety7000/gonema/internal/events"
	"github.com/fs0ciety7000/gonema/internal/store"
	"github.com/fs0ciety7000/gonema/internal/store/db"
)

// followed reports the event types the dispatcher subscribes to.
func followed(t events.Type) bool {
	switch t {
	case EventDownloadGrabbed, EventDownloadFailed, EventDownloadUpdated, EventFileImported,
		events.HealthIssueRaised, events.HealthIssueResolved, events.MovieDeleted, events.MovieFileRenamed, events.MovieFileDeleted:
		return true
	}
	return false
}

// Start subscribes to the bus and starts the worker pool. The listener never
// blocks the bus (its subscription is buffered) and never waits for a
// delivery (a full queue drops the message, logged).
func (s *Service) Start() {
	for range s.workers {
		s.wg.Add(1)
		go s.worker()
	}
	if s.bus == nil {
		return
	}
	s.sub = s.bus.Subscribe(eventBuffer, func(e *events.Event) bool { return followed(e.Type) })
	s.wg.Add(1)
	go s.listen()
}

// Close stops the dispatch: queued messages are dropped, deliveries in
// progress are canceled, and Close waits for the workers. Safe to call more
// than once.
func (s *Service) Close() {
	s.once.Do(func() {
		if s.sub != nil {
			s.sub.Close()
		}
		s.cancel()
		s.wg.Wait()
	})
}

// Dropped returns the number of messages dropped because the queue was full.
func (s *Service) Dropped() uint64 { return s.dropped.Load() }

func (s *Service) listen() {
	defer s.wg.Done()
	var reported uint64
	for e := range s.sub.C() {
		if d := s.sub.Dropped(); d > reported {
			s.log.Warn("notifications fell behind: some events were not notified", slog.Uint64("dropped", d-reported))
			reported = d
		}
		s.dispatch(&e)
	}
}

// dispatch builds the message of an event and queues it for every enabled
// notification that wants it.
func (s *Service) dispatch(e *events.Event) {
	ctx, cancel := context.WithTimeout(s.ctx, 10*time.Second)
	defer cancel()
	sub := s.b.build(ctx, e)
	if sub == nil {
		return
	}
	rows, err := s.st.Read().ListEnabledNotifications(ctx)
	if err != nil {
		s.log.Error("list notifications", slog.Any("error", store.MapError(err)))
		return
	}
	for i := range rows {
		if wants(&rows[i], sub) {
			s.submit(sendJob{id: rows[i].ID, msg: sub.msg})
		}
	}
}

// wants applies the event selection and the tag filter of a notification:
// a notification with tags only receives messages about movies carrying one
// of them; health messages ignore tags; a notification never receives the
// health messages about itself.
func wants(r *db.Notification, sub *subject) bool {
	m := sub.msg
	if !slices.Contains(decodeJSON[[]EventKind](r.Events), m.Event) {
		return false
	}
	if m.Event == EventHealth {
		return m.HealthSource != CheckID || m.HealthID != r.ID
	}
	tags := decodeJSON[[]string](r.Tags)
	if len(tags) == 0 {
		return true
	}
	for _, t := range sub.tags {
		if slices.Contains(tags, t) {
			return true
		}
	}
	return false
}

func (s *Service) submit(j sendJob) {
	select {
	case s.jobs <- j:
	default:
		s.dropped.Add(1)
		s.log.Warn("notification dropped: too many waiting", slog.Int64("notification_id", j.id), slog.String("event", string(j.msg.Event)))
	}
}

func (s *Service) worker() {
	defer s.wg.Done()
	for {
		select {
		case <-s.ctx.Done():
			return
		case j := <-s.jobs:
			s.deliver(j)
		}
	}
}

// deliver sends one message with retries and records the outcome.
func (s *Service) deliver(j sendJob) {
	ctx, cancel := context.WithTimeout(s.ctx, sendTimeout)
	defer cancel()
	r, err := s.st.Read().GetNotification(ctx, j.id)
	if err != nil || !r.Enabled {
		return // removed or disabled meanwhile
	}
	n, err := s.notifier(&r)
	if err == nil {
		err = s.retry.Do(ctx, func(ctx context.Context) error { return n.Send(ctx, j.msg) })
	}
	if s.ctx.Err() != nil {
		return // shutdown: not a delivery failure
	}
	if err != nil {
		s.recordFailure(&r, err)
		return
	}
	s.log.Debug("notification sent", slog.String("notification", r.Name), slog.String("event", string(j.msg.Event)))
	s.recordOK(ctx, &r)
}

func nowMillis(now func() time.Time) *int64 {
	ms := store.Millis(now())
	return &ms
}

// recordOK clears the failures and publishes the recovery of a notification
// that had raised a health issue.
func (s *Service) recordOK(ctx context.Context, r *db.Notification) {
	ctx = context.WithoutCancel(ctx)
	if err := store.RequireRow(s.st.Write().RecordNotificationOK(ctx, db.RecordNotificationOKParams{ID: r.ID, At: nowMillis(s.now)})); err != nil {
		s.log.Error("record notification health", slog.Int64("notification_id", r.ID), slog.Any("error", err))
		return
	}
	if r.FailureCount >= FailuresBeforeHealth {
		s.publishHealth(r, nil)
	}
}

// recordFailure counts a failed delivery; FailuresBeforeHealth in a row raise
// a health issue (once).
func (s *Service) recordFailure(r *db.Notification, cause error) {
	msg := cause.Error() // redacted by the implementations
	s.log.Warn("notification not delivered", slog.String("notification", r.Name), slog.String("kind", r.Kind), slog.String("error", msg))
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	n, err := s.st.Write().RecordNotificationFailure(ctx, db.RecordNotificationFailureParams{ID: r.ID, LastError: &msg, At: nowMillis(s.now)})
	if err != nil {
		s.log.Error("record notification health", slog.Int64("notification_id", r.ID), slog.Any("error", store.MapError(err)))
		return
	}
	if n == FailuresBeforeHealth {
		s.publishHealth(r, cause)
	}
}

func (s *Service) publishHealth(r *db.Notification, cause error) {
	if s.bus == nil {
		return
	}
	ev := HealthEvent{Source: HealthSource, ID: r.ID, Name: r.Name, OK: cause == nil}
	if cause != nil {
		ev.Message = fmt.Sprintf("The last %d messages to %s were not delivered: %s. Gonema keeps trying with the next ones.",
			FailuresBeforeHealth, r.Name, cause.Error())
	}
	s.bus.Publish(events.Event{Type: events.HealthChanged, Data: ev})
}
