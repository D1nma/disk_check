# Repository Guidelines — disk-explorer (disk_check)

High-performance disk usage analyzer and interactive TUI written in Go with a standalone Bash bootstrap fallback and remote SSH scanning capabilities.

---

## 1. Project Structure & Module Organization

- **Go CLI entry point**: `cmd/disk-explorer/main.go`
- **Internal packages** (`internal/`):
  - `scanner/`: High-performance parallel recursive filesystem scanning with worker pools and atomic metrics.
  - `tui/`: Bubble Tea TUI with instant O(1) directory navigation, sorting (size/name/date/growth), marks, cleanup plan, deletion with confirmation, rescan.
  - `snapshot/`, `reconcile/`, `live/`, `annotate/`, `export/`, `remote/`, `textsafe/`: scan history, df-vs-scan, live activity, path annotations, ncdu/Prometheus formats, agentless SSH scans, terminal-safe names (see `CLAUDE.md` for the full table — it is the reference).
- **Bash implementation** (`src/`):
  - Modular scripts assembled by `build.sh` into the single-file distributable `disk-explorer.sh`.
- **Tests**: Go unit tests beside packages (`*_test.go`), Bash smoke tests in `tests/run_tests.sh`.

---

## 2. Build, Test, and Development Commands

### Build & Verification (Mandatory before opening PR)
```bash
go test ./...                  # Run all Go package unit tests
go build ./cmd/disk-explorer   # Build the Go binary locally
./build.sh                     # Rebuild standalone disk-explorer.sh
tests/run_tests.sh             # Run Bash smoke tests
```

*Note: Always run `./build.sh` after editing files in `src/`: `disk-explorer.sh` is generated (there is no embedded Go asset). CI fails when it is stale.*

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
- **SSH & Remote Host Injection**: Remote hostnames, SSH ports, and scan paths must be strictly validated — in `src/remote.sh` (Bash `--remote`) and in `internal/remote` (Go `--ssh`: `ParseTarget`, `checkRemoteArg`; the remote script must stay free of `'` and `\`). Never interpolate unsanitized strings into a shell command or `eval`.
- **Terminal Injection**: file names, paths, process and container names are attacker-controlled. Pass them through `textsafe.Clean` before any terminal or report output (OSC 52 / title / cursor sequences, bidi overrides).
- **Symlink Traversal & Directory Traversal**: Prevent infinite recursion on circular symlinks (`scanner` must track visited inodes or respect symlink following flags).
- **Filesystem Permissions**: The scanner must handle `EACCES` without aborting, and must report it: `FlagUnreadable`/`FlagPartial`, `ScanProgress.Errors`, exit code 2. Never drop an error silently.

### ⚡ 2. Performance & Optimisation Concurrente (Performance / Bolt)
- **Parallel Scanning & Goroutine Pools**: File scanning must use bounded worker pools or semaphore channels to avoid spawning millions of goroutines and exhausting file descriptors.
- **Directory listing**: on Linux the scanner parses `getdents64` itself and calls `newfstatat` directly (zero-copy, slab-interned names, per-worker buffers). Do **not** replace it with `filepath.Walk`/`WalkDir`/`os.ReadDir`: they are slower. Measure with `go test ./internal/scanner -bench BenchmarkScan -benchmem` before and after any change.
- **Memory** is the scaling limit (~150 B per entry): keep `Node` compact (new per-node data goes in padding or in sparse maps on the scan result).
- **Bubble Tea TUI Responsiveness**: Navigation and sorting of large directory trees (100k+ files) must remain O(1) or cached; do not block the Bubble Tea update loop with heavy computations.

### 🧩 3. Robustesse & Résilience (Reliability & Portability)
- **Bash Fallback Portability**: The generated `disk-explorer.sh` must run on minimal Linux environments (Busybox, coreutils, older bash 4.x) without requiring external dependencies like `jq` or `awk` extensions.
- **Terminal Resize Handling**: Bubble Tea TUI must cleanly adapt to window resize events (`tea.WindowSizeMsg`) without panic or garbled rendering.

### 🧹 4. Qualité de Code & Synchronisation (Clean Code)
- Keep Go packages cohesive and decouple scanner logic from TUI representation.
- Ensure `build.sh` validates syntax (`bash -n`) before assembling `disk-explorer.sh`.

### 🧪 5. Tests & Non-Régression (Test Coverage)
- Scanner speed, sorting algorithms, and remote string parsing must be covered by Go unit tests.
- All Go tests (`go test -race ./...`), `tests/run_tests.sh` and bats must pass. Linux-only tests go in `*_linux_test.go` (the macOS CI job compiles the rest).

### 📦 6. Déploiement & Binaire Autonome (Zero-Dependency Delivery)
- The compiled Go binary must have zero runtime CGO dependencies (`CGO_ENABLED=0` for static cross-platform portability).
