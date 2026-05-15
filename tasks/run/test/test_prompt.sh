#!/usr/bin/env bash
set -euo pipefail
SELF_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
source "$SELF_DIR/lib.sh"
TMP="$(setup_fixture)"
trap 'teardown_fixture' EXIT

OUT="$(TASK_LIST_FILE="$TMP/task-list.md" \
       TASK_STATUS_FILE="$TMP/task-status.md" \
       bash "$SELF_DIR/../prompt.sh" F02)"

assert_contains "$OUT" "Task F02" "prompt mentions task ID"
assert_contains "$OUT" "Pending child" "prompt mentions title"
assert_contains "$OUT" "Mandatory Agent Loop"  "prompt cites Mandatory Loop"
assert_contains "$OUT" "AGENTS.md"             "reading list includes AGENTS.md"
assert_contains "$OUT" "docs/adr-reading-strategy.md" "reading list includes ADR strategy"
assert_contains "$OUT" "docs/fake/f02.md"      "spec ref appended"
assert_contains "$OUT" "docs/fake/f02-b.md"    "spec ref appended (multi)"
assert_contains "$OUT" "tasks/task-status.md"  "task-status file referenced"
assert_contains "$OUT" "make test"             "test requirement noted"
assert_contains "$OUT" "compose + Makefile"    "compose-via-Makefile rule cited"
assert_contains "$OUT" "Done"                  "asks agent to flip status to Done"
assert_contains "$OUT" "Expected Paths"        "expected-paths section present"
assert_contains "$OUT" "internal/fake/f02/**"  "expected-paths glob injected"
assert_contains "$OUT" "cmd/fake/f02.go"       "expected-paths glob injected (multi)"

# Resume mode injects extra line
OUT_RESUME="$(TASK_LIST_FILE="$TMP/task-list.md" \
              TASK_STATUS_FILE="$TMP/task-status.md" \
              bash "$SELF_DIR/../prompt.sh" --resume F02)"
assert_contains "$OUT_RESUME" "RESUME" "resume marker present"

echo "OK: test_prompt"
