# Project Cleanup and Fish Shell Compatibility Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Clean up historical/redundant files and update the README with a Fish-compatible universal command.

**Architecture:** Surgical deletion of identified files and a text replacement in `README.md`.

**Tech Stack:** Shell commands, Git.

---

### Task 1: Repository Cleanup

**Files:**
- Delete: `TODO.md`
- Delete: `bootstrap.sh`
- Delete: `install.sh`
- Delete: `docs/superpowers/plans/2026-05-08-macos-linux-compat.md`
- Delete: `docs/superpowers/plans/2026-05-09-fullscreen-tui.md`
- Delete: `docs/superpowers/plans/2026-05-11-go-rewrite.md`
- Delete: `docs/superpowers/plans/2026-05-12-go-rewrite-phase-2.md`
- Delete: `docs/superpowers/plans/2026-05-12-go-rewrite-phase-3.md`
- Delete: `docs/superpowers/plans/2026-05-12-go-rewrite-phase-4.md`
- Delete: `docs/superpowers/plans/2026-05-13-dynamic-sorting.md`
- Delete: `docs/superpowers/specs/2026-05-08-macos-linux-compat-design.md`
- Delete: `docs/superpowers/specs/2026-05-09-fullscreen-tui-design.md`
- Delete: `docs/superpowers/specs/2026-05-11-go-rewrite-design.md`
- Delete: `docs/superpowers/specs/2026-05-12-go-rewrite-phase-2-design.md`
- Delete: `docs/superpowers/specs/2026-05-12-go-rewrite-phase-3-design.md`
- Delete: `docs/superpowers/specs/2026-05-12-go-rewrite-phase-4-design.md`

- [ ] **Step 1: Delete redundant top-level files**

Run: `rm TODO.md bootstrap.sh install.sh`

- [ ] **Step 2: Delete historical planning documents**

Run: `rm docs/superpowers/plans/*.md`
(Ensure `2026-05-12-cleanup-and-fish-fix-design.md` and this plan are NOT deleted if they were already there, but we are about to save this plan now).

- [ ] **Step 3: Delete historical design documents**

Run: `find docs/superpowers/specs -name "*-design.md" ! -name "2026-05-12-cleanup-and-fish-fix-design.md" -delete`

- [ ] **Step 4: Commit cleanup**

```bash
git add .
git commit -m "chore: aggressive cleanup of redundant and historical files"
```

---

### Task 2: README Universal Command Update

**Files:**
- Modify: `README.md`

- [ ] **Step 1: Replace Bash process substitution with pipe**

**File:** `README.md`
**Old:** `bash <(curl -fsSL https://raw.githubusercontent.com/D1nma/disk_check/main/disk-explorer.sh)`
**New:** `curl -fsSL https://raw.githubusercontent.com/D1nma/disk_check/main/disk-explorer.sh | bash`

**Action:** Apply this change to both instances in `README.md`.

- [ ] **Step 2: Verify README content**

Run: `grep "curl -fsSL" README.md`
Expected: Only the new piped version exists.

- [ ] **Step 3: Commit README update**

```bash
git add README.md
git commit -m "docs: update universal command to be Fish-compatible"
```

---

### Task 3: Final Verification

- [ ] **Step 1: Run build script**

Run: `./build.sh`
Expected: `Build OK → /var/www/disk_check/disk_check/disk-explorer.sh (and synchronized to internal/assets)`

- [ ] **Step 2: Verify workspace state**

Run: `ls -R docs/superpowers/`
Expected: Only the current design and plan files remain.
