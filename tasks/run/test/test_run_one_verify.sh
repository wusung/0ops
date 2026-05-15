#!/usr/bin/env bash
set -euo pipefail
SELF_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
source "$SELF_DIR/lib.sh"

REPO_ROOT_REAL="$(repo_root)"
TMP="$(mktemp -d "${TMPDIR:-/tmp}/run-one-verify.XXXXXX")"
trap 'rm -rf "$TMP"' EXIT
cd "$TMP"
git init -q
git config user.email t@x
git config user.name t
git checkout -q -b main
mkdir -p tasks internal/fake/f02
cp "$REPO_ROOT_REAL/tasks/run/test/fixtures/task-list.md" tasks/task-list.md
cp "$REPO_ROOT_REAL/tasks/run/test/fixtures/task-status.md" tasks/task-status.md
# Stub Makefile so `make test` succeeds inside the worktree.
printf 'test:\n\t@echo ok\n' >Makefile
# Copy run scripts into the test repo so lib.sh's git -C lookup resolves to TMP.
mkdir -p tasks/run
cp -r "$REPO_ROOT_REAL/tasks/run/"*.sh tasks/run/
cp -r "$REPO_ROOT_REAL/tasks/run/test" tasks/run/test
git add . && git -c commit.gpgsign=false commit -q -m init

# Fake agent: writes a file in expected path + flips F02 status to Done.
cat >fake-agent.sh <<'EOA'
#!/usr/bin/env bash
set -euo pipefail
mkdir -p internal/fake/f02
echo "agent: noop with side effects" >internal/fake/f02/handler.go
sed -i 's/| F02  | Pending child   | Pending  | -              |/| F02  | Pending child   | Done     | 2026-05-15     |/' tasks/task-status.md
EOA
chmod +x fake-agent.sh

export TASK_AGENT_BIN="$TMP/fake-agent.sh"
export TASK_SKIP_PUSH=1   # short-circuit before push/PR/merge

OUT="$(bash "$TMP/tasks/run/run-one.sh" F02 2>&1)" || { echo "$OUT"; exit 1; }

assert_contains "$OUT" "MODE=fresh" "fresh worktree mode"
assert_contains "$OUT" "VERIFY=ok"  "verify section ran clean"
# A commit landed on task/F02
git -C "$TMP/.worktrees/F02" log --oneline | head -1 | grep -q "task(F02)" \
  || { echo "FAIL: missing task(F02) commit" >&2; exit 1; }

echo "OK: test_run_one_verify"
