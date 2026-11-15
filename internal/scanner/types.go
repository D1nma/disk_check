package scanner

import (
	"os"
	"time"
)

// walkEntry is one directory entry during a scan; produced by listDir,
// consumed by processDir.
type walkEntry struct {
	name    string
	size    int64
	modTime int64
	isDir   bool
	dev     uint64
	ino     uint64
	nlink   uint64
}

type ScanOptions struct {
	SameDevice bool     // like du -x: stay on same filesystem
	Excludes   []string // absolute paths to skip
	Workers    int      // maximum background directory workers; 0 selects automatically
	// ExpectedHint is the number of entries a previous scan of the same root
	// found; used for the progress percentage when statfs cannot tell.
	ExpectedHint int64
}

// ScanError is a directory (or some of its entries) the scan could not read.
type ScanError struct {
	Path string
	Err  string // short French description
}

type Node struct {
	Name      string
	Size      int64
	ModTime   int64 // unix seconds
	OwnSize   int64 // directory inode's own blocks; 0 for files
	Parent    *Node
	Children  []*Node
	FileCount int32
	DirCount  int32
	IsDir     bool
	Flags     uint8 // Flag* bits; fits in the padding after IsDir (no size cost)
}

// Node flags.
const (
	// FlagUnreadable: the directory (or some of its entries) could not be
	// read. Its Size is a lower bound.
	FlagUnreadable uint8 = 1 << iota
	// FlagHardlink: the file's inode has more than one link. Only one link
	// (the lexicographically smallest path seen) carries the size; the
	// others have Size 0.
	FlagHardlink
	// FlagMountRoot: the directory is the root of a filesystem other than
	// its parent's.
	FlagMountRoot
	// FlagPartial: some descendant directory is unreadable, so Size is a
	// lower bound. Set by the post-scan aggregation.
	FlagPartial
)

// Incomplete reports whether n's size may be underestimated because n or
// one of its descendants could not be read.
func (n *Node) Incomplete() bool {
	return n.Flags&(FlagUnreadable|FlagPartial) != 0
}

func (n *Node) Path() string {
	if n == nil {
		return ""
	}
	if n.Parent == nil {
		return n.Name
	}
	p := n.Parent.Path()
	if p == string(os.PathSeparator) {
		return p + n.Name
	}
	return p + string(os.PathSeparator) + n.Name
}

type ScanProgress struct {
	Files      int
	Dirs       int
	Size       int64
	Current    string
	Done       bool
	Expected   int64         // expected Files+Dirs when known (statfs or previous scan), else 0
	ErrorCount int           // directories with read errors so far
	Elapsed    time.Duration // time since the scan started

	// Set on the final message (Done) only.
	Err         error            // the root itself could not be read; Root is nil
	Root        *Node            // the complete, aggregated tree
	TopFiles    []*Node          // largest files, biggest first (up to 32)
	Errors      []ScanError      // first unreadable paths, sorted (ErrorCount has the total)
	DevBytes    map[uint64]int64 // bytes counted per filesystem (st_dev)
	Pinned      map[*Node]int64  // see Freeable
	Recent      map[*Node]int64  // directories: bytes of files modified since RecentSince
	RecentSince int64            // unix seconds
}
