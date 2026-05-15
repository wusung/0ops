# tasks/todo.md

> 本檔為專案待辦與進度的單一事實來源。
> 所有進度更新只在此檔案維護；其他文件可描述規格、流程、決策與計畫，但不得再承載 checkbox 狀態。

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

- [ ] **M2.8 端到端驗收腳本**
  - 建立一條可重複驗收流程：preview → confirm → dispatch → callback → sync → public URL 200
  - 驗收至少包含：
    - CLI 互動式
    - CLI `--yes`
    - MCP `create_app_preview` → `create_app`
    - `nextdemo.winshare.tw` 真 200
  - 這一條通過前，不得標示 M2 完成

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
- [ ] CLI 與 MCP 都各跑過一次端到端驗收

## Milestone Supporting Work

### docs/features 覆蓋補齊（追蹤缺漏項）

- [ ] `docs/features/audit-log/spec.md`
- [ ] `docs/features/auth-and-rbac/spec.md`
- [ ] `docs/features/auth-login-flow/spec.md`
- [ ] `docs/features/backend-ha-leader-election/spec.md`
- [ ] `docs/features/custom-domain-and-verify/spec.md`
- [ ] `docs/features/delete-app-flow/spec.md`
- [ ] `docs/features/github-app-install-flow/spec.md`
- [ ] `docs/features/mcp-tool-permissions/spec.md`
- [ ] `docs/features/postgres-ha-and-dr/spec.md`
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
