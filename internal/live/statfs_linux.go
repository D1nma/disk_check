//go:build linux

package live

import (
	"fmt"

	"golang.org/x/sys/unix"
)

// statfsUsage returns the used and available bytes of the filesystem
// holding path, the way df computes them.
//
// Statfs_t field types vary by architecture (Bsize is int32 on 32-bit,
// uint32 on s390x, int64 elsewhere), hence the explicit conversions.
// Block counts are in fragment-size units (f_frsize, as statvfs and df use);
// it equals f_bsize on nearly every filesystem, which is the fallback.
func statfsUsage(path string) (used, avail int64, err error) {
	var st unix.Statfs_t
	if err := unix.Statfs(path, &st); err != nil {
		return 0, 0, fmt.Errorf("live: statfs %s: %w", path, err)
	}
	bs := int64(st.Frsize)
	if bs <= 0 {
		bs = int64(st.Bsize)
	}
	used, avail = usage(uint64(st.Blocks), uint64(st.Bfree), uint64(st.Bavail), bs)
	return used, avail, nil
}
