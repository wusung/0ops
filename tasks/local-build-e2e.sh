#!/usr/bin/env bash
# tasks/local-build-e2e.sh — M5.6 acceptance script.
# Exercises: bootstrap → file:// preview → confirm → poll deploy_run live
# → verify image exists in local registry. Per ADR-0012 / sub-spec § 9.3.
set -euo pipefail

REGISTRY_HOST="${LOCAL_REGISTRY_HOST:-localhost:5000}"
TEAM_SLUG="${TEAM_SLUG:-personal}"
TEAM_NAME="${TEAM_NAME:-Personal}"
APP_SLUG="${APP_SLUG:-node-demo}"
GITHUB_LOGIN="${GITHUB_LOGIN:-dev}"
REPO_URL="${REPO_URL:-file:///workspace/examples/node-demo}"
HOST="${OPS_HOST:-http://localhost:8080}"
TIMEOUT_SECS="${TIMEOUT_SECS:-180}"
DATABASE_URL_DEFAULT="postgres://ops:ops_dev_pw@127.0.0.1:5432/ops?sslmode=disable"
DATABASE_URL="${DATABASE_URL:-$DATABASE_URL_DEFAULT}"

log() { echo "[e2e] $*" >&2; }

log "0. preflight: podman socket perms (ADR-0012 § 6.2 / sub-spec § 15)"
SOCK="/run/user/$(id -u)/podman/podman.sock"
if [ ! -S "$SOCK" ]; then
  echo "[e2e] socket missing at $SOCK — start it with: systemctl --user start podman.socket" >&2
  exit 1
fi
SOCK_PERMS=$(stat -c '%a' "$SOCK")
case "$SOCK_PERMS" in
  666|777)
    log "  socket perms=$SOCK_PERMS — pack lifecycle 可讀 OK"
    ;;
  *)
    cat >&2 <<EOF
[e2e] socket perms=$SOCK_PERMS at $SOCK — pack 的 lifecycle ephemeral container
      在 rootless podman uid mapping 下無法讀此 socket，ANALYZING phase 會失敗。

      在本機 dev 跑一次：

          make m5-6-podman-socket-loosen

      之後重跑 make m5-6-local-build-e2e。
      （ADR-0012 § 6.2 / docs/features/dev-environment/local-file-repo.md § 15）
EOF
    exit 1
    ;;
esac

log "1. compose up"
podman compose up -d >/dev/null

log "2. wait for server healthy at $HOST"
for i in $(seq 1 30); do
  if curl -fsS "$HOST/health" >/dev/null 2>&1; then break; fi
  sleep 2
  if [ "$i" = "30" ]; then echo "server not healthy" >&2; exit 1; fi
done

log "3. bootstrap example repo (idempotent)"
bash examples/node-demo/bootstrap.sh

log "4. bootstrap-owner (idempotent — ignore already-exists)"
go run ./cmd/cli admin bootstrap-owner \
  --host "$HOST" \
  --team-slug "$TEAM_SLUG" \
  --team-name "$TEAM_NAME" \
  --github-login "$GITHUB_LOGIN" \
  --output json >/dev/null 2>&1 || log "  (already bootstrapped, continuing)"

log "5. mint dev bearer via seed-cli-token (inside server container to reach db DNS)"
TOKEN="$(podman exec -e DATABASE_URL='postgres://ops:ops_dev_pw@db:5432/ops?sslmode=disable' \
  0ops-server-1 \
  go run ./cmd/devtools/seed-cli-token \
  --team-slug "$TEAM_SLUG" \
  --github-login "$GITHUB_LOGIN" \
  --name "m5.6-e2e" \
  --ttl 1h)"

if [ -z "$TOKEN" ]; then
  echo "failed to mint token" >&2; exit 1
fi

log "6. create app (preview + confirm)"
go run ./cmd/cli apps create \
  --host "$HOST" --team "$TEAM_SLUG" --token "$TOKEN" \
  --slug "$APP_SLUG" --repo-url "$REPO_URL" --ref main --yes

log "7. poll local registry until image appears or timeout (${TIMEOUT_SECS}s)"
# deploys/status 在 K3S_DISABLE_ISOLATION=true 下會立即回 "live"
# （ArgoCD fake 路徑；apps.go::getDeployStatusHandler），不反映 dispatcher
# 真實進度。本 step 改 poll registry tag list 作為 push 完成之 truth source；
# 同步 tail dispatcher 失敗 log 以早期捕捉 build/push 失敗。
TAGS_URL="http://${REGISTRY_HOST}/v2/0ops-apps/${TEAM_SLUG}/${APP_SLUG}/tags/list"
deadline=$(( $(date +%s) + TIMEOUT_SECS ))
while :; do
  if curl -fsS "$TAGS_URL" 2>/dev/null | jq -e '.tags | length > 0' >/dev/null 2>&1; then
    log "  image present in registry"
    break
  fi
  if podman logs --tail 200 0ops-server-1 2>&1 | grep -q "\"localbuild .* failed\".*\"$APP_SLUG\""; then
    echo "[e2e] dispatcher reported failure — full server log:" >&2
    podman logs --tail 100 0ops-server-1 2>&1 | grep "$APP_SLUG" >&2
    exit 1
  fi
  if [ "$(date +%s)" -ge "$deadline" ]; then
    echo "timeout waiting for image in registry at $TAGS_URL" >&2
    exit 1
  fi
  sleep 3
done

log "8. confirm deploys status reports live"
final_status="$(go run ./cmd/cli deploys status "$APP_SLUG" \
  --host "$HOST" --team "$TEAM_SLUG" --token "$TOKEN" --output json 2>/dev/null \
  | jq -r .status 2>/dev/null || echo unknown)"
log "  status=$final_status"
case "$final_status" in
  live) ;;
  *) echo "expected status=live after image present, got $final_status" >&2; exit 1 ;;
esac

log "OK — ${APP_SLUG} deploy_run reached live and image is present"
