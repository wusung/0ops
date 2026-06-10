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

# ArgoCD 不做 env substitution：runtime domain 來自 argocd/apps/server.yaml 的
# config.domainBase，不是 PROD_BASE_DOMAIN。兩者不一致時 server 會以另一個
# domain 服務（subdomain / wildcard / reserved suffix 全錯 zone），smoke 探測
# 失敗且難歸因 — 在這裡 fail-fast。
ARGOCD_SERVER_APP="deploy/gitops/argocd/apps/server.yaml"
argocd_domain=$(awk '/name: config.domainBase/{getline; sub(/.*value: */, ""); print; exit}' "$ARGOCD_SERVER_APP")
if [ -n "$argocd_domain" ] && [ "$argocd_domain" != "$PROD_BASE_DOMAIN" ]; then
  echo "ERROR: PROD_BASE_DOMAIN ($PROD_BASE_DOMAIN) != config.domainBase ($argocd_domain) in $ARGOCD_SERVER_APP" >&2
  echo "       ArgoCD does not substitute env vars — edit $ARGOCD_SERVER_APP (domainBase, ingress.host, publicURL) to match." >&2
  exit 1
fi

log() { printf '\033[1;36m[prod-up]\033[0m %s\n' "$*" >&2; }

# Preflight：驗 ghcr image 存在且可匿名拉。在動 host 之前 fail-fast —
# 沒 image 的 deploy 只會在 30 分鐘後以 ImagePullBackOff 告終。
# 失敗常因：(a) 該 tag 的 release 還沒 cut；(b) private repo / fork 的
# package 是 private（visibility 只能走 GitHub UI 改；README § ghcr）。
check_image() {
  local ref="$1"                 # ghcr.io/<owner>/<name>:<tag>
  local path="${ref#ghcr.io/}"   # <owner>/<name>:<tag>
  local repo="${path%%:*}" tag="${path##*:}"
  local token
  token=$(curl -fsSL "https://ghcr.io/token?scope=repository:${repo}:pull" 2>/dev/null \
    | sed -n 's/.*"token":"\([^"]*\)".*/\1/p')
  [ -n "$token" ] || { echo "ERROR: cannot get anonymous pull token for $repo" >&2; return 1; }
  curl -fsS -o /dev/null \
    -H "Authorization: Bearer $token" \
    -H "Accept: application/vnd.oci.image.index.v1+json, application/vnd.oci.image.manifest.v1+json, application/vnd.docker.distribution.manifest.v2+json" \
    "https://ghcr.io/v2/${repo}/manifests/${tag}" || {
    echo "ERROR: image not anonymously pullable: $ref" >&2
    echo "       - release workflow images job 已對該 tag 跑過？" >&2
    echo "       - ghcr package 已設 public？（首次發佈預設 private）" >&2
    return 1
  }
}

log "step 0/7 preflight: ghcr images pullable"
MIGRATIONS_IMAGE_REF="${OPS_MIGRATIONS_IMAGE_REPO:-${OPS_IMAGE_REPO%-server}-migrations}:${OPS_IMAGE_TAG}"
check_image "${OPS_IMAGE_REPO}:${OPS_IMAGE_TAG}"
check_image "$MIGRATIONS_IMAGE_REF"
log "preflight OK: ${OPS_IMAGE_REPO}:${OPS_IMAGE_TAG} + ${MIGRATIONS_IMAGE_REF}"

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
