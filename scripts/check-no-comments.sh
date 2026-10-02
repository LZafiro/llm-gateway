#!/usr/bin/env bash
set -euo pipefail

status=0
while IFS= read -r -d '' file; do
  if head -n 5 "$file" | grep -qE '^// Code generated .* DO NOT EDIT\.$'; then
    continue
  fi
  matches=$(awk '
    BEGIN { in_raw = 0 }
    {
      line = $0
      out = ""
      i = 1
      n = length(line)
      in_str = 0
      while (i <= n) {
        c = substr(line, i, 1)
        if (in_raw) {
          if (c == "`") in_raw = 0
          i++
          continue
        }
        if (in_str) {
          if (c == "\\") { i += 2; continue }
          if (c == in_str) in_str = 0
          i++
          continue
        }
        if (c == "`") { in_raw = 1; i++; continue }
        if (c == "\"" || c == "'"'"'") { in_str = c; i++; continue }
        if (substr(line, i, 2) == "/*") { print FILENAME ":" NR ": " line; break }
        if (substr(line, i, 2) == "//") {
          rest = substr(line, i)
          if (rest ~ /^\/\/go:/ || rest ~ /^\/\/nolint:/) break
          print FILENAME ":" NR ": " line
          break
        }
        i++
      }
    }
  ' "$file")
  if [[ -n "$matches" ]]; then
    echo "$matches"
    status=1
  fi
done < <(find . -name '*.go' -not -path './vendor/*' -print0)

if [[ $status -ne 0 ]]; then
  echo "comments are not allowed in Go code, see docs/adr/0016-no-comments-policy.md" >&2
fi
exit $status
