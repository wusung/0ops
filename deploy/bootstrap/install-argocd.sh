#!/usr/bin/env bash
# 安裝 ArgoCD 到 K3s。冪等：apply 本身冪等。
set -euo pipefail

: "${KUBECONFIG:?}"

ARGOCD_VERSION="${ARGOCD_VERSION:-v2.13.2}"
ARGOCD_NS="argocd"

kubectl create namespace "$ARGOCD_NS" --dry-run=client -o yaml | kubectl apply -f -
kubectl apply -n "$ARGOCD_NS" \
  -f "https://raw.githubusercontent.com/argoproj/argo-cd/${ARGOCD_VERSION}/manifests/install.yaml"

# 等 argocd-server 起來
kubectl -n "$ARGOCD_NS" rollout status deploy/argocd-server --timeout=300s
echo "argocd ready; UI not exposed by default (port-forward only — spec § 11 Q3)."
