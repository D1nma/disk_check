package scanner

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"syscall"
	"time"
)

// Scan lists direct children of root and streams them as entries become ready.
// Directories appear once their cumulative disk size is computed (may take time).
// Files appear immediately.
func Scan(ctx context.Context, root string, opts ScanOptions) <-chan Entry {
	ch := make(chan Entry, 32)
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

		dirEntries, err := os.ReadDir(root)
		if err != nil {
			return
		}

		var wg sync.WaitGroup
		sem := make(chan struct{}, 4)

		for _, de := range dirEntries {
			if ctx.Err() != nil {
				break
			}
			path := filepath.Join(root, de.Name())
			if isExcluded(path, opts.Excludes) {
				continue
			}
			info, err := de.Info()
			if err != nil {
				continue
			}

			if !de.IsDir() {
				select {
				case ch <- Entry{
					Path:    path,
					Size:    blockSize(info),
					IsDir:   false,
					ModTime: info.ModTime(),
				}:
				case <-ctx.Done():
					return
				}
				continue
			}

			// Directories: compute recursive size in a goroutine, stream when ready.
			wg.Add(1)
			mt := info.ModTime()
			go func(dir string, modTime time.Time) {
				defer wg.Done()
				sem <- struct{}{}
				defer func() { <-sem }()
				size := sumDir(ctx, dir, rootDev, opts)
				select {
				case ch <- Entry{Path: dir, Size: size, IsDir: true, ModTime: modTime}:
				case <-ctx.Done():
				}
			}(path, mt)
		}
		wg.Wait()
	}()
	return ch
}

// sumDir returns the total disk usage of dir (recursive, following opts).
func sumDir(ctx context.Context, root string, rootDev uint64, opts ScanOptions) int64 {
	var total int64
	filepath.WalkDir(root, func(path string, d os.DirEntry, err error) error {
		if err != nil {
			return nil
		}
		if ctx.Err() != nil {
			return filepath.SkipAll
		}
		if d.IsDir() && path != root && isExcluded(path, opts.Excludes) {
			return filepath.SkipDir
		}
		info, err := d.Info()
		if err != nil {
			return nil
		}
		if opts.SameDevice && rootDev != 0 {
			if st, ok := info.Sys().(*syscall.Stat_t); ok && st.Dev != rootDev {
				if d.IsDir() {
					return filepath.SkipDir
				}
				return nil
			}
		}
		if !d.IsDir() {
			total += blockSize(info)
		}
		return nil
	})
	return total
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
