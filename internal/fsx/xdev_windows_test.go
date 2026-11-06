//go:build windows

package fsx_test

import "golang.org/x/sys/windows"

// errCrossDevice is what the OS returns for a link or rename across volumes.
var errCrossDevice error = windows.ERROR_NOT_SAME_DEVICE
