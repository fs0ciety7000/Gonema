package importer

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"log/slog"
	"path/filepath"
	"slices"
	"strings"

	"github.com/fs0ciety7000/gonema/internal/events"
	"github.com/fs0ciety7000/gonema/internal/fsx"
	"github.com/fs0ciety7000/gonema/internal/library"
	"github.com/fs0ciety7000/gonema/internal/naming"
	"github.com/fs0ciety7000/gonema/internal/parser"
	"github.com/fs0ciety7000/gonema/internal/quality"
	"github.com/fs0ciety7000/gonema/internal/store"
	"github.com/fs0ciety7000/gonema/internal/store/db"
)

// File operations on library movies, requested by the user: rename to the
// naming templates, move to another root folder, delete the file, recycle the
// folder. Files are moved with fsx.Move (a rename on one file system; across
// file systems the source is removed only after a verified copy) and deleted
// files go to the recycle bin: nothing is ever removed for good here.

// ErrConflict is returned when the destination of a rename or move is taken.
var ErrConflict = errors.New("importer: destination taken")

// historyFileRenamed is the history event of renames and moves.
const historyFileRenamed = "file_renamed"

// Limits of the movie folder walk.
const (
	maxFolderFiles = 2000
	maxFolderDepth = 6
)

// Relocation kinds.
const (
	KindRename = "rename"
	KindMove   = "move"
)

// FileOp is one file to move, with absolute paths.
type FileOp struct {
	From string `json:"from"`
	To   string `json:"to"`
}

// Plan is a rename or move of a movie: where it is, where it goes and every
// file operation. Problems lists why it cannot run; an empty Ops with no
// problem and NoChange means there is nothing to do.
type Plan struct {
	Kind             string   `json:"kind" enum:"rename,move"`
	MovieID          int64    `json:"movieId"`
	FromRootFolderID int64    `json:"fromRootFolderId"`
	ToRootFolderID   int64    `json:"toRootFolderId"`
	FromFolder       string   `json:"fromFolder" doc:"Absolute movie folder now"`
	ToFolder         string   `json:"toFolder" doc:"Absolute movie folder after the operation"`
	FromPath         string   `json:"fromPath" doc:"Movie folder relative to its root folder, now"`
	ToPath           string   `json:"toPath"`
	FileFrom         string   `json:"fileFrom,omitempty" doc:"Current file, relative to the movie folder"`
	FileTo           string   `json:"fileTo,omitempty" doc:"New name of the current file"`
	MoveFiles        bool     `json:"moveFiles"`
	Ops              []FileOp `json:"ops"`
	Problems         []string `json:"problems" doc:"Why the operation is refused; empty when it can run"`
	Notes            []string `json:"notes"`
	NoChange         bool     `json:"noChange" doc:"Names and location already match: nothing to do"`
}

// Relocation is the outcome of a rename or move.
type Relocation struct {
	Plan
	Done []FileOp `json:"done" doc:"File operations performed"`
}

// walkFolder lists the regular files under dir (relative paths, sorted) and
// the symbolic links and other entries it skipped. A missing folder is empty.
func (s *Service) walkFolder(dir, skip string) (files, skipped []string, err error) {
	var walk func(rel string, depth int) error
	walk = func(rel string, depth int) error {
		abs := filepath.Join(dir, rel)
		entries, err := s.fs.ReadDir(abs)
		if err != nil {
			return err
		}
		for _, e := range entries {
			r := filepath.Join(rel, e.Name())
			p := filepath.Join(dir, r)
			switch {
			case e.Type().IsRegular():
				if len(files) >= maxFolderFiles {
					return fmt.Errorf("more than %d files in %s", maxFolderFiles, dir)
				}
				files = append(files, r)
			case e.IsDir() && p != skip && depth < maxFolderDepth:
				if err := walk(r, depth+1); err != nil {
					return err
				}
			default:
				skipped = append(skipped, r)
			}
		}
		return nil
	}
	if _, err := s.fs.Lstat(dir); errors.Is(err, fs.ErrNotExist) {
		return nil, nil, nil
	} else if err != nil {
		return nil, nil, err
	}
	if err := walk("", 0); err != nil {
		return nil, nil, err
	}
	slices.Sort(files)
	return files, skipped, nil
}

// targetFolder returns the folder name the template gives the movie in root,
// with the collision suffix when another movie uses it.
func (s *Service) targetFolder(ctx context.Context, t *target, rootID int64) (string, error) {
	n, err := s.Naming(ctx)
	if err != nil {
		return "", err
	}
	v := library.FolderVars{Title: t.movie.Title, OriginalTitle: t.movie.OriginalTitle, Year: int(t.movie.Year), TmdbID: t.movie.TmdbID}
	if t.movie.ImdbID != nil {
		v.ImdbID = *t.movie.ImdbID
	}
	name := library.FolderName(n.FolderTemplate, v)
	other, err := s.st.Read().GetMovieAtLocation(ctx, db.GetMovieAtLocationParams{RootFolderID: rootID, Path: name})
	switch err = store.MapError(err); {
	case err == nil && other.ID != t.movie.ID:
		return library.CollisionName(name, t.movie.TmdbID), nil
	case err != nil && !errors.Is(err, store.ErrNotFound):
		return "", fmt.Errorf("importer: folder in use: %w", err)
	}
	return name, nil
}

// newFileName renders the file template for the current file.
func (s *Service) newFileName(ctx context.Context, t *target) string {
	var parsed parser.ParsedRelease
	_ = json.Unmarshal([]byte(t.current.Parsed), &parsed) // a broken record renders without release details
	q := qualityID(t.current.Quality)
	rel := quality.Release{Title: t.current.SceneName, Parsed: &parsed, Size: t.current.Size}
	score := quality.Score(&t.profile, t.rules, &rel)
	v := varsOf(t, &parsed, q, score)
	tmpl, _ := s.fileTemplate(ctx)
	name := tmpl.FileName(&v, filepath.Ext(t.current.RelativePath))
	if naming.CleanExt(filepath.Ext(t.current.RelativePath)) == "" {
		name += filepath.Ext(t.current.RelativePath) // keep an unusual extension as is
	}
	return name
}

// companionName returns the new name of a file that shares the stem of the
// current file (subtitles: "<stem>.en.srt"), or "" when it is not one.
func companionName(rel, oldRel, newRel string) string {
	if filepath.Dir(rel) != filepath.Dir(oldRel) || !subtitleExts[strings.ToLower(filepath.Ext(rel))] {
		return ""
	}
	oldStem := strings.TrimSuffix(filepath.Base(oldRel), filepath.Ext(oldRel))
	base := filepath.Base(rel)
	if !strings.HasPrefix(base, oldStem+".") {
		return ""
	}
	newStem := strings.TrimSuffix(filepath.Base(newRel), filepath.Ext(newRel))
	return filepath.Join(filepath.Dir(newRel), newStem+base[len(oldStem):])
}

// plan computes a rename (toRoot == nil) or a move.
func (s *Service) plan(ctx context.Context, kind string, movieID, toRootID int64, moveFiles bool) (*Plan, *target, error) {
	t, err := s.loadTarget(ctx, movieID)
	if err != nil {
		return nil, nil, err
	}
	p := &Plan{
		Kind: kind, MovieID: movieID, FromRootFolderID: t.root.ID, ToRootFolderID: t.root.ID,
		FromPath: t.movie.Path, ToPath: t.movie.Path, MoveFiles: moveFiles || kind == KindRename,
		Ops: []FileOp{}, Problems: []string{}, Notes: []string{},
	}
	toRoot := t.root
	if kind == KindMove {
		if toRoot, err = s.st.Read().GetRootFolder(ctx, toRootID); err != nil {
			if errors.Is(store.MapError(err), store.ErrNotFound) {
				return nil, nil, userErr(ErrInvalid, nil, "root folder %d does not exist", toRootID)
			}
			return nil, nil, store.MapError(fmt.Errorf("importer: root folder %d: %w", toRootID, err))
		}
		p.ToRootFolderID = toRoot.ID
	}
	if p.FromFolder, err = s.fs.SafeJoin(t.root.Path, t.movie.Path); err != nil {
		return nil, nil, fsErr(err, "the folder of %s is not usable inside the root folder %s", t.movie.Title, t.root.Path)
	}
	if p.MoveFiles {
		if p.ToPath, err = s.targetFolder(ctx, t, toRoot.ID); err != nil {
			return nil, nil, err
		}
	}
	if p.ToFolder, err = s.fs.SafeJoin(toRoot.Path, p.ToPath); err != nil {
		return nil, nil, fsErr(err, "the folder %s is not usable inside the root folder %s", p.ToPath, toRoot.Path)
	}
	if t.current != nil {
		p.FileFrom, p.FileTo = t.current.RelativePath, t.current.RelativePath
		if kind == KindRename {
			p.FileTo = filepath.Join(filepath.Dir(t.current.RelativePath), s.newFileName(ctx, t))
		}
	}
	folderChanges := p.FromFolder != p.ToFolder
	p.NoChange = !folderChanges && p.FileFrom == p.FileTo && p.FromRootFolderID == p.ToRootFolderID
	if p.NoChange {
		p.Notes = append(p.Notes, "the names and the location already match the settings")
		return p, t, nil
	}
	if other, err := s.st.Read().GetMovieAtLocation(ctx, db.GetMovieAtLocationParams{RootFolderID: toRoot.ID, Path: p.ToPath}); err == nil && other.ID != movieID {
		p.Problems = append(p.Problems, fmt.Sprintf("%s (%d) already uses the folder %s", other.Title, other.Year, p.ToFolder))
	} else if err != nil && !errors.Is(store.MapError(err), store.ErrNotFound) {
		return nil, nil, store.MapError(fmt.Errorf("importer: folder in use: %w", err))
	}
	if !p.MoveFiles {
		p.Notes = append(p.Notes, "files are not moved: Gonema now looks for the movie in "+p.ToFolder)
		if _, err := s.fs.Lstat(p.ToFolder); err != nil {
			p.Notes = append(p.Notes, p.ToFolder+" does not exist yet: the movie shows as missing until its files are there")
		}
		return p, t, nil
	}
	if folderChanges && (fsx.Within(p.FromFolder, p.ToFolder) || fsx.Within(p.ToFolder, p.FromFolder)) {
		p.Problems = append(p.Problems, fmt.Sprintf("%s and %s are inside one another", p.FromFolder, p.ToFolder))
		return p, t, nil
	}
	if folderChanges {
		if _, err := s.fs.Lstat(p.ToFolder); err == nil {
			p.Problems = append(p.Problems, p.ToFolder+" already exists: move or rename it first")
		} else if !errors.Is(err, fs.ErrNotExist) {
			return nil, nil, fsErr(err, "cannot check %s", p.ToFolder)
		}
	}
	set, err := s.Settings(ctx)
	if err != nil {
		return nil, nil, err
	}
	files, skipped, err := s.walkFolder(p.FromFolder, binDir(&set, t.root.Path))
	if err != nil {
		return nil, nil, fsErr(err, "cannot list the files of %s", p.FromFolder)
	}
	for _, sk := range skipped {
		p.Notes = append(p.Notes, sk+" is not a regular file: left in place")
	}
	if len(files) == 0 {
		p.Notes = append(p.Notes, p.FromFolder+" has no files on disk: only the location in Gonema changes")
	}
	if t.current != nil && !slices.Contains(files, t.current.RelativePath) {
		p.Notes = append(p.Notes, "the current file "+t.current.RelativePath+" is missing from disk")
	}
	targets := map[string]bool{}
	for _, rel := range files {
		to := rel
		switch {
		case rel == p.FileFrom:
			to = p.FileTo
		case p.FileFrom != "" && p.FileFrom != p.FileTo:
			if c := companionName(rel, p.FileFrom, p.FileTo); c != "" {
				to = c
			}
		}
		if !folderChanges && to == rel {
			continue // stays where it is
		}
		op := FileOp{From: filepath.Join(p.FromFolder, rel), To: filepath.Join(p.ToFolder, to)}
		if targets[op.To] {
			p.Problems = append(p.Problems, fmt.Sprintf("two files would be named %s", op.To))
			continue
		}
		targets[op.To] = true
		if !folderChanges {
			if _, err := s.fs.Lstat(op.To); err == nil && !sameFile(op.From, op.To) {
				p.Problems = append(p.Problems, op.To+" already exists")
				continue
			}
		}
		p.Ops = append(p.Ops, op)
	}
	return p, t, nil
}

// PlanRename previews the rename of a movie to the naming templates: its folder
// (in the same root folder), its file and the file's subtitles.
func (s *Service) PlanRename(ctx context.Context, movieID int64) (Plan, error) {
	p, _, err := s.plan(ctx, KindRename, movieID, 0, true)
	if err != nil {
		return Plan{}, err
	}
	return *p, nil
}

// PlanMove previews the move of a movie to another root folder. With
// moveFiles, every file of its folder moves to a folder named with the
// template; without, only the location Gonema uses changes.
func (s *Service) PlanMove(ctx context.Context, movieID, rootFolderID int64, moveFiles bool) (Plan, error) {
	p, _, err := s.plan(ctx, KindMove, movieID, rootFolderID, moveFiles)
	if err != nil {
		return Plan{}, err
	}
	return *p, nil
}

// Rename renames the folder and files of a movie to the naming templates.
func (s *Service) Rename(ctx context.Context, movieID int64) (Relocation, error) {
	return s.relocate(ctx, KindRename, movieID, 0, true)
}

// Move moves a movie to another root folder (see PlanMove). The destination
// folder must not exist.
func (s *Service) Move(ctx context.Context, movieID, rootFolderID int64, moveFiles bool) (Relocation, error) {
	return s.relocate(ctx, KindMove, movieID, rootFolderID, moveFiles)
}

func (s *Service) relocate(ctx context.Context, kind string, movieID, rootFolderID int64, moveFiles bool) (Relocation, error) {
	if movieID <= 0 {
		return Relocation{}, userErr(ErrInvalid, nil, "the movie is required")
	}
	unlock := s.lockMovie(movieID)
	defer unlock()
	p, t, err := s.plan(ctx, kind, movieID, rootFolderID, moveFiles)
	if err != nil {
		return Relocation{}, err
	}
	res := Relocation{Plan: *p, Done: []FileOp{}}
	if len(p.Problems) > 0 {
		return res, userErr(ErrConflict, nil, "%s", strings.Join(p.Problems, "; "))
	}
	if p.NoChange {
		return res, nil
	}
	undoCtx := context.WithoutCancel(ctx)
	undo := func() {
		for i := len(res.Done) - 1; i >= 0; i-- {
			op := res.Done[i]
			if _, err := s.fs.Move(undoCtx, op.To, op.From); err != nil {
				s.log.Error("rollback: a file stays at its new place", slog.String("path", op.To), slog.Any("error", err))
			}
		}
		s.tidy(p.ToFolder)
	}
	for _, op := range p.Ops {
		if err := s.fs.MkdirAll(filepath.Dir(op.To)); err != nil {
			undo()
			return res, fsErr(err, "cannot create the folder %s", filepath.Dir(op.To))
		}
		out, err := s.fs.Move(ctx, op.From, op.To)
		if err != nil {
			undo()
			return res, fsErr(err, "cannot move %s to %s; the files already moved were put back", op.From, op.To)
		}
		if out.SourceKept {
			res.Notes = append(res.Notes, filepath.Base(op.From)+": "+out.Note)
		}
		res.Done = append(res.Done, op)
	}
	if p.FromFolder != p.ToFolder {
		s.tidy(p.FromFolder)
	}

	reason := s.relocationReason(p, t)
	files := make([]map[string]string, 0, len(res.Done))
	for _, op := range res.Done {
		files = append(files, map[string]string{"from": op.From, "to": op.To})
	}
	data := map[string]any{
		"reason": reason, "kind": kind, "fromFolder": p.FromFolder, "toFolder": p.ToFolder, "files": files,
		"fromRootFolderId": p.FromRootFolderID, "toRootFolderId": p.ToRootFolderID, "moveFiles": p.MoveFiles,
	}
	if len(res.Notes) > 0 {
		data["notes"] = res.Notes
	}
	var moved db.Movie
	err = s.st.WithTx(ctx, func(q *db.Queries) error {
		var err error
		if moved, err = q.SetMovieLocation(ctx, db.SetMovieLocationParams{ID: movieID, RootFolderID: p.ToRootFolderID, Path: p.ToPath}); err != nil {
			if errors.Is(store.MapError(err), store.ErrConflict) {
				return userErr(ErrConflict, err, "another movie already uses the folder %s", p.ToFolder)
			}
			return fmt.Errorf("update the movie location: %w", err)
		}
		if t.current != nil && p.FileTo != p.FileFrom {
			if _, err := q.SetMovieFileRelativePath(ctx, db.SetMovieFileRelativePathParams{MovieID: movieID, RelativePath: p.FileTo}); err != nil {
				return fmt.Errorf("update the file name: %w", err)
			}
		}
		h := &historyRow{movieID: movieID, event: historyFileRenamed, title: t.movie.Title, data: data}
		if t.current != nil {
			h.title, h.quality = t.current.SceneName, t.current.Quality
		}
		return s.appendHistory(ctx, q, h)
	})
	if err != nil {
		undo()
		var ue *Error
		if errors.As(err, &ue) {
			return res, ue
		}
		return res, fmt.Errorf("importer: record the %s of %s: %w", kind, t.movie.Title, err)
	}
	s.log.Info("movie files relocated", slog.Int64("movie_id", movieID), slog.String("kind", kind),
		slog.Int("files", len(res.Done)), slog.String("from", p.FromFolder), slog.String("to", p.ToFolder))
	s.publish(events.Event{Type: events.MovieFileRenamed, MovieID: movieID, Data: map[string]any{
		"path": p.ToFolder, "oldPath": p.FromFolder, "kind": kind, "reason": reason, "files": len(res.Done),
	}})
	if p.FromRootFolderID != p.ToRootFolderID || p.FromPath != p.ToPath {
		s.publish(events.Event{Type: events.MovieUpdated, MovieID: movieID, Data: library.MovieEvent{
			ID: moved.ID, TmdbID: moved.TmdbID, Title: moved.Title, Year: moved.Year, Status: moved.Status,
			Changes: []string{"rootFolderId", "path"}, Path: moved.Path,
		}})
	}
	return res, nil
}

func (s *Service) relocationReason(p *Plan, t *target) string {
	switch {
	case p.Kind == KindMove && !p.MoveFiles:
		return fmt.Sprintf("Location changed on request to %s; the files were not moved", p.ToFolder)
	case p.Kind == KindMove:
		return fmt.Sprintf("Moved on request from %s to %s (%d files)", p.FromFolder, p.ToFolder, len(p.Ops))
	case t.current != nil && p.FileFrom != p.FileTo && p.FromFolder != p.ToFolder:
		return fmt.Sprintf("Renamed on request to match the naming settings: folder %s, file %s", p.ToPath, filepath.Base(p.FileTo))
	case t.current != nil && p.FileFrom != p.FileTo:
		return fmt.Sprintf("Renamed on request to match the naming settings: %s", filepath.Base(p.FileTo))
	}
	return fmt.Sprintf("Folder renamed on request to match the naming settings: %s", p.ToPath)
}

// tidy removes the empty folders left under dir (dir included), deepest first.
// Folders with anything inside are kept.
func (s *Service) tidy(dir string) {
	var dirs []string
	var walk func(d string, depth int)
	walk = func(d string, depth int) {
		entries, err := s.fs.ReadDir(d)
		if err != nil {
			return
		}
		for _, e := range entries {
			if e.IsDir() && depth < maxFolderDepth {
				walk(filepath.Join(d, e.Name()), depth+1)
			}
		}
		dirs = append(dirs, d)
	}
	if fi, err := s.fs.Lstat(dir); err != nil || !fi.IsDir() {
		return
	}
	walk(dir, 0)
	for _, d := range dirs { // children were appended before their parent
		if err := s.fs.RemoveEmptyDir(d); err != nil && !errors.Is(err, fsx.ErrNotEmpty) && !errors.Is(err, fs.ErrNotExist) {
			s.log.Warn("empty folder kept", slog.String("path", d), slog.Any("error", err))
		}
	}
}

// ----------------------------------------------------------------- deletion

// Deletion is the outcome of a file deletion: what went to the recycle bin.
type Deletion struct {
	MovieID  int64    `json:"movieId"`
	Bin      string   `json:"bin" doc:"Recycle bin the files went to"`
	Recycled []string `json:"recycled" doc:"Names of the items in the recycle bin (restore them from there)"`
	Missing  []string `json:"missing" doc:"Files Gonema knew of that were already gone from disk"`
	Notes    []string `json:"notes"`
}

func confirmDelete(confirm bool, what string) error {
	if confirm {
		return nil
	}
	return userErr(ErrConfirmRequired, nil, "%s goes to the recycle bin (nothing is deleted for good, and it can be restored from there); "+
		"to go ahead, send the request again with confirm=true", what)
}

// DeleteFile moves the current file of a movie (and its subtitles) to the
// recycle bin and forgets it: the movie is missing again. It requires
// confirm.
func (s *Service) DeleteFile(ctx context.Context, movieID int64, confirm bool) (Deletion, error) {
	if err := confirmDelete(confirm, "The file of this movie"); err != nil {
		return Deletion{}, err
	}
	unlock := s.lockMovie(movieID)
	defer unlock()
	t, err := s.loadTarget(ctx, movieID)
	if err != nil {
		return Deletion{}, err
	}
	if t.current == nil {
		return Deletion{}, userErr(ErrNotFound, nil, "%s has no file to delete", t.movie.Title)
	}
	set, err := s.Settings(ctx)
	if err != nil {
		return Deletion{}, err
	}
	dir, err := s.fs.SafeJoin(t.root.Path, t.movie.Path)
	if err != nil {
		return Deletion{}, fsErr(err, "the folder of %s is not usable inside the root folder %s", t.movie.Title, t.root.Path)
	}
	files := []string{t.current.RelativePath}
	if entries, err := s.fs.ReadDir(filepath.Join(dir, filepath.Dir(t.current.RelativePath))); err == nil {
		for _, e := range entries {
			rel := filepath.Join(filepath.Dir(t.current.RelativePath), e.Name())
			if e.Type().IsRegular() && rel != t.current.RelativePath && companionName(rel, t.current.RelativePath, t.current.RelativePath) != "" {
				files = append(files, rel)
			}
		}
	}
	reason := fmt.Sprintf("Deleted on request: %s (%s) moved to the recycle bin", filepath.Base(t.current.RelativePath), qualityID(t.current.Quality).Name())
	del, items, err := s.recycleFiles(ctx, t, &set, dir, files, reason)
	if err != nil {
		return del, err
	}
	score := t.current.CustomScore
	err = s.st.WithTx(ctx, func(q *db.Queries) error {
		if _, err := q.DeleteMovieFile(ctx, movieID); err != nil {
			return fmt.Errorf("forget the file: %w", err)
		}
		return s.appendHistory(ctx, q, &historyRow{
			movieID: movieID, event: historyFileDeleted, title: t.current.SceneName, quality: t.current.Quality, score: &score,
			data: map[string]any{
				"reason": reason, "path": t.current.RelativePath, "size": t.current.Size, "bin": del.Bin,
				"recycled": del.Recycled, "missing": del.Missing,
			},
		})
	})
	if err != nil {
		s.restore(ctx, del.Bin, items)
		return Deletion{}, fmt.Errorf("importer: record the deletion of the file of %s: %w", t.movie.Title, err)
	}
	s.log.Info("movie file recycled on request", slog.Int64("movie_id", movieID), slog.String("bin", del.Bin), slog.Int("files", len(del.Recycled)))
	s.publish(events.Event{Type: events.MovieFileDeleted, MovieID: movieID, Data: map[string]any{"path": dir, "reason": reason}})
	s.publish(events.Event{Type: EventRecycleChanged, Data: map[string]any{"recycled": len(del.Recycled)}})
	return del, nil
}

// RecycleFolder moves every file of a movie folder to the recycle bin and
// forgets the movie's file, before the movie is removed from the library. The
// emptied folders are removed. It requires confirm.
func (s *Service) RecycleFolder(ctx context.Context, movieID int64, confirm bool) (Deletion, error) {
	if err := confirmDelete(confirm, "Every file of the movie folder"); err != nil {
		return Deletion{}, err
	}
	unlock := s.lockMovie(movieID)
	defer unlock()
	t, err := s.loadTarget(ctx, movieID)
	if err != nil {
		return Deletion{}, err
	}
	set, err := s.Settings(ctx)
	if err != nil {
		return Deletion{}, err
	}
	dir, err := s.fs.SafeJoin(t.root.Path, t.movie.Path)
	if err != nil {
		return Deletion{}, fsErr(err, "the folder of %s is not usable inside the root folder %s", t.movie.Title, t.root.Path)
	}
	bin := binDir(&set, t.root.Path)
	if fsx.Within(dir, bin) {
		return Deletion{}, userErr(ErrInvalid, nil, "the recycle bin %s is inside the movie folder %s: change it in Settings > Import first", bin, dir)
	}
	files, skipped, err := s.walkFolder(dir, bin)
	if err != nil {
		return Deletion{}, fsErr(err, "cannot list the files of %s", dir)
	}
	reason := fmt.Sprintf("%s removed from the library with its files, on request: the folder went to the recycle bin", t.movie.Title)
	del, items, err := s.recycleFiles(ctx, t, &set, dir, files, reason)
	if err != nil {
		return del, err
	}
	for _, sk := range skipped {
		del.Notes = append(del.Notes, sk+" is not a regular file: left in place")
	}
	s.tidy(dir)
	h := &historyRow{movieID: movieID, event: historyFileDeleted, title: t.movie.Title, data: map[string]any{
		"reason": reason, "folder": dir, "bin": del.Bin, "recycled": del.Recycled, "notes": del.Notes,
	}}
	if t.current != nil {
		score := t.current.CustomScore
		h.title, h.quality, h.score = t.current.SceneName, t.current.Quality, &score
	}
	err = s.st.WithTx(ctx, func(q *db.Queries) error {
		if _, err := q.DeleteMovieFile(ctx, movieID); err != nil {
			return fmt.Errorf("forget the file: %w", err)
		}
		return s.appendHistory(ctx, q, h)
	})
	if err != nil {
		s.restore(ctx, del.Bin, items)
		return Deletion{}, fmt.Errorf("importer: record the deletion of the files of %s: %w", t.movie.Title, err)
	}
	s.log.Info("movie folder recycled on request", slog.Int64("movie_id", movieID), slog.String("bin", del.Bin), slog.Int("files", len(del.Recycled)))
	s.publish(events.Event{Type: events.MovieFileDeleted, MovieID: movieID, Data: map[string]any{"path": dir, "reason": reason}})
	if len(del.Recycled) > 0 {
		s.publish(events.Event{Type: EventRecycleChanged, Data: map[string]any{"recycled": len(del.Recycled)}})
	}
	return del, nil
}

// recycleFiles moves files (relative to dir) to the bin of the movie's root
// folder. On failure the files already recycled are restored.
func (s *Service) recycleFiles(ctx context.Context, t *target, set *Settings, dir string, files []string, reason string) (Deletion, []string, error) {
	del := Deletion{MovieID: t.movie.ID, Bin: binDir(set, t.root.Path), Recycled: []string{}, Missing: []string{}, Notes: []string{}}
	var items []string
	for _, rel := range files {
		p := filepath.Join(dir, rel)
		if _, err := s.fs.Lstat(p); errors.Is(err, fs.ErrNotExist) {
			del.Missing = append(del.Missing, rel)
			continue
		}
		it, err := s.fs.Recycle(ctx, p, del.Bin, fsx.Manifest{MovieID: t.movie.ID, Reason: reason})
		if err != nil {
			s.restore(ctx, del.Bin, items)
			return Deletion{}, nil, fsErr(err, "cannot move %s to the recycle bin %s; nothing was deleted", rel, del.Bin)
		}
		items = append(items, it.Name)
		del.Recycled = append(del.Recycled, it.Name)
	}
	return del, items, nil
}

// restore puts recycled items back (rollback).
func (s *Service) restore(ctx context.Context, bin string, items []string) {
	ctx = context.WithoutCancel(ctx)
	for i := len(items) - 1; i >= 0; i-- {
		if _, err := s.fs.RestoreRecycled(ctx, bin, items[i], nil); err != nil {
			s.log.Error("rollback: a file stays in the recycle bin", slog.String("item", items[i]), slog.Any("error", err))
		}
	}
}
