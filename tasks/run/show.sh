#!/usr/bin/env bash
set -euo pipefail
source "$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)/lib.sh"

printf '%-8s | %-44s | %-8s | %-8s | %s\n' "ID" "TITLE" "STATUS" "WORKTREE" "DEPENDENCIES"
printf '%s\n' "------------------------------------------------------------------------------------------------"

awk -F'|' '
  /^\|[[:space:]]*[A-Za-z][A-Za-z0-9_.-]*[[:space:]]*\|/ {
    id=$2; title=$3; deps=$4
    gsub(/^[[:space:]]+|[[:space:]]+$/, "", id)
    gsub(/^[[:space:]]+|[[:space:]]+$/, "", title)
    gsub(/^[[:space:]]+|[[:space:]]+$/, "", deps)
    if (id == "ID") next
    print id "|" title "|" deps
  }
' "$TASK_LIST_FILE" | while IFS='|' read -r id title deps; do
  status="$(task_status "$id")"
  if task_worktree_exists "$id"; then wt="yes"; else wt="no"; fi
  printf '%-8s | %-44s | %-8s | %-8s | %s\n' "$id" "$title" "$status" "$wt" "$deps"
done
