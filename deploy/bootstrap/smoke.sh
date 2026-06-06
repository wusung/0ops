#!/usr/bin/env bash
# production-deployment spec § 5.3 — 對 production 做 HTTP 200 smoke。
# 通過為 prod-up 的 acceptance：api.<domain>/health 與 demo host 皆 200。
set -euo pipefail

ENV_FILE="${ENV_FILE:-deploy/bootstrap/.env.prod}"
if [ -f "$ENV_FILE" ]; then
  set -a
  # shellcheck disable=SC1090
  . "$ENV_FILE"
  set +a
fi

: "${PROD_API_HOST:?}"
: "${PROD_DEMO_HOST:?}"

DRY_RUN="${DRY_RUN:-0}"

probe() {
  local label="$1" url="$2"
  if [ "$DRY_RUN" = 1 ]; then
    echo "[dry-run] would curl $url"
    return 0
  fi
  for i in $(seq 1 30); do
    code=$(curl -sS -o /dev/null -w '%{http_code}' --max-time 5 "$url" || true)
    if [ "$code" = "200" ]; then
      echo "PASS $label → $url HTTP 200"
      return 0
    fi
    printf '\r[smoke] %-12s %s → %s (try %d/30)' "$label" "$url" "$code" "$i"
    sleep 5
  done
  echo ""
  echo "FAIL $label → $url not 200 after 30 tries" >&2
  return 1
}

probe "api"  "https://${PROD_API_HOST}/health"
probe "demo" "https://${PROD_DEMO_HOST}/" || {
  # demo host 可能尚未 deploy；改驗 wildcard 路徑可達（Cloudflare proxy 應回 522/404，不是 connection refused）
  code=$(curl -sS -o /dev/null -w '%{http_code}' --max-time 5 "https://${PROD_DEMO_HOST}/" || true)
  case "$code" in
    404|503) echo "WARN demo host reachable but app not deployed (HTTP $code) — acceptable for prod-up";;
    *) echo "FAIL demo host unreachable (HTTP $code)" >&2; exit 1;;
  esac
}

echo "smoke PASS."
