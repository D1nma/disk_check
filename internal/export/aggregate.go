package export

import "github.com/D1nma/disk_check/internal/scanner"

// aggregateDir finalizes a directory whose children are already final,
// mirroring the scanner's post-scan pass: Size becomes the directory's own
// blocks plus its children's sizes, FileCount the number of non-directory
// descendants, DirCount the number of descendant directories, and
// FlagPartial is set when a child is unreadable or partial.
//
// Like the scanner, counts are accumulated in int64 and truncated to int32.
func aggregateDir(n *scanner.Node) {
	var size, files, dirs int64
	partial := false
	for _, c := range n.Children {
		size += c.Size
		if c.IsDir {
			files += int64(c.FileCount)
			dirs += 1 + int64(c.DirCount)
		} else {
			files++
		}
		if c.Flags&(scanner.FlagUnreadable|scanner.FlagPartial) != 0 {
			partial = true
		}
	}
	n.Size = n.OwnSize + size
	n.FileCount = int32(files)
	n.DirCount = int32(dirs)
	if partial {
		n.Flags |= scanner.FlagPartial
	}
}

// nodeArena hands out Nodes from slabs to avoid one allocation per entry.
// Slabs start small and double up to maxSlab, so tiny trees stay tiny.
type nodeArena struct {
	cur  []scanner.Node // unused part of the current slab
	slab int            // size of the current slab
}

const maxSlab = 1024

func (a *nodeArena) new() *scanner.Node {
	if len(a.cur) == 0 {
		a.slab = min(max(2*a.slab, 16), maxSlab)
		a.cur = make([]scanner.Node, a.slab)
	}
	n := &a.cur[0]
	a.cur = a.cur[1:]
	return n
}
