#!/usr/bin/env bash
# Runbook：docs/runbooks/production-oauth-setup.md
# 互動式幫 user 註冊 production GitHub OAuth App，把 Client ID / Secret /
# Redirect URI 三行寫進 .env.prod。
#
# 用法：./manage.sh prod-setup-oauth
#
# 行為：
#   1. 讀 .env.prod 拿 PROD_API_HOST；若未設則 prompt 一次
#   2. 印 pre-filled GitHub 建立頁網址（如 host 有圖形介面則 xdg-open / open）
#   3. user 在 GitHub 按 Register；回腳本 prompt 貼 Client ID + Secret
#   4. 驗格式 + 非空 + 必要時 prompt 是否覆蓋舊值
#   5. 把 3 行寫進 .env.prod；備份原檔到 .env.prod.bak.<timestamp>
#
# 不會：
#   - 直接打 GitHub 建立 OAuth App（GitHub 不開該 API）
#   - 把 secret 印到 stdout / 不寫 log

set -euo pipefail

ROOT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")/../.." && pwd)"
cd "$ROOT_DIR"

ENV_FILE="${ENV_FILE:-deploy/bootstrap/.env.prod}"
EXAMPLE_FILE="deploy/bootstrap/env.example"

if [ ! -f "$ENV_FILE" ]; then
  echo "ERROR: $ENV_FILE not found." >&2
  echo "       cp $EXAMPLE_FILE $ENV_FILE then fill in PROD_API_HOST and re-run." >&2
  exit 1
fi

# 讀 host
PROD_API_HOST=$(grep -E '^PROD_API_HOST=' "$ENV_FILE" | head -n1 | cut -d= -f2- || true)
if [ -z "$PROD_API_HOST" ]; then
  read -r -p "PROD_API_HOST (例 api.jesontech.com): " PROD_API_HOST
  [ -z "$PROD_API_HOST" ] && { echo "PROD_API_HOST required" >&2; exit 1; }
fi

CALLBACK_URL="https://${PROD_API_HOST}/v1/auth/oauth2/callback"
HOMEPAGE_URL="https://${PROD_API_HOST}"
APP_NAME="0ops (${PROD_API_HOST})"

# 不能用 URLencode for slash, 但 GitHub URL 接受 plus-encoded space
# 用 python3 做 percent-encode 比 sed/awk 穩
encode() {
  python3 -c 'import sys,urllib.parse; print(urllib.parse.quote(sys.argv[1], safe=""))' "$1"
}

PREFILL_URL="https://github.com/settings/applications/new?oauth_application[name]=$(encode "$APP_NAME")&oauth_application[url]=$(encode "$HOMEPAGE_URL")&oauth_application[callback_url]=$(encode "$CALLBACK_URL")"

cat >&2 <<EOF

================================================================================
production GitHub OAuth App setup（runbook: docs/runbooks/production-oauth-setup.md）

1. 開啟下方網址（已預填欄位）：

   $PREFILL_URL

   提示：若要用 org-level OAuth App，改網址為
   https://github.com/organizations/<ORG>/settings/applications/new

2. 在 GitHub 頁面：
   - 確認 Application name / Homepage URL / Authorization callback URL 預填正確
   - **勾選** Enable Device Flow（CLI 預設走 device flow，不勾會 unauthorized_client）
   - 按 Register application
   - 複製 Client ID
   - 按 Generate a new client secret → 立刻複製（離開頁面後不可再讀）

EOF

# 試圖自動開瀏覽器（可選）
if command -v xdg-open >/dev/null 2>&1; then
  xdg-open "$PREFILL_URL" >/dev/null 2>&1 || true
elif command -v open >/dev/null 2>&1; then
  open "$PREFILL_URL" >/dev/null 2>&1 || true
fi

read -r -p "Client ID: " CLIENT_ID
[ -z "$CLIENT_ID" ] && { echo "Client ID required" >&2; exit 1; }

# 常見格式：'Ov23li...' (new format) / 'Iv1.xxx' / 20-char hex (legacy)
if ! printf '%s' "$CLIENT_ID" | grep -Eq '^([A-Za-z0-9_.]{8,})$'; then
  echo "WARN Client ID 不像 GitHub OAuth App ID 格式（仍會寫入）" >&2
fi

# secret 用 stty 隱藏輸入
read -r -s -p "Client Secret (輸入不顯示): " CLIENT_SECRET
echo
[ -z "$CLIENT_SECRET" ] && { echo "Client Secret required" >&2; exit 1; }

# 比對舊值
OLD_ID=$(grep -E '^GITHUB_OAUTH_CLIENT_ID=' "$ENV_FILE" | head -n1 | cut -d= -f2- || true)
OLD_SECRET=$(grep -E '^GITHUB_OAUTH_CLIENT_SECRET=' "$ENV_FILE" | head -n1 | cut -d= -f2- || true)

if [ -n "$OLD_ID" ] && [ "$OLD_ID" != "$CLIENT_ID" ]; then
  read -r -p ".env.prod 已有 Client ID（${OLD_ID:0:6}...）。覆蓋？[yes/NO] " ans
  [ "$ans" = "yes" ] || { echo "aborted, .env.prod unchanged" >&2; exit 1; }
fi
if [ -n "$OLD_SECRET" ] && [ "$OLD_SECRET" != "$CLIENT_SECRET" ]; then
  read -r -p ".env.prod 已有 Client Secret。覆蓋？[yes/NO] " ans
  [ "$ans" = "yes" ] || { echo "aborted, .env.prod unchanged" >&2; exit 1; }
fi

# 備份
BAK="${ENV_FILE}.bak.$(date +%Y%m%d%H%M%S)"
cp -p "$ENV_FILE" "$BAK"

# 用 awk 做 in-place 更新；不存在的 key 就 append。
update_env() {
  local key="$1" val="$2" tmp
  tmp=$(mktemp)
  awk -v K="$key" -v V="$val" '
    BEGIN { found = 0 }
    $0 ~ "^" K "=" { print K "=" V; found = 1; next }
    { print }
    END { if (!found) print K "=" V }
  ' "$ENV_FILE" >"$tmp"
  mv "$tmp" "$ENV_FILE"
}

update_env GITHUB_OAUTH_CLIENT_ID "$CLIENT_ID"
update_env GITHUB_OAUTH_CLIENT_SECRET "$CLIENT_SECRET"
update_env GITHUB_OAUTH_REDIRECT_URI "$CALLBACK_URL"

chmod 0600 "$ENV_FILE"

cat >&2 <<EOF

================================================================================
DONE — 3 行已寫入 $ENV_FILE（備份：$BAK）
  GITHUB_OAUTH_CLIENT_ID
  GITHUB_OAUTH_CLIENT_SECRET (hidden)
  GITHUB_OAUTH_REDIRECT_URI=$CALLBACK_URL

下一步：
  ./manage.sh prod-verify-oauth   # 驗 Client ID 在 GitHub 端有效
  ./manage.sh prod-up             # 一鍵 bootstrap
EOF
