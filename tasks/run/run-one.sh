#!/usr/bin/env bash
set -euo pipefail
source "$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)/lib.sh"

force="false"
while [[ $# -gt 0 ]]; do
  case "$1" in
    --force) force="true"; shift ;;
    --) shift; break ;;
    -*) die "unknown flag: $1" ;;
    *) break ;;
  esac
done

[[ $# -eq 1 ]] || die "usage: $0 [--force] <TASK_ID>"
task_id="$1"

# 1. Existence
task_exists "$task_id" || die "task not found: $task_id"
task_title_value="$(task_title "$task_id")"

# 2. Status / deps
if [[ "$force" != "true" ]]; then
  current_status="$(task_status "$task_id")"
  [[ "$current_status" == "Pending" ]] || die "status not Pending for $task_id (got $current_status); use --force to override"
  check_dependencies_done "$task_id"
fi

# 3. Worktree
worktree_path="$(task_worktree_path "$task_id")"
branch_name="$(task_branch_name "$task_id")"
mode="fresh"

if [[ -d "$worktree_path" ]]; then
  if [[ "$force" == "true" ]]; then
    git worktree remove --force "$worktree_path"
    git branch -D "$branch_name" 2>/dev/null || true
    mkdir -p "$TASK_WORKTREE_DIR"
    git worktree add "$worktree_path" -b "$branch_name"
    mode="fresh"
  else
    printf 'RESUMING=%s\n' "$task_id"
    mode="resume"
  fi
else
  mkdir -p "$TASK_WORKTREE_DIR"
  git worktree add "$worktree_path" -b "$branch_name"
  mode="fresh"
fi

# 4. Compose prompt
prompt_path="$(task_prompt_path "$task_id")"
mkdir -p "$(dirname "$prompt_path")"
if [[ "$mode" == "resume" ]]; then
  bash "$TASK_LIB_DIR/prompt.sh" --resume "$task_id" >"$prompt_path"
else
  bash "$TASK_LIB_DIR/prompt.sh" "$task_id" >"$prompt_path"
fi
prompt_text="$(<"$prompt_path")"

printf 'TASK_ID=%s\n' "$task_id"
printf 'TITLE=%s\n' "$task_title_value"
printf 'WORKTREE=%s\n' "$worktree_path"
printf 'BRANCH=%s\n' "$branch_name"
printf 'MODE=%s\n' "$mode"
printf 'PROMPT_FILE=%s\n' "$prompt_path"

# Debug short-circuit (used by tests to avoid invoking real agent)
if [[ "${TASK_RUN_ONE_PREFLIGHT_ONLY:-0}" == "1" ]]; then
  printf 'PREFLIGHT_ONLY=stop\n'
  exit 0
fi

# (steps 5+ added in Task 11 / Task 12)
die "run-one.sh post-preflight stages not yet implemented"
