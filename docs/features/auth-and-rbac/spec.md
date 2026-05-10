# Feature Spec：auth-and-rbac

> **狀態**：draft
> **來源**：`docs/0ops-plan.md`「Auth & RBAC」「DB schema」段；ADR-0001（多租戶與 RBAC）；ADR-0006（trace propagation 對 audit 需求）
> **適用範圍**：CLI / MCP 取得與保存 token 的流程、backend 對每個 request 的身份與權限決策；不含 GitHub App install 的 install/uninstall 業務流（見 `github-app-install-flow` spec）、不含 GitHub webhook 簽章驗證細節（見 `webhook-and-redeploy` spec）
> **對應 Milestone**：M1（middleware chain 與 device flow 為 read-only API 前置）

## 1. 結論（先讀本段）

- 認證有兩種 token，**皆走 `Authorization: Bearer <token>` 進 backend**：device flow token（user 互動式取得）與 PAT（CI / 自動化）
- 兩種 token 都綁 team：device flow token 視為持有所有 scope 但仍受 role 限制；PAT 綁定單一 team 並攜帶 scope 子集
- Middleware chain 固定五段：`RequestID → AuthBearer → ResolveTeam → CheckMembership → CheckTokenScope`；前置由 `Logger / Recovery / Tracing(otel)` 負責，本 spec 只規範後五段
- **Defense-in-depth 第一道**：所有 sqlc query 模板強制 `WHERE team_id = $1`；不存在缺少 team_id 的 query
- **Defense-in-depth 第二道**：middleware 鏈三層判斷（membership / role / scope）；任一層失敗回 4xx 並走 `error-model` envelope
- **Cross-team enumeration 防範**：team A 之 user 嘗試 `/v1/teams/teamB/...` 一律回 `404 team_not_found`，**不**回 `403`，避免列舉 slug
- 首次 device flow 登入自動建立 `personal-{github_login}` team 並設 user 為 `owner`
- PAT 雜湊以 argon2id 儲存；明文僅在 `tokens create` 回應一次，不可二次取得；90 天過期，過期前 14 天 CLI 主動警告
- Token 檔位置：`~/.config/0ops/auth.json`（perm 0600）；CLI 與 MCP 共用同一份；無 token 時 MCP tool 回應引導 user 跑 `0ops auth login`
- Backend 不持有任何「跨 team」的高權限旁通機制；超級管理操作走 DB 直接 SQL（runbook），不開 admin endpoint

## 2. 範圍

### 2.1 包含
- GitHub OAuth device flow：CLI 顯示 → user 瀏覽器確認 → backend 簽發 0ops bearer token
- PAT lifecycle：建立、列表、撤銷、過期警告、雜湊與比對
- Personal team auto-provisioning（首次登入）
- `internal/server/auth/` 之 middleware chain 五段實作
- `cli_token` 表之欄位語意、查詢路徑、scope 持有規則
- Cross-team enumeration 防範規則
- Token 檔（`~/.config/0ops/auth.json`）格式與權限、CLI / MCP 共讀規約
- 401 / 403 錯誤碼與 `error-model` 對齊

### 2.2 不包含
- GitHub App install 的安裝 / 解除 / installation token 流程（屬 `github-app-install-flow` spec）
- GitHub webhook 簽章驗證、`webhook_dedup` 行為（屬 `webhook-and-redeploy` spec）
- 內部 deploy callback HMAC（屬 `build-pipeline-and-callback` spec）
- Role 矩陣、Scope 列舉之 source-of-truth 程式定義（屬 `shared-dto-and-contract` spec § 6）
- 錯誤 envelope 結構（屬 `error-model` spec）
- Audit log 寫入語意（屬 `audit-log` spec；本 spec 只標 audit hook 點）
- Web UI 登入流程（v2）

## 3. 檔案結構

```
0ops/
├── internal/
│   ├── server/
│   │   ├── auth/
│   │   │   ├── bearer.go           # AuthBearer middleware：解 token、設 actor_user_id
│   │   │   ├── device.go           # device flow handlers：start, poll, callback
│   │   │   ├── pat.go              # PAT create / list / revoke handlers + argon2id
│   │   │   ├── resolveteam.go      # ResolveTeam middleware：URL slug → team row
│   │   │   ├── membership.go       # CheckMembership middleware
│   │   │   ├── scope.go            # CheckTokenScope middleware
│   │   │   ├── personalteam.go     # 首次登入 provisioning
│   │   │   └── doc.go
│   │   └── routers/
│   │       └── auth.go             # /v1/auth/device/{start,poll,callback}, /v1/auth/tokens
│   ├── cli/
│   │   ├── commands/auth/
│   │   │   ├── login.go            # 0ops auth login（device flow）
│   │   │   ├── logout.go           # 0ops auth logout
│   │   │   ├── status.go           # 0ops auth status
│   │   │   └── tokens.go           # 0ops auth tokens {create,list,revoke}
│   │   └── ctx/
│   │       └── auth.go             # 讀寫 ~/.config/0ops/auth.json
│   └── mcp/
│       └── auth/
│           └── reader.go           # 讀同一份 auth.json；無 token 時回引導訊息
└── migrations/
    └── 000X_cli_token.sql          # cli_token 表（plan.md 已定義；本 spec 不重述）
```

## 4. Authentication

### 4.1 GitHub OAuth device flow

順序：

```
CLI                               Backend                       GitHub
 |                                 |                              |
 | POST /v1/auth/device/start      |                              |
 |-------------------------------->|                              |
 |                                 | POST /login/device/code      |
 |                                 |----------------------------->|
 |                                 |    user_code, device_code,   |
 |                                 |    verification_uri, ...     |
 |                                 |<-----------------------------|
 |   user_code, verification_uri,  |                              |
 |   poll_token, interval, ttl_s   |                              |
 |<--------------------------------|                              |
 | <顯示給 user：開瀏覽器、輸 code> |                              |
 |                                 |                              |
 | (user 在瀏覽器上完成授權)        |                              |
 |                                 |                              |
 | POST /v1/auth/device/poll       |                              |
 |   { poll_token }                |                              |
 |-------------------------------->|                              |
 |                                 | POST /login/oauth/access_token (poll)
 |                                 |----------------------------->|
 |                                 |    access_token              |
 |                                 |<-----------------------------|
 |                                 | (取得 GitHub user 資訊       |
 |                                 |  → upsert user_account       |
 |                                 |  → 確保 personal team 存在)  |
 |     0ops bearer token           |                              |
 |     + default team_slug         |                              |
 |<--------------------------------|                              |
 | <寫入 ~/.config/0ops/auth.json> |                              |
```

### 4.2 端點規格

| Method | Path | Body | 回應 |
|---|---|---|---|
| POST | `/v1/auth/device/start` | `{}` | `{user_code, verification_uri, poll_token, interval_s, ttl_s}` |
| POST | `/v1/auth/device/poll` | `{poll_token}` | 待授權 → `202 {status: pending, retry_after_s}`；完成 → `200 {bearer_token, default_team_slug, github_login}` |
| POST | `/v1/auth/logout` | `{}` (Bearer 帶當前 token) | `204`；revoke 該 device flow token |

`poll_token` 為 server 端短期 token（HMAC），不對應 GitHub `device_code`；CLI 不接觸 GitHub 端 token。

### 4.3 0ops bearer token 結構

- 形式：URL-safe random 32 bytes base64 → 字串長度 ~43；prefix `op_dev_`（device flow）以利 log 識別
- DB 不存明文，僅存 `argon2id(token)`（與 PAT 共用同一表 `cli_token`，欄位 `kind = 'device' | 'pat'`，本 spec 視為 plan.md schema 之擴充列舉，需在 migration 加入）
- 過期：device flow token 預設 30 天滾動更新（每次成功請求若距上次更新 > 24h 則 backend 回 header `X-0ops-Token-Refreshed: <new_token>`，CLI 無感換 token；若 token 已過期則 401 引導重 login）
- 撤銷：`logout` 標 `revoked_at`；user 刪 `auth.json` 等同 client 端登出但 server 端 token 仍有效（直至過期或 revoke）

> **schema 擴充**：plan.md `cli_token` 表目前未含 `kind` 欄位。本 spec 要求新增 `kind text not null check(kind in ('device','pat')) default 'pat'`；列入 § 11「對 plan.md 修改清單」。

### 4.4 PAT lifecycle

| 動作 | CLI | API | 行為 |
|---|---|---|---|
| 建立 | `0ops auth tokens create --team=<slug> --name=ci --scopes=apps:read,apps:write [--expires=90d]` | `POST /v1/teams/{slug}/tokens` | 產 32-byte random、回應一次明文（prefix `op_pat_`）；DB 存 argon2id |
| 列表 | `0ops auth tokens list [--team=<slug>]` | `GET /v1/teams/{slug}/tokens` | 不含明文；列 name、scopes、created_at、last_used_at、expires_at |
| 撤銷 | `0ops auth tokens revoke <name>` | `DELETE /v1/teams/{slug}/tokens/{name}` | 設 `revoked_at`；後續使用 401 |
| 過期警告 | `0ops auth tokens list` | — | 過期前 14 天於輸出標註；過期後 7 天自動列出供 audit |

- PAT 必綁單一 team（`team_id not null`）；不可跨 team 使用
- 預設 90 天過期（`expires_at = created_at + 90d`）；最長允許 365 天
- argon2id 參數：memory=64 MiB、iterations=3、parallelism=2、salt=16 bytes、key=32 bytes（與 OWASP 建議對齊）；參數寫入 `internal/server/auth/pat.go` 常數，rotation 時須能讀舊參數驗證舊 hash

### 4.5 Token 檔（`~/.config/0ops/auth.json`）

```json
{
  "version": 1,
  "tokens": [
    {
      "host": "https://api.0ops.tw",
      "github_login": "alice",
      "default_team_slug": "personal-alice",
      "bearer_token": "op_dev_xxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxx",
      "issued_at": "2026-05-10T12:34:56Z",
      "expires_at": "2026-06-09T12:34:56Z"
    }
  ]
}
```

- 檔權限 `0600`；目錄 `0700`；CLI 在寫入時主動 chmod
- 多 host 並存（自架 / managed cloud）；CLI 以 `host` 欄位選擇；預設 host 由 `OPS_HOST` env 或 `0ops config set host` 決定
- MCP server 啟動時讀同一份；不寫；無 token 時於 tool 回應 envelope `{code: "unauthorized", message: "請先在 terminal 執行 0ops auth login，並重啟 MCP host", trace_id: ...}`

## 5. Personal team auto-provisioning

### 5.1 觸發條件

- 首次成功完成 device flow（DB 無對應 `user_account` 或對應 user 無任何 `team_membership`）
- Backend 在 `device/poll` 成功處理時於同一 transaction 內：
  1. `INSERT INTO user_account ...`（若不存在）
  2. `INSERT INTO team (slug, name, plan)` — slug = `personal-{github_login}`，name = `{github_login}'s personal team`，plan = `free`
  3. `INSERT INTO team_membership (team_id, user_id, role='owner', joined_at=now())`
- 失敗（例如 slug 已被別人占用）→ retry 加數字後綴 `personal-{github_login}-2`、`-3`...至 `-9`；超過則 500 並請 user 改用 `--team` 自選 team 加入

### 5.2 GitHub login 改名同步

- v1：`personal-*` slug 一旦建立**不**隨 GitHub login 改名而改變；slug 保持穩定（避免破壞既有 URL、PAT 綁定）
- 只更新 `user_account.github_login` 欄位以利後續 `name` 顯示
- v1.1 評估：是否在 user 主動觸發下 rename personal team slug

### 5.3 Personal team 限制

| 限制 | 規則 |
|---|---|
| Rename | v1 不允許（slug 固定為 `personal-{原 github_login}`） |
| Delete | v1 不允許（user 必須留至少一個 team；archive 待 v1.1） |
| Member 邀請 | 允許；邀請後 personal team 即「實質為 team」，但 slug 不變 |
| Plan | 預設 `free`；可升級 |

## 6. Middleware chain

### 6.1 鏈順序

```
RequestID → Logger → Recovery → Tracing(otel)
         → AuthBearer
         → ResolveTeam       (僅 team-scoped 路由)
         → CheckMembership   (僅 team-scoped 路由)
         → CheckTokenScope
         → handler
```

`/v1/me/*` 與 `/v1/auth/device/*` 不經 `ResolveTeam` / `CheckMembership`：

- `/v1/auth/device/{start,poll}`：不經 `AuthBearer`
- `/v1/auth/device/logout`、`/v1/me/teams`、`/v1/me/apps`：經 `AuthBearer` + `CheckTokenScope`，跳過 team 解析
- `/v1/teams/{slug}/...`：全鏈

### 6.2 各段行為

#### AuthBearer
- 解 `Authorization: Bearer <token>`；無 header → `401 unauthorized`
- 計算 `argon2id(token)` 並查 `cli_token`（含 device 與 pat）；查無 → `401 token_invalid`
- 檢查 `revoked_at` → `401 token_revoked`；`expires_at` → `401 token_expired`
- 設 `ctx.actor_user_id` = `cli_token.owner_user_id`、`ctx.token_id` = `cli_token.id`、`ctx.token_kind`、`ctx.token_scopes`
- 更新 `cli_token.last_used_at`（async / batched，避免每 request 寫一次 DB；以 in-memory channel + 背景 flush，30 秒批次）
- device token 觸發 rolling refresh（§ 4.3）→ 設 response header `X-0ops-Token-Refreshed`

#### ResolveTeam
- 取 URL path `team_slug`；查 `team` row；查無 → `404 team_not_found`
- `team.archived_at != null` → 對 write 動作回 `403 team_archived`；對 read 允許繼續
- 設 `ctx.team_id` = `team.id`、`ctx.team_slug`、`ctx.team_plan`

#### CheckMembership
- 查 `team_membership(team_id, user_id)`；無 → **回 `404 team_not_found`**（與 team 不存在同回應，防 enumeration）
- 設 `ctx.actor_role` = `team_membership.role`

#### CheckTokenScope
- 由 router 在註冊時宣告該 endpoint 之 `Action`（`rbac.Action`）；middleware 取 `rbac.RequiredFor(action)` 得 `{MinRole, RequiredScope}`
- `RoleAtLeast(actor_role, required_role) = false` → `403 forbidden_role`，details 含 `{required_role, actor_role}`
- `Scope ∈ token_scopes = false` → `403 forbidden_scope`，details 含 `{required_scope, token_scopes}`
- device token 視為持有所有 scope（`Scope` 檢查直接 pass），但仍受 role 限制

### 6.3 Audit hook 點

每段 middleware 失敗時不直接寫 audit；audit 由 `audit-log` spec 釘 hook 點。本 spec 約束：**任何 4xx**（unauthorized / forbidden / not_found）**之 actor / token / team_slug / route / status / trace_id 必入結構化 log**（非 audit_log；走 slog）。

## 7. Cross-team enumeration 防範

### 7.1 規則

| 情境 | 不採 | 採用 | 理由 |
|---|---|---|---|
| team 不存在 | `404 team_not_found` | `404 team_not_found` | 一致 |
| team 存在但 actor 非 member | `403 not_member` | **`404 team_not_found`** | 防 slug 列舉 |
| 跨 team 查詢資源（如 preview_id 屬於別 team） | `403 forbidden_team` | `404 resource_not_found` | 防 ID 列舉 |
| role 不足（actor 是 member） | `403 forbidden_role` | `403 forbidden_role` | 已是 member，無 enumeration 風險 |
| scope 不足 | `403 forbidden_scope` | `403 forbidden_scope` | 同上 |

### 7.2 例外：preview gate

`error-model` spec § 5.1 的 `forbidden_team` 仍保留為碼，但 backend 對「actor 連 team membership 都沒有」的 preview 跨 team 取用，回 `404 preview_not_found`；`forbidden_team` 留給「actor 確實是 team A 的 member，preview 屬於 team B」這類**內部不一致**情境（理論上不應發生，列為安全網）。

## 8. CLI / MCP 端行為

### 8.1 CLI

| 命令 | 行為 |
|---|---|
| `0ops auth login [--host=<url>]` | 啟 device flow、開瀏覽器（platform-specific）、寫 `auth.json` |
| `0ops auth logout` | 呼 `/v1/auth/logout`、清 `auth.json` 對應 host 的條目 |
| `0ops auth status` | 顯示當前 host、github_login、default_team_slug、token 過期時間；不洩漏 token 明文 |
| `0ops auth tokens create ...` | 呼 `POST /v1/teams/{slug}/tokens`；明文以 stdout 印出且警告「此為唯一一次顯示」 |
| `0ops auth tokens list` | 列出含過期警告 |
| `0ops auth tokens revoke <name>` | 呼 `DELETE`；確認 prompt（除非 `--yes`） |
| `0ops teams use <slug>` | 改本機 `auth.json` 之 `default_team_slug`；**不**呼 backend |

### 8.2 MCP

| 場景 | 行為 |
|---|---|
| MCP server 啟動 | 嘗試讀 `~/.config/0ops/auth.json`；找不到 → 不 fail（仍 register tools，但每次 call 回 unauthorized envelope） |
| Tool call 無 token | 回 `IsError: true` + envelope `{code: "unauthorized", message: "請先在 terminal 執行 0ops auth login 並重啟 MCP host", ...}` |
| Token 過期（401 token_expired） | 同上 |
| Backend 回 `X-0ops-Token-Refreshed` | MCP server **不**寫 `auth.json`（避免與 CLI race）；下次仍用舊 token；rolling refresh 留給 CLI 處理 |

> 設計理由：MCP 為長駐 stdio process，多個 MCP host instance 可能同時讀 auth.json；只允許單寫者（CLI）避免併發寫。權衡：MCP 內 token 不會自動延期，使用者每 30 天最少跑一次 CLI 命令即可。

### 8.3 MCP host 重啟需求

- 「重啟 MCP host」指：在 claude code / codex 端關閉並重啟 MCP 連線（具體做法依 CLI 而異；SKILL.md 須說明）
- 0ops-mcp 自身不提供 reload signal（v1）；v1.1 評估 SIGHUP

## 9. 驗證準則

| 驗證項 | 方式 | 通過條件 |
|---|---|---|
| Device flow 端到端 | `0ops auth login` 用測試 GitHub OAuth app | `auth.json` 寫入、`0ops auth status` 顯示正確 |
| PAT scope 限制 | 用 `apps:read` PAT 呼 `POST .../apps:preview` | `403 forbidden_scope`，details 含 required/actual |
| Cross-team enumeration | team A 的 PAT 呼 `/v1/teams/teamB/apps` | `404 team_not_found`（**非 403**） |
| Membership 失敗 | team 存在但 user 非 member | `404 team_not_found` |
| Role 不足 | viewer 呼 write endpoint | `403 forbidden_role` |
| Token 過期 | 模擬 PAT 過期（DB 改 `expires_at`） | `401 token_expired` |
| Token 撤銷 | revoke 後立即呼 API | `401 token_revoked` |
| Argon2id 比對 | 同明文、不同 salt 應 hash 不同；驗證 `Compare()` 通過 | `Compare(明文, hash) = true` |
| Personal team auto-provision | 新 user 首次 login | DB 內存在 `personal-{login}` team + owner membership |
| Personal team slug 衝突 | mock slug 已存在 | retry 後綴成功，至 `-9` 才失敗 |
| Token 檔權限 | `0ops auth login` 後 | `stat ~/.config/0ops/auth.json` 為 0600 |
| MCP 無 token | 啟 MCP 後 call tool | `IsError: true` + 引導訊息 |
| Rolling refresh | 模擬 device token 距上次更新 > 24h 的請求 | response header 帶 `X-0ops-Token-Refreshed`，CLI 寫入新 token |
| Last-used batched | 同 token 100 req | `cli_token.last_used_at` 至多更新 4 次（30s 批次間隔） |

## 10. SLI/SLO 對應（concurrent with `slo-and-alerting` spec）

| SLI | 目標 | 量測 |
|---|---|---|
| Device flow `start → poll success` p95 | < user 端 30s（含瀏覽器互動） | backend 視角：start 接收到 poll 200 的時間 p95 < 90s（涵蓋大多 user） |
| `AuthBearer` middleware 處理時間 p99 | < 5ms | argon2id 驗證為主成本；用 in-memory cache（5 分鐘）對 `argon2id(token)` 做緩存以降低重算 |
| 401 / 403 比例 | < 1% 全請求 | 高於則 dashboard 標記（可能配置錯誤） |

> argon2id cache：以 `token_hash_prefix(8)` + `argon2id(token)` 結果為 key；大小 1024 entry LRU；revoke 時 invalidate（內部 channel broadcast）；過期 token 自然 evict。

## 11. 對 `docs/0ops-plan.md` 的修改清單

1. **DB schema § `cli_token`**：新增 `kind text not null check(kind in ('device','pat')) default 'pat'` 欄位；plan.md 須補上
2. **Auth & RBAC § Authentication**：補入「device flow token rolling refresh（每 24h 經 backend 重發）」、「response header `X-0ops-Token-Refreshed` 機制」
3. **Auth & RBAC § Middleware chain**：補入「未通過 `CheckMembership` 回 `404 team_not_found`（非 403），enumeration 防範」
4. **Auth & RBAC § Authentication PAT**：明確 argon2id 參數（memory=64MiB, iterations=3, parallelism=2）並交叉引用本 spec § 4.4
5. **「立即下一步」**：在 M0 scaffold 後補 `internal/server/auth/` 之 middleware 五段為 M1 阻擋項

## 12. Open issues

> 來源：ADR-0001 § 9 之 4 條 Open Questions + 本 spec 撰寫期間新發現

- Personal team rename / delete 政策（ADR-0001 OQ#1）：v1 採「不允許」；v1.1 評估
- Team archival 後 PAT 是否立即失效（ADR-0001 OQ#2）：本 spec 採「team archived → write 403、read 允許；PAT 繼續有效但 write 同遭 archived 拒絕」；待 user 拍板
- Pending invite 狀態（ADR-0001 OQ#3）：屬 `members:manage` action；本 spec 不含；歸 v1.1 或併入 invite_member 對應 spec
- Scope 列舉變更通道（ADR-0001 OQ#4）：本 spec 採「新增 scope 須改 ADR-0001 列舉段或新 ADR；常數同步改 `internal/shared/rbac/scope.go`」
- MCP token reload：v1 需重啟 MCP host；v1.1 評估 SIGHUP 或 inotify
- 自架 host 之 device flow GitHub OAuth app：每個 deployment 須自備 OAuth app；onboarding runbook 待補
- argon2id 參數升級路徑：未來提升 memory cost 時需保留舊參數驗證舊 hash 的相容欄位（`cli_token` 加 `hash_params jsonb` 或 hash 字串內含參數，採後者並用 argon2 標準 PHC 字串格式）

## 13. 不可違反的硬性規則

> 違反以下任一項，PR 不可合入。

1. 任何 sqlc query 模板（`internal/server/db/queries.sql`）必含 `WHERE team_id = $1`（或 `user_id` for `/v1/me/*`）；codegen 不得產出無 team_id 條件的 team-scoped query
2. Middleware 鏈順序固定為 `RequestID → Logger → Recovery → Tracing → AuthBearer → ResolveTeam → CheckMembership → CheckTokenScope`；router 不得自行拆解或重排
3. team-scoped endpoint 對「team 不存在」與「user 非 member」必回相同 `404 team_not_found`；不得回 `403`
4. PAT 與 device token 一律以 argon2id 雜湊存 DB；明文不得入 DB、log、metric label、error envelope（包含 `details`）
5. PAT 必綁單一 team；`cli_token.team_id` not null；server 端拒絕對另一 team 之 endpoint 使用該 PAT（`404 team_not_found` 即達成此語意）
6. `~/.config/0ops/auth.json` 寫入時 CLI 必主動 chmod 0600；目錄 0700；non-Unix 平台（Windows）以等價 ACL 處理
7. `0ops auth tokens create` 之明文 token 僅在 stdout 顯示一次；DB / log / metric 不得留明文
8. `Authorization` header、bearer token 明文、PAT 明文不得進結構化 log message、`details`、metric label、audit args/result（與 `error-model` § 9 對齊）
9. Personal team slug 一旦建立不變；GitHub login 改名只更新 `user_account.github_login` 欄位
10. MCP server 不得寫 `auth.json`；rolling refresh 寫入由 CLI 唯一負責
