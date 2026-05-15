#!/usr/bin/env bash
set -euo pipefail
SELF_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
source "$SELF_DIR/lib.sh"
TMP="$(setup_fixture)"
trap 'teardown_fixture' EXIT

# Override BEFORE sourcing run/lib.sh so its conditional assignments pick them up.
TASK_LIST_FILE="$TMP/task-list.md"
TASK_STATUS_FILE="$TMP/task-status.md"
source "$SELF_DIR/../lib.sh"

# Case 1: Pending → Failed on a row whose Title contains the word "Pending"
# (F02 title is "Pending child"; regression guard against matching inside Title).
flip_task_status F02 Failed
assert_eq "$(task_status F02)" "Failed"  "F02 flipped to Failed"
assert_eq "$(task_status F01)" "Done"    "F01 untouched"
assert_eq "$(task_status F03)" "Failed"  "F03 untouched"
assert_eq "$(task_status F04)" "Pending" "F04 untouched"

# Completed Date column must be preserved (regression guard against the
# `$`-anchored regex bug that dropped 4-column tables on the floor).
assert_eq "$(task_completed_date F02)" "-" "F02 completed_date preserved"
assert_eq "$(task_completed_date F01)" "2026-05-10" "F01 completed_date preserved"

# Case 2: re-flip back to Pending
flip_task_status F02 Pending
assert_eq "$(task_status F02)" "Pending" "F02 flipped back to Pending"

# Case 3: non-existent task ID exits non-zero with descriptive message
set +e
err="$(flip_task_status FZZ Failed 2>&1)"
code=$?
set -e
assert_exit "$code" "1" "flip on missing task exits 1"
assert_contains "$err" "could not flip FZZ" "error message names the task"

echo "OK: test_flip_task_status"
