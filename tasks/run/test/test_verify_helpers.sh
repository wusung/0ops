#!/usr/bin/env bash
set -euo pipefail
SELF_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
source "$SELF_DIR/lib.sh"
source "$SELF_DIR/../lib.sh"

# path_matches_glob — globstar-style.
path_matches_glob "internal/fake/f02/handler.go" "internal/fake/f02/**" \
  || { echo "FAIL: glob match should succeed" >&2; exit 1; }
if path_matches_glob "internal/other/x.go" "internal/fake/f02/**"; then
  echo "FAIL: glob match should fail" >&2; exit 1
fi

# Multi-glob OR
path_matches_glob "cmd/fake/f02.go" "internal/fake/f02/**" "cmd/fake/f02.go" \
  || { echo "FAIL: multi-glob should match second pattern" >&2; exit 1; }

echo "OK: test_verify_helpers"
