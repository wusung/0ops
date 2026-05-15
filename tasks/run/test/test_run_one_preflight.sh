#!/usr/bin/env bash
set -euo pipefail
SELF_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
source "$SELF_DIR/lib.sh"
TMP="$(setup_fixture)"
trap 'teardown_fixture' EXIT

# Stub agent: no-op, exit 0. Forces preflight-only short-circuit.
export TASK_AGENT_BIN="/bin/true"
export TASK_RUN_ONE_PREFLIGHT_ONLY=1

# 1. Unknown task → die
set +e
OUT="$(TASK_LIST_FILE="$TMP/task-list.md" TASK_STATUS_FILE="$TMP/task-status.md" \
       bash "$SELF_DIR/../run-one.sh" FZZ 2>&1)"
EC=$?
set -e
assert_exit "$EC" "1" "unknown task exits 1"
assert_contains "$OUT" "task not found" "unknown task message"

# 2. status==Done without --force → die
set +e
OUT="$(TASK_LIST_FILE="$TMP/task-list.md" TASK_STATUS_FILE="$TMP/task-status.md" \
       bash "$SELF_DIR/../run-one.sh" F01 2>&1)"
EC=$?
set -e
assert_exit "$EC" "1" "Done task exits 1 without --force"
assert_contains "$OUT" "status not Pending" "Done refusal message"

# 3. status==Failed without --force → die
set +e
OUT="$(TASK_LIST_FILE="$TMP/task-list.md" TASK_STATUS_FILE="$TMP/task-status.md" \
       bash "$SELF_DIR/../run-one.sh" F03 2>&1)"
EC=$?
set -e
assert_exit "$EC" "1" "Failed task exits 1 without --force"

# 4. deps unmet without --force → die
set +e
OUT="$(TASK_LIST_FILE="$TMP/task-list.md" TASK_STATUS_FILE="$TMP/task-status.md" \
       bash "$SELF_DIR/../run-one.sh" F04 2>&1)"
EC=$?
set -e
assert_exit "$EC" "1" "deps unmet exits 1"
assert_contains "$OUT" "dependency not done" "deps message"

echo "OK: test_run_one_preflight"
