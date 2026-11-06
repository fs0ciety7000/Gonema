package api

import (
	"context"
	"fmt"
	"net/http"

	"github.com/danielgtaylor/huma/v2"

	"github.com/fs0ciety7000/gonema/internal/acquire"
	"github.com/fs0ciety7000/gonema/internal/jobs"
)

// Acquisition endpoints: indexers, download clients, remote path mappings,
// searches, the queue and the blocklist. Secrets are write-only and download
// links never appear in a response: releases are grabbed by opaque key.

type indexerBody struct {
	Body acquire.IndexerInput
}

type indexerIDBody struct {
	IDParam
	Body acquire.IndexerInput
}

type clientBody struct {
	Body acquire.DownloadClientInput
}

type clientIDBody struct {
	IDParam
	Body acquire.DownloadClientInput
}

type mappingBody struct {
	Body acquire.RemotePathMappingInput
}

type mappingIDBody struct {
	IDParam
	Body acquire.RemotePathMappingInput
}

type testOutput struct {
	Body acquire.TestResult
}

// SearchStarted is the answer of a started interactive search.
type SearchStarted struct {
	SearchID string `json:"searchId"`
	Location string `json:"location" doc:"Path of the search state"`
	acquire.SearchSnapshot
}

func (s *Server) acquireRoutes() {
	a := s.d.Acquire

	// ------------------------------------------------------------- indexers
	ixTags := []string{"Indexers"}
	huma.Register(s.api, huma.Operation{
		OperationID: "list-indexers", Method: http.MethodGet, Path: "/indexers", Summary: "List indexers",
		Description: "Preferred first. API keys are never returned: apiKeySet tells whether one is stored.", Tags: ixTags,
	}, func(ctx context.Context, _ *struct{}) (*struct{ Body []acquire.Indexer }, error) {
		list, err := a.ListIndexers(ctx)
		if err != nil {
			return nil, s.problem(ctx, err, false)
		}
		return &struct{ Body []acquire.Indexer }{list}, nil
	})
	huma.Register(s.api, huma.Operation{
		OperationID: "get-indexer", Method: http.MethodGet, Path: "/indexers/{id}", Summary: "Get an indexer", Tags: ixTags,
	}, func(ctx context.Context, in *IDParam) (*struct{ Body acquire.Indexer }, error) {
		ix, err := a.GetIndexer(ctx, in.ID)
		if err != nil {
			return nil, s.problem(ctx, err, false)
		}
		return &struct{ Body acquire.Indexer }{ix}, nil
	})
	huma.Register(s.api, huma.Operation{
		OperationID: "create-indexer", Method: http.MethodPost, Path: "/indexers", Summary: "Add an indexer",
		Description: "The API key is stored encrypted. Use POST /indexers/test first to check the settings.",
		Tags:        ixTags, DefaultStatus: http.StatusCreated,
	}, func(ctx context.Context, in *indexerBody) (*struct{ Body acquire.Indexer }, error) {
		ix, err := a.CreateIndexer(ctx, in.Body)
		if err != nil {
			return nil, s.problem(ctx, err, false)
		}
		return &struct{ Body acquire.Indexer }{ix}, nil
	})
	huma.Register(s.api, huma.Operation{
		OperationID: "update-indexer", Method: http.MethodPut, Path: "/indexers/{id}", Summary: "Replace indexer settings",
		Description: "Omit apiKey to keep the stored key. The health is reset.", Tags: ixTags,
	}, func(ctx context.Context, in *indexerIDBody) (*struct{ Body acquire.Indexer }, error) {
		ix, err := a.UpdateIndexer(ctx, in.ID, in.Body)
		if err != nil {
			return nil, s.problem(ctx, err, false)
		}
		return &struct{ Body acquire.Indexer }{ix}, nil
	})
	huma.Register(s.api, huma.Operation{
		OperationID: "delete-indexer", Method: http.MethodDelete, Path: "/indexers/{id}", Summary: "Remove an indexer",
		Tags: ixTags, DefaultStatus: http.StatusNoContent,
	}, func(ctx context.Context, in *IDParam) (*struct{}, error) {
		if err := a.DeleteIndexer(ctx, in.ID); err != nil {
			return nil, s.problem(ctx, err, false)
		}
		return nil, nil
	})
	huma.Register(s.api, huma.Operation{
		OperationID: "test-indexer", Method: http.MethodPost, Path: "/indexers/{id}/test", Summary: "Test a saved indexer",
		Description: "Always 200: ok and a plain-language message. A success resets the indexer health.", Tags: ixTags,
	}, func(ctx context.Context, in *IDParam) (*testOutput, error) {
		r, err := a.TestIndexer(ctx, in.ID)
		if err != nil {
			return nil, s.problem(ctx, err, false)
		}
		return &testOutput{r}, nil
	})
	huma.Register(s.api, huma.Operation{
		OperationID: "test-indexer-settings", Method: http.MethodPost, Path: "/indexers/test", Summary: "Test indexer settings before saving",
		Description: "With ?id= and no apiKey, the stored key of that indexer is used.", Tags: ixTags,
	}, func(ctx context.Context, in *struct {
		ID   int64 `query:"id" minimum:"0" doc:"Stored indexer whose key to use when apiKey is omitted"`
		Body acquire.IndexerInput
	}) (*testOutput, error) {
		r, err := a.TestIndexerConfig(ctx, in.ID, in.Body)
		if err != nil {
			return nil, s.problem(ctx, err, false)
		}
		return &testOutput{r}, nil
	})

	// ------------------------------------------------------------- download clients
	dcTags := []string{"Download clients"}
	huma.Register(s.api, huma.Operation{
		OperationID: "list-download-clients", Method: http.MethodGet, Path: "/download-clients", Summary: "List download clients",
		Description: "Passwords and API keys are never returned.", Tags: dcTags,
	}, func(ctx context.Context, _ *struct{}) (*struct{ Body []acquire.DownloadClient }, error) {
		list, err := a.ListDownloadClients(ctx)
		if err != nil {
			return nil, s.problem(ctx, err, false)
		}
		return &struct{ Body []acquire.DownloadClient }{list}, nil
	})
	huma.Register(s.api, huma.Operation{
		OperationID: "get-download-client", Method: http.MethodGet, Path: "/download-clients/{id}", Summary: "Get a download client", Tags: dcTags,
	}, func(ctx context.Context, in *IDParam) (*struct{ Body acquire.DownloadClient }, error) {
		c, err := a.GetDownloadClient(ctx, in.ID)
		if err != nil {
			return nil, s.problem(ctx, err, false)
		}
		return &struct{ Body acquire.DownloadClient }{c}, nil
	})
	huma.Register(s.api, huma.Operation{
		OperationID: "create-download-client", Method: http.MethodPost, Path: "/download-clients", Summary: "Add a download client",
		Tags: dcTags, DefaultStatus: http.StatusCreated,
	}, func(ctx context.Context, in *clientBody) (*struct{ Body acquire.DownloadClient }, error) {
		c, err := a.CreateDownloadClient(ctx, in.Body)
		if err != nil {
			return nil, s.problem(ctx, err, false)
		}
		return &struct{ Body acquire.DownloadClient }{c}, nil
	})
	huma.Register(s.api, huma.Operation{
		OperationID: "update-download-client", Method: http.MethodPut, Path: "/download-clients/{id}", Summary: "Replace download client settings",
		Description: "Omit username, password or apiKey to keep the stored value.", Tags: dcTags,
	}, func(ctx context.Context, in *clientIDBody) (*struct{ Body acquire.DownloadClient }, error) {
		c, err := a.UpdateDownloadClient(ctx, in.ID, in.Body)
		if err != nil {
			return nil, s.problem(ctx, err, false)
		}
		return &struct{ Body acquire.DownloadClient }{c}, nil
	})
	huma.Register(s.api, huma.Operation{
		OperationID: "delete-download-client", Method: http.MethodDelete, Path: "/download-clients/{id}", Summary: "Remove a download client",
		Description: "Its downloads keep their record and stop being tracked. Nothing is deleted in the client.",
		Tags:        dcTags, DefaultStatus: http.StatusNoContent,
	}, func(ctx context.Context, in *IDParam) (*struct{}, error) {
		if err := a.DeleteDownloadClient(ctx, in.ID); err != nil {
			return nil, s.problem(ctx, err, false)
		}
		return nil, nil
	})
	huma.Register(s.api, huma.Operation{
		OperationID: "test-download-client", Method: http.MethodPost, Path: "/download-clients/{id}/test", Summary: "Test a saved download client",
		Description: "Always 200: ok and a plain-language message.", Tags: dcTags,
	}, func(ctx context.Context, in *IDParam) (*testOutput, error) {
		r, err := a.TestDownloadClient(ctx, in.ID)
		if err != nil {
			return nil, s.problem(ctx, err, false)
		}
		return &testOutput{r}, nil
	})
	huma.Register(s.api, huma.Operation{
		OperationID: "test-download-client-settings", Method: http.MethodPost, Path: "/download-clients/test",
		Summary: "Test download client settings before saving", Description: "With ?id=, omitted secrets come from that stored client.", Tags: dcTags,
	}, func(ctx context.Context, in *struct {
		ID   int64 `query:"id" minimum:"0"`
		Body acquire.DownloadClientInput
	}) (*testOutput, error) {
		r, err := a.TestDownloadClientConfig(ctx, in.ID, in.Body)
		if err != nil {
			return nil, s.problem(ctx, err, false)
		}
		return &testOutput{r}, nil
	})

	// ------------------------------------------------------------- remote path mappings
	rpTags := []string{"Download clients"}
	huma.Register(s.api, huma.Operation{
		OperationID: "list-remote-path-mappings", Method: http.MethodGet, Path: "/remote-path-mappings", Summary: "List remote path mappings", Tags: rpTags,
	}, func(ctx context.Context, _ *struct{}) (*struct{ Body []acquire.RemotePathMapping }, error) {
		list, err := a.ListRemotePathMappings(ctx)
		if err != nil {
			return nil, s.problem(ctx, err, false)
		}
		return &struct{ Body []acquire.RemotePathMapping }{list}, nil
	})
	huma.Register(s.api, huma.Operation{
		OperationID: "create-remote-path-mapping", Method: http.MethodPost, Path: "/remote-path-mappings", Summary: "Add a remote path mapping",
		Tags: rpTags, DefaultStatus: http.StatusCreated,
	}, func(ctx context.Context, in *mappingBody) (*struct{ Body acquire.RemotePathMapping }, error) {
		m, err := a.CreateRemotePathMapping(ctx, in.Body)
		if err != nil {
			return nil, s.problem(ctx, err, false)
		}
		return &struct{ Body acquire.RemotePathMapping }{m}, nil
	})
	huma.Register(s.api, huma.Operation{
		OperationID: "update-remote-path-mapping", Method: http.MethodPut, Path: "/remote-path-mappings/{id}", Summary: "Replace a remote path mapping", Tags: rpTags,
	}, func(ctx context.Context, in *mappingIDBody) (*struct{ Body acquire.RemotePathMapping }, error) {
		m, err := a.UpdateRemotePathMapping(ctx, in.ID, in.Body)
		if err != nil {
			return nil, s.problem(ctx, err, false)
		}
		return &struct{ Body acquire.RemotePathMapping }{m}, nil
	})
	huma.Register(s.api, huma.Operation{
		OperationID: "delete-remote-path-mapping", Method: http.MethodDelete, Path: "/remote-path-mappings/{id}", Summary: "Remove a remote path mapping",
		Tags: rpTags, DefaultStatus: http.StatusNoContent,
	}, func(ctx context.Context, in *IDParam) (*struct{}, error) {
		if err := a.DeleteRemotePathMapping(ctx, in.ID); err != nil {
			return nil, s.problem(ctx, err, false)
		}
		return nil, nil
	})

	// ------------------------------------------------------------- searches
	s.bulkSearchRoutes(a)
	srTags := []string{"Search"}
	huma.Register(s.api, huma.Operation{
		OperationID: "search-movie", Method: http.MethodPost, Path: "/movies/{id}/search", Summary: "Search now and grab the best release",
		Description: "Queues an automatic search: the best accepted release is grabbed, unless as good a download is already queued.",
		Tags:        srTags, DefaultStatus: http.StatusAccepted,
	}, func(ctx context.Context, in *IDParam) (*refreshOutput, error) {
		if _, err := s.d.Library.GetMovie(ctx, in.ID); err != nil {
			return nil, s.problem(ctx, err, false)
		}
		job, created, err := s.d.Jobs.Enqueue(ctx, acquire.JobMovieSearch, acquire.MoviePayload{MovieID: in.ID},
			jobs.EnqueueOptions{DedupeKey: fmt.Sprintf("%s:%d", acquire.JobMovieSearch, in.ID), Priority: 10, MaxAttempts: 2})
		if err != nil {
			return nil, s.problem(ctx, err, false)
		}
		out := &refreshOutput{Status: http.StatusAccepted}
		out.Body.JobID, out.Body.Queued, out.Body.MovieID = job.ID, created, in.ID
		return out, nil
	})
	huma.Register(s.api, huma.Operation{
		OperationID: "start-interactive-search", Method: http.MethodPost, Path: "/movies/{id}/search/interactive",
		Summary: "Start an interactive search",
		Description: "Returns at once with the search id. Results stream on /events (search.batch, search.indexer_done, " +
			"search.indexer_failed, search.finished) and stay available at /searches/{searchId} for 15 minutes. " +
			"Each result has its decision: accepted, rejections, score breakdown and rank. Download links are never exposed.",
		Tags: srTags, DefaultStatus: http.StatusAccepted,
	}, func(ctx context.Context, in *IDParam) (*struct {
		Status int
		Body   SearchStarted
	}, error) {
		snap, err := a.StartInteractive(ctx, in.ID)
		if err != nil {
			return nil, s.problem(ctx, err, false)
		}
		return &struct {
			Status int
			Body   SearchStarted
		}{http.StatusAccepted, SearchStarted{SearchID: snap.ID, Location: s.d.BaseURL + Prefix + "/searches/" + snap.ID, SearchSnapshot: snap}}, nil
	})
	huma.Register(s.api, huma.Operation{
		OperationID: "get-search", Method: http.MethodGet, Path: "/searches/{searchId}", Summary: "State and results of an interactive search",
		Tags: srTags,
	}, func(ctx context.Context, in *struct {
		SearchID string `path:"searchId" minLength:"32" maxLength:"32" pattern:"^[0-9a-f]+$"`
	}) (*struct{ Body acquire.SearchSnapshot }, error) {
		snap, err := a.GetSearch(in.SearchID)
		if err != nil {
			return nil, s.problem(ctx, err, false)
		}
		return &struct{ Body acquire.SearchSnapshot }{snap}, nil
	})
	huma.Register(s.api, huma.Operation{
		OperationID: "grab-search-result", Method: http.MethodPost, Path: "/searches/{searchId}/grab", Summary: "Grab a release of an interactive search",
		Description: "A rejected release is refused (422 with the reasons) unless override is true: the grab is then recorded as an override.",
		Tags:        srTags, DefaultStatus: http.StatusCreated,
	}, func(ctx context.Context, in *struct {
		SearchID string `path:"searchId" minLength:"32" maxLength:"32" pattern:"^[0-9a-f]+$"`
		Body     struct {
			Key      string `json:"key" minLength:"1" maxLength:"64" doc:"key of the result"`
			Override bool   `json:"override,omitempty" doc:"Grab even though the release is rejected"`
		}
	}) (*struct{ Body acquire.QueueItem }, error) {
		item, err := a.GrabFromSearch(ctx, in.SearchID, in.Body.Key, in.Body.Override)
		if err != nil {
			return nil, s.problem(ctx, err, false)
		}
		return &struct{ Body acquire.QueueItem }{item}, nil
	})

	// ------------------------------------------------------------- queue
	qTags := []string{"Queue"}
	huma.Register(s.api, huma.Operation{
		OperationID: "list-queue", Method: http.MethodGet, Path: "/queue", Summary: "Downloads in progress",
		Description: "Oldest first, with the movie, indexer, retry chain and allowed actions. download.progress events " +
			"carry the live fields of one entry, keyed by id (see ProgressUpdate); download.* events mean: refetch.", Tags: qTags,
	}, func(ctx context.Context, _ *struct{}) (*struct{ Body []QueueEntry }, error) {
		list, err := a.Queue(ctx)
		if err != nil {
			return nil, s.problem(ctx, err, false)
		}
		entries, err := s.queueEntries(ctx, list, a.AcquisitionSettings(ctx).MaxAttemptsPerDay)
		if err != nil {
			return nil, s.problem(ctx, err, false)
		}
		return &struct{ Body []QueueEntry }{entries}, nil
	})
	for _, op := range []struct {
		path, id, summary string
		paused            bool
	}{{"/queue/{id}/pause", "pause-download", "Pause a download", true}, {"/queue/{id}/resume", "resume-download", "Resume a download", false}} {
		huma.Register(s.api, huma.Operation{
			OperationID: op.id, Method: http.MethodPost, Path: op.path, Summary: op.summary, Tags: qTags,
		}, func(ctx context.Context, in *IDParam) (*struct{ Body acquire.QueueItem }, error) {
			item, err := a.SetPaused(ctx, in.ID, op.paused)
			if err != nil {
				return nil, s.problem(ctx, err, false)
			}
			return &struct{ Body acquire.QueueItem }{item}, nil
		})
	}
	huma.Register(s.api, huma.Operation{
		OperationID: "remove-download", Method: http.MethodDelete, Path: "/queue/{id}", Summary: "Remove a download",
		Description: "Removes it from the client and the queue. deleteData (default false) also asks the client to delete the downloaded data.",
		Tags:        qTags, DefaultStatus: http.StatusNoContent,
	}, func(ctx context.Context, in *struct {
		IDParam
		Blocklist  bool `query:"blocklist" doc:"Blocklist the release"`
		Retry      bool `query:"retry" doc:"Grab the next best release"`
		DeleteData bool `query:"deleteData" doc:"Delete the downloaded data in the client (explicit request only)"`
	}) (*struct{}, error) {
		if err := a.RemoveFromQueue(ctx, in.ID, acquire.RemoveOptions{Blocklist: in.Blocklist, Retry: in.Retry, DeleteData: in.DeleteData}); err != nil {
			return nil, s.problem(ctx, err, false)
		}
		return nil, nil
	})

	for _, op := range []struct {
		path, id, summary, desc string
		stop                    bool
	}{
		{"/queue/{id}/stop-retrying", "stop-retrying-download", "Stop the automatic retries of a download chain",
			"Any download of the chain (failed or active; ids from the queue or history) can be given. When a download of the chain fails, " +
				"the next best release is no longer grabbed automatically; RSS and the scheduled searches still run. The current download " +
				"is not touched. Written to history (retries_stopped); undo with resume-retrying. Idempotent.", true},
		{"/queue/{id}/resume-retrying", "resume-retrying-download", "Resume the automatic retries of a download chain",
			"Undoes stop-retrying (history retries_resumed). When the newest download of the chain failed meanwhile and nothing is " +
				"downloading for the movie, the next release is tried now. Idempotent.", false},
	} {
		huma.Register(s.api, huma.Operation{
			OperationID: op.id, Method: http.MethodPost, Path: op.path, Summary: op.summary, Description: op.desc, Tags: qTags,
		}, func(ctx context.Context, in *IDParam) (*struct{ Body acquire.QueueItem }, error) {
			var item acquire.QueueItem
			var err error
			if op.stop {
				item, err = a.StopRetrying(ctx, in.ID)
			} else {
				item, err = a.ResumeRetrying(ctx, in.ID)
			}
			if err != nil {
				return nil, s.problem(ctx, err, false)
			}
			return &struct{ Body acquire.QueueItem }{item}, nil
		})
	}

	huma.Register(s.api, huma.Operation{
		OperationID: "retry-download-import", Method: http.MethodPost, Path: "/queue/{id}/import", Summary: "Import a completed download again",
		Description: "For a download in warning (failed import) or completed: runs the import again, e.g. after fixing a remote path mapping. " +
			"The output path is asked again from the client with the current mappings. movieId imports it for another movie. " +
			"The import runs in the background (202); its outcome streams as download.* events and is written to history.",
		Tags: qTags, DefaultStatus: http.StatusAccepted,
	}, func(ctx context.Context, in *struct {
		IDParam
		MovieID int64 `query:"movieId" minimum:"0" doc:"Import for this movie instead of the one the release was grabbed for"`
	}) (*struct{ Body acquire.QueueItem }, error) {
		item, err := a.RetryImport(ctx, in.ID, in.MovieID)
		if err != nil {
			return nil, s.problem(ctx, err, false)
		}
		return &struct{ Body acquire.QueueItem }{item}, nil
	})

	// ------------------------------------------------------------- settings
	huma.Register(s.api, huma.Operation{
		OperationID: "get-acquisition-settings", Method: http.MethodGet, Path: "/settings/acquisition", Summary: "Search and download settings",
		Description: "Usenet retention and minimum age, preferred protocol, automatic retries, RSS and missing search intervals.",
		Tags:        []string{"Settings"},
	}, func(ctx context.Context, _ *struct{}) (*struct{ Body acquire.AcquisitionSettings }, error) {
		return &struct{ Body acquire.AcquisitionSettings }{a.AcquisitionSettings(ctx)}, nil
	})
	huma.Register(s.api, huma.Operation{
		OperationID: "set-acquisition-settings", Method: http.MethodPut, Path: "/settings/acquisition", Summary: "Change search and download settings",
		Description: "Every field is required. Out-of-range values are refused with a message per field (422). New intervals apply at once.",
		Tags:        []string{"Settings"},
	}, func(ctx context.Context, in *struct{ Body acquire.AcquisitionSettings }) (*struct{ Body acquire.AcquisitionSettings }, error) {
		out, err := a.SetAcquisitionSettings(ctx, in.Body)
		if err != nil {
			return nil, s.problem(ctx, err, false)
		}
		return &struct{ Body acquire.AcquisitionSettings }{out}, nil
	})

	// ------------------------------------------------------------- blocklist
	blTags := []string{"Blocklist"}
	huma.Register(s.api, huma.Operation{
		OperationID: "list-blocklist", Method: http.MethodGet, Path: "/blocklist", Summary: "List blocklisted releases and groups",
		Description: "Newest first, keyset paginated.", Tags: blTags,
	}, func(ctx context.Context, in *struct {
		PageParams
		MovieID int64  `query:"movieId" minimum:"0"`
		Kind    string `query:"kind" enum:"release,group"`
	}) (*struct{ Body []acquire.BlocklistItem }, error) {
		list, err := a.ListBlocklist(ctx, acquire.BlocklistFilter{MovieID: in.MovieID, Kind: in.Kind, Before: in.Before, Limit: in.Limit})
		if err != nil {
			return nil, s.problem(ctx, err, false)
		}
		return &struct{ Body []acquire.BlocklistItem }{list}, nil
	})
	huma.Register(s.api, huma.Operation{
		OperationID: "delete-blocklist-entry", Method: http.MethodDelete, Path: "/blocklist/{id}", Summary: "Unblock a release or group",
		Tags: blTags, DefaultStatus: http.StatusNoContent,
	}, func(ctx context.Context, in *IDParam) (*struct{}, error) {
		if err := a.DeleteBlocklist(ctx, in.ID); err != nil {
			return nil, s.problem(ctx, err, false)
		}
		return nil, nil
	})
	huma.Register(s.api, huma.Operation{
		OperationID: "clear-movie-blocklist", Method: http.MethodDelete, Path: "/blocklist", Summary: "Clear the blocklist of a movie",
		Description: "Entries that apply to every movie are kept.", Tags: blTags,
	}, func(ctx context.Context, in *struct {
		MovieID int64 `query:"movieId" required:"true" minimum:"1"`
	}) (*struct {
		Body struct {
			Removed int64 `json:"removed"`
		}
	}, error) {
		n, err := a.ClearMovieBlocklist(ctx, in.MovieID)
		if err != nil {
			return nil, s.problem(ctx, err, false)
		}
		out := &struct {
			Body struct {
				Removed int64 `json:"removed"`
			}
		}{}
		out.Body.Removed = n
		return out, nil
	})
	huma.Register(s.api, huma.Operation{
		OperationID: "block-group", Method: http.MethodPost, Path: "/blocklist/groups", Summary: "Block a release group",
		Description: "For one movie, or for every movie when movieId is omitted.", Tags: blTags, DefaultStatus: http.StatusCreated,
	}, func(ctx context.Context, in *struct {
		Body struct {
			Group   string `json:"group" minLength:"1" maxLength:"100"`
			MovieID int64  `json:"movieId,omitempty" minimum:"0"`
			Reason  string `json:"reason,omitempty" maxLength:"500"`
		}
	}) (*struct{ Body acquire.BlocklistItem }, error) {
		item, err := a.BlockGroup(ctx, in.Body.Group, in.Body.MovieID, in.Body.Reason)
		if err != nil {
			return nil, s.problem(ctx, err, false)
		}
		return &struct{ Body acquire.BlocklistItem }{item}, nil
	})

	s.lot3AcquireRoutes(a)
}
