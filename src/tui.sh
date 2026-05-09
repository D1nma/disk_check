# === MODULE: tui ===
# TUI interactif : dessin, saisie, navigation

pause_screen() {
  echo
  read -r -p "Appuyez sur Entrée..." || true
}

show_help_screen() {
  [[ -t 1 ]] && clear
  cat <<EOF2
${BOLD}${BLUE}AIDE - DISK EXPLORER${NC}

${BOLD}Navigation${NC}
  [1-N]   Entrer dans un sous-dossier
  [0]     Retour arrière

${BOLD}Actions${NC}
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
  Mode taille        : $FILE_SIZE_MODE
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
  [[ -t 1 ]] && clear
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

  [[ -t 1 ]] && clear
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
    [[ -t 1 ]] && clear
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
  if df_fields="$(get_df_fields)"; then
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

  local rel_path="${full_path#"${CURRENT_DIR}/"}"
  [[ -z "$rel_path" || "$rel_path" == "$full_path" ]] && rel_path=$(basename -- "$full_path")
  SUBDIR_PATHS+=("$rel_path")

  local display_metric
  if [[ "$mode" == "size" ]]; then
    display_metric="$(human_size "$raw_data")"
  else
    display_metric="$(date_from_epoch "$raw_data")"
  fi

  local safe_rel_path
  safe_rel_path="$(sanitize_for_display "$rel_path")"

  if is_heavy_known "$rel_path"; then
    printf '  %b%2d)%b  %12s   %b%b%s%b\n' "$BOLD" "$idx" "$NC" "$display_metric" "$MAGENTA" "$BOLD" "$safe_rel_path" "$NC"
  else
    printf '  %b%2d)%b  %12s   %s\n' "$BOLD" "$idx" "$NC" "$display_metric" "$safe_rel_path"
  fi
}

show_heavy_subdirs() {
  SUBDIR_PATHS=()
  LAST_WARNING=""

  local tmp_file err_file
  tmp_file=$(make_temp_file) || return 1
  err_file=$(make_temp_file) || return 1

  echo -e "\n${CYAN}${BOLD}Analyse des sous-dossiers...${NC}"

  scan_subdirs_to_file "$tmp_file" "$err_file"
  LAST_WARNING="$SCAN_WARNING"

  local i=0
  local line ts full_path raw_size
  if [[ "$SORT_MODE" == "mtime" ]]; then
    while IFS= read -r -d '' line; do
      ts="${line%%$'\t'*}"
      full_path="${line#*$'\t'}"
      [[ -z "$full_path" || "$full_path" == "$line" ]] && continue
      process_line_display "$full_path" "mtime" "$ts" "$((++i))"
    done < "$tmp_file"
  else
    while IFS= read -r -d '' line; do
      raw_size="${line%%$'\t'*}"
      full_path="${line#*$'\t'}"
      [[ -z "$full_path" || "$full_path" == "$line" ]] && continue
      process_line_display "$full_path" "size" "$raw_size" "$((++i))"
    done < "$tmp_file"
  fi

  rm -f -- "$tmp_file" "$err_file"

  (( i == 0 )) && echo -e "  ${DIM}Aucun sous-dossier affichable.${NC}"
}

show_heavy_files() {
  LAST_WARNING=""

  [[ -t 1 ]] && clear
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

navigate() {
  while true; do
    show_header
    show_heavy_subdirs

    echo -e "\n${DIM}──────────────────────────────────────────────────────────${NC}"
    echo -e "  [1-${#SUBDIR_PATHS[@]}] Entrer dans un dossier   [0] Retour arrière"
    echo -e "  [s] Changer tri       [a] Taille fichiers (réel/apparent)   [p] Mode (partition/global)"
    echo -e "  [f] Top Fichiers      [e] Exclusions   [r] Rapport      [h/?] Aide      [c] Config      [q] Quitter"
    echo -e "${DIM}──────────────────────────────────────────────────────────${NC}"

    local choice idx target candidate prev_dir
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
              candidate="${CURRENT_DIR%/}/${target}"
              set_current_dir "$candidate" || {
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
