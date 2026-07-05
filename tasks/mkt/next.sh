#!/usr/bin/env bash
# tasks/mkt/next.sh <weekly|monthly|quarterly>
set -euo pipefail
source "$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)/lib.sh"
cadence="${1:-}"
case "$cadence" in
  weekly) prefix="MKT.W" ;; monthly) prefix="MKT.M" ;; quarterly) prefix="MKT.Q" ;;
  *) die "usage: next.sh <weekly|monthly|quarterly>" ;;
esac
src="$(ledger_next_available "$cadence")"
[[ -n "$src" ]] || die "no available $cadence source"
if grep -q "$src" "$TASK_LIST" 2>/dev/null; then echo "noop: $src already registered" >&2; exit 0; fi
n=1; while grep -q "| ${prefix}${n} " "$TASK_LIST" 2>/dev/null; do n=$((n+1)); done
id="${prefix}${n}"
title="Build-in-public $cadence post from $(basename "$src")"
printf '| %s | %s | - | %s | `docs/marketing/**` |\n' "$id" "$title" "docs/features/build-in-public-engine/spec.md, $src" >> "$TASK_LIST"
printf '| %s | %s | Pending | - |\n' "$id" "$title" >> "$TASK_STATUS"
cat >> "$TODO" <<EOF

### $id — $title
- [ ] 依 \`docs/features/build-in-public-engine/spec.md\` §4 由 $src 產出 $cadence 中英雙語 canonical 長文至 \`docs/marketing/posts/\`
- [ ] front-matter 含 \`cadence: $cadence\`、\`source: $src\`
- [ ] 通過 \`./manage.sh mkt-verify <post>\`（G1–G6）
- [ ] sources-ledger 標 $src consumed；editorial-calendar 加列
EOF
echo "$id"
