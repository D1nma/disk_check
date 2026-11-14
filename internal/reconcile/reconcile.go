// Package reconcile explains the gap between what df reports as used on a
// filesystem and what a scan of that filesystem actually found.
//
// The classic causes are, in order of how often they bite during a
// disk-full incident: files deleted while a process still holds them open
// (their blocks are only freed on the last close), directories the scan
// could not read, files hidden under a mount point, and filesystem
// metadata or snapshots. Stat and DeletedOpen collect the facts, Build
// computes the arithmetic and Render/Lines present it in French.
package reconcile

import (
	"path/filepath"
	"sort"
)

// FSInfo describes the filesystem holding a path.
type FSInfo struct {
	MountPoint string // longest mount point from /proc/self/mountinfo containing the path; "/" fallback
	Source     string // mount source, e.g. /dev/sda1 ("" if unknown)
	FSType     string // e.g. ext4, xfs, btrfs ("" if unknown)
	Dev        uint64 // st_dev of the mount point

	// Bytes, computed exactly like df: Used=(Blocks-Bfree)*bs,
	// Avail=Bavail*bs, Reserved=(Bfree-Bavail)*bs, where bs is the
	// fragment size (f_frsize, which equals f_bsize on virtually every
	// filesystem; f_bsize when the kernel does not report it).
	Total, Used, Avail, Reserved int64

	Inodes, InodesFree uint64
}

// DeletedFile is a regular file that has been deleted but is still held
// open, so its blocks are not freed.
type DeletedFile struct {
	PID      int    // lowest PID holding it (deterministic)
	Comm     string // /proc/PID/comm, trimmed
	FD       int    // fd number in that PID
	Holders  int    // number of (pid, fd) pairs holding this inode
	Path     string // readlink target with the trailing " (deleted)" removed
	Size     int64  // allocated bytes (st_blocks*512) of the open inode
	Dev, Ino uint64
}

// Report is the reconciliation of one filesystem.
type Report struct {
	FS           FSInfo
	Scanned      int64 // bytes the scan counted on this filesystem
	Covers       bool  // the scan started at FS.MountPoint, so it should have seen the whole filesystem
	Unreadable   int   // directories the scan could not read
	Deleted      []DeletedFile
	DeletedTotal int64
	Hidden       int   // processes not inspectable (from DeletedOpen)
	Gap          int64 // FS.Used - Scanned (signed; can be negative with compression/reflinks)
	Unexplained  int64 // Gap - DeletedTotal (signed)
}

// Build assembles a Report. deleted should hold the deleted-but-open files
// of fs (DeletedOpen with fs.Dev); Build keeps its own sorted copy, so the
// caller's slice is neither retained nor reordered.
func Build(fs FSInfo, scanned int64, covers bool, unreadable int, deleted []DeletedFile, hidden int) Report {
	r := Report{
		FS:         fs,
		Scanned:    scanned,
		Covers:     covers,
		Unreadable: unreadable,
		Hidden:     hidden,
	}
	if len(deleted) > 0 {
		r.Deleted = append([]DeletedFile(nil), deleted...)
		sortDeleted(r.Deleted)
	}
	for _, d := range r.Deleted {
		r.DeletedTotal += d.Size
	}
	r.Gap = fs.Used - scanned
	r.Unexplained = r.Gap - r.DeletedTotal
	return r
}

// StartsAtMount reports whether a scan rooted at root sees the whole
// filesystem described by fs, i.e. whether root is fs.MountPoint once both
// are made absolute and symlinks are resolved. It is the intended way to
// compute Build's covers argument.
func StartsAtMount(root string, fs FSInfo) bool {
	if fs.MountPoint == "" {
		return false
	}
	return canonical(root) == canonical(fs.MountPoint)
}

// canonical returns the absolute, cleaned, symlink-resolved form of p, or
// the best approximation when resolution fails.
func canonical(p string) string {
	abs, err := filepath.Abs(p)
	if err != nil {
		return filepath.Clean(p)
	}
	if real, err := filepath.EvalSymlinks(abs); err == nil {
		return real
	}
	return abs
}

// fromCounts converts statfs block counts into byte figures exactly as df
// does. Inconsistent counts (free > total) clamp to zero instead of going
// negative.
func fromCounts(blocks, bfree, bavail uint64, bs int64) FSInfo {
	if bs <= 0 {
		return FSInfo{}
	}
	b := uint64(bs)
	var used, reserved uint64
	if blocks > bfree {
		used = blocks - bfree
	}
	if bfree > bavail {
		reserved = bfree - bavail
	}
	return FSInfo{
		Total:    int64(blocks * b),
		Used:     int64(used * b),
		Avail:    int64(bavail * b),
		Reserved: int64(reserved * b),
	}
}

// sortDeleted orders by Size desc, then Path, then (PID, FD, Dev, Ino) so
// the order is total and deterministic.
func sortDeleted(files []DeletedFile) {
	sort.Slice(files, func(i, j int) bool {
		a, b := &files[i], &files[j]
		switch {
		case a.Size != b.Size:
			return a.Size > b.Size
		case a.Path != b.Path:
			return a.Path < b.Path
		case a.PID != b.PID:
			return a.PID < b.PID
		case a.FD != b.FD:
			return a.FD < b.FD
		case a.Dev != b.Dev:
			return a.Dev < b.Dev
		default:
			return a.Ino < b.Ino
		}
	})
}
