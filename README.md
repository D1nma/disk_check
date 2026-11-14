# disk-explorer

A fast, interactive disk usage explorer. Written in Go with a Bash bootstrap and fallback.

```bash
curl -fsSL https://raw.githubusercontent.com/D1nma/disk_check/main/install.sh | bash
```

```
DISK EXPLORER  /var  TOUT · taille ↓  v2.3.0
███████████████░░░░░ 78%  92.0 GiB / 116.6 GiB  · libre 24.6 GiB  ▲ +45.0 MiB/s → plein dans 9m
  31.2 GiB  27% du disque  1204331 fichiers  98211 dossiers  modifié < 24 h : 3.4 GiB  ⚠ 2 illisible(s) [e]
────────────────────────────────────────────────────────────────────────────────────────────
>   21.3 GiB   68.3%  ██████████    +3.2 GiB  lib/  · données Docker
     6.1 GiB   19.6%  ███░░░░░░░    +0.2 MiB  log/  ⚠
★  512.0 MiB    1.6%  ░░░░░░░░░░    ±0        cache/  · cache de paquets apt
────────────────────────────────────────────────────────────────────────────────────────────
  données Docker → docker system df  puis  docker system prune · Δ +3.2 GiB depuis le 13/11 09:12
[?] aide  [⏎] ouvrir  [←] retour  [s/n/t/c] tri  [espace] marquer  [d] suppr  [f] gros fichiers  …
```

Built for the disk-full incident on a server: not only *what is big*, but
*what grew, who is writing, why `df` disagrees with the scan, and what can be
removed safely* — locally or on any Linux host over SSH, without installing
anything there.

---

## Features

- **Fast full scan** — parallel scanner (zero-copy `getdents64` on Linux) builds the whole tree once; navigation is instant (O(1)), no re-scan.
- **Honest numbers** — unreadable directories are flagged (⚠), listed (`e`) and turn the exit code to 2; hard links are counted once and attributed deterministically; "really freeable" accounts for links that live elsewhere.
- **What grew?** — every scan records a small snapshot; the next one shows a Δ column (`c` sorts by growth) and the summary lists the biggest growths. Bytes modified in the last 24 h are shown even on the first run.
- **Real progress** — percentage and ETA from the filesystem's used-inode count (or the previous snapshot).
- **Why does `df` disagree?** — `w` in the TUI or `--why PATH`: deleted-but-still-open files (process, PID and the command to free them), unreadable directories, reserved blocks, unexplained remainder.
- **Live activity** — header shows the fill rate and time-to-full; `a` lists the files growing right now and the processes writing.
- **Knows what paths are** — Docker overlay layers, container logs and volumes, Kubernetes pods, podman, containerd, journald, package caches, core dumps, rotated logs, `node_modules`, `target/`… with the *proper* cleanup command (`docker image prune`, `journalctl --vacuum-size`, `apt-get clean`…).
- **Safe actions** — mark entries (`space`), review (`m`), write a reviewable cleanup script (`x`), delete with confirmation (`d`, `D`) or forbid it (`--read-only`); rescan the current directory (`r`); copy a path to your local clipboard even through SSH (`y`, OSC 52).
- **Agentless remote scans** — `--ssh host:/path` pushes the right static binary (14 architectures) through SSH, scans, brings the tree back and cleans up. Several hosts give a fleet table.
- **Open formats** — `--json`, `--export`/`--import` (ncdu JSON, both ways), `--prom` (node_exporter textfile collector).
- **Safe output** — file names are attacker-controlled: control characters, terminal escapes and bidi overrides are neutralised everywhere.
- **Auto-update** — background check (opt out with `DISK_EXPLORER_NO_UPDATE=1`), SHA256-verified, semver-aware.
- **Bash fallback** — full Bash implementation for hosts without a matching binary.

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

The script downloads, verifies and caches the Go binary of the release it was built from: the exact tag, or else the latest release tag reachable from its commit, so the copy on `main` uses the latest release. It runs the binary when every option has a Go equivalent (`--path DIR` becomes the positional path, `--top-count N` becomes `--top N`). Bash-only options (`--remote*`, `--self-check`, `--sort mtime`, `--file-size apparent`, `--max-depth`, `--top-files`, `--no-default-excludes`, `--help`, `--bash`) use the full Bash fallback. So do builds with no release tag and runs where the download fails (for example offline).

---

## Usage

### Interactive TUI

```bash
disk-explorer [PATH]

# Stay on the same filesystem (equivalent to du -x)
disk-explorer --mode partition /
```

**Key bindings** (`?` shows them in the TUI)

| Key | Action |
|-----|--------|
| `↑` `↓` / `k` `j`, `PgUp` `PgDn`, `g` `G` | Navigate the list |
| `Enter` / `→` / `l` | Open directory (instant, no re-scan) |
| `Backspace` / `←` / `h` | Go to parent directory |
| `s` `n` `t` `c` | Sort by size / name / date / growth since the last scan (again: reverse) |
| `f` | Largest files below the current directory (`Enter` jumps to the file) |
| `e` | Unreadable directories (the scan is incomplete) |
| `w` | Why `df` ≠ scan for this filesystem |
| `a` | Live activity: growing files, writing processes |
| `space` | Mark / unmark (running total in the footer) |
| `m` | Review marks: `x` writes a cleanup script to review, `D` deletes them all |
| `d` | Delete the selection (confirmation; disabled by `--read-only`, remote and imported trees) |
| `r` | Rescan the current directory |
| `y` | Copy the path to the clipboard (OSC 52, works over SSH/tmux) |
| `u` / `i` | Install / ignore a downloaded update |
| `q` / `Ctrl+C` | Quit |

The detail line under the list says, for the selected entry, what deleting it
really frees (hard links), recent writes, growth, and what the path is
(container, cache) with the suggested cleanup command.

### Non-interactive modes

```bash
disk-explorer --summary /var                 # top directories/files, growth, warnings
disk-explorer --json /var | jq .top_dirs     # same, as JSON
disk-explorer --tree --tree-depth 5 /var     # tree view with sizes and percentages
disk-explorer --report --report-dir ~/reports /var
disk-explorer --why /                        # explain df vs scan for this filesystem

# Options precede PATH
disk-explorer --summary --exclude /var/cache --exclude /var/tmp /var
disk-explorer --top 30 --summary /
disk-explorer --scan-workers 4 --summary /mnt/archive   # slow/remote storage
```

Without a mode flag and with stdout redirected, the summary is printed (as the
Bash version does). Outputs combine on a single scan, which suits cron:

```bash
# every hour: Prometheus metrics for node_exporter + a saved scan
disk-explorer --mode partition --prom /var/lib/node_exporter/textfile/disk.prom \
              --export /var/tmp/disk-scan.json /
```

Exit codes: `0` ok, `1` fatal (path missing or unreadable), `2` partial scan
(unreadable directories), `64` usage error.

### Scan history and growth

Each complete scan (TUI or not) stores the sizes of entries ≥ 1 MiB in
`~/.local/state/disk-explorer/snapshots` (`$XDG_STATE_HOME`,
`$DISK_EXPLORER_STATE_DIR`; 20 per root, a few hundred KiB). The next scan of
the same root, mode and exclusions shows the Δ column and the growth section.
Disable with `--no-snapshot` or `DISK_EXPLORER_NO_SNAPSHOT=1`.

### Remote hosts over SSH

```bash
disk-explorer --ssh web1:/var                  # TUI on the remote tree
disk-explorer --ssh root@db1:/ --summary        # or any output: --json, --tree, --export, --prom
disk-explorer --ssh web1,web2,db1 /             # several hosts: fleet table
disk-explorer --hosts-file servers.txt --ssh-jobs 8 --report --report-dir reports/
disk-explorer --ssh admin@nas --ssh-sudo --ssh-opt "-p 2222" /volume1
```

The remote host needs only `sh` and `mktemp`: no Bash, no coreutils, no
Internet. disk-explorer asks `uname -sm`, sends its own binary (or the release
binary for that architecture, downloaded once, SHA256-verified and cached),
runs it in a private temporary directory (falls back if `/tmp` is `noexec`),
reads the tree back and removes the binary. Authentication is your usual SSH
setup (`BatchMode=yes`: keys or agent). `--ssh-sudo` runs `sudo -n`.
Remote trees are read-only in the TUI; marks still produce a cleanup script
you can run on the host. Remote scans are recorded per host for growth.

The Bash distribution script keeps its own `--remote` mode
(`disk-explorer.sh --remote --remote-hosts user@host1,user@host2`).

### Export, import, Prometheus

```bash
disk-explorer --export scan.json /srv     # ncdu's JSON export format (format 1.2)
disk-explorer --import scan.json          # browse it later, elsewhere (read-only)
ncdu -o- /srv | disk-explorer --import - --summary
disk-explorer --prom /var/lib/node_exporter/textfile/disk.prom --prom-depth 2 /
```

The import accepts ncdu JSON exports too (hard links counted once, unreadable
and excluded entries honoured); a scan exported by disk-explorer is meant to
open in `ncdu -f`, but that direction is not yet tested in CI.

`--prom` writes `disk_explorer_directory_bytes{host,path}`, `..._files`,
`..._incomplete` and the scan timestamp for directories down to
`--prom-depth` (≥ 100 MiB), atomically.

### Environment

| Variable | Effect |
|----------|--------|
| `DISK_EXPLORER_NO_UPDATE` | no background update check |
| `DISK_EXPLORER_NO_SNAPSHOT` | no scan history |
| `DISK_EXPLORER_STATE_DIR` | where snapshots are kept |
| `DISK_EXPLORER_PLAN_DIR` | where `x` writes cleanup scripts (default: current directory) |
| `NO_COLOR` | no colours |

### Maintenance

```bash
disk-explorer --version          # print the current version
disk-explorer --update           # download, verify and install the latest release
disk-explorer.sh --bash          # distribution script: force the Bash fallback (skip Go binary)
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

---

## Repository structure

```
install.sh               # one-liner installer (curl | bash)
disk-explorer.sh         # distribution script (bootstrap + bash fallback)
build.sh                 # assembles src/*.sh → disk-explorer.sh
cmd/disk-explorer/       # Go entry point
internal/
  tui/                   # Bubble Tea TUI (model, view, actions)
  scanner/               # parallel scanner: zero-copy listing, errors, hard links, mountinfo
  display/               # non-interactive formatters (summary, JSON, tree)
  snapshot/              # scan history (growth since the last scan)
  reconcile/             # df vs scan: deleted-but-open files, reserved blocks
  live/                  # fill rate, time-to-full, growing files and writers
  annotate/              # docker/k8s/cache path labels and cleanup commands
  export/                # ncdu JSON export/import, Prometheus textfile
  remote/                # agentless SSH scans
  textsafe/              # neutralise terminal escapes in untrusted names
  updater/               # GitHub release check and self-update
src/                     # Bash implementation (fallback)
  main.sh                # constants, arg parsing, dispatch
  utils.sh               # pure helpers
  scan.sh                # du/find pipelines
  display.sh             # summary, report, tree output
  tui.sh                 # full-screen bash TUI
.github/workflows/
  ci.yml                 # Go (race), macOS, Bash suites, cross-compile
  release.yml            # multi-platform release on v* tag push
```

---

## Requirements

| Mode | Requirements |
|------|-------------|
| Go binary (default) | Linux or macOS. Static binaries (`CGO_ENABLED=0`) — any distro: glibc, musl/Alpine, Busybox. Linux archs: `amd64`, `386`, `arm` (ARMv6+ — Raspberry Pi 32-bit), `arm64`, `riscv64`, `ppc64le`, `s390x`, `loong64`, `mips`, `mipsle`, `mips64`, `mips64le` (MIPS builds use softfloat for FPU-less routers). macOS: `amd64`, `arm64`. `install.sh` and the `disk-explorer.sh` bootstrap auto-detect `uname -m`. |
| Bash fallback | Bash ≥ 4.4, GNU coreutils (`find -printf`, `sort -z`, `du -0`, `head -z`) — used automatically when no binary matches, the build has no release tag, or an option is Bash-only |

macOS Bash fallback: `brew install bash coreutils gawk findutils`

---

## Releasing a new version

```bash
git tag v1.2.3
git push origin v1.2.3
```

The `release.yml` workflow builds the 14 static binaries listed under Requirements (12 Linux architectures, 2 macOS), generates `SHA256SUMS`, and publishes a GitHub Release automatically. Remote scans of hosts of another architecture download those release binaries.

---

## License

MIT
