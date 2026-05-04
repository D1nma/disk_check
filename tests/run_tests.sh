#!/usr/bin/env bash

# Source the script to get access to functions
source ./disk-explorer.sh

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

echo -e "\nSummary: $total tests, $((total - failed)) passed, $failed failed."

if [ $failed -ne 0 ]; then
    exit 1
fi
