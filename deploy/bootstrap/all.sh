#!/usr/bin/env bash
# spec: docs/runbooks/production-acceptance.md
# 一條指令把整套 0ops production 立起來，跑到「外部 curl 拿 200」為止。
#
# 入口：./manage.sh prod-bootstrap-all
#
# 串接：
#   1. prod-setup-oauth        ← 互動式；若 .env.prod 已填則 prompt 是否略過
#   2. prod-verify-oauth       ← 驗 Client ID 有效
#   3. prod-up                 ← K3s + ArgoCD + sealed-secrets + root app
#   4. prod-smoke              ← /health → 200
#   5. prod-install-runner     ← self-hosted runner（可選；--skip-runner 跳過）
#   6. gh variable set GHA_RUNNER_LABEL
#   7. prod-runner-validate    ← 端到端鏈路
#   8. e2e-create-app E2E_MODE=production
#
# 每步 idempotent；任何一步失敗即停。
#
# Flags:
#   --skip-runner       不裝 self-hosted runner（用 ubuntu-latest）
#   --skip-e2e          不跑最後 e2e（只到 prod-up + smoke）
#   --resume-from=<N>   從第 N 步開始（重跑卡過的部分）

set -euo pipefail

ROOT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")/../.." && pwd)"
cd "$ROOT_DIR"

ENV_FILE="${ENV_FILE:-deploy/bootstrap/.env.prod}"
SKIP_RUNNER=0
SKIP_E2E=0
RESUME_FROM=1

for arg in "$@"; do
  case "$arg" in
    --skip-runner) SKIP_RUNNER=1 ;;
    --skip-e2e)    SKIP_E2E=1 ;;
    --resume-from=*) RESUME_FROM="${arg#--resume-from=}" ;;
    -h|--help)
      sed -n '/^# spec/,/^# Flags:/p' "$0" >&2
      exit 0
      ;;
    *) echo "unknown arg: $arg" >&2; exit 64 ;;
  esac
done

[ -f "$ENV_FILE" ] || { echo "ERROR: $ENV_FILE not found — cp deploy/bootstrap/env.example $ENV_FILE first" >&2; exit 1; }

set -a
# shellcheck disable=SC1090
. "$ENV_FILE"
set +a

GHA_REPO="${GHA_REPO:-wusung/0ops}"
GHA_RUNNER_LABEL="${GHA_RUNNER_LABEL:-0ops-builder}"

banner() { printf '\n\033[1;35m========== %s ==========\033[0m\n\n' "$*" >&2; }
log() { printf '\033[1;36m[prod-bootstrap-all]\033[0m %s\n' "$*" >&2; }

run_step() {
  local n="$1" name="$2"; shift 2
  if [ "$n" -lt "$RESUME_FROM" ]; then
    log "step ${n}/${TOTAL} ${name}: skip (--resume-from=${RESUME_FROM})"
    return 0
  fi
  banner "step ${n}/${TOTAL} ${name}"
  "$@"
}

# 計算總步數
TOTAL=8
[ "$SKIP_RUNNER" = 1 ] && TOTAL=$((TOTAL - 3))   # 跳 5,6,7
[ "$SKIP_E2E" = 1 ]    && TOTAL=$((TOTAL - 1))   # 跳 8

# ---------- 1. setup-oauth ----------
have_oauth_id=$(grep -E '^GITHUB_OAUTH_CLIENT_ID=' "$ENV_FILE" | head -n1 | cut -d= -f2- || true)
if [ -n "$have_oauth_id" ]; then
  log "step 1/${TOTAL} setup-oauth: GITHUB_OAUTH_CLIENT_ID already set in $ENV_FILE — skip"
else
  run_step 1 "setup-oauth (interactive)" bash deploy/bootstrap/setup-oauth-app.sh
fi

# ---------- 2. verify-oauth ----------
run_step 2 "verify-oauth" bash deploy/bootstrap/verify-oauth.sh

# ---------- 3. prod-up ----------
run_step 3 "prod-up (K3s + ArgoCD + secrets + apps)" bash deploy/bootstrap/up.sh

# ---------- 4. smoke ----------
run_step 4 "smoke (api /health HTTP 200)" bash deploy/bootstrap/smoke.sh

# ---------- 5. install-runner ----------
if [ "$SKIP_RUNNER" = 1 ]; then
  log "skip self-hosted runner (--skip-runner); workflow will use ubuntu-latest"
else
  run_step 5 "install self-hosted GHA runner" bash deploy/runner/install-runner.sh

  # ---------- 6. set GHA var ----------
  banner "step 6/${TOTAL} set vars.GHA_RUNNER_LABEL"
  command -v gh >/dev/null || { echo "gh CLI required" >&2; exit 1; }
  current=$(gh api "/repos/${GHA_REPO}/actions/variables/GHA_RUNNER_LABEL" -q '.value' 2>/dev/null || echo "")
  if [ "$current" = "$GHA_RUNNER_LABEL" ]; then
    log "vars.GHA_RUNNER_LABEL already=${current}"
  else
    gh variable set GHA_RUNNER_LABEL --repo "$GHA_REPO" --body "$GHA_RUNNER_LABEL"
    log "set vars.GHA_RUNNER_LABEL=${GHA_RUNNER_LABEL}"
  fi

  # ---------- 7. runner-validate ----------
  run_step 7 "runner-validate" bash tasks/m6-q1-runner-validate.sh
fi

# ---------- 8. e2e-create-app E2E_MODE=production ----------
if [ "$SKIP_E2E" = 1 ]; then
  log "skip e2e (--skip-e2e); production stack is up but final create_app drive not exercised"
else
  banner "step ${TOTAL}/${TOTAL} e2e-create-app E2E_MODE=production"
  OPS_HOST="https://${PROD_API_HOST}" \
  E2E_MODE=production \
  E2E_PUBLIC_URL="https://${PROD_DEMO_HOST}" \
    bash tasks/e2e-create-app.sh
fi

banner "DONE — production up; final acceptance PASS"
cat >&2 <<EOF
External URLs:
  https://${PROD_API_HOST}/health           ← backend
  https://${PROD_DEMO_HOST}/                ← demo app (after e2e create)

Status:
  ./manage.sh prod-smoke                    ← HTTP 200 重驗
  ./manage.sh prod-runner-status            ← runner + recent workflow runs
  ./manage.sh prod-verify                   ← ArgoCD sync + smoke
EOF
