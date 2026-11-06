package importer

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"strconv"
	"sync"
	"time"

	"github.com/fs0ciety7000/gonema/internal/events"
	"github.com/fs0ciety7000/gonema/internal/jobs"
	"github.com/fs0ciety7000/gonema/internal/store"
	"github.com/fs0ciety7000/gonema/internal/store/db"
)

// Library scans as background jobs: a scan of a large library takes minutes
// (one TMDB lookup per folder), so it runs in the job queue, keeps going when
// the page is left, streams each folder as it is described, can be cancelled,
// and keeps its results (library_import_scans) for a later review.

// JobLibraryScan is the job kind of library scans.
const JobLibraryScan = "library_import.scan"

// Library scan events.
const (
	EventScanFolder   events.Type = "libraryimport.folder"   // data: ScanFolderEvent
	EventScanProgress events.Type = "libraryimport.progress" // data: LibraryScan without folders
	EventScanFinished events.Type = "libraryimport.finished" // data: LibraryScan without folders
)

// Scan states.
const (
	ScanPending   = "pending"
	ScanRunning   = "running"
	ScanDone      = "done"
	ScanCancelled = "cancelled"
	ScanFailed    = "failed"
)

// Scan tuning.
const (
	keepScans      = 20
	scanSaveEvery  = 2 * time.Second
	scanJobTimeout = 6 * time.Hour
)

// LibraryScan is a scan of existing movie folders.
type LibraryScan struct {
	ID           int64           `json:"id"`
	JobID        *int64          `json:"jobId"`
	RootFolderID int64           `json:"rootFolderId"`
	Path         string          `json:"path" doc:"Scanned sub-path, relative to the root folder; empty for the root folder"`
	Status       string          `json:"status" enum:"pending,running,done,cancelled,failed"`
	Total        int             `json:"total" doc:"Folders to describe (known once the folder is listed)"`
	Done         int             `json:"done"`
	Error        string          `json:"error,omitempty"`
	CreatedAt    time.Time       `json:"createdAt"`
	UpdatedAt    time.Time       `json:"updatedAt"`
	Counts       ScanCounts      `json:"counts" doc:"Folders by status"`
	Folders      []LibraryFolder `json:"folders,omitempty" doc:"Described folders, in folder order (GET /library-import/scans/{id} only)"`
}

// ScanCounts counts the folders of a scan by status.
type ScanCounts struct {
	New       int `json:"new"`
	Ambiguous int `json:"ambiguous"`
	NoMatch   int `json:"noMatch"`
	InLibrary int `json:"inLibrary"`
}

func countFolders(fs []LibraryFolder) ScanCounts {
	var c ScanCounts
	for i := range fs {
		switch fs[i].Status {
		case FolderNew:
			c.New++
		case FolderAmbiguous:
			c.Ambiguous++
		case FolderNoMatch:
			c.NoMatch++
		case FolderInLibrary:
			c.InLibrary++
		}
	}
	return c
}

// ScanFolderEvent is the data of libraryimport.folder events.
type ScanFolderEvent struct {
	ScanID int64         `json:"scanId"`
	Done   int           `json:"done"`
	Total  int           `json:"total"`
	Folder LibraryFolder `json:"folder"`
}

type scanPayload struct {
	ScanID int64 `json:"scanId"`
}

// scanState keeps the folders found by the running scans of this process.
type scanState struct {
	mu   sync.Mutex
	live map[int64][]LibraryFolder
}

func (s *Service) liveFolders(id int64) ([]LibraryFolder, bool) {
	s.scans.mu.Lock()
	defer s.scans.mu.Unlock()
	f, ok := s.scans.live[id]
	return append([]LibraryFolder(nil), f...), ok
}

func (s *Service) setLive(id int64, f []LibraryFolder) {
	s.scans.mu.Lock()
	defer s.scans.mu.Unlock()
	if s.scans.live == nil {
		s.scans.live = map[int64][]LibraryFolder{}
	}
	if f == nil {
		delete(s.scans.live, id)
		return
	}
	s.scans.live[id] = f
}

// JobRunner is the part of the job runner RegisterJobs uses.
type JobRunner interface {
	Handle(kind string, fn jobs.Handler, opts jobs.HandlerOptions)
}

// RegisterJobs registers the library scan job.
func (s *Service) RegisterJobs(r JobRunner) {
	r.Handle(JobLibraryScan, s.handleScan, jobs.HandlerOptions{Timeout: scanJobTimeout})
}

// StartLibraryScan queues a scan of a root folder, or of rel inside it.
func (s *Service) StartLibraryScan(ctx context.Context, rootFolderID int64, rel string) (LibraryScan, error) {
	if s.lib == nil {
		return LibraryScan{}, ErrNoProvider
	}
	if s.jobs == nil || !s.jobs.Has(JobLibraryScan) {
		return LibraryScan{}, userErr(ErrInvalid, nil, "background scans are not available on this server")
	}
	root, err := s.rootFolder(ctx, rootFolderID)
	if err != nil {
		return LibraryScan{}, err
	}
	if _, rel, err = s.scanBase(&root, rel); err != nil {
		return LibraryScan{}, err
	}
	now := store.Millis(s.now())
	row, err := s.st.Write().CreateLibraryScan(ctx, db.CreateLibraryScanParams{RootFolderID: root.ID, Path: rel, Now: now})
	if err != nil {
		return LibraryScan{}, store.MapError(fmt.Errorf("importer: create scan: %w", err))
	}
	job, _, err := s.jobs.Enqueue(ctx, JobLibraryScan, scanPayload{ScanID: row.ID}, jobs.EnqueueOptions{
		DedupeKey: JobLibraryScan + ":" + strconv.FormatInt(row.ID, 10), MaxAttempts: 2,
	})
	if err != nil {
		return LibraryScan{}, fmt.Errorf("importer: enqueue scan: %w", err)
	}
	if err := s.st.Write().SetLibraryScanJob(ctx, db.SetLibraryScanJobParams{ID: row.ID, JobID: &job.ID}); err != nil {
		return LibraryScan{}, store.MapError(fmt.Errorf("importer: scan job: %w", err))
	}
	if _, err := s.st.Write().PruneLibraryScans(ctx, keepScans); err != nil {
		s.log.Warn("old library scans not pruned", slog.Any("error", store.MapError(err)))
	}
	row.JobID = &job.ID
	s.log.Info("library scan queued", slog.Int64("scan_id", row.ID), slog.Int64("root_folder_id", root.ID), slog.String("path", rel))
	return scanOf(&row, false), nil
}

func scanOf(r *db.LibraryImportScan, withFolders bool) LibraryScan {
	sc := LibraryScan{
		ID: r.ID, JobID: r.JobID, RootFolderID: r.RootFolderID, Path: r.Path, Status: r.Status, Total: int(r.Total),
		Done: int(r.Done), Error: r.Error, CreatedAt: store.FromMillis(r.CreatedAt).UTC(), UpdatedAt: store.FromMillis(r.UpdatedAt).UTC(),
	}
	var folders []LibraryFolder
	if json.Unmarshal([]byte(r.Folders), &folders) == nil {
		sc.Counts = countFolders(folders)
		if withFolders {
			sc.Folders = folders
		}
	}
	if withFolders && sc.Folders == nil {
		sc.Folders = []LibraryFolder{}
	}
	return sc
}

// LibraryScanByID returns a scan with its folders (the live ones while it runs
// in this process).
func (s *Service) LibraryScanByID(ctx context.Context, id int64) (LibraryScan, error) {
	r, err := s.st.Read().GetLibraryScan(ctx, id)
	if err != nil {
		if errors.Is(store.MapError(err), store.ErrNotFound) {
			return LibraryScan{}, userErr(ErrNotFound, nil, "library scan %d does not exist", id)
		}
		return LibraryScan{}, store.MapError(fmt.Errorf("importer: scan %d: %w", id, err))
	}
	sc := scanOf(&r, true)
	if live, ok := s.liveFolders(id); ok && len(live) > len(sc.Folders) {
		sc.Folders, sc.Done, sc.Counts = live, len(live), countFolders(live)
	}
	return sc, nil
}

// LibraryScans lists the recent scans, newest first, without their folders.
func (s *Service) LibraryScans(ctx context.Context) ([]LibraryScan, error) {
	rows, err := s.st.Read().ListLibraryScans(ctx, keepScans)
	if err != nil {
		return nil, store.MapError(fmt.Errorf("importer: list scans: %w", err))
	}
	out := make([]LibraryScan, 0, len(rows))
	for i := range rows {
		r := &rows[i]
		out = append(out, LibraryScan{
			ID: r.ID, JobID: r.JobID, RootFolderID: r.RootFolderID, Path: r.Path, Status: r.Status, Total: int(r.Total),
			Done: int(r.Done), Error: r.Error, CreatedAt: store.FromMillis(r.CreatedAt).UTC(), UpdatedAt: store.FromMillis(r.UpdatedAt).UTC(),
		})
	}
	return out, nil
}

func (s *Service) saveScan(ctx context.Context, sc *LibraryScan, folders []LibraryFolder) {
	raw, err := json.Marshal(folders)
	if err != nil {
		s.log.Error("encode scan folders", slog.Any("error", err))
		return
	}
	if folders == nil {
		raw = []byte("[]")
	}
	sc.UpdatedAt = s.now().UTC()
	if err := s.st.Write().UpdateLibraryScan(context.WithoutCancel(ctx), db.UpdateLibraryScanParams{
		ID: sc.ID, Status: sc.Status, Total: int64(sc.Total), Done: int64(sc.Done), Folders: string(raw), Error: sc.Error,
		UpdatedAt: store.Millis(sc.UpdatedAt),
	}); err != nil {
		s.log.Error("save library scan", slog.Int64("scan_id", sc.ID), slog.Any("error", store.MapError(err)))
	}
	sc.Counts = countFolders(folders)
}

func (s *Service) handleScan(ctx context.Context, job db.Job) error {
	var p scanPayload
	if err := json.Unmarshal([]byte(job.Payload), &p); err != nil || p.ScanID <= 0 {
		return fmt.Errorf("importer: invalid %s payload", job.Kind)
	}
	r, err := s.st.Read().GetLibraryScan(ctx, p.ScanID)
	if errors.Is(store.MapError(err), store.ErrNotFound) {
		return nil // pruned meanwhile
	}
	if err != nil {
		return store.MapError(err)
	}
	sc := scanOf(&r, false)
	return s.runScan(ctx, &sc)
}

// runScan describes the folders of a scan, publishing each one, and saves the
// results. A restart runs the scan again from the start.
func (s *Service) runScan(ctx context.Context, sc *LibraryScan) error {
	sc.Status, sc.Done, sc.Error = ScanRunning, 0, ""
	s.saveScan(ctx, sc, nil)
	s.publish(events.Event{Type: EventScanProgress, Data: *sc})
	defer s.setLive(sc.ID, nil)
	var (
		mu       sync.Mutex
		found    []LibraryFolder
		lastSave = s.now()
	)
	folders, err := s.ScanLibraryPath(ctx, sc.RootFolderID, sc.Path, func(done, total int, f *LibraryFolder) {
		mu.Lock()
		defer mu.Unlock()
		found = append(found, *f)
		s.setLive(sc.ID, found)
		sc.Done, sc.Total = done, total
		s.publish(events.Event{Type: EventScanFolder, Data: ScanFolderEvent{ScanID: sc.ID, Done: done, Total: total, Folder: *f}})
		if s.now().Sub(lastSave) >= scanSaveEvery {
			lastSave = s.now()
			s.saveScan(ctx, sc, found)
			s.publish(events.Event{Type: EventScanProgress, Data: *sc})
		}
	})
	mu.Lock()
	defer mu.Unlock()
	switch {
	case err == nil:
		sc.Status, sc.Total, sc.Done = ScanDone, len(folders), len(folders)
		found = folders // folder order
	case errors.Is(context.Cause(ctx), jobs.ErrCancelled):
		sc.Status, sc.Error = ScanCancelled, "Cancelled by the user; the folders found so far are kept."
	case ctx.Err() != nil:
		return err // shutdown: the job runs again at the next start
	default:
		sc.Status, sc.Error = ScanFailed, errText(err)
		if sc.Error == "" {
			sc.Error = "The scan failed; the logs have the details."
		}
		s.log.Error("library scan failed", slog.Int64("scan_id", sc.ID), slog.Any("error", err))
	}
	s.saveScan(ctx, sc, found)
	s.publish(events.Event{Type: EventScanFinished, Data: *sc})
	s.log.Info("library scan ended", slog.Int64("scan_id", sc.ID), slog.String("status", sc.Status), slog.Int("folders", len(found)))
	if sc.Status == ScanCancelled {
		return err
	}
	return nil
}
