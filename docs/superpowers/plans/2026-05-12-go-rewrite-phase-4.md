# Disk Explorer Go Rewrite - Phase 4 Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Implement auto-download, architecture detection, and caching in the Bash wrapper with seamless fallback.

**Architecture:** Update the shell header in `src/main.sh` to include a bootstrap shim. This shim will detect OS/Arch, check for a cached Go binary in `~/.cache/disk-explorer`, download it from GitHub if missing, and `exec` it. If any step fails, it falls through to the original Bash `main`.

**Tech Stack:** Bash, curl/wget, GitHub Releases.

---

### Task 1: OS & Architecture Detection

**Files:**
- Modify: `src/utils.sh`

- [ ] **Step 1: Add detection helper functions**
Add `get_os` and `get_arch` helpers to `src/utils.sh` to return Go-compatible strings.

```bash
# src/utils.sh

get_os() {
    local os
    os=$(uname -s | tr '[:upper:]' '[:lower:]')
    case "$os" in
        linux*) echo "linux" ;;
        darwin*) echo "darwin" ;;
        *) echo "$os" ;;
    esac
}

get_arch() {
    local arch
    arch=$(uname -m)
    case "$arch" in
        x86_64|amd64) echo "amd64" ;;
        arm64|aarch64) echo "arm64" ;;
        *) echo "$arch" ;;
    esac
}
```

- [ ] **Step 2: Verify detection**
Run a temporary script sourcing `src/utils.sh` to confirm it prints correct values for your current machine.

- [ ] **Step 3: Commit**
```bash
git add src/utils.sh
git commit -m "feat(bootstrap): add OS and Arch detection helpers"
```

---

### Task 2: Binary Download & Caching Logic

**Files:**
- Modify: `src/main.sh`

- [ ] **Step 1: Define Bootstrap Constants**
Add version and URL constants to the top of `src/main.sh` (header section).

```bash
# src/main.sh (header)
VERSION="v0.2.0" # Placeholder, should be updated by build process
REPO_URL="https://github.com/D1nma/disk_check"
CACHE_DIR="${HOME}/.cache/disk-explorer/bin/${VERSION}"
```

- [ ] **Step 2: Implement download_binary function**
Add a function to handle `curl` or `wget` downloads.

```bash
# src/main.sh (footer)
download_binary() {
    local os=$1 arch=$2 target=$3
    local url="${REPO_URL}/releases/download/${VERSION}/disk-explorer-${os}-${arch}"
    
    mkdir -p "$(dirname "$target")"
    if command -v curl >/dev/null 2>&1; then
        curl -SLf "$url" -o "$target"
    elif command -v wget >/dev/null 2>&1; then
        wget -q "$url" -O "$target"
    else
        return 1
    fi
    chmod +x "$target"
}
```

- [ ] **Step 3: Commit**
```bash
git add src/main.sh
git commit -m "feat(bootstrap): add binary download logic"
```

---

### Task 3: Execution Shim & Fallback

**Files:**
- Modify: `src/main.sh`

- [ ] **Step 1: Implement try_go_binary shim**
Add the logic to check cache, download if needed, and `exec`.

```bash
# src/main.sh (header - inside the shim section)

try_go_binary() {
    # Bypass if --bash flag is present
    for arg in "$@"; do [[ "$arg" == "--bash" ]] && return; done
    
    local os arch binary
    os=$(get_os)
    arch=$(get_arch)
    binary="${CACHE_DIR}/disk-explorer"

    if [[ ! -x "$binary" ]]; then
        # Try to download
        download_binary "$os" "$arch" "$binary" >/dev/null 2>&1 || return
    fi

    if [[ -x "$binary" ]]; then
        exec "$binary" "$@"
    fi
}
```

- [ ] **Step 2: Call shim before main**
Ensure `try_go_binary "$@"` is called before the Bash `main`.

- [ ] **Step 3: Verify fallback**
Test by pointing `REPO_URL` to a fake URL and ensuring the script still runs using the Bash implementation.

- [ ] **Step 4: Commit**
```bash
git add src/main.sh
git commit -m "feat(bootstrap): implement execution shim and fallback"
```

---

### Task 4: Build Script Integration

**Files:**
- Modify: `build.sh`

- [ ] **Step 1: Update version in generated script**
Modify `build.sh` to inject the current git tag or a version string into `disk-explorer.sh`.

```bash
# build.sh
VERSION=$(git describe --tags --always 2>/dev/null || echo "dev")
# Use sed to replace VERSION="..." in the generated file
```

- [ ] **Step 2: Commit**
```bash
git add build.sh
git commit -m "chore(build): inject version into bootstrap"
```
