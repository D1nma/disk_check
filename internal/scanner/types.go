package scanner

import "os"

type ScanOptions struct {
	SameDevice bool     // like du -x: stay on same filesystem
	Excludes   []string // absolute paths to skip
}

type Node struct {
	Name      string
	Size      int64
	ModTime   int64 // unix seconds
	Parent    *Node
	Children  []*Node
	FileCount int
	DirCount  int
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
	Files   int
	Dirs    int
	Size    int64
	Current string
	Done    bool
	Root    *Node
}
