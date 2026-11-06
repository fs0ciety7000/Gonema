package store

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"io/fs"
	"path"
	"strconv"
	"strings"
)

// BackupTo writes a consistent copy of the database to dst (which must not
// exist) with VACUUM INTO. It runs on a dedicated connection (the reader pool
// is query_only, which VACUUM INTO refuses): in WAL mode it reads one
// snapshot without blocking the writer, and the copy is compacted, without a
// write-ahead log.
func (s *Store) BackupTo(ctx context.Context, dst string) (err error) {
	if dst == "" || strings.ContainsRune(dst, 0) {
		return errors.New("store: invalid backup path")
	}
	conn, err := sql.Open("sqlite", s.path+fmt.Sprintf("?_pragma=busy_timeout(%d)", busyTimeoutMS))
	if err != nil {
		return fmt.Errorf("store: open a backup connection: %w", err)
	}
	defer func() {
		if cerr := conn.Close(); cerr != nil {
			err = errors.Join(err, fmt.Errorf("store: close the backup connection: %w", cerr))
		}
	}()
	conn.SetMaxOpenConns(1)
	if _, err := conn.ExecContext(ctx, "VACUUM INTO ?", dst); err != nil {
		return fmt.Errorf("store: back up the database: %w", err)
	}
	return nil
}

// MaxSchemaVersion returns the newest migration version this binary knows: a
// database with a higher version was written by a newer Gonema.
func MaxSchemaVersion() int64 {
	entries, err := fs.ReadDir(migrationFiles, "migrations")
	if err != nil {
		return 0 // embedded at build time: cannot happen
	}
	var top int64
	for _, e := range entries {
		name := path.Base(e.Name())
		num, _, ok := strings.Cut(name, "_")
		if !ok {
			continue
		}
		if v, err := strconv.ParseInt(num, 10, 64); err == nil {
			top = max(top, v)
		}
	}
	return top
}

// FileInfo describes a database file inspected by InspectFile.
type FileInfo struct {
	SchemaVersion int64
	Problems      []string // PRAGMA quick_check findings; empty when sound
}

// InspectFile opens the SQLite database at p read-only and returns its schema
// version and integrity problems. It never writes to the file.
func InspectFile(ctx context.Context, p string) (FileInfo, error) {
	if p == "" || strings.ContainsAny(p, "?#") {
		return FileInfo{}, fmt.Errorf("store: invalid database path %q", p)
	}
	conn, err := sql.Open("sqlite", "file:"+p+"?mode=ro&_pragma=query_only(1)&_pragma=busy_timeout(5000)")
	if err != nil {
		return FileInfo{}, fmt.Errorf("store: open %s: %w", path.Base(p), err)
	}
	defer func() { _ = conn.Close() }()
	conn.SetMaxOpenConns(1)
	var fi FileInfo
	rows, err := conn.QueryContext(ctx, fmt.Sprintf("PRAGMA quick_check(%d)", maxIntegrityProblems))
	if err != nil {
		return FileInfo{}, fmt.Errorf("store: not a usable SQLite database: %w", err)
	}
	for rows.Next() {
		var line string
		if err := rows.Scan(&line); err != nil {
			_ = rows.Close()
			return FileInfo{}, fmt.Errorf("store: quick_check: %w", err)
		}
		if line != "ok" {
			fi.Problems = append(fi.Problems, line)
		}
	}
	if err := errors.Join(rows.Err(), rows.Close()); err != nil {
		return FileInfo{}, fmt.Errorf("store: quick_check: %w", err)
	}
	var v sql.NullInt64
	if err := conn.QueryRowContext(ctx, "SELECT max(version_id) FROM goose_db_version WHERE is_applied").Scan(&v); err != nil {
		return FileInfo{}, fmt.Errorf("store: not a Gonema database (no migration table): %w", err)
	}
	fi.SchemaVersion = v.Int64
	return fi, nil
}
