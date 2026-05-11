# === MODULE: display ===
# Sorties non-TUI : summary, report, tree, self-check

_self_check_platform() {
  case "${PLATFORM:-}" in
    linux)  echo "[OK] Plateforme Linux détectée (${OSTYPE:-unknown})" ;;
    macos)  echo "[OK] Plateforme macOS détectée (${OSTYPE:-unknown})" ;;
    *)      echo "[KO] Plateforme non reconnue (${OSTYPE:-unknown})"; return 1 ;;
  esac
  return 0
}

_self_check_bash_version() {
  if (( BASH_VERSINFO[0] > 4 || (BASH_VERSINFO[0] == 4 && BASH_VERSINFO[1] >= 4) )); then
    echo "[OK] Bash >= 4.4 (${BASH_VERSION})"
    return 0
  else
    echo "[KO] Bash >= 4.4 requis (actuel: ${BASH_VERSION})"
    return 1
  fi
}

_self_check_commands() {
  # shellcheck disable=SC2178
  local -n missing_ref=$1
  local -i res=0

  local -A _cmd_map=(
    [awk]="awk"
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
  local canonical resolved cmd
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
      missing_ref+=("$resolved")
      res=1
    fi
  done
  return "$res"
}

_self_check_features() {
  local -a missing_cmds=("${@}")
  local -i res=0

  if ((${#missing_cmds[@]} > 0)); then
    local os_id
    os_id="$(detect_os_id)"
    echo "Suggestion d'installation ($os_id): $(install_hint "$os_id" "${missing_cmds[@]}")"
    echo "[INFO] Tests des fonctionnalités GNU ignorés (dépendances manquantes)."
    return 0
  fi

  local req_dir
  req_dir=$(mktemp -d "${TMPDIR:-/tmp}/disk-explorer.selfcheck.XXXXXX") || {
    echo "[KO] impossible de créer un répertoire temporaire pour les tests GNU"
    return 1
  }

  if "$FIND_CMD" "$req_dir" -maxdepth 0 -printf '' >/dev/null 2>&1; then
    echo "[OK] GNU find: support -printf"
  else
    echo "[KO] GNU find: -printf non supporté"
    res=1
  fi
  if printf '%b' 'a\0' | "$SORT_CMD" -z >/dev/null 2>&1; then
    echo "[OK] GNU sort: support -z"
  else
    echo "[KO] GNU sort: -z non supporté"
    res=1
  fi
  if printf '%b' 'a\0' | "$HEAD_CMD" -z -n 1 >/dev/null 2>&1; then
    echo "[OK] GNU head: support -z"
  else
    echo "[KO] GNU head: -z non supporté"
    res=1
  fi
  if "$DU_CMD" -0 --max-depth=0 "$req_dir" >/dev/null 2>&1; then
    echo "[OK] GNU du: support -0"
  else
    echo "[KO] GNU du: -0 non supporté"
    res=1
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
    res=1
  fi

  rm -rf -- "$req_dir"
  return "$res"
}

self_check_report() {
  local -i rc=0
  local -a missing_cmds=()

  echo "=== DISK EXPLORER :: SELF-CHECK ==="

  _self_check_platform || rc=1
  _self_check_bash_version || rc=1
  _self_check_commands missing_cmds || rc=1
  _self_check_features "${missing_cmds[@]}" || rc=1

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
      parent="$(dirname -- "$full_path")"
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
      done <<< "$children_raw" | LC_ALL=C "$SORT_CMD" -zrn | awk -v RS='\0' -v ORS='\0' -F '\t' '{sub(/^[^\t]*\t/, "", $0); print $0}'
    )

    local next_child
    for next_child in "${sorted_children[@]}"; do
      tree_print_node "$next_child" "$((depth + 1))" "$node_size"
    done
  }

  tree_print_node "$CURRENT_DIR" 0 "${tree_size_map[$CURRENT_DIR]}"

  rm -f -- "$tmp_file" "$err_file"
}
