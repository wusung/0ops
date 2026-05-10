# Feature Spec：rate-limit-and-abuse

> **狀態**：draft
> **來源**：`docs/0ops-plan.md`「Rate limit & abuse 偵測」段；ADR-0006（preview consumption rate 紅旗指標）；本 spec 依賴 `auth-and-rbac`、`error-model`、`observability-skeleton`
> **適用範圍**：per-token / per-team rate limit、429 回應、abuse 偵測規則；不含 GitHub / Cloudflare 對外 rate limit（屬 `winshare-subdomain-and-tunnel` 與 `github-app-install-flow` spec）
> **對應 Milestone**：M4

## 1. 結論（先讀本段）

- Rate limit 採 token bucket（`golang.org/x/time/rate`）；in-memory + per-key
- 三層 limit（依 plan tier）：per-token、per-team、per-action（preview 創建）
- 超限回 `429 rate_limited` + HTTP header `Retry-After: <seconds>` + envelope details
- v1 不持久化（重啟後重置）；M5 多 replica 後採 token bucket per pod（容忍 N 倍誤差，N=replica 數）
- Abuse 偵測：v1 量測 + audit；v1.1 自動阻擋
- 三條偵測規則：跨 ASN、同 IP 跨 team 401、preview create/consume ratio
- CLI 收 429 自動退避（指數退避 + jitter）；MCP 端 LLM 自行決定（SKILL.md 指引）
- preview 創建 limit 為 abuse 主防線：避免 LLM 失控連發 preview

## 2. 範圍

### 2.1 包含
- `internal/server/middleware/ratelimit.go`：chi middleware
- 三層 token bucket 配置與容量計算
- 429 response 格式（含 `Retry-After`）
- Abuse 偵測背景 goroutine
- audit_log 對 abuse 事件之記錄
- CLI 自動退避邏輯
- Metric `0ops_rate_limit_triggered_total`
- Per-tier 配額表（待 ADR-0011 拍板）

### 2.2 不包含
- 對 GitHub / Cloudflare 之 outbound rate limit（屬其各自 spec）
- WAF / DDoS（屬 Cloudflare edge；v1 用 Cloudflare 預設）
- IP 永久封鎖（v1 不做；v1.1 評估）
- 自動 token 撤銷（v1.1）

## 3. 檔案結構

```
0ops/
└── internal/
    └── server/
        ├── middleware/
        │   └── ratelimit.go               # chi middleware
        ├── services/
        │   └── ratelimit/
        │       ├── bucket.go              # token bucket per-key
        │       ├── limiter.go             # 三層整合
        │       ├── abuse_detector.go      # 背景 goroutine
        │       ├── metrics.go
        │       └── doc.go
        └── routers/
            └── *.go                       # 各 router 在註冊時宣告所屬類別（read / write / preview-create）
```

## 4. 三層 rate limit

### 4.1 配額表（source: ADR-0011 § 3.1）

| 範圍 | 操作類別 | free | starter | pro | team |
|---|---|---|---|---|---|
| **per-token** | read | 300/min | 600/min | 2400/min | 6000/min |
| per-token | write（preview/confirm 算同一） | 30/min | 60/min | 240/min | 600/min |
| per-token | preview-create（subset of write）| 10/min | 30/min | 120/min | 300/min |
| **per-team** | write 合計 | 60/min | 300/min | 1200/min | 3000/min |
| per-team | preview-create 合計 | 10/min | 30/min | 120/min | 300/min |
| per-team | build 觸發（含 webhook + manual redeploy） | 5/h | 20/h | 100/h | 300/h |
| per-team | build minutes / month（GHA 配額） | 50 | 500 | 2000 | 5000 |

> 數值由 ADR-0011 釘定；變更走 ADR 補丁。

### 4.2 配額查詢順序

```
For each request:
  1. category := router 註冊時之類別（read / write / preview-create）
  2. (token-level) check token bucket for (token_id, category)；fail → 429
  3. (team-level) check team bucket for (team_id, category)；fail → 429
  4. (build-level) check build bucket for team_id (for redeploy / create_app)
     - 由 redeploy / create_app handler 在進入 saga 前 check
  Allow request
```

### 4.3 Token bucket 結構

```go
// internal/server/services/ratelimit/bucket.go
type Bucket struct {
    limiter *rate.Limiter   // golang.org/x/time/rate
    plan    Plan            // free | starter | pro | team
}

type Limiter struct {
    perToken sync.Map        // token_id → *Bucket
    perTeam  sync.Map        // team_id → *Bucket
    perBuild sync.Map        // team_id → *Bucket（hourly）
    cfg      Config           // plan tier 配額表
}

func (l *Limiter) AllowToken(tokenID string, plan Plan, cat Category) (ok bool, retryAfter time.Duration) {
    bucket := l.getOrCreateToken(tokenID, plan)
    res := bucket.limiter.Reserve()
    if !res.OK() { return false, time.Hour }
    if res.Delay() > 0 {
        res.Cancel()
        return false, res.Delay()
    }
    return true, 0
}
```

### 4.4 Bucket 生命週期

- 首次 request 時 lazy 建立；plan 變動時 mark `stale` 並重建
- 背景 goroutine 每 1h 清掃 idle > 24h 之 bucket（避免 memory leak）
- M5 多 replica：每 pod 各自 bucket；N replica 配額膨脹為 N 倍（容忍）；v1.1 評估 Redis-backed 共享 bucket

## 5. 429 response 格式

### 5.1 HTTP

```
HTTP/1.1 429 Too Many Requests
Content-Type: application/json
Retry-After: 47

{
  "error": {
    "code": "rate_limited",
    "message": "Rate limit exceeded for this token (write 60/min for plan=free).",
    "details": {
      "scope": "per_token",
      "category": "write",
      "limit": 60,
      "window_s": 60,
      "retry_after_s": 47,
      "plan": "free"
    },
    "trace_id": "..."
  }
}
```

### 5.2 `Retry-After` 計算

- token bucket 之 `Reserve()` 回傳 `Delay()` 表示「再等多久即有 token」
- ceil 至秒；最小 1
- 若 plan 升級可立即解除：客戶端可帶 `If-Plan-Upgraded` header（v1.1）；v1 不支援

## 6. Abuse 偵測

### 6.1 背景 goroutine

`internal/server/services/ratelimit/abuse_detector.go`：

```go
func RunDetector(ctx context.Context, db *pgxpool.Pool, log *slog.Logger) {
    t := time.NewTicker(5 * time.Minute)
    defer t.Stop()
    for {
        select {
        case <-ctx.Done(): return
        case <-t.C:
            detectCrossASN(ctx, db, log)
            detectCrossTeam401(ctx, db, log)
            detectPreviewRatio(ctx, db, log)
        }
    }
}
```

### 6.2 規則 1：Token 跨 ASN

```sql
SELECT token_id, COUNT(DISTINCT client_asn) AS asn_count
  FROM access_log_aggregate          -- 假設 5 min 滾動聚合表
 WHERE window = 'last_1h'
 GROUP BY token_id
HAVING COUNT(DISTINCT client_asn) >= 3
```

對符合者：
- audit_log（actor=`system:abuse_detector`，subject=token_id）
- 通知 owner（v1 為 stdout/log；v1.1 為 webhook / email）
- v1.1：自動撤銷 token 或 require re-auth

> 需求：access log 含 `client_asn`，由 reverse proxy（Cloudflare）header `CF-IPCountry` + GeoIP 反查；本 spec 預留欄位，落地由 `observability-skeleton` 補

### 6.3 規則 2：同 IP 跨 team 401

```sql
SELECT client_ip, COUNT(DISTINCT team_id_attempted) AS teams
  FROM access_log_aggregate
 WHERE window = 'last_1h'
   AND status = 401
 GROUP BY client_ip
HAVING COUNT(DISTINCT team_id_attempted) >= 5
```

對符合者：
- audit_log
- v1：log warn；v1.1 短時封鎖 IP（10 min）

### 6.4 規則 3：Preview create/consume ratio

```sql
SELECT team_id,
       SUM(CASE action LIKE '%_preview' THEN 1 ELSE 0 END) AS created,
       SUM(CASE action NOT LIKE '%_preview' THEN 1 ELSE 0 END) AS consumed
  FROM audit_log_recent_1h
 GROUP BY team_id
HAVING created::float / NULLIF(consumed, 0) > 10
```

對符合者：
- audit_log + dashboard panel（與 ADR-0006 紅旗指標對齊）
- 不自動阻擋；提示 owner 檢查 LLM 行為

> 此規則為 product health 指標（LLM 是否亂發 preview）；v1 不阻擋

## 7. CLI 自動退避

### 7.1 Backoff 邏輯

```go
// internal/cli/client/client.go
func (c *Client) doWithRetry(req *http.Request) (*http.Response, error) {
    backoff := time.Second
    for attempt := 0; attempt < 5; attempt++ {
        resp, err := c.http.Do(req)
        if err != nil { return nil, err }
        if resp.StatusCode != 429 { return resp, nil }

        retryAfter := parseRetryAfter(resp.Header.Get("Retry-After"))
        if retryAfter == 0 { retryAfter = backoff }
        // jitter ±20%
        sleep := retryAfter + time.Duration(rand.Int63n(int64(retryAfter/5)))
        time.Sleep(sleep)
        backoff *= 2
    }
    return nil, errMaxRetries
}
```

- 最多 5 次；超過後 CLI 印錯誤訊息含 `code=rate_limited`、`details`
- `--no-retry` flag 跳過

### 7.2 MCP 端

- MCP 不主動 retry（避免 LLM 失控）
- 直接回 `IsError: true` + envelope 給 LLM
- SKILL.md 指引：「若 tool 回 `code=rate_limited`，告知 user 限額狀態，建議稍後再試或升級 plan」

## 8. Metric

| Metric | type | labels | help |
|---|---|---|---|
| `0ops_rate_limit_triggered_total` | counter | `scope`（per_token/per_team/per_build）, `category`（read/write/preview_create）, `plan` | 429 觸發次數 |
| `0ops_rate_limit_buckets_active` | gauge | `scope` | 當前活躍 bucket 數 |
| `0ops_abuse_detection_alerts_total` | counter | `rule`（cross_asn/cross_team_401/preview_ratio） | 偵測告警次數 |

## 9. 與其他 spec 接合點

| 接合 | spec |
|---|---|
| `rate_limited` 失敗碼 envelope | `error-model` § 5.6 |
| Plan tier 配額表 | ADR-0011（待拍板）|
| Build 觸發 limit 在 redeploy / create_app handler | `webhook-and-redeploy`、`create-app-flow` |
| audit_log 寫入 | `audit-log` spec |
| Preview consumption rate（紅旗）| ADR-0006、`slo-and-alerting` spec |
| Metric label cardinality | `observability-skeleton` § 4.5（plan 為合法 label，僅 4 值）|
| Token bucket per-pod 限制 | `backend-ha-leader-election` spec（M5 共享 bucket 評估）|

## 10. 驗證準則

| 驗證項 | 方式 | 通過條件 |
|---|---|---|
| Per-token write limit | 連續 61 次 write within 60s（plan=free） | 第 61 次 429 + Retry-After |
| Per-team write limit | 同 team 多 token 合計 301 次 | 第 301 429 |
| Per-team build limit | 21 次 redeploy within 1h | 第 21 個 skip + 429 |
| Plan upgrade 即解 | 從 free 升 starter 後立即重試 | 200（bucket rebuild） |
| Retry-After 計算 | 限 60/min，已用滿，等 30s 後重試 | Retry-After 約 30 |
| CLI 自動退避 | mock backend 連續 3 次 429 後 200 | CLI 第 4 次成功 |
| CLI --no-retry | mock 429 | CLI 立即 fail |
| MCP 不 retry | mock 429 | tool result IsError=true 第一次即回 |
| Abuse 跨 ASN | mock token 從 4 ASN 在 1h | audit_log + alert |
| Abuse 同 IP 跨 team 401 | mock 5 team 401 from same IP | audit_log + warn |
| Preview ratio 偵測 | mock create=11, consume=1 | dashboard panel + audit |
| Bucket cleanup | idle 25h | bucket 從 sync.Map 移除 |
| 多 replica（M5） | 兩 backend pod | 配額為 2× single pod；audit 標 |
| Metric `triggered_total` | 觸發 429 | counter +1 |

## 11. SLI 對應

| SLI | 目標 | 量測 |
|---|---|---|
| 429 rate（健康使用者）| < 0.5% / 28d | `triggered_total / total requests` |
| Abuse alert rate | < 0.01% token / day | `alerts_total / active_tokens` |
| Preview ratio 紅旗（per-team）| <= 10:1 | `preview_created / consumed` |
| CLI auto-retry 命中率 | > 95% retry 後成功 | CLI 端 metric（v1.1 補回傳） |

## 12. 對 `docs/0ops-plan.md` 的修改清單

1. 「Rate limit & abuse 偵測」段：交叉引用本 spec 為配額 + 偵測規則 source；plan 內列舉保留為摘要
2. plan tier 配額待 ADR-0011 拍板後本 spec § 4.1 更新
3. ADR-0006 紅旗指標 `preview consumption rate`：本 spec 為實作；交叉引用
4. 補入：「CLI 端對 429 之 retry 邏輯為硬性實作；MCP 不 retry」

## 13. Open issues

- ADR-0011 plan tier 配額：v1 提案值待拍板
- M5 多 replica 之配額膨脹（N 倍）是否可接受：v1 接受；M5 可能引入 Redis 共享 bucket（需新組件，違反 v1 簡化原則）
- Abuse 偵測之 access log 聚合表 schema：v1 不存在；本 spec 假設由 `audit_log` + reverse proxy log 推導；v1.1 補正式聚合管線
- IP allowlist / blocklist：v1 不做；v1.1 評估
- 自動撤銷可疑 token：v1 不做；v1.1
- preview ratio 之 dashboard 視覺化：屬 `slo-and-alerting` spec
- per-token plan 之取得：v1 從 `cli_token` join `team` 取 `team.plan`；快取 5 分鐘避免每 request DB 查
- 跨 region 流量 ASN 識別：v1 透過 Cloudflare header；自架（無 Cloudflare）需 GeoIP DB

## 14. 不可違反的硬性規則

> 違反以下任一項，PR 不可合入。

1. 任何 4xx response 必含 envelope；429 額外帶 `Retry-After` header
2. Rate limit middleware 必註冊於 chi 鏈（在 AuthBearer 之後、handler 之前）
3. 配額變動必走 ADR-0011 + plan.md 同步；不得個別覆寫
4. CLI 收 429 必走自動退避（除非 `--no-retry`）；不得 silent fail
5. MCP 不得自動 retry 429；必回 envelope 給 LLM
6. Bucket 為 in-memory；M5 共享 bucket 之引入需 ADR
7. Abuse 偵測 v1 不阻擋；只 audit + alert；自動阻擋為 v1.1
8. metric label `plan` 為固定 4 值（free/starter/pro/team）；其他 plan 視為 cardinality 違反
9. Preview create-only 為獨立 category；不可併入一般 write（避免 LLM 連發 preview 不被偵測）
10. build limit 之檢查必於 saga 進入 irreversible 前；不得在 GHA dispatch 後才阻擋（已 irreversible）
