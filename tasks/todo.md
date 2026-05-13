# M0.1 — dev-environment scaffold

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

# M2 — gap remediation backlog

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

- [ ] **M2.4 K3s namespace isolation 最小可用版**
  - 把 `internal/server/services/k3s/client.go` 從 no-op 改為真 client
  - 落地：
    - `EnsureNamespace`
    - `EnsureResourceQuota`
    - `EnsureLimitRange`
    - `EnsureNetworkPolicy`
    - `PatchNamespacePSA`
    - `PatchGHCRImagePullSecret`
  - 對齊 `docs/features/k3s-namespace-isolation/spec.md`
  - 驗收證據：
    - `team-<slug>` namespace 存在
    - ResourceQuota / LimitRange / NetworkPolicy / PSA label 皆可 `kubectl get` 驗證

- [ ] **M2.5 winshare 子網域真實路由**
  - 補 Cloudflare / tunnel route 整合，不可只回傳字串 URL
  - 對齊 `docs/features/winshare-subdomain-and-tunnel/spec.md`
  - 驗收證據：
    - `nextdemo.winshare.tw` 外部 HTTP 200
    - route 建立失敗時有明確錯誤分類與 rollback/收斂策略

- [ ] **M2.6 Observability GA**
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

- [ ] **M2.7 MCP preview/confirm description lint 契約**
  - 對 `create_app_preview` / `create_app` 補 `ALWAYS` / `NEVER` 必備句式
  - 補 server startup lint，違反時啟動失敗
  - 對齊 `docs/features/mcp-tool-description-lint/spec.md`
  - 驗收證據：
    - 啟動時 lint pass
    - 故意放錯 description 時測試紅燈

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
- [ ] Prometheus metrics 含 preview/deploy/cf 指標
- [ ] Grafana dashboard + burn-rate alert 可用
- [ ] MCP `create_app_preview` / `create_app` description lint 合規
- [ ] CLI 與 MCP 都各跑過一次端到端驗收
