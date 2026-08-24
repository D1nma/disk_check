# Fast Scan and Local Report Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Make `Scan()` faster than ncdu 2 (1-thread and `-t`) via fd-relative walking and compact nodes, and show a local size report for the current directory in the TUI and `--summary`/`--report`, from a single disk walk.

**Architecture:** Replace `os.ReadDir`+`Info()` with `open`/`Readdirnames`/`fstatat` on a ≤16 worker pool. Store basename only (`Path()` reconstructed). Post-scan aggregation includes directory inode blocks. Hardlinks with `nlink>1` are counted once. `--summary` uses in-memory `TopFiles`. TUI adds a Here line and per-child percent of the current directory.

**Tech Stack:** Go 1.25, `golang.org/x/sys/unix`, existing Bubble Tea TUI. No CGO, no `io_uring`.

**Spec:** `docs/superpowers/specs/2026-08-24-fast-scan-and-local-report-design.md`

---

## File map

| File | Responsibility |
|------|----------------|
| `internal/scanner/types.go` | Compact `Node`, `Path()` |
| `internal/scanner/walk_unix.go` | `listDir`: open + Readdirnames + Fstatat |
| `internal/scanner/mtime_linux.go` | `mtimeSec` from `Stat_t.Mtim` |
| `internal/scanner/mtime_darwin.go` | `mtimeSec` from `Stat_t.Mtimespec` |
| `internal/scanner/kernfs_linux.go` | `isKernFS(fsType int64) bool` |
| `internal/scanner/kernfs_stub.go` | `isKernFS` always false (non-Linux) |
| `internal/scanner/scanner.go` | `Scan`, workers, progress tick, aggregate, hardlinks |
| `internal/scanner/topfiles.go` | In-memory `TopFiles` |
| `internal/scanner/tree.go` | Compile against compact `Node` only (algorithm unchanged) |
| `internal/scanner/scanner_test.go` | Path, TopFiles, hardlink, exclude, aggregation |
| `internal/tui/model.go` | Here line, list %, `Name`/`ModTime`/`Path()` |
| `internal/tui/model_test.go` | Name sort, navigation via `Path()` |
| `internal/display/summary.go` | Local report fields |
| `internal/display/tree.go` | `Path()` / `Name` |
| `cmd/disk-explorer/main.go` | Drop disk `ScanTopFiles` |
| `scripts/bench-ncdu.sh` | Warm/cold vs ncdu |
| `go.mod` | `golang.org/x/sys` direct |

Do not edit Bash `src/` for this plan. Do not rewrite `ScanTree` algorithm. Do not disable GC.

---

### Task 1: Compact Node and Path()

**Files:**
- Modify: `internal/scanner/types.go`
- Test: `internal/scanner/scanner_test.go`
- Also must compile: `internal/scanner/scanner.go`, `internal/scanner/tree.go`, `internal/scanner/topfiles.go`, `internal/tui/model.go`, `internal/tui/model_test.go`, `internal/display/summary.go`, `internal/display/tree.go`

- [ ] **Step 1: Write the failing Path() test**

Append to `internal/scanner/scanner_test.go`:

```go
func TestNodePath(t *testing.T) {
	root := &Node{Name: "/home/user", IsDir: true}
	child := &Node{Name: "docs", IsDir: true, Parent: root}
	file := &Node{Name: "a.txt", Parent: child}
	root.Children = []*Node{child}
	child.Children = []*Node{file}

	if got := root.Path(); got != "/home/user" {
		t.Errorf("root.Path()=%q", got)
	}
	if got := child.Path(); got != "/home/user/docs" {
		t.Errorf("child.Path()=%q", got)
	}
	if got := file.Path(); got != "/home/user/docs/a.txt" {
		t.Errorf("file.Path()=%q", got)
	}

	slash := &Node{Name: "/", IsDir: true}
	etc := &Node{Name: "etc", IsDir: true, Parent: slash}
	if got := etc.Path(); got != "/etc" {
		t.Errorf("slash join Path()=%q, want /etc", got)
	}
}
```

- [ ] **Step 2: Run the test to verify it fails**

Run: `go test ./internal/scanner/ -run TestNodePath`
Expected: FAIL — `Node.Path` is a field, not a method, or method undefined.

- [ ] **Step 3: Replace Node in `internal/scanner/types.go`**

Replace the whole file with:

```go
package scanner

import "os"

type ScanOptions struct {
	SameDevice bool
	Excludes   []string
}

type Node struct {
	Name      string
	Size      int64
	ModTime   int64 // unix seconds
	Parent    *Node
	Children  []*Node
	FileCount int
	DirCount  int
	IsDir     bool
}

func (n *Node) Path() string {
	if n == nil {
		return ""
	}
	if n.Parent == nil {
		return n.Name
	}
	p := n.Parent.Path()
	if p == string(os.PathSeparator) {
		return p + n.Name
	}
	return p + string(os.PathSeparator) + n.Name
}

type ScanProgress struct {
	Files   int
	Dirs    int
	Size    int64
	Current string
	Done    bool
	Root    *Node
}
```

- [ ] **Step 4: Make the rest of the repo compile**

`internal/scanner/scanner.go` — stop setting `Path` and `time.Time` ModTime; set `Name` (root = absolute `root` path, children = entry name). Remove `node.mu` usage: assign `node.Children = localChildren` directly. Root:

```go
abs, err := filepath.Abs(root)
if err != nil {
    abs = root
}
rootNode := &Node{
    Name:  abs,
    IsDir: rootInfo.IsDir(),
}
if !rootInfo.IsDir() {
    rootNode.Size = blockSize(rootInfo)
    rootNode.FileCount = 1
    rootNode.ModTime = rootInfo.ModTime().Unix()
}
```

Child construction:

```go
child := &Node{
    Name:    name,
    IsDir:   entry.IsDir(),
    ModTime: info.ModTime().Unix(),
    Parent:  node,
}
```

Still use `childPath := path + string(os.PathSeparator) + name` only for exclude checks and for submitting the next directory task (the walk still needs a filesystem path). Do not store it on the node.

`internal/scanner/tree.go` — drop `Path` field; root at `depth==0` uses `Name: path`, nested dirs use `Name: filepath.Base(path)`; files use `Name: de.Name()`, `ModTime: info.ModTime().Unix()`.

`internal/scanner/topfiles.go` — set `Name: d.Name()` instead of `Path: path`. Leave disk walk until Task 6 (must still compile).

`internal/display/tree.go` — `node.Path` → `node.Path()`; child label `node.Name` instead of `filepath.Base(node.Path)`.

`internal/display/summary.go` — `filepath.Base(e.Path)` → `e.Name`; top-file relative path uses `f.Path()`.

`internal/tui/model.go`:
- `m.Path = m.Current.Path` → `m.Path = m.Current.Path()`
- Restore selection with pointer identity (`e == oldNode`) instead of `e.Path == oldPath`
- Sort by name: `m.Entries[i].Name < m.Entries[j].Name`
- Sort by date: `m.Entries[i].ModTime > m.Entries[j].ModTime` (int64 unix seconds)
- List label: `e.Name` instead of `filepath.Base(e.Path)`

`internal/tui/model_test.go` — use `Name` instead of `Path` in sort fixtures; navigation assertions use `.Path()`:

```go
func TestModelSortEntriesBySize(t *testing.T) {
	m := Model{
		Entries: []*scanner.Node{
			{Name: "small", Size: 100},
			{Name: "large", Size: 1000},
			{Name: "medium", Size: 500},
		},
		SortBy: SortSize,
	}
	m.sortEntries()
	if m.Entries[0].Name != "large" {
		t.Errorf("Expected large first, got %s", m.Entries[0].Name)
	}
	if m.Entries[2].Name != "small" {
		t.Errorf("Expected small last, got %s", m.Entries[2].Name)
	}
}

func TestModelSortEntriesByName(t *testing.T) {
	m := Model{
		Entries: []*scanner.Node{
			{Name: "b", Size: 100},
			{Name: "a", Size: 1000},
			{Name: "c", Size: 500},
		},
		SortBy: SortName,
	}
	m.sortEntries()
	if m.Entries[0].Name != "a" {
		t.Errorf("Expected a first, got %s", m.Entries[0].Name)
	}
	if m.Entries[2].Name != "c" {
		t.Errorf("Expected c last, got %s", m.Entries[2].Name)
	}
}

func TestModelSortEntriesByReverseSize(t *testing.T) {
	m := Model{
		Entries: []*scanner.Node{
			{Name: "small", Size: 100},
			{Name: "large", Size: 1000},
			{Name: "medium", Size: 500},
		},
		SortBy:      SortSize,
		SortReverse: true,
	}
	m.sortEntries()
	if m.Entries[0].Name != "small" {
		t.Errorf("Expected small first, got %s", m.Entries[0].Name)
	}
	if m.Entries[2].Name != "large" {
		t.Errorf("Expected large last, got %s", m.Entries[2].Name)
	}
}

func TestModelNavigation(t *testing.T) {
	root := &scanner.Node{Name: "/", IsDir: true}
	child := &scanner.Node{Name: "child", IsDir: true, Parent: root}
	root.Children = []*scanner.Node{child}

	m := Model{
		State:   StateBrowsing,
		Current: root,
		Entries: root.Children,
		Path:    "/",
	}

	msg := tea.KeyMsg{Type: tea.KeyEnter}
	newModel, cmd := m.Update(msg)
	m = newModel.(Model)
	if cmd != nil {
		t.Error("Expected nil command for instant navigation")
	}
	if m.Current != child {
		t.Errorf("Expected current node to be child, got %s", m.Current.Path())
	}
	if m.Path != "/child" {
		t.Errorf("Expected path to be /child, got %s", m.Path)
	}

	msg = tea.KeyMsg{Type: tea.KeyBackspace}
	newModel, cmd = m.Update(msg)
	m = newModel.(Model)
	if cmd != nil {
		t.Error("Expected nil command for instant navigation")
	}
	if m.Current != root {
		t.Errorf("Expected current node to be root, got %s", m.Current.Path())
	}
	if m.Path != "/" {
		t.Errorf("Expected path to be /, got %s", m.Path)
	}
}
```

`internal/scanner/scanner_test.go` — `findNode` compares `curr.Path()` to the absolute fixture path. `TestScanTree` uses `child.Name == "dir1"`. `TestScanTopFiles` uses `top[0].Name`. `TestScan_DepthOne` keys `byName[child.Name]`.

- [ ] **Step 5: Run tests**

Run: `go test ./...`
Expected: PASS (existing behavior, new `TestNodePath` PASS).

- [ ] **Step 6: Commit**

```bash
git add internal/scanner/types.go internal/scanner/scanner.go internal/scanner/tree.go internal/scanner/topfiles.go internal/scanner/scanner_test.go internal/tui/model.go internal/tui/model_test.go internal/display/summary.go internal/display/tree.go
git commit -m "refactor(scanner): compact Node and reconstruct Path()"
```

---

### Task 2: Unix listDir (fstatat)

**Files:**
- Create: `internal/scanner/walk_unix.go`
- Create: `internal/scanner/mtime_linux.go`
- Create: `internal/scanner/mtime_darwin.go`
- Modify: `go.mod` (promote `golang.org/x/sys`)
- Test: `internal/scanner/scanner_test.go`

- [ ] **Step 1: Write the failing listDir test**

```go
func TestListDir(t *testing.T) {
	tmp := t.TempDir()
	if err := os.WriteFile(filepath.Join(tmp, "file.txt"), []byte("hello"), 0644); err != nil {
		t.Fatal(err)
	}
	sub := filepath.Join(tmp, "sub")
	if err := os.Mkdir(sub, 0755); err != nil {
		t.Fatal(err)
	}

	ents, dirSize, _, err := listDir(tmp)
	if err != nil {
		t.Fatal(err)
	}
	if dirSize < 0 {
		t.Fatalf("dirSize=%d", dirSize)
	}
	var sawFile, sawDir bool
	for _, e := range ents {
		switch e.name {
		case "file.txt":
			sawFile = true
			if e.isDir {
				t.Error("file.txt should not be a dir")
			}
			if e.size <= 0 {
				t.Errorf("file.txt size=%d", e.size)
			}
		case "sub":
			sawDir = true
			if !e.isDir {
				t.Error("sub should be a dir")
			}
		}
	}
	if !sawFile || !sawDir {
		t.Fatalf("missing entries: file=%v dir=%v (n=%d)", sawFile, sawDir, len(ents))
	}
}
```

- [ ] **Step 2: Run to verify fail**

Run: `go test ./internal/scanner/ -run TestListDir`
Expected: FAIL — `listDir` undefined.

- [ ] **Step 3: Implement walk helpers**

`internal/scanner/mtime_linux.go`:

```go
//go:build linux

package scanner

import "golang.org/x/sys/unix"

func mtimeSec(st *unix.Stat_t) int64 {
	sec, _ := st.Mtim.Unix()
	return sec
}
```

`internal/scanner/mtime_darwin.go`:

```go
//go:build darwin

package scanner

import "golang.org/x/sys/unix"

func mtimeSec(st *unix.Stat_t) int64 {
	sec, _ := st.Mtimespec.Unix()
	return sec
}
```

`internal/scanner/walk_unix.go`:

```go
//go:build unix

package scanner

import (
	"os"

	"golang.org/x/sys/unix"
)

type walkEntry struct {
	name    string
	size    int64
	modTime int64
	isDir   bool
	dev     uint64
	ino     uint64
	nlink   uint64
}

func listDir(path string) (entries []walkEntry, dirSize int64, fsType int64, err error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, 0, 0, err
	}
	defer f.Close()
	fd := int(f.Fd())

	var st unix.Stat_t
	if err := unix.Fstat(fd, &st); err == nil {
		dirSize = st.Blocks * 512
	}
	var stfs unix.Statfs_t
	if err := unix.Fstatfs(fd, &stfs); err == nil {
		fsType = int64(stfs.Type)
	}

	names, err := f.Readdirnames(-1)
	if err != nil {
		return nil, dirSize, fsType, err
	}
	entries = make([]walkEntry, 0, len(names))
	for _, name := range names {
		if name == "." || name == ".." {
			continue
		}
		var cst unix.Stat_t
		if err := unix.Fstatat(fd, name, &cst, unix.AT_SYMLINK_NOFOLLOW); err != nil {
			continue
		}
		isDir := cst.Mode&unix.S_IFMT == unix.S_IFDIR
		sz := int64(0)
		if !isDir {
			sz = cst.Blocks * 512
		}
		entries = append(entries, walkEntry{
			name:    name,
			size:    sz,
			modTime: mtimeSec(&cst),
			isDir:   isDir,
			dev:     uint64(cst.Dev),
			ino:     uint64(cst.Ino),
			nlink:   uint64(cst.Nlink),
		})
	}
	return entries, dirSize, fsType, nil
}
```

Then:

```bash
go get golang.org/x/sys@v0.44.0
go mod tidy
```

- [ ] **Step 4: Run TestListDir**

Run: `go test ./internal/scanner/ -run TestListDir`
Expected: PASS.

- [ ] **Step 5: Commit**

```bash
git add internal/scanner/walk_unix.go internal/scanner/mtime_linux.go internal/scanner/mtime_darwin.go internal/scanner/scanner_test.go go.mod go.sum
git commit -m "feat(scanner): add fd-relative listDir via fstatat"
```

---

### Task 3: Scan uses listDir, capped workers, leftover, 100ms progress

**Files:**
- Modify: `internal/scanner/scanner.go` (replace `Scan` and helpers; keep `blockSize`, `isExcluded`, `sumDir` for `ScanTree`)
- Test: existing `TestScan_DepthOne`, `TestSizeAggregation`, `TestScan_ContextCancellation` must keep passing

- [ ] **Step 1: Add workerCount test**

```go
func TestWorkerCountBounds(t *testing.T) {
	n := workerCount()
	if n < 2 || n > 16 {
		t.Fatalf("workerCount=%d want in [2,16]", n)
	}
}
```

- [ ] **Step 2: Run to verify fail**

Run: `go test ./internal/scanner/ -run TestWorkerCountBounds`
Expected: FAIL — `workerCount` undefined.

- [ ] **Step 3: Replace Scan in `internal/scanner/scanner.go`**

Keep the `package` imports needed: `context`, `os`, `path/filepath`, `runtime`, `strings`, `sync`, `sync/atomic`, `syscall`, `time`.

```go
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
			p := ScanProgress{Files: 1, Size: rootNode.Size, Done: true, Root: rootNode}
			ch <- p
			return
		}

		var totalFiles int64
		var totalDirs int64
		var totalSize int64
		var current atomic.Value
		current.Store(abs)

		seenMu := sync.Mutex{}
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
			sep := string(os.PathSeparator)

			for _, ent := range ents {
				if ctx.Err() != nil {
					break
				}
				childPath := path
				if path == sep {
					childPath = sep + ent.name
				} else {
					childPath = path + sep + ent.name
				}
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
		select {
		case taskCh <- dirTask{abs, rootNode}:
		default:
			processDir(abs, rootNode, true)
		}

		go func() {
			taskWg.Wait()
			close(taskCh)
		}()
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
```

**Root-dir bug to avoid:** the first task must call `processDir(..., isRoot=true)` so kernfs is not applied to the scan root. Sending the root through `taskCh` makes a worker call `processDir(..., false)`. Submit the root **inline on the coordinator goroutine**, not via workers:

Replace the "submit root" block with:

```go
taskWg.Add(1)
processDir(abs, rootNode, true)
go func() {
    taskWg.Wait()
    close(taskCh)
}()
workerWg.Wait()
```

`processDir` for the root will `taskCh <-` children (workers live). Start workers **before** `processDir` on the root. Do not enqueue the root itself.

Add stub `isKernFS` in this task so it compiles; Task 5 replaces it:

Create `internal/scanner/kernfs_stub.go` temporarily:

```go
package scanner

func isKernFS(fsType int64) bool { return false }
```

Task 5 will split linux/stub with build tags. If both stub and linux exist without tags, they conflict — so for Task 3 keep a single untagged `isKernFS` in `scanner.go`:

```go
func isKernFS(fsType int64) bool { return false }
```

Delete that function in Task 5 when adding tagged files.

- [ ] **Step 4: Run scanner tests**

Run: `go test ./internal/scanner/ ./internal/tui/`
Expected: PASS.

Fix if `TestSizeAggregation` fails because dir size now includes inode blocks: change equality to `nDir2.Size >= nFile3.Size` and `nDir1.Size >= nFile2.Size+nDir2.Size` **or** keep exact equality of *file* sums plus own dir size by capturing `root.Size >= nFile1.Size+nDir1.Size`. Spec: dir size ≥ sum of children. Update `TestSizeAggregation` accordingly:

```go
if nDir2.Size < nFile3.Size {
    t.Errorf("dir2 size %d < file3 %d", nDir2.Size, nFile3.Size)
}
if nDir1.Size < nFile2.Size+nDir2.Size {
    t.Errorf("dir1 size %d < children %d", nDir1.Size, nFile2.Size+nDir2.Size)
}
if root.Size < nFile1.Size+nDir1.Size {
    t.Errorf("root size %d < children %d", root.Size, nFile1.Size+nDir1.Size)
}
```

Keep FileCount==3 and DirCount==2.

- [ ] **Step 5: Commit**

```bash
git add internal/scanner/scanner.go internal/scanner/scanner_test.go
git commit -m "feat(scanner): walk with fstatat, capped workers, tick progress"
```

---

### Task 4: Hardlink counting

**Files:**
- Test: `internal/scanner/scanner_test.go`
- Logic already in Task 3 `processDir`; this task proves it

- [ ] **Step 1: Write the failing/proving test**

```go
func TestScanHardlinkCountedOnce(t *testing.T) {
	tmp := t.TempDir()
	a := filepath.Join(tmp, "a")
	b := filepath.Join(tmp, "b")
	if err := os.WriteFile(a, make([]byte, 4096), 0644); err != nil {
		t.Fatal(err)
	}
	if err := os.Link(a, b); err != nil {
		t.Fatal(err)
	}

	var root *Node
	for p := range Scan(context.Background(), tmp, ScanOptions{}) {
		if p.Done {
			root = p.Root
		}
	}
	if root == nil {
		t.Fatal("no root")
	}
	var na, nb *Node
	for _, c := range root.Children {
		switch c.Name {
		case "a":
			na = c
		case "b":
			nb = c
		}
	}
	if na == nil || nb == nil {
		t.Fatalf("missing names: a=%v b=%v", na, nb)
	}
	if na.Size == 0 && nb.Size == 0 {
		t.Fatal("both hardlink sizes are 0")
	}
	if na.Size > 0 && nb.Size > 0 {
		t.Fatalf("hardlink counted twice: %d and %d", na.Size, nb.Size)
	}
}
```

- [ ] **Step 2: Run**

Run: `go test ./internal/scanner/ -run TestScanHardlinkCountedOnce`
Expected: PASS if Task 3 hardlink map is correct. If FAIL (both sizes > 0), the map is not wired — add the `nlink>1` block from Task 3.

- [ ] **Step 3: Commit**

```bash
git add internal/scanner/scanner_test.go internal/scanner/scanner.go
git commit -m "test(scanner): count hardlinks with nlink>1 once"
```

---

### Task 5: Linux kernfs skip

**Files:**
- Create: `internal/scanner/kernfs_linux.go`
- Create: `internal/scanner/kernfs_other.go`
- Modify: `internal/scanner/scanner.go` (delete local `isKernFS`)
- Test: `internal/scanner/scanner_test.go`

- [ ] **Step 1: Write magic-number tests**

```go
func TestIsKernFS(t *testing.T) {
	if !isKernFS(0x9fa0) { // proc
		t.Error("proc should be kernfs")
	}
	if !isKernFS(0x62656572) { // sysfs
		t.Error("sysfs should be kernfs")
	}
	if isKernFS(0x9123683e) { // btrfs
		t.Error("btrfs must not be skipped")
	}
	if isKernFS(0x01021994) { // tmpfs (includes /dev and /run)
		t.Error("tmpfs must not be skipped (would drop /tmp)")
	}
}
```

On non-Linux, `isKernFS` is always false: skip this test with `if runtime.GOOS != "linux" { t.Skip() }`.

- [ ] **Step 2: Run to fail**

Run: `go test ./internal/scanner/ -run TestIsKernFS`
Expected: FAIL on Linux (`isKernFS` always false).

- [ ] **Step 3: Implement**

`internal/scanner/kernfs_linux.go`:

```go
//go:build linux

package scanner

import "golang.org/x/sys/unix"

func isKernFS(fsType int64) bool {
	switch fsType {
	case unix.PROC_SUPER_MAGIC,
		unix.SYSFS_MAGIC,
		unix.SECURITYFS_MAGIC,
		unix.CGROUP_SUPER_MAGIC,
		unix.CGROUP2_SUPER_MAGIC,
		unix.DEBUGFS_MAGIC,
		unix.TRACEFS_MAGIC,
		unix.PSTOREFS_MAGIC,
		unix.DEVPTS_SUPER_MAGIC:
		return true
	default:
		return false
	}
}
```

`internal/scanner/kernfs_other.go`:

```go
//go:build !linux

package scanner

func isKernFS(fsType int64) bool { return false }
```

Remove `func isKernFS` from `scanner.go`.

If a constant is missing on the Go unix package, replace it with the numeric literal used in the test.

- [ ] **Step 4: Run**

Run: `go test ./internal/scanner/ -run 'TestIsKernFS|TestScan_'`
Expected: PASS.

- [ ] **Step 5: Commit**

```bash
git add internal/scanner/kernfs_linux.go internal/scanner/kernfs_other.go internal/scanner/scanner.go internal/scanner/scanner_test.go
git commit -m "feat(scanner): skip Linux kernfs on subdirectories"
```

---

### Task 6: In-memory TopFiles

**Files:**
- Modify: `internal/scanner/topfiles.go` (replace entire file)
- Modify: `internal/scanner/scanner_test.go` (`TestScanTopFiles`)
- Modify: `cmd/disk-explorer/main.go`

- [ ] **Step 1: Rewrite TestScanTopFiles to scan then TopFiles**

Replace `TestScanTopFiles` with:

```go
func TestTopFiles(t *testing.T) {
	tmp := t.TempDir()
	files := []struct {
		name string
		size int
	}{
		{"small", 10},
		{"medium", 100},
		{"large", 1000},
		{"huge", 10000},
	}
	for _, f := range files {
		if err := os.WriteFile(filepath.Join(tmp, f.name), make([]byte, f.size), 0644); err != nil {
			t.Fatal(err)
		}
	}
	var root *Node
	for p := range Scan(context.Background(), tmp, ScanOptions{}) {
		if p.Done {
			root = p.Root
		}
	}
	if root == nil {
		t.Fatal("no root")
	}
	top := TopFiles(root, 2)
	if len(top) != 2 {
		t.Fatalf("expected 2, got %d", len(top))
	}
	if top[0].Name != "huge" {
		t.Errorf("expected huge, got %s", top[0].Name)
	}
	if top[1].Name != "large" {
		t.Errorf("expected large, got %s", top[1].Name)
	}
}
```

- [ ] **Step 2: Run to fail**

Run: `go test ./internal/scanner/ -run TestTopFiles`
Expected: FAIL — `TopFiles` undefined.

- [ ] **Step 3: Replace `internal/scanner/topfiles.go`**

```go
package scanner

import "container/heap"

type fileHeap []*Node

func (h fileHeap) Len() int           { return len(h) }
func (h fileHeap) Less(i, j int) bool { return h[i].Size < h[j].Size }
func (h fileHeap) Swap(i, j int)      { h[i], h[j] = h[j], h[i] }
func (h *fileHeap) Push(x any)        { *h = append(*h, x.(*Node)) }
func (h *fileHeap) Pop() any {
	old := *h
	n := len(old)
	x := old[n-1]
	*h = old[:n-1]
	return x
}

// TopFiles returns up to limit largest files in n's subtree (in memory).
func TopFiles(n *Node, limit int) []*Node {
	if n == nil || limit <= 0 {
		return nil
	}
	h := &fileHeap{}
	heap.Init(h)
	var walk func(*Node)
	walk = func(cur *Node) {
		if cur == nil {
			return
		}
		if !cur.IsDir && cur.Size > 0 {
			if h.Len() < limit {
				heap.Push(h, cur)
			} else if (*h)[0].Size < cur.Size {
				heap.Pop(h)
				heap.Push(h, cur)
			}
		}
		for _, c := range cur.Children {
			walk(c)
		}
	}
	walk(n)
	out := make([]*Node, h.Len())
	for i := len(out) - 1; i >= 0; i-- {
		out[i] = heap.Pop(h).(*Node)
	}
	return out
}
```

- [ ] **Step 4: Wire `cmd/disk-explorer/main.go`**

In `writeSummary`, after `collectEntries` has the root from `Scan`, do not call `scanner.ScanTopFiles`. Change `collectEntries` to return `*scanner.Node`:

```go
func collectRoot(path string, opts scanner.ScanOptions) *scanner.Node {
	ch := scanner.Scan(context.Background(), path, opts)
	var last scanner.ScanProgress
	for p := range ch {
		last = p
	}
	return last.Root
}

func writeSummary(w io.Writer, path string, opts scanner.ScanOptions, topN int) {
	root := collectRoot(path, opts)
	var entries []*scanner.Node
	var topFiles []*scanner.Node
	if root != nil {
		sort.Slice(root.Children, func(i, j int) bool {
			return root.Children[i].Size > root.Children[j].Size
		})
		entries = root.Children
		topFiles = scanner.TopFiles(root, topN)
	}
	di := getDiskInfo(path)
	display.Summary(w, path, root, entries, topFiles, di, topN)
}
```

`display.Summary` gains `root *scanner.Node` in Task 7; for this task if Summary signature is unchanged, pass `topFiles` only and keep calling `display.Summary(w, path, entries, topFiles, di, topN)` until Task 7. **Do not call `ScanTopFiles`.** Remove unused `ScanTopFiles` usages. `go test` / `go build ./cmd/disk-explorer` must succeed.

Grep to confirm no remaining `ScanTopFiles` or `filepath.WalkDir` in `cmd/` or `topfiles.go`.

- [ ] **Step 5: Run**

Run: `go test ./internal/scanner/ ./cmd/disk-explorer/`
Expected: PASS. `go build ./cmd/disk-explorer` PASS.

- [ ] **Step 6: Commit**

```bash
git add internal/scanner/topfiles.go internal/scanner/scanner_test.go cmd/disk-explorer/main.go
git commit -m "feat(scanner): in-memory TopFiles, drop second disk walk"
```

---

### Task 7: Summary / report local fields

**Files:**
- Modify: `internal/display/summary.go`
- Modify: `cmd/disk-explorer/main.go` (signature already passing root from Task 6; finish wiring)

- [ ] **Step 1: Write a display test**

Create `internal/display/summary_test.go`:

```go
package display

import (
	"bytes"
	"strings"
	"testing"

	"github.com/D1nma/disk_check/internal/scanner"
)

func TestSummaryContainsLocalContext(t *testing.T) {
	root := &scanner.Node{Name: "/tmp/x", Size: 4096, FileCount: 2, DirCount: 1, IsDir: true}
	dir := &scanner.Node{Name: "sub", Size: 2048, IsDir: true, Parent: root}
	file := &scanner.Node{Name: "f.bin", Size: 1024, Parent: root}
	root.Children = []*scanner.Node{dir, file}

	var buf bytes.Buffer
	Summary(&buf, root.Name, root, root.Children, []*scanner.Node{file}, DiskInfo{Total: 10000, Used: 5000, Avail: 5000}, 20)
	s := buf.String()
	for _, want := range []string{"Dossier : /tmp/x", "Taille", "fichiers", "TOP SOUS-DOSSIERS", "TOP FICHIERS", "f.bin"} {
		if !strings.Contains(s, want) {
			t.Errorf("missing %q in:\n%s", want, s)
		}
	}
}
```

- [ ] **Step 2: Run to fail**

Run: `go test ./internal/display/ -run TestSummaryContainsLocalContext`
Expected: FAIL — `Summary` has the wrong signature or missing strings.

- [ ] **Step 3: Replace `Summary`**

```go
func Summary(w io.Writer, path string, root *scanner.Node, entries []*scanner.Node, topFiles []*scanner.Node, di DiskInfo, topN int) {
	fmt.Fprintf(w, "RAPPORT DISQUE - %s\n", time.Now().Format("2006-01-02 15:04:05"))
	fmt.Fprintf(w, "Dossier : %s\n", path)
	if root != nil {
		fmt.Fprintf(w, "Taille  : %s  (%d fichiers, %d dossiers)\n",
			FormatSize(root.Size), root.FileCount, root.DirCount)
	}
	if di.Total > 0 {
		pct := di.Used * 100 / di.Total
		fmt.Fprintf(w, "Disque  : %s utilisé / %s total (%d%%)\n",
			FormatSize(di.Used), FormatSize(di.Total), pct)
		fmt.Fprintf(w, "Libre   : %s\n", FormatSize(di.Avail))
		if root != nil {
			fmt.Fprintf(w, "Part de ce dossier dans le disque : %d%%\n",
				root.Size*100/di.Total)
		}
	}
	fmt.Fprintln(w)

	fmt.Fprintf(w, "TOP SOUS-DOSSIERS :\n")
	count := 0
	for _, e := range entries {
		if !e.IsDir {
			continue
		}
		if count >= topN {
			break
		}
		fmt.Fprintf(w, "  %10s  %s/\n", FormatSize(e.Size), e.Name)
		count++
	}
	if count == 0 {
		fmt.Fprintln(w, "  (aucun)")
	}
	fmt.Fprintln(w)

	fmt.Fprintf(w, "TOP FICHIERS :\n")
	for _, f := range topFiles {
		rel := f.Path()
		if strings.HasPrefix(rel, path+"/") {
			rel = rel[len(path)+1:]
		}
		fmt.Fprintf(w, "  %10s  %s\n", FormatSize(f.Size), rel)
	}
	if len(topFiles) == 0 {
		fmt.Fprintln(w, "  (aucun)")
	}
}
```

Update `writeSummary` to pass `root`.

- [ ] **Step 4: Run**

Run: `go test ./internal/display/ ./cmd/disk-explorer/`
Expected: PASS.

- [ ] **Step 5: Commit**

```bash
git add internal/display/summary.go internal/display/summary_test.go cmd/disk-explorer/main.go
git commit -m "feat(display): local folder size and counts in summary"
```

---

### Task 8: TUI Here line and child percents

**Files:**
- Modify: `internal/tui/model.go`
- Test: `internal/tui/model_test.go`

- [ ] **Step 1: Write View tests**

```go
func TestViewHereLineOmitsParentAtRoot(t *testing.T) {
	root := &scanner.Node{Name: "/data", Size: 1000, FileCount: 3, DirCount: 1, IsDir: true}
	child := &scanner.Node{Name: "a", Size: 400, IsDir: true, Parent: root}
	root.Children = []*scanner.Node{child}
	m := Model{
		State:     StateBrowsing,
		Path:      "/data",
		Current:   root,
		Entries:   root.Children,
		diskTotal: 10000,
		Width:     80,
		Height:    24,
	}
	v := m.View()
	if !strings.Contains(v, "/data") {
		t.Errorf("missing current path in view:\n%s", v)
	}
	if strings.Contains(v, "of parent") {
		t.Errorf("root should omit parent percent:\n%s", v)
	}
	if !strings.Contains(v, "of disk") {
		t.Errorf("missing disk percent:\n%s", v)
	}
}

func TestViewHereLineIncludesParentWhenNested(t *testing.T) {
	root := &scanner.Node{Name: "/data", Size: 1000, IsDir: true}
	child := &scanner.Node{Name: "a", Size: 400, FileCount: 1, DirCount: 0, IsDir: true, Parent: root}
	root.Children = []*scanner.Node{child}
	m := Model{
		State:     StateBrowsing,
		Path:      "/data/a",
		Current:   child,
		Entries:   child.Children,
		diskTotal: 10000,
		Width:     80,
		Height:    24,
	}
	v := m.View()
	if !strings.Contains(v, "of parent") {
		t.Errorf("nested dir should show parent percent:\n%s", v)
	}
}
```

Add `"strings"` to the tui test imports.

- [ ] **Step 2: Run to fail**

Run: `go test ./internal/tui/ -run TestViewHere`
Expected: FAIL — View has no "of parent" / "of disk".

- [ ] **Step 3: Update View and listHeight**

`listHeight` when `StateBrowsing`: `m.Height - 6` (header, disk, here, sep, sep, footer). When scanning: keep `m.Height - 5`.

In browsing View, after the disk line (or after header if no disk), write the Here line:

```go
if m.State == StateBrowsing && m.Current != nil {
    var parts []string
    parts = append(parts, formatSize(m.Current.Size))
    if m.Current.Parent != nil && m.Current.Parent.Size > 0 {
        pct := m.Current.Size * 100 / m.Current.Parent.Size
        parts = append(parts, fmt.Sprintf("%d%% of parent", pct))
    }
    if m.diskTotal > 0 {
        pct := m.Current.Size * 100 / m.diskTotal
        parts = append(parts, fmt.Sprintf("%d%% of disk", pct))
    }
    parts = append(parts, fmt.Sprintf("%d files", m.Current.FileCount))
    parts = append(parts, fmt.Sprintf("%d dirs", m.Current.DirCount))
    b.WriteString(dimStyle.Render("  "+strings.Join(parts, "  ")) + "\n")
}
```

List lines:

```go
var dirSize int64
if m.Current != nil {
    dirSize = m.Current.Size
}
pctStr := "  0.0%"
if dirSize > 0 {
    pctStr = fmt.Sprintf("%5.1f%%", float64(e.Size)*100/float64(dirSize))
}
name := e.Name
if e.IsDir {
    name += "/"
}
line := fmt.Sprintf("%s%10s  %s  %s  %s", cursor, formatSize(e.Size), pctStr, bar, name)
```

Header already uses `m.Path` (set from `Path()` on navigate).

- [ ] **Step 4: Run**

Run: `go test ./internal/tui/`
Expected: PASS.

- [ ] **Step 5: Commit**

```bash
git add internal/tui/model.go internal/tui/model_test.go
git commit -m "feat(tui): here-line and percent of current directory"
```

---

### Task 9: Exclude regression + full test

**Files:**
- Test: `internal/scanner/scanner_test.go`

- [ ] **Step 1: Write exclude test**

```go
func TestScanExclude(t *testing.T) {
	tmp := t.TempDir()
	keep := filepath.Join(tmp, "keep")
	skip := filepath.Join(tmp, "skip")
	os.Mkdir(keep, 0755)
	os.Mkdir(skip, 0755)
	os.WriteFile(filepath.Join(keep, "a"), []byte("aa"), 0644)
	os.WriteFile(filepath.Join(skip, "b"), []byte("bbbbbbbb"), 0644)

	var root *Node
	for p := range Scan(context.Background(), tmp, ScanOptions{Excludes: []string{skip}}) {
		if p.Done {
			root = p.Root
		}
	}
	for _, c := range root.Children {
		if c.Name == "skip" {
			t.Fatal("excluded dir still present")
		}
	}
}
```

- [ ] **Step 2: Run**

Run: `go test ./internal/scanner/ -run TestScanExclude`
Expected: PASS (isExcluded already used in processDir). If FAIL, join childPath as in Task 3 before `isExcluded`.

- [ ] **Step 3: Full suite**

Run: `go test ./...` and `go vet ./...`
Expected: PASS, no vet findings.

- [ ] **Step 4: Commit if the exclude test is new**

```bash
git add internal/scanner/scanner_test.go
git commit -m "test(scanner): skip excluded subtrees"
```

---

### Task 10: Bench script vs ncdu

**Files:**
- Create: `scripts/bench-ncdu.sh`

- [ ] **Step 1: Write the script**

```bash
#!/usr/bin/env bash
set -euo pipefail

TARGET="${1:-/usr}"
BIN="${BIN:-./disk-explorer}"
if [[ ! -x "$BIN" ]]; then
  go build -o disk-explorer ./cmd/disk-explorer
  BIN=./disk-explorer
fi

nproc_n="$(nproc 2>/dev/null || echo 4)"

time_cmd() {
  local label="$1"
  shift
  local start end
  start="$(date +%s.%N)"
  "$@" >/dev/null
  end="$(date +%s.%N)"
  python3 - "$label" "$start" "$end" <<'PY'
import sys
label, start, end = sys.argv[1], float(sys.argv[2]), float(sys.argv[3])
print(f"{label:28s} {end-start:.3f}s")
PY
}

run_suite() {
  local tag="$1"
  echo "=== $tag  target=$TARGET ==="
  time_cmd "ncdu 1-thread" ncdu -0 -o /dev/null "$TARGET"
  time_cmd "ncdu -t ${nproc_n}" ncdu -0 -o /dev/null -t "$nproc_n" "$TARGET"
  time_cmd "disk-explorer --summary" "$BIN" --summary "$TARGET"
}

echo "warmup..."
ncdu -0 -o /dev/null "$TARGET" >/dev/null || true
"$BIN" --summary "$TARGET" >/dev/null || true

run_suite "warm"

if [[ -w /proc/sys/vm/drop_caches ]]; then
  sync
  echo 3 > /proc/sys/vm/drop_caches
  run_suite "cold"
else
  echo "cold: skipped (cannot write /proc/sys/vm/drop_caches)"
fi
```

If `python3` is missing, print times with `awk` instead of python:

```bash
awk -v s="$start" -v e="$end" -v l="$label" 'BEGIN{printf "%-28s %.3fs\n", l, e-s}'
```

Use awk in the committed script (no python dependency).

- [ ] **Step 2: chmod +x and smoke on a small tree**

Run: `chmod +x scripts/bench-ncdu.sh && go build -o disk-explorer ./cmd/disk-explorer && ./scripts/bench-ncdu.sh /usr`
Expected: three warm times printed. disk-explorer should be faster than both ncdu numbers. If it is not, profile `Scan` (`go test -bench` or `perf`) before merging — typical causes: still using `Info()`/`lstat`, too many workers, progress lock, second walk.

- [ ] **Step 3: Commit**

```bash
git add scripts/bench-ncdu.sh
git commit -m "chore: add ncdu warm/cold bench script"
```

---

## Self-review (spec coverage)

| Spec section | Task |
|--------------|------|
| Compact Node, Path(), `/` join | 1 |
| open/Readdirnames/fstatat | 2 |
| Workers [2,16], leftover, no relay | 3 |
| 100ms progress tick | 3 |
| Dir inode blocks in aggregate | 3 |
| Hardlinks nlink>1 | 3–4 |
| Kernfs Linux-only, not tmpfs | 5 |
| TopFiles in memory, no second walk | 6 |
| Summary local fields | 7 |
| TUI Here + child % | 8 |
| Exclude + full test | 9 |
| Bench script | 10 |
| `--tree` algorithm unchanged | Task 1 compile-only on tree.go |
| No GC disable, no io_uring | not introduced |

No TBD/TODO placeholders. `Path()` / `TopFiles` / `listDir` / `isKernFS` / `workerCount` names are consistent across tasks.

**Root scan + workers:** Task 3 explicitly processes the scan root on the coordinator with `isRoot=true` so `/proc` as a *target* still scans, but `/proc` as a *child* of `/` is skipped once Task 5 is in.
