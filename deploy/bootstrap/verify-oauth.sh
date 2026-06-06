#!/usr/bin/env bash
# Runbook：docs/runbooks/production-oauth-setup.md § 4
# 對 GitHub public device-code 端點打一次，驗 Client ID 與 Enable Device Flow
# 設定有效。不驗 Client Secret（device flow start 階段 GitHub 不查 secret）。
#
# 用法：./manage.sh prod-verify-oauth
#
# 退出碼：
#   0 — Client ID OK 且 Device Flow 已啟用（GitHub 回 200 + device_code）
#   1 — 缺檔 / 缺 env
#   2 — GitHub 回 unauthorized_client（OAuth App 沒勾 Enable Device Flow）
#   3 — GitHub 回其他 OAuth error（invalid_client_id 等）
#   4 — 網路 / curl 失敗

set -euo pipefail

ROOT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")/../.." && pwd)"
cd "$ROOT_DIR"

ENV_FILE="${ENV_FILE:-deploy/bootstrap/.env.prod}"
[ -f "$ENV_FILE" ] || { echo "ERROR: $ENV_FILE not found." >&2; exit 1; }

set -a
# shellcheck disable=SC1090
. "$ENV_FILE"
set +a

: "${GITHUB_OAUTH_CLIENT_ID:?GITHUB_OAUTH_CLIENT_ID empty in $ENV_FILE — run prod-setup-oauth first}"

GH_BASE="${GITHUB_OAUTH_BASE_URL:-https://github.com}"
URL="${GH_BASE}/login/device/code"

resp=$(curl -sS --max-time 10 \
  -H 'Accept: application/json' \
  -d "client_id=${GITHUB_OAUTH_CLIENT_ID}" \
  -d "scope=user:email" \
  "$URL" 2>&1) || { echo "ERROR curl failed: $resp" >&2; exit 4; }

# 預期成功 payload：device_code / user_code / verification_uri / interval
if printf '%s' "$resp" | grep -q '"device_code"'; then
  user_code=$(printf '%s' "$resp" | python3 -c 'import json,sys; print(json.load(sys.stdin).get("user_code", ""))')
  echo "PASS GitHub Device Flow 啟用，Client ID 有效（temporary user_code=${user_code}）"
  exit 0
fi

# 失敗：印 GitHub 原 error 並映射退出碼
err=$(printf '%s' "$resp" | python3 -c 'import json,sys; d=json.load(sys.stdin); print(d.get("error", "unknown"))' 2>/dev/null || echo "parse-fail")
case "$err" in
  unauthorized_client)
    echo "FAIL OAuth App 沒勾 Enable Device Flow。" >&2
    echo "     Settings → Developer settings → OAuth Apps → <App> → 勾 Enable Device Flow → Update application" >&2
    exit 2
    ;;
  invalid_client*|incorrect_client*|client_not_found)
    echo "FAIL Client ID 無效（GitHub 回 $err）。" >&2
    echo "     確認 $ENV_FILE 中 GITHUB_OAUTH_CLIENT_ID 是否漏字 / 來自正確的 OAuth App。" >&2
    exit 3
    ;;
  *)
    echo "FAIL GitHub 回 $err；完整 payload：" >&2
    echo "$resp" >&2
    exit 3
    ;;
esac
