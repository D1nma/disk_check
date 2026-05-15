# CLAUDE.md

This file provides guidance to Claude Code (claude.ai/code) when working with code in this repository.

## Build

### Go binary

```bash
go build ./cmd/disk-explorer
go build ./...          # build all packages
```

The version string is `"dev"` by default. At release it is injected via ldflags:

```bash
go build -ldflags "-X main.version=v1.2.3" -o disk-explorer ./cmd/disk-explorer
```

### Bash distributable

`disk-explorer.sh` is assembled from `src/` modules:

```bash
./build.sh
```

The build concatenates modules in this order: `main.sh` header (inline code before the first function definition), then `utils.sh`, `scan.sh`, `display.sh`, `tui.sh`, `remote.sh`, then `main.sh` footer (all functions). After assembly, `bash -n` validates syntax before replacing the output file. The assembled script is also copied to `internal/assets/disk-explorer.sh`.

`VERSION` in the assembled script is set to the exact git tag (`git describe --tags --exact-match`) when on a tagged commit, or the short git hash otherwise. Only exact tag versions trigger binary download in `try_go_binary`.

**Always run `build.sh` after editing any `src/*.sh` file** — the source files are authoritative, `disk-explorer.sh` is generated.

## Tests

### Go tests

```bash
go test ./...
go test ./internal/scanner/...    # scanner only
go test ./internal/tui/...        # TUI model only
```

### Bash test suites

Two suites coexist:

```bash
# Custom suite (sources disk-explorer.sh, tests functions directly)
bash tests/run_tests.sh

# Bats suite (requires bats installed)
bats tests/is_integer.bats
bats tests/platform_compat.bats
```

Both suites work by sourcing `disk-explorer.sh` and calling internal functions directly. The `BASH_SOURCE[0] == $0` guard in `main.sh` prevents `main()` from executing on source.

To run a single bats test by name:
```bash
bats tests/is_integer.bats --filter "is_integer: zero"
```

## Architecture

### Go packages

| Package | Role |
|---------|------|
| `cmd/disk-explorer` | Entry point: flag parsing, mode dispatch (TUI / summary / report / tree / update) |
| `internal/tui` | Bubble Tea model; streams entries from `scanner.Scan`; handles keyboard navigation, sort, history |
| `internal/scanner` | `Scan()` — depth-1 children with parallel cumulative dir sizes; `ScanTree()` — recursive tree up to maxDepth; `ScanTopFiles()` — top N files by disk usage |
| `internal/display` | Non-interactive output: `Summary()`, `Tree()`, `FormatSize()` |
| `internal/updater` | `LatestRelease()` — GitHub API; `UpdateAvailable()` — compare versions; `SelfUpdate()` — download + SHA256 verify + atomic rename |
| `internal/remote` | Native Go SSH client (`golang.org/x/crypto/ssh`) for remote host scanning |
| `internal/assets` | Embeds `disk-explorer.sh` for streaming to remote hosts over SSH |

### Bash modules

| File | Role |
|------|------|
| `src/main.sh` | Constants, global variables, traps, arg parsing, `main()` dispatch |
| `src/utils.sh` | Pure helpers: `die`, `human_size`, `sanitize_for_display`, `is_integer`, platform detection, `date_from_epoch` |
| `src/scan.sh` | Disk scanning: `build_du_cmd`, `build_find_prefix`, `scan_subdirs_to_file`, `scan_top_files_to_file`, temp file management, exclusion state |
| `src/display.sh` | Non-interactive outputs: `print_summary`, `print_tree_view`, `generate_report_file`, `self_check_report` |
| `src/tui.sh` | Full-screen TUI (`navigate`, `tui_draw`, `draw_header`, `draw_list`, `draw_footer`) and legacy fallback (`navigate_legacy`) |
| `src/remote.sh` | SSH orchestration and report aggregation |

### Key Go design decisions

**Depth-1 scanner with parallel goroutines.** `scanner.Scan()` lists only direct children. Files emit immediately; each directory spawns a goroutine that calls `sumDir()` (recursive `filepath.WalkDir`) and emits once complete. A semaphore of 4 limits concurrency. This makes the TUI feel responsive: files appear instantly and directories fill in as they finish.

**Generation counter for stale-message rejection.** `Model.generation` is incremented on every navigation. `entryMsg` and `scanDoneMsg` carry the generation at which they were dispatched; messages from a previous generation are silently dropped. This prevents entries from a slow old scan appearing after the user has already navigated elsewhere.

**`blockSize` uses `Stat_t.Blocks * 512`.** All size measurements use actual disk allocation (512-byte blocks × number of blocks), not apparent file size, matching `du` behavior.

**`ScanTree` builds a tree by bounded recursion.** At leaf depth (`currentDepth >= maxDepth`), it calls `sumDir()` for the cumulative size. At inner depths, it recurses, accumulating child sizes. Children are sorted by size descending before being stored.

**Non-interactive modes share the same scanner.** `--summary` drains `scanner.Scan()` synchronously; `--tree` calls `scanner.ScanTree()`; `--report` wraps `--summary` output in an atomic file write (tmp + rename). No TUI is started.

**Background update check in TUI.** A goroutine calls `updater.UpdateAvailable()` (5 s HTTP timeout) and sends to `UpdateChan` if a newer release exists. The TUI model listens via `listenForUpdate()` and shows a yellow footer banner on receipt. The check is skipped for `"dev"` builds.

### Key Bash design decisions

**NUL-delimited records everywhere.** All scan pipelines use `find -printf '…\0'`, `sort -z`, `head -z` to handle filenames with spaces, newlines, and special characters safely.

**Two TUI modes.** `navigate()` probes `tput smcup`; if supported it enters the alternate screen buffer (`tui_enter`), otherwise falls back to `navigate_legacy` (clear + blocking `read`). `TUI_CAPABLE` tracks which mode is active.

**GNU tool aliasing for macOS.** `FIND_CMD`, `SORT_CMD`, `HEAD_CMD`, `DU_CMD`, `NUMFMT_CMD` are variables initialized to bare names and remapped to `gfind`, `gsort`, … by `resolve_gnu_tools_macos` when `PLATFORM=macos`. Always use these variables in scan commands, never the bare names.

**`main.sh` split by `build.sh`.** The awk pattern `^[[:space:]]*[a-zA-Z_][a-zA-Z0-9_]*[[:space:]]*\(\)/` finds the first function definition to split header (inline code) from footer (functions). Inline code (`set -u`, variable declarations, traps) must remain before any function definition in `main.sh`.

**`NO_COLOR` compliance.** The variable is checked with `${NO_COLOR+x}` (portable with `set -u`) at initialization. Color codes are assigned only if stdout is a TTY and `NO_COLOR` is unset.

## CI / CD

| Workflow | Trigger | Action |
|----------|---------|--------|
| `ci.yml` | Push / PR to `main` | `go build ./...`, `go test ./...`, `go vet ./...` |
| `release.yml` | Push of a `v*` tag | Build 4 platform binaries with version ldflags, generate `SHA256SUMS`, publish GitHub Release |

Go version is read from `go.mod` via `go-version-file: 'go.mod'` in `actions/setup-go`.

To publish a release:

```bash
git tag v1.2.3
git push origin v1.2.3
```

## Platform support

**Go binary**: Linux and macOS, amd64 and arm64. Built with `CGO_ENABLED=0` for static linking.

**Bash fallback**: GNU/Linux and macOS (with Homebrew GNU coreutils). Requires Bash ≥ 4.4. The shim at the top of `main.sh` re-execs via `/opt/homebrew/bin/bash` or `/usr/local/bin/bash` on macOS when the system Bash is too old.

`numfmt` is optional — `human_size()` has an awk fallback.
