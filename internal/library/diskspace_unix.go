//go:build linux || darwin || freebsd

package library

import "golang.org/x/sys/unix"

func diskSpace(path string) (free, total uint64, err error) {
	var st unix.Statfs_t
	if err := unix.Statfs(path, &st); err != nil {
		return 0, 0, err
	}
	// Field types differ across platforms (int64, uint32, uint64).
	bsize := uint64(st.Bsize) //nolint:unconvert,gosec // portable across GOOS; block size is positive
	return uint64(st.Bavail) * bsize, uint64(st.Blocks) * bsize, nil //nolint:unconvert,gosec // see above
}
