# Design : Compatibilité macOS / Linux — disk-explorer.sh

**Date :** 2026-05-08
**Statut :** Approuvé (v4 — post spec-review x3)

---

## Objectif

Rendre `disk-explorer.sh` plug & play sur macOS et GNU/Linux sans intervention manuelle de l'utilisateur. Le script détecte la plateforme, utilise les outils natifs quand c'est possible, et installe automatiquement (via Homebrew) ce qui est strictement nécessaire.

---

## Diagnostic des incompatibilités macOS

### Fixables nativement (BSD)

| Incompatibilité | Fix natif |
|---|---|
| `[[ -v VAR ]]` (Bash ≥ 4.2) | `[[ -n "${VAR+x}" ]]` — fix global et inconditionnel dans le source |
| `date -d '@epoch'` | `date -r epoch` |
| `df --output=size,used,avail,pcent,target -B1` | `df -Pk` + parsing awk (voir détail section 6) |
| Vérification `OSTYPE == linux*` bloquante | Supprimée, remplacée par détection de plateforme |
| `realpath -m` (l. 365) | BSD `realpath` ne supporte pas `-m` ; tester via `realpath -m -- / >/dev/null 2>&1` avant d'appeler, sinon utiliser le fallback lexical existant |
| `usage()` l. 105 : "Ce script vise GNU/Linux … Bash >= 4.3" | Mettre à jour : "macOS et GNU/Linux … Bash >= 4.4" |
| `check_runtime_requirements()` l. 213 : `BASH_VERSINFO[1] >= 3` | Changer `3` → `4` (Bash 4.4) |
| `self_check_report()` l. 258 : `[OK] Bash >= 4.3` | Changer `4.3` → `4.4` |

### Requiert GNU tools (brew)

| Outil | Usage | Paquet brew |
|---|---|---|
| `gfind` | `-printf`, null-output (`\0`) | `findutils` |
| `gsort` | `-z` (null-separated) | `coreutils` |
| `ghead` | `-z` (null-separated) | `coreutils` |
| `gdu` | `-0`, `-B1`, `--max-depth`, `--exclude` | `coreutils` |
| `gnumfmt` | `--to=iec-i` (optionnel, fallback interne existant) | `coreutils` |

Notes :
- `--exclude` dans `du` est une extension GNU-only. Sur macOS, `$DU_CMD` est toujours `gdu`.
- `install_hint()` (l. 157) n'est jamais appelée sur macOS : toute installation passe par `resolve_gnu_tools_macos()`. Aucune modification de `install_hint()` n'est nécessaire.

Les pipelines null (`find -printf ...\0 | sort -z | head -z`) sont indispensables pour la robustesse sur les noms de fichiers avec espaces et caractères spéciaux. Aucun équivalent BSD fiable n'existe sans réécriture complète.

### Bash version

macOS embarque Bash 3.2 (licence GPLv2). Le script utilise :
- `declare -A` (tableaux associatifs) → Bash ≥ 4.0
- `local -n` (namerefs) → Bash ≥ 4.3
- `mapfile -d ''` → Bash ≥ 4.4

La version minimale requise est donc **Bash ≥ 4.4**. Brew fournit Bash 5 via `brew install bash`.

Toutes les occurrences de `4.3` dans le source (conditions, messages, usage) doivent être mises à jour vers `4.4` :
- l. 105 : `usage()` — "Bash >= 4.3"
- l. 213 : `check_runtime_requirements()` — `BASH_VERSINFO[1] >= 3`
- l. 258–263 : `self_check_report()` — condition et message `[OK] Bash >= 4.3`

---

## Problème de bootstrap (critique)

Le script contient du code Bash 4+ au niveau global (hors fonctions), notamment `[[ -v NO_COLOR ]]` à la ligne 46. Ce code est évalué au chargement du script avant que toute logique de détection de version puisse s'exécuter.

**Solution : shim POSIX-compatible en tête de script**

Le shim est inséré **immédiatement après la ligne `#!/usr/bin/env bash`**, avant `set -u -o pipefail` et avant toute autre instruction. Il utilise uniquement `(( ))`, `[[ -x ]]`, `exec` et `printf` — tous disponibles en Bash 3.2.

```bash
# Shim : garantit Bash >= 4.4 avant toute syntaxe incompatible.
# Doit précéder set -u et tout code utilisant declare -A, local -n, mapfile, [[ -v ]].
if (( BASH_VERSINFO[0] < 4 || (BASH_VERSINFO[0] == 4 && BASH_VERSINFO[1] < 4) )); then
  # Résoudre le chemin absolu du script avant l'exec (évite l'échec si $0 est relatif)
  _self="$(cd -- "$(dirname -- "${BASH_SOURCE[0]}")" && pwd)/$(basename -- "${BASH_SOURCE[0]}")"
  for _bash in /opt/homebrew/bin/bash /usr/local/bin/bash; do
    [[ -x "$_bash" ]] && exec "$_bash" -- "$_self" "$@"
  done
  printf 'Erreur: Bash >= 4.4 requis.\nSur macOS: brew install bash\nPuis relancer: /opt/homebrew/bin/bash %s\n' "${BASH_SOURCE[0]}" >&2
  exit 1
fi
```

Note : `${BASH_SOURCE[0]}` est disponible en Bash 3.x et donne le chemin du script même quand le script est sourcé ou appelé via un wrapper. La résolution en chemin absolu via `cd + pwd` garantit que `exec` trouve le script même si `$0` est relatif.

De plus, `[[ -v NO_COLOR ]]` est remplacé par `[[ -n "${NO_COLOR+x}" ]]` de façon inconditionnelle dans le source (l. 46, occurrence unique). Ce fix est redondant avec le shim mais améliore la lisibilité.

---

## Initialisation des variables de commandes

Les variables `FIND_CMD`, `SORT_CMD`, `HEAD_CMD`, `DU_CMD`, `NUMFMT_CMD` sont déclarées et initialisées **au niveau global** avec les valeurs par défaut GNU/Linux, dans la section "VARIABLES" du script, immédiatement après les autres variables globales :

```bash
FIND_CMD="find"
SORT_CMD="sort"
HEAD_CMD="head"
DU_CMD="du"
NUMFMT_CMD="numfmt"
PLATFORM=""
```

Sur macOS, `resolve_gnu_tools_macos()` remplace ces valeurs par les commandes g-préfixées détectées. Ces variables sont ainsi toujours définies avant toute utilisation, y compris dans `self_check_report()` et `init_numfmt_support()`.

---

## Architecture de la solution

### 1. Shim de bootstrap (ligne 2 du script, après le shebang)

Voir section "Problème de bootstrap" ci-dessus.

### 2. Initialisation des variables de commandes (section VARIABLES)

Voir section "Initialisation des variables de commandes" ci-dessus.

### 3. Détection de plateforme

Fonction `detect_platform()` — appelée en **premier** dans `main()`, avant toute autre fonction, y compris `init_numfmt_support()` :

```bash
detect_platform() {
  [[ "$OSTYPE" == darwin* ]] && PLATFORM="macos" || PLATFORM="linux"
}
```

Sur macOS, `detect_platform()` est suivie immédiatement de `resolve_gnu_tools_macos()` (dans `main()`), de sorte que `$NUMFMT_CMD` est correct avant l'appel à `init_numfmt_support()`.

### 4. Résolution des outils GNU sur macOS

Fonction `resolve_gnu_tools_macos()` — appelée **uniquement depuis `main()`**, juste après `detect_platform()` si `$PLATFORM == macos`. Elle n'est pas appelée depuis `self_check_report()` (les variables `$CMD` sont déjà fixées à ce stade).

`missing_tools` est déclaré **`local`** à l'intérieur de la fonction : chaque appel repart d'un tableau vide. Cela rend la fonction idempotente et sûre à appeler plusieurs fois sans accumulation d'entrées dupliquées.

```
local -a missing_tools=()
Pour chaque outil { find→gfind, sort→gsort, head→ghead, du→gdu, numfmt→gnumfmt } :
  1. Tester la commande g-préfixée (gfind, gsort, ...)
  2. Tester la commande sans préfixe si elle est GNU (--version 2>&1 | grep -q GNU)
  3. Si absent → missing_tools+=("paquet_brew")
Si missing_tools non vide → déclencher l'install brew (voir section 5)
```

Résultat : mise à jour de `FIND_CMD`, `SORT_CMD`, `HEAD_CMD`, `DU_CMD`, `NUMFMT_CMD`.

### 5. Installation automatique via Homebrew

Si `missing_tools[]` est non vide sur macOS :

```
Si brew absent :
  → Erreur : "Homebrew requis. Installez-le depuis https://brew.sh"
  → exit 1

Si TTY absent (non-interactif) :
  → Erreur : "Outils manquants: <liste>. Installez manuellement: brew install coreutils findutils"
  → exit 1

Sinon (TTY présent) :
  → Affiche les paquets manquants
  → Demande confirmation [o/N]
  → HOMEBREW_NO_AUTO_UPDATE=1 brew install <paquets>
  → Re-vérifie les outils (rappel de resolve_gnu_tools_macos())
  → Continue ou exit 1
```

Note : `HOMEBREW_NO_AUTO_UPDATE=1` évite un `brew update` bloquant en CI ou environnement restreint.

### 6. Adaptations BSD dans les fonctions existantes

#### Variables de commandes — sites d'appel à mettre à jour

**Tous** les appels directs à `find`, `sort`, `head`, `du` dans les fonctions suivantes doivent utiliser `$FIND_CMD`, `$SORT_CMD`, `$HEAD_CMD`, `$DU_CMD` :

| Fonction | Lignes | Outil |
|---|---|---|
| `build_find_prefix()` | 790 | `find` → `"$FIND_CMD"` |
| `check_runtime_requirements()` | 235 | `find` → `"$FIND_CMD"` |
| `self_check_report()` | 287 | `find` → `"$FIND_CMD"` |
| `scan_subdirs_to_file()` (pipeline) | 827, 844 | `sort`, `head` |
| `scan_top_files_to_file()` (pipeline) | 880, 886 | `sort`, `head` |
| `tree_print_node()` (dans `print_tree_view`) | 1599 | `sort` |
| `build_du_cmd()` | 757 | `du` → `"$DU_CMD"` |
| `build_du_tree_cmd()` | 773 | `du` → `"$DU_CMD"` |
| `self_check_report()` (tests GNU) | 293, 299, 305 | `sort`, `head`, `du` |

Sur macOS `$DU_CMD=gdu` qui supporte `--max-depth`, `-B1`, `-0` et `--exclude` — aucune substitution de flags nécessaire.

#### `get_df_fields()`

Deux branches selon `$PLATFORM` :

**Linux :** code actuel (`df --output=...`) inchangé.

**macOS :**
```bash
local df_out
# -P (POSIX) empêche df de wrapper les longues lignes sur deux lignes.
df_out=$(df -Pk -- "$CURRENT_DIR" 2>/dev/null | tail -n 1) || return 1
# Colonnes POSIX df -Pk : Filesystem 1024-blocs Used Available Capacity% Mounted-on
# Sur APFS, des colonnes inode peuvent s'intercaler avant le point de montage ;
# le point de montage est toujours le premier champ commençant par "/" après $5.
local size used avail usep mounted
# printf "%d" évite la notation scientifique sur les disques > 1 To.
size=$(awk '{printf "%d\n", $2 * 1024}' <<< "$df_out")
used=$(awk '{printf "%d\n", $3 * 1024}' <<< "$df_out")
avail=$(awk '{printf "%d\n", $4 * 1024}' <<< "$df_out")
usep=$(awk '{print $5}' <<< "$df_out")
mounted=$(awk '{found=""; for(i=6;i<=NF;i++){if($i~/^\//){for(j=i;j<=NF;j++) printf "%s%s",$j,(j<NF?" ":""); print ""; found=1; exit}} if(!found) print $NF}' <<< "$df_out")
printf '%s %s %s %s %s\n' "$size" "$used" "$avail" "$usep" "$mounted"
```

Note clé : `-P` (POSIX mode) est requis pour garantir une ligne unique même si le chemin du device est long, et pour avoir des colonnes consistantes quel que soit le type de filesystem. Sans `-P`, BSD `df` peut écrire la ligne de données sur deux lignes si le nom du filesystem est trop long, décalant tous les indices de colonnes.

#### `date_from_epoch()`
- Linux : `date -d "@${1%.*}"` (inchangé)
- macOS : `date -r "${1%.*}"`

#### `resolve_path_lexical()` / `realpath`
Sur macOS, BSD `realpath` existe mais ne supporte pas `-m`. Modifier le test :

```bash
if realpath -m -- / >/dev/null 2>&1; then
  realpath -m -- "$input"
  return
fi
# fallback lexical (code existant inchangé)
```

#### `[[ -v NO_COLOR ]]` (ligne 46)
Remplacer par `[[ -n "${NO_COLOR+x}" ]]` — fix inconditionnel dans le source.

#### `init_numfmt_support()`
Utiliser `$NUMFMT_CMD` : `command -v "$NUMFMT_CMD" >/dev/null 2>&1 && HAVE_NUMFMT=1 || HAVE_NUMFMT=0`.
Dans `human_size()`, utiliser `"$NUMFMT_CMD"` au lieu de `numfmt`.

#### `check_runtime_requirements()` / `self_check_report()`

**`check_runtime_requirements()` :**
- Supprimer la vérification `OSTYPE == linux*` bloquante (l. 212)
- Mettre à jour la condition Bash : `BASH_VERSINFO[1] >= 4` (Bash 4.4)
- Sur macOS : si `missing_tools` de l'appel initial dans `main()` a déclenché une install brew, re-vérifier les outils. `resolve_gnu_tools_macos()` n'est pas rappelée ici (les variables sont déjà fixées) — seule la vérification finale des commandes résolues est effectuée.
- Sur Linux : vérifier les commandes via les `$CMD` variables (déjà initialisées à `find` etc.)

**`self_check_report()` — sortie attendue sur macOS :**
```
=== DISK EXPLORER :: SELF-CHECK ===
[OK] Plateforme macOS détectée (darwin24.0)
[OK] Bash >= 4.4 (5.2.x...)
[OK] Commande présente: gfind  (FIND_CMD)
[OK] Commande présente: gsort  (SORT_CMD)
[OK] Commande présente: ghead  (HEAD_CMD)
[OK] Commande présente: gdu    (DU_CMD)
[OK] GNU find: support -printf
[OK] GNU sort: support -z
[OK] GNU head: support -z
[OK] GNU du: support -0
[OK] GNU date: support -d / -r
[OK] numfmt détecté  OU  [INFO] numfmt non détecté (fallback activé)
```

`self_check_report()` utilise directement les variables `$FIND_CMD`, `$SORT_CMD`, etc. — déjà initialisées par `resolve_gnu_tools_macos()` dans `main()`. Elle n'appelle pas `resolve_gnu_tools_macos()` elle-même.

---

## Flux de démarrage (main)

```
[SHIM]   re-exec si Bash < 4.4              ← ligne 2, avant set -u
main()
  → parse_args()
  → detect_platform()                        [NOUVEAU — en premier]
  → if PLATFORM == macos:
      → resolve_gnu_tools_macos()            [NOUVEAU — avant init_numfmt_support]
  → init_numfmt_support()                   (utilise $NUMFMT_CMD déjà résolu)
  → if SELF_CHECK_ONLY:
      → self_check_report()                  (utilise les $CMD variables déjà fixées)
      → return
  → check_runtime_requirements()             [MODIFIÉ : multi-plateforme]
      → vérif Bash >= 4.4
      → sur macOS : install brew si missing_tools[] non vide
      → vérifier les commandes requises (via $FIND_CMD etc.)
  → prepare_current_dir()
  → ...suite inchangée
```

---

## Gestion des erreurs

- Toute erreur d'installation brew est fatale (exit 1) avec message clair
- Si l'utilisateur refuse l'installation → exit 1 avec message explicatif
- Si TTY absent et installation nécessaire → exit 1 avec commande manuelle à exécuter
- Les messages d'erreur sont toujours en français (cohérence avec le script)
- Le mode `--self-check` est mis à jour pour afficher la plateforme et l'état de chaque outil résolu

---

## Tests

Les tests existants (bats) restent valides. Ajout de cas de test pour :
- `detect_platform()` : mock `$OSTYPE=darwin` et `$OSTYPE=linux-gnu`
- `resolve_gnu_tools_macos()` : mock `command -v` avec outils présents / absents
- `get_df_fields()` sur macOS : mock `df -Pk` — cas standard (6 colonnes), cas APFS avec inodes (9 colonnes), cas mount point avec espaces, cas filesystem path long (vérifier -P empêche le wrapping)
- `date_from_epoch()` sur macOS : vérification format `date -r`
- `human_size()` sur disque > 1 To : vérifier pas de notation scientifique dans la sortie awk
- Comportement non-interactif de l'invite brew (TTY absent → exit 1 avec commande)
- Shim avec chemin relatif : vérifier que `BASH_SOURCE[0]` résolution absolue fonctionne

---

## Non concerné par ce changement

- La logique métier (scan, tri, affichage, rapports) reste identique
- Les options CLI sont inchangées
- Le format de sortie est inchangé
- `install_hint()` : non modifiée (jamais appelée sur macOS)
