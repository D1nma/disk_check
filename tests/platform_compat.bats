#!/usr/bin/env bats

setup() {
    source "${BATS_TEST_DIRNAME}/../disk-explorer.sh"
}

# -- detect_platform ----------------------------------------------------------

@test "detect_platform: linux quand OSTYPE=linux-gnu" {
    OSTYPE="linux-gnu"
    detect_platform
    [ "$PLATFORM" = "linux" ]
}

@test "detect_platform: linux quand OSTYPE=linux-musl" {
    OSTYPE="linux-musl"
    detect_platform
    [ "$PLATFORM" = "linux" ]
}

@test "detect_platform: macos quand OSTYPE=darwin23.0" {
    OSTYPE="darwin23.0"
    detect_platform
    [ "$PLATFORM" = "macos" ]
}

@test "detect_platform: macos quand OSTYPE=darwin24.0" {
    OSTYPE="darwin24.0"
    detect_platform
    [ "$PLATFORM" = "macos" ]
}

# -- resolve_gnu_tools_macos --------------------------------------------------

@test "resolve_gnu_tools_macos: utilise gfind si stub gfind dans PATH" {
    local stub_dir
    stub_dir="$(mktemp -d)"
    # Creer des stubs minimaux pour tous les outils g-prefixes
    for tool in gfind gsort ghead gdu gnumfmt; do
        printf '#!/usr/bin/env bash\necho "GNU %s"\n' "$tool" > "$stub_dir/$tool"
        chmod +x "$stub_dir/$tool"
    done
    PLATFORM="macos"
    PATH="$stub_dir:$PATH" resolve_gnu_tools_macos
    [ "$FIND_CMD" = "gfind" ]
    [ "$SORT_CMD" = "gsort" ]
    [ "$HEAD_CMD" = "ghead" ]
    [ "$DU_CMD"   = "gdu"   ]
    rm -rf "$stub_dir"
}

@test "resolve_gnu_tools_macos: ne modifie pas les CMD sur linux" {
    PLATFORM="linux"
    # Sur linux, resolve_gnu_tools_macos n'est jamais appelee
    # Verifier que les variables gardent leurs valeurs par defaut apres source
    [ "$FIND_CMD" = "find" ]
    [ "$SORT_CMD" = "sort" ]
    [ "$HEAD_CMD" = "head" ]
    [ "$DU_CMD"   = "du"   ]
}

# ── get_df_fields (macOS) ─────────────────────────────────────────

# Helper : mock df -Pk avec une sortie donnée.
# Utilise une variable exportée (_MOCK_DF_OUT) pour que la fonction df()
# soit accessible dans les sous-shells (command substitution).
_mock_df_pk() {
  export _MOCK_DF_OUT="$1"
  df() { printf '%s\n' "$_MOCK_DF_OUT"; }
  export -f df
}

@test "get_df_fields macOS: parse standard 6 colonnes" {
    PLATFORM="macos"
    CURRENT_DIR="/"
    _mock_df_pk "Filesystem 1024-blocks Used Available Capacity Mounted on
/dev/disk3s1s1 976490568 42949672 933540895 5% /"
    result=$(get_df_fields)
    [[ "$result" == *"/"* ]]
    [[ "$result" == *"5%"* ]]
    size=$(awk '{print $1}' <<< "$result")
    (( size > 0 ))
    unset -f df
}

@test "get_df_fields macOS: mount point avec espace" {
    PLATFORM="macos"
    CURRENT_DIR="/Volumes/My Drive"
    _mock_df_pk "Filesystem 1024-blocks Used Available Capacity Mounted on
/dev/disk5s1 976490568 10000000 966490568 2% /Volumes/My Drive"
    result=$(get_df_fields)
    [[ "$result" == *"/Volumes/My Drive"* ]]
    unset -f df
}

@test "get_df_fields macOS: colonnes APFS avec inode (9 colonnes)" {
    PLATFORM="macos"
    CURRENT_DIR="/System/Volumes/Data"
    _mock_df_pk "Filesystem 1024-blocks Used Available Capacity iused ifree %iused Mounted on
/dev/disk3s5 976490568 200000000 776490568 21% 1500000 5800000000 0% /System/Volumes/Data"
    result=$(get_df_fields)
    [[ "$result" == *"/System/Volumes/Data"* ]]
    unset -f df
}

# ── date_from_epoch ───────────────────────────────────────────────

_make_date_stub() {
    local stub_dir
    stub_dir="$(mktemp -d)"
    cat > "$stub_dir/date" << 'STUBEOF'
#!/usr/bin/env bash
# Stub: translate GNU "date -d @epoch fmt" to BSD "date -r epoch fmt"
args=("$@")
new_args=()
i=0
while (( i < ${#args[@]} )); do
    arg="${args[$i]}"
    if [[ "$arg" == "-d" ]]; then
        (( i++ ))
        val="${args[$i]}"
        if [[ "$val" == @* ]]; then
            epoch="${val#@}"
            new_args+=("-r" "${epoch%.*}")
        else
            new_args+=("-d" "$val")
        fi
    else
        new_args+=("$arg")
    fi
    (( i++ ))
done
exec /bin/date "${new_args[@]}"
STUBEOF
    chmod +x "$stub_dir/date"
    echo "$stub_dir"
}

@test "date_from_epoch: epoch 0 sur linux produit une date valide" {
    PLATFORM="linux"
    local stub_dir
    stub_dir="$(_make_date_stub)"
    result=$(PATH="$stub_dir:$PATH" date_from_epoch "0")
    rm -rf "$stub_dir"
    [[ "$result" =~ ^[0-9]{4}-[0-9]{2}-[0-9]{2}\ [0-9]{2}:[0-9]{2}$ ]]
}

@test "date_from_epoch: accepte un timestamp flottant (strip .xxx)" {
    PLATFORM="linux"
    local stub_dir
    stub_dir="$(_make_date_stub)"
    result=$(PATH="$stub_dir:$PATH" date_from_epoch "1700000000.5")
    rm -rf "$stub_dir"
    [[ "$result" != "?" ]]
    [[ "$result" =~ ^[0-9]{4} ]]
}

# ── Variables CMD initialisées ────────────────────────────────────

@test "CMD variables: initialisées avec des valeurs non vides" {
    [ -n "$FIND_CMD" ]
    [ -n "$SORT_CMD" ]
    [ -n "$HEAD_CMD" ]
    [ -n "$DU_CMD" ]
    [ -n "$NUMFMT_CMD" ]
}

@test "CMD variables: PLATFORM initialisée (vide avant detect_platform)" {
    # Avant detect_platform(), PLATFORM est ""
    # Après source, les variables globales sont définies
    # [ -v PLATFORM ] nécessite Bash 4.2+; on utilise une forme portable
    [[ "${PLATFORM+x}" == "x" ]]  # la variable existe (même si vide)
}
