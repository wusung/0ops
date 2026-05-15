#!/usr/bin/env bash
set -euo pipefail
SELF_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
pass=0
fail=0
failed_tests=()
for t in "$SELF_DIR"/test_*.sh; do
  [[ -f "$t" ]] || continue
  if bash "$t"; then
    pass=$((pass + 1))
  else
    fail=$((fail + 1))
    failed_tests+=("$(basename "$t")")
  fi
done
printf '\n=== Task runner tests: %d passed, %d failed ===\n' "$pass" "$fail"
if (( fail > 0 )); then
  printf 'Failed:\n'
  printf -- '- %s\n' "${failed_tests[@]}"
  exit 1
fi
