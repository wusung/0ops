# Feature Spec：mcp-tool-permissions

> **狀態**：draft
> **對應需求**：CLI / MCP 登入後授權 tools；GitHub OAuth2 Device Flow + MCP tool permissions selection
> **對應 Milestone**：M1（與 auth-and-rbac 同步）

## 1. 結論（先讀本段）

- OAuth2 登入成功後自動進行 **MCP tool permissions 選擇畫面**
- 兩層授權：**GitHub OAuth scopes**（系統級）→ **MCP tool grants**（使用者選擇級）
- Deny by default：未明確授權的 tool 直接拒絕調用，回 4xx + 引導訊息
- 高風險工具（deploy/delete/secrets）獨立開關，預設關閉
- 授權結果存 `tool_grants` 表（user/team/tool_id/allowed），與 `auth_status` 同步寫入
- CLI 支援互動式選單 + 非互動式（`--grants=...`）
- MCP server 每次 tool call 檢查 `tool_grants`，無授權回 `tool_not_permitted` error

## 2. 範圍

### 2.1 包含
- OAuth2 Device Flow 成功後的 tool permissions 選擇 UI（CLI / MCP）
- `tool_grants` 表之結構與 CRUD 操作
- Tool classification（read / write / delete / sensitive）與預設權限規則
- MCP server 端 tool availability filter（基於 grants）
- CLI `auth grant {tool_name}` / `auth revoke {tool_name}` 命令
- Backend `GET /v1/auth/token-info` 回傳 user 可用 tools 列表
- Deny-by-default 強制（無 grant → tool invocation 4xx）
- 高風險 tool（deploy/delete）的顯式 opt-in UI

### 2.2 不包含
- GitHub App permission（見 `github-app-install-flow`）
- Web UI 授權流程（v2）
- Tool 定義與 parameter schema（見 `shared-dto-and-contract`）
- Audit log 寫入（見 `audit-log`；本 spec 只標 hook 點）
- Token scope 與 tool grants 細粒度交叉（tool grant 是獨立決策，不由 token scope 推導）

## 3. 架構

### 3.1 OAuth2 Device Flow 改進流程

```
┌─ CLI / MCP ──────────────────────────────────────────────────────────┐
│                                                                        │
│  1. POST /v1/auth/device/start                                        │
│     ↓                                                                  │
│  2. [後端回 user_code, verification_uri, ...]                        │
│     ↓                                                                  │
│  3. 使用者在瀏覽器確認授權                                             │
│     ↓                                                                  │
│  4. POST /v1/auth/device/poll { poll_token }                         │
│     ├─ 回 access_token（GitHub）                                     │
│     ├─ 建立 personal-{login} team（首次）                            │
│     ├─ 建立/更新 user 與 team_membership                            │
│     └─ ⭐ 觸發 MCP tool permissions 選擇                             │
│        ↓                                                              │
│  5. [CLI 顯示互動式選單 / MCP tool 列表]                              │
│     ├─ Read tools：自動全選（可選取消）                               │
│     ├─ Write tools：預設全關（逐一勾選）                             │
│     └─ Delete/Sensitive tools：預設關（逐一勾選）                    │
│        ↓                                                              │
│  6. POST /v1/teams/{team}/auth:grant-tools                            │
│     { tools: ["list_apps", "create_app", "redeploy"] }              │
│     ↓                                                                  │
│  7. 後端寫入 `tool_grants` → 簽發 0ops bearer token                   │
│     ↓                                                                  │
│  8. 成功：存 ~/.config/0ops/auth.json，MCP 可用                      │
│                                                                        │
└────────────────────────────────────────────────────────────────────────┘
```

### 3.2 Tool Classification

| 分類 | 包含 Tools | 預設授權 | 使用者選擇 | 風險等級 |
|------|-----------|---------|----------|--------|
| **Read-only** | list_apps, get_app, list_domains, get_deploy_status, tail_logs, inspect_repo, list_teams, query_audit_log | ✅ 全選 | 可取消 | 低 |
| **Write (non-destructive)** | create_app, add_domain, redeploy, update_app, invite_member, update_app_settings | ❌ 全關 | 逐一勾 | 中 |
| **Delete / Destructive** | delete_app, remove_domain, uninstall_github_app | ❌ 全關 | 逐一勾 | 高 |
| **Sensitive** | rotate_secrets, view_secrets, github_install | ❌ 全關 | 逐一勾 | 高 |

高風險 tool（Delete + Sensitive）UI 上獨立「⚠️ 危險操作」分組，視覺上強調。

### 3.3 DB Schema 擴展

```sql
-- 新表：MCP tool grants
CREATE TABLE tool_grants (
  id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
  team_id UUID NOT NULL REFERENCES team(id) ON DELETE CASCADE,
  user_id UUID NOT NULL REFERENCES user_account(id) ON DELETE CASCADE,
  tool_id TEXT NOT NULL,  -- e.g., "create_app", "delete_app"
  allowed BOOLEAN NOT NULL DEFAULT false,
  granted_at TIMESTAMP NOT NULL DEFAULT now(),
  granted_by_actor_id UUID,  -- user who set this grant (NULL if auto-provisioned)
  UNIQUE(team_id, user_id, tool_id),
  FOREIGN KEY (team_id, granted_by_actor_id) REFERENCES team_membership(team_id, user_id)
);

-- 新欄：user_account
ALTER TABLE user_account
  ADD COLUMN auth_status TEXT CHECK (auth_status IN ('authorized', 'expired', 'revoked'))
    DEFAULT 'authorized',
  ADD COLUMN auth_scopes JSONB DEFAULT '{}',  -- e.g., {"oauth": ["user", "repo"], "version": "v1"}
  ADD COLUMN auth_expires_at TIMESTAMP;

-- Audit hook：記錄誰什麼時候改了誰的 tool grants
-- （寫入由 audit-log spec 定義；本 spec 只說有 audit_log entry 點）
-- INSERT INTO audit_log(...action='tool_grant'...) on every tool_grants change
```

## 4. API 設計

### 4.1 Device Flow Poll 與 Tool Selection

**Request**（無變化，但後端邏輯改進）：
```http
POST /v1/auth/device/poll
Content-Type: application/json

{ "poll_token": "..." }
```

**Response（M1 新增欄位）**：
```json
{
  "access_token": "...",
  "token_type": "Bearer",
  "expires_in": 86400,
  "team": {
    "id": "...",
    "slug": "personal-alice",
    "name": "Alice's Team"
  },
  "available_tools": [
    {
      "id": "list_apps",
      "name": "List Apps",
      "category": "read",
      "description": "列出該 team 的所有 app",
      "default_allowed": true
    },
    {
      "id": "create_app",
      "name": "Create App",
      "category": "write",
      "description": "建立新 app",
      "default_allowed": false,
      "risk_level": "medium"
    },
    {
      "id": "delete_app",
      "name": "Delete App",
      "category": "delete",
      "description": "刪除 app 與相關資源",
      "default_allowed": false,
      "risk_level": "high",
      "warning": "⚠️ 無法復原"
    }
  ],
  "next_step": "grant_tools"
}
```

### 4.2 提交 Tool Grants

**Request**：
```http
POST /v1/teams/{team_slug}/auth:grant-tools
Authorization: Bearer {temp_token}
Content-Type: application/json

{
  "tools": ["list_apps", "get_app", "inspect_repo", "create_app", "redeploy"]
}
```

**Response**：
```json
{
  "access_token": "...",
  "token_type": "Bearer",
  "expires_in": 86400,
  "granted_tools": ["list_apps", "get_app", "inspect_repo", "create_app", "redeploy"],
  "auth_status": "authorized"
}
```

### 4.3 查詢目前授權

**Request**：
```http
GET /v1/me/auth/token-info
Authorization: Bearer {token}
```

**Response**：
```json
{
  "user_id": "...",
  "username": "alice",
  "team_id": "...",
  "team_slug": "personal-alice",
  "granted_tools": ["list_apps", "get_app", "inspect_repo", "create_app", "redeploy"],
  "auth_status": "authorized",
  "expires_at": "2026-05-12T12:34:56Z"
}
```

### 4.4 更新 Tool Grants（後續）

允許使用者事後修改授權（CLI 命令或未來 Web UI）。

**Request**：
```http
PATCH /v1/me/auth/tool-grants
Authorization: Bearer {token}
Content-Type: application/json

{
  "grant": ["list_apps", "get_app", "inspect_repo"],
  "revoke": ["create_app"]
}
```

**Response**：
```json
{
  "granted_tools": ["list_apps", "get_app", "inspect_repo"],
  "revoked_tools": ["create_app"]
}
```

## 5. CLI 流程

### 5.1 `0ops auth login`（改進）

```bash
$ 0ops auth login

打開瀏覽器在 https://github.com/login/device
輸入代碼: ABC-1234

✓ GitHub 授權成功
✓ 創建 team: personal-alice

─────────────────────────────────
  選擇允許的 MCP Tools
─────────────────────────────────

✓ [自動全選] 讀取類 (3)
  ✓ list_apps
  ✓ get_app
  ✓ tail_logs

[ ] 編輯類 (5)
  [ ] create_app
  [ ] add_domain
  [ ] update_app
  [ ] redeploy
  [ ] invite_member

[ ] ⚠️ 危險操作 (3)
  [ ] delete_app
  [ ] remove_domain
  [ ] uninstall_github_app

選項：
  (y) 確定並保存
  (e) 編輯選擇
  (a) 全選 (高危)
  (c) 取消

輸入 [y]: y

✓ 授權已保存至 ~/.config/0ops/auth.json
✓ 可用 tools: 8 個
```

### 5.2 `0ops auth grant {tool_name}` / `revoke`

```bash
$ 0ops auth grant create_app
✓ 已授予 create_app

$ 0ops auth revoke create_app
✓ 已撤銷 create_app

$ 0ops auth status
認證狀態: authorized
  - Team: personal-alice
  - 授予 tools: 11 個
  - 過期時間: 2026-05-12
```

## 6. MCP Server 實作

### 6.1 Tool Registry 過濾

```go
// internal/mcp/server/registry.go
type ToolRegistry struct {
  tools map[string]*Tool
  grants *GrantChecker  // 檢查使用者授權
}

func (r *ToolRegistry) AvailableTools(ctx context.Context) []Tool {
  var available []Tool
  for _, t := range r.tools {
    if r.grants.IsGranted(ctx, t.ID) {
      available = append(available, t)
    }
  }
  return available
}

func (r *ToolRegistry) CallTool(ctx context.Context, name string, args json.RawMessage) (result interface{}, err error) {
  if !r.grants.IsGranted(ctx, name) {
    return nil, &MCPError{
      Code: "tool_not_permitted",
      Message: fmt.Sprintf("tool %q not granted. run 'oops auth grant %s' to enable", name, name),
    }
  }
  // ... 真正執行 tool
}
```

### 6.2 MCP Tool 描述動態更新

MCP 的 `initialize` 回應與 `list_tools` 只列出已授權的 tools，未授權的不出現在列表（避免 LLM 嘗試呼叫無權限的 tool）。

```go
func (s *Server) ListTools(ctx context.Context) []mcp.Tool {
  user := ctx.Value("user_id").(string)
  var results []mcp.Tool
  
  for _, t := range allTools {
    if s.grants.IsGranted(ctx, user, t.ID) {
      results = append(results, t.MCPTool())
    }
  }
  return results
}
```

## 7. 安全性規則

1. **Deny by default**：無明確 grant → tool call 直接 4xx，**不走 fallback 或 silent ignore**
2. **Token scope 與 tool grant 正交**：GitHub OAuth scope 決定 API endpoint 可見性；tool grant 決定特定 tool availability。兩者都滿足才能調用
3. **Audit trail**：每次 tool grant/revoke 寫 `audit_log(action='tool_grant', ...)`，包含 granted_by
4. **過期或撤銷 token**：auth_status 一旦 `expired` 或 `revoked`，所有 MCP tool call 直接 4xx，不論 grant 狀態
5. **隔離**：team A user 無法看到 team B 的 tool 列表或修改 team B 的 grant（`ResolveTeam` middleware 強制）

## 8. 測試策略

### 8.1 單元測試
- Grant/revoke 邏輯（正常、邊界、異常）
- Tool classification 與預設值
- Deny-by-default 驗證

### 8.2 集成測試
- OAuth2 device flow 完整流程 → tool permissions selection
- Grant submission → access_token 簽發
- MCP tool availability filter（基於 grants）
- Tool call with/without grant

### 8.3 Contract test
- CLI ↔ Backend：grant request/response schema
- MCP ↔ Backend：tool availability API

## 9. 里程碑

- **M1**：Device flow + tool permissions selection（核心流程）
- **M1.1**：`auth grant/revoke` CLI 命令
- **M2**：Web UI 授權頁面（v2）
- **M2.1**：細粒度 OAuth scope 與 tool grant 交叉（未定）

## 10. 常見問題

**Q：為什麼不用 GitHub OAuth scope 直接控制 MCP tools？**  
A：GitHub scope 粒度太粗（e.g., `repo` 表示全部 repo 操作）；我們需要更細粒度的控制（e.g., 允許讀 app 但禁止刪）。而且 GitHub scope 是「GitHub 能做什麼」，tool grant 是「用戶選擇在 0ops 做什麼」，兩者維度不同。

**Q：高風險 tool 為什麼不用額外密碼確認？**  
A：V1 先用 deny-by-default + 互動式授權。V2 可加上 confirm + OTP 等機制。

**Q：MCP server 離線時如何檢查 grant？**  
A：MCP server 會快取 token 與 grants（`~/.config/0ops/auth.json` 內），無法 online verify 時按本地快取決策（信任 token 簽名）。若 token 被撤銷，下次 online 時自動更新。

**Q：能否允許使用者共享 token 給別人使用？**  
A：不建議。Token 是個人憑證，若分享會失去 audit trail。建議使用者為隊友建立單獨帳戶或使用 PAT（帶更粗的 scope）。
