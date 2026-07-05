#!/usr/bin/env bash
# tasks/mkt/verify.sh <post-path>
set -euo pipefail
source "$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)/lib.sh"
post="${1:-}"
[[ -n "$post" && -f "$post" ]] || die "usage: verify.sh <existing-post-path>"
fail() { echo "VERIFY FAIL [$1]: $2" >&2; exit 1; }

# G1 bilingual
grep -q '^## 中文' "$post" || fail G1 "missing '## 中文'"
grep -q '^## English' "$post" || fail G1 "missing '## English'"
zh=$(awk '/^## 中文/{f=1;next} /^## English/{f=0} f' "$post" | tr -d '[:space:]')
en=$(awk '/^## English/{f=1;next} f' "$post" | tr -d '[:space:]')
[[ -n "$zh" ]] || fail G1 "zh empty"; [[ -n "$en" ]] || fail G1 "en empty"

# G2 template structure by cadence
cadence=$(sed -n 's/^cadence:[[:space:]]*//p' "$post" | head -1)
case "$cadence" in
  weekly)    reqs=("限制" "選項" "取捨") ;;
  monthly)   reqs=("症狀" "根因" "為何" "制度") ;;
  quarterly) reqs=("痛點" "設計約束" "決策" "驗證" "失敗模式") ;;
  *) fail G2 "unknown cadence: '$cadence'" ;;
esac
for h in "${reqs[@]}"; do grep -q "$h" "$post" || fail G2 "missing marker: $h"; done

# G3 engineering anchor
grep -Eq 'ADR-[0-9]{4}|[A-Za-z0-9_./-]+\.go:[0-9]+|\b[0-9a-f]{7,40}\b' "$post" \
  || fail G3 "no verifiable anchor (ADR-XXXX / file.go:line / sha)"

# G4 boundary (content tasks only; bootstrap sets MKT_VERIFY_SKIP_G4=1)
if [[ "${MKT_VERIFY_SKIP_G4:-0}" != "1" ]]; then
  while IFS= read -r p; do
    [[ -z "$p" ]] && continue
    case "$p" in docs/marketing/*) ;; *) fail G4 "change outside docs/marketing/: $p" ;; esac
  done < <(git -C "$REPO_ROOT" status --porcelain | awk '{print $2}')
fi

# G5 ledger + calendar
src=$(sed -n 's/^source:[[:space:]]*//p' "$post" | head -1)
[[ -n "$src" ]] || fail G5 "missing 'source:' front-matter"
grep -F "$src" "$LEDGER" | grep -q 'consumed' || fail G5 "source not consumed in ledger: $src"
grep -Fq "$(basename "$post")" "$CALENDAR" || fail G5 "post not in editorial-calendar"

# G6 threads length
qfile="$QUEUE_DIR/$(basename "$post" .md).yaml"
if [[ -f "$qfile" ]]; then
  tlen=$(awk '/^threads:/{f=1;next} f&&/^[a-z]+:/{f=0} f' "$qfile" | tr -d '\n' | wc -m)
  (( tlen <= 500 )) || fail G6 "threads $tlen > 500 chars"
fi

echo "VERIFY PASS: $post"
