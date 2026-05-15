#!/usr/bin/env bash
set -euo pipefail
SELF_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
source "$SELF_DIR/lib.sh"
TMP="$(setup_fixture)"
trap 'teardown_fixture' EXIT

OUT="$(TASK_LIST_FILE="$TMP/task-list.md" \
       TASK_STATUS_FILE="$TMP/task-status.md" \
       bash "$SELF_DIR/../show.sh")"

assert_contains "$OUT" "ID"        "header column ID"
assert_contains "$OUT" "TITLE"     "header column TITLE"
assert_contains "$OUT" "STATUS"    "header column STATUS"
assert_contains "$OUT" "COMPLETED" "header column COMPLETED"
assert_contains "$OUT" "WORKTREE"  "header column WORKTREE"
assert_contains "$OUT" "F01"       "row F01"
assert_contains "$OUT" "2026-05-10" "row F01 completed date"
assert_contains "$OUT" "Pending child" "row F02 title"
assert_contains "$OUT" "Failed"    "row F03 status"

echo "OK: test_show"
