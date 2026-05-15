printf '123\t/path\0' > out.txt
while IFS=$'\t' read -r -d '' val path; do
  echo "Val: $val, Path: $path"
done < out.txt
