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

# 5. Invoke agent inside the worktree
mapfile -d '' -t agent_argv < <(build_agent_command "$prompt_text")
( cd "$worktree_path" && "${agent_argv[@]}" ) || {
  printf 'AGENT_FAILED=%s\n' "$task_id"
  mark_task_failed "$task_id"
  die "agent exited non-zero for $task_id"
}

# 6. Verify (run inside the worktree)
verify_failed=0

# 6a. status diff for this task ID flipped to Done
status_diff="$(git -C "$worktree_path" diff main -- tasks/task-status.md || true)"
if [[ -z "$status_diff" ]] || ! grep -qE "^\+.*\b${task_id}\b.*\bDone\b" <<<"$status_diff"; then
  printf 'VERIFY_FAILED=status\n' >&2
  verify_failed=1
fi

# 6b. expected paths — at least one match (excluding status file)
if [[ "$verify_failed" -eq 0 ]]; then
  mapfile -t expected < <(task_expected_paths "$task_id")
  matched=""
  while IFS= read -r changed; do
    [[ -n "$changed" ]] || continue
    [[ "$changed" == "tasks/task-status.md" ]] && continue
    if path_matches_glob "$changed" "${expected[@]}"; then
      matched="$changed"
      break
    fi
  done < <(cd "$worktree_path" && git_changed_paths_vs_main)
  if [[ -z "$matched" ]]; then
    printf 'VERIFY_FAILED=expected_paths\n' >&2
    verify_failed=1
  fi
fi

# 6c. make test
if [[ "$verify_failed" -eq 0 ]]; then
  if ! ( cd "$worktree_path" && make test ); then
    printf 'VERIFY_FAILED=make_test\n' >&2
    verify_failed=1
  fi
fi

if [[ "$verify_failed" -ne 0 ]]; then
  mark_task_failed "$task_id"
  die "verify failed for $task_id (worktree preserved at $worktree_path)"
fi
printf 'VERIFY=ok\n'

# 7. Commit on the task branch (inside worktree)
mapfile -d '' -t commit_argv < <(build_commit_command "$task_id" "$task_title_value")
( cd "$worktree_path" && git add -A && "${commit_argv[@]}" )
printf 'COMMIT=%s\n' "$(git -C "$worktree_path" rev-parse HEAD)"

# 8. Push / PR / merge / cleanup — implemented in Task 12
if [[ "${TASK_SKIP_PUSH:-0}" == "1" ]]; then
  printf 'SKIPPED=push_pr_merge\n'
  exit 0
fi

die "run-one.sh push/PR/merge stages not yet implemented"
