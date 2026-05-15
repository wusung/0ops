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

# --- executability ---

check_dependencies_done() {
  local task_id="$1" dep
  while IFS= read -r dep; do
    [[ -n "$dep" ]] || continue
    if [[ "$(task_status "$dep")" != "Done" ]]; then
      die "dependency not done: $dep for $task_id"
    fi
  done < <(task_dependencies "$task_id")
}

task_is_executable() {
  local task_id="$1" current_status dep
  current_status="$(task_status "$task_id")"
  [[ "$current_status" == "Pending" ]] || return 1
  while IFS= read -r dep; do
    [[ -n "$dep" ]] || continue
    [[ "$(task_status "$dep")" == "Done" ]] || return 1
  done < <(task_dependencies "$task_id")
  return 0
}

# --- agent + command builders ---

# Print the argv (one per line) for the agent CLI.
# Precedence: TASK_AGENT_BIN env > `claude` on PATH > die.
agent_runner() {
  if [[ -n "${TASK_AGENT_BIN:-}" ]]; then
    printf '%s\n' "$TASK_AGENT_BIN"
    return 0
  fi
  if command -v claude >/dev/null 2>&1; then
    printf 'claude\n'
    return 0
  fi
  die "no agent CLI found (set TASK_AGENT_BIN or install 'claude')"
}

# Build full agent invocation (NUL-separated argv).
# Default flavour is `claude -p <prompt> --dangerously-skip-permissions`.
# When TASK_AGENT_BIN is set the runner just appends the prompt as the last arg
# (caller is responsible for any non-default flags via the wrapper script).
build_agent_command() {
  local prompt_text="$1"
  mapfile -t base < <(agent_runner)
  if [[ -n "${TASK_AGENT_BIN:-}" ]]; then
    printf '%s\0' "${base[@]}" "$prompt_text"
  else
    printf '%s\0' "${base[@]}" "-p" "$prompt_text" "--dangerously-skip-permissions"
  fi
}

build_commit_command() {
  local task_id="$1" task_title_value="$2"
  local msg
  msg=$(printf 'task(%s): %s' "$task_id" "$task_title_value")
  printf '%s\0' "git" "commit" "-m" "$msg"
}

build_pr_command() {
  local task_id="$1" task_title_value="$2" body="$3"
  local title
  title=$(printf 'task(%s): %s' "$task_id" "$task_title_value")
  printf '%s\0' "gh" "pr" "create" \
    "--base" "main" \
    "--head" "$(task_branch_name "$task_id")" \
    "--title" "$title" \
    "--body" "$body"
}

build_merge_command() {
  local pr_number="$1"
  printf '%s\0' "gh" "pr" "merge" "$pr_number" "--merge" "--delete-branch"
}

# --- verify + state helpers ---

# Match path against any of the provided glob patterns. Uses bash globstar.
path_matches_glob() {
  local path="$1"
  shift
  shopt -s globstar
  local pattern
  for pattern in "$@"; do
    [[ -n "$pattern" ]] || continue
    # shellcheck disable=SC2053
    if [[ "$path" == $pattern ]]; then
      shopt -u globstar
      return 0
    fi
  done
  shopt -u globstar
  return 1
}

# Within the current worktree, list all paths that differ from `main`
# (committed diff + uncommitted changes + untracked files).
git_changed_paths_vs_main() {
  {
    git diff main --name-only
    git ls-files --others --exclude-standard
  } | sort -u
}

# Mark a task Failed in main + commit a chore record. Run from any cwd.
mark_task_failed() {
  local task_id="$1"
  local main_status="$TASK_REPO_ROOT/tasks/task-status.md"
  python3 - "$main_status" "$task_id" <<'PY'
import re, sys, pathlib
path = pathlib.Path(sys.argv[1])
task_id = sys.argv[2]
text = path.read_text()
pat = re.compile(rf'^(\|\s*{re.escape(task_id)}\s*\|[^|]*\|\s*)(Pending|Done|Failed)(\s*\|)$', re.M)
new_text, n = pat.subn(rf'\1Failed\3', text)
if n != 1:
    sys.exit(f"could not flip {task_id} to Failed (matches={n})")
path.write_text(new_text)
PY
  ( cd "$TASK_REPO_ROOT" \
    && git add tasks/task-status.md \
    && git -c commit.gpgsign=false commit -m "chore(task-runner): mark $task_id failed" )
}
