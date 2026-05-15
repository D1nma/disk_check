#!/usr/bin/env bash
# disk-explorer installer
# Usage: curl -fsSL https://raw.githubusercontent.com/D1nma/disk_check/main/install.sh | bash
set -euo pipefail

REPO="D1nma/disk_check"
INSTALL_DIR="${DISK_EXPLORER_INSTALL_DIR:-${HOME}/.local/bin}"

die() { printf "Erreur: %s\n" "$*" >&2; exit 1; }

get_os() {
    case "$(uname -s)" in
        Linux*)  echo "linux" ;;
        Darwin*) echo "darwin" ;;
        *)       die "Système non supporté: $(uname -s)" ;;
    esac
}

get_arch() {
    case "$(uname -m)" in
        x86_64|amd64)   echo "amd64" ;;
        arm64|aarch64)  echo "arm64" ;;
        *)               die "Architecture non supportée: $(uname -m)" ;;
    esac
}

fetch_latest_tag() {
    if command -v curl >/dev/null 2>&1; then
        curl -fsSL "https://api.github.com/repos/${REPO}/releases/latest" \
            -H "Accept: application/vnd.github+json" \
            -H "User-Agent: disk-explorer-installer" \
          | grep '"tag_name"' | sed 's/.*"tag_name": *"\([^"]*\)".*/\1/'
    elif command -v wget >/dev/null 2>&1; then
        wget -qO- "https://api.github.com/repos/${REPO}/releases/latest" \
          | grep '"tag_name"' | sed 's/.*"tag_name": *"\([^"]*\)".*/\1/'
    else
        die "curl ou wget est requis"
    fi
}

download() {
    local url=$1 dest=$2
    if command -v curl >/dev/null 2>&1; then
        curl -fsSL "$url" -o "$dest"
    else
        wget -qO "$dest" "$url"
    fi
}

verify_sha256() {
    local file=$1 expected=$2
    local actual
    if command -v sha256sum >/dev/null 2>&1; then
        actual=$(sha256sum "$file" | awk '{print $1}')
    elif command -v shasum >/dev/null 2>&1; then
        actual=$(shasum -a 256 "$file" | awk '{print $1}')
    else
        printf "Avertissement: sha256sum/shasum non disponible, checksum ignoré\n" >&2
        return 0
    fi
    if [[ "$actual" != "$expected" ]]; then
        die "Checksum SHA256 invalide (attendu: $expected, obtenu: $actual)"
    fi
}

# === GLOBALS ===
TMP_FILE=""

cleanup() {
    [[ -n "$TMP_FILE" ]] && rm -f "$TMP_FILE" "${TMP_FILE}.sums"
}
trap cleanup EXIT

main() {
    local os arch tag name url sums_url dest

    os=$(get_os)
    arch=$(get_arch)

    printf "Récupération de la dernière version...\n"
    tag=$(fetch_latest_tag)
    [[ -n "$tag" ]] || die "impossible de récupérer la dernière version"
    printf "Version: %s\n" "$tag"

    name="disk-explorer-${os}-${arch}"
    url="https://github.com/${REPO}/releases/download/${tag}/${name}"
    sums_url="https://github.com/${REPO}/releases/download/${tag}/SHA256SUMS"

    TMP_FILE=$(mktemp)

    printf "Téléchargement de %s...\n" "$name"
    download "$url" "$TMP_FILE"

    # Verify SHA256
    printf "Vérification du checksum...\n"
    download "$sums_url" "${TMP_FILE}.sums" 2>/dev/null || true
    if [[ -s "${TMP_FILE}.sums" ]]; then
        expected=$(grep "${name}$" "${TMP_FILE}.sums" | awk '{print $1}')
        [[ -n "$expected" ]] && verify_sha256 "$TMP_FILE" "$expected"
        printf "Checksum OK\n"
    fi

    mkdir -p "$INSTALL_DIR"
    dest="${INSTALL_DIR}/disk-explorer"
    chmod 755 "$TMP_FILE"
    mv "$TMP_FILE" "$dest"
    printf "Installé : %s\n" "$dest"

    # Remind about PATH if needed
    if ! echo ":${PATH}:" | grep -q ":${INSTALL_DIR}:"; then
        printf "\nAjoutez cette ligne à votre .bashrc / .zshrc :\n"
        printf '  export PATH="%s:$PATH"\n' "$INSTALL_DIR"
    fi

    printf "\nPour lancer : disk-explorer [dossier]\n"
}

main "$@"
