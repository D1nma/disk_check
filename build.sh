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

# Versions injectées dans le header (lignes VERSION= et BINARY_TAG= de src/main.sh),
# calculées AU MOMENT du build sur les seuls tags de release (v<chiffre>…) :
#   VERSION    (affichage)  : tag exact, sinon <dernier-tag>-<n>-g<hash>,
#                             sinon hash court, sinon "dev" (hors dépôt git).
#   BINARY_TAG (bootstrap)  : tag exact, sinon dernier tag atteignable, sinon
#                             vide. C'est la release dont try_go_binary
#                             télécharge le binaire : un rebuild sur un commit
#                             non tagué de main garde donc le binaire de la
#                             dernière release au lieu de le désactiver.
_git() { git -C "$SCRIPT_DIR" "$@" 2>/dev/null; }
GIT_VERSION=$(_git describe --tags --match 'v[0-9]*' || _git rev-parse --short HEAD || echo "dev")
GIT_BINARY_TAG=$(_git describe --tags --match 'v[0-9]*' --abbrev=0 || :)

# Ces valeurs sont insérées dans du code Bash et dans une expression sed.
[[ "$GIT_VERSION" =~ ^[0-9A-Za-z._+-]+$ ]] || {
  echo "ATTENTION: version '$GIT_VERSION' ignorée (caractères non autorisés)" >&2
  GIT_VERSION="dev"
}
[[ -z "$GIT_BINARY_TAG" || "$GIT_BINARY_TAG" =~ ^v[0-9][0-9A-Za-z._+-]*$ ]] || {
  echo "ATTENTION: tag '$GIT_BINARY_TAG' ignoré (caractères non autorisés) : Bash seul" >&2
  GIT_BINARY_TAG=""
}

awk '
  /^[[:space:]]*[a-zA-Z_][a-zA-Z0-9_]*[[:space:]]*\(\)/ { exit }
  { print }
' "$SCRIPT_DIR/src/main.sh" \
  | sed -e "s/^VERSION=.*/VERSION=\"$GIT_VERSION\"/" \
        -e "s/^BINARY_TAG=.*/BINARY_TAG=\"$GIT_BINARY_TAG\"/" >> "$TMP"

# Les placeholders doivent exister dans le header, sinon le bootstrap ne
# serait jamais activé (ou la version jamais affichée) sans erreur visible.
for _var in VERSION BINARY_TAG; do
  grep -q "^${_var}=" "$TMP" || { echo "ERREUR: ligne ${_var}= absente du header de src/main.sh" >&2; exit 1; }
done

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
echo "Build OK → $OUT"
