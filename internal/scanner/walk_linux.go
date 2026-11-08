//go:build linux

package scanner

import (
	"bytes"
	"unsafe"

	"golang.org/x/sys/unix"
)

const (
	direntBufSize = 64 << 10 // getdents64 buffer reused per worker
	nameSlabSize  = 32 << 10 // name chunk per worker; strings point into it
	direntNameOff = 19       // offset of d_name inside linux_dirent64
)

// entBufMax caps the per-worker reusable entry buffer. Directories larger
// than this fall back to a transient allocation, matching the old profile.
var entBufMax = 16384

// listCtx holds per-worker buffers. Nothing here is safe for concurrent use:
// one listCtx belongs to exactly one scan worker. The entries returned by
// listDir stay valid until the next listDir call on the same context, which
// is how processDir consumes them (fully read before recursing).
type listCtx struct {
	dirent  []byte      // raw getdents64 buffer
	entries []walkEntry // reusable entry slice, capped at entBufMax
	chunks  [][]byte    // completed name chunks, kept alive with the tree
	slab    []byte      // current name chunk (still has room)
	slabLen int         // bytes used in the current chunk
}

// slabString stores a copy of name in the worker's name chunk and returns a
// string pointing into that chunk. Chunks are never moved or reused, so the
// returned string stays valid for the life of the tree.
func (lc *listCtx) slabString(name []byte) string {
	if lc.slab == nil || lc.slabLen+len(name) > len(lc.slab) {
		chunk := make([]byte, nameSlabSize)
		lc.chunks = append(lc.chunks, chunk)
		lc.slab = chunk
		lc.slabLen = 0
	}
	start := lc.slabLen
	copy(lc.slab[start:], name)
	lc.slabLen = start + len(name)
	return unsafe.String(&lc.slab[start], len(name))
}

func listDir(lc *listCtx, path string, allowKernFS bool) (entries []walkEntry, dirSize int64, fsType int64, subdirs int, err error) {
	fd, err := unix.Open(path, unix.O_RDONLY|unix.O_DIRECTORY|unix.O_CLOEXEC, 0)
	if err != nil {
		return nil, 0, 0, 0, err
	}
	defer unix.Close(fd)

	var st unix.Stat_t
	if err := unix.Fstat(fd, &st); err != nil {
		return nil, 0, 0, 0, err
	}
	dirSize = st.Blocks * 512
	var stfs unix.Statfs_t
	if err := unix.Fstatfs(fd, &stfs); err != nil {
		return nil, dirSize, 0, 0, err
	}
	fsType = int64(stfs.Type)
	// Pseudo-filesystems (proc, sysfs, ...) are dropped by the caller anyway;
	// skip their entries instead of stat-ing them first.
	if !allowKernFS && isKernFS(fsType) {
		return nil, dirSize, fsType, 0, nil
	}

	if lc.dirent == nil {
		lc.dirent = make([]byte, direntBufSize)
	}
	ents := lc.entries[:0]
	overflow := false
	for {
		n, err := unix.Getdents(fd, lc.dirent)
		if err != nil {
			return nil, dirSize, fsType, subdirs, err
		}
		if n == 0 {
			break
		}
		buf := lc.dirent[:n]
		for off := 0; off+18 < len(buf); {
			reclen := int(buf[off+16]) | int(buf[off+17])<<8
			if off+reclen > len(buf) || reclen < direntNameOff+2 {
				break
			}
			rec := buf[off : off+reclen]
			off += reclen

			// d_name is NUL-terminated inside the record.
			name := rec[direntNameOff:]
			if i := bytes.IndexByte(name, 0); i >= 0 {
				name = name[:i]
			} else {
				continue
			}
			if len(name) == 0 {
				continue
			}
			if name[0] == '.' && (len(name) == 1 || (len(name) == 2 && name[1] == '.')) {
				continue
			}

			var cst unix.Stat_t
			if err := fstatatRaw(fd, rec[direntNameOff:], &cst, unix.AT_SYMLINK_NOFOLLOW); err != nil {
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
			e := walkEntry{
				name:    lc.slabString(name),
				size:    sz,
				modTime: mtimeSec(&cst),
				isDir:   isDir,
				dev:     uint64(cst.Dev),
				ino:     uint64(cst.Ino),
				nlink:   uint64(cst.Nlink),
			}
			if !overflow && len(ents) == entBufMax {
				// Huge directory: switch to a transient slice for this one,
				// keep the reusable buffer capped.
				big := make([]walkEntry, entBufMax, 2*entBufMax)
				copy(big, ents)
				ents = big
				overflow = true
			}
			ents = append(ents, e)
		}
	}
	if overflow {
		lc.entries = lc.entries[:0]
		return ents, dirSize, fsType, subdirs, nil
	}
	lc.entries = ents // len kept for reuse; entries point into lc.entries' array
	return ents, dirSize, fsType, subdirs, nil
}
