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

func listDir(_ *listCtx, path string, wantStat bool, fsHint int64, allowKern bool) ([]walkEntry, dirInfo, error) {
	var info dirInfo
	f, err := os.Open(path)
	if err != nil {
		return nil, info, err
	}
	defer f.Close()
	fd := int(f.Fd())

	var st unix.Stat_t
	if err := unix.Fstat(fd, &st); err == nil {
		info.size = int64(st.Blocks) * 512
		info.dev = uint64(st.Dev)
	}
	info.fsType = fsHint
	if wantStat {
		var stfs unix.Statfs_t
		if err := unix.Fstatfs(fd, &stfs); err == nil {
			info.fsType = int64(stfs.Type)
		}
	}
	// Same pseudo-filesystem shortcut as the Linux path: the caller drops
	// these entries, so skip stat-ing them (roots still list their entries).
	if !allowKern && info.fsType != 0 && isKernFS(info.fsType) {
		return nil, info, nil
	}

	names, err := f.Readdirnames(-1)
	if err != nil {
		return nil, info, err
	}
	entries := make([]walkEntry, 0, len(names))
	for _, name := range names {
		if name == "." || name == ".." {
			continue
		}
		var cst unix.Stat_t
		if err := unix.Fstatat(fd, name, &cst, unix.AT_SYMLINK_NOFOLLOW); err != nil {
			if err != unix.ENOENT {
				info.statErrs++
				if info.statErr == nil {
					info.statErr = err
				}
			}
			continue
		}
		isDir := cst.Mode&unix.S_IFMT == unix.S_IFDIR
		if isDir {
			info.subdirs++
		}
		sz := int64(0)
		if !isDir {
			sz = int64(cst.Blocks) * 512
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
	return entries, info, nil
}
