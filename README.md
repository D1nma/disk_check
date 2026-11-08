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
- **Remote scans over SSH** — via the `disk-explorer.sh` distribution script and your system `ssh`; runs one scan per host and aggregates the reports (Go binary: not yet)

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
| `u` | Install a downloaded update and restart (shown in the banner when ready) |
| `i` | Ignore the downloaded update for this session |
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
disk-explorer --update           # download, verify and install the latest release
disk-explorer --bash             # force the Bash fallback (skip Go binary)
```

In the TUI, the update check runs in the background; when a newer release
exists the binary is downloaded and verified automatically, the banner
offers `[u] installer et redémarrer` or `[i] ignorer`. On `[u]` the
verified binary atomically replaces the running one and disk-explorer
relaunches itself on the same path and arguments. The installed updater
requires the release SHA256SUMS entry to match; `--update` does the same
without a restart prompt.

### Benchmark against ncdu

```bash
CGO_ENABLED=0 go build -o disk-explorer ./cmd/disk-explorer
python3 benchmarks/compare_ncdu.py /path/to/directory
```

The script alternates eight warm-cache runs and reports median completed-scan times. It runs `disk-explorer --summary` and `ncdu -0 --ignore-config -o /dev/null` on the same path. Their output work differs, so compare trends across directory shapes and storage devices rather than treating the ratio as a universal speed claim. On a Raspberry Pi 5 (Linux arm64, four CPUs), /usr (166 000 entries, ext4, warm cache) scans in about 137 ms versus 383 ms for ncdu 1.22 (2.8x), and a synthetic 204 000-entry tmpfs tree in about 162 ms versus 295 ms (1.8x).

The automatic scanner starts with one background worker and grows the pool when it discovers parallel directory work. The maximum follows `GOMAXPROCS` (four workers per CPU, capped at 32). Use `--scan-workers N` to cap it at 1–64 for storage where another limit performs better. Pass the same option to `benchmarks/compare_ncdu.py` to compare limits on your own machine.

For scanner-only Go measurements of wide and deep trees, run `go test ./internal/scanner -run '^$' -bench BenchmarkScan -benchmem`.

### Remote scan (SSH)

```bash
disk-explorer --remote --remote-hosts user@host1,user@host2
```

Runs through the `disk-explorer.sh` distribution script with your system `ssh`
(each host is scanned over SSH and the reports are aggregated). The compiled
Go binary does not offer this mode yet.

---

## Repository structure

```
install.sh               # one-liner installer (curl | bash)
disk-explorer.sh         # distribution script (bootstrap + bash fallback)
build.sh                 # assembles src/*.sh → disk-explorer.sh
cmd/disk-explorer/       # Go entry point
internal/
  tui/                   # Bubble Tea TUI (model, view, update loop)
  scanner/               # parallel scanner: zero-copy listing, TopFiles, mountinfo
  display/               # non-interactive formatters (summary, tree)
  updater/               # GitHub release check and self-update
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
| Go binary (default) | Linux or macOS. Static binaries (`CGO_ENABLED=0`) — any distro: glibc, musl/Alpine, Busybox. Linux archs: `amd64`, `386`, `arm` (ARMv6+ — Raspberry Pi 32-bit), `arm64`, `riscv64`, `ppc64le`, `s390x`, `loong64`, `mips`, `mipsle`, `mips64`, `mips64le` (MIPS builds use softfloat for FPU-less routers). macOS: `amd64`, `arm64`. `install.sh` and the `disk-explorer.sh` bootstrap auto-detect `uname -m`. |
| Bash fallback | Bash ≥ 4.4, GNU coreutils (`find -printf`, `sort -z`, `du -0`, `head -z`) — used automatically when no binary matches |

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
