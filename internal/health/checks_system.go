package health

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/fs0ciety7000/gonema/internal/events"
	"github.com/fs0ciety7000/gonema/internal/importer"
	"github.com/fs0ciety7000/gonema/internal/store"
)

// ----------------------------------------------------------------- recycle bin

// RecycleSource lists the recycle bins (importer.Service).
type RecycleSource interface {
	Settings(ctx context.Context) (importer.Settings, error)
	ListRecycle(ctx context.Context) ([]importer.RecycleEntry, error)
}

// RecycleBinCheck mentions a recycle bin larger than the threshold, and files
// kept longer than the retention. It only suggests a purge: the bin is never
// emptied by Gonema. Every 6 hours and when the bin changes.
func RecycleBinCheck(src RecycleSource, settings func(context.Context) Settings, now func() time.Time) Check {
	return Check{
		ID: CheckRecycleBin, Interval: 6 * time.Hour, Timeout: time.Minute,
		Triggers: []events.Type{importer.EventRecycleChanged, events.SettingsChanged},
		Run: func(ctx context.Context) ([]Issue, error) {
			set, err := src.Settings(ctx)
			if err != nil {
				return nil, err
			}
			items, err := src.ListRecycle(ctx)
			if err != nil {
				return nil, err
			}
			return recycleIssues(items, set.RecycleRetentionDays, settings(ctx).RecycleBinMaxGB, now()), nil
		},
	}
}

func recycleIssues(items []importer.RecycleEntry, retentionDays, maxGB int, now time.Time) []Issue {
	var total, oldSize int64
	old := 0
	cutoff := now.AddDate(0, 0, -retentionDays)
	for i := range items {
		total += items[i].Size
		if retentionDays > 0 && items[i].RecycledAt.Before(cutoff) {
			old++
			oldSize += items[i].Size
		}
	}
	fix := []string{
		"Review the files in Settings > Library > Recycle bin and restore anything you want back.",
		fmt.Sprintf("Then purge the files older than %d days (or all of them) from the same page.", retentionDays),
	}
	var issues []Issue
	if limit := int64(maxGB) * 1_000_000_000; maxGB > 0 && total > limit {
		issues = append(issues, Issue{
			ID: "recycle.large", Severity: Info, Title: "The recycle bin holds " + size(total),
			Message: fmt.Sprintf("Replaced and removed files (%s) are kept there so nothing is lost by mistake, "+
				"and they now take %s. Gonema never empties the bin on its own.", plural(len(items), "file", "files"), size(total)),
			Fix:  append(fix, fmt.Sprintf("The %d GB threshold can be changed in Settings > Health.", maxGB)),
			Link: linkRecycleBin,
		})
	}
	if old > 0 {
		issues = append(issues, Issue{
			ID: "recycle.expired", Severity: Info,
			Title: fmt.Sprintf("%s in the recycle bin %s older than %d days", plural(old, "file", "files"), isAre(old), retentionDays),
			Message: fmt.Sprintf("They're past the retention you set (%d days) and take %s. Gonema doesn't delete them by itself: "+
				"purging is your call.", retentionDays, size(oldSize)),
			Fix:  fix,
			Link: linkRecycleBin,
		})
	}
	return issues
}

func isAre(n int) string {
	if n == 1 {
		return "is"
	}
	return "are"
}

// ----------------------------------------------------------------- database

// Database is the SQLite maintenance the database check uses (*store.Store).
type Database interface {
	QuickCheck(ctx context.Context) ([]string, error)
	WALSize() (int64, error)
}

// Database check tuning.
const (
	// IntegrityEvery is the period of the integrity check (it reads the whole file).
	IntegrityEvery = 7 * 24 * time.Hour
	// MaxWALSize is the write-ahead log size above which the check speaks up.
	MaxWALSize = 512 << 20
	// settingIntegrity keeps the last integrity result across restarts.
	settingIntegrity = "health.db_integrity"
)

type integrityResult struct {
	CheckedAt int64    `json:"checkedAt"` // unix ms
	Problems  []string `json:"problems"`
}

// DatabaseCheck runs SQLite's quick integrity check weekly (the result is kept
// across restarts) and watches the size of the write-ahead log, hourly.
func DatabaseCheck(st *store.Store, dbase Database, now func() time.Time) Check {
	return Check{
		ID: CheckDatabase, Interval: time.Hour, Timeout: 10 * time.Minute,
		Run: func(ctx context.Context) ([]Issue, error) {
			res, err := store.GetSetting[integrityResult](ctx, st, settingIntegrity)
			if err != nil && !errors.Is(err, store.ErrNotFound) {
				res = integrityResult{} // unreadable: check again
			}
			if t := now(); res.CheckedAt == 0 || t.Sub(store.FromMillis(res.CheckedAt)) >= IntegrityEvery {
				problems, err := dbase.QuickCheck(ctx)
				if err != nil {
					return nil, err
				}
				res = integrityResult{CheckedAt: store.Millis(t), Problems: problems}
				if err := store.SetSetting(ctx, st, settingIntegrity, res); err != nil {
					return nil, err
				}
			}
			var issues []Issue
			if n := len(res.Problems); n > 0 {
				more := ""
				if n > 1 {
					more = fmt.Sprintf(" (and %d more)", n-1)
				}
				issues = append(issues, Issue{
					ID: "database.integrity", Severity: Error, Title: "The database reports damage",
					Message: fmt.Sprintf("SQLite's integrity check found problems in gonema.db: %s%s. Gonema keeps running, but some "+
						"data may be wrong, and the damage can spread with new writes.", strings.TrimSpace(res.Problems[0]), more),
					Fix: []string{
						"Stop Gonema and copy gonema.db, gonema.db-wal and gonema.key somewhere safe.",
						"Restore the latest backup from Settings > Backups, or run sqlite3 gonema.db \".recover\" on the copy.",
						"Check the disk for errors (SMART status, file system check) before starting again.",
					},
					Link: linkBackups, Since: store.FromMillis(res.CheckedAt),
				})
			}
			wal, err := dbase.WALSize()
			if err != nil {
				return nil, err
			}
			if wal > MaxWALSize {
				issues = append(issues, Issue{
					ID: "database.wal_large", Severity: Info, Title: "The database journal has grown to " + size(wal),
					Message: "SQLite writes changes to gonema.db-wal before folding them into the database. It's usually a few MB; " +
						"a large one only wastes disk space. It shrinks when Gonema restarts.",
					Fix: []string{
						"Restart Gonema at a quiet moment.",
						"If it grows again, check that no other program (backup tool, database viewer) keeps gonema.db open.",
					},
					Link: linkSystem,
				})
			}
			return issues, nil
		},
	}
}
