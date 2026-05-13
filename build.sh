#!/usr/bin/env bash
# build.sh — concatène src/ → disk-explorer.sh
set -euo pipefail

SCRIPT_DIR="$(cd -- "$(dirname -- "${BASH_SOURCE[0]}")" && pwd)"
OUT="$SCRIPT_DIR/disk-explorer.sh"
TMP="$(mktemp "$SCRIPT_DIR/.disk-explorer.build.XXXXXX")"

trap 'rm -f "$TMP"' EXIT

# src/main.sh est scindé autour de sa première définition de fonction.
# - "header" : shebang, shim, set -u, constantes, variables, traps (code inline)
# - "footer" : fonctions d'init, usage, parse_args, main(), guard BASH_SOURCE
#
# Le header est tout ce qui précède la première ligne `nom() {`.
# Le footer est le reste.

# On prépare d'abord le contenu pour avoir le bon hash de commit si on commit après.
# Mais ici on veut le hash AU MOMENT du build.
GIT_VERSION=$(git describe --tags --exact-match 2>/dev/null || git rev-parse --short HEAD 2>/dev/null || echo "dev")

awk '
  /^[[:space:]]*[a-zA-Z_][a-zA-Z0-9_]*[[:space:]]*\(\)/ { exit }
  { print }
' "$SCRIPT_DIR/src/main.sh" | sed "s/^VERSION=.*/VERSION=\"$GIT_VERSION\"/" >> "$TMP"

for module in utils scan display tui remote; do
  printf '\n' >> "$TMP"
  cat "$SCRIPT_DIR/src/${module}.sh" >> "$TMP"
done

printf '\n' >> "$TMP"
awk '
  found || /^[[:space:]]*[a-zA-Z_][a-zA-Z0-9_]*[[:space:]]*\(\)/ { found=1; print }
' "$SCRIPT_DIR/src/main.sh" >> "$TMP"

chmod 755 "$TMP"
bash -n "$TMP" || { echo "ERREUR: syntaxe invalide dans le fichier généré" >&2; exit 1; }
mv "$TMP" "$OUT"
cp "$OUT" "$SCRIPT_DIR/internal/assets/disk-explorer.sh"
echo "Build OK → $OUT (and synchronized to internal/assets)"
