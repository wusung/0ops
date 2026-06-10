## Auth & RBAC

### Authentication
- **CLI / MCP**：GitHub OAuth **device flow**
  1. `0ops auth login` → CLI 顯示 device code + 驗證 URL
  2. 使用者瀏覽器登入 → backend 拿到 access token
  3. CLI poll backend `/v1/auth/device/poll` → 取得 0ops bearer token
  4. 寫入 `~/.config/0ops/auth.json`（perm 0600）
  5. 首次登入自動建立 `personal-{github_login}` team，user 為 owner
  6. MCP server 啟動時讀同一份檔；無 token 時 tool 回錯誤訊息引導跑 `0ops auth login`
- **CI / 自動化**：`0ops auth tokens create --team=<slug> --name=ci --scopes=apps:read,apps:write` 產生 PAT
  - PAT **必須綁 team**，不能跨 team 使用
  - `cli_token.scopes` 為 string array；`cli_token.token_hash` 用 argon2id
  - PAT 預設 90 天過期，過期前 14 天 `0ops auth tokens list` 顯示警告
- Backend 全部走 `Authorization: Bearer <token>`

### Authorization model
有效權限 = `team_membership.role × cli_token.scopes` 交集（device flow token 視為持有所有 scope）。

#### Role 矩陣

| Role | apps:read | apps:write | apps:delete | domains:write | audit:read | tokens:manage（自己） | members:manage |
|---|---|---|---|---|---|---|---|
| owner | ✓ | ✓ | ✓ | ✓ | ✓ | ✓ | ✓ |
| admin | ✓ | ✓ | ✓ | ✓ | ✓ | ✓ | ✗ |
| member | ✓ | ✓ | ✗ | ✓ | 自己的 | ✓ | ✗ |
| viewer | ✓ | ✗ | ✗ | ✗ | 自己的 | ✓ | ✗ |

#### Scope 列舉（與 role 正交）
- `apps:read`、`apps:write`、`apps:delete`
- `domains:write`
- `repos:read`
- `audit:read`
- `members:manage`
- `tokens:manage`
- `teams:read`

#### Middleware chain
```
RequestID → Logger → Recovery → Tracing(otel)
         → AuthBearer        // 解 token、設 actor_user_id
         → ResolveTeam       // 從 URL path 取 team_slug，載入 team_membership
         → CheckMembership   // role >= required
         → CheckTokenScope   // scopes ⊇ required
         → handler
```
未通過任一檢查回 403，body 帶 `code: forbidden_role | forbidden_scope | not_member`，方便 CLI/MCP 給使用者具體訊息。

### GitHub App 權限 scope（最小化）
- `contents:read` — repo introspect、build 來源
- `metadata:read` — webhook event 來源驗證
- `actions:write` — `workflow_dispatch` 觸發 deploy
- `pull_requests:read`（v1.1 預覽部署用，v1 不勾）

App install 掛 `team.github_install_id`；user 離開 team 不影響授權。

### GitHub App install 綁 team

**前置**：team owner 才能 install / uninstall（`role=owner` + `members:manage` scope）。
**流程**：

1. owner 跑 `0ops teams github install`（CLI）或 LLM 呼 `install_github_app_preview`（MCP）
2. CLI/MCP 拿到 backend 簽發的 `state` token（10 分鐘 TTL，HMAC 綁 team_id + actor_user_id）
3. CLI 開瀏覽器到 `https://github.com/apps/0ops/installations/new?state=<state>`
4. 使用者在 GitHub 選擇 install target（personal account 或 GitHub org）+ 選擇 repo 範圍
5. GitHub 重導 callback `https://0ops.jesontech.com/v1/auth/github/install-callback?installation_id=...&state=...`
6. Backend 驗 `state` HMAC + 未過期 + actor 仍是該 team owner → `UPDATE team SET github_install_id = $1 WHERE id = $2`
7. 已綁 team 又重 install：覆寫 install_id，舊 install 標 deprecated（user 可選 GitHub UI 端 uninstall）
8. install 後 webhook 進來時用 `X-GitHub-Hook-Installation-Target-ID` 反查 `team`，找不到的 install 直接 200 ignore（避免回 4xx 觸發 GitHub 重試）

**Uninstall**：兩條路徑
- `0ops teams github uninstall`（preview/confirm）：backend 呼 `DELETE /app/installations/{id}`，清 `team.github_install_id = NULL`，現有 app 進 `paused` 狀態（不再 redeploy，但保留資料）
- 使用者直接在 GitHub UI uninstall：`installation` webhook event `deleted` → backend 同步清欄位 + paused

### Webhook 安全
- GitHub webhook：HMAC-SHA256（`crypto/hmac` + stdlib），`X-Hub-Signature-256`
- **Replay protection**：`X-GitHub-Delivery` 入 `webhook_dedup` 表；同 (provider, delivery_id) 24h 內重送回 200 不再處理
- 內部 deploy callback（GHA → backend）：自簽 HMAC，header `X-0ops-Signature` + `X-0ops-Timestamp`，timestamp 偏離當下 ±5min 拒收

### Secrets management
- v1：K8s native `Secret` + `External Secrets Operator`（可選），給 managed app 注入 env
- backend 自身敏感設定（Cloudflare API token、GitHub App private key）走 `koanf` + 檔案 mount，不放 env var
- v2：規劃 Vault / SOPS

