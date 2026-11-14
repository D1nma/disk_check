package display

import (
	"bufio"
	"fmt"
	"io"
	"sort"
	"strconv"

	"github.com/D1nma/disk_check/internal/scanner"
	"github.com/D1nma/disk_check/internal/textsafe"
)

// displaySize returns the tree-view size of a node: the subtree's file
// blocks, excluding the directory inode's own blocks (du-style content size).
func displaySize(n *scanner.Node) int64 {
	if !n.IsDir {
		return n.Size
	}
	return n.Size - n.OwnSize
}

// treeCtx carries reusable render buffers for one Tree call.
type treeCtx struct {
	bw   *bufio.Writer
	line []byte // reused output line
	pct  []byte // reused percentage field
	size []byte // reused size field
}

// appendSize writes the same bytes as FormatSize ("%.1f %s") without Sprintf.
// Scaling divides by powers of two only, so it is bit-identical to the
// repeated f /= 1024 loop of FormatSize for every int64 input.
func appendSize(dst []byte, size int64) []byte {
	const (
		kib = 1024.0
		mib = kib * 1024.0
		gib = mib * 1024.0
		tib = gib * 1024.0
	)
	f := float64(size)
	switch {
	case f >= tib:
		return appendScaled(dst, f/tib, "TiB")
	case f >= gib:
		return appendScaled(dst, f/gib, "GiB")
	case f >= mib:
		return appendScaled(dst, f/mib, "MiB")
	case f >= kib:
		return appendScaled(dst, f/kib, "KiB")
	default:
		return appendScaled(dst, f, "B")
	}
}

func appendScaled(dst []byte, f float64, unit string) []byte {
	dst = strconv.AppendFloat(dst, f, 'f', 1, 64)
	dst = append(dst, ' ')
	return append(dst, unit...)
}

func Tree(w io.Writer, node *scanner.Node, maxDepth int) {
	bw := bufio.NewWriter(w)
	defer bw.Flush()
	fmt.Fprintf(bw, "TREE SIZE VIEW (depth=%d) - %s\n\n", maxDepth, textsafe.Clean(node.Path()))
	tc := &treeCtx{bw: bw}
	printNode(tc, node, displaySize(node), nil, 0, true, false, maxDepth)
}

// printNode renders one node then, if depth allows, its children.
// prefix holds the continuation columns of the ancestors ("│   " or "    "
// per level below the root); isLast selects "└── " instead of "├── ".
func printNode(tc *treeCtx, node *scanner.Node, rootSize int64, prefix []byte, depth int, isRoot, isLast bool, maxDepth int) {
	size := displaySize(node)

	name := textsafe.Clean(node.Name)
	if isRoot {
		name = "."
	}
	if node.IsDir {
		name += "/"
	}

	// Line: "%10s  %5.1f%%  %s%s\n" without fmt.
	sz := appendSize(tc.size[:0], size)
	tc.size = sz
	line := tc.line[:0]
	for i := len(sz); i < 10; i++ {
		line = append(line, ' ')
	}
	line = append(line, sz...)
	line = append(line, ' ', ' ')

	if rootSize > 0 {
		tc.pct = strconv.AppendFloat(tc.pct[:0], float64(size)*100/float64(rootSize), 'f', 1, 64)
	} else {
		tc.pct = append(tc.pct[:0], '0', '.', '0')
	}
	for i := len(tc.pct); i < 5; i++ {
		line = append(line, ' ')
	}
	line = append(line, tc.pct...)
	line = append(line, '%', ' ', ' ')

	if depth > 0 {
		line = append(line, prefix...)
		if isLast {
			line = append(line, "└── "...)
		} else {
			line = append(line, "├── "...)
		}
	}
	line = append(line, name...)
	switch {
	case node.Flags&scanner.FlagUnreadable != 0:
		line = append(line, "  ⚠ illisible"...)
	case node.Flags&scanner.FlagPartial != 0:
		line = append(line, "  ⚠ incomplet"...)
	}
	line = append(line, '\n')
	tc.bw.Write(line)
	tc.line = line

	if depth >= maxDepth {
		return
	}
	if len(node.Children) == 0 {
		return
	}

	// Children are printed biggest-first. Copy to avoid reordering the
	// shared tree. The root's children have no continuation prefix.
	var childPrefix []byte
	if depth > 0 {
		childPrefix = append(make([]byte, 0, len(prefix)+6), prefix...)
		if isLast {
			childPrefix = append(childPrefix, "    "...)
		} else {
			childPrefix = append(childPrefix, "│   "...)
		}
	}
	kids := make([]*scanner.Node, len(node.Children))
	copy(kids, node.Children)
	sort.SliceStable(kids, func(i, j int) bool {
		return displaySize(kids[i]) > displaySize(kids[j])
	})
	for i, child := range kids {
		printNode(tc, child, rootSize, childPrefix, depth+1, false, i == len(kids)-1, maxDepth)
	}
}
