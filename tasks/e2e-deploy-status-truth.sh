#!/usr/bin/env bash
set -euo pipefail

# e2e-deploy-status-truth.sh
#
# 端到端驗收 issue #54：`GET /v1/teams/{team}/deploys/status`（CLI: `0ops deploys
# status <app-slug>`）必須回報 deploy_run.status 的持久化值，且不得在 DB 尚處於
# queued/preparing/building/pushing 這類早期階段時，被任何即時來源覆寫成 "live"
# （apps.go::getDeployStatusHandler，docs/features/create-app-flow/spec.md
# § 7.1/7.2 之「狀態查詢不是推進者」段落）。修復後的規則只有一條：端點逐字回傳
# deploy_run.status，讀取路徑不再探詢 ArgoCD，syncing → live 一律由
# reconciler.ArgoSyncScanner 提交。
#
# 為何選 local file:// 建置路徑作為驅動方式：
#   dev compose 跑在 K3S_DISABLE_ISOLATION=true 下，k3s.Client.GetApplicationStatus
#   在此模式對任何 app 立即假回傳 Synced/Healthy（src/internal/server/services/
#   k3s/argocd.go 的 DisableNamespaceIsolation 分支；見
#   TestGetApplicationStatus_DisabledMode_Returns_Synced_Healthy）。這正是 #54
#   在修復前於 dev 環境必然重現的條件：ArgoCD 全程回「健康」，只要 handler 在讀取
#   路徑採信它，deploy_run 一建立就會被回報成 live（tasks/local-build-e2e.sh 步驟 7 的
#   舊註解即記錄了這個已修復的繞路：「deploys/status 在 K3S_DISABLE_ISOLATION=true
#   下會立即回 live」）。因此本腳本不需要真 k8s/ArgoCD 叢集，也不需要 mock：修復是
#   否生效，直接由 dev compose 的既有假路徑就能誠實見真章。
#
# 這也代表 local file:// 建置本身（preview→confirm→pack build→push→部署）才是本
# feature 的「真組裝」路徑——比照 tasks/local-build-e2e.sh 的 bootstrap 順序，
# 讓 deploy_run 真的走過 queued → preparing → building → pushing 等早期階段，
# 而非用 SQL 假造這些列。SQL 在本腳本中「僅」用於唯讀讀取 deploy_run.status 作為
# 比對用的獨立 oracle（AGENTS.md e2e 規則允許的非招牌保證用途），從未用來偽造或
# 推進 CLI 回應的招牌保證本身。
#
# 使用方式：
#   ./manage.sh e2e-deploy-status-truth
#   ./tasks/e2e-deploy-status-truth.sh [--phase=<name>]
#
#   --phase=<name>   只跑單一 phase（合法值：preflight | drive | poll）
#
# 環境變數：
#   OPS_HOST            backend host；預設 http://127.0.0.1:${OPS_HOST_PORT:-8080}
#   TEAM_SLUG           預設 personal
#   TEAM_NAME           預設 Personal
#   APP_SLUG_PREFIX     app slug 前綴；預設 status-truth（每次跑加時間戳避免撞號）
#   GITHUB_LOGIN        預設 dev
#   TIMEOUT_SECS        等待 image push 完成 / live 的逾時；預設 180
#   POLL_INTERVAL_SECS  poll 間隔；預設 1
#   POSTGRES_USER/DB    db 容器內 psql 唯讀 oracle 用的使用者/資料庫；預設 ops/ops
#                       （經 `podman compose exec -T db psql`，不需 host 安裝 psql）
#   E2E_REQUIRE_PASS    設 1 時，若 PASSED 計數為 0，exit 6
#
# 結束碼：
#   0  全部 phase 通過或合法 SKIP
#   2  preflight 缺工具 / podman socket 權限不足
#   3  斷言失敗（含 #54 覆寫回歸 —— 見下方「回歸判準」）
#   4  CLI / podman / curl 子程序非預期退出
#   6  E2E_REQUIRE_PASS=1 但無任何 phase passed
#  64  用法錯誤
#
# 回歸判準（rollback criterion）：
#   若本腳本以 exit 3 失敗且失敗訊息含 "REGRESSION #54"，代表
#   getDeployStatusHandler 不再逐字回傳 deploy_run.status——有人在讀取路徑重新
#   引入了即時來源覆寫。此為「不可上線」訊號：
#     1. 立即 revert 觸發此腳本失敗的那次變更（不得帶著失敗的 e2e 合併/部署）。
#     2. 若已合併，對 apps.go::getDeployStatusHandler 的最近一次變更執行
#        revert，重跑本腳本至綠燈後再重新規劃修復。
#   逾時（TIMEOUT_SECS 內未見 image push 完成）不算回歸，屬環境問題（見腳本內訊息）。
#
# 硬性約束（docs/features/e2e-testing/spec.md §2 / AGENTS.md）：
# - 一律經 OPS_HOST 打 compose stack；不在 host 直接跑 ./bin/0ops-server。
# - CLI 以 podman run（或本腳本內以 go -C src run，比照 local-build-e2e.sh 既有
#   慣例，因 local file:// 路徑需要 host podman socket 掛載一致）驅動；backend
#   本身全程只透過 HTTP 存取，不繞過。
# - 招牌保證（CLI 回報 == 持久化值、且早期階段絕不誤報 live）全程由 CLI→HTTP
#   live 路徑行使；DB 讀取僅作唯讀 oracle 比對，從未寫入或偽造。

PHASES_ALL=(preflight drive poll)
SELECTED_PHASE=""

usage() {
  sed -n '/^# e2e-deploy-status-truth.sh/,/^$/p' "$0"
}

for arg in "$@"; do
  case "$arg" in
    --phase=*)
      SELECTED_PHASE="${arg#--phase=}"
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

OPS_HOST="${OPS_HOST:-http://127.0.0.1:${OPS_HOST_PORT:-8080}}"
TEAM_SLUG="${TEAM_SLUG:-personal}"
TEAM_NAME="${TEAM_NAME:-Personal}"
APP_SLUG="${APP_SLUG_PREFIX:-status-truth}-$(date +%s)"
GITHUB_LOGIN="${GITHUB_LOGIN:-dev}"
REPO_URL="${REPO_URL:-file:///workspace/examples/node-demo}"
TIMEOUT_SECS="${TIMEOUT_SECS:-180}"
POLL_INTERVAL_SECS="${POLL_INTERVAL_SECS:-1}"
POSTGRES_USER="${POSTGRES_USER:-ops}"
POSTGRES_DB="${POSTGRES_DB:-ops}"
REGISTRY_HOST="${LOCAL_REGISTRY_HOST:-localhost:5000}"
E2E_REQUIRE_PASS="${E2E_REQUIRE_PASS:-0}"

EXIT_TOOL_MISSING=2
EXIT_ASSERT_FAIL=3
EXIT_SUBPROC_FAIL=4
EXIT_NO_PASS=6

E2E_TMPDIR="$(mktemp -d -t e2e-deploy-status-truth.XXXXXX)"
trap 'rm -rf "$E2E_TMPDIR"' EXIT

PASSED=0
SKIPPED=0
FAILED=0
TOKEN=""

# stage order per create-app-flow spec § 7.1 workflow table.
STAGE_ORDER=(queued preparing building pushing rendering syncing live)
# 「早期階段」= 本次建置尚未產出 image 的階段。舊版 handler 正是在這些階段被
# 上一版 Application 的 Healthy 蓋掉，下方斷言直接釘住該缺陷形狀。
POST_BUILD_STAGES=(rendering syncing)

phase_header() {
  echo ""
  echo "=== PHASE: $1 ==="
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
    exit $EXIT_TOOL_MISSING
  fi
}

stage_index() {
  local target="$1" i
  for i in "${!STAGE_ORDER[@]}"; do
    if [[ "${STAGE_ORDER[$i]}" == "$target" ]]; then
      echo "$i"
      return 0
    fi
  done
  echo "-1"
}

# db_status: read-only oracle over the persisted deploy_run row, read via
# `podman compose exec` into the db container (no host psql needed). Never
# used to fake or advance the guarantee under test — only to check the
# CLI's answer against ground truth.
db_status() {
  podman compose exec -T db psql -U "$POSTGRES_USER" -d "$POSTGRES_DB" -tAc "
    SELECT dr.status
    FROM deploy_run dr
    JOIN app a ON a.id = dr.app_id
    WHERE a.slug = '${APP_SLUG}'
    ORDER BY dr.started_at DESC NULLS LAST, dr.id DESC
    LIMIT 1
  " 2>/dev/null | tr -d '[:space:]'
}

cli_status() {
  go -C src run ./cmd/cli deploys status "$APP_SLUG" \
    --host "$OPS_HOST" --team "$TEAM_SLUG" --token "$TOKEN" --output json 2>/dev/null \
    | jq -r '.status // "unknown"' 2>/dev/null || echo "unknown"
}

phase_preflight() {
  phase_header "preflight"
  require_cmd podman
  require_cmd curl
  require_cmd jq
  require_cmd go

  local sock="/run/user/$(id -u)/podman/podman.sock"
  if [[ ! -S "$sock" ]]; then
    echo "✗ podman socket missing at $sock — start it with: systemctl --user start podman.socket" >&2
    return $EXIT_TOOL_MISSING
  fi
  local perms
  perms=$(stat -c '%a' "$sock")
  case "$perms" in
    666|777) ;;
    *)
      echo "✗ podman socket perms=$perms at $sock — pack lifecycle container 無法讀取；" \
        "先跑 ./manage.sh podman-socket-loosen" >&2
      return $EXIT_TOOL_MISSING
      ;;
  esac

  if ! curl -fsS --max-time 3 "$OPS_HOST/health" >/dev/null 2>&1; then
    echo "✗ backend unreachable at $OPS_HOST — run './manage.sh dev' first" >&2
    return $EXIT_TOOL_MISSING
  fi
  if ! podman compose exec -T db psql -U "$POSTGRES_USER" -d "$POSTGRES_DB" -tAc "select 1" >/dev/null 2>&1; then
    echo "✗ cannot reach db oracle via 'podman compose exec db psql' (is the db service up?)" >&2
    return $EXIT_TOOL_MISSING
  fi
  phase_pass "backend /health OK, podman socket OK, db oracle reachable"
}

phase_drive() {
  phase_header "drive"

  echo "  bootstrap example repo (idempotent)"
  bash examples/node-demo/bootstrap.sh >/dev/null

  echo "  bootstrap-owner (idempotent — ignore already-exists)"
  go -C src run ./cmd/cli admin bootstrap-owner \
    --host "$OPS_HOST" --team-slug "$TEAM_SLUG" --team-name "$TEAM_NAME" \
    --github-login "$GITHUB_LOGIN" --output json >/dev/null 2>&1 || true

  echo "  mint dev bearer via seed-cli-token"
  TOKEN="$(podman exec -e DATABASE_URL='postgres://ops:ops_dev_pw@db:5432/ops?sslmode=disable' \
    0ops-server-1 \
    go run ./cmd/devtools/seed-cli-token \
    --team-slug "$TEAM_SLUG" \
    --github-login "$GITHUB_LOGIN" \
    --name "e2e-deploy-status-truth" \
    --ttl 1h)"
  if [[ -z "$TOKEN" ]]; then
    echo "✗ failed to mint dev bearer token" >&2
    return $EXIT_SUBPROC_FAIL
  fi

  echo "  create app (preview + confirm) for slug=$APP_SLUG"
  if ! go -C src run ./cmd/cli apps create \
      --host "$OPS_HOST" --team "$TEAM_SLUG" --token "$TOKEN" \
      --slug "$APP_SLUG" --repo-url "$REPO_URL" --ref main --yes; then
    echo "✗ apps create exited non-zero" >&2
    return $EXIT_SUBPROC_FAIL
  fi
  phase_pass "create_app dispatched for $APP_SLUG (real local build pipeline, no SQL fixtures)"
}

phase_poll() {
  phase_header "poll"
  if [[ -z "$TOKEN" ]]; then
    echo "✗ poll phase requires drive phase to have run first (no token)" >&2
    return $EXIT_ASSERT_FAIL
  fi

  local saw_early=0
  local saw_live=0
  local deadline=$(( $(date +%s) + TIMEOUT_SECS ))
  local checks=0

  while :; do
    local db1 cli db2
    db1="$(db_status)"
    cli="$(cli_status)"
    db2="$(db_status)"
    checks=$((checks + 1))

    if [[ -z "$db1" || -z "$cli" || -z "$db2" ]]; then
      echo "  (transient empty read: db1='$db1' cli='$cli' db2='$db2', retrying)"
      sleep "$POLL_INTERVAL_SECS"
      if [[ "$(date +%s)" -ge "$deadline" ]]; then
        echo "✗ timed out waiting for readable status (db1/cli/db2 kept coming back empty)" >&2
        return $EXIT_SUBPROC_FAIL
      fi
      continue
    fi

    local i1 ic i2
    i1="$(stage_index "$db1")"
    ic="$(stage_index "$cli")"
    i2="$(stage_index "$db2")"

    if [[ "$i1" == "-1" || "$ic" == "-1" || "$i2" == "-1" ]]; then
      echo "✗ REGRESSION-UNRELATED: unknown status value observed (db1=$db1 cli=$cli db2=$db2)" >&2
      return $EXIT_ASSERT_FAIL
    fi

    # Core #54 assertion: the CLI-reported stage must sit between the two
    # bracketing DB reads in the monotonic stage order — never ahead of
    # both (which would mean the handler asserted a transition no
    # 推進者 performed), never behind both (stale read).
    if (( ic < i1 || ic > i2 )); then
      echo "✗ REGRESSION #54: CLI reported '$cli' (stage $ic) outside the DB bracket" \
        "[$db1 (stage $i1) .. $db2 (stage $i2)] for app=$APP_SLUG" >&2
      return $EXIT_ASSERT_FAIL
    fi

    # Specifically: a DB row at an early (pre-image) stage must never be
    # reported as "live" by the CLI. This is the exact shape of issue #54:
    # a stale/fake-healthy ArgoCD Application overwriting an early row.
    local early=1
    for s in "${POST_BUILD_STAGES[@]}" live; do
      [[ "$db1" == "$s" || "$db2" == "$s" ]] && early=0
    done
    if [[ "$early" == "1" ]]; then
      saw_early=1
      if [[ "$cli" == "live" ]]; then
        echo "✗ REGRESSION #54: DB status is '$db1'/'$db2' (pre-image stage) but CLI reported 'live' for app=$APP_SLUG" >&2
        return $EXIT_ASSERT_FAIL
      fi
    fi

    if [[ "$db1" == "live" && "$db2" == "live" && "$cli" == "live" ]]; then
      saw_live=1
      echo "  db=$db1 cli=$cli — reached live, consistent"
      break
    fi

    echo "  db1=$db1 cli=$cli db2=$db2 (check #$checks, consistent)"

    if podman logs --tail 200 0ops-server-1 2>&1 | grep -q "\"localbuild .* failed\".*\"$APP_SLUG\""; then
      echo "✗ dispatcher reported build failure — see server logs for app=$APP_SLUG" >&2
      podman logs --tail 100 0ops-server-1 2>&1 | grep "$APP_SLUG" >&2 || true
      return $EXIT_SUBPROC_FAIL
    fi

    if [[ "$(date +%s)" -ge "$deadline" ]]; then
      echo "✗ timed out after ${TIMEOUT_SECS}s waiting for deploy_run to reach live (last db=$db2 cli=$cli)" >&2
      echo "  (this is an environment/build-speed timeout, not a #54 regression, unless an" >&2
      echo "   assertion above already fired)" >&2
      return $EXIT_SUBPROC_FAIL
    fi
    sleep "$POLL_INTERVAL_SECS"
  done

  if [[ "$saw_early" != "1" ]]; then
    echo "✗ never observed an early (pre-image) stage before live — build raced ahead of the" \
      "poll loop, so the #54 guarantee was not actually exercised; lower POLL_INTERVAL_SECS or" \
      "re-run" >&2
    return $EXIT_ASSERT_FAIL
  fi
  if [[ "$saw_live" != "1" ]]; then
    echo "✗ loop exited without confirming live (unexpected)" >&2
    return $EXIT_ASSERT_FAIL
  fi

  # Confirm image landed in the local registry too — independent evidence
  # the pipeline actually ran end-to-end rather than the DB row being
  # advanced with nothing behind it.
  local tags_url="http://${REGISTRY_HOST}/v2/0ops-apps/${TEAM_SLUG}/${APP_SLUG}/tags/list"
  if curl -fsS "$tags_url" 2>/dev/null | jq -e '.tags | length > 0' >/dev/null 2>&1; then
    phase_pass "checked $checks polls across early stages; CLI never reported live early; final state live and consistent; image present in registry"
  else
    echo "✗ deploy_run reached live but no image tags found at $tags_url" >&2
    return $EXIT_ASSERT_FAIL
  fi
}

run_phase() {
  local rc=0
  case "$1" in
    preflight) phase_preflight || rc=$? ;;
    drive)     phase_drive     || rc=$? ;;
    poll)      phase_poll      || rc=$? ;;
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

echo "🔍 deploy-status-truth end-to-end acceptance (issue #54)"
echo "  host: $OPS_HOST"
echo "  team: $TEAM_SLUG"
echo "  app:  $APP_SLUG"

if [[ -n "$SELECTED_PHASE" ]]; then
  run_phase "$SELECTED_PHASE"
  echo ""
  echo "  summary: passed=$PASSED skipped=$SKIPPED failed=$FAILED"
  if [[ "$E2E_REQUIRE_PASS" == "1" && $PASSED -eq 0 ]]; then
    echo "✗ E2E_REQUIRE_PASS=1 set but no phase passed" >&2
    exit $EXIT_NO_PASS
  fi
  echo "✅ deploy-status-truth phase '$SELECTED_PHASE' done"
  exit 0
fi

for phase in "${PHASES_ALL[@]}"; do
  run_phase "$phase" || { rc=$?; echo ""; echo "  summary: passed=$PASSED skipped=$SKIPPED failed=$FAILED"; exit "$rc"; }
done

echo ""
echo "  summary: passed=$PASSED skipped=$SKIPPED failed=$FAILED"
if [[ "$E2E_REQUIRE_PASS" == "1" && $PASSED -eq 0 ]]; then
  echo "✗ E2E_REQUIRE_PASS=1 set but no phase passed; all phases SKIPped" >&2
  exit $EXIT_NO_PASS
fi
echo "✅ deploy-status-truth end-to-end acceptance completed (issue #54 guarantee held)"
