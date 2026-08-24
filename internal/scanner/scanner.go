package scanner

import (
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

		var processDir func(path string, node *Node, isRoot bool)
		processDir = func(path string, node *Node, isRoot bool) {
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

			for _, ent := range ents {
				if ctx.Err() != nil {
					break
				}
				childPath := joinPath(path, ent.name)
				if isExcluded(childPath, opts.Excludes) {
					continue
				}
				if opts.SameDevice && rootDev != 0 && ent.dev != rootDev {
					continue
				}

				child := &Node{
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
					atomic.AddInt64(&totalFiles, 1)
					atomic.AddInt64(&totalSize, sz)
				} else {
					atomic.AddInt64(&totalDirs, 1)
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
			node.Children = localChildren
			for _, t := range leftover {
				processDir(t.path, t.node, false)
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
				for t := range taskCh {
					processDir(t.path, t.node, false)
				}
			}()
		}

		taskWg.Add(1)
		go func() {
			taskWg.Wait()
			close(taskCh)
		}()
		processDir(abs, rootNode, true)
		workerWg.Wait()
		close(stopProgress)
		progressWg.Wait()

		var aggregate func(n *Node) (int64, int, int)
		aggregate = func(n *Node) (int64, int, int) {
			if !n.IsDir {
				return n.Size, 1, 0
			}
			own := n.Size
			var files, dirs int
			var sz int64
			for _, child := range n.Children {
				s, f, d := aggregate(child)
				sz += s
				files += f
				dirs += d
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
			return n.Size, n.FileCount, n.DirCount
		}
		aggregate(rootNode)

		cur, _ := current.Load().(string)
		ch <- ScanProgress{
			Files:   int(atomic.LoadInt64(&totalFiles)),
			Dirs:    int(atomic.LoadInt64(&totalDirs)),
			Size:    rootNode.Size,
			Current: cur,
			Done:    true,
			Root:    rootNode,
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
