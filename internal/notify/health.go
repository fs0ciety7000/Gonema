package notify

import (
	"context"
	"fmt"
	"strconv"
	"time"

	"github.com/fs0ciety7000/gonema/internal/events"
	"github.com/fs0ciety7000/gonema/internal/store"
)

// CheckID is the health check of the notifications (the Source of its issues).
const CheckID = "notifications"

// HealthIssues lists the enabled notifications whose last
// FailuresBeforeHealth deliveries failed. It is the Run function of the
// "notifications" health check (internal/health), triggered by the
// health.changed events this package publishes.
func (s *Service) HealthIssues(ctx context.Context) ([]events.HealthIssue, error) {
	rows, err := s.st.Read().ListEnabledNotifications(ctx)
	if err != nil {
		return nil, store.MapError(fmt.Errorf("notify: list for health: %w", err))
	}
	var out []events.HealthIssue
	for i := range rows {
		r := &rows[i]
		if r.FailureCount < FailuresBeforeHealth {
			continue
		}
		since := s.now()
		if r.LastFailedAt != nil {
			since = store.FromMillis(*r.LastFailedAt)
		}
		reason := "unknown error"
		if r.LastError != nil {
			reason = *r.LastError
		}
		out = append(out, events.HealthIssue{
			ID:       "notification.failing:" + strconv.FormatInt(r.ID, 10),
			Severity: events.SeverityWarning,
			Title:    r.Name + " is not delivering messages",
			Message: fmt.Sprintf("The last %d messages to %s were not delivered (%s). Gonema keeps sending the next ones.",
				r.FailureCount, r.Name, reason),
			Fix: []string{
				"Check that the " + Kind(r.Kind).Label() + " service is reachable from the Gonema server.",
				"Open Settings > Notifications, check the settings and secrets, then send a test message.",
			},
			Link:   "/settings/notifications/" + strconv.FormatInt(r.ID, 10),
			Source: CheckID,
			Since:  since.UTC().Truncate(time.Second),
		})
	}
	return out, nil
}
