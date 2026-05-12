# Feature Spec：observability-skeleton

> **狀態**：draft
> **來源**：`docs/0ops-plan.md`「Observability & SLO」「Logging」「Trace propagation」段；ADR-0006（Observability Baseline）
> **適用範圍**：M1 必上之觀測層基線：metrics exposition、structured logging、trace propagation；不含 SLO 目標達成判定、burn-rate alert、dashboard 設計（屬 `slo-and-alerting` spec）
> **對應 Milestone**：M1（read-only API 動工前）；M2 為 GA（補完 reconciler 與 deploy 對應 metric 名稱）

## 1. 結論（先讀本段）

- Metric 暴露端：backend `/metrics`（Prometheus pull）；CLI / MCP **不**暴露 `/metrics`
- 固定 label set：`{route, method, status, team_bucket}`；不允許高 cardinality 維度（user_id、preview_id、commit_sha、team_slug）進 label
- `team_bucket = fmt.Sprintf("%02d", crc32(team_id) mod 64)`；ADR-0006 OQ#2 已選定 CRC32（理由見 § 4.2）
- Metric 命名 `0ops_<domain>_<noun>_<unit>`；本 spec 釘 M1 必上之 7 條 metric（http、preview、reconciliation），其餘（deploy_run、cloudflare_api、github_rate）隨對應 feature spec 加入
- Logging：`log/slog` JSON handler；backend 走 stdout、MCP binary 走 stderr、CLI 預設不寫 log（除非 `--verbose`）
- Log 標準欄位 10 條（time/level/msg/trace_id/team_id/actor_user_id/route/status/latency_ms/err）；新增欄位走 ReplaceAttr 收口
- Trace 採 W3C `traceparent`；不另設自訂 header；五段鏈路（HTTP → slog → repository_dispatch → callback → audit_log）任一段斷即視為 propagation bug
- Redaction 中央化於 `internal/server/observability/redactor.go`；`slog` ReplaceAttr 與 `error-model` § 9 共用同一 redactor 實例
- M1 範圍**只**含 propagation infra；五段鏈路中 GHA / callback / audit 兩段於對應 feature spec 補（本 spec 釘介接點 contract）

## 2. 範圍

### 2.1 包含
- `internal/server/observability/` package：metrics、tracing、logging、redactor 四檔
- `/metrics` endpoint 註冊與 Prometheus exposition 設定
- M1 必上之 metric 名稱、type、label、help 文字
- `team_bucket` 計算函式與 hash 算法選擇
- `slog` JSON handler 設定、標準欄位 schema、ReplaceAttr 與 redactor 接合
- W3C `traceparent` middleware（OTel `otelhttp`）、`context.Context` 注入規約
- backend → 外部呼叫（`http.Client`、Cloudflare SDK、GitHub SDK）的 trace propagation 規約
- error envelope 取 trace_id 的來源（`internal/server/apperror` 對 `ctx` 的依賴）

### 2.2 不包含
- SLO 目標、SLI 達成判定、burn-rate alert 規則、dashboard 設計（屬 `slo-and-alerting` spec）
- `failure_classification` 列舉與 CFR 計算（屬 `reconciler-and-incident` spec；本 spec 只規範該欄位寫入時 trace_id 同步）
- Audit log 寫入點（屬 `audit-log` spec；本 spec 只釘 `audit_log.trace_id` 取自同一 ctx 的契約）
- GHA `repository_dispatch` payload 攜帶 `trace_id` 之 workflow YAML（屬 `build-pipeline-and-callback` spec；本 spec 只釘介接 contract）
- 內部 callback `/internal/deploy-runs/{id}/callback` 之 trace_id 回收（同上）
- OTLP exporter / Grafana / Loki / log shipper 部署（v1 僅落地 stdout/stderr）

## 3. 檔案結構

```
0ops/
└── internal/
    └── server/
        └── observability/
            ├── metrics.go         # Registry、Collector 註冊、http handler
            ├── http_metrics.go    # chi middleware：HTTP histogram + counter
            ├── tracing.go         # OTel TracerProvider、otelhttp middleware、傳入 outgoing client
            ├── logging.go         # slog JSON handler 設定、ReplaceAttr 接 redactor
            ├── redactor.go        # 中央化敏感欄位 mask；error-model § 9 共用
            ├── teambucket.go      # CRC32 hash mod 64
            ├── ctxkeys.go         # context key 常數（trace_id, team_id, actor_user_id, route）
            └── doc.go
```

## 4. Metrics

### 4.1 Exposition 與 registry

- 採 `github.com/prometheus/client_golang/prometheus`；用獨立 `*prometheus.Registry`，不用全域 `DefaultRegisterer`
  - **理由**：避免第三方套件偷加 metric 進 default registry 造成不可控；本 spec 註冊之 metric 集中可審
- `/metrics` 由 `promhttp.HandlerFor(reg, promhttp.HandlerOpts{})` 提供
- 對外暴露於 backend HTTP server 之 `/metrics` path；不過 `AuthBearer` middleware（屬 unauthenticated endpoint）；以 K3s `NetworkPolicy` 限制只允許 Prometheus pod 拉取
- backend 啟動時 panic-on-duplicate；註冊衝突立即發現

### 4.2 `team_bucket` 計算

```go
package observability

import "hash/crc32"

const TeamBucketCount = 64

// TeamBucket 回傳 "00".."63"；team_id 為 UUID 字串。
func TeamBucket(teamID string) string {
    if teamID == "" {
        return "anon"
    }
    h := crc32.ChecksumIEEE([]byte(teamID))
    return fmt.Sprintf("%02d", h%TeamBucketCount)
}
```

- 演算法選 **CRC32 (IEEE)**：分布均勻足夠（hash mod 64 之均勻性 spike 顯示 ±5% 內），stdlib 內建零依賴，計算成本 < 100ns
- FNV-1a 與 SHA256-truncate 均通過分布測試；CRC32 因為 stdlib + 速度勝出
- `anon` 為跨 team / unauth 路徑（如 `/v1/auth/device/start`、`/healthz`）的 fallback；列入固定值，不參與 mod
- TeamBucketCount = 64 為 ADR-0006 (a) 採用值；變更需 ADR 補丁（series 名稱保持，數值精度改變屬 breaking 改動）

### 4.3 M1 必上 metric

| Metric | Type | Labels | Help |
|---|---|---|---|
| `zeroops_http_requests_total` | counter | route, method, status, team_bucket | Total HTTP requests handled |
| `zeroops_http_request_duration_seconds` | histogram | route, method, team_bucket | HTTP request duration histogram |
| `0ops_preview_created_total` | counter | action | Number of previews created (by action type) |
| `0ops_preview_consumed_total` | counter | action, outcome | Previews consumed; outcome ∈ {success, failed, idempotent_replay} |
| `0ops_preview_expired_total` | counter | action | Previews that expired without consumption |
| `0ops_reconciliation_jobs_pending` | gauge | kind | Pending reconciliation jobs (sampled by reconciler tick) |
| `0ops_build_info` | gauge (=1) | version, commit, go_version | Static build info; useful for dashboard filtering |

> 直方圖 bucket：`prometheus.ExponentialBuckets(0.005, 2, 12)` = 5ms..~10s；對 read p95 < 200ms 與 preview p95 < 800ms 兩 SLO 都覆蓋

### 4.4 後續 milestone 補入

下列 metric 不在 M1 範圍，由對應 spec 加入時遵守命名與 label cardinality 規約：

| Metric | 對應 spec |
|---|---|
| `0ops_deploy_run_duration_seconds` (histogram, labels: stage, outcome) | `build-pipeline-and-callback` |
| `0ops_deploy_run_failures_total` (counter, labels: stage, classification) | `reconciler-and-incident` |
| `0ops_domain_verify_attempts_total` (counter, labels: outcome) | `custom-domain-and-verify` |
| `0ops_cloudflare_api_calls_total` (counter, labels: op, outcome) | `winshare-subdomain-and-tunnel` / `custom-domain-and-verify` |
| `0ops_github_api_rate_remaining` (gauge, labels: install_id_bucket) | `github-app-install-flow` |

> `install_id_bucket` 與 `team_bucket` 同模式（CRC32 mod 64），避免 install_id 直接作 label

### 4.5 Cardinality 守門員

- 註冊 metric 時 `MustRegister`；同名重註 panic
- label set 由 register 時宣告，不允許動態擴增；新增 label 必走新 metric 名（避免 series 不連續）
- CI lint：`internal/server/observability/lint.go` 的單元測試遍歷所有 collector，比對 label set 是否與本 spec § 4.3 / § 4.4 表格相符

## 5. Structured logging

### 5.1 `slog` 設定

```go
// internal/server/observability/logging.go
func NewLogger(level slog.Level, redactor *Redactor) *slog.Logger {
    handler := slog.NewJSONHandler(os.Stdout, &slog.HandlerOptions{
        Level:       level,
        ReplaceAttr: redactor.ReplaceAttr, // mask 敏感欄位
        AddSource:   false,                 // 生產不留 source（成本 + 噪音）
    })
    return slog.New(handler)
}
```

- backend：`os.Stdout`
- MCP binary：`os.Stderr`（**硬性規則**；ADR-0003 § 4 與本 spec 一致）
- CLI：預設 `io.Discard`；`--verbose` 改 `os.Stderr`；不可走 stdout（汙染 CLI 輸出）

### 5.2 標準欄位 schema

| 欄位 | 型別 | 必填 | 來源 |
|---|---|---|---|
| `time` | RFC3339Nano | 是 | `slog` 預設 |
| `level` | string (`debug`/`info`/`warn`/`error`) | 是 | `slog` 預設 |
| `msg` | string | 是 | 呼叫方傳入 |
| `trace_id` | string (W3C 32 hex) | 是（有 ctx 時） | `ctx` 注入；空時填 `00000000000000000000000000000000` 並標 `trace_missing=true` |
| `team_id` | string (UUID) | 否 | `ctx` 注入；空字串時欄位省略 |
| `actor_user_id` | string (UUID) | 否 | 同上 |
| `route` | string (chi RoutePattern) | 否 | HTTP middleware 注入 |
| `status` | int | 否 | HTTP middleware 注入 |
| `latency_ms` | int64 | 否 | HTTP middleware 注入 |
| `err` | string (`error.Error()`) | 否 | 呼叫方 `slog.Any("err", err)`；redactor 過濾 |

> 注意：`team_id` 為 raw（非 bucket）；log 為非聚合資料，cardinality 由 retention 系統處理

### 5.3 ReplaceAttr 與 redactor

- `Redactor` 為 stateless 物件；`ReplaceAttr(groups []string, a slog.Attr) slog.Attr` 規則：
  1. key 為以下之一：`Authorization`、`authorization`、`Cookie`、`Set-Cookie`、`X-Hub-Signature-256`、`X-0ops-Signature` → value 替換為 `***`
  2. key prefix `secret_` / `password` / `private_key` 或 suffix `_secret` / `_token`（除 `trace_id`、`request_id` 外）→ value 替換為 `***`
  3. key = `body` 且 value 含 `password`/`token`/`secret` 子字串 → 整 value 替換為 `<redacted body>`
  4. 其他 → 原樣
- redactor 與 `error-model` spec § 9 共用同一實例（exported singleton）

### 5.4 例：HTTP middleware 寫 log

```go
// internal/server/observability/http_metrics.go (節錄)
func LoggingMiddleware(logger *slog.Logger) func(http.Handler) http.Handler {
    return func(next http.Handler) http.Handler {
        return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
            start := time.Now()
            ww := chimw.NewWrapResponseWriter(w, r.ProtoMajor)
            next.ServeHTTP(ww, r)
            logger.LogAttrs(r.Context(), levelFor(ww.Status()),
                "http_request",
                slog.String("route", chi.RouteContext(r.Context()).RoutePattern()),
                slog.String("method", r.Method),
                slog.Int("status", ww.Status()),
                slog.Int64("latency_ms", time.Since(start).Milliseconds()),
            )
        })
    }
}
```

`trace_id` / `team_id` / `actor_user_id` 由 ctx 自動帶入，不必在每個呼叫點寫；實作於自定 `ContextHandler` wrap：

```go
type ContextHandler struct{ slog.Handler }

func (h ContextHandler) Handle(ctx context.Context, r slog.Record) error {
    if id := TraceIDFromContext(ctx); id != "" {
        r.AddAttrs(slog.String("trace_id", id))
    } else {
        r.AddAttrs(slog.String("trace_id", "00000000000000000000000000000000"),
                   slog.Bool("trace_missing", true))
    }
    if tid := TeamIDFromContext(ctx); tid != "" {
        r.AddAttrs(slog.String("team_id", tid))
    }
    if uid := ActorUserIDFromContext(ctx); uid != "" {
        r.AddAttrs(slog.String("actor_user_id", uid))
    }
    return h.Handler.Handle(ctx, r)
}
```

## 6. Trace propagation

### 6.1 W3C `traceparent`

- 採 `go.opentelemetry.io/otel/trace` + `go.opentelemetry.io/contrib/instrumentation/net/http/otelhttp`
- 入口 middleware：`otelhttp.NewMiddleware("0ops-server")`；自動處理 `traceparent` / `tracestate` header；無則建立新 span
- TracerProvider：v1 採 `noop.NewTracerProvider()`（不送 OTLP），但 span 仍在 ctx 流轉，`trace_id` 可從 `trace.SpanContextFromContext(ctx)` 取出
- v1.1 評估啟用 OTLP exporter（ADR-0006 OQ#1）；本 spec 預留 `OBS_OTLP_ENDPOINT` env 偵測：非空時換 `otlptracehttp` exporter

### 6.2 ctx key 與 trace_id 取出

```go
// internal/server/observability/ctxkeys.go

func TraceIDFromContext(ctx context.Context) string {
    sc := trace.SpanContextFromContext(ctx)
    if !sc.IsValid() {
        return ""
    }
    return sc.TraceID().String() // 32 hex chars
}
```

- `apperror.WriteJSON()` 取此函式（與 `error-model` § 8 對齊）
- `audit_log.trace_id` 寫入時取此值
- `deploy_run.trace_id` 寫入時取此值

### 6.3 出向呼叫帶 trace

| 客戶端 | 方法 |
|---|---|
| 自家 backend → GitHub API | `*http.Client` 包 `otelhttp.NewTransport`；header 自動含 `traceparent` |
| 自家 backend → Cloudflare API | 同上 |
| 自家 backend → K8s API | `client-go` 之 `RestClient.Transport` 包 `otelhttp.NewTransport` |
| 自家 backend → GHA `repository_dispatch` | header `traceparent` 自動帶；同時 payload `client_payload.trace_id` 顯式帶（contract see § 6.4） |

### 6.4 五段鏈路 contract

| 段 | 由誰負責 | 介接點 | 失敗 contract |
|---|---|---|---|
| 1. HTTP middleware | 本 spec | `otelhttp.NewMiddleware` 必註冊於 chi 鏈最外層之一（在 RequestID 之後） | 缺：所有後續無 trace_id；`trace_missing=true` 警報 |
| 2. slog | 本 spec | `ContextHandler` 寫入每行 log | 缺：log 無 trace_id；同上警報 |
| 3. `repository_dispatch` payload | `build-pipeline-and-callback` spec | `client_payload.trace_id` 為必填字串欄位 | 缺：GHA 端 trace 鏈斷；reconciler-and-incident spec 必檢測 |
| 4. callback `/internal/deploy-runs/{id}/callback` | `build-pipeline-and-callback` spec | request body 必含 `trace_id`；server 端注入回 ctx | 缺：callback 後續處理無 trace_id |
| 5. `audit_log.trace_id`、`deploy_run.trace_id` | `audit-log` spec / `reconciler-and-incident` spec | 寫入時呼 `TraceIDFromContext()` | 缺：DB 欄位空，dashboard 標 trace_missing |

## 7. CLI / MCP 觀測規約

### 7.1 CLI

- 不暴露 `/metrics`
- log 走 `--verbose`（stderr）；無 verbose 時只印「人類可讀錯誤訊息」（屬 `error-model` § 6）
- 不主動建 trace；對 backend 呼叫只把 backend 回的 `error.trace_id` 顯示給 user（屬 `error-model` § 6.1）

### 7.2 MCP

- 不暴露 `/metrics`（無 HTTP server）
- log 走 stderr（`os.Stderr`）；level 由 env `OPS_MCP_LOG_LEVEL` 控制，預設 `warn`
- MCP server 對 backend 呼叫**主動建** root span（client 端 trace），把 `traceparent` 寫入 outgoing header；backend 端 `otelhttp` middleware 接續同一 trace
  - **理由**：使用者一句 LLM 指令對應一條 root trace，可串「LLM 解析 → tool call → backend 動作」三段
  - 實作：MCP server 啟動時建 noop TracerProvider；每個 tool call 進入時 `tracer.Start(ctx, "mcp.tool.<name>")`

## 8. 啟動與設定

### 8.1 backend env 變數

| 變數 | 預設 | 說明 |
|---|---|---|
| `OPS_LOG_LEVEL` | `info` | `debug`/`info`/`warn`/`error` |
| `OPS_METRICS_LISTEN_ADDR` | （與主 server 同 port） | 若設則開獨立 port 暴露 `/metrics`（v1.1 評估） |
| `OBS_OTLP_ENDPOINT` | （空） | 非空時啟用 OTLP exporter（v1 不採） |
| `OBS_OTLP_INSECURE` | `false` | OTLP 是否走 HTTP |

### 8.2 MCP env 變數

| 變數 | 預設 | 說明 |
|---|---|---|
| `OPS_MCP_LOG_LEVEL` | `warn` | MCP stderr log level |

## 9. 驗證準則

| 驗證項 | 方式 | 通過條件 |
|---|---|---|
| `/metrics` 回 200 + 文本 | `curl http://localhost:8080/metrics` | exposition format 通過 `promtool check metrics` |
| label set 不漂移 | `go test ./internal/server/observability/...` | 所有 collector label set 與 § 4.3 / § 4.4 表完全相符 |
| `team_bucket` 分布 | 隨機 10000 UUID，bucket count 各值偏差 | 任一 bucket 偏離平均 ±10% 內 |
| `team_bucket` for empty | `TeamBucket("")` | 回 `"anon"`，不 panic |
| trace_id 出現於 log | httptest 一次 GET，stdout 解析 | log JSON 含合法 32-hex `trace_id` |
| `trace_missing=true` 偵測 | 模擬 ctx 無 span，呼叫 logger | log 含 `trace_missing=true` 與 32-zero traceparent |
| ReplaceAttr mask | `slog.LogAttrs(slog.String("Authorization", "Bearer xxx"))` | 輸出 `"Authorization":"***"` |
| ReplaceAttr 不誤 mask | `slog.LogAttrs(slog.String("trace_id", "abc..."))` | 不 mask（trace_id / request_id 例外） |
| outgoing client 帶 traceparent | mock GitHub HTTP server，檢查 inbound header | 有 `traceparent` 且格式 `00-<32hex>-<16hex>-<2hex>` |
| MCP tool root span | mock MCP tool call，檢查 ctx | 含合法 trace_id；後續 backend 呼叫 traceparent 為同一 trace |
| MCP log 走 stderr | 啟 MCP binary 並 capture | stdout 為 MCP protocol、stderr 為 JSON log |
| CLI 預設不寫 log | 跑 `0ops --version` | stderr / stdout 無 JSON log 行 |
| `--verbose` CLI log | `0ops --verbose ...` | stderr 出現 JSON log |
| Cardinality lint | 嘗試註冊額外 label 的 collector | panic / test fail |

## 10. 對 `docs/0ops-plan.md` 的修改清單

1. 「Observability & SLO § Metrics 暴露」段：補入 `0ops_build_info` gauge；補註解 `team_bucket` 算法為 CRC32 IEEE
2. 「Trace propagation」段：交叉引用本 spec § 6.4 為五段 contract source of truth；GHA payload 之 `client_payload.trace_id` 欄位由 `build-pipeline-and-callback` spec 釘
3. 「Logging」段：補入「`trace_missing=true` 警示欄位」與「CLI 預設不寫 log；`--verbose` 走 stderr」
4. 「Reconciler 收斂迴圈」段：補入「`failure_classification` 寫入時 trace_id 必同步」交叉引用 `reconciler-and-incident` spec
5. 「Goals (v1)」段：明確 M1 必上 observability skeleton（與 plan.md milestones M1 行同步）

## 11. Open issues

- OTLP exporter 啟用時程（ADR-0006 OQ#1）：v1 採 noop；v1.1 評估
- 是否在 Prom 端設 `metric_relabel_configs` 守門員（ADR-0006 OQ#4）：v1 暫不設；M2 後流量觀察
- Synthetic probe（ADR-0006 OQ#6）：v1 不做；M2 後評估
- Log sampling（ADR-0006 OQ#5）：v1 不做
- `OBS_OTLP_ENDPOINT` 啟用後，noop TracerProvider 切換需設計零停機切換流程（不在 M1 範圍）
- TeamBucketCount 升 128/256 的 series 命名版本化（ADR-0006 § 6.2）：M5 重審
- backend 啟動時是否將整個 metric registry dump 到 log（debug 用）：v1 不做，避免啟動 log 噪音

## 12. 不可違反的硬性規則

> 違反以下任一項，PR 不可合入。

1. backend `/metrics` 之 metric label set 必為固定 `{route, method, status, team_bucket}`；不得新增 user_id、preview_id、commit_sha、team_slug 等高 cardinality label
2. MCP binary 之 log 必走 `os.Stderr`；不得走 stdout（會汙染 stdio MCP protocol）
3. `slog` 之 ReplaceAttr 必接 `Redactor`；不允許繞過 redactor 直接寫 `Authorization` / token / webhook payload
4. trace propagation 五段鏈路任一新增介接點必更新 § 6.4 contract 表，並補對應驗證測試
5. 出向 HTTP client 必包 `otelhttp.NewTransport`（GitHub、Cloudflare、K8s API）；新增第三方 client 須同樣處理
6. Metric 命名必為 `0ops_<domain>_<noun>_<unit>` snake_case；單位必含於名稱（`_seconds` / `_bytes` / `_total`）
7. 新增 metric 必同時更新本 spec § 4.3 / § 4.4 表與 cardinality lint 測試
8. CLI 預設不得寫 JSON log；只在 `--verbose` 啟用，且必走 stderr
9. `team_bucket` 算法常數（CRC32 IEEE、mod 64）只允許整體版本化升級（新 metric 名）；不得就地改變 mod 數
