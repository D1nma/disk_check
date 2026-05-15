# Full Scan Refactor Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Transition Disk Explorer to a full-scan model with a progress screen and instant navigation.

**Architecture:** Implement a parallel recursive scanner that builds a bidirectional tree in memory. Refactor the TUI into two states: `Scanning` (progress bar/stats) and `Browsing` (instant list view).

**Tech Stack:** Go, Bubble Tea, Lip Gloss.

---

### Task 1: Data Structure Update

**Files:**
- Modify: `disk_check/internal/scanner/types.go`

- [ ] **Step 1: Update the Node structure**
Update `Entry` or create a new `Node` type that supports the tree structure.

```go
package scanner

import (
	"time"
	"sync"
)

type Node struct {
	Name      string
	Path      string
	Size      int64
	IsDir     bool
	ModTime   time.Time
	Parent    *Node
	Children  []*Node
	FileCount int
	DirCount  int
	mu        sync.Mutex // For thread-safe aggregation during parallel scan
}
```

- [ ] **Step 2: Commit**
```bash
git add disk_check/internal/scanner/types.go
git commit -m "feat: define tree Node structure"
```

---

### Task 2: Parallel Scanner Implementation

**Files:**
- Modify: `disk_check/internal/scanner/scanner.go`
- Test: `disk_check/internal/scanner/scanner_test.go`

- [ ] **Step 1: Create a progress message type**
```go
type ScanProgress struct {
	Files   int
	Dirs    int
	Size    int64
	Current string
	Done    bool
	Root    *Node
}
```

- [ ] **Step 2: Implement the parallel walker**
Replace `Scan` with a version that builds the tree and reports progress.

- [ ] **Step 3: Write tests for size aggregation**
Ensure that a child's size is correctly added to all its ancestors.

- [ ] **Step 4: Commit**
```bash
git commit -m "feat: implement parallel scanner with tree building"
```

---

### Task 3: TUI State Refactoring

**Files:**
- Modify: `disk_check/internal/tui/model.go`

- [ ] **Step 1: Add new states to Model**
```go
type State int
const (
	StateScanning State = iota
	StateBrowsing
)

type Model struct {
	// ... existing fields
	State    State
	Root     *scanner.Node
	Current  *scanner.Node
	Progress scanner.ScanProgress
}
```

- [ ] **Step 2: Update `Update` function to handle `ScanProgress`**
Switch to `StateBrowsing` when `Progress.Done` is true.

- [ ] **Step 3: Update `View` to render progress screen**
Show counters when `m.State == StateScanning`.

- [ ] **Step 4: Commit**
```bash
git commit -m "feat: refactor TUI for Scanning and Browsing states"
```

---

### Task 4: Instant Navigation

**Files:**
- Modify: `disk_check/internal/tui/model.go`

- [ ] **Step 1: Implement O(1) navigation**
Instead of calling `scanner.Scan` on "enter", just set `m.Current = selectedChild`.

- [ ] **Step 2: Implement "Back" using `node.Parent`**

- [ ] **Step 3: Commit**
```bash
git commit -m "feat: enable instant navigation using the in-memory tree"
```
