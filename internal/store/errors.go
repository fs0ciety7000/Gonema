package store

import (
	"database/sql"
	"errors"
	"fmt"
	"strings"

	"modernc.org/sqlite"
	sqlite3 "modernc.org/sqlite/lib"
)

// Domain errors. Errors returned by WithTx, ReadTx and MapError wrap one of them
// (when applicable) together with the original driver error, so callers test with
// errors.Is and logs keep SQLite's message (which names the constraint).
var (
	// ErrNotFound: no row matched (sql.ErrNoRows, or an update/delete that
	// affected no row, see RequireRow).
	ErrNotFound = errors.New("store: not found")
	// ErrConflict: a UNIQUE / PRIMARY KEY constraint (duplicate) or a FOREIGN KEY
	// constraint (missing parent on insert, or row still referenced on delete).
	ErrConflict = errors.New("store: conflict")
	// ErrInvalid: a CHECK, NOT NULL or type constraint (bad enum, invalid JSON...).
	// Inputs should be validated in Go first; this is the safety net.
	ErrInvalid = errors.New("store: invalid value")
	// ErrImmutable: a trigger refused the change (history is append-only).
	ErrImmutable = errors.New("store: immutable row")
)

// MapError translates driver errors into the domain errors above. It returns nil
// for nil and leaves other errors (I/O, context cancellation...) unchanged.
func MapError(err error) error {
	if err == nil {
		return nil
	}
	for _, sentinel := range []error{ErrNotFound, ErrConflict, ErrInvalid, ErrImmutable} {
		if errors.Is(err, sentinel) {
			return err
		}
	}
	if errors.Is(err, sql.ErrNoRows) {
		return fmt.Errorf("%w: %w", ErrNotFound, err)
	}
	var se *sqlite.Error
	if !errors.As(err, &se) {
		return err
	}
	switch se.Code() {
	case sqlite3.SQLITE_CONSTRAINT_UNIQUE, sqlite3.SQLITE_CONSTRAINT_PRIMARYKEY,
		sqlite3.SQLITE_CONSTRAINT_FOREIGNKEY:
		return fmt.Errorf("%w: %w", ErrConflict, err)
	case sqlite3.SQLITE_CONSTRAINT_CHECK, sqlite3.SQLITE_CONSTRAINT_NOTNULL,
		sqlite3.SQLITE_CONSTRAINT_DATATYPE:
		return fmt.Errorf("%w: %w", ErrInvalid, err)
	case sqlite3.SQLITE_CONSTRAINT_TRIGGER:
		// ON DELETE RESTRICT is reported with the trigger code too.
		if strings.Contains(se.Error(), "FOREIGN KEY constraint failed") {
			return fmt.Errorf("%w: %w", ErrConflict, err)
		}
		return fmt.Errorf("%w: %w", ErrImmutable, err)
	}
	return err
}

// RequireRow turns the result of an :execrows query into an error: ErrNotFound
// when no row was affected, the mapped error otherwise.
func RequireRow(affected int64, err error) error {
	if err != nil {
		return MapError(err)
	}
	if affected == 0 {
		return ErrNotFound
	}
	return nil
}
