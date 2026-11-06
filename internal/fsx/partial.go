package fsx

import (
	"errors"
	"io/fs"
	"path/filepath"
	"strings"
)

// RemovePartials removes, from dir, the temporary files that copies write
// before renaming them into place (".gonema-partial-*"): a crash during a copy
// leaves one behind. They are Gonema's own incomplete files, never a user file:
// only regular files with that prefix are removed. A missing dir is not an
// error. It returns the names removed.
func (o *Ops) RemovePartials(dir string) ([]string, error) {
	entries, err := o.fs.ReadDir(dir)
	if errors.Is(err, fs.ErrNotExist) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	var removed []string
	for _, e := range entries {
		if !strings.HasPrefix(e.Name(), tempPrefix) || !e.Type().IsRegular() {
			continue
		}
		if err := o.fs.Remove(filepath.Join(dir, e.Name())); err != nil && !errors.Is(err, fs.ErrNotExist) {
			return removed, err
		}
		removed = append(removed, e.Name())
	}
	return removed, nil
}
