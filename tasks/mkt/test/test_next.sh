#!/usr/bin/env bash
set -euo pipefail
here="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
tmp="$(mktemp -d)"; cp "$here/fixtures/ledger.md" "$tmp/ledger.md"
# regression: a NON-MKT task referencing the same ADR must not cause a false "already registered" noop
printf '| M9.9 | unrelated audit task | - | docs/adrs/0002-idempotency-and-compensation.md | x |\n' > "$tmp/task-list.md"
: > "$tmp/task-status.md"; : > "$tmp/todo.md"
export MKT_LEDGER="$tmp/ledger.md" MKT_TASK_LIST="$tmp/task-list.md" MKT_TASK_STATUS="$tmp/task-status.md" MKT_TODO="$tmp/todo.md"
id="$(bash "$here/../next.sh" weekly)"
[[ "$id" == "MKT.W1" ]] || { echo "FAIL id=$id"; exit 1; }
grep -q "MKT.W1" "$tmp/task-list.md" && grep -q "MKT.W1" "$tmp/task-status.md" && grep -q "MKT.W1" "$tmp/todo.md" || { echo "FAIL registry"; exit 1; }
bash "$here/../next.sh" weekly >/dev/null; c=$(grep -c "MKT.W1" "$tmp/task-list.md")
[[ "$c" == "1" ]] || { echo "FAIL idempotency c=$c"; exit 1; }
echo "PASS test_next"
