source disk-explorer.sh --self-check >/dev/null
RUN_MODE="interactive"
ENABLE_SPINNER=0
CURRENT_DIR="/etc"
TOP_COUNT=15
TOP_FILES_COUNT=20
SORT_MODE="size"
FILE_SIZE_MODE="real"
MAX_DEPTH=-1
ANALYSIS_MODE="partition"
ACTIVE_EXCLUDED_DIRS=("/proc" "/sys" "/dev" "/run" "/tmp" "/snap" "/boot" "/overlay")

tmp_dirs=$(mktemp)
err_dirs=$(mktemp)
tmp_files=$(mktemp)
err_files=$(mktemp)
out_file=$(mktemp)

scan_subdirs_to_file "$tmp_dirs" "$err_dirs"
scan_top_files_to_file "$tmp_files" "$err_files"

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

echo "OUT_FILE contents:"
cat "$out_file" | tr '\0' '\n'
