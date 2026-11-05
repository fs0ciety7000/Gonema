package store

import (
	"context"
	"database/sql"
	"embed"
	"fmt"
	"io/fs"

	"github.com/pressly/goose/v3"
)

//go:embed migrations/*.sql
var migrationFiles embed.FS

// newMigrator returns a goose provider over the embedded migrations.
func newMigrator(conn *sql.DB) (*goose.Provider, error) {
	fsys, err := fs.Sub(migrationFiles, "migrations")
	if err != nil {
		return nil, fmt.Errorf("store: migrations fs: %w", err)
	}
	p, err := goose.NewProvider(goose.DialectSQLite3, conn, fsys)
	if err != nil {
		return nil, fmt.Errorf("store: migration provider: %w", err)
	}
	return p, nil
}

// migrateUp applies every pending migration, each in its own transaction.
func migrateUp(ctx context.Context, conn *sql.DB) error {
	p, err := newMigrator(conn)
	if err != nil {
		return err
	}
	if _, err := p.Up(ctx); err != nil {
		return fmt.Errorf("store: migrate up: %w", err)
	}
	return nil
}

// MigrateDownTo rolls the schema back to version (0 = empty database). It is
// destructive: data of dropped tables is lost. Meant for tests and explicit
// operator action only, never called automatically.
func (s *Store) MigrateDownTo(ctx context.Context, version int64) error {
	p, err := newMigrator(s.writer)
	if err != nil {
		return err
	}
	if _, err := p.DownTo(ctx, version); err != nil {
		return fmt.Errorf("store: migrate down to %d: %w", version, err)
	}
	return nil
}

// MigrateUp applies pending migrations (after MigrateDownTo, or for tests).
func (s *Store) MigrateUp(ctx context.Context) error { return migrateUp(ctx, s.writer) }

// SchemaVersion returns the current migration version.
func (s *Store) SchemaVersion(ctx context.Context) (int64, error) {
	p, err := newMigrator(s.writer)
	if err != nil {
		return 0, err
	}
	v, err := p.GetDBVersion(ctx)
	if err != nil {
		return 0, fmt.Errorf("store: schema version: %w", err)
	}
	return v, nil
}
