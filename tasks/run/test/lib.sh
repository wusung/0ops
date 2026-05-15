#!/usr/bin/env bash
# Test helpers shared by tasks/run/test/test_*.sh

set -euo pipefail

TEST_TMP_DIR=""

assert_eq() {
  local actual="$1" expected="$2" desc="$3"
  if [[ "$actual" != "$expected" ]]; then
    printf 'FAIL: %s\n  expected: %s\n  actual:   %s\n' "$desc" "$expected" "$actual" >&2
    return 1
  fi
}

assert_contains() {
  local haystack="$1" needle="$2" desc="$3"
  if [[ "$haystack" != *"$needle"* ]]; then
    printf 'FAIL: %s\n  needle:   %s\n  haystack: %s\n' "$desc" "$needle" "$haystack" >&2
    return 1
  fi
}

assert_exit() {
  local actual_code="$1" expected_code="$2" desc="$3"
  if [[ "$actual_code" != "$expected_code" ]]; then
    printf 'FAIL: %s\n  expected exit: %s\n  actual exit:   %s\n' "$desc" "$expected_code" "$actual_code" >&2
    return 1
  fi
}

repo_root() {
  git -C "$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)" rev-parse --show-toplevel
}

setup_fixture() {
  TEST_TMP_DIR="$(mktemp -d "${TMPDIR:-/tmp}/task-runner-test.XXXXXX")"
  cp "$(repo_root)/tasks/run/test/fixtures/task-list.md" "$TEST_TMP_DIR/task-list.md"
  cp "$(repo_root)/tasks/run/test/fixtures/task-status.md" "$TEST_TMP_DIR/task-status.md"
  printf '%s\n' "$TEST_TMP_DIR"
}

teardown_fixture() {
  if [[ -n "$TEST_TMP_DIR" && -d "$TEST_TMP_DIR" ]]; then
    rm -rf "$TEST_TMP_DIR"
  fi
  TEST_TMP_DIR=""
}
