package scanner

import (
	"container/heap"
	"sort"
)

// Analysis is what Analyze recomputes from an already aggregated tree,
// typically one imported from an export or a remote scan.
type Analysis struct {
	TopFiles   []*Node
	Recent     map[*Node]int64 // directories: bytes of files modified since the cutoff
	Errors     []ScanError     // unreadable directories (FlagUnreadable), sorted, capped
	ErrorCount int
}

// Analyze walks root without changing sizes: the largest files (up to
// topN), recently modified bytes per directory and unreadable directories.
func Analyze(root *Node, recentSince int64, topN int) Analysis {
	res := Analysis{Recent: make(map[*Node]int64)}
	if root == nil {
		return res
	}
	h := &fileHeap{}
	var walk func(n *Node) int64
	walk = func(n *Node) int64 {
		if !n.IsDir {
			if n.Size > 0 {
				if h.Len() < topN {
					heap.Push(h, n)
				} else if topN > 0 && (*h)[0].Size < n.Size {
					heap.Pop(h)
					heap.Push(h, n)
				}
				if n.ModTime >= recentSince {
					return n.Size
				}
			}
			return 0
		}
		if n.Flags&FlagUnreadable != 0 {
			res.ErrorCount++
			if len(res.Errors) < maxErrors {
				res.Errors = append(res.Errors, ScanError{Path: n.Path(), Err: "illisible lors du scan"})
			}
		}
		var rec int64
		for _, c := range n.Children {
			rec += walk(c)
		}
		if rec > 0 {
			res.Recent[n] = rec
		}
		return rec
	}
	walk(root)
	res.TopFiles = make([]*Node, h.Len())
	for i := len(res.TopFiles) - 1; i >= 0; i-- {
		res.TopFiles[i] = heap.Pop(h).(*Node)
	}
	sort.Slice(res.Errors, func(i, j int) bool { return res.Errors[i].Path < res.Errors[j].Path })
	return res
}
