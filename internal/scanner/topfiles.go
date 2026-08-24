package scanner

import "container/heap"

type fileHeap []*Node

func (h fileHeap) Len() int           { return len(h) }
func (h fileHeap) Less(i, j int) bool { return h[i].Size < h[j].Size }
func (h fileHeap) Swap(i, j int)      { h[i], h[j] = h[j], h[i] }
func (h *fileHeap) Push(x any)        { *h = append(*h, x.(*Node)) }
func (h *fileHeap) Pop() any {
	old := *h
	n := len(old)
	x := old[n-1]
	*h = old[:n-1]
	return x
}

// TopFiles returns up to limit largest files in n's subtree (in memory).
func TopFiles(n *Node, limit int) []*Node {
	if n == nil || limit <= 0 {
		return nil
	}
	h := &fileHeap{}
	heap.Init(h)
	var walk func(*Node)
	walk = func(cur *Node) {
		if cur == nil {
			return
		}
		if !cur.IsDir && cur.Size > 0 {
			if h.Len() < limit {
				heap.Push(h, cur)
			} else if (*h)[0].Size < cur.Size {
				heap.Pop(h)
				heap.Push(h, cur)
			}
		}
		for _, c := range cur.Children {
			walk(c)
		}
	}
	walk(n)
	out := make([]*Node, h.Len())
	for i := len(out) - 1; i >= 0; i-- {
		out[i] = heap.Pop(h).(*Node)
	}
	return out
}
