package store_test

import (
	"context"
	"errors"
	"fmt"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"github.com/fs0ciety7000/gonema/internal/store"
	"github.com/fs0ciety7000/gonema/internal/store/db"
	"github.com/fs0ciety7000/gonema/internal/store/storetest"
)

var t0 = time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)

func job(kind string, runAt time.Time, priority int64, dedupe string) db.EnqueueJobParams {
	p := db.EnqueueJobParams{
		Kind: kind, Payload: `{}`, Priority: priority, RunAt: store.Millis(runAt),
		MaxAttempts: 3, CreatedAt: store.Millis(t0),
	}
	if dedupe != "" {
		p.DedupeKey = &dedupe
	}
	return p
}

func TestJobLifecycle(t *testing.T) {
	ctx := context.Background()
	s := storetest.New(t)

	a, created, err := s.EnqueueJob(ctx, job("refresh", t0, 0, "refresh:1"))
	if err != nil || !created {
		t.Fatalf("EnqueueJob = %v, %v", created, err)
	}
	if _, created, err := s.EnqueueJob(ctx, job("refresh", t0, 0, "refresh:1")); err != nil || created {
		t.Fatalf("duplicate active dedupe key: created=%v err=%v, want deduplicated", created, err)
	}
	hi, _, err := s.EnqueueJob(ctx, job("search", t0.Add(time.Second), 10, ""))
	if err != nil {
		t.Fatal(err)
	}
	if _, _, err := s.EnqueueJob(ctx, job("later", t0.Add(time.Hour), 99, "")); err != nil {
		t.Fatal(err)
	}
	if _, _, err := s.EnqueueJob(ctx, db.EnqueueJobParams{Kind: "x", Payload: "nope", MaxAttempts: 1}); !errors.Is(err, store.ErrInvalid) {
		t.Errorf("invalid payload err = %v, want ErrInvalid", err)
	}

	now := t0.Add(time.Minute)
	// Highest priority among due jobs first; the future job is not due.
	got, ok, err := s.ClaimJob(ctx, now)
	if err != nil || !ok || got.ID != hi.ID || got.Status != "running" || got.Attempts != 1 {
		t.Fatalf("first claim = %+v, %v, %v; want job %d", got, ok, err, hi.ID)
	}
	got, ok, err = s.ClaimJob(ctx, now)
	if err != nil || !ok || got.ID != a.ID {
		t.Fatalf("second claim = %+v, %v, %v; want job %d", got, ok, err, a.ID)
	}
	if _, ok, err := s.ClaimJob(ctx, now); err != nil || ok {
		t.Fatalf("third claim ok=%v err=%v, want nothing due", ok, err)
	}
	// While running, the dedupe key still blocks.
	if _, created, _ := s.EnqueueJob(ctx, job("refresh", t0, 0, "refresh:1")); created {
		t.Error("dedupe key of a running job did not block")
	}

	if err := s.CompleteJob(ctx, hi.ID, now); err != nil {
		t.Fatal(err)
	}
	if err := s.CompleteJob(ctx, hi.ID, now); !errors.Is(err, store.ErrNotFound) {
		t.Errorf("completing a done job err = %v, want ErrNotFound", err)
	}

	// Failure: retried with backoff until max_attempts, then failed.
	f, err := s.FailJob(ctx, a.ID, got.Attempts, now, "tmdb: 503")
	if err != nil || f.Status != "pending" || f.RunAt != store.Millis(now.Add(store.Backoff(1))) || *f.LastError != "tmdb: 503" {
		t.Fatalf("FailJob #1 = %+v, %v", f, err)
	}
	for attempt := int64(2); attempt <= 3; attempt++ {
		now = time.UnixMilli(f.RunAt)
		got, ok, err = s.ClaimJob(ctx, now)
		if err != nil || !ok || got.ID != a.ID || got.Attempts != attempt {
			t.Fatalf("claim attempt %d = %+v, %v, %v", attempt, got, ok, err)
		}
		if f, err = s.FailJob(ctx, a.ID, got.Attempts, now, "again"); err != nil {
			t.Fatal(err)
		}
	}
	if f.Status != "failed" || f.FinishedAt == nil {
		t.Fatalf("after max attempts: %+v, want failed", f)
	}
	// A finished job frees its dedupe key.
	if _, created, err := s.EnqueueJob(ctx, job("refresh", t0, 0, "refresh:1")); err != nil || !created {
		t.Errorf("re-enqueue after failure: created=%v err=%v", created, err)
	}

	if n, err := s.Write().DeleteFinishedJobsBefore(ctx, store.Millis(now)+1); err != nil || n != 2 {
		t.Errorf("DeleteFinishedJobsBefore = %d, %v; want 2", n, err)
	}
	if err := store.RequireRow(s.Write().CancelJob(ctx, db.CancelJobParams{ID: a.ID, Now: 1})); !errors.Is(err, store.ErrNotFound) {
		t.Errorf("cancel of a deleted job err = %v", err)
	}
	pending, err := s.Read().ListJobsByStatus(ctx, db.ListJobsByStatusParams{Status: "pending", Limit: 10})
	if err != nil || len(pending) != 2 {
		t.Fatalf("pending = %v, %v", pending, err)
	}
	if err := store.RequireRow(s.Write().CancelJob(ctx, db.CancelJobParams{ID: pending[0].ID, Now: 1})); err != nil {
		t.Error(err)
	}
	if j, err := s.Read().GetJob(ctx, pending[0].ID); err != nil || j.Status != "cancelled" {
		t.Errorf("GetJob = %+v, %v", j, err)
	}
}

func TestResetRunningJobs(t *testing.T) {
	ctx := context.Background()
	s := storetest.New(t)
	for i := range 3 {
		if _, _, err := s.EnqueueJob(ctx, job("k", t0, 0, fmt.Sprint(i))); err != nil {
			t.Fatal(err)
		}
	}
	for range 2 {
		if _, ok, err := s.ClaimJob(ctx, t0); err != nil || !ok {
			t.Fatal(ok, err)
		}
	}
	n, err := s.ResetRunningJobs(ctx)
	if err != nil || n != 2 {
		t.Fatalf("ResetRunningJobs = %d, %v; want 2", n, err)
	}
	running, _ := s.Read().ListJobsByStatus(ctx, db.ListJobsByStatusParams{Status: "running", Limit: 10})
	if len(running) != 0 {
		t.Errorf("still running: %v", running)
	}
}

func TestBackoff(t *testing.T) {
	tests := []struct {
		attempts int64
		want     time.Duration
	}{
		{0, 30 * time.Second}, {1, 30 * time.Second}, {2, time.Minute}, {3, 2 * time.Minute},
		{10, 256 * time.Minute}, {11, 6 * time.Hour}, {1000, 6 * time.Hour},
	}
	for _, tt := range tests {
		if got := store.Backoff(tt.attempts); got != tt.want {
			t.Errorf("Backoff(%d) = %v, want %v", tt.attempts, got, tt.want)
		}
	}
}

// TestClaimJobConcurrent: N workers, spread over two Store instances (two writer
// connections, as two processes would have), claim jobs until none is due; each
// job must be claimed exactly once.
func TestClaimJobConcurrent(t *testing.T) {
	ctx := context.Background()
	path := filepath.Join(t.TempDir(), "gonema.db")
	var stores []*store.Store
	for range 2 {
		s, err := store.Open(ctx, path)
		if err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() { _ = s.Close() })
		stores = append(stores, s)
	}
	const jobs, workers = 200, 16
	err := stores[0].WithTx(ctx, func(q *db.Queries) error {
		for i := range jobs {
			if _, err := q.EnqueueJob(ctx, job("k", t0, int64(i%5), "")); err != nil {
				return err
			}
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}

	var (
		mu      sync.Mutex
		claimed = map[int64]int{}
		wg      sync.WaitGroup
		errs    = make(chan error, workers)
	)
	for w := range workers {
		wg.Go(func() {
			s := stores[w%len(stores)]
			for {
				j, ok, err := s.ClaimJob(ctx, t0)
				if err != nil {
					errs <- err
					return
				}
				if !ok {
					return
				}
				mu.Lock()
				claimed[j.ID]++
				mu.Unlock()
			}
		})
	}
	wg.Wait()
	close(errs)
	for err := range errs {
		t.Fatal(err)
	}
	if len(claimed) != jobs {
		t.Fatalf("claimed %d distinct jobs, want %d", len(claimed), jobs)
	}
	for id, n := range claimed {
		if n != 1 {
			t.Errorf("job %d claimed %d times", id, n)
		}
	}
}
