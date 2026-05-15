#!/usr/bin/env bash
# tasks/run/lib.sh — shared library for task runner.
# Sourced by show.sh / next.sh / prompt.sh / run-one.sh / run-all.sh.

set -euo pipefail

readonly TASK_LIB_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
readonly TASK_REPO_ROOT="$(git -C "$TASK_LIB_DIR" rev-parse --show-toplevel)"
# These four allow env override (set the var BEFORE sourcing lib.sh) so tests
# can swap in fixture paths.
TASK_LIST_FILE="${TASK_LIST_FILE:-$TASK_REPO_ROOT/tasks/task-list.md}"
TASK_STATUS_FILE="${TASK_STATUS_FILE:-$TASK_REPO_ROOT/tasks/task-status.md}"
TASK_WORKTREE_DIR="${TASK_WORKTREE_DIR:-$TASK_REPO_ROOT/.worktrees}"
TASK_SESSION_DIR="${TASK_SESSION_DIR:-$TASK_REPO_ROOT/.task-sessions}"

die() {
  printf 'ERROR: %s\n' "$1" >&2
  exit 1
}

trim() {
  local value="$1"
  value="${value#"${value%%[![:space:]]*}"}"
  value="${value%"${value##*[![:space:]]}"}"
  printf '%s' "$value"
}

task_branch_name()    { printf 'task/%s' "$1"; }
task_worktree_path()  { printf '%s/%s' "$TASK_WORKTREE_DIR" "$1"; }
task_prompt_path()    { printf '%s/%s/prompt.txt' "$TASK_SESSION_DIR" "$1"; }
task_worktree_exists() { [[ -d "$(task_worktree_path "$1")" ]]; }
