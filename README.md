# disk-explorer

A single-file Bash TUI for exploring disk usage — no installation required.

```bash
bash <(curl -fsSL https://raw.githubusercontent.com/D1nma/disk_check/main/disk-explorer.sh)
```

```
DISK EXPLORER  /home/user  MÊME PARTITION · size
████████░░░░░░░░░░░ 42%   58 Go / 230 Go
────────────────────────────────────────────────────────────────────────────
   1)    21,3 Go  ██████████  .cargo/
   2)    18,7 Go  ████████░░  node_modules/
   3)     6,1 Go  ███░░░░░░░  .local/
   4)   512,0 Mo  ██░░░░░░░░  archive.tar.gz
   5)   128,0 Mo  █░░░░░░░░░  Downloads/
────────────────────────────────────────────────────────────────────────────
  [↑↓] naviguer  [Entrée] ouvrir  [d] supprimer  [1-5] accès direct  [0] retour
  [s] tri  [a] taille  [f] fichiers  [r] rapport  [h] aide  [q] quitter
```

---

## Features

- **Full-screen TUI** — arrow key navigation, proportional bars per entry, disk usage bar
- **Mixed files + directories** in the same sorted list (like ncdu)
- **Delete** selected item with inline confirmation (`d`)
- **Summary mode** — non-interactive report for scripts and CI
- **Report mode** — timestamped text report written to disk
- **Tree mode** — size tree with `% of parent`
- **Sort** by size or last modified date
- **Exclusions** — configurable per-run or persistent via config menu
- **No install** — single self-contained script, works via `curl | bash`

---

## Quick start

### Interactive TUI

| Shell | Command |
|---|---|
| bash / zsh | `bash <(curl -fsSL https://raw.githubusercontent.com/D1nma/disk_check/main/disk-explorer.sh)` |
| fish | `bash (curl -fsSL https://raw.githubusercontent.com/D1nma/disk_check/main/disk-explorer.sh \| psub)` |
| any (universal) | `curl -fsSL https://raw.githubusercontent.com/D1nma/disk_check/main/disk-explorer.sh -o /tmp/de.sh && bash /tmp/de.sh` |

### Summary only (all shells, pipe-friendly)

```bash
curl -fsSL https://raw.githubusercontent.com/D1nma/disk_check/main/disk-explorer.sh | bash
```

> **Why two commands?**  
> `curl URL | bash` feeds the script via stdin, which prevents the TUI from reading keystrokes.  
> Process substitution (`<(...)` or `psub`) downloads the script to a file descriptor first, leaving stdin connected to the terminal.

> **Corporate proxies:** if you get `curl: (35) OpenSSL SSL_connect: Connection reset`, your proxy is blocking DuckDNS or custom domains. The `raw.githubusercontent.com` URL above works on virtually all corporate networks.

---

## Usage

```bash
# Current directory (interactive TUI)
./disk-explorer.sh

# Specific path
./disk-explorer.sh /var

# Quick summary (non-interactive)
./disk-explorer.sh --summary /home

# Timestamped report
./disk-explorer.sh --report --report-dir /tmp/reports /srv

# Tree view
./disk-explorer.sh --tree --tree-depth 3 /home

# Custom scan
./disk-explorer.sh --mode global --sort mtime --top-count 20 --top-files 30 /data

# Diagnose dependencies
./disk-explorer.sh --self-check
```

### TUI key bindings

| Key | Action |
|---|---|
| `↑` `↓` | Navigate list |
| `Enter` | Open directory |
| `d` | Delete selected item (confirmation required) |
| `0` | Go to parent directory |
| `1`–`9` | Jump directly to entry N |
| `s` | Toggle sort: size / mtime |
| `a` | Toggle file size: real blocks / apparent |
| `p` | Toggle scan mode: partition / global |
| `f` | Show largest files (recursive) |
| `r` | Generate report |
| `e` | Show/edit exclusions |
| `c` | Config menu |
| `h` / `?` | Help |
| `q` | Quit |

---

## Options

```text
--path DIR               Directory to analyse (default: current)
--mode partition|global  Stay on one filesystem or traverse all mounts
--sort size|mtime        Sort by size (default) or last modified
--file-size real|apparent
--top-count N            Max directories shown (default: 15)
--top-files N            Max files shown in file view
--max-depth N            Max recursion depth for file scan
--exclude DIR            Exclude a directory (repeatable)
--no-default-excludes    Disable built-in exclusions (proc, sys, dev…)
--summary                Non-interactive summary then exit
--tree                   Tree view with sizes and % of parent
--tree-depth N
--report                 Write timestamped report to file
--report-dir DIR
--no-color
--no-spinner
--self-check             Diagnose runtime dependencies
--help
```

---

## Requirements

GNU/Linux with Bash ≥ 4.4. Standard GNU coreutils (`find`, `du`, `sort`, `head`, `df`, `date`). `numfmt` recommended (graceful fallback if absent).

macOS with Homebrew GNU coreutils is partially supported.

---

## Repository structure

```
disk-explorer.sh      # single distributable file (generated by build.sh)
build.sh              # assembles src/ → disk-explorer.sh
src/
  main.sh             # entry point, arg parsing, globals
  utils.sh            # pure helpers (human_size, sanitize, platform…)
  scan.sh             # du/find scan functions, temp file management
  display.sh          # non-interactive modes (summary, report, tree)
  tui.sh              # full-screen TUI (draw, input, navigation)
tests/
  run_tests.sh        # custom test suite
  *.bats              # bats test suite
docs/
  superpowers/        # design specs and implementation plans
```

Build after editing any `src/` file:

```bash
./build.sh
```

---

## License

MIT
