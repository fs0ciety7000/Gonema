//go:build unix

package fsx

import (
	"errors"
	"syscall"
)

// isCrossDevice reports whether err is a link or rename across file systems.
func isCrossDevice(err error) bool {
	return errors.Is(err, ErrCrossDevice) || errors.Is(err, syscall.EXDEV)
}
