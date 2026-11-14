# Bolt Performance Memory — disk-explorer

## 2026-11-14 - The scanner already beats filepath.WalkDir
**Learning:** On Linux the scanner parses `getdents64` itself and stats entries with a direct `newfstatat` relative to the directory fd (zero-copy names interned in per-worker slabs). `filepath.Walk`, `WalkDir` and `os.ReadDir` are all slower here.
**Action:** Never "optimise" the walk back to `filepath.WalkDir`. Benchmark any scanner change with `go test ./internal/scanner -run '^$' -bench BenchmarkScan -benchmem` and `scripts/bench-ncdu.sh`.

## 2026-09-07 - Bounded Worker Pools for I/O Scans
**Learning:** Spawning unbounded goroutines on NVMe or fast storage can overwhelm OS file descriptors (`EMFILE: too many open files`).
**Action:** Keep the pool bounded (`GOMAXPROCS*4`, capped at 32; `--scan-workers` 1-64).

## 2026-11-14 - Memory is the limit, not speed
**Learning:** A full scan costs about 150 bytes per entry; 50 million files need ~7 GB.
**Action:** Do not grow `scanner.Node`. Put new per-node data in free padding (`Flags`) or in sparse maps on `ScanProgress` (`Pinned`, `Recent`).
