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
				rootDev = st.Dev
			}
		}

		rootNode := &Node{
			Name:    filepath.Base(root),
			Path:    root,
			IsDir:   rootInfo.IsDir(),
			ModTime: rootInfo.ModTime(),
		}

		var wg sync.WaitGroup
		sem := make(chan struct{}, 32)

		var totalFiles int32
		var totalDirs int32
		var totalSize int64
		var progressMu sync.Mutex
		var lastReport time.Time

		report := func(path string, isDir bool, size int64, final bool) {
			progressMu.Lock()
			if !final {
				if isDir {
					atomic.AddInt32(&totalDirs, 1)
				} else {
					atomic.AddInt32(&totalFiles, 1)
					atomic.AddInt64(&totalSize, size)
				}
			}

			now := time.Now()
			if final || now.Sub(lastReport) > 50*time.Millisecond {
				p := ScanProgress{
					Files:   int(atomic.LoadInt32(&totalFiles)),
					Dirs:    int(atomic.LoadInt32(&totalDirs)),
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

		var scanDir func(path string, node *Node)
		scanDir = func(path string, node *Node) {
			defer wg.Done()

			entries, err := os.ReadDir(path)
			if err != nil {
				return
			}

			for _, entry := range entries {
				if ctx.Err() != nil {
					return
				}

				info, err := entry.Info()
				if err != nil {
					continue
				}

				childPath := filepath.Join(path, entry.Name())
				if isExcluded(childPath, opts.Excludes) {
					continue
				}

				if opts.SameDevice && rootDev != 0 {
					if st, ok := info.Sys().(*syscall.Stat_t); ok && st.Dev != rootDev {
						continue
					}
				}

				child := &Node{
					Name:    entry.Name(),
					Path:    childPath,
					IsDir:   entry.IsDir(),
					ModTime: info.ModTime(),
					Parent:  node,
				}

				node.mu.Lock()
				node.Children = append(node.Children, child)
				node.mu.Unlock()

				if !entry.IsDir() {
					sz := blockSize(info)
					child.Size = sz
					child.FileCount = 1
					updateAncestors(child, sz, 1, 0)
					report(childPath, false, sz, false)
				} else {
					updateAncestors(child, 0, 0, 1)
					report(childPath, true, 0, false)
					wg.Add(1)
					go func(cp string, cn *Node) {
						sem <- struct{}{}
						defer func() { <-sem }()
						scanDir(cp, cn)
					}(childPath, child)
				}
			}
		}

		wg.Add(1)
		scanDir(root, rootNode)
		wg.Wait()

		report(root, true, 0, true)
	}()
	return ch
}

func updateAncestors(node *Node, size int64, files, dirs int) {
	curr := node.Parent
	for curr != nil {
		curr.mu.Lock()
		curr.Size += size
		curr.FileCount += files
		curr.DirCount += dirs
		curr.mu.Unlock()
		curr = curr.Parent
	}
}

// blockSize returns the actual disk usage of a file in bytes (512-byte blocks × 512).
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
				if st, ok := info.Sys().(*syscall.Stat_t); ok && st.Dev != rootDev {
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
