# GEMINI.md

This file provides context and instructions for working with the `disk-explorer` project.

## Project Overview

`disk-explorer` is a portable, single-file Bash TUI (Text User Interface) for exploring disk usage, inspired by tools like `ncdu`. It is designed to be highly compatible across Linux and macOS (with Homebrew GNU coreutils) and requires no installation—it can even be run via `curl | bash`.

### Architecture

The project follows a modular structure in `src/`, which is assembled into a single distributable script `disk-explorer.sh` via a build process.

-   **`src/main.sh`**: Entry point, constants, global variables, and argument parsing. It is split into a "header" (inline code) and a "footer" (functions) by the build script.
-   **`src/utils.sh`**: Pure helper functions (formatting, validation, platform detection).
-   **`src/scan.sh`**: Logic for scanning directories using `du` and `find`.
-   **`src/display.sh`**: Non-interactive output modes (summary, report, tree).
-   **`src/tui.sh`**: Full-screen TUI logic and navigation.
-   **`src/remote.sh`**: SSH orchestration for scanning remote hosts.

## Building and Running

### Build Process

The `disk-explorer.sh` file is **generated**. Never edit it directly. Always edit files in `src/` and then run:

```bash
./build.sh
```

The build script validates syntax with `bash -n` before completing.

### Running the Tool

```bash
# Interactive TUI (default)
./disk-explorer.sh [PATH]

# Quick summary mode
./disk-explorer.sh --summary [PATH]

# Generate a timestamped report
./disk-explorer.sh --report --report-dir ./reports [PATH]

# Remote scan via SSH
./disk-explorer.sh --remote --remote-hosts user@host1,user@host2
```

## Testing

The project uses two testing approaches, both of which source `disk-explorer.sh` to test internal functions directly.

```bash
# Run the custom smoke and unit test suite
bash tests/run_tests.sh

# Run BATS (Bash Automated Testing System) tests (requires bats)
bats tests/is_integer.bats
bats tests/platform_compat.bats
```

## Development Conventions

-   **NUL Delimiters**: Use `\0` delimiters in all scan pipelines (`find -printf ...\0`, `sort -z`, etc.) to safely handle filenames with special characters.
-   **GNU Tool Aliasing**: For macOS compatibility, always use variable aliases (`$FIND_CMD`, `$DU_CMD`, etc.) instead of bare commands. These are resolved to GNU versions (e.g., `gfind`) on macOS.
-   **Bash Version**: Target Bash ≥ 4.4. A shim in `main.sh` handles re-execing on macOS if the system Bash is too old.
-   **Header/Footer Split**: In `src/main.sh`, maintain the separation between inline initialization code (header) and function definitions (footer). The first function definition marks the split point for `build.sh`.
-   **Terminal Safety**: Check `TUI_CAPABLE` before using TUI features. The tool should fall back to a "legacy" mode or summary mode if a full TUI isn't possible (e.g., non-TTY stdin).
-   **No Color**: Respect the `NO_COLOR` environment variable.
