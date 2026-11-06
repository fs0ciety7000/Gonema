package importer_test

import (
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/fs0ciety7000/gonema/internal/events"
	"github.com/fs0ciety7000/gonema/internal/fsx"
	"github.com/fs0ciety7000/gonema/internal/importer"
	"github.com/fs0ciety7000/gonema/internal/parser"
	"github.com/fs0ciety7000/gonema/internal/store"
	"github.com/fs0ciety7000/gonema/internal/store/db"
)

// withFile records a file for a movie, as an import would have.
func (e *env) withFile(m db.Movie, rel, scene string) string {
	e.t.Helper()
	dir := filepath.Join(e.root, m.Path)
	p := e.file(filepath.Join(dir, rel), 1<<20)
	parsed := parser.Parse(scene)
	raw, err := json.Marshal(&parsed)
	if err != nil {
		e.t.Fatal(err)
	}
	if _, err := e.st.Write().UpsertMovieFile(e.ctx, db.UpsertMovieFileParams{
		MovieID: m.ID, RelativePath: rel, Size: 1 << 20, Quality: "bluray-1080p", Revision: 1, Languages: "[]",
		SceneName: scene, Parsed: string(raw), MediaInfo: "{}", ImportedAt: store.Millis(e.now),
	}); err != nil {
		e.t.Fatal(err)
	}
	return p
}

func (e *env) subscribe(types ...events.Type) *events.Subscription {
	sub := e.bus.Subscribe(64, func(ev *events.Event) bool { return slices.Contains(types, ev.Type) })
	e.t.Cleanup(sub.Close)
	return sub
}

func next(t *testing.T, sub *events.Subscription) events.Event {
	t.Helper()
	select {
	case ev := <-sub.C():
		return ev
	case <-time.After(2 * time.Second):
		t.Fatal("no event")
	}
	return events.Event{}
}

func historyKinds(rows []db.History) []string {
	out := make([]string, 0, len(rows))
	for _, h := range rows {
		out = append(out, h.Event)
	}
	return out
}

func TestRenamePreviewAndRename(t *testing.T) {
	e := newEnv(t)
	m := e.insertMovie(949, "Heat", 1995, "heat-old", e.profID)
	scene := "Heat.1995.1080p.BluRay.x264-GRP"
	video := e.withFile(m, "heat.old.mkv", scene)
	sub := e.file(filepath.Join(e.root, "heat-old", "heat.old.en.srt"), 10)
	art := e.file(filepath.Join(e.root, "heat-old", "extras", "poster.jpg"), 10)

	p, err := e.svc.PlanRename(e.ctx, m.ID)
	if err != nil {
		t.Fatal(err)
	}
	newDir := filepath.Join(e.root, "Heat (1995)")
	if p.ToPath != "Heat (1995)" || p.FromFolder != filepath.Join(e.root, "heat-old") || p.ToFolder != newDir ||
		p.FileFrom != "heat.old.mkv" || !strings.HasPrefix(p.FileTo, "Heat (1995)") || !strings.HasSuffix(p.FileTo, ".mkv") ||
		len(p.Problems) != 0 || p.NoChange || len(p.Ops) != 3 {
		t.Fatalf("plan = %+v", p)
	}
	newStem := strings.TrimSuffix(p.FileTo, ".mkv")
	wantOps := map[string]string{
		video: filepath.Join(newDir, p.FileTo),
		sub:   filepath.Join(newDir, newStem+".en.srt"),
		art:   filepath.Join(newDir, "extras", "poster.jpg"),
	}
	for _, op := range p.Ops {
		if wantOps[op.From] != op.To {
			t.Errorf("op %s -> %s, want -> %s", op.From, op.To, wantOps[op.From])
		}
	}
	if !exists(t, video) || exists(t, newDir) {
		t.Fatal("the preview touched the files")
	}

	evs := e.subscribe(events.MovieFileRenamed, events.MovieUpdated)
	res, err := e.svc.Rename(e.ctx, m.ID)
	if err != nil {
		t.Fatal(err)
	}
	if len(res.Done) != 3 {
		t.Errorf("done = %+v", res.Done)
	}
	for from, to := range wantOps {
		if exists(t, from) || !exists(t, to) {
			t.Errorf("%s not moved to %s", from, to)
		}
	}
	if exists(t, filepath.Join(e.root, "heat-old")) {
		t.Error("the emptied old folder is still there")
	}
	got, _ := e.st.Read().GetMovie(e.ctx, m.ID)
	if got.Path != "Heat (1995)" {
		t.Errorf("movie path = %q", got.Path)
	}
	if f := e.currentFile(m.ID); f == nil || f.RelativePath != p.FileTo {
		t.Errorf("file = %+v", f)
	}
	h := e.history(m.ID)
	if len(h) != 1 || h[0].Event != "file_renamed" || !strings.Contains(h[0].Data, "Renamed on request") || h[0].SourceTitle != scene {
		t.Fatalf("history = %+v", h)
	}
	ev := next(t, evs)
	data, _ := ev.Data.(map[string]any)
	if ev.Type != events.MovieFileRenamed || data["path"] != newDir || data["oldPath"] != filepath.Join(e.root, "heat-old") {
		t.Errorf("event = %+v", ev)
	}
	if ev := next(t, evs); ev.Type != events.MovieUpdated || ev.MovieID != m.ID {
		t.Errorf("event = %+v", ev)
	}

	// Already matching: nothing to do, nothing written.
	res, err = e.svc.Rename(e.ctx, m.ID)
	if err != nil || !res.NoChange || len(res.Done) != 0 || len(e.history(m.ID)) != 1 {
		t.Errorf("second rename = %+v, %v", res, err)
	}
}

func TestRenameRefusesTakenNames(t *testing.T) {
	e := newEnv(t)
	m := e.insertMovie(949, "Heat", 1995, "heat-old", e.profID)
	video := e.withFile(m, "heat.old.mkv", "Heat.1995.1080p.BluRay.x264-GRP")
	if err := os.MkdirAll(filepath.Join(e.root, "Heat (1995)"), 0o750); err != nil {
		t.Fatal(err)
	}
	p, err := e.svc.PlanRename(e.ctx, m.ID)
	if err != nil || len(p.Problems) != 1 || !strings.Contains(p.Problems[0], "already exists") {
		t.Fatalf("plan = %+v, %v", p, err)
	}
	if _, err := e.svc.Rename(e.ctx, m.ID); !errors.Is(err, importer.ErrConflict) {
		t.Fatalf("rename = %v", err)
	}
	if !exists(t, video) || len(e.history(m.ID)) != 0 {
		t.Error("a refused rename changed something")
	}

	// Same folder, file name taken by another file.
	m2 := e.insertMovie(550, "Fight Club", 1999, "Fight Club (1999)", e.profID)
	e.withFile(m2, "fc.mkv", "Fight.Club.1999.1080p.BluRay.x264-GRP")
	p2, err := e.svc.PlanRename(e.ctx, m2.ID)
	if err != nil {
		t.Fatal(err)
	}
	e.file(filepath.Join(e.root, "Fight Club (1999)", p2.FileTo), 5)
	if _, err := e.svc.Rename(e.ctx, m2.ID); !errors.Is(err, importer.ErrConflict) {
		t.Fatalf("rename onto an existing file = %v", err)
	}
	if !exists(t, filepath.Join(e.root, "Fight Club (1999)", "fc.mkv")) {
		t.Error("the file was moved")
	}
}

func (e *env) secondRoot() (dir string, id int64) {
	e.t.Helper()
	dir = filepath.Join(filepath.Dir(e.root), "archive")
	if err := os.MkdirAll(dir, 0o750); err != nil {
		e.t.Fatal(err)
	}
	rf, err := e.st.Write().CreateRootFolder(e.ctx, dir)
	if err != nil {
		e.t.Fatal(err)
	}
	return dir, rf.ID
}

func TestMoveToAnotherRootFolder(t *testing.T) {
	e := newEnv(t)
	archive, archiveID := e.secondRoot()
	m := e.movie(949, "Heat", 1995)
	video := e.withFile(m, "Heat.1995.mkv", "Heat.1995.1080p.BluRay.x264-GRP")
	nfo := e.file(filepath.Join(e.root, "Heat (1995)", "movie.nfo"), 3)

	p, err := e.svc.PlanMove(e.ctx, m.ID, archiveID, true)
	if err != nil || len(p.Ops) != 2 || p.ToFolder != filepath.Join(archive, "Heat (1995)") || len(p.Problems) != 0 {
		t.Fatalf("plan = %+v, %v", p, err)
	}
	if !exists(t, video) {
		t.Fatal("the preview moved files")
	}
	evs := e.subscribe(events.MovieFileRenamed)
	res, err := e.svc.Move(e.ctx, m.ID, archiveID, true)
	if err != nil {
		t.Fatal(err)
	}
	if len(res.Done) != 2 || exists(t, video) || exists(t, nfo) ||
		!exists(t, filepath.Join(archive, "Heat (1995)", "Heat.1995.mkv")) || !exists(t, filepath.Join(archive, "Heat (1995)", "movie.nfo")) {
		t.Fatalf("files after move: %+v", res)
	}
	if exists(t, filepath.Join(e.root, "Heat (1995)")) {
		t.Error("old folder left behind")
	}
	got, _ := e.st.Read().GetMovie(e.ctx, m.ID)
	if got.RootFolderID != archiveID || got.Path != "Heat (1995)" {
		t.Errorf("movie = root %d path %q", got.RootFolderID, got.Path)
	}
	if f := e.currentFile(m.ID); f == nil || f.RelativePath != "Heat.1995.mkv" {
		t.Errorf("a move renamed the file: %+v", f)
	}
	ev := next(t, evs)
	if data, _ := ev.Data.(map[string]any); data["oldPath"] != filepath.Join(e.root, "Heat (1995)") || data["kind"] != importer.KindMove {
		t.Errorf("event = %+v", ev)
	}
	if h := e.history(m.ID); len(h) != 1 || h[0].Event != "file_renamed" || !strings.Contains(h[0].Data, `"kind":"move"`) {
		t.Errorf("history = %+v", h)
	}

	// Back, but the destination folder exists: refused, nothing moves.
	if err := os.MkdirAll(filepath.Join(e.root, "Heat (1995)"), 0o750); err != nil {
		t.Fatal(err)
	}
	if _, err := e.svc.Move(e.ctx, m.ID, e.rootID, true); !errors.Is(err, importer.ErrConflict) {
		t.Fatalf("move onto an existing folder = %v", err)
	}
	if !exists(t, filepath.Join(archive, "Heat (1995)", "Heat.1995.mkv")) {
		t.Error("refused move touched the files")
	}

	// Without the files: only the location changes.
	res, err = e.svc.Move(e.ctx, m.ID, e.rootID, false)
	if err != nil || len(res.Done) != 0 {
		t.Fatalf("location-only move = %+v, %v", res, err)
	}
	if got, _ := e.st.Read().GetMovie(e.ctx, m.ID); got.RootFolderID != e.rootID {
		t.Errorf("root = %d", got.RootFolderID)
	}
	if !exists(t, filepath.Join(archive, "Heat (1995)", "Heat.1995.mkv")) {
		t.Error("files moved without moveFiles")
	}
	if _, err := e.svc.Move(e.ctx, m.ID, 999, true); !errors.Is(err, importer.ErrInvalid) {
		t.Errorf("unknown root = %v", err)
	}
}

// crossFS makes renames between the two root folders fail as across file
// systems: Move must copy, verify, then remove the source.
type crossFS struct {
	fsx.OS
	roots []string
}

func (c crossFS) dev(p string) int {
	for i, r := range c.roots {
		if fsx.Within(r, p) {
			return i
		}
	}
	return -1
}

func (c crossFS) Rename(oldpath, newpath string) error {
	if c.dev(oldpath) != c.dev(newpath) {
		return &os.LinkError{Op: "rename", Old: oldpath, New: newpath, Err: fsx.ErrCrossDevice}
	}
	return c.OS.Rename(oldpath, newpath)
}

func TestMoveAcrossFileSystems(t *testing.T) {
	e := newEnv(t)
	archive, archiveID := e.secondRoot()
	svc, err := importer.New(importer.Options{Store: e.st, Bus: e.bus, Now: func() time.Time { return e.now },
		FS: fsx.New(crossFS{roots: []string{e.root, archive}}, fsx.Options{Now: func() time.Time { return e.now }})})
	if err != nil {
		t.Fatal(err)
	}
	m := e.movie(949, "Heat", 1995)
	video := e.withFile(m, "Heat.1995.mkv", "Heat.1995.1080p.BluRay.x264-GRP")
	res, err := svc.Move(e.ctx, m.ID, archiveID, true)
	if err != nil {
		t.Fatal(err)
	}
	dst := filepath.Join(archive, "Heat (1995)", "Heat.1995.mkv")
	fi, err := os.Stat(dst)
	if err != nil || fi.Size() != 1<<20 || exists(t, video) || len(res.Done) != 1 {
		t.Fatalf("cross-device move: %+v, %v (source exists %v)", res, err, exists(t, video))
	}
}

func TestDeleteFileGoesToRecycleBin(t *testing.T) {
	e := newEnv(t)
	m := e.movie(949, "Heat", 1995)
	video := e.withFile(m, "Heat.1995.mkv", "Heat.1995.1080p.BluRay.x264-GRP")
	sub := e.file(filepath.Join(e.root, "Heat (1995)", "Heat.1995.fr.srt"), 4)
	other := e.file(filepath.Join(e.root, "Heat (1995)", "movie.nfo"), 4)

	_, err := e.svc.DeleteFile(e.ctx, m.ID, false)
	if !errors.Is(err, importer.ErrConfirmRequired) || !strings.Contains(err.Error(), "confirm=true") || !strings.Contains(err.Error(), "recycle bin") {
		t.Fatalf("without confirm = %v", err)
	}
	if !exists(t, video) || e.currentFile(m.ID) == nil {
		t.Fatal("deleted without confirmation")
	}
	evs := e.subscribe(events.MovieFileDeleted)
	d, err := e.svc.DeleteFile(e.ctx, m.ID, true)
	if err != nil {
		t.Fatal(err)
	}
	if exists(t, video) || exists(t, sub) || !exists(t, other) || len(d.Recycled) != 2 || e.currentFile(m.ID) != nil {
		t.Fatalf("deletion = %+v", d)
	}
	bin := filepath.Join(e.root, importer.RecycleDirName)
	if d.Bin != bin {
		t.Errorf("bin = %s", d.Bin)
	}
	for _, name := range d.Recycled {
		if !exists(t, filepath.Join(bin, name)) {
			t.Errorf("%s is not in the bin", name)
		}
	}
	if h := e.history(m.ID); len(h) != 1 || h[0].Event != "file_deleted" || !strings.Contains(h[0].Data, "Deleted on request") {
		t.Errorf("history = %+v", historyKinds(h))
	}
	if ev := next(t, evs); ev.MovieID != m.ID {
		t.Errorf("event = %+v", ev)
	}
	// Restorable: the bin lists both with their origin.
	items, err := e.svc.ListRecycle(e.ctx)
	if err != nil || len(items) != 2 || items[0].Manifest == nil || items[0].Manifest.MovieID != m.ID {
		t.Fatalf("bin = %+v, %v", items, err)
	}
	if _, err := e.svc.DeleteFile(e.ctx, m.ID, true); !errors.Is(err, importer.ErrNotFound) {
		t.Errorf("no file = %v", err)
	}
}

func TestRecycleFolder(t *testing.T) {
	e := newEnv(t)
	m := e.movie(949, "Heat", 1995)
	dir := filepath.Join(e.root, "Heat (1995)")
	video := e.withFile(m, "Heat.1995.mkv", "Heat.1995.1080p.BluRay.x264-GRP")
	extra := e.file(filepath.Join(dir, "extras", "making-of.mkv"), 4)
	if _, err := e.svc.RecycleFolder(e.ctx, m.ID, false); !errors.Is(err, importer.ErrConfirmRequired) {
		t.Fatalf("without confirm = %v", err)
	}
	if !exists(t, video) {
		t.Fatal("recycled without confirmation")
	}
	d, err := e.svc.RecycleFolder(e.ctx, m.ID, true)
	if err != nil {
		t.Fatal(err)
	}
	if len(d.Recycled) != 2 || exists(t, video) || exists(t, extra) || exists(t, dir) || e.currentFile(m.ID) != nil {
		t.Fatalf("recycle folder = %+v (folder exists %v)", d, exists(t, dir))
	}
	if n := len(e.dirNames(filepath.Join(e.root, importer.RecycleDirName))); n != 4 { // 2 files + 2 manifests
		t.Errorf("bin entries = %d", n)
	}
	if h := e.history(m.ID); len(h) != 1 || h[0].Event != "file_deleted" {
		t.Errorf("history = %v", historyKinds(h))
	}
}
