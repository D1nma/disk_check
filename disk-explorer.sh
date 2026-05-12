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

VERSION="6246775"
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


# === MODULE: utils ===
# Utilitaires purs

die() {
  echo "Erreur: $*" >&2
  exit 1
}

is_non_negative_int() {
  [[ "$1" =~ ^[0-9]+$ ]]
}

is_integer() {
  [[ "$1" =~ ^-?[0-9]+$ ]]
}

sanitize_for_display() {
  local s="$1"
  s=${s//$'\n'/\\n}
  s=${s//$'\r'/\\r}
  s=${s//$'\t'/\\t}
  s=${s//[[:cntrl:]]/?}
  printf '%s' "$s"
}

contains_glob_meta() {
  [[ "$1" == *[\*\?\[\]]* ]]
}

detect_os_id() {
  local os_id="linux"
  if [[ -r /etc/os-release ]]; then
    # Lecture défensive (sans "source") : on ne veut ni effets de bord
    # sur l'environnement shell, ni exécution de contenu externe.
    local line
    while IFS= read -r line; do
      case "$line" in
        ID=*)
          os_id="${line#ID=}"
          os_id="${os_id%\"}"
          os_id="${os_id#\"}"
          break
          ;;
      esac
    done < /etc/os-release
  fi
  printf '%s\n' "$os_id"
}

install_hint() {
  local os_id="$1"
  shift
  local -a missing_cmds=("$@")
  local cmd pkg
  # Déduplication pour éviter des commandes d'installation verbeuses
  # quand plusieurs binaires proviennent du même paquet.
  local -A pkg_seen=()
  local -a pkgs=()

  for cmd in "${missing_cmds[@]}"; do
    case "$cmd" in
      awk) pkg="gawk" ;;
      find) pkg="findutils" ;;
      sort|head|du|date|mktemp|df|tail) pkg="coreutils" ;;
      *) pkg="$cmd" ;;
    esac
    if [[ -z "${pkg_seen[$pkg]+x}" ]]; then
      pkg_seen[$pkg]=1
      pkgs+=("$pkg")
    fi
  done

  local pkg_list
  printf -v pkg_list '%s ' "${pkgs[@]}"
  pkg_list="${pkg_list% }"

  case "$os_id" in
    debian|ubuntu|linuxmint|pop|kali|raspbian)
      printf 'sudo apt-get update && sudo apt-get install -y %s\n' "$pkg_list"
      ;;
    fedora|rhel|centos|rocky|almalinux)
      printf 'sudo dnf install -y %s\n' "$pkg_list"
      ;;
    opensuse*|sles)
      printf 'sudo zypper install -y %s\n' "$pkg_list"
      ;;
    arch|manjaro)
      printf 'sudo pacman -S --needed %s\n' "$pkg_list"
      ;;
    alpine)
      printf 'sudo apk add %s\n' "$pkg_list"
      ;;
    *)
      printf 'Installez les paquets suivants via votre gestionnaire de paquets: %s\n' "$pkg_list"
      ;;
  esac
}

human_size() {
  local size="$1"

  if [[ "$HAVE_NUMFMT" -eq 1 ]]; then
    "$NUMFMT_CMD" --to=iec-i --suffix=B --format="%.1f" "$size" 2>/dev/null && return 0
  fi

  # Fallback manuel : une décimale calculée pour rester cohérent avec numfmt.
  local i=0 rem=0
  local -a units=("B" "KiB" "MiB" "GiB" "TiB" "PiB" "EiB" "ZiB" "YiB")
  while (( size >= 1024 && i < ${#units[@]} - 1 )); do
    rem=$(( size % 1024 ))
    size=$(( size / 1024 ))
    (( i++ ))
  done
  if (( i > 0 )); then
    printf '%d.%d%s\n' "$size" "$(( rem * 10 / 1024 ))" "${units[i]}"
  else
    printf '%d%s\n' "$size" "${units[i]}"
  fi
}

path_is_equal_or_within() {
  local path="$1"
  local base="$2"

  if [[ "$base" == "/" ]]; then
    [[ "$path" == /* ]]
    return
  fi

  [[ "$path" == "$base" || "$path" == "$base"/* ]]
}

analysis_label() {
  [[ "$ANALYSIS_MODE" == "partition" ]] && echo "MÊME PARTITION" || echo "GLOBAL"
}

detect_platform() {
  [[ "$OSTYPE" == darwin* ]] && PLATFORM="macos" || PLATFORM="linux"
}

get_os() {
    local os
    os=$(uname -s | tr '[:upper:]' '[:lower:]')
    case "$os" in
        linux*) echo "linux" ;;
        darwin*) echo "darwin" ;;
        *) echo "$os" ;;
    esac
}

get_arch() {
    local arch
    arch=$(uname -m)
    case "$arch" in
        x86_64|amd64) echo "amd64" ;;
        arm64|aarch64) echo "arm64" ;;
        *) echo "$arch" ;;
    esac
}

file_size_label() {
  [[ "$FILE_SIZE_MODE" == "real" ]] && echo "Réel (blocs alloués)" || echo "Apparent (taille logique)"
}

depth_label() {
  (( MAX_DEPTH >= 0 )) && echo "$MAX_DEPTH" || echo "illimitée"
}

is_heavy_known() {
  local name="$1"
  local pat
  for pat in "${HEAVY_KNOWN_PATTERNS[@]}"; do
    [[ "$name" == *"$pat"* ]] && return 0
  done
  return 1
}

date_from_epoch() {
  if [[ "$PLATFORM" == "macos" ]]; then
    date -r "${1%.*}" '+%Y-%m-%d %H:%M' 2>/dev/null || echo "?"
  else
    date -d "@${1%.*}" '+%Y-%m-%d %H:%M' 2>/dev/null || echo "?"
  fi
}

# === MODULE: scan ===
# Scan disque, fichiers temporaires, exclusions

normalize_dir() {
  local input="$1"
  (cd -- "$input" 2>/dev/null && pwd -P)
}

resolve_path_lexical() {
  local input="$1"

  if [[ "$input" != /* ]]; then
    input="${CURRENT_DIR%/}/$input"
  fi

  if realpath -m -- / >/dev/null 2>&1; then
    realpath -m -- "$input"
    return
  fi

  local path="$input"
  local IFS='/'
  local -a parts stack=()
  read -r -a parts <<< "${path#/}"

  local part idx joined
  for part in "${parts[@]}"; do
    case "$part" in
      ''|.)
        continue
        ;;
      ..)
        if ((${#stack[@]} > 0)); then
          idx=$((${#stack[@]} - 1))
          unset "stack[$idx]"
          stack=("${stack[@]}")
        fi
        ;;
      *)
        stack+=("$part")
        ;;
    esac
  done

  if ((${#stack[@]} == 0)); then
    printf '/\n'
  else
    printf -v joined '%s/' "${stack[@]}"
    printf '/%s\n' "${joined%/}"
  fi
}

init_temp_root() {
  TEMP_ROOT="$(mktemp -d "${TMPDIR:-/tmp}/disk-explorer.XXXXXX")" || die "impossible de créer le répertoire temporaire"
}

make_temp_file() {
  [[ -n "$TEMP_ROOT" && -d "$TEMP_ROOT" ]] || init_temp_root
  mktemp "$TEMP_ROOT/tmp.XXXXXX"
}

cleanup() {
  [[ -n "$TEMP_ROOT" && -d "$TEMP_ROOT" ]] && rm -rf -- "$TEMP_ROOT"
}

on_interrupt() {
  # Le trap EXIT appellera cleanup() automatiquement après exit.
  printf '\n' >&2
  exit 130
}

spinner() {
  local pid="$1"
  local chars="|/-\\"
  local i=0

  while kill -0 "$pid" 2>/dev/null; do
    printf ' [%s]\r' "${chars:i:1}" >&2
    i=$(((i + 1) % 4))
    sleep 0.1
  done
  # Effacement propre de la ligne de spinner.
  printf '\r\033[K' >&2
}

wait_for_job() {
  local pid="$1"
  if [[ "$ENABLE_SPINNER" -eq 1 ]]; then
    spinner "$pid"
  fi
  wait "$pid"
}

set_current_dir() {
  local target="$1"
  local normalized

  normalized="$(normalize_dir "$target")" || return 1
  CURRENT_DIR="$normalized"
  refresh_active_exclusions
}

prepare_current_dir() {
  set_current_dir "$CURRENT_DIR_INPUT" || die "le dossier '$CURRENT_DIR_INPUT' n'existe pas ou n'est pas accessible"
}

prepare_exclusions() {
  EXCLUDED_DIRS=()

  if [[ "$USE_DEFAULT_EXCLUDES" -eq 1 ]]; then
    EXCLUDED_DIRS=("${DEFAULT_EXCLUDED_DIRS[@]}")
  fi

  local d resolved
  for d in "${EXTRA_EXCLUDED_DIRS[@]}"; do
    contains_glob_meta "$d" && die "les métacaractères de glob (* ? [ ]) ne sont pas autorisés dans --exclude : $d"
    if [[ "$d" != /* ]]; then
      d="${CURRENT_DIR%/}/$d"
    fi
    # On tente normalize_dir (physique, suit les symlinks) pour être cohérent
    # avec CURRENT_DIR. Si le chemin n'existe pas encore, on repli sur la
    # résolution lexicale afin de ne pas bloquer une exclusion anticipée.
    resolved="$(normalize_dir "$d" 2>/dev/null)" || resolved="$(resolve_path_lexical "$d")"
    EXCLUDED_DIRS+=("$resolved")
  done
}

refresh_active_exclusions() {
  ACTIVE_EXCLUDED_DIRS=()

  local d
  for d in "${EXCLUDED_DIRS[@]}"; do
    if path_is_equal_or_within "$CURRENT_DIR" "$d"; then
      continue
    fi
    ACTIVE_EXCLUDED_DIRS+=("$d")
  done

  if [[ -n "$TEMP_ROOT" ]]; then
    ACTIVE_EXCLUDED_DIRS+=("$TEMP_ROOT")
  fi
}

get_df_fields() {
  local df_out size used avail usep mounted

  if [[ "$PLATFORM" == "macos" ]]; then
    # -P (POSIX) empêche le wrapping des longues lignes sur deux lignes.
    df_out=$(df -Pk -- "$CURRENT_DIR" 2>/dev/null | tail -n 1)
    [[ -z "$df_out" ]] && return 1
    # Colonnes : Filesystem 1024-blocs Used Available Capacity [iused ifree %iused] Mounted-on
    # printf "%d" évite la notation scientifique sur les disques > 1 To.
    size=$("$AWK_CMD" '{printf "%d\n", $2 * 1024}' <<< "$df_out")
    used=$("$AWK_CMD" '{printf "%d\n", $3 * 1024}' <<< "$df_out")
    avail=$("$AWK_CMD" '{printf "%d\n", $4 * 1024}' <<< "$df_out")
    usep=$("$AWK_CMD" '{print $5}' <<< "$df_out")
    # Le mount point commence après Capacity ($5) ; peut contenir des espaces.
    # Sur APFS avec colonnes inode, le mount point est le premier champ commençant par "/".
    # Fallback sur $NF si aucun champ ne commence par "/".
    mounted=$("$AWK_CMD" '{
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

  printf '%s %s %s %s\t%s\n' "$size" "$used" "$avail" "$usep" "$mounted"
}

build_du_cmd() {
  # shellcheck disable=SC2178
  local -n out_arr="$1"
  refresh_active_exclusions

  out_arr=("$DU_CMD" -P -0 -B1 --max-depth=1)
  [[ "$ANALYSIS_MODE" == "partition" ]] && out_arr+=(-x)

  local d
  for d in "${ACTIVE_EXCLUDED_DIRS[@]}"; do
    out_arr+=(--exclude="$d")
  done

  out_arr+=(-- "$CURRENT_DIR")
}

build_du_tree_cmd() {
  # shellcheck disable=SC2178
  local -n out_arr="$1"
  refresh_active_exclusions

  out_arr=("$DU_CMD" -P -0 -B1 --max-depth="$TREE_DEPTH")
  [[ "$ANALYSIS_MODE" == "partition" ]] && out_arr+=(-x)

  local d
  for d in "${ACTIVE_EXCLUDED_DIRS[@]}"; do
    out_arr+=(--exclude="$d")
  done

  out_arr+=(-- "$CURRENT_DIR")
}

build_find_prefix() {
  # shellcheck disable=SC2178
  local -n out_arr="$1"
  local maxdepth="${2-}"

  refresh_active_exclusions
  out_arr=("$FIND_CMD" -P "$CURRENT_DIR")

  [[ "$ANALYSIS_MODE" == "partition" ]] && out_arr+=(-xdev)
  [[ -n "$maxdepth" ]] && out_arr+=(-maxdepth "$maxdepth")

  if ((${#ACTIVE_EXCLUDED_DIRS[@]} > 0)); then
    out_arr+=( '(' )

    local d first=1
    for d in "${ACTIVE_EXCLUDED_DIRS[@]}"; do
      (( first )) || out_arr+=(-o)
      out_arr+=(-path "$d" -o -path "$d/*")
      first=0
    done

    out_arr+=( ')' -prune -o )
  fi
}

scan_subdirs_to_file() {
  local out_file="$1"
  local err_file="$2"
  local job_rc=0

  SCAN_WARNING=""
  : > "$out_file"
  : > "$err_file"
  (( TOP_COUNT > 0 )) || return 0

  if [[ "$SORT_MODE" == "mtime" ]]; then
    local -a find_cmd
    build_find_prefix find_cmd 1

    (
      "${find_cmd[@]}" -mindepth 1 -type d -printf '%T@\t%p\0' |
        LC_ALL=C "$SORT_CMD" -zrn |
        "$HEAD_CMD" -z -n "$TOP_COUNT"
    ) >"$out_file" 2>"$err_file" &
  else
    local -a du_cmd
    build_du_cmd du_cmd

    (
      "${du_cmd[@]}" |
        "$AWK_CMD" -v RS='\0' -v ORS='\0' -v root="$CURRENT_DIR" '
          {
            tab = index($0, "\t")
            if (tab == 0) next
            path = substr($0, tab + 1)
            if (path != root) print $0
          }
        ' |
        LC_ALL=C "$SORT_CMD" -zrn |
        "$HEAD_CMD" -z -n "$TOP_COUNT"
    ) >"$out_file" 2>"$err_file" &
  fi

  local pid=$!
  wait_for_job "$pid" || job_rc=$?
  update_scan_warning "$err_file" "Analyse partielle possible" "$job_rc"
  return "$job_rc"
}

scan_top_files_to_file() {
  local out_file="$1"
  local err_file="$2"
  local job_rc=0

  SCAN_WARNING=""
  : > "$out_file"
  : > "$err_file"
  (( TOP_FILES_COUNT > 0 )) || return 0

  local -a find_cmd
  local effective_find_maxdepth
  if (( MAX_DEPTH >= 0 )); then
    # Le contrat utilisateur exprime une profondeur relative au dossier courant :
    #   0 = fichiers du dossier courant, 1 = + fichiers des sous-dossiers directs, etc.
    # Avec find, le point de départ lui-même est à profondeur 0 ; on décale donc de +1.
    effective_find_maxdepth=$((MAX_DEPTH + 1))
    build_find_prefix find_cmd "$effective_find_maxdepth"
  else
    build_find_prefix find_cmd
  fi

  if [[ "$FILE_SIZE_MODE" == "apparent" ]]; then
    (
      "${find_cmd[@]}" -type f -printf '%s\t%p\0' |
        LC_ALL=C "$SORT_CMD" -zrn |
        "$HEAD_CMD" -z -n "$TOP_FILES_COUNT"
    ) >"$out_file" 2>"$err_file" &
  else
    (
      "${find_cmd[@]}" -type f -printf '%b\t%p\0' |
        LC_ALL=C "$SORT_CMD" -zrn |
        "$HEAD_CMD" -z -n "$TOP_FILES_COUNT"
    ) >"$out_file" 2>"$err_file" &
  fi

  local pid=$!
  wait_for_job "$pid" || job_rc=$?
  update_scan_warning "$err_file" "Analyse partielle possible" "$job_rc"
  return "$job_rc"
}

update_scan_warning() {
  local err_file="$1"
  local context="$2"
  local job_rc="$3"

  SCAN_WARNING=""

  if [[ -s "$err_file" ]]; then
    local sample
    sample=$(head -n 1 -- "$err_file" 2>/dev/null)
    sample=${sample:-"détails non disponibles"}
    SCAN_WARNING="${context} : $(sanitize_for_display "$sample")"
  elif (( job_rc > 128 )); then
    SCAN_WARNING="${context} : commande interrompue par signal $((job_rc - 128))"
  elif (( job_rc != 0 )); then
    SCAN_WARNING="${context} : commande terminée avec code ${job_rc}"
  fi
}

run_scan_subdirs_job() {
  local out_file="$1"
  local err_file="$2"
  local warning_file="$3"
  local scan_rc=0

  ENABLE_SPINNER=0
  scan_subdirs_to_file "$out_file" "$err_file" || scan_rc=$?
  printf '%s' "$SCAN_WARNING" > "$warning_file"
  return "$scan_rc"
}

run_scan_top_files_job() {
  local out_file="$1"
  local err_file="$2"
  local warning_file="$3"
  local scan_rc=0

  ENABLE_SPINNER=0
  scan_top_files_to_file "$out_file" "$err_file" || scan_rc=$?
  printf '%s' "$SCAN_WARNING" > "$warning_file"
  return "$scan_rc"
}

# === MODULE: display ===
# Sorties non-TUI : summary, report, tree, self-check

self_check_report() {
  local -i rc=0
  local -a missing_cmds=()

  echo "=== DISK EXPLORER :: SELF-CHECK ==="

  case "${PLATFORM:-}" in
    linux)  echo "[OK] Plateforme Linux détectée (${OSTYPE:-unknown})" ;;
    macos)  echo "[OK] Plateforme macOS détectée (${OSTYPE:-unknown})" ;;
    *)      echo "[KO] Plateforme non reconnue (${OSTYPE:-unknown})"; rc=1 ;;
  esac

  if (( BASH_VERSINFO[0] > 4 || (BASH_VERSINFO[0] == 4 && BASH_VERSINFO[1] >= 4) )); then
    echo "[OK] Bash >= 4.4 (${BASH_VERSION})"
  else
    echo "[KO] Bash >= 4.4 requis (actuel: ${BASH_VERSION})"
    rc=1
  fi

  local -A _cmd_map=(
    [awk]="$AWK_CMD"
    [find]="$FIND_CMD"
    [sort]="$SORT_CMD"
    [head]="$HEAD_CMD"
    [du]="$DU_CMD"
    [numfmt]="$NUMFMT_CMD"
    [date]="date"
    [mktemp]="mktemp"
    [df]="df"
    [tail]="tail"
  )
  local canonical resolved
  for canonical in awk find sort head du numfmt date mktemp df tail; do
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

  if ((${#missing_cmds[@]} > 0)); then
    local os_id
    os_id="$(detect_os_id)"
    echo "Suggestion d'installation ($os_id): $(install_hint "$os_id" "${missing_cmds[@]}")"
    echo "[INFO] Tests des fonctionnalités GNU ignorés (dépendances manquantes)."
  else
    local req_dir
    req_dir=$(mktemp -d "${TMPDIR:-/tmp}/disk-explorer.selfcheck.XXXXXX") || {
      echo "[KO] impossible de créer un répertoire temporaire pour les tests GNU"
      return 1
    }

    if "$FIND_CMD" "$req_dir" -maxdepth 0 -printf '' >/dev/null 2>&1; then
      echo "[OK] GNU find: support -printf"
    else
      echo "[KO] GNU find: -printf non supporté"
      rc=1
    fi
    if printf '%b' 'a\0' | "$SORT_CMD" -z >/dev/null 2>&1; then
      echo "[OK] GNU sort: support -z"
    else
      echo "[KO] GNU sort: -z non supporté"
      rc=1
    fi
    if printf '%b' 'a\0' | "$HEAD_CMD" -z -n 1 >/dev/null 2>&1; then
      echo "[OK] GNU head: support -z"
    else
      echo "[KO] GNU head: -z non supporté"
      rc=1
    fi
    if "$DU_CMD" -0 --max-depth=0 "$req_dir" >/dev/null 2>&1; then
      echo "[OK] GNU du: support -0"
    else
      echo "[KO] GNU du: -0 non supporté"
      rc=1
    fi
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

    rm -rf -- "$req_dir"
  fi

  if [[ "$HAVE_NUMFMT" -eq 1 ]]; then
    echo "[OK] numfmt détecté (format humain précis activé)"
  else
    echo "[INFO] numfmt non détecté (fallback interne activé)"
  fi

  return "$rc"
}

print_exclusions_summary() {
  refresh_active_exclusions

  echo "Exclusions configurées  : ${#EXCLUDED_DIRS[@]}"
  echo "Exclusions actives      : ${#ACTIVE_EXCLUDED_DIRS[@]}"

  if (( ${#ACTIVE_EXCLUDED_DIRS[@]} > 0 )); then
    local d
    echo "Liste exclusions actives :"
    for d in "${ACTIVE_EXCLUDED_DIRS[@]}"; do
      echo "  - $(sanitize_for_display "$d")"
    done
  fi
}

generate_report_file() {
  LAST_WARNING=""

  mkdir -p -- "$REPORT_DIR" || die "impossible de créer le dossier de rapport '$REPORT_DIR'"

  local timestamp safe_name report_file
  timestamp=$(date '+%Y-%m-%d_%H-%M-%S')
  safe_name=$(basename -- "$CURRENT_DIR" | tr -cd '[:alnum:]_-')
  [[ -z "$safe_name" ]] && safe_name="root"
  report_file="${REPORT_DIR}/Report_${safe_name}_${timestamp}.txt"

  local tmp_report staged_report
  # Générer d'abord le rapport hors du périmètre potentiellement scanné pour
  # éviter toute auto-influence si REPORT_DIR est sous CURRENT_DIR.
  tmp_report=$(make_temp_file) || die "impossible de préparer le rapport"

  if ! print_summary > "$tmp_report"; then
    rm -f -- "$tmp_report"
    die "échec de génération du rapport"
  fi

  # Une fois le contenu final stabilisé, préparer un fichier de staging dans
  # REPORT_DIR puis faire un rename final atomique sur le même filesystem.
  staged_report=$(mktemp -- "${REPORT_DIR}/.Report_${safe_name}_${timestamp}.stage.XXXXXX")     || { rm -f -- "$tmp_report"; die "impossible de préparer le fichier de staging du rapport dans '$REPORT_DIR'"; }

  if ! cat -- "$tmp_report" > "$staged_report"; then
    rm -f -- "$tmp_report" "$staged_report"
    die "impossible d'écrire le rapport temporaire dans '$REPORT_DIR'"
  fi

  rm -f -- "$tmp_report"

  mv -f -- "$staged_report" "$report_file" || { rm -f -- "$staged_report"; die "impossible d'écrire le rapport '$report_file'"; }

  LAST_WARNING="rapport créé : $report_file"
}

print_summary() {
  LAST_WARNING=""
  PARTIAL_SCAN_DETECTED=0

  local tmp_sub err_sub warn_sub tmp_files err_files warn_files
  tmp_sub=$(make_temp_file) || return 1
  err_sub=$(make_temp_file) || return 1
  warn_sub=$(make_temp_file) || return 1
  tmp_files=$(make_temp_file) || return 1
  err_files=$(make_temp_file) || return 1
  warn_files=$(make_temp_file) || return 1

  local sub_warning="" files_warning=""
  local sub_rc=0 files_rc=0
  local pid_sub pid_files

  run_scan_subdirs_job "$tmp_sub" "$err_sub" "$warn_sub" &
  pid_sub=$!
  run_scan_top_files_job "$tmp_files" "$err_files" "$warn_files" &
  pid_files=$!

  wait "$pid_sub" || sub_rc=$?
  wait "$pid_files" || files_rc=$?

  (( sub_rc != 0 )) && { rm -f -- "$tmp_sub" "$err_sub" "$warn_sub" "$tmp_files" "$err_files" "$warn_files"; return "$sub_rc"; }
  (( files_rc != 0 )) && { rm -f -- "$tmp_sub" "$err_sub" "$warn_sub" "$tmp_files" "$err_files" "$warn_files"; return "$files_rc"; }

  sub_warning=$(cat -- "$warn_sub" 2>/dev/null)
  files_warning=$(cat -- "$warn_files" 2>/dev/null)

  local df_fields size used avail use_p mounted fields
  if df_fields="$(get_df_fields)"; then
    IFS=$'\t' read -r fields mounted <<< "$df_fields"
    read -r size used avail use_p <<< "$fields"
  else
    size="?"
    used="?"
    avail="?"
    use_p="?"
    mounted="?"
  fi

  echo "RAPPORT DISQUE - $(date)"
  echo "Dossier                : $CURRENT_DIR"
  echo "Mode analyse           : $(analysis_label)"
  echo "Tri sous-dossiers      : $SORT_MODE"
  echo "Mode taille fichiers   : $(file_size_label)"
  echo "Profondeur max fichiers: $(depth_label)"
  echo "Top sous-dossiers      : $TOP_COUNT"
  echo "Top fichiers           : $TOP_FILES_COUNT"
  print_exclusions_summary
  echo "Partition              : $mounted"
  [[ "$size" == "?" ]] && echo "Total                  : ?" || echo "Total                  : $(human_size "$size")"
  [[ "$avail" == "?" ]] && echo "Libre                  : ?" || echo "Libre                  : $(human_size "$avail")"
  echo "Occupation             : $use_p"
  echo

  if [[ -n "$sub_warning" || -n "$files_warning" ]]; then
    PARTIAL_SCAN_DETECTED=1
    echo "AVERTISSEMENTS :"
    [[ -n "$sub_warning" ]] && echo "  - $sub_warning"
    [[ -n "$files_warning" ]] && echo "  - $files_warning"
    echo
    LAST_WARNING="${sub_warning:-$files_warning}"
  fi

  echo "TOP SOUS-DOSSIERS :"
  local found=0
  local line ts full_path raw_size
  if [[ "$SORT_MODE" == "mtime" ]]; then
    while IFS= read -r -d '' line; do
      ts="${line%%$'\t'*}"
      full_path="${line#*$'\t'}"
      [[ -z "$full_path" || "$full_path" == "$line" ]] && continue
      printf '  %s  %s\n' "$(date_from_epoch "$ts")" "$(sanitize_for_display "$full_path")"
      found=1
    done < "$tmp_sub"
  else
    while IFS= read -r -d '' line; do
      raw_size="${line%%$'\t'*}"
      full_path="${line#*$'\t'}"
      [[ -z "$full_path" || "$full_path" == "$line" ]] && continue
      printf '  %12s  %s\n' "$(human_size "$raw_size")" "$(sanitize_for_display "$full_path")"
      found=1
    done < "$tmp_sub"
  fi
  (( found == 0 )) && echo "  (aucun)"

  echo
  echo "TOP FICHIERS :"
  found=0
  local value path size_bytes
  while IFS= read -r -d '' line; do
    value="${line%%$'\t'*}"
    path="${line#*$'\t'}"
    [[ -z "$path" || "$path" == "$line" ]] && continue

    if [[ "$FILE_SIZE_MODE" == "apparent" ]]; then
      size_bytes="$value"
    else
      size_bytes=$((value * 512))
    fi

    printf '  %12s  %s\n' "$(human_size "$size_bytes")" "$(sanitize_for_display "$path")"
    found=1
  done < "$tmp_files"
  (( found == 0 )) && echo "  (aucun)"

  rm -f -- "$tmp_sub" "$err_sub" "$warn_sub" "$tmp_files" "$err_files" "$warn_files"
}

print_tree_view() {
  LAST_WARNING=""

  local tmp_file err_file
  tmp_file=$(make_temp_file) || return 1
  err_file=$(make_temp_file) || return 1
  : > "$tmp_file"
  : > "$err_file"

  local -a du_cmd
  build_du_tree_cmd du_cmd

  "${du_cmd[@]}" >"$tmp_file" 2>"$err_file"

  local job_rc=$?
  update_scan_warning "$err_file" "Vue arborescente partielle possible" "$job_rc"
  [[ -n "$SCAN_WARNING" ]] && echo "Avertissement: $SCAN_WARNING"

  echo "TREE SIZE VIEW (depth=${TREE_DEPTH}) - $CURRENT_DIR"

  declare -A tree_size_map=()
  declare -A tree_children_map=()

  local line raw_size full_path parent
  while IFS= read -r -d '' line; do
    raw_size="${line%%$'\t'*}"
    full_path="${line#*$'\t'}"
    [[ -z "$full_path" || "$full_path" == "$line" ]] && continue

    tree_size_map["$full_path"]="$raw_size"

    if [[ "$full_path" != "$CURRENT_DIR" ]]; then
      parent="${full_path%/*}"; parent="${parent:-/}"
      if path_is_equal_or_within "$parent" "$CURRENT_DIR"; then
        tree_children_map["$parent"]+="$full_path"$'\n'
      fi
    fi
  done < "$tmp_file"

  if [[ -z "${tree_size_map["$CURRENT_DIR"]+x}" ]]; then
    echo "Aucune donnée d'arborescence disponible."
    rm -f -- "$tmp_file" "$err_file"
    return 0
  fi

  tree_percent_str() {
    local child_size="$1"
    local parent_size="$2"
    local pct10=0
    if (( parent_size > 0 )); then
      pct10=$(( child_size * 1000 / parent_size ))
    fi
    printf '%d.%d%%' "$((pct10 / 10))" "$((pct10 % 10))"
  }

  tree_print_node() {
    local node="$1"
    local depth="$2"
    local parent_size="$3"
    local node_size="${tree_size_map[$node]}"
    local rel indent pct

    if [[ "$node" == "$CURRENT_DIR" ]]; then
      rel="."
      pct="100.0%"
    else
      rel="${node#"${CURRENT_DIR}/"}"
      pct="$(tree_percent_str "$node_size" "$parent_size")"
    fi

    if (( depth > 0 )); then
      printf -v indent '%*s' $(( (depth - 1) * 2 )) ''
      printf '%12s  %7s  %s├── %s\n' "$(human_size "$node_size")" "$pct" "$indent" "$(sanitize_for_display "$rel")"
    else
      printf '%12s  %7s  %s\n' "$(human_size "$node_size")" "$pct" "$(sanitize_for_display "$rel")"
    fi

    local children_raw child
    children_raw="${tree_children_map[$node]-}"
    [[ -z "$children_raw" ]] && return 0

    local -a sorted_children=()
    mapfile -d '' -t sorted_children < <(
      while IFS= read -r child; do
        [[ -z "$child" ]] && continue
        printf '%s\t%s\0' "${tree_size_map[$child]}" "$child"
      done <<< "$children_raw" | LC_ALL=C "$SORT_CMD" -zrn | "$AWK_CMD" -v RS='\0' -v ORS='\0' -F '\t' '{sub(/^[^\t]*\t/, "", $0); print $0}'
    )

    local next_child
    for next_child in "${sorted_children[@]}"; do
      tree_print_node "$next_child" "$((depth + 1))" "$node_size"
    done
  }

  tree_print_node "$CURRENT_DIR" 0 "${tree_size_map[$CURRENT_DIR]}"

  rm -f -- "$tmp_file" "$err_file"
}

# === MODULE: tui ===
# TUI interactif : dessin, saisie, navigation

_TUI_DF_CACHE_DIR=""
_TUI_DF_CACHE_VAL=""

_tui_refresh_df_cache() {
  if [[ "$CURRENT_DIR" != "$_TUI_DF_CACHE_DIR" || -z "$_TUI_DF_CACHE_VAL" ]]; then
    _TUI_DF_CACHE_VAL="$(get_df_fields 2>/dev/null)"
    _TUI_DF_CACHE_DIR="$CURRENT_DIR"
  fi
}

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

show_help_screen() {
  [[ "$TUI_CAPABLE" -eq 0 && -t 1 ]] && clear
  cat <<EOF2
${BOLD}${BLUE}AIDE - DISK EXPLORER${NC}

${BOLD}Navigation${NC}
  [1-N]   Entrer dans un sous-dossier
  [0]     Retour arrière

${BOLD}Actions${NC}
  [d]     Supprimer l'entrée sélectionnée (confirmation requise)
  [s]     Changer le tri (size / mtime)
  [a]     Changer le mode taille fichiers (real / apparent)
  [p]     Changer le mode d'analyse (partition / global)
  [f]     Afficher les plus gros fichiers
  [e]     Voir les exclusions configurées
  [r]     Générer un rapport
  [h/?]   Afficher cette aide
  [c]     Ouvrir le menu de configuration
  [q]     Quitter

${BOLD}Réglages courants${NC}
  Mode analyse       : $(analysis_label)
  Tri                : $SORT_MODE
  Taille fichiers    : $FILE_SIZE_MODE
  Top sous-dossiers  : $TOP_COUNT
  Top fichiers       : $TOP_FILES_COUNT
  Profondeur max     : $(depth_label)
  Exclusions config. : ${#EXCLUDED_DIRS[@]}

${BOLD}Rappels${NC}
  même partition = reste sur le même filesystem
  global         = traverse aussi les mountpoints
  real      = blocs réellement alloués sur disque
  apparent  = taille logique du fichier

${BOLD}Attention${NC}
  - le tri mtime porte sur la date du dossier lui-même
  - la profondeur max ne concerne que le scan récursif des fichiers
  - ce script cible GNU/Linux
  - les exclusions utilisateur sont littérales, pas des globs
EOF2
  pause_screen
}

show_exclusions_screen() {
  [[ "$TUI_CAPABLE" -eq 0 && -t 1 ]] && clear
  echo -e "${BOLD}${BLUE}EXCLUSIONS CONFIGURÉES${NC}"
  echo
  if ((${#EXCLUDED_DIRS[@]} == 0)); then
    echo "Aucune exclusion configurée."
  else
    local i=1
    local path
    for path in "${EXCLUDED_DIRS[@]}"; do
      printf '  %2d) %s\n' "$i" "$path"
      ((i++))
    done
  fi
  pause_screen
}

add_exclusion_interactive() {
  local input resolved
  echo
  read -r -p "Chemin à exclure (absolu ou relatif au dossier courant) : " input
  [[ -z "$input" ]] && return 0

  if contains_glob_meta "$input"; then
    LAST_WARNING="les métacaractères de glob (* ? [ ]) ne sont pas autorisés"
    return 0
  fi

  if [[ "$input" != /* ]]; then
    input="${CURRENT_DIR%/}/$input"
  fi

  # Même stratégie que pour la CLI : chemin physique si possible, sinon
  # résolution lexicale pour conserver une exclusion cohérente avec CURRENT_DIR.
  resolved="$(normalize_dir "$input" 2>/dev/null)" || resolved="$(resolve_path_lexical "$input")"

  local existing
  for existing in "${EXCLUDED_DIRS[@]}"; do
    if [[ "$existing" == "$resolved" ]]; then
      LAST_WARNING="exclusion déjà présente"
      return 0
    fi
  done

  if [[ ! -e "$resolved" ]]; then
    LAST_WARNING="exclusion anticipée ajoutée : $resolved"
    EXCLUDED_DIRS+=("$resolved")
    return 0
  fi

  LAST_WARNING="exclusion ajoutée : $resolved"
  EXCLUDED_DIRS+=("$resolved")
}

remove_exclusion_interactive() {
  if ((${#EXCLUDED_DIRS[@]} == 0)); then
    LAST_WARNING="aucune exclusion à supprimer"
    return 0
  fi

  [[ "$TUI_CAPABLE" -eq 0 && -t 1 ]] && clear
  echo -e "${BOLD}${BLUE}SUPPRIMER UNE EXCLUSION${NC}"
  echo

  local i=1
  local path
  for path in "${EXCLUDED_DIRS[@]}"; do
    printf '  %2d) %s\n' "$i" "$path"
    ((i++))
  done

  echo
  local choice idx
  read -r -p "Numéro à supprimer (0 pour annuler) : " choice || return 0

  [[ "$choice" =~ ^[0-9]+$ ]] || {
    LAST_WARNING="saisie invalide"
    return 0
  }

  (( choice == 0 )) && return 0
  idx=$((choice - 1))

  if [[ -z "${EXCLUDED_DIRS[$idx]-}" ]]; then
    LAST_WARNING="sélection hors plage"
    return 0
  fi

  LAST_WARNING="exclusion supprimée : ${EXCLUDED_DIRS[$idx]}"
  unset 'EXCLUDED_DIRS[idx]'
  EXCLUDED_DIRS=("${EXCLUDED_DIRS[@]}")
}

# set_numeric_interactive var_name prompt validation_func min max warning_label [display_func]
set_numeric_interactive() {
  # shellcheck disable=SC2178
  local -n __sni_var_ref="$1"
  local __sni_prompt="$2"
  local __sni_val_func="$3"
  local __sni_min_val="$4"
  local __sni_max_val="$5"
  local __sni_label="$6"
  local __sni_disp_func="${7-}"
  local __sni_value

  read -r -p "$__sni_prompt" __sni_value
  [[ -z "$__sni_value" ]] && return 0

  "$__sni_val_func" "$__sni_value" || {
    LAST_WARNING="valeur invalide"
    return 0
  }

  (( __sni_value >= __sni_min_val )) || {
    LAST_WARNING="la valeur doit être >= $__sni_min_val"
    return 0
  }

  (( __sni_value <= __sni_max_val )) || {
    LAST_WARNING="la valeur doit être <= $__sni_max_val"
    return 0
  }

  __sni_var_ref="$__sni_value"
  if [[ -n "$__sni_disp_func" ]]; then
    LAST_WARNING="$__sni_label = $("$__sni_disp_func")"
  else
    LAST_WARNING="$__sni_label = $__sni_var_ref"
  fi
}

set_top_count_interactive() {
  set_numeric_interactive TOP_COUNT "Nouveau top sous-dossiers : " is_non_negative_int 0 "$MAX_ALLOWED_RESULTS" "top sous-dossiers"
}

set_top_files_interactive() {
  set_numeric_interactive TOP_FILES_COUNT "Nouveau top fichiers : " is_non_negative_int 0 "$MAX_ALLOWED_RESULTS" "top fichiers"
}

set_max_depth_interactive() {
  set_numeric_interactive MAX_DEPTH "Nouvelle profondeur max (-1 = illimitée) : " is_integer -1 "$MAX_ALLOWED_DEPTH" "profondeur max" depth_label
}

config_menu() {
  local choice
  while true; do
    [[ "$TUI_CAPABLE" -eq 0 && -t 1 ]] && clear
    echo -e "${BOLD}${BLUE}CONFIGURATION${NC}"
    echo
    echo "  1) Mode analyse        : $(analysis_label)"
    echo "  2) Tri                 : $SORT_MODE"
    echo "  3) Taille fichiers     : $FILE_SIZE_MODE"
    echo "  4) Top sous-dossiers   : $TOP_COUNT"
    echo "  5) Top fichiers        : $TOP_FILES_COUNT"
    echo "  6) Profondeur max      : $(depth_label)"
    echo "  7) Voir exclusions"
    echo "  8) Ajouter exclusion"
    echo "  9) Supprimer exclusion"
    echo "  0) Retour"
    echo
    [[ -n "$LAST_WARNING" ]] && echo -e "${YELLOW}$LAST_WARNING${NC}\n"

    read -r -p "Choix > " choice || break

    case "$choice" in
      0) break ;;
      1)
        [[ "$ANALYSIS_MODE" == "partition" ]] && ANALYSIS_MODE="global" || ANALYSIS_MODE="partition"
        LAST_WARNING="mode analyse = $ANALYSIS_MODE"
        ;;
      2)
        [[ "$SORT_MODE" == "size" ]] && SORT_MODE="mtime" || SORT_MODE="size"
        LAST_WARNING="tri = $SORT_MODE"
        ;;
      3)
        [[ "$FILE_SIZE_MODE" == "real" ]] && FILE_SIZE_MODE="apparent" || FILE_SIZE_MODE="real"
        LAST_WARNING="mode taille fichiers = $FILE_SIZE_MODE"
        ;;
      4) set_top_count_interactive ;;
      5) set_top_files_interactive ;;
      6) set_max_depth_interactive ;;
      7) show_exclusions_screen ;;
      8) add_exclusion_interactive; refresh_active_exclusions ;;
      9) remove_exclusion_interactive; refresh_active_exclusions ;;
      *) LAST_WARNING="commande invalide" ;;
    esac
  done
}

show_header() {
  [[ -t 1 ]] && clear

  echo -e "${BOLD}${BLUE}┌──────────────────── DISK EXPLORER ────────────────────┐${NC}"
  echo -e "  Dossier : ${YELLOW}${CURRENT_DIR}${NC}"

  local df_fields
  _tui_refresh_df_cache
  df_fields="$_TUI_DF_CACHE_VAL"

  if [[ -n "$df_fields" ]]; then
    local size used avail use_p mounted use_pct fields
    IFS=$'\t' read -r fields mounted <<< "$df_fields"
    read -r size used avail use_p <<< "$fields"
    use_pct="${use_p%\%}"

    local color=$GREEN
    if [[ "$use_pct" =~ ^[0-9]+$ ]]; then
      (( use_pct > 85 )) && color=$YELLOW
      (( use_pct > 95 )) && color=$RED
    fi

    echo -e "  Mode analyse : ${CYAN}$(analysis_label)${NC}"
    echo -e "  Partition    : ${BOLD}${mounted}${NC} | Total: $(human_size "$size") | Libre: $(human_size "$avail") | Occ: ${color}${use_p}${NC}"
  else
    echo -e "  Mode analyse : ${CYAN}$(analysis_label)${NC}"
  fi

  refresh_active_exclusions
  echo -e "  Mode fichiers: ${CYAN}$(file_size_label)${NC}"
  echo -e "  Tri          : ${CYAN}${SORT_MODE}${NC} | Profondeur scan fichiers : ${CYAN}$(depth_label)${NC}"
  echo -e "  Exclusions   : ${CYAN}${#ACTIVE_EXCLUDED_DIRS[@]} actives${NC} / ${CYAN}${#EXCLUDED_DIRS[@]} configurées${NC}"

  if [[ -n "$LAST_WARNING" ]]; then
    echo -e "  ${YELLOW}Avertissement :${NC} ${LAST_WARNING}"
  fi

  echo -e "${BLUE}└──────────────────────────────────────────────────────────┘${NC}"
}

process_line_display() {
  local full_path="$1"
  local mode="$2"
  local raw_data="$3"
  local idx="$4"
  local entry_type="${5:-d}"

  local rel_path="${full_path#"${CURRENT_DIR}/"}"
  [[ -z "$rel_path" || "$rel_path" == "$full_path" ]] && rel_path=$(basename -- "$full_path")
  SUBDIR_PATHS+=("$full_path")
  SUBDIR_DATA+=("$raw_data")
  SUBDIR_TYPES+=("$entry_type")

  local display_metric
  if [[ "$mode" == "size" ]]; then
    display_metric="$(human_size "$raw_data")"
  else
    display_metric="$(date_from_epoch "$raw_data")"
  fi

  local safe_rel_path
  safe_rel_path="$(sanitize_for_display "$rel_path")"
  [[ "$entry_type" == "d" ]] && safe_rel_path="${safe_rel_path}/"

  if is_heavy_known "$rel_path"; then
    printf '  %b%2d)%b  %12s   %b%b%s%b\n' "$BOLD" "$idx" "$NC" "$display_metric" "$MAGENTA" "$BOLD" "$safe_rel_path" "$NC"
  else
    printf '  %b%2d)%b  %12s   %s\n' "$BOLD" "$idx" "$NC" "$display_metric" "$safe_rel_path"
  fi
}

# Scanne les fichiers (non récursif, depth 1) dans CURRENT_DIR.
# Sortie : NUL-délimitée, format "valeur\tchemin" (taille apparente ou mtime).
_tui_scan_shallow_files() {
  local out_file="$1"
  local err_file="$2"
  local -a find_cmd
  build_find_prefix find_cmd 1
  if [[ "$SORT_MODE" == "mtime" ]]; then
    "${find_cmd[@]}" -type f -printf '%T@\t%p\0'
  else
    "${find_cmd[@]}" -type f -printf '%s\t%p\0'
  fi >"$out_file" 2>"$err_file" || true
  update_scan_warning "$err_file" "Analyse fichiers partielle" "$?"
}

_TUI_SCAN_PID=0
_TUI_SCAN_RESULT_FILE=""
_TUI_SCAN_DONE_FILE=""
_TUI_SCAN_WARNING_FILE=""

# Scanne et écrit dans un fichier temporaire (NUL-délimité, "valeur\ttype:chemin").
_tui_scan_to_file() {
  local out_file="$1"
  local tmp_dirs err_dirs tmp_files err_files
  tmp_dirs=$(make_temp_file)   || return 1
  err_dirs=$(make_temp_file)   || return 1
  tmp_files=$(make_temp_file)  || return 1
  err_files=$(make_temp_file)  || return 1

  if [[ "$DEBUG_TUI" -eq 1 ]]; then
    printf "[DEBUG] Starting _tui_scan_to_file at %s\n" "$(date)" >> ~/disk-explorer.debug
    printf "[DEBUG] out_file: %s\n" "$out_file" >> ~/disk-explorer.debug
  fi

  scan_subdirs_to_file "$tmp_dirs" "$err_dirs"
  local sub_rc=$?
  local warn="$SCAN_WARNING"

  _tui_scan_shallow_files "$tmp_files" "$err_files"
  local file_rc=$?
  [[ -n "$SCAN_WARNING" && -z "$warn" ]] && warn="$SCAN_WARNING"

  if [[ "$DEBUG_TUI" -eq 1 ]]; then
    printf "[DEBUG] _tui_scan_shallow_files rc: %d\n" "$file_rc" >> ~/disk-explorer.debug
    printf "[DEBUG] _tui_scan_shallow_files stdout size: %d\n" "$(wc -c < "$tmp_files")" >> ~/disk-explorer.debug
    printf "[DEBUG] _tui_scan_shallow_files stderr: %s\n" "$(cat "$err_files")" >> ~/disk-explorer.debug
  fi

  # Fusion résiliente sans awk -v RS='\0'
  : > "$out_file"
  while IFS=$'\t' read -r -d '' val path; do
    printf '%s\td:%s\0' "$val" "$path" >> "$out_file"
  done < "$tmp_dirs"
  while IFS=$'\t' read -r -d '' val path; do
    printf '%s\tf:%s\0' "$val" "$path" >> "$out_file"
  done < "$tmp_files"

  # Tri final
  local tmp_sorted; tmp_sorted=$(make_temp_file)
  LC_ALL=C "$SORT_CMD" -zrn "$out_file" | "$HEAD_CMD" -z -n "$TOP_COUNT" > "$tmp_sorted"
  mv -f -- "$tmp_sorted" "$out_file"

  if [[ "$DEBUG_TUI" -eq 1 ]]; then
    printf "[DEBUG] final merged size: %d\n" "$(wc -c < "$out_file")" >> ~/disk-explorer.debug
  fi

  # On sauve le warning dans un fichier si précisé, sinon variable globale
  if [[ -n "${_TUI_SCAN_WARNING_FILE-}" ]]; then
    echo -n "$warn" > "$_TUI_SCAN_WARNING_FILE"
  else
    SCAN_WARNING="$warn"
  fi

  rm -f -- "$tmp_dirs" "$err_dirs" "$tmp_files" "$err_files"
}

show_heavy_subdirs() {
  SUBDIR_PATHS=()
  SUBDIR_DATA=()
  SUBDIR_TYPES=()
  LAST_WARNING=""

  local tmp_merged
  tmp_merged=$(make_temp_file) || return 1

  echo -e "\n${CYAN}${BOLD}Analyse des sous-dossiers...${NC}"

  _tui_scan_to_file "$tmp_merged"
  LAST_WARNING="$SCAN_WARNING"

  local i=0 line val tagged entry_type full_path mode
  [[ "$SORT_MODE" == "mtime" ]] && mode="mtime" || mode="size"
  while IFS= read -r -d '' line; do
    val="${line%%$'\t'*}"
    tagged="${line#*$'\t'}"       # "d:/path" ou "f:/path"
    entry_type="${tagged:0:1}"    # "d" ou "f"
    full_path="${tagged:2}"
    [[ -z "$full_path" || "$entry_type" != [df] ]] && continue
    process_line_display "$full_path" "$mode" "$val" "$((++i))" "$entry_type"
  done < "$tmp_merged"

  rm -f -- "$tmp_merged"
  if (( i == 0 )); then echo -e "  ${DIM}Aucun élément affichable.${NC}"; fi
}


show_heavy_files() {
  LAST_WARNING=""

  [[ "$TUI_CAPABLE" -eq 0 && -t 1 ]] && clear
  echo -e "${BOLD}${BLUE}===== TOP FILES (Récursif) =====${NC}"
  echo -e "Source : ${YELLOW}${CURRENT_DIR}${NC}"
  echo -e "Mode analyse : ${CYAN}$(analysis_label)${NC}"
  echo -e "Recherche des ${TOP_FILES_COUNT} plus gros fichiers...\n"

  local tmp_file err_file
  tmp_file=$(make_temp_file) || return 1
  err_file=$(make_temp_file) || return 1

  scan_top_files_to_file "$tmp_file" "$err_file"
  LAST_WARNING="$SCAN_WARNING"

  if [[ -n "$LAST_WARNING" ]]; then
    echo -e "${YELLOW}${LAST_WARNING}${NC}\n"
  fi

  local found=0
  local line value path size_bytes
  while IFS= read -r -d '' line; do
    value="${line%%$'\t'*}"
    path="${line#*$'\t'}"
    [[ -z "$path" || "$path" == "$line" ]] && continue

    if [[ "$FILE_SIZE_MODE" == "apparent" ]]; then
      size_bytes="$value"
    else
      size_bytes=$((value * 512))
    fi

    printf '%12s   %s\n' "$(human_size "$size_bytes")" "$(sanitize_for_display "$path")"
    found=1
  done < "$tmp_file"

  rm -f -- "$tmp_file" "$err_file"

  (( found == 0 )) && echo -e "${DIM}Aucun fichier trouvé.${NC}"

  pause_screen
}

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

tui_exit() {
  _tui_stop_background_scan
  trap - SIGWINCH
  stty echo cooked 2>/dev/null || true
  tput cnorm 2>/dev/null || true
  tput rmcup 2>/dev/null || true
}

tui_enter() {
  tput smcup 2>/dev/null || true
  tput civis 2>/dev/null || true
  stty -echo raw 2>/dev/null || true
  trap 'tui_exit; cleanup' EXIT
  trap '_NEEDS_REDRAW=1' SIGWINCH
}

# Lit une touche ou séquence d'échappement.
# Écrit dans _LAST_KEY (variable globale) pour éviter que $() ne strippé \n.
# -N1 (majuscule) : lit exactement 1 char sans traiter \n comme délimiteur.
_LAST_KEY=""
read_key() {
  local seq
  _LAST_KEY=""
  IFS= read -r -s -t 0.2 -N1 _LAST_KEY 2>/dev/null || true
  if [[ "$_LAST_KEY" == $'\x1b' ]]; then
    IFS= read -r -s -t 0.1 -n5 seq 2>/dev/null || true
    _LAST_KEY="${_LAST_KEY}${seq}"
  fi
}

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
  _tui_refresh_df_cache
  df_fields="$_TUI_DF_CACHE_VAL"

  if [[ -n "$df_fields" ]]; then
    IFS=$'\t' read -r fields mounted <<< "$df_fields"
    read -r size used avail use_p <<< "$fields"
  else
    size=0; used=0; avail=0; use_p="?%"; mounted="?"
  fi

  # Ligne 1 : titre + chemin + mode
  local line1_plain="DISK EXPLORER  ${CURRENT_DIR}  $(analysis_label) · $SORT_MODE"
  printf '%s\r\n' "$(_tui_pad "$line1_plain" "$COLUMNS")"

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
  printf '%s\r\n' "$(_tui_pad "$line2_plain" "$COLUMNS")"

  # Séparateur haut
  local sep=""
  for (( i=0; i<COLUMNS; i++ )); do sep+="─"; done
  printf '%s\r\n' "${sep:0:$COLUMNS}"
}

# ── Zone liste avec viewport et curseur ──────────────────────────────────

_TUI_LIST_MAX_VAL=0

_tui_precompute_list_stats() {
  _TUI_LIST_MAX_VAL=0
  local total=${#SUBDIR_PATHS[@]}
  local i
  if [[ "$SORT_MODE" != "mtime" ]]; then
    for (( i=0; i<total; i++ )); do
      local _d="${SUBDIR_DATA[$i]:-0}"
      [[ "$_d" == "__dotdot__" || ! "$_d" =~ ^[0-9]+$ ]] && continue
      (( _d > _TUI_LIST_MAX_VAL )) && _TUI_LIST_MAX_VAL=$_d
    done
  fi
}

_tui_draw_row() {
  local i="$1"     # Index global dans SUBDIR_PATHS
  local row="$2"   # Ligne écran (0 = première ligne de la zone liste)
  local total=${#SUBDIR_PATHS[@]}
  local bar_width=10

  tput cup $(( 3 + row )) 0 2>/dev/null || true

  if (( i >= total )); then
    printf '%s' "$(_tui_pad "" "$COLUMNS")"
    return
  fi

  local full_path="${SUBDIR_PATHS[$i]}"
  local raw_data="${SUBDIR_DATA[$i]:-0}"
  local entry_type="${SUBDIR_TYPES[$i]:-d}"
  local metric safe_name bar_str

  if [[ "$raw_data" == "__dotdot__" ]]; then
    safe_name="../"
    metric="            "
    bar_str="          "
  else
    local rel_path="${full_path#"${CURRENT_DIR}/"}"
    [[ "$rel_path" == "$full_path" ]] && rel_path="$(basename -- "$full_path")"
    safe_name="$(sanitize_for_display "$rel_path")"
    [[ "$entry_type" == "d" ]] && safe_name="${safe_name}/"
    if [[ "$SORT_MODE" == "mtime" ]]; then
      metric="$(date_from_epoch "$raw_data")"
      bar_str="          "
    else
      metric="$(human_size "$raw_data")"
      local val="${raw_data:-0}"
      [[ "$val" =~ ^[0-9]+$ ]] || val=0
      local fill=0
      (( _TUI_LIST_MAX_VAL > 0 )) && fill=$(( bar_width * val / _TUI_LIST_MAX_VAL ))
      (( fill > bar_width )) && fill=$bar_width
      local empty=$(( bar_width - fill ))
      bar_str=""
      local k
      for (( k=0; k<fill;  k++ )); do bar_str+="█"; done
      for (( k=0; k<empty; k++ )); do bar_str+="░"; done
    fi
  fi

  local name_max=$(( COLUMNS - 35 ))
  (( name_max < 5 )) && name_max=5
  if (( ${#safe_name} > name_max )); then
    safe_name="${safe_name:0:$((name_max-1))}…"
  fi

  local line_text
  printf -v line_text '  %2d)  %12s  %s  %s' "$((i+1))" "$metric" "$bar_str" "$safe_name"

  if (( i == CURSOR )); then
    printf '%s%s%s' "$(tput rev 2>/dev/null || true)" \
      "$(_tui_pad "$line_text" "$COLUMNS")" \
      "$(tput sgr0 2>/dev/null || true)"
  else
    printf '%s' "$(_tui_pad "$line_text" "$COLUMNS")"
  fi
}

draw_list() {
  local visible=$(( LINES - 6 ))
  (( visible < 1 )) && visible=1
  local total=${#SUBDIR_PATHS[@]}
  local i row=0

  _tui_precompute_list_stats

  if (( total == 0 )); then
    tput cup 3 0 2>/dev/null || true
    printf '%s\r\n' "$(_tui_pad "  Aucun sous-dossier accessible." "$COLUMNS")"
    (( row++ ))
  fi

  for (( i=SCROLL_OFFSET; i<total && row<visible; i++, row++ )); do
    _tui_draw_row "$i" "$row"
    printf '\r\n'
  done

  # Remplir les lignes vides restantes
  while (( row < visible )); do
    tput cup $(( 3 + row )) 0 2>/dev/null || true
    printf '%s\r\n' "$(_tui_pad "" "$COLUMNS")"
    (( row++ ))
  done

  # Indicateur de scroll si des entrées sont hors champ
  local remaining=$(( total - SCROLL_OFFSET - visible ))
  if (( remaining > 0 )); then
    tput cup $(( 3 + visible - 1 )) 0 2>/dev/null || true
    local hint="  ↓ $remaining autre(s)…"
    printf '%s\r\n' "$(_tui_pad "$hint" "$COLUMNS")"
  fi
}

# ── Footer (séparateur + 2 lignes de raccourcis) ─────────────────────────

draw_footer() {
  tput cup $(( LINES - 3 )) 0 2>/dev/null || true
  local sep="" i
  for (( i=0; i<COLUMNS; i++ )); do sep+="─"; done
  printf '%s\r\n' "${sep:0:$COLUMNS}"
  local n="${#SUBDIR_PATHS[@]}"
  local f1="  [↑↓] naviguer  [Entrée] ouvrir  [d] supprimer  [1-$n] accès direct  [0] retour"
  local -a bindings=("[s] tri" "[a] taille" "[f] fichiers" "[r] rapport" "[h] aide" "[c] config" "[q] quitter")
  local f2="  " sep2="" b candidate
  for b in "${bindings[@]}"; do
    candidate="${f2}${sep2}${b}"
    (( ${#candidate} > COLUMNS )) && break
    f2="$candidate"
    sep2=" "
  done
  printf '%s\r\n' "$(_tui_pad "$f1" "$COLUMNS")"
  printf '%s' "$(_tui_pad "$f2" "$COLUMNS")"
}

# ── Curseur ────────────────────────────────────────────────────────────────

cursor_up() {
  local old_cursor=$CURSOR
  (( CURSOR > 0 )) && (( CURSOR-- )) || return 0
  
  if (( CURSOR < SCROLL_OFFSET )); then
    (( SCROLL_OFFSET-- ))
    _NEEDS_REDRAW=1
  else
    _tui_precompute_list_stats
    _tui_draw_row "$old_cursor" "$(( old_cursor - SCROLL_OFFSET ))"
    _tui_draw_row "$CURSOR" "$(( CURSOR - SCROLL_OFFSET ))"
  fi
}

cursor_down() {
  local visible=$(( LINES - 6 ))
  (( visible < 1 )) && visible=1
  local total=${#SUBDIR_PATHS[@]}
  local last=$(( total - 1 ))
  (( last < 0 )) && return 0
  
  local old_cursor=$CURSOR
  (( CURSOR < last )) && (( CURSOR++ )) || return 0
  
  if (( CURSOR >= SCROLL_OFFSET + visible )); then
    (( SCROLL_OFFSET++ ))
    _NEEDS_REDRAW=1
  else
    _tui_precompute_list_stats
    _tui_draw_row "$old_cursor" "$(( old_cursor - SCROLL_OFFSET ))"
    _tui_draw_row "$CURSOR" "$(( CURSOR - SCROLL_OFFSET ))"
  fi
}

cursor_reset() {
  CURSOR=0
  SCROLL_OFFSET=0
}

# ── Redessin complet ──────────────────────────────────────────────────────

tui_draw() {
  LINES=$(tput lines 2>/dev/null || echo 24)
  COLUMNS=$(tput cols  2>/dev/null || echo 80)
  tput cup 0 0 2>/dev/null || true
  tput ed      2>/dev/null || true   # efface jusqu'à fin d'écran (gère rétrécissement)

  if (( LINES < 9 )); then
    printf 'Terminal trop petit (%d lignes). Agrandissez la fenêtre.\r\n' "$LINES"
    _NEEDS_REDRAW=0
    return
  fi

  draw_header
  draw_list
  tput cup $(( LINES - 3 )) 0 2>/dev/null || true
  draw_footer

  _NEEDS_REDRAW=0   # remis à 0 APRÈS le dessin complet
}

navigate_legacy() {
  while true; do
    show_header
    show_heavy_subdirs

    echo -e "\n${DIM}──────────────────────────────────────────────────────────${NC}"
    echo -e "  [1-${#SUBDIR_PATHS[@]}] Entrer dans un dossier   [0] Retour arrière"
    echo -e "  [s] Changer tri       [a] Taille fichiers (réel/apparent)   [p] Mode (partition/global)"
    echo -e "  [f] Top Fichiers      [e] Exclusions   [r] Rapport      [h/?] Aide      [c] Config      [q] Quitter"
    echo -e "${DIM}──────────────────────────────────────────────────────────${NC}"

    local choice idx target prev_dir
    read -r -p "Action > " choice || { echo; exit 0; }

    case "${choice,,}" in
      q)
        [[ -t 1 ]] && clear
        exit 0
        ;;
      s)
        [[ "$SORT_MODE" == "size" ]] && SORT_MODE="mtime" || SORT_MODE="size"
        ;;
      a)
        [[ "$FILE_SIZE_MODE" == "real" ]] && FILE_SIZE_MODE="apparent" || FILE_SIZE_MODE="real"
        ;;
      p)
        [[ "$ANALYSIS_MODE" == "partition" ]] && ANALYSIS_MODE="global" || ANALYSIS_MODE="partition"
        ;;
      f)
        show_heavy_files
        ;;
      e)
        show_exclusions_screen
        ;;
      r)
        generate_report_file
        ;;
      h|\?)
        show_help_screen
        ;;
      c)
        config_menu
        ;;
      *)
        if [[ "$choice" =~ ^[0-9]+$ ]]; then
          if (( choice == 0 )); then
            if [[ "$CURRENT_DIR" != "/" ]]; then
              set_current_dir "$(dirname -- "$CURRENT_DIR")" || LAST_WARNING="retour arrière impossible"
            fi
          else
            idx=$((choice - 1))
            if [[ -n "${SUBDIR_PATHS[$idx]-}" ]]; then
              target="${SUBDIR_PATHS[$idx]}"
              prev_dir="$CURRENT_DIR"
              set_current_dir "$target" || {
                LAST_WARNING="navigation impossible vers le dossier demandé"
                CURRENT_DIR="$prev_dir"
                continue
              }
            else
              LAST_WARNING="sélection hors plage"
            fi
          fi
        else
          LAST_WARNING="commande invalide"
        fi
        ;;
    esac
  done
}

# Supprime l'entrée sous le curseur après confirmation inline dans le footer.
_tui_delete_selected() {
  (( ${#SUBDIR_PATHS[@]} == 0 || CURSOR >= ${#SUBDIR_PATHS[@]} )) && return
  [[ "${SUBDIR_DATA[$CURSOR]:-}" == "__dotdot__" ]] && return

  local path="${SUBDIR_PATHS[$CURSOR]}"
  local entry_type="${SUBDIR_TYPES[$CURSOR]:-d}"

  # Gardes de sécurité : chemin vide, égal à CURRENT_DIR, ou hors de CURRENT_DIR
  if [[ -z "$path" ]]; then
    LAST_WARNING="suppression refusée : chemin vide"
    _NEEDS_REDRAW=1; return
  fi
  if [[ "$path" == "$CURRENT_DIR" ]]; then
    LAST_WARNING="suppression refusée : répertoire courant"
    _NEEDS_REDRAW=1; return
  fi
  if ! path_is_equal_or_within "$path" "$CURRENT_DIR"; then
    LAST_WARNING="suppression refusée : chemin hors du répertoire courant"
    _NEEDS_REDRAW=1; return
  fi

  local display_name
  display_name="$(sanitize_for_display "$(basename -- "$path")")"
  [[ "$entry_type" == "d" ]] && display_name="${display_name}/"

  # Confirmation dans la dernière ligne du footer (sans quitter le buffer TUI)
  tput cup $(( LINES - 1 )) 0 2>/dev/null || true
  local prompt="  Supprimer \"${display_name}\" ? [y/N] "
  printf '%s' "$(_tui_pad "$prompt" "$COLUMNS")"
  tput cup $(( LINES - 1 )) ${#prompt} 2>/dev/null || true
  stty echo cooked 2>/dev/null || true
  local answer=""
  IFS= read -r answer 2>/dev/null || answer=""
  stty -echo raw 2>/dev/null || true

  if [[ "${answer,,}" == "y" ]]; then
    local rm_ok=0 rm_msg=""
    if [[ "$entry_type" == "d" ]]; then
      rm_msg=$(rm -rf -- "$path" 2>&1) && rm_ok=1 || true
    else
      rm_msg=$(rm -f -- "$path" 2>&1) && rm_ok=1 || true
    fi
    if (( rm_ok )); then
      LAST_WARNING="\"${display_name}\" supprimé"
      _tui_reload_subdirs
      # Ajuster le curseur si hors plage après rechargement
      (( ${#SUBDIR_PATHS[@]} > 0 && CURSOR >= ${#SUBDIR_PATHS[@]} )) && (( CURSOR = ${#SUBDIR_PATHS[@]} - 1 )) || true
    else
      local err_detail
      err_detail="$(sanitize_for_display "${rm_msg:-permission refusée}")"
      LAST_WARNING="échec suppression : ${err_detail}"
    fi
  fi
  _NEEDS_REDRAW=1
}

_tui_stop_background_scan() {
  if [[ "$_TUI_SCAN_PID" -gt 0 ]]; then
    kill -9 "$_TUI_SCAN_PID" 2>/dev/null || true
    wait "$_TUI_SCAN_PID" 2>/dev/null || true
  fi
  _TUI_SCAN_PID=0
  [[ -f "$_TUI_SCAN_RESULT_FILE" ]] && rm -f "$_TUI_SCAN_RESULT_FILE"
  [[ -f "$_TUI_SCAN_DONE_FILE" ]]   && rm -f "$_TUI_SCAN_DONE_FILE"
  [[ -f "$_TUI_SCAN_WARNING_FILE" ]] && rm -f "$_TUI_SCAN_WARNING_FILE"
}

_TUI_PRESERVE_PATH=""

_tui_check_scan_completion() {
  [[ "$_TUI_SCAN_PID" -eq 0 ]] && return 0
  [[ ! -f "$_TUI_SCAN_DONE_FILE" ]] && return 0

  local line val tagged entry_type full_path
  SUBDIR_PATHS=()
  SUBDIR_DATA=()
  SUBDIR_TYPES=()
  
  # Charger les résultats du scan fini
  while IFS= read -r -d '' line; do
    val="${line%%$'\t'*}"
    tagged="${line#*$'\t'}"
    entry_type="${tagged:0:1}"
    full_path="${tagged:2}"
    [[ -z "$full_path" || "$entry_type" != [df] ]] && continue
    SUBDIR_PATHS+=("$full_path")
    SUBDIR_DATA+=("$val")
    SUBDIR_TYPES+=("$entry_type")
  done < "$_TUI_SCAN_RESULT_FILE"

  # Charger le warning s'il existe
  if [[ -f "$_TUI_SCAN_WARNING_FILE" ]]; then
    LAST_WARNING="$(cat "$_TUI_SCAN_WARNING_FILE")"
  fi

  # Ajouter ".."
  if [[ "$CURRENT_DIR" != "/" ]]; then
    SUBDIR_PATHS=("$(dirname -- "$CURRENT_DIR")" "${SUBDIR_PATHS[@]+"${SUBDIR_PATHS[@]}"}")
    SUBDIR_DATA=("__dotdot__" "${SUBDIR_DATA[@]+"${SUBDIR_DATA[@]}"}")
    SUBDIR_TYPES=("d" "${SUBDIR_TYPES[@]+"${SUBDIR_TYPES[@]}"}")
  fi

  # Tenter de restaurer le curseur si demandé
  if [[ -n "$_TUI_PRESERVE_PATH" ]]; then
    local i
    for (( i=0; i<${#SUBDIR_PATHS[@]}; i++ )); do
      if [[ "${SUBDIR_PATHS[$i]}" == "$_TUI_PRESERVE_PATH" ]]; then
        CURSOR=$i
        # Ajuster le scroll si nécessaire
        local visible=$(( LINES - 6 ))
        (( visible < 1 )) && visible=1
        if (( CURSOR < SCROLL_OFFSET || CURSOR >= SCROLL_OFFSET + visible )); then
          SCROLL_OFFSET=$(( CURSOR - (visible / 2) ))
          (( SCROLL_OFFSET < 0 )) && SCROLL_OFFSET=0
        fi
        break
      fi
    done
    _TUI_PRESERVE_PATH=""
  fi

  _tui_stop_background_scan
  _NEEDS_REDRAW=1
  return 1 # Signale que le scan est fini
}


_TUI_SPINNER_FRAMES=("⠋" "⠙" "⠹" "⠸" "⠼" "⠴" "⠦" "⠧" "⠇" "⠏")
_TUI_SPINNER_IDX=0

_tui_draw_loading_indicator() {
  local frame="${_TUI_SPINNER_FRAMES[$_TUI_SPINNER_IDX]}"
  tput cup 3 0 2>/dev/null || true
  printf '  %b%s%b  Analyse en cours…' "$CYAN" "$frame" "$NC"
  _TUI_SPINNER_IDX=$(( (_TUI_SPINNER_IDX + 1) % ${#_TUI_SPINNER_FRAMES[@]} ))
}

# Recharge SUBDIR_PATHS/SUBDIR_DATA. En mode TUI, lance un scan ASYNC.
# $1: optionnel, chemin à tenter de resélectionner après le scan.
_tui_reload_subdirs() {
  _TUI_DF_CACHE_VAL=""
  LAST_WARNING=""
  _TUI_PRESERVE_PATH="${1-}"
  
  if [[ "$TUI_CAPABLE" -eq 0 ]]; then
    # Mode synchrone (legacy / summary)
    SUBDIR_PATHS=()
    SUBDIR_DATA=()
    SUBDIR_TYPES=()
    show_heavy_subdirs >/dev/null 2>/dev/null
    [[ -n "$SCAN_WARNING" ]] && LAST_WARNING="$SCAN_WARNING" || true
    if [[ "$CURRENT_DIR" != "/" ]]; then
      SUBDIR_PATHS=("$(dirname -- "$CURRENT_DIR")" "${SUBDIR_PATHS[@]+"${SUBDIR_PATHS[@]}"}")
      SUBDIR_DATA=("__dotdot__" "${SUBDIR_DATA[@]+"${SUBDIR_DATA[@]}"}")
      SUBDIR_TYPES=("d" "${SUBDIR_TYPES[@]+"${SUBDIR_TYPES[@]}"}")
    fi
    return
  fi

  # Mode TUI : Asynchrone
  _tui_stop_background_scan
  
  _TUI_SCAN_RESULT_FILE=$(make_temp_file) || return 1
  _TUI_SCAN_DONE_FILE=$(make_temp_file)   || return 1
  _TUI_SCAN_WARNING_FILE=$(make_temp_file) || return 1
  
  # Feedback immédiat
  SUBDIR_PATHS=()
  SUBDIR_DATA=()
  SUBDIR_TYPES=()
  _TUI_SPINNER_IDX=0
  _tui_draw_loading_indicator

  # Lancer le scan en background
  # On force ENABLE_SPINNER=0 pour éviter que les fonctions de scan
  # n'essaient d'afficher un spinner sur stderr (conflit avec le TUI).
  (
    ENABLE_SPINNER=0
    if [[ "${DEBUG_TUI:-0}" -eq 1 ]]; then
      printf "[DEBUG] BACKGROUND JOB STARTING for %s\n" "$CURRENT_DIR" >> ~/disk-explorer.debug
    fi
    _tui_scan_to_file "$_TUI_SCAN_RESULT_FILE"
    local res=$?
    touch "$_TUI_SCAN_DONE_FILE"
    if [[ "${DEBUG_TUI:-0}" -eq 1 ]]; then
      printf "[DEBUG] BACKGROUND JOB FINISHED with rc %d\n" "$res" >> ~/disk-explorer.debug
    fi
  ) &
  _TUI_SCAN_PID=$!
}



# Lance un écran secondaire dans le buffer alternatif actif.
# Restaure cooked avant l'appel (pour les read -r -p dans config/exclusions).
# Force _NEEDS_REDRAW=1 au retour pour que la vue principale se redessine.
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

_tui_show_report_result() {
  echo
  if [[ -n "$LAST_WARNING" ]]; then
    printf '%s\n' "$LAST_WARNING"
  else
    echo "Rapport généré."
  fi
  pause_screen
}

# ── Boucle principale TUI ─────────────────────────────────────────────────

navigate() {
  tui_check_capability
  if [[ "$TUI_CAPABLE" -eq 0 ]]; then
    navigate_legacy
    return
  fi

  tui_enter
  LINES=$(tput lines 2>/dev/null || echo 24)
  COLUMNS=$(tput cols  2>/dev/null || echo 80)
  tput cup 0 0 2>/dev/null || true
  tput ed      2>/dev/null || true
  printf '%s\r\n' "$(_tui_pad "DISK EXPLORER  ${CURRENT_DIR}  $(analysis_label) · ${SORT_MODE}" "$COLUMNS")"
  _tui_reload_subdirs
  _NEEDS_REDRAW=1

  local key target prev_dir idx

  while true; do
    _tui_check_scan_completion || true
    [[ "$_NEEDS_REDRAW" -eq 1 ]] && tui_draw

    read_key
    key="$_LAST_KEY"

    if [[ -z "$key" && "$_TUI_SCAN_PID" -gt 0 ]]; then
      _tui_draw_loading_indicator
      continue
    fi

    case "$key" in
      # Flèches
      $'\x1b[A') cursor_up  ; _NEEDS_REDRAW=1 ;;
      $'\x1b[B') cursor_down; _NEEDS_REDRAW=1 ;;

      # Entrée : ouvrir le dossier sous le curseur (ignoré pour les fichiers)
      $'\n'|$'\r')
        if (( ${#SUBDIR_PATHS[@]} > 0 && CURSOR < ${#SUBDIR_PATHS[@]} )); then
          if [[ "${SUBDIR_TYPES[$CURSOR]:-d}" == "f" ]]; then
            LAST_WARNING="fichier — non navigable"
          else
            target="${SUBDIR_PATHS[$CURSOR]}"
            prev_dir="$CURRENT_DIR"
            if set_current_dir "$target"; then
              _tui_reload_subdirs
              cursor_reset
            else
              LAST_WARNING="navigation impossible vers ce dossier"
              CURRENT_DIR="$prev_dir"
            fi
          fi
          _NEEDS_REDRAW=1
        fi
        ;;

      # Retour arrière
      0)
        if [[ "$CURRENT_DIR" != "/" ]]; then
          prev_dir="$CURRENT_DIR"
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

      # Accès direct par numéro (1-9)
      [1-9])
        idx=$(( key - 1 ))
        if [[ -n "${SUBDIR_PATHS[$idx]-}" ]]; then
          if [[ "${SUBDIR_TYPES[$idx]:-d}" == "f" ]]; then
            LAST_WARNING="fichier — non navigable"
          else
            target="${SUBDIR_PATHS[$idx]}"
            prev_dir="$CURRENT_DIR"
            if set_current_dir "$target"; then
              _tui_reload_subdirs
              cursor_reset
            else
              LAST_WARNING="navigation impossible"
              CURRENT_DIR="$prev_dir"
            fi
          fi
        else
          LAST_WARNING="sélection hors plage"
        fi
        _NEEDS_REDRAW=1
        ;;

      s|S)
        local cur_p=""
        (( CURSOR < ${#SUBDIR_PATHS[@]} )) && cur_p="${SUBDIR_PATHS[$CURSOR]}"
        [[ "$SORT_MODE" == "size" ]] && SORT_MODE="mtime" || SORT_MODE="size"
        _tui_reload_subdirs "$cur_p"
        _NEEDS_REDRAW=1
        ;;
      a|A)
        [[ "$FILE_SIZE_MODE" == "real" ]] && FILE_SIZE_MODE="apparent" || FILE_SIZE_MODE="real"
        _NEEDS_REDRAW=1
        ;;
      d|D)
        _tui_delete_selected
        ;;
      p|P)
        local cur_p=""
        (( CURSOR < ${#SUBDIR_PATHS[@]} )) && cur_p="${SUBDIR_PATHS[$CURSOR]}"
        [[ "$ANALYSIS_MODE" == "partition" ]] && ANALYSIS_MODE="global" || ANALYSIS_MODE="partition"
        _tui_reload_subdirs "$cur_p"
        _NEEDS_REDRAW=1
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
        exit 0
        ;;
      '')
        # Timeout read_key — pas de touche, vérifier _NEEDS_REDRAW au prochain tour
        ;;
    esac
  done
}

# === MODULE: remote ===
# Exécution à distance via SSH : envoie disk-explorer en mode --summary sur N
# machines en parallèle et enregistre un rapport texte par machine en local.
#
# Prérequis côté orchestrateur (machine locale) :
#   - ssh disponible dans le PATH
#   - authentification par clé SSH configurée (BatchMode=yes — pas de prompt)
#   - le script doit être lancé depuis un fichier (pas via pipe/bash -s)
#
# Prérequis côté cible (machine distante) :
#   - Bash >= 4.4
#   - GNU find, sort, head, du (même dépendances que le mode local)
#
# Options de scan propagées vers chaque cible :
#   --mode, --sort, --top-count, --top-files, --max-depth
#
# Options NON propagées :
#   --exclude, --no-default-excludes  (chemins locaux sans sens à distance)
#   options interactives / TUI

# ---------------------------------------------------------------------------
# remote_resolve_hosts
#   Fusionne REMOTE_HOSTS (--remote-hosts) et REMOTE_HOSTS_FILE dans le
#   tableau global REMOTE_HOSTS. Ignore les lignes vides et les commentaires.
# ---------------------------------------------------------------------------
remote_resolve_hosts() {
  if [[ -n "$REMOTE_HOSTS_FILE" ]]; then
    [[ -f "$REMOTE_HOSTS_FILE" ]] \
      || die "fichier d'hôtes introuvable : $REMOTE_HOSTS_FILE"
    local line
    while IFS= read -r line || [[ -n "$line" ]]; do
      line="${line%%#*}"                   # retirer commentaires inline
      line="${line#"${line%%[! ]*}"}"      # ltrim espaces
      line="${line%"${line##*[! ]}"}"      # rtrim espaces
      [[ -z "$line" ]] && continue
      REMOTE_HOSTS+=("$line")
    done < "$REMOTE_HOSTS_FILE"
  fi

  (( ${#REMOTE_HOSTS[@]} > 0 )) \
    || die "aucun hôte spécifié — utilisez --remote-hosts ou --remote-hosts-file"
}

# ---------------------------------------------------------------------------
# remote_validate_host HOST
#   Refuse les noms d'hôtes contenant des métacaractères shell susceptibles
#   de causer une injection de commande.
#   Format accepté : [user@]hostname[:port] — alphanum, tirets, points, @, :
#   Note : les adresses IPv6 littérales ex. [::1] ne sont pas supportées
#   directement ; utiliser --remote-ssh-opt "-o Hostname=::1" à la place.
#
#   Attention regex : en POSIX ERE, \] ferme la classe de caractères — éviter
#   d'inclure '[' et ']' via \[\] ; mettre '-' en fin de classe.
# ---------------------------------------------------------------------------
remote_validate_host() {
  local host="$1"
  [[ -z "$host" ]] && die "hôte vide dans la liste"
  [[ "$host" =~ ^[a-zA-Z0-9@:._-]+$ ]] \
    || die "hôte invalide (caractères non autorisés) : '$host'"
}

# ---------------------------------------------------------------------------
# remote_run_host HOST OUT_FILE ERR_FILE STATUS_FILE
#   Streame le script courant via SSH sur HOST en mode --summary non interactif.
#   Redirige stdout → OUT_FILE, stderr → ERR_FILE.
#   Écrit le code de retour ssh(1) dans STATUS_FILE (0/1/2/255).
#
#   Code de retour distant :
#     0 = succès complet
#     2 = scan partiel (PARTIAL_SCAN_DETECTED) — rapport quand même valide
#     autres = erreur (connexion, dépendances manquantes…)
#
#   Conçu pour être appelé en arrière-plan (&).
# ---------------------------------------------------------------------------
remote_run_host() {
  local host="$1" out_file="$2" err_file="$3" status_file="$4"

  local -a ssh_opts=(
    -o "ConnectTimeout=${REMOTE_TIMEOUT}"
    -o "BatchMode=yes"
    -o "StrictHostKeyChecking=accept-new"
  )
  (( ${#REMOTE_SSH_OPTS[@]} > 0 )) && ssh_opts+=("${REMOTE_SSH_OPTS[@]}")

  # Options de scan propagées vers la machine distante
  local -a remote_args=(
    --path        "$REMOTE_PATH"
    --summary
    --no-color
    --no-spinner
    --mode        "$ANALYSIS_MODE"
    --sort        "$SORT_MODE"
    --top-count   "$TOP_COUNT"
    --top-files   "$TOP_FILES_COUNT"
    --max-depth   "$MAX_DEPTH"
  )

  local rc=0
  # Le script est pipé via stdin ; bash -s passe les arguments suivant "--"
  # comme $@ au script distant (évite une copie temporaire avec scp).
  ssh "${ssh_opts[@]}" "$host" bash -s -- "${remote_args[@]}" \
    < "$_SCRIPT_SELF" \
    > "$out_file"     \
    2> "$err_file"    \
    || rc=$?

  printf '%d' "$rc" > "$status_file"
}

# ---------------------------------------------------------------------------
# remote_run_all
#   Point d'entrée du mode --remote :
#     1. Résout et valide la liste d'hôtes
#     2. Lance remote_run_host en arrière-plan pour chaque hôte
#     3. Attend la fin de tous les jobs
#     4. Pour chaque hôte : génère Report_<host>_<ts>.txt (succès)
#                           ou    Error_<host>_<ts>.txt   (échec, contient stderr)
#     5. Affiche un tableau récapitulatif avec statut et chemin du rapport
#
#   Code de sortie :
#     0 — tous les hôtes ont retourné 0 ou 2 (succès / scan partiel)
#     1 — au moins un hôte en erreur
# ---------------------------------------------------------------------------
remote_run_all() {
  command -v ssh >/dev/null 2>&1 \
    || die "commande 'ssh' introuvable — requise pour le mode --remote"

  [[ -f "$_SCRIPT_SELF" ]] \
    || die "impossible de localiser le script sur le disque local." \
           " Le mode --remote ne fonctionne pas si le script est exécuté via pipe (bash -s)."

  remote_resolve_hosts

  mkdir -p -- "$REMOTE_REPORT_DIR" \
    || die "impossible de créer le dossier de rapports : $REMOTE_REPORT_DIR"

  local timestamp total
  timestamp=$(date '+%Y-%m-%d_%H-%M-%S')
  total=${#REMOTE_HOSTS[@]}

  local -a pids=() out_files=() err_files=() status_files=() ordered_hosts=()

  printf 'Lancement sur %d machine(s)\n' "$total"
  printf 'Chemin distant   : %s\n' "$REMOTE_PATH"
  printf 'Rapports locaux  : %s\n' "$REMOTE_REPORT_DIR"
  printf 'Timeout SSH      : %ss\n\n' "$REMOTE_TIMEOUT"

  # ── Lancer tous les jobs en parallèle ──────────────────────────────────
  local host i=0
  for host in "${REMOTE_HOSTS[@]}"; do
    remote_validate_host "$host"

    local safe_host out_file err_file status_file
    safe_host="${host//[:@\/]/_}"
    out_file=$(mktemp "${TMPDIR:-/tmp}/remote_out_${safe_host}.XXXXXX") \
      || die "mktemp échoué pour out_file"
    err_file=$(mktemp "${TMPDIR:-/tmp}/remote_err_${safe_host}.XXXXXX") \
      || die "mktemp échoué pour err_file"
    status_file=$(mktemp "${TMPDIR:-/tmp}/remote_st_${safe_host}.XXXXXX") \
      || die "mktemp échoué pour status_file"
    printf '255' > "$status_file"   # valeur sentinelle = erreur par défaut

    remote_run_host "$host" "$out_file" "$err_file" "$status_file" &
    pids+=($!)
    out_files+=("$out_file")
    err_files+=("$err_file")
    status_files+=("$status_file")
    ordered_hosts+=("$host")

    printf '  → %-40s  [PID %d]\n' "$host" "${pids[$i]}"
    (( i++ )) || true
  done

  printf '\nAttente de la fin des %d job(s)...\n\n' "$total"

  local pid
  for pid in "${pids[@]}"; do
    wait "$pid" 2>/dev/null || true
  done

  # ── Collecter les résultats et générer les rapports ────────────────────
  local ok_count=0 fail_count=0
  local -a summary_lines=()
  local sep sep2
  sep='========================================================================'
  sep2='------------------------------------------------------------------------'

  for (( i=0; i<total; i++ )); do
    host="${ordered_hosts[$i]}"

    local rc_val
    rc_val=$(cat "${status_files[$i]}" 2>/dev/null) || rc_val=255
    rc_val="${rc_val//[^0-9]/}"
    [[ -z "$rc_val" ]] && rc_val=255

    local safe_host report_file status_label
    safe_host="${host//[:@\/]/_}"

    if (( rc_val == 0 || rc_val == 2 )); then
      report_file="${REMOTE_REPORT_DIR}/Report_${safe_host}_${timestamp}.txt"
      cat "${out_files[$i]}" > "$report_file" 2>/dev/null || true
      if (( rc_val == 2 )); then
        status_label="OK (partiel)"
      else
        status_label="OK"
      fi
      (( ok_count++ )) || true
    else
      # Conserver le stderr dans un fichier Error_ pour faciliter le débogage
      report_file="${REMOTE_REPORT_DIR}/Error_${safe_host}_${timestamp}.txt"
      {
        printf 'ERREUR SSH — hôte : %s — code retour : %d\n' "$host" "$rc_val"
        printf 'Script transmis   : %s\n' "$_SCRIPT_SELF"
        printf '--- STDERR ---\n'
        cat "${err_files[$i]}" 2>/dev/null || true
      } > "$report_file" 2>/dev/null || true
      status_label="ÉCHEC (rc=${rc_val})"
      (( fail_count++ )) || true
    fi

    summary_lines+=("$(printf '  %-42s  %-16s  %s' "$host" "$status_label" "$report_file")")

    rm -f -- "${out_files[$i]}" "${err_files[$i]}" "${status_files[$i]}"
  done

  # ── Tableau récapitulatif ──────────────────────────────────────────────
  echo "$sep"
  echo "  RÉCAPITULATIF"
  echo "$sep"
  printf '  %-42s  %-16s  %s\n' "HÔTE" "STATUT" "RAPPORT LOCAL"
  echo "$sep2"
  local line
  for line in "${summary_lines[@]}"; do
    echo "$line"
  done
  echo "$sep2"
  printf '  Succès : %d / %d   Échecs : %d / %d\n' \
    "$ok_count" "$total" "$fail_count" "$total"
  echo "$sep"

  (( fail_count == 0 ))
}

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
VERSION="v0.2.2-RESILIENT" # Hardcoded for debugging
...
  if [[ "${DEBUG_TUI:-0}" -eq 1 ]]; then
    # Check TTY without redirection to avoid false negatives in log
    local t0 t1
    [[ -t 0 ]] && t0=Yes || t0=No
    [[ -t 1 ]] && t1=Yes || t1=No
    printf "[DEBUG] main() starting at %s\n" "$(date)" > ~/disk-explorer.debug
    printf "[DEBUG] VERSION: %s\n" "$VERSION" >> ~/disk-explorer.debug
    printf "[DEBUG] PWD: %s\n" "$(pwd)" >> ~/disk-explorer.debug
    printf "[DEBUG] AWK_CMD: %s\n" "$AWK_CMD" >> ~/disk-explorer.debug
    printf "[DEBUG] RUN_MODE: %s\n" "$RUN_MODE" >> ~/disk-explorer.debug
    printf "[DEBUG] TTY 0: %s, TTY 1: %s\n" "$t0" "$t1" >> ~/disk-explorer.debug
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
