# Repository Guidelines

## Project Structure & Module Organization

This repository builds `disk-explorer`, a Go TUI with a Bash bootstrap and fallback. The Go entry point is `cmd/disk-explorer/main.go`. Internal packages live under `internal/`: `scanner/` handles high-performance parallel recursive scanning, `tui/` contains Bubble Tea UI logic with instant O(1) navigation, `remote/` handles native SSH scanning, and `assets/` embeds the Bash fallback.
 The modular Bash implementation is in `src/`; `build.sh` concatenates it into `disk-explorer.sh` and syncs the generated copy to `internal/assets/disk-explorer.sh`. Shell smoke and unit tests live in `tests/`. Design notes and implementation plans are under `docs/superpowers/`.

## Build, Test, and Development Commands

- `go test ./...` runs all Go package tests.
- `go build ./cmd/disk-explorer` builds the Go CLI locally.
- `./build.sh` regenerates `disk-explorer.sh` from `src/*.sh` and validates Bash syntax.
- `tests/run_tests.sh` runs Bash smoke tests against the generated wrapper.
- `./disk-explorer.sh /path` runs the explorer against a local path.
- `./disk-explorer.sh --bash /path` forces the Bash fallback.

Run `./build.sh` after editing files in `src/` so the distributable wrapper and embedded asset stay in sync.

## Coding Style & Naming Conventions

Format Go code with `gofmt`; keep package names short and lowercase. Use exported names only for cross-package APIs and keep tests beside the package they cover, for example `internal/scanner/scanner_test.go`. Bash scripts use `#!/usr/bin/env bash`, `set -euo pipefail` where appropriate, lowercase function names, and uppercase global configuration variables such as `REMOTE_HOSTS` or `SORT_MODE`.

## Testing Guidelines

Prefer focused Go tests for scanner, TUI model, and remote behavior. Name Go tests `TestSomethingSpecific`. Bash tests should cover generated-script behavior, portability, and helper functions; keep reusable checks in `tests/run_tests.sh` or Bats files under `tests/`. Before submitting, run `go test ./...`, `./build.sh`, and `tests/run_tests.sh`.

## Commit & Pull Request Guidelines

Recent history uses concise imperative commits, often Conventional Commit style such as `feat: add bootstrap wrapper` or scoped messages like `feat(tui): optimize performance`. Follow that pattern. Pull requests should describe the user-visible change, list validation commands run, link related issues, and include terminal screenshots or recordings when TUI behavior changes.

## Security & Configuration Tips

Treat remote host input as untrusted. Preserve validation in `internal/remote/` and `src/remote.sh`, avoid shell evaluation of host strings, and do not commit generated binaries or local machine paths.
