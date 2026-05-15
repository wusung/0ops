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

# --- table readers ---

# Find a task row by ID in the given file. Returns full row text or empty.
task_row_from_file() {
  local file="$1"
  local task_id="$2"
  awk -F'|' -v task_id="$task_id" '
    /^\|[[:space:]]*[A-Za-z][A-Za-z0-9_.-]*[[:space:]]*\|/ {
      id=$2
      gsub(/^[[:space:]]+|[[:space:]]+$/, "", id)
      if (id == task_id) {
        print $0
        exit
      }
    }
  ' "$file"
}

task_field() {
  local row="$1" field_index="$2"
  awk -F'|' -v idx="$field_index" '{ v=$idx; gsub(/^[[:space:]]+|[[:space:]]+$/, "", v); print v }' <<<"$row"
}

task_exists() { [[ -n "$(task_row_from_file "$TASK_LIST_FILE" "$1")" ]]; }

task_title() {
  local row; row="$(task_row_from_file "$TASK_LIST_FILE" "$1")"
  [[ -n "$row" ]] || die "task not found: $1"
  task_field "$row" 3
}

task_status() {
  local row; row="$(task_row_from_file "$TASK_STATUS_FILE" "$1")"
  [[ -n "$row" ]] || die "task status not found: $1"
  task_field "$row" 4
}

# Split a comma-separated cell into one entry per line.
# Strips surrounding backticks and whitespace. Skips empty / `-` / `無` placeholders.
_split_cell() {
  local raw="$1" token
  IFS=',' read -r -a tokens <<<"$raw"
  for token in "${tokens[@]}"; do
    token="$(trim "$token")"
    token="${token//\`/}"
    [[ -z "$token" || "$token" == "-" || "$token" == "無" ]] && continue
    printf '%s\n' "$token"
  done
}

task_dependencies() {
  local row; row="$(task_row_from_file "$TASK_LIST_FILE" "$1")"
  [[ -n "$row" ]] || die "task not found: $1"
  _split_cell "$(task_field "$row" 4)"
}

task_spec_refs() {
  local row; row="$(task_row_from_file "$TASK_LIST_FILE" "$1")"
  [[ -n "$row" ]] || die "task not found: $1"
  _split_cell "$(task_field "$row" 5)"
}

task_expected_paths() {
  local row; row="$(task_row_from_file "$TASK_LIST_FILE" "$1")"
  [[ -n "$row" ]] || die "task not found: $1"
  _split_cell "$(task_field "$row" 6)"
}
