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
