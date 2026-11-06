package acquire

import (
	"errors"
	"slices"
	"strings"
	"testing"

	"github.com/fs0ciety7000/gonema/internal/store"
	"github.com/fs0ciety7000/gonema/internal/store/db"
)

func TestChainOf(t *testing.T) {
	p := func(v int64) *int64 { return &v }
	all := []db.Download{
		{ID: 1, GrabbedAt: 10},
		{ID: 2, GrabbedAt: 20, RetryOf: p(1)},
		{ID: 3, GrabbedAt: 30, RetryOf: p(2)},
		{ID: 4, GrabbedAt: 40},                 // another chain
		{ID: 5, GrabbedAt: 50, RetryOf: p(99)}, // parent gone (ON DELETE SET NULL not yet applied)
		{ID: 6, GrabbedAt: 60, RetryOf: p(6)},  // self loop: must not hang
	}
	ids := func(ds []db.Download) []int64 {
		out := []int64{}
		for _, d := range ds {
			out = append(out, d.ID)
		}
		return out
	}
	for _, tc := range []struct {
		id   int64
		want []int64
	}{
		{1, []int64{1, 2, 3}},
		{2, []int64{1, 2, 3}},
		{3, []int64{1, 2, 3}},
		{4, []int64{4}},
		{5, []int64{5}},
		{6, []int64{6}},
		{42, []int64{}},
	} {
		if got := ids(chainOf(all, tc.id)); !slices.Equal(got, tc.want) {
			t.Errorf("chainOf(%d) = %v, want %v", tc.id, got, tc.want)
		}
	}
}

// TestStopRetrying: stopping the retries of a chain keeps the failed download
// from being replaced, writes history, and resuming tries the next release.
func TestStopRetrying(t *testing.T) {
	e, qb, movie, first := grabbedEnv(t)

	item, err := e.svc.StopRetrying(e.ctx, first.ID)
	if err != nil || !item.RetryStopped || item.ID != first.ID {
		t.Fatalf("stop = %+v, %v", item, err)
	}
	// Idempotent: no second history row.
	if _, err := e.svc.StopRetrying(e.ctx, first.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := e.svc.StopRetrying(e.ctx, 999); !errors.Is(err, store.ErrNotFound) {
		t.Errorf("stop unknown = %v", err)
	}
	if q, _ := e.svc.Queue(e.ctx); len(q) != 1 || !q[0].RetryStopped {
		t.Errorf("queue = %+v", q)
	}

	// The download fails: blocklisted, but no next release.
	qb.Set(deref(first.ClientRef), "error", 0.4, "")
	if _, err := e.svc.TrackOnce(e.ctx); err != nil {
		t.Fatal(err)
	}
	if dls := e.downloads(movie.ID); len(dls) != 1 || dls[0].Status != "failed" || dls[0].RetryStoppedAt == nil {
		t.Fatalf("downloads after failure = %+v", dls)
	}
	hist := e.history(movie.ID)
	if got := historyEvents(hist); !slices.Equal(got, []string{"grabbed", "retries_stopped", "download_failed", "blocklisted"}) {
		t.Fatalf("history = %v", got)
	}
	if !strings.Contains(hist[1].Data, "stopped by the user") || hist[1].DownloadID == nil || *hist[1].DownloadID != first.ID {
		t.Errorf("stop history = %+v", hist[1])
	}

	// Resume: the next best release is grabbed at once.
	item, err = e.svc.ResumeRetrying(e.ctx, first.ID)
	if err != nil || item.RetryStopped {
		t.Fatalf("resume = %+v, %v", item, err)
	}
	dls := e.downloads(movie.ID)
	if len(dls) != 2 || dls[1].ReleaseTitle != relWeb || deref(dls[1].RetryOf) != first.ID || dls[0].RetryStoppedAt != nil {
		t.Fatalf("downloads after resume = %+v", dls)
	}
	if got := historyEvents(e.history(movie.ID)); !slices.Equal(got[4:], []string{"retries_resumed", "retry_scheduled", "grabbed"}) {
		t.Errorf("history after resume = %v", got)
	}

	// Stopping from the new download marks the whole chain.
	if _, err := e.svc.StopRetrying(e.ctx, dls[1].ID); err != nil {
		t.Fatal(err)
	}
	for _, d := range e.downloads(movie.ID) {
		if d.RetryStoppedAt == nil {
			t.Errorf("download %d of the chain not stopped", d.ID)
		}
	}
}

// TestRemoveAndRetryResumes: asking for the next release of a stopped chain
// resumes its retries.
func TestRemoveAndRetryResumes(t *testing.T) {
	e, _, movie, first := grabbedEnv(t)
	if _, err := e.svc.StopRetrying(e.ctx, first.ID); err != nil {
		t.Fatal(err)
	}
	if err := e.svc.RemoveFromQueue(e.ctx, first.ID, RemoveOptions{Blocklist: true, Retry: true}); err != nil {
		t.Fatal(err)
	}
	dls := e.downloads(movie.ID)
	if len(dls) != 2 || dls[1].ReleaseTitle != relWeb || dls[1].RetryStoppedAt != nil {
		t.Fatalf("downloads = %+v", dls)
	}
	if got := historyEvents(e.history(movie.ID)); !slices.Contains(got, "retries_resumed") {
		t.Errorf("history = %v", got)
	}
}
