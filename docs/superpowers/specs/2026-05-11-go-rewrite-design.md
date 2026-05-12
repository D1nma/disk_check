# Design Spec: disk-explorer Go Rewrite

**Date:** 2026-05-11
**Status:** Draft
**Author:** Gemini CLI

## 1. Overview
The goal is to rewrite the `disk-explorer` Bash utility in Go to significantly improve performance (via parallelism) and user experience (via a modern TUI library), while maintaining the seamless "no-install" distribution model and SSH remote capabilities.

## 2. Success Criteria
*   **Performance:** Local scanning must be substantially faster than the current Bash/`du` implementation on multi-core systems.
*   **User Experience:** The TUI must be more responsive, handle window resizing gracefully, and provide real-time feedback during scans.
*   **Portability:** Maintain the ability to run on remote hosts via SSH without requiring Go or any specific binaries on the target (using the existing Bash script as a payload).
*   **Distribution:** Preserve the `curl | bash` experience via a bootstrap script.

## 3. Architecture

### 3.1 Components
*   **`cmd/disk-explorer`**: Entry point. Handles CLI flag parsing (using `flag` or `cobra`) and dispatches to local or remote modes.
*   **`internal/scanner`**:
    *   Native Go implementation using `os.ReadDir`.
    *   **Concurrency**: Uses a worker pool of goroutines to explore directories in parallel.
    *   **Communication**: Sends updates (found entries, progress) via channels to the TUI.
*   **`internal/tui`**:
    *   Framework: **Bubble Tea** (The architectural core).
    *   Styling: **Lip Gloss** (For layout, colors, and borders).
    *   Components:
        *   `Header`: Real-time disk usage bar and current path.
        *   `FileTable`: Scrollable list of files/directories with size bars.
        *   `Footer`: Keyboard shortcuts and status messages.
*   **`internal/remote`**:
    *   **Orchestrator**: Uses the local `ssh` system command.
    *   **Payload**: The original `disk-explorer.sh` Bash script is embedded into the Go binary using `go:embed`.
    *   **Execution**: Sends the script to remote hosts via `ssh user@host bash -s -- [args]`.

### 3.2 Data Flow
1.  User launches the tool.
2.  `scanner` starts a background scan.
3.  `scanner` sends `EntryMsg` for every scanned directory.
4.  `tui` updates the internal model and re-renders the list.
5.  `tui` handles keyboard events (navigation, deletion, sorting) and updates the view.

## 4. Technical Details

### 4.1 Parallel Scan Implementation
A `sync.WaitGroup` and a limited worker pool will be used to prevent hitting file descriptor limits.
```go
// Simplified concept
func scanDir(path string, results chan<- Entry) {
    entries, _ := os.ReadDir(path)
    for _, entry := range entries {
        // process...
        if entry.IsDir() {
            go scanDir(childPath, results) // In reality, use a worker pool
        }
    }
}
```

### 4.2 TUI Interaction
*   **Navigation**: Arrow keys for selection, Enter to enter directory, `0` or `backspace` to go up.
*   **Sorting**: Toggle between Size and MTime.
*   **Deletion**: Trigger `os.RemoveAll` with a TUI confirmation prompt.

### 4.3 Distribution (Bootstrap)
A `bootstrap.sh` script will:
1. Detect OS and Architecture (`uname -s`, `uname -m`).
2. Download the corresponding pre-compiled static binaire from GitHub Releases.
3. Cache it in `~/.local/bin` or `/tmp`.
4. Execute it with passed arguments.

## 5. Testing Strategy
*   **Unit Tests**: Test the scanner logic against a mock file system.
*   **Integration Tests**: Verify the embedded Bash script can still be extracted and executed.
*   **TUI Testing**: Use Bubble Tea's `tea.Model` testing capabilities.

## 6. Risks & Mitigations
*   **File Descriptor Limits**: Mitigated by using a bounded worker pool in the scanner.
*   **Platform Compatibility**: Go handles cross-compilation well, but we must ensure the `bootstrap.sh` is robust for all target shells (bash, zsh, fish).
