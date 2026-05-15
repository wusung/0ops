#!/usr/bin/env bash
set -euo pipefail
source "$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)/lib.sh"

{
  printf 'ID\tTITLE\tSTATUS\tCOMPLETED\tWORKTREE\tDEPENDENCIES\n'
  awk -F'|' '
    /^\|[[:space:]]*[A-Za-z][A-Za-z0-9_.-]*[[:space:]]*\|/ {
      id=$2; title=$3; deps=$4
      gsub(/^[[:space:]]+|[[:space:]]+$/, "", id)
      gsub(/^[[:space:]]+|[[:space:]]+$/, "", title)
      gsub(/^[[:space:]]+|[[:space:]]+$/, "", deps)
      if (id == "ID") next
      print id "\t" title "\t" deps
    }
  ' "$TASK_LIST_FILE" | while IFS=$'\t' read -r id title deps; do
    status="$(task_status "$id")"
    completed="$(task_completed_date "$id")"
    if task_worktree_exists "$id"; then wt="yes"; else wt="no"; fi
    printf '%s\t%s\t%s\t%s\t%s\t%s\n' "$id" "$title" "$status" "$completed" "$wt" "$deps"
  done
} | column -t -s $'\t' -o ' | '
