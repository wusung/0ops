#!/usr/bin/env bash
set -euo pipefail
SELF_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
source "$SELF_DIR/lib.sh"
TMP="$(setup_fixture)"
trap 'teardown_fixture' EXIT

TASK_LIST_FILE="$TMP/task-list.md"
TASK_STATUS_FILE="$TMP/task-status.md"
source "$SELF_DIR/../lib.sh"

# F01 = Done → not executable
if task_is_executable F01; then echo "FAIL: F01 (Done) should not be executable" >&2; exit 1; fi
# F02 = Pending, deps F01 Done → executable
task_is_executable F02 || { echo "FAIL: F02 should be executable" >&2; exit 1; }
# F03 = Failed → not executable
if task_is_executable F03; then echo "FAIL: F03 (Failed) should not be executable" >&2; exit 1; fi
# F04 = Pending, deps F02 (Pending) F03 (Failed) → not executable
if task_is_executable F04; then echo "FAIL: F04 should not be executable (deps unmet)" >&2; exit 1; fi

# check_dependencies_done — F04 deps unmet, die calls exit; subshell to isolate.
check_dependencies_done F02 || { echo "FAIL: F02 deps should be met" >&2; exit 1; }
if ( check_dependencies_done F04 ) 2>/dev/null; then
  echo "FAIL: F04 deps should be unmet" >&2; exit 1
fi

echo "OK: test_executability"
