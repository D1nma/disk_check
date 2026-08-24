package scanner

import (
	"container/heap"
	"context"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"sync/atomic"
	"syscall"
	"time"
)

func workerCount() int {
	n := runtime.GOMAXPROCS(0) * 2
	if n > 16 {
		return 16
	}
	if n < 2 {
		return 2
	}
	return n
}

type inodeKey struct {
	dev uint64
	ino uint64
}

type dirTask struct {
	path string
	node *Node
}

func joinPath(dir, name string) string {
	if dir == string(os.PathSeparator) {
		return dir + name
	}
	return dir + string(os.PathSeparator) + name
}

type nodeArena struct {
	cur []Node
	i   int
}

func (a *nodeArena) new() *Node {
	if a.i >= len(a.cur) {
		a.cur = make([]Node, 1024)
		a.i = 0
	}
	n := &a.cur[a.i]
	a.i++
	return n
}

// Scan builds a full Node tree and reports progress via ScanProgress.
func Scan(ctx context.Context, root string, opts ScanOptions) <-chan ScanProgress {
	ch := make(chan ScanProgress, 16)
	go func() {
		defer close(ch)

		abs, err := filepath.Abs(root)
		if err != nil {
			abs = root
		}
		rootInfo, err := os.Lstat(abs)
		if err != nil {
			return
		}

		var rootDev uint64
		if opts.SameDevice {
			if st, ok := rootInfo.Sys().(*syscall.Stat_t); ok {
				rootDev = uint64(st.Dev)
			}
		}

		rootNode := &Node{
			Name:    abs,
			IsDir:   rootInfo.IsDir(),
			ModTime: rootInfo.ModTime().Unix(),
		}
		if !rootNode.IsDir {
			rootNode.Size = blockSize(rootInfo)
			rootNode.FileCount = 1
			ch <- ScanProgress{Files: 1, Size: rootNode.Size, Done: true, Root: rootNode}
			return
		}

		var totalFiles int64
		var totalDirs int64
		var totalSize int64
		var current atomic.Value
		current.Store(abs)

		var seenMu sync.Mutex
		seen := make(map[inodeKey]struct{})

		nWorkers := workerCount()
		taskCh := make(chan dirTask, nWorkers*4)
		var taskWg sync.WaitGroup

		var processDir func(path string, node *Node, isRoot bool, arena *nodeArena)
		processDir = func(path string, node *Node, isRoot bool, arena *nodeArena) {
			defer taskWg.Done()
			if ctx.Err() != nil {
				return
			}
			current.Store(path)

			ents, dirSize, fsType, err := listDir(path)
			if err != nil {
				return
			}
			if !isRoot && isKernFS(fsType) {
				return
			}
			node.Size = dirSize

			localChildren := make([]*Node, 0, len(ents))
			var leftover []dirTask
			var localFiles, localDirs, localSize int64
			hasExcludes := len(opts.Excludes) > 0

			for _, ent := range ents {
				if ctx.Err() != nil {
					break
				}
				if opts.SameDevice && rootDev != 0 && ent.dev != rootDev {
					continue
				}

				var childPath string
				if ent.isDir || hasExcludes {
					childPath = joinPath(path, ent.name)
					if hasExcludes && isExcluded(childPath, opts.Excludes) {
						continue
					}
				}

				child := arena.new()
				*child = Node{
					Name:    ent.name,
					IsDir:   ent.isDir,
					ModTime: ent.modTime,
					Parent:  node,
				}

				if !ent.isDir {
					sz := ent.size
					if ent.nlink > 1 {
						k := inodeKey{ent.dev, ent.ino}
						seenMu.Lock()
						if _, ok := seen[k]; ok {
							sz = 0
						} else {
							seen[k] = struct{}{}
						}
						seenMu.Unlock()
					}
					child.Size = sz
					child.FileCount = 1
					localFiles++
					localSize += sz
				} else {
					localDirs++
					taskWg.Add(1)
					t := dirTask{childPath, child}
					select {
					case taskCh <- t:
					default:
						leftover = append(leftover, t)
					}
				}
				localChildren = append(localChildren, child)
			}
			if localFiles != 0 {
				atomic.AddInt64(&totalFiles, localFiles)
			}
			if localDirs != 0 {
				atomic.AddInt64(&totalDirs, localDirs)
			}
			if localSize != 0 {
				atomic.AddInt64(&totalSize, localSize)
			}
			node.Children = localChildren
			for _, t := range leftover {
				processDir(t.path, t.node, false, arena)
			}
		}

		stopProgress := make(chan struct{})
		var progressWg sync.WaitGroup
		progressWg.Add(1)
		go func() {
			defer progressWg.Done()
			tick := time.NewTicker(100 * time.Millisecond)
			defer tick.Stop()
			for {
				select {
				case <-stopProgress:
					return
				case <-tick.C:
					cur, _ := current.Load().(string)
					p := ScanProgress{
						Files:   int(atomic.LoadInt64(&totalFiles)),
						Dirs:    int(atomic.LoadInt64(&totalDirs)),
						Size:    atomic.LoadInt64(&totalSize),
						Current: cur,
					}
					select {
					case ch <- p:
					default:
					}
				}
			}
		}()

		var workerWg sync.WaitGroup
		for i := 0; i < nWorkers; i++ {
			workerWg.Add(1)
			go func() {
				defer workerWg.Done()
				var arena nodeArena
				for t := range taskCh {
					processDir(t.path, t.node, false, &arena)
				}
			}()
		}

		taskWg.Add(1)
		go func() {
			taskWg.Wait()
			close(taskCh)
		}()
		var rootArena nodeArena
		processDir(abs, rootNode, true, &rootArena)
		workerWg.Wait()
		close(stopProgress)
		progressWg.Wait()

		const topCap = 32
		topH := &fileHeap{}
		heap.Init(topH)
		var aggregate func(n *Node)
		aggregate = func(n *Node) {
			if !n.IsDir {
				if n.Size > 0 {
					if topH.Len() < topCap {
						heap.Push(topH, n)
					} else if (*topH)[0].Size < n.Size {
						heap.Pop(topH)
						heap.Push(topH, n)
					}
				}
				return
			}
			own := n.Size
			var files int
			var sz int64
			for _, child := range n.Children {
				aggregate(child)
				sz += child.Size
				if child.IsDir {
					files += child.FileCount
				} else {
					files++
				}
			}
			n.Size = own + sz
			n.FileCount = files
			dirCount := 0
			for _, child := range n.Children {
				if child.IsDir {
					dirCount += 1 + child.DirCount
				}
			}
			n.DirCount = dirCount
		}
		aggregate(rootNode)
		topFiles := make([]*Node, topH.Len())
		for i := len(topFiles) - 1; i >= 0; i-- {
			topFiles[i] = heap.Pop(topH).(*Node)
		}

		cur, _ := current.Load().(string)
		ch <- ScanProgress{
			Files:    int(atomic.LoadInt64(&totalFiles)),
			Dirs:     int(atomic.LoadInt64(&totalDirs)),
			Size:     rootNode.Size,
			Current:  cur,
			Done:     true,
			Root:     rootNode,
			TopFiles: topFiles,
		}
	}()
	return ch
}

func blockSize(info os.FileInfo) int64 {
	if st, ok := info.Sys().(*syscall.Stat_t); ok {
		return st.Blocks * 512
	}
	return info.Size()
}

func isExcluded(path string, excludes []string) bool {
	for _, ex := range excludes {
		if path == ex || strings.HasPrefix(path, ex+string(os.PathSeparator)) {
			return true
		}
	}
	return false
}

func sumDir(ctx context.Context, root string, rootDev uint64, opts ScanOptions) int64 {
	var total int64
	filepath.WalkDir(root, func(path string, d os.DirEntry, err error) error {
		if err != nil || ctx.Err() != nil {
			return nil
		}
		if isExcluded(path, opts.Excludes) {
			if d.IsDir() {
				return filepath.SkipDir
			}
			return nil
		}
		if opts.SameDevice && rootDev != 0 {
			if info, err := d.Info(); err == nil {
				if st, ok := info.Sys().(*syscall.Stat_t); ok && uint64(st.Dev) != rootDev {
					if d.IsDir() {
						return filepath.SkipDir
					}
					return nil
				}
			}
		}
		if !d.IsDir() {
			if info, err := d.Info(); err == nil {
				total += blockSize(info)
			}
		}
		return nil
	})
	return total
}
