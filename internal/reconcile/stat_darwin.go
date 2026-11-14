//go:build darwin

package reconcile

import (
	"os"

	"golang.org/x/sys/unix"
)

// Stat describes the filesystem holding path. macOS has no mountinfo, but
// statfs(2) itself reports the mount point, source and type.
func Stat(path string) (FSInfo, error) {
	abs := canonical(path)
	var sfs unix.Statfs_t
	if err := unix.Statfs(abs, &sfs); err != nil {
		return FSInfo{}, &os.PathError{Op: "statfs", Path: path, Err: err}
	}
	info := fromCounts(uint64(sfs.Blocks), uint64(sfs.Bfree), uint64(sfs.Bavail), int64(sfs.Bsize))
	info.Inodes, info.InodesFree = uint64(sfs.Files), uint64(sfs.Ffree)
	info.MountPoint = unix.ByteSliceToString(sfs.Mntonname[:])
	info.Source = unix.ByteSliceToString(sfs.Mntfromname[:])
	info.FSType = unix.ByteSliceToString(sfs.Fstypename[:])
	if info.MountPoint == "" {
		info.MountPoint = "/"
	}
	var st unix.Stat_t
	if err := unix.Stat(info.MountPoint, &st); err == nil {
		info.Dev = uint64(uint32(st.Dev)) // int32 on darwin: no sign extension
	}
	return info, nil
}
