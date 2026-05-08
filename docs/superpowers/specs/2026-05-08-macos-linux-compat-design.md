# Design : Compatibilité macOS / Linux — disk-explorer.sh

**Date :** 2026-05-08
**Statut :** Approuvé

---

## Objectif

Rendre `disk-explorer.sh` plug & play sur macOS et GNU/Linux sans intervention manuelle de l'utilisateur. Le script détecte la plateforme, utilise les outils natifs quand c'est possible, et installe automatiquement (via Homebrew) ce qui est strictement nécessaire.

---

## Diagnostic des incompatibilités macOS

### Fixables nativement (BSD)

| Incompatibilité | Fix natif |
|---|---|
| `[[ -v VAR ]]` (Bash ≥ 4.2) | `[[ -n "${VAR+x}" ]]` |
| `date -d '@epoch'` | `date -r epoch` |
| `df --output=size,used,avail,pcent,target -B1` | `df -k` + parsing awk |
| `du --max-depth=N` | `du -d N` |
| Vérification `OSTYPE == linux*` bloquante | Supprimée, remplacée par détection de plateforme |

### Requiert GNU tools (brew)

| Outil | Usage | Paquet brew |
|---|---|---|
| `gfind` | `-printf`, null-output (`\0`) | `findutils` |
| `gsort` | `-z` (null-separated) | `coreutils` |
| `ghead` | `-z` (null-separated) | `coreutils` |
| `gdu` | `-0`, `-B1`, `--max-depth` | `coreutils` |
| `gnumfmt` | `--to=iec-i` (optionnel, fallback interne existant) | `coreutils` |

Les pipelines null (`find -printf ...\0 | sort -z | head -z`) sont au cœur du script pour la robustesse sur les noms de fichiers avec espaces et caractères spéciaux. Aucun équivalent BSD fiable n'existe sans réécriture complète.

### Bash version

macOS embarque Bash 3.2 (licence GPLv2). Le script utilise :
- `declare -A` (tableaux associatifs) → Bash ≥ 4.0
- `local -n` (namerefs) → Bash ≥ 4.3

Brew fournit Bash 5 via `brew install bash`.

---

## Architecture de la solution

### 1. Détection de plateforme

Ajout d'une variable globale `PLATFORM` (`linux` | `macos`) initialisée via `$OSTYPE` au plus tôt dans le script (avant toute autre initialisation).

```
detect_platform()  →  PLATFORM="linux" | "macos"
```

### 2. Vérification Bash sur macOS

Avant toute utilisation de `declare -A` ou `local -n`, si `BASH_VERSINFO` indique Bash < 4.3 :
- Recherche d'un bash brew à `/opt/homebrew/bin/bash` ou `/usr/local/bin/bash`
- Si trouvé → affiche la commande de relancement et quitte proprement
- Si absent → propose `brew install bash` et quitte

Cette vérification a lieu dans `check_runtime_requirements()`, remplacée par une version multi-plateforme.

### 3. Résolution des outils GNU sur macOS

Fonction `resolve_gnu_tools_macos()` appelée après la vérification Bash :

```
Pour chaque outil { find, sort, head, du, numfmt } :
  1. Tester la commande avec préfixe g (gfind, gsort, ...)
  2. Tester la commande sans préfixe (GNU dans PATH ?)
  3. Si absent → ajouter au tableau missing_tools[]
```

Variables globales résultantes : `FIND_CMD`, `SORT_CMD`, `HEAD_CMD`, `DU_CMD`, `NUMFMT_CMD`.

Sur Linux, ces variables pointent directement sur `find`, `sort`, `head`, `du`, `numfmt`.

### 4. Installation automatique via Homebrew

Si `missing_tools[]` est non vide sur macOS :

```
Si brew absent :
  → Erreur : "Homebrew requis. Installez-le depuis https://brew.sh"
  → exit 1

Sinon :
  → Affiche les paquets manquants
  → Demande confirmation [o/N]
  → brew install coreutils findutils  (selon besoins)
  → Re-vérifie les outils
  → Continue ou exit 1
```

### 5. Adaptations BSD dans les fonctions existantes

#### `get_df_fields()`
Deux branches selon `$PLATFORM` :
- Linux : code actuel (`df --output=...`)
- macOS : `df -k -- "$CURRENT_DIR" | tail -n 1` + parsing awk pour extraire les 5 champs (taille, utilisé, disponible, pourcentage, point de montage). Conversion kB → bytes pour rester cohérent avec le reste.

#### `date_from_epoch()`
- Linux : `date -d "@${epoch}"` (inchangé)
- macOS : `date -r "${epoch}"`

#### `build_du_cmd()` / `build_du_tree_cmd()`
- Remplacer `--max-depth=N` par `$([[ $PLATFORM == macos ]] && echo "-d" || echo "--max-depth=")N`
- Utiliser `$DU_CMD` au lieu de `du`

#### Toutes les pipelines find/sort/head
- Remplacer `find` par `$FIND_CMD`, `sort` par `$SORT_CMD`, `head` par `$HEAD_CMD`

#### `[[ -v NO_COLOR ]]`
- Remplacer par `[[ -n "${NO_COLOR+x}" ]]` (fix global, unique occurrence)

#### `init_numfmt_support()`
- Utiliser `$NUMFMT_CMD` et `command -v "$NUMFMT_CMD"`

#### `check_runtime_requirements()` / `self_check_report()`
- Supprimer la vérification `OSTYPE == linux*` bloquante
- Intégrer la vérification multi-plateforme (Bash version + outils selon OS)
- `self_check_report()` affiche les infos plateforme et outils détectés

---

## Flux de démarrage (main)

```
main()
  → parse_args()
  → detect_platform()           [NOUVEAU]
  → init_numfmt_support()
  → check_runtime_requirements() [MODIFIÉ : multi-plateforme]
      → check bash version (avec guide brew si macOS)
      → sur macOS : resolve_gnu_tools_macos() + install si besoin
      → vérifier les commandes requises (via $FIND_CMD etc.)
  → prepare_current_dir()
  → ...suite inchangée
```

---

## Gestion des erreurs

- Toute erreur d'installation brew est fatale (exit 1) avec message clair
- Si l'utilisateur refuse l'installation → exit 1 avec message explicatif
- Les messages d'erreur sont toujours en français (cohérence avec le script)
- Le mode `--self-check` est mis à jour pour afficher la plateforme et l'état de chaque outil

---

## Tests

Les tests existants (bats) restent valides. Ajout de cas de test pour :
- `detect_platform()` sur linux et darwin
- `resolve_gnu_tools_macos()` avec outils présents / absents
- `get_df_fields()` sur macOS (mock de `df -k`)
- `date_from_epoch()` sur macOS

---

## Non concerné par ce changement

- La logique métier (scan, tri, affichage, rapports) reste identique
- Les options CLI sont inchangées
- Le format de sortie est inchangé
