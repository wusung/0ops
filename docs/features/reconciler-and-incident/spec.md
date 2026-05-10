# Feature Spec：reconciler-and-incident

> **狀態**：draft
> **來源**：`docs/0ops-plan.md`「Reconciler 收斂迴圈」「Deploy 狀態機」段；ADR-0002 § 4 第 8 點（reconciler）；ADR-0006（failure_classification 強制）；本 spec 依賴 `preview-confirm-gate`、`build-pipeline-and-callback`、`custom-domain-and-verify`、`gitops-render-and-argocd`、`audit-log`、`backend-ha-leader-election`
> **適用範圍**：reconciliation_job 表、收斂迴圈、deploy_run 滯留偵測、incident 表、failure_classification 強制
> **對應 Milestone**：M5

## 1. 結論（先讀本段）

- Reconciler 為 backend 內背景 goroutine；M5 後僅 leader 跑（接續 `backend-ha-leader-election`）
- `reconciliation_job` 表為 DB-backed 工作隊列；指數退避 `min(60s × 2^attempts, 30min)`，> 8 次轉 `failed_permanently`
- 偵測四類滯留：deploy_run building > 30 min（pull GHA workflow_run）、deploy_run applying > 15 min（pull ArgoCD）、domain_verify pending（30s tick；屬 `custom-domain-and-verify`）、ghcr-pull refresh
- 各 reconciler kind 為獨立 goroutine（共用同一 leader-only gate）；tick 不同步
- `failure_classification` 強制非 null：寫入 `deploy_run` final state 時 SQL CHECK + backend 端 lint 攔；`unknown` 比例 dashboard panel
- Incident 表（v1 引入）：MTTR 量測來源；reconciler `failed_permanently` 自動建 incident；ops 手動 close
- Deploy 狀態機之完整轉移由本 spec 定（接續 plan.md Deploy 狀態機段）；transition 之 invariant 與失敗矩陣

## 2. 範圍

### 2.1 包含
- `reconciliation_job` 表 schema 與 lifecycle
- 各 reconciler kind 之偵測 / 推進邏輯
- Deploy_run 狀態機之 transition 表 + invariant
- `failure_classification` 列舉與強制機制
- Incident 表 schema、自動建立、手動 close、MTTR 計算
- Reconciler 之 leader-only 行為
- Metric / SLI（reconciler 健康度、incident MTTR）

### 2.2 不包含
- 個別 action 之主流程（屬 create-app / delete-app / redeploy / add-domain spec）
- 對外 alerting（屬 `slo-and-alerting` spec）
- audit_log 寫入細節（屬 `audit-log` spec；本 spec 規範寫入時機）
- on-call runbook（屬 ops 範圍）

## 3. 檔案結構

```
0ops/
├── internal/
│   └── server/
│       └── reconciler/
│           ├── runner.go              # 主入口；leader-only gate；起子 goroutine
│           ├── jobs.go                # reconciliation_job CRUD
│           ├── deploy_status.go       # deploy_run building/applying 滯留
│           ├── domain_verify.go       # 委派 custom-domain spec
│           ├── ghcr_refresh.go        # 委派 k3s-namespace-isolation spec
│           ├── preview_cleanup.go     # preview gc（屬 preview-confirm-gate spec；本 spec 引用為 leader-only）
│           ├── incident.go            # 自動建 incident
│           ├── statemachine.go        # deploy_run transition + invariant 檢查
│           ├── metrics.go
│           └── doc.go
└── migrations/
    └── 000X_incident.sql              # incident 表
```

## 4. `reconciliation_job` schema

接續 plan.md 已定（本 spec 補語意 + 索引）：

```sql
reconciliation_job(
  id uuid pk,
  team_id uuid fk not null,
  subject_type text not null,            -- 'deploy_run' | 'domain_binding' | 'app' | 'install' | 'ghcr_secret'
  subject_id uuid not null,
  kind text not null,                    -- 'deploy_status_pull' | 'domain_verify' | 'cleanup_residue' | 'ghcr_refresh' | ...
  attempts int default 0,
  next_attempt_at timestamptz,
  payload jsonb,
  last_error text,
  status text not null default 'pending', -- 'pending' | 'in_progress' | 'completed' | 'failed_permanently'
  created_at timestamptz default now(),
  completed_at timestamptz,
  -- 本 spec 補：
  trace_id text                          -- 與 subject 之 trace 對齊
);

CREATE INDEX recon_pending ON reconciliation_job (status, next_attempt_at) WHERE status = 'pending';
CREATE INDEX recon_subject ON reconciliation_job (subject_type, subject_id);
CREATE INDEX recon_team    ON reconciliation_job (team_id);
```

## 5. Reconciler 主迴圈

### 5.1 主結構

```go
// internal/server/reconciler/runner.go
func Run(ctx context.Context, leader leaderelection.Leader, db *pgxpool.Pool, log *slog.Logger) {
    go runDeployStatus(ctx, leader, db, log)
    go runDomainVerify(ctx, leader, db, log)
    go runGhcrRefresh(ctx, leader, db, log)
    go runPreviewCleanup(ctx, leader, db, log)
    go runJobProcessor(ctx, leader, db, log)
}

func runDeployStatus(ctx, leader, db, log) {
    t := time.NewTicker(30 * time.Second)
    for {
        select {
        case <-ctx.Done(): return
        case <-t.C:
            if !leader.IsLeader() { continue }
            scanDeployRunStuck(ctx, db, log)
        }
    }
}
```

### 5.2 Leader-only

- 透過 `internal/server/leaderelection.Leader` 介面（屬 `backend-ha-leader-election` spec）
- v1 single replica：`IsLeader()` 永遠 true
- M5 多 replica：僅 leader pod 跑

## 6. Deploy_run 狀態機

### 6.1 完整 transition 表

| From | To | 觸發者 | 條件 |
|---|---|---|---|
| `queued` | `preparing` | execute() | 進入 reversible 階段 |
| `preparing` | `building` | execute() | 完成 reversible，進 irreversible |
| `building` | `pushing` | callback handler | callback status=success + image 欄位 |
| `pushing` | `rendering` | callback handler 內部 | image 確認後 backend 端 render image_ref |
| `rendering` | `syncing` | callback handler / reconciler | gitops push 完成；ArgoCD 偵測 |
| `syncing` | `live` | reconciler | ArgoCD Healthy |
| 任意 forward stage | `failed` | callback handler 或 reconciler | callback status=failure 或 stage timeout |
| `preparing` 失敗 | `compensating` | execute() | reversible 階段失敗，啟動 undo |
| `compensating` | `rolled_back` | execute() | undo 完成 |
| `compensating` | `failed_permanently` | reconciler | undo 8 次仍失敗 |
| `building` (≤ 30 min) | `building` | reconciler | 等待 callback；不 transition |
| `building` (> 30 min) | `pushing` / `failed` | reconciler | pull GHA workflow_run 結果 |
| `syncing` (> 15 min) | `live` / `failed` | reconciler | pull ArgoCD Application 狀態 |

### 6.2 Invariant

- final state（`live` / `failed` / `rolled_back` / `failed_permanently` / `cancelled`）一旦進入即不可離開
- `failure_classification` 在 final state 為 `failed` / `rolled_back` / `failed_permanently` 時必非 null
- `cancelled` 不需 failure_classification
- transition 必寫入 `deploy_run.events` jsonb append（含 `at`, `from`, `to`, `actor`）

### 6.3 SQL CHECK

```sql
ALTER TABLE deploy_run
  ADD CONSTRAINT failure_classification_required
  CHECK (
    (status NOT IN ('failed', 'rolled_back', 'failed_permanently'))
    OR (failure_classification IS NOT NULL)
  );
```

### 6.4 Backend 端 lint

`internal/server/reconciler/statemachine.go` 之 `Transition()` 函式：
- 入參 `(deployRunID, fromState, toState, payload)`
- 驗 transition 合法（依 § 6.1 表）；非法即 panic（fail-fast；屬程式 bug）
- final state 寫入時要求 payload 含 `failure_classification`（若適用）；無則 panic

## 7. `failure_classification` 列舉

### 7.1 完整列表（接續 plan.md）

```
build_*:
  - buildpack_detect_failed
  - build_compile_error
  - build_timeout

registry_*:
  - registry_auth_failed
  - registry_push_failed

scan_*:
  - image_scan_blocked          # v1.1 起 Trivy exit-code=1

gitops_*:
  - gitops_push_conflict

deploy_*:
  - argo_sync_timeout
  - health_check_failed

cloudflare_*:
  - cloudflare_api_failed

token_*:
  - build_secret_expired

repo_*:
  - repo_checkout_failed

unknown                          # 必須 < 5%
```

新增分類必：
1. plan.md 補列
2. 本 spec § 7.1 補列
3. dashboard panel 加 series

### 7.2 自動分類來源

| 訊號 | 對應 classification |
|---|---|
| GHA `pack build` step exit | `buildpack_detect_failed` / `build_compile_error` |
| GHA timeout 20 min | `build_timeout` |
| GHA `docker login` step fail | `registry_auth_failed` |
| GHA `pack build --publish` push fail | `registry_push_failed` |
| GHA Trivy exit-code=1 | `image_scan_blocked` |
| GHA `git push` 5 retry fail | `gitops_push_conflict` |
| ArgoCD Application status=Degraded | `health_check_failed` |
| ArgoCD sync timeout | `argo_sync_timeout` |
| Cloudflare API 5xx 5 retry fail | `cloudflare_api_failed` |
| ops_token 過期 | `build_secret_expired` |
| Checkout target repo 失敗 | `repo_checkout_failed` |
| 其他 / 非預期 | `unknown` → 工程師檢查補分類 |

### 7.3 `unknown` 偵測

- Dashboard panel：`(failed where classification='unknown') / (total failed)` 過去 7d
- 比例 > 5% 持續 14d → ADR-0006 Revisit Trigger #7（觸發強制工程介入）

## 8. Reconciler kinds

### 8.1 `deploy_status_pull`（building > 30 min）

```
SELECT * FROM deploy_run
 WHERE status = 'building'
   AND started_at < now() - interval '30 min';

For each:
  workflow_run := github.GetWorkflowRun(deploy_run.workflow_run_id)
  switch workflow_run.status:
    completed.success → transition to pushing → ... (走 callback 路徑相同 logic)
    completed.failure → transition to failed + classification
    in_progress → continue（下次 tick）
    queued → continue
```

### 8.2 `argo_sync_pull`（syncing > 15 min）

```
SELECT * FROM deploy_run
 WHERE status = 'syncing'
   AND started_at < now() - interval '15 min';

For each:
  app := argocd.GetApplication(team_slug + '-' + app_slug)
  switch app.status.health.status:
    Healthy → transition to live
    Degraded → transition to failed + 'health_check_failed'
    Progressing → continue
```

### 8.3 `domain_verify`

委派 `custom-domain-and-verify` § 6.1 之 poller；本 spec 只規範「leader-only gate」；reconciliation_job 表追蹤 attempts。

### 8.4 `ghcr_refresh`

委派 `k3s-namespace-isolation` § 8.2 之 30 min refresh；本 spec 規範「失敗時走 reconciliation_job 退避重試」。

### 8.5 `preview_cleanup`

委派 `preview-confirm-gate` § 8.2 之 GC；本 spec 規範「leader-only gate」。

### 8.6 `cleanup_residue`（delete-app 殘餘）

委派 `delete-app-flow` § 6；本 spec 規範「reconciliation_job 表追蹤」。

## 9. Incident 表

### 9.1 schema

```sql
CREATE TABLE incident (
  id uuid PRIMARY KEY,
  team_id uuid REFERENCES team(id),
  subject_type text NOT NULL,                -- 'deploy_run' | 'domain_binding' | 'app'
  subject_id uuid NOT NULL,
  kind text NOT NULL,                         -- 'failed_permanently' | 'cleanup_residue_stuck' | 'ghcr_refresh_failed'
  severity text NOT NULL DEFAULT 'medium',    -- 'low' | 'medium' | 'high' | 'critical'
  description text,
  trace_id text,
  opened_at timestamptz DEFAULT now(),
  closed_at timestamptz,
  closed_by uuid REFERENCES user_account(id),
  closed_note text
);

CREATE INDEX incident_open ON incident (opened_at) WHERE closed_at IS NULL;
CREATE INDEX incident_team ON incident (team_id, opened_at DESC);
```

### 9.2 自動建 incident

| 觸發 | severity |
|---|---|
| reconciliation_job 進 `failed_permanently` | medium |
| `failure_classification = unknown` 比例突增（> 10% / 1h） | high |
| Cleanup residue stuck > 24h | high |
| Postgres replica lag > 60s（屬 `postgres-ha-and-dr`） | critical |
| Backend leader handover 失敗（屬 `backend-ha-leader-election`） | critical |

### 9.3 手動 close

```
$ 0ops incidents list
ID                 OPENED                       SUBJECT              KIND                       SEVERITY
abc-123            2026-05-10 12:34:56 +08:00   deploy_run/xyz       failed_permanently         medium
...

$ 0ops incidents close abc-123 --note="root cause: GitHub API 中斷；已恢復"
✓ closed
```

CLI endpoint：`POST /v1/teams/{slug}/incidents/{id}:close`，role >= admin。

### 9.4 MTTR 計算

- `MTTR = avg(closed_at - opened_at)`，per severity / per kind / per period
- v1 簡單聚合查詢（背景 5 min 結算寫入 metric）
- SLO：MTTR p50 < 1h（plan SLO；屬 `slo-and-alerting`）

## 10. Audit log 寫入

- 每 reconciliation_job 之 `failed_permanently` 寫 audit_log（`audit-log` spec § 5.1）
- 每 incident 自動建 / close 寫 audit_log
- 每 deploy_run transition 寫 deploy_run.events（不入 audit_log；避免噪音）

## 11. 與其他 spec 接合點

| 接合 | spec |
|---|---|
| Leader-only gate | `backend-ha-leader-election` |
| Domain verify polling | `custom-domain-and-verify` § 6 |
| Preview cleanup | `preview-confirm-gate` § 8.2 |
| ghcr-pull refresh | `k3s-namespace-isolation` § 8.2 |
| Cleanup residue（delete-app）| `delete-app-flow` § 6 |
| Build callback 與 polling fallback | `build-pipeline-and-callback` § 6 |
| ArgoCD Application 狀態查詢 | `gitops-render-and-argocd` § 6.3 |
| MTTR / SLO | `slo-and-alerting` spec |
| Audit | `audit-log` spec |
| failure_classification 與 trace_id | `observability-skeleton` § 6.4 |

## 12. 驗證準則

| 驗證項 | 方式 | 通過條件 |
|---|---|---|
| Building > 30 min 偵測 | mock deploy_run started_at 31 min 前 | reconciler 拉 workflow_run、transition |
| Syncing > 15 min 偵測 | mock | reconciler 拉 ArgoCD app、transition |
| 退避 attempts 1..8 | mock 持續失敗 | next_attempt_at = 60 * 2^attempts |
| Failed_permanently | 8 次失敗後 | status=failed_permanently；建 incident |
| `failure_classification` SQL CHECK | mock UPDATE deploy_run SET status='failed', classification=NULL | DB 拒；CHECK violation |
| `unknown` 比例 panel | dashboard | < 5% 不告警；> 5% panel 紅 |
| Leader-only | 兩 backend pod | 僅 leader pod 跑 reconciler；follower 跳過 |
| Incident 自動建 | mock failed_permanently | incident row 創建；severity=medium |
| Incident 手動 close | `0ops incidents close <id>` | UPDATE closed_at + closed_by；audit |
| MTTR 計算 | 模擬幾個 incident open/close | metric 聚合正確 |
| Audit log 寫入 | failed_permanently | audit_log 含 reconciler_failed_permanently |
| State machine invariant | 嘗試 final → 其他 state | panic（程式 bug） |

## 13. SLI 對應

| SLI | 目標 | 量測 |
|---|---|---|
| Reconciler tick miss rate | < 0.1% | tick 預期次數 vs 實際次數 |
| Failed_permanently 比例 | < 1% / 28d total deploy_run | `incident{kind=failed_permanently} / deploy_run total` |
| MTTR p50 | < 1h（plan SLO） | `avg(closed_at - opened_at)` |
| `unknown` failure_classification 比例 | < 5% / 7d | dashboard panel |
| Leader handover 不影響 reconciler tick | 0 missed tick during handover | M5 後驗證 |

## 14. 對 `docs/0ops-plan.md` 的修改清單

1. 「Reconciler 收斂迴圈」段：交叉引用本 spec；補入「leader-only（M5+）」、「reconciliation_job 表 status 列舉」
2. 「Deploy 狀態機」段：交叉引用本 spec § 6 之 transition 表；plan.md 之圖保留
3. 「DB schema」：補入 `incident` 表；`reconciliation_job` 補欄位 `trace_id`、`status`
4. 「DB schema § deploy_run」：補 SQL CHECK constraint `failure_classification_required`
5. 「Observability & SLO § failure_classification」段：交叉引用本 spec § 7

## 15. Open issues

- M5 之前 reconciler 全跑於 single backend；leader-only 為 M5 升級時引入；不影響 v1 行為
- Incident 嚴重度自動評定：v1 採固定規則（§ 9.2）；v1.1 評估動態（如 affected user 數）
- Incident 之 page on-call 整合：v1 為 audit + dashboard；v1.1 PagerDuty / Slack
- MTTR per kind 聚合：v1 為 SQL 查詢；v1.1 評估 materialize view
- `failed_permanently` 之手動 retry 路徑：v1 不支援；ops 須直 SQL UPDATE 重啟（runbook）
- Reconciliation_job 之 dead-letter 表：超過 30 天的 failed_permanently job 移轉；屬 v1.1
- `cleanup_residue` 失敗類型細分：v1 採單一 kind；v1.1 拆 sub-kind
- `unknown` 比例突增的 alert：本 spec 提及 dashboard panel；自動 alert 屬 `slo-and-alerting`
- Reconciler 對「callback 已到但 backend 同步處理慢」之 race：v1 採「先進 reconciler 嘗試，若 callback 到達已推進 state，reconciler 視為 no-op」

## 16. 不可違反的硬性規則

> 違反以下任一項，PR 不可合入。

1. `deploy_run.failure_classification` SQL CHECK constraint 必有；違反即 DB 拒
2. State machine `Transition()` 函式必驗轉移合法性；非法即 panic（不允許 silent 跳轉）
3. Reconciler 必 leader-only（M5 後）；不得多 replica 並行掃同一 row
4. 退避策略固定 `min(60s × 2^attempts, 30min)`；不得個別 kind 自定
5. Failed_permanently 必建 incident；audit 必寫
6. `unknown` failure_classification 必入 dashboard panel；> 5% 必觸發工程介入
7. `reconciliation_job` 表 status 列舉固定：pending / in_progress / completed / failed_permanently；不得自由擴增
8. Incident close 必經 CLI（含 audit）；不得直 SQL UPDATE 跳過 audit
9. Final state（live / failed / rolled_back / failed_permanently / cancelled）一旦寫入即 immutable
10. Reconciler tick miss（goroutine panic）必導致 leader release；不得 silent 中斷
