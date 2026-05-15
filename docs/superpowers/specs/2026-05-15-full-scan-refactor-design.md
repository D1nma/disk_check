# Design Spec: Full Scan Refactor for Disk Explorer

This document describes the architectural changes required to transition `disk-explorer` from a "Lazy Scanning" model to a "Full Scan" model, matching the performance and behavior of `ncdu`.

## 1. Goal
- Implement a complete disk scan at startup.
- Provide a dedicated progress screen during scanning.
- Enable instant navigation after scanning by keeping the full tree in memory.
- Optimize performance using parallel scanning.

## 2. Architecture

### 2.1 Data Structure (`internal/scanner/types.go`)
We will use a `Node` structure that represents a file or directory.

```go
type Node struct {
	Name     string
	Path     string
	Size     int64
	IsDir    bool
	Parent   *Node
	Children []*Node
	FileCount int
	DirCount  int
}
```
- **Bidirectional**: Each node knows its parent and children for O(1) navigation.
- **Pre-computed**: Sizes and counts are aggregated during the scan.

### 2.2 Parallel Scanner (`internal/scanner/scanner.go`)
- **Parallelism**: Use a worker pool (goroutines) for scanning branches.
- **Communication**: A progress channel will stream updates (files found, current directory) to the TUI.
- **Aggregation**: A final pass or a thread-safe aggregation mechanism will ensure all parent sizes are correct once the scan completes.

### 2.3 TUI Model (`internal/tui/model.go`)
The TUI will have two distinct phases:

1.  **Scanning Phase**:
    - Displays a progress screen.
    - Stats: total files, total folders, total size, current path.
    - Listens to the progress channel.
2.  **Browsing Phase**:
    - Switches to the familiar list view.
    - All data is already in memory.
    - Sorting happens only once per directory entry or when requested.

## 3. Data Flow
1.  **Start**: `main.go` starts the TUI.
2.  **TUI Init**: Triggers the `StartScan` command.
3.  **Scanner**: Walks the filesystem. Sends periodic updates to TUI.
4.  **TUI Update**: Receives `ProgressMsg`, updates counters.
5.  **Scanner Done**: Sends `ScanCompleteMsg` containing the root `Node`.
6.  **TUI Navigation**: User moves through the tree. TUI simply renders `node.Children`.

## 4. Performance Optimizations
- **UI Throttling**: The scanning screen will update at most every 100ms to save CPU.
- **Sorting**: Children will be sorted by size once when the scan finishes, and re-sorted only if the user changes the sort key.
- **Memory**: For very large disks, we store only essential fields.

## 5. Verification Plan
- **Unit Tests**:
    - Verify size aggregation for nested directories.
    - Test the parallel walker for race conditions.
- **Manual Verification**:
    - Scan a directory with 100k+ files.
    - Verify that memory usage remains reasonable.
    - Check that navigation is indeed "instant".
