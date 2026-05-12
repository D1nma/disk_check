tmp_dirs="test_tmp_dirs"
printf '123\t/path/to/dir\0' > "$tmp_dirs"
printf '456\t/another/dir\0' >> "$tmp_dirs"
out_file="test_out"
> "$out_file"
while IFS=$'\t' read -r -d '' val path; do
  printf '%s\td:%s\0' "$val" "$path" >> "$out_file"
done < "$tmp_dirs"
cat "$out_file" | od -c
