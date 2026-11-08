package scanner

import "os"

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
