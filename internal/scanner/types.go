package scanner

import "time"

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
