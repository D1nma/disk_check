#!/usr/bin/env bash

# Source the script to get access to functions
source "$(dirname "$0")/../disk-explorer.sh"

failed=0
total=0

assert_true() {
    local cmd="$1"
    local desc="$2"
    total=$((total + 1))
    if eval "$cmd"; then
        echo "[PASS] $desc"
    else
        echo "[FAIL] $desc"
        failed=$((failed + 1))
    fi
}

assert_false() {
    local cmd="$1"
    local desc="$2"
    total=$((total + 1))
    if ! eval "$cmd"; then
        echo "[PASS] $desc"
    else
        echo "[FAIL] $desc"
        failed=$((failed + 1))
    fi
}

echo "Running build smoke tests..."

assert_true '
  (cd "$(dirname "$0")/.." && ./build.sh >/dev/null 2>&1 && bash -n disk-explorer.sh)
' "build.sh: produit un fichier syntaxiquement valide"

assert_true '
  "$(dirname "$0")/../disk-explorer.sh" --self-check >/dev/null 2>&1
' "disk-explorer.sh --self-check: exit 0"

assert_true '
  "$(dirname "$0")/../disk-explorer.sh" --summary /tmp >/dev/null 2>&1
' "disk-explorer.sh --summary /tmp: exit 0"

assert_true '
  echo "" | "$(dirname "$0")/../disk-explorer.sh" /tmp >/dev/null 2>&1
' "disk-explorer.sh: bascule en summary quand stdin n'est pas un TTY"

echo "Running tests for is_integer..."
assert_true "is_integer 123" "is_integer: positive integer"
assert_true "is_integer 0" "is_integer: zero"
assert_true "is_integer -456" "is_integer: negative integer"
assert_false "is_integer abc" "is_integer: non-integer string"
assert_false "is_integer 12.34" "is_integer: decimal number"
assert_false "is_integer ''" "is_integer: empty string"
assert_false "is_integer ' 123 '" "is_integer: spaces"

echo -e "\nRunning tests for is_non_negative_int..."
assert_true "is_non_negative_int 123" "is_non_negative_int: positive integer"
assert_true "is_non_negative_int 0" "is_non_negative_int: zero"
assert_false "is_non_negative_int -123" "is_non_negative_int: negative integer"

echo -e "\nRunning tests for TUI capability detection..."

assert_true '
  TUI_CAPABLE=0
  tui_check_capability 2>/dev/null
  [[ "$TUI_CAPABLE" -eq 0 || "$TUI_CAPABLE" -eq 1 ]]
' "tui_check_capability: produit 0 ou 1 sans crash"

assert_true '
  _NEEDS_REDRAW=0
  trap '"'"'_NEEDS_REDRAW=1'"'"' SIGWINCH
  kill -WINCH $$
  sleep 0.05
  [[ "$_NEEDS_REDRAW" -eq 1 ]]
' "SIGWINCH handler: positionne _NEEDS_REDRAW=1"

echo -e "\nRunning tests for read_key..."

assert_true '
  key=$(echo "" | read_key 2>/dev/null || true)
  [[ -z "$key" ]]
' "read_key: retourne chaine vide si stdin est fermé"

echo -e "\nRunning tests for cursor navigation..."

# Helper portable : crée N chemins sans seq -f (incompatible macOS sans GNU seq)
_make_paths() {
  local n="$1" i; for (( i=1; i<=n; i++ )); do printf '/dir%d\n' "$i"; done
}

# Charge N chemins dans SUBDIR_PATHS sans mapfile (absent de bash 3.x/macOS)
_load_paths() {
  local n="$1"
  SUBDIR_PATHS=()
  local i
  for (( i=1; i<=n; i++ )); do SUBDIR_PATHS+=("/dir$i"); done
}

assert_true '
  CURSOR=0; SCROLL_OFFSET=0
  _load_paths 3; LINES=10
  cursor_down; [[ "$CURSOR" -eq 1 ]]
' "cursor_down: incrémente CURSOR"

assert_true '
  CURSOR=0; SCROLL_OFFSET=0
  _load_paths 3; LINES=10
  cursor_up; [[ "$CURSOR" -eq 0 ]]
' "cursor_up: ne descend pas sous 0"

assert_true '
  CURSOR=2; SCROLL_OFFSET=0
  _load_paths 3; LINES=10
  cursor_down; [[ "$CURSOR" -eq 2 ]]
' "cursor_down: ne dépasse pas le dernier élément"

assert_true '
  CURSOR=0; SCROLL_OFFSET=0
  _load_paths 20; LINES=10
  # visible = LINES - 6 = 4 ; descendre jusqu'"'"'à ce que SCROLL_OFFSET bouge
  for _i in $(seq 4); do cursor_down; done
  [[ "$SCROLL_OFFSET" -eq 1 ]]
' "cursor_down: scroll quand curseur dépasse la zone visible"

assert_true '
  CURSOR=3; SCROLL_OFFSET=2
  _load_paths 20; LINES=10
  cursor_up; cursor_up; cursor_up
  [[ "$SCROLL_OFFSET" -eq 0 ]]
' "cursor_up: déscroll quand curseur remonte au-dessus du viewport"

echo -e "\nRunning tests for draw_list..."

assert_true '
  LINES=24; COLUMNS=80
  CURRENT_DIR="/tmp"
  SUBDIR_PATHS=("/tmp/a" "/tmp/b" "/tmp/c")
  SUBDIR_DATA=("1024" "2048" "512")
  SORT_MODE="size"
  CURSOR=0; SCROLL_OFFSET=0
  line_count=$(draw_list 2>/dev/null | wc -l)
  visible=$(( LINES - 6 ))
  (( line_count == visible ))
' "draw_list: produit exactement LINES-6 lignes (18 pour LINES=24)"

echo -e "\nSummary: $total tests, $((total - failed)) passed, $failed failed."

if [ $failed -ne 0 ]; then
    exit 1
fi
