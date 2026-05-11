package scanner

import (
    "os"
    "path/filepath"
    "sync"
)

// Scan recursively scans the root directory using the specified number of concurrent workers.
// It returns a channel that will receive ScanResults as directories are processed.
func Scan(root string, concurrency int) <-chan ScanResult {
    results := make(chan ScanResult)
    var wg sync.WaitGroup
    sem := make(chan struct{}, concurrency)

    go func() {
        defer close(results)
        wg.Add(1)
        scanDir(root, results, &wg, sem)
        wg.Wait()
    }()

    return results
}

func scanDir(path string, results chan<- ScanResult, wg *sync.WaitGroup, sem chan struct{}) {
    defer wg.Done()
    
    // Acquire semaphore
    sem <- struct{}{}
    defer func() { <-sem }()

    entries, err := os.ReadDir(path)
    if err != nil {
        results <- ScanResult{Error: err}
        return
    }

    var resultEntries []Entry
    for _, e := range entries {
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
        resultEntries = append(resultEntries, entry)
        if e.IsDir() {
            wg.Add(1)
            go scanDir(entry.Path, results, wg, sem)
        }
    }
    results <- ScanResult{Entries: resultEntries}
}
