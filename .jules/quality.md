# Quality & Reliability Memory — disk-explorer

## 2026-09-07 - Build Pipeline Synchronization
**Learning:** Modifying shell logic in `src/` without running `build.sh` results in the embedded Go asset `internal/assets/disk-explorer.sh` diverging from the source code.
**Action:** Always run `./build.sh` and commit both `src/` and the regenerated wrapper.
