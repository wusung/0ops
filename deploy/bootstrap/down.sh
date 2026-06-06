#!/usr/bin/env bash
# production-deployment spec § 4 — 對應 up.sh 的反向卸載。
# 危險：會 prune ArgoCD root app 與相關 namespace。Postgres PV 由 chart annotations
# 鎖 Prune=false 保留；如要清空，請手動 kubectl delete pvc。

set -euo pipefail

ROOT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")/../.." && pwd)"
cd "$ROOT_DIR"

ENV_FILE="${ENV_FILE:-deploy/bootstrap/.env.prod}"
if [ -f "$ENV_FILE" ]; then
  set -a
  # shellcheck disable=SC1090
  . "$ENV_FILE"
  set +a
fi
export KUBECONFIG="${PROD_KUBECONFIG_LOCAL:-$KUBECONFIG}"

log() { printf '\033[1;33m[prod-down]\033[0m %s\n' "$*" >&2; }

read -r -p "Sure to delete prod ArgoCD root app + system-0ops + cloudflare-tunnel? [yes/NO] " ans
[ "$ans" = "yes" ] || { log "aborted."; exit 1; }

log "delete argocd root app (cascades to children)"
kubectl delete -f deploy/gitops/argocd/root-app.yaml --ignore-not-found

log "delete namespaces (PVCs kept; manual delete if needed)"
for ns in system-0ops cloudflare-tunnel observability; do
  kubectl delete ns "$ns" --ignore-not-found --timeout=60s
done

log "DONE — postgres ns kept (PVC 保留)；如需全清，手動 kubectl delete ns postgres"
