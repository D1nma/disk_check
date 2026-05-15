# Fix st.Dev Type Mismatch Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Explicitly cast `st.Dev` to `uint64` in `internal/scanner/*.go` to fix type mismatch errors on architectures where `st.Dev` is not `uint64`.

**Architecture:** Surgical update of assignments and comparisons involving `st.Dev` and `rootDev` (which is `uint64`).

**Tech Stack:** Go

---

### Task 1: Fix `internal/scanner/scanner.go`

**Files:**
- Modify: `internal/scanner/scanner.go`

- [ ] **Step 1: Update `st.Dev != rootDev` comparisons**
Update all instances of `st.Dev != rootDev` to `uint64(st.Dev) != rootDev`.

- [ ] **Step 2: Verify existing cast (if any)**
Check if `rootDev = uint64(st.Dev)` is already correct (it appears to be from research).

### Task 2: Fix `internal/scanner/topfiles.go`

**Files:**
- Modify: `internal/scanner/topfiles.go`

- [ ] **Step 1: Update `rootDev = st.Dev` assignment**
Change `rootDev = st.Dev` to `rootDev = uint64(st.Dev)`.

- [ ] **Step 2: Update `st.Dev != rootDev` comparison**
Change `st.Dev != rootDev` to `uint64(st.Dev) != rootDev`.

### Task 3: Fix `internal/scanner/tree.go`

**Files:**
- Modify: `internal/scanner/tree.go`

- [ ] **Step 1: Update `rootDev = st.Dev` assignment**
Change `rootDev = st.Dev` to `rootDev = uint64(st.Dev)`.

- [ ] **Step 2: Update `st.Dev != rootDev` comparison**
Change `st.Dev != rootDev` to `uint64(st.Dev) != rootDev`.

### Task 4: Validation

- [ ] **Step 1: Cross-compile for linux/amd64**
Run `GOOS=linux GOARCH=amd64 go build ./...` in the root directory.
Expected: PASS

- [ ] **Step 2: Run tests**
Run `go test ./internal/scanner/...`
Expected: PASS
