# Design: Project Cleanup and Fish Shell Compatibility

**Topic:** Project Cleanup and Fish Shell Compatibility
**Date:** 2026-05-12
**Author:** Gemini CLI

## 1. Overview
This design addresses two main requests:
1.  **Aggressive Cleanup:** Streamlining the repository by removing redundant or historical files.
2.  **Fish Shell Compatibility:** Fixing the "Universal Command" to work in the Fish shell.

## 2. Proposed Changes

### 2.1 Workspace Cleanup
The following files and directories will be removed:
- `TODO.md`: This file has all tasks checked off and is no longer needed.
- `bootstrap.sh`: A legacy bootstrap attempt. Its logic has been integrated into the main `disk-explorer.sh` distribution script.
- `install.sh`: A secondary installation script that is redundant given the universal one-liner approach.
- `docs/superpowers/`: This directory contains historical planning and design documents that have served their purpose and are now just cluttering the repository.

### 2.2 Fish Shell Compatibility
The current recommended command in `README.md`:
```bash
bash <(curl -fsSL https://raw.githubusercontent.com/D1nma/disk_check/main/disk-explorer.sh)
```
fails in Fish because Fish does not support `<(...)` process substitution.

**Solution:**
Replace it with a more portable command:
```bash
curl -fsSL https://raw.githubusercontent.com/D1nma/disk_check/main/disk-explorer.sh | bash
```
This command works across Bash, Zsh, and Fish.

## 3. Impact Assessment
- **`build.sh`**: Verified that `build.sh` only depends on the `src/` directory and `main.sh`. It does not use `bootstrap.sh` or `install.sh`.
- **Distribution**: `disk-explorer.sh` remains the primary entry point and is unaffected by these deletions.
- **Documentation**: `README.md` will be updated to reflect the new universal command.

## 4. Validation Plan
1.  **Deletion**: Execute `rm` on the specified files and directories.
2.  **README Update**: Apply `replace` to update the command in `README.md`.
3.  **Build Verification**: Run `./build.sh` to ensure the build process remains intact.
4.  **Final Review**: Ensure no obviously necessary files were removed.
