//go:build linux || darwin || freebsd || netbsd || openbsd || dragonfly

package fsx

import "golang.org/x/sys/unix"

func freeSpace(path string) (uint64, error) {
	var st unix.Statfs_t
	if err := unix.Statfs(path, &st); err != nil {
		return 0, err
	}
	// Field types differ across platforms (int64, uint32, uint64).
	return uint64(st.Bavail) * uint64(st.Bsize), nil //nolint:unconvert,gosec // platform-dependent types
}
