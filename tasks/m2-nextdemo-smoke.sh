#!/usr/bin/env bash
set -euo pipefail

required_vars=(
  OPS_API_BASE
  OPS_BEARER_TOKEN
  OPS_TEAM_SLUG
  OPS_CALLBACK_SECRET
)

for v in "${required_vars[@]}"; do
  if [[ -z "${!v:-}" ]]; then
    echo "missing required env: ${v}" >&2
    exit 1
  fi
done

if ! command -v jq >/dev/null 2>&1; then
  echo "jq is required" >&2
  exit 1
fi

app_slug="${OPS_APP_SLUG:-nextdemo}"
repo_url="${OPS_REPO_URL:-https://github.com/vercel/next.js}"
repo_ref="${OPS_REPO_REF:-main}"
wait_seconds="${OPS_WAIT_SECONDS:-10}"
target_url="${OPS_TARGET_URL:-https://${app_slug}.winshare.tw}"
trace_id="m2-smoke-$(date -u +%Y%m%d%H%M%S)"

api_base="${OPS_API_BASE%/}"
auth_header="Authorization: Bearer ${OPS_BEARER_TOKEN}"

echo "==> preview create_app: ${app_slug}"
preview_payload="$(jq -nc \
  --arg slug "${app_slug}" \
  --arg repo "${repo_url}" \
  --arg ref "${repo_ref}" \
  '{slug:$slug,repo_url:$repo,ref:$ref}')"

preview_resp="$(curl -sS -f \
  -X POST "${api_base}/v1/teams/${OPS_TEAM_SLUG}/apps:preview" \
  -H "${auth_header}" \
  -H "Content-Type: application/json" \
  -d "${preview_payload}")"
preview_id="$(jq -r '.preview_id // empty' <<<"${preview_resp}")"
if [[ -z "${preview_id}" ]]; then
  echo "missing preview_id: ${preview_resp}" >&2
  exit 1
fi

echo "==> confirm create_app: preview_id=${preview_id}"
confirm_payload="$(jq -nc --arg preview_id "${preview_id}" '{preview_id:$preview_id}')"
confirm_resp="$(curl -sS -f \
  -X POST "${api_base}/v1/teams/${OPS_TEAM_SLUG}/apps" \
  -H "${auth_header}" \
  -H "Content-Type: application/json" \
  -d "${confirm_payload}")"
deploy_run_id="$(jq -r '.deploy_run_id // empty' <<<"${confirm_resp}")"
if [[ -z "${deploy_run_id}" ]]; then
  echo "missing deploy_run_id: ${confirm_resp}" >&2
  exit 1
fi

echo "==> callback deploy success: run_id=${deploy_run_id}"
callback_body="$(jq -nc \
  --arg run_id "${deploy_run_id}" \
  --arg status "success" \
  --arg trace_id "${trace_id}" \
  '{run_id:$run_id,status:$status,trace_id:$trace_id}')"
ts="$(date -u +%s)"
sig_hex="$(printf '%s.%s' "${ts}" "${callback_body}" | openssl dgst -sha256 -hmac "${OPS_CALLBACK_SECRET}" | awk '{print $2}')"
sig_header="sha256=${sig_hex}"

curl -sS -f \
  -X POST "${api_base}/internal/deploy-runs/${deploy_run_id}/callback" \
  -H "Content-Type: application/json" \
  -H "X-0ops-Timestamp: ${ts}" \
  -H "X-0ops-Signature: ${sig_header}" \
  -H "X-0ops-Delivery-ID: ${trace_id}" \
  -d "${callback_body}" >/dev/null

echo "==> wait ${wait_seconds}s and probe ${target_url}"
sleep "${wait_seconds}"
http_code="$(curl -sS -o /tmp/m2-nextdemo-smoke-body.txt -w '%{http_code}' "${target_url}")"
if [[ "${http_code}" != "200" ]]; then
  echo "target not healthy: HTTP ${http_code}" >&2
  head -c 300 /tmp/m2-nextdemo-smoke-body.txt >&2 || true
  echo >&2
  exit 1
fi

echo "smoke passed: ${target_url} HTTP 200"
