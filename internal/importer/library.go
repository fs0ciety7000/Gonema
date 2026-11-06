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
	"github.com/fs0ciety7000/gonema/internal/jobs"
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
	maxSubfolders        = 2000
)

// Match thresholds of the folder status.
const (
	confidentMatch = 0.85 // a best candidate at least this sure...
	matchMargin    = 0.15 // ...and this far ahead of the second is "new", not "ambiguous"
)

// Folder statuses of a library scan.
const (
	FolderNew       = "new"       // one confident match, not in the library: ready to import
	FolderAmbiguous = "ambiguous" // several possible movies, or a weak match: choose
	FolderNoMatch   = "no_match"  // no movie found (or no video): search by hand or skip
	FolderInLibrary = "in_library"
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
	Confidence float64 `json:"confidence" doc:"0 to 1, computed against the folder and file names"`
	Reason     string  `json:"reason" doc:"Why this movie is proposed, plain language"`
	MovieID    int64   `json:"movieId,omitempty" doc:"Library movie when it is already in the library"`
	PosterURL  *string `json:"posterUrl" doc:"w342, through the image cache"`
	posterPath string
}

// LibraryFolder is a folder of a root folder that is not yet a managed movie.
type LibraryFolder struct {
	Folder  string     `json:"folder" doc:"Path relative to the root folder (a name, or sub/name for a scan of a sub-path)"`
	Name    string     `json:"name" doc:"Folder name"`
	Path    string     `json:"path" doc:"Absolute path"`
	File    string     `json:"file,omitempty" doc:"Main video, relative to the folder"`
	Size    int64      `json:"size,omitempty"`
	Quality quality.ID `json:"quality,omitempty" doc:"Detected from the folder and file names"`
	HDR     []string   `json:"hdr" doc:"HDR formats read from the names (dv, hdr10...)"`
	Title   string     `json:"title,omitempty" doc:"Read from the names"`
	Year    int        `json:"year,omitempty"`
	Status  string     `json:"status" enum:"new,ambiguous,no_match,in_library"`
	// MovieID is the library movie that already uses this folder (without a
	// file, confirming links the file to it), or the library movie the
	// confident match already is.
	MovieID int64 `json:"movieId,omitempty"`
	// LibraryPath is where that library movie lives (absolute).
	LibraryPath string             `json:"libraryPath,omitempty"`
	Candidates  []LibraryCandidate `json:"candidates" doc:"Best first"`
	Rejections  []string           `json:"rejections,omitempty"`
}

// SetImageURL sets how candidate posters are linked (the API image route).
func (s *Service) SetImageURL(fn func(tmdbPath string) *string) { s.posterURL = fn }

func (s *Service) poster(path string) *string {
	if s.posterURL == nil || path == "" {
		return nil
	}
	return s.posterURL(path)
}

// scanBase returns the folder to scan: the root folder, or rel inside it.
func (s *Service) scanBase(root *db.RootFolder, rel string) (string, string, error) {
	rel = strings.Trim(filepath.ToSlash(strings.TrimSpace(rel)), "/")
	if rel == "" {
		return root.Path, "", nil
	}
	for _, part := range strings.Split(rel, "/") {
		if part == "" || part == "." || part == ".." || strings.HasPrefix(part, ".") {
			return "", "", userErr(ErrInvalid, nil, "%q is not a folder of the root folder %s", rel, root.Path)
		}
	}
	dir, err := s.fs.SafeJoin(root.Path, filepath.FromSlash(rel))
	if err != nil {
		return "", "", userErr(ErrInvalid, err, "%q is not a folder of the root folder %s", rel, root.Path)
	}
	if fi, err := s.fs.Lstat(dir); err != nil || !fi.IsDir() {
		return "", "", userErr(ErrNotFound, err, "%s is not a folder", dir)
	}
	return dir, rel, nil
}

func (s *Service) rootFolder(ctx context.Context, id int64) (db.RootFolder, error) {
	root, err := s.st.Read().GetRootFolder(ctx, id)
	if err != nil {
		if errors.Is(store.MapError(err), store.ErrNotFound) {
			return root, userErr(ErrNotFound, nil, "root folder %d does not exist", id)
		}
		return root, store.MapError(fmt.Errorf("importer: root folder %d: %w", id, err))
	}
	return root, nil
}

// Subfolder is a folder of the folder picker.
type Subfolder struct {
	Name   string `json:"name"`
	Path   string `json:"path" doc:"Relative to the root folder"`
	Movie  bool   `json:"movie" doc:"It holds a video file directly: a movie folder rather than a folder of movies"`
	Hidden bool   `json:"-"`
}

// Subfolders lists the folders of a root folder, or of rel inside it, for the
// folder picker (hidden folders and the recycle bin excluded). Nothing is
// changed.
func (s *Service) Subfolders(ctx context.Context, rootFolderID int64, rel string) ([]Subfolder, error) {
	root, err := s.rootFolder(ctx, rootFolderID)
	if err != nil {
		return nil, err
	}
	base, rel, err := s.scanBase(&root, rel)
	if err != nil {
		return nil, err
	}
	entries, err := s.fs.ReadDir(base)
	if err != nil {
		return nil, fsErr(err, "cannot list the folder %s", base)
	}
	out := []Subfolder{}
	for _, e := range entries {
		if !e.IsDir() || strings.HasPrefix(e.Name(), ".") {
			continue
		}
		if len(out) >= maxSubfolders {
			break
		}
		p := e.Name()
		if rel != "" {
			p = rel + "/" + e.Name()
		}
		out = append(out, Subfolder{Name: e.Name(), Path: p, Movie: s.holdsVideo(filepath.Join(base, e.Name()))})
	}
	return out, nil
}

// holdsVideo reports whether dir holds a video file directly.
func (s *Service) holdsVideo(dir string) bool {
	entries, err := s.fs.ReadDir(dir)
	if err != nil {
		return false
	}
	for _, e := range entries {
		if e.Type().IsRegular() && videoExts[strings.ToLower(filepath.Ext(e.Name()))] {
			return true
		}
	}
	return false
}

// listFolders returns the candidate folders of a scan: the folders of base, or
// base itself when it is a movie folder (a video directly inside).
func (s *Service) listFolders(ctx context.Context, root *db.RootFolder, rel string) ([]LibraryFolder, error) {
	base, rel, err := s.scanBase(root, rel)
	if err != nil {
		return nil, err
	}
	movies, err := s.st.Read().ListRootFolderMovies(ctx, root.ID)
	if err != nil {
		return nil, store.MapError(fmt.Errorf("importer: movies of root folder %d: %w", root.ID, err))
	}
	byPath := make(map[string]db.ListRootFolderMoviesRow, len(movies))
	for _, m := range movies {
		byPath[m.Path] = m
	}
	var names []string
	if rel != "" && s.holdsVideo(base) {
		names = []string{rel}
	} else {
		entries, err := s.fs.ReadDir(base)
		if err != nil {
			if rel == "" {
				return nil, fsErr(err, "cannot list the root folder %s", base)
			}
			return nil, fsErr(err, "cannot list the folder %s", base)
		}
		for _, e := range entries {
			if !e.IsDir() || strings.HasPrefix(e.Name(), ".") {
				continue // files, symbolic links, hidden folders and recycle bins
			}
			n := e.Name()
			if rel != "" {
				n = rel + "/" + n
			}
			names = append(names, n)
		}
	}
	var folders []LibraryFolder
	for _, n := range names {
		lf := LibraryFolder{Folder: n, Name: filepath.Base(filepath.FromSlash(n)), Path: filepath.Join(root.Path, filepath.FromSlash(n)),
			Candidates: []LibraryCandidate{}, HDR: []string{}}
		if m, ok := byPath[n]; ok {
			if m.FileID != nil {
				continue // managed already
			}
			lf.MovieID, lf.LibraryPath = m.ID, lf.Path
			lf.Candidates = append(lf.Candidates, LibraryCandidate{
				TmdbID: int(m.TmdbID), Title: m.Title, Year: int(m.Year), Confidence: 1, MovieID: m.ID,
				Reason: "the folder of a library movie that has no file yet",
			})
		}
		folders = append(folders, lf)
	}
	return folders, nil
}

// ScanLibrary lists the folders of a root folder that hold a movie Gonema does
// not manage yet, with the TMDB movies they may be (best first). Nothing is
// changed, on disk or in the database.
func (s *Service) ScanLibrary(ctx context.Context, rootFolderID int64) ([]LibraryFolder, error) {
	return s.ScanLibraryPath(ctx, rootFolderID, "", nil)
}

// ScanLibraryPath is ScanLibrary for a sub-path of the root folder ("" for the
// root folder itself); found, when set, is called with each described folder
// (from several goroutines, one call at a time).
func (s *Service) ScanLibraryPath(ctx context.Context, rootFolderID int64, rel string, found func(done, total int, f *LibraryFolder)) ([]LibraryFolder, error) {
	if s.lib == nil {
		return nil, ErrNoProvider
	}
	root, err := s.rootFolder(ctx, rootFolderID)
	if err != nil {
		return nil, err
	}
	folders, err := s.listFolders(ctx, &root, rel)
	if err != nil {
		return nil, err
	}
	// Look the folders up concurrently (bounded); results keep their order.
	var (
		wg       sync.WaitGroup
		sem      = make(chan struct{}, libraryLookupWorkers)
		mu       sync.Mutex
		done     int
		firstErr error
	)
	for i := range folders {
		if err := ctx.Err(); err != nil {
			wg.Wait()
			return folders[:0], err
		}
		wg.Add(1)
		sem <- struct{}{}
		go func(lf *LibraryFolder) {
			defer wg.Done()
			defer func() { <-sem }()
			err := s.describeFolder(ctx, lf)
			mu.Lock()
			defer mu.Unlock()
			if err != nil && firstErr == nil {
				firstErr = err
			}
			done++
			if err == nil && found != nil {
				found(done, len(folders), lf)
			}
		}(&folders[i])
	}
	wg.Wait()
	if firstErr != nil {
		return nil, firstErr
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if folders == nil {
		folders = []LibraryFolder{}
	}
	return folders, nil
}

// describeFolder fills the file, parsed title, candidates and status of lf.
// Only a missing provider is an error; other problems become rejections.
func (s *Service) describeFolder(ctx context.Context, lf *LibraryFolder) error {
	defer s.settle(ctx, lf)
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
	parsed := folderParse(lf.Name, valid)
	lf.Title, lf.Year = parsed.Title, parsed.Year
	if len(valid) > 0 {
		lf.File, lf.Size = valid[0].rel, valid[0].size
		lf.Quality = quality.FromParsed(&parsed)
		for _, h := range parsed.HDR {
			lf.HDR = append(lf.HDR, h.String())
		}
	}
	if lf.MovieID != 0 {
		return nil
	}
	return s.lookupFolder(ctx, lf, &parsed)
}

func folderParse(name string, valid []videoFile) parser.ParsedRelease {
	names := []named{{name, parser.Parse(name)}}
	if len(valid) > 0 {
		names = append(names, named{filepath.Base(valid[0].path), valid[0].parsed})
	}
	p, _ := bestParse(names)
	return p
}

// settle sets the status of a described folder and, for a match already in the
// library, where it lives.
func (s *Service) settle(ctx context.Context, lf *LibraryFolder) {
	lf.Status = folderStatus(lf)
	if lf.Status != FolderInLibrary || lf.LibraryPath != "" || len(lf.Candidates) == 0 {
		return
	}
	id := lf.Candidates[0].MovieID
	lf.MovieID = id
	m, err := s.st.Read().GetMovie(ctx, id)
	if err != nil {
		return
	}
	if rf, err := s.st.Read().GetRootFolder(ctx, m.RootFolderID); err == nil {
		lf.LibraryPath = filepath.Join(rf.Path, filepath.FromSlash(m.Path))
	}
}

// folderStatus classifies a described folder (pure).
func folderStatus(lf *LibraryFolder) string {
	if lf.MovieID != 0 {
		return FolderInLibrary
	}
	if len(lf.Candidates) == 0 {
		return FolderNoMatch
	}
	best := lf.Candidates[0]
	sure := best.Confidence >= confidentMatch &&
		(len(lf.Candidates) == 1 || best.Confidence-lf.Candidates[1].Confidence >= matchMargin)
	switch {
	case sure && best.MovieID != 0:
		return FolderInLibrary
	case sure && lf.File != "":
		return FolderNew
	}
	return FolderAmbiguous
}

func (s *Service) lookupFolder(ctx context.Context, lf *LibraryFolder, parsed *parser.ParsedRelease) error {
	query, year, hint := "", 0, ""
	switch {
	case tmdbHint.MatchString(lf.Name):
		query, hint = "tmdb:"+tmdbHint.FindStringSubmatch(lf.Name)[1], "the TMDB id in the folder name"
	case imdbHint.MatchString(lf.Name):
		query, hint = strings.ToLower(imdbHint.FindStringSubmatch(lf.Name)[1]), "the IMDb id in the folder name"
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
		lf.Candidates = append(lf.Candidates, s.candidate(&r, 1, "found with "+hint))
		return nil
	}
	lf.Candidates = append(lf.Candidates, s.rankCandidates(parsed, results)...)
	return nil
}

func (s *Service) candidate(r *library.LookupResult, conf float64, reason string) LibraryCandidate {
	return LibraryCandidate{
		TmdbID: r.TMDBID, Title: r.Title, Year: r.Year, Confidence: conf, Reason: reason, MovieID: r.MovieID,
		PosterURL: s.poster(r.PosterPath), posterPath: r.PosterPath,
	}
}

// rankCandidates scores lookup results against the parsed folder: matched
// titles first with the matcher's confidence and reason, then the other
// results (low confidence), at most maxLibraryCandidates.
func (s *Service) rankCandidates(parsed *parser.ParsedRelease, results []library.LookupResult) []LibraryCandidate {
	entries := make([]match.Entry, 0, len(results))
	byID := make(map[int64]library.LookupResult, len(results))
	for i := range results {
		r := &results[i]
		entries = append(entries, match.Entry{MovieID: int64(r.TMDBID), Title: r.Title, OriginalTitle: r.OriginalTitle, Year: r.Year})
		byID[int64(r.TMDBID)] = *r
	}
	var out []LibraryCandidate
	seen := map[int]bool{}
	for _, c := range match.NewIndex(entries).Match(*parsed) {
		r := byID[c.MovieID]
		out = append(out, s.candidate(&r, c.Confidence, c.Reason))
		seen[r.TMDBID] = true
	}
	for i := range results {
		r := &results[i]
		if len(out) >= maxLibraryCandidates {
			break
		}
		if !seen[r.TMDBID] {
			out = append(out, s.candidate(r, 0.2, "returned by the TMDB search; the title or year differs from the folder"))
		}
	}
	if len(out) > maxLibraryCandidates {
		out = out[:maxLibraryCandidates]
	}
	return out
}

// MatchCandidates searches TMDB for query (a title, tmdb:<id> or an IMDb id)
// and scores the results against a folder of a root folder: the "change
// match" dialog. Nothing is changed.
func (s *Service) MatchCandidates(ctx context.Context, rootFolderID int64, folder, query string, year int) ([]LibraryCandidate, error) {
	if s.lib == nil {
		return nil, ErrNoProvider
	}
	root, err := s.rootFolder(ctx, rootFolderID)
	if err != nil {
		return nil, err
	}
	dir, rel, err := s.scanBase(&root, folder)
	if err != nil {
		return nil, err
	}
	if rel == "" {
		return nil, userErr(ErrInvalid, nil, "choose a folder of the root folder")
	}
	var valid []videoFile
	if res, err := s.scan(ctx, dir); err == nil {
		valid = res.valid()
	}
	parsed := folderParse(filepath.Base(dir), valid)
	results, err := s.lib.Lookup(ctx, query, year)
	if err != nil {
		return nil, err
	}
	out := s.rankCandidates(&parsed, results)
	if out == nil {
		out = []LibraryCandidate{}
	}
	return out, nil
}

func errMessage(err error) string {
	var ue *Error
	if errors.As(err, &ue) {
		return ue.UserMessage()
	}
	return "cannot be read"
}

// Import modes of a library folder.
const (
	LibraryKeep = "keep" // record the file where it is (default)
	LibraryMove = "move" // then move it into Gonema's naming (folder and file templates)
)

// LibrarySelection confirms the movie of a library folder.
type LibrarySelection struct {
	Folder string `json:"folder" doc:"Folder as listed by the scan (relative to the root folder)"`
	// TmdbID is required unless the folder already belongs to a library movie.
	TmdbID              int    `json:"tmdbId,omitempty"`
	QualityProfileID    int64  `json:"qualityProfileId,omitempty"`
	Monitored           bool   `json:"monitored" doc:"Monitored movies are upgraded later, up to the profile cutoff"`
	MinimumAvailability string `json:"minimumAvailability,omitempty" doc:"Default released"`
	SearchOnAdd         bool   `json:"searchOnAdd,omitempty" doc:"Search right after the import: a missing file, or an upgrade below the cutoff (monitored movies only)"`
	Mode                string `json:"mode,omitempty" enum:"keep,move" doc:"keep (default) records the file where it is; move then moves it into the naming templates (nothing is deleted; on failure the files are put back)"`
}

// Outcomes of a library folder.
const (
	OutcomeKept    = "kept"    // file recorded in place
	OutcomeMoved   = "moved"   // file recorded then moved into the naming templates
	OutcomeAdded   = "added"   // movie added without a file
	OutcomeSkipped = "skipped" // nothing to do (the movie already has a file)
	OutcomeFailed  = "failed"
)

// LibraryResult is the outcome of one LibrarySelection.
type LibraryResult struct {
	Folder  string `json:"folder"`
	Outcome string `json:"outcome" enum:"kept,moved,added,skipped,failed"`
	MovieID int64  `json:"movieId,omitempty"`
	FileID  int64  `json:"fileId,omitempty"`
	Reason  string `json:"reason,omitempty"`
	Error   string `json:"error,omitempty"`
	// BelowCutoff: the recorded file is below the profile cutoff.
	BelowCutoff bool `json:"belowCutoff"`
	created     bool
}

// LibraryImportSummary counts the outcomes of a confirmation.
type LibraryImportSummary struct {
	Imported int `json:"imported" doc:"Files recorded (kept + moved)"`
	Kept     int `json:"kept"`
	Moved    int `json:"moved"`
	Added    int `json:"added" doc:"Movies added without a file"`
	Skipped  int `json:"skipped"`
	Failed   int `json:"failed"`
	// BelowCutoff counts the imported files below their profile cutoff:
	// monitored movies are upgraded later.
	BelowCutoff int `json:"belowCutoff" doc:"Imported files below their profile cutoff (upgraded later when monitored)"`
	// CollectionsGrown are the collections with more films on disk after the
	// import; CollectionsCompleted those whose every known film is now in the
	// library thanks to it.
	CollectionsGrown     []CompletedCollection `json:"collectionsGrown"`
	CollectionsCompleted []CompletedCollection `json:"collectionsCompleted"`
}

// CompletedCollection is a collection completed by an import.
type CompletedCollection struct {
	ID   int64  `json:"id"`
	Name string `json:"name"`
}

// LibraryImport is the outcome of a confirmation.
type LibraryImport struct {
	Results []LibraryResult      `json:"results" doc:"In the order of the selections"`
	Summary LibraryImportSummary `json:"summary"`
}

// ConfirmLibrary adopts library folders (see ImportLibrary) and returns the
// per-folder results.
func (s *Service) ConfirmLibrary(ctx context.Context, rootFolderID int64, sels []LibrarySelection) ([]LibraryResult, error) {
	out, err := s.ImportLibrary(ctx, rootFolderID, sels)
	return out.Results, err
}

// ImportLibrary adopts library folders: each movie is added to the library (or
// found), pointed at its existing folder, and its main video is recorded where
// it is. With Mode move, the folder and file are then renamed to the naming
// templates inside the root folder (a move, never a deletion; on failure the
// files already moved are put back and the movie keeps its original folder).
func (s *Service) ImportLibrary(ctx context.Context, rootFolderID int64, sels []LibrarySelection) (LibraryImport, error) {
	out := LibraryImport{Results: []LibraryResult{}, Summary: LibraryImportSummary{
		CollectionsGrown: []CompletedCollection{}, CollectionsCompleted: []CompletedCollection{},
	}}
	if s.lib == nil {
		return out, ErrNoProvider
	}
	if len(sels) == 0 {
		return out, userErr(ErrInvalid, nil, "choose at least one folder")
	}
	if len(sels) > maxLibrarySelections {
		return out, userErr(ErrInvalid, nil, "confirm at most %d folders at once", maxLibrarySelections)
	}
	root, err := s.rootFolder(ctx, rootFolderID)
	if err != nil {
		return out, err
	}
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
			r.Outcome = OutcomeFailed
			r.Error = errText(err)
			if r.Error == "" {
				s.log.Error("library import failed", slog.String("folder", sels[i].Folder), slog.Any("error", err))
				r.Error = "the import failed on the server; the logs have the details"
			}
		}
		if r.Outcome == OutcomeKept || r.Outcome == OutcomeMoved {
			if st, err := s.st.Read().GetMovieState(ctx, r.MovieID); err == nil && st.State == "upgradable" {
				r.BelowCutoff = true
				out.Summary.BelowCutoff++
			}
		}
		out.Results = append(out.Results, r)
		s.count(&out.Summary, &r)
	}
	out.Summary.CollectionsGrown, out.Summary.CollectionsCompleted = s.collectionsOf(ctx, out.Results)
	s.log.Info("library folders imported", slog.Int("kept", out.Summary.Kept), slog.Int("moved", out.Summary.Moved),
		slog.Int("added", out.Summary.Added), slog.Int("skipped", out.Summary.Skipped), slog.Int("failed", out.Summary.Failed))
	return out, nil
}

func (s *Service) count(sum *LibraryImportSummary, r *LibraryResult) {
	switch r.Outcome {
	case OutcomeKept:
		sum.Kept++
		sum.Imported++
	case OutcomeMoved:
		sum.Moved++
		sum.Imported++
	case OutcomeAdded:
		sum.Added++
	case OutcomeSkipped:
		sum.Skipped++
	default:
		sum.Failed++
	}
}

// collectionViewer is implemented by library.Service.
type collectionViewer interface {
	CollectionView(ctx context.Context, id int64) (library.CollectionView, library.CollectionDetails, error)
}

// collectionsOf returns the collections of the imported files (grown) and,
// among those, the ones whose every known film is now in the library while a
// film of them was added by this import (completed).
func (s *Service) collectionsOf(ctx context.Context, results []LibraryResult) (grown, completed []CompletedCollection) {
	grown, completed = []CompletedCollection{}, []CompletedCollection{}
	cv, ok := s.lib.(collectionViewer)
	if !ok {
		return grown, completed
	}
	added := map[int64]bool{}
	var order []int64
	for i := range results {
		r := &results[i]
		if r.MovieID == 0 || (r.Outcome != OutcomeKept && r.Outcome != OutcomeMoved) {
			continue
		}
		m, err := s.st.Read().GetMovie(ctx, r.MovieID)
		if err != nil || m.CollectionID == nil {
			continue
		}
		if _, seen := added[*m.CollectionID]; !seen {
			order = append(order, *m.CollectionID)
			added[*m.CollectionID] = false
		}
		added[*m.CollectionID] = added[*m.CollectionID] || r.created
	}
	for _, id := range order {
		v, _, err := cv.CollectionView(ctx, id)
		if err != nil {
			continue
		}
		c := CompletedCollection{ID: v.Collection.ID, Name: v.Collection.Name}
		grown = append(grown, c)
		if added[id] && v.PartsKnown && v.Counts.NotInLibrary == 0 {
			completed = append(completed, c)
		}
	}
	return grown, completed
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
	switch sel.Mode {
	case "", LibraryKeep, LibraryMove:
	default:
		return LibraryResult{}, userErr(ErrInvalid, nil, "the mode must be keep or move")
	}
	dir, folder, err := s.scanBase(root, sel.Folder)
	if err != nil || folder == "" {
		return LibraryResult{}, userErr(ErrInvalid, err, "%q is not a folder of the root folder %s", sel.Folder, root.Path)
	}

	movie, created, err := s.movieForFolder(ctx, root, folder, sel)
	if err != nil {
		return LibraryResult{created: created, MovieID: movie.ID}, err
	}
	r, err := s.adoptFile(ctx, movie, dir, folder)
	r.created = created
	if err != nil {
		return r, err
	}
	if r.Outcome == OutcomeKept && sel.Mode == LibraryMove {
		if _, err := s.Rename(ctx, movie.ID); err != nil {
			r.Reason += "; not moved: " + errorText(err) + " (the files stay where they were)"
		} else {
			r.Outcome = OutcomeMoved
			r.Reason += "; then moved into the naming templates"
		}
	}
	if sel.SearchOnAdd && movie.Monitored {
		s.enqueueSearch(ctx, movie.ID)
	}
	return r, nil
}

func errorText(err error) string {
	if t := errText(err); t != "" {
		return t
	}
	return "the move failed"
}

// enqueueSearch queues the automatic search of an imported movie: the missing
// file, or an upgrade when the file is below the cutoff.
func (s *Service) enqueueSearch(ctx context.Context, movieID int64) {
	if s.jobs == nil {
		return
	}
	if _, _, err := s.jobs.Enqueue(ctx, library.JobMovieSearch, library.MoviePayload{MovieID: movieID}, jobs.EnqueueOptions{
		DedupeKey: fmt.Sprintf("%s:%d", library.JobMovieSearch, movieID), Priority: -2, MaxAttempts: 2,
	}); err != nil {
		s.log.Warn("search after library import not queued", slog.Int64("movie_id", movieID), slog.Any("error", err))
	}
}

// adoptFile records the main video of dir for movie, left in place.
func (s *Service) adoptFile(ctx context.Context, movie db.Movie, dir, folder string) (LibraryResult, error) {
	unlock := s.lockMovie(movie.ID)
	defer unlock()
	t, err := s.loadTarget(ctx, movie.ID)
	if err != nil {
		return LibraryResult{MovieID: movie.ID}, err
	}
	if t.current != nil {
		return LibraryResult{MovieID: movie.ID, FileID: t.current.ID, Outcome: OutcomeSkipped, Reason: "the movie already has a file; nothing changed"}, nil
	}
	res, err := s.scan(ctx, dir)
	if err != nil {
		return LibraryResult{MovieID: movie.ID}, err
	}
	valid := res.valid()
	if len(valid) == 0 {
		return LibraryResult{MovieID: movie.ID, Outcome: OutcomeAdded, Reason: "added without a file: " + noMovieReason(res)}, nil
	}
	if multiPart(valid) {
		return LibraryResult{MovieID: movie.ID, Outcome: OutcomeAdded, Reason: "added without a file: multi-part movies (CD1, CD2...) are not supported"}, nil
	}
	main := valid[0]
	name := filepath.Base(dir)
	parsed, from := bestParse([]named{{name, parser.Parse(name)}, {filepath.Base(main.path), main.parsed}})
	p := &plan{t: t, src: main.path, size: main.size, parsed: parsed, quality: quality.FromParsed(&parsed),
		scene: strings.TrimSuffix(from, filepath.Ext(from)), source: "library"}
	p.scoreIt()
	row, err := s.record(ctx, p, main.rel)
	if err != nil {
		return LibraryResult{MovieID: movie.ID}, err
	}
	return LibraryResult{MovieID: movie.ID, FileID: row.ID, Outcome: OutcomeKept,
		Reason: fmt.Sprintf("adopted %s (%s), left in place", main.rel, p.quality.Name())}, nil
}

// movieForFolder returns the library movie of folder, adding it when needed;
// created tells that it was added.
func (s *Service) movieForFolder(ctx context.Context, root *db.RootFolder, folder string, sel *LibrarySelection) (db.Movie, bool, error) {
	movies, err := s.st.Read().ListRootFolderMovies(ctx, root.ID)
	if err != nil {
		return db.Movie{}, false, store.MapError(fmt.Errorf("importer: movies of root folder %d: %w", root.ID, err))
	}
	for _, m := range movies {
		if m.Path == folder {
			row, err := s.st.Read().GetMovie(ctx, m.ID)
			return row, false, store.MapError(err)
		}
	}
	if sel.TmdbID <= 0 || sel.QualityProfileID <= 0 {
		return db.Movie{}, false, userErr(ErrInvalid, nil, "choose the movie (TMDB id) and the quality profile of %s", folder)
	}
	movie, created, err := s.lib.AddMovie(ctx, sel.TmdbID, library.AddOptions{
		RootFolderID: root.ID, QualityProfileID: sel.QualityProfileID, MinimumAvailability: sel.MinimumAvailability,
		Monitored: sel.Monitored, // searched after the file is recorded (adopt), never before
		Reason:    "Added by the import of an existing folder: " + folder,
	})
	if err != nil {
		return db.Movie{}, false, err
	}
	if !created {
		if movie.RootFolderID == root.ID && movie.Path == folder {
			return movie, false, nil
		}
		return db.Movie{}, false, userErr(ErrInvalid, store.ErrConflict, "%s is already in the library, in the folder %q", movie.Title, movie.Path)
	}
	if movie.Path == folder {
		return movie, true, nil
	}
	updated, err := s.st.Write().SetMoviePath(ctx, db.SetMoviePathParams{ID: movie.ID, Path: folder})
	if err != nil {
		if errors.Is(store.MapError(err), store.ErrConflict) {
			return movie, true, userErr(ErrInvalid, store.ErrConflict, "another movie already uses the folder %q", folder)
		}
		return movie, true, store.MapError(fmt.Errorf("importer: set the folder of movie %d: %w", movie.ID, err))
	}
	return updated, true, nil
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
