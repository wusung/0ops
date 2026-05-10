# Feature Spec：github-app-install-flow

> **狀態**：draft
> **來源**：`docs/0ops-plan.md`「Auth & RBAC / GitHub App install 綁 team」「GitHub App 權限 scope」「Webhook 安全」段；ADR-0001（team 一階）；本 spec 依賴 `auth-and-rbac`、`preview-confirm-gate`、`secrets-management`、`error-model`、`shared-dto-and-contract`
> **適用範圍**：GitHub App install / uninstall 之 preview/confirm 流程、state HMAC、callback 處理、installation token 生命週期
> **對應 Milestone**：M3（custom domain 與 webhook auto redeploy 共同 milestone；`create_app` 仰賴本 spec 之 install 完成）

## 1. 結論（先讀本段）

- GitHub App「0ops」為 0ops 自有 App；單一 GitHub App，多 team 共用（每 team 一個 installation）
- Install 流程：CLI/MCP 觸發 preview → backend 簽 state HMAC → user 瀏覽器至 GitHub install → callback → backend 綁 `team.github_install_id`
- Uninstall 兩條路徑：CLI/MCP 主動（preview/confirm + DELETE）/ user 直接於 GitHub UI；後者透過 `installation` webhook 同步 backend
- `state` token：HMAC 簽，10 分鐘 TTL；綁 `team_id + actor_user_id`
- App scope（最小化）：`contents:read` + `metadata:read` + `actions:write`；`pull_requests:read` 為 v1.1
- Installation token（短期）：backend 用 App private key + installation_id 換；1h TTL；3 個用途：（1）`inspect_repo` 之 GitHub API 呼叫、（2）GHA workflow target repo checkout、（3）`ghcr-pull` Secret 之 GHCR token
- Token cache：per `installation_id` 快取於 backend；TTL = 50 min（GitHub 端 1h - 10 min buffer）；過期前 backend 自動 refresh
- 重 install（同 team 又 install）：覆寫 `installation_id`；舊 installation 標 deprecated；webhook 之 `installation_repositories` 事件透過 dedup 避免雙重處理
- 已 install 之 team 跑 uninstall 時，現有 app 進 `paused` 狀態（不再 redeploy，但保留資料）；未來重 install 後可手動 redeploy

## 2. 範圍

### 2.1 包含
- `install_github_app` / `uninstall_github_app` 兩 action 之 preview/confirm
- `state` token 簽 / 驗 / 過期
- `/v1/auth/github/install-callback` callback handler
- `installation` / `installation_repositories` webhook event 處理
- backend `internal/server/services/githubapp/`：JWT 簽、installation token 換取與快取
- App private key 之讀取與輪替（與 `secrets-management` 對齊）
- App scope 列表與用途對應
- Reinstall / 重複 callback 之 idempotency

### 2.2 不包含
- `inspect_repo` 之 buildpack 偵測邏輯（屬 `read-api-vertical-slice` § 4.3）
- GHCR token 之 ImagePullSecret refresh 排程（屬 `k3s-namespace-isolation` § 8.2，使用本 spec 之 token 取得函式）
- GitHub push webhook 處理（屬 `webhook-and-redeploy` spec；本 spec 只規範 `installation*` 兩種 webhook）
- App 自身的 GitHub Marketplace listing（屬 ops 範圍）
- App private key 的 backup（屬 `secrets-management` spec）

## 3. 檔案結構

```
0ops/
├── internal/
│   └── server/
│       ├── services/
│       │   └── githubapp/
│       │       ├── jwt.go               # App JWT 簽（RS256；private key 從 secrets 讀）
│       │       ├── installation.go      # 換 installation token（POST /app/installations/{id}/access_tokens）
│       │       ├── token_cache.go       # per-install token cache + auto refresh
│       │       ├── ghcr_token.go        # 將 installation token 包裝為 GHCR docker config
│       │       ├── state.go             # state HMAC 簽 / 驗
│       │       ├── webhook_install.go   # installation* webhook handler
│       │       └── doc.go
│       └── routers/
│           └── auth_github.go           # /v1/auth/github/install-callback
└── migrations/
    └── 000X_team_install.sql            # team.github_install_id 已於 plan.md 定義；本 spec 補 (github_install_id) 索引
```

## 4. Install flow

### 4.1 序列圖

```
CLI/MCP                     Backend                          GitHub
  | (1) install_github_app_preview
  |--------------------------->|
  |        preview side_effects:
  |          - 簽 state token (10 min TTL)
  |          - 開啟 GitHub install URL
  |        計算 PlanPreview
  | <-- preview_id, side_effects, expires_at
  |
  | (2) install_github_app { preview_id }
  |--------------------------->|
  |        confirm:
  |          - state := HMAC(team_id + actor_user_id + preview_id + ts)
  |          - 寫 deploy_run-style audit
  | <-- last_result { install_url: "https://github.com/apps/0ops/installations/new?state=<state>" }
  |
  | (3) CLI 開瀏覽器至 install_url；MCP 印 URL 給 user
  |
  |                                                          (4) user 在 GitHub
  |                                                              選 install target
  |                                                              選 repo 範圍
  |                                                              按 Install
  |
  |                            <----------------------------- (5) GitHub redirect to
  |                                                              callback_url with
  |                                                              installation_id + state
  |                            (6) callback handler:
  |                                  - 驗 state HMAC
  |                                  - 驗 actor + team_id 仍可信
  |                                  - 驗 preview 已 consumed（避免被偽造 state 覆寫）
  |                                  - UPDATE team SET github_install_id = ?
  |                                  - 寫 audit_log
  |                            <-- HTTP 302 至 confirmation page
  |
  | (7) CLI/MCP polling backend "is install complete?"
  |     或 user 重跑 0ops teams github install --status
```

### 4.2 Preview 階段

- args 為空（`{}`）；preview 純粹簽 state + 顯示 install URL
- side_effects（1 項）：
  - `Generate install URL and redirect to GitHub for approval`（reversible：state 過期即失效）
- action_summary：`為 team \`<team_slug>\` 安裝 GitHub App「0ops」`

### 4.3 Confirm 階段

- 簽 state HMAC：`HMAC-SHA256(installation_state_secret, team_id + ":" + actor_user_id + ":" + preview_id + ":" + timestamp)`，10 分鐘 TTL
- last_result：
  ```json
  {
    "install_url": "https://github.com/apps/0ops/installations/new?state=<state>",
    "expires_at": "..."
  }
  ```
- 將 state 簽發事件寫 audit_log（actor / preview_id / state hash 8 字摘要）

### 4.4 Callback handler

```
GET /v1/auth/github/install-callback?installation_id=12345&state=abcdef...

1. 解 state；驗 HMAC + 未過期 + actor / team / preview 對應仍有效
2. 驗 actor 仍是該 team 之 owner（role 不可降級在這段期間）
3. 取出 installation_id (uint64)
4. SELECT team WHERE id = ?
   - team 已有 github_install_id 且非當前 → 視為 reinstall（log warn + 標記舊 install deprecated）
5. UPDATE team SET github_install_id = $installation_id
6. INSERT audit_log
7. 302 redirect to https://app.0ops.tw/teams/<slug>/integrations/github?status=success
   （v1 因無 web UI，redirect 到 docs page；CLI/MCP 端透過 polling 偵測）
```

State 驗失敗 → 400；不 redirect 至成功頁；audit_log 標 `state_invalid`。

### 4.5 CLI / MCP 端

#### CLI
```
$ 0ops teams github install

即將執行：為 team `acme-prod` 安裝 GitHub App「0ops」
副作用：
  1. Generate install URL and redirect to GitHub for approval

確認執行? [y/N] y

請開啟瀏覽器：
  https://github.com/apps/0ops/installations/new?state=abcdef...
（已自動嘗試開啟）

等待安裝完成... ⠋
✓ 安裝成功（installation_id=12345）；team `acme-prod` 已綁定。
```

CLI 端在 confirm 後 polling `/v1/teams/<slug>/github/install-status`；偵測 `installed_at != null` 即視為完成；timeout 10 分鐘（與 state TTL 對齊）。

#### MCP
```
LLM call: install_github_app_preview(team_slug=...)
LLM 顯示 PlanPreview 給 user
LLM 取得同意，call install_github_app(team_slug=..., preview_id=...)
LLM 收到 last_result.install_url
LLM 顯示給 user：「請開啟以下 URL 完成安裝：<url>。完成後跑 `0ops teams github install --status` 確認。」
```

> MCP 不主動 polling；理由：MCP tool 應為快速 return，避免長等；user 在 host shell 執行 CLI status 命令確認。

## 5. Uninstall flow

### 5.1 主動 uninstall（CLI/MCP）

```
1. uninstall_github_app_preview { team_slug }
   side_effects:
     - DELETE GitHub App installation (irreversible)
     - 將 team 內所有 app 標記為 paused
2. uninstall_github_app { preview_id }
   - 呼 DELETE /app/installations/{installation_id}
   - UPDATE team SET github_install_id = NULL
   - UPDATE app SET status = 'paused' WHERE team_id = ?
   - 清除 token cache
   - 清除 team-<slug> namespace 之 ghcr-pull Secret（K8s API delete）
```

### 5.2 user 從 GitHub UI uninstall

- GitHub 發 webhook `installation` event 之 `action: deleted`
- backend 端 webhook handler：
  ```
  1. 驗 X-Hub-Signature-256（與 webhook-and-redeploy spec 共用驗章）
  2. 解 payload；取 installation.id
  3. SELECT team WHERE github_install_id = ?
     - 0 row → 200 ignore（避免 GitHub retry）
  4. UPDATE team SET github_install_id = NULL
  5. UPDATE app SET status = 'paused' WHERE team_id = ?
  6. 清 token cache + ghcr-pull Secret
  7. 寫 audit_log（actor = `system:github_webhook`）
  8. 200 OK
  ```

### 5.3 paused app 行為

- `paused` app 之 `redeploy` 與 `webhook-and-redeploy` push event 觸發即拒：CLI / MCP / webhook handler 端檢查 `app.status = paused` → no-op 並 log
- 既有 K3s pod 不刪除（保留資料完整性）；user 於 v1.1 後可選擇 manual delete
- 重 install 同一 team → app 仍為 paused；user 須手動 `0ops apps update <slug> --resume`（v1.1）；v1 採重 install 後仍 paused；user 跑 `0ops deploys redeploy <slug>` 即視為 resume

## 6. Installation token 取得與 cache

### 6.1 取得流程

```go
// internal/server/services/githubapp/installation.go
func GetInstallationToken(ctx context.Context, installID int64) (string, error) {
    // 1. cache 命中 → return
    if t := cache.Get(installID); t != nil && t.expiresIn() > 5*time.Minute {
        return t.Value, nil
    }

    // 2. 簽 App JWT（RS256；private key + iss=app_id；exp 10 min）
    jwt := signAppJWT(privateKey, appID, /*ttl*/ 10*time.Minute)

    // 3. POST /app/installations/{id}/access_tokens
    resp := github.PostInstallationAccessToken(jwt, installID)
    // resp.token, resp.expires_at（GitHub 預設 1h）

    // 4. cache
    cache.Set(installID, resp.token, resp.expires_at)
    return resp.token, nil
}
```

### 6.2 Cache 結構

- 記憶體 cache（per backend pod；不持久化）
- Key：`installation_id`
- Value：`{ token, expires_at }`
- TTL 計算：`expires_at - 10 min buffer`（自動 refresh window）
- 背景 goroutine `installation_token_refresher`：每 1 min 掃 cache，將 `expires_at - now() < 10 min` 的條目主動 refresh
- M5 多 replica：每 pod 各自 cache；不共享（簡化）；多 refresh 屬可接受成本

### 6.3 Token 用途分流

| 用途 | 取 token 路徑 |
|---|---|
| `inspect_repo` GitHub API（拉 repo metadata） | `GetInstallationToken(team.github_install_id)` |
| GHA workflow checkout target repo | dispatch payload 帶 `secrets.GITHUB_APP_TOKEN`（GHA secret） vs short-lived token via `client_payload`：本 spec 採前者（GHA 端 secret 由 ops 預先設定 App private key + workflow 內簽 JWT 換 token）|
| `ghcr-pull` Secret 之 docker config | `GetInstallationToken` 後包裝為 dockerconfigjson |

> GHA workflow checkout 之 token：GHA secret 持有 App private key（較大 blast radius）vs ops 簽短期 token via client_payload（較安全但複雜）。本 spec 採後者：backend 在 dispatch 時順便簽 1h installation token 帶入 `client_payload.installation_token`，workflow 內 checkout 用此 token；token 過期即 build fail（4-6 min build < 1h，安全）。

修正本 spec § 6.3：
- GHA checkout token：由 backend dispatch 時簽 1h installation token，透過 `client_payload.installation_token` 帶入（屬 `build-pipeline-and-callback` spec 之 ClientPayload 補一欄）

## 7. Webhook events 處理

### 7.1 `installation` event

`action` 列舉：`created` / `deleted` / `suspend` / `unsuspend` / `new_permissions_accepted`

| action | 處置 |
|---|---|
| `created` | 通常我們已透過 callback 處理；此 event 為冗餘確認；webhook_dedup 之 (provider='github', delivery_id) 攔重複 |
| `deleted` | 同 § 5.2 |
| `suspend` | UPDATE team.github_install_id = NULL；app 進 paused；audit `installation_suspended` |
| `unsuspend` | UPDATE team.github_install_id = ?（從 payload 取）；app **仍** paused；user 須手動 redeploy |
| `new_permissions_accepted` | scope 變動；audit log；不改 install_id |

### 7.2 `installation_repositories` event

- user 在 GitHub UI 改 repo 範圍
- `action`: `added` / `removed`
- 處置：v1 不主動處理；下次 `inspect_repo` 偵測 `installed_no_access` 自然 surface
- v1.1 評估：偵測到 `removed` 含 0ops 已綁定的 repo 時，發送 owner 通知

## 8. App private key 與 secret

### 8.1 Private key 儲存

- K8s Secret `github-app-private-key`（於 `system-0ops` namespace）
- key 格式：PEM PKCS#1 RSA private key（GitHub 下載格式）
- 由 `secrets-management` § 4 之 B 類管理；rotation 走 GitHub UI 換新 key（雙 key 並存 7 天，與 D 類同邏輯）

### 8.2 簽 App JWT

```go
func signAppJWT(privateKey *rsa.PrivateKey, appID int64, ttl time.Duration) string {
    claims := jwt.MapClaims{
        "iat": time.Now().Add(-30*time.Second).Unix(),  // 防 clock drift
        "exp": time.Now().Add(ttl).Unix(),
        "iss": appID,                                     // App ID（非 installation ID）
    }
    return jwt.NewWithClaims(jwt.SigningMethodRS256, claims).SignedString(privateKey)
}
```

### 8.3 State HMAC secret

- K8s Secret 內額外欄位 `state_hmac_secret`（屬 `secrets-management` A 類；90d rotation；雙 secret 30 min window）
- 與 `OPS_TOKEN_SIGNING_SECRET` 為**獨立** secret（不同用途、不同 blast radius）

## 9. 與其他 spec 接合點

| 接合 | spec |
|---|---|
| `install_github_app` / `uninstall_github_app` 走 preview-confirm 通用框架 | `preview-confirm-gate` |
| App private key + state HMAC secret 之儲存 / rotation | `secrets-management` § 4 |
| Installation token 用於 `inspect_repo` | `read-api-vertical-slice` § 4.3 |
| Installation token 用於 GHA dispatch（`client_payload.installation_token`）| `build-pipeline-and-callback` spec |
| Installation token 包裝為 ghcr-pull Secret | `k3s-namespace-isolation` § 8.2 |
| `installation` webhook 之 HMAC 驗章 | `webhook-and-redeploy` spec § Webhook 安全 |
| `team.github_install_id` 欄位 | `docs/0ops-plan.md` DB schema |
| State callback 之 audit log | `audit-log` spec |
| `github_app_not_installed` / `github_repo_not_accessible` 失敗碼 | `error-model` § 5.5 |
| App scope 最小化 | `docs/0ops-plan.md` Auth & RBAC § GitHub App 權限 scope |
| Owner-only install/uninstall | `auth-and-rbac` spec（role=owner + members:manage scope）|

## 10. 驗證準則

| 驗證項 | 方式 | 通過條件 |
|---|---|---|
| Owner 可 install | role=owner + scope members:manage | preview / confirm 通過 |
| 非 owner 拒 | role=admin | preview 即 403 forbidden_role |
| State 簽 / 驗 round-trip | unit test | sign 後 verify 通過；改 1 byte 即拒 |
| State 過期 | 11 分鐘後 callback | 400 state_invalid |
| State 跨 team 偽造 | 改 team_id 重簽 | HMAC 不符；400 |
| Callback 成功更新 | mock GitHub redirect with valid state | UPDATE team.github_install_id 成功；audit_log 有 record |
| Reinstall 同 team | 已有 install 又 install | 覆寫；舊 installation 標 deprecated；audit 雙 record |
| Uninstall（CLI 主動）| preview / confirm | 呼 GitHub DELETE；team.install=NULL；app=paused |
| Uninstall（GitHub UI）| mock `installation.deleted` webhook | team.install=NULL；app=paused |
| Webhook signature 驗失敗 | 改 sig 1 byte | 401 webhook_signature_invalid |
| Webhook replay | 同 delivery_id 重送 | 200 ok（webhook_dedup） |
| Installation token 取得 + cache | 連兩次呼 `GetInstallationToken(123)` | 第二次 cache 命中（< 10ms） |
| Installation token refresh | 等 51 分鐘（mock）| cache 自動 refresh；GitHub API 被呼 |
| Suspend / unsuspend | mock 兩 webhook | team.install 隨之變動 |
| App scope 最小化 | 對 GitHub App 設定檢查 | scope 列表 = `contents:read` + `metadata:read` + `actions:write` |
| state_hmac_secret rotation 雙 window | rotation 中簽 / 驗 | new + old 都通 |

## 11. SLI 對應

| SLI | 目標 | 量測 |
|---|---|---|
| Install callback 處理 latency | p95 < 500ms | callback handler 端到端 |
| Installation token cache 命中率 | > 95% | hits / (hits + misses) |
| Token refresh 成功率 | > 99% | `0ops_github_installation_token_refresh_total{outcome=success}` |
| Webhook signature reject rate | < 0.1%（多為攻擊嘗試）| `webhook_signature_invalid` 比例 |
| Webhook replay rate | < 0.5% | `webhook_dedup` hit / total |

## 12. 對 `docs/0ops-plan.md` 的修改清單

1. 「Auth & RBAC / GitHub App install 綁 team」段：交叉引用本 spec § 4 為流程 source；plan 內步驟保留為摘要
2. 「Webhook 安全」段：交叉引用本 spec § 7 為 `installation*` event 處理 source
3. 「Auth & RBAC / GitHub App 權限 scope」段：補入「`pull_requests:read` 為 v1.1，v1 不勾」（plan.md line 664 已有）
4. ADR-0005 之 client_payload 補一欄 `installation_token`：屬 `build-pipeline-and-callback` spec § 4.1 新增

## 13. Open issues

- App 之 `webhook` 設定（events list）：本 spec 涵蓋 `installation` / `installation_repositories`；其他（push / pull_request）屬 `webhook-and-redeploy` spec
- Reinstall 後 paused app 是否自動 resume：v1 不自動；user 主動 `redeploy`；v1.1 評估配合 plan tier 自動 resume
- Suspend 期間 app 進 paused：與 uninstall 行為一致；unsuspend 後仍 paused，須手動 redeploy
- App 升級新權限（new_permissions_accepted）：v1 不主動處理；audit log 記錄即可；v1.1 評估通知 owner
- 多 GitHub Enterprise 帳號：v1 假設只 github.com；GHE 自架 endpoint 需另一份 App + endpoint base URL；v2
- App private key rotation 中斷：rotation 期間舊 JWT 已簽出（10 min TTL）仍可用；新 key 簽完即可；雙 key 並存涵蓋此 window
- 「team owner 在 install 中途離職」：state callback 階段檢查 actor 仍為 owner；非 → 拒
- Personal team 之 install URL 預設選擇：v1 不限定；user 自選（personal account 或 GitHub org）

## 14. 不可違反的硬性規則

> 違反以下任一項，PR 不可合入。

1. install / uninstall 兩 action 必經 preview-confirm-gate 通用框架
2. install 操作必驗 actor role = `owner` + scope `members:manage`；admin 級不可 install
3. State token 必為 HMAC 簽 + 10 分鐘 TTL；不得改長
4. Callback handler 必驗 state HMAC + actor 仍可信；任一失敗 400 + 不 update DB
5. Installation token 必快取（per backend pod）；不得每次呼叫都打 GitHub
6. Installation token 不得進 log / metric / audit_log args；只可放於 DB（短期 cli_token row）或 K8s Secret（dockerconfigjson 內）
7. App private key 不得進 git；必經 K8s Secret；rotation 走雙 key window
8. uninstall 後 app 必進 `paused`；不可立即刪 K3s 資源（保留 user 重 install 後資料）
9. `installation_repositories` event v1 不主動處理；不得在 inspect_repo 之外的端點觸發 GitHub API
10. 任何 GitHub API 呼叫必走 `internal/server/services/githubapp/` 之共用 client；不得在其他 package 直接 instantiate `go-github` client
