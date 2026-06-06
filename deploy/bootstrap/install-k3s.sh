#!/usr/bin/env bash
# 在 PROD_HOST 上裝 K3s。冪等：偵測 /usr/local/bin/k3s 存在則跳過。
# 鎖版以避免 upstream 升級破壞 traefik 行為。
set -euo pipefail

: "${PROD_HOST:?}"
: "${PROD_SSH_KEY:?}"

K3S_VERSION="${K3S_VERSION:-v1.31.4+k3s1}"

ssh -i "$PROD_SSH_KEY" "$PROD_HOST" bash -se <<EOF
set -euo pipefail
if command -v k3s >/dev/null 2>&1; then
  echo "k3s already installed: \$(k3s --version | head -n1) — skip."
  exit 0
fi
curl -sfL https://get.k3s.io | INSTALL_K3S_VERSION="$K3S_VERSION" sh -s - \
  --write-kubeconfig-mode=0644 \
  --disable=servicelb \
  --node-taint=CriticalAddonsOnly=true:NoExecute || true

# 等 kubeconfig 寫入
for i in \$(seq 1 30); do
  [ -f /etc/rancher/k3s/k3s.yaml ] && break
  sleep 1
done
kubectl --kubeconfig /etc/rancher/k3s/k3s.yaml wait --for=condition=Ready node --all --timeout=120s
EOF
