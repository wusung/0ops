#!/usr/bin/env bash
set -euo pipefail
here="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
tmp="$(mktemp -d)"; : > "$tmp/published.md"
export MKT_PUBLISHED_LEDGER="$tmp/published.md"
# Real queue files carry only `post:` (no `slug:`); publish.sh must resolve the
# slug from the POST front-matter under $MKT_POSTS_DIR. Point it at a fixtures
# post dir whose queue-item.md declares `slug: sample-promo-slug`.
export MKT_POSTS_DIR="$here/fixtures/posts"
out="$(bash "$here/../publish.sh" "$here/fixtures/queue-item.yaml")"
echo "$out" | grep -q "channel=fb" && echo "$out" | grep -q "channel=threads" || { echo "FAIL channels"; exit 1; }
echo "$out" | grep -q "dry-run" || { echo "FAIL dry-run"; exit 1; }
echo "$out" | grep -q 'preview→confirm' || { echo "FAIL body empty"; exit 1; }
# canonical_url must resolve to https://0ops.sh/blog/<slug> where <slug> comes from
# the POST front-matter (sample-promo-slug), not the queue filename, and leave no placeholder.
echo "$out" | grep -q 'https://0ops.sh/blog/sample-promo-slug' || { echo "FAIL canonical_url not resolved from post slug"; exit 1; }
echo "$out" | grep -q '{{' && { echo "FAIL placeholder left in output"; exit 1; } || true
bash "$here/../publish.sh" "$here/fixtures/queue-item.yaml" --publish 2>/dev/null && { echo "FAIL publish-guard"; exit 1; } || echo "publish guarded"
echo "PASS test_publish"
