#!/usr/bin/env bash
set -euo pipefail
source "$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)/lib.sh"

while IFS='|' read -r id title deps; do
  [[ -n "$id" ]] || continue
  if task_is_executable "$id"; then
    printf 'TASK_ID=%s\n' "$id"
    printf 'TITLE=%s\n' "$title"
    printf 'STATUS=%s\n' "$(task_status "$id")"
    printf 'DEPENDENCIES=%s\n' "$deps"
    exit 0
  fi
done < <(
  awk -F'|' '
    /^\|[[:space:]]*[A-Za-z][A-Za-z0-9_.-]*[[:space:]]*\|/ {
      id=$2; title=$3; deps=$4
      gsub(/^[[:space:]]+|[[:space:]]+$/, "", id)
      gsub(/^[[:space:]]+|[[:space:]]+$/, "", title)
      gsub(/^[[:space:]]+|[[:space:]]+$/, "", deps)
      if (id == "ID") next
      print id "|" title "|" deps
    }
  ' "$TASK_LIST_FILE"
)

die "no executable task found"
