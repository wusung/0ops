# audit-event-notification 實作計畫（M9.6）

> 來源 spec：`docs/features/audit-event-notification/spec.md`（單一事實來源）。
> 本檔為可執行計畫，鎖定非平凡設計決策與 TDD 任務拆解。

**Goal：** audit_log 重要事件落地後，於同一 DB transaction enqueue 到 per-team 訂閱的 outbound webhook（transactional outbox），背景 dispatcher 非同步投遞 + retry + 熔斷。

**Tech Stack：** Go、pgx v5、chi router、goose migration、HMAC-SHA256、partitioned table。

---

## 非平凡設計決策（落定，動工依此）

1. **enqueue 寄生在 audit 寫入既有 tx（不新增 LogTx）**
   `db.Repository.InsertAuditLog` 已自開一個 tx（chain-head lock → allocate id → INSERT → advance head → COMMIT）。outbox enqueue 直接放進**這個既有 tx**，在 `INSERT audit_log` 之後、`COMMIT` 之前呼叫注入的 `AuditEnqueuer`。
   - 滿足 hard rule #3（同 tx）、#4（非阻塞，enqueue 只 INSERT 不發 HTTP）。
   - **零呼叫端改動**：所有現有 `audit.Log()` caller 不動。
   - 注入方式：`Repository` 增 `auditEnqueuer AuditEnqueuer` 欄位（nil-safe，預設 nil → 不 enqueue，維持既有測試行為）。介面 + event struct 定義在 `db` package（避免 `db`→`notify` 反向 import）；`notify` 實作該介面。

2. **enqueue 失敗隔離（§7.1 / hard rule 之 audit 硬保證）**
   catalog 比對 + payload 組裝為純 Go，以 `defer-recover` 包住；panic → 記 metric + log warn，**回傳 nil**（不影響 audit commit）。實際 `INSERT webhook_delivery` 的 DB 錯誤才回傳 error（屬真 tx 故障，audit 連帶 rollback，caller 重試）。

3. **partitioned table 索引 vs migrationlint R1**
   `TestRepoMigrationsPassLint`（repo_test.go）會掃真實 migrations dir，floor=9，**所有 `CREATE INDEX` 必含 CONCURRENTLY**。但 PG 不允許在 partitioned **parent** 上 `CREATE INDEX CONCURRENTLY`。
   - 解法：migration 用 `-- +goose NO TRANSACTION`（同 00017）；secondary / unique 索引**逐 partition** 建（partition 子表是普通表，可 `CREATE INDEX CONCURRENTLY`，每句含 CONCURRENTLY → 通過 lint）。
   - dedup 唯一性：每 partition 建 `UNIQUE INDEX CONCURRENTLY (subscription_id, audit_log_id)`；**不依賴 parent-level ON CONFLICT arbiter**（partitioned parent 無此 index）。
   - enqueue 冪等：plain `INSERT INTO webhook_delivery`，捕捉 pgconn 23505（unique_violation）視為 no-op（等價 ON CONFLICT DO NOTHING）。設計上同 audit_log_id 不會雙發（nextval 每次新 id；audit insert 原子且不以同 id 重放），唯一索引為 hard rule #7 的 defense-in-depth。

4. **partition seed + DEFAULT 兜底**
   migration pre-create 月 partition（2026-05..2026-12）+ 一個 `DEFAULT` partition（retention/rollover 排程 deferred，DEFAULT 確保任意日期 insert 不失敗）。每個 partition（含 default）建索引。

5. **簽章金鑰 at-rest 加密 deferred（secrets-management 本體未在 repo）**
   `SecretStore` 介面（`Put(ctx, subID, key) (ref, err)` / `Get(ctx, ref) (key, err)`）。v1 提供 `dbSecretStore`：金鑰存於 `webhook_subscription.secret_ref` 對應的密文欄位——**v1 暫存明文 base64 並於程式碼/文件明標 `DEFERRED: at-rest envelope encryption pending secrets-management`**（hard rule #10 的加密面 deferred，誠實標註，不宣稱已加密）。write-only reveal：建立 / rotate 回應明文一次，GET 不回。

6. **dispatcher 認領用 `FOR UPDATE SKIP LOCKED`**（spec §7.2 明列；reconciler 既有用 conditional UPDATE，但 spec 要 SKIP LOCKED，多 replica 安全）。leader gate 沿用 `reconciler.Leader` 介面。

7. **catalog 為穩定對應層**：`audit_log.action` → notify event key（§5.1）。enqueue 比對 entry.action，無命中即跳過。

---

## 範圍切分（deferred，誠實標註）

- §8 DB at-rest envelope 加密：介面就緒，v1 明文暫存（deferred）。
- §9 retention drop 排程：partition 結構就緒 + DEFAULT 兜底，rollover job 不接（deferred）。
- §11 native SIEM push（v3）/ §16 MCP write tool：不做。

---

## 檔案結構

```
src/migrations/00018_webhook_subscription_and_delivery.sql      # 兩表 + 月 partition + 逐-partition CONCURRENTLY 索引
src/internal/shared/dto/notification.go                         # 訂閱 DTO + outbound payload DTO
src/internal/shared/rbac/scope.go (+)                           # webhook:read / webhook:write
src/internal/shared/rbac/action.go (+)                          # ListWebhooks / ManageWebhooks / ReadWebhookDeliveries / RedeliverWebhook
src/internal/server/services/audit/notify/
    catalog.go        # action→event key 映射 + 預設摘要器（純函式）
    catalog_test.go
    payload.go        # redacted payload 組裝（白名單，無 args/result/secret/token/signature）
    payload_test.go
    sign.go           # HMAC-SHA256(secret, ts + "." + body) + 三 header 組裝
    sign_test.go
    ssrf.go           # https-only + 拒私網/loopback/link-local/metadata
    ssrf_test.go
    secret.go         # SecretStore 介面 + 金鑰產生（≥32B）+ v1 dbSecretStore（at-rest deferred）
    secret_test.go
    enqueue.go        # AuditEnqueuer 實作：tx 內 catalog 比對 + SELECT 訂閱 + INSERT delivery（recover 隔離）
    dispatcher.go     # 背景 worker：SKIP LOCKED poll + 投遞 + retry backoff + 熔斷
    dispatcher_test.go
    backoff.go        # 指數退避階梯 + jitter（純函式）
    backoff_test.go
    subscription.go   # 訂閱 CRUD service（preview-confirm Action）+ redeliver
    actions.go        # audit action 常數（webhook_subscription_disabled / webhook_redeliver）+ event keys
    metrics.go        # MetricObserver 介面 + Nop
    doc.go
src/internal/server/db/audit.go (+)                             # AuditEnqueuer 介面 + InsertAuditLog 注入呼叫
src/internal/server/db/webhook_notify.go                        # delivery / subscription repo 查詢（dispatcher poll、CRUD、redeliver）
src/internal/server/db/webhook_notify_test.go                   # DB 整合測（outbox / dispatcher poll / dedup / 熔斷）
src/internal/server/webhook_subscriptions.go                    # router + handlers（CRUD + deliveries 查詢 + rotate-secret + redeliver）
src/internal/server/webhook_subscriptions_test.go               # httptest（RBAC / preview-confirm / SSRF 422 / write-only）
src/internal/shared/rbac/webhook_test.go                        # scope 矩陣 contract test
```

## TDD 任務順序（依賴序）

T1 schema migration（+ migrationlint pass + DB 整合測：表存在、dedup 唯一、partition routing）
T2 catalog（純函式 + test）
T3 payload redact（純函式 + test：無禁用欄位、無 args/result）
T4 sign（純函式 + test：HMAC over ts.body、三 header）
T5 ssrf（純函式 + test：https-only、拒私網/metadata → invalid）
T6 backoff（純函式 + test：階梯 + jitter 邊界）
T7 secret（介面 + 產生 ≥32B + write-only + test）
T8 RBAC scope/action（+ contract test）
T9 DTO（訂閱 + payload）
T10 AuditEnqueuer 介面 + InsertAuditLog 注入 + enqueue 實作（DB 整合測：audit 成功⇒delivery；rollback⇒一併；未訂閱不投；子集；panic 隔離）
T11 dispatcher 狀態機（DB poll SKIP LOCKED + httptest mock receiver：成功/retry/drop/熔斷寫 audit）
T12 subscription CRUD service（preview-confirm + redeliver）+ router/handlers（httptest：RBAC 403、SSRF 422 preview、write-only reveal、deliveries 讀）
T13 main 接線（enqueuer 注入 repo、dispatcher 起 goroutine、router 掛載）
T14 文件：plan 收尾 + spec §15 對 0ops-plan 的修改、release migration doc
T15 verification-before-completion：`./manage.sh test` 綠 + 改 task-status M9.6 Done
