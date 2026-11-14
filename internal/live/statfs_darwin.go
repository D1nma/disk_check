//go:build darwin

package live

import (
	"fmt"

	"golang.org/x/sys/unix"
)

// statfsUsage returns the used and available bytes of the filesystem
// holding path. On darwin f_bsize is the fundamental block size the counts
// are expressed in (f_iosize is the optimal transfer size).
func statfsUsage(path string) (used, avail int64, err error) {
	var st unix.Statfs_t
	if err := unix.Statfs(path, &st); err != nil {
		return 0, 0, fmt.Errorf("live: statfs %s: %w", path, err)
	}
	used, avail = usage(uint64(st.Blocks), uint64(st.Bfree), uint64(st.Bavail), int64(st.Bsize))
	return used, avail, nil
}
