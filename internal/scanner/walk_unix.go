//go:build unix && !linux

package scanner

import (
	"os"

	"golang.org/x/sys/unix"
)

// listCtx is intentionally empty on non-Linux unix; listDir keeps the
// simple allocation profile there. The release binaries target Linux and
// macOS only.
type listCtx struct{}

func listDir(_ *listCtx, path string, allowKernFS bool) (entries []walkEntry, dirSize int64, fsType int64, subdirs int, err error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, 0, 0, 0, err
	}
	defer f.Close()
	fd := int(f.Fd())

	var st unix.Stat_t
	if err := unix.Fstat(fd, &st); err == nil {
		dirSize = st.Blocks * 512
	}
	var stfs unix.Statfs_t
	if err := unix.Fstatfs(fd, &stfs); err == nil {
		fsType = int64(stfs.Type)
	}
	// Same pseudo-filesystem shortcut as the Linux path: the caller drops
	// these entries, so skip stat-ing them.
	if !allowKernFS && isKernFS(fsType) {
		return nil, dirSize, fsType, 0, nil
	}

	names, err := f.Readdirnames(-1)
	if err != nil {
		return nil, dirSize, fsType, 0, err
	}
	entries = make([]walkEntry, 0, len(names))
	for _, name := range names {
		if name == "." || name == ".." {
			continue
		}
		var cst unix.Stat_t
		if err := unix.Fstatat(fd, name, &cst, unix.AT_SYMLINK_NOFOLLOW); err != nil {
			continue
		}
		isDir := cst.Mode&unix.S_IFMT == unix.S_IFDIR
		if isDir {
			subdirs++
		}
		sz := int64(0)
		if !isDir {
			sz = cst.Blocks * 512
		}
		entries = append(entries, walkEntry{
			name:    name,
			size:    sz,
			modTime: mtimeSec(&cst),
			isDir:   isDir,
			dev:     uint64(cst.Dev),
			ino:     uint64(cst.Ino),
			nlink:   uint64(cst.Nlink),
		})
	}
	return entries, dirSize, fsType, subdirs, nil
}
