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
1. Shebang + shim Bash (depuis `main.sh`, lignes 1-14)
2. `utils.sh` (sans shebang : les fichiers `src/` autres que `main.sh` n'incluent pas de ligne `#!/`)
3. `scan.sh`
4. `display.sh`
5. `tui.sh`
6. Corps principal de `main.sh` (parsing args, `main()`, sans le shebang/shim déjà émis)

Les marqueurs de section (`# === MODULE: xxx ===`) sont conservés dans le fichier généré pour la lisibilité. Les fichiers `src/*.sh` (hors `main.sh`) ne commencent **pas** par un shebang afin que la concaténation ne produise pas de shebang parasite au milieu du fichier généré.

---

## Layout de l'écran

```
┌─────────────────────────────────────────────────────────────────┐
│ DISK EXPLORER   /home/user/Projet          partition · taille    │  ← ligne 1 (header)
│ ████████░░░░░░░░░░░░░░ 38%   125,4 Go / 330 Go    ⚠ warning    │  ← ligne 2 (barre + warning)
├─────────────────────────────────────────────────────────────────┤  ← séparateur haut
│  1)    48,2 Go  ████████░░  node_modules/                       │
│  2)    21,7 Go  ████░░░░░░  .cargo/                    ◄ curseur│  ← liste (LINES - 6 lignes)
│  3)    12,1 Go  ██░░░░░░░░  docker/                            │
│  ...                                                            │
│ ↓ 10 autres dossiers…                                           │
├─────────────────────────────────────────────────────────────────┤  ← séparateur bas
│  [↑↓] naviguer  [Entrée] ouvrir  [1-N] accès direct  [0] retour│  ← footer ligne 1
│  [s] tri  [a] taille  [f] fichiers  [r] rapport  [h] aide  [q] │  ← footer ligne 2
└─────────────────────────────────────────────────────────────────┘
```

**Décompte des lignes fixes :** 2 header + 1 séparateur haut + 1 séparateur bas + 2 footer = **6 lignes fixes**.
Le nombre de lignes disponibles pour la liste est donc `LINES - 6`.

**Règles de rendu :**

- Chaque ligne est paddée ou tronquée exactement à `COLUMNS` caractères (efface les résidus)
- La barre de progression `█` est proportionnelle à `COLUMNS` (environ 20% de la largeur)
- Les noms de dossiers trop longs sont tronqués avec `…` à `COLUMNS - 30` caractères
- Si `LINES < 9`, affichage dégradé : footer masqué, avertissement affiché

---

## Couche TUI (`tui.sh`)

### Globals déclarés dans `tui.sh`

```bash
TUI_CAPABLE=0      # 1 si le terminal supporte smcup/rmcup
_NEEDS_REDRAW=0    # flag positionné par le handler SIGWINCH
```

### Détection de capacité (sans effet de bord visible)

La détection redirige stdout vers `/dev/null` : les séquences d'échappement ne parviennent jamais au terminal, donc aucun flash visible.

```bash
tui_check_capability() {
  # Redirige stdout pour éviter tout flicker : les séquences smcup/rmcup
  # partent dans /dev/null et le terminal ne les voit pas.
  if tput smcup >/dev/null 2>&1 && tput rmcup >/dev/null 2>&1; then
    TUI_CAPABLE=1
  fi
}
```

`navigate()` appelle `tui_check_capability` au démarrage. Si `TUI_CAPABLE=0`, il bascule sur l'ancien code (boucle avec `clear`).

### Initialisation

```bash
tui_enter() {
  tput smcup          # buffer alternatif
  tput civis          # masquer curseur
  stty -echo raw      # saisie immédiate
  trap tui_exit EXIT
  trap '_NEEDS_REDRAW=1' SIGWINCH   # flag sûr, pas d'appel direct à tui_draw
}

tui_exit() {
  stty echo cooked
  tput cnorm          # restaurer curseur
  tput rmcup          # buffer principal
}
```

### Cycle de redessin

Le handler `SIGWINCH` positionne uniquement un flag. La boucle principale vérifie ce flag **avant chaque `read_key`** — aucun redessin ré-entrant depuis un handler de signal.

`_NEEDS_REDRAW` est remis à `0` **à la fin** du redessin (pas au début) : si un `SIGWINCH` arrive pendant le dessin, le flag reste à `1` et déclenche un second redessin au prochain tour de boucle.

```bash
tui_draw() {
  LINES=$(tput lines)
  COLUMNS=$(tput cols)
  tput cup 0 0        # repositionner le curseur en haut à gauche
  tput ed             # effacer jusqu'à la fin de l'écran (gère le rétrécissement)
  draw_header
  draw_list
  draw_footer
  _NEEDS_REDRAW=0     # remis à 0 APRÈS le dessin complet
}

# Dans la boucle navigate() :
while true; do
  [[ "$_NEEDS_REDRAW" -eq 1 ]] && tui_draw
  key=$(read_key)
  # ... traitement de key ...
done
```

### Saisie clavier

Le premier `read` utilise un timeout court (`-t 0.2`) pour permettre à la boucle de vérifier `_NEEDS_REDRAW` même sans interaction clavier. Si le timeout expire sans touche, `read_key` retourne une chaîne vide et la boucle recommence.

```bash
read_key() {
  local key seq
  # Timeout 0.2 s : permet de traiter _NEEDS_REDRAW après un resize
  # sans attendre une touche. Retourne "" si timeout.
  IFS= read -r -s -t 0.2 -n1 key 2>/dev/null || true
  if [[ "$key" == $'\x1b' ]]; then
    # Timeout 0.1 s, jusqu'à 5 octets pour couvrir les séquences longues
    # (ex: $'\x1b[1;2A' = Shift+Haut = 6 octets).
    # Tradeoff documenté : sur SSH très haut-latence (> 100 ms RTT), une flèche
    # peut être fragmentée ; la séquence incomplète est traitée comme no-op silencieux.
    IFS= read -r -s -t 0.1 -n5 seq 2>/dev/null || true
    key="${key}${seq}"
  fi
  printf '%s' "$key"
}
```

Les séquences non reconnues (ESC partiel, séquences exotiques) sont traitées comme un **no-op silencieux**.

Mapping des touches :

| Séquence | Action |
|---|---|
| `$'\x1b[A'` | ↑ — curseur vers le haut |
| `$'\x1b[B'` | ↓ — curseur vers le bas |
| `$'\n'` ou `$'\r'` | Entrée — ouvrir le dossier sous le curseur |
| `""` (timeout) | no-op — retour au début de boucle |
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

### Navigation curseur et viewport

```bash
CURSOR=0        # index 0-based dans SUBDIR_PATHS (dossier sélectionné)
SCROLL_OFFSET=0 # première entrée visible dans la liste

cursor_up() {
  (( CURSOR > 0 )) && (( CURSOR-- ))
  (( CURSOR < SCROLL_OFFSET )) && (( SCROLL_OFFSET-- ))
}

cursor_down() {
  local visible=$(( LINES - 6 ))
  (( CURSOR < ${#SUBDIR_PATHS[@]} - 1 )) && (( CURSOR++ ))
  (( CURSOR >= SCROLL_OFFSET + visible )) && (( SCROLL_OFFSET++ ))
}

cursor_reset() { CURSOR=0; SCROLL_OFFSET=0; }
```

`draw_list` affiche les entrées `[SCROLL_OFFSET .. SCROLL_OFFSET + visible - 1]`. La ligne `CURSOR` est rendue avec `tput rev` (vidéo inverse). Si la liste dépasse la zone visible, un indicateur `↓ N autres…` est affiché sur la dernière ligne de la zone.

### Sous-menus nécessitant une saisie texte

Les sous-menus `add_exclusion_interactive`, `set_numeric_interactive`, `remove_exclusion_interactive` utilisent `read -r -p` qui requiert le mode cooked. Ces fonctions restaurent temporairement le mode normal :

```bash
stty echo cooked      # avant read -r -p
# ... read -r -p "..." input ...
stty -echo raw        # après, pour revenir en mode TUI
```

### Écrans secondaires (aide, config, exclusions, fichiers)

Les écrans secondaires s'affichent dans le buffer alternatif déjà actif. Chaque écran secondaire a sa propre micro-boucle d'attente clavier, indépendante de la boucle principale.

Protocole :

1. L'écran secondaire dessine son contenu complet (appel à ses propres fonctions de rendu, **pas** à `tui_draw`)
2. Il entre dans sa propre boucle : `key=$(read_key)`, vérifie `_NEEDS_REDRAW` pour se redessiner lui-même en cas de resize, attend `[q]` ou `[Entrée]`
3. À la sortie, il positionne `_NEEDS_REDRAW=1` pour forcer un redessin complet de la vue principale au retour dans la boucle `navigate`

Cela évite toute interaction avec le flag `_NEEDS_REDRAW` de la boucle principale pendant que l'écran secondaire est actif.

---

## Compatibilité et fallbacks

| Condition | Détection | Comportement |
|---|---|---|
| `tput smcup` non supporté | `tui_check_capability` → `TUI_CAPABLE=0` | Fallback : ancienne boucle avec `clear` |
| `LINES < 9` | Vérifié dans `tui_draw` | Affichage dégradé, footer masqué |
| Non-TTY (`! -t 1`) | Guard existant en début de `navigate` | Mode summary/report automatique |
| SSH avec TTY | — | Fonctionne nativement (TERM + SIGWINCH forwarded) |

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
- Globals : `TUI_CAPABLE`, `_NEEDS_REDRAW`, `CURSOR`, `SCROLL_OFFSET`
- `tui_check_capability`, `tui_enter`, `tui_exit`, `tui_draw`
- `draw_header`, `draw_list`, `draw_footer`
- `read_key`, `cursor_up`, `cursor_down`, `cursor_reset`
- `show_help_screen`, `show_exclusions_screen`, `show_heavy_files`
- `show_config_screen`, `navigate`

### `main.sh`
- Shebang (`#!/usr/bin/env bash`) + shim Bash (lignes 1-14 du fichier actuel)
- `set -u -o pipefail`
- Variables globales, constantes
- `usage`, `parse_args`, `init_colors`, `init_numfmt_support`
- `check_runtime_requirements`, `detect_platform`, `init_cmd_vars`
- `main()`

---

## Tests

Tous les tests sourcent le fichier **généré** `disk-explorer.sh`, pas les fichiers `src/` individuels (qui dépendent les uns des autres pour leurs globals). On ajoute :

- Test smoke : `build.sh` produit un fichier exécutable valide (`bash -n disk-explorer.sh`)
- Tests unitaires TUI : sourcent `disk-explorer.sh`, guards `[[ -t 1 ]]` permettent l'exécution hors TTY
- Test `SIGWINCH` : envoi du signal au PID du processus de test, vérification que `_NEEDS_REDRAW=1`
- Test timeout `read_key` : vérifie que `read_key` retourne `""` après 0.2 s sans touche

---

## Ce qui ne change pas

- Modes `--summary`, `--report`, `--tree`, `--self-check` : comportement identique
- Interface CLI (tous les flags) : identique
- Logique de scan, exclusions, tri : identique
- Compatibilité Linux / macOS / BSD : maintenue
