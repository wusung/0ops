# Feature Spec：error-model

> **狀態**：draft
> **來源**：`docs/0ops-plan.md`「Backend：preview gate」「Auth & RBAC」「Webhook 安全」段；ADR-0002（preview 4xx 行為）、ADR-0003（MCP error 形式）、ADR-0006（trace_id propagation、redaction）
> **適用範圍**：backend HTTP error 回應、CLI 錯誤呈現、MCP tool error envelope；不含個別 endpoint 的業務邏輯錯誤判斷
> **對應 Milestone**：M0 末（地基；M1 任何 endpoint 動工前必須完成）

## 1. 結論（先讀本段）

- Backend 統一 error envelope：`{"error": {"code", "message", "details?", "trace_id"}}`；HTTP body 永遠 JSON，不論 status code
- `code` 為 machine-readable 短字串（snake_case），CLI / MCP 依 `code` 分支處理；`message` 為 human-readable，**禁止** client 端 string-match `message`
- 錯誤分類化：`apperror.Class` 七類（`bad_request` / `unauthorized` / `forbidden` / `not_found` / `conflict` / `unprocessable` / `internal` / `unavailable`）對應固定 HTTP status；`code` 為 class 內細分
- `trace_id` 出現於每個 error envelope（即使 `internal`），與 `audit_log.trace_id`、`deploy_run.trace_id`、log 輸出共用同一 W3C traceparent
- Preview gate 4xx 採固定碼集（ADR-0002）：`missing_preview_id` / `preview_expired` / `preview_consumed`（200 + last_result，非錯誤）/ `forbidden_team`
- Auth 失敗採固定碼集：`unauthorized` / `forbidden_role` / `forbidden_scope` / `not_member` / `token_revoked` / `token_expired`
- MCP tool error 走官方 Go SDK 之 `IsError: true` + `Content` 含 envelope JSON；不抛 Go error（避免 SDK 自動包裝丟失欄位）
- CLI 預設輸出人類可讀錯誤（含 `code` 後綴、操作建議）；`--output json` 直接 dump envelope；exit code 依 class 分配
- Internal error（`5xx`）對外只回 `code: internal_error` + `trace_id`；**不**回堆疊、不回外部 API 原始錯誤；完整資訊只進 server 端結構化 log
- Redaction 為硬性：`Authorization` header、`Set-Cookie`、`secret_*`、PAT / token 任何欄位、webhook payload 全文不得進 error message 或 `details`

## 2. 範圍

### 2.1 包含
- `internal/server/apperror/` package：`Error` 結構、`Class` 列舉、`code` 常數集、constructor helper
- HTTP error mapping middleware：捕捉 `*apperror.Error` → JSON envelope；非 `apperror.Error` 之 panic / unknown error → `internal_error`
- 錯誤碼總表（preview gate / auth / 一般 CRUD / 外部依賴）
- CLI 錯誤呈現：human / json 兩格式、exit code 對應、token 過期提示、preview 過期提示
- MCP tool error envelope 格式與 SDK 介接
- Trace propagation：error envelope 之 `trace_id` 與 logging / audit 一致來源
- Redaction 規則與測試

### 2.2 不包含
- 個別 endpoint 之 input validation 規則（屬該 feature spec）
- preview/confirm 流程本體（屬 `preview-confirm-gate` spec）
- middleware chain 之 auth 機制本體（屬 `auth-and-rbac` spec）
- structured logging 欄位完整定義（屬 `observability-skeleton` spec；本 spec 只規範 error 經 log 輸出時的欄位）
- 對外 status page / webhook 通知（屬 v2）
- Rate limit 之 429 細節（屬 `rate-limit-and-abuse` spec；本 spec 只列 code）

## 3. 檔案結構

```
0ops/
├── internal/
│   ├── server/
│   │   ├── apperror/
│   │   │   ├── error.go           # Error 結構 + Class enum + 常數
│   │   │   ├── constructors.go    # NewBadRequest(code, msg, details...) 等 helper
│   │   │   ├── http.go            # ToHTTPStatus(class) + WriteJSON(w, err, traceID)
│   │   │   └── doc.go
│   │   └── middleware/
│   │       └── errorwriter.go     # chi middleware：捕捉 panic、轉 *apperror.Error、寫 envelope
│   ├── cli/
│   │   └── output/
│   │       └── error.go           # PrintError(err, format)；exit code 對應
│   └── mcp/
│       └── tools/
│           └── errors.go          # ToolError(err) → mcp.CallToolResult{IsError: true, ...}
└── internal/shared/
    └── apierror/                  # client 端共用：解析 envelope、code 常數鏡像
        ├── envelope.go
        ├── codes.go               # 與 server 端 codes 同步（contract test 強制）
        └── doc.go
```

## 4. Error envelope 結構

### 4.1 Wire JSON

```json
{
  "error": {
    "code": "preview_expired",
    "message": "Preview has expired (10 minutes TTL). Re-issue preview before retry.",
    "details": {
      "preview_id": "8f4e2d1a-...",
      "expired_at": "2026-05-10T12:34:56.123456789Z"
    },
    "trace_id": "0af7651916cd43dd8448eb211c80319c"
  }
}
```

| 欄位 | 必填 | 說明 |
|---|---|---|
| `error.code` | 是 | snake_case；對應 `internal/shared/apierror/codes.go` 常數 |
| `error.message` | 是 | 人類可讀；UTF-8；不得含 token / secret；可含 placeholder（如資源 slug） |
| `error.details` | 否 | 結構化補充；schema 由 `code` 決定，本 spec § 6 列舉 |
| `error.trace_id` | 是 | W3C trace_id（32 hex chars）；與 audit/log 同源 |

### 4.2 Go 端結構

```go
package apperror

type Class string

const (
    ClassBadRequest    Class = "bad_request"     // 400
    ClassUnauthorized  Class = "unauthorized"    // 401
    ClassForbidden     Class = "forbidden"       // 403
    ClassNotFound      Class = "not_found"       // 404
    ClassConflict      Class = "conflict"        // 409
    ClassUnprocessable Class = "unprocessable"   // 422
    ClassTooManyReqs   Class = "too_many_requests" // 429
    ClassGone          Class = "gone"            // 410
    ClassInternal      Class = "internal"        // 500
    ClassUnavailable   Class = "unavailable"     // 503
)

type Error struct {
    Class   Class
    Code    string                 // 細分碼，e.g. "preview_expired"
    Message string                 // 對外可呈現
    Details map[string]any         // 結構化補充
    Cause   error                  // 內部因，僅進 log
}

func (e *Error) Error() string { ... }
func (e *Error) Unwrap() error  { ... }
```

### 4.3 HTTP status 對應

| Class | HTTP status |
|---|---|
| `bad_request` | 400 |
| `unauthorized` | 401 |
| `forbidden` | 403 |
| `not_found` | 404 |
| `conflict` | 409 |
| `gone` | 410 |
| `unprocessable` | 422 |
| `too_many_requests` | 429 |
| `internal` | 500 |
| `unavailable` | 503 |

> `gone` 專供 `preview_expired`（語意：資源曾存在、現已不可用），與 ADR-0002 第 4 點 TTL 行為對齊。

## 5. 錯誤碼總表

> 完整 list；新增 code 須同步增於 `internal/shared/apierror/codes.go` 與本表。

### 5.1 Preview gate（ADR-0002）

| Code | Class / HTTP | Details schema | 觸發 |
|---|---|---|---|
| `missing_preview_id` | bad_request / 400 | `{action}` | confirm 端點未帶 `preview_id` |
| `preview_expired` | gone / 410 | `{preview_id, expired_at}` | preview TTL 10 分鐘已過 |
| `preview_not_found` | not_found / 404 | `{preview_id}` | preview_id 在 DB 不存在（含已被清理） |
| `forbidden_team` | forbidden / 403 | `{preview_id, team_slug}` | preview 屬於另一 team |
| `forbidden_actor` | forbidden / 403 | `{preview_id, actor_user_id}` | preview 之 `actor_user_id` 與當前 user 不符 |
| `idempotency_key_conflict` | unprocessable / 422 | `{preview_id, idempotency_key}` | client 帶 `Idempotency-Key` header 與 `preview_id` 衝突 |
| `precondition_changed` | conflict / 409 | `{check}` | confirm 階段重檢先決條件失敗（如 slug 已被搶用） |

> **非錯誤**：`preview_consumed` 重試由 backend 直接回 200 + `last_result`，**不**走 error envelope。CLI/MCP 識別「同一 preview_id 重試」由 HTTP 200 + 結果的 `idempotent_replay: true` flag 判定（具體欄位於 `preview-confirm-gate` spec 釘）。

### 5.2 Auth（plan.md Auth & RBAC）

| Code | Class / HTTP | Details | 觸發 |
|---|---|---|---|
| `unauthorized` | unauthorized / 401 | `{}` | `Authorization` header 缺、格式錯 |
| `token_invalid` | unauthorized / 401 | `{}` | bearer 無法 parse 或簽章錯 |
| `token_expired` | unauthorized / 401 | `{expired_at}` | PAT 已過期 |
| `token_revoked` | unauthorized / 401 | `{}` | PAT `revoked_at != null` |
| `not_member` | forbidden / 403 | `{team_slug}` | 解 team 後當前 user 非該 team membership |
| `forbidden_role` | forbidden / 403 | `{required_role, actor_role}` | role 不足 |
| `forbidden_scope` | forbidden / 403 | `{required_scope, token_scopes}` | token scope 不含所需 scope |
| `team_not_found` | not_found / 404 | `{team_slug}` | URL path team_slug 不存在 |
| `team_archived` | forbidden / 403 | `{team_slug}` | team `archived_at != null`，僅允許 read |

### 5.3 Webhook / Callback（plan.md Webhook 安全；ADR-0005）

| Code | Class / HTTP | Details | 觸發 |
|---|---|---|---|
| `webhook_signature_invalid` | unauthorized / 401 | `{}` | HMAC 比對失敗 |
| `webhook_timestamp_skew` | bad_request / 400 | `{server_time, header_time, max_skew}` | timestamp 偏離 ±5 min |
| `webhook_replay` | conflict / 409 | `{provider, delivery_id}` | `webhook_dedup` 命中（return 200 in success path；此 code 僅供內部 metric / log） |
| `callback_token_consumed` | conflict / 409 | `{deploy_run_id}` | GHA 短期 token 已 consume 過 |

> `webhook_replay` 對 GitHub 端正常 webhook 應回 HTTP 200（避免 GitHub retry storm）；本 code 僅用於 server log/metric 標註。對內部 deploy callback 之重複才以 409 回應 GHA。

### 5.4 一般資源 CRUD

| Code | Class / HTTP | 適用 |
|---|---|---|
| `validation_failed` | unprocessable / 422 | 通用驗證失敗；`details.fields[]` 為 `{field, reason}` |
| `slug_taken` | conflict / 409 | `(team_id, slug)` 已存在 |
| `slug_invalid` | bad_request / 400 | slug 格式不合（regex / 保留字） |
| `resource_not_found` | not_found / 404 | 通用；`details.kind` + `details.id` |
| `resource_archived` | conflict / 409 | 嘗試對 `archived` 資源寫入 |
| `unsupported_action` | unprocessable / 422 | 動作不適用於當前狀態（如對 `failed` deploy 觸發 cancel） |

### 5.5 外部依賴

| Code | Class / HTTP | 適用 |
|---|---|---|
| `github_api_error` | unavailable / 503 | GitHub API 5xx / timeout；`details.retry_after_s` |
| `github_rate_limited` | too_many_requests / 429 | GitHub API 429；`details.reset_at` |
| `github_app_not_installed` | forbidden / 403 | team 未綁 installation；`details.team_slug` |
| `github_repo_not_accessible` | forbidden / 403 | installation 無對該 repo 權限；`details.repo_url` |
| `cloudflare_api_error` | unavailable / 503 | Cloudflare API 5xx / timeout |
| `cloudflare_rate_limited` | too_many_requests / 429 | Cloudflare API 429 |
| `gitops_push_conflict` | conflict / 409 | GitOps repo push 衝突重試耗盡（接續 ADR-0005 retry 5 次後） |
| `buildpack_detect_failed` | unprocessable / 422 | CNB 偵測不出 stack；`details.repo_url, details.detected_languages[]` |

### 5.6 速率限制（`rate-limit-and-abuse` spec 細節）

| Code | Class / HTTP | Details |
|---|---|---|
| `rate_limited` | too_many_requests / 429 | `{scope: per_token|per_team, retry_after_s, limit, window_s}`；HTTP header 同附 `Retry-After` |

### 5.7 內部錯誤

| Code | Class / HTTP | 對外輸出 |
|---|---|---|
| `internal_error` | internal / 500 | 僅 `code` + `message: "Internal error. Reference trace_id for support."` + `trace_id`；**禁止** `details` 含內部資訊 |
| `service_unavailable` | unavailable / 503 | `{retry_after_s}`；用於計畫性下線 / 依賴中斷 |

## 6. CLI 錯誤呈現

### 6.1 Human format（預設）

```
Error: Preview has expired (10 minutes TTL). Re-issue preview before retry.
  code:     preview_expired
  trace_id: 0af7651916cd43dd8448eb211c80319c
  details:  preview_id=8f4e2d1a-...
            expired_at=2026-05-10T12:34:56Z

Hint: re-run with --dry-run to issue a fresh preview, then confirm within 10 minutes.
```

- `Hint:` 句僅對部分 code 提供，依下表：

| Code | Hint |
|---|---|
| `preview_expired` | 重新 `--dry-run` 取新 preview |
| `token_expired` / `token_revoked` | `0ops auth login` 重新登入 |
| `forbidden_role` / `forbidden_scope` | 顯示需要的 role/scope；提示聯絡 team owner |
| `not_member` | 列當前 user 所在 team 並提示 `0ops teams use <slug>` |
| `slug_taken` | 提示換名或 `--force-create` 不存在（v1 不支援強制覆寫） |
| `buildpack_detect_failed` | 提示 v1.1 將支援 Dockerfile mode；列已偵測語言 |
| `github_app_not_installed` | 提示 `0ops teams github install` |
| `rate_limited` | 提示 `Retry-After`；建議降低呼叫頻率 |

### 6.2 JSON format（`--output json`）

```json
{
  "error": {
    "code": "preview_expired",
    "message": "...",
    "details": { ... },
    "trace_id": "..."
  }
}
```

直接輸出 envelope，不額外包裝。

### 6.3 Exit code 對應

| Class | Exit code |
|---|---|
| `bad_request` / `unprocessable` / `validation_failed` | 2 |
| `unauthorized` / `token_*` | 3 |
| `forbidden` / `not_member` / `forbidden_role` / `forbidden_scope` | 4 |
| `not_found` | 5 |
| `conflict` / `slug_taken` / `gone`（`preview_expired`） | 6 |
| `too_many_requests` | 7 |
| `internal` / `unavailable` | 1 |
| 成功 | 0 |

> exit code 為 CI 腳本可分支處理之契約；新增 class 須同步擴充。

## 7. MCP tool error envelope

### 7.1 SDK 介接（ADR-0003）

採官方 `github.com/modelcontextprotocol/go-sdk` 之 `CallToolResult`：

```go
func toolError(ctx context.Context, e *apperror.Error) *mcp.CallToolResult {
    body := apierror.Envelope{
        Error: apierror.ErrorBody{
            Code:    e.Code,
            Message: e.Message,
            Details: e.Details,
            TraceID: trace.IDFromContext(ctx),
        },
    }
    payload, _ := json.Marshal(body)
    return &mcp.CallToolResult{
        IsError: true,
        Content: []mcp.Content{mcp.TextContent{Text: string(payload)}},
    }
}
```

- **不**從 tool 直接 `return nil, err`：SDK 會把 Go error 包成自家 message，丟失 `code` / `trace_id` / `details`
- LLM 看到的就是上述 JSON 字串（`TextContent`），可由 SKILL.md 指引解析

### 7.2 SKILL.md 對 LLM 的指引（mcp-tool-description-lint spec 補充）

本 spec 只規範對 LLM 顯示 error 之最小語句：

> "If a tool returns IsError=true, parse the JSON in Content[0].text. Show the user `code`, `message`, `details` (if present), and `trace_id`. Do NOT retry automatically on `forbidden_*`, `token_*`, `validation_failed`, `slug_taken`. For `preview_expired`, re-issue the `*_preview` tool before re-attempting."

## 8. Trace propagation

- 每個 inbound HTTP request 由 `RequestID → Logger → Tracing(otel)` middleware 取出或建立 W3C `traceparent`
- `trace_id` 取出後注入 `context.Context`，`apperror.WriteJSON()` 從 ctx 取出寫入 envelope
- `audit_log.trace_id`、`deploy_run.trace_id`、`slog` 結構化欄位 `trace_id` 全用同一值
- MCP tool 端：MCP 對 backend 之呼叫沿用 W3C 標頭；error envelope 之 `trace_id` 即為 backend 端 trace
- CLI 端：CLI 不主動產生 trace；輸出 backend 回的 `trace_id`，使用者可貼到 oncall ticket

## 9. Redaction

### 9.1 必 mask 欄位
- HTTP request header：`Authorization`、`Cookie`、`X-Hub-Signature-256`、`X-0ops-Signature`
- HTTP response header：`Set-Cookie`
- request / response body 內欄位名 prefix `secret_` / `token` / `password` / `private_key` / 結尾 `_secret` / `_token`
- webhook payload 全文（僅留 `delivery_id` + sha256 摘要）
- PAT 明文（僅留首尾 4 字 + 長度）

### 9.2 套用範圍
- 進 server log（structured）的 error / cause chain
- 進 `audit_log.args` / `audit_log.result`
- 進對外 error envelope（含 `details`）
- 進 metric label（已由 ADR-0006 限制 label set 規避，但 message 仍須 mask）

### 9.3 實作位置
- `internal/server/observability/redaction.go`：提供 `Redact(any) any` 與 `RedactString(string) string`
- error constructor 預設不對 `Details` 做 redact（callers 須自證乾淨）；中介 middleware 在序列化前 enforce 白名單欄位

## 10. 對 `docs/0ops-plan.md` 的修改清單

1. 「Auth & RBAC § Middleware chain」段（line 658）目前列 `forbidden_role | forbidden_scope | not_member`：補充本 spec 為碼集 source of truth；plan 句子保留為提示
2. 「Backend：preview gate」段提及之 `400 missing_preview_id` / `410 preview_expired` / `403 forbidden_team`：交叉引用本 spec § 5.1
3. 「Observability & SLO § Redaction」段：交叉引用本 spec § 9 為實作規約位置

## 11. Open issues

- `validation_failed` 之 `details.fields[]` schema 與 i18n：v1 暫採英文 reason 字串；i18n 待 v2 web UI 同步規劃
- 是否提供 RFC 7807 Problem Details 相容輸出（`application/problem+json`）：v1 不採；統一用 `application/json` + 自定 envelope，避免 SDK 雙模式
- CLI exit code 是否區分 retry-able vs non-retry-able：本 spec 採 class-based 分配，未額外給 retry-able bit；CI 腳本若需可由 `code` 自行判斷
- MCP tool 對 `preview_expired` 是否應自動重打 preview：本 spec 採「不自動重試，由 LLM 依 SKILL 引導決定」；自動重試會繞過 user 確認，違反兩階段精神

## 12. 不可違反的硬性規則

> 違反以下任一項，PR 不可合入。

1. 對外任何 HTTP error 回應之 body 必為 envelope JSON；禁止純文字 / HTML / 空 body
2. `Authorization` header、PAT 明文、webhook payload 全文、`secret_*` 欄位不得進 error message、`details`、log message、audit args/result
3. 新增 error code 必同步：`apperror/constructors.go` constructor、`apierror/codes.go` 常數、本 spec § 5 表格
4. backend handler 不得 `panic(string)` 或 `panic(error)` 傳遞業務錯誤；業務錯誤一律以 `*apperror.Error` 回傳
5. CLI / MCP 不得對 `error.message` 做 string match 分支；分支只能用 `error.code`
6. `internal_error` / `service_unavailable` 之 `details` 必為空（{}）；任何內部 cause 只進 server log
7. error envelope 必含 `trace_id`；未取得 trace 時以「全 0」UUID 字串占位並在 server log 標 warn（屬 trace propagation bug）
8. `preview_consumed` 重試**不**經 error envelope 路徑；走 200 + `idempotent_replay: true` 由 `preview-confirm-gate` spec 釘
