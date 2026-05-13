package scanner

import (
	"context"
	"os"
	"path/filepath"
	"sort"
	"syscall"
)

// ScanTopFiles returns up to n largest files under root, sorted by size descending.
func ScanTopFiles(ctx context.Context, root string, n int, opts ScanOptions) []Entry {
	var files []Entry

	rootInfo, err := os.Lstat(root)
	if err != nil {
		return nil
	}
	var rootDev uint64
	if opts.SameDevice {
		if st, ok := rootInfo.Sys().(*syscall.Stat_t); ok {
			rootDev = st.Dev
		}
	}

	filepath.WalkDir(root, func(path string, d os.DirEntry, err error) error {
		if err != nil || ctx.Err() != nil {
			return nil
		}
		if d.IsDir() {
			if path != root && isExcluded(path, opts.Excludes) {
				return filepath.SkipDir
			}
			return nil
		}
		info, err := d.Info()
		if err != nil {
			return nil
		}
		if opts.SameDevice && rootDev != 0 {
			if st, ok := info.Sys().(*syscall.Stat_t); ok && st.Dev != rootDev {
				return nil
			}
		}
		files = append(files, Entry{
			Path:    path,
			Size:    blockSize(info),
			IsDir:   false,
			ModTime: info.ModTime(),
		})
		return nil
	})

	sort.Slice(files, func(i, j int) bool {
		return files[i].Size > files[j].Size
	})

	if n > 0 && len(files) > n {
		return files[:n]
	}
	return files
}
