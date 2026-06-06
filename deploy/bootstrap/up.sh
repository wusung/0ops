#!/usr/bin/env bash
# production-deployment spec § 4 / § 5.2 — 一鍵 production bootstrap。
# 走法：./manage.sh prod-up  （或 bash deploy/bootstrap/up.sh）。
# 冪等：重跑安全；步驟皆 `apply`/`upgrade --install` 風格。

set -euo pipefail

ROOT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")/../.." && pwd)"
cd "$ROOT_DIR"

ENV_FILE="${ENV_FILE:-deploy/bootstrap/.env.prod}"
if [ ! -f "$ENV_FILE" ]; then
  echo "ERROR: $ENV_FILE not found." >&2
  echo "       cp deploy/bootstrap/env.example $ENV_FILE then fill in values." >&2
  exit 1
fi

set -a
# shellcheck disable=SC1090
. "$ENV_FILE"
set +a

required=(
  PROD_HOST PROD_SSH_KEY PROD_KUBECONFIG_LOCAL
  PROD_BASE_DOMAIN PROD_API_HOST PROD_DEMO_HOST
  CF_TUNNEL_TOKEN
  GITHUB_OAUTH_CLIENT_ID GITHUB_OAUTH_CLIENT_SECRET GITHUB_OAUTH_REDIRECT_URI
  OPS_IMAGE_TAG OPS_IMAGE_REPO
  PG_SUPERUSER_PASSWORD PG_REPLICA_PASSWORD
  ARGOCD_REPO_URL ARGOCD_TARGET_REVISION
)
missing=()
for v in "${required[@]}"; do
  [ -z "${!v:-}" ] && missing+=("$v")
done
if [ ${#missing[@]} -gt 0 ]; then
  echo "ERROR: required env vars are empty in $ENV_FILE: ${missing[*]}" >&2
  exit 1
fi

log() { printf '\033[1;36m[prod-up]\033[0m %s\n' "$*" >&2; }

log "step 1/7 install k3s on $PROD_HOST"
bash deploy/bootstrap/install-k3s.sh

log "step 2/7 fetch kubeconfig → $PROD_KUBECONFIG_LOCAL"
mkdir -p "$(dirname "$PROD_KUBECONFIG_LOCAL")"
ssh -i "$PROD_SSH_KEY" "$PROD_HOST" 'sudo cat /etc/rancher/k3s/k3s.yaml' \
  | sed "s|127.0.0.1|${PROD_HOST#*@}|" >"$PROD_KUBECONFIG_LOCAL"
chmod 0600 "$PROD_KUBECONFIG_LOCAL"
export KUBECONFIG="$PROD_KUBECONFIG_LOCAL"

log "step 3/7 install argocd"
bash deploy/bootstrap/install-argocd.sh

log "step 4/7 install sealed-secrets controller"
bash deploy/bootstrap/install-sealed-secrets.sh

log "step 5/7 seal secrets → deploy/bootstrap/tmp/sealed/"
bash deploy/bootstrap/seal-secrets.sh

log "step 6/7 apply sealed secrets + argocd root app"
kubectl apply -f deploy/bootstrap/tmp/sealed/
# root-app.yaml 用 envsubst 注入 repoURL / targetRevision；
# apps/*.yaml 是 ArgoCD 自行從 git 讀取，不能 envsubst（spec § 7 註）。
# 非 default 域名需 fork repo 後改 apps/*.yaml。
envsubst <deploy/gitops/argocd/root-app.yaml | kubectl apply -f -

log "step 7/7 wait for sync + smoke"
bash deploy/bootstrap/wait-for-sync.sh
bash deploy/bootstrap/smoke.sh

log "DONE — production up. acceptance: tasks/m2-8-e2e-acceptance.sh E2E_MODE=production"
