// Package backup creates, lists, validates and restores the backups of the
// Gonema database.
//
// A backup is a zip archive in <data dir>/backups holding:
//
//   - gonema.db: a consistent online copy made with VACUUM INTO (writers are
//     not blocked);
//   - manifest.json: format, Gonema version, schema version, date, kind, the
//     SHA-256 of the database and a fingerprint of the master key;
//   - the master key file (gonema.key), ONLY when explicitly asked: the
//     database holds secrets (indexer API keys, client passwords, the TMDB key)
//     encrypted with that key, so an archive holding both unlocks them all.
//     By default the key is left out and must be backed up separately.
//
// A restore is staged: the archive is validated (manifest, checksum, SQLite
// integrity, schema version not newer than this binary), the current state is
// backed up first (kind pre-restore), and the database is extracted to
// <data dir>/restore. ApplyPending swaps it in at the next start, before the
// database is opened; the replaced files are kept in <data dir>/restore/replaced.
package backup

import (
	"archive/zip"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"log/slog"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/fs0ciety7000/gonema/internal/secretbox"
	"github.com/fs0ciety7000/gonema/internal/store"
)

// File names in the data directory and in archives.
const (
	DBFile       = "gonema.db"
	KeyFile      = "gonema.key"
	ManifestFile = "manifest.json"
	DirName      = "backups"
	RestoreDir   = "restore"
)

// FormatVersion is the archive format written by this version.
const FormatVersion = 1

// Kind says why a backup was made.
type Kind string

// Kinds.
const (
	KindManual     Kind = "manual"
	KindScheduled  Kind = "scheduled"
	KindPreRestore Kind = "pre-restore"
	KindUploaded   Kind = "uploaded"
)

func (k Kind) valid() bool {
	return k == KindManual || k == KindScheduled || k == KindPreRestore || k == KindUploaded
}

// Errors, matched with errors.Is. Messages of *Error are plain sentences.
var (
	ErrInvalid         = errors.New("backup: invalid request")
	ErrNotFound        = errors.New("backup: not found")
	ErrConfirmRequired = errors.New("backup: confirmation required")
	ErrBusy            = errors.New("backup: another backup or restore is running")
)

// Error carries a plain-language message.
type Error struct {
	Kind error
	Msg  string
	Err  error
}

func (e *Error) Error() string {
	if e.Err != nil {
		return e.Msg + ": " + e.Err.Error()
	}
	return e.Msg
}

// UserMessage returns the message for the user.
func (e *Error) UserMessage() string { return e.Msg }

// Is matches the kind.
func (e *Error) Is(target error) bool { return target == e.Kind }

// Unwrap returns the cause.
func (e *Error) Unwrap() error { return e.Err }

func userErr(kind, cause error, format string, args ...any) *Error {
	return &Error{Kind: kind, Msg: fmt.Sprintf(format, args...), Err: cause}
}

// Manifest describes an archive.
type Manifest struct {
	Format        int       `json:"format"`
	App           string    `json:"app"`
	AppVersion    string    `json:"appVersion"`
	SchemaVersion int64     `json:"schemaVersion"`
	CreatedAt     time.Time `json:"createdAt"`
	Kind          Kind      `json:"kind"`
	IncludesKey   bool      `json:"includesKey"`
	// KeyFingerprint identifies the master key the secrets were encrypted
	// with (first 8 bytes of its SHA-256, hex): it tells whether a restore
	// needs another key, without revealing the key.
	KeyFingerprint string `json:"keyFingerprint,omitempty"`
	DBSize         int64  `json:"dbSize"`
	DBSHA256       string `json:"dbSha256"`
}

// Info describes a backup archive.
type Info struct {
	Name          string    `json:"name"`
	Size          int64     `json:"size"`
	CreatedAt     time.Time `json:"createdAt"`
	Kind          Kind      `json:"kind" enum:"manual,scheduled,pre-restore,uploaded"`
	AppVersion    string    `json:"appVersion"`
	SchemaVersion int64     `json:"schemaVersion"`
	IncludesKey   bool      `json:"includesKey" doc:"The archive holds gonema.key: whoever has the file can decrypt the stored secrets"`
	Restorable    bool      `json:"restorable"`
	Problem       string    `json:"problem,omitempty" doc:"Why it cannot be restored (restorable false)"`
}

// namePattern is the only accepted shape of an archive name: it cannot hold a
// path separator or "..", so a name can never leave the backups folder.
var namePattern = regexp.MustCompile(`^gonema-backup-[0-9]{8}-[0-9]{6}(-[0-9]{1,3})?-(manual|scheduled|pre-restore|uploaded)\.zip$`)

// ValidName reports whether name is an archive name Gonema creates.
func ValidName(name string) bool { return len(name) <= 80 && namePattern.MatchString(name) }

// Fingerprint returns the fingerprint of a master key.
func Fingerprint(k secretbox.Key) string {
	sum := sha256.Sum256(k[:])
	return hex.EncodeToString(sum[:8])
}

// Source is the database to back up (store.Store).
type Source interface {
	BackupTo(ctx context.Context, dst string) error
	SchemaVersion(ctx context.Context) (int64, error)
}

// Options configures a Service.
type Options struct {
	DataDir string // required
	Store   Source // required to create backups
	Version string // Gonema version written to manifests
	// Key is the master key in use (for its fingerprint).
	Key secretbox.Key
	// KeyFromEnv is true when the key comes from GONEMA_MASTER_KEY: there is
	// no key file to include or replace.
	KeyFromEnv bool
	Logger     *slog.Logger
	Now        func() time.Time
	// MaxDBSize bounds the database extracted from an archive; default 64 GiB.
	MaxDBSize int64
}

// Service manages the backups of one data directory. It is safe for
// concurrent use; backups and restores run one at a time.
type Service struct {
	dataDir    string
	src        Source
	version    string
	keyFP      string
	keyFromEnv bool
	log        *slog.Logger
	now        func() time.Time
	maxDB      int64

	mu sync.Mutex // one create, import, delete or restore at a time

	lastFailure atomic.Pointer[failure] // last scheduled backup, when it failed
}

// New returns a service.
func New(opts Options) (*Service, error) {
	if opts.DataDir == "" {
		return nil, errors.New("backup: data directory is required")
	}
	s := &Service{
		dataDir: opts.DataDir, src: opts.Store, version: opts.Version, keyFromEnv: opts.KeyFromEnv,
		log: opts.Logger, now: opts.Now, maxDB: opts.MaxDBSize,
	}
	if opts.Key != (secretbox.Key{}) {
		s.keyFP = Fingerprint(opts.Key)
	}
	if s.log == nil {
		s.log = slog.New(slog.DiscardHandler)
	}
	if s.now == nil {
		s.now = time.Now
	}
	if s.maxDB <= 0 {
		s.maxDB = 64 << 30
	}
	return s, nil
}

// Dir returns the backups folder.
func (s *Service) Dir() string { return filepath.Join(s.dataDir, DirName) }

func (s *Service) lock() (func(), error) {
	if !s.mu.TryLock() {
		return nil, userErr(ErrBusy, nil, "Another backup or restore is running; try again in a moment.")
	}
	return s.mu.Unlock, nil
}

// Create makes a backup now. includeKey adds the master key file to the
// archive: the archive then unlocks every stored secret (say so to the user).
func (s *Service) Create(ctx context.Context, kind Kind, includeKey bool) (Info, error) {
	unlock, err := s.lock()
	if err != nil {
		return Info{}, err
	}
	defer unlock()
	return s.create(ctx, kind, includeKey)
}

func (s *Service) create(ctx context.Context, kind Kind, includeKey bool) (Info, error) {
	if s.src == nil {
		return Info{}, errors.New("backup: no database to back up")
	}
	if !kind.valid() {
		return Info{}, fmt.Errorf("backup: invalid kind %q", kind)
	}
	var key []byte
	if includeKey {
		if s.keyFromEnv {
			return Info{}, userErr(ErrInvalid, nil, "The master key comes from the %s variable, not from %s: back it up where you keep that variable.", secretbox.EnvMasterKey, KeyFile)
		}
		b, err := os.ReadFile(filepath.Join(s.dataDir, KeyFile))
		if err != nil {
			return Info{}, fmt.Errorf("backup: read the master key file: %w", err)
		}
		key = b
	}
	if err := os.MkdirAll(s.Dir(), 0o750); err != nil {
		return Info{}, fmt.Errorf("backup: create the backups folder: %w", err)
	}
	tmpDir, err := os.MkdirTemp(s.Dir(), ".tmp-")
	if err != nil {
		return Info{}, fmt.Errorf("backup: create a temporary folder: %w", err)
	}
	defer func() { _ = os.RemoveAll(tmpDir) }() // our own temporary copy

	start := s.now()
	dbCopy := filepath.Join(tmpDir, DBFile)
	if err := s.src.BackupTo(ctx, dbCopy); err != nil {
		return Info{}, err
	}
	version, err := s.src.SchemaVersion(ctx)
	if err != nil {
		return Info{}, err
	}
	m := Manifest{
		Format: FormatVersion, App: "gonema", AppVersion: s.version, SchemaVersion: version,
		CreatedAt: start.UTC().Truncate(time.Second), Kind: kind, IncludesKey: includeKey, KeyFingerprint: s.keyFP,
	}
	name, err := s.freeName(start, kind)
	if err != nil {
		return Info{}, err
	}
	partial := filepath.Join(tmpDir, name)
	if err := writeArchive(ctx, partial, dbCopy, key, &m); err != nil {
		return Info{}, err
	}
	final := filepath.Join(s.Dir(), name)
	if err := noReplaceRename(partial, final); err != nil {
		return Info{}, fmt.Errorf("backup: move the archive into place: %w", err)
	}
	fi, err := os.Stat(final)
	if err != nil {
		return Info{}, fmt.Errorf("backup: %w", err)
	}
	s.log.Info("backup created", slog.String("name", name), slog.String("kind", string(kind)),
		slog.Int64("size", fi.Size()), slog.Bool("includes_key", includeKey), slog.Duration("elapsed", s.now().Sub(start)))
	return infoOf(name, fi.Size(), &m, nil), nil
}

// freeName returns an archive name for t and kind that is not taken.
func (s *Service) freeName(t time.Time, kind Kind) (string, error) {
	stamp := t.UTC().Format("20060102-150405")
	for n := 1; n < 1000; n++ {
		name := "gonema-backup-" + stamp + "-" + string(kind) + ".zip"
		if n > 1 {
			name = "gonema-backup-" + stamp + "-" + strconv.Itoa(n) + "-" + string(kind) + ".zip"
		}
		_, err := os.Lstat(filepath.Join(s.Dir(), name))
		if errors.Is(err, fs.ErrNotExist) {
			return name, nil
		}
		if err != nil {
			return "", fmt.Errorf("backup: %w", err)
		}
	}
	return "", errors.New("backup: no free archive name")
}

// noReplaceRename renames src to dst, refusing to replace an existing dst.
func noReplaceRename(src, dst string) error {
	if _, err := os.Lstat(dst); err == nil {
		return fmt.Errorf("%s already exists", filepath.Base(dst))
	}
	return os.Rename(src, dst)
}

// writeArchive writes the zip: database (deflated, hashed on the way), the
// key when given, then the manifest. The file is synced before returning.
func writeArchive(ctx context.Context, dst, dbPath string, key []byte, m *Manifest) (err error) {
	f, err := os.OpenFile(dst, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0o600)
	if err != nil {
		return fmt.Errorf("backup: create the archive: %w", err)
	}
	defer func() {
		if cerr := f.Close(); cerr != nil && err == nil {
			err = fmt.Errorf("backup: close the archive: %w", cerr)
		}
	}()
	zw := zip.NewWriter(f)
	src, err := os.Open(dbPath) //nolint:gosec // our own temporary copy
	if err != nil {
		return fmt.Errorf("backup: open the database copy: %w", err)
	}
	defer func() { _ = src.Close() }()
	w, err := zw.CreateHeader(&zip.FileHeader{Name: DBFile, Method: zip.Deflate, Modified: m.CreatedAt})
	if err != nil {
		return fmt.Errorf("backup: %w", err)
	}
	h := sha256.New()
	n, err := io.Copy(io.MultiWriter(w, h), ctxReader{ctx, src})
	if err != nil {
		return fmt.Errorf("backup: write the database: %w", err)
	}
	m.DBSize, m.DBSHA256 = n, hex.EncodeToString(h.Sum(nil))
	if key != nil {
		kw, err := zw.CreateHeader(&zip.FileHeader{Name: KeyFile, Method: zip.Store, Modified: m.CreatedAt})
		if err != nil {
			return fmt.Errorf("backup: %w", err)
		}
		if _, err := kw.Write(key); err != nil {
			return fmt.Errorf("backup: write the key: %w", err)
		}
	}
	raw, err := json.MarshalIndent(m, "", "  ")
	if err != nil {
		return fmt.Errorf("backup: encode the manifest: %w", err)
	}
	mw, err := zw.CreateHeader(&zip.FileHeader{Name: ManifestFile, Method: zip.Deflate, Modified: m.CreatedAt})
	if err != nil {
		return fmt.Errorf("backup: %w", err)
	}
	if _, err := mw.Write(append(raw, '\n')); err != nil {
		return fmt.Errorf("backup: write the manifest: %w", err)
	}
	if err := zw.Close(); err != nil {
		return fmt.Errorf("backup: finish the archive: %w", err)
	}
	if err := f.Sync(); err != nil {
		return fmt.Errorf("backup: sync the archive: %w", err)
	}
	return nil
}

// ctxReader stops a copy when ctx is canceled.
type ctxReader struct {
	ctx context.Context
	r   io.Reader
}

func (c ctxReader) Read(p []byte) (int, error) {
	if err := c.ctx.Err(); err != nil {
		return 0, err
	}
	return c.r.Read(p)
}

func infoOf(name string, size int64, m *Manifest, problem error) Info {
	in := Info{Name: name, Size: size, Restorable: problem == nil}
	if m != nil {
		in.CreatedAt, in.Kind, in.AppVersion, in.SchemaVersion, in.IncludesKey = m.CreatedAt, m.Kind, m.AppVersion, m.SchemaVersion, m.IncludesKey
	}
	if problem != nil {
		var ue *Error
		if errors.As(problem, &ue) {
			in.Problem = ue.Msg
		} else {
			in.Problem = problem.Error()
		}
	}
	return in
}

// List returns the archives of the backups folder, newest first. Archives
// with an unreadable manifest are listed with Restorable false.
func (s *Service) List(_ context.Context) ([]Info, error) {
	entries, err := os.ReadDir(s.Dir())
	if errors.Is(err, fs.ErrNotExist) {
		return []Info{}, nil
	}
	if err != nil {
		return nil, fmt.Errorf("backup: list the backups folder: %w", err)
	}
	out := make([]Info, 0, len(entries))
	for _, e := range entries {
		if !e.Type().IsRegular() || !ValidName(e.Name()) {
			continue
		}
		fi, err := e.Info()
		if err != nil {
			continue // removed meanwhile
		}
		m, err := readManifest(filepath.Join(s.Dir(), e.Name()))
		if err == nil {
			err = checkManifest(m)
		}
		in := infoOf(e.Name(), fi.Size(), m, err)
		if m == nil {
			in.CreatedAt = fi.ModTime().UTC()
			in.Kind = kindFromName(e.Name())
		}
		out = append(out, in)
	}
	slices.SortFunc(out, func(a, b Info) int {
		if c := b.CreatedAt.Compare(a.CreatedAt); c != 0 {
			return c
		}
		return strings.Compare(b.Name, a.Name)
	})
	return out, nil
}

func kindFromName(name string) Kind {
	for _, k := range []Kind{KindPreRestore, KindScheduled, KindManual, KindUploaded} {
		if strings.HasSuffix(name, "-"+string(k)+".zip") {
			return k
		}
	}
	return KindManual
}

// readManifest reads the manifest of an archive.
func readManifest(p string) (*Manifest, error) {
	zr, err := zip.OpenReader(p)
	if err != nil {
		return nil, userErr(ErrInvalid, err, "This file is not a readable zip archive")
	}
	defer func() { _ = zr.Close() }()
	for _, f := range zr.File {
		if f.Name != ManifestFile {
			continue
		}
		if f.UncompressedSize64 > 1<<20 {
			return nil, userErr(ErrInvalid, nil, "The manifest of this archive is too large")
		}
		r, err := f.Open()
		if err != nil {
			return nil, userErr(ErrInvalid, err, "The manifest of this archive cannot be read")
		}
		var m Manifest
		err = json.NewDecoder(io.LimitReader(r, 1<<20)).Decode(&m)
		_ = r.Close()
		if err != nil {
			return nil, userErr(ErrInvalid, err, "The manifest of this archive is not valid JSON")
		}
		return &m, nil
	}
	return nil, userErr(ErrInvalid, nil, "This archive has no manifest.json: it was not made by Gonema")
}

// checkManifest verifies what can be told from the manifest alone.
func checkManifest(m *Manifest) error {
	switch {
	case m.App != "gonema":
		return userErr(ErrInvalid, nil, "This archive was not made by Gonema")
	case m.Format < 1 || m.Format > FormatVersion:
		return userErr(ErrInvalid, nil, "This archive uses format %d, which this version of Gonema does not know: update Gonema first", m.Format)
	case m.SchemaVersion < 1:
		return userErr(ErrInvalid, nil, "The manifest gives no schema version")
	case m.SchemaVersion > store.MaxSchemaVersion():
		return userErr(ErrInvalid, nil, "This backup was made by a newer Gonema (database schema %d, this version knows up to %d): update Gonema before restoring it",
			m.SchemaVersion, store.MaxSchemaVersion())
	case len(m.DBSHA256) != 64:
		return userErr(ErrInvalid, nil, "The manifest gives no database checksum")
	}
	return nil
}

// path returns the path of an existing archive, checking its name.
func (s *Service) path(name string) (string, error) {
	if !ValidName(name) {
		return "", userErr(ErrNotFound, nil, "There is no backup named %q.", name)
	}
	p := filepath.Join(s.Dir(), name)
	fi, err := os.Lstat(p)
	if errors.Is(err, fs.ErrNotExist) || err == nil && !fi.Mode().IsRegular() {
		return "", userErr(ErrNotFound, nil, "There is no backup named %q.", name)
	}
	if err != nil {
		return "", fmt.Errorf("backup: %w", err)
	}
	return p, nil
}

// Open opens an archive for download. The name is checked against the archive
// name pattern and the file is opened inside the backups folder only.
func (s *Service) Open(name string) (*os.File, int64, error) {
	if _, err := s.path(name); err != nil {
		return nil, 0, err
	}
	root, err := os.OpenRoot(s.Dir())
	if err != nil {
		return nil, 0, fmt.Errorf("backup: open the backups folder: %w", err)
	}
	defer func() { _ = root.Close() }()
	f, err := root.Open(name)
	if err != nil {
		return nil, 0, userErr(ErrNotFound, err, "There is no backup named %q.", name)
	}
	fi, err := f.Stat()
	if err != nil {
		_ = f.Close()
		return nil, 0, fmt.Errorf("backup: %w", err)
	}
	return f, fi.Size(), nil
}

// Delete removes an archive; confirm must be true.
func (s *Service) Delete(name string, confirm bool) error {
	if !confirm {
		return userErr(ErrConfirmRequired, nil, "Deleting a backup cannot be undone: confirm it (confirm=true).")
	}
	unlock, err := s.lock()
	if err != nil {
		return err
	}
	defer unlock()
	if _, err := s.path(name); err != nil {
		return err
	}
	root, err := os.OpenRoot(s.Dir())
	if err != nil {
		return fmt.Errorf("backup: open the backups folder: %w", err)
	}
	defer func() { _ = root.Close() }()
	if err := root.Remove(name); err != nil {
		return fmt.Errorf("backup: delete %s: %w", name, err)
	}
	s.log.Info("backup deleted", slog.String("name", name))
	return nil
}

// Prune deletes the scheduled backups beyond the newest keep.
func (s *Service) Prune(ctx context.Context, keep int) ([]string, error) {
	if keep < 1 {
		keep = 1
	}
	all, err := s.List(ctx)
	if err != nil {
		return nil, err
	}
	var removed []string
	n := 0
	for _, in := range all {
		if kindFromName(in.Name) != KindScheduled {
			continue
		}
		n++
		if n <= keep {
			continue
		}
		if err := os.Remove(filepath.Join(s.Dir(), in.Name)); err != nil && !errors.Is(err, fs.ErrNotExist) {
			return removed, fmt.Errorf("backup: prune %s: %w", in.Name, err)
		}
		removed = append(removed, in.Name)
		s.log.Info("old scheduled backup deleted", slog.String("name", in.Name), slog.Int("keep", keep))
	}
	return removed, nil
}
