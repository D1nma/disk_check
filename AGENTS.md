# Repository Guidelines — disk-explorer (disk_check)

High-performance disk usage analyzer and interactive TUI written in Go with a standalone Bash bootstrap fallback and remote SSH scanning capabilities.

---

## 1. Project Structure & Module Organization

- **Go CLI entry point**: `cmd/disk-explorer/main.go`
- **Internal packages** (`internal/`):
  - `scanner/`: High-performance parallel recursive filesystem scanning with worker pools and atomic metrics.
  - `tui/`: Bubble Tea TUI with instant O(1) directory navigation, sorting, and filtering.
  - `remote/`: Native SSH remote disk scanning without installing dependencies on remote hosts.
  - `assets/`: Embeds the standalone Bash fallback script (`internal/assets/disk-explorer.sh`).
- **Bash implementation** (`src/`):
  - Modular scripts assembled by `build.sh` into the single-file distributable `disk-explorer.sh`.
- **Tests**: Go unit tests beside packages (`*_test.go`), Bash smoke tests in `tests/run_tests.sh`.

---

## 2. Build, Test, and Development Commands

### Build & Verification (Mandatory before opening PR)
```bash
go test ./...                  # Run all Go package unit tests
go build ./cmd/disk-explorer   # Build the Go binary locally
./build.sh                     # Rebuild standalone disk-explorer.sh & sync embedded assets
tests/run_tests.sh             # Run Bash smoke tests
```

*Note: Always run `./build.sh` after editing files in `src/` to ensure the generated shell script and Go embedded asset stay in sync.*

---

## 3. Coding Style & Conventions

- Format Go code strictly with `gofmt`.
- Keep package names short and lowercase. Export only necessary APIs.
- Bash scripts use `#!/usr/bin/env bash`, `set -euo pipefail`, lowercase function names, and uppercase global config variables.
- Untrusted input (file paths, remote hosts) must never be evaluated in raw shell execution.

---

## 4. Jules 360° Audit & Review Guidelines

When reviewing, maintaining, or generating optimizations for this repository, Jules MUST inspect code across these 6 core dimensions:

### 🛡️ 1. Sécurité & Injection de Commandes (Security / Sentinel)
- **SSH & Remote Host Injection**: Remote hostnames, SSH ports, and scan paths in `internal/remote/` and `src/remote.sh` must be strictly sanitized. Never interpolate unsanitized strings directly into `exec.Command("ssh", ...)` or `eval`.
- **Symlink Traversal & Directory Traversal**: Prevent infinite recursion on circular symlinks (`scanner` must track visited inodes or respect symlink following flags).
- **Filesystem Permissions**: The scanner should gracefully handle `EACCES` / permission-denied errors on restricted directories without aborting the scan.

### ⚡ 2. Performance & Optimisation Concurrente (Performance / Bolt)
- **Parallel Scanning & Goroutine Pools**: File scanning must use bounded worker pools or semaphore channels to avoid spawning millions of goroutines and exhausting file descriptors.
- **Slice & Buffer Reallocation**: Minimize memory allocations during directory walks (`filepath.WalkDir` instead of `filepath.Walk`). Re-use slices and string builders where possible.
- **Bubble Tea TUI Responsiveness**: Navigation and sorting of large directory trees (100k+ files) must remain O(1) or cached; do not block the Bubble Tea update loop with heavy computations.

### 🧩 3. Robustesse & Résilience (Reliability & Portability)
- **Bash Fallback Portability**: The generated `disk-explorer.sh` must run on minimal Linux environments (Busybox, coreutils, older bash 4.x) without requiring external dependencies like `jq` or `awk` extensions.
- **Terminal Resize Handling**: Bubble Tea TUI must cleanly adapt to window resize events (`tea.WindowSizeMsg`) without panic or garbled rendering.

### 🧹 4. Qualité de Code & Synchronisation (Clean Code)
- Keep Go packages cohesive and decouple scanner logic from TUI representation.
- Ensure `build.sh` validates syntax (`bash -n`) before assembling `disk-explorer.sh`.

### 🧪 5. Tests & Non-Régression (Test Coverage)
- Scanner speed, sorting algorithms, and remote string parsing must be covered by Go unit tests.
- All Go tests (`go test ./...`) and Bash tests must pass.

### 📦 6. Déploiement & Binaire Autonome (Zero-Dependency Delivery)
- The compiled Go binary must have zero runtime CGO dependencies (`CGO_ENABLED=0` for static cross-platform portability).
