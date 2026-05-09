# TUI plein écran + modules + install — Implementation Plan

> **For agentic workers:** REQUIRED: Use superpowers:subagent-driven-development (if subagents available) or superpowers:executing-plans to implement this plan. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Transformer disk-explorer.sh en TUI plein écran adaptatif (buffer alternatif, LINES/COLUMNS adaptatif, flèches, SIGWINCH), découper les sources en modules `src/`, et fournir un `install.sh` curl-friendly.

**Architecture:** `build.sh` concatène `src/{utils,scan,display,tui,main}.sh` → `disk-explorer.sh` (fichier unique distribuable). La couche TUI repose sur `tput smcup/rmcup`, `stty -echo raw`, un flag `_NEEDS_REDRAW` pour SIGWINCH, et `read_key` avec timeout `-t 0.2`. `SUBDIR_PATHS` stocke des **chemins absolus** (modification de `process_line_display`). `install.sh` permet l'installation via `curl -fsSL url | bash`.

**Tech Stack:** Bash 4.4+, tput/ncurses (standard), stty, GNU coreutils.

**Spec de référence:** `docs/superpowers/specs/2026-05-09-fullscreen-tui-design.md`

**Limitation connue :** les écrans secondaires (aide, config…) ne se redessinent pas automatiquement sur resize — un resize pendant un écran secondaire est pris en compte au retour à la vue principale. Acceptable pour v1.

---

## Chunk 1: Module split + build.sh

### Task 1: Créer src/ et remplir les modules (extraction de fonctions)

**Files:**
- Create: `src/utils.sh`
- Create: `src/scan.sh`
- Create: `src/display.sh`
- Create: `src/tui.sh`
- Create: `src/main.sh`

**Règle de nommage :** aucun fichier `src/` (sauf `main.sh`) ne commence par un shebang `#!/`. Ils contiennent uniquement des définitions de fonctions.

**Allocation des fonctions** (numéros de ligne dans `disk-explorer.sh` actuel) :

`src/utils.sh` :
- `die` (L133-136)
- `is_non_negative_int` (L138-140)
- `is_integer` (L142-144)
- `sanitize_for_display` (L146-153)
- `contains_glob_meta` (L155-157)
- `detect_os_id` (L159-177)
- `install_hint` (L179-226)
- `human_size` (L378-398)
- `path_is_equal_or_within` (L520-530)
- `analysis_label` (L532-534)
- `detect_platform` (L536-538)
- `file_size_label` (L617-619)
- `depth_label` (L621-623)
- `is_heavy_known` (L625-632)
- `date_from_epoch` (L634-640)

`src/scan.sh` :
- `normalize_dir` (L400-403)
- `resolve_path_lexical` (L405-447)
- `init_temp_root` (L449-451)
- `make_temp_file` (L453-456)
- `cleanup` (L458-460)
- `on_interrupt` (L462-466)
- `spinner` (L498-510)
- `wait_for_job` (L512-518)
- `update_scan_warning` (L642-659)
- `run_scan_subdirs_job` (L661-671)
- `run_scan_top_files_job` (L673-683)
- `refresh_active_exclusions` (L857-871)
- `get_df_fields` (L875-907)
- `build_du_cmd` (L909-923)
- `build_du_tree_cmd` (L925-939)
- `build_find_prefix` (L941-964)
- `scan_subdirs_to_file` (L968-1010)
- `scan_top_files_to_file` (L1012-1052)
- `set_current_dir` (L823-830)
- `prepare_current_dir` (L832-834)
- `prepare_exclusions` (L836-855)

`src/display.sh` :
- `self_check_report` (L270-376)
- `print_exclusions_summary` (L1435-1448)
- `generate_report_file` (L1450-1485)
- `print_summary` (L1561-1670)
- `print_tree_view` (L1672-1770)

`src/tui.sh` — fonctions interactives existantes + nouvelles :
- `pause_screen` (L685-688) — sera adaptée (Task 6, Step 7)
- `config_menu` (L1243-1287)
- `add_exclusion_interactive` (L1117-1152)
- `remove_exclusion_interactive` (L1154-1192)
- `set_numeric_interactive` (L1193-1230)
- `set_top_count_interactive` (L1231-1233)
- `set_top_files_interactive` (L1235-1237)
- `set_max_depth_interactive` (L1239-1241)
- `show_help_screen` (L1056-1098)
- `show_exclusions_screen` (L1100-1115)
- `show_heavy_files` (L1391-1433)
- `show_header` (L1291-1326) — sera remplacée par `draw_header` en Chunk 2
- `process_line_display` (L1328-1353) — sera modifiée en Chunk 2 (Task 4, Step 3)
- `show_heavy_subdirs` (L1355-1388)
- `navigate` (L1487-1557) — sera réécrite en Chunk 3

`src/main.sh` :
- Shebang L1 + shim Bash L2-14
- `set -u -o pipefail` (L20)
- Constantes `readonly` (L22-39)
- Variables globales mutables (L41-83) **+ 3 nouvelles :**
  ```bash
  declare -a SUBDIR_DATA=()   # données brutes parallèles à SUBDIR_PATHS
  TUI_CAPABLE=0
  _NEEDS_REDRAW=0
  CURSOR=0
  SCROLL_OFFSET=0
  ```
- Variables couleurs (L86)
- `init_colors` (L471-483)
- `init_runtime_flags` (L485-496)
- `init_numfmt_support` (L228-230)
- `check_runtime_requirements` (L232-268)
- `resolve_gnu_tools_macos` (L540-615)
- `usage` (L90-131)
- `parse_args` (L692-821)
- Traps inline : `trap cleanup EXIT` + `trap on_interrupt INT TERM` (L468-469)
- `main()` (L1774-1834)
- Guard BASH_SOURCE (L1836-1838)

- [ ] **Step 1 : Créer les fichiers modules**

```bash
mkdir -p src
touch src/utils.sh src/scan.sh src/display.sh src/tui.sh src/main.sh
```

- [ ] **Step 2 : Remplir src/utils.sh**

Copier exactement les fonctions listées dans l'ordre de leur numéro de ligne dans `disk-explorer.sh`. Commencer par :

```bash
# === MODULE: utils ===
# Utilitaires purs
```

- [ ] **Step 3 : Remplir src/scan.sh**

```bash
# === MODULE: scan ===
# Scan disque, fichiers temporaires, exclusions
```

- [ ] **Step 4 : Remplir src/display.sh**

```bash
# === MODULE: display ===
# Sorties non-TUI : summary, report, tree, self-check
```

- [ ] **Step 5 : Remplir src/tui.sh**

```bash
# === MODULE: tui ===
# TUI interactif : dessin, saisie, navigation
```

Copier les fonctions interactives listées dans l'ordre de leur numéro de ligne.

- [ ] **Step 6 : Remplir src/main.sh**

Structure exacte :

```
src/main.sh
├── shebang + shim Bash (L1-14)
├── # === MODULE: main ===
├── set -u -o pipefail
├── Constantes readonly
├── Variables globales mutables (inclure les 5 nouvelles : SUBDIR_DATA, TUI_CAPABLE,
│   _NEEDS_REDRAW, CURSOR, SCROLL_OFFSET)
├── Variables couleurs
├── # ================== TRAPS ==================
├── trap cleanup EXIT
├── trap on_interrupt INT TERM
└── Fonctions : init_colors, init_runtime_flags, init_numfmt_support,
    check_runtime_requirements, resolve_gnu_tools_macos, usage, parse_args, main()
    Guard BASH_SOURCE
```

---

### Task 2: Créer build.sh

**Files:**
- Create: `build.sh`

- [ ] **Step 1 : Écrire build.sh**

```bash
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

awk '
  /^[[:space:]]*[a-zA-Z_][a-zA-Z0-9_]*[[:space:]]*\(\)/ { exit }
  { print }
' "$SCRIPT_DIR/src/main.sh" >> "$TMP"

for module in utils scan display tui; do
  printf '\n' >> "$TMP"
  cat "$SCRIPT_DIR/src/${module}.sh" >> "$TMP"
done

printf '\n' >> "$TMP"
awk '
  found || /^[[:space:]]*[a-zA-Z_][a-zA-Z0-9_]*[[:space:]]*\(\)/ { found=1; print }
' "$SCRIPT_DIR/src/main.sh" >> "$TMP"

chmod +x "$TMP"
bash -n "$TMP" || { echo "ERREUR: syntaxe invalide dans le fichier généré" >&2; exit 1; }
mv "$TMP" "$OUT"
echo "Build OK → $OUT"
```

- [ ] **Step 2 : Rendre build.sh exécutable**

```bash
chmod +x build.sh
```

- [ ] **Step 3 : Lancer le build**

```bash
./build.sh
```

Résultat attendu : `Build OK → /…/disk-explorer.sh`

---

### Task 3: Vérifier que le build passe les tests existants

- [ ] **Step 1 : Lancer les tests**

```bash
bash tests/run_tests.sh
```

Résultat attendu : `Summary: X tests, X passed, 0 failed.`

- [ ] **Step 2 : Self-check**

```bash
./disk-explorer.sh --self-check
```

Résultat attendu : `[OK]` partout.

- [ ] **Step 3 : Summary sur /tmp**

```bash
./disk-explorer.sh --summary /tmp
```

Résultat attendu : rapport texte, exit 0.

- [ ] **Step 4 : Commit**

```bash
git add src/ build.sh
git commit -m "refactor: split disk-explorer.sh into src/ modules + build.sh"
```

---

## Chunk 2: TUI core — capability, enter/exit, SIGWINCH, read_key, draw_*

### Task 4: Modifier process_line_display + ajouter fonctions TUI dans src/tui.sh

**Files:**
- Modify: `src/tui.sh`

**Décision importante :** `SUBDIR_PATHS` stockera désormais des **chemins absolus** (et `SUBDIR_DATA` la valeur brute parallèle). Cela simplifie `draw_list` et `navigate()`. On modifie `process_line_display` pour stocker `full_path` au lieu de `rel_path`, et pour alimenter `SUBDIR_DATA`.

- [ ] **Step 1 : Écrire les tests TUI dans tests/run_tests.sh**

Ajouter à la fin du fichier de tests (après les assertions existantes, avant le résumé final) :

```bash
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
```

- [ ] **Step 2 : Lancer les tests — vérifier l'échec**

```bash
./build.sh && bash tests/run_tests.sh 2>/dev/null
```

Résultat attendu : `[FAIL]` sur les 3 tests TUI (fonctions non définies).

- [ ] **Step 3 : Modifier process_line_display pour stocker full_path et SUBDIR_DATA**

Dans `src/tui.sh`, dans la fonction `process_line_display`, remplacer la ligne qui fait `SUBDIR_PATHS+=("$rel_path")` par :

```bash
SUBDIR_PATHS+=("$full_path")
SUBDIR_DATA+=("$raw_data")
```

Et au début de `show_heavy_subdirs`, après la ligne `SUBDIR_PATHS=()`, ajouter :

```bash
SUBDIR_DATA=()
```

- [ ] **Step 4 : Implémenter tui_check_capability**

Ajouter dans `src/tui.sh` après les fonctions interactives existantes :

```bash
# ======================================================================
# === TUI CORE ===
# ======================================================================

# Teste si le terminal supporte smcup sans aucun effet visible (stdout→/dev/null).
tui_check_capability() {
  if tput smcup >/dev/null 2>&1 && tput rmcup >/dev/null 2>&1; then
    TUI_CAPABLE=1
  else
    TUI_CAPABLE=0
  fi
}
```

- [ ] **Step 5 : Implémenter tui_enter / tui_exit**

```bash
tui_exit() {
  stty echo cooked 2>/dev/null || true
  tput cnorm 2>/dev/null || true
  tput rmcup 2>/dev/null || true
}

tui_enter() {
  tput smcup
  tput civis
  stty -echo raw
  trap tui_exit EXIT
  trap '_NEEDS_REDRAW=1' SIGWINCH
}
```

- [ ] **Step 6 : Implémenter read_key**

```bash
# Lit une touche ou séquence d'échappement.
# Retourne "" sur timeout (0.2 s) pour permettre la vérification de _NEEDS_REDRAW.
read_key() {
  local key seq
  IFS= read -r -s -t 0.2 -n1 key 2>/dev/null || true
  if [[ "$key" == $'\x1b' ]]; then
    IFS= read -r -s -t 0.1 -n5 seq 2>/dev/null || true
    key="${key}${seq}"
  fi
  printf '%s' "$key"
}
```

- [ ] **Step 7 : Lancer les tests — vérifier que les 3 tests TUI passent**

```bash
./build.sh && bash tests/run_tests.sh
```

Résultat attendu : 0 failed.

- [ ] **Step 8 : Implémenter les fonctions de rendu**

Ajouter dans `src/tui.sh` :

```bash
# ── Utilitaires de rendu ───────────────────────────────────────────────────

# Tronque/padde $1 à exactement $2 caractères visibles (pas de sequences ANSI).
_tui_pad() {
  local str="$1"
  local width="$2"
  local vis="${str:0:$width}"
  printf "%-${width}s" "$vis"
}

# ── En-tête (2 lignes + 1 séparateur) ────────────────────────────────────

draw_header() {
  local df_fields size used avail use_p mounted fields
  if df_fields="$(get_df_fields 2>/dev/null)"; then
    IFS=$'\t' read -r fields mounted <<< "$df_fields"
    read -r size used avail use_p <<< "$fields"
  else
    size=0; used=0; avail=0; use_p="?%"; mounted="?"
  fi

  # Ligne 1 : titre + chemin + mode
  local line1_plain="DISK EXPLORER  ${CURRENT_DIR}  $(analysis_label) · $SORT_MODE"
  printf '%s\n' "$(_tui_pad "$line1_plain" "$COLUMNS")"

  # Ligne 2 : barre de progression
  local use_int="${use_p//[^0-9]/}"
  local bar_width=$(( COLUMNS * 20 / 100 ))
  (( bar_width < 5 )) && bar_width=5
  local bar_fill=$(( bar_width * ${use_int:-0} / 100 ))
  (( bar_fill > bar_width )) && bar_fill=$bar_width
  local bar_empty=$(( bar_width - bar_fill ))
  local bar=""
  local i
  for (( i=0; i<bar_fill;  i++ )); do bar+="█"; done
  for (( i=0; i<bar_empty; i++ )); do bar+="░"; done
  local avail_h used_h total_h
  [[ "$avail" =~ ^[0-9]+$ ]] && avail_h="$(human_size "$avail")" || avail_h="?"
  [[ "$used"  =~ ^[0-9]+$ ]] && used_h="$(human_size "$used")"   || used_h="?"
  [[ "$size"  =~ ^[0-9]+$ ]] && total_h="$(human_size "$size")"  || total_h="?"
  local warn_suffix=""
  [[ -n "$LAST_WARNING" ]] && warn_suffix="  ⚠ $(sanitize_for_display "$LAST_WARNING")"
  local line2_plain="${bar} ${use_p}  ${used_h} / ${total_h}${warn_suffix}"
  printf '%s\n' "$(_tui_pad "$line2_plain" "$COLUMNS")"

  # Séparateur haut
  local sep=""
  for (( i=0; i<COLUMNS; i++ )); do sep+="─"; done
  printf '%s\n' "${sep:0:$COLUMNS}"
}

# ── Zone liste avec viewport et curseur ──────────────────────────────────

draw_list() {
  local visible=$(( LINES - 6 ))
  (( visible < 1 )) && visible=1
  local total=${#SUBDIR_PATHS[@]}
  local i row=0

  for (( i=SCROLL_OFFSET; i<total && row<visible; i++, row++ )); do
    local full_path="${SUBDIR_PATHS[$i]}"
    # Chemin relatif pour l'affichage
    local rel_path="${full_path#"${CURRENT_DIR}/"}"
    [[ "$rel_path" == "$full_path" ]] && rel_path="$(basename -- "$full_path")"
    local safe_name
    safe_name="$(sanitize_for_display "$rel_path")"

    local raw_data="${SUBDIR_DATA[$i]:-0}"
    local metric
    if [[ "$SORT_MODE" == "mtime" ]]; then
      metric="$(date_from_epoch "$raw_data")"
    else
      metric="$(human_size "$raw_data")"
    fi

    local name_max=$(( COLUMNS - 30 ))
    (( name_max < 5 )) && name_max=5
    if (( ${#safe_name} > name_max )); then
      safe_name="${safe_name:0:$((name_max-1))}…"
    fi

    local line_text
    printf -v line_text '  %2d)  %12s   %s' "$((i+1))" "$metric" "$safe_name"

    if (( i == CURSOR )); then
      printf '%s%s%s\n' "$(tput rev 2>/dev/null || true)" \
        "$(_tui_pad "$line_text" "$COLUMNS")" \
        "$(tput sgr0 2>/dev/null || true)"
    else
      printf '%s\n' "$(_tui_pad "$line_text" "$COLUMNS")"
    fi
  done

  # Remplir les lignes vides restantes
  while (( row < visible )); do
    printf '%s\n' "$(_tui_pad "" "$COLUMNS")"
    (( row++ ))
  done

  # Indicateur de scroll si des entrées sont hors champ
  local remaining=$(( total - SCROLL_OFFSET - visible ))
  if (( remaining > 0 )); then
    # Écraser la dernière ligne de la zone liste
    tput cup $(( 2 + 1 + visible - 1 )) 0 2>/dev/null || true
    local hint="  ↓ $remaining autre(s)…"
    printf '%s\n' "$(_tui_pad "$hint" "$COLUMNS")"
  fi
}

# ── Footer (séparateur + 2 lignes de raccourcis) ─────────────────────────

draw_footer() {
  local sep="" i
  for (( i=0; i<COLUMNS; i++ )); do sep+="─"; done
  printf '%s\n' "${sep:0:$COLUMNS}"
  local n="${#SUBDIR_PATHS[@]}"
  local f1="  [↑↓] naviguer  [Entrée] ouvrir  [1-$n] accès direct  [0] retour"
  local f2="  [s] tri  [a] taille  [f] fichiers  [r] rapport  [h] aide  [c] config  [q] quitter"
  printf '%s\n' "$(_tui_pad "$f1" "$COLUMNS")"
  printf '%s\n' "$(_tui_pad "$f2" "$COLUMNS")"
}

# ── Redessin complet ──────────────────────────────────────────────────────

tui_draw() {
  LINES=$(tput lines 2>/dev/null || echo 24)
  COLUMNS=$(tput cols  2>/dev/null || echo 80)
  tput cup 0 0 2>/dev/null || true
  tput ed      2>/dev/null || true   # efface jusqu'à fin d'écran (gère rétrécissement)

  if (( LINES < 9 )); then
    printf 'Terminal trop petit (%d lignes). Agrandissez la fenêtre.\n' "$LINES"
    _NEEDS_REDRAW=0
    return
  fi

  draw_header
  draw_list
  tput cup $(( LINES - 3 )) 0 2>/dev/null || true
  draw_footer

  _NEEDS_REDRAW=0   # remis à 0 APRÈS le dessin complet
}
```

- [ ] **Step 9 : Rebuilder et vérifier syntaxe**

```bash
./build.sh && bash -n disk-explorer.sh && echo "Syntaxe OK"
```

- [ ] **Step 10 : Commit**

```bash
git add src/tui.sh src/main.sh tests/run_tests.sh
git commit -m "feat: TUI core — capability, enter/exit, SIGWINCH, read_key, draw_*"
```

---

## Chunk 3: TUI navigation — curseur, viewport, navigate() rewrite, écrans secondaires

### Task 5: Curseur et viewport

**Files:**
- Modify: `src/tui.sh`
- Modify: `tests/run_tests.sh`

- [ ] **Step 1 : Écrire les tests de curseur**

Ajouter à la fin de `tests/run_tests.sh` (avant le bloc `echo "Summary"`) :

```bash
echo -e "\nRunning tests for cursor navigation..."

# Helper portable : crée N chemins sans seq -f (incompatible macOS sans GNU seq)
_make_paths() {
  local n="$1" i; for (( i=1; i<=n; i++ )); do printf '/dir%d\n' "$i"; done
}

assert_true '
  CURSOR=0; SCROLL_OFFSET=0
  mapfile -t SUBDIR_PATHS < <(_make_paths 3); LINES=10
  cursor_down; [[ "$CURSOR" -eq 1 ]]
' "cursor_down: incrémente CURSOR"

assert_true '
  CURSOR=0; SCROLL_OFFSET=0
  mapfile -t SUBDIR_PATHS < <(_make_paths 3); LINES=10
  cursor_up; [[ "$CURSOR" -eq 0 ]]
' "cursor_up: ne descend pas sous 0"

assert_true '
  CURSOR=2; SCROLL_OFFSET=0
  mapfile -t SUBDIR_PATHS < <(_make_paths 3); LINES=10
  cursor_down; [[ "$CURSOR" -eq 2 ]]
' "cursor_down: ne dépasse pas le dernier élément"

assert_true '
  CURSOR=0; SCROLL_OFFSET=0
  mapfile -t SUBDIR_PATHS < <(_make_paths 20); LINES=10
  # visible = LINES - 6 = 4 ; descendre jusqu'"'"'à ce que SCROLL_OFFSET bouge
  for _i in $(seq 4); do cursor_down; done
  [[ "$SCROLL_OFFSET" -eq 1 ]]
' "cursor_down: scroll quand curseur dépasse la zone visible"

assert_true '
  CURSOR=3; SCROLL_OFFSET=2
  mapfile -t SUBDIR_PATHS < <(_make_paths 20); LINES=10
  cursor_up; cursor_up; cursor_up
  [[ "$SCROLL_OFFSET" -eq 0 ]]
' "cursor_up: déscroll quand curseur remonte au-dessus du viewport"
```

- [ ] **Step 2 : Lancer les tests — vérifier l'échec**

```bash
./build.sh && bash tests/run_tests.sh 2>/dev/null
```

Résultat attendu : `[FAIL]` sur les 5 tests curseur.

- [ ] **Step 3 : Implémenter cursor_up / cursor_down / cursor_reset**

Ajouter dans `src/tui.sh` :

```bash
# ── Curseur ────────────────────────────────────────────────────────────────

cursor_up() {
  (( CURSOR > 0 )) && (( CURSOR-- )) || true
  (( CURSOR < SCROLL_OFFSET )) && (( SCROLL_OFFSET-- )) || true
}

cursor_down() {
  local visible=$(( LINES - 6 ))
  (( visible < 1 )) && visible=1
  local last=$(( ${#SUBDIR_PATHS[@]} - 1 ))
  (( last < 0 )) && return
  (( CURSOR < last )) && (( CURSOR++ )) || true
  (( CURSOR >= SCROLL_OFFSET + visible )) && (( SCROLL_OFFSET++ )) || true
}

cursor_reset() {
  CURSOR=0
  SCROLL_OFFSET=0
}
```

- [ ] **Step 4 : Lancer les tests — vérifier qu'ils passent**

```bash
./build.sh && bash tests/run_tests.sh
```

Résultat attendu : 0 failed.

- [ ] **Step 5 : Commit**

```bash
git add src/tui.sh tests/run_tests.sh
git commit -m "feat: cursor navigation with scroll viewport"
```

---

### Task 6: Réécriture de navigate() et protocole des écrans secondaires

**Files:**
- Modify: `src/tui.sh`

- [ ] **Step 1 : Renommer navigate() en navigate_legacy()**

Dans `src/tui.sh`, renommer la fonction `navigate()` existante en `navigate_legacy()`. Ne pas modifier son corps — elle conserve l'ancien comportement `clear`-based pour le fallback.

Supprimer les appels à `[[ -t 1 ]] && clear` dans `show_help_screen`, `show_exclusions_screen`, `show_heavy_files`, `remove_exclusion_interactive`, et `config_menu` (car on est déjà dans le buffer alternatif en mode TUI). Remplacer par un test conditionnel pour que la suppression n'affecte pas le mode legacy :

```bash
# Partout où il y a [[ -t 1 ]] && clear, remplacer par :
[[ "$TUI_CAPABLE" -eq 0 && -t 1 ]] && clear
```

Laisser le `[[ -t 1 ]] && clear` présent dans `navigate_legacy()` (branche `q`) tel quel — il est correct pour le mode non-TUI.

- [ ] **Step 2 : Corriger navigate_legacy() pour les chemins absolus**

Dans `navigate_legacy()` (l'ancienne `navigate()` renommée), `SUBDIR_PATHS` stocke maintenant des chemins absolus. La construction `candidate="${CURRENT_DIR%/}/${target}"` produirait un double-chemin invalide. Remplacer le bloc de navigation directe par numéro :

```bash
# Ancienne version (à remplacer) :
target="${SUBDIR_PATHS[$idx]}"
prev_dir="$CURRENT_DIR"
candidate="${CURRENT_DIR%/}/${target}"
set_current_dir "$candidate" || { … }

# Nouvelle version (chemin absolu direct) :
target="${SUBDIR_PATHS[$idx]}"
prev_dir="$CURRENT_DIR"
set_current_dir "$target" || {
  LAST_WARNING="navigation impossible vers le dossier demandé"
  CURRENT_DIR="$prev_dir"
  continue
}
```

De même pour le bloc `Entrée` (`$'\n'` / `$'\r'`) dans `navigate_legacy()` si présent.

- [ ] **Step 3 : Adapter pause_screen pour le mode TUI**

Remplacer `pause_screen` dans `src/tui.sh` par :

```bash
pause_screen() {
  echo
  if [[ "$TUI_CAPABLE" -eq 1 ]]; then
    printf '  Appuyez sur une touche…'
    stty echo cooked 2>/dev/null || true
    read -r -s -n1 2>/dev/null || true
    echo
    stty -echo raw 2>/dev/null || true
  else
    read -r -p "Appuyez sur Entrée…" || true
  fi
}
```

- [ ] **Step 4 : Implémenter _tui_reload_subdirs**

```bash
# Recharge SUBDIR_PATHS/SUBDIR_DATA en relançant le scan.
# Si le scan échoue, SUBDIR_PATHS reste vide et LAST_WARNING est positionné.
_tui_reload_subdirs() {
  LAST_WARNING=""
  SUBDIR_PATHS=()
  SUBDIR_DATA=()
  show_heavy_subdirs >/dev/null 2>/dev/null || {
    LAST_WARNING="erreur lors du scan des sous-dossiers"
  }
}
```

- [ ] **Step 5 : Implémenter _tui_secondary_screen**

```bash
# Lance un écran secondaire dans le buffer alternatif actif.
# Restaure cooked avant l'appel (pour les read -r -p dans config/exclusions).
# Force _NEEDS_REDRAW=1 au retour pour que la vue principale se redessine.
# Note : les écrans secondaires ne se redessinent pas sur SIGWINCH — le resize
# est pris en compte au retour dans la boucle principale (limitation v1).
_tui_secondary_screen() {
  local fn="$1"
  shift
  stty echo cooked 2>/dev/null || true
  tput cup 0 0 2>/dev/null || true
  tput ed      2>/dev/null || true
  "$fn" "$@"
  stty -echo raw 2>/dev/null || true
  _NEEDS_REDRAW=1
}
```

- [ ] **Step 6 : Implémenter _tui_show_report_result**

```bash
_tui_show_report_result() {
  echo
  if [[ -n "$LAST_WARNING" ]]; then
    printf '%s\n' "$LAST_WARNING"
  else
    echo "Rapport généré."
  fi
  pause_screen
}
```

- [ ] **Step 7 : Implémenter la nouvelle navigate()**

Ajouter dans `src/tui.sh` :

```bash
# ── Boucle principale TUI ─────────────────────────────────────────────────

navigate() {
  tui_check_capability
  if [[ "$TUI_CAPABLE" -eq 0 ]]; then
    navigate_legacy
    return
  fi

  _tui_reload_subdirs
  tui_enter
  _NEEDS_REDRAW=1

  while true; do
    [[ "$_NEEDS_REDRAW" -eq 1 ]] && tui_draw

    local key
    key=$(read_key)

    case "$key" in
      # Flèches
      $'\x1b[A') cursor_up  ; _NEEDS_REDRAW=1 ;;
      $'\x1b[B') cursor_down; _NEEDS_REDRAW=1 ;;

      # Entrée : ouvrir le dossier sous le curseur
      $'\n'|$'\r')
        if (( ${#SUBDIR_PATHS[@]} > 0 && CURSOR < ${#SUBDIR_PATHS[@]} )); then
          local target="${SUBDIR_PATHS[$CURSOR]}"
          local prev_dir="$CURRENT_DIR"
          if set_current_dir "$target"; then
            _tui_reload_subdirs
            cursor_reset
          else
            LAST_WARNING="navigation impossible vers ce dossier"
            CURRENT_DIR="$prev_dir"
          fi
          _NEEDS_REDRAW=1
        fi
        ;;

      # Retour arrière
      0)
        if [[ "$CURRENT_DIR" != "/" ]]; then
          local prev_dir="$CURRENT_DIR"
          if set_current_dir "$(dirname -- "$CURRENT_DIR")"; then
            _tui_reload_subdirs
            cursor_reset
          else
            LAST_WARNING="retour arrière impossible"
            CURRENT_DIR="$prev_dir"
          fi
          _NEEDS_REDRAW=1
        fi
        ;;

      # Accès direct par numéro (1-9, limitation connue : max item 9 par touche)
      [1-9])
        local idx=$(( key - 1 ))
        if [[ -n "${SUBDIR_PATHS[$idx]-}" ]]; then
          local target="${SUBDIR_PATHS[$idx]}"
          local prev_dir="$CURRENT_DIR"
          if set_current_dir "$target"; then
            _tui_reload_subdirs
            cursor_reset
          else
            LAST_WARNING="navigation impossible"
            CURRENT_DIR="$prev_dir"
          fi
        else
          LAST_WARNING="sélection hors plage"
        fi
        _NEEDS_REDRAW=1
        ;;

      s|S)
        [[ "$SORT_MODE" == "size" ]] && SORT_MODE="mtime" || SORT_MODE="size"
        _tui_reload_subdirs; cursor_reset; _NEEDS_REDRAW=1
        ;;
      a|A)
        [[ "$FILE_SIZE_MODE" == "real" ]] && FILE_SIZE_MODE="apparent" || FILE_SIZE_MODE="real"
        _NEEDS_REDRAW=1
        ;;
      p|P)
        [[ "$ANALYSIS_MODE" == "partition" ]] && ANALYSIS_MODE="global" || ANALYSIS_MODE="partition"
        _tui_reload_subdirs; cursor_reset; _NEEDS_REDRAW=1
        ;;
      f|F)
        _tui_secondary_screen show_heavy_files
        ;;
      r|R)
        generate_report_file || true
        _tui_secondary_screen _tui_show_report_result
        ;;
      h|H|'?')
        _tui_secondary_screen show_help_screen
        ;;
      c|C)
        _tui_secondary_screen config_menu
        _tui_reload_subdirs; cursor_reset
        ;;
      e|E)
        _tui_secondary_screen show_exclusions_screen
        ;;
      q|Q)
        tui_exit
        exit 0
        ;;
      '')
        # Timeout read_key — pas de touche, vérifier _NEEDS_REDRAW au prochain tour
        ;;
    esac
  done
}
```

- [ ] **Step 8 : Rebuilder et vérifier syntaxe**

```bash
./build.sh && bash -n disk-explorer.sh && echo "Syntaxe OK"
```

- [ ] **Step 9 : Lancer les tests**

```bash
bash tests/run_tests.sh
```

Résultat attendu : 0 failed.

- [ ] **Step 10 : Test manuel avec un vrai TTY**

```bash
./disk-explorer.sh /tmp
```

Vérifier :
- Buffer alternatif actif (écran précédent restauré à la sortie avec `q`)
- Flèches ↑↓ déplacent le curseur (surbrillance)
- Entrée ouvre le dossier sous le curseur
- `0` remonte d'un niveau
- `s` change le tri (liste rafraîchie)
- `h` affiche l'aide, retour sur touche
- Resize de la fenêtre → redessin adapté au tour suivant
- `q` quitte proprement

- [ ] **Step 11 : Commit**

```bash
git add src/tui.sh
git commit -m "feat: TUI navigate() with arrow keys, cursor, secondary screens, legacy fallback"
```

---

## Chunk 4: install.sh + tests complémentaires

### Task 7: install.sh curl-friendly

**Files:**
- Create: `install.sh`

`install.sh` est conçu pour `curl -fsSL https://example.com/install.sh | bash`. Il télécharge `disk-explorer.sh` depuis une URL configurable et l'installe dans `~/.local/bin`.

- [ ] **Step 1 : Créer install.sh**

```bash
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

main() {
  check_bash
  check_downloader
  mkdir -p "$INSTALL_DIR"

  local tmp
  tmp="$(mktemp /tmp/disk-explorer.install.XXXXXX)"
  trap 'rm -f "$tmp"' EXIT

  printf 'Téléchargement depuis %s…\n' "$INSTALL_URL"
  download_file "$INSTALL_URL" "$tmp"

  bash -n "$tmp" || {
    printf 'Erreur: le fichier téléchargé est invalide.\n' >&2; exit 1
  }

  chmod +x "$tmp"
  mv "$tmp" "${INSTALL_DIR}/${INSTALL_NAME}"
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
```

- [ ] **Step 2 : Rendre install.sh exécutable**

```bash
chmod +x install.sh
```

- [ ] **Step 3 : Vérifier la syntaxe**

```bash
bash -n install.sh && echo "Syntaxe OK"
```

- [ ] **Step 4 : Test local (sans réseau)**

```bash
DISK_EXPLORER_URL="file://$(pwd)/disk-explorer.sh" bash install.sh
```

Résultat attendu :
```
Téléchargement depuis file://…/disk-explorer.sh…
Installé : /home/user/.local/bin/disk-explorer
```

- [ ] **Step 5 : Vérifier que l'installé fonctionne**

```bash
~/.local/bin/disk-explorer --self-check
```

Résultat attendu : `[OK]` partout.

- [ ] **Step 6 : Commit**

```bash
git add install.sh
git commit -m "feat: add install.sh for curl -fsSL url | bash installation"
```

---

### Task 8: Tests complémentaires

**Files:**
- Modify: `tests/run_tests.sh`

- [ ] **Step 1 : Ajouter les tests smoke du build**

Dans `tests/run_tests.sh`, après la définition de `assert_false` (après le bloc `assert_false() { … }`, qui termine vers la ligne 31) et avant le premier `echo "Running tests for is_integer..."`, ajouter :

```bash
echo "Running build smoke tests..."

assert_true '
  (cd "$(dirname "$0")/.." && ./build.sh >/dev/null 2>&1 && bash -n disk-explorer.sh)
' "build.sh: produit un fichier syntaxiquement valide"

assert_true '
  "$(dirname "$0")/../disk-explorer.sh" --self-check >/dev/null 2>&1
' "disk-explorer.sh --self-check: exit 0"

assert_true '
  "$(dirname "$0")/../disk-explorer.sh" --summary /tmp >/dev/null 2>&1
' "disk-explorer.sh --summary /tmp: exit 0"

assert_true '
  echo "" | "$(dirname "$0")/../disk-explorer.sh" /tmp >/dev/null 2>&1
' "disk-explorer.sh: bascule en summary quand stdin n'"'"'est pas un TTY"
```

- [ ] **Step 2 : Ajouter un test draw_list**

Ajouter à la fin de `tests/run_tests.sh` (avant le résumé) :

```bash
echo -e "\nRunning tests for draw_list..."

assert_true '
  LINES=24; COLUMNS=80
  CURRENT_DIR="/tmp"
  SUBDIR_PATHS=("/tmp/a" "/tmp/b" "/tmp/c")
  SUBDIR_DATA=("1024" "2048" "512")
  SORT_MODE="size"
  CURSOR=0; SCROLL_OFFSET=0
  output="$(draw_list 2>/dev/null)"
  visible=$(( LINES - 6 ))
  line_count=$(printf "%s" "$output" | wc -l)
  (( line_count == visible ))
' "draw_list: produit exactement LINES-6 lignes (18 pour LINES=24)"
```

- [ ] **Step 3 : Lancer tous les tests**

```bash
./build.sh && bash tests/run_tests.sh
```

Résultat attendu : 0 failed.

- [ ] **Step 4 : Commit**

```bash
git add tests/run_tests.sh
git commit -m "test: build smoke tests + draw_list non-TTY test"
```

---

### Task 9: Vérification finale

- [ ] **Step 1 : Lancer les tests complets**

```bash
bash tests/run_tests.sh
```

Résultat attendu : 0 failed.

- [ ] **Step 2 : Tester les modes non-interactifs**

```bash
./disk-explorer.sh --summary /tmp
./disk-explorer.sh --tree /tmp
./disk-explorer.sh --self-check
```

- [ ] **Step 3 : Vérifier le comportement curl|bash**

```bash
echo "" | ./disk-explorer.sh /tmp
```

Résultat attendu : summary texte affiché, exit 0 (pas de TUI car stdin n'est pas un TTY).

- [ ] **Step 4 : Vérifier install.sh**

```bash
bash -n install.sh && echo "Syntaxe OK"
```

- [ ] **Step 5 : Commit de clôture si des ajustements ont été nécessaires**

```bash
git status
# Si des fichiers ont été modifiés :
git add -p
git commit -m "fix: post-integration cleanup"
```
