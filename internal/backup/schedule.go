package backup

import (
	"context"
	"errors"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"time"

	"github.com/fs0ciety7000/gonema/internal/jobs"
	"github.com/fs0ciety7000/gonema/internal/store"
	"github.com/fs0ciety7000/gonema/internal/store/db"
)

// JobScheduled is the job kind of scheduled backups.
const JobScheduled = "backup.scheduled"

// SettingKey is the settings key of Settings (JSON).
const SettingKey = "backup.settings"

// Settings configure the scheduled backups.
type Settings struct {
	Enabled       bool `json:"enabled"`
	IntervalHours int  `json:"intervalHours" minimum:"1" maximum:"720" doc:"Hours between two scheduled backups; default 168 (weekly)"`
	Keep          int  `json:"keep" minimum:"1" maximum:"100" doc:"Scheduled backups kept; older scheduled ones are deleted (manual, uploaded and pre-restore backups are kept until you delete them)"`
	IncludeKey    bool `json:"includeKey" doc:"Also store gonema.key in scheduled backups. Warning: whoever gets such an archive can decrypt every stored secret"`
}

// DefaultSettings returns the defaults: weekly, keep 4.
func DefaultSettings() Settings { return Settings{Enabled: true, IntervalHours: 168, Keep: 4} }

// Validate checks the ranges.
func (s *Settings) Validate() error {
	switch {
	case s.IntervalHours < 1 || s.IntervalHours > 720:
		return userErr(ErrInvalid, nil, "The backup interval must be between 1 and 720 hours (30 days).")
	case s.Keep < 1 || s.Keep > 100:
		return userErr(ErrInvalid, nil, "The number of scheduled backups kept must be between 1 and 100.")
	}
	return nil
}

// SettingsStore reads and writes Settings; it caches the interval for the job
// scheduler.
type SettingsStore struct {
	St       *store.Store
	interval atomic.Int64 // nanoseconds; 0 until loaded
}

// Get returns the settings (defaults when unset or invalid).
func (ss *SettingsStore) Get(ctx context.Context) Settings {
	set := DefaultSettings()
	v, err := store.GetSetting[Settings](ctx, ss.St, SettingKey)
	if err == nil && v.Validate() == nil {
		set = v
	}
	ss.interval.Store(int64(time.Duration(set.IntervalHours) * time.Hour))
	return set
}

// Set validates and stores the settings.
func (ss *SettingsStore) Set(ctx context.Context, set Settings) (Settings, error) {
	if err := set.Validate(); err != nil {
		return Settings{}, err
	}
	if err := store.SetSetting(ctx, ss.St, SettingKey, set); err != nil {
		return Settings{}, err
	}
	ss.interval.Store(int64(time.Duration(set.IntervalHours) * time.Hour))
	return set, nil
}

// Interval returns the cached interval (read by the job scheduler).
func (ss *SettingsStore) Interval() time.Duration {
	if v := ss.interval.Load(); v > 0 {
		return time.Duration(v)
	}
	return time.Duration(DefaultSettings().IntervalHours) * time.Hour
}

// RegisterJobs registers the scheduled backup with the runner. Call before
// the runner starts; ss.Get must have run once (it loads the interval).
func (s *Service) RegisterJobs(r *jobs.Runner, ss *SettingsStore) {
	r.Handle(JobScheduled, func(ctx context.Context, _ db.Job) error {
		set := ss.Get(ctx)
		if !set.Enabled {
			s.log.Debug("scheduled backups are turned off")
			return nil
		}
		if _, err := s.Create(ctx, KindScheduled, set.IncludeKey); err != nil {
			if errors.Is(err, ErrBusy) {
				return err // retried later by the runner
			}
			s.log.Error("scheduled backup failed", slog.Any("error", err))
			s.lastFailure.Store(&failure{err: err, at: s.now()})
			return err
		}
		s.lastFailure.Store(nil)
		if _, err := s.Prune(ctx, set.Keep); err != nil {
			s.log.Warn("could not delete old scheduled backups", slog.Any("error", err))
		}
		return nil
	}, jobs.HandlerOptions{Timeout: 30 * time.Minute})
	r.EveryFunc(JobScheduled, ss.Interval, 10*time.Minute, nil)
}

type failure struct {
	err error
	at  time.Time
}

// LastScheduledFailure returns the error of the last scheduled backup of this
// process when it failed, and when (nil when it succeeded or never ran).
func (s *Service) LastScheduledFailure() (error, time.Time) { //nolint:staticcheck // (error, time) reads naturally here
	if f := s.lastFailure.Load(); f != nil {
		return f.err, f.at
	}
	return nil, time.Time{}
}

// CleanTemp removes the temporary folders an interrupted backup or upload left
// in the backups folder (Gonema's own files). Call at startup.
func (s *Service) CleanTemp() {
	entries, err := os.ReadDir(s.Dir())
	if err != nil {
		return
	}
	for _, e := range entries {
		if e.IsDir() && (strings.HasPrefix(e.Name(), ".tmp-") || strings.HasPrefix(e.Name(), uploadPrefix)) {
			if err := os.RemoveAll(filepath.Join(s.Dir(), e.Name())); err != nil {
				s.log.Warn("could not remove a temporary backup folder", slog.String("name", e.Name()), slog.Any("error", err))
			}
		}
	}
}
