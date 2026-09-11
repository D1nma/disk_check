# Sentinel Security Memory — disk-explorer

## 2026-09-07 - Safe Argument Escaping for Remote SSH Scanning
**Vulnerability:** Command injection when passing remote host arguments or custom paths to `ssh`.
**Learning:** Shell evaluation of user-supplied remote host parameters allows arbitrary command execution.
**Prevention:** Pass arguments to `exec.Command` as discrete slice elements without shell interpolation. Enforce alphanumeric hostname validation and flag sanitization.

## 2026-09-07 - Circular Symlink Detection
**Vulnerability:** Infinite loops and stack overflows when scanning directories containing recursive symlinks.
**Learning:** Naive recursive traversal of symlinks can trap the scanner in an infinite directory loop.
**Prevention:** Use inode/device tuple tracking for followed symlinks, or default to not following directory symlinks.
