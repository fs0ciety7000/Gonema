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
	"slices"
	"strings"
	"time"

	"github.com/fs0ciety7000/gonema/internal/secretbox"
	"github.com/fs0ciety7000/gonema/internal/store"
)

// Files of a staged restore, in <data dir>/restore.
const (
	pendingFile  = "pending.json"
	stagedDB     = "gonema.db.staged"
	stagedKey    = "gonema.key.staged"
	replacedDir  = "replaced"
	uploadPrefix = ".upload-"
)

// Pending describes a staged restore, applied at the next start.
type Pending struct {
	Name       string    `json:"name"`
	StagedAt   time.Time `json:"stagedAt"`
	RestoreKey bool      `json:"restoreKey"`
}

// Staged is the outcome of StageRestore.
type Staged struct {
	RestartRequired  bool     `json:"restartRequired"`
	PreRestoreBackup Info     `json:"preRestoreBackup"`
	Warnings         []string `json:"warnings,omitempty"`
	Message          string   `json:"message"`
}

func (s *Service) restoreDir() string { return filepath.Join(s.dataDir, RestoreDir) }

// validated is an archive whose database was extracted and checked.
type validated struct {
	manifest *Manifest
	db       string // extracted database
	key      []byte // archived key, when present and asked for
}

// validate checks the archive at p and extracts its database (and key when
// wantKey) into dir. It verifies the entries, the manifest, the checksum, the
// SQLite integrity and the schema version.
func (s *Service) validate(ctx context.Context, p, dir string, wantKey bool) (*validated, error) {
	m, err := readManifest(p)
	if err != nil {
		return nil, err
	}
	if err := checkManifest(m); err != nil {
		return nil, err
	}
	zr, err := zip.OpenReader(p)
	if err != nil {
		return nil, userErr(ErrInvalid, err, "This file is not a readable zip archive")
	}
	defer func() { _ = zr.Close() }()
	v := &validated{manifest: m}
	var dbEntry, keyEntry *zip.File
	for _, f := range zr.File {
		switch f.Name {
		case ManifestFile:
		case DBFile:
			dbEntry = f
		case KeyFile:
			keyEntry = f
		default:
			return nil, userErr(ErrInvalid, nil, "The archive holds an unexpected file (%q): it was not made by Gonema", f.Name)
		}
	}
	if dbEntry == nil {
		return nil, userErr(ErrInvalid, nil, "The archive holds no database (%s)", DBFile)
	}
	if dbEntry.UncompressedSize64 > uint64(s.maxDB) || int64(dbEntry.UncompressedSize64) != m.DBSize {
		return nil, userErr(ErrInvalid, nil, "The database in the archive does not have the size the manifest gives")
	}
	if err := os.MkdirAll(dir, 0o750); err != nil {
		return nil, fmt.Errorf("backup: %w", err)
	}
	v.db = filepath.Join(dir, stagedDB)
	_ = os.Remove(v.db) // a previous staged copy: ours
	if err := extract(ctx, dbEntry, v.db, m.DBSize, m.DBSHA256); err != nil {
		_ = os.Remove(v.db)
		return nil, err
	}
	info, err := store.InspectFile(ctx, v.db)
	if err != nil {
		_ = os.Remove(v.db)
		return nil, userErr(ErrInvalid, err, "The database in the archive cannot be opened")
	}
	switch {
	case len(info.Problems) > 0:
		_ = os.Remove(v.db)
		return nil, userErr(ErrInvalid, nil, "The database in the archive is damaged (%s)", info.Problems[0])
	case info.SchemaVersion != m.SchemaVersion:
		_ = os.Remove(v.db)
		return nil, userErr(ErrInvalid, nil, "The database in the archive has schema %d, the manifest says %d", info.SchemaVersion, m.SchemaVersion)
	}
	if wantKey {
		if keyEntry == nil || !m.IncludesKey {
			_ = os.Remove(v.db)
			return nil, userErr(ErrInvalid, nil, "This archive does not include the master key; restore it without restoreKey")
		}
		r, err := keyEntry.Open()
		if err != nil {
			_ = os.Remove(v.db)
			return nil, userErr(ErrInvalid, err, "The key in the archive cannot be read")
		}
		b, err := io.ReadAll(io.LimitReader(r, 4096))
		_ = r.Close()
		if err != nil {
			_ = os.Remove(v.db)
			return nil, userErr(ErrInvalid, err, "The key in the archive cannot be read")
		}
		k, err := secretbox.ParseKey(b)
		if err != nil {
			_ = os.Remove(v.db)
			return nil, userErr(ErrInvalid, nil, "The key in the archive is not a valid master key")
		}
		if m.KeyFingerprint != "" && Fingerprint(k) != m.KeyFingerprint {
			_ = os.Remove(v.db)
			return nil, userErr(ErrInvalid, nil, "The key in the archive is not the one its secrets were encrypted with")
		}
		v.key = b
	}
	return v, nil
}

// extract writes a zip entry to dst, checking its size and SHA-256.
func extract(ctx context.Context, f *zip.File, dst string, size int64, sum string) (err error) {
	r, err := f.Open()
	if err != nil {
		return userErr(ErrInvalid, err, "The database in the archive cannot be read")
	}
	defer func() { _ = r.Close() }()
	out, err := os.OpenFile(dst, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0o600)
	if err != nil {
		return fmt.Errorf("backup: extract the database: %w", err)
	}
	defer func() {
		if cerr := out.Close(); cerr != nil && err == nil {
			err = fmt.Errorf("backup: extract the database: %w", cerr)
		}
	}()
	h := sha256.New()
	n, err := io.Copy(io.MultiWriter(out, h), ctxReader{ctx, io.LimitReader(r, size+1)})
	if err != nil {
		return userErr(ErrInvalid, err, "The database in the archive cannot be read (damaged archive?)")
	}
	if n != size || hex.EncodeToString(h.Sum(nil)) != sum {
		return userErr(ErrInvalid, nil, "The database in the archive does not match its checksum (damaged archive?)")
	}
	if err := out.Sync(); err != nil {
		return fmt.Errorf("backup: sync the extracted database: %w", err)
	}
	return nil
}

// Import adds an archive uploaded by the user to the backups folder, after
// validating it (the same checks as a restore), as kind "uploaded".
func (s *Service) Import(ctx context.Context, r io.Reader) (Info, error) {
	unlock, err := s.lock()
	if err != nil {
		return Info{}, err
	}
	defer unlock()
	if err := os.MkdirAll(s.Dir(), 0o750); err != nil {
		return Info{}, fmt.Errorf("backup: create the backups folder: %w", err)
	}
	tmpDir, err := os.MkdirTemp(s.Dir(), uploadPrefix)
	if err != nil {
		return Info{}, fmt.Errorf("backup: %w", err)
	}
	defer func() { _ = os.RemoveAll(tmpDir) }() // our own temporary files
	up := filepath.Join(tmpDir, "upload.zip")
	f, err := os.OpenFile(up, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0o600)
	if err != nil {
		return Info{}, fmt.Errorf("backup: %w", err)
	}
	_, err = io.Copy(f, ctxReader{ctx, r})
	if cerr := f.Close(); err == nil {
		err = cerr
	}
	if err != nil {
		return Info{}, userErr(ErrInvalid, err, "The upload did not complete")
	}
	v, err := s.validate(ctx, up, tmpDir, false)
	if err != nil {
		return Info{}, err
	}
	_ = os.Remove(v.db)
	name, err := s.freeName(s.now(), KindUploaded)
	if err != nil {
		return Info{}, err
	}
	final := filepath.Join(s.Dir(), name)
	if err := noReplaceRename(up, final); err != nil {
		return Info{}, fmt.Errorf("backup: %w", err)
	}
	fi, err := os.Stat(final)
	if err != nil {
		return Info{}, fmt.Errorf("backup: %w", err)
	}
	s.log.Info("backup uploaded", slog.String("name", name), slog.Int64("size", fi.Size()),
		slog.Int64("schema_version", v.manifest.SchemaVersion), slog.String("app_version", v.manifest.AppVersion))
	return infoOf(name, fi.Size(), v.manifest, nil), nil
}

// StageRestore validates the archive name, backs up the current state (kind
// pre-restore), and stages the archived database (and key with restoreKey) to
// replace the current one at the next start. confirm must be true.
func (s *Service) StageRestore(ctx context.Context, name string, restoreKey, confirm bool) (Staged, error) {
	if !confirm {
		return Staged{}, userErr(ErrConfirmRequired, nil,
			"Restoring replaces the whole database (library, settings, history) at the next start: confirm it (confirm=true).")
	}
	unlock, err := s.lock()
	if err != nil {
		return Staged{}, err
	}
	defer unlock()
	p, err := s.path(name)
	if err != nil {
		return Staged{}, err
	}
	if restoreKey && s.keyFromEnv {
		return Staged{}, userErr(ErrInvalid, nil,
			"The master key comes from the %s variable: set that variable to the archived key instead of restoreKey.", secretbox.EnvMasterKey)
	}
	v, err := s.validate(ctx, p, s.restoreDir(), restoreKey)
	if err != nil {
		return Staged{}, err
	}
	var warnings []string
	if !restoreKey && v.manifest.KeyFingerprint != "" && s.keyFP != "" && v.manifest.KeyFingerprint != s.keyFP {
		w := "The secrets in this backup (indexer API keys, download client passwords, the TMDB key) were encrypted with another master key: " +
			"after the restore they will have to be entered again"
		if v.manifest.IncludesKey {
			w += ", unless you restore with restoreKey (the archive includes its key)"
		}
		warnings = append(warnings, w+".")
	}
	if v.manifest.SchemaVersion < store.MaxSchemaVersion() {
		warnings = append(warnings, fmt.Sprintf("The backup has database schema %d; it is upgraded to %d at the next start.",
			v.manifest.SchemaVersion, store.MaxSchemaVersion()))
	}
	pre, err := s.create(ctx, KindPreRestore, false)
	if err != nil {
		_ = os.Remove(v.db)
		return Staged{}, fmt.Errorf("backup: back up the current state before the restore: %w", err)
	}
	if v.key != nil {
		if err := writeFileSync(filepath.Join(s.restoreDir(), stagedKey), v.key, 0o600); err != nil {
			_ = os.Remove(v.db)
			return Staged{}, err
		}
	}
	pend := Pending{Name: name, StagedAt: s.now().UTC().Truncate(time.Second), RestoreKey: v.key != nil}
	raw, err := json.Marshal(pend)
	if err != nil {
		return Staged{}, fmt.Errorf("backup: %w", err)
	}
	// The marker is written last: without it, a staged file is ignored.
	if err := writeFileSync(filepath.Join(s.restoreDir(), pendingFile), raw, 0o600); err != nil {
		_ = os.Remove(v.db)
		return Staged{}, err
	}
	s.log.Warn("restore staged: it replaces the database at the next start", slog.String("name", name),
		slog.Bool("restore_key", pend.RestoreKey), slog.String("pre_restore_backup", pre.Name))
	return Staged{
		RestartRequired: true, PreRestoreBackup: pre, Warnings: warnings,
		Message: "The backup " + name + " is ready: restart Gonema to restore it. The current state was saved as " + pre.Name + ".",
	}, nil
}

func writeFileSync(p string, b []byte, mode os.FileMode) error {
	tmp := p + ".tmp"
	f, err := os.OpenFile(tmp, os.O_CREATE|os.O_TRUNC|os.O_WRONLY, mode)
	if err != nil {
		return fmt.Errorf("backup: write %s: %w", filepath.Base(p), err)
	}
	_, err = f.Write(b)
	if err == nil {
		err = f.Sync()
	}
	if cerr := f.Close(); err == nil {
		err = cerr
	}
	if err == nil {
		err = os.Rename(tmp, p)
	}
	if err != nil {
		_ = os.Remove(tmp)
		return fmt.Errorf("backup: write %s: %w", filepath.Base(p), err)
	}
	return nil
}

// PendingRestore returns the staged restore, or nil.
func (s *Service) PendingRestore() (*Pending, error) { return readPending(s.restoreDir()) }

func readPending(dir string) (*Pending, error) {
	raw, err := os.ReadFile(filepath.Join(dir, pendingFile))
	if errors.Is(err, fs.ErrNotExist) {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("backup: read the staged restore: %w", err)
	}
	var p Pending
	if err := json.Unmarshal(raw, &p); err != nil {
		return nil, fmt.Errorf("backup: the staged restore marker is damaged: %w", err)
	}
	return &p, nil
}

// CancelRestore removes the staged restore (the pre-restore backup stays).
func (s *Service) CancelRestore() error {
	unlock, err := s.lock()
	if err != nil {
		return err
	}
	defer unlock()
	p, err := s.PendingRestore()
	if err != nil {
		return err
	}
	if p == nil {
		return userErr(ErrNotFound, nil, "No restore is staged.")
	}
	dir := s.restoreDir()
	for _, f := range []string{pendingFile, stagedDB, stagedKey} {
		if err := os.Remove(filepath.Join(dir, f)); err != nil && !errors.Is(err, fs.ErrNotExist) {
			return fmt.Errorf("backup: cancel the restore: %w", err)
		}
	}
	s.log.Info("staged restore canceled", slog.String("name", p.Name))
	return nil
}

// ApplyPending swaps a staged restore in. Call it at startup, BEFORE the
// database is opened. The current database files (and the key with
// RestoreKey) are moved to <data dir>/restore/replaced/<time>; only the newest
// replaced folder is kept (a pre-restore backup archive exists anyway). It is
// idempotent: a crash in the middle is completed at the next start.
func ApplyPending(dataDir string, now time.Time, log *slog.Logger) (*Pending, error) {
	if log == nil {
		log = slog.New(slog.DiscardHandler)
	}
	dir := filepath.Join(dataDir, RestoreDir)
	p, err := readPending(dir)
	if err != nil || p == nil {
		return nil, err
	}
	staged := filepath.Join(dir, stagedDB)
	if _, err := os.Stat(staged); err != nil {
		if errors.Is(err, fs.ErrNotExist) {
			// Already swapped by an interrupted start: finish.
			return p, removeIfExists(filepath.Join(dir, pendingFile))
		}
		return nil, fmt.Errorf("backup: staged database: %w", err)
	}
	moved := filepath.Join(dir, replacedDir, now.UTC().Format("20060102-150405"))
	if err := os.MkdirAll(moved, 0o750); err != nil {
		return nil, fmt.Errorf("backup: %w", err)
	}
	names := []string{DBFile, DBFile + "-wal", DBFile + "-shm"}
	if p.RestoreKey {
		names = append(names, KeyFile)
	}
	for _, n := range names {
		if err := os.Rename(filepath.Join(dataDir, n), filepath.Join(moved, n)); err != nil && !errors.Is(err, fs.ErrNotExist) {
			return nil, fmt.Errorf("backup: move the current %s aside: %w", n, err)
		}
	}
	if p.RestoreKey {
		if err := os.Rename(filepath.Join(dir, stagedKey), filepath.Join(dataDir, KeyFile)); err != nil {
			return nil, fmt.Errorf("backup: put the restored key in place: %w", err)
		}
	}
	if err := os.Rename(staged, filepath.Join(dataDir, DBFile)); err != nil {
		return nil, fmt.Errorf("backup: put the restored database in place: %w", err)
	}
	if err := removeIfExists(filepath.Join(dir, pendingFile)); err != nil {
		return nil, err
	}
	pruneReplaced(filepath.Join(dir, replacedDir), filepath.Base(moved), log)
	log.Warn("backup restored: the previous database was moved aside", slog.String("name", p.Name), slog.String("previous", moved),
		slog.Bool("key_restored", p.RestoreKey))
	return p, nil
}

func removeIfExists(p string) error {
	if err := os.Remove(p); err != nil && !errors.Is(err, fs.ErrNotExist) {
		return fmt.Errorf("backup: %w", err)
	}
	return nil
}

// pruneReplaced keeps only the newest folder of replaced files.
func pruneReplaced(dir, keep string, log *slog.Logger) {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return
	}
	names := make([]string, 0, len(entries))
	for _, e := range entries {
		if e.IsDir() && e.Name() != keep && len(e.Name()) == len("20060102-150405") && !strings.ContainsAny(e.Name(), `/\.`) {
			names = append(names, e.Name())
		}
	}
	slices.Sort(names)
	for _, n := range names {
		if _, err := os.Lstat(filepath.Join(dir, n, KeyFile)); err == nil {
			continue // a replaced master key is never removed
		}
		if err := os.RemoveAll(filepath.Join(dir, n)); err != nil {
			log.Warn("could not remove an old replaced database folder", slog.String("path", filepath.Join(dir, n)), slog.Any("error", err))
		}
	}
}
