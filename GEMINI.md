# GEMINI.md

This file provides context and instructions for the `disk-explorer` Go rewrite.

## Project Overview

`disk-explorer` is a hybrid Go/Bash tool for exploring disk usage. It combines the performance and interactivity of Go with the portability of a Bash script.

### Architecture

The project is structured as a Go application that is distributed via a Bash wrapper.

1.  **Go Application**:
    *   **`cmd/disk-explorer/main.go`**: Entry point. Orchestrates the scanner and TUI.
    *   **`internal/scanner`**: Parallel, cancellable directory scanner. Implements "Lazy Scanning" (scanning only the current view's directory).
    *   **`internal/tui`**: Interactive interface built with **Bubble Tea** and **Lip Gloss**. Supports real-time updates and dynamic sorting.
    *   **`internal/remote`**: Native SSH orchestration using `golang.org/x/crypto/ssh`.
    *   **`internal/assets`**: Contains the embedded original Bash script.

2.  **Bash Wrapper (`disk-explorer.sh`)**:
    *   Acts as a **bootstrap script**.
    *   Detects OS (Linux/macOS) and Architecture (amd64/arm64).
    *   Checks for a cached Go binary in `~/.cache/disk-explorer/bin/`.
    *   Automatically downloads missing binaries from GitHub Releases.
    *   **Fallback**: Seamlessly executes the embedded Bash implementation if the Go binary fails to download or run.

## Building and Running

### Building

*   **Go Binary**: `go build -o disk-explorer ./cmd/disk-explorer`
*   **Bash Wrapper**: `./build.sh`
    *   This script concatenates the `src/` modules into `disk-explorer.sh`.
    *   Injects the current git version.
    *   Synchronizes the result to `internal/assets/disk-explorer.sh` for Go embedding.

### Running

```bash
# Default (attempts Go, falls back to Bash)
./disk-explorer.sh [PATH]

# Force original Bash implementation
./disk-explorer.sh --bash [PATH]
```

## Development Conventions

### Go
*   **Concurrency**: Use `context.Context` for cancelling scans when navigating.
*   **TUI**: Follow the Bubble Tea Model-Update-View pattern.
*   **Streaming**: Use channels to stream `Entry` objects from the scanner to the TUI.

### Bash (Fallback Logic)
*   **Modular**: Edits should be made in `src/*.sh`, then run `./build.sh`.
*   **GNU Coreutils**: Always use variable aliases (`$FIND_CMD`, etc.) for macOS compatibility.
*   **NUL Delimiters**: Use `\0` in all pipelines to safely handle special characters.
