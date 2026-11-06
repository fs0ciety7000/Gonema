package api

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"strconv"

	"github.com/danielgtaylor/huma/v2"

	"github.com/fs0ciety7000/gonema/internal/acquire"
	"github.com/fs0ciety7000/gonema/internal/jobs"
	"github.com/fs0ciety7000/gonema/internal/library"
	"github.com/fs0ciety7000/gonema/internal/store"
)

// Bulk search filters of POST /movies/search.
const (
	bulkMissingReleased = "missing_released"
	bulkCutoffUnmet     = "cutoff_unmet"
	bulkSelection       = "selection"
)

// BulkSearchRequest asks for automatic searches of several movies: either ids
// or a filter.
type BulkSearchRequest struct {
	IDs    []int64 `json:"ids,omitempty" maxItems:"5000" doc:"Library movie ids, searched in this order"`
	Filter string  `json:"filter,omitempty" enum:"missing_released,cutoff_unmet" doc:"missing_released: the Wanted > Missing list (monitored, no file, released enough for the minimum availability), the dashboard's \"Search the N released\"; cutoff_unmet: the Wanted > Cutoff unmet list"`
}

// BulkSearchStarted is the answer of a queued bulk search.
type BulkSearchStarted struct {
	JobID    int64  `json:"jobId"`
	Total    int    `json:"total" doc:"Movies queued"`
	Label    string `json:"label" enum:"missing_released,cutoff_unmet,selection"`
	Location string `json:"location" doc:"Path of the progress (GET), also the cancel endpoint (DELETE)"`
}

type bulkJobParam struct {
	JobID int64 `path:"jobId" minimum:"1" doc:"Job id of the bulk search"`
}

func (s *Server) bulkSearchRoutes(a *acquire.Service) {
	tags := []string{"Search"}
	huma.Register(s.api, huma.Operation{
		OperationID: "bulk-search-movies", Method: http.MethodPost, Path: "/movies/search",
		Summary: "Search several movies automatically",
		Description: "Queues one background job that runs the automatic search of each movie, one at a time, spaced out, waiting " +
			"while every indexer is paused by its circuit breaker (rate limit, repeated errors). Each grab is written to history. " +
			"Progress streams as bulksearch.progress events (bulksearch.finished at the end) and at GET /bulk-searches/{jobId}; " +
			"DELETE /bulk-searches/{jobId} cancels it. Give ids or a filter, not both. One bulk search at a time (409 otherwise). " +
			"Movies that are unmonitored or not available yet are skipped.",
		Tags: tags, DefaultStatus: http.StatusAccepted,
		Errors: []int{http.StatusConflict, http.StatusUnprocessableEntity},
	}, func(ctx context.Context, in *struct{ Body BulkSearchRequest }) (*struct {
		Status int
		Body   BulkSearchStarted
	}, error) {
		b := in.Body
		var ids []int64
		label := bulkSelection
		switch {
		case len(b.IDs) > 0 && b.Filter != "":
			return nil, huma.Error422UnprocessableEntity("Give either ids or a filter, not both.")
		case len(b.IDs) > 0:
			ids = b.IDs
		case b.Filter != "":
			wanted := library.WantedMissing
			if b.Filter == bulkCutoffUnmet {
				wanted = library.WantedCutoffUnmet
			}
			page, err := s.d.Library.ListMovies(ctx, library.ListQuery{All: true, Filter: library.ListFilter{Wanted: wanted}})
			if err != nil {
				return nil, s.problem(ctx, err, false)
			}
			for _, m := range page.Movies {
				ids = append(ids, m.ID)
			}
			label = b.Filter
			if len(ids) == 0 {
				return nil, huma.Error422UnprocessableEntity("No movie is on this list: there is nothing to search.")
			}
		default:
			return nil, huma.Error422UnprocessableEntity("Give the ids of the movies to search, or a filter.")
		}
		job, total, err := a.StartBulkSearch(ctx, ids, label)
		if err != nil {
			return nil, s.problem(ctx, err, false)
		}
		out := &struct {
			Status int
			Body   BulkSearchStarted
		}{Status: http.StatusAccepted}
		out.Body = BulkSearchStarted{JobID: job.ID, Total: total, Label: label, Location: s.d.BaseURL + Prefix + "/bulk-searches/" + strconv.FormatInt(job.ID, 10)}
		return out, nil
	})

	huma.Register(s.api, huma.Operation{
		OperationID: "get-bulk-search", Method: http.MethodGet, Path: "/bulk-searches/{jobId}", Summary: "Progress of a bulk search",
		Description: "Live counts while it runs in this process; after a restart, the state of its job with the total only.",
		Tags:        tags,
	}, func(ctx context.Context, in *bulkJobParam) (*struct{ Body acquire.BulkSearchProgress }, error) {
		p, err := s.bulkProgress(ctx, a, in.JobID)
		if err != nil {
			return nil, err
		}
		return &struct{ Body acquire.BulkSearchProgress }{p}, nil
	})

	huma.Register(s.api, huma.Operation{
		OperationID: "cancel-bulk-search", Method: http.MethodDelete, Path: "/bulk-searches/{jobId}", Summary: "Cancel a bulk search",
		Description: "A queued bulk search is cancelled at once; a running one stops after the current movie's search (bulksearch.finished " +
			"with state cancelled). Grabs already made stay. 409 when it already ended.",
		Tags: tags, DefaultStatus: http.StatusAccepted,
		Errors: []int{http.StatusNotFound, http.StatusConflict},
	}, func(ctx context.Context, in *bulkJobParam) (*struct{ Body acquire.BulkSearchProgress }, error) {
		if _, err := s.bulkProgress(ctx, a, in.JobID); err != nil {
			return nil, err
		}
		if err := s.d.Jobs.Cancel(ctx, in.JobID); err != nil {
			if errors.Is(err, jobs.ErrNotCancellable) {
				return nil, huma.Error409Conflict("This bulk search already ended.")
			}
			return nil, s.problem(ctx, err, false)
		}
		p, err := s.bulkProgress(ctx, a, in.JobID)
		if err != nil {
			return nil, err
		}
		return &struct{ Body acquire.BulkSearchProgress }{p}, nil
	})
}

// bulkProgress returns the progress of a bulk search job: live when this
// process runs it, else from the job row.
func (s *Server) bulkProgress(ctx context.Context, a *acquire.Service, jobID int64) (acquire.BulkSearchProgress, error) {
	job, err := s.d.Store.Read().GetJob(ctx, jobID)
	if err != nil {
		if errors.Is(store.MapError(err), store.ErrNotFound) {
			return acquire.BulkSearchProgress{}, huma.Error404NotFound("There is no bulk search with this id.")
		}
		return acquire.BulkSearchProgress{}, s.problem(ctx, store.MapError(err), false)
	}
	if job.Kind != acquire.JobBulkSearch {
		return acquire.BulkSearchProgress{}, huma.Error404NotFound("There is no bulk search with this id.")
	}
	p, live := a.BulkSearch(jobID)
	if !live {
		var pl acquire.BulkSearchPayload
		if json.Unmarshal([]byte(job.Payload), &pl) == nil {
			p = acquire.BulkSearchProgress{JobID: jobID, Label: pl.Label, Total: len(pl.MovieIDs)}
		}
		p.UpdatedAt = store.FromMillis(job.CreatedAt).UTC()
	}
	// The job row is the authority on the final states.
	switch job.Status {
	case "pending":
		if !live || p.State == "" {
			p.State = "pending"
		}
	case "cancelled":
		p.State = acquire.BulkCancelled
	case "failed":
		p.State = acquire.BulkStopped
		if job.LastError != nil && p.Reason == "" {
			p.Reason = "The bulk search failed; the logs have the details."
		}
	case "done":
		if p.State != acquire.BulkStopped {
			p.State = acquire.BulkFinished
		}
	}
	return p, nil
}
