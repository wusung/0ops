#!/usr/bin/env bash
set -euo pipefail
here="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
export MKT_LEDGER="$here/fixtures/ledger.md"
source "$here/../lib.sh"
got="$(ledger_next_available weekly)"
[[ "$got" == "docs/adrs/0002-idempotency-and-compensation.md" ]] || { echo "FAIL: got '$got'"; exit 1; }
[[ "$(ledger_next_available quarterly)" == "milestone:M6-app-source-ingestion" ]] || { echo "FAIL quarterly"; exit 1; }
echo "PASS test_lib"
