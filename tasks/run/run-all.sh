#!/usr/bin/env bash
set -euo pipefail
source "$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)/lib.sh"

max_iterations="${TASK_RUN_ALL_MAX_ITERATIONS:-50}"
count=0

while true; do
  if next_output="$("$TASK_LIB_DIR/next.sh" 2>&1)"; then
    :
  else
    next_status=$?
    next_clean="${next_output//$'\r'/}"
    if grep -q "no executable task found" <<<"$next_clean"; then
      printf 'ALL_TASKS_COMPLETED\n'
      exit 0
    fi
    printf '%s\n' "$next_output" >&2
    die "next.sh failed unexpectedly (status=$next_status)"
  fi

  task_id="$(awk -F'=' '/^TASK_ID=/{print $2; exit}' <<<"$next_output")"
  [[ -n "$task_id" ]] || die "next.sh did not return TASK_ID"

  printf 'RUNNING=%s\n' "$task_id"
  if "$TASK_LIB_DIR/run-one.sh" "$task_id"; then
    :
  else
    run_one_status=$?
    die "run-one failed for $task_id (status=$run_one_status)"
  fi

  printf 'COMPLETED=%s\n' "$task_id"
  count=$((count + 1))
  if (( count >= max_iterations )); then
    die "run-all exceeded max iterations: $max_iterations"
  fi
  printf 'ITERATION=%d\n' "$count"
done
