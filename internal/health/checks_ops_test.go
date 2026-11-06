package health

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/fs0ciety7000/gonema/internal/importer"
)

type fakeJournal []importer.JournalEntry

func (f fakeJournal) ListJournal(context.Context, bool) ([]importer.JournalEntry, error) {
	return f, nil
}

type fakeBackup struct {
	err error
	at  time.Time
}

func (f fakeBackup) LastScheduledFailure() (error, time.Time) { return f.err, f.at } //nolint:staticcheck // mirrors the interface

func TestImportJournalCheck(t *testing.T) {
	ctx := context.Background()
	if issues, err := ImportJournalCheck(fakeJournal{}).Run(ctx); err != nil || len(issues) != 0 {
		t.Errorf("empty = %+v %v", issues, err)
	}
	issues, err := ImportJournalCheck(fakeJournal{{ID: 7, Source: "/dl/a.mkv", Destination: "/movies/A/a.mkv", Message: "Neither exists."}}).Run(ctx)
	if err != nil || len(issues) != 1 || issues[0].ID != "import.interrupted:7" || issues[0].Severity != Warning ||
		!strings.Contains(issues[0].Fix[0], "/dl/a.mkv") || !strings.Contains(issues[0].Fix[2], "/imports/journal/7/dismiss") {
		t.Errorf("issues = %+v %v", issues, err)
	}
}

func TestBackupCheck(t *testing.T) {
	ctx := context.Background()
	if issues, _ := BackupCheck(fakeBackup{}).Run(ctx); len(issues) != 0 {
		t.Errorf("ok = %+v", issues)
	}
	issues, _ := BackupCheck(fakeBackup{err: errors.New("disk full"), at: time.Unix(1, 0)}).Run(ctx)
	if len(issues) != 1 || issues[0].ID != "backup.failed" || !strings.Contains(issues[0].Message, "disk full") {
		t.Errorf("failed = %+v", issues)
	}
}
