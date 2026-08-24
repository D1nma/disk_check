#!/usr/bin/env bash
set -euo pipefail

TARGET="${1:-/usr}"
BIN="${BIN:-./disk-explorer}"
if [[ ! -x "$BIN" ]]; then
  go build -o disk-explorer ./cmd/disk-explorer
  BIN=./disk-explorer
fi

nproc_n="$(nproc 2>/dev/null || echo 4)"

time_cmd() {
  local label="$1"
  shift
  local start end
  start="$(date +%s.%N)"
  "$@" >/dev/null
  end="$(date +%s.%N)"
  awk -v s="$start" -v e="$end" -v l="$label" 'BEGIN{printf "%-28s %.3fs\n", l, e-s}'
}

run_suite() {
  local tag="$1"
  echo "=== $tag  target=$TARGET ==="
  time_cmd "ncdu 1-thread" ncdu -0 -o /dev/null "$TARGET"
  time_cmd "ncdu -t ${nproc_n}" ncdu -0 -o /dev/null -t "$nproc_n" "$TARGET"
  time_cmd "disk-explorer --summary" "$BIN" --summary "$TARGET"
}

echo "warmup..."
ncdu -0 -o /dev/null "$TARGET" >/dev/null || true
"$BIN" --summary "$TARGET" >/dev/null || true

run_suite "warm"

if [[ -w /proc/sys/vm/drop_caches ]]; then
  sync
  echo 3 > /proc/sys/vm/drop_caches
  run_suite "cold"
else
  echo "cold: skipped (cannot write /proc/sys/vm/drop_caches)"
fi
