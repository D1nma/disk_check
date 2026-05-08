# macOS/Linux Compatibility Implementation Plan

> **For agentic workers:** REQUIRED: Use superpowers:subagent-driven-development (if subagents available) or superpowers:executing-plans to implement this plan. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Rendre `disk-explorer.sh` plug & play sur macOS et GNU/Linux sans intervention manuelle.

**Architecture:** Shim Bash en tête de script pour re-exec avec brew bash si Bash < 4.4. Variables de commandes globales (`FIND_CMD`, `SORT_CMD`, etc.) initialisées aux valeurs GNU, surchargées sur macOS via `resolve_gnu_tools_macos()`. Branches BSD pour `df` et `date`. Installation automatique via Homebrew si outils GNU manquants.

**Tech Stack:** Bash ≥ 4.4, GNU coreutils + findutils (via Homebrew sur macOS), bats (tests)

**Spec:** `docs/superpowers/specs/2026-05-08-macos-linux-compat-design.md`

---

## Notes importantes avant de commencer

### Shim et `source`
Le shim de re-exec **doit être gardé** par `[[ "${BASH_SOURCE[0]}" == "${0}" ]]` pour ne pas tenter un `exec` quand le script est sourcé (par les tests bats). Sans cette garde, `source disk-explorer.sh` dans un test remplacerait le shell de test.

### Pattern bats existant
Les tests sourcent le script : `source "${BATS_TEST_DIRNAME}/../disk-explorer.sh"`. Cela ne déclenche pas `main` (guard ligne 1673). Nouveau fichier de test : `tests/platform_compat.bats`.

### Commande pour lancer les tests bats
```bash
bats tests/is_integer.bats
bats tests/platform_compat.bats
```
Si bats n'est pas installé : `brew install bats-core` (macOS) ou `apt install bats` (Linux).

---

## Chunk 1 : Variables globales + shim + fix [[ -v ]]

### Task 1 : Initialiser les variables de commandes et PLATFORM dans la section VARIABLES

**Files:**
- Modify: `disk-explorer.sh` (section VARIABLES, après la ligne `LAST_WARNING=""`)

- [ ] **Step 1 : Ajouter les variables globales dans la section VARIABLES**

Chercher le bloc (lignes 55–62) :
```bash
TEMP_ROOT=""
LAST_WARNING=""
SCAN_WARNING=""
PARTIAL_SCAN_DETECTED=0
```
Insérer juste après `PARTIAL_SCAN_DETECTED=0`, avant le bloc `declare -a` :
```bash
PLATFORM=""
FIND_CMD="find"
SORT_CMD="sort"
HEAD_CMD="head"
DU_CMD="du"
NUMFMT_CMD="numfmt"
```

- [ ] **Step 2 : Vérifier la syntaxe**
```bash
bash -n disk-explorer.sh
```
Attendu : aucune sortie (pas d'erreur).

- [ ] **Step 3 : Commit**
```bash
git add disk-explorer.sh
git commit -m "feat: add global platform and command variables"
```

---

### Task 2 : Insérer le shim de bootstrap Bash

**Files:**
- Modify: `disk-explorer.sh` (ligne 2, après le shebang)

- [ ] **Step 1 : Insérer le shim après `#!/usr/bin/env bash` (ligne 1)**

Le contenu à insérer en ligne 2, avant `# =====... DISK EXPLORER` :
```bash
# Shim : garantit Bash >= 4.4 avant toute syntaxe incompatible.
# Gardé par BASH_SOURCE[0]==$0 pour ne pas re-exec quand le script est sourcé (tests).
if [[ "${BASH_SOURCE[0]}" == "${0}" ]]; then
  if (( BASH_VERSINFO[0] < 4 || (BASH_VERSINFO[0] == 4 && BASH_VERSINFO[1] < 4) )); then
    _self="$(cd -- "$(dirname -- "${BASH_SOURCE[0]}")" && pwd)/$(basename -- "${BASH_SOURCE[0]}")"
    for _bash in /opt/homebrew/bin/bash /usr/local/bin/bash; do
      [[ -x "$_bash" ]] && exec "$_bash" -- "$_self" "$@"
    done
    printf 'Erreur: Bash >= 4.4 requis.\nSur macOS: brew install bash\nPuis relancer: /opt/homebrew/bin/bash %s\n' "${BASH_SOURCE[0]}" >&2
    exit 1
  fi
fi
```

- [ ] **Step 2 : Vérifier la syntaxe**
```bash
bash -n disk-explorer.sh
```
Attendu : aucune sortie.

- [ ] **Step 3 : Commit**
```bash
git add disk-explorer.sh
git commit -m "feat: add bash 4.4+ bootstrap shim for macOS compatibility"
```

---

### Task 3 : Remplacer `[[ -v NO_COLOR ]]` par la syntaxe portable

**Files:**
- Modify: `disk-explorer.sh` (ligne ~46)

- [ ] **Step 1 : Localiser et remplacer**

Remplacer le bloc :
```bash
# Conforme no-color.org : toute variable NO_COLOR définie (même vide) désactive les couleurs.
# [[ -v ]] est sûr avec set -u et disponible dès Bash 4.2 (< 4.3 requis ailleurs).
if [[ -v NO_COLOR ]]; then
  NO_COLOR=1
else
  NO_COLOR=0
fi
```
Par :
```bash
# Conforme no-color.org : toute variable NO_COLOR définie (même vide) désactive les couleurs.
# ${VAR+x} est portable dès Bash 3.x et sûr avec set -u.
if [[ -n "${NO_COLOR+x}" ]]; then
  NO_COLOR=1
else
  NO_COLOR=0
fi
```

- [ ] **Step 2 : Vérifier la syntaxe**
```bash
bash -n disk-explorer.sh
```

- [ ] **Step 3 : Commit**
```bash
git add disk-explorer.sh
git commit -m "fix: replace [[ -v NO_COLOR ]] with portable \${NO_COLOR+x}"
```

---

## Chunk 2 : Détection de plateforme et résolution des outils

### Task 4 : Écrire les tests pour `detect_platform()`

**Files:**
- Create: `tests/platform_compat.bats`

- [ ] **Step 1 : Créer le fichier de tests**

```bash
#!/usr/bin/env bats

setup() {
    source "${BATS_TEST_DIRNAME}/../disk-explorer.sh"
}

# ── detect_platform ──────────────────────────────────────────────

@test "detect_platform: linux quand OSTYPE=linux-gnu" {
    OSTYPE="linux-gnu"
    detect_platform
    [ "$PLATFORM" = "linux" ]
}

@test "detect_platform: linux quand OSTYPE=linux-musl" {
    OSTYPE="linux-musl"
    detect_platform
    [ "$PLATFORM" = "linux" ]
}

@test "detect_platform: macos quand OSTYPE=darwin23.0" {
    OSTYPE="darwin23.0"
    detect_platform
    [ "$PLATFORM" = "macos" ]
}

@test "detect_platform: macos quand OSTYPE=darwin24.0" {
    OSTYPE="darwin24.0"
    detect_platform
    [ "$PLATFORM" = "macos" ]
}
```

- [ ] **Step 2 : Vérifier que les tests échouent (fonction absente)**
```bash
bats tests/platform_compat.bats
```
Attendu : toutes les assertions FAIL avec "detect_platform: command not found" ou similaire.

- [ ] **Step 3 : Implémenter `detect_platform()` dans disk-explorer.sh**

Ajouter après la fonction `analysis_label()` (ligne ~484) :
```bash
detect_platform() {
  [[ "$OSTYPE" == darwin* ]] && PLATFORM="macos" || PLATFORM="linux"
}
```

- [ ] **Step 4 : Vérifier que les tests passent**
```bash
bats tests/platform_compat.bats
```
Attendu : 4 tests PASS.

- [ ] **Step 5 : Commit**
```bash
git add disk-explorer.sh tests/platform_compat.bats
git commit -m "feat: add detect_platform() with tests"
```

---

### Task 5 : Écrire les tests et implémenter `resolve_gnu_tools_macos()`

**Files:**
- Modify: `disk-explorer.sh`
- Modify: `tests/platform_compat.bats`

- [ ] **Step 1 : Ajouter les tests pour `resolve_gnu_tools_macos()`**

Note : on ne peut pas overrider le builtin `command` avec une fonction shell — il faut une approche par stub PATH.

Ajouter dans `tests/platform_compat.bats` :
```bash
# ── resolve_gnu_tools_macos ───────────────────────────────────────

@test "resolve_gnu_tools_macos: utilise gfind si stub gfind dans PATH" {
    local stub_dir
    stub_dir="$(mktemp -d)"
    # Créer des stubs minimaux pour tous les outils g-préfixés
    for tool in gfind gsort ghead gdu gnumfmt; do
        printf '#!/usr/bin/env bash\necho "GNU %s"\n' "$tool" > "$stub_dir/$tool"
        chmod +x "$stub_dir/$tool"
    done
    PLATFORM="macos"
    PATH="$stub_dir:$PATH" resolve_gnu_tools_macos
    [ "$FIND_CMD" = "gfind" ]
    [ "$SORT_CMD" = "gsort" ]
    [ "$HEAD_CMD" = "ghead" ]
    [ "$DU_CMD"   = "gdu"   ]
    rm -rf "$stub_dir"
}

@test "resolve_gnu_tools_macos: ne modifie pas les CMD sur linux" {
    PLATFORM="linux"
    local saved_find="$FIND_CMD"
    # Sur linux, resolve_gnu_tools_macos n'est jamais appelée — vérifier
    # que les variables gardent leurs valeurs par défaut après source
    [ "$FIND_CMD" = "find" ]
    [ "$SORT_CMD" = "sort" ]
    [ "$HEAD_CMD" = "head" ]
    [ "$DU_CMD"   = "du"   ]
}
```

- [ ] **Step 2 : Vérifier que les tests échouent**
```bash
bats tests/platform_compat.bats
```
Attendu : les nouveaux tests FAIL.

- [ ] **Step 3 : Implémenter `resolve_gnu_tools_macos()` dans disk-explorer.sh**

Ajouter juste après `detect_platform()` :
```bash
resolve_gnu_tools_macos() {
  local -a missing_tools=()
  local -a brew_pkgs=()

  _try_gnu_tool() {
    local var="$1" gnu_name="$2" pkg="$3"
    if command -v "$gnu_name" >/dev/null 2>&1; then
      printf -v "$var" '%s' "$gnu_name"
    elif command -v "${gnu_name#g}" >/dev/null 2>&1 && \
         "${gnu_name#g}" --version 2>&1 | grep -q GNU; then
      printf -v "$var" '%s' "${gnu_name#g}"
    else
      missing_tools+=("$gnu_name")
      brew_pkgs+=("$pkg")
    fi
  }

  _try_gnu_tool FIND_CMD  gfind   findutils
  _try_gnu_tool SORT_CMD  gsort   coreutils
  _try_gnu_tool HEAD_CMD  ghead   coreutils
  _try_gnu_tool DU_CMD    gdu     coreutils
  _try_gnu_tool NUMFMT_CMD gnumfmt coreutils

  unset -f _try_gnu_tool

  if (( ${#missing_tools[@]} == 0 )); then
    return 0
  fi

  # Dédupliquer les paquets
  local -A _seen=()
  local -a unique_pkgs=()
  local p
  for p in "${brew_pkgs[@]}"; do
    if [[ -z "${_seen[$p]+x}" ]]; then
      _seen[$p]=1
      unique_pkgs+=("$p")
    fi
  done

  local missing_str
  printf -v missing_str '%s ' "${missing_tools[@]}"
  missing_str="${missing_str% }"

  if ! command -v brew >/dev/null 2>&1; then
    printf 'Erreur: outils GNU manquants: %s\n' "$missing_str" >&2
    printf 'Homebrew requis. Installez-le depuis https://brew.sh puis relancez.\n' >&2
    exit 1
  fi

  if [[ ! -t 0 || ! -t 1 ]]; then
    printf 'Erreur: outils GNU manquants: %s\n' "$missing_str" >&2
    printf 'Installez-les manuellement: brew install %s\n' "${unique_pkgs[*]}" >&2
    exit 1
  fi

  printf 'Outils GNU requis manquants: %s\n' "$missing_str" >&2
  printf 'Installation via Homebrew: brew install %s\n' "${unique_pkgs[*]}" >&2
  local answer
  read -r -p "Installer maintenant ? [o/N] " answer
  if [[ "${answer,,}" != "o" ]]; then
    printf 'Installation annulée.\n' >&2
    exit 1
  fi

  HOMEBREW_NO_AUTO_UPDATE=1 brew install "${unique_pkgs[@]}" || die "échec de l'installation Homebrew"

  # Re-vérifier après install
  local tool
  for tool in "${missing_tools[@]}"; do
    command -v "$tool" >/dev/null 2>&1 || die "outil toujours manquant après install: $tool"
  done

  # Réappel pour fixer les variables CMD
  resolve_gnu_tools_macos
}
```

- [ ] **Step 4 : Vérifier que les tests passent**
```bash
bats tests/platform_compat.bats
```

- [ ] **Step 5 : Vérifier la syntaxe globale**
```bash
bash -n disk-explorer.sh
```

- [ ] **Step 6 : Commit**
```bash
git add disk-explorer.sh tests/platform_compat.bats
git commit -m "feat: add resolve_gnu_tools_macos() with brew install prompt"
```

---

### Task 6 : Mettre à jour `main()` et le flux de démarrage

**Files:**
- Modify: `disk-explorer.sh` (fonction `main()`)

- [ ] **Step 1 : Mettre à jour `main()` pour appeler `detect_platform()` en premier**

Localiser dans `main()` :
```bash
main() {
  parse_args "$@"
  init_numfmt_support
```
Remplacer par :
```bash
main() {
  parse_args "$@"
  detect_platform
  if [[ "$PLATFORM" == "macos" ]]; then
    resolve_gnu_tools_macos
  fi
  init_numfmt_support
```

- [ ] **Step 2 : Vérifier la syntaxe**
```bash
bash -n disk-explorer.sh
```

- [ ] **Step 3 : Commit**
```bash
git add disk-explorer.sh
git commit -m "feat: call detect_platform and resolve_gnu_tools_macos early in main()"
```

---

## Chunk 3 : Mise à jour des sites d'appel find/sort/head/du/numfmt

### Task 7 : Remplacer les appels directs aux outils dans toutes les fonctions

**Files:**
- Modify: `disk-explorer.sh` (8 fonctions)

Référence des sites d'appel (numéros de ligne approximatifs avant modifications) :

| Fonction | Outil | Remplacement |
|---|---|---|
| `build_find_prefix()` l.790 | `find` | `"$FIND_CMD"` |
| `check_runtime_requirements()` l.235 | `find` | `"$FIND_CMD"` |
| `self_check_report()` l.287 | `find` | `"$FIND_CMD"` |
| `scan_subdirs_to_file()` l.827, 844 | `sort`, `head` | `"$SORT_CMD"`, `"$HEAD_CMD"` |
| `scan_top_files_to_file()` l.880, 886 | `sort`, `head` | `"$SORT_CMD"`, `"$HEAD_CMD"` |
| `tree_print_node()` dans `print_tree_view` l.1599 | `sort` | `"$SORT_CMD"` |
| `build_du_cmd()` l.757 | `du` | `"$DU_CMD"` |
| `build_du_tree_cmd()` l.773 | `du` | `"$DU_CMD"` |
| `self_check_report()` l.293,299,305 | `sort`, `head`, `du` | `"$SORT_CMD"`, `"$HEAD_CMD"`, `"$DU_CMD"` |

- [ ] **Step 1 : `build_du_cmd()` — remplacer `du` par `"$DU_CMD"`**

Trouver :
```bash
  out_arr=(du -P -0 -B1 --max-depth=1)
```
Remplacer par :
```bash
  out_arr=("$DU_CMD" -P -0 -B1 --max-depth=1)
```

- [ ] **Step 2 : `build_du_tree_cmd()` — remplacer `du` par `"$DU_CMD"`**

Trouver :
```bash
  out_arr=(du -P -0 -B1 --max-depth="$TREE_DEPTH")
```
Remplacer par :
```bash
  out_arr=("$DU_CMD" -P -0 -B1 --max-depth="$TREE_DEPTH")
```

- [ ] **Step 3 : `build_find_prefix()` — remplacer `find` par `"$FIND_CMD"`**

Trouver :
```bash
  out_arr=(find -P "$CURRENT_DIR")
```
Remplacer par :
```bash
  out_arr=("$FIND_CMD" -P "$CURRENT_DIR")
```

- [ ] **Step 4 : `scan_subdirs_to_file()` — remplacer `sort` et `head`**

Trouver (branche mtime) :
```bash
        LC_ALL=C sort -zrn |
        head -z -n "$TOP_COUNT"
```
Remplacer par :
```bash
        LC_ALL=C "$SORT_CMD" -zrn |
        "$HEAD_CMD" -z -n "$TOP_COUNT"
```

Trouver (branche size) :
```bash
        LC_ALL=C sort -zrn |
        head -z -n "$TOP_COUNT"
```
Remplacer par :
```bash
        LC_ALL=C "$SORT_CMD" -zrn |
        "$HEAD_CMD" -z -n "$TOP_COUNT"
```

- [ ] **Step 5 : `scan_top_files_to_file()` — remplacer `sort` et `head`**

Deux occurrences (branche apparent et real) :
```bash
        LC_ALL=C sort -zrn |
        head -z -n "$TOP_FILES_COUNT"
```
Remplacer les deux par :
```bash
        LC_ALL=C "$SORT_CMD" -zrn |
        "$HEAD_CMD" -z -n "$TOP_FILES_COUNT"
```

- [ ] **Step 6 : `tree_print_node()` dans `print_tree_view` — remplacer `sort`**

Trouver :
```bash
      done <<< "$children_raw" | LC_ALL=C sort -zrn | awk
```
Remplacer par :
```bash
      done <<< "$children_raw" | LC_ALL=C "$SORT_CMD" -zrn | awk
```

- [ ] **Step 7 : `check_runtime_requirements()` — remplacer find, sort, head et du (lignes 235-238)**

Trouver le bloc entier :
```bash
  find "$req_dir" -maxdepth 0 -printf '' >/dev/null 2>&1 || { rm -rf -- "$req_dir"; die "GNU find avec -printf requis"; }
  printf '%b' 'a\0' | sort -z >/dev/null 2>&1 || { rm -rf -- "$req_dir"; die "GNU sort avec -z requis"; }
  printf '%b' 'a\0' | head -z -n 1 >/dev/null 2>&1 || { rm -rf -- "$req_dir"; die "GNU head avec -z requis"; }
  du -0 --max-depth=0 "$req_dir" >/dev/null 2>&1 || { rm -rf -- "$req_dir"; die "GNU du avec -0 requis"; }
```
Remplacer par :
```bash
  "$FIND_CMD" "$req_dir" -maxdepth 0 -printf '' >/dev/null 2>&1 || { rm -rf -- "$req_dir"; die "GNU find avec -printf requis"; }
  printf '%b' 'a\0' | "$SORT_CMD" -z >/dev/null 2>&1 || { rm -rf -- "$req_dir"; die "GNU sort avec -z requis"; }
  printf '%b' 'a\0' | "$HEAD_CMD" -z -n 1 >/dev/null 2>&1 || { rm -rf -- "$req_dir"; die "GNU head avec -z requis"; }
  "$DU_CMD" -0 --max-depth=0 "$req_dir" >/dev/null 2>&1 || { rm -rf -- "$req_dir"; die "GNU du avec -0 requis"; }
```

- [ ] **Step 8 : `self_check_report()` — remplacer find, sort, head, du dans les tests GNU**

Trouver :
```bash
    if find "$req_dir" -maxdepth 0 -printf '' >/dev/null 2>&1; then
```
Remplacer par :
```bash
    if "$FIND_CMD" "$req_dir" -maxdepth 0 -printf '' >/dev/null 2>&1; then
```

Trouver :
```bash
    if printf '%b' 'a\0' | sort -z >/dev/null 2>&1; then
```
Remplacer par :
```bash
    if printf '%b' 'a\0' | "$SORT_CMD" -z >/dev/null 2>&1; then
```

Trouver :
```bash
    if printf '%b' 'a\0' | head -z -n 1 >/dev/null 2>&1; then
```
Remplacer par :
```bash
    if printf '%b' 'a\0' | "$HEAD_CMD" -z -n 1 >/dev/null 2>&1; then
```

Trouver :
```bash
    if du -0 --max-depth=0 "$req_dir" >/dev/null 2>&1; then
```
Remplacer par :
```bash
    if "$DU_CMD" -0 --max-depth=0 "$req_dir" >/dev/null 2>&1; then
```

- [ ] **Step 9 : Vérifier la syntaxe**
```bash
bash -n disk-explorer.sh
```

- [ ] **Step 10 : Commit**
```bash
git add disk-explorer.sh
git commit -m "feat: route find/sort/head/du through CMD variables for cross-platform support"
```

---

### Task 8 : Mettre à jour `init_numfmt_support()` et `human_size()`

**Files:**
- Modify: `disk-explorer.sh`

- [ ] **Step 1 : `init_numfmt_support()` — utiliser `$NUMFMT_CMD`**

Trouver :
```bash
  command -v numfmt >/dev/null 2>&1 && HAVE_NUMFMT=1 || HAVE_NUMFMT=0
```
Remplacer par :
```bash
  command -v "$NUMFMT_CMD" >/dev/null 2>&1 && HAVE_NUMFMT=1 || HAVE_NUMFMT=0
```

- [ ] **Step 2 : `human_size()` — utiliser `$NUMFMT_CMD`**

Trouver :
```bash
    numfmt --to=iec-i --suffix=B --format="%.1f" "$size" 2>/dev/null && return 0
```
Remplacer par :
```bash
    "$NUMFMT_CMD" --to=iec-i --suffix=B --format="%.1f" "$size" 2>/dev/null && return 0
```

- [ ] **Step 3 : Vérifier la syntaxe**
```bash
bash -n disk-explorer.sh
```

- [ ] **Step 4 : Commit**
```bash
git add disk-explorer.sh
git commit -m "feat: use \$NUMFMT_CMD in init_numfmt_support and human_size"
```

---

## Chunk 4 : Adaptations BSD (df, date, realpath)

### Task 9 : Tests et implémentation de `get_df_fields()` macOS

**Files:**
- Modify: `tests/platform_compat.bats`
- Modify: `disk-explorer.sh`

- [ ] **Step 1 : Ajouter les tests `get_df_fields` macOS**

Ajouter dans `tests/platform_compat.bats` :
```bash
# ── get_df_fields (macOS) ─────────────────────────────────────────

# Helper : mock df -Pk avec une sortie donnée.
# Utilise une variable exportée (_MOCK_DF_OUT) pour que la fonction df()
# soit accessible dans les sous-shells (command substitution).
_mock_df_pk() {
  export _MOCK_DF_OUT="$1"
  df() { printf '%s\n' "$_MOCK_DF_OUT"; }
  export -f df
}

@test "get_df_fields macOS: parse standard 6 colonnes" {
    PLATFORM="macos"
    CURRENT_DIR="/"
    # Filesystem  1024-blocs  Used  Avail  Capacity  Mounted
    _mock_df_pk "Filesystem 1024-blocks Used Available Capacity Mounted on
/dev/disk3s1s1 976490568 42949672 933540895 5% /"
    result=$(get_df_fields)
    [[ "$result" == *"/"* ]]       # mount point présent
    [[ "$result" == *"5%"* ]]     # pourcentage présent
    # size = 1024-blocs * 1024 bytes
    size=$(awk '{print $1}' <<< "$result")
    (( size > 0 ))
    unset -f df
}

@test "get_df_fields macOS: mount point avec espace" {
    PLATFORM="macos"
    CURRENT_DIR="/Volumes/My Drive"
    _mock_df_pk "Filesystem 1024-blocks Used Available Capacity Mounted on
/dev/disk5s1 976490568 10000000 966490568 2% /Volumes/My Drive"
    result=$(get_df_fields)
    [[ "$result" == *"/Volumes/My Drive"* ]]
    unset -f df
}

@test "get_df_fields macOS: colonnes APFS avec inode (9 colonnes)" {
    PLATFORM="macos"
    CURRENT_DIR="/System/Volumes/Data"
    _mock_df_pk "Filesystem 1024-blocks Used Available Capacity iused ifree %iused Mounted on
/dev/disk3s5 976490568 200000000 776490568 21% 1500000 5800000000 0% /System/Volumes/Data"
    result=$(get_df_fields)
    [[ "$result" == *"/System/Volumes/Data"* ]]
    unset -f df
}
```

- [ ] **Step 2 : Vérifier que les tests échouent**
```bash
bats tests/platform_compat.bats --filter "get_df_fields"
```

- [ ] **Step 3 : Modifier `get_df_fields()` pour ajouter la branche macOS**

Trouver le début de `get_df_fields()` :
```bash
get_df_fields() {
  local df_out
  df_out=$(df --output=size,used,avail,pcent,target -B1 -- "$CURRENT_DIR" 2>/dev/null | tail -n 1)
```
Remplacer toute la fonction par :
```bash
get_df_fields() {
  local df_out size used avail usep mounted

  if [[ "$PLATFORM" == "macos" ]]; then
    # -P (POSIX) empêche le wrapping des longues lignes sur deux lignes.
    df_out=$(df -Pk -- "$CURRENT_DIR" 2>/dev/null | tail -n 1)
    [[ -z "$df_out" ]] && return 1
    # Colonnes : Filesystem 1024-blocs Used Available Capacity [iused ifree %iused] Mounted-on
    # printf "%d" évite la notation scientifique sur les disques > 1 To.
    size=$(awk '{printf "%d\n", $2 * 1024}' <<< "$df_out")
    used=$(awk '{printf "%d\n", $3 * 1024}' <<< "$df_out")
    avail=$(awk '{printf "%d\n", $4 * 1024}' <<< "$df_out")
    usep=$(awk '{print $5}' <<< "$df_out")
    # Le mount point commence après Capacity ($5) ; peut contenir des espaces.
    # Sur APFS avec colonnes inode, le mount point est le premier champ commençant par "/".
    # Fallback sur $NF si aucun champ ne commence par "/".
    mounted=$(awk '{
      for(i=6;i<=NF;i++){
        if($i~/^\//){
          for(j=i;j<=NF;j++) printf "%s%s",$j,(j<NF?" ":"")
          print ""; exit
        }
      }
      print $NF
    }' <<< "$df_out")
  else
    df_out=$(df --output=size,used,avail,pcent,target -B1 -- "$CURRENT_DIR" 2>/dev/null | tail -n 1)
    [[ -z "$df_out" ]] && return 1
    read -r size used avail usep mounted <<< "$df_out"
  fi

  printf '%s %s %s %s %s\n' "$size" "$used" "$avail" "$usep" "$mounted"
}
```

- [ ] **Step 4 : Vérifier que les tests passent**
```bash
bats tests/platform_compat.bats --filter "get_df_fields"
```

- [ ] **Step 5 : Vérifier la syntaxe**
```bash
bash -n disk-explorer.sh
```

- [ ] **Step 6 : Commit**
```bash
git add disk-explorer.sh tests/platform_compat.bats
git commit -m "feat: add macOS BSD df -Pk branch in get_df_fields()"
```

---

### Task 10 : Tests et implémentation de `date_from_epoch()` macOS

**Files:**
- Modify: `tests/platform_compat.bats`
- Modify: `disk-explorer.sh`

- [ ] **Step 1 : Ajouter les tests**

```bash
# ── date_from_epoch ───────────────────────────────────────────────

@test "date_from_epoch: epoch 0 sur linux produit une date valide" {
    PLATFORM="linux"
    result=$(date_from_epoch "0")
    [[ "$result" =~ ^[0-9]{4}-[0-9]{2}-[0-9]{2}\ [0-9]{2}:[0-9]{2}$ ]]
}

@test "date_from_epoch: accepte un timestamp flottant (strip .xxx)" {
    PLATFORM="linux"
    result=$(date_from_epoch "1700000000.5")
    [[ "$result" != "?" ]]
    [[ "$result" =~ ^[0-9]{4} ]]
}
```

Note : le test macOS `date -r` ne peut pas être facilement isolé sans mock. Vérifier manuellement sur macOS après implémentation.

- [ ] **Step 2 : Vérifier que les tests passent sur linux (comportement inchangé)**
```bash
bats tests/platform_compat.bats --filter "date_from_epoch"
```

- [ ] **Step 3 : Modifier `date_from_epoch()` pour ajouter la branche macOS**

Trouver :
```bash
date_from_epoch() {
  date -d "@${1%.*}" '+%Y-%m-%d %H:%M' 2>/dev/null || echo "?"
}
```
Remplacer par :
```bash
date_from_epoch() {
  if [[ "$PLATFORM" == "macos" ]]; then
    date -r "${1%.*}" '+%Y-%m-%d %H:%M' 2>/dev/null || echo "?"
  else
    date -d "@${1%.*}" '+%Y-%m-%d %H:%M' 2>/dev/null || echo "?"
  fi
}
```

- [ ] **Step 4 : Vérifier que les tests passent**
```bash
bats tests/platform_compat.bats --filter "date_from_epoch"
```

- [ ] **Step 5 : Commit**
```bash
git add disk-explorer.sh tests/platform_compat.bats
git commit -m "feat: add macOS date -r branch in date_from_epoch()"
```

---

### Task 11 : Corriger `resolve_path_lexical()` — `realpath -m` non supporté sur BSD

**Files:**
- Modify: `disk-explorer.sh`

- [ ] **Step 1 : Modifier `resolve_path_lexical()` pour tester `-m` avant usage**

Trouver :
```bash
  if command -v realpath >/dev/null 2>&1; then
    realpath -m -- "$input"
    return
  fi
```
Remplacer par :
```bash
  if realpath -m -- / >/dev/null 2>&1; then
    realpath -m -- "$input"
    return
  fi
```

- [ ] **Step 2 : Vérifier la syntaxe**
```bash
bash -n disk-explorer.sh
```

- [ ] **Step 3 : Commit**
```bash
git add disk-explorer.sh
git commit -m "fix: test realpath -m support before use (BSD realpath lacks -m)"
```

---

## Chunk 5 : Vérifications de version, documentation, self-check

### Task 12 : Mettre à jour les vérifications Bash (4.3 → 4.4) et supprimer le check Linux-only

**Files:**
- Modify: `disk-explorer.sh`

- [ ] **Step 1 : `usage()` — mettre à jour le texte**

Trouver :
```bash
  - Ce script vise GNU/Linux (GNU findutils, coreutils et Bash >= 4.3).
```
Remplacer par :
```bash
  - Ce script supporte GNU/Linux et macOS (GNU findutils, coreutils et Bash >= 4.4).
  - Sur macOS, les outils GNU sont installés automatiquement via Homebrew si nécessaire.
```

- [ ] **Step 2 : `check_runtime_requirements()` — supprimer le check Linux-only et mettre à jour la version Bash**

Trouver et supprimer cette ligne :
```bash
  [[ "${OSTYPE:-}" == linux* ]] || die "GNU/Linux requis (OSTYPE détecté: ${OSTYPE:-inconnu})"
```

Trouver :
```bash
  (( BASH_VERSINFO[0] > 4 || (BASH_VERSINFO[0] == 4 && BASH_VERSINFO[1] >= 3) )) || die "Bash >= 4.3 requis"
```
Remplacer par :
```bash
  (( BASH_VERSINFO[0] > 4 || (BASH_VERSINFO[0] == 4 && BASH_VERSINFO[1] >= 4) )) || die "Bash >= 4.4 requis"
```

- [ ] **Step 3 : `self_check_report()` — mettre à jour le check et le message Bash**

Trouver :
```bash
  if [[ "${OSTYPE:-}" == linux* ]]; then
    echo "[OK] Plateforme Linux détectée (${OSTYPE:-unknown})"
  else
    echo "[KO] Plateforme non supportée (${OSTYPE:-unknown})"
    rc=1
  fi
```
Remplacer par :
```bash
  case "${PLATFORM:-}" in
    linux)  echo "[OK] Plateforme Linux détectée (${OSTYPE:-unknown})" ;;
    macos)  echo "[OK] Plateforme macOS détectée (${OSTYPE:-unknown})" ;;
    *)      echo "[KO] Plateforme non reconnue (${OSTYPE:-unknown})"; rc=1 ;;
  esac
```

Trouver :
```bash
  if (( BASH_VERSINFO[0] > 4 || (BASH_VERSINFO[0] == 4 && BASH_VERSINFO[1] >= 3) )); then
    echo "[OK] Bash >= 4.3 (${BASH_VERSION})"
  else
    echo "[KO] Bash >= 4.3 requis (actuel: ${BASH_VERSION})"
    rc=1
  fi
```
Remplacer par :
```bash
  if (( BASH_VERSINFO[0] > 4 || (BASH_VERSINFO[0] == 4 && BASH_VERSINFO[1] >= 4) )); then
    echo "[OK] Bash >= 4.4 (${BASH_VERSION})"
  else
    echo "[KO] Bash >= 4.4 requis (actuel: ${BASH_VERSION})"
    rc=1
  fi
```

- [ ] **Step 4 : `self_check_report()` — mettre à jour le test `date` pour macOS**

Trouver (ligne ~311) :
```bash
    if date -d '@0' '+%Y-%m-%d %H:%M' >/dev/null 2>&1; then
      echo "[OK] GNU date: support -d"
    else
      echo "[KO] GNU date: -d non supporté"
      rc=1
    fi
```
Remplacer par :
```bash
    local _date_ok=0
    if [[ "$PLATFORM" == "macos" ]]; then
      date -r 0 '+%Y-%m-%d %H:%M' >/dev/null 2>&1 && _date_ok=1
    else
      date -d '@0' '+%Y-%m-%d %H:%M' >/dev/null 2>&1 && _date_ok=1
    fi
    if (( _date_ok )); then
      echo "[OK] GNU/BSD date: support epoch"
    else
      echo "[KO] date: support epoch non disponible"
      rc=1
    fi
```

- [ ] **Step 5 : `self_check_report()` — afficher les commandes résolues**

Trouver le bloc qui vérifie les commandes requises dans `self_check_report()` :
```bash
  for required_cmd in "${required_cmds[@]}"; do
    if command -v "$required_cmd" >/dev/null 2>&1; then
      echo "[OK] Commande présente: $required_cmd"
    else
      echo "[KO] Commande manquante: $required_cmd"
```
Le bloc de vérification doit utiliser les variables CMD. Remplacer le tableau `required_cmds` et la boucle par :

```bash
  local -A _cmd_map=(
    [awk]="awk"
    [find]="$FIND_CMD"
    [sort]="$SORT_CMD"
    [head]="$HEAD_CMD"
    [du]="$DU_CMD"
    [date]="date"
    [mktemp]="mktemp"
    [df]="df"
    [tail]="tail"
  )
  local canonical resolved
  for canonical in awk find sort head du date mktemp df tail; do
    resolved="${_cmd_map[$canonical]}"
    if command -v "$resolved" >/dev/null 2>&1; then
      if [[ "$resolved" != "$canonical" ]]; then
        echo "[OK] Commande présente: $resolved (→ $canonical)"
      else
        echo "[OK] Commande présente: $resolved"
      fi
    else
      echo "[KO] Commande manquante: $resolved"
      missing_cmds+=("$resolved")
      rc=1
    fi
  done
```

- [ ] **Step 6 : Vérifier la syntaxe**
```bash
bash -n disk-explorer.sh
```

- [ ] **Step 7 : Lancer les tests existants**
```bash
bats tests/is_integer.bats
bats tests/platform_compat.bats
```
Attendu : tous les tests passent.

- [ ] **Step 8 : Commit**
```bash
git add disk-explorer.sh
git commit -m "feat: update bash version check to 4.4, remove linux-only guard, update self_check_report for macOS"
```

---

### Task 13 : Test de smoke global + vérification `--self-check`

**Files:**
- Modify: `tests/platform_compat.bats`

- [ ] **Step 1 : Ajouter un test de smoke pour les variables CMD**

Ajouter dans `tests/platform_compat.bats` :
```bash
# ── Variables CMD initialisées ────────────────────────────────────

@test "CMD variables: initialisées avec des valeurs non vides" {
    [ -n "$FIND_CMD" ]
    [ -n "$SORT_CMD" ]
    [ -n "$HEAD_CMD" ]
    [ -n "$DU_CMD" ]
    [ -n "$NUMFMT_CMD" ]
}

@test "CMD variables: PLATFORM initialisée (vide avant detect_platform)" {
    # Avant detect_platform(), PLATFORM est ""
    # Après source, les variables globales sont définies
    [ -v PLATFORM ]  # la variable existe (même si vide)
}
```

- [ ] **Step 2 : Vérifier que les tests passent**
```bash
bats tests/platform_compat.bats
```

- [ ] **Step 3 : Test manuel `--self-check` (sur la machine de développement)**
```bash
bash disk-explorer.sh --self-check
```
Attendu sur Linux : toutes les lignes `[OK]`, pas de `[KO]`.
Attendu sur macOS (avec brew bash + coreutils + findutils) : toutes les lignes `[OK]` avec `gfind`, `gsort`, etc.

- [ ] **Step 4 : Commit final**
```bash
git add tests/platform_compat.bats
git commit -m "test: add smoke tests for CMD variable initialization"
```

---

## Récapitulatif des commits attendus

1. `feat: add global platform and command variables`
2. `feat: add bash 4.4+ bootstrap shim for macOS compatibility`
3. `fix: replace [[ -v NO_COLOR ]] with portable ${NO_COLOR+x}`
4. `feat: add detect_platform() with tests`
5. `feat: add resolve_gnu_tools_macos() with brew install prompt`
6. `feat: call detect_platform and resolve_gnu_tools_macos early in main()`
7. `feat: route find/sort/head/du through CMD variables for cross-platform support`
8. `feat: use $NUMFMT_CMD in init_numfmt_support and human_size`
9. `feat: add macOS BSD df -Pk branch in get_df_fields()`
10. `feat: add macOS date -r branch in date_from_epoch()`
11. `fix: test realpath -m support before use (BSD realpath lacks -m)`
12. `feat: update bash version check to 4.4, remove linux-only guard, update self_check_report for macOS`
13. `test: add smoke tests for CMD variable initialization`
