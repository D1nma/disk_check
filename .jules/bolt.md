# Bolt Performance Memory — disk-explorer

## 2026-09-07 - High-Throughput Directory Walks with filepath.WalkDir
**Learning:** In Go, `filepath.Walk` calls `os.Lstat` on every file, whereas `filepath.WalkDir` provides `fs.DirEntry` which already has file type information on modern Linux kernels, cutting syscalls in half.
**Action:** Always use `filepath.WalkDir` in Go 1.16+ to minimize system calls during directory indexing.

## 2026-09-07 - Bounded Worker Pools for I/O Scans
**Learning:** Spawning unbounded goroutines on NVMe or fast storage can overwhelm OS file descriptors (`EMFILE: too many open files`).
**Action:** Cap concurrent folder scanner workers (e.g. `runtime.NumCPU() * 4` or a semaphore of 32-64 workers).
