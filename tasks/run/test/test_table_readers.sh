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

# task_exists
task_exists F01 || { echo "FAIL: task_exists F01" >&2; exit 1; }
if task_exists FZZ; then
  echo "FAIL: task_exists FZZ should be false" >&2; exit 1
fi

# task_title
assert_eq "$(task_title F01)" "Done foundation" "task_title F01"
assert_eq "$(task_title F02)" "Pending child"   "task_title F02"

# task_status
assert_eq "$(task_status F01)" "Done"    "task_status F01"
assert_eq "$(task_status F02)" "Pending" "task_status F02"
assert_eq "$(task_status F03)" "Failed"  "task_status F03"

# task_dependencies — one ID per line, no blanks
assert_eq "$(task_dependencies F01 | tr '\n' ',')" ""              "task_dependencies F01 (none)"
assert_eq "$(task_dependencies F02 | tr '\n' ',')" "F01,"          "task_dependencies F02"
assert_eq "$(task_dependencies F04 | tr '\n' ',')" "F02,F03,"      "task_dependencies F04"

# task_expected_paths — one glob per line
assert_eq "$(task_expected_paths F02 | tr '\n' '|')" "internal/fake/f02/**|cmd/fake/f02.go|" "task_expected_paths F02"

# task_spec_refs — one path per line
assert_eq "$(task_spec_refs F02 | tr '\n' '|')" "docs/fake/f02.md|docs/fake/f02-b.md|" "task_spec_refs F02"

echo "OK: test_table_readers"
