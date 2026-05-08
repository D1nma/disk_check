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
