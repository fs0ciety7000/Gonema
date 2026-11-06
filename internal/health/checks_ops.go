package health

import (
	"context"
	"fmt"
	"strconv"
	"time"

	"github.com/fs0ciety7000/gonema/internal/events"
	"github.com/fs0ciety7000/gonema/internal/importer"
)

// Checks of phase 8 operations.
const (
	CheckImports = "imports"
	CheckBackups = "backups"
)

// JournalSource lists the interrupted imports that need attention
// (importer.Service).
type JournalSource interface {
	ListJournal(ctx context.Context, all bool) ([]importer.JournalEntry, error)
}

// ImportJournalCheck reports each interrupted import that startup recovery
// could not settle on its own (see importer.Recover): the user checks the
// files, then dismisses the entry. Runs hourly and when an entry changes.
func ImportJournalCheck(src JournalSource) Check {
	return Check{
		ID: CheckImports, Interval: time.Hour,
		Triggers: []events.Type{importer.EventImportJournal},
		Run: func(ctx context.Context) ([]Issue, error) {
			entries, err := src.ListJournal(ctx, false)
			if err != nil {
				return nil, err
			}
			issues := make([]Issue, 0, len(entries))
			for _, e := range entries {
				issues = append(issues, Issue{
					ID: "import.interrupted:" + strconv.FormatInt(e.ID, 10), Severity: Warning,
					Title: "An import was interrupted and needs a check",
					Message: e.Message + " Gonema changed nothing on disk for this import after the restart: " +
						"it never deletes your files on a guess.",
					Fix: []string{
						fmt.Sprintf("Look at the source (%s) and the destination (%s).", e.Source, e.Destination),
						"Put the file where you want it, then rescan the movie or import it manually.",
						"Then dismiss this entry (Import journal, or POST /api/v1/imports/journal/" + strconv.FormatInt(e.ID, 10) + "/dismiss?confirm=true).",
					},
					Link:  linkSystem,
					Since: e.UpdatedAt,
				})
			}
			return issues, nil
		},
	}
}

// BackupStatus says when the last scheduled backup ran (backup service).
type BackupStatus interface {
	// LastScheduledFailure returns the error of the last scheduled backup
	// when it failed (nil when it succeeded or never ran) and its time.
	LastScheduledFailure() (error, time.Time)
}

// BackupCheck reports a failed scheduled backup. Runs every 6 hours.
func BackupCheck(src BackupStatus) Check {
	return Check{
		ID: CheckBackups, Interval: 6 * time.Hour,
		Run: func(context.Context) ([]Issue, error) {
			err, at := src.LastScheduledFailure()
			if err == nil {
				return nil, nil
			}
			return []Issue{{
				ID: "backup.failed", Severity: Warning, Title: "The last scheduled backup failed",
				Message: "Gonema could not back up the database (" + err.Error() + "). The previous backups are kept.",
				Fix: []string{
					"Check the free space and the permissions of the backups folder in the data directory.",
					"Then make a backup now from Settings > Backups to confirm it works.",
				},
				Link: linkBackups, Since: at,
			}}, nil
		},
	}
}
