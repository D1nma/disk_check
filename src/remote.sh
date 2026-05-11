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
