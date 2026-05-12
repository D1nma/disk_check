package scanner

import (
	"context"
	"os"
	"path/filepath"
	"sync"
)

// Scan recursively scans the root directory using the specified number of concurrent workers.
// It returns a channel that will receive individual entries as they are discovered.
func Scan(ctx context.Context, root string, concurrency int) chan Entry {
	entries := make(chan Entry)
	var wg sync.WaitGroup
	sem := make(chan struct{}, concurrency)

	go func() {
		defer close(entries)
		wg.Add(1)
		scanDir(ctx, root, entries, &wg, sem)
		wg.Wait()
	}()

	return entries
}

func scanDir(ctx context.Context, path string, entries chan<- Entry, wg *sync.WaitGroup, sem chan struct{}) {
	defer wg.Done()

	select {
	case <-ctx.Done():
		return
	case sem <- struct{}{}:
		defer func() { <-sem }()
	}

	dirEntries, err := os.ReadDir(path)
	if err != nil {
		// For now, we ignore errors or could log them.
		// Real-time streaming focus.
		return
	}

	for _, e := range dirEntries {
		// Check context again inside loop for long directories
		select {
		case <-ctx.Done():
			return
		default:
		}

		info, err := e.Info()
		if err != nil {
			continue
		}
		entry := Entry{
			Path:    filepath.Join(path, e.Name()),
			Size:    info.Size(),
			IsDir:   e.IsDir(),
			ModTime: info.ModTime(),
		}
		entries <- entry
		if e.IsDir() {
			wg.Add(1)
			go scanDir(ctx, entry.Path, entries, wg, sem)
		}
	}
}
