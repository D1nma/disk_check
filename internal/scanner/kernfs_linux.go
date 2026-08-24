//go:build linux

package scanner

import "golang.org/x/sys/unix"

func isKernFS(fsType int64) bool {
	switch fsType {
	case unix.PROC_SUPER_MAGIC,
		unix.SYSFS_MAGIC,
		unix.SECURITYFS_MAGIC,
		unix.CGROUP_SUPER_MAGIC,
		unix.CGROUP2_SUPER_MAGIC,
		unix.DEBUGFS_MAGIC,
		unix.TRACEFS_MAGIC,
		unix.PSTOREFS_MAGIC,
		unix.DEVPTS_SUPER_MAGIC:
		return true
	default:
		return false
	}
}
