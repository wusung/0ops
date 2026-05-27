# Feature Spec：shared-dto-and-contract

> **狀態**：draft
> **來源**：`docs/0ops-plan.md`「Tool catalog」「DB schema」「Auth & RBAC」三段；ADR-0001、ADR-0002、ADR-0003
> **適用範圍**：CLI / MCP / backend 三方共用之 wire-level 型別與 contract 測試框架；不含個別 endpoint 的業務邏輯
> **對應 Milestone**：M0 末（地基；M1 read-only chain 動工前必須完成）

## 1. 結論（先讀本段）

- 共用型別集中於三個 package：`internal/shared/dto`、`internal/shared/preview`、`internal/shared/rbac`；backend、CLI、MCP 三方**只能**從這三個 package 取共用型別，不得各自重複定義
- DTO 為 wire-level（JSON tag 為 source of truth）；DB row（sqlc 產出）與 wire DTO 嚴格分離，由 `internal/server` 內部 mapper 轉換
- `PlanPreview` 結構為所有 `*:preview` endpoint 一致回傳；schema 由 `preview` package 唯一擁有，CLI / MCP 直接複用
- `Role` 與 `Scope` 為 typed string enum（非 free-form）；矩陣（`role × scope` → 是否允許）以**程式可查詢的查表函式**提供，避免靠註解同步
- 所有 DTO 改動須通過 contract test：backend handler 產出之 JSON，必能由 CLI/MCP 之 unmarshal 路徑無錯解析，且關鍵欄位 round-trip 等值
- 跨 binary 的測試契約以 `internal/shared/contracttest` 提供 fixture 與 round-trip helper；任一 binary 引入 DTO 即須掛上對應 contract test
- 版本策略：v1 期間 DTO 採「附加相容」（新增欄位允許，刪除/語意改動禁止）；breaking change 須走 `/v2` 路徑前綴或新欄位名

## 2. 範圍

### 2.1 包含
- `internal/shared/dto/` 內各資源（apps、teams、domains、deploys、repos、audit、members）之請求 / 回應 DTO 定義原則
- `internal/shared/preview/` 之 `PlanPreview`、`PreviewID`、相關錯誤碼常數
- `internal/shared/rbac/` 之 `Role`、`Scope`、權限矩陣查表
- 三方 contract test 框架（單元層 + httptest 層）
- DTO 命名規約、JSON tag 規約、時間 / UUID / 列舉的線上格式
- DTO 演進規則（附加相容 / breaking change 通道）

### 2.2 不包含
- 個別 endpoint 的 handler 邏輯（屬各 feature spec）
- DB schema 與 sqlc query 模板（屬 `0ops-plan.md` DB schema 段；本 spec 只規範 DB row → wire DTO 的轉換邊界）
- Error envelope 內容語意與錯誤碼列表（屬 `error-model` spec）
- `PlanPreview` 的產生與消費邏輯、`SELECT FOR UPDATE` race、TTL 清理（屬 `preview-confirm-gate` spec）
- MCP tool description 句式 lint（屬 `mcp-tool-description-lint` spec）

## 3. 檔案結構

```
0ops/
└── internal/
    └── shared/
        ├── dto/
        │   ├── apps.go              # AppRef, App, AppCreateArgs, AppUpdateArgs, ...
        │   ├── teams.go             # Team, TeamMember, MemberInviteArgs, ...
        │   ├── domains.go           # DomainBinding, DomainAddArgs, DomainVerifyResult
        │   ├── deploys.go           # DeployRun, DeployStatus, LogLine, RedeployArgs
        │   ├── repos.go             # RepoInspectArgs, RepoInspectResult, BuildpackDetect
        │   ├── audit.go             # AuditLogEntry, AuditQuery
        │   ├── github.go            # InstallGitHubAppArgs, UninstallGitHubAppArgs
        │   ├── time.go              # 時間序列化共用 helper（RFC3339Nano）
        │   └── doc.go               # package 用途與規約
        ├── preview/
        │   ├── plan.go              # PlanPreview, PreviewID, SideEffect
        │   ├── errors.go            # MissingPreviewID / Expired / Consumed / ForbiddenTeam 錯誤碼常數
        │   └── doc.go
        ├── rbac/
        │   ├── role.go              # Role typed string + Validate()
        │   ├── scope.go             # Scope typed string + Validate()
        │   ├── matrix.go            # Allow(role Role, scope Scope) bool；查表函式
        │   ├── action.go            # Action enum（create_app / delete_app / ...）+ RequiredFor(action)
        │   └── doc.go
        └── contracttest/
            ├── fixtures.go          # 各 DTO 的 golden fixture（JSON）
            ├── roundtrip.go         # marshal → unmarshal → equal helper
            ├── httptest_helpers.go  # 啟動 backend test server、CLI/MCP 拉取後比對
            └── README.md            # contract test 撰寫流程
```

## 4. DTO 設計原則

### 4.1 命名

| 類別 | 後綴 | 範例 |
|---|---|---|
| 完整資源實體 | 無 | `App`、`Team`、`DeployRun`、`DomainBinding` |
| 列表項摘要（欄位較少） | `Ref` | `AppRef`、`TeamRef` |
| 寫入請求 | `Args` | `AppCreateArgs`、`DomainAddArgs` |
| 寫入回應（preview 階段） | `Args` 配對 `PlanPreview` | 不另設 |
| 寫入回應（confirm 階段） | 完整資源實體 | `App`（confirm `create_app` 回 `App`） |
| 列表回應 | 切片 | `[]AppRef` |
| 查詢參數 | `Query` | `AuditQuery`、`AppListQuery` |

> `Args` 不用 `Request`，避免與 `*http.Request` 字面衝突；**讀取**端用 `Query` 表達 query string，**寫入**端用 `Args` 表達 body。

### 4.2 JSON 慣例

- 欄位 JSON tag 一律 `snake_case`
- 時間欄位以 RFC3339Nano string 序列化；對應 Go 型別為 `time.Time`，不用 unix epoch
- UUID 以字串形式序列化（小寫、含連字號）；對應 Go 型別建議 `string`，避免引入 UUID 套件之 wire 表示差異
- 列舉欄位以字串序列化（不用 int），值定義於 `rbac/` 或對應 dto 檔
- 可選欄位用 `*T` 或 `omitempty`：寫入 DTO 用 `*T`（區分「未提供」與「零值」）；讀取 DTO 對外保證欄位存在則用 `T`，可空者用 `omitempty`
- 巢狀物件用 `*Nested` 而非 inline anonymous struct；保證 reflect / 測試 fixture 可重用

### 4.3 DB row 與 wire DTO 嚴格分離

- sqlc 產出之型別僅限於 `internal/server/db`；不得將 sqlc struct 直接輸出到 HTTP response
- `internal/server/services/*` 或 `internal/server/routers/*` 內須有顯式 mapper 函式：`func toAppDTO(row db.App) dto.App`
- 反向：寫入路徑由 `dto.AppCreateArgs` 經 mapper 轉為 sqlc 參數
- **理由**：DB schema 演進（加 column、改 type）不應強迫變動 wire；wire 演進（重命名 JSON tag）也不應強迫 DB migration

### 4.4 不可變欄位語意

| 欄位 | 規則 |
|---|---|
| `id` / `team_id` / `slug` | 一旦回傳即不變；client 可作為 cache key |
| `created_at` | 建立後不變 |
| `updated_at` | 任何欄位變動時更新 |
| `status` | 字串列舉；新增允許值需走附加相容；刪除值禁止 |

## 5. `internal/shared/preview` 規格

### 5.1 `PlanPreview` 結構

```go
package preview

import "time"

type PreviewID string // UUID v4 字串形式

type SideEffect struct {
    Description string `json:"description"`            // 人類可讀
    Resource    string `json:"resource,omitempty"`     // e.g. "cloudflare:hostname"
    Reversible  bool   `json:"reversible"`             // 影響副作用順序
}

type PlanPreview struct {
    PreviewID     PreviewID    `json:"preview_id"`
    Action        string       `json:"action"`               // 對應 rbac.Action
    ActionSummary string       `json:"action_summary"`
    SideEffects   []SideEffect `json:"side_effects"`
    ExpiresAt     time.Time    `json:"expires_at"`
}
```

### 5.2 confirm body 規範

所有寫入 / 刪除主端點之 request body 必有 `preview_id` 欄位（top-level）：

```go
type ConfirmEnvelope struct {
    PreviewID PreviewID `json:"preview_id"`
}
```

具體 confirm 請求 DTO（如 `AppCreateConfirmArgs`）以 embed 的方式組合：

```go
type AppCreateConfirmArgs struct {
    ConfirmEnvelope
    // 其他 confirm 階段才補的欄位（罕見；多數情況 preview 已含全部 args）
}
```

> **與 ADR-0002 對齊**：`preview_id` 兼 idempotency key。confirm 重試帶同一 `preview_id` 由 backend 回 `last_result`，client 端 DTO 不變。

### 5.3 `preview/errors.go` 錯誤碼常數

定義於本 package 之常數字串，供 backend 產出與 client 比對：

```go
const (
    CodeMissingPreviewID = "missing_preview_id" // 400
    CodePreviewExpired   = "preview_expired"    // 410
    CodePreviewConsumed  = "preview_consumed"   // 200 + last_result（非錯誤；常數供 client 識別）
    CodeForbiddenTeam    = "forbidden_team"     // 403
)
```

> 完整錯誤 envelope 結構與其餘錯誤碼歸 `error-model` spec；本 package 只列 preview gate 直接相關者。

## 6. `internal/shared/rbac` 規格

### 6.1 `Role` 與 `Scope` typed string

```go
type Role string

const (
    RoleOwner  Role = "owner"
    RoleAdmin  Role = "admin"
    RoleMember Role = "member"
    RoleViewer Role = "viewer"
)

func (r Role) Validate() error { ... } // 非允許值即 error

type Scope string

const (
    ScopeAppsRead       Scope = "apps:read"
    ScopeAppsWrite      Scope = "apps:write"
    ScopeAppsDelete     Scope = "apps:delete"
    ScopeDomainsWrite   Scope = "domains:write"
    ScopeReposRead      Scope = "repos:read"
    ScopeAuditRead      Scope = "audit:read"
    ScopeMembersManage  Scope = "members:manage"
    ScopeTokensManage   Scope = "tokens:manage"
    ScopeTeamsRead      Scope = "teams:read"
)
```

### 6.2 `matrix.go` 查表

```go
// Allow 回傳 role 是否允許持有該 scope（device flow token 視為持有所有 scope；此函式只判 role 維度）
func Allow(role Role, scope Scope) bool { ... }

// RoleAtLeast 比較階級：owner > admin > member > viewer
func RoleAtLeast(have, need Role) bool { ... }
```

矩陣 source of truth 為 `0ops-plan.md` Auth & RBAC § Role 矩陣。實作以查表 map 表達，**不**用 if/else 鏈。新增 role 或 scope 時：先改 `0ops-plan.md` → 再改本 package → contract test 自動偵測 fixture 缺漏。

### 6.3 `action.go`

```go
type Action string

const (
    ActionCreateApp        Action = "create_app"
    ActionUpdateApp        Action = "update_app"
    ActionDeleteApp        Action = "delete_app"
    ActionRedeploy         Action = "redeploy"
    ActionAddDomain        Action = "add_domain"
    ActionRemoveDomain     Action = "remove_domain"
    ActionInviteMember     Action = "invite_member"
    ActionInstallGitHubApp Action = "install_github_app"
    ActionUninstallGitHubApp Action = "uninstall_github_app"
)

type Requirement struct {
    MinRole       Role
    RequiredScope Scope
}

// RequiredFor 回傳該 action 所需最低 role 與 scope；middleware 與 preview gate 共用。
func RequiredFor(a Action) Requirement { ... }
```

對應 `0ops-plan.md` Tool catalog 之「最低 role」與「scope」欄位。新增 action 須同時更新 plan.md 對應列。

## 7. Contract test 框架（`internal/shared/contracttest`）

### 7.1 三層測試金字塔

| 層次 | 範圍 | 使用工具 | 失敗門檻 |
|---|---|---|---|
| 單元 round-trip | DTO marshal → unmarshal → 等值 | `testing` + `encoding/json` | 任一 fixture 不通過 → fail |
| backend↔CLI contract | backend handler 產出 → CLI client 解析 | `httptest` + CLI 套件之 client 函式 | 解析失敗或 round-trip 不等 → fail |
| backend↔MCP contract | backend handler 產出 → MCP client 解析 | `httptest` + MCP `internal/mcp/client` | 同上 |

### 7.2 Fixture 規約

- 每個 DTO 至少一份 `golden.json` 放於 `internal/shared/contracttest/fixtures/<resource>/<dto>.json`
- 新增欄位：fixture 同步增；contract test 在 `t.Run` 中自動 enumerate
- 移除欄位：禁止；breaking change 走 v2 通道

### 7.3 必要測試案例

| 案例 | 說明 |
|---|---|
| 完整欄位 round-trip | 所有欄位填值，marshal → unmarshal 結果等值 |
| 可選欄位省略 | `omitempty` 欄位省略後 unmarshal 不報錯且取得零值 |
| 未知欄位忽略 | client 端對 backend 新增的未知欄位採忽略策略（不 fail） |
| 列舉值無效 | client 收到非允許 enum 值時的行為（依 DTO 標註：嚴格 fail vs 容錯） |
| `PlanPreview` 跨 binary | backend 產出 → CLI、MCP 兩端各自解析等值 |
| `Time` 序列化精度 | nanosecond 精度 round-trip 不丟 |

### 7.4 CI gating

- `./manage.sh contract-test`：跑 `go test ./internal/shared/contracttest/...`
- PR 改動 `internal/shared/dto/**` 或 `internal/server/routers/**` 必觸發 contract test
- contract test 紅 → PR 不可合入（屬 AGENTS.md「Testing」§ 高風險區域）

## 8. 命名與版本演進

### 8.1 v1 期間附加相容（additive-compatible）

允許：
- 新增可選欄位（`omitempty` 或 `*T`）
- 列舉新增允許值（client 須以「未知值容錯」處理）
- 新增 endpoint

禁止：
- 改既有欄位的 JSON tag 名
- 改既有欄位的型別或語意
- 移除欄位
- 列舉移除允許值

### 8.2 Breaking change 通道

僅在以下其一觸發：
1. 升 API major（`/v2` 前綴）
2. 廢棄欄位 → 新欄位名共存 ≥ 1 個 release，期間舊欄位 mark deprecated（godoc + JSON tag 註解）；後續 release 移除

廢棄期間 contract test 必含「同時填新舊欄位、回應仍正確」案例。

### 8.3 程式碼層工具

- `golangci-lint` 的 `gochecksumtype` / 自定 `forbidigo`：禁止在 `internal/server/routers/` 直接 import sqlc package 之外回傳給 handler
- `go vet` + 自訂 analyzer（v1.1 後考慮）：偵測 `dto` package 內欄位 JSON tag 變動

## 9. 驗證準則

| 驗證項 | 方式 | 通過條件 |
|---|---|---|
| 三方 import 邊界 | grep `internal/server/db` 不被 `internal/cli` / `internal/mcp` import | 無命中 |
| DTO round-trip | `go test ./internal/shared/contracttest/...` | 全綠 |
| RBAC 矩陣與 plan 一致 | 自動測試讀 plan.md 表格與 `rbac.matrix` 比對 | 表格值完全相符 |
| `PlanPreview` 跨 binary 等值 | backend httptest 產出 → CLI/MCP 各自解析 | 三方反序列化結果欄位相等 |
| 未知欄位忽略 | client 對含未宣告欄位之 JSON 解析 | 不報錯，已知欄位值正確 |
| 列舉無效值處理 | client 對非允許 enum 值 | 依標註：strict 模式 fail / lenient 模式保留原字串 |
| sqlc row 不外洩 | `go vet` + custom check | 任一 router/service 直接 return sqlc struct → fail |

## 10. 對 `docs/0ops-plan.md` 的修改清單

本 spec 不要求變更 plan.md，但下列段落於本 spec 完成後應交叉引用：

1. 「Tool catalog」表新增註解：「最低 role」與「scope」欄位由 `internal/shared/rbac/action.go` 之 `RequiredFor()` 提供 source of truth；plan 表為文件呈現
2. 「Auth & RBAC」§ Role 矩陣新增註解：矩陣以查表函式 `internal/shared/rbac/matrix.Allow()` 實作
3. 「DB schema」段新增註解：sqlc 產出之 row 不得直接作為 wire 回應；轉換責任在 `internal/server` 內 mapper

## 11. Open issues

- 是否引入 OpenAPI / JSON Schema 自動產 DTO：v1 暫採手寫 + contract test；v1.1 評估自動化效益
- `omitempty` vs `*T` 在 PATCH 半更新的選擇：本 spec 採 `*T`；具體 PATCH 行為待 `update_app` 對應 spec 釘
- 對外 API 是否需 envelope（`{"data": ..., "meta": ...}`）：v1 採直接序列化資源；envelope 待外部 SDK 議題出現再評估
- Contract test 是否要測 SSE（log follow）幀格式：本 spec 暫不含；歸 `read-api-vertical-slice` spec 內定義 SSE event 結構與測試

## 12. 不可違反的硬性規則

> 違反以下任一項，PR 不可合入。

1. `internal/cli` 與 `internal/mcp` 不得 import `internal/server/db`；CLI/MCP 與 backend 共享之型別只能來自 `internal/shared/{dto,preview,rbac}`
2. backend handler / router 不得直接回應 sqlc struct；必經 `internal/shared/dto` 型別
3. `Role`、`Scope`、`Action` 不得以 raw string literal 出現於 `internal/server/routers/`、`internal/cli/commands/`、`internal/mcp/tools/`；必須引用 `rbac` package 常數
4. 新增 / 修改 `internal/shared/dto/**` 之 PR 必含對應 contract test 異動；CI 偵測無變動 → fail
5. JSON tag 一律 `snake_case`；不得使用 `camelCase` 或 `PascalCase`
6. 時間欄位序列化必為 RFC3339Nano；不得使用 unix epoch、不得使用無時區 string
7. 列舉欄位序列化必為字串；不得使用整數 ordinal
8. v1 期間對 `dto/**` 既有欄位的「重命名」「型別變更」「移除」一律不可；要改走 `/v2` 通道
