package library

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"time"

	"github.com/fs0ciety7000/gonema/internal/imagecache"
	"github.com/fs0ciety7000/gonema/internal/jobs"
	"github.com/fs0ciety7000/gonema/internal/metadata"
	"github.com/fs0ciety7000/gonema/internal/store"
	"github.com/fs0ciety7000/gonema/internal/store/db"
)

// Schedules of the recurring library jobs.
const (
	RefreshAllInterval = 24 * time.Hour
	RefreshAllJitter   = time.Hour
	DiskSpaceInterval  = 15 * time.Minute
	DiskSpaceJitter    = time.Minute
)

// JobRunner is the part of jobs.Runner used to register the library jobs.
type JobRunner interface {
	Handle(kind string, fn jobs.Handler, opts jobs.HandlerOptions)
	Every(kind string, interval, jitter time.Duration, payload any)
}

// RegisterJobs registers the library job handlers and recurring schedules:
// metadata refresh daily, disk space every 15 minutes.
func (s *Service) RegisterJobs(r JobRunner) {
	r.Handle(JobMovieImages, s.handleImages, jobs.HandlerOptions{Timeout: 2 * time.Minute})
	r.Handle(JobMovieRefresh, s.handleRefresh, jobs.HandlerOptions{Timeout: 2 * time.Minute})
	r.Handle(JobRefreshAll, s.handleRefreshAll, jobs.HandlerOptions{Timeout: 10 * time.Minute})
	r.Handle(JobRootFolderSpace, func(ctx context.Context, _ db.Job) error { return s.UpdateDiskSpace(ctx) },
		jobs.HandlerOptions{Timeout: time.Minute})
	r.Every(JobRefreshAll, RefreshAllInterval, RefreshAllJitter, nil)
	r.Every(JobRootFolderSpace, DiskSpaceInterval, DiskSpaceJitter, nil)
}

func moviePayload(job db.Job) (int64, error) {
	var p MoviePayload
	if err := json.Unmarshal([]byte(job.Payload), &p); err != nil || p.MovieID <= 0 {
		return 0, fmt.Errorf("library: invalid %s payload %q", job.Kind, job.Payload)
	}
	return p.MovieID, nil
}

// handleImages caches the poster and backdrop of a movie and stores the
// dominant color of the poster.
func (s *Service) handleImages(ctx context.Context, job db.Job) error {
	id, err := moviePayload(job)
	if err != nil {
		return err
	}
	images := s.imageCache()
	if images == nil {
		return nil
	}
	m, err := s.st.Read().GetMovie(ctx, id)
	if errors.Is(store.MapError(err), store.ErrNotFound) {
		return nil // deleted meanwhile
	}
	if err != nil {
		return store.MapError(err)
	}
	var errs []error
	if m.BackdropPath != nil {
		for _, size := range imagecache.Sizes(imagecache.Backdrop) {
			if _, err := images.Fetch(ctx, imagecache.Backdrop, size, *m.BackdropPath); err != nil {
				errs = append(errs, err)
			}
		}
	}
	if m.PosterPath == nil {
		return errors.Join(errs...)
	}
	for _, size := range imagecache.Sizes(imagecache.Poster) {
		if _, err := images.Fetch(ctx, imagecache.Poster, size, *m.PosterPath); err != nil {
			errs = append(errs, err)
		}
	}
	color, err := images.PosterColor(ctx, *m.PosterPath)
	if err != nil {
		return errors.Join(append(errs, err)...)
	}
	poster := *m.PosterPath
	err = s.st.WithTx(ctx, func(q *db.Queries) error {
		cur, err := q.GetMovie(ctx, id)
		if err != nil {
			return err
		}
		if cur.PosterPath == nil || *cur.PosterPath != poster || eqPtr(cur.DominantColor, &color) {
			return nil // poster changed meanwhile, or nothing to do
		}
		p := paramsFromMovie(&cur)
		p.DominantColor = &color
		_, err = q.UpdateMovieMetadata(ctx, p)
		return err
	})
	if errors.Is(err, store.ErrNotFound) {
		return errors.Join(errs...)
	}
	if err != nil {
		return fmt.Errorf("library: save dominant color of movie %d: %w", id, err)
	}
	return errors.Join(errs...)
}

func (s *Service) handleRefresh(ctx context.Context, job db.Job) error {
	id, err := moviePayload(job)
	if err != nil {
		return err
	}
	_, err = s.RefreshMovie(ctx, id)
	if errors.Is(err, store.ErrNotFound) || errors.Is(err, ErrNoProvider) {
		s.log.Info("movie refresh skipped", slog.Int64("movie_id", id), slog.Any("reason", err))
		return nil
	}
	return err
}

// RefreshDue reports whether the metadata of a movie should be refreshed now:
//
//   - never refreshed: yes;
//   - announced or in cinemas: daily, dates and status move;
//   - released, latest known release less than 180 days ago: every 3 days;
//   - other (older or deleted from TMDB): every 30 days.
func RefreshDue(status string, releases []*string, refreshedAt *int64, now time.Time) bool {
	if refreshedAt == nil {
		return true
	}
	age := now.Sub(store.FromMillis(*refreshedAt))
	switch status {
	case string(metadata.StatusAnnounced), string(metadata.StatusInCinemas):
		return age >= 20*time.Hour // < 24 h: the daily job runs with jitter
	}
	var latest time.Time
	for _, r := range releases {
		if r == nil {
			continue
		}
		if t, err := time.Parse(time.DateOnly, *r); err == nil && t.After(latest) {
			latest = t
		}
	}
	if status == string(metadata.StatusReleased) && !latest.IsZero() && now.Sub(latest) < 180*24*time.Hour {
		return age >= 3*24*time.Hour
	}
	return age >= 30*24*time.Hour
}

// handleRefreshAll enqueues a refresh for every movie whose metadata is due.
func (s *Service) handleRefreshAll(ctx context.Context, _ db.Job) error {
	if !s.TMDBConfigured() {
		s.log.Info("metadata refresh skipped: no TMDB key configured")
		return nil
	}
	rows, err := s.st.Read().ListMoviesForRefresh(ctx)
	if err != nil {
		return store.MapError(fmt.Errorf("library: list movies for refresh: %w", err))
	}
	now := s.now()
	due := 0
	for _, r := range rows {
		if !RefreshDue(r.Status, []*string{r.ReleaseCinema, r.ReleaseDigital, r.ReleasePhysical}, r.MetadataRefreshedAt, now) {
			continue
		}
		due++
		s.enqueue(ctx, JobMovieRefresh, MoviePayload{MovieID: r.ID}, jobs.EnqueueOptions{
			DedupeKey: fmt.Sprintf("%s:%d", JobMovieRefresh, r.ID), Priority: -10, MaxAttempts: 3,
		})
	}
	s.log.Info("metadata refresh planned", slog.Int("movies", len(rows)), slog.Int("due", due))
	return nil
}
