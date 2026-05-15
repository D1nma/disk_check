package scanner

import (
	"sync"
	"time"
)

type Entry struct {
	Path    string
	Size    int64
	IsDir   bool
	ModTime time.Time
}

type ScanOptions struct {
	SameDevice bool     // like du -x: stay on same filesystem
	Excludes   []string // absolute paths to skip
}

type Node struct {
	Name      string
	Path      string
	Size      int64
	IsDir     bool
	ModTime   time.Time
	Parent    *Node
	Children  []*Node
	FileCount int
	DirCount  int
	mu        sync.Mutex // For thread-safe aggregation during parallel scan
}

type ScanProgress struct {
	Files   int
	Dirs    int
	Size    int64
	Current string
	Done    bool
	Root    *Node
}
