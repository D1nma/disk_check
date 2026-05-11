# CLAUDE.md

This file provides guidance to Claude Code (claude.ai/code) when working with code in this repository.

## Build

`disk-explorer.sh` is the single distributable file, assembled from `src/` modules:

```bash
./build.sh
```

The build concatenates modules in this order: `main.sh` header (inline code before the first function definition), then `utils.sh`, `scan.sh`, `display.sh`, `tui.sh`, then `main.sh` footer (all functions). After assembly, `bash -n` validates syntax before replacing the output file.

**Always run `build.sh` after editing any `src/*.sh` file** — the source files are authoritative, `disk-explorer.sh` is generated.

## Tests

Two test suites coexist:

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

### Module responsibilities

| File | Role |
|------|------|
| `src/main.sh` | Constants, global variables, traps, arg parsing, `main()` dispatch |
| `src/utils.sh` | Pure helpers: `die`, `human_size`, `sanitize_for_display`, `is_integer`, platform detection, `date_from_epoch` |
| `src/scan.sh` | Disk scanning: `build_du_cmd`, `build_find_prefix`, `scan_subdirs_to_file`, `scan_top_files_to_file`, temp file management, exclusion state |
| `src/display.sh` | Non-interactive outputs: `print_summary`, `print_tree_view`, `generate_report_file`, `self_check_report` |
| `src/tui.sh` | Full-screen TUI (`navigate`, `tui_draw`, `draw_header`, `draw_list`, `draw_footer`) and legacy fallback (`navigate_legacy`) |

### Key design decisions

**NUL-delimited records everywhere.** All scan pipelines use `find -printf '…\0'`, `sort -z`, `head -z` to handle filenames with spaces, newlines, and special characters safely.

**Two TUI modes.** `navigate()` probes `tput smcup`; if supported it enters the alternate screen buffer (`tui_enter`), otherwise falls back to `navigate_legacy` (clear + blocking `read`). `TUI_CAPABLE` tracks which mode is active.

**GNU tool aliasing for macOS.** `FIND_CMD`, `SORT_CMD`, `HEAD_CMD`, `DU_CMD`, `NUMFMT_CMD` are variables initialized to bare names (`find`, `sort`, …) and remapped to `gfind`, `gsort`, … by `resolve_gnu_tools_macos` when `PLATFORM=macos`. Always use these variables in scan commands, never the bare names.

**`main.sh` split by `build.sh`.** The awk pattern `^[[:space:]]*[a-zA-Z_][a-zA-Z0-9_]*[[:space:]]*\(\)/` finds the first function definition to split header (inline code) from footer (functions). Inline code (set -u, variable declarations, traps) must remain before any function definition in `main.sh`.

**Parallel scan in `print_summary`.** Subdirectory and top-files scans run as background jobs, both writing to separate temp files under `TEMP_ROOT`. The `ENABLE_SPINNER` flag is forced to 0 for these sub-jobs to avoid interleaved output.

**`NO_COLOR` compliance.** The variable is checked with `${NO_COLOR+x}` (portable with `set -u`) at initialization. Color codes are assigned only if stdout is a TTY and `NO_COLOR` is unset.

## Platform support

Targets GNU/Linux and macOS (with Homebrew GNU coreutils). Requires Bash ≥ 4.4. The shim at the top of `main.sh` re-execs via `/opt/homebrew/bin/bash` or `/usr/local/bin/bash` on macOS when system Bash is too old.

`numfmt` is optional — `human_size()` has an awk fallback.
