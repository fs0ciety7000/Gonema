//go:build unix

package fsx_test

import "syscall"

// errCrossDevice is what the OS returns for a link or rename across file systems.
var errCrossDevice error = syscall.EXDEV
