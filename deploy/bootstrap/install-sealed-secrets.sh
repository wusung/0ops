#!/usr/bin/env bash
# 安裝 bitnami-labs sealed-secrets controller。冪等：apply 本身冪等。
set -euo pipefail

: "${KUBECONFIG:?}"

SS_VERSION="${SS_VERSION:-v0.27.1}"
SS_NS="kube-system"

kubectl apply -f \
  "https://github.com/bitnami-labs/sealed-secrets/releases/download/${SS_VERSION}/controller.yaml"

kubectl -n "$SS_NS" rollout status deploy/sealed-secrets-controller --timeout=300s

# Pubkey fetch — kubeseal 用得到（local 端跑 seal-secrets.sh）
mkdir -p deploy/bootstrap/tmp
kubectl -n "$SS_NS" get secret \
  -l sealedsecrets.bitnami.com/sealed-secrets-key \
  -o jsonpath='{.items[0].data.tls\.crt}' | base64 -d \
  >deploy/bootstrap/tmp/sealed-secrets-pub.pem
echo "sealed-secrets pubkey saved: deploy/bootstrap/tmp/sealed-secrets-pub.pem"
