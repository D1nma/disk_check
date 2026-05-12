# disk-explorer Go Rewrite Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Rewrite the `disk-explorer` utility in Go for better performance and a modern TUI, while keeping the bootstrap experience.

**Architecture:** A Go application using Bubble Tea for the TUI and a parallel scanner. The SSH remote mode is handled by embedding the original Bash script and executing it via the system's `ssh` command.

**Tech Stack:** Go 1.21+, Bubble Tea (TUI), Lip Gloss (Styling), `go:embed`.

---

## File Structure

- `cmd/disk-explorer/main.go`: Entry point, flag parsing, and dispatching.
- `internal/scanner/scanner.go`: Parallel directory traversal logic.
- `internal/scanner/types.go`: Data structures for scan results.
- `internal/tui/model.go`: Bubble Tea model and message types.
- `internal/tui/view.go`: Lip Gloss styling and UI layout.
- `internal/tui/update.go`: Key bindings and state transitions.
- `internal/remote/remote.go`: SSH orchestration logic.
- `internal/remote/embed.go`: Embedding the original Bash script.
- `bootstrap.sh`: The new distribution wrapper script.

---

## Phase 1: Foundation & Scanner

### Task 1: Project Initialization

**Files:**
- Create: `go.mod`
- Create: `cmd/disk-explorer/main.go`

- [ ] **Step 1: Initialize Go module**
Run: `go mod init github.com/D1nma/disk_check`

- [ ] **Step 2: Create basic main.go**
```go
package main

import "fmt"

func main() {
    fmt.Println("Disk Explorer Go")
}
```

- [ ] **Step 3: Verify execution**
Run: `go run cmd/disk-explorer/main.go`
Expected: Output "Disk Explorer Go"

- [ ] **Step 4: Commit**
```bash
git add go.mod cmd/disk-explorer/main.go
git commit -m "feat: initialize go project"
```

### Task 2: Parallel Scanner Implementation

**Files:**
- Create: `internal/scanner/types.go`
- Create: `internal/scanner/scanner.go`
- Test: `internal/scanner/scanner_test.go`

- [ ] **Step 1: Define Scanner Types**
```go
package scanner

import "time"

type Entry struct {
    Path     string
    Size     int64
    IsDir    bool
    ModTime  time.Time
}

type ScanResult struct {
    Entries []Entry
    Error   error
}
```

- [ ] **Step 2: Implement Parallel Scanner**
```go
package scanner

import (
    "os"
    "path/filepath"
    "sync"
)

func Scan(root string, concurrency int) chan ScanResult {
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
        resultEntries = append(resultEntries) // To be refined in actual impl to calculate recursive dir size
        if e.IsDir() {
            wg.Add(1)
            go scanDir(entry.Path, results, wg, sem)
        }
    }
    results <- ScanResult{Entries: resultEntries}
}
```

- [ ] **Step 3: Commit**
```bash
git add internal/scanner/
git commit -m "feat: add parallel scanner foundation"
```

---

## Phase 2: TUI with Bubble Tea

### Task 3: Setup Bubble Tea Model

**Files:**
- Create: `internal/tui/model.go`

- [ ] **Step 1: Define Model and Messages**
```go
package tui

import (
    "github.com/D1nma/disk_check/internal/scanner"
    tea "github.com/charmbracelet/bubbletea"
)

type Model struct {
    Path    string
    Entries []scanner.Entry
    Ready   bool
}

type ScanUpdateMsg []scanner.Entry

func (m Model) Init() tea.Cmd {
    return nil
}
```

- [ ] **Step 2: Install dependencies**
Run: `go get github.com/charmbracelet/bubbletea github.com/charmbracelet/lipgloss`

- [ ] **Step 3: Commit**
```bash
git add internal/tui/model.go go.sum
git commit -m "feat: setup tui model"
```

---

## Phase 3: Remote Orchestration

### Task 4: Embed Bash Script

**Files:**
- Create: `internal/remote/embed.go`
- Modify: `internal/remote/remote.go`

- [ ] **Step 1: Embed script**
```go
package remote

import _ "embed"

//go:embed ../../disk-explorer.sh
var BashScript string
```

- [ ] **Step 2: Commit**
```bash
git add internal/remote/
git commit -m "feat: embed original bash script"
```

---

## Phase 4: Distribution Wrapper

### Task 5: Create bootstrap.sh

**Files:**
- Create: `bootstrap.sh`

- [ ] **Step 1: Write bootstrap logic**
```bash
#!/bin/bash
OS=$(uname -s | tr '[:upper:]' '[:lower:]')
ARCH=$(uname -m)
# Mapping logic...
# Download from GitHub...
# Execute binary
```

- [ ] **Step 2: Commit**
```bash
git add bootstrap.sh
git commit -m "feat: add bootstrap wrapper"
```
