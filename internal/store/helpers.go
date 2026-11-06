package store

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/fs0ciety7000/gonema/internal/store/db"
)

// Millis converts t to the storage format of timestamps: unix milliseconds UTC.
func Millis(t time.Time) int64 { return t.UnixMilli() }

// FromMillis converts a stored timestamp back to a UTC time.
func FromMillis(ms int64) time.Time { return time.UnixMilli(ms).UTC() }

// AlternateTitle is one alternate title to store for a movie.
type AlternateTitle struct {
	Title      string
	CleanTitle string
	Country    string
}

// ReplaceAlternateTitles replaces, in one transaction, the alternate titles of
// movieID that come from source ("tmdb" or "user"); titles of the other source
// are kept, so a metadata refresh never erases titles added by the user.
func (s *Store) ReplaceAlternateTitles(ctx context.Context, movieID int64, source string, titles []AlternateTitle) error {
	return s.WithTx(ctx, func(q *db.Queries) error {
		if err := q.DeleteAlternateTitles(ctx, db.DeleteAlternateTitlesParams{MovieID: movieID, Source: source}); err != nil {
			return fmt.Errorf("delete alternate titles of movie %d: %w", movieID, err)
		}
		for _, t := range titles {
			if _, err := q.InsertAlternateTitle(ctx, db.InsertAlternateTitleParams{
				MovieID: movieID, Title: t.Title, CleanTitle: t.CleanTitle, Country: t.Country, Source: source,
			}); err != nil {
				return fmt.Errorf("insert alternate title of movie %d: %w", movieID, err)
			}
		}
		return nil
	})
}

// ProfileItem is one quality of a profile, in storage form. Members of a group
// share Position and GroupName (see quality_profile_items).
type ProfileItem struct {
	Quality   string
	Position  int64
	GroupName string // empty for a single quality
	Allowed   bool
}

// ReplaceQualityProfileItems replaces the items of a profile. It must run inside
// a transaction (q from WithTx) together with the profile write.
func ReplaceQualityProfileItems(ctx context.Context, q *db.Queries, profileID int64, items []ProfileItem) error {
	if err := q.DeleteQualityProfileItems(ctx, profileID); err != nil {
		return fmt.Errorf("delete items of profile %d: %w", profileID, err)
	}
	for _, it := range items {
		var group *string
		if it.GroupName != "" {
			group = &it.GroupName
		}
		if err := q.InsertQualityProfileItem(ctx, db.InsertQualityProfileItemParams{
			ProfileID: profileID, Quality: it.Quality, Position: it.Position, GroupName: group, Allowed: it.Allowed,
		}); err != nil {
			return fmt.Errorf("insert item %s of profile %d: %w", it.Quality, profileID, err)
		}
	}
	return nil
}

// CreateQualityProfile inserts a profile and its items in one transaction.
func (s *Store) CreateQualityProfile(ctx context.Context, p db.CreateQualityProfileParams, items []ProfileItem) (db.QualityProfile, error) {
	var out db.QualityProfile
	err := s.WithTx(ctx, func(q *db.Queries) error {
		var err error
		if out, err = q.CreateQualityProfile(ctx, p); err != nil {
			return fmt.Errorf("create profile %q: %w", p.Name, err)
		}
		return ReplaceQualityProfileItems(ctx, q, out.ID, items)
	})
	return out, err
}

// GetSetting decodes the JSON value of key into a T. ErrNotFound if unset.
func GetSetting[T any](ctx context.Context, s *Store, key string) (T, error) {
	var v T
	raw, err := s.r.GetSetting(ctx, key)
	if err != nil {
		return v, MapError(fmt.Errorf("get setting %q: %w", key, err))
	}
	if err := json.Unmarshal([]byte(raw), &v); err != nil {
		return v, fmt.Errorf("store: decode setting %q: %w", key, err)
	}
	return v, nil
}

// SetSetting stores v, encoded as JSON, under key.
func SetSetting(ctx context.Context, s *Store, key string, v any) error {
	raw, err := json.Marshal(v)
	if err != nil {
		return fmt.Errorf("store: encode setting %q: %w", key, err)
	}
	if err := s.w.SetSetting(ctx, db.SetSettingParams{Key: key, Value: string(raw)}); err != nil {
		return MapError(fmt.Errorf("set setting %q: %w", key, err))
	}
	return nil
}

// EnqueueJob inserts a pending job. created is false (and job zero) when an active
// job already holds the same dedupe key.
func (s *Store) EnqueueJob(ctx context.Context, p db.EnqueueJobParams) (job db.Job, created bool, err error) {
	job, err = s.w.EnqueueJob(ctx, p)
	switch {
	case errors.Is(err, sql.ErrNoRows):
		return db.Job{}, false, nil
	case err != nil:
		return db.Job{}, false, MapError(fmt.Errorf("enqueue job %q: %w", p.Kind, err))
	}
	return job, true, nil
}

// ClaimJob moves the next due job to running and returns it; ok is false when no
// job is due. The claim is a single UPDATE ... RETURNING, atomic even across
// processes sharing the database.
func (s *Store) ClaimJob(ctx context.Context, now time.Time) (job db.Job, ok bool, err error) {
	job, err = s.w.ClaimJob(ctx, Millis(now))
	switch {
	case errors.Is(err, sql.ErrNoRows):
		return db.Job{}, false, nil
	case err != nil:
		return db.Job{}, false, MapError(fmt.Errorf("claim job: %w", err))
	}
	return job, true, nil
}

// CompleteJob marks a running job done. ErrNotFound if it is not running.
func (s *Store) CompleteJob(ctx context.Context, id int64, now time.Time) error {
	if err := RequireRow(s.w.CompleteJob(ctx, db.CompleteJobParams{ID: id, Now: Millis(now)})); err != nil {
		return fmt.Errorf("complete job %d: %w", id, err)
	}
	return nil
}

// Backoff bounds for failed jobs: attempt n waits base·2^(n-1), capped.
const (
	backoffBase = 30 * time.Second
	backoffMax  = 6 * time.Hour
)

// Backoff returns the delay before retrying a job that failed its attempts-th try.
// Deterministic (no jitter): jobs are few and the queue is single-process.
func Backoff(attempts int64) time.Duration {
	if attempts < 1 {
		attempts = 1
	}
	d := backoffBase
	for i := int64(1); i < attempts; i++ {
		d *= 2
		if d >= backoffMax {
			return backoffMax
		}
	}
	return d
}

// FailJob records the failure of a running job: it is retried after Backoff, or
// marked failed once max_attempts is reached. The updated job is returned.
// msg is stored and shown in the UI: it must not contain secrets.
func (s *Store) FailJob(ctx context.Context, id, attempts int64, now time.Time, msg string) (db.Job, error) {
	job, err := s.w.FailJob(ctx, db.FailJobParams{
		ID: id, Now: Millis(now), RetryAt: Millis(now.Add(Backoff(attempts))), LastError: msg,
	})
	if err != nil {
		return db.Job{}, MapError(fmt.Errorf("fail job %d: %w", id, err))
	}
	return job, nil
}

// ResetRunningJobs requeues the jobs a previous process left running. Call it
// once at startup, before the workers start.
func (s *Store) ResetRunningJobs(ctx context.Context) (int64, error) {
	n, err := s.w.ResetRunningJobs(ctx)
	if err != nil {
		return 0, MapError(fmt.Errorf("reset running jobs: %w", err))
	}
	return n, nil
}
