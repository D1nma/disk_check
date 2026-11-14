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

The build concatenates modules in this order: `main.sh` header (inline code before the first function definition), then `utils.sh`, `scan.sh`, `display.sh`, `tui.sh`, `remote.sh`, then `main.sh` footer (all functions). After assembly, `bash -n` validates syntax before replacing the output file.
`build.sh` rewrites two placeholder lines in the `main.sh` header, using only release tags (`v[0-9]*`):
- `VERSION` (display): the exact tag on a tagged commit, otherwise `git describe --tags` (`<latest-tag>-<n>-g<hash>`), otherwise the short hash (`dev` outside a git repo).
- `BINARY_TAG` (bootstrap): the exact tag, otherwise the latest reachable tag (`git describe --tags --abbrev=0`), otherwise empty. `try_go_binary` and the binary cache (`~/.cache/disk-explorer/bin/$BINARY_TAG`) use it, and run only when it matches `^v[0-9]`. A rebuild on an untagged commit of `main` therefore keeps the latest release's binary. When no tag is reachable (or tags were not fetched), the script runs Bash only.

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

The bats `setup()` restores bats' own EXIT trap after sourcing the script (whose `trap cleanup EXIT` would otherwise make failing tests vanish from the report).

Both suites work by sourcing `disk-explorer.sh` and calling internal functions directly. The `BASH_SOURCE[0] == $0` guard in `main.sh` prevents `main()` from executing on source.

To run a single bats test by name:
```bash
bats tests/is_integer.bats --filter "is_integer: zero"
```

## Architecture

### Go packages

| Package | Role |
|---------|------|
| `cmd/disk-explorer` | Entry point: `run()` (testable), flag parsing, sources (local scan, `--import`, `--ssh`), combinable outputs, TUI wiring (`hooks.go`), fleet table (`fleet.go`), exit codes 0/1/2/64 |
| `internal/tui` | Bubble Tea model (`model.go`), rendering (`view.go`), marks/plan/delete/rescan/OSC 52 (`actions.go`); optional `Hooks` for annotations, df-vs-scan and live activity |
| `internal/scanner` | `Scan()` — parallel scan building the full `Node` tree plus results (errors, `Pinned`/`Freeable`, `DevBytes`, `Recent`, `Expected`); `TopFiles()`; `Analyze()` for imported trees |
| `internal/display` | Non-interactive output: `Summary()`, `SummaryJSON()`, `Tree()`, `FormatSize()` |
| `internal/snapshot` | Scan history: `FromTree`, `Store.Save/Latest`, `Delta`, `TopGrowth` |
| `internal/reconcile` | df vs scan: `Stat()` (statfs + mountinfo), `DeletedOpen()` (/proc), `Build/Render/Lines` |
| `internal/live` | `FillMeter` (fill rate, ETA), `Tracker` (writers and growing files from /proc) |
| `internal/annotate` | Path labels (Docker, podman, containerd, Kubernetes, system caches, dev artifacts) and cleanup commands |
| `internal/export` | ncdu JSON `WriteNcdu`/`ReadNcdu` (streaming), Prometheus `WriteProm` |
| `internal/remote` | Agentless SSH scan: probe, push binary, `--export -`, read back |
| `internal/textsafe` | `Clean()` — neutralise control chars, escapes, bidi overrides in untrusted names |
| `internal/updater` | `LatestRelease()`, `Newer()` (semver), `UpdateAvailable()`, `CheckEnabled()`, `FetchBinary()` (any GOOS/GOARCH, SHA256-verified), `SelfUpdate()` |

### Bash modules

| File | Role |
|------|------|
| `src/main.sh` | Constants, global variables, traps, Go bootstrap (`try_go_binary`, `go_args_from_bash_args`, `download_binary`), arg parsing, `main()` dispatch |
| `src/utils.sh` | Pure helpers: `die`, `human_size`, `sanitize_for_display`, `is_integer`, platform detection, `date_from_epoch` |
| `src/scan.sh` | Disk scanning: `build_du_cmd`, `build_find_prefix`, `scan_subdirs_to_file`, `scan_top_files_to_file`, temp file management, exclusion state |
| `src/display.sh` | Non-interactive outputs: `print_summary`, `print_tree_view`, `generate_report_file`, `self_check_report` |
| `src/tui.sh` | Full-screen TUI (`navigate`, `tui_draw`, `draw_header`, `draw_list`, `draw_footer`) and legacy fallback (`navigate_legacy`) |
| `src/remote.sh` | SSH orchestration and report aggregation |

### Key Go design decisions

**Parallel recursive scanner with post-scan aggregation.** `scanner.Scan()` performs a high-performance parallel walk of the entire filesystem. It builds a bidirectional tree in memory. Size and count aggregation is performed in a fast post-scan pass to minimize mutex contention. Workers grow on demand when the queue fills, capped at `GOMAXPROCS×4` (32) by default; `--scan-workers` allows 1–64. Linux listing is zero-copy (raw getdents64 parsing, slab-interned names, direct newfstatat), and filesystem types are inherited from `/proc/self/mountinfo` except at mount roots.

**Errors are part of the result.** A missing/unreadable root ends the scan with `ScanProgress.Err` (the TUI quits with the message, CLI exits 1). Unreadable directories (or entries failing `fstatat` with anything but `ENOENT`) get `FlagUnreadable`, ancestors `FlagPartial`; they are listed in `Errors` and make non-interactive modes exit 2.

**Deterministic hard links.** Files with `nlink > 1` are collected per worker (no lock while walking) and attributed after the walk to the smallest path; other links get size 0 and `FlagHardlink`. `Pinned` records bytes a deletion would not free (links outside the subtree or the scan); use `scanner.Freeable(n, pinned)`.

**Every name printed is untrusted.** Always pass names, paths, process and container names through `textsafe.Clean` before writing them to a terminal or a report comment. The cleanup plan leaves out paths with control characters rather than quoting them.

**Snapshots drive growth and progress.** `cmd` loads the latest snapshot of (root, mode, excludes) before scanning (`ExpectedHint`, Δ column) and saves a new one after. `scanner.expectedEntries` uses statfs used-inode counts when the root is a mount point.

**Remote and imported trees are read-only.** `tui.Model.Imported` disables delete, rescan, statfs and local hooks; `cmd` never wires `localHooks()` for them. The remote command is a single-quoted POSIX sh script with no `'` or `\` inside, so it works under any login shell; user values are validated by `remote.checkRemoteArg`.

**Instant O(1) navigation.** After the initial scan, the TUI enters a browsing state where all navigation (Enter, Backspace) only involves updating a pointer to the existing tree. No further disk I/O occurs during navigation.

**Generation counter for stale-message rejection.** `Model.generation` is incremented on every navigation. `progressMsg` carries the generation at which it was dispatched; messages from a previous generation are silently dropped.

**`blockSize` uses `Stat_t.Blocks * 512`.** All size measurements use actual disk allocation (512-byte blocks × number of blocks), not apparent file size, matching `du` behavior.

**`--tree` derives from the full scan.** `runTree` drains the same parallel `Scan()` and `display.Tree` renders a depth-limited view. Directory sizes shown by `--tree` are du-style content sizes (`Node.Size - Node.OwnSize`, the inode's own blocks excluded), and hardlinks are counted once, so `--tree`, `--summary`, and the TUI agree on every number. The line renderer is fmt-free but byte-identical to the previous output.

**Non-interactive modes share the same scanner.** `--summary` drains `scanner.Scan()` synchronously; `--tree` does the same and renders a depth-limited view; `--report` wraps `--summary` output in an atomic file write (tmp + rename). No TUI is started.

**Background update check in TUI.** A goroutine calls `updater.UpdateAvailable()` (5 s HTTP timeout, semver comparison) and sends to `UpdateChan` if a newer release exists. The TUI model listens via `listenForUpdate()` and shows a yellow footer banner on receipt. The check is skipped for `"dev"` builds and when `DISK_EXPLORER_NO_UPDATE` is set; when the binary's directory is not writable the banner only announces the release.

### Key Bash design decisions

**Bootstrap execs the Go binary only with translated flags.** The Go CLI (stdlib `flag`) exits 2 on any flag it does not define. `go_args_from_bash_args` (pure, fills `GO_ARGS`) maps the flags that have an exact Go equivalent (`--path DIR` → positional, `--top-count N` → `--top N`, `--no-color` → `NO_COLOR=1`, …). It returns 1 for Bash-only flags (`--remote*`, `--self-check`, `--sort mtime`, `--file-size apparent`, `--max-depth`, `--top-files`, `--no-default-excludes`, `-h/--help`, `--bash`), unknown flags and values `parse_args` would reject; `try_go_binary` then returns and the Bash implementation runs. Remote mode sends `--bash` to the targets (`remote_build_args`) so they always produce the Bash summary and exit codes (0 ok, 2 partial) that `remote_run_all` expects.

**NUL-delimited records everywhere.** All scan pipelines use `find -printf '…\0'`, `sort -z`, `head -z` to handle filenames with spaces, newlines, and special characters safely.

**Two TUI modes.** `navigate()` probes `tput smcup`; if supported it enters the alternate screen buffer (`tui_enter`), otherwise falls back to `navigate_legacy` (clear + blocking `read`). `TUI_CAPABLE` tracks which mode is active.

**GNU tool aliasing for macOS.** `FIND_CMD`, `SORT_CMD`, `HEAD_CMD`, `DU_CMD`, `NUMFMT_CMD` are variables initialized to bare names and remapped to `gfind`, `gsort`, … by `resolve_gnu_tools_macos` when `PLATFORM=macos`. Always use these variables in scan commands, never the bare names.

**`main.sh` split by `build.sh`.** The awk pattern `^[[:space:]]*[a-zA-Z_][a-zA-Z0-9_]*[[:space:]]*\(\)/` finds the first function definition to split header (inline code) from footer (functions). Inline code (`set -u`, variable declarations, traps) must remain before any function definition in `main.sh`.

**`NO_COLOR` compliance.** The variable is checked with `${NO_COLOR+x}` (portable with `set -u`) at initialization. Color codes are assigned only if stdout is a TTY and `NO_COLOR` is unset.

## CI / CD

| Workflow | Trigger | Action |
|----------|---------|--------|
| `ci.yml` | Push / PR to `main` | gofmt, `go vet`, `go test -race ./...`; macOS `go test`; Bash: `build.sh` sync check, shellcheck (errors), `tests/run_tests.sh`, bats; cross-compile of every release target |
| `release.yml` | Push of a `v*` tag | Build 14 static binaries (12 Linux archs, 2 macOS) with version ldflags, generate `SHA256SUMS`, publish GitHub Release |

Go version is read from `go.mod` via `go-version-file: 'go.mod'` in `actions/setup-go`.

To publish a release:

```bash
git tag v1.2.3
git push origin v1.2.3
```

## Platform support

**Go binary**: Linux (amd64, 386, arm v6+, arm64, riscv64, ppc64le, s390x, loong64, mips/mipsle/mips64/mips64le softfloat) and macOS (amd64, arm64). Built with `CGO_ENABLED=0` for static linking. `/proc`-based features (`live.Tracker`, `reconcile.DeletedOpen`, mountinfo) are Linux-only behind build tags with stubs; Linux-only tests live in `*_linux_test.go`.

**Bash fallback**: GNU/Linux and macOS (with Homebrew GNU coreutils). Requires Bash ≥ 4.4. The shim at the top of `main.sh` re-execs via `/opt/homebrew/bin/bash` or `/usr/local/bin/bash` on macOS when the system Bash is too old.

`numfmt` is optional — `human_size()` has an awk fallback.
