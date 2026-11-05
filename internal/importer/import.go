package importer

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"log/slog"
	"os"
	"path/filepath"
	"strconv"
	"strings"

	"github.com/fs0ciety7000/gonema/internal/decision"
	"github.com/fs0ciety7000/gonema/internal/events"
	"github.com/fs0ciety7000/gonema/internal/fsx"
	"github.com/fs0ciety7000/gonema/internal/library"
	"github.com/fs0ciety7000/gonema/internal/match"
	"github.com/fs0ciety7000/gonema/internal/naming"
	"github.com/fs0ciety7000/gonema/internal/parser"
	"github.com/fs0ciety7000/gonema/internal/quality"
	"github.com/fs0ciety7000/gonema/internal/store"
	"github.com/fs0ciety7000/gonema/internal/store/db"
)

// ErrFileSystem is returned when a file operation fails (permissions, full disk, missing
// folder). The source is left as it was.
var ErrFileSystem = errors.New("importer: file operation failed")

// Request asks to import a completed download for a movie.
type Request struct {
	DownloadID int64 // optional: the queue entry; the movie it was grabbed for is trusted
	MovieID    int64
	// LocalPath is the file or folder to import, as seen by Gonema (remote path
	// mapping applied). It must be inside an allowed folder.
	LocalPath    string
	ReleaseTitle string // the grabbed release name; helps when file names are obfuscated
	Protocol     decision.Protocol
	// ClientSeeding is true when the client keeps the data (seeding torrent): the
	// file is hard linked or copied, never moved.
	ClientSeeding bool
}

// Result is the outcome of an import. Imported false with a nil error means the
// content was refused (sample, wrong movie, not an upgrade...): Reason says why
// in plain language. An error means the import could not run (permissions,
// disk): nothing changed.
type Result struct {
	Imported   bool     `json:"imported"`
	Upgraded   bool     `json:"upgraded"`
	FileID     int64    `json:"fileId,omitempty"`
	Path       string   `json:"path,omitempty"` // relative to the movie folder
	Reason     string   `json:"reason"`
	Rejections []string `json:"rejections,omitempty"` // files skipped and why
	Subtitles  []string `json:"subtitles,omitempty"`  // subtitle files imported
	Notes      []string `json:"notes,omitempty"`      // fallbacks and warnings
}

// target is the movie a file is imported for, with what decisions need.
type target struct {
	movie   db.Movie
	root    db.RootFolder
	alts    []db.AlternateTitle
	profile quality.Profile
	rules   []*quality.Rule
	rename  map[string]bool // rule name -> include_in_rename
	current *db.MovieFile
}

func (s *Service) loadTarget(ctx context.Context, movieID int64) (*target, error) {
	t := &target{rename: map[string]bool{}}
	err := s.st.ReadTx(ctx, func(q *db.Queries) error {
		var err error
		if t.movie, err = q.GetMovie(ctx, movieID); err != nil {
			if errors.Is(store.MapError(err), store.ErrNotFound) {
				return userErr(ErrNotFound, nil, "movie %d is not in the library", movieID)
			}
			return err
		}
		if t.root, err = q.GetRootFolder(ctx, t.movie.RootFolderID); err != nil {
			return fmt.Errorf("root folder of movie %d: %w", movieID, err)
		}
		if t.alts, err = q.ListAlternateTitles(ctx, movieID); err != nil {
			return fmt.Errorf("alternate titles of movie %d: %w", movieID, err)
		}
		if t.profile, err = library.LoadQualityProfile(ctx, q, t.movie.QualityProfileID); err != nil {
			return err
		}
		if t.rules, err = library.LoadScoringRules(ctx, q); err != nil {
			return err
		}
		rows, err := q.ListScoringRules(ctx)
		if err != nil {
			return fmt.Errorf("list scoring rules: %w", err)
		}
		for _, r := range rows {
			t.rename[r.Name] = r.IncludeInRename
		}
		f, err := q.GetCurrentMovieFile(ctx, movieID)
		switch {
		case err == nil:
			t.current = &f
		case !errors.Is(store.MapError(err), store.ErrNotFound):
			return fmt.Errorf("current file of movie %d: %w", movieID, err)
		}
		return nil
	})
	if err != nil {
		var ue *Error
		if errors.As(err, &ue) {
			return nil, ue
		}
		return nil, fmt.Errorf("importer: load movie %d: %w", movieID, err)
	}
	return t, nil
}

// plan is one file about to be imported.
type plan struct {
	t          *target
	src        string
	size       int64
	parsed     parser.ParsedRelease
	scene      string // release name kept as movie_files.scene_name
	quality    quality.ID
	score      quality.Breakdown
	mode       fsx.Mode
	downloadID int64
	subs       []subFile
	anyQuality bool // the profile's allowed qualities are not checked (manual)
	replace    bool // replace the current file even if not an upgrade (manual)
	upgrade    decision.UpgradeReason
	source     string // "download", "manual", "library"
}

func (p *plan) fileQuality() decision.FileQuality {
	return decision.FileQuality{Quality: p.quality, Revision: p.parsed.Revision, CustomScore: p.score.Total}
}

// score computes the custom score of the plan.
func (p *plan) scoreIt() {
	p.score = quality.Score(&p.t.profile, p.t.rules, &quality.Release{Title: p.scene, Parsed: &p.parsed, Size: p.size})
}

// evaluate returns why the plan must not be imported, or "".
func (p *plan) evaluate() string {
	prof := &p.t.profile
	if !p.anyQuality && !prof.Allowed(p.quality) {
		return fmt.Sprintf("%s is not wanted by the quality profile %q", p.quality.Name(), prof.Name)
	}
	if p.t.current == nil {
		return ""
	}
	cur := fileQuality(p.t.current)
	if p.replace {
		p.upgrade = "manual"
		return ""
	}
	reason, ok := decision.IsUpgrade(cur, p.fileQuality(), prof)
	if ok {
		p.upgrade = reason
		return ""
	}
	why := ""
	switch {
	case !prof.UpgradeAllowed:
		why = fmt.Sprintf("; upgrades are turned off in the quality profile %q", prof.Name)
	case decision.CutoffMet(cur, prof):
		why = "; the current file already meets the profile's cutoff"
	}
	return fmt.Sprintf("Not an upgrade: the current file is %s (score %d), this one is %s (score %d)%s",
		qualityLabel(cur.Quality, cur.Revision), cur.CustomScore, qualityLabel(p.quality, p.parsed.Revision), p.score.Total, why)
}

func qualityLabel(id quality.ID, r parser.Revision) string {
	s := id.Name()
	if r.Version > 1 {
		s += " (revision " + strconv.Itoa(r.Version) + ")"
	}
	return s
}

// Import imports a completed download (see Request and Result).
func (s *Service) Import(ctx context.Context, req Request) (Result, error) {
	if req.MovieID <= 0 {
		return Result{}, userErr(ErrInvalid, nil, "the movie to import for is required")
	}
	unlock := s.lockMovie(req.MovieID)
	defer unlock()

	set, err := s.Settings(ctx)
	if err != nil {
		return Result{}, err
	}
	src, err := s.checkSource(ctx, &set, req.LocalPath)
	if err != nil {
		return Result{}, err
	}
	t, err := s.loadTarget(ctx, req.MovieID)
	if err != nil {
		return Result{}, err
	}
	var dl *db.Download
	if req.DownloadID > 0 {
		d, err := s.st.Read().GetDownload(ctx, req.DownloadID)
		if err != nil {
			if errors.Is(store.MapError(err), store.ErrNotFound) {
				return Result{}, userErr(ErrNotFound, nil, "download %d does not exist", req.DownloadID)
			}
			return Result{}, store.MapError(fmt.Errorf("importer: get download %d: %w", req.DownloadID, err))
		}
		if d.MovieID != req.MovieID {
			return Result{}, userErr(ErrInvalid, nil, "download %d was grabbed for another movie (%d)", d.ID, d.MovieID)
		}
		dl = &d
		if req.ReleaseTitle == "" {
			req.ReleaseTitle = d.ReleaseTitle
		}
		if req.Protocol == "" {
			req.Protocol = decision.Protocol(d.Protocol)
		}
	}
	reject := func(reason string, rejections []string) (Result, error) {
		r := Result{Reason: reason, Rejections: rejections}
		s.recordRejection(ctx, t, &req, &r)
		return r, nil
	}

	res, err := s.scan(ctx, src)
	if err != nil {
		return Result{}, err
	}
	valid := res.valid()
	if len(valid) == 0 {
		return reject(noMovieReason(res), res.rejections())
	}
	if multiPart(valid) {
		return reject("The movie is split into parts (CD1, CD2...): multi-part movies are not supported; join the parts into one file, then import it manually", res.rejections())
	}
	if req.ReleaseTitle != "" {
		if rp := parser.Parse(req.ReleaseTitle); !rp.IsMovie() {
			return reject(fmt.Sprintf("%q is not the movie itself (sample, bonus material or repair pack)", req.ReleaseTitle), res.rejections())
		}
	}
	main := valid[0]
	names := []named{{req.ReleaseTitle, parser.Parse(req.ReleaseTitle)}}
	if dir := filepath.Dir(main.path); dir != src && filepath.Clean(dir) != filepath.Clean(filepath.Dir(src)) {
		names = append(names, named{filepath.Base(dir), parser.Parse(filepath.Base(dir))})
	}
	if fi, err := s.fs.Lstat(src); err == nil && fi.IsDir() {
		names = append(names, named{filepath.Base(src), parser.Parse(filepath.Base(src))})
	}
	names = append(names, named{filepath.Base(main.path), main.parsed})
	parsed, from := bestParse(names)
	scene := req.ReleaseTitle
	if scene == "" {
		scene = strings.TrimSuffix(from, filepath.Ext(from))
	}
	if dl == nil {
		if why := s.checkMatch(t, &parsed); why != "" {
			return reject(why, res.rejections())
		}
	}
	q := quality.FromParsed(&parsed)
	if q == quality.Unknown && dl != nil {
		q = qualityID(dl.Quality)
	}
	if q == quality.Unknown {
		return reject(fmt.Sprintf("The quality of %s cannot be told from its name; import it manually and choose the quality", filepath.Base(main.path)), res.rejections())
	}
	mode := set.Mode
	if req.Protocol == decision.Torrent && req.ClientSeeding {
		mode = set.TorrentMode
	}
	p := &plan{
		t: t, src: main.path, size: main.size, parsed: parsed, scene: scene, quality: q, mode: mode,
		downloadID: req.DownloadID, anyQuality: dl != nil && dl.Manual, source: "download",
	}
	if set.ImportSubtitles {
		p.subs = res.subs
	}
	p.scoreIt()
	if why := p.evaluate(); why != "" {
		return reject(why, res.rejections())
	}
	out, err := s.execute(ctx, &set, p)
	if err != nil {
		return Result{}, err
	}
	out.Rejections = res.rejections()
	return out, nil
}

// noMovieReason explains why no video of a scan is the movie.
func noMovieReason(res *scanResult) string {
	samples := 0
	for i := range res.videos {
		if strings.Contains(res.videos[i].rejection, "sample") {
			samples++
		}
	}
	switch {
	case len(res.videos) == 0:
		return "No video file was found (mkv, mp4, avi, m2ts...)"
	case samples == len(res.videos):
		return "Only a sample was found, not the movie"
	}
	return "No video file is the movie itself (samples, extras or files too small)"
}

// checkMatch verifies that the parsed name is about the target movie. It
// returns why not, or "".
func (s *Service) checkMatch(t *target, p *parser.ParsedRelease) string {
	m := &t.movie
	if p.Title == "" {
		return "The name does not tell which movie this is (obfuscated name?); import it manually and choose the movie"
	}
	e := match.Entry{MovieID: m.ID, Title: m.Title, OriginalTitle: m.OriginalTitle, Year: int(m.Year)}
	if m.ImdbID != nil {
		e.IMDbID = *m.ImdbID
	}
	for _, a := range t.alts {
		e.AlternateTitles = append(e.AlternateTitles, a.Title)
	}
	if len(match.NewIndex([]match.Entry{e}).Match(*p)) == 0 {
		return fmt.Sprintf("Wrong movie: the release is %s, not %s", describe(p.Title, p.Year), describe(m.Title, int(m.Year)))
	}
	if s.matcher != nil {
		if c := s.matcher.MatchRelease(*p); len(c) > 0 && c[0].MovieID != m.ID && c[0].Confident() {
			return fmt.Sprintf("Wrong movie: the release looks like %s, another movie of the library, not %s", describe(c[0].Title, c[0].Year), describe(m.Title, int(m.Year)))
		}
	}
	return ""
}

func describe(title string, year int) string {
	if year > 0 {
		return fmt.Sprintf("%q (%d)", title, year)
	}
	return strconv.Quote(title)
}

// recordRejection writes the refusal of an automatic import to history.
func (s *Service) recordRejection(ctx context.Context, t *target, req *Request, r *Result) {
	data := map[string]any{"reason": r.Reason, "path": req.LocalPath}
	if len(r.Rejections) > 0 {
		data["rejections"] = r.Rejections
	}
	if err := s.appendHistory(ctx, s.st.Write(), &historyRow{
		movieID: t.movie.ID, event: historyImportFailed, title: req.ReleaseTitle, downloadID: req.DownloadID, data: data,
	}); err != nil {
		s.log.Error("history: import refused", slog.Int64("movie_id", t.movie.ID), slog.Any("error", err))
	}
	s.log.Info("import refused", slog.Int64("movie_id", t.movie.ID), slog.Int64("download_id", req.DownloadID), slog.String("reason", r.Reason))
	s.publish(events.Event{Type: EventImportRejected, MovieID: t.movie.ID, Data: map[string]any{
		"downloadId": req.DownloadID, "reason": r.Reason, "rejections": r.Rejections,
	}})
}

type historyRow struct {
	movieID    int64
	event      string
	title      string
	quality    string
	score      *int64
	downloadID int64
	data       map[string]any
}

func (s *Service) appendHistory(ctx context.Context, q *db.Queries, h *historyRow) error {
	raw, err := json.Marshal(h.data)
	if err != nil {
		return fmt.Errorf("encode history data: %w", err)
	}
	p := db.AppendHistoryParams{
		Event: h.event, SourceTitle: h.title, CustomScore: h.score, Data: string(raw), CreatedAt: store.Millis(s.now()),
	}
	if h.movieID != 0 {
		p.MovieID = &h.movieID
	}
	if h.quality != "" {
		p.Quality = &h.quality
	}
	if h.downloadID > 0 {
		p.DownloadID = &h.downloadID
	}
	if _, err := q.AppendHistory(ctx, p); err != nil {
		return store.MapError(fmt.Errorf("append history: %w", err))
	}
	return nil
}

// fileTemplate returns the configured file template and whether files are
// renamed.
func (s *Service) fileTemplate(ctx context.Context) (*naming.Template, bool) {
	rename := true
	if v, err := store.GetSetting[bool](ctx, s.st, naming.SettingRenameFiles); err == nil {
		rename = v
	}
	tmpl, err := store.GetSetting[string](ctx, s.st, naming.SettingFileTemplate)
	if err == nil {
		if t, perr := naming.Parse(tmpl); perr == nil {
			return t, rename
		}
		s.log.Warn("file name template invalid, using the default")
	}
	t, _ := naming.Parse(naming.DefaultFileTemplate) // valid by construction (tested)
	return t, rename
}

// varsOf returns the naming values of a planned file.
func varsOf(t *target, p *parser.ParsedRelease, q quality.ID, score quality.Breakdown) naming.Vars {
	v := naming.Vars{
		Title: t.movie.Title, OriginalTitle: t.movie.OriginalTitle, Year: int(t.movie.Year), TmdbID: t.movie.TmdbID,
		Quality: q, Release: *p,
	}
	if t.movie.ImdbID != nil {
		v.ImdbID = *t.movie.ImdbID
	}
	for _, r := range score.Rules {
		if t.rename[r.Rule] {
			v.CustomFormats = append(v.CustomFormats, r.Rule)
		}
	}
	return v
}

// freeName returns name, or "stem (n).ext" when name is taken in dir.
func (s *Service) freeName(dir, name string) (string, error) {
	ext := filepath.Ext(name)
	stem := strings.TrimSuffix(name, ext)
	for n := 1; n < 100; n++ {
		cand := name
		if n > 1 {
			cand = stem + " (" + strconv.Itoa(n) + ")" + ext
		}
		_, err := s.fs.Lstat(filepath.Join(dir, cand))
		if errors.Is(err, fs.ErrNotExist) {
			return cand, nil
		}
		if err != nil {
			return "", err
		}
	}
	return "", fmt.Errorf("no free file name for %s in %s", name, dir)
}

func sameFile(a, b string) bool {
	if filepath.Clean(a) == filepath.Clean(b) {
		return true
	}
	fa, err1 := os.Lstat(a)
	fb, err2 := os.Lstat(b)
	return err1 == nil && err2 == nil && os.SameFile(fa, fb)
}

func randomHex() string {
	var b [6]byte
	_, _ = rand.Read(b[:])
	return hex.EncodeToString(b[:])
}

func methodText(m fsx.Method) string {
	switch m {
	case fsx.MethodHardlink:
		return "hard link"
	case fsx.MethodCopy:
		return "copy"
	case fsx.MethodRename:
		return "move"
	case fsx.MethodCopyRemove:
		return "move across file systems"
	}
	return string(m)
}

func fsErr(err error, format string, args ...any) error {
	msg := fmt.Sprintf(format, args...)
	switch {
	case errors.Is(err, fsx.ErrNoSpace):
		msg += ": not enough free space"
	case errors.Is(err, fs.ErrPermission):
		msg += ": permission denied (check the user and group Gonema runs as)"
	case errors.Is(err, fs.ErrNotExist):
		msg += ": a file or folder is missing"
	case errors.Is(err, fs.ErrExist):
		msg += ": a file already exists there"
	case errors.Is(err, context.Canceled), errors.Is(err, context.DeadlineExceeded):
		msg += ": canceled"
	}
	return userErr(ErrFileSystem, err, "%s", msg)
}

// execute places the file of p in the movie folder, recycles the file it
// replaces, records it and writes history. On failure everything done is undone
// and the source is as before.
func (s *Service) execute(ctx context.Context, set *Settings, p *plan) (Result, error) {
	t := p.t
	movieDir, err := s.fs.SafeJoin(t.root.Path, t.movie.Path)
	if err != nil {
		return Result{}, fsErr(err, "the folder of %s is not usable inside the root folder %s", t.movie.Title, t.root.Path)
	}
	if err := s.fs.MkdirAll(movieDir); err != nil {
		return Result{}, fsErr(err, "cannot create the movie folder %s", movieDir)
	}
	ext := naming.CleanExt(filepath.Ext(p.src))
	tmpl, rename := s.fileTemplate(ctx)
	name := ""
	if rename {
		v := varsOf(t, &p.parsed, p.quality, p.score)
		name = tmpl.FileName(&v, ext)
	} else {
		name = library.SanitizeName(strings.TrimSuffix(filepath.Base(p.src), filepath.Ext(p.src))) + ext
	}

	var curPath string
	if t.current != nil {
		if cp, err := s.fs.SafeJoin(movieDir, t.current.RelativePath); err == nil {
			if _, err := s.fs.Lstat(cp); err == nil {
				curPath = cp
			}
		}
	}
	dst := filepath.Join(movieDir, name)
	var place string
	staging := false
	switch {
	case curPath != "" && sameFile(dst, curPath):
		// Same name as the file it replaces: stage next to it, recycle the old one,
		// then rename into place.
		place = filepath.Join(movieDir, ".gonema-import-"+randomHex()+ext)
		staging = true
	default:
		free, err := s.freeName(movieDir, name)
		if err != nil {
			return Result{}, fsErr(err, "cannot name the file in %s", movieDir)
		}
		name, dst, place = free, filepath.Join(movieDir, free), filepath.Join(movieDir, free)
	}

	out, err := s.fs.Transfer(ctx, p.src, place, p.mode)
	if err != nil {
		return Result{}, fsErr(err, "cannot %s %s into %s", modeVerb(p.mode), filepath.Base(p.src), movieDir)
	}
	// Undo uses a detached context: it must run even when ctx is canceled.
	undoCtx := context.WithoutCancel(ctx)
	undoTransfer := func(at string) {
		if err := s.fs.Revert(undoCtx, out, p.src, at); err != nil {
			s.log.Error("import rollback: the new file could not be undone", slog.String("path", at), slog.Any("error", err))
		}
	}

	var notes []string
	if out.Note != "" {
		notes = append(notes, out.Note)
	}
	var recycled *fsx.RecycledItem
	bin := binDir(set, t.root.Path)
	if t.current != nil {
		if curPath == "" {
			notes = append(notes, "the previous file ("+t.current.RelativePath+") was already missing from disk")
		} else {
			it, err := s.fs.Recycle(ctx, curPath, bin, fsx.Manifest{
				MovieID: t.movie.ID, Reason: fmt.Sprintf("replaced by %s (%s)", p.quality.Name(), p.scene),
			})
			if err != nil {
				undoTransfer(place)
				return Result{}, fsErr(err, "cannot move the previous file %s to the recycle bin %s", filepath.Base(curPath), bin)
			}
			recycled = &it
		}
	}
	restoreOld := func() {
		if recycled == nil {
			return
		}
		if _, err := s.fs.RestoreRecycled(undoCtx, bin, recycled.Name, nil); err != nil {
			s.log.Error("import rollback: the previous file stays in the recycle bin", slog.String("item", recycled.Name), slog.Any("error", err))
		}
	}
	if staging {
		if _, err := s.fs.Move(ctx, place, dst); err != nil {
			undoTransfer(place)
			restoreOld()
			return Result{}, fsErr(err, "cannot rename the new file into place in %s", movieDir)
		}
	}

	// Record.
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
	upgraded := t.current != nil
	reason := fmt.Sprintf("Imported %s as %s (%s)", qualityLabel(p.quality, p.parsed.Revision), name, methodText(out.Method))
	event := historyImported
	if upgraded {
		event = historyUpgraded
		reason = fmt.Sprintf("Upgraded from %s to %s (%s), as %s (%s)", qualityLabel(qualityID(t.current.Quality), fileQuality(t.current).Revision),
			qualityLabel(p.quality, p.parsed.Revision), upgradeText(p.upgrade), name, methodText(out.Method))
		if recycled != nil {
			reason += "; the previous file is in the recycle bin"
		}
	}
	data := map[string]any{
		"reason": reason, "method": out.Method, "path": name, "size": p.size, "score": p.score,
		"source": p.source, "sourcePath": p.src,
	}
	if out.Note != "" {
		data["note"] = out.Note
	}
	if upgraded {
		data["upgradeReason"] = p.upgrade
		data["previous"] = map[string]any{
			"path": t.current.RelativePath, "quality": t.current.Quality, "score": t.current.CustomScore,
			"sceneName": t.current.SceneName,
		}
	}
	if recycled != nil {
		data["recycled"] = map[string]any{"bin": bin, "name": recycled.Name}
	}
	score := int64(p.score.Total)
	var row db.MovieFile
	err = s.st.WithTx(ctx, func(q *db.Queries) error {
		var err error
		row, err = q.UpsertMovieFile(ctx, db.UpsertMovieFileParams{
			MovieID: t.movie.ID, RelativePath: name, Size: p.size, Quality: string(p.quality),
			Revision: int64(max(p.parsed.Revision.Version, 1)), Edition: p.parsed.Edition.String(),
			Languages: string(langJSON), ReleaseGroup: p.parsed.ReleaseGroup, SceneName: p.scene,
			Parsed: string(parsedJSON), MediaInfo: string(media), CustomScore: score,
			ImportedAt: store.Millis(s.now()),
		})
		if err != nil {
			return fmt.Errorf("record movie file: %w", err)
		}
		return s.appendHistory(ctx, q, &historyRow{
			movieID: t.movie.ID, event: event, title: p.scene, quality: string(p.quality), score: &score,
			downloadID: p.downloadID, data: data,
		})
	})
	if err != nil {
		undoTransfer(dst)
		restoreOld()
		return Result{}, fmt.Errorf("importer: record the import of %s: %w", t.movie.Title, err)
	}

	result := Result{Imported: true, Upgraded: upgraded, FileID: row.ID, Path: name, Reason: reason}
	// Companion files: best effort, the movie is imported already.
	if upgraded && t.current != nil {
		notes = append(notes, s.recycleOldSubtitles(ctx, movieDir, t.current.RelativePath, bin, t.movie.ID)...)
	}
	if len(p.subs) > 0 {
		subMode := p.mode
		done, subNotes := s.importSubtitles(ctx, p.subs, movieDir, strings.TrimSuffix(name, ext), subMode)
		result.Subtitles = done
		notes = append(notes, subNotes...)
	}
	result.Notes = notes

	s.log.Info("movie file imported", slog.Int64("movie_id", t.movie.ID), slog.String("title", t.movie.Title),
		slog.String("quality", string(p.quality)), slog.Bool("upgraded", upgraded), slog.String("method", string(out.Method)),
		slog.String("path", filepath.Join(movieDir, name)))
	s.publish(events.Event{Type: EventFileImported, MovieID: t.movie.ID, Data: map[string]any{
		"fileId": row.ID, "path": name, "quality": p.quality, "upgraded": upgraded, "reason": reason,
		"downloadId": p.downloadID,
	}})
	return result, nil
}

// recycleOldSubtitles moves the subtitles of the replaced file ("<old stem>.*.srt")
// to the recycle bin.
func (s *Service) recycleOldSubtitles(ctx context.Context, movieDir, oldRel, bin string, movieID int64) []string {
	oldStem := strings.TrimSuffix(filepath.Base(oldRel), filepath.Ext(oldRel))
	entries, err := s.fs.ReadDir(filepath.Join(movieDir, filepath.Dir(oldRel)))
	if err != nil {
		return nil
	}
	var notes []string
	for _, e := range entries {
		n := e.Name()
		if !e.Type().IsRegular() || !subtitleExts[strings.ToLower(filepath.Ext(n))] || !strings.HasPrefix(n, oldStem+".") {
			continue
		}
		p := filepath.Join(movieDir, filepath.Dir(oldRel), n)
		if _, err := s.fs.Recycle(ctx, p, bin, fsx.Manifest{MovieID: movieID, Reason: "subtitle of the replaced file"}); err != nil {
			notes = append(notes, n+": old subtitle kept ("+err.Error()+")")
		}
	}
	return notes
}

func modeVerb(m fsx.Mode) string {
	switch m {
	case fsx.ModeHardlink:
		return "hard link"
	case fsx.ModeCopy:
		return "copy"
	}
	return "move"
}

func upgradeText(r decision.UpgradeReason) string {
	switch r {
	case decision.UpgradeQuality:
		return "better quality"
	case decision.UpgradeRevision:
		return "PROPER/REPACK of the same release"
	case decision.UpgradeScore:
		return "higher custom score"
	case decision.UpgradeLanguage:
		return "wanted language"
	case "manual":
		return "replaced on request"
	}
	return string(r)
}

// mediaInfo is what the release name says about the streams (no probe yet).
func mediaInfo(p *parser.ParsedRelease) map[string]any {
	m := map[string]any{"source": "release name"}
	if p.VideoCodec != parser.CodecUnknown {
		m["videoCodec"] = p.VideoCodec
	}
	if p.BitDepth > 0 {
		m["bitDepth"] = p.BitDepth
	}
	if len(p.HDR) > 0 {
		m["hdr"] = p.HDR
	}
	if len(p.Audio) > 0 {
		m["audio"] = p.Audio
	}
	if p.Atmos {
		m["atmos"] = true
	}
	if p.Channels != "" {
		m["channels"] = p.Channels
	}
	if p.Resolution != parser.ResolutionUnknown {
		m["resolution"] = p.Resolution
	}
	return m
}
