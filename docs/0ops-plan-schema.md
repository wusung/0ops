## DB schema

> **下游 spec schema 補丁清單**（v1 起手 schema 為以下 sql block；下列 spec 各自規範須補的欄位 / 表，依 milestone 上線時回填本段）：
>
> - `cli_token`：補 `kind text not null check(kind in ('device','pat','ops_token')) default 'pat'`（`docs/features/auth-and-rbac/spec.md` § 4.3、`docs/features/build-pipeline-and-callback/spec.md` § 5.2）
> - `app`：補 `status text not null default 'live' check(status in ('live','paused','deleting','delete_compensated'))`（`docs/features/delete-app-flow/spec.md` § 11、`docs/features/webhook-and-redeploy/spec.md` § 7）
> - `domain_binding`：補 `is_apex bool`、`extends_used int default 0`、`health_check_failed_at timestamptz`、`status text not null default 'pending'`（`docs/features/custom-domain-and-verify/spec.md` § 4.1）
> - `deploy_run`：補 `scan_high int`、`scan_critical int`、`gitops_commit_sha text`、`source text not null default 'user'`、`webhook_delivery_id text`；加 CHECK constraint `failure_classification IS NOT NULL` for final states（`docs/features/build-pipeline-and-callback/spec.md` § 11、`docs/features/webhook-and-redeploy/spec.md` § 12、`docs/features/reconciler-and-incident/spec.md` § 6.3）
> - `reconciliation_job`：補 `trace_id text`、`status text not null default 'pending'`（`docs/features/reconciler-and-incident/spec.md` § 4）
> - `audit_log`：補 `source text not null default 'user'`、`outcome text not null default 'success'`、`http_status int`；改為 partition by month（`docs/features/audit-log/spec.md` § 4.1, § 4.3）
> - `team`：補 `plan` 之 CHECK constraint `plan in ('free','starter','pro','team')`；`free` 必對應 personal team（`slug LIKE 'personal-%'`）—— ADR-0011 § 4
> - 新增 `incident` 表（`docs/features/reconciler-and-incident/spec.md` § 9.1）
> - 新增 `audit_log_archive` 表（保留 `delete_app` 永久不刪之 row；`docs/features/audit-log/spec.md` § 9.2）

```sql
-- 租戶與身份
user_account(id uuid pk, github_login citext unique, email citext,
             created_at timestamptz)

team(id uuid pk, slug citext unique, name text,
     plan text not null default 'free',
     github_install_id bigint,                          -- App install 掛 team，不掛 user
     created_at timestamptz, archived_at timestamptz)

team_membership(team_id uuid fk, user_id uuid fk,
                role text not null check(role in ('owner','admin','member','viewer')),
                invited_at timestamptz, joined_at timestamptz,
                primary key(team_id, user_id))

-- 主要 entity（slug 在 team 內唯一）
app(id uuid pk, team_id uuid fk not null,
    slug citext not null, name text,
    repo_url text, repo_default_branch text,
    image_ref text, builder text,
    created_by uuid fk references user_account(id),
    status text, created_at timestamptz, updated_at timestamptz,
    unique(team_id, slug))

domain_binding(id uuid pk, app_id uuid fk, team_id uuid fk not null,
               hostname citext unique,
               kind text check(kind in ('primary','extra')),
               verified bool, verification_token text,
               cf_hostname_id text, cf_dns_record_id text,
               expires_at timestamptz,                  -- pending domain TTL（24h，可手動 extend）
               created_at timestamptz, verified_at timestamptz)

-- Deploy 狀態機 + 失敗分類（DORA / CFR 量測來源）+ metering 預埋
deploy_run(id uuid pk, app_id uuid fk, team_id uuid fk not null,
           commit_sha text, ref text, workflow_run_id bigint,
           status text not null,                         -- queued|building|pushing|rendering|syncing|live|failed|compensating|rolled_back
           failure_classification text,                  -- buildpack_detect_failed|build_compile_error|build_timeout|registry_push_failed|gitops_push_conflict|argo_sync_timeout|health_check_failed|cloudflare_api_failed|unknown
           trace_id text,                                -- OTel trace_id
           events jsonb not null default '[]',           -- 階段 transition + timestamps
           -- metering（v1 只記錄，v2 才用於計費）
           build_minutes numeric(10,2),                  -- GHA build 耗時
           image_size_bytes bigint,                      -- pack build output 大小
           started_at timestamptz, finished_at timestamptz, error_summary text)

-- 用量採樣（v1 寫入，v2 才暴露 query；計費鋪路）
usage_sample(id bigserial pk, team_id uuid fk not null, app_id uuid fk,
             sampled_at timestamptz default now(),
             cpu_millicores int, memory_bytes bigint,
             active bool,                                -- pod ready & ingress 有流量
             egress_bytes bigint)
-- 採樣頻率：每 5 min 由 reconciler 從 K8s metrics-server 拉
-- 保留：30 天熱資料 + 物化 daily aggregate 永存（與 deploy_run 一致）

-- 兩階段寫入 + idempotency
preview(id uuid pk, team_id uuid fk not null,
        actor_user_id uuid fk not null references user_account(id),
        action text, args jsonb, action_summary text, side_effects jsonb,
        idempotency_key text,                            -- 預設 = preview_id
        last_result jsonb,                               -- consumed 後存執行結果，重試直接回該值
        expires_at timestamptz, consumed_at timestamptz, created_at timestamptz,
        unique(team_id, idempotency_key))

-- 認證
cli_token(id uuid pk,
          owner_user_id uuid fk not null references user_account(id),
          team_id uuid fk not null,                      -- token 綁定 team，不可跨 team
          token_hash text, name text,
          scopes text[] not null,                        -- {'apps:read','apps:write','domains:write',...}
          last_used_at timestamptz, created_at timestamptz, revoked_at timestamptz)

-- Webhook 防重放
webhook_dedup(provider text, delivery_id text,
              received_at timestamptz default now(),
              primary key(provider, delivery_id))        -- TTL 24h（背景清理）

-- 稽核（actor vs subject 區分）
audit_log(id bigserial pk, team_id uuid fk not null,
          actor_user_id uuid fk references user_account(id),
          subject_type text, subject_id uuid,
          action text, args jsonb, result jsonb,
          preview_id uuid, trace_id text,
          created_at timestamptz)

-- Reconciler 收斂工作（compensation）
reconciliation_job(id uuid pk, team_id uuid fk not null,
                   subject_type text, subject_id uuid,
                   kind text,                            -- e.g. deploy_status_pull, cloudflare_state_sync
                   attempts int default 0, next_attempt_at timestamptz,
                   payload jsonb, last_error text,
                   created_at timestamptz, completed_at timestamptz)
```

**索引與隔離**：
- 所有 query 走 sqlc 模板強制 `WHERE team_id = $1`；無 `team_id` 的 query 不存在於 codegen 輸出
- `app(team_id, slug)`、`preview(team_id, idempotency_key)`、`domain_binding(hostname)`：多租戶安全鎖定
- `deploy_run(team_id, app_id, started_at desc)`：DORA aggregate query 性能

**Migration policy**：`goose` 或 `golang-migrate`，檔放 `migrations/`。Schema 變更走多步 zero-downtime 範本：
1. 新欄位先 `add column ... null`
2. 雙寫 + backfill
3. 切換讀路徑
4. 標記舊欄位 deprecated（CI lint 警告）
5. 下一個 release drop column

**保留期**：
- `audit_log` 保留 13 個月（合規最小值）；之後 partition drop
- `deploy_run` 保留 90 天熱資料 + 物化 monthly aggregate 永存
- `usage_sample` 保留 30 天熱資料 + 物化 daily aggregate 永存
- `webhook_dedup` 24h 滾動清理
- `preview` 過期 + consumed 後 7 天清
- `reconciliation_job` completed 後 7 天清；`failed_permanently` 保留 30 天供 root-cause

## 資料分類 + 保留（合規錨點）

> **來源**：`docs/features/compliance-framework-mapping/spec.md` § 4（四級分類）+ § 9（統一保留矩陣）。
> 本區為 schema 側的雙向可查錨點，讓「資料分類 已具備」之主張可由 schema 直接對照；分類標準與處理規則之單一事實來源仍為 compliance spec，本區僅鏡射、不另行裁決。

**四級分類**（public / internal / customer / secret，對齊 compliance spec § 4.1）：

| 級別 | 定義 | log / audit 明文 | at-rest 加密 | 對應 schema 欄位（例） |
|---|---|---|---|---|
| **public** | 公開即無損：repo URL、app slug、公開域名 | 可 | 不要求 | `app.slug`、`domain_binding.hostname` |
| **internal** | 內部營運，外洩有限影響：desired state、deploy_run、metric label | 可（必要時 redact） | 建議（DB at-rest） | `deploy_run.*`、`reconciliation_job.*`、`audit_log.*` |
| **customer** | 客戶資產與 PII：repo 原始碼/artifact、github_login、email | 僅 id / 摘要 | 強制 | `user_account.github_login`、`user_account.email` |
| **secret** | 憑證與金鑰：PAT/device token、App token、CF token、app env secret | 禁止明文（redactor 強制） | 強制（加密 + token argon2 雜湊） | `cli_token.token_hash`（僅雜湊，不存明文） |

**各級保留期**（對齊 compliance spec § 9 + 上節「保留期」）：

| 資料類別 | 分類 | 保留期 |
|---|---|---|
| `audit_log` | internal（取證） | 13 個月；`delete_app` 對應永久移 `audit_log_archive` |
| `deploy_run` | internal | 90 天熱資料 + monthly aggregate 永存 |
| `usage_sample` | internal | 30 天熱資料 + daily aggregate 永存 |
| secret（`cli_token` 等） | secret | 不保留明文；TTL `expires_at` / rotation 兩段 |
| PII—`audit_log` 內（github_login/email 欄） | customer（PII） | 隨 `audit_log` 13 個月 |
| PII—`user_account`（github_login/email） | customer（PII） | 帳號生命週期；刪除流程未釘定（PDPA 刪除權，compliance spec § 6 / § 14 Open issue） |
| gitops desired state | internal | 永久（git history 不可變） |

