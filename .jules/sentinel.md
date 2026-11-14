# Sentinel Security Memory — disk-explorer

## 2026-09-07 - Safe Argument Escaping for Remote SSH Scanning
**Vulnerability:** Command injection when passing remote host arguments or custom paths to `ssh`.
**Learning:** Shell evaluation of user-supplied remote host parameters allows arbitrary command execution.
**Prevention:** Bash `--remote`: validate hosts and quote every argument (`printf %q`). Go `--ssh` (`internal/remote`): `ParseTarget` validates hosts (no leading `-`), `checkRemoteArg` rejects quotes, `$`, backticks, backslashes and control characters; the remote script is a single-quoted sh script with no `'` or `\` inside.

## 2026-11-14 - Terminal escape injection through file names
**Vulnerability:** Any local user can create a file named with ESC/OSC sequences in /tmp; printed raw by a root scan, it sets the terminal title, writes the clipboard (OSC 52) or breaks the layout.
**Prevention:** Every name printed goes through `textsafe.Clean`. The cleanup plan skips paths containing control characters (a quoted newline would show a fake command to the reviewer).

## 2026-09-07 - Circular Symlink Detection
**Vulnerability:** Infinite loops when following recursive symlinks.
**Prevention:** The scanner never follows symlinks (`AT_SYMLINK_NOFOLLOW`).

## 2026-11-14 - Remote and imported trees
**Vulnerability:** An imported or remote tree describes another machine; deleting "the same path" locally would destroy local data.
**Prevention:** `tui.Model.Imported` forces read-only and disables rescan, statfs and local hooks.
