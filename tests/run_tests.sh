#!/usr/bin/env bash

# Source the script to get access to functions
source "$(dirname "$0")/../disk-explorer.sh"

# Cursor tests call drawing helpers without initializing the terminal.
COLUMNS=80

failed=0
total=0

assert_true() {
    local cmd="$1"
    local desc="$2"
    total=$((total + 1))
    if eval "$cmd"; then
        echo "[PASS] $desc"
    else
        echo "[FAIL] $desc"
        failed=$((failed + 1))
    fi
}

assert_false() {
    local cmd="$1"
    local desc="$2"
    total=$((total + 1))
    if ! eval "$cmd"; then
        echo "[PASS] $desc"
    else
        echo "[FAIL] $desc"
        failed=$((failed + 1))
    fi
}

echo "Running build smoke tests..."

assert_true '
  _bt_tmp=$(mktemp -d)
  cp "$(dirname "$0")/../build.sh" "$_bt_tmp/" && cp -r "$(dirname "$0")/../src" "$_bt_tmp/"
  (cd "$_bt_tmp" && ./build.sh >/dev/null 2>&1 && bash -n disk-explorer.sh)
  _bt_rc=$?
  rm -rf "$_bt_tmp"
  [[ $_bt_rc -eq 0 ]]
' "build.sh: produit un fichier syntaxiquement valide (dans une copie temporaire)"

assert_true '
  # --bash : ces tests ciblent le fallback Bash ; sans ce flag, le
  # bootstrap télécharge et exécute le binaire Go quand un tag de release
  # existe (nécessite réseau et change les drapeaux disponibles).
  "$(dirname "$0")/../disk-explorer.sh" --bash --self-check >/dev/null 2>&1
' "disk-explorer.sh --bash --self-check: exit 0"

assert_true '
  "$(dirname "$0")/../disk-explorer.sh" --summary /tmp >/dev/null 2>&1
  rc=$?
  [[ $rc -eq 0 || $rc -eq 2 ]]
' "disk-explorer.sh --summary /tmp: exit 0 ou 2"

assert_true '
  echo "" | "$(dirname "$0")/../disk-explorer.sh" --bash /tmp >/dev/null 2>&1
  rc=$?
  [[ $rc -eq 0 || $rc -eq 2 ]]
' "disk-explorer.sh: bascule en summary quand stdin n'est pas un TTY (mode --bash, exit 0 ou 2)"

# install.sh doit échouer quand la somme ne peut pas être vérifiée (fail-closed).
# $1 = scénario : nosums | noentry | ok
_fake_install() {
    local mode="$1" root dir rc
    root="$(cd "$(dirname "$0")/.." && pwd)"
    dir=$(mktemp -d)
    (
        source "$root/install.sh"
        INSTALL_DIR="$dir/bin"
        fetch_latest_tag() { echo v9.9.9; }
        download() {
            local url=$1 dest=$2
            case "$url" in
                *SHA256SUMS)
                    case "$mode" in
                        nosums)  return 1 ;;
                        noentry) echo "0000  autre-binaire" > "$dest" ;;
                        ok)      echo "$(sha256sum "$TMP_FILE" | awk '{print $1}')  disk-explorer-$(get_os)-$(get_arch)" > "$dest" ;;
                    esac ;;
                *) printf 'fake' > "$dest" ;;
            esac
        }
        main
    ) >/dev/null 2>&1
    rc=$?
    rm -rf "$dir"
    return $rc
}

assert_false "_fake_install nosums"  "install.sh: échoue si SHA256SUMS est indisponible"
assert_false "_fake_install noentry" "install.sh: échoue si aucune somme ne correspond au binaire"
assert_true  "_fake_install ok"      "install.sh: installe quand la somme correspond"

echo "Running tests for is_integer..."
assert_true "is_integer 123" "is_integer: positive integer"
assert_true "is_integer 0" "is_integer: zero"
assert_true "is_integer -456" "is_integer: negative integer"
assert_false "is_integer abc" "is_integer: non-integer string"
assert_false "is_integer 12.34" "is_integer: decimal number"
assert_false "is_integer ''" "is_integer: empty string"
assert_false "is_integer ' 123 '" "is_integer: spaces"

echo -e "\nRunning tests for is_non_negative_int..."
assert_true "is_non_negative_int 123" "is_non_negative_int: positive integer"
assert_true "is_non_negative_int 0" "is_non_negative_int: zero"
assert_false "is_non_negative_int -123" "is_non_negative_int: negative integer"

echo -e "\nRunning tests for TUI capability detection..."

assert_true '
  TUI_CAPABLE=0
  tui_check_capability 2>/dev/null
  [[ "$TUI_CAPABLE" -eq 0 || "$TUI_CAPABLE" -eq 1 ]]
' "tui_check_capability: produit 0 ou 1 sans crash"

assert_true '
  _NEEDS_REDRAW=0
  trap '"'"'_NEEDS_REDRAW=1'"'"' SIGWINCH
  kill -WINCH $$
  sleep 0.05
  [[ "$_NEEDS_REDRAW" -eq 1 ]]
' "SIGWINCH handler: positionne _NEEDS_REDRAW=1"

echo -e "\nRunning tests for read_key..."

assert_true '
  key=$(echo "" | read_key 2>/dev/null || true)
  [[ -z "$key" ]]
' "read_key: retourne chaine vide si stdin est fermé"

echo -e "\nRunning tests for cursor navigation..."

# Helper portable : crée N chemins sans seq -f (incompatible macOS sans GNU seq)
_make_paths() {
  local n="$1" i; for (( i=1; i<=n; i++ )); do printf '/dir%d\n' "$i"; done
}

# Charge N chemins dans SUBDIR_PATHS sans mapfile (absent de bash 3.x/macOS)
_load_paths() {
  local n="$1"
  SUBDIR_PATHS=()
  SUBDIR_DATA=()
  SUBDIR_TYPES=()
  local i
  for (( i=1; i<=n; i++ )); do SUBDIR_PATHS+=("/dir$i"); done
}

assert_true '
  CURSOR=0; SCROLL_OFFSET=0
  _load_paths 3; LINES=10
  cursor_down; [[ "$CURSOR" -eq 1 ]]
' "cursor_down: incrémente CURSOR"

assert_true '
  CURSOR=0; SCROLL_OFFSET=0
  _load_paths 3; LINES=10
  cursor_up; [[ "$CURSOR" -eq 0 ]]
' "cursor_up: ne descend pas sous 0"

assert_true '
  CURSOR=2; SCROLL_OFFSET=0
  _load_paths 3; LINES=10
  cursor_down; [[ "$CURSOR" -eq 2 ]]
' "cursor_down: ne dépasse pas le dernier élément"

assert_true '
  CURSOR=0; SCROLL_OFFSET=0
  _load_paths 20; LINES=10
  # visible = LINES - 6 = 4 ; descendre jusqu'"'"'à ce que SCROLL_OFFSET bouge
  for _i in $(seq 4); do cursor_down; done
  [[ "$SCROLL_OFFSET" -eq 1 ]]
' "cursor_down: scroll quand curseur dépasse la zone visible"

assert_true '
  CURSOR=3; SCROLL_OFFSET=2
  _load_paths 20; LINES=10
  cursor_up; cursor_up; cursor_up
  [[ "$SCROLL_OFFSET" -eq 0 ]]
' "cursor_up: déscroll quand curseur remonte au-dessus du viewport"

echo -e "\nRunning tests for draw_list..."

assert_true '
  LINES=24; COLUMNS=80
  CURRENT_DIR="/tmp"
  SUBDIR_PATHS=("/tmp/a" "/tmp/b" "/tmp/c")
  SUBDIR_DATA=("1024" "2048" "512")
  SORT_MODE="size"
  CURSOR=0; SCROLL_OFFSET=0
  line_count=$(draw_list 2>/dev/null | wc -l)
  visible=$(( LINES - 6 ))
  (( line_count == visible ))
' "draw_list: produit exactement LINES-6 lignes (18 pour LINES=24)"

echo -e "\nRunning tests for remote_validate_host..."

assert_true  "remote_validate_host 'hostname'"              "remote_validate_host: hostname simple"
assert_true  "remote_validate_host 'user@hostname'"         "remote_validate_host: user@hostname"
assert_true  "remote_validate_host '10.0.0.1'"              "remote_validate_host: adresse IP"
assert_true  "remote_validate_host 'host.example.com'"      "remote_validate_host: FQDN"
assert_true  "remote_validate_host 'user@host.example.com'" "remote_validate_host: user@FQDN"
assert_true  "remote_validate_host 'host:2222'"             "remote_validate_host: host:port accepté"
assert_true  "remote_validate_host 'user@host:2222'"        "remote_validate_host: user@host:port accepté"
assert_false "( remote_validate_host '-host' ) 2>/dev/null"           "remote_validate_host: nom commençant par '-' rejeté"
assert_false "( remote_validate_host '' ) 2>/dev/null"                "remote_validate_host: hôte vide rejeté"
assert_false "( remote_validate_host 'host;evil' ) 2>/dev/null"       "remote_validate_host: point-virgule rejeté"
assert_false "( remote_validate_host 'host evil' ) 2>/dev/null"       "remote_validate_host: espace rejeté"
assert_false "( remote_validate_host 'host\$(cmd)' ) 2>/dev/null"     "remote_validate_host: substitution de commande rejetée"

echo -e "\nRunning tests for remote_resolve_hosts..."

assert_true '
  _rrh_tmp=$(mktemp)
  printf "host1\n# commentaire\n\nhost2\nhost3  # inline\n" > "$_rrh_tmp"
  REMOTE_HOSTS=(); REMOTE_HOSTS_FILE="$_rrh_tmp"
  remote_resolve_hosts
  rm -f "$_rrh_tmp"; REMOTE_HOSTS_FILE=""
  [[ "${#REMOTE_HOSTS[@]}" -eq 3 && "${REMOTE_HOSTS[0]}" == "host1" && "${REMOTE_HOSTS[2]}" == "host3" ]]
' "remote_resolve_hosts: parse le fichier (ignore vides et commentaires)"

assert_true '
  _rrh_tmp=$(mktemp)
  printf "  spaced-host  \n" > "$_rrh_tmp"
  REMOTE_HOSTS=(); REMOTE_HOSTS_FILE="$_rrh_tmp"
  remote_resolve_hosts
  rm -f "$_rrh_tmp"; REMOTE_HOSTS_FILE=""
  [[ "${#REMOTE_HOSTS[@]}" -eq 1 && "${REMOTE_HOSTS[0]}" == "spaced-host" ]]
' "remote_resolve_hosts: supprime les espaces en début et fin de ligne"

assert_false '
  ( REMOTE_HOSTS=(); REMOTE_HOSTS_FILE="/tmp/nope_no_such_file_$$"; remote_resolve_hosts ) 2>/dev/null
' "remote_resolve_hosts: échoue si le fichier est introuvable"

assert_false '
  ( REMOTE_HOSTS=(); REMOTE_HOSTS_FILE=""; remote_resolve_hosts ) 2>/dev/null
' "remote_resolve_hosts: échoue si aucun hôte fourni"

echo -e "\nRunning tests for remote_run_all guards..."

assert_false '
  ( _SCRIPT_SELF=""; REMOTE_HOSTS=(h1); remote_run_all ) 2>/dev/null
' "remote_run_all: échoue si _SCRIPT_SELF vide (exécution via pipe)"

_test_remote_escaping() {
  ssh() {
    printf "%s" "${*: -1}"
    return 0
  }
  _SCRIPT_SELF="/dev/null"
  REMOTE_PATH="/var/log/my folder & tests"
  ANALYSIS_MODE="partition"
  SORT_MODE="size"
  TOP_COUNT=10
  TOP_FILES_COUNT=10
  MAX_DEPTH=-1
  REMOTE_TIMEOUT=10
  REMOTE_SSH_OPTS=()
  local out_f err_f st_f res_cmd
  out_f=$(mktemp) err_f=$(mktemp) st_f=$(mktemp)
  remote_run_host "myhost" "$out_f" "$err_f" "$st_f"
  res_cmd=$(cat "$out_f")
  rm -f "$out_f" "$err_f" "$st_f"
  [[ "$res_cmd" == *"my\\ folder\\ \\&\\ tests"* || "$res_cmd" == *"'/var/log/my folder & tests'"* ]]
}
assert_true "_test_remote_escaping" "remote_run_host: échappe de manière sûre les arguments SSH distants"

echo -e "\nRunning tests for --remote option parsing..."

assert_true '
  REMOTE_HOSTS=(); RUN_MODE="interactive"
  parse_args --remote --remote-hosts "h1,h2,h3"
  [[ "${#REMOTE_HOSTS[@]}" -eq 3 && "${REMOTE_HOSTS[1]}" == "h2" && "$RUN_MODE" == "remote" ]]
' "--remote-hosts: découpe les virgules et active RUN_MODE=remote"

assert_true '
  REMOTE_HOSTS=()
  parse_args --remote --remote-hosts "h1" --remote-hosts "h2"
  [[ "${#REMOTE_HOSTS[@]}" -eq 2 && "${REMOTE_HOSTS[0]}" == "h1" && "${REMOTE_HOSTS[1]}" == "h2" ]]
' "--remote-hosts: accumule les appels répétés"

assert_true '
  REMOTE_PATH="/"
  parse_args --remote --remote-hosts "h1" --remote-path "/var/log"
  [[ "$REMOTE_PATH" == "/var/log" ]]
' "--remote-path: positionne REMOTE_PATH"

assert_true '
  REMOTE_TIMEOUT=10
  parse_args --remote --remote-hosts "h1" --remote-timeout 30
  [[ "$REMOTE_TIMEOUT" == "30" ]]
' "--remote-timeout: positionne REMOTE_TIMEOUT"

assert_false '
  ( parse_args --remote --remote-hosts "h1" --remote-timeout abc ) 2>/dev/null
' "--remote-timeout: rejette une valeur non entière"

assert_true '
  REMOTE_SSH_OPTS=()
  parse_args --remote --remote-hosts "h1" --remote-ssh-opt "-p 2222" --remote-ssh-opt "-i /key"
  [[ "${#REMOTE_SSH_OPTS[@]}" -eq 2 && "${REMOTE_SSH_OPTS[0]}" == "-p 2222" ]]
' "--remote-ssh-opt: accumule les options répétées"

echo -e "\nRunning tests for remote_build_args..."

assert_true '
  REMOTE_PATH="/srv"; ANALYSIS_MODE="global"; SORT_MODE="size"
  TOP_COUNT=15; TOP_FILES_COUNT=20; MAX_DEPTH=-1
  remote_build_args
  [[ "${REMOTE_ARGS[0]}" == "--bash" ]]
' "remote_build_args: --bash en tête (la cible exécute toujours le Bash)"

assert_true '
  REMOTE_PATH="/srv data"; ANALYSIS_MODE="partition"; SORT_MODE="mtime"
  TOP_COUNT=5; TOP_FILES_COUNT=7; MAX_DEPTH=3
  remote_build_args
  _ra_old_ifs="$IFS"; IFS="|"
  _ra_joined="${REMOTE_ARGS[*]}"; IFS="$_ra_old_ifs"
  [[ "$_ra_joined" == "--bash|--path|/srv data|--summary|--no-color|--no-spinner|--mode|partition|--sort|mtime|--top-count|5|--top-files|7|--max-depth|3" ]]
' "remote_build_args: propage chemin et options de scan"

_test_remote_cmd_bash() {
  ssh() { printf "%s" "${*: -1}"; }
  _SCRIPT_SELF="/dev/null"; REMOTE_PATH="/"; REMOTE_SSH_OPTS=()
  local out_f err_f st_f res_cmd
  out_f=$(mktemp) err_f=$(mktemp) st_f=$(mktemp)
  remote_run_host "myhost" "$out_f" "$err_f" "$st_f"
  res_cmd=$(cat "$out_f")
  rm -f "$out_f" "$err_f" "$st_f"
  unset -f ssh
  [[ "$res_cmd" == "bash -s -- --bash --path / --summary "* ]]
}
assert_true "_test_remote_cmd_bash" "remote_run_host: la commande distante force --bash"

echo -e "\nRunning tests for go_args_from_bash_args..."

# _go_expect ATTENDU ARGS... : la traduction réussit et GO_ARGS, joint par
# « | », vaut ATTENDU.
_go_expect() {
  local expected="$1"; shift
  go_args_from_bash_args "$@" || { echo "    refusé (Bash) au lieu de: $expected"; return 1; }
  local IFS='|'
  [[ "${GO_ARGS[*]}" == "$expected" ]] || { echo "    attendu: $expected / obtenu: ${GO_ARGS[*]}"; return 1; }
}
# _go_bash_only ARGS... : pas d'équivalent Go, le script doit rester en Bash.
_go_bash_only() { ! go_args_from_bash_args "$@"; }

assert_true  "_go_expect ''"                                  "go_args: aucun argument → binaire sans argument (TUI)"
assert_true  "_go_expect '--|/var' --path /var"               "go_args: --path DIR → positionnel"
assert_true  "_go_expect '--|/var' /var"                      "go_args: chemin positionnel transmis"
assert_true  "_go_expect '--|-odd' -- -odd"                   "go_args: chemin après -- (commençant par -) protégé par --"
assert_true  "_go_expect '--|/b' --path /a /b"                "go_args: positionnel après --path l'emporte (comme parse_args)"
assert_true  "_go_expect '--|/a' /b --path /a"                "go_args: --path après positionnel l'emporte (comme parse_args)"
assert_true  "_go_expect '--mode|partition' --mode partition" "go_args: --mode partition transmis"
assert_true  "_go_expect '--mode|global' --mode global"       "go_args: --mode global transmis"
assert_true  "_go_expect '--exclude|/a|--exclude|/b' --exclude /a --exclude /b" "go_args: --exclude répétable transmis"
assert_true  "_go_expect '--exclude|/var/cache|--|/var' --exclude cache /var"    "go_args: --exclude relatif résolu depuis le chemin scanné"
assert_true  "_go_expect '--exclude|/cache|--|/' --path / --exclude cache"       "go_args: --exclude relatif sous /"
assert_true  "_go_expect '--exclude|cache' --exclude cache"   "go_args: --exclude relatif sans chemin → répertoire courant (identique)"
assert_true  "_go_expect '--summary' --summary"               "go_args: --summary transmis"
assert_true  "_go_expect '--report' --report"                 "go_args: --report transmis"
assert_true  "_go_expect '--tree' --tree"                     "go_args: --tree transmis"
assert_true  "_go_expect '--tree' --summary --tree"           "go_args: dernier mode gagnant (comme parse_args)"
assert_true  "_go_expect '' --summary --interactive"          "go_args: --interactive → TUI (mode par défaut Go)"
assert_true  "_go_expect '--tree|--tree-depth|5|--|/var' --tree --tree-depth 5 /var" "go_args: --tree-depth transmis"
assert_true  "_go_expect '--report|--report-dir|/r|--|/var' --report --report-dir /r /var" "go_args: --report-dir transmis"
assert_true  "_go_expect '--summary|--top|30' --top-count 30 --summary" "go_args: --top-count N → --top N"
assert_true  "_go_expect '--summary' --sort size --file-size real --summary" "go_args: --sort size / --file-size real (défauts Go) retirés"
assert_true  "_go_expect '--|/var' --no-spinner --interactive /var" "go_args: --no-spinner retiré"
assert_true  "_go_expect '--version' --version"               "go_args: --version transmis"
assert_true  "_go_expect '--mode|partition|--exclude|/proc|--summary|--top|10|--|/srv' --mode partition --exclude /proc --summary --no-color --no-spinner --top-count 10 --path /srv" \
  "go_args: combinaison mixte traduite"

assert_true  'go_args_from_bash_args --no-color --summary && [[ "$GO_NO_COLOR" -eq 1 && "${GO_ARGS[*]}" == "--summary" ]]' \
  "go_args: --no-color retiré, GO_NO_COLOR=1"
assert_true  'go_args_from_bash_args --no-color && go_args_from_bash_args --summary && [[ "$GO_NO_COLOR" -eq 0 ]]' \
  "go_args: GO_NO_COLOR remis à 0 à chaque appel"

for _go_case in \
  "--remote" "--remote --remote-hosts h1" "--remote-hosts h1" "--remote-hosts-file hosts.txt" \
  "--remote-path /" "--remote-report-dir out" "--remote-timeout 5" "--remote-ssh-opt -p2222" \
  "--self-check" "--sort mtime" "--file-size apparent" "--max-depth 2" "--max-depth -1" \
  "--top-files 20" "--no-default-excludes" "--debug-tui" "--bash" "-h" "--help" \
  "--bogus" "--scan-workers 4" "--update" "--path=/var" "--top-count=5" \
  "--summary /var --remote" "--summary --bash /var" "--top-count 10 --top-files 10"; do
  # eval découpe le cas en mots (un cas = option + valeur éventuelle).
  assert_true "_go_bash_only $_go_case" "go_args: reste en Bash pour « $_go_case »"
done
unset _go_case

assert_true  "_go_bash_only --mode foo"          "go_args: --mode invalide → Bash (erreur de parse_args)"
assert_true  "_go_bash_only --top-count abc"     "go_args: --top-count non entier → Bash"
assert_true  "_go_bash_only --top-count 010"     "go_args: --top-count à zéro de tête (octal pour flag Go) → Bash"
assert_true  "_go_bash_only --top-count 10001"   "go_args: --top-count au-delà du maximum → Bash"
assert_true  "_go_bash_only --tree-depth -1"     "go_args: --tree-depth négatif → Bash"
assert_true  "_go_bash_only --tree-depth 1025"   "go_args: --tree-depth au-delà du maximum → Bash"
assert_true  "_go_bash_only --exclude '/var/*'"  "go_args: --exclude avec glob → Bash (refusé par prepare_exclusions)"
assert_true  "_go_bash_only --exclude ''"        "go_args: --exclude vide → Bash"
assert_true  "_go_bash_only --report-dir ''"     "go_args: --report-dir vide → Bash"
assert_true  "_go_bash_only --path ''"           "go_args: --path vide → Bash"
assert_true  "_go_bash_only ''"                  "go_args: chemin positionnel vide → Bash"
assert_true  "_go_bash_only --path"              "go_args: valeur manquante (--path) → Bash"
assert_true  "_go_bash_only --summary --top-count" "go_args: valeur manquante (--top-count) → Bash"
assert_true  "_go_bash_only /a /b"               "go_args: deux chemins positionnels → Bash (erreur de parse_args)"
assert_true  "_go_bash_only /a -- /b"            "go_args: positionnel avant et après -- → Bash"

echo -e "\nRunning tests for try_go_binary (BINARY_TAG, faux binaire, sans réseau)..."

# Faux binaire en cache : affiche NO_COLOR puis ses arguments.
_go_fake_cache=$(mktemp -d)
_go_empty_cache=$(mktemp -d)
cat > "$_go_fake_cache/disk-explorer" <<'EOF'
#!/usr/bin/env bash
printf 'GO NO_COLOR=%s' "${NO_COLOR-unset}"
(( $# == 0 )) || printf ' %s' "$@"
EOF
chmod +x "$_go_fake_cache/disk-explorer"

# _go_bootstrap TAG ARGS... : lance try_go_binary dans un sous-shell (cache
# = $_go_cache, téléchargement neutralisé) ; affiche « BASH » s'il rend la main.
_go_bootstrap() {
  local tag="$1"; shift
  (
    unset NO_COLOR
    BINARY_TAG="$tag"
    CACHE_DIR="${_go_cache:-$_go_fake_cache}"
    download_binary() { return 1; }
    try_go_binary "$@" 2>/dev/null
    printf 'BASH'
  )
}

assert_true '[[ "$(_go_bootstrap v9.9.9 --path /tmp --summary)" == "GO NO_COLOR=unset --summary -- /tmp" ]]' \
  "try_go_binary: BINARY_TAG vX → exec du binaire avec les arguments traduits"
assert_true '[[ "$(_go_bootstrap v9.9.9 --no-color --summary)" == "GO NO_COLOR=1 --summary" ]]' \
  "try_go_binary: --no-color → NO_COLOR=1 exporté pour le binaire"
assert_true '[[ "$(_go_bootstrap v9.9.9)" == "GO NO_COLOR=unset" ]]' \
  "try_go_binary: sans argument → binaire sans argument"
assert_true '[[ "$(_go_bootstrap "" --summary)" == "BASH" ]]' \
  "try_go_binary: BINARY_TAG vide → pas d'exec (Bash)"
assert_true '[[ "$(_go_bootstrap 5dd945f --summary)" == "BASH" ]]' \
  "try_go_binary: BINARY_TAG hash (hors vX) → pas d'exec"
assert_true '[[ "$(_go_bootstrap dev --summary)" == "BASH" ]]' \
  "try_go_binary: BINARY_TAG dev → pas d'exec"
assert_true '[[ "$(_go_bootstrap v9.9.9 --remote --remote-hosts h1)" == "BASH" ]]' \
  "try_go_binary: option Bash seule (--remote) → pas d'exec"
assert_true '[[ "$(_go_bootstrap v9.9.9 --bash --summary)" == "BASH" ]]' \
  "try_go_binary: --bash → pas d'exec"
assert_true '[[ "$(_go_cache="$_go_empty_cache" _go_bootstrap v9.9.9 --summary)" == "BASH" ]]' \
  "try_go_binary: binaire absent et téléchargement en échec → Bash"

_test_download_url() {
  local d rc
  d=$(mktemp -d)
  (
    BINARY_TAG="v9.9.9"; VERSION="v9.9.9-4-gdeadbee"
    curl() { printf '%s\n' "$*" >> "$d/curl.log"; return 1; }
    download_binary linux amd64 "$d/bin/disk-explorer"
  ) >/dev/null 2>&1
  grep -q "/releases/download/v9.9.9/disk-explorer-linux-amd64" "$d/curl.log"
  rc=$?
  rm -rf "$d"
  return $rc
}
assert_true "_test_download_url" "download_binary: télécharge la release BINARY_TAG (pas VERSION)"

assert_true '[[ "$CACHE_DIR" == "${HOME}/.cache/disk-explorer/bin/${BINARY_TAG}" ]]' \
  "CACHE_DIR: indexé par BINARY_TAG"

rm -rf "$_go_fake_cache" "$_go_empty_cache"

echo -e "\nRunning tests for --version (Bash)..."

assert_true '[[ "$(parse_args --version)" == "disk-explorer $VERSION" ]]' \
  "parse_args --version: affiche VERSION et quitte"

echo -e "\nRunning tests for build.sh version policy..."

# _test_build_versions : dépôt git jetable ; vérifie VERSION / BINARY_TAG
# injectés par build.sh hors dépôt, sans tag, sur un tag, après un tag.
_test_build_versions() {
  local root d rc
  root="$(cd "$(dirname "$0")/.." && pwd)"
  d=$(mktemp -d)
  cp "$root/build.sh" "$d/" && cp -r "$root/src" "$d/" || { rm -rf "$d"; return 1; }
  (
    cd "$d" || exit 1
    _g() { git -c user.name=t -c user.email=t@example.invalid -c commit.gpgsign=false -c tag.gpgsign=false "$@" >/dev/null 2>&1; }
    _v() { sed -n "s/^$1=\"\(.*\)\"\$/\1/p" disk-explorer.sh; }
    _check() {  # _check VERSION_ATTENDUE BINARY_TAG_ATTENDU
      ./build.sh >/dev/null 2>&1 || { echo "    build.sh a échoué"; return 1; }
      [[ "$(_v VERSION)" == "$1" && "$(_v BINARY_TAG)" == "$2" ]] \
        || { echo "    attendu: $1 / '$2' — obtenu: $(_v VERSION) / '$(_v BINARY_TAG)'"; return 1; }
    }
    _check "dev" "" || exit 1                       # hors dépôt git
    _g init -q && _g add -A && _g commit -q -m c1 || exit 1
    _check "$(git rev-parse --short HEAD)" "" || exit 1   # aucun tag : Bash seul
    _g tag v1.2.3 || exit 1
    _check "v1.2.3" "v1.2.3" || exit 1              # sur le tag
    _g commit -q --allow-empty -m c2 && _g tag nightly || exit 1
    _check "v1.2.3-1-g$(git rev-parse --short HEAD)" "v1.2.3" || exit 1  # après le tag ; tag hors release ignoré
  )
  rc=$?
  rm -rf "$d"
  return $rc
}
if command -v git >/dev/null 2>&1; then
  assert_true "_test_build_versions" "build.sh: VERSION descriptive, BINARY_TAG = dernière release atteignable"
else
  echo "[SKIP] build.sh version policy (git absent)"
fi

echo -e "\nSummary: $total tests, $((total - failed)) passed, $failed failed."

if [ $failed -ne 0 ]; then
    exit 1
fi
