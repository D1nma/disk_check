#!/usr/bin/env bash
# install.sh — Installe disk-explorer dans ~/.local/bin
# Usage: curl -fsSL https://example.com/install.sh | bash
# Override URL: DISK_EXPLORER_URL=https://… bash install.sh
set -euo pipefail

# ── Configuration ──────────────────────────────────────────────────────────
# Mettre à jour DISK_EXPLORER_URL avant de publier.
INSTALL_URL="${DISK_EXPLORER_URL:-https://raw.githubusercontent.com/OWNER/REPO/main/disk-explorer.sh}"
INSTALL_DIR="${HOME}/.local/bin"
INSTALL_NAME="disk-explorer"

# ── Vérifications ──────────────────────────────────────────────────────────

check_bash() {
  local major="${BASH_VERSINFO[0]:-0}" minor="${BASH_VERSINFO[1]:-0}"
  if (( major < 4 || (major == 4 && minor < 4) )); then
    printf 'Erreur: Bash >= 4.4 requis (actuel: %s)\n' "$BASH_VERSION" >&2
    printf 'Sur macOS: brew install bash\n' >&2
    exit 1
  fi
}

check_downloader() {
  if command -v curl >/dev/null 2>&1; then
    DOWNLOADER="curl"
  elif command -v wget >/dev/null 2>&1; then
    DOWNLOADER="wget"
  else
    printf 'Erreur: curl ou wget requis.\n' >&2; exit 1
  fi
}

download_file() {
  local url="$1" dest="$2"
  if [[ "$DOWNLOADER" == "curl" ]]; then
    curl -fsSL --output "$dest" "$url"
  else
    wget -qO "$dest" "$url"
  fi
}

# ── Installation ───────────────────────────────────────────────────────────

_TMP=""  # global so the EXIT trap can reference it after main() returns
trap 'rm -f "$_TMP"' EXIT

main() {
  check_bash
  check_downloader
  mkdir -p "$INSTALL_DIR"

  _TMP="$(mktemp /tmp/disk-explorer.install.XXXXXX)"

  printf 'Téléchargement depuis %s…\n' "$INSTALL_URL"
  download_file "$INSTALL_URL" "$_TMP"

  bash -n "$_TMP" || {
    printf 'Erreur: le fichier téléchargé est invalide.\n' >&2; exit 1
  }

  chmod 755 "$_TMP"
  mv "$_TMP" "${INSTALL_DIR}/${INSTALL_NAME}"
  _TMP=""  # already moved; prevent trap from trying to remove it
  printf 'Installé : %s/%s\n' "$INSTALL_DIR" "$INSTALL_NAME"

  if [[ ":$PATH:" != *":${INSTALL_DIR}:"* ]]; then
    printf '\n⚠  %s n'"'"'est pas dans votre PATH.\n' "$INSTALL_DIR"
    printf 'Ajoutez à ~/.bashrc ou ~/.zshrc :\n'
    printf '  export PATH="%s:$PATH"\n\n' "$INSTALL_DIR"
  else
    printf '\nLancez : %s\n' "$INSTALL_NAME"
  fi
}

main "$@"
