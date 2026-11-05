//go:build !linux && !darwin && !freebsd && !netbsd && !openbsd && !dragonfly && !windows

package fsx

func freeSpace(string) (uint64, error) { return 0, ErrUnsupported }
