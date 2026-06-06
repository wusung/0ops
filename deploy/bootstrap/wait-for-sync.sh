#!/usr/bin/env bash
# 等 ArgoCD root app + 所有 child app 進入 Synced + Healthy。
# 5 分鐘 timeout；之後 smoke.sh 仍會獨立驗 HTTP 200，避免假 ready。
set -euo pipefail

: "${KUBECONFIG:?}"

TIMEOUT="${SYNC_TIMEOUT:-300}"
APPS=(root-app postgres ops-server cloudflare-tunnel observability)
start=$(date +%s)

while true; do
  all_ok=1
  for app in "${APPS[@]}"; do
    status=$(kubectl -n argocd get application "$app" -o jsonpath='{.status.sync.status}/{.status.health.status}' 2>/dev/null || echo "missing/missing")
    if [ "$status" != "Synced/Healthy" ]; then
      all_ok=0
      now=$(date +%s)
      elapsed=$((now - start))
      [ "$elapsed" -gt "$TIMEOUT" ] && {
        echo "ERROR: $app stuck at $status after ${elapsed}s" >&2
        kubectl -n argocd get application "$app" -o yaml | tail -40 >&2 || true
        exit 1
      }
      printf '\r[wait-for-sync] %-25s %s (%ds)' "$app" "$status" "$elapsed"
      sleep 5
      continue 2
    fi
  done
  [ "$all_ok" = 1 ] && break
done
echo ""
echo "all argocd apps Synced + Healthy."
