#!/usr/bin/env bash
set -euo pipefail

# e2e-create-app.sh
# create_app 端到端驗收：preview → confirm → dispatch → callback → sync → public URL 200
#
# 對齊 docs/features/create-app-flow/spec.md § 12「驗證準則」之 End-to-end happy path
# 行；本腳本是 create_app flow 的單一驗收入口（CLI 互動式 / CLI --yes / MCP / public URL）。
#
# 工作模式（E2E_MODE）：
#   local       (default) 只在本機 compose stack 上做結構性驗證；create_app 因
#               缺 GitHub App install 與 Cloudflare 資料而以 [SKIP] 標記，並改以
#               callback HMAC 簽章 + 路由可達性自我檢查代替。CI 預設走此 mode。
#   staging     需先 export OPS_HOST、OPS_BEARER_TOKEN、OPS_TEAM_SLUG、E2E_REPO_URL；
#               對 staging backend 跑完整 preview/confirm（含 GitHub App 路徑），
#               但 public-url-probe 仍可關閉。
#   production  staging + 強制執行 public-url-probe（curl https://nextdemo.jesontech.com
#               必須 200）；用於正式 rollout 後的單次驗收。
#
# 使用方式：
#   ./tasks/e2e-create-app.sh [--phase=<name>] [--mode=<mode>] [-h|--help]
#
#   --phase=<name>     只跑單一 phase（預設跑全部）
#                      合法值：preflight | cli-yes | cli-interactive | mcp |
#                              callback | public-url-probe
#   --mode=<mode>      覆蓋 E2E_MODE 環境變數（local|staging|production）
#
# 必要 / 可選環境變數：
#   E2E_MODE                  預設 local
#   OPS_HOST                  backend host；local 預設 http://127.0.0.1:${OPS_HOST_PORT:-8080}
#   OPS_BEARER_TOKEN          bearer token；local mode 可由 bootstrap 取得後手動匯出
#   OPS_TEAM_SLUG             team slug；staging+ 必填
#   OPS_CALLBACK_SECRET       callback HMAC 共用密鑰；預設 dev-callback-secret-change-me
#   E2E_REPO_URL              create_app 用的 repo URL；預設 https://github.com/example/nextdemo
#   E2E_REPO_REF              git ref；預設 main
#   E2E_APP_SLUG_PREFIX       app slug 前綴；預設 nextdemo
#   E2E_PUBLIC_URL            public URL 探測目標；預設 https://nextdemo.jesontech.com
#   E2E_CLI_IMAGE             CLI runtime image；預設 localhost/0ops-cli:runtime
#   E2E_MCP_IMAGE             MCP runtime image；預設 localhost/0ops-mcp:runtime
#   E2E_NETWORK               compose 網路名；預設 0ops_default；OPS_HOST 指外部 host 時自動 bypass
#   E2E_REQUIRE_PASS          設 1 時，若 PASSED 計數為 0（即所有 phase 都 SKIP），exit 6；
#                             用於 CI / cron 場景，避免「全 SKIP exit 0」變成假綠
#
# 結束碼：
#   0  全部 phase 通過或合法 SKIP
#   2  preflight 缺工具（無 podman / curl / openssl / python3）
#   3  phase 內部斷言失敗（含任一 phase 失敗的整體結果碼）
#   4  CLI / MCP / curl 子程序非預期退出
#   5  HMAC callback 簽章驗證失敗（自我比對）
#   6  E2E_REQUIRE_PASS=1 但無任何 phase passed
#  64  CLI 用法錯誤（無效 flag / phase）
#
# 硬性約束（lessons/L001 + AGENTS.md）：
# - 不可直接在 host 跑 ./bin/0ops-server 取代 compose stack；本腳本所有對 backend
#   的呼叫一律經 OPS_HOST 指向 compose stack 或 staging 端點。
# - CLI / MCP 二進位以 podman run 對應 runtime image 的方式驅動，保留 distroless
#   non-root 邊界；不在 host 直接執行 ./bin/0ops 或 ./bin/0ops-mcp。

MODE_DEFAULT="local"
PHASES_ALL=(preflight cli-yes cli-interactive mcp callback public-url-probe)
SELECTED_PHASE=""

usage() {
  sed -n '/^# e2e-create-app.sh/,/^$/p' "$0"
}

for arg in "$@"; do
  case "$arg" in
    --phase=*)
      SELECTED_PHASE="${arg#--phase=}"
      ;;
    --mode=*)
      E2E_MODE="${arg#--mode=}"
      export E2E_MODE
      ;;
    -h|--help)
      usage
      exit 0
      ;;
    *)
      echo "✗ unknown argument: $arg" >&2
      usage >&2
      exit 64
      ;;
  esac
done

E2E_MODE="${E2E_MODE:-$MODE_DEFAULT}"
case "$E2E_MODE" in
  local|staging|production) ;;
  *)
    echo "✗ invalid E2E_MODE=$E2E_MODE (allowed: local|staging|production)" >&2
    exit 64
    ;;
esac

OPS_HOST="${OPS_HOST:-http://127.0.0.1:${OPS_HOST_PORT:-8080}}"
OPS_BEARER_TOKEN="${OPS_BEARER_TOKEN:-}"
OPS_TEAM_SLUG="${OPS_TEAM_SLUG:-}"
OPS_CALLBACK_SECRET="${OPS_CALLBACK_SECRET:-dev-callback-secret-change-me}"
E2E_REPO_URL="${E2E_REPO_URL:-https://github.com/example/nextdemo}"
E2E_REPO_REF="${E2E_REPO_REF:-main}"
E2E_APP_SLUG_PREFIX="${E2E_APP_SLUG_PREFIX:-nextdemo}"
E2E_PUBLIC_URL="${E2E_PUBLIC_URL:-https://nextdemo.jesontech.com}"
E2E_CLI_IMAGE="${E2E_CLI_IMAGE:-localhost/0ops-cli:runtime}"
E2E_MCP_IMAGE="${E2E_MCP_IMAGE:-localhost/0ops-mcp:runtime}"
E2E_NETWORK="${E2E_NETWORK:-0ops_default}"
E2E_REQUIRE_PASS="${E2E_REQUIRE_PASS:-0}"

EXIT_PHASE_FAIL=3
EXIT_SUBPROC_FAIL=4
EXIT_SIG_MISMATCH=5
EXIT_NO_PASS=6

# tmpdir for transient capture files; cleaned on exit
E2E_TMPDIR="$(mktemp -d -t e2e-create-app.XXXXXX)"
trap 'rm -rf "$E2E_TMPDIR"' EXIT

PASSED=0
SKIPPED=0
FAILED=0

phase_header() {
  echo ""
  echo "=== PHASE: $1 ==="
  echo "  mode: $E2E_MODE"
}

phase_skip() {
  echo "  [SKIP] $1"
  SKIPPED=$((SKIPPED + 1))
}

phase_pass() {
  echo "  ✓ $1"
  PASSED=$((PASSED + 1))
}

require_cmd() {
  local cmd="$1"
  if ! command -v "$cmd" >/dev/null 2>&1; then
    echo "✗ required command not found: $cmd" >&2
    exit 2
  fi
}

# Resolve podman --network arg lazily: only attach to the compose network when
# OPS_HOST points at the local stack (compose-internal hostname or loopback).
# Staging / production hosts use external DNS and must not be routed through
# the compose network (which may not exist).
podman_network_args() {
  case "$OPS_HOST" in
    *127.0.0.1*|*localhost*|*://server*|*://server:*)
      printf -- '--network %s' "$E2E_NETWORK"
      ;;
    *)
      printf -- ''
      ;;
  esac
}

run_cli() {
  # shellcheck disable=SC2046  # $(podman_network_args) intentionally word-splits
  podman run --rm $(podman_network_args) \
    -e OPS_HOST="$OPS_HOST" \
    -e OPS_BEARER_TOKEN="$OPS_BEARER_TOKEN" \
    -e OPS_TEAM="$OPS_TEAM_SLUG" \
    "$E2E_CLI_IMAGE" "$@"
}

run_cli_interactive() {
  # shellcheck disable=SC2046
  printf 'y\n' | podman run --rm -i $(podman_network_args) \
    -e OPS_HOST="$OPS_HOST" \
    -e OPS_BEARER_TOKEN="$OPS_BEARER_TOKEN" \
    -e OPS_TEAM="$OPS_TEAM_SLUG" \
    "$E2E_CLI_IMAGE" "$@"
}

run_mcp_call() {
  local payload="$1"
  # shellcheck disable=SC2046
  printf '%s\n' "$payload" | podman run --rm -i $(podman_network_args) \
    -e OPS_HOST="$OPS_HOST" \
    -e OPS_BEARER_TOKEN="$OPS_BEARER_TOKEN" \
    "$E2E_MCP_IMAGE"
}

phase_preflight() {
  phase_header "preflight"
  require_cmd podman
  require_cmd curl
  require_cmd openssl
  require_cmd python3
  if [[ "$E2E_MODE" == "local" ]]; then
    if ! curl -fsS --max-time 3 "$OPS_HOST/health" >/dev/null 2>&1; then
      phase_skip "backend unreachable at $OPS_HOST; run './manage.sh dev' first if you intend to drive a live stack"
    else
      phase_pass "backend /health OK at $OPS_HOST"
    fi
  else
    if [[ -z "$OPS_BEARER_TOKEN" || -z "$OPS_TEAM_SLUG" ]]; then
      echo "  ✗ OPS_BEARER_TOKEN and OPS_TEAM_SLUG are required for mode=$E2E_MODE" >&2
      return $EXIT_PHASE_FAIL
    fi
    for img in "$E2E_CLI_IMAGE" "$E2E_MCP_IMAGE"; do
      if ! podman image exists "$img"; then
        echo "  ✗ container image $img missing; run './manage.sh build-images'" >&2
        return $EXIT_PHASE_FAIL
      fi
    done
    if curl -fsS "$OPS_HOST/health" >/dev/null; then
      phase_pass "backend /health OK at $OPS_HOST; CLI/MCP images present"
    else
      echo "  ✗ /health probe failed at $OPS_HOST" >&2
      return $EXIT_PHASE_FAIL
    fi
  fi
}

# 共用：staging+ 模式跑 CLI / MCP create_app 路徑前的條件檢查
require_staging_or_skip() {
  local phase="$1"
  if [[ "$E2E_MODE" == "local" ]]; then
    phase_skip "$phase requires GitHub App install + valid bearer token; available in staging|production only"
    return 1
  fi
  if [[ -z "$OPS_BEARER_TOKEN" || -z "$OPS_TEAM_SLUG" ]]; then
    echo "  ✗ OPS_BEARER_TOKEN / OPS_TEAM_SLUG missing in mode=$E2E_MODE" >&2
    return $EXIT_PHASE_FAIL
  fi
  return 0
}

phase_cli_yes() {
  phase_header "cli-yes"
  if ! require_staging_or_skip "cli-yes"; then return 0; fi

  local slug="${E2E_APP_SLUG_PREFIX}-yes-$(date +%s)"
  if ! run_cli apps create \
      --slug "$slug" \
      --repo-url "$E2E_REPO_URL" \
      --ref "$E2E_REPO_REF" \
      --yes \
      --output json; then
    echo "  ✗ CLI --yes path exited non-zero" >&2
    return $EXIT_SUBPROC_FAIL
  fi
  phase_pass "CLI --yes preview→confirm completed for $slug"
}

phase_cli_interactive() {
  phase_header "cli-interactive"
  if ! require_staging_or_skip "cli-interactive"; then return 0; fi

  local slug="${E2E_APP_SLUG_PREFIX}-int-$(date +%s)"
  if ! run_cli_interactive apps create \
      --slug "$slug" \
      --repo-url "$E2E_REPO_URL" \
      --ref "$E2E_REPO_REF" \
      --output json; then
    echo "  ✗ CLI interactive path exited non-zero" >&2
    return $EXIT_SUBPROC_FAIL
  fi
  phase_pass "CLI interactive (y) preview→confirm completed for $slug"
}

phase_mcp() {
  phase_header "mcp"
  if ! require_staging_or_skip "mcp"; then return 0; fi

  local slug="${E2E_APP_SLUG_PREFIX}-mcp-$(date +%s)"
  local init_req='{"jsonrpc":"2.0","id":1,"method":"initialize","params":{"protocolVersion":"2025-06-18","capabilities":{},"clientInfo":{"name":"m2-8-e2e","version":"dev"}}}'
  local preview_req confirm_req
  preview_req=$(python3 -c "import json,sys; print(json.dumps({'jsonrpc':'2.0','id':2,'method':'tools/call','params':{'name':'create_app_preview','arguments':{'team_slug':sys.argv[1],'slug':sys.argv[2],'repo_url':sys.argv[3],'ref':sys.argv[4]}}}))" "$OPS_TEAM_SLUG" "$slug" "$E2E_REPO_URL" "$E2E_REPO_REF")

  local mcp_out="$E2E_TMPDIR/mcp-preview.out"
  if ! run_mcp_call "$init_req"$'\n'"$preview_req" >"$mcp_out" 2>&1; then
    cat "$mcp_out" >&2
    echo "  ✗ MCP create_app_preview round-trip failed" >&2
    return $EXIT_SUBPROC_FAIL
  fi

  # Parse preview_id from JSON-RPC response; fail loudly if absent.
  local preview_id
  preview_id=$(python3 -c '
import json, sys
for raw in open(sys.argv[1]).read().splitlines():
    raw = raw.strip()
    if not raw.startswith("{"):
        continue
    try:
        msg = json.loads(raw)
    except json.JSONDecodeError:
        continue
    if msg.get("id") != 2 or "result" not in msg:
        continue
    result = msg["result"]
    structured = result.get("structuredContent")
    candidates = []
    if isinstance(structured, dict):
        candidates.append(structured)
    for item in result.get("content", []):
        if isinstance(item, dict) and item.get("type") == "text":
            try:
                candidates.append(json.loads(item.get("text", "")))
            except json.JSONDecodeError:
                pass
    for cand in candidates:
        pid = cand.get("preview_id")
        if pid:
            print(pid)
            sys.exit(0)
sys.exit(1)
' "$mcp_out") || preview_id=""

  if [[ -z "$preview_id" ]]; then
    cat "$mcp_out" >&2
    echo "  ✗ could not parse preview_id from MCP create_app_preview response" >&2
    return $EXIT_PHASE_FAIL
  fi

  confirm_req=$(python3 -c "import json,sys; print(json.dumps({'jsonrpc':'2.0','id':3,'method':'tools/call','params':{'name':'create_app','arguments':{'team_slug':sys.argv[1],'preview_id':sys.argv[2]}}}))" "$OPS_TEAM_SLUG" "$preview_id")

  local confirm_out="$E2E_TMPDIR/mcp-confirm.out"
  if ! run_mcp_call "$init_req"$'\n'"$confirm_req" >"$confirm_out" 2>&1; then
    cat "$confirm_out" >&2
    echo "  ✗ MCP create_app round-trip failed" >&2
    return $EXIT_SUBPROC_FAIL
  fi
  if ! grep -q '"app_id"\|"deploy_run_id"\|"subdomain_url"' "$confirm_out"; then
    cat "$confirm_out" >&2
    echo "  ✗ MCP create_app response missing app_id/deploy_run_id/subdomain_url" >&2
    return $EXIT_PHASE_FAIL
  fi
  phase_pass "MCP create_app_preview → create_app round-trip succeeded (preview_id=$preview_id)"
}

phase_callback() {
  phase_header "callback"
  # 此 phase 用 deploy-run callback 自我比對 HMAC，不依賴實際 deploy_run row
  # 存在；只驗 backend signature/timestamp 路徑與 OPS_CALLBACK_SECRET 一致。
  if ! curl -fsS --max-time 3 "$OPS_HOST/health" >/dev/null 2>&1; then
    phase_skip "backend unreachable at $OPS_HOST; run './manage.sh dev' or set OPS_HOST"
    return 0
  fi

  # deploy_run.id is a uuid column; a non-UUID literal triggers SQLSTATE
  # 22P02 (invalid_text_representation) inside ApplyDeployCallback before
  # the handler can map ErrNoRows → 404. Use a syntactically valid UUID
  # that is guaranteed not to exist so the path resolves to
  # deploy_run_not_found (404), which is the value this phase asserts.
  local run_id="00000000-0000-0000-0000-000000000000"
  local ts
  ts="$(date -u +%s)"
  local body
  body=$(printf '{"run_id":"%s","status":"failed","trace_id":"trace-m2-8","failure_classification":"build_compile_error","error_summary":"acceptance probe"}' "$run_id")
  local sig
  sig="sha256=$(printf '%s.%s' "$ts" "$body" | openssl dgst -sha256 -hmac "$OPS_CALLBACK_SECRET" | awk '{print $2}')"

  local cb_out="$E2E_TMPDIR/callback.out"
  local http_status
  http_status=$(curl -sS -o "$cb_out" -w '%{http_code}' \
    -X POST "$OPS_HOST/internal/deploy-runs/$run_id/callback" \
    -H "Content-Type: application/json" \
    -H "X-0ops-Timestamp: $ts" \
    -H "X-0ops-Signature: $sig" \
    --data "$body" || true)

  case "$http_status" in
    200|404)
      # 200 = body 接受、deploy_run row 存在；
      # 404 = signature OK 但 deploy_run_not_found（local mode 預期值）
      phase_pass "callback signature accepted by backend (HTTP $http_status)"
      ;;
    401)
      cat "$cb_out" >&2
      echo "  ✗ backend rejected HMAC; OPS_CALLBACK_SECRET mismatch with backend" >&2
      return $EXIT_SIG_MISMATCH
      ;;
    "")
      echo "  ✗ callback request failed to complete (curl error)" >&2
      return $EXIT_SUBPROC_FAIL
      ;;
    *)
      cat "$cb_out" >&2
      echo "  ✗ unexpected callback response: HTTP $http_status" >&2
      return $EXIT_PHASE_FAIL
      ;;
  esac
}

phase_public_url_probe() {
  phase_header "public-url-probe"
  if [[ "$E2E_MODE" != "production" ]]; then
    phase_skip "public URL probe runs only in mode=production (target: $E2E_PUBLIC_URL)"
    return 0
  fi
  local http_status
  http_status=$(curl -sS -o /dev/null -w '%{http_code}' --max-time 30 "$E2E_PUBLIC_URL" || true)
  if [[ "$http_status" == "200" ]]; then
    phase_pass "$E2E_PUBLIC_URL returned 200"
    return 0
  fi
  echo "  ✗ $E2E_PUBLIC_URL returned HTTP $http_status (expected 200)" >&2
  return $EXIT_PHASE_FAIL
}

run_phase() {
  local rc=0
  case "$1" in
    preflight)         phase_preflight        || rc=$? ;;
    cli-yes)           phase_cli_yes          || rc=$? ;;
    cli-interactive)   phase_cli_interactive  || rc=$? ;;
    mcp)               phase_mcp              || rc=$? ;;
    callback)          phase_callback         || rc=$? ;;
    public-url-probe)  phase_public_url_probe || rc=$? ;;
    *)
      echo "✗ unknown phase: $1" >&2
      usage >&2
      exit 64
      ;;
  esac
  if [[ $rc -ne 0 ]]; then
    FAILED=$((FAILED + 1))
    return $rc
  fi
  return 0
}

echo "🔍 create_app end-to-end acceptance"
echo "  mode:   $E2E_MODE"
echo "  host:   $OPS_HOST"
echo "  team:   ${OPS_TEAM_SLUG:-<unset>}"
echo "  target: $E2E_PUBLIC_URL"

if [[ -n "$SELECTED_PHASE" ]]; then
  run_phase "$SELECTED_PHASE"
  echo ""
  echo "  summary: passed=$PASSED skipped=$SKIPPED failed=$FAILED"
  if [[ "$E2E_REQUIRE_PASS" == "1" && $PASSED -eq 0 ]]; then
    echo "✗ E2E_REQUIRE_PASS=1 set but no phase passed (mode=$E2E_MODE)" >&2
    exit $EXIT_NO_PASS
  fi
  echo "✅ create_app phase '$SELECTED_PHASE' done"
  exit 0
fi

for phase in "${PHASES_ALL[@]}"; do
  run_phase "$phase" || true
done

echo ""
echo "  summary: passed=$PASSED skipped=$SKIPPED failed=$FAILED"
if [[ $FAILED -gt 0 ]]; then
  echo "✗ create_app end-to-end acceptance failed (mode=$E2E_MODE)" >&2
  exit $EXIT_PHASE_FAIL
fi
if [[ "$E2E_REQUIRE_PASS" == "1" && $PASSED -eq 0 ]]; then
  echo "✗ E2E_REQUIRE_PASS=1 set but no phase passed; all phases SKIPped (mode=$E2E_MODE)" >&2
  exit $EXIT_NO_PASS
fi
echo "✅ create_app end-to-end acceptance completed (mode=$E2E_MODE)"
