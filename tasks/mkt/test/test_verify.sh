#!/usr/bin/env bash
set -euo pipefail
here="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
v="$here/../verify.sh"
export MKT_VERIFY_SKIP_G4=1 MKT_LEDGER="$here/fixtures/ledger-consumed.md" MKT_CALENDAR="$here/fixtures/calendar-ok.md"
bash "$v" "$here/fixtures/post-good.md"         && echo "good ok"                        || { echo "FAIL good"; exit 1; }
bash "$v" "$here/fixtures/post-no-en.md"        && { echo "FAIL no-en"; exit 1; }         || echo "no-en rejected"
bash "$v" "$here/fixtures/post-internal-ref.md" && { echo "FAIL internal-ref"; exit 1; }  || echo "internal-ref rejected"
bash "$v" "$here/fixtures/post-no-cta.md"       && { echo "FAIL no-cta"; exit 1; }        || echo "no-cta rejected"
echo "PASS test_verify"
