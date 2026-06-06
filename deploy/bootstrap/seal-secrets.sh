#!/usr/bin/env bash
# 把 .env.prod 中的 secret 轉成 SealedSecret YAML。
# 輸出：deploy/bootstrap/tmp/sealed/*.yaml（已加入 .gitignore，commit 安全；
# 但 SealedSecret 本身是 ciphertext，理論上可 commit — 留 user 決定）。
set -euo pipefail

: "${CF_TUNNEL_TOKEN:?}"
: "${GITHUB_OAUTH_CLIENT_ID:?}"
: "${GITHUB_OAUTH_CLIENT_SECRET:?}"
: "${GITHUB_OAUTH_REDIRECT_URI:?}"
: "${PG_SUPERUSER_PASSWORD:?}"
: "${PG_REPLICA_PASSWORD:?}"
: "${KUBECONFIG:?}"

if ! command -v kubeseal >/dev/null 2>&1; then
  echo "ERROR: kubeseal not installed. brew install kubeseal / pacman -S kubeseal" >&2
  exit 1
fi

PUB="deploy/bootstrap/tmp/sealed-secrets-pub.pem"
[ -f "$PUB" ] || { echo "ERROR: $PUB missing — run install-sealed-secrets.sh first" >&2; exit 1; }

OUT="deploy/bootstrap/tmp/sealed"
rm -rf "$OUT"
mkdir -p "$OUT"

seal_secret() {
  local name="$1" ns="$2" yaml="$3"
  # plain Secret → kubeseal --cert pub.pem → SealedSecret
  printf '%s' "$yaml" | kubeseal --cert "$PUB" --format yaml >"$OUT/${ns}-${name}.yaml"
  echo "sealed: $OUT/${ns}-${name}.yaml"
}

# 1) backend env secret — ops-server-env in system-0ops
backend=$(cat <<EOF
apiVersion: v1
kind: Secret
metadata:
  name: ops-server-env
  namespace: system-0ops
type: Opaque
stringData:
  GITHUB_OAUTH_CLIENT_ID: "${GITHUB_OAUTH_CLIENT_ID}"
  GITHUB_OAUTH_CLIENT_SECRET: "${GITHUB_OAUTH_CLIENT_SECRET}"
  GITHUB_OAUTH_REDIRECT_URI: "${GITHUB_OAUTH_REDIRECT_URI}"
  DATABASE_URL: "postgres://0ops:${PG_SUPERUSER_PASSWORD}@pg-main.postgres.svc:5432/0ops?sslmode=disable"
EOF
)
seal_secret ops-server-env system-0ops "$backend"

# 2) cloudflare tunnel token — cloudflared-tunnel-token in cloudflare-tunnel
tunnel=$(cat <<EOF
apiVersion: v1
kind: Secret
metadata:
  name: cloudflared-tunnel-token
  namespace: cloudflare-tunnel
type: Opaque
stringData:
  token: "${CF_TUNNEL_TOKEN}"
EOF
)
seal_secret cloudflared-tunnel-token cloudflare-tunnel "$tunnel"

# 3) postgres credentials — pg-credentials in postgres
pg=$(cat <<EOF
apiVersion: v1
kind: Secret
metadata:
  name: pg-credentials
  namespace: postgres
type: Opaque
stringData:
  POSTGRES_PASSWORD: "${PG_SUPERUSER_PASSWORD}"
  REPLICA_PASSWORD: "${PG_REPLICA_PASSWORD}"
EOF
)
seal_secret pg-credentials postgres "$pg"

echo "DONE — $(ls $OUT | wc -l) sealed secrets at $OUT/"
