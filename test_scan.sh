set -u -o pipefail
AWK_CMD=awk
SORT_CMD=sort
HEAD_CMD=head
CURRENT_DIR="/etc"
du_cmd=(du -P -0 -B1 --max-depth=1 -x -- "$CURRENT_DIR")
err_file="err.log"
out_file="out.log"
"${du_cmd[@]}" 2>"$err_file" |
  "$AWK_CMD" -v RS='\0' -v ORS='\0' -v root="$CURRENT_DIR" '
    {
      tab = index($0, "\t")
      if (tab == 0) next
      path = substr($0, tab + 1)
      if (path != root) print $0
    }
  ' |
  LC_ALL=C "$SORT_CMD" -zrn |
  "$HEAD_CMD" -z -n 15 >"$out_file"
echo "RC: $?"
echo "OUT:"
cat "$out_file" | tr '\0' '\n'
echo "ERR:"
cat "$err_file"
