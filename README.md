# disk-explorer

A fast, interactive disk usage explorer. Written in Go with a Bash bootstrap and fallback.

```bash
curl -fsSL https://raw.githubusercontent.com/D1nma/disk_check/main/install.sh | bash
```

```
DISK EXPLORER  /home/user  ALL · size ↓  ⠋
███████████████░░░░░ 78%  92.0 GiB / 116.6 GiB
────────────────────────────────────────────────────────
>    21.3 GiB  ██████████  .cargo/
     18.7 GiB  █████████░  node_modules/
      6.1 GiB  ███░░░░░░░  .local/
    512.0 MiB  ██░░░░░░░░  archive.tar.gz
    128.0 MiB  █░░░░░░░░░  Downloads/
────────────────────────────────────────────────────────
[↑↓/jk] nav  [Enter] cd  [←/h] retour  [s]ize [n]ame [t]ime  [q]uit
```

---

## Features

- **Real-time TUI** — built with [Bubble Tea](https://github.com/charmbracelet/bubbletea); shows a detailed progress screen during scanning and then switches to an instant browsing view
- **Full scanning** — high-performance parallel scan at startup builds a complete in-memory tree for zero-latency navigation (O(1))
- **Non-interactive modes** — `--summary`, `--report`, `--tree` for scripting and CI pipelines
- **Auto-update** — checks for new releases at startup; `--update` upgrades to the latest binary
- **SHA256 verification** — all downloaded binaries are verified against the release `SHA256SUMS` file
- **Bash fallback** — full-featured Bash implementation for offline or unsupported environments
- **Native SSH support** — scan remote hosts via the built-in SSH client (no system `ssh` required)

---

## Installation

### Installer (recommended)

```bash
curl -fsSL https://raw.githubusercontent.com/D1nma/disk_check/main/install.sh | bash
```

Installs the binary to `~/.local/bin`. Override with `DISK_EXPLORER_INSTALL_DIR=/usr/local/bin`.

### Run without installing

```bash
curl -fsSL https://raw.githubusercontent.com/D1nma/disk_check/main/disk-explorer.sh | bash
```

On a tagged release, the script downloads and caches the binary automatically. On untagged builds (or offline), it runs the full Bash fallback.

---

## Usage

### Interactive TUI

```bash
disk-explorer [PATH]

# Stay on the same filesystem (equivalent to du -x)
disk-explorer --mode partition /
```

**Key bindings**

| Key | Action |
|-----|--------|
| `↑` `↓` / `k` `j` | Navigate the list |
| `Enter` | Open directory (instant, no re-scan) |
| `Backspace` / `←` / `h` | Go to parent directory (instant, no re-scan) |
| `s` | Sort by size (press again to reverse) |
| `n` | Sort by name (press again to reverse) |
| `t` | Sort by modification date (press again to reverse) |
| `q` / `Ctrl+C` | Quit |

### Non-interactive modes

```bash
# Print top directories and files to stdout
disk-explorer --summary /var

# Skip one or more paths from the Go scan (options precede PATH)
disk-explorer --summary --exclude /var/cache --exclude /var/tmp /var

# Write a timestamped report to a file
disk-explorer --report /var
disk-explorer --report --report-dir ~/reports /var

# Tree view with sizes and percentages
disk-explorer --tree /var
disk-explorer --tree --tree-depth 5 /var

# Show 30 entries instead of the default 20
disk-explorer --top 30 --summary /

# Limit concurrent directory scans on slow or remote storage (0 = automatic)
disk-explorer --scan-workers 4 --summary /mnt/archive
```

### Maintenance

```bash
disk-explorer --version          # print the current version
disk-explorer --update           # download and cache the latest release
disk-explorer --bash             # force the Bash fallback (skip Go binary)
```

### Benchmark against ncdu

```bash
CGO_ENABLED=0 go build -o disk-explorer ./cmd/disk-explorer
python3 benchmarks/compare_ncdu.py /path/to/directory
```

The script alternates eight warm-cache runs and reports median completed-scan times. It runs `disk-explorer --summary` and `ncdu -0 --ignore-config -o /dev/null` on the same path. Their output work differs, so compare trends across directory shapes and storage devices rather than treating the ratio as a universal speed claim. On this Linux arm64 machine with four CPUs, a 100-directory, 20,000-empty-file tree scanned in about 16–18 ms versus 32–40 ms for ncdu 1.22, on both tmpfs and the repository filesystem. A flat 20,000-file directory took about 37–40 ms versus 33–36 ms for ncdu, and a 100-level chain remained slower.

The automatic scanner starts with one background worker and grows the pool when it discovers parallel directory work. The maximum follows `GOMAXPROCS` (four workers per CPU, capped at 32). Use `--scan-workers N` to cap it at 1–64 for storage where another limit performs better. Pass the same option to `benchmarks/compare_ncdu.py` to compare limits on your own machine.

For scanner-only Go measurements of wide and deep trees, run `go test ./internal/scanner -run '^$' -bench BenchmarkScan -benchmem`.

### Remote scan (SSH)

```bash
disk-explorer --remote --remote-hosts user@host1,user@host2
```

---

## Repository structure

```
install.sh               # one-liner installer (curl | bash)
disk-explorer.sh         # distribution script (bootstrap + bash fallback)
build.sh                 # assembles src/*.sh → disk-explorer.sh
cmd/disk-explorer/       # Go entry point
internal/
  tui/                   # Bubble Tea TUI (model, view, update loop)
  scanner/               # depth-1 scanner, ScanTree, ScanTopFiles
  display/               # non-interactive formatters (summary, tree)
  updater/               # GitHub release check and self-update
  remote/                # native Go SSH client
  assets/                # embedded bash script (used for remote runs)
src/                     # Bash implementation (fallback)
  main.sh                # constants, arg parsing, dispatch
  utils.sh               # pure helpers
  scan.sh                # du/find pipelines
  display.sh             # summary, report, tree output
  tui.sh                 # full-screen bash TUI
.github/workflows/
  ci.yml                 # build + test + vet on every push
  release.yml            # multi-platform release on v* tag push
```

---

## Requirements

| Mode | Requirements |
|------|-------------|
| Go binary (default) | Linux or macOS, amd64 or arm64 |
| Bash fallback | Bash ≥ 4.4, GNU coreutils (`find -printf`, `sort -z`, `du -0`, `head -z`) |

macOS Bash fallback: `brew install bash coreutils gawk findutils`

---

## Releasing a new version

```bash
git tag v1.2.3
git push origin v1.2.3
```

The `release.yml` workflow builds `linux/amd64`, `linux/arm64`, `darwin/amd64`, `darwin/arm64` binaries, generates `SHA256SUMS`, and publishes a GitHub Release automatically.

---

## License

MIT
