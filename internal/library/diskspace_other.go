//go:build !linux && !darwin && !freebsd && !windows

package library

func diskSpace(string) (free, total uint64, err error) { return 0, 0, ErrDiskSpaceUnsupported }
