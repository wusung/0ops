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

# G2 promo structure: valid cadence + a headline present in each language section
cadence=$(sed -n 's/^cadence:[[:space:]]*//p' "$post" | head -1)
case "$cadence" in weekly|monthly|quarterly) ;; *) fail G2 "unknown or missing cadence: '$cadence'" ;; esac
zh_head=$(awk '/^## 中文/{f=1;next} /^## English/{f=0} f&&/^# /{print;exit}' "$post")
en_head=$(awk '/^## English/{f=1;next} f&&/^# /{print;exit}' "$post")
[[ -n "$zh_head" ]] || fail G2 "missing '# headline' in 中文 section"
[[ -n "$en_head" ]] || fail G2 "missing '# headline' in English section"

# G3 external-safe promo (see docs/marketing/WRITING-PRINCIPLES.md):
#   (a) no internal jargon leakage — reject ADR-XXXX / file.go:line
#   (b) must carry a call-to-action — install command or try link
if grep -Eq 'ADR-[0-9]{4}|[A-Za-z0-9_./-]+\.go:[0-9]+' "$post"; then
  fail G3 "internal reference leaked into external copy (ADR-XXXX / file.go:line) — WRITING-PRINCIPLES.md rule 2"
fi
grep -Eq 'curl |0ops (apps|auth)|https?://' "$post" \
  || fail G3 "no call-to-action (install command / try link) — WRITING-PRINCIPLES.md rule 6"

# G4 boundary (content tasks only; bootstrap sets MKT_VERIFY_SKIP_G4=1)
if [[ "${MKT_VERIFY_SKIP_G4:-0}" != "1" ]]; then
  while IFS= read -r -d '' p; do
    [[ -z "$p" ]] && continue
    case "$p" in docs/marketing/*) ;; *) fail G4 "change outside docs/marketing/: $p" ;; esac
  done < <( { git -C "$REPO_ROOT" diff --name-only --no-renames -z HEAD; git -C "$REPO_ROOT" ls-files --others --exclude-standard -z; } )
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
