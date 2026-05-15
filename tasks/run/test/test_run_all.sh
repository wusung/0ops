#!/usr/bin/env bash
set -euo pipefail
SELF_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
source "$SELF_DIR/lib.sh"
TMP="$(setup_fixture)"
trap 'teardown_fixture' EXIT

# Status with everything Done → run-all should print ALL_TASKS_COMPLETED.
sed -i 's/| Pending  |/| Done     |/' "$TMP/task-status.md"
sed -i 's/| Failed   |/| Done     |/' "$TMP/task-status.md"

OUT="$(TASK_LIST_FILE="$TMP/task-list.md" TASK_STATUS_FILE="$TMP/task-status.md" \
       bash "$SELF_DIR/../run-all.sh" 2>&1)"
assert_contains "$OUT" "ALL_TASKS_COMPLETED" "run-all completion banner"

echo "OK: test_run_all"
