package importer

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"path/filepath"
	"regexp"
	"strings"
	"sync"

	"github.com/fs0ciety7000/gonema/internal/events"
	"github.com/fs0ciety7000/gonema/internal/library"
	"github.com/fs0ciety7000/gonema/internal/match"
	"github.com/fs0ciety7000/gonema/internal/parser"
	"github.com/fs0ciety7000/gonema/internal/quality"
	"github.com/fs0ciety7000/gonema/internal/store"
	"github.com/fs0ciety7000/gonema/internal/store/db"
)

// Library scan limits.
const (
	maxLibraryCandidates = 5
	libraryLookupWorkers = 4
	maxLibrarySelections = 500
)

var (
	tmdbHint = regexp.MustCompile(`(?i)tmdb(?:id)?[-=_ ]?(\d{1,9})`)
	imdbHint = regexp.MustCompile(`(?i)(?:^|[^a-z0-9])(tt\d{7,9})(?:\D|$)`)
)

// LibraryCandidate is a TMDB movie a library folder may hold.
type LibraryCandidate struct {
	TmdbID     int     `json:"tmdbId"`
	Title      string  `json:"title"`
	Year       int     `json:"year,omitempty"`
	Confidence float64 `json:"confidence"`
	Reason     string  `json:"reason"`
	MovieID    int64   `json:"movieId,omitempty"` // already in the library
}

// LibraryFolder is a folder of a root folder that is not yet a managed movie.
type LibraryFolder struct {
	Folder  string     `json:"folder"` // name in the root folder
	Path    string     `json:"path"`
	File    string     `json:"file,omitempty"` // main video, relative to the folder
	Size    int64      `json:"size,omitempty"`
	Quality quality.ID `json:"quality,omitempty"`
	Title   string     `json:"title,omitempty"` // read from the names
	Year    int        `json:"year,omitempty"`
	// MovieID is the library movie that already uses this folder (without a
	// file): confirming links the file to it.
	MovieID    int64              `json:"movieId,omitempty"`
	Candidates []LibraryCandidate `json:"candidates"` // best first
	Rejections []string           `json:"rejections,omitempty"`
}

// ScanLibrary lists the folders of a root folder that hold a movie Gonema does
// not manage yet, with the TMDB movies they may be (best first). Nothing is
// changed, on disk or in the database.
func (s *Service) ScanLibrary(ctx context.Context, rootFolderID int64) ([]LibraryFolder, error) {
	if s.lib == nil {
		return nil, ErrNoProvider
	}
	root, err := s.st.Read().GetRootFolder(ctx, rootFolderID)
	if err != nil {
		if errors.Is(store.MapError(err), store.ErrNotFound) {
			return nil, userErr(ErrNotFound, nil, "root folder %d does not exist", rootFolderID)
		}
		return nil, store.MapError(fmt.Errorf("importer: root folder %d: %w", rootFolderID, err))
	}
	movies, err := s.st.Read().ListRootFolderMovies(ctx, rootFolderID)
	if err != nil {
		return nil, store.MapError(fmt.Errorf("importer: movies of root folder %d: %w", rootFolderID, err))
	}
	byPath := make(map[string]db.ListRootFolderMoviesRow, len(movies))
	for _, m := range movies {
		byPath[m.Path] = m
	}
	entries, err := s.fs.ReadDir(root.Path)
	if err != nil {
		return nil, fsErr(err, "cannot list the root folder %s", root.Path)
	}
	var folders []LibraryFolder
	for _, e := range entries {
		name := e.Name()
		if !e.IsDir() || strings.HasPrefix(name, ".") {
			continue // files, symbolic links, hidden folders and recycle bins
		}
		lf := LibraryFolder{Folder: name, Path: filepath.Join(root.Path, name), Candidates: []LibraryCandidate{}}
		if m, ok := byPath[name]; ok {
			if m.FileID != nil {
				continue // managed already
			}
			lf.MovieID = m.ID
			lf.Candidates = append(lf.Candidates, LibraryCandidate{
				TmdbID: int(m.TmdbID), Title: m.Title, Year: int(m.Year), Confidence: 1, MovieID: m.ID,
				Reason: "the folder of a library movie that has no file yet",
			})
		}
		folders = append(folders, lf)
	}

	// Look the folders up concurrently (bounded); results keep their order.
	var (
		wg       sync.WaitGroup
		sem      = make(chan struct{}, libraryLookupWorkers)
		errMu    sync.Mutex
		firstErr error
	)
	for i := range folders {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		wg.Add(1)
		sem <- struct{}{}
		go func(lf *LibraryFolder) {
			defer wg.Done()
			defer func() { <-sem }()
			if err := s.describeFolder(ctx, lf); err != nil {
				errMu.Lock()
				if firstErr == nil {
					firstErr = err
				}
				errMu.Unlock()
			}
		}(&folders[i])
	}
	wg.Wait()
	if firstErr != nil {
		return nil, firstErr
	}
	if folders == nil {
		folders = []LibraryFolder{}
	}
	return folders, nil
}

// describeFolder fills the file, parsed title and candidates of lf. Only a
// missing provider is an error; other problems become rejections.
func (s *Service) describeFolder(ctx context.Context, lf *LibraryFolder) error {
	res, err := s.scan(ctx, lf.Path)
	if err != nil {
		lf.Rejections = append(lf.Rejections, errMessage(err))
		return nil
	}
	valid := res.valid()
	switch {
	case len(valid) == 0:
		lf.Rejections = append(lf.Rejections, noMovieReason(res))
	case multiPart(valid):
		lf.Rejections = append(lf.Rejections, "multi-part movie (CD1, CD2...): not supported")
	}
	names := []named{{lf.Folder, parser.Parse(lf.Folder)}}
	if len(valid) > 0 {
		lf.File, lf.Size = valid[0].rel, valid[0].size
		names = append(names, named{filepath.Base(valid[0].path), valid[0].parsed})
	}
	parsed, _ := bestParse(names)
	lf.Title, lf.Year = parsed.Title, parsed.Year
	if len(valid) > 0 {
		lf.Quality = quality.FromParsed(&parsed)
	}
	if lf.MovieID != 0 {
		return nil
	}
	return s.lookupFolder(ctx, lf, &parsed)
}

func (s *Service) lookupFolder(ctx context.Context, lf *LibraryFolder, parsed *parser.ParsedRelease) error {
	query, year, hint := "", 0, ""
	switch {
	case tmdbHint.MatchString(lf.Folder):
		query, hint = "tmdb:"+tmdbHint.FindStringSubmatch(lf.Folder)[1], "the TMDB id in the folder name"
	case imdbHint.MatchString(lf.Folder):
		query, hint = strings.ToLower(imdbHint.FindStringSubmatch(lf.Folder)[1]), "the IMDb id in the folder name"
	case parsed.Title != "":
		query, year = parsed.Title, parsed.Year
	default:
		lf.Rejections = append(lf.Rejections, "the folder name does not tell which movie it is")
		return nil
	}
	results, err := s.lib.Lookup(ctx, query, year)
	if year != 0 && err == nil && len(results) == 0 {
		results, err = s.lib.Lookup(ctx, query, 0) // the year may be off by one
	}
	switch {
	case errors.Is(err, library.ErrNoProvider):
		return err
	case err != nil:
		s.log.Warn("library import lookup failed", slog.String("folder", lf.Folder), slog.Any("error", err))
		lf.Rejections = append(lf.Rejections, "the TMDB lookup failed; try again later")
		return nil
	case len(results) == 0:
		lf.Rejections = append(lf.Rejections, "TMDB has no movie matching "+describe(query, year))
		return nil
	}
	if hint != "" {
		r := results[0]
		lf.Candidates = append(lf.Candidates, LibraryCandidate{
			TmdbID: r.TMDBID, Title: r.Title, Year: r.Year, Confidence: 1, Reason: "found with " + hint, MovieID: r.MovieID,
		})
		return nil
	}
	entries := make([]match.Entry, 0, len(results))
	byID := make(map[int64]library.LookupResult, len(results))
	for i := range results {
		r := &results[i]
		entries = append(entries, match.Entry{MovieID: int64(r.TMDBID), Title: r.Title, OriginalTitle: r.OriginalTitle, Year: r.Year})
		byID[int64(r.TMDBID)] = *r
	}
	seen := map[int]bool{}
	for _, c := range match.NewIndex(entries).Match(*parsed) {
		r := byID[c.MovieID]
		lf.Candidates = append(lf.Candidates, LibraryCandidate{
			TmdbID: r.TMDBID, Title: r.Title, Year: r.Year, Confidence: c.Confidence, Reason: c.Reason, MovieID: r.MovieID,
		})
		seen[r.TMDBID] = true
	}
	for i := range results {
		r := &results[i]
		if len(lf.Candidates) >= maxLibraryCandidates {
			break
		}
		if !seen[r.TMDBID] {
			lf.Candidates = append(lf.Candidates, LibraryCandidate{
				TmdbID: r.TMDBID, Title: r.Title, Year: r.Year, Confidence: 0.2, Reason: "returned by the TMDB search", MovieID: r.MovieID,
			})
		}
	}
	if len(lf.Candidates) > maxLibraryCandidates {
		lf.Candidates = lf.Candidates[:maxLibraryCandidates]
	}
	return nil
}

func errMessage(err error) string {
	var ue *Error
	if errors.As(err, &ue) {
		return ue.UserMessage()
	}
	return "cannot be read"
}

// LibrarySelection confirms the movie of a library folder.
type LibrarySelection struct {
	Folder string `json:"folder"` // name in the root folder
	// TmdbID is required unless the folder already belongs to a library movie.
	TmdbID              int    `json:"tmdbId,omitempty"`
	QualityProfileID    int64  `json:"qualityProfileId,omitempty"`
	Monitored           bool   `json:"monitored"`
	MinimumAvailability string `json:"minimumAvailability,omitempty"` // default "released"
}

// LibraryResult is the outcome of one LibrarySelection.
type LibraryResult struct {
	Folder  string `json:"folder"`
	MovieID int64  `json:"movieId,omitempty"`
	FileID  int64  `json:"fileId,omitempty"`
	Reason  string `json:"reason,omitempty"`
	Error   string `json:"error,omitempty"`
}

// ConfirmLibrary adopts library folders: the movie is added to the library (or
// found), pointed at its existing folder, and its main video is recorded where
// it is. No file is moved, renamed or deleted.
func (s *Service) ConfirmLibrary(ctx context.Context, rootFolderID int64, sels []LibrarySelection) ([]LibraryResult, error) {
	if s.lib == nil {
		return nil, ErrNoProvider
	}
	if len(sels) == 0 {
		return nil, userErr(ErrInvalid, nil, "choose at least one folder")
	}
	if len(sels) > maxLibrarySelections {
		return nil, userErr(ErrInvalid, nil, "confirm at most %d folders at once", maxLibrarySelections)
	}
	root, err := s.st.Read().GetRootFolder(ctx, rootFolderID)
	if err != nil {
		if errors.Is(store.MapError(err), store.ErrNotFound) {
			return nil, userErr(ErrNotFound, nil, "root folder %d does not exist", rootFolderID)
		}
		return nil, store.MapError(fmt.Errorf("importer: root folder %d: %w", rootFolderID, err))
	}
	out := make([]LibraryResult, 0, len(sels))
	for i := range sels {
		if err := ctx.Err(); err != nil {
			return out, err
		}
		r, err := s.adopt(ctx, &root, &sels[i])
		r.Folder = sels[i].Folder
		if err != nil {
			if errors.Is(err, library.ErrNoProvider) {
				return out, err
			}
			r.Error = errText(err)
			if r.Error == "" {
				s.log.Error("library import failed", slog.String("folder", sels[i].Folder), slog.Any("error", err))
				r.Error = "the import failed on the server; the logs have the details"
			}
		}
		out = append(out, r)
	}
	return out, nil
}

// errText returns the user message of err, or "".
func errText(err error) string {
	var um interface{ UserMessage() string }
	if errors.As(err, &um) {
		return um.UserMessage()
	}
	return ""
}

func (s *Service) adopt(ctx context.Context, root *db.RootFolder, sel *LibrarySelection) (LibraryResult, error) {
	folder := strings.TrimSpace(sel.Folder)
	if folder == "" || strings.ContainsAny(folder, `/\`) || strings.HasPrefix(folder, ".") {
		return LibraryResult{}, userErr(ErrInvalid, nil, "%q is not a folder name of the root folder", sel.Folder)
	}
	dir, err := s.fs.SafeJoin(root.Path, folder)
	if err != nil {
		return LibraryResult{}, userErr(ErrInvalid, err, "%q is not a folder of the root folder %s", folder, root.Path)
	}
	if fi, err := s.fs.Lstat(dir); err != nil || !fi.IsDir() {
		return LibraryResult{}, userErr(ErrNotFound, err, "%s is not a folder", dir)
	}

	movie, err := s.movieForFolder(ctx, root, folder, sel)
	if err != nil {
		return LibraryResult{}, err
	}
	unlock := s.lockMovie(movie.ID)
	defer unlock()
	t, err := s.loadTarget(ctx, movie.ID)
	if err != nil {
		return LibraryResult{MovieID: movie.ID}, err
	}
	if t.current != nil {
		return LibraryResult{MovieID: movie.ID, FileID: t.current.ID, Reason: "the movie already has a file; nothing changed"}, nil
	}
	res, err := s.scan(ctx, dir)
	if err != nil {
		return LibraryResult{MovieID: movie.ID}, err
	}
	valid := res.valid()
	if len(valid) == 0 {
		return LibraryResult{MovieID: movie.ID, Reason: "added without a file: " + noMovieReason(res)}, nil
	}
	if multiPart(valid) {
		return LibraryResult{MovieID: movie.ID, Reason: "added without a file: multi-part movies (CD1, CD2...) are not supported"}, nil
	}
	main := valid[0]
	parsed, from := bestParse([]named{{folder, parser.Parse(folder)}, {filepath.Base(main.path), main.parsed}})
	p := &plan{t: t, src: main.path, size: main.size, parsed: parsed, quality: quality.FromParsed(&parsed),
		scene: strings.TrimSuffix(from, filepath.Ext(from)), source: "library"}
	p.scoreIt()
	row, err := s.record(ctx, p, main.rel)
	if err != nil {
		return LibraryResult{MovieID: movie.ID}, err
	}
	return LibraryResult{MovieID: movie.ID, FileID: row.ID, Reason: fmt.Sprintf("adopted %s (%s), left in place", main.rel, p.quality.Name())}, nil
}

// movieForFolder returns the library movie of folder, adding it when needed.
func (s *Service) movieForFolder(ctx context.Context, root *db.RootFolder, folder string, sel *LibrarySelection) (db.Movie, error) {
	movies, err := s.st.Read().ListRootFolderMovies(ctx, root.ID)
	if err != nil {
		return db.Movie{}, store.MapError(fmt.Errorf("importer: movies of root folder %d: %w", root.ID, err))
	}
	for _, m := range movies {
		if m.Path == folder {
			row, err := s.st.Read().GetMovie(ctx, m.ID)
			return row, store.MapError(err)
		}
	}
	if sel.TmdbID <= 0 || sel.QualityProfileID <= 0 {
		return db.Movie{}, userErr(ErrInvalid, nil, "choose the movie (TMDB id) and the quality profile of %s", folder)
	}
	movie, created, err := s.lib.AddMovie(ctx, sel.TmdbID, library.AddOptions{
		RootFolderID: root.ID, QualityProfileID: sel.QualityProfileID, MinimumAvailability: sel.MinimumAvailability,
		Monitored: sel.Monitored,
	})
	if err != nil {
		return db.Movie{}, err
	}
	if !created {
		if movie.RootFolderID == root.ID && movie.Path == folder {
			return movie, nil
		}
		return db.Movie{}, userErr(ErrInvalid, store.ErrConflict, "%s is already in the library, in the folder %q", movie.Title, movie.Path)
	}
	if movie.Path == folder {
		return movie, nil
	}
	updated, err := s.st.Write().SetMoviePath(ctx, db.SetMoviePathParams{ID: movie.ID, Path: folder})
	if err != nil {
		if errors.Is(store.MapError(err), store.ErrConflict) {
			return movie, userErr(ErrInvalid, store.ErrConflict, "another movie already uses the folder %q", folder)
		}
		return movie, store.MapError(fmt.Errorf("importer: set the folder of movie %d: %w", movie.ID, err))
	}
	return updated, nil
}

// record stores an adopted file (left in place) and writes history.
func (s *Service) record(ctx context.Context, p *plan, rel string) (db.MovieFile, error) {
	t := p.t
	parsedJSON, err := jsonText(&p.parsed)
	if err != nil {
		return db.MovieFile{}, err
	}
	media, err := jsonText(mediaInfo(&p.parsed))
	if err != nil {
		return db.MovieFile{}, err
	}
	langs := make([]string, 0, len(p.parsed.Languages))
	for _, l := range p.parsed.Languages {
		langs = append(langs, l.String())
	}
	langJSON, err := jsonText(langs)
	if err != nil {
		return db.MovieFile{}, err
	}
	score := int64(p.score.Total)
	reason := fmt.Sprintf("Adopted the existing file %s (%s); it was left in place", rel, p.quality.Name())
	var row db.MovieFile
	err = s.st.WithTx(ctx, func(q *db.Queries) error {
		var err error
		row, err = q.UpsertMovieFile(ctx, db.UpsertMovieFileParams{
			MovieID: t.movie.ID, RelativePath: rel, Size: p.size, Quality: string(p.quality),
			Revision: int64(max(p.parsed.Revision.Version, 1)), Edition: p.parsed.Edition.String(),
			Languages: langJSON, ReleaseGroup: p.parsed.ReleaseGroup, SceneName: p.scene,
			Parsed: parsedJSON, MediaInfo: media, CustomScore: score, ImportedAt: store.Millis(s.now()),
		})
		if err != nil {
			return fmt.Errorf("record movie file: %w", err)
		}
		return s.appendHistory(ctx, q, &historyRow{
			movieID: t.movie.ID, event: historyImported, title: p.scene, quality: string(p.quality), score: &score,
			data: map[string]any{"reason": reason, "source": p.source, "path": rel, "size": p.size, "score": p.score},
		})
	})
	if err != nil {
		return db.MovieFile{}, fmt.Errorf("importer: record the file of %s: %w", t.movie.Title, err)
	}
	s.log.Info("existing movie file adopted", slog.Int64("movie_id", t.movie.ID), slog.String("path", rel))
	s.publish(events.Event{Type: EventFileImported, MovieID: t.movie.ID, Data: map[string]any{
		"fileId": row.ID, "path": rel, "quality": p.quality, "upgraded": false, "reason": reason,
	}})
	return row, nil
}

func jsonText(v any) (string, error) {
	b, err := json.Marshal(v)
	if err != nil {
		return "", fmt.Errorf("importer: encode: %w", err)
	}
	return string(b), nil
}
