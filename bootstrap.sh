#!/usr/bin/env bash
set -euo pipefail

OS=$(uname -s | tr '[:upper:]' '[:lower:]')
ARCH=$(uname -m)

case "$ARCH" in
    x86_64) ARCH="amd64" ;;
    aarch64|arm64) ARCH="arm64" ;;
esac

BINARY_NAME="disk-explorer-${OS}-${ARCH}"
# PLACEHOLDER: URL="https://github.com/D1nma/disk_check/releases/latest/download/${BINARY_NAME}"

if [[ -f "./disk-explorer" ]]; then
    exec "./disk-explorer" "$@"
elif [[ -f "./cmd/disk-explorer/disk-explorer" ]]; then
    exec "./cmd/disk-explorer/disk-explorer" "$@"
elif [[ -f "./${BINARY_NAME}" ]]; then
    exec "./${BINARY_NAME}" "$@"
fi
else
    echo "Binary ${BINARY_NAME} not found."
    echo "To build it: go build -o ${BINARY_NAME} ./cmd/disk-explorer/main.go"
    exit 1
fi
