package mediaserver

import (
	"context"
	"fmt"
	"strconv"
	"time"

	"github.com/fs0ciety7000/gonema/internal/events"
	"github.com/fs0ciety7000/gonema/internal/store"
)

// CheckID is the health check of the media servers (the Source of its issues).
const CheckID = "media-servers"

// HealthIssues lists the enabled media servers whose last refresh failed
// (after retries). It is the Run function of the "media-servers" health check
// (internal/health), triggered by the health.changed events this package
// publishes.
func (s *Service) HealthIssues(ctx context.Context) ([]events.HealthIssue, error) {
	rows, err := s.st.Read().ListEnabledMediaServers(ctx)
	if err != nil {
		return nil, store.MapError(fmt.Errorf("mediaserver: list for health: %w", err))
	}
	var out []events.HealthIssue
	for i := range rows {
		r := &rows[i]
		if r.FailureCount == 0 {
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
		kind := Kind(r.Kind).Label()
		fix := []string{
			"Check that " + r.Name + " is running and reachable at " + r.BaseUrl + ".",
			"Open Settings > Media servers, check the credentials, then use Test.",
		}
		if Kind(r.Kind) != Jellyfin {
			fix = append(fix, "Check the path mappings: Gonema's movie folder must map to the folder "+kind+" sees.")
		}
		out = append(out, events.HealthIssue{
			ID:       "mediaserver.failing:" + strconv.FormatInt(r.ID, 10),
			Severity: events.SeverityWarning,
			Title:    r.Name + " is not refreshing its library",
			Message: fmt.Sprintf("Gonema could not ask %s to rescan a movie folder (%s). Imports go on; %s shows them after its next scheduled scan.",
				r.Name, reason, kind),
			Fix:    fix,
			Link:   "/settings/media-servers/" + strconv.FormatInt(r.ID, 10),
			Source: CheckID,
			Since:  since.UTC().Truncate(time.Second),
		})
	}
	return out, nil
}
