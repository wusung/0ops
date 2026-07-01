# tasks/todo-archive.md

> 本檔為 M0-M6 milestone 之歷史 backlog snapshot（凍結於 2026-05-21）。
> 不再維護；進度單一事實來源已移回 `tasks/todo.md`（精簡為活躍項目）。
> 保留目的：驗收證據、out-of-scope 風險記錄、設計決策 trace、後續 agent 讀取參照。
> 內容含 M0.1 / M2.* / M3.* / M4.* / M5.* / M5.6.* 之完整任務分解與 P0/P1 史料。

## Milestone Backlog

### M0.1 — dev-environment scaffold

> 對應 milestone：**M0**（Module scaffold + dev env + ADR 定稿）
> 對應 spec：`docs/features/dev-environment/spec.md`
> 對應 plan：`docs/0ops-plan.md` § 立即下一步 step 2

## 已完成

- [x] `go mod init github.com/winshare/zeroops`（Go 1.25；M0 由 1.23 上修以符 mcp-go-sdk v1.6 最低版本，spec § 6.1 與 plan § Go 技術棧已同步更新）
- [x] 目錄結構：`cmd/{server,cli,mcp}/`、`internal/{server,cli,mcp,shared}/`、`migrations/`、`tasks/`
- [x] `cmd/server/main.go`：chi router + `/health`（json）+ `/metrics`（prometheus registry）+ slog JSON + graceful shutdown
- [x] `cmd/cli/main.go`：cobra root + `--version`（透過 `internal/shared.Version`，可 ldflags 注入）
- [x] `cmd/mcp/main.go`：官方 `modelcontextprotocol/go-sdk` v1.6 + stdio transport，logger 走 stderr
- [x] `internal/server/health/`、`internal/server/observability/`、`internal/shared/version.go`
- [x] `migrations/00001_init.sql`：v1 起手 schema block（user_account / team / team_membership / app / domain_binding / deploy_run / usage_sample / preview / cli_token / webhook_dedup / audit_log / reconciliation_job）
- [x] `migrations/Dockerfile`：multi-stage、`goose@v3.22.1` pin、distroless nonroot（ADR-0009）
- [x] `cmd/server/Dockerfile`：deps / builder / dev (air@v1.62.0) / runtime（distroless nonroot）
- [x] `cmd/cli/Dockerfile`、`cmd/mcp/Dockerfile`：deps / builder / runtime
- [x] `compose.yaml`（root）：`db`(pg17-alpine, healthcheck) → `migrate`(one-shot, depends_on healthy) → `server`(dev target, healthcheck `/health`)
- [x] `.dockerignore`、`.gitignore`（雙含 `.env`）、`.env.example`、`.env`（本機 dev 從範本複製）
- [x] `Makefile` target 契約（spec § 9）：`dev / dev-down / dev-clean / dev-logs / dev-shell / migrate / migrate-down / build-images / lint-compose / lint-docker / lint-go / test / build / tidy`
- [x] `.air.toml`（server dev stage 熱重載）
- [x] `.golangci.yml`（govet / staticcheck / errcheck / gosec / revive / misspell / ineffassign / unused）
- [x] `go mod tidy`：依賴解析通過（chi, prometheus, cobra, mcp-go-sdk, slog stdlib）
- [x] `make lint-compose`：`podman compose config -q` 通過
- [x] **lessons L001 / 全域 feedback memory**：dev 驗證走 compose / Makefile，禁止 host 直跑 binary

## 進行中

（無）

## 已完成（cont.）

- [x] `podman compose build`（4 images：server-dev / migrations / server-runtime / cli-runtime / mcp-runtime）
  - Sizes: server-runtime 12.3 MB / cli-runtime 7.08 MB / mcp-runtime 8.75 MB / migrations 54 MB / server-dev 495 MB
- [x] `make dev`：db healthy → migrate 0 exit (`migrated to version: 1`) → server healthcheck healthy
- [x] `/health` HTTP 200，body `{"status":"ok","version":"dev"}`
- [x] `/metrics` 暴露 `zeroops_http_request_duration_seconds`（route/method/team_bucket）+ `zeroops_http_requests_total`（route/method/status/team_bucket）+ `zeroops_http_requests_in_flight` + Go runtime collectors
- [x] migrate idempotent：第二次 up 顯示 `no migrations to run. current version: 1`
- [x] runtime image 全為 `User=nonroot:nonroot`（4/4）
- [x] runtime image 無 `/bin/sh`（distroless 驗證）
- [x] `podman run --rm 0ops-cli:runtime --version` → `0ops dev`
- [x] MCP `initialize` round-trip：回 `{"jsonrpc":"2.0","id":1,"result":{"protocolVersion":"2025-06-18","serverInfo":{"name":"0ops-mcp","version":"dev"},...}}`
- [x] M0.1 最小驗收測試：`internal/server/health`、`internal/server/observability`、`internal/cli`、`internal/mcp/server`
- [x] `go test ./...` 通過（含上述 4 個測試 package）
- [x] `go vet ./...` 通過
- [x] dev stack 收尾：`make dev-down` 清淨

## host port 衝突解法（spec § 12 open issue 已關）

- compose.yaml `server.ports` 改為 `${OPS_HOST_PORT:-8080}:8080`
- `.env` `.env.example` 新增 `OPS_HOST_PORT`（預設 8080）
- `compose.override.yaml.example` 保留為 db port 暴露等其他覆寫情境（git/docker ignore 雙含 `compose.override.yaml`）
- spec § 5.3、§ 12 已同步更新

## 後續（不阻擋 M0.1 收尾）

- [ ] `golangci-lint` 與 `hadolint` 安裝後跑 `make lint-go` / `make lint-docker`（CI 強制 / 本機可選）
- [x] M0.2：`internal/server/db/`（pgx + sqlc 架接）+ `goose create` 之開發 workflow runbook
- [x] M0.3：第一條 read-only chain（apps list）— backend handler + middleware chain + CLI + MCP tool（屬 M1，但延伸自 M0 scaffold）；並擴充 repo inspect / deploys status+logs / domains list 之 backend + CLI + MCP read slice

## 設計決策變更（與 spec / plan 衝突已同步修補）

- Go 版本由 1.23 → 1.25。原因：mcp-go-sdk v1.6 之最低 Go 為 1.25。同步修補：
  - `cmd/{server,cli,mcp}/Dockerfile` builder + dev stage
  - `migrations/Dockerfile` builder
  - `docs/features/dev-environment/spec.md` § 1、§ 6.1
  - `docs/0ops-plan.md` Go 技術棧 row、TBD 已決議區塊「Go 版本」

## 驗證準則對應（spec § 10）

| 驗證項 | 狀態 |
|---|---|
| compose schema `podman compose config -q` | ✅ |
| Dockerfile lint `hadolint` | ⏸ 待安裝 |
| migrate idempotent（連跑兩次） | ✅（第二次 `no migrations to run`） |
| server `/health` HTTP 200（30s 內） | ✅ |
| Image distroless（無 shell） | ✅（4/4 image `/bin/sh` not found） |
| Image nonroot 執行 | ✅（4/4 image `User=nonroot:nonroot`） |
| `.env` 不入版本控制 | ✅（`.gitignore` + `.dockerignore` 雙含） |
| rootless podman 啟動成功 | ✅ |

---

### M2 — gap remediation backlog

> 對應 milestone：**M2**（`create_app` + 兩階段 preview/confirm + idempotency + winshare 子網域 + observability GA + 隔離模型）
> 對應 milestone 定義：`docs/0ops-plan-milestones.md`
> 對應主 specs：
> - `docs/features/create-app-flow/spec.md`
> - `docs/features/preview-confirm-gate/spec.md`
> - `docs/features/build-pipeline-and-callback/spec.md`
> - `docs/features/k3s-namespace-isolation/spec.md`
> - `docs/features/winshare-subdomain-and-tunnel/spec.md`
> - `docs/features/observability-skeleton/spec.md`
> - `docs/features/slo-and-alerting/spec.md`
> - `docs/features/gitops-render-and-argocd/spec.md`

## 結論

- 目前狀態屬 **M2 部分骨架已存在，但未達 milestone 驗收**。
- `create_app` 已有 preview/confirm、CLI `--yes`、MCP tool、callback 驗章初版、基本 metrics。
- 阻擋項集中在：**真實 deploy orchestration、K3s 隔離落地、GitOps/ArgoCD 鏈路、observability GA、MCP description lint 契約、外部驗收證據**。

## P0 — milestone blocker（未完成前不得宣稱 M2 done）

- [x] **M2.1 create_app orchestration 落地**
  - 補 `internal/server/services/createapp/`，把目前 handler 內的簡化流程抽成 spec 定義的 `SideEffects / Precheck / Execute / Compensate / state_machine`
  - 對齊 `docs/features/create-app-flow/spec.md` §1, §5, §6, §7
  - `deploy_run` 狀態至少補齊：`queued → preparing → building → pushing → rendering → syncing → live`
  - callback/replay 必須回放 `last_result`，不可只寫 DB row 後直接回成功
  - 測試：
    - handler + service unit tests
    - idempotent replay contract test
    - failed reversible step → compensate test

- [x] **M2.2 GitHub Actions dispatch + callback 全鏈路**
  - ✅ 補 backend 到 GHA `workflow_dispatch` 觸發 client（Client.Dispatch in workflowdispatch/client.go）
  - ✅ 補 ephemeral `ops_token` 簽發與 callback payload 對應欄位（OpsTokenSigner in workflowdispatch/opstoken.go）
  - ✅ 對齊 `docs/features/build-pipeline-and-callback/spec.md`（deploy/workflows/deploy-app.yml）
  - ✅ 補 `deploy_run` callback 後續轉態：normalizeDeployStatus 支援全 10 狀態（queued/preparing/building/pushing/rendering/syncing/live/failed/canceled/rolled_back）
  - ✅ 補 dedup、timestamp window、signature failure 測試
  - ✅ 驗收證據：
    - 一次 dispatch 成功 → TestClientDispatchPostsRepositoryDispatch
    - 一次 callback success → TestDeployRunCallbackHMACAndDedup
    - 一次 callback duplicate no-op → TestDeployCallbackDedupPreventsDuplicateDelivery

- [x] **M2.3 GitOps render/push + ArgoCD sync 鏈路**
  - 已補 render service 與 git push 執行路徑
  - 已定義 `0ops-gitops` repo 目錄責任與最小 manifest 模板
  - 已補 ArgoCD sync 狀態查詢介面，`deploys/status` 可 overlay syncing/live
  - 對齊 `docs/features/gitops-render-and-argocd/spec.md`
  - 測試：
    - render output contract test
    - git push failure → compensate
    - sync status transition test

- [x] **M2.4 K3s namespace isolation 最小可用版**（2026-05-15）
  - `internal/server/services/k3s/client.go` 為真 dynamic client，非 no-op
  - 落地：
    - `EnsureNamespace`（PSA label 隨 namespace 同 transaction 套用，spec § 4.2 + 硬性規則 #4）
    - `EnsureResourceQuota`（依 plan tier，spec § 5.1）
    - `EnsureLimitRange`
    - `EnsureNetworkPolicy`（ingress + egress 預設拒跨 team）
    - `PatchNamespacePSA`（reconciler 用，現場補救殘缺 PSA）
    - `PatchGHCRImagePullSecret`（method 就緒；token 簽發等 M3.2 wire `github_install_id`）
    - `EnsureTeamIsolation`（atomic orchestration：NS + RQ + LR + NP；任一失敗則 `DeleteNamespace` 回滾，spec § 15 硬性規則 #3）
  - `internal/server/services/createapp/service.go` saga 改呼叫 `EnsureTeamIsolation`，K3s 失敗會 rollback `CreateApp`
  - 對齊 `docs/features/k3s-namespace-isolation/spec.md`
  - 驗收證據：
    - `internal/server/services/k3s/client_test.go` 使用 `dynamicfake.NewSimpleDynamicClient` 模擬 `kubectl get`：team-acme namespace、PSA labels、ResourceQuota (plan tier 值)、LimitRange、NetworkPolicy (ingress/egress) 皆存在；rollback 路徑驗證任一 sub-step 失敗時 namespace 被 `DeleteNamespace` 回收
    - saga test `TestConfirmRollsBackAppWhenK3sIsolationFails` 證明 isolation 失敗 → app row 回滾、preview 不被 consume

- [x] **M2.5 winshare 子網域真實路由**（2026-05-15）
  - 補 Cloudflare / tunnel route 整合，不可只回傳字串 URL
  - 對齊 `docs/features/winshare-subdomain-and-tunnel/spec.md`
  - 落地：
    - `create_app` confirm 流程已在 `RouteAppToDomain` 後接續呼叫 `CreateTunnelRoute`；tunnel route 建立失敗會中止流程並 rollback（刪除已建立 app row）
    - `internal/server/services/cloudflare/tunnel.go`：`GetTunnelConnectorsReady` 走 `/accounts/{account}/cfd_tunnel/{id}/connections`，提供 reconciler 拿真實 connector 數
    - `internal/server/services/cloudflare/client.go`：`request()` 透過 `recordCloudflareCallDurationFn` 量測每 op 延遲，新增 `BindCallDurationMetric`；wired in `cmd/server/main.go`
    - `internal/server/apperror`：新增 `ClassUnavailable`（503），`apps.go` 將 `ErrRouteMissing` / `ErrConfigMissing` 改 map 至 `cloudflare_api_error` (`unavailable`/503)，`ErrRateLimited` 維持 `cloudflare_rate_limited` (`too_many_requests`/429)，對齊 `docs/features/error-model/spec.md` § 5.5
    - `internal/server/observability/metrics.go`：新增 `zeroops_cloudflare_api_call_duration_seconds`（histogram, op）與 `zeroops_cloudflare_tunnel_connectors_ready`（gauge），含 `ObserveCloudflareAPICallDuration` / `SetCloudflareTunnelConnectorsReady`
    - `deploy/chart/cloudflare-tunnel/`：完整 Helm chart（namespace + deployment 3 replica + anti-affinity + 鎖版 image + `--no-autoupdate`、secret-from-Secret、NetworkPolicy ingress 拒 / egress 僅 7844+443+traefik）；chart 含 `fail` guard 拒絕 < 3 replica
    - `deploy/gitops/observability/prometheus-alert-rules.yaml`：新增 `TunnelConnectorsLow`（< 2 for 5m, critical）/ `TunnelDown`（== 0 for 1m, critical），對齊 `slo-and-alerting` § 6.4；promtool 驗證通過（7 alerts / 7 recording rules）
  - 驗收證據：
    - `internal/server/services/cloudflare/tunnel_test.go::TestGetTunnelConnectorsReadyCountsActiveConnections`、`TestRequestRecordsCallDuration` 通過
    - `internal/server/observability/metrics_test.go::TestM2_5CloudflareTunnelMetricsExpose` 通過
    - `deploy/chart/cloudflare-tunnel/chart_test.go::TestChartFilesEnforceHardRules` / `TestDeploymentGuardsReplicaFloor` 通過
    - `deploy/gitops/observability/assets_test.go::TestPrometheusAlertRulesContainM26CriticalRules` 已擴充並通過（含 `TunnelConnectorsLow` / `TunnelDown`）
    - `make test` 全綠；`make lint-compose` 通過；`make m2-6-promtool` 通過
    - route 建立失敗已有明確錯誤分類（`cloudflare_api_error` / `cloudflare_rate_limited`）與 rollback：`createapp.Service.Confirm` 於 `RouteAppToDomain` / `CreateTunnelRoute` 任一失敗時呼叫 `DeleteAppByID` 回滾 app row 且 preview 不被 consume，由 `internal/server/services/createapp/service_test.go::TestConfirmRollsBackAppOnCloudflareFailure` / `TestConfirmRollsBackWhenCreateTunnelRouteFails` 守備
  - 限制（不在 worktree 內可驗）：
    - 「`nextdemo.winshare.tw` 外部 HTTP 200」屬 production smoke：需 Cloudflare zone 已部署 wildcard CNAME + 已部署 `deploy/chart/cloudflare-tunnel/` + K3s ingress 已 sync；本 milestone 提供完整鏈路所需製品，但實際 200 留待 M2.8 端到端腳本 + production rollout 驗證

- [x] **M2.6 Observability GA**
  - 在現有 `internal/server/observability/metrics.go` 之外補齊：
    - preview/deploy/cf 指標
    - `cluster:zeroops_preview_consumption_rate:7d`
    - `histogram_quantile(... zeroops_preview_consume_duration_seconds_bucket ... )`
    - `zeroops_cloudflare_api_calls_total`
    - deploy success/failure / lead time 所需 metrics
  - 補 dashboard / alert 規則資產，對齊 `docs/features/slo-and-alerting/spec.md`
  - 驗收證據：
    - metrics scrape 可見
    - dashboard 載入
    - burn-rate rule 可被 promtool 驗證

- [x] **M2.7 MCP preview/confirm description lint 契約**（2026-05-15）
  - `create_app_preview` / `create_app` description 已補入 `ALWAYS call this BEFORE` / `NEVER call this tool without` verbatim 子字串；同步補 `invite_member` / `remove_member` preview-confirm pair
  - `cmd/mcp/main.go` 改為 `run()` 結構：完成 `mcpserver.NewWithRegistry` 後立即跑 `lint.ApplyAll`，違反任一規則 → 印所有違反行 → `os.Exit(2)`（spec § 4.6）
  - `internal/mcp/registry.go`：新增 `Registry` + 泛型 `AddTool[In, Out]`，於註冊時透過 `jsonschema.For[In]` 擷取 InputSchema，補上 SDK 未公開的 reflective tool 列舉接口（spec § 4.7）
  - `internal/mcp/lint/`：實作 R1（`*_preview` 必含 `ALWAYS call this BEFORE`）、R2（write/delete tool 必含 `NEVER call this tool without`）、R3（write/preview tool input schema 必 required `team_slug`）；`Violation` 型別承載 RuleID、Tool、Message
  - `WriteActions()` 與 spec § 4.3 表同步，並補入 `remove_member`（spec hard rule #6 同步）
  - 對齊 `docs/features/mcp-tool-description-lint/spec.md`
  - 驗收證據：
    - `internal/mcp/lint/rules_test.go`：12 條規則 fixture（fail / pass）皆通過，含 R3 對「`team_slug` 在 properties 但不在 required」紅燈分支
    - `internal/mcp/server/lint_test.go::TestRegisteredToolsPassStartupLint`：實際 `NewWithRegistry` 註冊的 14 個 tool 全數通過 `lint.ApplyAll`
    - `internal/mcp/server/lint_test.go::TestCreateAppToolsContainRequiredClauses`：固化 `create_app_preview` / `create_app` 的 ALWAYS / NEVER 子字串
    - `cmd/mcp/main_test.go::TestStartupLintWouldRejectBadDescription`：對「故意放錯 description」case 回傳 exit code 2 並印出 `Aborting startup`

- [x] **M2.8 端到端驗收腳本**（2026-05-15）
  - `tasks/m2-8-e2e-acceptance.sh`：6-phase orchestrator（preflight / cli-yes / cli-interactive / mcp / callback / public-url-probe），3 mode（`local|staging|production`）；`--phase=` 可單跑某 phase；`E2E_REQUIRE_PASS=1` 用於 CI/cron，當無任何 phase passed 時退出碼 6（避免「全 SKIP exit 0」變成假綠）。對齊 `docs/features/create-app-flow/spec.md` § 12「End-to-end happy path」行
  - 落地：
    - **CLI `--yes`**：`run_cli apps create --slug ... --yes` 經 `podman run [--network 0ops_default] localhost/0ops-cli:runtime` 驅動，避免 host 直跑 binary（lessons/L001）；`OPS_HOST` 為 staging/production 外部 host 時自動 bypass `--network`（避免被綁回 compose 網路）
    - **CLI 互動式**：`printf 'y\n' | podman run -i ... apps create ...` 走 `internal/cli.confirmAction` 提示路徑
    - **MCP `create_app_preview` → `create_app`**：單一 `podman run -i localhost/0ops-mcp:runtime` 同 stdio session 內跑 `initialize` → `tools/call create_app_preview`，用 python3 解出 JSON-RPC `id=2` response 的 `preview_id`，再對同 session 跑 `tools/call create_app`，最後驗 `app_id` / `deploy_run_id` / `subdomain_url` 在 confirm response 內
    - **callback HMAC 自我比對**：`openssl dgst -sha256 -hmac $OPS_CALLBACK_SECRET` 簽 `<ts>.<body>`，POST `/internal/deploy-runs/<run_id>/callback`，預期 200/404（local 無 row → 404 但 sig OK）；401 視為 secret mismatch 並退出 5
    - **public-url-probe**：`mode=production` 才執行 `curl --max-time 30 $E2E_PUBLIC_URL`，預期 200；非 production 自動 SKIP
    - 非 local mode preflight 強制檢查 `podman image exists` 對 `0ops-cli:runtime` / `0ops-mcp:runtime`，缺即提示 `make build-images`；中介檔案統一走 `mktemp -d`，trap 清理
  - Makefile：新增 `m2-8-e2e-acceptance`（預設 mode=local；CI 應額外 export `E2E_REQUIRE_PASS=1` 才有實值守護）與 `m2-8-check`（lint-go + test 別名，沿用 `m2-2-check` 慣例）
  - 驗收證據（worktree 內可驗）：
    - `internal/server/m2_8_acceptance_test.go::TestM28EndToEndPreviewConfirmCallback`：preview → confirm → idempotent replay → success callback → bad-sig 拒絕，整條 spec § 6.3 / § 12 接合（涵蓋 AGENTS.md 所列 preview/confirm、idempotent retry、callback 簽章、deploy 狀態轉移四個高風險區）
    - `internal/server/m2_8_acceptance_test.go::TestM28AcceptanceScriptShape`：守護 6 個 phase 之 `phase_header` 呼叫 + `PHASES_ALL` 完整一行 + 3 個 E2E_MODE 值 + 必要 env vars + `/internal/deploy-runs/` 路徑 + `nextdemo.winshare.tw` default + MCP tool 名稱 + executable bit
    - `make test` 全綠；`make lint-compose` 通過
    - script local mode 自跑 exit 0；同樣參數加 `E2E_REQUIRE_PASS=1` 退出碼 6（驗證 CI 假綠保險）
  - 限制（不在 worktree 可驗）：`E2E_MODE=production` 對 `nextdemo.winshare.tw` 真 200 仍依賴 M2.5 production rollout（Cloudflare zone wildcard CNAME + `deploy/chart/cloudflare-tunnel/` 部署 + K3s ingress sync）；本腳本提供完整驅動與斷言，rollout 後執行 `E2E_MODE=production make m2-8-e2e-acceptance` 即可定案 M2 收尾。`make m2-8-check` 之 `lint-go` 子步驟受 M2.5 留下的 27 條 pre-existing lint debt 影響為紅燈，與 M2.8 範圍無關，併同 P1「補 docs 與程式一致性」追蹤

## P1 — 高優先但可在 P0 串接中分批完成

- [ ] **補 DB schema / migration 漂移**
  - 檢查 `deploy_run` 欄位是否足夠承接 spec：status、trace、scan、classification、events、gitops commit sha 等
  - 缺欄位就補 migration，不要把 spec 需求留在註解層

- [ ] **補 failure classification 與錯誤模型對齊**
  - `create_app` / callback / sync / route failure 全部要有穩定錯誤碼與 `failure_classification`
  - 對齊 `docs/features/error-model/spec.md` 與 `docs/features/reconciler-and-incident/spec.md`

- [ ] **補 trace_id 全鏈路**
  - backend request → preview row → deploy_run → GHA payload → callback → audit/structured log 必須可串回同一 trace

- [ ] **補 docs 與程式一致性**
  - 實作完成的每一批，立即回寫：
    - `docs/0ops-plan.md`
    - `docs/0ops-plan-observability.md`
    - 對應 feature spec 若有 drift

## P2 — M2 收尾與後續銜接

- [ ] **補 runbook**
  - GHA callback 驗章失敗排查
  - create_app stuck in building/syncing 排查
  - winshare subdomain 路由失敗排查
  - burn-rate alert 處理流程

- [ ] **補 lessons**
  - 完成 M2 後，把踩到的 infra / callback / GitOps / Cloudflare 問題寫入 `tasks/lessons.md`

## 驗收基準（判定 M2 done）

- [ ] `create_app` preview/confirm 真正走完整 saga，而非單純 DB insert
- [ ] `preview_id` replay 會回放 `last_result`
- [ ] `deploy_run` 狀態機完整，並有測試
- [ ] GHA callback 驗章、dedup、狀態推進全通
- [ ] `team-<slug>` namespace + ResourceQuota + LimitRange + NetworkPolicy + PSA baseline 可被驗證
- [ ] `nextdemo.winshare.tw` 真實外部 HTTP 200
- [x] Prometheus metrics 含 preview/deploy/cf 指標
- [x] Grafana dashboard + burn-rate alert 可用
- [x] MCP `create_app_preview` / `create_app` description lint 合規
- [x] CLI 與 MCP 都各跑過一次端到端驗收（harness 落地於 `tasks/m2-8-e2e-acceptance.sh`，2026-05-15；production smoke 待 M2.5 rollout）

## M3 — install / domain verify backlog

### M4.1 — Webhook auto/manual redeploy + replay protection（2026-05-16）

> 對應 spec：`docs/features/webhook-and-redeploy/spec.md`
> 對應 task：`tasks/task-list.md` row M4.1
> 對應 paths：`internal/server/services/githubwebhook/**`、`internal/server/services/redeploy/**`

- [x] `POST /v1/webhooks/github` 重構為單一 dispatcher：HMAC verify + 5MB payload cap + event whitelist + delivery_id dedup（push 路徑）；installation* 維持原 githubapp.Service 路由（既有 dedup 不動，避免雙重 insert）
- [x] `internal/server/services/githubwebhook/`：verify.go (5MB bound) / parse.go (event whitelist + branch/ref/repo normalization) / dispatcher.go / push_handler.go / metrics.go / doc.go
- [x] `internal/server/services/redeploy/`：trigger.go（共用 INSERT + workflow_dispatch；source/actor/delivery 屬性鍵）+ action.go（user-initiated preview-confirm；in-flight/paused/expired 守備）+ doc.go
- [x] CLI：`0ops deploys redeploy <slug>` 支援 `--ref` / `--commit-sha` / `--yes` / `--dry-run`
- [x] MCP：`redeploy_preview` + `redeploy` tools；R1 (`ALWAYS call this BEFORE`) / R2 (`NEVER call this tool without`) / R3 (`team_slug` required) 三規則皆通過 lint
- [x] Backend client：`PreviewRedeploy` / `Redeploy`
- [x] DTO：`internal/shared/dto/deploys.go`（`RedeployRequest` / `ConfirmRedeployRequest` / `RedeployResponse`）
- [x] DB schema：migration `00005_deploy_run_redeploy.sql` 新增 `deploy_run.source` (`user`/`webhook`/`reconciler` CHECK constraint) + `webhook_delivery_id` + `actor_user_id` (FK)；補 `(app_id, status)` partial index 與 `webhook_delivery_id` partial index
- [x] DB methods (`internal/server/db/redeploy.go`)：`InsertRedeployRun` / `HasInFlightDeployRun` / `FindLiveAppsByRepoAndBranch` (repo_url 正規化處理 `/` 與 `.git`) / `AppendWebhookAudit`
- [x] RBAC：新增 `ActionRedeploy` (member + `apps:write`)
- [x] 高風險區覆蓋（AGENTS.md「Testing」段）：
  - preview/confirm：preview row 建立、paused 拒絕、cross-actor/cross-team isolation、idempotent replay、preview expired、in-flight (`ErrAppBusy`) (`internal/server/services/redeploy/action_test.go`)
  - idempotent retry：webhook dedup via `webhook_dedup` 24h、preview last_result 回放、webhook handler in-flight skip (`internal/server/redeploy_test.go::TestWebhookPushReplayIsDeduped`、`TestRedeployHTTPIdempotentReplay`)
  - team isolation：preview cross-team 回 `ErrPreviewNotFound`（同 ADR-0001 enumeration 防範）
  - role/scope 權限矩陣：`ActionRedeploy` 走 `mw.CheckTokenScope` 與 `apps:write`
  - webhook 簽章驗證：HMAC valid/invalid/payload-too-large/missing-delivery-id 全經 dispatcher 前置守備，驗章前不寫 DB (`TestWebhookBadSignatureRejected`、`TestWebhookPayloadTooLargeRejected`)
  - deploy 狀態轉移：`InsertRedeployRun` 落入 `queued`；後續轉態走既有 callback 鏈
  - reconciler 收斂：source/delivery_id/actor_user_id 都已落 DB，給 M5.3 reconciler 用
- [x] HTTP 整合測試：preview happy path / paused rejection / confirm idempotent replay / webhook push triggers redeploy for live app / webhook installation event 仍走原路徑 (`internal/server/redeploy_test.go`)
- [x] Webhook dispatcher 單元測試：signature failure / ping / unsupported event ack / push routing & dedup / payload too large / installation delegated / missing delivery_id (`internal/server/services/githubwebhook/dispatcher_test.go`)
- [x] Push handler 單元測試：live app trigger / paused skip / in-flight skip / branch-deleted ignore / tag push ignore / multi-app fan-out / installation 未綁定 team (`internal/server/services/githubwebhook/push_handler_test.go`)
- [x] Verify / parse 單元測試：branch ref normalize / repo url normalize / event whitelist / payload decode / 5MB bound (`internal/server/services/githubwebhook/{verify,parse}_test.go`)
- [x] MCP lint：`redeploy_preview` + `redeploy` 已存在於 `internal/mcp/lint/rules.go::writeActions`；lint test 自動覆蓋
- [x] `make test` 全綠（27 packages）
- [x] `make lint-compose` 通過
- [x] dev DB smoke：00001 + 00005 up/down roundtrip 對 postgres-17 真實庫驗證 schema + index + check + FK 全部建立 / 移除（手動 psql 套用因 M3.2 留下的 `migrations/00003_*.sql` duplicate version 在 worktree 內仍阻擋 `make migrate` image-rebuild path）

**Out of scope / 風險回報**：M4.1 不修 `migrations/00003_*.sql` duplicate version panic（屬 M2 → M3.2 既知遺留問題）；M4.1 migration 編號 `00005` 不衝突且已通過真實 psql roundtrip 驗證。後續另起任務 rename `00003_tool_grants_and_auth_status.sql` → `00004_*.sql`，現有 `00004_team_github_install_index.sql` 與本任務 `00005_deploy_run_redeploy.sql` 順延一格。

### M4.2 — Rate limit (per-token / per-team) + 429（2026-05-16）

> 對應 spec：`docs/features/rate-limit-and-abuse/spec.md`
> 對應 task：`tasks/task-list.md` row M4.2
> 對應 paths：`internal/server/middleware/ratelimit/**`
> 對應 ADR：ADR-0011（plan tier 配額表 § 3.1）

- [x] `internal/server/middleware/ratelimit/`：plan tier 表（ADR-0011 § 3.1 free/starter/pro/team × per-token read/write/preview-create + per-team write/preview-create）、token-bucket pool（`golang.org/x/time/rate`，per-(scope,key,category) `sync.Map`）、chi middleware（categorize by method+path；GET → read，含 `:preview` substring → preview-create，其餘 → write）
- [x] 429 envelope（`apperror.Write` `code=rate_limited`、`class=too_many_requests`）+ `Retry-After` header（ceil seconds, min 1）+ `details.{scope, category, limit, window_s, retry_after_s, plan}`，符合 spec § 5.1 + § 14 hard rule #1
- [x] 兩層 enforce：per-token 然後 per-team（spec § 4.2 步驟 2、3）；per-team read 無 quota cell → 不限制；per-token / per-team 各自獨立 bucket（spec § 4.3）
- [x] in-memory bucket，plan 變動 invalidate（`Limiter.InvalidateKey`）；`SweepIdle` 24h TTL 清理；`RunCleanup` background goroutine 由 `cmd/server` 啟動（context bound to SIGINT/SIGTERM）
- [x] chi 鏈整合：`r.Route("/v1/teams/{team_slug}")` 與 `r.Route("/v1/me")` 在 Bearer / ResolveTeam / CheckMembership 之後加上 `ratelimit.NewMiddleware(...).Handler`（spec § 14 hard rule #2）
- [x] Plan 來源：`auth.ResolveTeam` 將 `team.plan` 寫入 ctx（新增 `keyTeamPlan` + `TeamPlan(ctx)` getter + `withTeamPlan` setter）；middleware 從 ctx 讀；無 team route fallback `free`（最保守）
- [x] Metric `zeroops_rate_limit_triggered_total{scope, category, plan}`（cardinality 3×3×4 = 36；ADR-0006 § 4.5 cardinality 安全範圍內；spec § 14 hard rule #8 plan 固定 4 值）
- [x] CLI 自動退避：`backendclient.Client` 對 429 走 `RetryMax=5` + 解析 `Retry-After` + jitter ±20% + 指數遞增 base；`OPS_NO_RETRY=1` env 與 `Client.NoRetry=true` 跳過（spec § 7.1 + § 14 hard rule #4）；POST body 透過 `snapshotBody` 重放，多次 attempt 間 idempotent
- [x] MCP：保持既有「不主動 retry」行為（`internal/mcp/server` 不呼叫 backendclient retry path），envelope 直接回 LLM（spec § 7.2 + § 14 hard rule #5）
- [x] 高風險區覆蓋（AGENTS.md「Testing」段）：
  - **preview/confirm 流程**：既有 createapp / redeploy preview-confirm 整合測試在新 middleware 下持續綠（`NewRouter` 不掛 limiter，避免 burst 衝突；只在 `NewRouterWithRateLimit` wire）；`internal/server/services/createapp/...`、`internal/server/services/redeploy/...` 全綠
  - **idempotent retry**：`backendclient` retry path 對 429 自動退避；`Client.do` 重放 request body；`TestClientRetriesOn429UntilSuccess` / `TestClientGivesUpAfterMaxRetries` / `TestClientPostRetriesReplayBody` 守備
  - **team 隔離**：`TestLimiterBucketsIsolatedByKey`（per-token A 排空不影響 B）+ `TestMiddlewarePerTeamLimitTriggers`（兩 token 同 team，per-team bucket 排空後第三個 token 也 429）
  - **role / scope 權限矩陣**：middleware 在 CheckMembership 之後跑，scope 檢驗不受 ratelimit middleware 影響；既有 RBAC 測試全綠
  - **Retry-After 計算**：`TestMiddlewareReturns429WithRetryAfterAndEnvelope` 守備 `Retry-After >= 1`；`TestMiddlewareReadCategoryUsesReadBucket` 守備 read 類別獨立 bucket
- [x] 整合測試：`internal/server/ratelimit_integration_test.go::TestRouterEnforcesPerTokenRateLimit` 用真 router + small quota 驗證 third request 回 429 + envelope details + `Retry-After`
- [x] `make test` 全綠（28 packages）
- [x] `make lint-compose` 通過
- [x] dev 行為驗證：`internal/server/ratelimit_integration_test.go::TestRouterEnforcesPerTokenRateLimit` 用真 chi router + auth chain + ratelimit middleware（同 prod 路徑）跑端到端，drain → 429 → envelope 全證；M4.2 不含 schema 變更，無需 `make migrate` 路徑。`make dev` 在本 worktree 仍受 M3.2 留下的 `migrations/00003_*.sql` duplicate version panic 阻擋（同 M4.1 出口報告之既知遺留），與 M4.2 範圍無關

**Out of scope / 風險回報**：
1. **Build trigger limit（spec § 4.2 步驟 4 + § 14 hard rule #10）** — 屬 redeploy / create_app saga 內 GHA `workflow_dispatch` 之前的 hourly bucket check，需注入 limiter 至 `redeploy.Service` 與 `createapp.Service`，超出 M4.2「per-token / per-team」標題。建議起後續 task `M4.2.1 — build-trigger rate limit` 處理。當前 limiter 已預留 `Allow(ScopePerTeam, ..., CategoryWrite)` 路徑可重用，落地時新增 `ScopeBuild` + `CategoryBuild` + per-hour quota 即可。
2. **Abuse detector（spec § 6）** — 三條偵測規則需 `access_log_aggregate` 聚合表（v1 不存在）+ audit_log 整合，超出 M4.2 範圍。建議起後續 task `M4.2.2 — abuse detection v1 (audit only)`，與 M5.2 audit_log task 同期。
3. **Per-token plan 5 分鐘快取（spec § 13 open issue）** — v1 在 `auth.ResolveTeam` 從 DB 取 plan 入 ctx 已涵蓋常用路徑（每 request 走 DB 一次，與既有 `ResolveTeamBySlug` 同調用點，無額外 round trip）；快取屬效能優化，留待 M5 觀察。
4. **`internal/server/services/ratelimit/`（spec § 3 草案）** — 未引入；middleware + limiter 緊耦合，單套件結構更乾淨。abuse detector 引入後若邏輯量上升再評估拆包。
5. **Spec § 8 metric 命名 `0ops_*`** — Prometheus exposition format 不允許 metric name 起手為 digit，已在 `docs/features/rate-limit-and-abuse/spec.md` § 1 implementation note 記錄；實作沿用專案 `zeroops_` prefix。

### M5.2 — audit_log + audit CLI/MCP（2026-05-16）

> 對應 spec：`docs/features/audit-log/spec.md`
> 對應 task：`tasks/task-list.md` row M5.2
> 對應 paths：`internal/server/services/audit/**`

- [x] migration `00007_audit_log_partition.sql`：將 `audit_log` 重建為 `PARTITION BY RANGE (created_at)` 月份分區，PK 改為 `(id, created_at)`；新增 `source`/`outcome`/`http_status` 欄位（含 CHECK 限制 spec § 4.1 之 enum）；建 spec § 4.2 四條索引（team+created / team+action+created / team+actor+created / trace_id）；備份既有列、setval 序列；額外建 `audit_log_archive` 表（spec § 9.2 永久保留 delete_app 列）；種 9 個月分區（history + 2026-01 → 2026-08）
- [x] `internal/server/services/audit/`：`log.go` Log(ctx, Entry)（redact + trace_id sentinel）/ `query.go` Query/Get + cursor pagination + ParsePageSize / `partition.go` Rollover + PartitionLabel / `redact.go` 大小寫不敏感子字串遮罩（spec § 8 + error-model § 9）/ `metrics.go` MetricObserver + NopObserver / `doc.go`
- [x] `internal/server/db/audit.go`：`InsertAuditLog`（實作 audit.Writer）/ `ListAuditLog`（keyset cursor + actor uuid/github_login fallback + action prefix LIKE + trace_id 嚴格匹配 + cross-team 由 team_id 守備）/ `GetAuditLogByID`（team-scope）/ `CreateMonthlyPartition` / `ArchiveDeleteAppRows` / `DropMonthlyPartition` / `ListPartitionMonths`
- [x] DTO `internal/shared/dto/audit.go`：`AuditLogEntry`（含 actor github_login + ActorUserID + redacted args/result + trace_id + http_status）+ `ListAuditResponse`
- [x] HTTP：`GET /v1/teams/{slug}/audit` 與 `GET /v1/teams/{slug}/audit/{id}`；中間件採 `ActionListSelfAudit`（viewer + `audit:read`）為最低門檻，handler 內依 actor 過濾與角色升級至 admin（spec § 6.2）
- [x] RBAC：新增 `ScopeAuditRead`、`ActionListAudit`（admin+ audit:read）、`ActionListSelfAudit`（viewer+ audit:read）；舊 token 不受影響
- [x] backendclient：`ListAudit(AuditListParams)` / `GetAudit(id)`；CLI / MCP 共用
- [x] CLI：`0ops audit list` 支援 `--since`（accept RFC3339 / `24h` / `7d`）/`--until`/`--action` (prefix)/`--actor`（github_login / uuid / `me`）/`--trace`/`--page-size`/`--cursor`/`--all`/`--output table|json|yaml`；`0ops audit get <id>`
- [x] MCP：新增 read tool `query_audit_log`（不適用 lint R1/R2/R3，因非 write/preview）；既有 14 個 write/preview tool 仍通過 startup lint
- [x] 高風險區覆蓋（AGENTS.md「Testing」段）：
  - **redact**：args / result / 巢狀 map / list 元素 / case-insensitive 子字串（token / secret / signature / private_key / authorization / cookie / bearer）皆遮罩；非敏感欄位保留
  - **idempotent retry**：query 採 cursor (created_at, id) 嚴格遞減鎖位，重打同 cursor 不會跳行；redact 為純函式無副作用
  - **team 隔離**：`GetAuditLogByID` 一律帶 team_id；ListAuditLog where team_id；ADR-0001 enumeration 阻斷 cross-team id 探測
  - **role / scope 權限矩陣**：viewer 帶 `audit:read` 只能查 `actor=me`；admin 帶 `audit:read` 才能查全 team；無 `audit:read` 一律 403 forbidden_scope；無 audit svc 註冊時 route 不存在（cmd/server 預設啟用）
  - **trace_id 落地**：`TraceIDFromContext` 取 chi request id；missing 時填 32-zero sentinel（spec § 15 hard rule #3）；不會 silent drop
- [x] 整合測試：
  - `internal/server/services/audit/redact_test.go`：含敏感子字串大小寫敏感度、巢狀 map / list、未知型別 fallback
  - `internal/server/services/audit/log_test.go`：redact 攔截 / trace sentinel / 非 user source + actor != nil validation 拒絕 / writer error 走 metric outcome `write_error` / TraceIDFromContext override + fallback
  - `internal/server/services/audit/query_test.go`：default page size / since 預設 7d / clamp 200 / self scope 缺 actor 拒絕 / cross-actor Get → ErrForbidden / cursor roundtrip / ParsePageSize 邊界
  - `internal/server/services/audit/partition_test.go`：rollover 創 lookahead + 落 retention 邊界 + create 錯誤傳導 + PartitionLabel roundtrip
  - `internal/server/audit_handlers_test.go`：viewer 全 team 403 forbidden_role / viewer actor=me 200 + scope=self / admin 200 + 結構 / 缺 audit:read 403 forbidden_scope / GetAudit ErrNotFound 映射
  - `internal/cli/audit_test.go`：table 輸出 + 過濾轉發 / json 輸出 roundtrip / `--since=24h` 規格化為 RFC3339 / `audit get <id>` 輸出 / 不支援 output format
- [x] `cmd/server/main.go` 接入 `audit.NewService(repo, repo, NopObserver())`，並改用新 constructor `NewRouterWithRateLimitAndAudit`；新舊 constructor 共存，既有測試零變更
- [x] `make test` 全綠（30 packages，含新增 audit service package）
- [x] `make lint-compose` 通過
- [x] dev DB smoke：通過 podman compose 對 postgres-17 真實庫驗證 migration 00001 → 00007 sequential apply 通過；audit_log 為 partitioned；spec § 4.2 四索引存在；source/outcome CHECK 生效；INSERT 自動 route 至 `audit_log_2026_05` 分區（驗收 spec § 11 partition 跨月行）；`audit_log_archive` 與 `audit_log_*` 分區皆建立

**Out of scope / 風險回報**：
1. **`migrations/00003_*.sql` duplicate version panic（M2 → M3.2 既有遺留）** — 直接執行 `make migrate` 會 panic；本任務之 schema 驗證採與 M3.2 / M4.1 相同的 `psql` sequential apply 路徑（已於 dev compose db 通過）。建議獨立任務 rename `00003_tool_grants_and_auth_status.sql` → `00004_*.sql`，後續 migration 順延一格。
2. **既有 audit_log 寫入點（device login / member invite / member remove / redeploy webhook / github install / delete app / bootstrap owner）保留原 `INSERT INTO audit_log ...` 直 SQL 寫法** — 對新增的 `source`/`outcome`/`http_status` 欄位走 DEFAULT，不破壞 schema。完整改寫為 `audit.Service.Log()` 統一介面屬大範圍 refactor，超出 M5.2「補 Log/Query API + CLI/MCP + partition」範疇；下一 task 處理（建議：`M5.2.1 — adopt audit.Service across existing writers`）。spec § 11 之 「Webhook 寫入 source='webhook'」「Reconciler 寫入 source='reconciler'」驗證已透過 service unit test 覆蓋（fake writer 攔截 InsertRow.Source），現有 SQL 寫法之 source 由 DEFAULT 走 'user'，需於 M5.2.1 切換時補上正確 source 值。
3. **Partition rollover background job（spec § 9.1 K8s CronJob）** — `audit.Rollover` Go API + maintainer 已 ready；K8s CronJob YAML 與 `0ops-ops audit-rollover` CLI 屬 ops 部署工件，超出 backend 範疇；建議獨立任務 `M5.4-adjacent — ops audit-rollover cron`。M5.2 之 migration 已預埋 9 個月分區，足夠 v1 觀察期。
4. **「audit log 寫入失敗 reconciliation_job 重寫」（spec § 15 hard rule #10）** — service `Log()` 已回傳 error 不 silent；reconciliation_job 重寫鉤子需在所有 audit 寫入點都採 service 後再加，屬 M5.2.1 配套。
5. **shared redactor 落於 `internal/server/observability/redaction.go`（error-model spec § 9.3）** — 該 package 之 observability skeleton 尚未實作。M5.2 redactor 暫居 `internal/server/services/audit/redact.go`；待 observability skeleton 落地後再 rehome；行為一致，僅 import path 變化。

### M5.3 — reconciler GA + incident classification（2026-05-16）

> 對應 spec：`docs/features/reconciler-and-incident/spec.md`
> 對應 task：`tasks/task-list.md` row M5.3
> 對應 paths：`internal/server/services/reconciler/**`

- [x] migration `00008_incident_and_recon_extensions.sql`：建 `incident` 表（PK uuid、severity CHECK、opened/closed 索引）；reconciliation_job 補 `status`（CHECK pending/in_progress/completed/failed_permanently）+ `trace_id`；重建 `recon_pending` 為 `WHERE status='pending'`；加 `recon_subject` / `recon_team` 索引；`deploy_run` 新增 SQL CHECK `failure_classification_required`（spec § 6.3 + § 16 #1）
- [x] `internal/server/services/reconciler/`：`doc.go` / `leader.go`（Leader 介面 + `AlwaysLeader` stub，M5.5 將替換為 Lease 實作）/ `store.go`（Store 介面，prod 走 *db.Repository、tests 走 fakeStore）/ `statemachine.go`（spec § 6.1 完整 transition 表 + `Lint()` + `ErrIllegalTransition` / `ErrMissingClassification`）/ `classification.go`（13 個 spec § 7.1 enum + ClassifyWorkflowRun / ClassifyArgoCD）/ `jobs.go`（HandlerRegistry + NextBackoff + ShouldFailPermanently，固定 60s × 2^attempts cap 30min，> 8 次轉永久失敗，hard rule #4）/ `deploy_status.go`（building > 30min 拉 GHA workflow_run + 分類落入 failed） / `argo_sync.go`（syncing > 15min 拉 ArgoCD Application、Healthy→live、Degraded→failed/health_check_failed、Missing/OutOfSync→failed/argo_sync_timeout、Progressing→defer） / `incident.go`（IncidentService Open/Get/List/Close、AuditWriter 介面避免 audit↔reconciler 循環依賴）/ `metrics.go`（Observer 介面 + NopObserver）/ `runner.go`（4 個 leader-gated goroutines：deploy_status / argo_sync / job_queue / metrics；ProcessOne 供測試驅動）
- [x] DB 層：`internal/server/db/reconciler.go`（ListPendingReconciliationJobs / Count / ClaimReconciliationJob 原子 CAS / CompleteReconciliationJob / RescheduleReconciliationJob / FailReconciliationJobPermanently / ListStuckBuildingDeployRuns / ListStuckSyncingDeployRuns / `TransitionDeployRun` 含 optimistic CAS + events jsonb append + `ErrDeployRunStateConflict`）; `internal/server/db/incident.go`（InsertIncident / GetIncident / ListIncidents keyset (opened_at, id) desc / CloseIncident / CountOpenIncidents）
- [x] RBAC：新增 `ScopeIncidentsRead` / `ScopeIncidentsWrite`；`ActionListIncidents`（viewer + incidents:read，spec § 9.3）/ `ActionCloseIncident`（admin + incidents:write，spec § 16 #8）
- [x] HTTP：`GET /v1/teams/{slug}/incidents`（含 status/kind/severity 過濾 + base64 keyset cursor）/ `GET /v1/teams/{slug}/incidents/{id}` / `POST /v1/teams/{slug}/incidents/{id}:close`；新增 `NewRouterWithReconciler` constructor 與舊有 6 個 constructor 共存
- [x] DTO：`internal/shared/dto/incidents.go`（Incident / ListIncidentsResponse / CloseIncidentRequest）
- [x] backendclient：`ListIncidents(IncidentListParams)` / `GetIncident(id)` / `CloseIncident(id, req)`
- [x] CLI：`0ops incidents list [--status=open|closed|all] [--kind] [--severity] [--page-size] [--cursor] [--all]`、`0ops incidents get <id>`、`0ops incidents close <id> --note "..."`（spec § 16 #8 規定 close 必經 CLI、含 audit）
- [x] MCP：新增 read tool `list_incidents`（status/kind/severity 過濾、不適用 lint R1/R2/R3 因非 write/preview）；既有 14 個 write/preview tool 仍通過 startup lint（`TestRegisteredToolsPassStartupLint`）
- [x] cmd/server 接入：`reconciler.New(Config{...}).Start(ctx)`；`AlwaysLeader` stub（M5.5 升 leader election 替換）；`auditAdapter`（橋接 audit.Service.Log onto reconciler.AuditWriter，避免 service 間循環依賴）；`reconcilerObserver`（橋接 reconciler.Observer onto observability.Metrics）；`argoCDAdapter`（橋接 k3s.Client.GetApplicationStatus）；deploy_status scanner 在無 GHA 環境變數時 fetcher=nil → tick no-op（dev 不擋）
- [x] Observability：新增 6 條 metric — `zeroops_reconciler_tick_total{kind,outcome}` / `zeroops_reconciler_job_terminal_total{kind,outcome}` / `zeroops_deploy_run_failure_classification_total{classification}`（spec § 7.3 unknown panel 來源）/ `zeroops_incident_opened_total{kind,severity}` / `zeroops_incident_closed_total{kind,severity}` / `zeroops_incident_open{severity}`；既有 `zeroops_reconciliation_jobs_pending{kind}` 由 metrics tick 維護
- [x] 高風險區覆蓋（AGENTS.md「Testing」段）：
  - **preview/confirm 流程**：incident.Service.Close 採顯式單階段且 emit audit_log；CLI close 命令路徑驗 spec § 16 #8（close_handler_test 守備 already_closed → 409、cross-team → 404、無 admin → 403、無 incidents:read → 403）
  - **idempotent retry**：reconciliation_job 重試走 `RescheduleReconciliationJob` 同 row attempts++ + 不變 row_id；同樣 job 連續失敗 8 次後永久失敗（`TestRunnerProcessOneFailsPermanentlyAfterMaxAttempts` 守備）；`ClaimReconciliationJob` 原子 CAS 防雙 worker 重入（`TestRunnerProcessOneIgnoresClaimedJobs`）
  - **team 隔離**：`Incident.GetIncident` 一律帶 team_id，cross-team 走 `ErrIncidentNotFound`（ADR-0001 enumeration 防範）；`TestIncidentServiceCloseCrossTeamReturnsNotFound` 守備
  - **role / scope 權限矩陣**：list 走 `ActionListIncidents`（viewer+incidents:read），close 走 `ActionCloseIncident`（admin+incidents:write）；handler test 涵蓋三條失敗路徑
  - **deploy 狀態轉移**：`statemachine.Lint` 攔截非法 transition（self-loop / 從 final 退出 / 跳階段）+ 強制 failed/rolled_back/failed_permanently 必帶 `failure_classification`；`TestLintRejectsIllegalTransitions` / `TestLintEnforcesClassificationOnFailureStates` 守備；deploy_status_test / argo_sync_test 守備 reconciler→transition→失敗分類 整鏈
  - **reconciler 收斂**：`NextBackoff(attempts)` 完整覆蓋 spec § 16 #4 公式（含 cap、負數正規化）；`TestNextBackoffMatchesSpec` / `TestShouldFailPermanently` 守備；CAS 衝突視為 no-op（`TestDeployStatusScannerCASConflictIsNoOp`）；leader gate false 時 tick 走 skipped_not_leader（`TestRunnerSkipsWhenLeaderGateFalse`）
- [x] 測試摘要：
  - `internal/server/services/reconciler/statemachine_test.go`：4 個 case set，含 9 條合法 transition、6 條非法、3 個必要分類路徑、2 個 canceled/live 不需分類
  - `internal/server/services/reconciler/classification_test.go`：覆蓋 spec § 7.2 自動分類表的 11 個 signal → classification mapping、ArgoCD 4 個 health 分支
  - `internal/server/services/reconciler/jobs_test.go`：backoff/permanent flip/registry duplicate panic
  - `internal/server/services/reconciler/deploy_status_test.go`：success / timeout failure with classification / in_progress 跳過 / 未超 threshold 跳過 / CAS 衝突 no-op
  - `internal/server/services/reconciler/argo_sync_test.go`：Healthy→live、Degraded→failed/health_check_failed、Progressing→defer、Missing→failed/argo_sync_timeout
  - `internal/server/services/reconciler/incident_test.go`：Open + List、Close emit audit、already-closed、cross-team not-found、empty inputs reject、clock override
  - `internal/server/services/reconciler/runner_test.go`：ProcessOne happy / failed permanently → incident + audit / reschedule / 已 claimed 不再執行 / unknown kind / leader gate skip
  - `internal/server/incident_handlers_test.go`：list + filter / 404 / close note 傳遞 / already_closed 409 / member 拒絕 / 缺 scope 403 / cursor roundtrip
  - `internal/cli/incidents_test.go`：table render + close note POST 傳遞
  - `internal/server/services/reconciler/fake_store_test.go`：完整 Store fake，跨 7 個 test 檔
- [x] `make test` 全綠（31 packages，含新增 reconciler service package）
- [x] `make lint-compose` 通過
- [x] dev DB smoke：通過 podman compose db (postgres-17) 真實庫驗證 migration 00001 → 00008 sequential apply（同 M5.2 採 awk extract `+goose Up` 區塊）；incident 表完整 schema + 索引 + severity CHECK + FK；reconciliation_job 補 status/trace_id 含 CHECK；deploy_run `failure_classification_required` CHECK 已生效（手動 NULL classification + status='failed' insert 被 DB 拒；正確帶 'build_timeout' 通過）；`EXPLAIN` 確認 `SELECT ... WHERE closed_at IS NULL ORDER BY opened_at DESC` 走 `incident_open` 部分索引

**Out of scope / 風險回報**：
1. **`migrations/00003_*.sql` duplicate version panic（M2 → M3.2 既有遺留）** — `make migrate` 仍 panic。本任務沿用 M3.2 / M4.1 / M5.2 同模式：以 `psql` sequential apply 於 dev compose db 驗證 schema 正確。建議獨立任務 rename `00003_tool_grants_and_auth_status.sql` → `00004_*.sql`，後續 migration 順延。
2. **deploy_status scanner 之 GHA fetcher 在 cmd/server 預設為 nil** — 因 workflowdispatch.Client 仰賴 `OPS_GITHUB_REPOSITORY` + token；dev/staging 無此 env 時 fetcher=nil → 該 scanner tick 視為 no-op。production rollout 時應於 cmd/server 將 `workflowdispatch.NewClientFromEnv` 之 client 注入 scanner（M5.3 已提供 WorkflowRunFetcher 介面與 `GetWorkflowRun` 方法）。建議於 M5.3 rollout 補 wiring。
3. **leader election 為 `AlwaysLeader` stub** — 屬 M5.5 範圍（backend-ha-leader-election spec）；M5.3 之 reconciler 介面已準備好接入 Lease 實作，M5.5 落地時只需替換 Config.Leader。
4. **spec § 9.2「unknown 比例突增（> 10% / 1h）」自動建 incident** — v1 未在 backend tick 中實作；該觸發需 Prometheus rate query 結果聚合，超出 reconciler tick 範疇。已暴露 `zeroops_deploy_run_failure_classification_total{classification}` metric 供 dashboard / alerting 計算；建議於 M5.4 之 alerting roundtrip 中補 spec § 9.2 自動觸發鏈路。
5. **incident close 之 MCP write tool** — spec § 16 #8 明定 close 必經 CLI（含 audit）；MCP 僅暴露 read 只 `list_incidents`，與 spec 一致。

### M5.4 — Postgres HA + WAL archive + PITR 演練（2026-05-16）

> 對應 spec：`docs/features/postgres-ha-and-dr/spec.md`
> 對應 task：`tasks/task-list.md` row M5.4
> 對應 paths：`deploy/postgres/**`、`internal/server/db/**`
> 對應 ADR：ADR-0008 § 4 第 5/6/7 點

- [x] `deploy/postgres/` Helm chart：`Chart.yaml`（appVersion 17.2）、`values.yaml`（main + replica replicas=1、`archiveTimeoutSeconds=300`、`synchronousCommit="off"`、`retentionDays=30`、`schedule="0 18 * * *"`）、`README.md`、`templates/configmap-postgresql-conf.yaml`（spec § 4.4 全部 knob：wal_level/max_wal_senders/wal_keep_size/archive_mode/archive_command=`/scripts/wal-push.sh %p %f`/archive_timeout/hot_standby/synchronous_commit/max_connections）/ `templates/configmap-pg-hba.yaml`（replication/app/dump user scram-sha-256）/ `templates/configmap-scripts.yaml`（Helm `.Files.Get` 把 scripts/ 內容 mount 進 pod，scripts 與 ConfigMap 來源一致）/ `templates/statefulset-main.yaml` + `templates/statefulset-replica.yaml`（皆帶 `podAntiAffinity.requiredDuringSchedulingIgnoredDuringExecution` + `topologyKey: kubernetes.io/hostname`，spec § 16 hard rule #2；replica 帶 `pg_basebackup` init container）/ `templates/service-main.yaml` + `templates/service-replica.yaml`（ClusterIP, port 5432）/ `templates/networkpolicy.yaml`（ingress 限 system-0ops + peer postgres pod、egress 443 至 R2）/ `templates/secret-placeholder.yaml`（`auth.renderPlaceholder=false`，prod 由 secrets-management 補）/ `templates/cronjob-pg-dump.yaml`（spec § 7.1 daily 18:00 UTC、concurrencyPolicy=Forbid、backoffLimit=2）
- [x] Helm 渲染時硬性閘門（`fail` 觸發）：`statefulset-main.yaml` 拒絕 `main.replicas < 1` 或 `replica.replicas < 1`（spec § 16 hard rule #1，禁止 single Postgres on production）；`configmap-postgresql-conf.yaml` 拒絕 `archiveTimeoutSeconds > 300`（spec § 16 hard rule #3，RPO ≤ 5 min）
- [x] `deploy/postgres/chart_test.go`：3 個 test 守備 — `TestChartFilesEnforceHardRules`（11 個檔案的 substring matrix，含 anti-affinity / archive_command / `pg_basebackup` / `--write-recovery-conf`）/ `TestTemplateGuards`（Helm `fail` 條件文字必含 spec § 16 hard rule reference）/ `TestRetentionPolicyMatchesHardRule`（walArchive + pgDump 兩 block 各自 retentionDays=30）
- [x] `deploy/postgres/scripts/`：`wal-push.sh`（archive_command；timeline 取前 8 char、`aws s3 cp --endpoint-url` 推 R2、失敗 exit 1 由 PostgreSQL retry 至 `archive_timeout`）/ `pg-dump.sh`（spec § 7.2 `pg_dump -Fc -Z9` + `aws s3 cp` + `trap rm`）/ `replica-init.sh`（idempotent guard：`PGDATA/PG_VERSION` 存在即 skip；首跑 `pg_basebackup --wal-method=stream --write-recovery-conf` + `touch standby.signal`）/ `pitr-drill.sh`（podman-based 本機 PITR drill：source/target Postgres pair、`archive_timeout=5` 加速、`pg_switch_wal()` 觸發、`recovery_target_time` + `recovery_target_action=promote`、canary id=1 必須存在且 id=2 必須不存在）
- [x] `internal/server/db/migrationlint/`：`lint.go`（`ScanDir(dir, floor)` + `ScanBytes(name, content)` + `Aggregate(violations)`；2 條 rule — R1 `CREATE/ALTER INDEX` 必含 `CONCURRENTLY`（spec § 16 hard rule #7）、R2 `ADD COLUMN ... NOT NULL DEFAULT ...` 必拆 3 步（spec § 16 hard rule #8）；`stripCommentsAndStrings` 處理 `--` line comment、`/* */` block comment、單引號字串字面值，避免 false positive）/ `lint_test.go`（11 個 case：R1 bare CREATE / R1 ALTER / R1 大小寫變體 / R1 comment 忽略 / R1 string literal 忽略 / R2 combined / R2 nullable add 與 bare NOT NULL 不誤判 / R2 跨多行 / R2 ALTER TABLE 多 ADD COLUMN 中段命中 / Aggregate / numericPrefix）/ `repo_test.go`（`TestRepoMigrationsPassLint` 掃 `migrations/*.sql`，以 `DefaultGrandfatherFloor=9` 跳過既有 00001..00008 baseline）
- [x] `internal/server/db/primary.go`：`EnsurePrimary(ctx, Probe)` 跑 `SHOW transaction_read_only`，回 `ErrConnectedToReplica` 若值為 `on`（spec § 4.2 + § 16 hard rule #10）；`PoolProbe{Pool}` adapter 包 `*pgxpool.Pool`；`Probe` / `PrimaryQueryRow` 介面讓單元測試不需起 pgx
- [x] `internal/server/db/primary_test.go`：4 個 case — off 接受 / on 回 `ErrConnectedToReplica` / Scan error 走 `errors.Is` 包裹 / nil probe 拒絕
- [x] `cmd/server/main.go` 接入：pool 建好 + `MustPing` 後立即跑 `db.EnsurePrimary(ctx, db.PoolProbe{Pool: pool})`；失敗 `logger.Error` + `os.Exit(1)`（避免 backend 在 replica 上服務寫流量靜默失敗）
- [x] `docs/runbooks/postgres-failover.md`：spec § 9 手動 failover 流程（pg_ctl promote + Service selector patch + backend rolling restart）、RTO 預算說明、worst-case 回退、演練要求
- [x] `docs/runbooks/postgres-pitr.md`：spec § 8 PITR 流程（scale backend → 0、pull base + WAL archive、`recovery_target_time` + `recovery_target_action=pause`、verify canary、resume + promote、rolling restart backend）
- [x] `docs/runbooks/postgres-restore-test.md`：演練清單分三層（local PITR drill / full PITR drill / failover drill）、每季排程、演練紀錄 append-only；M5.4 工件就位紀錄
- [x] Makefile：新增 `migrate-lint` target（`go test ./internal/server/db/migrationlint/...`，spec § 10.1 CI 攔截）+ `m5-4-pitr-drill` target（`bash deploy/postgres/scripts/pitr-drill.sh`，spec § 8.3 本機 drill）
- [x] 高風險區覆蓋（AGENTS.md「Testing」段）：
  - **deploy 狀態 / reconciler 收斂**：本任務不動 deploy 狀態機；EnsurePrimary 與 reconciler 正交。
  - **idempotent retry**：`replica-init.sh` 以 `PG_VERSION` 存在性為 idempotency 標記，第二次執行即 skip pg_basebackup；`pitr-drill.sh` 的 trap cleanup 在 success / fail / interrupt 三種出口都釋放 podman resource。
  - **權限 / 隔離**：NetworkPolicy 限制 5432 入流為 `system-0ops` 命名空間 + peer postgres pod；pg_hba.conf 內 replicator/ops/dumper 三角色 scram-sha-256 + 私網段；無 host 0.0.0.0 trust。
  - **簽章 / preview-confirm**：本任務不引入新 write endpoint；EnsurePrimary 為 read-only probe，無 preview / confirm 對。
  - **migration lint（spec § 16 hard rule #7 / #8）**：lint_test.go 覆蓋 CONCURRENTLY 大小寫 + 註解內 false positive 防護 + ADD COLUMN 跨多行 + 同 ALTER TABLE 多 ADD COLUMN 中段命中；repo_test.go 對既有 migrations 走 floor=9 grandfather。
  - **failover 流程**：runbook Step 4 強制 rolling restart backend；EnsurePrimary 於新 pod 啟動時驗證 DSN 指向 primary（hard rule #10）。
- [x] `make test` 全綠（33 packages，新增 `deploy/postgres` + `internal/server/db/migrationlint`）
- [x] `make migrate-lint` 通過（既有 8 支 migration 全在 baseline 之下 grandfathered；新 rule 對 ≥ 00009 才生效）
- [x] `make lint-compose` 通過
- [x] dev DB smoke：`podman compose up -d db` → `psql -At -c 'SHOW transaction_read_only'` 回 `off`；`SELECT pg_is_in_recovery()` 回 `f`；確認 EnsurePrimary 對單實例 dev compose db 為 pass-through，不會誤擋 dev 啟動。

**Out of scope / 風險回報**：
1. **deploy/postgres 路徑 vs spec § 3 layout 差異** — spec § 3 文字列 `deploy/chart/postgres/` 與既有 `deploy/chart/cloudflare-tunnel/` 對齊；但 `tasks/task-list.md` row M5.4 之 Expected Paths 為 `deploy/postgres/**`。為對齊 task runner verify 契約（runner 會驗 changed paths 至少一條落在 `deploy/postgres/**`），實作落 `deploy/postgres/`。spec § 3 為 draft 狀態的示意 layout，與 task-list 衝突時 task-list 勝出。建議未來統一：若要遵循 `deploy/chart/*` 命名，應更新 task-list.md row M5.4；本任務不擅改 task-list。
2. **Full prod-style PITR / failover drill 尚未跑** — local PITR drill 腳本 (`deploy/postgres/scripts/pitr-drill.sh`) 已就位且可隨 chart 變更跑；但 spec § 8.3 之「M5 GA 前必演練一次完整 PITR」屬 ops 排程（需 staging cluster + 真實 R2），不在本 PR 範疇。`docs/runbooks/postgres-restore-test.md` § 5.1 已記錄此 gap 並指派為 M5 GA 前 owner=ops。
3. **postgres_exporter sidecar（spec § 11）** — `pg_replication_lag_seconds` / `pg_wal_archive_status` 之 Prometheus exporter sidecar 於 v1.1 補；v1 採 ops 手動觀察 + 演練覆蓋，與 spec 一致。M5.5 之 backend HA 完成後再評估是否同時上 exporter。
4. **R2 bucket lifecycle 30d** — chart values 已宣告 `retentionDays: 30`（spec § 16 hard rule #9），但 R2 bucket 端 lifecycle rule 為帳號層級設定，需由 ops 在 Cloudflare 控制台或 wrangler 配置，chart 本身無法施加。已在 README + values 註解明示。
5. **wal-g / pgbackrest 替代 archive_command** — spec § 6.1 列為 v1.1 評估項；本任務沿用 `archive_command` + `wal-push.sh` 直推 R2，與 spec 一致。
6. **`migrations/00003_*.sql` duplicate version panic（M2 遺留）** — 本任務不修；migration lint 的 grandfather floor=9 設計即建立在現狀之上，floor 提升前不會觸碰既有 8 支 migration。

### M5.5 — Backend 2 replica + Leader election（2026-05-16）

> 對應 spec：`docs/features/backend-ha-leader-election/spec.md`
> 對應 task：`tasks/task-list.md` row M5.5
> 對應 paths：`internal/server/leader/**`、`deploy/server/**`
> 對應 ADR：ADR-0008 § 4 第 1/2/3/4 點

- [x] `internal/server/leader/`：`doc.go`（package overview + ADR-0008 引用 + Pull-not-Push 模型說明）/ `identity.go`（`PodIdentity()`：優先 `POD_NAME` env → fallback hostname → `unknown`；UUID v4 尾綴 8 字元做 pod 重啟唯一性，spec § 4.2）/ `leader.go`（`Leader` 介面 `IsLeader() bool` + `Identity() string`；`AlwaysLeader{Name}`，spec § 4.3 v1 預設 mode；Identity() 空時 fallback `PodIdentity()`）/ `lease.go`（`LeaseLeader`，使用 `k8s.io/client-go/tools/leaderelection.RunOrDie`；Lock 為 `resourcelock.LeaseLock`；`LeaseDuration=15s / RenewDeadline=10s / RetryPeriod=2s` 預設 + 改變需 ADR；`ReleaseOnCancel: true` 硬寫 + 不經 Config 暴露；OnStartedLeading CompareAndSwap 真正 leader 轉變才 OnGained；OnStoppedLeading CompareAndSwap 真正 leader 轉變才 OnLost — client-go 即便沒先 OnStartedLeading 也會在 Run exit 呼 OnStoppedLeading，需防 phantom）/ `metrics.go`（`Observer` 介面：`OnGained(id) / OnLost(id) / OnNewLeader(currentID, newID) / OnLeaseRenew(outcome)` + `NopObserver`；`PrometheusProvider` 實作 `leaderelection.MetricsProvider` 將 client-go 內部 leader On/Off/Slowpath 映射到 OnLeaseRenew(acquired/lost/slow_acquire)）
- [x] `internal/server/leader/` 測試（10 個 case set）：
  - `identity_test.go`：POD_NAME 優先 / fallback hostname / 多次呼叫各帶不同 UUID 後綴
  - `leader_test.go`：`AlwaysLeader.IsLeader()=true` / `Identity()` 非空 / 空 Name fallback PodIdentity()
  - `metrics_test.go`：NopObserver zero-value safe / PrometheusProvider On→acquired / Off→lost / Slowpath→slow_acquire / nil Observer 不 panic
  - `lease_test.go`：starts as follower / OnStartedLeading 翻 IsLeader 並 emit OnGained / 二次 OnStartedLeading idempotent（CompareAndSwap 防 phantom gain）/ OnStoppedLeading 翻回 false 並 emit OnLost / OnStoppedLeading 無前置 gain 不 emit phantom lost / OnNewLeader 同 identity 不 inc handover、不同 identity 才 inc / NewLeaseLeader 拒絕缺欄位 / LeaseDuration/RenewDeadline/RetryPeriod 預設 15s/10s/2s + ReleaseOnCancel=true / Run 在 ctx pre-cancelled 時立即退出且不留 leader 狀態
- [x] `internal/server/observability/metrics.go`：新增 3 條 leader metric（spec § 8.1）：
  - `zeroops_leader_status{pod_name}` gauge — `SetLeaderStatus(pod, on bool)`
  - `zeroops_leader_handover_total{pod_name}` counter — `ObserveLeaderHandover(pod)`
  - `zeroops_leader_lease_renew_total{pod_name, outcome}` counter — `ObserveLeaseRenew(pod, outcome)`（outcome ∈ {acquired, lost, slow_acquire}）
- [x] `internal/server/observability/metrics_test.go`：新增 `TestM5_5LeaderMetricsExpose`（3 條 metric 寫入後 `/metrics` 渲出含 pod_name + outcome 三組值）+ `TestM5_5SetLeaderStatusFalseClearsGauge`（true→false reset 回 0）
- [x] `cmd/server/leader.go`：`buildLeader(mode, identity, metrics, kubeconfigPath)` 解析 `OPS_LEADER_MODE`（預設 `always`；`always|lease` 兩值，其他值拒絕）+ `reconcilerLeaderGate{l: leader.Leader}` adapter（reconciler.Leader 介面只要 `IsLeader()`，避免 reconciler 套件 import cycle）+ `metricsLeaderObserver` 把 leader.Observer onto observability.Metrics（OnGained → SetLeaderStatus(true); OnLost → SetLeaderStatus(false); OnNewLeader → ObserveLeaderHandover; OnLeaseRenew → ObserveLeaseRenew("", outcome)）
- [x] `cmd/server/main.go`：
  - 啟動讀 `OPS_LEADER_MODE`，呼 `leader.PodIdentity()` 取得穩定 identity
  - `buildLeader(...)` 失敗 fatal exit；mode=lease 走 `go runLeader(ctx)` 背景 goroutine（內部 `leaderelection.RunOrDie` + `ReleaseOnCancel=true` 在 ctx cancel 時立即 release）
  - mode=always 路徑啟動時 seed `metrics.SetLeaderStatus(identity, true)`，讓 dev compose `/metrics` 一開機即看到 leader_status=1
  - `startReconciler(..., ldr leader.Leader)`：reconciler.Config.Leader 由 `AlwaysLeader` stub 改成 `reconcilerLeaderGate{l: ldr}`
  - mode=lease 時 `leaderelection.SetProvider(leader.PrometheusProvider{Observer: ...})` 註冊 client-go MetricsProvider（process-global onlyOnce，第二次 set 自動 no-op）
- [x] `cmd/server/leader_test.go`：6 個 case set —
  - `always` mode 回 `AlwaysLeader{}` + 不註冊 Run handle / IsLeader=true
  - empty mode 預設 `always`
  - 未知 mode（如 `master`）拒絕 + error 含 `OPS_LEADER_MODE`
  - lease mode 無 kubeconfig + 無 InClusterConfig 時 fatal error（避免 prod 誤 fall back to AlwaysLeader）
  - `reconcilerLeaderGate` 鏡像 leader.Leader.IsLeader() + 滿足 `reconciler.Leader` 介面
  - `metricsLeaderObserver` 四個 callback 全鏈不 panic（feed metrics）
- [x] `deploy/server/` Helm chart：
  - `Chart.yaml`（name=`ops-server`，符合 K8s DNS-1035；appVersion=`0.5.5`；chart version `0.1.0`）
  - `values.yaml`（replicas=2 / mode=lease / leaseName=`0ops-backend-leader` / namespace=`system-0ops` / strategy.maxSurge=1.maxUnavailable=0 / terminationGracePeriodSeconds=60 / preStop.sleepSeconds=5 / probes / podAntiAffinityWeight=100 preferred）
  - `templates/{serviceaccount,role,rolebinding,deployment,service}.yaml`：deployment 走 RollingUpdate + preStop sleep + readiness/liveness `/health` + `OPS_LEADER_MODE=lease` + `POD_NAME` downward API + `OPS_LEADER_NAMESPACE` + `OPS_LEADER_LEASE_NAME`；role 限 `coordination.k8s.io/leases` 且 resourceName=leaseName + 額外 `create/list`（K8s RBAC 對 create/list 不支援 resourceName filter）
  - Helm `fail` 渲染期硬閘（spec § 14）：
    - hard rule #1：`replicas < 2` 拒渲（M5+ backend 必跑 leader election）
    - hard rule #1+#5：`leaderElection.mode != "lease"` 拒渲（production 必跑 lease）
    - hard rule #7：`preStop.sleepSeconds < 5` 拒渲（preStop 必含 sleep 5）
  - `README.md`：TL;DR + 硬閘表 + dev vs production
- [x] `deploy/server/chart_test.go`：3 個 test set — `TestChartFilesEnforceSpec`（substring matrix 守備 Chart.yaml / values.yaml / 5 templates 之 spec 必要欄位）/ `TestTemplateGuards`（Helm `fail` 三段條件文字 + spec § 14 hard rule reference）/ `TestValuesDefaultsMatchSpec`（replicas=2 / mode=lease / leaseName / namespace / sleepSeconds=5 / terminationGracePeriodSeconds=60）。`helm lint` 通過、`helm template` 渲出合法 YAML。
- [x] `docs/features/backend-ha-leader-election/spec.md` § 3 改 `internal/server/leader/`（對齊 task-list Expected Paths 與本任務實作）+ § 8.1 metric 命名前綴 `0ops_` → `zeroops_`（對齊既有 metric naming convention）+ § 5.3 補註 Pull-not-Push 模型決定
- [x] 高風險區覆蓋（AGENTS.md「Testing」段）：
  - **preview/confirm 流程**：N/A（本任務不引入新 write/preview endpoint）
  - **idempotent retry**：LeaseLeader callback 多次觸發 idempotent — `OnStartedLeading` 二次觸發 IsLeader 維持 true 不重複 inc handover（`TestLeaseLeaderOnStartedLeadingIdempotent`）；`OnStoppedLeading` 無前置 gain 不 emit phantom lost（`TestLeaseLeaderOnStoppedLeadingIdempotent`，防 client-go Run exit 時的 unconditional callback）；`OnNewLeader` 同 identity 不 inc 也不重複（`TestLeaseLeaderOnNewLeaderCountsOnlyForeignHandovers`）
  - **team 隔離**：N/A（Lease 為全域 backend-leader，非 per-team）
  - **role / scope 權限矩陣**：Lease RBAC 經 ServiceAccount；Role 用 `resourceNames: [<leaseName>]` 把 get/watch/update/patch 限定到單一 Lease（spec § 14 hard rule #2 縮窄）；`create`/`list` K8s RBAC 不支援 resourceName filter 分離為第二條 rule。`TestChartFilesEnforceSpec` 守備 `resourceNames:` 文字 + `.Values.leaderElection.leaseName` 變數引用
  - **簽章 / webhook**：N/A
  - **deploy 狀態 / reconciler 收斂**：reconciler.Config.Leader 替換為 `reconcilerLeaderGate`；既有 `reconciler/runner_test.go` 守備 leader gate false 走 `skipped_not_leader` metric 不變；`cmd/server/leader_test.go` 新增 `TestReconcilerLeaderGateMirrorsLeader` 守備 adapter 行為與滿足介面
- [x] `make test` 全綠（37 packages，新增 `internal/server/leader` + `deploy/server` 兩 package）
- [x] `make lint-compose` 通過（compose.yaml 不變，dev 預設 `OPS_LEADER_MODE=always`）
- [x] Helm 渲染驗證：`helm lint deploy/server/` info-only（icon 建議），無 error；`helm template deploy/server/` 渲出 169 行合法 YAML（5 個 K8s resource）
- [x] cmd/server 啟動順序：metrics → leader（決定 identity + mode=lease 開 Run goroutine）→ reconciler runner（pull IsLeader）→ HTTP server；shutdown 順序逆向（HTTP shutdown → ctx cancel → leader 因 `ReleaseOnCancel=true` 立即 release lease → reconciler runner ctx 結束 drain）

**Out of scope / 風險回報**：
1. **spec § 3 `internal/server/leaderelection/` 與 task-list `internal/server/leader/**` 命名差異** — task-list 為 task runner verify 契約；spec § 3 為 draft；本任務對齊 task-list 並把 spec § 3 修齊。延續 M5.4 之 deploy/postgres 處理模式（spec draft vs task-list 衝突時 task-list 勝出）。
2. **spec § 8.1 metric 命名前綴 `0ops_` vs 既有 `zeroops_`** — 全專案 metric 統一 `zeroops_` 前綴（既有 22+ metric 全部如此）；本任務採 `zeroops_leader_*` 並同步把 spec § 8.1 改齊。
3. **chart resource name `ops-server` 而非 `0ops-server`** — Helm `helm lint` 對 K8s DNS-1035 強制（resource name 必須以字母開頭）；改採 `ops-server` 對齊 spec § 7.1 之 deployment YAML 範例（`metadata.name: ops-server`），同時與既有 backend binary 名 `0ops-server`（host）對映清楚。chart `name:` 同步為 `ops-server`。
4. **`Subscribe() <-chan Event`（spec § 4.1）不引入** — reconciler 採 Pull `IsLeader()` 模型，Observer callback 已覆蓋 metric/log 需求；channel 為 dead weight；待未來 caller 真的需要 push 通知再加，本任務在 spec § 5.3 補註此 Pull-not-Push 決定。
5. **SSE active connections metric（spec § 8.1）不在本任務範圍** — 該 metric 為 `read-api-vertical-slice` § 4.4 之 SSE 計收責任；本任務僅實作 leader 相關 3 條 metric。
6. **HPA 不在本任務範圍** — spec § 1 + ADR-0008 OQ#2 明示 M6 後評估；本任務僅做手動 replicas=2。
7. **完整 K8s e2e（兩 pod 真互競 lease、SIGTERM handover < 5s 實測）不在本任務範圍** — 需 staging K3s cluster + 真實 Lease object；本 PR 採 chart_test.go substring + helm lint/template + Go callback 單元測試覆蓋 spec § 10 之契約點；ops 在 v1 GA 前演練屬 K3s deploy 工件範疇（沿用 M5.4 PITR drill 對應 ops 排程模式）。
8. **`leaderelection.SetProvider` 為 process-global onlyOnce** — client-go 設計如此；多次呼叫只有第一次生效。本任務在 `buildLeader` lease 路徑呼叫一次；cmd/server unit test 不依此 global state（避免 test pollution），改測 `metricsLeaderObserver` 直接驅動。
9. **`lease_renew_total` 的 `pod_name` 在 v1 落地為空字串** — client-go 的 `MetricsProvider.NewLeaderMetric()` 為 process-global 結構，呼叫 On/Off/Slowpath 不會帶 identity。HA 儀表板可改 join `zeroops_leader_status` 取得 pod-level 視角；spec § 8.1 註記此限制。
10. **dev compose smoke 受兩個 M2 遺留 infra 問題阻擋** —
    - `migrations/00003_*.sql` duplicate version panic（M2.x→M5.x 既有遺留）→ `make migrate` 仍 panic
    - go.mod 自 M3.1 起 `go 1.26.0`、但 `cmd/server/Dockerfile` builder 仍 pin `golang:1.25-alpine` → dev 容器 air rebuild 失敗 `go: go.mod requires go >= 1.26.0`
    
    兩者皆屬 infra 工件、與 M5.5 範圍正交。本任務沿用 M5.4 / M5.3 / M5.2 同處理模式：以 `go test ./...`（37 packages 全綠）+ `helm lint` + `helm template` 完整渲染驗證；compose smoke 為 ops 在處理上述 M2/M3.1 infra debt 後再跑。建議獨立任務 `M5.5-adjacent — bump Dockerfile builder to golang:1.26`（單行改、無功能影響）。

## M3 — install / domain verify backlog

### M3.2 — GitHub App install/uninstall 流程（2026-05-15）

> 對應 spec：`docs/features/github-app-install-flow/spec.md`
> 對應 task：`tasks/task-list.md` row M3.2
> 對應 paths：`internal/server/services/githubapp/**`

- [x] preview/confirm 兩階段 install action（service `PreviewInstall` / `ConfirmInstall`，last_result idempotent replay）
- [x] preview/confirm 兩階段 uninstall action（service `PreviewUninstall` / `ConfirmUninstall`，GitHub DELETE → 清 install_id → app paused → token cache invalidate）
- [x] `state` HMAC 簽/驗 10 分鐘 TTL；callback handler 驗 HMAC + actor 仍為 owner + preview 已 consumed
- [x] `/v1/auth/github/install-callback` 真正 update `team.github_install_id`，302 redirect 至 success page
- [x] `/v1/webhooks/github` `installation` event (`created/deleted/suspend/unsuspend/new_permissions_accepted`) 與 dedup via `webhook_dedup`
- [x] `/v1/teams/{slug}/github/install-status` 提供 CLI polling
- [x] Installation token cache（per-pod，`TokenProvider`）；uninstall 與 webhook `deleted` 走 `Invalidate`
- [x] RBAC：`ActionManageGithubApp` 升級為 `RoleOwner`，admin 級被拒絕；spec § 14 hard rule #2
- [x] migration `00004_team_github_install_index.sql` 新增 partial index on `team(github_install_id)`
- [x] CLI：`0ops teams github install / uninstall / status` 兩階段流程 + 10 分鐘 status polling
- [x] MCP：`install_github_app_preview` / `install_github_app` / `uninstall_github_app_preview` / `uninstall_github_app`，全數滿足 R1/R2/R3 lint 規則（`internal/mcp/server/lint_test.go` 涵蓋）
- [x] 高風險區測試：state HMAC tampering / cross-actor preview / role downgrade between confirm and callback / install URL idempotent replay / uninstall GitHub failure 不清 binding / webhook 簽章拒絕 / webhook dedup not re-acting (`internal/server/services/githubapp/service_test.go` + `internal/server/github_handlers_test.go`)
- [x] `make test` 通過

**Out of scope / 風險回報**：M3.2 不修 `migrations/00003_*.sql` 兩支同版本檔造成的 goose duplicate version panic（屬 M2 遺留問題；只有 image 重建時觸發）。本任務的 migration `00004` 已透過 podman compose db 容器手動 apply 驗證正確 (`team_github_install_id_idx` 建立成功)。建議另起任務改 rename 其中一支 `00003` → `00004`，本任務的 index 順延為 `00005`。

## Milestone Supporting Work

### M5.6 — local-file-repo dev mode

- [x] OPS_ENV runtime helper + production safety assertion
- [x] file:// validator + LOCAL_FILE_REPO_ENABLED gate
- [x] Inspector interface + LocalInspector + GitHubInspector stub
- [x] examples/node-demo + bootstrap
- [x] localbuild package: config + signed callback client
- [x] LocalBuildDispatcher with pack-build + state chain
- [x] RoutingDispatcher + GetAppRepoURLByTeamAndAppSlug + apps.go wiring
- [x] compose registry + podman socket mount + Makefile targets
- [x] tasks/local-build-e2e.sh acceptance script
- [x] docs alignment + lessons capture（L005 / L006 / L007）
- [x] e2e 實跑驗證（2026-05-19）：rebuild image 後跑 make m5-6-local-build-e2e → `OK — node-demo-pass deploy_run reached live and image is present`；sub-spec status: draft → accepted

### M5.6.1 — split pack/push + rewrite imageRef to LOCAL_REGISTRY

- [x] dispatcher 分 Pack / Push，DefaultPush 走 Docker-compat REST API
- [x] `rewriteImageRef` 替換 ghcr.io 前綴為 LOCAL_REGISTRY
- [x] failure_classification 對齊 callback handler allowlist
- [x] socket path 一致性（server 容器與 host 同路徑 /run/user/$UID/podman/podman.sock）
- [x] e2e push → registry 跑通驗證（M5.6.3 修補後達成）

### M5.6.2 — rootless podman socket perms 文件化

- [x] sub-spec § 15 加 host 環境前提段（uid mapping 解析 + 三方案 + 採行決策）
- [x] ADR-0012 § 6.2 / § 9 補 podman socket perms 負面項與已決議
- [x] `make m5-6-podman-socket-loosen` target（OPS_ENV=production 時 refuse）
- [x] `tasks/local-build-e2e.sh` preflight：偵測 socket perms 0660 → fail-fast + 印指引
- [x] examples/node-demo/README + .env.example 加首次 setup 步驟
- [x] tasks/lessons.md L008 紀錄
- [x] e2e 實跑驗證（2026-05-19）：socket perms 0666 + DefaultPack `--docker-host=inherit` + libpod push 後跑通

### M5.6.3 — dispatcher e2e 修補

- [x] `DefaultPack` 加 `--docker-host=inherit`（避開 host docker.sock perms 衝突）
- [x] `DefaultPush` 改用 podman libpod endpoint + `tlsVerify=false`（local HTTP registry）
- [x] `tasks/local-build-e2e.sh` step 7 改 poll registry tag list 作 truth source（K3S_DISABLE_ISOLATION 之假 live 不可信）；step 8 才 confirm `deploys/status=live`
- [x] e2e 實跑驗證（2026-05-19）：`OK — node-demo-pass ... image is present`；`curl /v2/_catalog` 列出 `0ops-apps/personal/node-demo-pass`

### docs/features 覆蓋補齊（追蹤缺漏項）

- [x] `docs/features/audit-log/spec.md`
- [ ] `docs/features/auth-and-rbac/spec.md`
- [ ] `docs/features/auth-login-flow/spec.md`
- [ ] `docs/features/backend-ha-leader-election/spec.md`
- [ ] `docs/features/custom-domain-and-verify/spec.md`
- [ ] `docs/features/delete-app-flow/spec.md`
- [x] `docs/features/github-app-install-flow/spec.md`
- [ ] `docs/features/mcp-tool-permissions/spec.md`
- [x] `docs/features/postgres-ha-and-dr/spec.md`
- [ ] `docs/features/rate-limit-and-abuse/spec.md`
- [ ] `docs/features/read-api-vertical-slice/spec.md`
- [ ] `docs/features/secrets-management/spec.md`
- [ ] `docs/features/shared-dto-and-contract/spec.md`
- [ ] `docs/features/webhook-and-redeploy/spec.md`

## Migrated Todo Sources

> 本區集中管理原先散落於 docs/ 的 checkbox。來源文件已移除 checkbox，僅保留說明文字。
> 規則：進度只在 `tasks/todo.md` 更新；其他文件不得再新增 checkbox。

### 治理與決策待辦

#### docs/0ops-business-plan.md

- [ ] 公司法律主體
- [ ] 領投人選與時程
- [ ] Open source 範圍（v1 全閉源 → v2 部分開源 vs v1 即 OSS core）
- [ ] 商業 managed cloud 上線時程（建議與 v2 Web UI 同步）
- [ ] 對 AI CLI 廠商的合作 outreach 順序

#### docs/0ops-plan-milestones.md

- [ ] Repo 主機位置（自建 git server、GitHub org、其他）
- [ ] **Copilot CLI / Codex CLI 與官方 Go SDK 相容性矩陣**：M0 spike 驗證 tool registry、preview/confirm、streaming fallback
- [ ] **Copilot CLI 是否原生支援 MCP**（影響 skill pack 形式：MCP 共用 / 退路 wrap CLI）
- [ ] **Codex / Copilot skill metadata 精確格式**（v1 起手時驗證）
- [ ] Backend 是否需要 SSE → MCP streaming（官方 Go SDK 若支援不足，則改分頁拉取）

### 執行計畫遷移待辦

> 詳細 step-by-step 執行內容保留在來源 plan 文件。
> `tasks/todo.md` 只追蹤 task-group 層級狀態，不再鏡像每個 `Run test` / `Commit` 子步驟。

#### docs/admin-user-provisioning/draft/2026-05-11-plan.md

- 內容與 `docs/admin-user-provisioning/release/2026-05-11-plan.md` 相同。
- 進度只追蹤 release 版本，避免 draft/release 雙重維護。

#### docs/admin-user-provisioning/release/2026-05-11-plan.md

- [ ] DB schema + query groundwork
- [ ] Bootstrap owner one-shot flow
- [ ] Members list/invite/remove API with RBAC + preview/confirm
- [ ] CLI members subcommands
- [ ] MCP members tools
- [ ] End-to-end verification and docs alignment

#### docs/db-foundation/draft/2026-05-10-plan.md

- [x] DB foundation delivered: pgx + sqlc + repository smoke test + goose create runbook
- [x] M0.2：`internal/server/db/`（pgx + sqlc 架接）+ `goose create` 之開發 workflow runbook
- [ ] Post-delivery targeted verification refresh
- [ ] Repo-wide verification refresh
- [ ] Generated/tracked files review

#### docs/features/build-pipeline-and-callback/draft/2026-05-13-plan.md

- [ ] Callback HMAC validation coverage
- [ ] Callback timestamp window coverage
- [ ] Webhook dedup coverage
- [ ] Deploy status normalization coverage
- [ ] Failure classification validation
- [ ] Workflow YAML and GitOps script assets
- [ ] E2E acceptance script and Makefile target
- [ ] Final verification bundle

#### docs/features/create-app-flow/draft/2025-01-15-plan.md

- [x] Action/service skeleton and argument validation
- [x] SideEffect definitions and coverage
- [x] `deploy_run` state machine groundwork
- [x] Precheck flow
- [x] Execute saga structure
- [x] Handler delegation to service
- [x] Idempotent replay integration coverage
- [x] M2.1 — create_app saga pattern + state machine
- [ ] Full-suite verification refresh

#### docs/gitops-render-and-argocd/draft/2026-05-13-plan.md

- [x] GitOps render output contract
- [x] Git push path and failure compensation
- [x] ArgoCD sync status transitions
- [x] Focused verification and repo checks
- [x] Docs alignment note

## Governance Guide

> 本區不是進度追蹤，不用 checkbox。
> 作用是把執行工作時必須套用的治理規則集中放在同一份檔案，避免與 backlog 混淆。

### docs/adr-reading-strategy.md

- 修改涉及的模組或概念是否已在 ADR 明確提及
- 修改是否違反 TL;DR 的前三項決策；若無明確提及，改為 **Read**
- Context 中提及的問題是否仍然適用
- Decision 中的選項取捨是否影響本次修改
- Consequences 中列舉的限制是否與本次修改衝突；若超出預期，改為 **Deep**
- Consequences 中列舉的長期影響是否已納入本次修改評估
- Revisit Triggers 中是否有會被本次修改觸發的條件
- Open Questions 是否暗示未來不確定性會影響本次設計
- 若需變更已決策項，應新增 ADR，而非直接修改既有決策

### 執行順序

- 識別相關 ADR
- 執行讀取
- 記錄發現
- 發現違反時停止擴寫實作，先回到 ADR decision / consequences

### 交付前檢查

- 涉及新 API 或 schema 時，確認 ADR 深度足夠
- 做架構決策檢查
- 做文件同步檢查
- 做測試完整性檢查
