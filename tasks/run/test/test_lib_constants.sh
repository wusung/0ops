#!/usr/bin/env bash
set -euo pipefail
SELF_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
source "$SELF_DIR/lib.sh"
source "$SELF_DIR/../lib.sh"

assert_eq "$(basename "$TASK_LIST_FILE")"   "task-list.md"   "TASK_LIST_FILE basename"
assert_eq "$(basename "$TASK_STATUS_FILE")" "task-status.md" "TASK_STATUS_FILE basename"
assert_eq "$(basename "$TASK_WORKTREE_DIR")" ".worktrees"    "TASK_WORKTREE_DIR basename"
assert_eq "$(basename "$TASK_SESSION_DIR")"  ".task-sessions" "TASK_SESSION_DIR basename"

assert_eq "$(task_branch_name M2.4)"   "task/M2.4"        "task_branch_name M2.4"
assert_eq "$(basename "$(task_worktree_path M2.4)")" "M2.4" "task_worktree_path basename"
assert_eq "$(basename "$(task_prompt_path M2.4)")"   "prompt.txt" "task_prompt_path basename"

echo "OK: test_lib_constants"
