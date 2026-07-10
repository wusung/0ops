#!/usr/bin/env bash
set -euo pipefail
MKT_LIB_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
REPO_ROOT="$(cd "$MKT_LIB_DIR/../.." && pwd)"
MARKETING_DIR="$REPO_ROOT/docs/marketing"
LEDGER="${MKT_LEDGER:-$MARKETING_DIR/sources-ledger.md}"
CALENDAR="${MKT_CALENDAR:-$MARKETING_DIR/editorial-calendar.md}"
POSTS_DIR="${MKT_POSTS_DIR:-$MARKETING_DIR/posts}"
QUEUE_DIR="$MARKETING_DIR/queue"
PUBLISHED_LEDGER="${MKT_PUBLISHED_LEDGER:-$MARKETING_DIR/published-ledger.md}"
TASK_LIST="${MKT_TASK_LIST:-$REPO_ROOT/tasks/task-list.md}"
TASK_STATUS="${MKT_TASK_STATUS:-$REPO_ROOT/tasks/task-status.md}"
TODO="${MKT_TODO:-$REPO_ROOT/tasks/todo.md}"

die() { echo "mkt: $*" >&2; exit 1; }

# ledger table cols: | source | cadence | status | post |
ledger_next_available() {
  local cadence="$1"
  awk -F'|' -v c="$cadence" '
    NR>2 && /\|/ {
      s=$2; cad=$3; st=$4;
      gsub(/^[ \t]+|[ \t]+$/,"",s); gsub(/^[ \t]+|[ \t]+$/,"",cad); gsub(/^[ \t]+|[ \t]+$/,"",st);
      if (cad==c && st=="available") { print s; exit }
    }' "$LEDGER"
}
