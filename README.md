# disk-explorer

A fast, portable disk usage explorer. Written in Go with a seamless Bash bootstrap and fallback.

```bash
bash <(curl -fsSL https://raw.githubusercontent.com/D1nma/disk_check/main/disk-explorer.sh)
```

```
DISK EXPLORER  /home/user  MÊME PARTITION · size
████████░░░░░░░░░░░ 42%   58 Go / 230 Go
────────────────────────────────────────────────────────────────────────────
   1)    21.3 GiB  [##########]  .cargo/
   2)    18.7 GiB  [########  ]  node_modules/
   3)     6.1 GiB  [###       ]  .local/
   4)   512.0 MiB  [##        ]  archive.tar.gz
   5)   128.0 MiB  [#         ]  Downloads/
────────────────────────────────────────────────────────────────────────────
  [↑↓] navigate  [Enter] open  [s] sort  [n] name  [t] date  [q] quit
```

---

## Features

- **Hybrid Architecture** — A pre-compiled Go binary for speed, wrapped in a portable Bash script for a zero-install experience.
- **Lazy Scanning** — Only scans the directory you are looking at. Fast, efficient, and keeps memory usage to a minimum.
- **Real-time TUI** — Built with [Bubble Tea](https://github.com/charmbracelet/bubbletea), entries stream live as they are discovered.
- **Native SSH Support** — Built-in SSH client (`golang.org/x/crypto/ssh`) to scan remote hosts without depending on the system's `ssh` command.
- **Automatic Distribution** — The Bash wrapper detects your OS/Architecture and automatically downloads the correct Go binary from GitHub Releases.
- **Reliable Fallback** — If the Go binary can't be run (unsupported platform or offline), it seamlessly falls back to the original full-featured Bash implementation.

---

## Quick start

### Universal Command

```bash
bash <(curl -fsSL https://raw.githubusercontent.com/D1nma/disk_check/main/disk-explorer.sh)
```

The script will automatically detect your platform (Linux/macOS), architecture (amd64/arm64), and download the appropriate Go binary.

### Explicit Bash Fallback

If you wish to force the legacy Bash implementation:

```bash
./disk-explorer.sh --bash [PATH]
```

---

## Usage

```bash
# Current directory
./disk-explorer.sh

# Specific path
./disk-explorer.sh /var

# Remote scan via native Go SSH
./disk-explorer.sh --remote --remote-hosts user@host1,user@host2
```

### TUI key bindings

| Key | Action |
|---|---|
| `↑` `↓` (or `k` `j`) | Navigate list |
| `Enter` | Open directory (Lazy Scan) |
| `Backspace` (or `←` `h`) | Go up to parent directory |
| `s` | Sort by Size (toggle Asc/Desc) |
| `n` | Sort by Name (toggle Asc/Desc) |
| `t` | Sort by Date (toggle Asc/Desc) |
| `q` | Quit |

---

## Repository structure

```
disk-explorer.sh      # distribution script (bootstrap + bash fallback)
build.sh              # build orchestrator (Go build + Bash assembly)
cmd/disk-explorer/    # Go entry point
internal/
  tui/                # Bubble Tea UI logic
  scanner/            # Parallel directory scanner
  remote/             # Native Go SSH client
  assets/             # Embedded bash script for fallback/remote
src/                  # Original Bash implementation (modules)
docs/
  superpowers/        # architecture and implementation history
```

---

## Requirements

- **Go version (default)**: Linux or macOS (amd64/arm64).
- **Bash version (fallback)**: GNU/Linux with Bash ≥ 4.4 and standard GNU coreutils.

---

## License

MIT
