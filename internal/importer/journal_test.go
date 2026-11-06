package importer_test

import (
	"errors"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/fs0ciety7000/gonema/internal/decision"
	"github.com/fs0ciety7000/gonema/internal/fsx"
	"github.com/fs0ciety7000/gonema/internal/importer"
	"github.com/fs0ciety7000/gonema/internal/library"
	"github.com/fs0ciety7000/gonema/internal/store"
	"github.com/fs0ciety7000/gonema/internal/store/db"
)

var errCrash = errors.New("simulated crash")

// service returns a new importer on the env's store, as after a restart;
// crashAt, when not empty, stops imports at that step.
func (e *env) service(crashAt string) *importer.Service {
	e.t.Helper()
	opts := importer.Options{
		Store: e.st, Bus: e.bus, Matcher: e.matcher, Library: e.lib,
		Now: func() time.Time { return e.now }, FS: fsx.New(nil, fsx.Options{Now: func() time.Time { return e.now }}),
	}
	if crashAt != "" {
		opts.Fault = func(step string) error {
			if step == crashAt {
				return errCrash
			}
			return nil
		}
	}
	svc, err := importer.New(opts)
	if err != nil {
		e.t.Fatal(err)
	}
	return svc
}

func (e *env) download(movieID int64, title, protocol string) db.Download {
	e.t.Helper()
	d, err := e.st.Write().InsertDownload(e.ctx, db.InsertDownloadParams{
		MovieID: movieID, ReleaseTitle: title, Protocol: protocol, Quality: "webdl-1080p", Revision: 1, Decision: "{}",
		GrabbedAt: store.Millis(e.now),
	})
	if err != nil {
		e.t.Fatal(store.MapError(err))
	}
	if _, err := e.st.Write().UpdateDownloadState(e.ctx, db.UpdateDownloadStateParams{ID: d.ID, Status: "importing", Progress: 1, StatusMessages: "[]"}); err != nil {
		e.t.Fatal(err)
	}
	return d
}

func (e *env) journal() []importer.JournalEntry {
	e.t.Helper()
	all, err := e.svc.ListJournal(e.ctx, true)
	if err != nil {
		e.t.Fatal(err)
	}
	return all
}

// TestRecoverAfterCrashBeforeCommit: the file was moved, the process died
// before the database commit. At restart the database is completed.
func TestRecoverAfterCrashBeforeCommit(t *testing.T) {
	e := newEnv(t)
	m := e.movie(550, "Fight Club", 1999)
	rel := "Fight.Club.1999.1080p.WEB-DL.DD5.1.H264-GRP"
	src := e.file(filepath.Join(e.dl, rel+".mkv"), movieSize)
	d := e.download(m.ID, rel, "usenet")

	_, err := e.service(importer.FaultBeforeCommit).Import(e.ctx, importer.Request{
		DownloadID: d.ID, MovieID: m.ID, LocalPath: src, Protocol: decision.Usenet,
	})
	if !errors.Is(err, errCrash) {
		t.Fatalf("import = %v, want the simulated crash", err)
	}
	movieDir := filepath.Join(e.root, "Fight Club (1999)")
	if exists(t, src) || len(e.dirNames(movieDir)) != 1 || e.currentFile(m.ID) != nil {
		t.Fatalf("crash state: source %v, folder %v, file row %v", exists(t, src), e.dirNames(movieDir), e.currentFile(m.ID))
	}
	// A partial copy left by a crash during a copy is cleaned up too.
	e.file(filepath.Join(movieDir, ".gonema-partial-123"), 10)

	sum, err := e.service("").Recover(e.ctx)
	if err != nil {
		t.Fatal(err)
	}
	if sum.Completed != 1 || sum.Failed != 0 || sum.Attention != 0 || sum.Cleaned != 1 {
		t.Errorf("summary = %+v", sum)
	}
	f := e.currentFile(m.ID)
	if f == nil || f.Size != movieSize || f.Quality != "webdl-1080p" || !strings.HasPrefix(f.RelativePath, "Fight Club (1999)") {
		t.Fatalf("file row = %+v", f)
	}
	h := e.history(m.ID)
	if len(h) == 0 || h[0].Event != "imported" || !strings.Contains(h[0].Data, `"recovered":true`) || h[0].DownloadID == nil {
		t.Errorf("history = %+v", h)
	}
	got, err := e.st.Read().GetDownload(e.ctx, d.ID)
	if err != nil || got.Status != "imported" || got.ImportedAt == nil {
		t.Errorf("download = %+v %v", got, err)
	}
	if j := e.journal(); len(j) != 1 || j[0].Status != importer.JournalDone {
		t.Errorf("journal = %+v", j)
	}
	if names := e.dirNames(movieDir); len(names) != 1 || strings.HasPrefix(names[0], ".") {
		t.Errorf("movie folder = %v", names)
	}
	// Idempotent: nothing pending any more.
	if sum, err := e.service("").Recover(e.ctx); err != nil || sum != (importer.RecoverySummary{}) {
		t.Errorf("second recovery = %+v %v", sum, err)
	}
}

// TestRecoverAfterCrashBeforeTransfer: the journal was written, nothing was
// moved. The entry is marked failed and the download keeps "importing" so the
// tracker imports it again.
func TestRecoverAfterCrashBeforeTransfer(t *testing.T) {
	e := newEnv(t)
	m := e.movie(550, "Fight Club", 1999)
	rel := "Fight.Club.1999.1080p.WEB-DL.DD5.1.H264-GRP"
	src := e.file(filepath.Join(e.dl, rel+".mkv"), movieSize)
	d := e.download(m.ID, rel, "usenet")
	if _, err := e.service(importer.FaultAfterIntent).Import(e.ctx, importer.Request{
		DownloadID: d.ID, MovieID: m.ID, LocalPath: src, Protocol: decision.Usenet,
	}); !errors.Is(err, errCrash) {
		t.Fatal(err)
	}
	sum, err := e.service("").Recover(e.ctx)
	if err != nil || sum.Failed != 1 || sum.Completed != 0 {
		t.Fatalf("summary = %+v %v", sum, err)
	}
	if !exists(t, src) || e.currentFile(m.ID) != nil {
		t.Error("recovery touched the source or recorded a file")
	}
	if got, _ := e.st.Read().GetDownload(e.ctx, d.ID); got.Status != "importing" {
		t.Errorf("download status = %s, want importing (retried by the tracker)", got.Status)
	}
	if j := e.journal(); j[0].Status != importer.JournalFailed || !strings.Contains(j[0].Message, "imported again") {
		t.Errorf("journal = %+v", j)
	}
	// The retry succeeds.
	res, err := e.service("").Import(e.ctx, importer.Request{DownloadID: d.ID, MovieID: m.ID, LocalPath: src, Protocol: decision.Usenet})
	if err != nil || !res.Imported {
		t.Fatalf("retry = %+v %v", res, err)
	}
}

// TestRecoverHardlinkBothExist: a seeding torrent was hard linked (source and
// destination both exist, as expected): the database is completed and the
// source is kept.
func TestRecoverHardlinkBothExist(t *testing.T) {
	e := newEnv(t)
	m := e.movie(438631, "Dune", 2021)
	rel := "Dune.2021.1080p.BluRay.x264-GRP"
	src := e.file(filepath.Join(e.dl, rel, rel+".mkv"), movieSize)
	if _, err := e.service(importer.FaultAfterTransfer).Import(e.ctx, importer.Request{
		MovieID: m.ID, LocalPath: filepath.Join(e.dl, rel), ReleaseTitle: rel, Protocol: decision.Torrent, ClientSeeding: true,
	}); !errors.Is(err, errCrash) {
		t.Fatal(err)
	}
	sum, err := e.service("").Recover(e.ctx)
	if err != nil || sum.Completed != 1 {
		t.Fatalf("summary = %+v %v", sum, err)
	}
	if !exists(t, src) || e.currentFile(m.ID) == nil {
		t.Error("seeding source removed or file not recorded")
	}
}

// TestRecoverStagedReplacement: an upgrade with the same file name crashed
// after the new file was staged next to the old one. Recovery finishes the
// replacement: old file to the recycle bin, new file in place.
func TestRecoverStagedReplacement(t *testing.T) {
	e := newEnv(t)
	if _, err := e.svc.SetNaming(e.ctx, importer.NamingSettings{FolderTemplate: library.DefaultFolderTemplate, FileTemplate: "{Movie Title}", RenameFiles: true}); err != nil {
		t.Fatal(err)
	}
	m := e.movie(949, "Heat", 1995)
	if res, err := e.svc.Import(e.ctx, importer.Request{MovieID: m.ID, LocalPath: e.file(filepath.Join(e.dl, "Heat.1995.1080p.BluRay.x264-GRP.mkv"), movieSize), Protocol: decision.Usenet}); err != nil || res.Path != "Heat.mkv" {
		t.Fatalf("first = %+v, %v", res, err)
	}
	_, err := e.service(importer.FaultAfterTransfer).Import(e.ctx, importer.Request{
		MovieID: m.ID, LocalPath: e.file(filepath.Join(e.dl, "Heat.1995.PROPER.1080p.BluRay.x264-GRP.mkv"), movieSize+7), Protocol: decision.Usenet,
	})
	if !errors.Is(err, errCrash) {
		t.Fatal(err)
	}
	dir := filepath.Join(e.root, "Heat (1995)")
	if names := e.dirNames(dir); len(names) != 2 {
		t.Fatalf("crash state folder = %v", names)
	}
	sum, err := e.service("").Recover(e.ctx)
	if err != nil || sum.Completed != 1 {
		t.Fatalf("summary = %+v %v", sum, err)
	}
	if names := e.dirNames(dir); !slices.Equal(names, []string{"Heat.mkv"}) {
		t.Errorf("folder = %v", names)
	}
	if fi, err := os.Stat(filepath.Join(dir, "Heat.mkv")); err != nil || fi.Size() != movieSize+7 {
		t.Errorf("new file not in place: %v", err)
	}
	if items, _ := e.svc.ListRecycle(e.ctx); len(items) != 1 {
		t.Errorf("old file not in the recycle bin: %+v", items)
	}
	if f := e.currentFile(m.ID); f == nil || f.Size != movieSize+7 || f.Revision != 2 {
		t.Errorf("file row = %+v", f)
	}
	if h := e.history(m.ID); h[0].Event != "upgraded" {
		t.Errorf("history = %s", h[0].Event)
	}
}

// TestRecoverAmbiguousNeedsAttention: the source and the destination are both
// gone. Nothing is deleted or guessed: the entry needs attention, can be
// listed and dismissed.
func TestRecoverAmbiguousNeedsAttention(t *testing.T) {
	e := newEnv(t)
	m := e.movie(550, "Fight Club", 1999)
	rel := "Fight.Club.1999.1080p.WEB-DL.DD5.1.H264-GRP"
	src := e.file(filepath.Join(e.dl, rel+".mkv"), movieSize)
	if _, err := e.service(importer.FaultBeforeCommit).Import(e.ctx, importer.Request{MovieID: m.ID, LocalPath: src, Protocol: decision.Usenet}); !errors.Is(err, errCrash) {
		t.Fatal(err)
	}
	dir := filepath.Join(e.root, "Fight Club (1999)")
	names := e.dirNames(dir)
	if len(names) != 1 {
		t.Fatalf("folder = %v", names)
	}
	// The user moved the file away before the restart.
	if err := os.Rename(filepath.Join(dir, names[0]), filepath.Join(t.TempDir(), "elsewhere.mkv")); err != nil {
		t.Fatal(err)
	}
	sum, err := e.service("").Recover(e.ctx)
	if err != nil || sum.Attention != 1 {
		t.Fatalf("summary = %+v %v", sum, err)
	}
	list, err := e.svc.ListJournal(e.ctx, false)
	if err != nil || len(list) != 1 || list[0].Status != importer.JournalAttention || !strings.Contains(list[0].Message, "Neither") {
		t.Fatalf("attention list = %+v %v", list, err)
	}
	if _, err := e.svc.DismissJournal(e.ctx, list[0].ID, false); !errors.Is(err, importer.ErrConfirmRequired) {
		t.Errorf("dismiss without confirm = %v", err)
	}
	if got, err := e.svc.DismissJournal(e.ctx, list[0].ID, true); err != nil || got.Status != importer.JournalDismissed {
		t.Errorf("dismiss = %+v %v", got, err)
	}
	if _, err := e.svc.DismissJournal(e.ctx, list[0].ID, true); !errors.Is(err, importer.ErrInvalid) {
		t.Errorf("second dismiss = %v", err)
	}
	if _, err := e.svc.DismissJournal(e.ctx, 999, true); !errors.Is(err, importer.ErrNotFound) {
		t.Errorf("unknown dismiss = %v", err)
	}
	if list, _ := e.svc.ListJournal(e.ctx, false); len(list) != 0 {
		t.Errorf("still listed: %+v", list)
	}
}

// TestRecoverUnexpectedFileNeedsAttention: a different file sits at the
// destination; it is left alone.
func TestRecoverUnexpectedFileNeedsAttention(t *testing.T) {
	e := newEnv(t)
	m := e.movie(550, "Fight Club", 1999)
	rel := "Fight.Club.1999.1080p.WEB-DL.DD5.1.H264-GRP"
	src := e.file(filepath.Join(e.dl, rel+".mkv"), movieSize)
	if _, err := e.service(importer.FaultBeforeCommit).Import(e.ctx, importer.Request{MovieID: m.ID, LocalPath: src, Protocol: decision.Usenet}); !errors.Is(err, errCrash) {
		t.Fatal(err)
	}
	dir := filepath.Join(e.root, "Fight Club (1999)")
	name := e.dirNames(dir)[0]
	if err := os.Truncate(filepath.Join(dir, name), 1234); err != nil {
		t.Fatal(err)
	}
	sum, err := e.service("").Recover(e.ctx)
	if err != nil || sum.Attention != 1 {
		t.Fatalf("summary = %+v %v", sum, err)
	}
	if !exists(t, filepath.Join(dir, name)) || e.currentFile(m.ID) != nil {
		t.Error("the unexpected file was touched or recorded")
	}
}

// TestImportWritesJournal: a normal import leaves a done journal entry
// (marked in the same transaction as the movie file row).
func TestImportWritesJournal(t *testing.T) {
	e := newEnv(t)
	m := e.movie(550, "Fight Club", 1999)
	src := e.file(filepath.Join(e.dl, "Fight.Club.1999.1080p.WEB-DL.DD5.1.H264-GRP.mkv"), movieSize)
	if res, err := e.svc.Import(e.ctx, importer.Request{MovieID: m.ID, LocalPath: src, Protocol: decision.Usenet}); err != nil || !res.Imported {
		t.Fatal(res, err)
	}
	j := e.journal()
	if len(j) != 1 || j[0].Status != importer.JournalDone || j[0].Mode != "move" || j[0].Source != src {
		t.Errorf("journal = %+v", j)
	}
}
