#!/usr/bin/env bash
# tasks/mkt/deploy-site.sh [--deploy]
#
# Deploy the marketing static site to Cloudflare Pages. This round is dry-run
# only: it prints the exact `wrangler pages deploy` command and makes no network
# calls. Real deploy (--deploy) is guarded and deferred to MKT.4 — it requires
# a CF Pages project + API token, which are not wired this round.
#
# See docs/features/marketing-landing-site/spec.md §5, §7.
set -euo pipefail

here="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
repo_root="$(cd "$here/../.." && pwd)"

DIST="docs/marketing/site/dist"
PROJECT="${MKT_SITE_CF_PROJECT:-0ops-site}"
CMD="wrangler pages deploy $DIST --project-name $PROJECT"

mode="dry-run"
[[ "${1:-}" == "--deploy" ]] && mode="deploy"

if [[ ! -d "$repo_root/$DIST" ]]; then
  echo "deploy-site: no $DIST — run './manage.sh mkt-site-build' first" >&2
  exit 1
fi

if [[ "$mode" == "deploy" ]]; then
  # Guard: real deploy is out of scope this round (MKT.4, gated).
  if [[ -z "${CF_API_TOKEN:-}" || "${MKT_SITE_DEPLOY_CONFIRMED:-}" != "1" ]]; then
    echo "deploy-site: real deploy disabled in MKT.3." >&2
    echo "  needs CF_API_TOKEN + MKT_SITE_DEPLOY_CONFIRMED=1 (CF Pages project) — spec §7 MKT.4" >&2
    exit 1
  fi
  echo "deploy-site: MKT.4 real deploy path not wired this round." >&2
  exit 1
fi

echo "=== mkt-site deploy (dry-run) ==="
echo "[dry-run] would run (no network):"
echo "  $CMD"
