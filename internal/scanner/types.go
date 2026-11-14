package scanner

import "os"

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
	Files    int
	Dirs     int
	Size     int64
	Current  string
	Done     bool
	Root     *Node
	TopFiles []*Node
}
