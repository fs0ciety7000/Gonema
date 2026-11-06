//go:build !unix && !windows

package fsx

import "errors"

// isCrossDevice reports whether err is a link or rename across file systems.
func isCrossDevice(err error) bool { return errors.Is(err, ErrCrossDevice) }
