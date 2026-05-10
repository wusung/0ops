# Feature Spec：read-api-vertical-slice

> **狀態**：draft
> **來源**：`docs/0ops-plan.md`「Tool catalog」「Go 技術棧」「使用者腳本範例 Pattern A/B」段；ADR-0001（URL routing）；ADR-0003（MCP）；本 spec 依賴 `shared-dto-and-contract`、`error-model`、`auth-and-rbac`、`observability-skeleton`、`mcp-tool-description-lint`
> **適用範圍**：M1 之 read-only vertical slice：backend route → CLI command → MCP tool 三層串通；含 SSE log follow（依 ADR-0003 spike 結果為 streaming 或分頁）
> **對應 Milestone**：M1（完成標準包含本 spec 之全部驗證項）

## 1. 結論（先讀本段）

- M1 vertical slice 含 7 條 read endpoint：`list_apps`、`get_app`、`inspect_repo`、`tail_logs`、`get_deploy_status`、`list_domains`、`list_teams`（含 `/v1/me/teams`）
- 三層串通的 source of truth 為 `internal/shared/dto`：backend handler 回 DTO、CLI client unmarshal DTO、MCP tool 取 DTO 後 marshal 為 `mcp.CallToolResult.Content`
- `0ops teams use <slug>` 為**純 client 端**動作；改本機 `auth.json.default_team_slug`，不打 backend
- SSE log follow（`tail_logs`）走 ADR-0003 Spike B1：官方 SDK 支援 streaming → MCP 用 streaming；不支援 → 退分頁 + cursor。CLI 端一律用 backend SSE（`text/event-stream`）+ `--follow`，與 MCP 路徑解耦
- CLI 輸出三格式：`table`（預設）、`json`、`yaml`；ENV `OPS_OUTPUT` 可全域改預設；個別命令以 `--output` 覆寫
- backend 對所有 list endpoint 強制分頁（`?page_size=N&cursor=...`）；預設 page_size = 50、最大 200；無 cursor 時從 head 開始
- Cross-team 列表透過 `/v1/me/*`（不需 `team_slug`）；`/v1/me/apps` 列當前 user 在所有 team 之 app；隔離仍由 `team_membership` 過濾
- 7 條 read endpoint 必含 contract test（backend httptest + CLI client + MCP tool 三方對 DTO 解析等值）；屬 `shared-dto-and-contract` § 7 框架之第一批用例

## 2. 範圍

### 2.1 包含
- 7 條 read endpoint 的 backend handler、SQL query、DTO 對映
- CLI 對應命令：output 格式、互動 prompt（無）、context（current team）解析
- MCP 對應 tool：input schema、output 格式、auth 依賴
- SSE 串流之 backend / CLI / MCP 三端行為
- 分頁協定（cursor 編碼、page_size 上限）
- `/v1/me/*` 跨 team 端點之獨立 routing
- M1 完成標準的具體驗收清單

### 2.2 不包含
- 寫入 / 刪除類 endpoint（屬批次 3 之 `preview-confirm-gate` 與各 feature spec）
- `inspect_repo` 之 GitHub App installation token 取得（屬 `github-app-install-flow` spec；本 spec 假設 token 可取）
- log 來源 K8s API client 細節（屬 `k3s-namespace-isolation` spec；本 spec 只規範 SSE wire format）
- M1 之 observability metrics 暴露本身（屬 `observability-skeleton` spec；本 spec 只規範本批 endpoint 觸發 metric 的點）
- audit log 寫入（read endpoint 不寫 audit；audit 屬寫入路徑）

## 3. 檔案結構

```
0ops/
├── internal/
│   ├── server/
│   │   ├── routers/
│   │   │   ├── apps.go              # GET /v1/teams/{slug}/apps + /apps/{appslug}
│   │   │   ├── deploys.go           # GET /v1/teams/{slug}/apps/{appslug}/{logs,deploy-status}
│   │   │   ├── domains.go           # GET /v1/teams/{slug}/apps/{appslug}/domains
│   │   │   ├── repos.go             # POST /v1/teams/{slug}/repos:inspect
│   │   │   ├── teams.go             # GET /v1/me/teams + /v1/me/apps
│   │   │   └── health.go            # /healthz、/readyz（不在 RBAC chain）
│   │   ├── services/
│   │   │   ├── repointrospect/      # GitHub API 取 repo 元資料 + buildpack detect
│   │   │   └── k8sstatus/           # K8s API 取 pod log SSE、deploy status
│   │   └── db/
│   │       └── queries.sql          # sqlc 模板：本批 7 條 read query
│   ├── cli/
│   │   ├── commands/
│   │   │   ├── teams.go             # 0ops teams {list,use}
│   │   │   ├── apps.go              # 0ops apps {list,get}
│   │   │   ├── repo.go              # 0ops repo inspect
│   │   │   ├── deploys.go           # 0ops deploys {logs,status}
│   │   │   └── domains.go           # 0ops domains list
│   │   ├── client/
│   │   │   ├── client.go            # *http.Client 包裝、retry、429 處理
│   │   │   ├── apps.go              # type-safe per-resource methods
│   │   │   └── sse.go               # text/event-stream 解析
│   │   └── output/
│   │       ├── table.go             # tablewriter 寫表
│   │       ├── json.go              # encoding/json
│   │       └── yaml.go              # gopkg.in/yaml.v3
│   └── mcp/
│       ├── tools/
│       │   ├── list_teams.go
│       │   ├── list_apps.go
│       │   ├── get_app.go
│       │   ├── inspect_repo.go
│       │   ├── tail_logs.go
│       │   ├── get_deploy_status.go
│       │   └── list_domains.go
│       └── client/
│           └── client.go            # 共用 backend HTTP client（與 CLI 對等）
└── migrations/
    └── 000X_indexes.sql             # 本批 query 必要的索引
```

## 4. Endpoint 規格

### 4.1 路由與 RBAC（與 plan.md Tool catalog 對齊）

| Tool / CLI / Endpoint | 最低 role | scope | 路由 |
|---|---|---|---|
| `list_apps` / `0ops apps list` | viewer | `apps:read` | `GET /v1/teams/{team_slug}/apps?page_size=&cursor=` |
| `get_app` / `0ops apps get <slug>` | viewer | `apps:read` | `GET /v1/teams/{team_slug}/apps/{app_slug}` |
| `inspect_repo` / `0ops repo inspect <url>` | member | `repos:read` | `POST /v1/teams/{team_slug}/repos:inspect` |
| `tail_logs` / `0ops deploys logs <slug>` | viewer | `apps:read` | `GET /v1/teams/{team_slug}/apps/{app_slug}/logs?lines=&follow=&cursor=` |
| `get_deploy_status` / `0ops deploys status <slug>` | viewer | `apps:read` | `GET /v1/teams/{team_slug}/apps/{app_slug}/deploy-status` |
| `list_domains` / `0ops domains list <slug>` | viewer | `apps:read` | `GET /v1/teams/{team_slug}/apps/{app_slug}/domains` |
| `list_teams` / `0ops teams list` | — | `teams:read` | `GET /v1/me/teams` |
| `0ops teams use <slug>` | — | — | （client 端，不打 backend） |

> `inspect_repo` 雖為「讀 repo」但 `member` 起跳：取 GitHub installation token 屬 side-effect-touching 操作，且只有 member 之 `team` 才有對應 install。

### 4.2 分頁協定

```
GET /v1/teams/{slug}/apps?page_size=50&cursor=eyJpZCI6Ii4uIn0
→ 200
{
  "items": [...],
  "next_cursor": "eyJpZCI6Ii4uIn0",   // 無下一頁時為 null
  "page_size": 50
}
```

- `cursor` 為 base64-encoded JSON：`{"id": "<last_uuid>", "ts": "<rfc3339nano>"}`
- 預設 `page_size = 50`，最大 200；超出回 `400 validation_failed` + `details.fields[]`
- backend 用 `(team_id, id)` 索引避免 OFFSET；查詢條件 `WHERE team_id = $1 AND id > $2 ORDER BY id LIMIT page_size + 1`
- 多取一筆判斷是否有下一頁（取出第 N+1 筆 → `next_cursor` 設為第 N 筆 id；丟棄第 N+1 筆）
- CLI / MCP **不**自動翻頁；分頁為 client 責任；CLI 提供 `--all` flag 自動翻頁直到結尾，MCP 不提供（避免 LLM 失控翻整個 team）

### 4.3 `inspect_repo` 行為

```
POST /v1/teams/{slug}/repos:inspect
{ "url": "https://github.com/vercel/next.js-helloworld", "ref": "main" }
→ 200
{
  "url": "https://github.com/vercel/next.js-helloworld",
  "ref": "main",
  "commit_sha": "abc123...",
  "default_branch": "main",
  "buildpack": {
    "detected_languages": ["nodejs"],
    "builder": "paketobuildpacks/builder-jammy-base",
    "primary_port": 3000,
    "build_image_size_estimate_mb": 250
  },
  "github_app_status": "installed_with_access"  // installed_with_access | installed_no_access | not_installed
}
```

- 結果快取 5 分鐘（`patrickmn/go-cache`，key = `team_id + url + ref`）；同 team 對同 repo 重複呼直接命中
- `not_installed` 回 200 但 `buildpack.detected_languages` 為空；CLI / MCP 提示跑 `0ops teams github install`
- `installed_no_access` 回 200 + `buildpack=null` + 提示 GitHub App 需勾選該 repo
- 不真打 `pack build`（成本高且需 daemon）；改以靜態檔案掃描（`package.json`、`pyproject.toml`、`go.mod` 等）+ paketo builder image label 對映；準確度約 90%，剩餘 10% 在實際 build 時於 GHA 端揭露

### 4.4 `tail_logs` 行為

#### CLI 端

```
$ 0ops deploys logs nextdemo --follow --lines=200
[2026-05-10T12:34:56Z] starting server on :3000
[2026-05-10T12:34:57Z] ready
^C
```

- 預設 `--lines=100`，最大 5000；`--follow` 開啟 SSE
- backend 回 `text/event-stream`：每行一 event，`data: {"ts":"...","level":"info","msg":"..."}\n\n`
- CLI client（`internal/cli/client/sse.go`）解析 SSE event；遇 `event: end` 結束 stream
- ctrl-C 中斷送 `Connection: close`；server 端取消 K8s log stream

#### MCP 端

依 ADR-0003 Spike B1 結果：

| Spike 結果 | MCP 行為 |
|---|---|
| 官方 SDK 支援 streaming tool result | tool 直接 stream；result `Content` 多次 append |
| 不支援 | 採分頁：`tail_logs(team_slug, app_slug, cursor?)` → `{lines: [...], next_cursor: "..."}`；client 重複呼至 `next_cursor=null` |

> 本 spec 假設 spike 未完成；實作時依 `docs/runbooks/mcp-sdk-spike-results.md` 結論補入具體 API 名稱。

#### SSE 與 cursor 同步

- backend SSE 之每 event 含 `id: <rfc3339nano>`；CLI 重連時帶 `Last-Event-ID` header
- backend 用此 cursor 對 K8s log API 之 `SinceTime` 接續（接續 ADR-0008 § 4 SSE stateless cursor 設計）
- 即使 v1 single replica，cursor 行為仍實作（M5 升 2 replica 時不需改 client）

### 4.5 `get_deploy_status`

```
GET /v1/teams/{slug}/apps/{app_slug}/deploy-status
→ 200
{
  "current_deploy_run": {
    "id": "...",
    "status": "live",            // queued | preparing | building | pushing | rendering | syncing | live | failed | compensating | rolled_back
    "stage": "live",             // 細粒度，與 status 對齊
    "commit_sha": "abc123",
    "ref": "main",
    "started_at": "...",
    "finished_at": "...",
    "trace_id": "0af7651916cd43dd8448eb211c80319c",
    "events": [                  // 階段轉移時序
      {"at": "...", "from": "queued", "to": "preparing"},
      ...
    ],
    "failure_classification": null  // 非 failed 時為 null
  },
  "last_successful_deploy_run_id": "..."   // null 表示從未成功
}
```

> M1 範圍：本端點只回現況不含長期歷史；歷史查詢屬 v1.1 / `audit-log` spec。

### 4.6 `/v1/me/*` 跨 team 端點

| 端點 | 回傳 |
|---|---|
| `GET /v1/me/teams` | 當前 user 之 `[{team_slug, team_name, role, plan}]` 列表 |
| `GET /v1/me/apps?page_size=&cursor=` | 當前 user 在所有 team 之 app 列表（含 `team_slug` 欄位）|

- 不經 `ResolveTeam` / `CheckMembership`；只經 `AuthBearer` + `CheckTokenScope`（scope = `teams:read` / `apps:read`）
- backend SQL 直 join `team_membership` 過濾；不暴露 user 沒參與的 team 之資料

## 5. CLI 行為

### 5.1 命令矩陣

| 命令 | 路徑 | flag 重點 |
|---|---|---|
| `0ops teams list` | `GET /v1/me/teams` | `--output` |
| `0ops teams use <slug>` | client 端 only | 改 `auth.json.default_team_slug`；無網路呼叫 |
| `0ops apps list [--team=<slug>]` | `GET .../apps` | `--all`（自動翻頁）、`--output` |
| `0ops apps get <slug>` | `GET .../apps/{slug}` | `--output` |
| `0ops repo inspect <url> [--ref=main]` | `POST .../repos:inspect` | `--output` |
| `0ops deploys logs <app_slug> [--follow] [--lines=N]` | SSE | `--lines`、`--follow`、`--output=raw\|json` |
| `0ops deploys status <app_slug>` | `GET .../deploy-status` | `--output` |
| `0ops domains list <app_slug>` | `GET .../domains` | `--output` |

### 5.2 Output 格式

| 格式 | 用途 | 實作 |
|---|---|---|
| `table`（預設） | 人類閱讀；自動截斷長欄位 | `olekukonko/tablewriter` |
| `json` | 腳本 / `jq` pipeline | `encoding/json` MarshalIndent |
| `yaml` | 設定 / patch 範本 | `gopkg.in/yaml.v3` |
| `raw`（僅 logs） | 原始輸出，不加色彩 | `io.Copy` |

- `table` 欄位每命令固定（不可由 user 改）；JSON / YAML 直接序列化 DTO
- 全域 ENV `OPS_OUTPUT=json` 切預設；CLI flag 覆寫 ENV
- 表格寬度由 `tablewriter.SetAutoWrapText(true)` 控制；終端寬度自動偵測

### 5.3 Current team 解析

優先序：

1. `--team=<slug>` flag
2. `OPS_TEAM=<slug>` ENV
3. `auth.json.default_team_slug`（由 `0ops teams use` 設定；首次登入填 `personal-{login}`）
4. 無 → CLI 直接 fail：`Error: no team in context. Run 0ops teams use <slug> or pass --team.`

## 6. MCP 行為

### 6.1 Tool input schema

```json
// list_apps
{
  "type": "object",
  "properties": {
    "team_slug": {"type": "string"},
    "page_size": {"type": "integer", "minimum": 1, "maximum": 200, "default": 50},
    "cursor": {"type": "string"}
  },
  "required": ["team_slug"]
}
```

- 所有 read tool 之 `team_slug` 必填（與 ADR-0003 對齊）；唯一例外：`list_teams` 不需 `team_slug`
- LLM 若漏傳 `team_slug` → SDK 端 schema 校驗 fail，回 MCP error；不打 backend
- description 不需強制句式（`mcp-tool-description-lint` § 4.5）

### 6.2 Output 格式

- read tool result 為 `Content[0].text` 之 JSON 字串（與 backend DTO 一致）
- LLM 自行從 JSON 取欄位呈現給 user；本 spec 不要求 MCP 端做美化
- 例外：`tail_logs` 為串流（streaming 或分頁），output 結構見 § 4.4

### 6.3 Auth 依賴

- 啟動時讀 `~/.config/0ops/auth.json`（與 `auth-and-rbac` § 8.2 對齊）
- 無 token / token 過期 → tool 回 `IsError: true` + envelope `{code: "unauthorized", ...}`
- 多 host 切換：本 spec 採「以 `auth.json.tokens[0].host` 為當前 host」，不允許 MCP 端切換 host；若需多 host，user 在 host shell 改 `OPS_HOST` 後重啟 MCP

## 7. SSE wire format

```
HTTP/1.1 200 OK
Content-Type: text/event-stream
Cache-Control: no-cache
Connection: keep-alive

id: 2026-05-10T12:34:56.123456789Z
event: log
data: {"ts":"2026-05-10T12:34:56.123456789Z","level":"info","msg":"starting server"}

id: 2026-05-10T12:34:57.000000000Z
event: log
data: {"ts":"2026-05-10T12:34:57Z","level":"info","msg":"ready"}

event: end
data: {"reason":"client_disconnect"}
```

- `id`：RFC3339Nano；CLI / MCP 重連時送 `Last-Event-ID: <id>`
- `event`：`log`（資料）/ `end`（流結束）/ `error`（流中斷且不可恢復）
- `data`：JSON-encoded `LogLine` DTO（`internal/shared/dto/deploys.go`）
- 心跳：閒置 15 秒送 `event: heartbeat` 防 NAT timeout
- backend 端讀 K8s API `corev1.Pod.GetLogs(...).Stream(ctx)`；遇 EOF 不立即關 SSE，等 K8s API 重試 3 次仍 EOF 才送 `event: end`

## 8. Contract test 套用

依 `shared-dto-and-contract` § 7 之三層金字塔：

| 測試 | 範圍 | Fixture |
|---|---|---|
| 單元 round-trip | 7 條 read 之 DTO | `fixtures/{apps,deploys,domains,repos,teams}/golden.json` |
| backend ↔ CLI | httptest backend → CLI client method 解析 | 共用 fixture |
| backend ↔ MCP | httptest backend → MCP tool `Call()` → 驗證 `Content[0].text` 解析 | 共用 fixture |
| SSE | backend SSE → CLI 解析 + MCP（streaming or 分頁）解析 | `fixtures/sse/log_stream.txt` |
| 分頁 | 多頁取完 + 中途中止 | `fixtures/pagination/three_pages.json` |
| 跨 team enumeration | team A user 呼 team B endpoint | 期望 `404 team_not_found` |

## 9. M1 完成標準（與 plan.md Milestones 對齊）

| 項目 | 通過條件 |
|---|---|
| 7 條 read endpoint backend | 全部 unit test + httptest 通過 |
| CLI 命令 | `0ops teams list/use`、`apps list/get`、`repo inspect`、`deploys logs/status`、`domains list` 全部以 `table/json/yaml` 輸出正確 |
| MCP 對應 tool | claude code 端啟 MCP，`tools/list` 列 7 條，逐條 `Call()` 通過 |
| Middleware chain | `AuthBearer → ResolveTeam → CheckMembership → CheckTokenScope` 五段於本批 endpoint 全套用 |
| `/metrics` | 暴露 `0ops_http_requests_total` 與 `0ops_http_request_duration_seconds`，含 `team_bucket` label |
| trace propagation | 一條 CLI / MCP 呼叫，backend log 含對應 `trace_id`；SSE 每 event 之 log 同 trace_id |
| Cross-team enumeration | team A 之 user 呼 team B endpoint 回 `404`，非 403 |
| Contract test | `make contract-test` 全綠 |
| MCP description lint | `0ops-mcp` 啟動不 fail（read tool 不觸 R1/R2） |

## 10. 性能目標（對齊 ADR-0006 SLO）

| 端點 | p95 目標 | 量測 |
|---|---|---|
| `list_apps` / `get_app` / `list_domains` / `list_teams` | < 200ms | `0ops_http_request_duration_seconds` p95 |
| `inspect_repo`（cache hit） | < 50ms | 同上 |
| `inspect_repo`（cache miss，含 GitHub API） | < 1500ms | 同上 |
| `get_deploy_status` | < 200ms | 同上 |
| SSE first byte | < 500ms | backend log 計算 K8s API streaming 連上至首 byte |

## 11. 對 `docs/0ops-plan.md` 的修改清單

1. 「Tool catalog」表交叉引用本 spec § 4.1 為路由 source of truth；plan 表保留總覽
2. 「核心元件設計 / MCP server」段：補入「read tool 不強制句式（lint R1/R2 不適用）」交叉引用 `mcp-tool-description-lint` § 4.5
3. 「使用者腳本範例 Pattern A/B」段：交叉引用本 spec § 4 / § 5 為 endpoint / CLI 行為實作 source
4. 「Verification / Smoke」段：M1 vertical slice 範圍以本 spec § 9 為驗收清單
5. 「Milestones / M1」之「完成標準」：以本 spec § 9 為展開細節

## 12. Open issues

- `inspect_repo` 之靜態掃描 vs 真 `pack build` 之選擇：v1 採靜態 + 標明準確度約 90%；M2 後若回報率高，評估 GHA 內補真 build dry-run 的 endpoint
- SSE 心跳間隔 15s：M5 多 replica 後可能需與 K8s LB idle timeout 對齊（GKE 預設 30 分鐘、AWS NLB 350s）；v1 single replica 不影響
- `--all` 自動翻頁的硬上限：CLI 端建議設 100 頁安全網（5000 筆），避免 user 不小心拉 100k 筆 app；待實測
- log 等級過濾：v1 不在 backend 過濾（K8s log 為 stdout 一條流）；CLI / MCP 端 client-side 過濾為 v1.1
- `tail_logs` MCP streaming spike 結果：M0 尾段落地至 `docs/runbooks/mcp-sdk-spike-results.md` 後，本 spec § 4.4 補具體 SDK API 名稱
- `inspect_repo` 之 5 分鐘快取於 user 推 commit 後 stale 的處理：v1 不做 cache invalidation；user 重 inspect 即可（成本可接受）
- backend 對 `Last-Event-ID` cursor 過舊（如 24h 前）的處理：v1 直接從 head 接續並送 `event: log_truncated` 事件提示 client；K8s 預設 log 保留期視 ring buffer 大小

## 13. 不可違反的硬性規則

> 違反以下任一項，PR 不可合入。

1. 7 條 read endpoint 必經 middleware chain（含 `AuthBearer / ResolveTeam / CheckMembership / CheckTokenScope`）；禁止 hand-roll 鏈順序
2. List endpoint 必含分頁參數；無分頁的全表查詢端點不存在
3. CLI / MCP 對 backend DTO 必經 `internal/shared/dto`；不得另定型別
4. `0ops teams use` 必為 client 端 only；禁止打 backend
5. 跨 team 嘗試讀必回 `404 team_not_found`（與 `auth-and-rbac` § 7 對齊）
6. SSE event `id` 必為 RFC3339Nano；client 重連 `Last-Event-ID` 必被 backend 接受
7. `tail_logs` MCP 行為依 spike 結果二擇一；不得自簽 streaming 協定
8. read tool description 不得加 `ALWAYS call this BEFORE` / `NEVER call this tool without`（會誤觸 lint 並誤導 LLM）
9. `inspect_repo` 不可真執行 `pack build`；只允許靜態掃描 + GitHub metadata
10. backend log endpoint 不得讀 `node-level` log（如 `journalctl`）；只走 K8s API
