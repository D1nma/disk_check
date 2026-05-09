# Design : TUI plein écran adaptatif

**Date :** 2026-05-09
**Scope :** Remplacement du mode interactif actuel par un TUI plein écran adaptatif, plus refactoring en modules sources + script de build.

---

## Objectif

Transformer le mode interactif de `disk-explorer.sh` en TUI plein écran qui :

- Occupe tout l'écran du terminal (hauteur et largeur)
- Utilise le buffer alternatif (comme `vim` / `less` — l'écran est restauré à la sortie)
- Se redessine en place sans `clear` (repositionnement curseur via `tput`)
- S'adapte dynamiquement au redimensionnement du terminal (`SIGWINCH`)
- Accepte la saisie en mode caractère immédiat (sans Entrée)
- Supporte la navigation par flèches (↑ ↓) avec curseur mis en surbrillance
- Fonctionne sans installation supplémentaire sur Linux, macOS, BSD (tput/ncurses universellement disponible)
- Fonctionne via SSH (le client SSH forward `TERM` et `SIGWINCH` nativement)

---

## Architecture des fichiers

### Sources (développement)

```
src/
  main.sh       # point d'entrée, parsing des arguments, dispatch des modes
  tui.sh        # buffer alternatif, dessin, curseur, SIGWINCH, saisie
  scan.sh       # fonctions de scan disque (du, find)
  display.sh    # modes non-TUI : summary, report, tree
  utils.sh      # utilitaires partagés : human_size, sanitize_for_display, etc.
build.sh        # script de build : concatène src/ → disk-explorer.sh
```

### Distribution

```
disk-explorer.sh   # fichier unique généré par build.sh, auto-suffisant
```

Le fichier distribué est identique à l'actuel du point de vue de l'utilisateur final : `chmod +x disk-explorer.sh && ./disk-explorer.sh`.

### Script de build

`build.sh` concatène les fichiers dans l'ordre :
1. Shebang + shim Bash (depuis `main.sh`)
2. `utils.sh`
3. `scan.sh`
4. `display.sh`
5. `tui.sh`
6. Corps principal de `main.sh` (parsing args, `main()`)

Les marqueurs de section (`# === MODULE: xxx ===`) sont conservés dans le fichier généré pour la lisibilité.

---

## Layout de l'écran (Option A)

```
┌─────────────────────────────────────────────────────────────────┐
│ DISK EXPLORER   /home/user/Projet          partition · taille    │  ← ligne 1 (header)
│ ████████░░░░░░░░░░░░░░ 38%   125,4 Go / 330 Go    ⚠ warning    │  ← ligne 2 (barre + warning)
├─────────────────────────────────────────────────────────────────┤
│  1)    48,2 Go  ████████░░  node_modules/                       │  ← liste (LINES - 5 lignes)
│  2)    21,7 Go  ████░░░░░░  .cargo/                    ◄ curseur│
│  3)    12,1 Go  ██░░░░░░░░  docker/                            │
│  ...                                                            │
│ ↓ 10 autres dossiers…                                           │
├─────────────────────────────────────────────────────────────────┤
│  [↑↓] naviguer  [Entrée] ouvrir  [1-N] accès direct  [0] retour│  ← footer ligne 1
│  [s] tri  [a] taille  [f] fichiers  [r] rapport  [h] aide  [q] │  ← footer ligne 2
└─────────────────────────────────────────────────────────────────┘
```

**Règles de rendu :**

- Chaque ligne est paddée ou tronquée exactement à `COLUMNS` caractères (efface les résidus)
- La barre de progression `█` est proportionnelle à `COLUMNS` (environ 20% de la largeur)
- Les noms de dossiers trop longs sont tronqués avec `…` à `COLUMNS - 30` caractères
- Le nombre de lignes de liste est `LINES - 5` (2 header + 1 séparateur + 2 footer)
- Si `LINES < 8`, affichage dégradé : on supprime les footers et on avertit

---

## Couche TUI (`tui.sh`)

### Initialisation

```bash
tui_enter() {
  tput smcup          # buffer alternatif
  tput civis          # masquer curseur
  stty -echo raw      # saisie immédiate
  trap tui_exit EXIT
  trap tui_draw SIGWINCH
}

tui_exit() {
  stty echo cooked
  tput cnorm          # restaurer curseur
  tput rmcup          # buffer principal
}
```

### Cycle de redessin

```bash
tui_draw() {
  LINES=$(tput lines)
  COLUMNS=$(tput cols)
  tput cup 0 0        # repositionner sans clear
  draw_header
  draw_list
  draw_footer
}
```

### Saisie clavier

```bash
read_key() {
  local key
  IFS= read -r -s -n1 key
  # Séquences d'échappement (flèches, etc.)
  if [[ "$key" == $'\x1b' ]]; then
    local seq
    IFS= read -r -s -t 0.05 -n3 seq 2>/dev/null || true
    key="${key}${seq}"
  fi
  printf '%s' "$key"
}
```

Mapping des touches :

| Séquence | Action |
|---|---|
| `$'\x1b[A'` | ↑ — curseur vers le haut |
| `$'\x1b[B'` | ↓ — curseur vers le bas |
| `$'\n'` ou `$'\r'` | Entrée — ouvrir le dossier sélectionné |
| `[0-9]` | accès direct au dossier N |
| `q` / `Q` | quitter |
| `s` | changer tri |
| `a` | changer mode taille |
| `p` | changer mode analyse |
| `f` | top fichiers |
| `r` | générer rapport |
| `h` / `?` | aide |
| `c` | menu config |
| `e` | exclusions |

### Navigation curseur

```bash
CURSOR=0   # index 0-based dans SUBDIR_PATHS

cursor_up()   { (( CURSOR > 0 )) && (( CURSOR-- )); }
cursor_down() { (( CURSOR < ${#SUBDIR_PATHS[@]} - 1 )) && (( CURSOR++ )); }
cursor_reset() { CURSOR=0; }
```

La ligne `CURSOR` est rendue avec `tput rev` (vidéo inverse) ou couleur de fond.

### Écrans secondaires (aide, config, exclusions, fichiers)

Les écrans secondaires (help, config, fichiers lourds…) s'affichent dans le buffer alternatif déjà actif. Ils redessinent l'écran entier et attendent une touche (`[q]` ou `[Entrée]`) avant de revenir à la boucle principale.

---

## Compatibilité et fallbacks

| Condition | Comportement |
|---|---|
| `tput smcup` non supporté (`TERM=dumb`) | Fallback : mode actuel avec `clear` |
| `LINES < 8` | Affichage dégradé, footer masqué |
| Non-TTY (`! -t 1`) | Déjà géré : mode summary/report automatique |
| SSH avec TTY | Fonctionne nativement (TERM + SIGWINCH forwarded) |

---

## Découpage des modules sources

### `utils.sh`
- `human_size`, `sanitize_for_display`, `date_from_epoch`
- `is_integer`, `is_non_negative_int`, `contains_glob_meta`
- `die`, `install_hint`, `detect_os_id`

### `scan.sh`
- `scan_subdirs_to_file`, `scan_top_files_to_file`
- `refresh_active_exclusions`, `normalize_dir`, `resolve_path_lexical`
- `make_temp_file`, `update_scan_warning`

### `display.sh`
- `print_summary` (mode `--summary`)
- `generate_report_file` (mode `--report`)
- `print_tree` (mode `--tree`)
- `self_check_report`

### `tui.sh`
- `tui_enter`, `tui_exit`, `tui_draw`
- `draw_header`, `draw_list`, `draw_footer`
- `read_key`, `cursor_up`, `cursor_down`, `cursor_reset`
- `show_help_screen`, `show_exclusions_screen`, `show_heavy_files`
- `show_config_screen`, `navigate`

### `main.sh`
- Shim Bash, `set -u -o pipefail`
- Variables globales, constantes
- `usage`, `parse_args`, `init_colors`, `init_numfmt_support`
- `check_runtime_requirements`, `detect_platform`, `init_cmd_vars`
- `main()`

---

## Tests

Les tests existants dans `tests/run_tests.sh` continuent de fonctionner car ils sourcent `disk-explorer.sh` (fichier généré). On ajoute :

- Test smoke : `build.sh` produit un fichier exécutable valide
- Tests unitaires sur les fonctions de `tui.sh` en mode non-TTY (guards `[[ -t 1 ]]`)
- Test `SIGWINCH` : simulation d'un resize (envoi du signal au PID du test)

---

## Ce qui ne change pas

- Modes `--summary`, `--report`, `--tree`, `--self-check` : comportement identique
- Interface CLI (tous les flags) : identique
- Logique de scan, exclusions, tri : identique
- Compatibilité Linux / macOS / BSD : maintenue
