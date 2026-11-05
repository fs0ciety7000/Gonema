// Package store is the SQLite persistence layer of Gonema: connection pools,
// embedded goose migrations, and a thin layer over the sqlc-generated queries
// of package db (transactions, domain errors, a few multi-statement helpers).
//
// SQLite allows a single writer at a time. The store therefore keeps two pools on
// the same database file:
//
//   - a writer pool limited to ONE connection, whose transactions start with
//     BEGIN IMMEDIATE: writers queue in Go (database/sql) instead of failing with
//     SQLITE_BUSY or deadlocking on a read-to-write lock upgrade;
//   - a reader pool of several query_only connections: in WAL mode readers never
//     block the writer nor each other, and always see a consistent snapshot.
//
// Every connection runs with foreign_keys=ON, busy_timeout, synchronous=NORMAL
// (durable in WAL mode except on power loss for the last commits) and
// temp_store=MEMORY.
package store

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"runtime"
	"strings"

	"github.com/fs0ciety7000/gonema/internal/store/db"

	_ "modernc.org/sqlite" // registers the "sqlite" database/sql driver
)

// busyTimeoutMS bounds how long a connection waits on a lock held by another
// process (e.g. a backup tool) before failing with SQLITE_BUSY.
const busyTimeoutMS = 5000

// Store holds the writer and reader pools of one SQLite database.
type Store struct {
	writer *sql.DB
	reader *sql.DB
	w      *db.Queries
	r      *db.Queries
}

// Open opens (creating it if needed) the SQLite database at path, applies the
// pending migrations and returns the store. The caller must Close it.
func Open(ctx context.Context, path string) (*Store, error) {
	if path == "" || strings.ContainsAny(path, "?#") {
		return nil, fmt.Errorf("store: invalid database path %q", path)
	}
	common := fmt.Sprintf("_pragma=busy_timeout(%d)&_pragma=foreign_keys(1)&_pragma=synchronous(NORMAL)&_pragma=temp_store(MEMORY)", busyTimeoutMS)

	writer, err := sql.Open("sqlite", path+"?"+common+"&_pragma=journal_mode(WAL)&_txlock=immediate")
	if err != nil {
		return nil, fmt.Errorf("store: open writer: %w", err)
	}
	writer.SetMaxOpenConns(1)
	writer.SetMaxIdleConns(1)
	writer.SetConnMaxLifetime(0)
	writer.SetConnMaxIdleTime(0)

	if err := checkWriter(ctx, writer); err != nil {
		return nil, errors.Join(err, writer.Close())
	}
	if err := migrateUp(ctx, writer); err != nil {
		return nil, errors.Join(err, writer.Close())
	}

	reader, err := sql.Open("sqlite", path+"?"+common+"&_query_only=1")
	if err != nil {
		return nil, errors.Join(fmt.Errorf("store: open reader: %w", err), writer.Close())
	}
	n := max(4, runtime.GOMAXPROCS(0))
	reader.SetMaxOpenConns(n)
	reader.SetMaxIdleConns(n)
	if err := reader.PingContext(ctx); err != nil {
		return nil, errors.Join(fmt.Errorf("store: ping reader: %w", err), reader.Close(), writer.Close())
	}

	// Prepared once: parsing a statement costs about as much as running it.
	w, err := db.Prepare(ctx, writer)
	if err != nil {
		return nil, errors.Join(fmt.Errorf("store: prepare writer queries: %w", err), reader.Close(), writer.Close())
	}
	r, err := db.Prepare(ctx, reader)
	if err != nil {
		return nil, errors.Join(fmt.Errorf("store: prepare reader queries: %w", err), w.Close(), reader.Close(), writer.Close())
	}
	return &Store{writer: writer, reader: reader, w: w, r: r}, nil
}

// checkWriter verifies that the pragmas took effect: WAL can silently fail (e.g.
// on some network file systems) and the reader/writer split relies on it.
func checkWriter(ctx context.Context, writer *sql.DB) error {
	var mode string
	if err := writer.QueryRowContext(ctx, "PRAGMA journal_mode").Scan(&mode); err != nil {
		return fmt.Errorf("store: read journal_mode: %w", err)
	}
	if !strings.EqualFold(mode, "wal") {
		return fmt.Errorf("store: journal_mode is %q, want wal (unsupported file system?)", mode)
	}
	var fk int
	if err := writer.QueryRowContext(ctx, "PRAGMA foreign_keys").Scan(&fk); err != nil {
		return fmt.Errorf("store: read foreign_keys: %w", err)
	}
	if fk != 1 {
		return errors.New("store: foreign_keys is off")
	}
	return nil
}

// Close optimizes the query planner statistics and closes both pools.
func (s *Store) Close() error {
	var errs []error
	if err := s.r.Close(); err != nil {
		errs = append(errs, fmt.Errorf("store: close reader statements: %w", err))
	}
	if err := s.w.Close(); err != nil {
		errs = append(errs, fmt.Errorf("store: close writer statements: %w", err))
	}
	if err := s.reader.Close(); err != nil {
		errs = append(errs, fmt.Errorf("store: close reader: %w", err))
	}
	if _, err := s.writer.ExecContext(context.Background(), "PRAGMA optimize"); err != nil {
		errs = append(errs, fmt.Errorf("store: optimize: %w", err))
	}
	if err := s.writer.Close(); err != nil {
		errs = append(errs, fmt.Errorf("store: close writer: %w", err))
	}
	return errors.Join(errs...)
}

// Read returns the queries bound to the reader pool. Write queries fail on it
// (query_only). Errors are raw: wrap them with MapError.
func (s *Store) Read() *db.Queries { return s.r }

// Write returns the queries bound to the single writer connection, for one-statement
// writes outside a transaction. Errors are raw: wrap them with MapError.
func (s *Store) Write() *db.Queries { return s.w }

// WithTx runs fn in a write transaction (BEGIN IMMEDIATE) and commits if fn
// returns nil, rolls back otherwise. The returned error goes through MapError.
// fn must not call other Store methods that write: the writer pool has one
// connection, held by this transaction, so they would wait forever.
func (s *Store) WithTx(ctx context.Context, fn func(q *db.Queries) error) error {
	return runTx(ctx, s.writer, s.w, fn)
}

// ReadTx runs fn in a read transaction on the reader pool: every query of fn sees
// the same snapshot. The returned error goes through MapError.
func (s *Store) ReadTx(ctx context.Context, fn func(q *db.Queries) error) error {
	return runTx(ctx, s.reader, s.r, fn)
}

func runTx(ctx context.Context, conn *sql.DB, q *db.Queries, fn func(q *db.Queries) error) (err error) {
	tx, err := conn.BeginTx(ctx, nil)
	if err != nil {
		return MapError(fmt.Errorf("store: begin: %w", err))
	}
	defer func() {
		if p := recover(); p != nil {
			_ = tx.Rollback()
			panic(p)
		}
	}()
	if err := fn(q.WithTx(tx)); err != nil {
		if rbErr := tx.Rollback(); rbErr != nil {
			return MapError(errors.Join(err, fmt.Errorf("store: rollback: %w", rbErr)))
		}
		return MapError(err)
	}
	if err := tx.Commit(); err != nil {
		return MapError(fmt.Errorf("store: commit: %w", err))
	}
	return nil
}
