package store

import (
	"context"
	"errors"
	"fmt"
	"io/fs"
	"os"
)

// maxIntegrityProblems bounds the problems PRAGMA quick_check reports.
const maxIntegrityProblems = 20

// QuickCheck runs PRAGMA quick_check on a reader connection and returns the
// problems found (nil when the database is sound). It reads the whole file:
// run it rarely (the health check runs it weekly).
func (s *Store) QuickCheck(ctx context.Context) ([]string, error) {
	rows, err := s.reader.QueryContext(ctx, fmt.Sprintf("PRAGMA quick_check(%d)", maxIntegrityProblems))
	if err != nil {
		return nil, fmt.Errorf("store: quick_check: %w", err)
	}
	defer rows.Close()
	var problems []string
	for rows.Next() {
		var line string
		if err := rows.Scan(&line); err != nil {
			return nil, fmt.Errorf("store: quick_check: %w", err)
		}
		if line != "ok" {
			problems = append(problems, line)
		}
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("store: quick_check: %w", err)
	}
	return problems, nil
}

// WALSize returns the size in bytes of the write-ahead log file (0 when it does
// not exist).
func (s *Store) WALSize() (int64, error) {
	fi, err := os.Stat(s.path + "-wal")
	if errors.Is(err, fs.ErrNotExist) {
		return 0, nil
	}
	if err != nil {
		return 0, fmt.Errorf("store: stat the write-ahead log: %w", err)
	}
	return fi.Size(), nil
}

// Path returns the database file path.
func (s *Store) Path() string { return s.path }
