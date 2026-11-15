# Quality & Reliability Memory — disk-explorer

## 2026-11-14 - Build Pipeline Synchronization
**Learning:** `disk-explorer.sh` is generated from `src/*.sh` by `build.sh` (there is no embedded Go asset). CI compares the committed file with a fresh build (VERSION/BINARY_TAG lines excepted).
**Action:** Always run `./build.sh` and commit both `src/` and the regenerated script.

## 2026-11-14 - Errors must surface
**Learning:** Silently skipped directories made reports look complete while gigabytes were missing.
**Action:** Every read error ends up in `ScanProgress.Errors` / node flags and changes the exit code (2). Do not add `continue` on errors without counting them.

## 2026-11-14 - bats and the script's EXIT trap
**Learning:** Sourcing `disk-explorer.sh` installs `trap cleanup EXIT`, which replaced bats' trap: failing tests vanished ("Executed 20 instead of expected 23").
**Action:** bats `setup()` saves and restores bats' EXIT trap around `source`.
