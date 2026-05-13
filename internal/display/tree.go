package display

import (
	"fmt"
	"io"
	"path/filepath"
	"strings"

	"github.com/D1nma/disk_check/internal/scanner"
)

func Tree(w io.Writer, node *scanner.TreeNode, maxDepth int) {
	fmt.Fprintf(w, "TREE SIZE VIEW (depth=%d) - %s\n\n", maxDepth, node.Path)
	printNode(w, node, node.Size, 0, true)
}

func printNode(w io.Writer, node *scanner.TreeNode, rootSize int64, depth int, isRoot bool) {
	var pct float64
	if rootSize > 0 {
		pct = float64(node.Size) * 100 / float64(rootSize)
	}

	name := filepath.Base(node.Path)
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

	fmt.Fprintf(w, "%10s  %5.1f%%  %s%s\n", FormatSize(node.Size), pct, prefix, name)

	for _, child := range node.Children {
		printNode(w, child, rootSize, depth+1, false)
	}
}
