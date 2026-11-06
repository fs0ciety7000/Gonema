package backup

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
)

// Upload bounds. An uploaded archive holds one database: it has no reason to
// be much larger than the current one.
const (
	// MinUploadLimit is the smallest upload limit, whatever the database size.
	MinUploadLimit = 1 << 30
	// uploadSpaceMargin is kept free besides the upload and its extraction.
	uploadSpaceMargin = 256 << 20
)

// More errors of uploads.
var (
	ErrTooLarge = errors.New("backup: archive too large")
	ErrNoSpace  = errors.New("backup: not enough free space")
)

// UploadLimit returns the largest archive Import accepts: twice the size of
// the current database (with its WAL), at least MinUploadLimit, at most the
// largest database a restore accepts.
func (s *Service) UploadLimit() int64 {
	var size int64
	for _, name := range []string{DBFile, DBFile + "-wal"} {
		if fi, err := os.Stat(filepath.Join(s.dataDir, name)); err == nil {
			size += fi.Size()
		}
	}
	return min(max(2*size, MinUploadLimit), s.maxDB)
}

// CheckUpload refuses an upload of n bytes (-1: unknown, the limit is
// assumed) above UploadLimit, or that the data directory (or the temporary
// folder, where the request body is spooled) has no room for: the archive is
// stored, then its database extracted to be checked.
func (s *Service) CheckUpload(n int64) error {
	limit := s.UploadLimit()
	if n > limit {
		return userErr(ErrTooLarge, nil, "This archive is larger than %s, the most Gonema accepts for a database of this size", sizeText(limit))
	}
	if n < 0 {
		n = limit
	}
	need := uint64(2*n) + uploadSpaceMargin //nolint:gosec // n >= 0
	for _, dir := range []string{s.dataDir, os.TempDir()} {
		free, err := s.freeSpace(dir)
		if err != nil {
			continue // unknown (fsx.ErrUnsupported...): the copy itself fails if the disk fills up
		}
		if free < need {
			return userErr(ErrNoSpace, nil, "Not enough free space to receive this archive: %s needed, %s free", sizeText(int64(need)), sizeText(int64(free))) //nolint:gosec // sizes
		}
	}
	return nil
}

func sizeText(n int64) string {
	switch {
	case n >= 1<<30:
		return fmt.Sprintf("%.1f GiB", float64(n)/(1<<30))
	case n >= 1<<20:
		return fmt.Sprintf("%.0f MiB", float64(n)/(1<<20))
	}
	return fmt.Sprintf("%d bytes", n)
}
