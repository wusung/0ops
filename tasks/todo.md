# tasks/todo.md

> 進度單一事實來源。歷史已完成項移至 `tasks/todo-archive.md`（凍結 2026-05-21）。
> 規則：進度只在本檔更新；docs/ 不得再新增 checkbox。

## 當前狀態（2026-05-21）

v1 範圍（M0-M6）全部 ship。M7 (Web UI) 為 post-v1，不阻擋 v1 上線。

| Milestone | 狀態 | 摘要 |
|---|---|---|
| M0-M5.6 | Done | 詳見 `tasks/task-status.md` + `tasks/todo-archive.md` |
| M6 app-source-ingestion | Done 2026-05-21 | PR #62-#84 + hotfix #85/#86 + sync #87；e2e 9 步全 PASS |
| M7 Web UI (post-v1) | Pending | Vue 3 + Vite + Tailwind + shadcn-vue；不在 v1 範圍 |

## 活躍 backlog

### M6 follow-up（來源：`docs/features/app-source-ingestion/spec.md` § 16-17）

- [ ] **Q1 — production CI workflow 驗證**（waiting on user-side resources）
  - 目標：`deploy/workflows/deploy-app-from-upload.yml` 對 self-hosted runner 在 production GHA 跑通；JWT fetch 路徑端到端驗
  - 工程封裝完成 2026-06-06：`docs/features/self-hosted-runner/spec.md` + workflow opt-in
    (`runs-on: ${{ vars.GHA_RUNNER_LABEL || 'ubuntu-latest' }}`) + `deploy/runner/`（install-runner.sh、
    status.sh、systemd unit template、values.yaml）+ `manage.sh prod-{install-runner,runner-status,
    runner-validate}` + `tasks/m6-q1-runner-validate.sh` + `docs/runbooks/gha-self-hosted-runner.md`
  - 剩下動工：user 端
    1. `prod-up` 完成、`api.<domain>/health` 200（已工程封裝；user 端外部資源到位後）
    2. `./manage.sh prod-install-runner`（會自動安裝 actions-runner + pack + podman、註冊 + systemd）
    3. `gh variable set GHA_RUNNER_LABEL --repo wusung/0ops --body 0ops-builder`
    4. `./manage.sh prod-runner-validate` 全綠
- [ ] **Q3 — OCI artifact registry ADR-0014**
  - 目標：寫 ADR 評估「OCI artifact 取代本地 ingest tree」之 trade-off
  - 動工條件：v2 多 region / artifact promotion 需求浮現；v1 不採
- [ ] **Q6 — `repo_url` 全面移除**
  - 目標：CLI/API 完全移除 deprecated `repo_url` 路徑；只剩 `source` sum type
  - 動工條件：M8 啟動（spec § 17 Target M8）；release migration doc 已預告

### v1 收尾殘留（不阻擋 ship）

- [ ] **`nextdemo.winshare.tw` 真實外部 HTTP 200**（waiting on user-side resources）
  - 來源：M2 驗收基準（`tasks/todo-archive.md` § 驗收基準）
  - 工程封裝**全部完成**（2026-06-06，PR #107 / #108 / #109 / #110 + 本次）：
    - 部署層：`docs/features/production-deployment/spec.md` + `deploy/bootstrap/` + `deploy/gitops/argocd/` + server ingress/config
    - OAuth：`./manage.sh prod-setup-oauth` / `prod-verify-oauth` + `docs/runbooks/production-oauth-setup.md`
    - End-user：`scripts/install.sh` + `0ops mcp setup <host>` + `docs/quickstart.md`
    - Self-hosted runner：workflow opt-in + `./manage.sh prod-install-runner` + `prod-runner-validate`
    - One-shot：`./manage.sh prod-bootstrap-all` + `docs/runbooks/production-acceptance.md`
    - Runbook：`winshare-route-failure.md` § 2-5 全填
  - 剩下純 user 動作：
    1. 備齊外部資源：Cloudflare zone + wildcard CNAME + tunnel token + K3s host + `kubeseal`/`gh`/`gh auth login`
    2. `cp deploy/bootstrap/env.example deploy/bootstrap/.env.prod`，填值
    3. `./manage.sh prod-bootstrap-all`
  - 驗收：`docs/runbooks/production-acceptance.md` § 9 三條 curl 全 200
- [x] **trace_id 全鏈路驗證**
  - 結果：C1（middleware ctx 注入）/ C2（preview.trace_id 欄位）/ C3（callback handler 補 audit.Log）三個 fix + redeploy/create-app/delete-app confirm 都改讀 `audit.TraceIDFromContext` + e2e composition test 已 ship。
  - 既已驗：HTTP header `X-Trace-ID` → middleware ctx → preview.trace_id → deploy_run.trace_id → workflow_dispatch payload trace_id → callback payload trace_id → audit_log.trace_id 同一 trace 串到底。
  - 範圍排除（follow-up）：M1 `reconciliation_job.trace_id` 欄位、M2 slog `ContextHandler` 自動注入、`requestTrace` middleware 抽到 shared package、`apps.go:554` slog `*string` 列印 ptr 位址而非值（pre-existing bug）。
- [x] **runbook winshare 細節補完** — 2026-06-06，隨 `feat/production-deployment` PR 收。§ 6 「待落地」段移除；§ 2-5 已補 kubectl / cloudflared / argocd 具體指令。

### 治理 / 商業（文件層 backlog）

> 不是工程任務；user / founder 決策範疇。

- [ ] 公司法律主體
- [ ] 領投人選與時程
- [ ] Open source 範圍決策（v1 全閉源 vs v1 OSS core）
- [ ] Managed cloud 上線時程（建議與 v2 Web UI 同步）
- [ ] AI CLI 廠商合作 outreach 順序
- [ ] Repo 主機位置最終定案（自建 vs GitHub org）
- [ ] Copilot CLI / Codex CLI 與官方 Go SDK 相容性矩陣（v1 起手時驗證）
- [ ] Backend SSE → MCP streaming 評估（官方 Go SDK 若不足則分頁拉取）

## Governance Guide

> 本區不是進度追蹤，不用 checkbox。

### docs/adr-reading-strategy.md 套用要點

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
