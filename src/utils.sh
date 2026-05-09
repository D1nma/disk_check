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
