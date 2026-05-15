#!/usr/bin/env bash
set -euo pipefail
SELF_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
source "$SELF_DIR/lib.sh"
TMP="$(setup_fixture)"
trap 'teardown_fixture' EXIT

# Happy path: F02 is first Pending whose deps (F01) are Done.
OUT="$(TASK_LIST_FILE="$TMP/task-list.md" \
       TASK_STATUS_FILE="$TMP/task-status.md" \
       bash "$SELF_DIR/../next.sh")"
assert_contains "$OUT" "TASK_ID=F02" "next picks F02"
assert_contains "$OUT" "STATUS=Pending" "status field"
assert_contains "$OUT" "TITLE=Pending child" "title field"

# Sad path: mark every Pending Done → next should pick nothing
sed -i 's/| Pending  |/| Done     |/' "$TMP/task-status.md"
set +e
OUT="$(TASK_LIST_FILE="$TMP/task-list.md" \
       TASK_STATUS_FILE="$TMP/task-status.md" \
       bash "$SELF_DIR/../next.sh" 2>&1)"
EC=$?
set -e
assert_exit "$EC" "1" "next.sh non-zero when nothing executable"
assert_contains "$OUT" "no executable task found" "stderr message"

echo "OK: test_next"
