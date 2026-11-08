package display

import (
	"fmt"
	"io"
	"sort"
	"strings"

	"github.com/D1nma/disk_check/internal/scanner"
)

// displaySize returns the tree-view size of a node: the subtree's file
// blocks, excluding the directory inode's own blocks (du-style content size).
func displaySize(n *scanner.Node) int64 {
	if !n.IsDir {
		return n.Size
	}
	return n.Size - n.OwnSize
}

func Tree(w io.Writer, node *scanner.Node, maxDepth int) {
	fmt.Fprintf(w, "TREE SIZE VIEW (depth=%d) - %s\n\n", maxDepth, node.Path())
	printNode(w, node, displaySize(node), 0, true, maxDepth)
}

func printNode(w io.Writer, node *scanner.Node, rootSize int64, depth int, isRoot bool, maxDepth int) {
	size := displaySize(node)
	var pct float64
	if rootSize > 0 {
		pct = float64(size) * 100 / float64(rootSize)
	}

	name := node.Name
	if isRoot {
		name = "."
	}
	if node.IsDir {
		name += "/"
	}

	var prefix string
	if depth == 0 {
		prefix = ""
	} else {
		prefix = strings.Repeat("  ", depth-1) + "├── "
	}

	fmt.Fprintf(w, "%10s  %5.1f%%  %s%s\n", FormatSize(size), pct, prefix, name)

	if depth >= maxDepth {
		return
	}

	// Print children biggest-first. Copy to avoid reordering the shared tree.
	kids := make([]*scanner.Node, len(node.Children))
	copy(kids, node.Children)
	sort.Slice(kids, func(i, j int) bool {
		return displaySize(kids[i]) > displaySize(kids[j])
	})
	for _, child := range kids {
		printNode(w, child, rootSize, depth+1, false, maxDepth)
	}
}
