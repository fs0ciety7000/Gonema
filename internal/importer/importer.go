// Package importer brings downloaded and existing files into the library:
// automatic import of completed downloads, manual import of a folder chosen by the
// user, and adoption of an existing library folder.
//
// Every file operation goes through internal/fsx: nothing is overwritten, a
// replaced file goes to the recycle bin (never deleted), and a failed import
// leaves the source as it was. Every import writes its reasons to history.
package importer

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"time"

	"github.com/fs0ciety7000/gonema/internal/decision"
	"github.com/fs0ciety7000/gonema/internal/events"
	"github.com/fs0ciety7000/gonema/internal/fsx"
	"github.com/fs0ciety7000/gonema/internal/library"
	"github.com/fs0ciety7000/gonema/internal/match"
	"github.com/fs0ciety7000/gonema/internal/parser"
	"github.com/fs0ciety7000/gonema/internal/store"
	"github.com/fs0ciety7000/gonema/internal/store/db"
)

// Event types published by the importer (SSE "event:" names).
const (
	EventFileImported   events.Type = "moviefile.imported"
	EventImportRejected events.Type = "import.rejected"
	EventRecycleChanged events.Type = "recycle.changed"
)

// History events written by the importer (history.event).
const (
	historyImported     = "imported"
	historyUpgraded     = "upgraded"
	historyImportFailed = "import_failed"
	historyFileDeleted  = "file_deleted"
)

// SettingImport is the settings key of Settings (a JSON object).
const SettingImport = "import.settings"

// RecycleDirName is the recycle bin folder created in each root folder when no
// single bin is configured. It is hidden, and skipped by library scans.
const RecycleDirName = ".gonema-recycle"

// DefaultRecycleRetentionDays is how long the bin keeps files before a purge
// (requested by the user) may delete them.
const DefaultRecycleRetentionDays = 30

// Errors, matched with errors.Is.
var (
	// ErrInvalid: a request was rejected; the message says why.
	ErrInvalid = errors.New("importer: invalid request")
	// ErrNotFound: a movie, root folder, download or bin item does not exist. Also
	// matches store.ErrNotFound.
	ErrNotFound = errors.New("importer: not found")
	// ErrOutsideRoots: a path is outside the folders Gonema may import from.
	ErrOutsideRoots = errors.New("importer: path outside the allowed folders")
	// ErrConfirmRequired: a purge was requested without confirm=true.
	ErrConfirmRequired = errors.New("importer: confirmation required")
	// ErrNoProvider: the existing library import needs the metadata provider.
	ErrNoProvider = library.ErrNoProvider
)

// Error is an error whose message is a plain-language sentence for the user.
type Error struct {
	Kind error
	Msg  string
	Err  error // cause, optional
}

func (e *Error) Error() string {
	if e.Err != nil {
		return e.Msg + ": " + e.Err.Error()
	}
	return e.Msg
}

// UserMessage returns the plain-language message.
func (e *Error) UserMessage() string { return e.Msg }

// Is matches the kind (and store.ErrNotFound for ErrNotFound).
func (e *Error) Is(target error) bool {
	return target == e.Kind || (e.Kind == ErrNotFound && target == store.ErrNotFound)
}

// Unwrap returns the cause.
func (e *Error) Unwrap() error { return e.Err }

func userErr(kind, cause error, format string, args ...any) *Error {
	return &Error{Kind: kind, Msg: fmt.Sprintf(format, args...), Err: cause}
}

// Settings configure imports (settings key "import.settings").
type Settings struct {
	// Mode transfers Usenet downloads and torrents the client no longer seeds:
	// "move" (default) or "copy".
	Mode fsx.Mode `json:"mode"`
	// TorrentMode transfers torrents still seeding: "hardlink" (default; falls
	// back to a copy across file systems) or "copy". Never "move".
	TorrentMode fsx.Mode `json:"torrentMode"`
	// AllowedRoots are the folders imports may read from (download folders as
	// seen by Gonema). The local paths of remote path mappings are allowed too.
	AllowedRoots []string `json:"allowedRoots"`
	// RecycleBin is a single bin folder for every root folder; empty means a
	// ".gonema-recycle" folder in each root folder (moves stay on one file system).
	RecycleBin string `json:"recycleBin"`
	// RecycleRetentionDays is the default age of the files a purge deletes.
	RecycleRetentionDays int `json:"recycleRetentionDays"`
	// ImportSubtitles carries subtitle files (.srt, .ass...) along.
	ImportSubtitles bool `json:"importSubtitles"`
}

// DefaultSettings returns the settings used when none are stored.
func DefaultSettings() Settings {
	return Settings{
		Mode: fsx.ModeMove, TorrentMode: fsx.ModeHardlink, AllowedRoots: []string{},
		RecycleRetentionDays: DefaultRecycleRetentionDays, ImportSubtitles: true,
	}
}

// Validate checks and normalizes s.
func (s *Settings) Validate() error {
	if s.Mode != fsx.ModeMove && s.Mode != fsx.ModeCopy {
		return userErr(ErrInvalid, nil, "the import mode must be move or copy")
	}
	if s.TorrentMode != fsx.ModeHardlink && s.TorrentMode != fsx.ModeCopy {
		return userErr(ErrInvalid, nil, "the mode for seeding torrents must be hardlink or copy (moving would stop the seeding)")
	}
	if s.RecycleRetentionDays < 1 || s.RecycleRetentionDays > 3650 {
		return userErr(ErrInvalid, nil, "the recycle bin retention must be between 1 and 3650 days")
	}
	roots := make([]string, 0, len(s.AllowedRoots))
	for _, r := range s.AllowedRoots {
		r = strings.TrimSpace(r)
		if r == "" || strings.ContainsRune(r, 0) || !filepath.IsAbs(r) {
			return userErr(ErrInvalid, nil, "allowed import folders must be absolute paths, e.g. /downloads; %q is not", r)
		}
		r = filepath.Clean(r)
		if !slices.Contains(roots, r) {
			roots = append(roots, r)
		}
	}
	s.AllowedRoots = roots
	s.RecycleBin = strings.TrimSpace(s.RecycleBin)
	if s.RecycleBin != "" {
		if strings.ContainsRune(s.RecycleBin, 0) || !filepath.IsAbs(s.RecycleBin) {
			return userErr(ErrInvalid, nil, "the recycle bin must be an absolute path, or empty for one bin per root folder")
		}
		s.RecycleBin = filepath.Clean(s.RecycleBin)
	}
	return nil
}

// Matcher finds the library movies a release name is about (library.Service).
type Matcher interface {
	MatchRelease(p parser.ParsedRelease) []match.Candidate
}

// Library is the part of the library service the existing library import uses.
type Library interface {
	Lookup(ctx context.Context, query string, year int) ([]library.LookupResult, error)
	AddMovie(ctx context.Context, tmdbID int, opts library.AddOptions) (db.Movie, bool, error)
}

// Options configures a Service. Store is required.
type Options struct {
	Store   *store.Store
	FS      *fsx.Ops     // default fsx.New(fsx.OS{})
	Matcher Matcher      // optional: detects a download of another library movie
	Library Library      // optional: required by ScanLibrary and ConfirmLibrary
	Bus     *events.Bus  // optional
	Logger  *slog.Logger // default discards
	Now     func() time.Time
	// ExtraRoots, optional, returns more folders imports may read from (e.g. the
	// download folders reported by the download clients).
	ExtraRoots func(ctx context.Context) []string
	// DataDir, optional, is the data directory: the recycle bin may not be in
	// it (nor contain it).
	DataDir string
	// Fault, for tests only, is called between the steps of an import (see
	// the Fault* constants); an error stops the import there as a crash
	// would: nothing is undone and the journal entry stays pending.
	Fault func(step string) error
}

// Service imports files. It is safe for concurrent use; imports of one movie
// are serialized.
type Service struct {
	st         *store.Store
	fs         *fsx.Ops
	matcher    Matcher
	lib        Library
	bus        *events.Bus
	log        *slog.Logger
	now        func() time.Time
	extraRoots func(ctx context.Context) []string
	fault      func(step string) error
	dataDir    string

	locks sync.Map // movie id -> *sync.Mutex
}

// New returns a service.
func New(opts Options) (*Service, error) {
	if opts.Store == nil {
		return nil, errors.New("importer: store is required")
	}
	s := &Service{
		st: opts.Store, fs: opts.FS, matcher: opts.Matcher, lib: opts.Library, bus: opts.Bus,
		log: opts.Logger, now: opts.Now, extraRoots: opts.ExtraRoots, fault: opts.Fault, dataDir: opts.DataDir,
	}
	if s.now == nil {
		s.now = time.Now
	}
	if s.fs == nil {
		s.fs = fsx.New(fsx.OS{}, fsx.Options{Now: s.now})
	}
	if s.log == nil {
		s.log = slog.New(slog.DiscardHandler)
	}
	return s, nil
}

func (s *Service) publish(e events.Event) {
	if s.bus != nil {
		s.bus.Publish(e)
	}
}

// lockMovie serializes the imports of one movie.
func (s *Service) lockMovie(id int64) func() {
	v, _ := s.locks.LoadOrStore(id, &sync.Mutex{})
	mu, _ := v.(*sync.Mutex)
	mu.Lock()
	return mu.Unlock
}

// Settings returns the import settings (defaults for unset fields).
func (s *Service) Settings(ctx context.Context) (Settings, error) {
	set := DefaultSettings()
	raw, err := s.st.Read().GetSetting(ctx, SettingImport)
	switch {
	case errors.Is(store.MapError(err), store.ErrNotFound):
		return set, nil
	case err != nil:
		return set, store.MapError(fmt.Errorf("importer: read settings: %w", err))
	}
	if err := json.Unmarshal([]byte(raw), &set); err != nil {
		s.log.Warn("import settings unreadable, using the defaults", slog.Any("error", err))
		return DefaultSettings(), nil
	}
	if err := set.Validate(); err != nil {
		s.log.Warn("stored import settings invalid, using the defaults", slog.Any("error", err))
		return DefaultSettings(), nil
	}
	return set, nil
}

// SetSettings validates and stores the import settings. Allowed folders must
// exist.
func (s *Service) SetSettings(ctx context.Context, set Settings) (Settings, error) {
	if err := set.Validate(); err != nil {
		return Settings{}, err
	}
	for _, r := range set.AllowedRoots {
		fi, err := s.fs.Stat(r)
		if err != nil || !fi.IsDir() {
			return Settings{}, userErr(ErrInvalid, nil, "the import folder %s does not exist or is not a folder (check the volume mapping in Docker)", r)
		}
	}
	if set.RecycleBin != "" {
		if err := s.checkRecycleBin(ctx, set.RecycleBin); err != nil {
			return Settings{}, err
		}
	}
	if err := store.SetSetting(ctx, s.st, SettingImport, set); err != nil {
		return Settings{}, fmt.Errorf("importer: save settings: %w", err)
	}
	s.publish(events.Event{Type: events.SettingsChanged, Data: map[string]any{"key": SettingImport}})
	return set, nil
}

// checkRecycleBin refuses a single recycle bin that could mix Gonema's
// recycled files with other files: /, the data directory (or a folder holding
// it), a root folder, a folder inside one (movie folders included) or
// holding one, or an existing folder that holds anything but recycled files.
// Only <root folder>/.gonema-recycle, the default bin of that root folder, is
// accepted inside a root folder. Every refusal gives the same message: the
// setting must not tell what exists on the server.
func (s *Service) checkRecycleBin(ctx context.Context, bin string) error {
	refuse := userErr(ErrInvalid, nil, "%s cannot be the recycle bin: choose a folder used only for it (empty, or holding only files "+
		"Gonema recycled), outside the data folder and the root folders, and not /", bin)
	paths := []string{filepath.Clean(bin)}
	if r, err := s.fs.EvalSymlinks(bin); err == nil && r != paths[0] {
		paths = append(paths, r)
	}
	rfs, err := s.st.Read().ListRootFolders(ctx)
	if err != nil {
		return store.MapError(fmt.Errorf("importer: list root folders: %w", err))
	}
	overlaps := func(p, dir string) bool { return fsx.Within(dir, p) || fsx.Within(p, dir) }
	for _, p := range paths {
		if filepath.Dir(p) == p { // "/" or a volume root
			return refuse
		}
		if s.dataDir != "" && overlaps(p, filepath.Clean(s.dataDir)) {
			return refuse
		}
		for _, rf := range rfs {
			if p == filepath.Join(rf.Path, RecycleDirName) {
				continue
			}
			if overlaps(p, rf.Path) {
				return refuse
			}
		}
	}
	if n, err := s.fs.ForeignEntries(bin); err != nil || n > 0 {
		return refuse
	}
	return nil
}

// allowedRoots returns the folders imports may read from.
func (s *Service) allowedRoots(ctx context.Context, set *Settings) ([]string, error) {
	roots := slices.Clone(set.AllowedRoots)
	mappings, err := s.st.Read().ListRemotePathMappings(ctx)
	if err != nil {
		return nil, store.MapError(fmt.Errorf("importer: list remote path mappings: %w", err))
	}
	for _, m := range mappings {
		if p := filepath.Clean(m.LocalPath); filepath.IsAbs(p) {
			roots = append(roots, p)
		}
	}
	if s.extraRoots != nil {
		for _, r := range s.extraRoots(ctx) {
			if filepath.IsAbs(r) {
				roots = append(roots, filepath.Clean(r))
			}
		}
	}
	return roots, nil
}

// checkSource resolves p and checks it is inside an allowed folder and outside
// every root folder of the library (importing from the library into itself
// would move files around).
func (s *Service) checkSource(ctx context.Context, set *Settings, p string) (string, error) {
	p = strings.TrimSpace(p)
	if p == "" || strings.ContainsRune(p, 0) || !filepath.IsAbs(p) {
		return "", userErr(ErrInvalid, nil, "the path to import must be absolute, as seen by Gonema (after path mapping)")
	}
	roots, err := s.allowedRoots(ctx, set)
	if err != nil {
		return "", err
	}
	resolved, _, err := s.fs.Contains(roots, p)
	switch {
	case errors.Is(err, fsx.ErrOutsideRoot):
		return "", userErr(ErrOutsideRoots, nil, "%s is outside the folders Gonema may import from; add its download folder in Settings > Import (allowed folders) or a remote path mapping", p)
	case err != nil:
		return "", userErr(ErrNotFound, err, "%s cannot be read (missing, or no permission)", p)
	}
	rfs, err := s.st.Read().ListRootFolders(ctx)
	if err != nil {
		return "", store.MapError(fmt.Errorf("importer: list root folders: %w", err))
	}
	for _, rf := range rfs {
		if _, _, err := s.fs.Contains([]string{rf.Path}, resolved); err == nil {
			return "", userErr(ErrInvalid, nil, "%s is inside the root folder %s: files already in the library are adopted with the library import, not imported", p, rf.Path)
		}
	}
	return resolved, nil
}

// binDir returns the recycle bin of a root folder.
func binDir(set *Settings, rootPath string) string {
	if set.RecycleBin != "" {
		return set.RecycleBin
	}
	return filepath.Join(rootPath, RecycleDirName)
}

// fileQuality is the upgrade view of a stored file.
func fileQuality(f *db.MovieFile) decision.FileQuality {
	fq := decision.FileQuality{Quality: qualityID(f.Quality), CustomScore: int(f.CustomScore)}
	fq.Revision.Version = int(max(f.Revision, 1))
	var p struct {
		Revision parser.Revision `json:"revision"`
	}
	if json.Unmarshal([]byte(f.Parsed), &p) == nil && p.Revision.Version == fq.Revision.Version {
		fq.Revision.Real = p.Revision.Real
	}
	return fq
}
