package scanner

import (
	"context"
	"os"
	"path/filepath"
	"sync"
)

// Scan recursively scans the root directory using the specified number of concurrent workers.
// It returns a receive-only channel that will receive individual entries as they are discovered.
func Scan(ctx context.Context, root string, concurrency int) <-chan Entry {
	entries := make(chan Entry, 64) // Buffered to improve performance
	var wg sync.WaitGroup
	sem := make(chan struct{}, concurrency)

	// Start the initial scan
	wg.Add(1)
	go scanDir(ctx, root, entries, &wg, sem)

	// Closer goroutine
	go func() {
		wg.Wait()
		close(entries)
	}()

	return entries
}

func scanDir(ctx context.Context, path string, entries chan<- Entry, wg *sync.WaitGroup, sem chan struct{}) {
	defer wg.Done()

	// Limit concurrency
	select {
	case <-ctx.Done():
		return
	case sem <- struct{}{}:
		defer func() { <-sem }()
	}

	dirEntries, err := os.ReadDir(path)
	if err != nil {
		return
	}

	for _, e := range dirEntries {
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

		// Send entry to channel with cancellation check to avoid leaks
		select {
		case entries <- entry:
		case <-ctx.Done():
			return
		}

		if e.IsDir() {
			wg.Add(1)
			go scanDir(ctx, entry.Path, entries, wg, sem)
		}
	}
}
