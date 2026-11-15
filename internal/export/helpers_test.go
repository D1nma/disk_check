package export

import (
	"testing"

	"github.com/D1nma/disk_check/internal/scanner"
)

// dir builds a directory node owning kids; own is the inode's own blocks.
// Call testAggregate on the root once the tree is complete.
func dir(name string, own, mtime int64, flags uint8, kids ...*scanner.Node) *scanner.Node {
	d := &scanner.Node{Name: name, IsDir: true, OwnSize: own, ModTime: mtime, Flags: flags, Children: kids}
	for _, k := range kids {
		k.Parent = d
	}
	return d
}

func file(name string, size, mtime int64, flags uint8) *scanner.Node {
	return &scanner.Node{Name: name, Size: size, ModTime: mtime, Flags: flags, FileCount: 1}
}

// testAggregate is an independent recursive restatement of the scanner's
// post-scan pass (plus FlagPartial), used to build expected trees.
func testAggregate(n *scanner.Node) {
	if !n.IsDir {
		return
	}
	n.Size = n.OwnSize
	var files, dirs int64
	for _, c := range n.Children {
		testAggregate(c)
		n.Size += c.Size
		if c.IsDir {
			files += int64(c.FileCount)
			dirs += 1 + int64(c.DirCount)
		} else {
			files++
		}
		if c.Incomplete() {
			n.Flags |= scanner.FlagPartial
		}
	}
	n.FileCount = int32(files)
	n.DirCount = int32(dirs)
}

// compareTrees checks that got mirrors want field by field (flags under
// mask), children in the same order, and that got's Parent pointers are
// consistent.
func compareTrees(t *testing.T, want, got *scanner.Node, mask uint8) {
	t.Helper()
	type pair struct {
		w, g, gParent *scanner.Node
	}
	stack := []pair{{want, got, got.Parent}}
	for len(stack) > 0 {
		p := stack[len(stack)-1]
		stack = stack[:len(stack)-1]
		w, g := p.w, p.g
		path := w.Path()
		if g.Name != w.Name {
			t.Errorf("%s: Name = %q, want %q", path, g.Name, w.Name)
		}
		if g.Parent != p.gParent {
			t.Errorf("%s: wrong Parent pointer", path)
		}
		if g.IsDir != w.IsDir || g.Size != w.Size || g.OwnSize != w.OwnSize || g.ModTime != w.ModTime ||
			g.FileCount != w.FileCount || g.DirCount != w.DirCount || g.Flags&mask != w.Flags&mask {
			t.Errorf("%s:\n got dir=%v size=%d own=%d mtime=%d files=%d dirs=%d flags=%04b\nwant dir=%v size=%d own=%d mtime=%d files=%d dirs=%d flags=%04b",
				path, g.IsDir, g.Size, g.OwnSize, g.ModTime, g.FileCount, g.DirCount, g.Flags&mask,
				w.IsDir, w.Size, w.OwnSize, w.ModTime, w.FileCount, w.DirCount, w.Flags&mask)
		}
		if len(g.Children) != len(w.Children) {
			t.Errorf("%s: %d children, want %d", path, len(g.Children), len(w.Children))
			continue
		}
		for i := range w.Children {
			stack = append(stack, pair{w.Children[i], g.Children[i], g})
		}
	}
}

// summary is the comparable state of one node, keyed by path.
type summary struct {
	isDir       bool
	size, own   int64
	files, dirs int32
	flags       uint8
	parentOK    bool
}

// summarize flattens a tree into path → summary, checking Parent links.
func summarize(root *scanner.Node, mask uint8) map[string]summary {
	out := make(map[string]summary)
	var walk func(n *scanner.Node)
	walk = func(n *scanner.Node) {
		parentOK := true
		for _, c := range n.Children {
			if c.Parent != n {
				parentOK = false
			}
		}
		out[n.Path()] = summary{n.IsDir, n.Size, n.OwnSize, n.FileCount, n.DirCount, n.Flags & mask, parentOK}
		for _, c := range n.Children {
			walk(c)
		}
	}
	walk(root)
	return out
}
