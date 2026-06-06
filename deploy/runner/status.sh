#!/usr/bin/env bash
# spec: docs/features/self-hosted-runner/spec.md § 5 / § 6
# 印 runner online 狀態 + 最近 N 個 workflow runs 之 runner 來源（hosted vs self-hosted）。
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
N="${N:-5}"

command -v gh >/dev/null 2>&1 || { echo "gh CLI not installed" >&2; exit 1; }
gh auth status >/dev/null 2>&1 || { echo "gh not authenticated" >&2; exit 1; }

echo "== runners on ${GHA_REPO} =="
gh api "/repos/${GHA_REPO}/actions/runners" \
  -q '.runners[] | [.name, .os, .status, (.labels | map(.name) | join(","))] | @tsv' \
  | column -t -s $'\t' || true

echo ""
echo "== last ${N} workflow runs =="
gh api "/repos/${GHA_REPO}/actions/runs?per_page=${N}" \
  -q '.workflow_runs[] | [.id, .name, .status, .conclusion, .head_branch, .created_at] | @tsv' \
  | column -t -s $'\t' || true

echo ""
echo "== current vars.GHA_RUNNER_LABEL =="
label=$(gh api "/repos/${GHA_REPO}/actions/variables/GHA_RUNNER_LABEL" -q '.value' 2>/dev/null || echo "(unset → fallback ubuntu-latest)")
echo "$label"
