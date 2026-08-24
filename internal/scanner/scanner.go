package scanner

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"syscall"
	"time"
)

// Scan builds a full Node tree and reports progress via ScanProgress.
// It uses a fixed worker pool (64 workers) to bound goroutine count,
// avoiding unbounded goroutine creation on wide/deep filesystems.
func Scan(ctx context.Context, root string, opts ScanOptions) <-chan ScanProgress {
	ch := make(chan ScanProgress, 128)
	go func() {
		defer close(ch)

		rootInfo, err := os.Lstat(root)
		if err != nil {
			return
		}

		var rootDev uint64
		if opts.SameDevice {
			if st, ok := rootInfo.Sys().(*syscall.Stat_t); ok {
				rootDev = uint64(st.Dev)
			}
		}

		abs, absErr := filepath.Abs(root)
		if absErr != nil {
			abs = root
		}
		rootNode := &Node{
			Name:    abs,
			IsDir:   rootInfo.IsDir(),
			ModTime: rootInfo.ModTime().Unix(),
		}

		type dirTask struct {
			path string
			node *Node
		}

		const numWorkers = 64
		taskCh := make(chan dirTask, numWorkers*16)

		var totalFiles int64
		var totalDirs int64
		var totalSize int64
		var progressMu sync.Mutex
		var lastReport time.Time

		report := func(path string, final bool) {
			progressMu.Lock()
			now := time.Now()
			if final || now.Sub(lastReport) > 50*time.Millisecond {
				p := ScanProgress{
					Files:   int(atomic.LoadInt64(&totalFiles)),
					Dirs:    int(atomic.LoadInt64(&totalDirs)),
					Size:    atomic.LoadInt64(&totalSize),
					Current: path,
					Done:    final,
				}
				if final {
					p.Root = rootNode
					ch <- p
					lastReport = now
				} else {
					select {
					case ch <- p:
						lastReport = now
					default:
					}
				}
			}
			progressMu.Unlock()
		}

		var taskWg sync.WaitGroup

		// submit enqueues a directory task. If taskCh is full it spawns a
		// lightweight relay goroutine (send-only, no I/O) rather than blocking
		// the calling worker.
		submit := func(t dirTask) {
			taskWg.Add(1)
			select {
			case taskCh <- t:
			default:
				go func() {
					select {
					case taskCh <- t:
					case <-ctx.Done():
						taskWg.Done()
					}
				}()
			}
		}

		processDir := func(path string, node *Node) {
			defer taskWg.Done()

			entries, err := os.ReadDir(path)
			if err != nil {
				return
			}

			localChildren := make([]*Node, 0, len(entries))
			defer func() {
				node.Children = localChildren
			}()

			for _, entry := range entries {
				if ctx.Err() != nil {
					return
				}

				name := entry.Name()
				childPath := path + string(os.PathSeparator) + name
				if isExcluded(childPath, opts.Excludes) {
					continue
				}

				info, err := entry.Info()
				if err != nil {
					continue
				}

				if opts.SameDevice && rootDev != 0 {
					if st, ok := info.Sys().(*syscall.Stat_t); ok && uint64(st.Dev) != rootDev {
						continue
					}
				}

				child := &Node{
					Name:    name,
					IsDir:   entry.IsDir(),
					ModTime: info.ModTime().Unix(),
					Parent:  node,
				}
				localChildren = append(localChildren, child)

				if !entry.IsDir() {
					sz := blockSize(info)
					child.Size = sz
					child.FileCount = 1
					atomic.AddInt64(&totalFiles, 1)
					atomic.AddInt64(&totalSize, sz)
				} else {
					atomic.AddInt64(&totalDirs, 1)
					submit(dirTask{childPath, child})
				}

				if atomic.LoadInt64(&totalFiles)%100 == 0 {
					report(childPath, false)
				}
			}
		}

		var workerWg sync.WaitGroup
		for i := 0; i < numWorkers; i++ {
			workerWg.Add(1)
			go func() {
				defer workerWg.Done()
				for task := range taskCh {
					processDir(task.path, task.node)
				}
			}()
		}

		submit(dirTask{root, rootNode})
		go func() {
			taskWg.Wait()
			close(taskCh)
		}()

		workerWg.Wait()

		// Post-scan aggregation: compute cumulative sizes and counts in O(N).
		var aggregate func(n *Node) (int64, int, int)
		aggregate = func(n *Node) (int64, int, int) {
			if !n.IsDir {
				return n.Size, 1, 0
			}
			var sz int64
			var files, dirs int
			for _, child := range n.Children {
				s, f, d := aggregate(child)
				sz += s
				files += f
				dirs += d
			}
			n.Size = sz
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

		report(root, true)
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
