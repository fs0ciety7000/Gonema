package api

import (
	"context"
	"errors"
	"net/http"

	"github.com/danielgtaylor/huma/v2"

	"github.com/fs0ciety7000/gonema/internal/events"
	"github.com/fs0ciety7000/gonema/internal/health"
)

// RegisterHealthRoutes adds the health endpoints: current issues, checks run
// on demand, and the thresholds. Call it once, after New and before serving.
func (s *Server) RegisterHealthRoutes(h *health.Scheduler, settings health.SettingsStore) {
	s.health = h
	tags := []string{"Health"}
	huma.Register(s.api, huma.Operation{
		OperationID: "list-health-issues", Method: http.MethodGet, Path: "/health/issues", Summary: "Current health issues",
		Description: "Most urgent first. Each issue says what happened, what Gonema does about it and how to fix it. " +
			"Changes stream as health.issue_raised and health.issue_resolved events. An empty list means all is well.",
		Tags: tags,
	}, func(context.Context, *struct{}) (*struct{ Body []health.Issue }, error) {
		return &struct{ Body []health.Issue }{h.Issues()}, nil
	})
	huma.Register(s.api, huma.Operation{
		OperationID: "run-health-checks", Method: http.MethodPost, Path: "/health/run", Summary: "Run every health check now",
		Description: "Waits for the checks (up to a minute: some ask the indexers' breaker, the download clients and TMDB) " +
			"and returns the issues.",
		Tags: tags,
	}, func(ctx context.Context, _ *struct{}) (*struct{ Body []health.Issue }, error) {
		return &struct{ Body []health.Issue }{h.RunAll(ctx)}, nil
	})
	huma.Register(s.api, huma.Operation{
		OperationID: "list-health-checks", Method: http.MethodGet, Path: "/health/checks", Summary: "Health checks and their last run",
		Tags: tags,
	}, func(context.Context, *struct{}) (*struct{ Body []health.CheckStatus }, error) {
		return &struct{ Body []health.CheckStatus }{h.Checks()}, nil
	})
	huma.Register(s.api, huma.Operation{
		OperationID: "get-health-settings", Method: http.MethodGet, Path: "/settings/health", Summary: "Health check thresholds",
		Tags: []string{"Settings"},
	}, func(ctx context.Context, _ *struct{}) (*struct{ Body health.Settings }, error) {
		return &struct{ Body health.Settings }{settings.Get(ctx)}, nil
	})
	huma.Register(s.api, huma.Operation{
		OperationID: "set-health-settings", Method: http.MethodPut, Path: "/settings/health", Summary: "Change health check thresholds",
		Description: "Free space (share of the disk or GB) and recycle bin size. Out-of-range values are refused (422).",
		Tags:        []string{"Settings"},
	}, func(ctx context.Context, in *struct{ Body health.Settings }) (*struct{ Body health.Settings }, error) {
		out, err := settings.Set(ctx, in.Body)
		if err != nil {
			var um userMessage
			if errors.Is(err, health.ErrInvalid) && errors.As(err, &um) {
				return nil, huma.Error422UnprocessableEntity(um.UserMessage())
			}
			return nil, s.problem(ctx, err, false)
		}
		if s.d.Bus != nil {
			s.d.Bus.Publish(events.Event{Type: events.SettingsChanged, Data: map[string]any{"setting": "health"}})
		}
		return &struct{ Body health.Settings }{out}, nil
	})
}
