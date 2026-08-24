# Design Spec: Fast Scan and Local Report

Beat ncdu 2 (1 thread and `-t`) on scan time, with a local report that follows the current directory. One disk walk. Simple, low resource, any Linux.

This extends `docs/superpowers/specs/2026-05-15-full-scan-refactor-design.md` (full scan + TUI states). It does not replace that model: scan still finishes before browsing.

## 1. Goal

- Scan faster than `ncdu` 2.9+ default (1 thread) **and** `ncdu -t $(nproc)`, warm and cold cache, on a tree the size of `/usr` (~400k files).
- After the scan, the TUI and `--summary`/`--report` describe **the current directory**, not only the scan root.
- Stay light: few goroutines, few fds, no RSS spike from disabling GC, no extra disk walk.

## 2. Constraints

- **Simple:** no `io_uring`, no `statx`, no custom `getdents` parser, no live navigation during scan.
- **Portable Linux:** `open` + `Readdirnames` + `fstatat(fd, name, AT_SYMLINK_NOFOLLOW)`. These syscalls exist on old kernels, Alpine/musl, containers, ext4/xfs/btrfs/nfs. Same code path on macOS via `golang.org/x/sys/unix`. Fallback is not needed on supported platforms.
- **Low resource:** workers = `GOMAXPROCS*2`, clamped to `[2, 16]`. One directory fd per busy worker (~16 fds). Compact nodes. GC stays at the default percent.
- **One walk:** `Scan()` builds the tree. `--summary`/`--report` and TUI top-files use that tree. Delete the extra `ScanTopFiles` disk walk from those modes.
- **`--tree`:** unchanged (bounded `ScanTree`). Out of scope.

## 3. Architecture

```
disk → fd-relative walker (≤16 workers) → compact Node tree
                     ↓
            post-scan aggregate O(N)
                     ↓
     TUI (local)   --summary/--report   bench vs ncdu
```

Scan still reports progress on a channel, then one `Done` event with `Root`. TUI states stay `Scanning` then `Browsing`.

## 4. Node

```go
type Node struct {
	Name      string // basename; on the root node, the absolute scan path
	Size      int64
	ModTime   int64 // unix seconds (mtime)
	Parent    *Node
	Children  []*Node
	FileCount int
	DirCount  int
	IsDir     bool
}
```

No `Path` field, no `sync.Mutex`, no `time.Time`.

- `Path()` walks parents and joins with `os.PathSeparator`. Root `Name` is the absolute scan path, so the result is absolute. If the parent path is `/`, join as `"/" + name` (never `"//etc"`). Call `Path()` only for display and tests — never in the inner stat loop. Exclude checks use the walk's current directory path + name, not `Node.Path()`.
- Display of children uses `Name`, not `filepath.Base(Path())`.
- Root `Name` is the cleaned absolute path passed to `Scan` (e.g. `/home/user`).

## 5. Scanner

### 5.1 Walk

`internal/scanner` keeps `Scan(ctx, root, opts) <-chan ScanProgress`.

Each task is one directory:

1. `os.Open(dir)` (directory fd).
2. `Readdirnames(-1)`.
3. For each name: `unix.Fstatat(fd, name, &st, unix.AT_SYMLINK_NOFOLLOW)`.
4. Skip `.` and `..`. Skip names that match `opts.Excludes` using `dir+sep+name` (string built only for the exclude check, not stored on the node).
5. Build a `Node` with `Name`, `IsDir` from mode, `ModTime` from `st.Mtim.Sec`, size from `st.Blocks * 512` for non-directories.
6. Directories: enqueue a task (or process inline, see workers). Files: `FileCount = 1`.

Do not call `os.ReadDir` + `entry.Info()` (that `lstat`s the full path). Do not call `entry.IsDir()` then `Info()` (double stat on `DT_UNKNOWN`).

Open/stat errors (`EACCES`, `ENOENT`, `ELOOP`, …): skip that entry, continue. A fully unreadable directory becomes a node with no children and only its own directory size if the dir inode was statable.

### 5.2 Workers

- `n = GOMAXPROCS(0) * 2`; if `n > 16` then `16`; if `n < 2` then `2`.
- Buffered task channel. If send would block, the **same worker** appends the directory to a local leftover slice and processes it after the current listing. No relay goroutines.
- `taskWg` still tracks outstanding directories so the pool can close when the tree is done.

### 5.3 Sizes and hard links

- File and directory sizes use `st.Blocks * 512` (same as `du` / ncdu).
- A directory's `Size` after aggregation includes its own inode blocks plus all descendants.
- Hard links: if `st.Nlink > 1` and it is not a directory, key `(dev, ino)` in a mutex-protected map. First sight counts size; later sights get `Size = 0` and are still listed. `nlink == 1` does not touch the map.

### 5.4 Kernfs

Skip virtual filesystems by `f_type` from `unix.Fstatfs` on the directory fd, before listing:

- `proc`, `sysfs`, `devtmpfs`, `devfs`, `securityfs`, `cgroup` / `cgroup2`, `debugfs`, `tracefs`, `pstore`, `overlay` is **not** skipped (real merged trees).
- Apply to subdirectories, not to the scan root: scanning `/proc` on purpose still works; scanning `/` does not walk `/proc`.
- Kernfs skip is Linux-only (`GOOS=linux`). On macOS, only `SameDevice` and `Excludes` apply.
- Same-device (`opts.SameDevice`): skip entries whose `st.Dev` differs from the root device (already existing `--mode partition`).

### 5.5 Progress

Atomic counters: files, dirs, size (sum of file blocks as they are seen; final sizes come from aggregation). A single goroutine ticks every 100ms and sends a non-blocking `ScanProgress` (`Current` = last directory path opened). Final send is blocking and sets `Done` + `Root`.

Do not take a mutex per file. Do not `% 100` on the file counter.

### 5.6 Aggregation

After workers finish, a single-threaded post-order walk:

- At walk time, a directory node's `Size` is set to its own inode `st.Blocks * 512` (possibly 0 if the dir stat failed).
- Aggregation: `own := n.Size`; then `n.Size = own + sum(child.Size)`.
- `FileCount` / `DirCount` as today (files in subtree, directories in subtree).

Then return the tree. Do not sort the whole tree here. The TUI sorts the current directory's `Children` on enter / sort-key change (already the case). `--summary` sorts root children by size for the top-dir list.

### 5.7 Top files (in memory)

```go
func TopFiles(n *Node, limit int) []*Node
```

Walk the subtree in memory, keep a min-heap of size `limit` of non-directory nodes with `Size > 0`. Used by `--summary`/`--report`. Not stored on each directory. TUI list stays **direct children only**.

Replace `ScanTopFiles` (disk `WalkDir`) with `TopFiles`. Summary/report and tests call `TopFiles` on an already-scanned tree. No `filepath.WalkDir` remains on the summary path.

## 6. TUI (where you are)

Scanning view: keep files / dirs / size / current path (data now from the 100ms tick).

Browsing view:

| Line | Content |
|------|---------|
| Header | `DISK EXPLORER  <current path>  ALL\|PARTITION · sort` |
| Disk | existing used/total bar; omit if `Statfs` failed |
| Here | `<size>  <pct of parent> of parent  <pct of disk> of disk  <files> files  <dirs> dirs` |
| List | `>  12.4 GiB  41.2%  ████░░░░░░  name/` |

Rules:

- Header path is the current node's `Path()`, already updated on enter/back.
- Parent percent: `current.Size * 100 / parent.Size` when parent exists and `parent.Size > 0`; omit the parent clause on the scan root.
- Disk percent: `current.Size * 100 / diskTotal` when `diskTotal > 0`; omit otherwise.
- List percent: `child.Size * 100 / current.Size` when `current.Size > 0`, else 0. Bar still relative to the largest **visible sibling** (readable at a glance).
- No second pane. No recursive top-files mixed into the list. No new keys in this spec (`r` to dump a report is out of scope).

Sort by name uses `Name`. Sort by date uses `ModTime` (unix seconds).

## 7. `--summary` / `--report`

One `Scan()`, drain until `Done`, then format from `Root` + `TopFiles(root, topN)` + `Statfs`.

Report contents:

```
RAPPORT DISQUE - <timestamp>
Dossier : <abs path>
Taille  : <root.Size>  (<files> fichiers, <dirs> dossiers)
Disque  : <used> utilisé / <total> total (<pct>%)
Libre   : <avail>
Part de ce dossier dans le disque : <root.Size/total %>

TOP SOUS-DOSSIERS :
  <size>  <name>/

TOP FICHIERS :
  <size>  <path relative to scan root>
```

`--report` is this text written atomically (tmp + rename), as today.

## 8. Tests

Keep existing scanner tests, updated for `Path()` / `Name` / unix `ModTime`.

Add:

- Size aggregation still holds (including directory inode blocks: dir size ≥ sum of children).
- `Path()` reconstruction for nested nodes.
- `TopFiles` returns the largest files in a fixture tree without a second walk.
- Hard link: two names, one inode, size counted once.
- Exclude still skips a named subtree.
- Context cancel still stops promptly.
- TUI: sort by name uses `Name`; Here-line parent percent omitted at root (unit test on a small model if cheap; otherwise View string contains current path after navigation — existing navigation tests already cover path updates).

`go test ./...` must pass. No new CGO.

## 9. Benchmarks (merge criterion)

Script: `scripts/bench-ncdu.sh`.

- Target directory: argument, default `/usr`.
- Warm: run each command once to fill cache, then time.
- Cold: if writable, `sync` + `echo 3 > /proc/sys/vm/drop_caches`; otherwise print that cold was skipped.
- Commands:
  1. `ncdu -0 -o /dev/null TARGET`
  2. `ncdu -0 -o /dev/null -t $(nproc) TARGET`
  3. `disk-explorer --summary TARGET >/dev/null`
- Print wall time for each. Merge requires (3) faster than (1) **and** (2) on warm cache. If drop_caches works, the same must hold on cold cache. If drop_caches is unavailable, print that cold was skipped and do not block on it.

Do not add a hidden `--bench` flag. `--summary` after this spec is a single walk plus a cheap in-memory heap.

## 10. Out of scope

- Live browse during scan
- Bash fallback performance vs ncdu
- Rewriting `ScanTree` / `--tree`
- `io_uring`, `statx`, Windows
- Disabling GC during scan
- Per-directory stored top-K heaps
- New TUI keys, delete, or refresh-scan

## 11. Files

| File | Change |
|------|--------|
| `internal/scanner/types.go` | Compact `Node`, `Path()` |
| `internal/scanner/scanner.go` | Worker policy, progress tick, aggregate, kernfs, hardlinks |
| `internal/scanner/walk_unix.go` | Open + Readdirnames + Fstatat |
| `internal/scanner/scanner_test.go` | Path, TopFiles, hardlink, aggregation |
| `internal/scanner/topfiles.go` | Replace disk `WalkDir` with in-memory `TopFiles` |
| `internal/tui/model.go` | Here line, list %, `Name` / `ModTime` |
| `internal/tui/model_test.go` | Name-based sort, navigation still valid |
| `internal/display/summary.go` | Local report fields, `TopFiles` |
| `cmd/disk-explorer/main.go` | Drop `ScanTopFiles` disk call |
| `scripts/bench-ncdu.sh` | Warm/cold vs ncdu |
| `go.mod` | `golang.org/x/sys` direct |

## 12. Success

- `go test ./...`, `go vet ./...` pass.
- `--summary` does not walk the disk twice (no `filepath.WalkDir` after `Scan`).
- TUI browsing shows current path, here-line, and per-child % of the current directory.
- `scripts/bench-ncdu.sh /usr` (warm): disk-explorer faster than ncdu 1-thread and ncdu `-t`.
