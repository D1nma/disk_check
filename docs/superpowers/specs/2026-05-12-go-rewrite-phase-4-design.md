# Design Spec: Disk Explorer Go Rewrite - Phase 4 (Advanced Bootstrap)

**Date:** 2026-05-12
**Status:** Approved
**Topic:** Implementing auto-download, architecture detection, and caching in the Bash wrapper.

## 1. Overview
Phase 4 makes the Go rewrite the default, transparent experience. The `disk-explorer.sh` wrapper will automatically detect the user's OS and architecture, check for a cached Go binary, and download it from GitHub Releases if missing. If anything fails, it seamlessly falls back to the original Bash implementation.

## 2. Core Components

### 2.1 OS & Architecture Detection
*   Use `uname -s` and `uname -m` to determine the target platform.
*   Map to standard Go values: `linux`, `darwin` (OS) and `amd64`, `arm64` (Arch).

### 2.2 Download & Caching Logic
*   **Source:** GitHub Releases (`https://github.com/D1nma/disk_check/releases/download/<tag>/disk-explorer-<os>-<arch>`).
*   **Cache Location:** `~/.cache/disk-explorer/bin/<version>/disk-explorer`.
*   **Check:** If binary exists and is executable, run it.
*   **Download:** Use `curl` or `wget` (whichever is available) to pull the binary.

### 2.3 Fallback Mechanism
*   If `curl`/`wget` fails, or if the downloaded binary doesn't run, the script will execute its internal `main "$@"` function (the original Bash logic).
*   Add a `--bash` flag to explicitly bypass the Go binary.

## 3. Data Flow
1.  **Script execution:** `disk-explorer.sh` starts.
2.  **Detection:** Identify OS/Arch.
3.  **Cache hit?** If yes, `exec ~/.cache/disk-explorer/bin/...`
4.  **Download:** Try to pull from GitHub.
5.  **Execution:** If success, `exec` the new binary.
6.  **Fallback:** If any step above fails, continue to the original Bash code.

## 4. Success Criteria
*   New users running `curl | bash` get the Go version automatically.
*   Users on unsupported architectures or without network access still get the Bash version.
*   Zero manual intervention required to "install" the Go version.

## 5. Next Steps
1.  Update `src/main.sh` (the header) with detection and download logic.
2.  Refactor `bootstrap.sh` if necessary (it might become redundant or a helper).
3.  Ensure `build.sh` properly updates version tags in the wrapper.
