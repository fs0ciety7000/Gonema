//go:build windows

package fsx

import (
	"errors"

	"golang.org/x/sys/windows"
)

// isCrossDevice reports whether err is a link or rename across volumes.
func isCrossDevice(err error) bool {
	return errors.Is(err, ErrCrossDevice) || errors.Is(err, windows.ERROR_NOT_SAME_DEVICE)
}
