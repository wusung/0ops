# Feature Spec：audit-log

> **狀態**：draft
> **來源**：`docs/0ops-plan.md` DB schema `audit_log`、Tool catalog `query_audit_log`；ADR-0006 第 5 點（trace_id 跨界第 5 段）；本 spec 依賴 `preview-confirm-gate`、`error-model`、`auth-and-rbac`、`shared-dto-and-contract`、`observability-skeleton`
> **適用範圍**：audit_log 寫入點、查詢 API、CLI/MCP 呈現、保留期；不含 K8s audit log（屬 ops runbook）
> **對應 Milestone**：M5

## 1. 結論（先讀本段）

- audit_log 為 0ops 之「業務行為帳本」；與 K8s audit log（基礎設施帳本）分離
- 寫入點全部由 backend 程式碼明確呼 `audit.Log(ctx, entry)`；不採自動 middleware（避免 false positive）
- 必入 audit 之事件類別：
  - 所有寫入 / 刪除 action 之 preview / confirm（含失敗）
  - Auth 操作（login / logout / token create / revoke）
  - GitHub App install / uninstall（含 webhook-driven）
  - Plan tier 變動
  - Secret rotation
  - Abuse 偵測告警
  - Reconciler `failed_permanently`
- 結構：`{actor, subject, action, args, result, preview_id, trace_id, created_at}`；redact 後寫入
- 查詢 API：`GET /v1/teams/{slug}/audit?since=&until=&action=&actor=`；支援分頁
- 保留期：13 個月（合規最小值；plan.md 已定）；超過分區 drop
- CLI / MCP 端讀取：role >= admin（owner/admin 可看全 team；member 只看自己）
- trace_id 為跨 5 段鏈路第 5 段（與 ADR-0006 § 4 第 5 點對齊）；audit_log 為 trace 之終點落地

## 2. 範圍

### 2.1 包含
- `internal/server/audit/` package：`Log()` 介面、`Query()` 介面、redactor 整合
- `audit_log` 表之欄位語意與索引
- 全部 audit 寫入點清單與時機
- `query_audit_log` endpoint 與 CLI / MCP
- 保留期實作（partition by month + drop）
- 跨 binary 一致性（CLI / MCP 無 audit；只 backend 寫）
- audit log 之 redaction 規則（與 `error-model` § 9 共用 redactor）

### 2.2 不包含
- K8s audit log（K3s 端設定，屬 ops runbook）
- Reverse proxy 之 access log（屬 `observability-skeleton`）
- 結構化日誌本身（屬 `observability-skeleton` § 5）
- 對外通知（webhook / email；v1.1）
- audit log 之長期歸檔到 R2（v2 評估）

## 3. 檔案結構

```
0ops/
├── internal/
│   └── server/
│       ├── audit/
│       │   ├── log.go              # Log(ctx, entry) 寫入路徑
│       │   ├── query.go            # Query(filter) 查詢路徑
│       │   ├── partition.go        # 月度 partition + drop
│       │   ├── redact.go           # 與 error-model § 9 共用 redactor
│       │   ├── metrics.go
│       │   └── doc.go
│       └── routers/
│           └── audit.go            # GET .../audit
└── migrations/
    └── 000X_audit_log_partition.sql   # convert audit_log to partitioned table by month
```

## 4. Schema

### 4.1 plan.md 已定欄位（補語意）

```sql
audit_log(
  id bigserial pk,
  team_id uuid fk not null,
  actor_user_id uuid fk references user_account(id),  -- system 來源時為 null
  subject_type text,                                    -- 'app' | 'domain' | 'team' | 'token' | 'membership' | 'secret' | 'github_install' | 'system'
  subject_id uuid,                                       -- 對應 subject_type 之 id；null 為非具體實體
  action text,                                           -- 動作字串，e.g. 'create_app', 'delete_app', 'login', 'token_create', 'plan_change'
  args jsonb,                                            -- 行為參數（已 redact）
  result jsonb,                                          -- 結果（成功 = data；失敗 = error envelope，已 redact）
  preview_id uuid,                                       -- 對應之 preview_id（若行動經 preview/confirm；否則 null）
  trace_id text,                                         -- W3C trace_id；32 hex 或 null
  created_at timestamptz default now(),
  -- 本 spec 補欄位：
  source text not null default 'user',                  -- 'user' | 'webhook' | 'reconciler' | 'system'
  outcome text not null default 'success',              -- 'success' | 'failure' | 'idempotent_replay'
  http_status int                                       -- HTTP 回應 status code（若適用）
)
```

### 4.2 索引

```sql
CREATE INDEX audit_log_team_created ON audit_log (team_id, created_at DESC);
CREATE INDEX audit_log_team_action  ON audit_log (team_id, action, created_at DESC);
CREATE INDEX audit_log_team_actor   ON audit_log (team_id, actor_user_id, created_at DESC);
CREATE INDEX audit_log_trace        ON audit_log (trace_id);   -- root-cause 查詢
```

### 4.3 Partition by month

```sql
-- 改 audit_log 為 partitioned table（v1 即啟）
CREATE TABLE audit_log (...) PARTITION BY RANGE (created_at);

-- 每月 partition：audit_log_2026_05, audit_log_2026_06, ...
-- 由背景 job 預建未來 3 個月 partition
-- 13 個月後 drop 舊 partition
```

## 5. 寫入點清單

### 5.1 必入 audit_log 之事件

| Action | source | 寫入時機 |
|---|---|---|
| `*_preview` 全部 action | user | preview 創建成功 / 失敗 |
| `*` 全部寫入 action（confirm）| user | confirm 完成（success / failure / idempotent_replay 三狀態）|
| `login` | user | device flow 完成 |
| `logout` | user | logout 端點呼 |
| `token_create` | user | PAT 建立 |
| `token_revoke` | user | PAT revoke |
| `install_github_app_callback` | system | callback 處理完 |
| `uninstall_github_app` | user / webhook | 兩種 source 各記 |
| `webhook_received` | webhook | 收到 push event 後（per-team） |
| `redeploy_triggered` | webhook / user / reconciler | 觸發 deploy_run 時 |
| `plan_change` | user / system | team.plan 變動 |
| `secret_rotate_start` / `secret_rotate_finalize` | user | secrets-management § 9.1 |
| `secret_use_failed` | system | secrets-management § 9.1 |
| `abuse_detected` | system | rate-limit-and-abuse § 6 |
| `reconciler_failed_permanently` | reconciler | reconciler-and-incident spec |
| `domain_grace_unbind` | reconciler | custom-domain § 8.2 |
| `app_status_change` | user / system | live → paused / paused → live / live → deleting |

### 5.2 不入 audit_log（避免噪音）

- 一般 read endpoint（已透過 access log）
- middleware 失敗（401 / 403）：屬 access log + metric
- preview consumed 之 idempotent_replay 不重寫 audit（首次寫即可；replay 改進 metric counter）
  - 例外：失敗的 preview 重試成功時補 audit（標 `outcome=success`、`source=user`、`note=after_retry`）
- 純 backend 內部 transition（如 deploy_run state machine）：寫 deploy_run.events，不入 audit_log

### 5.3 寫入介面

```go
// internal/server/audit/log.go
type Entry struct {
    TeamID       string
    ActorUserID  *string         // nil for system / webhook
    Source       Source          // user | webhook | reconciler | system
    SubjectType  string
    SubjectID    *string
    Action       string
    Args         any             // 經 redactor
    Result       any             // 經 redactor
    PreviewID    *string
    TraceID      string          // 從 ctx 取
    Outcome      Outcome         // success | failure | idempotent_replay
    HTTPStatus   *int
}

func Log(ctx context.Context, e Entry) error {
    e.Args = redactor.Redact(e.Args)
    e.Result = redactor.Redact(e.Result)
    if e.TraceID == "" { e.TraceID = observability.TraceIDFromContext(ctx) }
    return db.InsertAuditLog(ctx, e)
}
```

## 6. Query API

### 6.1 Endpoint

```
GET /v1/teams/{team_slug}/audit?since=&until=&action=&actor=&page_size=&cursor=
```

| Param | 說明 |
|---|---|
| `since` | RFC3339；預設 7 天前 |
| `until` | RFC3339；預設 now |
| `action` | 字串 prefix match（如 `delete_*`） |
| `actor` | github_login 或 user_id |
| `page_size` | 預設 50；最大 200 |
| `cursor` | 翻頁 |

### 6.2 RBAC

- 最低 role：取決於 query scope
  - 全 team audit（不帶 `actor` filter）：role >= admin + scope `audit:read`
  - 自己 audit（`actor=current_user`）：role >= viewer + scope `audit:read`（self-service）
- middleware 在 router 端註冊時宣告

### 6.3 回應 DTO

```go
// internal/shared/dto/audit.go
type AuditLogEntry struct {
    ID           int64     `json:"id"`
    Time         time.Time `json:"time"`
    Source       string    `json:"source"`
    Actor        *string   `json:"actor,omitempty"`         // github_login or "system:..."
    Action       string    `json:"action"`
    SubjectType  string    `json:"subject_type"`
    SubjectID    *string   `json:"subject_id,omitempty"`
    Outcome      string    `json:"outcome"`
    PreviewID    *string   `json:"preview_id,omitempty"`
    TraceID      *string   `json:"trace_id,omitempty"`
    HTTPStatus   *int      `json:"http_status,omitempty"`
    Args         any       `json:"args,omitempty"`           // redacted
    Result       any       `json:"result,omitempty"`         // redacted
}
```

## 7. CLI / MCP

### 7.1 CLI

```
$ 0ops audit list --since=24h --action=create_app
TIME                         ACTOR              ACTION         OUTCOME   SUBJECT
2026-05-10 12:34:56 +08:00   alice              create_app     success   app/nextdemo
2026-05-10 11:22:01 +08:00   alice              create_app     failure   app/(--)
...

$ 0ops audit get <id>
[完整 JSON 輸出，含 args / result / trace_id]

$ 0ops audit list --trace=<trace_id>
[同 trace_id 下所有事件，跨多 team 不可（仍 RBAC）]
```

- `--output table/json/yaml`
- 預設僅顯示當前 team；`--all-teams` 列當前 user 所屬全部 team（v1.1）

### 7.2 MCP

`query_audit_log` tool；input schema：
```json
{
  "type": "object",
  "properties": {
    "team_slug": {"type": "string"},
    "since": {"type": "string", "format": "date-time"},
    "until": {"type": "string", "format": "date-time"},
    "action": {"type": "string"},
    "actor": {"type": "string"},
    "page_size": {"type": "integer", "default": 50, "maximum": 200}
  },
  "required": ["team_slug"]
}
```

read tool；不需強制句式。

## 8. Redaction

### 8.1 規則（與 `error-model` § 9 同 redactor）

- args / result 之欄位 prefix `secret_` / `password` / `token` / `_signature` → 替換 `***`
- `Authorization` header / cookie / bearer 明文 → mask
- Webhook payload 全文不入 args；只記 `delivery_id` + 摘要

### 8.2 範例

```
原始 args（create_app preview）:
{ "slug": "nextdemo", "repo_url": "https://github.com/...", "ref": "main" }
→ redact 不影響（無 secret）

原始 args（token_create）:
{ "name": "ci", "scopes": ["apps:read"], "token": "op_pat_xxxxxxxx..." }
→ redact 後:
{ "name": "ci", "scopes": ["apps:read"], "token": "***" }
```

## 9. 保留期

### 9.1 13 個月（plan.md 已定）

- partition by month；每月底 ops 跑 `0ops-ops audit-rollover` 預建下月 + drop 13 月前 partition
- 自動化：背景 cron（v1 為 K8s CronJob）每月 1 號執行
- v2 評估：drop 前 archive 至 R2（cold storage）

### 9.2 例外保留

- `delete_app` 對應之 audit_log：永久保留（接續 `delete-app-flow` § 13 規則）
- 實作：drop partition 前 SELECT `action='delete_app'` 之 row 移至 `audit_log_archive` 表
- archive table 不參與 query API；ops 透過 SQL 直查

## 10. 與其他 spec 接合點

| 接合 | spec |
|---|---|
| Trace ID 跨界第 5 段 | `observability-skeleton` § 6.4 |
| Redactor（共用 instance）| `error-model` § 9 |
| Preview consumed 寫入時機 | `preview-confirm-gate` § 11 |
| Webhook 寫入點 | `webhook-and-redeploy` § 5.2 |
| Reconciler 寫入點 | `reconciler-and-incident` spec |
| Secret 寫入點 | `secrets-management` § 9 |
| Abuse 寫入點 | `rate-limit-and-abuse` § 6 |
| Plan change 寫入 | `auth-and-rbac` 之 plan 升降級 path |
| RBAC（admin / member / scope）| `auth-and-rbac` § 6 |
| `query_audit_log` tool / CLI | `mcp-tool-description-lint`（read tool 不強制句式）|

## 11. 驗證準則

| 驗證項 | 方式 | 通過條件 |
|---|---|---|
| 寫入：preview 創建 | 跑 create_app_preview | DB 含對應 row、action='create_app_preview' |
| 寫入：confirm success | 跑 create_app | DB 含 row, outcome='success', preview_id 對應 |
| 寫入：confirm failure | mock saga compensate | row outcome='failure', result.error 含 envelope |
| Idempotent replay 不重寫 | 同 preview_id 第二次 confirm | 不新增 row（除非「失敗後成功」例外） |
| Webhook 寫入 | mock push event | row source='webhook', actor=null |
| Reconciler 寫入 | mock failed_permanently | row source='reconciler' |
| Redaction args | 含 token 之 token_create | row.args.token = '***' |
| Redaction result | 含 secret 之 result | 同 redact |
| Trace_id 落地 | 從 CLI 一個動作 | row.trace_id 與 backend log / deploy_run 一致 |
| Query by team | role=admin | 回 team 全 audit |
| Query by self | role=member | 只回 actor=self |
| Query by trace_id | 給 trace | 回該 trace 全部 row（仍 RBAC team filter） |
| Query partition 跨月 | since 跨多月 | 多 partition union 結果正確 |
| Partition drop | 14 個月後 | 舊 partition drop；archive 已轉移 delete_app rows |
| `delete_app` 永久保留 | 14 個月後 | row 在 audit_log_archive；query API 不顯示 |
| MCP query_audit_log | claude code 端 call | 回 JSON 結構符合 DTO |
| RBAC 拒 viewer 看全 team | viewer + audit:read | 403 forbidden_role |

## 12. SLI 對應

| SLI | 目標 | 量測 |
|---|---|---|
| audit log write 成功率 | > 99.99% / 28d | `audit_log_write_total{outcome=success} / total` |
| audit query latency p95 | < 500ms | `0ops_http_request_duration_seconds{route="audit"}` |
| Partition rollover 成功率 | 100% | 每月 cron 是否完成 |
| `unknown` actor 比例 | < 0.1% | actor 缺失（應由 source 推斷）之 row 比例 |

## 13. 對 `docs/0ops-plan.md` 的修改清單

1. 「DB schema § audit_log」：新增欄位 `source`、`outcome`、`http_status`；補 partition by month 規約
2. 「Tool catalog」`query_audit_log`：交叉引用本 spec；補入 RBAC 規則
3. 「保留期」段：補入「`delete_app` 對應之 audit 永久保留（移至 audit_log_archive）」
4. 「Observability & SLO § Trace propagation」段（line 739 附近）：交叉引用本 spec 為第 5 段落地點

## 14. Open issues

- audit_log_archive 表 schema：與 audit_log 同；分離為避免主表查詢負擔
- 跨 team audit（user 自己的）：v1 用 `/v1/me/audit`（待補；v1.1）
- audit query 之 full-text search：v1 不支援；v2 評估（PostgreSQL `tsvector`）
- Audit log export（CSV / JSON dump）：v1.1
- 對外 webhook 通知（重要 audit 事件，如 delete_app）：v1.1
- audit_log 寫入失敗（DB 不通）之 fallback：v1 採 log warn + 進 reconciliation_job 重寫；audit 不該 silent 丟失
- `webhook_received` 之高頻率（每 push 一筆）可能噪音：v1 接受；v1.1 評估只記 unique（per app per day）
- archive 後 delete_app row 是否影響 deploy_run 歷史查詢：deploy_run 為獨立表，audit archive 不影響
- audit_log 之 PII：actor_user_id 為 UUID；github_login 為 PII（GDPR）；保留 13 月為合規（待法務確認）

## 15. 不可違反的硬性規則

> 違反以下任一項，PR 不可合入。

1. 所有寫入 / 刪除 action 必入 audit_log（preview + confirm 兩段）
2. audit_log args / result 必經 redactor；secret / token / webhook payload 全文不得入
3. trace_id 必填（空時填 32-zero + 標 trace_missing）；audit_log 為 trace 鏈第 5 段，不得缺
4. webhook / reconciler / system 來源必填 source 欄位且 actor_user_id 必為 NULL
5. preview / confirm 對應之 audit row 必透過 `preview_id` 關聯
6. partition by month 為硬性；不得 single-table 無 partition
7. 13 個月保留為硬性；超期 partition 必 drop（除 `delete_app` 移 archive）
8. CLI / MCP 端不寫 audit_log；只 backend 寫
9. RBAC：全 team audit 必 admin+；self audit 須 actor 對應 ctx user
10. audit_log 寫入失敗不得 silent；必 log warn + 進 reconciliation_job 重寫
