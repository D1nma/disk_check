package scanner

import (
	"context"
	"os"
	"path/filepath"
	"sort"
	"syscall"
)

func ScanTree(ctx context.Context, root string, maxDepth int, opts ScanOptions) (*Node, error) {
	rootInfo, err := os.Lstat(root)
	if err != nil {
		return nil, err
	}
	var rootDev uint64
	if opts.SameDevice {
		if st, ok := rootInfo.Sys().(*syscall.Stat_t); ok {
			rootDev = st.Dev
		}
	}
	node := buildTreeNode(ctx, root, 0, maxDepth, rootDev, opts)
	return node, nil
}

func buildTreeNode(ctx context.Context, path string, depth, maxDepth int, rootDev uint64, opts ScanOptions) *Node {
	node := &Node{
		Name:  filepath.Base(path),
		Path:  path,
		IsDir: true,
	}

	if ctx.Err() != nil || depth >= maxDepth {
		node.Size = sumDir(ctx, path, rootDev, opts)
		return node
	}

	entries, err := os.ReadDir(path)
	if err != nil {
		node.Size = sumDir(ctx, path, rootDev, opts)
		return node
	}

	for _, de := range entries {
		if ctx.Err() != nil {
			break
		}
		childPath := filepath.Join(path, de.Name())
		if isExcluded(childPath, opts.Excludes) {
			continue
		}
		info, err := de.Info()
		if err != nil {
			continue
		}
		if opts.SameDevice && rootDev != 0 {
			if st, ok := info.Sys().(*syscall.Stat_t); ok && st.Dev != rootDev {
				continue
			}
		}

		if de.IsDir() {
			child := buildTreeNode(ctx, childPath, depth+1, maxDepth, rootDev, opts)
			child.Parent = node
			node.Children = append(node.Children, child)
			node.Size += child.Size
		} else {
			sz := blockSize(info)
			node.Children = append(node.Children, &Node{
				Name:    de.Name(),
				Path:    childPath,
				Size:    sz,
				IsDir:   false,
				ModTime: info.ModTime(),
				Parent:  node,
			})
			node.Size += sz
		}
	}

	sort.Slice(node.Children, func(i, j int) bool {
		return node.Children[i].Size > node.Children[j].Size
	})

	return node
}
