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

- [ ] **Q1 — production CI workflow 驗證**
  - 目標：`deploy/workflows/deploy-app-from-upload.yml` 對 self-hosted runner 在 production GHA 跑通；JWT fetch 路徑端到端驗
  - 卡點：需 staging / production GHA runner + `OPS_API_PUBLIC_URL` 對外可達；dev e2e (`tasks/m6-source-upload-e2e.sh`) 不覆蓋 workflow 端
  - Owner：待 production rollout 時 ops + agent 配合
- [ ] **Q3 — OCI artifact registry ADR-0014**
  - 目標：寫 ADR 評估「OCI artifact 取代本地 ingest tree」之 trade-off
  - 動工條件：v2 多 region / artifact promotion 需求浮現；v1 不採
- [ ] **Q6 — `repo_url` 全面移除**
  - 目標：CLI/API 完全移除 deprecated `repo_url` 路徑；只剩 `source` sum type
  - 動工條件：M8 啟動（spec § 17 Target M8）；release migration doc 已預告

### v1 收尾殘留（不阻擋 ship）

- [ ] **`nextdemo.winshare.tw` 真實外部 HTTP 200**
  - 來源：M2 驗收基準（`tasks/todo-archive.md` § 驗收基準）
  - 卡點：需 Cloudflare zone wildcard CNAME + `deploy/chart/cloudflare-tunnel/` 部署 + K3s ingress sync
  - 驗法：`E2E_MODE=production make m2-8-e2e-acceptance`
- [ ] **trace_id 全鏈路驗證**
  - 目標：grep + 驗 backend request → preview row → deploy_run → GHA payload → callback → audit_log → structured log 串回同一 trace
  - 注意：M5.2 audit + M5.3 reconciler 已各自實作 `TraceIDFromContext`；動工前需先 audit 而非從零做
- [ ] **runbook 補齊**
  - 缺：GHA callback 驗章失敗排查、create_app stuck in building/syncing、winshare subdomain 路由失敗、burn-rate alert 處理流程
  - 既有：`docs/runbooks/{postgres-failover,postgres-pitr,postgres-restore-test,goose-create-workflow}.md`
- [ ] **`migrations/00003_*.sql` duplicate version rename**
  - 多個 milestone 出口報告之既知遺留；`make migrate` 之 image rebuild path 仍 panic
  - 修法：rename `00003_tool_grants_and_auth_status.sql` → `00004_*.sql`；現有 `00004_team_github_install_index.sql` → `00005_*.sql`；後續編號順延
  - 風險：無 schema 變更，純檔名 rename + goose meta 重建

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
