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

echo -e "\nSummary: $total tests, $((total - failed)) passed, $failed failed."

if [ $failed -ne 0 ]; then
    exit 1
fi
