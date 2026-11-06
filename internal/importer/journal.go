package importer

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"log/slog"
	"os"
	"path/filepath"
	"time"

	"github.com/fs0ciety7000/gonema/internal/events"
	"github.com/fs0ciety7000/gonema/internal/fsx"
	"github.com/fs0ciety7000/gonema/internal/store"
	"github.com/fs0ciety7000/gonema/internal/store/db"
)

// Steps of an import where Options.Fault is called (tests inject crashes).
const (
	FaultAfterIntent   = "after-intent"   // journal written, nothing touched yet
	FaultAfterTransfer = "after-transfer" // file linked, copied or moved
	FaultAfterRecycle  = "after-recycle"  // replaced file in the recycle bin
	FaultBeforeCommit  = "before-commit"  // file in place, database not written
)

// Journal statuses (import_journal.status).
const (
	JournalPending   = "pending"
	JournalDone      = "done"
	JournalFailed    = "failed"
	JournalAttention = "attention"
	JournalDismissed = "dismissed"
)

// journalRetention is how long done and dismissed entries are kept.
const journalRetention = 30 * 24 * time.Hour

// journalRecord is what the journal keeps to finish an import after a crash.
type journalRecord struct {
	File       db.UpsertMovieFileParams `json:"file"`
	Event      string                   `json:"event"`
	Title      string                   `json:"title"`
	Quality    string                   `json:"quality"`
	Score      int64                    `json:"score"`
	DownloadID int64                    `json:"downloadId,omitempty"`
	Source     string                   `json:"source"`
	Bin        string                   `json:"bin"`
	// PreviousPath is the file the import replaces (absolute), "" for none;
	// PreviousSize is its size.
	PreviousPath string `json:"previousPath,omitempty"`
	PreviousSize int64  `json:"previousSize,omitempty"`
}

// fileParams returns the movie file row of a planned import named name.
func (s *Service) fileParams(p *plan, name string) db.UpsertMovieFileParams {
	parsedJSON, err := json.Marshal(&p.parsed)
	if err != nil {
		parsedJSON = []byte("{}")
	}
	langs := make([]string, 0, len(p.parsed.Languages))
	for _, l := range p.parsed.Languages {
		langs = append(langs, l.String())
	}
	langJSON, _ := json.Marshal(langs)
	media, _ := json.Marshal(mediaInfo(&p.parsed))
	return db.UpsertMovieFileParams{
		MovieID: p.t.movie.ID, RelativePath: name, Size: p.size, Quality: string(p.quality),
		Revision: int64(max(p.parsed.Revision.Version, 1)), Edition: p.parsed.Edition.String(),
		Languages: string(langJSON), ReleaseGroup: p.parsed.ReleaseGroup, SceneName: p.scene,
		Parsed: string(parsedJSON), MediaInfo: string(media), CustomScore: int64(p.score.Total),
	}
}

// journalBegin records the intent of an import. place is where the transfer
// writes (dst, or a staging name when staging).
func (s *Service) journalBegin(ctx context.Context, p *plan, set *Settings, dst, place string, staging bool, curPath string,
	file *db.UpsertMovieFileParams) (int64, error) {
	rec := journalRecord{
		File: *file, Event: historyImported, Title: p.scene, Quality: string(p.quality), Score: int64(p.score.Total),
		DownloadID: p.downloadID, Source: p.source, Bin: binDir(set, p.t.root.Path), PreviousPath: curPath,
	}
	if p.t.current != nil {
		rec.Event = historyUpgraded
		rec.PreviousSize = p.t.current.Size
	}
	raw, err := json.Marshal(&rec)
	if err != nil {
		return 0, fmt.Errorf("importer: encode the import journal: %w", err)
	}
	arg := db.CreateImportJournalParams{
		MovieID: p.t.movie.ID, Source: p.src, Destination: dst, Mode: string(p.mode), Size: p.size,
		Record: string(raw), CreatedAt: store.Millis(s.now()),
	}
	if staging {
		arg.Staging = place
	}
	if p.downloadID > 0 {
		arg.DownloadID = &p.downloadID
	}
	row, err := s.st.Write().CreateImportJournal(ctx, arg)
	if err != nil {
		return 0, store.MapError(fmt.Errorf("importer: write the import journal: %w", err))
	}
	return row.ID, nil
}

func (s *Service) faultAt(step string) error {
	if s.fault == nil {
		return nil
	}
	if err := s.fault(step); err != nil {
		return fmt.Errorf("importer: stopped at %s: %w", step, err)
	}
	return nil
}

// journalDone marks an entry done; q is the transaction of the movie file row.
func (s *Service) journalDone(ctx context.Context, q *db.Queries, id int64, msg string) error {
	if _, err := q.SetImportJournalStatus(ctx, db.SetImportJournalStatusParams{
		Status: JournalDone, Message: msg, UpdatedAt: store.Millis(s.now()), ID: id,
	}); err != nil {
		return fmt.Errorf("mark the import journal done: %w", err)
	}
	return nil
}

// journalSet changes the status of an entry outside any transaction (best
// effort: a failure is logged).
func (s *Service) journalSet(ctx context.Context, id int64, status, msg string) {
	if _, err := s.st.Write().SetImportJournalStatus(context.WithoutCancel(ctx), db.SetImportJournalStatusParams{
		Status: status, Message: msg, UpdatedAt: store.Millis(s.now()), ID: id,
	}); err != nil {
		s.log.Error("import journal: record the outcome", slog.Int64("journal_id", id), slog.String("status", status),
			slog.Any("error", store.MapError(err)))
	}
}

// journalFail marks an entry failed after an import error that was undone.
func (s *Service) journalFail(ctx context.Context, id int64, err error) {
	msg := err.Error()
	var ue *Error
	if errors.As(err, &ue) {
		msg = ue.Msg
	}
	s.journalSet(ctx, id, JournalFailed, msg)
}

// RecoverySummary counts what Recover did.
type RecoverySummary struct {
	Completed int // the file was in place: the database was completed
	Failed    int // nothing had moved: the import runs again
	Attention int // ambiguous: a health issue asks the user to check
	Cleaned   int // temporary partial copies removed
}

// Recover reconciles the imports a crash interrupted (pending journal
// entries). Call it at startup, before the queue tracker resumes imports:
//
//   - the new file is at its destination (moved, linked or copied): the
//     database is completed (movie file, history, download imported); an
//     interrupted replacement is finished (the old file goes to the recycle
//     bin, as the import intended);
//   - the source is untouched and the destination absent: the entry is marked
//     failed; a download stays "importing" and is imported again;
//   - anything else (both missing, an unexpected file at the destination):
//     the entry needs attention and a health issue says so. No user file is
//     ever deleted.
//
// Leftover partial copies (".gonema-partial-*") in the destination folder are
// removed. Done and dismissed entries older than 30 days are pruned.
func (s *Service) Recover(ctx context.Context) (RecoverySummary, error) {
	var sum RecoverySummary
	rows, err := s.st.Read().ListImportJournalByStatus(ctx, JournalPending)
	if err != nil {
		return sum, store.MapError(fmt.Errorf("importer: read the import journal: %w", err))
	}
	for i := range rows {
		j := &rows[i]
		if removed, err := s.fs.RemovePartials(filepath.Dir(j.Destination)); err == nil {
			sum.Cleaned += len(removed)
		} else {
			s.log.Warn("import recovery: partial copies not removed", slog.String("folder", filepath.Dir(j.Destination)), slog.Any("error", err))
		}
		switch status := s.recoverOne(ctx, j); status {
		case JournalDone:
			sum.Completed++
		case JournalFailed:
			sum.Failed++
		default:
			sum.Attention++
		}
	}
	if _, err := s.st.Write().PruneImportJournal(ctx, store.Millis(s.now().Add(-journalRetention))); err != nil {
		s.log.Warn("import journal: prune", slog.Any("error", store.MapError(err)))
	}
	if len(rows) > 0 {
		s.log.Warn("imports interrupted by the previous shutdown were reconciled", slog.Int("completed", sum.Completed),
			slog.Int("retried", sum.Failed), slog.Int("need_attention", sum.Attention), slog.Int("partial_copies_removed", sum.Cleaned))
	}
	return sum, nil
}

// stat returns the file info of p, nil when it does not exist.
func (s *Service) stat(p string) (fs.FileInfo, error) {
	if p == "" {
		return nil, nil
	}
	fi, err := s.fs.Lstat(p)
	if errors.Is(err, fs.ErrNotExist) {
		return nil, nil
	}
	return fi, err
}

// recoverOne reconciles one pending entry and returns its new status.
func (s *Service) recoverOne(ctx context.Context, j *db.ImportJournal) string {
	unlock := s.lockMovie(j.MovieID)
	defer unlock()
	log := s.log.With(slog.Int64("journal_id", j.ID), slog.Int64("movie_id", j.MovieID))
	attention := func(msg string) string {
		log.Warn("interrupted import needs attention", slog.String("reason", msg))
		s.journalSet(ctx, j.ID, JournalAttention, msg)
		s.publish(events.Event{Type: EventImportJournal, MovieID: j.MovieID, Data: map[string]any{"id": j.ID, "status": JournalAttention}})
		return JournalAttention
	}
	var rec journalRecord
	if err := json.Unmarshal([]byte(j.Record), &rec); err != nil {
		return attention("The journal entry of this import is unreadable; check the files by hand.")
	}
	src, err1 := s.stat(j.Source)
	dst, err2 := s.stat(j.Destination)
	stg, err3 := s.stat(j.Staging)
	if err := errors.Join(err1, err2, err3); err != nil {
		return attention(fmt.Sprintf("The files of this import cannot be checked (%v).", err))
	}
	base := filepath.Base(j.Destination)

	if stg != nil {
		// Staged next to the file it replaces: the transfer completed. Finish
		// the replacement as the import intended.
		if stg.Size() != j.Size {
			return attention(fmt.Sprintf("The staged copy %s does not have the expected size; check it and %s by hand.", filepath.Base(j.Staging), base))
		}
		if dst != nil {
			if _, err := s.fs.Recycle(ctx, j.Destination, rec.Bin, fsx.Manifest{
				MovieID: j.MovieID, Reason: "replaced by an import completed after a restart",
			}); err != nil {
				return attention(fmt.Sprintf("The previous file %s could not be moved to the recycle bin (%v); the new one waits as %s.",
					base, err, filepath.Base(j.Staging)))
			}
		}
		if _, err := s.fs.Move(ctx, j.Staging, j.Destination); err != nil {
			return attention(fmt.Sprintf("The new file could not be renamed from %s to %s (%v).", filepath.Base(j.Staging), base, err))
		}
		return s.complete(ctx, j, &rec, src != nil, log, attention)
	}

	isNew := false
	switch {
	case dst == nil:
	case dst.Size() != j.Size:
		if j.Staging == "" {
			return attention(fmt.Sprintf("A file is at the destination (%s) but it is not the one being imported (size %d, expected %d); check both by hand.",
				base, dst.Size(), j.Size))
		}
		// Staging case: the destination still holds the previous file.
	case j.Staging == "" || rec.PreviousSize != j.Size:
		isNew = true
	case j.Mode == string(fsx.ModeMove):
		isNew = src == nil
	case src != nil && os.SameFile(src, dst):
		isNew = true
	default:
		return attention(fmt.Sprintf("Gonema cannot tell whether %s is the new file or the one it replaces (same size); check it by hand.", base))
	}

	switch {
	case isNew:
		if rec.PreviousPath != "" && rec.PreviousPath != j.Destination {
			if prev, err := s.stat(rec.PreviousPath); err == nil && prev != nil {
				if _, err := s.fs.Recycle(ctx, rec.PreviousPath, rec.Bin, fsx.Manifest{
					MovieID: j.MovieID, Reason: "replaced by an import completed after a restart",
				}); err != nil {
					log.Warn("import recovery: the replaced file stays in the movie folder", slog.String("path", rec.PreviousPath), slog.Any("error", err))
				}
			}
		}
		return s.complete(ctx, j, &rec, src != nil, log, attention)
	case src != nil:
		msg := "Gonema stopped before the file was transferred; nothing changed."
		if j.DownloadID != nil {
			msg += " The download is imported again."
		} else {
			msg += " Run the import again."
		}
		log.Info("interrupted import: nothing was transferred", slog.String("source", j.Source))
		s.journalSet(ctx, j.ID, JournalFailed, msg)
		return JournalFailed
	}
	return attention(fmt.Sprintf("Neither the source (%s) nor the destination (%s) of this import exists any more; check the movie folder and the download folder.",
		j.Source, j.Destination))
}

// complete writes the database rows of an import whose file is in place.
func (s *Service) complete(ctx context.Context, j *db.ImportJournal, rec *journalRecord, sourceKept bool, log *slog.Logger,
	attention func(string) string) string {
	now := s.now()
	reason := fmt.Sprintf("Imported as %s, completed after a restart (Gonema stopped between placing the file and recording it)", rec.File.RelativePath)
	if sourceKept && j.Mode == string(fsx.ModeMove) {
		reason += "; the source is still in the download folder: remove it once you have checked the library copy"
	}
	data := map[string]any{
		"reason": reason, "path": rec.File.RelativePath, "size": rec.File.Size, "source": rec.Source,
		"sourcePath": j.Source, "recovered": true, "journalId": j.ID,
	}
	rec.File.ImportedAt = store.Millis(now)
	var row db.MovieFile
	err := s.st.WithTx(ctx, func(q *db.Queries) error {
		var err error
		if row, err = q.UpsertMovieFile(ctx, rec.File); err != nil {
			return fmt.Errorf("record movie file: %w", err)
		}
		score := rec.Score
		if err := s.appendHistory(ctx, q, &historyRow{
			movieID: j.MovieID, event: rec.Event, title: rec.Title, quality: rec.Quality, score: &score,
			downloadID: rec.DownloadID, data: data,
		}); err != nil {
			return err
		}
		if j.DownloadID != nil {
			if err := markDownloadImported(ctx, q, *j.DownloadID, now); err != nil {
				return err
			}
		}
		return s.journalDone(ctx, q, j.ID, reason)
	})
	if err != nil {
		return attention(fmt.Sprintf("The file is in place but the database could not be completed (%v).", err))
	}
	log.Warn("interrupted import completed", slog.String("path", filepath.Join(filepath.Dir(j.Destination), rec.File.RelativePath)))
	s.publish(events.Event{Type: EventFileImported, MovieID: j.MovieID, Data: map[string]any{
		"fileId": row.ID, "path": rec.File.RelativePath, "quality": rec.Quality, "upgraded": rec.Event == historyUpgraded,
		"reason": reason, "downloadId": rec.DownloadID, "recovered": true,
	}})
	return JournalDone
}

// markDownloadImported sets a download imported unless it already is (or
// left the queue).
func markDownloadImported(ctx context.Context, q *db.Queries, id int64, now time.Time) error {
	d, err := q.GetDownload(ctx, id)
	if errors.Is(store.MapError(err), store.ErrNotFound) {
		return nil
	}
	if err != nil {
		return fmt.Errorf("get download %d: %w", id, err)
	}
	if d.Status == "imported" || d.Status == "removed" {
		return nil
	}
	at := store.Millis(now)
	if _, err := q.UpdateDownloadState(ctx, db.UpdateDownloadStateParams{
		ID: id, Status: "imported", Progress: 1, OutputPath: d.OutputPath, CompletedAt: d.CompletedAt, ImportedAt: &at,
		StatusMessages: `["Imported after a restart: the import had been interrupted."]`,
	}); err != nil {
		return fmt.Errorf("mark download %d imported: %w", id, err)
	}
	return nil
}

// EventImportJournal is published when an interrupted import needs
// attention or is dismissed.
const EventImportJournal events.Type = "import.journal"

// JournalEntry is an import journal entry as the API shows it.
type JournalEntry struct {
	ID          int64     `json:"id"`
	MovieID     int64     `json:"movieId"`
	DownloadID  int64     `json:"downloadId,omitempty"`
	Source      string    `json:"source"`
	Destination string    `json:"destination"`
	Mode        string    `json:"mode" enum:"move,copy,hardlink"`
	Status      string    `json:"status" enum:"pending,done,failed,attention,dismissed"`
	Message     string    `json:"message"`
	CreatedAt   time.Time `json:"createdAt"`
	UpdatedAt   time.Time `json:"updatedAt"`
}

func journalEntryOf(j *db.ImportJournal) JournalEntry {
	e := JournalEntry{
		ID: j.ID, MovieID: j.MovieID, Source: j.Source, Destination: j.Destination, Mode: j.Mode, Status: j.Status,
		Message: j.Message, CreatedAt: store.FromMillis(j.CreatedAt), UpdatedAt: store.FromMillis(j.UpdatedAt),
	}
	if j.DownloadID != nil {
		e.DownloadID = *j.DownloadID
	}
	return e
}

// ListJournal returns the entries that need attention (all: the latest 500
// entries of every status), newest first.
func (s *Service) ListJournal(ctx context.Context, all bool) ([]JournalEntry, error) {
	var rows []db.ImportJournal
	var err error
	if all {
		rows, err = s.st.Read().ListImportJournal(ctx, 500)
	} else {
		rows, err = s.st.Read().ListImportJournalByStatus(ctx, JournalAttention)
	}
	if err != nil {
		return nil, store.MapError(fmt.Errorf("importer: list the import journal: %w", err))
	}
	out := make([]JournalEntry, 0, len(rows))
	for i := len(rows) - 1; i >= 0 && !all; i-- {
		out = append(out, journalEntryOf(&rows[i]))
	}
	for i := 0; i < len(rows) && all; i++ {
		out = append(out, journalEntryOf(&rows[i]))
	}
	return out, nil
}

// DismissJournal marks an entry needing attention as handled by the user. No
// file is touched. confirm must be true.
func (s *Service) DismissJournal(ctx context.Context, id int64, confirm bool) (JournalEntry, error) {
	if !confirm {
		return JournalEntry{}, userErr(ErrConfirmRequired, nil, "Confirm that you checked the files of this import by hand (confirm=true).")
	}
	j, err := s.st.Read().GetImportJournal(ctx, id)
	if err != nil {
		if errors.Is(store.MapError(err), store.ErrNotFound) {
			return JournalEntry{}, userErr(ErrNotFound, nil, "Import journal entry %d does not exist.", id)
		}
		return JournalEntry{}, store.MapError(fmt.Errorf("importer: get journal entry %d: %w", id, err))
	}
	if j.Status != JournalAttention {
		return JournalEntry{}, userErr(ErrInvalid, nil, "Only an interrupted import that needs attention can be dismissed; this one is %s.", j.Status)
	}
	row, err := s.st.Write().SetImportJournalStatus(ctx, db.SetImportJournalStatusParams{
		Status: JournalDismissed, Message: j.Message, UpdatedAt: store.Millis(s.now()), ID: id,
	})
	if err != nil {
		return JournalEntry{}, store.MapError(fmt.Errorf("importer: dismiss journal entry %d: %w", id, err))
	}
	s.log.Info("interrupted import dismissed by the user", slog.Int64("journal_id", id), slog.Int64("movie_id", j.MovieID))
	s.publish(events.Event{Type: EventImportJournal, MovieID: j.MovieID, Data: map[string]any{"id": id, "status": JournalDismissed}})
	return journalEntryOf(&row), nil
}
