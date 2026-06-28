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

### M9 Trust（Compliance / Audit / Security）— 來源：`docs/trust-and-compliance/plan.md` § 5.1

> 登記於 task-list/task-status：PR #128（M9.0–M9.6）。文件層全封板：plan + 7 feature spec
> + ADR-0015/0016/0017 + business-plan 餵回（PR #126/#127）。實作層按依賴序逐切片跑 agent loop。

- [x] **M9.0 threat-model（STRIDE 系統威脅模型）** — Done 2026-06-28，PR #126（純文件）
- [x] **M9.1 audit append-only + tamper-evidence + export/verify** — Done 2026-06-29
  （`docs/features/audit-export-and-integrity/spec.md` / ADR-0015；切片化）
  - [x] **slice a**：hash-chain 核心（`chain.go`）+ migration `00013`(schema) + ADR-0015→Accepted
    + spec §4.3 配方對齊 — Done 2026-06-29，**PR #130**；13 單元測試（golden-vector / 0x1F 注入 /
    竄改偵測×8）、migration up/down/up 可逆驗證、CI `test` 綠
  - [x] **slice b**：寫入路徑交易（head-lock + `ON CONFLICT` upsert + hash + UPDATE head）—
    Done 2026-06-29，**PR #133**；5 整合測試（重算 / 跨 team 隔離 / 24-writer 並發無丟失 /
    unicode+大整數 jsonb / 非 canonical UUID）對真 postgres 綠、CI `test` 綠
  - [x] **slice b2（append-only role）** — Done 2026-06-29；migration `00014` 建
    `"0ops_app"`/`"0ops_migrate"`/`"0ops_archive"`（NOLOGIN，無密碼）+ `revoke UPDATE/DELETE`
    on audit_log（parent + 每 partition + archive，動態）+ `audit_chain_head` 保留 SELECT/INSERT/
    UPDATE（hard rule #1/#2）；`db.ConfigFromEnv` 優先讀 `APP_DATABASE_URL`；`CreateMonthlyPartition`
    對新 partition 補 revoke；連線切換落 compose（`provision-app-role` 服務 + `deploy/dev/`）+ .env；
    runbook `docs/runbooks/audit-append-only-role.md`。整合測試：app role 改/刪被拒 + chain_head 可改 +
    新 partition 被拒；migration up/down/up 可逆（Down 走 REASSIGN OWNED 防誤刪 migrate 擁有之表）。
  - [x] **slice c（export API）** — Done 2026-06-29；`GET .../audit/export`（CSV/JSON、串流游標、
    13mo 上限 422、since 必填）+ 新 scope `audit:export`（admin 專屬，與 `audit:read` 正交，hard rule #6）+
    integrity manifest（per-chain genesis/tip/row_count，CSV 走 `X-0ops-Audit-Integrity` header；hard rule #7）；
    export entries 帶 prev/row hash 供離線 verify；`ExportAuditLog`/`ListChainHeads` + handler + CLI `audit export`
    + backendclient。RBAC（缺 scope 403 / viewer 403）+ 全鏈竄改偵測整合測試。
  - [x] **slice d（verify CLI）** — Done 2026-06-29；`audit.VerifyChain` 重算 per-(team,month) 鏈
    （row_hash / linkage / row_count / tip 斷裂偵測，exit 1）；`0ops audit verify` 逐月完整抓取（避免
    部分視窗 false-BREAK）→ `verifyEnvelope` 重算；verify/export 皆不暴露 MCP（hard rule #9）。
- [x] **M9.2 compliance-framework-mapping（PDPA/SOC2 控制對應）** — Done 2026-06-29（控制狀態對齊已交付：A1 HA/PITR/SLO 升已具備 M5.4/M5.5/M2.6+ADR-0008、audit tamper-evidence/append-only/export/verify 升已具備 M9.1+ADR-0015、§3 範例改用 SSO M9.5/supply-chain M9.4；spec → accepted；`docs/0ops-plan-schema.md` 加資料分類+保留錨點）
- [ ] **M9.3 security-hardening** — Pending（依 M9.0）
  - **已核准 v1 scope（直接執行、勿再停下問範圍）**：尊重 spec 的 deferred/open 邊界與 hard rule #4/#5/#7、§12。
    - **§5 高風險差異化確認 — 完整落地（本 task 主體）**：`security/risk.go` 純函式（risk_level 目錄）+ migration 加
      `preview.risk_level`/`required_phrase` 欄 + confirm 端 typed-confirmation AND 驗證（**不繞過既有 preview/confirm 後端強制**）+ DTO 唯讀欄 + CLI/MCP typed confirmation + 高風險動作必測。
    - **§6 token anomaly — 僅純模組**：`security/anomaly.go` 評估純函式 + 反應政策 + `abuse_detected` audit action 常數 + 單元測試（餵訊號→斷言 emit）。**不建偵測 goroutine**（歸 rate-limit-and-abuse，deferred；hard rule #5）。
    - **§7 TTL team policy — 僅純函式**：`security/policy.go` `ResolveTTL = min(req, teamCap, globalMax)` + 全域常數 + 單元測試。**不加 migration、不改簽發路徑**（team_security_policy schema 屬 §12 open，待 auth-and-rbac）。
    - **§4/§8/§9 — 文件**：`baseline-matrix.md`（盤點，審計可出示）、§8 default-deny-all manifest 與跨-ns CI 列明確 deferred（歸 k3s-namespace-isolation + 需 CI cluster）、§9 at-rest 金鑰 runbook。
    - **誠實**：spec §11 三條 end-to-end（TTL 簽發收斂 / anomaly→abuse_detected / 跨 ns 拒）降級為函式級單元測試 + 文件標 deferred 條件，不灌水講成已具備（hard rule #4）。
- [ ] **M9.4 supply-chain-security** — Pending（依 M2.2/M2.3；ADR-0017）
- [ ] **M9.5 sso-saml（OIDC + 集中撤權）** — Pending（依 M1；ADR-0016）
- [ ] **M9.6 audit-event-notification（outbox webhook）** — Pending（依 M9.1）

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
- [x] **Q6 — `repo_url` github alias 移除（M8）** — Done 2026-06-09
  - 範圍決策（窄刪）：移除 github-via-`repo_url` alias（API/CLI/MCP），刪 server normalize shim，
    MCP `create_app_preview` 補 `source` 欄位。github 一律走 `source`；`repo_url` 僅留 ADR-0012
    dev `file://`（spec § 2.2 不改動，非「完全移除 repo_url」）。
  - 落地：`internal/server/apps.go`（reject github repo_url）、`createapp/service.go`（service 層同步）、
    `internal/cli/root.go`（`--repo-url github` 硬錯導向 `--source`）、`internal/mcp/server/server.go`
    （`source` 欄位 + 透傳）、`dto/apps.go`（comment）。測試全綠（36 套件）。文件：spec § 16/§ 17 +
    release migration doc「M8 更新」段。
  - 殘留（非本次範圍）：dev `file://` 之最終移除須待 ADR-0012 supersede，屬獨立決策。

### v1 收尾殘留（不阻擋 ship）

- [ ] **`nextdemo.jesontech.com` 真實外部 HTTP 200**（waiting on user-side resources）
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

### Bug fixes / hardening（2026-06-09，end-user UX 驗證連帶）

> 起點：真實在 Claude Code 內用 `0ops onboard` + MCP `delete_app` 驗證 end-user UX 時連環暴露。
> 全部對 dev compose backend 端到端驗證，非僅單元測試。

- [x] **delete_app 永遠卡 `deleting`（P0）** — PR #117 + #118，2026-06-09
  - Root cause：`cmd/server/main.go` 給 reconciler 傳空 `HandlerRegistry`，`cleanup_residue`
    job 撞 `unknown job kind` → 8 次 retry 後 `failed_permanently` → app row 永遠 `deleting`。
    `deleteapp.HandleResidue` 早寫好且有測試，只是從沒接上 M5.3 runner。
  - **#117**（修因）：`deleteapp.ResidueJobKind` 常數 + `Service.ResidueHandler()` adapter +
    `server.RegisterReconcilerHandlers` 可測 wiring；`cmd/server` 改呼叫它而非傳空 registry。
    紅綠驗證 + live 驗證（`hello` app 自動收斂消失）。
  - **#118**（修果）：`0ops admin retry-delete --team-slug --app-slug` + `db.RetryStuckDelete`
    （驗 app 仍 `deleting`，re-enqueue fresh job）+ `docs/runbooks/delete-app-residue.md`
    （spec § 6.3 標「待 M5 撰寫」那份）。清掉卡 10 天的 node-demo / node-demo-2（live 驗證收斂）。
  - 連帶修復（同期暴露）：
    - **#113** `CreateCLIToken` 漏 `expires_at`（migration 00003 NOT NULL）→ device flow login 在套了
      00003 的 DB 會 fail；verification 跑 `manage.sh test` 才暴露。
    - **#112** host-side DB test DSN translation（`@db:5432` → `@127.0.0.1:15432`）讓 `manage.sh test`
      在 host 端能跑 DB integration（先前全 SKIP）。
    - **#114** CI 加 postgres service + migrations，讓 `internal/server/db` smoke tests 在 GHA 真跑
      （root cause：CI 從沒設 DATABASE_URL → 上述 schema-vs-query 不一致沒被攔下）。

### end-user onboarding（2026-06-08/09）

- [x] **一條 curl install + login + AI CLI 接線** — PR #115，2026-06-08
  - `scripts/install.sh` 設 `OPS_HOST` 後自動跑 `0ops onboard`（device flow login + 偵測 claude/codex
    + 寫 MCP config）。`0ops mcp setup <host>` + `0ops onboard <host>` 子命令。
  - 端到端驗證：v0.1.2 release binary `curl|sh` → onboard → MCP `tools/list` 24 tools →
    `tools/call list_apps` 回真實資料。

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
