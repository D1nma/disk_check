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
    printf "[DEBUG] Starting TUI scan at %s\n" "$(date)" > /tmp/disk-explorer.debug
    printf "[DEBUG] CURRENT_DIR: %s\n" "$CURRENT_DIR" >> /tmp/disk-explorer.debug
  fi

  scan_subdirs_to_file "$tmp_dirs" "$err_dirs"
  local sub_rc=$?
  local warn="$SCAN_WARNING"

  _tui_scan_shallow_files "$tmp_files" "$err_files"
  local file_rc=$?
  [[ -n "$SCAN_WARNING" && -z "$warn" ]] && warn="$SCAN_WARNING"

  if [[ "$DEBUG_TUI" -eq 1 ]]; then
    printf "[DEBUG] scan_subdirs_to_file rc: %d\n" "$sub_rc" >> /tmp/disk-explorer.debug
    printf "[DEBUG] scan_subdirs_to_file output size: %d\n" "$(wc -c < "$tmp_dirs")" >> /tmp/disk-explorer.debug
    printf "[DEBUG] scan_subdirs_to_file errors: %s\n" "$(cat "$err_dirs")" >> /tmp/disk-explorer.debug
    printf "[DEBUG] _tui_scan_shallow_files rc: %d\n" "$file_rc" >> /tmp/disk-explorer.debug
    printf "[DEBUG] _tui_scan_shallow_files output size: %d\n" "$(wc -c < "$tmp_files")" >> /tmp/disk-explorer.debug
    printf "[DEBUG] _tui_scan_shallow_files errors: %s\n" "$(cat "$err_files")" >> /tmp/disk-explorer.debug
  fi

  {
    "$AWK_CMD" -v RS='\0' -v ORS='\0' '{
      tab = index($0, "\t")
      if (tab == 0 || length($0) <= 1) next
      print substr($0,1,tab-1) "\td:" substr($0,tab+1)
    }' "$tmp_dirs"
    "$AWK_CMD" -v RS='\0' -v ORS='\0' '{
      tab = index($0, "\t")
      if (tab == 0 || length($0) <= 1) next
      print substr($0,1,tab-1) "\tf:" substr($0,tab+1)
    }' "$tmp_files"
  } | LC_ALL=C "$SORT_CMD" -zrn | "$HEAD_CMD" -z -n "$TOP_COUNT" > "$out_file"

  if [[ "$DEBUG_TUI" -eq 1 ]]; then
    printf "[DEBUG] final merged size: %d\n" "$(wc -c < "$out_file")" >> /tmp/disk-explorer.debug
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
  (
    _tui_scan_to_file "$_TUI_SCAN_RESULT_FILE"
    touch "$_TUI_SCAN_DONE_FILE"
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
