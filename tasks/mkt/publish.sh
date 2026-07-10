#!/usr/bin/env bash
# tasks/mkt/publish.sh <queue-item.yaml> [--publish]
set -euo pipefail
source "$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)/lib.sh"
item="${1:-}"; mode="dry-run"
[[ -n "$item" && -f "$item" ]] || die "usage: publish.sh <queue-item.yaml> [--publish]"
[[ "${2:-}" == "--publish" ]] && mode="publish"
post_id="$(basename "$item" .yaml)"
# Resolve {{canonical_url}} → <base>/blog/<slug>. The blog page is named by the
# POST's front-matter `slug` (mkt-site writes blog/<slug>.html), NOT by the queue
# item's filename — real queue files carry only `post:` and no `slug:`, so we must
# read the slug from the post to match the rendered page. Fall back to post_id only
# when the post file or its slug is absent. spec §4.
base="${MKT_SITE_BASE_URL:-https://0ops.sh}"
post_file="$POSTS_DIR/$post_id.md"
slug=""
if [[ -f "$post_file" ]]; then
  slug="$(awk -F':' '/^slug:/ {gsub(/^[ \t]+|[ \t]+$/,"",$2); gsub(/^["'\'']|["'\'']$/,"",$2); print $2; exit}' "$post_file")"
fi
[[ -n "$slug" ]] || slug="$post_id"
canonical_url="$base/blog/$slug"
for ch in fb threads; do
  key="$(printf '%s|%s' "$post_id" "$ch" | sha256sum | cut -c1-16)"
  if grep -q "$key" "$PUBLISHED_LEDGER" 2>/dev/null; then echo "skip (already published): $ch $post_id"; continue; fi
  body="$(awk -v c="$ch" '$0 ~ "^"c":" {f=1; next} f && /^[a-z_]+:/{f=0} f' "$item" | sed 's/^[[:space:]]*//')"
  body="${body//\{\{canonical_url\}\}/$canonical_url}"
  echo "=== channel=$ch post=$post_id dedup=$key ==="; printf '%s\n' "$body"
  if [[ "$mode" == "publish" ]]; then
    die "real publish disabled in MKT.1 (needs Meta creds + MKT_PUBLISH_CONFIRMED=1) — spec §9 MKT.2"
  else
    echo "[dry-run] would POST to $ch; no network."
  fi
done
