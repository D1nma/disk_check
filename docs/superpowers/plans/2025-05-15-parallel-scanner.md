# Parallel Scanner Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Implement a parallel scanner that builds a `Node` tree and reports progress via `ScanProgress`.

**Architecture:** Use `sync.WaitGroup` and a semaphore to walk the directory tree concurrently. Use `sync.Mutex` in `Node` to safely aggregate sizes and counts from children to ancestors.

**Tech Stack:** Go (standard library: `os`, `path/filepath`, `sync`, `context`)

---

### Task 1: Implement Parallel Scanner

**Files:**
- Modify: `disk_check/internal/scanner/scanner.go`

- [ ] **Step 1: Replace `Scan` function with the new implementation**

The new `Scan` function returns `<-chan ScanProgress` and builds a full `Node` tree.

```go
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
				}
				select {
				case ch <- p:
					lastReport = now
				default:
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
```

### Task 2: Update Tests and Verify Size Aggregation

**Files:**
- Modify: `disk_check/internal/scanner/scanner_test.go`

- [ ] **Step 1: Update tests to use the new `Scan` return type**
- [ ] **Step 2: Add a test specifically for size aggregation**

```go
func TestSizeAggregation(t *testing.T) {
    // ...
}
```

- [ ] **Step 3: Run tests**

Run: `go test ./internal/scanner/...`

### Task 3: Commit

- [ ] **Step 1: Commit the changes**

```bash
git commit -am "feat: implement parallel scanner with tree building"
```
