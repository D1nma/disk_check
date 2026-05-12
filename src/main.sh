#!/usr/bin/env bash

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

# === MODULE: main ===

set -u -o pipefail

# If the script is being piped to bash (curl | bash), we re-execute it via bash -c
# to decouple it from stdin, allowing us to reconnect stdin to /dev/tty for the TUI.
if [[ ! -t 0 && -t 1 && -z "${BASH_SOURCE[0]:-}" && -z "${_DISK_EXPLORER_REEXEC:-}" ]]; then
  export _DISK_EXPLORER_REEXEC=1
  exec bash -c "$(cat)" bash "$@"
fi

# Reconnect stdin to TTY if redirected (supports curl | bash or bash < script.sh)
if [[ ! -t 0 && -t 1 ]]; then
  exec < /dev/tty 2>/dev/null || :
fi

VERSION="v0.2.0" # Placeholder, should be updated by build process
REPO_URL="https://github.com/D1nma/disk_check"
CACHE_DIR="${HOME}/.cache/disk-explorer/bin/${VERSION}"

# ================== CONFIGURATION PAR DÉFAUT ==================
readonly DEFAULT_REPORT_DIR="${HOME}/disk-reports"
readonly DEFAULT_TOP_COUNT=15
readonly DEFAULT_TOP_FILES_COUNT=20
readonly DEFAULT_TREE_DEPTH=3
readonly DEFAULT_MAX_DEPTH=-1  # -1 = illimité
readonly MAX_ALLOWED_DEPTH=1024
readonly MAX_ALLOWED_RESULTS=10000

readonly DEFAULT_EXCLUDED_DIRS=(
  "/proc" "/sys" "/dev" "/run" "/tmp" "/snap" "/boot" "/overlay"
)

readonly HEAVY_KNOWN_PATTERNS=(
  "node_modules" ".cache" ".gradle" "venv" "virtualenv" "__pycache__"
  "target" "build" "dist" ".npm" ".cargo" "overlay2" "containers/storage"
  "backup" "docker" "podman" "Steam" "wine"
)

# ================== VARIABLES ==================
REPORT_DIR="$DEFAULT_REPORT_DIR"
TOP_COUNT="$DEFAULT_TOP_COUNT"
TOP_FILES_COUNT="$DEFAULT_TOP_FILES_COUNT"
TREE_DEPTH="$DEFAULT_TREE_DEPTH"
MAX_DEPTH="$DEFAULT_MAX_DEPTH"

CURRENT_DIR_INPUT="$(pwd)"
CURRENT_DIR=""

SORT_MODE="size"          # size | mtime
FILE_SIZE_MODE="real"     # real | apparent
ANALYSIS_MODE="partition" # partition | global
RUN_MODE="interactive"    # interactive | summary | report | tree
SELF_CHECK_ONLY=0
DEBUG_TUI=0

USE_DEFAULT_EXCLUDES=1
# Conforme no-color.org : toute variable NO_COLOR définie (même vide) désactive les couleurs.
# ${VAR+x} est portable dès Bash 3.x et sûr avec set -u.
if [[ -n "${NO_COLOR+x}" ]]; then
  NO_COLOR=1
else
  NO_COLOR=0
fi
NO_SPINNER=0
ENABLE_SPINNER=0
HAVE_NUMFMT=0

TEMP_ROOT=""
LAST_WARNING=""
SCAN_WARNING=""
PARTIAL_SCAN_DETECTED=0

PLATFORM=""
FIND_CMD="find"
SORT_CMD="sort"
HEAD_CMD="head"
DU_CMD="du"
NUMFMT_CMD="numfmt"
AWK_CMD="awk"

declare -a EXTRA_EXCLUDED_DIRS=()
declare -a EXCLUDED_DIRS=()
declare -a ACTIVE_EXCLUDED_DIRS=()
declare -a SUBDIR_PATHS=()
declare -a SUBDIR_DATA=()   # données brutes parallèles à SUBDIR_PATHS
declare -a SUBDIR_TYPES=()  # 'd' = répertoire, 'f' = fichier
TUI_CAPABLE=0
_NEEDS_REDRAW=0
CURSOR=0
SCROLL_OFFSET=0

# ================== EXÉCUTION DISTANTE (SSH) ==================
declare -a REMOTE_HOSTS=()   # hôtes passés via --remote-hosts
REMOTE_HOSTS_FILE=""         # fichier d'hôtes (--remote-hosts-file)
REMOTE_PATH="/"              # répertoire scanné sur chaque cible
REMOTE_REPORT_DIR="$(pwd)/remote-reports"  # dossier local pour les rapports
REMOTE_TIMEOUT=10            # ConnectTimeout SSH en secondes
declare -a REMOTE_SSH_OPTS=() # options SSH supplémentaires (--remote-ssh-opt)

# Chemin absolu du script courant ; vide si lancé via pipe (bash -s).
# Utilisé par remote_run_host pour streamer le script sur SSH.
_SCRIPT_SELF=""
if [[ -n "${BASH_SOURCE[0]:-}" ]]; then
  _self_dir="$(cd -- "$(dirname -- "${BASH_SOURCE[0]}")" 2>/dev/null && pwd)"
  _SCRIPT_SELF="${_self_dir}/$(basename -- "${BASH_SOURCE[0]}")"
  unset _self_dir
fi

RED='' GREEN='' YELLOW='' BLUE='' CYAN='' MAGENTA='' BOLD='' DIM='' NC=''

# ================== TRAPS ==================
trap cleanup EXIT
trap on_interrupt INT TERM

try_go_binary() {
    # Bypass if --bash flag is present
    for arg in "$@"; do [[ "$arg" == "--bash" ]] && return; done
    
    # Only try to download/use Go binary if it looks like a release version (starts with v)
    [[ "$VERSION" == v* ]] || return

    local os arch binary
    os=$(get_os)
    arch=$(get_arch)
    binary="${CACHE_DIR}/disk-explorer"

    if [[ ! -x "$binary" ]]; then
        # Try to download
        download_binary "$os" "$arch" "$binary" >/dev/null 2>&1 || return
    fi

    if [[ -x "$binary" ]]; then
        exec "$binary" "$@"
    fi
}

download_binary() {
    local os=$1 arch=$2 target=$3
    local url="${REPO_URL}/releases/download/${VERSION}/disk-explorer-${os}-${arch}"
    
    mkdir -p "$(dirname "$target")"
    if command -v curl >/dev/null 2>&1; then
        curl -SLf "$url" -o "$target"
    elif command -v wget >/dev/null 2>&1; then
        wget -q "$url" -O "$target"
    else
        return 1
    fi
    chmod +x "$target"
}

init_colors() {
  if [[ "$NO_COLOR" -eq 0 && -t 1 ]]; then
    RED='\033[0;31m'
    GREEN='\033[0;32m'
    YELLOW='\033[1;33m'
    BLUE='\033[0;34m'
    CYAN='\033[0;36m'
    MAGENTA='\033[0;35m'
    BOLD='\033[1m'
    DIM='\033[2m'
    NC='\033[0m'
  fi
}

init_runtime_flags() {
  if [[ -t 1 && "$NO_SPINNER" -eq 0 ]]; then
    ENABLE_SPINNER=1
  else
    ENABLE_SPINNER=0
  fi

  if [[ "$RUN_MODE" == "interactive" && ( ! -t 0 || ! -t 1 ) ]]; then
    RUN_MODE="summary"
    ENABLE_SPINNER=0
  fi
}

init_numfmt_support() {
  command -v "$NUMFMT_CMD" >/dev/null 2>&1 && HAVE_NUMFMT=1 || HAVE_NUMFMT=0
}

check_runtime_requirements() {
  (( BASH_VERSINFO[0] > 4 || (BASH_VERSINFO[0] == 4 && BASH_VERSINFO[1] >= 4) )) || die "Bash >= 4.4 requis"

  local required_cmd
  # numfmt est optionnel : human_size() utilise un fallback awk si absent (voir HAVE_NUMFMT).
  local -a required_cmds=(awk "$FIND_CMD" "$SORT_CMD" "$HEAD_CMD" "$DU_CMD" date mktemp df tail)
  local -a missing_cmds=()
  for required_cmd in "${required_cmds[@]}"; do
    command -v "$required_cmd" >/dev/null 2>&1 || missing_cmds+=("$required_cmd")
  done

  if ((${#missing_cmds[@]} > 0)); then
    local os_id missing
    os_id="$(detect_os_id)"
    printf -v missing '%s ' "${missing_cmds[@]}"
    missing="${missing% }"
    # Sortie volontairement explicite pour réduire le temps de diagnostic.
    echo "Erreur: commandes manquantes: $missing" >&2
    echo "Suggestion d'installation ($os_id): $(install_hint "$os_id" "${missing_cmds[@]}")" >&2
    exit 1
  fi

  local req_dir
  req_dir=$(mktemp -d "${TMPDIR:-/tmp}/disk-explorer.req.XXXXXX") || die "impossible de vérifier les prérequis runtime"
  "$FIND_CMD" "$req_dir" -maxdepth 0 -printf '' >/dev/null 2>&1 || { rm -rf -- "$req_dir"; die "GNU find avec -printf requis"; }
  printf '%b' 'a\0' | "$SORT_CMD" -z >/dev/null 2>&1 || { rm -rf -- "$req_dir"; die "GNU sort avec -z requis"; }
  printf '%b' 'a\0' | "$HEAD_CMD" -z -n 1 >/dev/null 2>&1 || { rm -rf -- "$req_dir"; die "GNU head avec -z requis"; }
  "$DU_CMD" -0 --max-depth=0 "$req_dir" >/dev/null 2>&1 || { rm -rf -- "$req_dir"; die "GNU du avec -0 requis"; }
  {
    if [[ "$PLATFORM" == "macos" ]]; then
      date -r 0 '+%Y-%m-%d %H:%M' >/dev/null 2>&1
    else
      date -d '@0' '+%Y-%m-%d %H:%M' >/dev/null 2>&1
    fi
  } || { rm -rf -- "$req_dir"; die "date: support epoch non disponible"; }
  rm -rf -- "$req_dir"
}

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
  _try_gnu_tool AWK_CMD   gawk    gawk

  unset -f _try_gnu_tool

  if (( ${#missing_tools[@]} == 0 )); then
    return 0
  fi

  # Dedupliquer les paquets
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
    printf 'Installation annulee.\n' >&2
    exit 1
  fi

  HOMEBREW_NO_AUTO_UPDATE=1 brew install "${unique_pkgs[@]}" || die "echec de l'installation Homebrew"

  # Re-verifier apres install
  local tool
  for tool in "${missing_tools[@]}"; do
    command -v "$tool" >/dev/null 2>&1 || die "outil toujours manquant apres install: $tool"
  done

  # Reappel pour fixer les variables CMD
  resolve_gnu_tools_macos
}

usage() {
  cat <<'EOF2'
Usage:
  disk-explorer.sh [OPTIONS] [PATH]

Options:
  --path DIR                Dossier de départ
  --mode MODE               partition | global
  --sort MODE               size | mtime
  --file-size MODE          real | apparent
  --top-count N             Nombre de sous-dossiers affichés
  --top-files N             Nombre de fichiers affichés
  --max-depth N             Profondeur max du scan récursif des fichiers
                            (0 = seulement le dossier courant, 1 = + sous-dossiers directs…)
                            (-1 = illimité)
                            Note : s'applique aux fichiers, pas à la vue sous-dossiers.
                            Maximum accepté : 1024
  --report                  Génère un rapport texte puis quitte
  --summary                 Affiche un résumé puis quitte
  --tree                    Affiche une vue arborescente type TreeSize puis quitte
  --tree-depth N            Profondeur max de la vue arborescente (--tree)
  --self-check              Vérifie la compatibilité runtime puis quitte
  --interactive             Force le mode interactif (si TTY disponible)
  --report-dir DIR          Dossier de sortie des rapports
  --exclude PATH            Ajoute une exclusion (répétable)
  --no-default-excludes     N'utilise pas les exclusions par défaut
  --no-color                Désactive les couleurs
  --no-spinner              Désactive le spinner
  --bash                    Force l'utilisation de l'implémentation Bash
  -h, --help                Aide

Mode Remote SSH (--remote) :
  --remote                  Lance le scan sur des machines distantes via SSH
  --remote-hosts HÔTES      Hôtes cibles, séparés par des virgules
                            Exemples: user@host1,host2  ou  root@10.0.0.1
                            (répétable : --remote-hosts h1 --remote-hosts h2)
  --remote-hosts-file FILE  Fichier texte, un hôte par ligne (# = commentaire)
  --remote-path DIR         Répertoire à scanner sur chaque machine (défaut: /)
  --remote-report-dir DIR   Dossier local où stocker les rapports (défaut: ./remote-reports)
  --remote-timeout N        ConnectTimeout SSH en secondes (défaut: 10)
  --remote-ssh-opt OPT      Option SSH brute passée à ssh(1), répétable
                            Exemples: -i ~/.ssh/id_ed25519  -p 2222

Remarques:
  - Le mode PARTITION/CENTREON reste sur le même filesystem.
  - Le tri mtime des sous-dossiers repose sur la date du dossier lui-même,
    pas sur l'activité récursive de tout son contenu.
  - Le mode real/apparent concerne les fichiers ; la vue sous-dossiers repose
    toujours sur l'occupation disque remontée par du.
  - Ce script supporte GNU/Linux et macOS (GNU findutils, coreutils et Bash >= 4.4).
  - Sur macOS, les outils GNU sont installés automatiquement via Homebrew si nécessaire.
  - Les exclusions utilisateur sont traitées comme des chemins littéraux :
    les métacaractères de glob (* ? [ ]) sont refusés.
  - Le mode --remote nécessite une authentification SSH par clé (BatchMode=yes).
    Chaque machine distante doit avoir Bash >= 4.4 et les GNU coreutils installés.
    Les exclusions locales (--exclude) ne sont pas propagées aux machines distantes.
EOF2
}

parse_args() {
  local positional_path_used=0

  while [[ $# -gt 0 ]]; do
    case "$1" in
      --path)
        shift
        [[ $# -gt 0 ]] || die "argument manquant pour --path"
        CURRENT_DIR_INPUT="$1"
        ;;
      --mode)
        shift
        [[ $# -gt 0 ]] || die "argument manquant pour --mode"
        case "$1" in
          partition|global) ANALYSIS_MODE="$1" ;;
          *) die "--mode doit valoir partition ou global" ;;
        esac
        ;;
      --sort)
        shift
        [[ $# -gt 0 ]] || die "argument manquant pour --sort"
        case "$1" in
          size|mtime) SORT_MODE="$1" ;;
          *) die "--sort doit valoir size ou mtime" ;;
        esac
        ;;
      --file-size)
        shift
        [[ $# -gt 0 ]] || die "argument manquant pour --file-size"
        case "$1" in
          real|apparent) FILE_SIZE_MODE="$1" ;;
          *) die "--file-size doit valoir real ou apparent" ;;
        esac
        ;;
      --top-count)
        shift
        [[ $# -gt 0 ]] || die "argument manquant pour --top-count"
        is_non_negative_int "$1" || die "--top-count doit être un entier >= 0"
        (( $1 <= MAX_ALLOWED_RESULTS )) || die "--top-count doit être <= ${MAX_ALLOWED_RESULTS}"
        TOP_COUNT="$1"
        ;;
      --top-files)
        shift
        [[ $# -gt 0 ]] || die "argument manquant pour --top-files"
        is_non_negative_int "$1" || die "--top-files doit être un entier >= 0"
        (( $1 <= MAX_ALLOWED_RESULTS )) || die "--top-files doit être <= ${MAX_ALLOWED_RESULTS}"
        TOP_FILES_COUNT="$1"
        ;;
      --max-depth)
        shift
        [[ $# -gt 0 ]] || die "argument manquant pour --max-depth"
        is_integer "$1" || die "--max-depth doit être un entier"
        (( "$1" >= -1 )) || die "--max-depth doit être >= -1"
        (( "$1" <= MAX_ALLOWED_DEPTH )) || die "--max-depth doit être <= ${MAX_ALLOWED_DEPTH}"
        MAX_DEPTH="$1"
        ;;
      --report)
        RUN_MODE="report"
        ;;
      --summary)
        RUN_MODE="summary"
        ;;
      --tree)
        RUN_MODE="tree"
        ;;
      --tree-depth)
        shift
        [[ $# -gt 0 ]] || die "argument manquant pour --tree-depth"
        is_non_negative_int "$1" || die "--tree-depth doit être un entier >= 0"
        (( "$1" <= MAX_ALLOWED_DEPTH )) || die "--tree-depth doit être <= ${MAX_ALLOWED_DEPTH}"
        TREE_DEPTH="$1"
        ;;
      --self-check)
        SELF_CHECK_ONLY=1
        ;;
      --debug-tui)
        DEBUG_TUI=1
        ;;
      --interactive)
        RUN_MODE="interactive"
        ;;
      --report-dir)
        shift
        [[ $# -gt 0 ]] || die "argument manquant pour --report-dir"
        REPORT_DIR="$1"
        ;;
      --exclude)
        shift
        [[ $# -gt 0 ]] || die "argument manquant pour --exclude"
        EXTRA_EXCLUDED_DIRS+=("$1")
        ;;
      --no-default-excludes)
        USE_DEFAULT_EXCLUDES=0
        ;;
      --no-color)
        NO_COLOR=1
        ;;
      --no-spinner)
        NO_SPINNER=1
        ;;
      --bash)
        # Ignoré ici, déjà traité par le shim try_go_binary
        ;;
      --remote)
        RUN_MODE="remote"
        ;;
      --remote-hosts)
        shift
        [[ $# -gt 0 ]] || die "argument manquant pour --remote-hosts"
        IFS=',' read -r -a _rh <<< "$1"
        local _h
        for _h in "${_rh[@]}"; do
          _h="${_h#"${_h%%[! ]*}"}"   # ltrim espaces
          _h="${_h%"${_h##*[! ]}"}"   # rtrim espaces
          [[ -n "$_h" ]] && REMOTE_HOSTS+=("$_h")
        done
        unset _rh _h
        ;;
      --remote-hosts-file)
        shift
        [[ $# -gt 0 ]] || die "argument manquant pour --remote-hosts-file"
        REMOTE_HOSTS_FILE="$1"
        ;;
      --remote-path)
        shift
        [[ $# -gt 0 ]] || die "argument manquant pour --remote-path"
        REMOTE_PATH="$1"
        ;;
      --remote-report-dir)
        shift
        [[ $# -gt 0 ]] || die "argument manquant pour --remote-report-dir"
        REMOTE_REPORT_DIR="$1"
        ;;
      --remote-timeout)
        shift
        [[ $# -gt 0 ]] || die "argument manquant pour --remote-timeout"
        is_non_negative_int "$1" || die "--remote-timeout doit être un entier >= 0"
        REMOTE_TIMEOUT="$1"
        ;;
      --remote-ssh-opt)
        shift
        [[ $# -gt 0 ]] || die "argument manquant pour --remote-ssh-opt"
        REMOTE_SSH_OPTS+=("$1")
        ;;
      -h|--help)
        usage
        exit 0
        ;;
      --)
        shift
        # Convention POSIX : tout ce qui suit -- est positionnel, pas une option.
        while [[ $# -gt 0 ]]; do
          if (( positional_path_used == 0 )); then
            CURRENT_DIR_INPUT="$1"
            positional_path_used=1
          else
            die "trop d'arguments positionnels"
          fi
          shift
        done
        break
        ;;
      -*)
        die "option inconnue : $1"
        ;;
      *)
        if (( positional_path_used == 0 )); then
          CURRENT_DIR_INPUT="$1"
          positional_path_used=1
        else
          die "trop d'arguments positionnels"
        fi
        ;;
    esac
    shift
  done
}

main() {
  parse_args "$@"
  detect_platform
  if [[ "$PLATFORM" == "macos" ]]; then
    resolve_gnu_tools_macos
  fi
  init_numfmt_support

  if [[ "$SELF_CHECK_ONLY" -eq 1 ]]; then
    # Mode preflight: ne touche ni au scan ni à la navigation interactive.
    self_check_report
    return $?
  fi

  if [[ "$RUN_MODE" == "remote" ]]; then
    # Mode remote : orchestration SSH pure, pas de scan local.
    # On n'appelle ni check_runtime_requirements ni prepare_current_dir.
    init_colors
    remote_run_all
    return $?
  fi

  check_runtime_requirements

  export AWK_CMD FIND_CMD SORT_CMD HEAD_CMD DU_CMD NUMFMT_CMD PLATFORM VERSION DEBUG_TUI

  if [[ "$DEBUG_TUI" -eq 1 ]]; then
    printf "[DEBUG] main() starting at %s\n" "$(date)" > ~/disk-explorer.debug
    printf "[DEBUG] VERSION: %s\n" "$VERSION" >> ~/disk-explorer.debug
    printf "[DEBUG] PWD: %s\n" "$(pwd)" >> ~/disk-explorer.debug
    printf "[DEBUG] AWK_CMD: %s\n" "$AWK_CMD" >> ~/disk-explorer.debug
  fi

  prepare_current_dir
  prepare_exclusions
  init_temp_root
  init_colors
  init_runtime_flags

  local run_rc=0

  case "$RUN_MODE" in
    interactive)
      navigate
      ;;
    summary)
      print_summary || run_rc=$?
      if (( run_rc != 0 )); then
        return "$run_rc"
      fi
      if (( PARTIAL_SCAN_DETECTED != 0 )); then
        return 2
      fi
      return 0
      ;;
    report)
      generate_report_file || run_rc=$?
      if (( run_rc != 0 )); then
        return "$run_rc"
      fi
      echo "$LAST_WARNING"
      if (( PARTIAL_SCAN_DETECTED != 0 )); then
        return 2
      fi
      return 0
      ;;
    tree)
      print_tree_view || run_rc=$?
      if (( run_rc != 0 )); then
        return "$run_rc"
      fi
      return 0
      ;;
    *)
      die "mode d'exécution invalide"
      ;;
  esac
}

# :-  : BASH_SOURCE[0] is unset/empty when piped to bash (curl … | bash), set -u requires default.
if [[ "${BASH_SOURCE[0]:-}" == "${0}" || -z "${BASH_SOURCE[0]:-}" ]]; then
  try_go_binary "$@"
  main "$@"
fi
