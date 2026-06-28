# Feature Spec：audit-event-notification

> **狀態**：draft
> **來源**：`docs/trust-and-compliance/plan.md` § 3.2（Audit 缺口「無對外通知 / 無 SIEM 串接」）、§ 5.1（P2 拆解列）；`docs/features/audit-log/spec.md` § 14（v1.1 列「對外 webhook 通知」，本 spec 為其正式化）；`docs/features/threat-model/spec.md` § 5.7 AD3 + § 6（指向本 spec）；HMAC 簽章風格承 `webhook-and-redeploy` spec
> **適用範圍**：重要 audit 事件之對外 outbound webhook 投遞（訂閱、payload、簽章、retry、投遞紀錄、SIEM 串接路徑）；不含 audit 寫入點本身（屬 `audit-log`）、不含 inbound GitHub webhook（屬 `webhook-and-redeploy`）
> **對應**：plan § 5.1 P2；依賴 `audit-log`、`preview-confirm-gate`、`auth-and-rbac`、`secrets-management`、`error-model`、`shared-dto-and-contract`、`rate-limit-and-abuse`

## 1. 結論（先讀本段）

- 本 spec 解 threat-model **AD3**（缺對外可出示證據 / 通知）之「通知」面：enterprise 無法即時知道 `delete_app`、`token_revoke`、`abuse_detected` 等重要事件。export / hash chain（取證面）屬 `audit-export-and-integrity`，本 spec 不重述。
- 機制：重要 audit 事件落地後，**fan-out 到 per-team 訂閱的 outbound webhook**。投遞為**非同步、fire-and-retry**，不阻塞、不回滾觸發事件的主業務流程。
- 不另建第二套事件源：通知**唯一事實來源是 `audit_log`**。`audit.Log()` 寫入成功後，在**同一 DB transaction** 內依訂閱比對寫入 `webhook_delivery`（transactional outbox），保證「audit 有寫到 = 通知不會漏」。背景 dispatcher 負責 HTTP 投遞與 retry。
- 可訂閱事件為 `audit_log.action` 的**重要子集**（§ 5 catalog）；team owner/admin 可配置只訂閱其中部分。
- 簽章承 `webhook-and-redeploy` 之 HMAC-SHA256 風格，但**方向相反**（0ops 為簽章方）：header 帶 `X-0ops-Signature-256`、`X-0ops-Timestamp`（簽進 HMAC 內，接收端可防重放）、`X-0ops-Delivery`（delivery_id，接收端可去重）。
- payload 為**精簡且已 redact 的 audit 事件**：只送 metadata + 白名單 summary，**不送 `args` / `result` 全文**（即便 audit 已 redact，仍二次收斂外送面，承 audit-log redaction 硬規則）。secret / token / webhook 內文絕不外送。
- SIEM 串接：v1 走 webhook（接收端自行轉 Splunk / Datadog）；原生 syslog / JSON push 列 v3 future（§ 11 Open issue）。
- 投遞失敗：標記 + 指數退避 retry（上限後 drop）+ 連續失敗熔斷自動停用訂閱（寫一筆 `webhook_subscription_disabled` audit + alert）。**投遞嘗試本身不入 audit_log**（避免遞迴與噪音），只進 `webhook_delivery` + metrics。

## 2. 範圍

### 2.1 包含

- `internal/server/audit/notify/` package：訂閱 CRUD、catalog 比對、transactional outbox enqueue、background dispatcher、HMAC 簽章、redacted payload 組裝。
- `webhook_subscription`（訂閱設定）與 `webhook_delivery`（投遞紀錄）兩表 + migration `00013`。
- 訂閱管理 API（owner/admin，走 preview-confirm-gate）與投遞紀錄查詢 API。
- Outbound payload DTO 與簽章 header 規約。
- Retry / 熔斷 / idempotency（delivery_id）/ 手動 redeliver。
- 訂閱簽章金鑰之儲存（接 `secrets-management` at-rest 加密）與 rotation。

### 2.2 不包含

- audit 寫入點與 redactor 本體（屬 `audit-log` § 5 / § 8）。
- audit export（CSV / JSON dump）與 tamper-evidence / hash chain（屬 `audit-export-and-integrity`）。
- Inbound GitHub `push` / `installation` webhook（屬 `webhook-and-redeploy`、`github-app-install-flow`）。
- Email / Slack / PagerDuty 原生通道（v1 只 generic webhook；接收端自行轉接）。
- 原生 SIEM push（syslog / CEF / OTLP）：v3 future。
- 通知內容的長期歸檔（投遞紀錄保留期見 § 9）。

## 3. 檔案結構

```
0ops/
├── internal/
│   └── server/
│       ├── audit/
│       │   └── notify/
│       │       ├── catalog.go        # 可訂閱事件 catalog（action → notify event key + 預設摘要器）
│       │       ├── enqueue.go        # transactional outbox：audit.Log 後同 tx 比對訂閱 INSERT webhook_delivery
│       │       ├── dispatcher.go     # 背景 worker：poll pending、投遞、retry 排程、熔斷
│       │       ├── client.go         # outbound HTTP client（timeout、TLS-only、回應大小上限）
│       │       ├── sign.go           # HMAC-SHA256(secret, timestamp + "." + body) + header 組裝
│       │       ├── payload.go        # redacted payload 組裝（白名單欄位 + summary）
│       │       ├── subscription.go   # 訂閱 CRUD service（preview-confirm Action 實作）
│       │       ├── secret.go         # 簽章金鑰產生 / 儲存（接 secrets-management at-rest）/ rotation
│       │       ├── metrics.go
│       │       └── doc.go
│       └── routers/
│           └── webhook_subscriptions.go   # 訂閱 CRUD + 投遞紀錄查詢
├── internal/shared/dto/
│   └── notification.go               # 訂閱 DTO + outbound payload DTO
└── migrations/
    └── 00013_webhook_subscription.sql
```

> `notify` 置於 `audit/` 之下，因其唯一事件源為 audit_log，且 enqueue 必須與 `audit.Log()` 共用同一 transaction（outbox）。dispatcher 為獨立 goroutine，由 backend 主進程在 leader 上啟動（與 reconciler 同模式）。

## 4. Schema

### 4.1 `webhook_subscription`（訂閱設定）

```sql
CREATE TABLE webhook_subscription (
  id                   uuid PRIMARY KEY DEFAULT gen_random_uuid(),
  team_id              uuid NOT NULL REFERENCES team(id) ON DELETE CASCADE,
  url                  text NOT NULL,                          -- 必 https://；不接受 http / 私網位址（SSRF 防護，見 § 6.4）
  events               text[] NOT NULL,                        -- notify event key 子集；CHECK 非空且為 catalog 合法值
  secret_ref           text NOT NULL,                          -- 指向 secrets-management at-rest 加密之簽章金鑰 envelope（見 § 8）
  description          text,
  active               boolean NOT NULL DEFAULT true,          -- owner/admin 可手動停用
  disabled_reason      text,                                   -- 'auto_circuit_breaker' | 'manual' | NULL
  consecutive_failures int  NOT NULL DEFAULT 0,                -- 連續投遞失敗計數（熔斷用）；任一成功歸零
  last_delivery_at     timestamptz,
  created_by           uuid REFERENCES user_account(id),       -- owner/admin user
  created_at           timestamptz NOT NULL DEFAULT now(),
  updated_at           timestamptz NOT NULL DEFAULT now()
);

CREATE INDEX webhook_subscription_team       ON webhook_subscription (team_id);
CREATE INDEX webhook_subscription_team_active ON webhook_subscription (team_id) WHERE active;
-- enqueue 熱路徑：依 team 撈 active 訂閱 + GIN 比對 events 命中
CREATE INDEX webhook_subscription_events_gin ON webhook_subscription USING gin (events);
```

> 每 team 訂閱數上限（v1：10）由 service 層 enforce（避免 fan-out 放大），不寫死於 schema。

### 4.2 `webhook_delivery`（投遞紀錄 / outbox）

```sql
CREATE TABLE webhook_delivery (
  id              uuid NOT NULL DEFAULT gen_random_uuid(),     -- delivery_id；對外 header X-0ops-Delivery，接收端去重
  subscription_id uuid NOT NULL,                               -- → webhook_subscription.id（不設 FK：partition + 訂閱刪除後仍留紀錄）
  team_id         uuid NOT NULL,                               -- denormalize：RBAC / partition
  audit_log_id    bigint NOT NULL,                             -- 對應 audit_log.id（事件源；不外送 args/result）
  event           text NOT NULL,                               -- notify event key
  payload         jsonb NOT NULL,                              -- 已 redact 的 outbound payload 快照（不含 secret）
  status          text NOT NULL DEFAULT 'pending',             -- 'pending' | 'delivering' | 'succeeded' | 'failed' | 'dropped'
  attempt         int  NOT NULL DEFAULT 0,                     -- 已嘗試次數
  max_attempts    int  NOT NULL DEFAULT 6,
  next_attempt_at timestamptz NOT NULL DEFAULT now(),          -- dispatcher poll 依據
  response_status int,                                         -- 接收端 HTTP status（最後一次）
  response_ms     int,                                         -- 最後一次往返毫秒
  error           text,                                        -- 失敗摘要（已 redact、截斷 ≤ 512 byte）
  created_at      timestamptz NOT NULL DEFAULT now(),
  delivered_at    timestamptz,                                 -- 成功或 drop 之 terminal 時間
  PRIMARY KEY (id, created_at)                                 -- partition key 含 created_at
) PARTITION BY RANGE (created_at);

-- 月度 partition（承 audit-log § 4.3 模式）；投遞紀錄保留 90 天（見 § 9）
CREATE INDEX webhook_delivery_due          ON webhook_delivery (next_attempt_at) WHERE status IN ('pending','failed');
CREATE INDEX webhook_delivery_sub_created  ON webhook_delivery (subscription_id, created_at DESC);
CREATE INDEX webhook_delivery_team_created ON webhook_delivery (team_id, created_at DESC);
CREATE UNIQUE INDEX webhook_delivery_dedup ON webhook_delivery (subscription_id, audit_log_id, created_at);
```

> `webhook_delivery_dedup` 保證「同一 audit 事件對同一訂閱只產生一筆 delivery」——enqueue 為冪等，重試 enqueue（如 leader 切換）不會重複 fan-out。

## 5. 可訂閱事件 catalog

### 5.1 catalog（notify event key ↔ `audit_log.action`）

來源為 `audit-log` § 5.1 寫入點中的「重要事件」。catalog 是 audit action 到對外語意的**穩定對應層**；audit action 改名時 catalog 維持對外契約。

| notify event key | 對應 audit `action` | source | 預設摘要（redacted） |
|---|---|---|---|
| `app.deleted` | `delete_app`（confirm success） | user | `App <slug> deleted by <actor>` |
| `token.created` | `token_create` | user | `PAT <name> created by <actor>` |
| `token.revoked` | `token_revoke` | user | `PAT <name> revoked by <actor>` |
| `plan.changed` | `plan_change` | user / system | `Plan <old> → <new>` |
| `abuse.detected` | `abuse_detected` | system | `Abuse signal: <rule>` |
| `reconciler.failed_permanently` | `reconciler_failed_permanently` | reconciler | `App <slug> reconcile failed permanently` |
| `secret.rotated` | `secret_rotate_finalize` | user | `Secret <name> rotation finalized` |
| `member.added` | membership invite accept（subject_type=`membership`） | user | `<github_login> added as <role>` |
| `member.removed` | membership remove（subject_type=`membership`） | user | `<github_login> removed` |
| `member.role_changed` | membership role change（subject_type=`membership`） | user | `<github_login> role <old> → <new>` |
| `domain.unbound` | `domain_grace_unbind`（reconciler）/ 使用者主動 unbind | reconciler / user | `Custom domain <host> unbound` |

> 訂閱設定的 `events` 欄位存 notify event key（非 audit action），由 `catalog.go` 映射。訂閱可只選子集（如只訂 `app.deleted` + `token.revoked` + `abuse.detected`）。

### 5.2 不可訂閱（避免噪音與遞迴）

- 高頻 / 低訊號事件：`*_preview`、`webhook_received`、`redeploy_triggered`、`login` / `logout`、`app_status_change` 之常規轉移 —— v1 不開放訂閱（噪音）。
- **訂閱本身的 config 變動**（`webhook_subscription_*`，§ 7.3）不列入 catalog —— 即便列入也不會遞迴（投遞嘗試不寫 audit，見 § 7.5），但 v1 為單純起見不開放。
- `secret_rotate_start`、`secret_use_failed`、`secret_refresh_failed`：屬營運內部訊號，不外送（避免洩漏 secret 生命週期細節給外部接收端）。

## 6. Outbound webhook 投遞

### 6.1 訂閱設定（per-team）

- 由 team **owner / admin** 設定（§ 10 RBAC）；走 preview-confirm-gate（寫入動作）。
- 欄位：`url`（https）、`events`（catalog 子集）、`description`、`active`。
- 建立 / rotate 時 backend **產生簽章金鑰**（`openssl rand` 等級，base64，≥ 32 byte），**僅在回應中明文回傳一次**供接收端設定；之後存 at-rest 加密、不可再讀（write-only reveal，承 token 不可逆原則）。

### 6.2 Payload DTO（精簡、已 redact、不含 secret）

```go
// internal/shared/dto/notification.go
type NotificationPayload struct {
    DeliveryID  string    `json:"delivery_id"`            // = webhook_delivery.id；接收端去重鍵
    Event       string    `json:"event"`                 // notify event key, e.g. "app.deleted"
    TeamSlug    string    `json:"team_slug"`
    OccurredAt  time.Time `json:"occurred_at"`           // audit_log.created_at（RFC3339）
    Actor       *string   `json:"actor,omitempty"`       // github_login 或 "system:..."；system 事件可為 null
    Source      string    `json:"source"`                // user | webhook | reconciler | system
    SubjectType string    `json:"subject_type"`          // app | token | team | membership | domain | ...
    SubjectID   *string   `json:"subject_id,omitempty"`
    Outcome     string    `json:"outcome"`               // success | failure
    AuditID     int64     `json:"audit_id"`              // 對應 audit_log.id；接收端可回查（需走 audit query API + RBAC）
    TraceID     *string   `json:"trace_id,omitempty"`
    Summary     string    `json:"summary"`               // 白名單組裝之人類可讀摘要（已 redact）
    // 注意：v1 刻意不含 args / result。
}
```

> **redact 二次收斂**：audit_log.args / result 即便已 redact，仍可能含 repo_url、scope 等內部 context；對外面只送上列白名單欄位 + `Summary`。`Summary` 由 catalog 之摘要器以白名單欄位組裝，**不得**插入 args/result 原值或任何 `secret_*` / `token` / `*_signature` 欄位。

### 6.3 簽章（承 webhook-and-redeploy HMAC 風格，方向相反）

請求為 `POST <subscription.url>`，header：

| Header | 值 |
|---|---|
| `Content-Type` | `application/json` |
| `X-0ops-Event` | notify event key |
| `X-0ops-Delivery` | `delivery_id`（UUID）；接收端去重 |
| `X-0ops-Timestamp` | Unix 秒；**簽進 HMAC**，接收端據此拒過舊請求（防重放） |
| `X-0ops-Signature-256` | `sha256=<hex(HMAC-SHA256(secret, timestamp + "." + raw_body))>` |

- 簽章輸入為 `timestamp + "." + raw_body`（綁定 timestamp，較 inbound GitHub 之「只簽 body」更強；inbound 無法控 timestamp，outbound 由 0ops 控）。
- 接收端驗證：`abs(now - X-0ops-Timestamp) ≤ tolerance`（建議 ≤ 5 min）+ constant-time 比對 signature + 以 `X-0ops-Delivery` 去重。**接收端驗證準則寫入 § 12 與接收端文件**，0ops 端只負責正確簽章。
- rotation 期間（§ 8）：以 `current-key` 簽；接收端在 rotation window 內須同時接受新舊（0ops 端提供 rotation 程序文件，類比 `secrets-management` § 5.1 雙 window）。

### 6.4 HTTP client 與 SSRF / 濫用防護

- 僅允許 `https://`；拒私網 / loopback / link-local / metadata（`169.254.169.254`）目的地（SSRF；解析後驗 IP）。
- 連線 timeout 5s、整體 timeout 10s；回應 body 讀取上限 64 KB（只看 status，不解析 body）。
- 2xx = 成功；其餘（含逾時、連線拒絕、TLS 失敗、3xx/4xx/5xx）= 失敗 → 進 retry。
- per-subscription 投遞速率上限（避免對接收端 DoS、避免成 SSRF 放大器）：引用 `rate-limit-and-abuse`，本 spec 不重述閾值。

## 7. 投遞可靠性

### 7.1 Transactional outbox（不漏、不阻塞）

```
audit.Log(ctx, entry)：
  1. INSERT audit_log row              （audit-log 既有路徑）
  2. notify.Enqueue(ctx, tx, auditRow)：同一 tx 內
       a. catalog 比對 entry.action → notify event key（無命中即跳過）
       b. SELECT active subscription WHERE team_id=? AND key = ANY(events)
       c. 對每個命中訂閱 INSERT webhook_delivery(status='pending', payload=已組裝 redacted snapshot)
          （ON CONFLICT DO NOTHING：dedup index 保冪等）
  3. COMMIT
  → 主流程結束；HTTP 投遞由背景 dispatcher 接手
```

- enqueue 只做 INSERT，與 audit 同 tx commit：audit 成功 ⇒ delivery 必已落地（不漏）；audit / 主流程失敗 rollback ⇒ delivery 一併 rollback（不誤送）。
- enqueue **不發 HTTP**、**不等接收端**：對主業務流程零阻塞、零回滾風險。
- enqueue 內部失敗（如 catalog panic）以 defer-recover 隔離：**記 metric + log warn，不影響 audit 寫入與主流程 commit**（通知為盡力交付，audit 為硬保證）。

### 7.2 Dispatcher（背景 worker，leader-only）

```
loop（每 ~2s 或事件喚醒）：
  SELECT ... FROM webhook_delivery
    WHERE status IN ('pending','failed') AND next_attempt_at <= now()
    ORDER BY next_attempt_at FOR UPDATE SKIP LOCKED LIMIT N
  對每筆：
    status='delivering'
    POST（§ 6.3 簽章）
    成功（2xx）：status='succeeded'、delivered_at=now()、subscription.consecutive_failures=0
    失敗：
      attempt++
      若 attempt >= max_attempts：status='dropped'、delivered_at=now()
      否則：status='failed'、next_attempt_at = now() + backoff(attempt)
      subscription.consecutive_failures++ → 觸發熔斷判斷（§ 7.4）
```

- `FOR UPDATE SKIP LOCKED`：多 replica 安全（即便非 leader-only 亦不重送）。

### 7.3 Retry（指數退避 + 上限）

| attempt | 下次間隔 |
|---|---|
| 1 → 2 | 1 min |
| 2 → 3 | 5 min |
| 3 → 4 | 30 min |
| 4 → 5 | 2 h |
| 5 → 6 | 6 h |
| 6（達 max_attempts） | drop（`status='dropped'`） |

- 加 ±10% jitter 避免同步重試風暴。
- drop 不重試；可由 owner/admin 手動 redeliver（§ 7.6）。

### 7.4 失敗處理與熔斷

- 連續失敗（`consecutive_failures`）達閾值（v1：20，跨多事件累計）→ 自動停用訂閱：`active=false`、`disabled_reason='auto_circuit_breaker'`。
- 熔斷時寫**一筆** audit `webhook_subscription_disabled`（source=`system`、subject=該訂閱）+ alert（接 observability）。此為 config 狀態變更，**非投遞紀錄**，故入 audit 合理且不遞迴。
- 任一次投遞成功即 `consecutive_failures=0`（半開恢復）。
- 失敗不阻塞、不回滾任何主業務流程；最壞情況為通知 drop + 訂閱被熔斷停用。

### 7.5 Idempotency 與「投遞不入 audit」

- **接收端去重**：`X-0ops-Delivery`（= `delivery_id`）穩定；retry 用同一 `delivery_id` 與同一 body/簽章，接收端據此去重。
- **enqueue 去重**：`webhook_delivery_dedup` unique index，同 audit 事件對同訂閱僅一筆。
- **投遞嘗試本身不寫 `audit_log`**：只更新 `webhook_delivery`（status/attempt/response）+ metrics。理由：(1) 防遞迴（若投遞寫 audit，又可能觸發通知…）；(2) 防噪音（每次 retry 一筆 audit）。投遞**結果**之可觀測性由 `webhook_delivery` 表 + § 13 SLI 提供，不污染業務帳本。

### 7.6 手動 redeliver

- owner/admin 可對 `failed` / `dropped` / `succeeded` 之 delivery 觸發 redeliver：複製一筆新 `webhook_delivery`（新 `delivery_id`，同 `audit_log_id` + `subscription_id`，`status='pending'`），由 dispatcher 重投。
- redeliver 為 config 操作 → 入 audit `webhook_redeliver`（user）；投遞本身仍不入 audit。

## 8. 簽章金鑰儲存與 rotation

- 每訂閱一把獨立簽章金鑰；**per-subscription 數量不可控 → 不入 K8s Secret**，改走 `secrets-management` 之 **at-rest DB 加密**機制（與客戶 token 同等：加密儲存，`secret_ref` 指向 envelope）。
- 產生：建立 / rotate 訂閱時 backend 端產生（≥ 32 byte 隨機），明文僅回應一次（write-only reveal），落地僅存密文。
- rotation：`POST .../{id}/rotate-secret`（owner/admin、preview-confirm）。回應新金鑰一次；提供雙 window 程序文件（接收端在 window 內同時接受新舊金鑰簽章），語意類比 `secrets-management` § 5.1（A 類）。
- 金鑰絕不出現在 payload、log、audit、metrics、error envelope（承 redactor 規則）。

## 9. 投遞紀錄保留期

- `webhook_delivery` partition by month（承 audit-log § 4.3）；保留 **90 天**後 drop（投遞紀錄為營運除錯用，非合規帳本；合規帳本是 `audit_log`，保留 13 個月）。
- 不歸檔（與 audit `delete_app` 永久保留不同）；需更長保存者透過接收端 SIEM 落地。

## 10. RBAC

| 操作 | 最低 role | scope |
|---|---|---|
| 建立 / 更新 / 刪除訂閱、rotate-secret | owner / admin | `webhook:write` |
| 列出 / 檢視訂閱設定（不含金鑰明文） | admin | `webhook:read` |
| 查投遞紀錄（`webhook_delivery`） | admin | `webhook:read` |
| 手動 redeliver | owner / admin | `webhook:write` |

- member / viewer 不得設定或檢視 webhook（含投遞紀錄）；違者 `403 forbidden_role`。
- 簽章金鑰：建立 / rotate 之**回應**明文回傳一次；任何 GET 路徑**不**回金鑰（write-only reveal）。
- 金鑰 at-rest 儲存與存取限制接 `secrets-management`；backend 以 `secret_ref` 解密，僅在簽章時於記憶體使用。
- middleware 在 router 端宣告（承 `auth-and-rbac` § 6）；新增 scope `webhook:read` / `webhook:write` 須同步 `auth-and-rbac` scope 矩陣與整合測試。

## 11. SIEM 串接路徑

- **v1**：generic outbound webhook。接收端（客戶自建 receiver 或 iPaaS）負責轉送至 Splunk HEC / Datadog Logs / Elastic 等。0ops 不直連 SIEM。
- **v3 future（Open issue）**：原生 push——syslog（RFC 5424）/ CEF / OTLP logs。需評估 per-tenant 連線管理、TLS client cert、背壓。本 spec **不**實作；於 § 14 與 plan § 3.2「SIEM 串接 P3」對齊登記。

## 12. 與其他 spec 接合點

| 接合 | spec |
|---|---|
| 事件源（唯一）：audit_log 寫入點 + action 命名 | `audit-log` § 5.1 |
| redactor（payload 二次收斂共用判定） | `error-model` § 9 / `audit-log` § 8 |
| 訂閱 CRUD / rotate-secret 走 preview-confirm | `preview-confirm-gate` |
| RBAC（owner/admin + scope `webhook:read/write`） | `auth-and-rbac` § 6 |
| 簽章金鑰 at-rest 加密 + rotation 雙 window 語意 | `secrets-management`（at-rest 加密 / § 5.1 A 類程序） |
| HMAC-SHA256 簽章風格（方向相反） | `webhook-and-redeploy` § 4.2 |
| 投遞速率上限 / SSRF 濫用防護閾值 | `rate-limit-and-abuse` |
| dispatcher leader 啟動 / 收斂模式 | `reconciler-and-incident` |
| 解 threat-model AD3（通知面） | `threat-model` § 5.7 / § 6 |
| 失敗碼（`webhook_url_invalid` / `forbidden_role` 等） | `error-model` § 5.3 |
| 取證 export / hash chain（互補、非本 spec） | `audit-export-and-integrity` |

## 13. SLI 對應

| SLI | 目標 | 量測 |
|---|---|---|
| 通知投遞成功率（max_attempts 內） | > 99% / 28d | `0ops_webhook_delivery_total{status=succeeded} / (succeeded+dropped)` |
| audit → 首次投遞延遲 | p95 < 30s | `first_attempt_at - audit_log.created_at` |
| enqueue 不漏（outbox 一致性） | 100% | 命中訂閱之 audit 事件數 = 對應 delivery 數（對帳） |
| 訂閱熔斷率 | < 1% 訂閱 / 28d | `auto_circuit_breaker` 停用比例 |
| payload redaction 無洩漏 | 0 違規 | 靜態白名單測試 + 隨機抽樣掃描（無 `secret_`/`token`/`_signature`） |

## 14. 驗證準則

| 驗證項 | 方式 | 通過條件 |
|---|---|---|
| 事件觸發投遞 | 跑 `delete_app` confirm（team 有訂 `app.deleted`） | 同 tx 產生 `webhook_delivery` pending；dispatcher POST 命中 receiver |
| 未訂閱不投遞 | team 只訂 `app.deleted`，跑 `token_revoke` | 不產生 delivery |
| 訂閱子集 | events=`["abuse.detected"]`，發 `delete_app` | 不投遞；發 `abuse_detected` 才投遞 |
| 簽章可驗 | receiver 用 secret 重算 `HMAC(timestamp+"."+body)` | 與 `X-0ops-Signature-256` 相符 |
| Timestamp 防重放 | receiver 收舊 timestamp（> tolerance） | receiver 依準則可拒（0ops 端 timestamp 已簽進 HMAC） |
| Idempotency | 同一事件 retry 兩次 | `X-0ops-Delivery` 不變；receiver 去重；不重複 enqueue（dedup index） |
| Retry 後成功 | receiver 前兩次回 500、第三次 200 | delivery 走 backoff 後 `status=succeeded`、attempt=3 |
| Retry 上限 drop | receiver 持續 500 | attempt 達 6 後 `status=dropped`，不再投遞 |
| 失敗不阻塞主流程 | receiver 全 timeout，跑 `delete_app` | `delete_app` 仍 success commit；delivery 標 failed/dropped；無回滾 |
| enqueue 失敗隔離 | mock catalog panic | audit_log 仍寫入、主流程 commit；metric 記 enqueue_error |
| Redact 生效 | 訂 `token.created`，payload 檢查 | 無 `token` / `secret_*` / `*_signature`；無 args/result 全文；只白名單欄位 |
| 熔斷自動停用 | receiver 連續失敗達閾值 | 訂閱 `active=false`、`disabled_reason=auto_circuit_breaker`；寫一筆 `webhook_subscription_disabled` audit |
| 投遞不入 audit | 觀察 audit_log | 投遞 attempt / 結果無對應 audit row（只 config 變更 + 熔斷 + redeliver 入 audit） |
| RBAC 寫 | member 嘗試建訂閱 | `403 forbidden_role` |
| RBAC 讀投遞紀錄 | viewer 查 deliveries | `403 forbidden_role` |
| 金鑰 write-only | GET 訂閱 | 回應不含金鑰明文；只建立 / rotate 回一次 |
| SSRF 拒私網 | url=`http://169.254.169.254/...` 或 `http://` | preview 階段 `422 webhook_url_invalid` |
| 手動 redeliver | 對 dropped delivery redeliver | 產生新 delivery_id、status=pending；入 `webhook_redeliver` audit |
| 投遞紀錄保留 | 91 天後 | 舊 partition drop；audit_log 不受影響（13 月） |

## 15. 對 `docs/0ops-plan.md` 的修改清單

1. 「DB schema」：新增 `webhook_subscription`、`webhook_delivery`（partitioned）兩表。
2. 「Auth & RBAC」scope 清單：新增 `webhook:read` / `webhook:write`，補 owner/admin 限定。
3. 「Tool catalog」：v1 不開放 MCP 設定 webhook（避免 agent 自設外送通道擴大攻擊面）；列為 v2 評估（§ 16 Open issue）。
4. `docs/features/audit-log/spec.md` § 14：將「對外 webhook 通知（v1.1）」標註指向本 spec 為正式化文件。
5. `docs/trust-and-compliance/plan.md` § 3.2 / § 5.1：本 spec 由 plan 化為 draft。

## 16. Open issues

- MCP 是否開放 `manage_webhook_subscription` write tool：v1 否（agent 自設外送通道 = 資料外流面，需高風險二次確認；接 `security-hardening`）；v2 評估。
- 原生 SIEM push（syslog / CEF / OTLP）：v3；per-tenant 連線、TLS client cert、背壓未定（plan § 3.2 P3）。
- Email / Slack / PagerDuty 原生通道：v1 靠 generic webhook + 接收端轉接；需求明確再評估原生通道。
- payload 是否提供 opt-in 帶 redacted `args` 摘要：v1 一律不帶（最保守）；enterprise 若要更多 context，評估「白名單 per-action 欄位」而非整包 args。
- 熔斷閾值（20）與 backoff 階梯：v1 固定；v1.1 評估 per-subscription 可調 + 熔斷後自動半開探測。
- 投遞紀錄 90 天保留是否足夠對帳：與 audit 13 月不一致是刻意（投遞紀錄非合規帳本）；若 enterprise 要求一致，評估延長或將 delivery outcome 摘要回寫 audit（須先解遞迴顧慮）。
- `webhook_delivery` 與 `audit_log` 之 outbox 對帳 job（偵測命中訂閱卻無 delivery 的漏洞）：建議 reconciler 週期對帳；歸屬 `reconciler-and-incident` 或本 spec 之 dispatcher 待定。
- receiver mTLS / 自帶 CA：v1 只標準 TLS 驗 server；client cert / pinning 列 v2。

## 17. 不可違反的硬性規則

> 違反以下任一項，PR 不可合入。

1. 通知事件源唯一為 `audit_log`；不得另建平行事件源或繞過 audit 直接投遞。
2. outbound payload 必經 redact，且**不得**含 `args` / `result` 全文、`secret_*` / `password` / `token` / `*_signature` 任一欄位；簽章金鑰絕不外送、不入 log / audit / metric / error。
3. 投遞 enqueue 必與 `audit.Log()` 同一 transaction（transactional outbox）；audit 成功而 delivery 漏寫，或 delivery 寫成功而 audit rollback，皆不可。
4. 投遞失敗（含逾時 / 熔斷 / drop）不得阻塞或回滾觸發事件之主業務流程（fire-and-retry，非同步）。
5. 投遞嘗試本身不得寫 `audit_log`（防遞迴 + 噪音）；只更新 `webhook_delivery` + metrics。config 變更、熔斷停用、手動 redeliver 入 audit。
6. 每次投遞必帶 `X-0ops-Signature-256`（HMAC over `timestamp + "." + body`）、`X-0ops-Timestamp`、`X-0ops-Delivery`；缺一不可，不得發未簽章請求。
7. 同一 audit 事件對同一訂閱僅一筆 delivery（dedup index）；retry 用同一 `delivery_id` 供接收端去重。
8. 訂閱 url 必 `https://` 且非私網 / loopback / metadata 位址（SSRF）；違者 preview 拒 `422 webhook_url_invalid`。
9. 訂閱 CRUD / rotate-secret 為 owner/admin + scope `webhook:write` 之 preview-confirm 寫入動作；投遞紀錄讀取為 admin + `webhook:read`。
10. 簽章金鑰走 `secrets-management` at-rest 加密；明文僅建立 / rotate 回應一次（write-only reveal），任何 GET 不回金鑰。
11. `webhook_delivery` partition by month、保留 90 天；不得當作合規帳本取代 `audit_log`（13 月）。
12. dispatcher 為背景非同步投遞；retry 必指數退避 + 上限後 drop，連續失敗必熔斷（不得無限重試耗資源 / 不得對接收端構成 DoS）。
