#!/usr/bin/env bash
set -euo pipefail

# m2-6-promtool-validate.sh
# 用 podman 拉 prom/prometheus 容器執行 `promtool check rules`，驗證
# deploy/gitops/observability/ 之 recording + alert rules ConfigMap 內嵌
# rule 表達式可被 Prometheus 接受（無 PromQL 語法錯誤、label 結構合法）。
#
# Usage: ./tasks/m2-6-promtool-validate.sh
#
# Hard requirement: podman 必須可用；不允許在 host 直接跑 binary（見 lessons L001）。

PROM_IMAGE="${PROM_IMAGE:-docker.io/prom/prometheus:v2.55.1}"
WORKDIR="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
OBSERVABILITY_DIR="$WORKDIR/deploy/gitops/observability"

if ! command -v podman >/dev/null 2>&1; then
  echo "✗ podman not found; install rootless podman per AGENTS.md" >&2
  exit 1
fi

TMPDIR="$(mktemp -d)"
trap 'rm -rf "$TMPDIR"' EXIT

extract_rules() {
  local cm_path="$1"
  local out_path="$2"
  # ConfigMap 的 data.rules.yaml 為 YAML literal block (`|`)；用 awk 抽出 block
  # 內容並去掉統一縮排，得到純 rules YAML。
  awk '
    BEGIN { in_data=0; capture=0; indent=-1 }
    /^data:/ { in_data=1; next }
    in_data && /^[[:space:]]*rules\.yaml:[[:space:]]*\|/ { capture=1; next }
    capture {
      if (indent < 0) {
        match($0, /^[[:space:]]*/)
        indent = RLENGTH
        if (indent == 0) { capture=0; next }
      }
      if ($0 ~ /^[^[:space:]]/) { capture=0; next }
      print substr($0, indent+1)
    }
  ' "$cm_path" >"$out_path"
}

echo "🔍 M2.6 promtool validation"
echo "  workdir: $WORKDIR"
echo "  image:   $PROM_IMAGE"

RECORDING_OUT="$TMPDIR/recording.yaml"
ALERT_OUT="$TMPDIR/alerting.yaml"

extract_rules "$OBSERVABILITY_DIR/prometheus-recording-rules.yaml" "$RECORDING_OUT"
extract_rules "$OBSERVABILITY_DIR/prometheus-alert-rules.yaml" "$ALERT_OUT"

if [[ ! -s "$RECORDING_OUT" ]]; then
  echo "✗ failed to extract recording rules payload" >&2
  exit 1
fi
if [[ ! -s "$ALERT_OUT" ]]; then
  echo "✗ failed to extract alert rules payload" >&2
  exit 1
fi

echo "✓ Extracted rule payloads"

chmod 0755 "$TMPDIR"
chmod 0644 "$RECORDING_OUT" "$ALERT_OUT"

podman run --rm \
  --userns=keep-id \
  --entrypoint promtool \
  -v "$TMPDIR":/rules:ro,z \
  "$PROM_IMAGE" \
  check rules /rules/recording.yaml /rules/alerting.yaml

echo "✓ promtool check rules passed"
