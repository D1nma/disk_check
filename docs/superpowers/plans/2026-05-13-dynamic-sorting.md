# Dynamic Sorting Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Implement dynamic sorting by size, name, and date in the Disk Explorer TUI.

**Architecture:** Add sort state (key and direction) to the `Model`, implement a `sortEntries` method, and handle sort key toggles in the `Update` loop. Maintain sorted order as new entries arrive.

**Tech Stack:** Go, Bubble Tea (TUI framework).

---

### Task 1: Add Sort Types and State

**Files:**
- Modify: `internal/tui/model.go`
- Create: `internal/tui/model_test.go`

- [ ] **Step 1: Define SortKey enum and add fields to Model**

```go
type SortKey int

const (
	SortSize SortKey = iota
	SortName
	SortDate
)

type Model struct {
	// ... (existing fields)
	SortBy      SortKey
	SortReverse bool
}
```

- [ ] **Step 2: Create initial test file with a basic Model setup**

```go
package tui

import (
	"testing"
	"github.com/D1nma/disk_check/internal/scanner"
)

func TestModelSorting(t *testing.T) {
	// Placeholder for TDD
}
```

- [ ] **Step 3: Commit**

```bash
git add internal/tui/model.go
git commit -m "chore(tui): add sort types and state to Model"
```

### Task 2: Implement and Test Sorting Logic

**Files:**
- Modify: `internal/tui/model.go`
- Modify: `internal/tui/model_test.go`

- [ ] **Step 1: Write failing test for sorting by size**

```go
func TestModelSortEntriesBySize(t *testing.T) {
	m := Model{
		Entries: []scanner.Entry{
			{Path: "small", Size: 100},
			{Path: "large", Size: 1000},
			{Path: "medium", Size: 500},
		},
		SortBy: SortSize,
		SortReverse: false,
	}
	m.sortEntries()
	if m.Entries[0].Path != "large" {
		t.Errorf("Expected large first, got %s", m.Entries[0].Path)
	}
}
```

- [ ] **Step 2: Run test to verify it fails (method missing)**

- [ ] **Step 3: Implement `sortEntries` method**

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
(Note: need to import `sort` package)

- [ ] **Step 4: Verify test passes**

- [ ] **Step 5: Add tests for name and date sorting**

- [ ] **Step 6: Commit**

```bash
git add internal/tui/model.go internal/tui/model_test.go
git commit -m "feat(tui): implement sorting logic with tests"
```

### Task 3: Handle Sort Keys and Real-time Sorting

**Files:**
- Modify: `internal/tui/model.go`

- [ ] **Step 1: Update `Update` to maintain sort on `NewEntryMsg`**

```go
	case NewEntryMsg:
		m.Entries = append(m.Entries, scanner.Entry(msg))
		m.sortEntries() // Keep it sorted
		return m, listenForEntries(m.ScannerChan)
```

- [ ] **Step 2: Update `Update` to handle 's', 'n', 't' keys**

```go
		case "s":
			if m.SortBy == SortSize {
				m.SortReverse = !m.SortReverse
			} else {
				m.SortBy = SortSize
				m.SortReverse = false
			}
			m.sortEntries()
		case "n":
			if m.SortBy == SortName {
				m.SortReverse = !m.SortReverse
			} else {
				m.SortBy = SortName
				m.SortReverse = false
			}
			m.sortEntries()
		case "t":
			if m.SortBy == SortDate {
				m.SortReverse = !m.SortReverse
			} else {
				m.SortBy = SortDate
				m.SortReverse = false
			}
			m.sortEntries()
```

- [ ] **Step 3: Commit**

```bash
git add internal/tui/model.go
git commit -m "feat(tui): handle sort keys and real-time sorting"
```

### Task 4: Verification and Final Polish

- [ ] **Step 1: Run the application and verify sorting manually**

`go run cmd/disk-explorer/main.go .`

- [ ] **Step 2: Run all tests**

`go test ./...`

- [ ] **Step 3: Commit**

```bash
git commit -m "test(tui): final verification of sorting"
```
