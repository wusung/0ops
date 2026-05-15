#!/usr/bin/env bash
set -euo pipefail
SELF_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
source "$SELF_DIR/lib.sh"
source "$SELF_DIR/../lib.sh"

# Default agent_runner picks claude when available, else dies; in tests we stub via TASK_AGENT_BIN.
TASK_AGENT_BIN="/bin/echo"
mapfile -t cmd < <(agent_runner)
assert_eq "${cmd[0]}" "/bin/echo" "agent_runner honours TASK_AGENT_BIN"

# build_agent_command produces NUL-separated argv
TASK_AGENT_BIN="/bin/echo"
mapfile -d '' -t argv < <(build_agent_command "hello world")
assert_eq "${argv[0]}" "/bin/echo" "build_agent_command argv[0]"
assert_contains "${argv[*]}" "hello world" "build_agent_command includes prompt"

# build_commit_command
mapfile -d '' -t cmt < <(build_commit_command M2.4 "K3s namespace isolation")
assert_eq "${cmt[0]}" "git" "build_commit_command starts with git"
assert_contains "${cmt[*]}" "task(M2.4): K3s namespace isolation" "commit message format"

# build_pr_command
mapfile -d '' -t pr < <(build_pr_command M2.4 "K3s namespace isolation" "body line")
assert_eq "${pr[0]}" "gh" "build_pr_command starts with gh"
assert_contains "${pr[*]}" "task(M2.4): K3s namespace isolation" "PR title"

# build_merge_command
mapfile -d '' -t mg < <(build_merge_command 42)
assert_contains "${mg[*]}" "--merge" "merge mode"
assert_contains "${mg[*]}" "--delete-branch" "delete branch on merge"
assert_contains "${mg[*]}" "42" "merge target PR number"

echo "OK: test_command_builders"
