#!/usr/bin/env bash
# test_run_one_verify_only.sh — exercise the --verify-only recovery path.
# Scenario: a prior run-one invocation flipped main's task-status to Failed
# after verify failed, but the worktree already contains agent's work. The
# user adjusts task-list.md (or fixes whatever made verify fail) and reruns
# with --verify-only so the harness picks up commit/push/PR without invoking
# the agent again.

set -euo pipefail
SELF_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
source "$SELF_DIR/lib.sh"

REPO_ROOT_REAL="$(repo_root)"
TMP="$(mktemp -d "${TMPDIR:-/tmp}/run-one-verify-only.XXXXXX")"
trap 'rm -rf "$TMP"' EXIT
cd "$TMP"
git init -q
git config user.email t@x
git config user.name t
git checkout -q -b main
mkdir -p tasks
cp "$REPO_ROOT_REAL/tasks/run/test/fixtures/task-list.md" tasks/task-list.md
cp "$REPO_ROOT_REAL/tasks/run/test/fixtures/task-status.md" tasks/task-status.md
printf '#!/usr/bin/env bash\nif [ "$1" = "test" ]; then echo ok; exit 0; fi\nexit 0\n' >manage.sh
chmod +x manage.sh
mkdir -p tasks/run
cp -r "$REPO_ROOT_REAL/tasks/run/"*.sh tasks/run/
cp -r "$REPO_ROOT_REAL/tasks/run/test" tasks/run/test
git add . && git -c commit.gpgsign=false commit -q -m init

# Simulate the post-failure state:
#   - worktree exists with agent's work (expected path file + Done flip)
#   - main has been flipped to Failed by mark_task_failed
mkdir -p .worktrees
git worktree add -q .worktrees/F02 -b task/F02

mkdir -p .worktrees/F02/internal/fake/f02
echo "agent: simulated artifact" >.worktrees/F02/internal/fake/f02/handler.go
sed -i 's/| F02  | Pending child   | Pending  | -              |/| F02  | Pending child   | Done     | 2026-05-15     |/' \
  .worktrees/F02/tasks/task-status.md

sed -i 's/| F02  | Pending child   | Pending  | -              |/| F02  | Pending child   | Failed   | -              |/' \
  tasks/task-status.md
git add tasks/task-status.md
git -c commit.gpgsign=false commit -q -m "chore(task-runner): mark F02 failed"

# --- Test 1: happy path ---
# /bin/false as agent — if --verify-only forgets to skip step 5 the script
# dies before VERIFY=ok and the test fails loudly.
export TASK_AGENT_BIN="/bin/false"
export TASK_SKIP_PUSH=1

OUT="$(bash "$TMP/tasks/run/run-one.sh" --verify-only F02 2>&1)" \
  || { printf '%s\n' "$OUT" >&2; echo "FAIL: --verify-only happy path exited non-zero" >&2; exit 1; }

assert_contains "$OUT" "MODE=verify_only" "mode reported as verify_only"
assert_contains "$OUT" "VERIFY=ok"        "verify passed"
[[ "$OUT" != *"PROMPT_FILE="* ]] \
  || { echo "FAIL: PROMPT_FILE leaked into verify-only output" >&2; exit 1; }

git -C "$TMP/.worktrees/F02" log -1 --format=%s | grep -q "task(F02)" \
  || { echo "FAIL: missing task(F02) commit" >&2; exit 1; }

# --- Test 2: --verify-only requires existing worktree ---
git worktree remove --force "$TMP/.worktrees/F02"
git branch -D task/F02 >/dev/null 2>&1 || true

set +e
OUT2="$(bash "$TMP/tasks/run/run-one.sh" --verify-only F02 2>&1)"
EC=$?
set -e
assert_exit "$EC" "1" "missing worktree exits 1"
assert_contains "$OUT2" "requires existing worktree" "missing-worktree message"

# --- Test 3: --verify-only conflicts with --force ---
set +e
OUT3="$(bash "$TMP/tasks/run/run-one.sh" --verify-only --force F02 2>&1)"
EC=$?
set -e
assert_exit "$EC" "1" "verify-only + force exits 1"
assert_contains "$OUT3" "conflicts with --force" "conflict message"

echo "OK: test_run_one_verify_only"
