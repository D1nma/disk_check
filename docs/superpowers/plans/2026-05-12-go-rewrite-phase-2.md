# Disk Explorer Go Rewrite - Phase 2 Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Implement interactive TUI navigation, real-time scanner integration, and native Go SSH remote execution.

**Architecture:** Use Bubble Tea for the TUI, connecting to the parallel scanner via channels and `tea.Cmd`. Remote scanning will use `golang.org/x/crypto/ssh` to run the embedded Bash script and parse results.

**Tech Stack:** Go, Bubble Tea, Lip Gloss, golang.org/x/crypto/ssh.

---

### Task 1: TUI Model Expansion & Navigation

**Files:**
- Modify: `internal/tui/model.go`

- [ ] **Step 1: Update Model and View**
Update `Model` to include current path, entries, selection index, and terminal dimensions.

```go
package tui

import (
	"fmt"
	"github.com/D1nma/disk_check/internal/scanner"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
)

type Model struct {
	Path     string
	Entries  []scanner.Entry
	Selected int
	Width    int
	Height   int
	Scanning bool
}

func (m Model) View() string {
	if len(m.Entries) == 0 {
		return "Scanning..."
	}

	var s string
	s += lipgloss.NewStyle().Bold(true).Render(fmt.Sprintf(" Path: %s", m.Path)) + "\n\n"

	for i, entry := range m.Entries {
		cursor := " "
		if m.Selected == i {
			cursor = ">"
		}
		s += fmt.Sprintf("%s %s\n", cursor, entry.Path)
	}

	return s
}
```

- [ ] **Step 2: Implement basic navigation keys**
Handle `up`, `down`, `enter`, `backspace`, and `q`.

```go
func (m Model) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.KeyMsg:
		switch msg.String() {
		case "q", "ctrl+c":
			return m, tea.Quit
		case "up", "k":
			if m.Selected > 0 {
				m.Selected--
			}
		case "down", "j":
			if m.Selected < len(m.Entries)-1 {
				m.Selected++
			}
		}
	case tea.WindowSizeMsg:
		m.Width = msg.Width
		m.Height = msg.Height
	}
	return m, nil
}
```

- [ ] **Step 3: Run and verify**
Execute `go run cmd/disk-explorer/main.go .` and verify the TUI shows a list and responds to arrow keys.

- [ ] **Step 4: Commit**
```bash
git add internal/tui/model.go
git commit -m "feat(tui): add basic navigation and list view"
```

---

### Task 2: Real-time Scanner Integration

**Files:**
- Modify: `internal/tui/model.go`
- Modify: `internal/scanner/scanner.go`

- [ ] **Step 1: Update scanner to send entries one by one**
Modify `Scan` to return a channel of `scanner.Entry` instead of `ScanResult`.

```go
// internal/scanner/scanner.go
func Scan(root string, concurrency int) chan Entry {
	entries := make(chan Entry)
	var wg sync.WaitGroup
	sem := make(chan struct{}, concurrency)

	go func() {
		defer close(entries)
		wg.Add(1)
		scanDir(root, entries, &wg, sem)
		wg.Wait()
	}()

	return entries
}
```

- [ ] **Step 2: Implement TUI message for new entries**
Add a command in `internal/tui/model.go` to listen for scanner updates.

```go
type NewEntryMsg scanner.Entry

func listenForEntries(entries chan scanner.Entry) tea.Cmd {
	return func() tea.Msg {
		e, ok := <-entries
		if !ok {
			return nil // Channel closed
		}
		return NewEntryMsg(e)
	}
}
```

- [ ] **Step 3: Handle NewEntryMsg in Update**
Update the model with incoming entries and trigger the next `listenForEntries` command.

```go
func (m Model) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case NewEntryMsg:
		m.Entries = append(m.Entries, scanner.Entry(msg))
		// Continue listening (placeholder: need to store channel in model)
		return m, nil 
	}
	// ... existing key handling ...
	return m, nil
}
```

- [ ] **Step 4: Verify real-time updates**
Run the tool on a large directory and watch files appear live.

- [ ] **Step 5: Commit**
```bash
git add internal/scanner/scanner.go internal/tui/model.go
git commit -m "feat(tui): integrate scanner with real-time updates"
```

---

### Task 3: Native Go SSH Support

**Files:**
- Modify: `go.mod`
- Modify: `internal/remote/remote.go`

- [ ] **Step 1: Add SSH dependency**
Run `go get golang.org/x/crypto/ssh`

- [ ] **Step 2: Implement native SSH client**
Replace placeholder in `internal/remote/remote.go` with actual SSH logic.

```go
package remote

import (
	"golang.org/x/crypto/ssh"
	"os"
	"io"
	"github.com/D1nma/disk_check/internal/assets"
)

func RunRemote(host string, user string) (io.ReadCloser, error) {
	// 1. Setup Auth (simplified for plan, real impl will look for keys)
	config := &ssh.ClientConfig{
		User: user,
		Auth: []ssh.AuthMethod{}, // TODO: Add key/agent auth
		HostKeyCallback: ssh.InsecureIgnoreHostKey(),
	}

	client, err := ssh.Dial("tcp", host+":22", config)
	if err != nil {
		return nil, err
	}

	session, err := client.NewSession()
	if err != nil {
		return nil, err
	}

	// 2. Run embedded script
	stdout, err := session.StdoutPipe()
	if err != nil {
		return nil, err
	}

	err = session.Start(string(assets.BashScript))
	return stdout, err
}
```

- [ ] **Step 3: Verify SSH connection**
(Requires a local SSH server or a test container)

- [ ] **Step 4: Commit**
```bash
git add go.mod go.sum internal/remote/remote.go
git commit -m "feat(remote): implement native Go SSH client"
```
