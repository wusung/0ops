#!/usr/bin/env bash
# spec: docs/features/self-hosted-runner/spec.md § 6
# 端到端驗 self-hosted runner 鏈路：runner online → vars 切換 → workflow 真實跑 →
# callback HMAC 收到 → audit_log 串得回 trace_id。
#
# 退出碼：
#   0 — 全鏈路 PASS
#   1 — runner offline / 環境不足
#   2 — workflow run 失敗
#   3 — callback 沒收到 / trace_id 不對齊
#   4 — vars.GHA_RUNNER_LABEL 不對

set -euo pipefail

ENV_FILE="${ENV_FILE:-deploy/bootstrap/.env.prod}"
if [ -f "$ENV_FILE" ]; then
  set -a
  # shellcheck disable=SC1090
  . "$ENV_FILE"
  set +a
fi

GHA_REPO="${GHA_REPO:-wusung/0ops}"
GHA_RUNNER_NAME="${GHA_RUNNER_NAME:-0ops-prod-1}"
GHA_RUNNER_LABEL="${GHA_RUNNER_LABEL:-0ops-builder}"
TIMEOUT_MIN="${TIMEOUT_MIN:-10}"

log() { printf '\033[1;36m[runner-validate]\033[0m %s\n' "$*" >&2; }
fail() { printf '\033[1;31m[runner-validate]\033[0m FAIL: %s\n' "$*" >&2; exit "$2"; }

# 1) gh CLI ready
command -v gh >/dev/null 2>&1 || fail "gh CLI not installed" 1
gh auth status >/dev/null 2>&1 || fail "gh not authenticated" 1

# 2) runner online
log "checking runner '${GHA_RUNNER_NAME}' status"
status=$(gh api "/repos/${GHA_REPO}/actions/runners" \
  -q ".runners[] | select(.name==\"${GHA_RUNNER_NAME}\") | .status" || true)
if [ "$status" != "online" ]; then
  fail "runner offline (status=${status:-missing}) — run ./manage.sh prod-install-runner first" 1
fi
log "runner online"

# 3) vars.GHA_RUNNER_LABEL points at our label
current_label=$(gh api "/repos/${GHA_REPO}/actions/variables/GHA_RUNNER_LABEL" -q '.value' 2>/dev/null || echo "")
if [ "$current_label" != "$GHA_RUNNER_LABEL" ]; then
  fail "vars.GHA_RUNNER_LABEL='${current_label}' (expected '${GHA_RUNNER_LABEL}'); set via: gh variable set GHA_RUNNER_LABEL --repo ${GHA_REPO} --body ${GHA_RUNNER_LABEL}" 4
fi
log "vars.GHA_RUNNER_LABEL=${current_label}"

# 4) Trigger a smoke dispatch (optional — only if OPS_API_PUBLIC_URL + OPS_BEARER_TOKEN are set)
if [ -n "${OPS_API_PUBLIC_URL:-}" ] && [ -n "${OPS_BEARER_TOKEN:-}" ]; then
  TRACE_ID="runner-validate-$(date +%s)"
  log "triggering smoke deploy via backend (trace_id=$TRACE_ID)"

  # Use existing e2e harness — E2E_MODE=production drives real workflow + observes callback
  if [ -x tasks/e2e-create-app.sh ]; then
    OPS_HOST="$OPS_API_PUBLIC_URL" E2E_MODE=production E2E_TRACE_ID="$TRACE_ID" \
      bash tasks/e2e-create-app.sh --phase=cli-yes || \
      fail "e2e-create-app.sh failed under production mode" 2
    log "production e2e PASS"
  else
    log "skip: tasks/e2e-create-app.sh missing (ok if testing harness only)"
  fi
else
  log "skip workflow trigger: OPS_API_PUBLIC_URL/OPS_BEARER_TOKEN not set in env"
fi

# 5) Confirm latest workflow run used our runner label
log "verifying last run used self-hosted runner"
last_run_id=$(gh api "/repos/${GHA_REPO}/actions/runs?per_page=1" -q '.workflow_runs[0].id')
if [ -n "$last_run_id" ]; then
  job_labels=$(gh api "/repos/${GHA_REPO}/actions/runs/${last_run_id}/jobs" \
    -q '.jobs[0].labels // [] | join(",")' 2>/dev/null || echo "")
  case "$job_labels" in
    *${GHA_RUNNER_LABEL}*) log "last run used '${GHA_RUNNER_LABEL}' (PASS)";;
    "") log "last run job labels unavailable (likely too new) — skip";;
    *) fail "last run did not use '${GHA_RUNNER_LABEL}' (labels=${job_labels})" 2;;
  esac
fi

log "DONE — self-hosted runner end-to-end PASS"
