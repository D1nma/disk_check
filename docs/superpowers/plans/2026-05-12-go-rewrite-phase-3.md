# Disk Explorer Go Rewrite - Phase 3 Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Implement hierarchical TUI navigation with "Lazy Scanning" and dynamic sorting.

**Architecture:** Extend the TUI model to handle a directory stack and use `context.Context` to cancel ongoing scans when navigating. Implement in-place sorting and responsive UI elements like percentage bars.

**Tech Stack:** Go, Bubble Tea, Lip Gloss.

---

### Task 1: Scanner Cancellation with Context

**Files:**
- Modify: `internal/scanner/scanner.go`

- [ ] **Step 1: Update Scan signature to accept Context**

```go
// internal/scanner/scanner.go
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
```

- [ ] **Step 2: Update scanDir to respect Context cancellation**

```go
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
```

- [ ] **Step 3: Run and verify**
Update `cmd/disk-explorer/main.go` to pass `context.Background()` and verify it still builds and runs.
Run: `go build ./...`

- [ ] **Step 4: Commit**
```bash
git add internal/scanner/scanner.go
git commit -m "feat(scanner): add context support for cancellation"
```

---

### Task 2: Navigation Stack & Path Transition

**Files:**
- Modify: `internal/tui/model.go`

- [ ] **Step 1: Add directory stack and context canceler to Model**

```go
type Model struct {
	Path        string
	Entries     []scanner.Entry
	Selected    int
	Width       int
	Height      int
	Scanning    bool
	ScannerChan chan scanner.Entry
	CancelScan  context.CancelFunc // New: Store cancel function
	History     []string           // New: Breadcrumbs/Stack
}
```

- [ ] **Step 2: Implement Navigation logic in Update**
Handle `enter` to go deeper and `backspace` to go up.

```go
func (m Model) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.KeyMsg:
		switch msg.String() {
		case "enter":
			if len(m.Entries) > 0 && m.Entries[m.Selected].IsDir {
				return m.navigateTo(m.Entries[m.Selected].Path)
			}
		case "backspace", "left":
			if len(m.History) > 0 {
				last := m.History[len(m.History)-1]
				m.History = m.History[:len(m.History)-1]
				return m.navigateTo(last)
			}
		}
	}
	// ...
}

func (m *Model) navigateTo(newPath string) (tea.Model, tea.Cmd) {
	if m.CancelScan != nil {
		m.CancelScan()
	}
	// Push current to history if going deeper (simplified)
	// Reset entries and start new scan
	ctx, cancel := context.WithCancel(context.Background())
	m.CancelScan = cancel
	m.Path = newPath
	m.Entries = nil
	m.ScannerChan = scanner.Scan(ctx, newPath, 4)
	return m, listenForEntries(m.ScannerChan)
}
```

- [ ] **Step 3: Verify navigation**
Run `go run cmd/disk-explorer/main.go .` and verify you can enter folders and go back.

- [ ] **Step 4: Commit**
```bash
git add internal/tui/model.go
git commit -m "feat(tui): implement hierarchical navigation with lazy scanning"
```

---

### Task 3: Dynamic Sorting

**Files:**
- Modify: `internal/tui/model.go`

- [ ] **Step 1: Add sort state to Model**

```go
type SortKey int
const (
	SortSize SortKey = iota
	SortName
	SortDate
)

type Model struct {
	// ...
	SortBy      SortKey
	SortReverse bool
}
```

- [ ] **Step 2: Implement sorting function**

```go
func (m *Model) sortEntries() {
	sort.Slice(m.Entries, func(i, j int) bool {
		var res bool
		switch m.SortBy {
		case SortSize:
			res = m.Entries[i].Size > m.Entries[j].Size
		case SortName:
			res = m.Entries[i].Path < m.Entries[j].Path
		case SortDate:
			res = m.Entries[i].ModTime.After(m.Entries[j].ModTime)
		}
		if m.SortReverse {
			return !res
		}
		return res
	})
}
```

- [ ] **Step 3: Handle sort keys in Update**

```go
case "s":
	m.SortBy = SortSize
	m.sortEntries()
case "n":
	m.SortBy = SortName
	m.sortEntries()
```

- [ ] **Step 4: Verify sorting**
Run the tool and toggle `s` or `n` to see the list reorder.

- [ ] **Step 5: Commit**
```bash
git add internal/tui/model.go
git commit -m "feat(tui): add dynamic sorting by size and name"
```

---

### Task 4: Visual Polish (Percentage Bars)

**Files:**
- Modify: `internal/tui/model.go`

- [ ] **Step 1: Update View to show percentage bars**
Calculate relative size to the largest item.

```go
func (m Model) View() string {
	// ... find max size ...
	// render [#####     ] next to entries
}
```

- [ ] **Step 2: Commit**
```bash
git add internal/tui/model.go
git commit -m "style(tui): add percentage bars to file list"
```
