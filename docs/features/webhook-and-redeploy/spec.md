# Feature Spec：webhook-and-redeploy

> **狀態**：draft
> **來源**：`docs/0ops-plan.md`「Webhook 安全」「Build & deploy」段；ADR-0005（HMAC + replay protection）；ADR-0002（preview-confirm 對寫入動作）；本 spec 依賴 `auth-and-rbac`、`preview-confirm-gate`、`build-pipeline-and-callback`、`github-app-install-flow`、`error-model`
> **適用範圍**：GitHub `push` webhook 自動 redeploy、CLI/MCP 主動 redeploy、replay protection；不含 install/uninstall webhook（屬 `github-app-install-flow` § 7）、不含 GHA callback（屬 `build-pipeline-and-callback` § 6）
> **對應 Milestone**：M4

## 1. 結論（先讀本段）

- Endpoint：`POST /webhooks/github`（單一 endpoint，不分 team）；GitHub App webhook 設定指向此
- 驗章：HMAC-SHA256 over raw body，header `X-Hub-Signature-256: sha256=<hex>`；secret 為 `webhook-signing-secret`（與 `secrets-management` § 4 對齊）
- Replay protection：`webhook_dedup(provider='github', delivery_id=X-GitHub-Delivery)` 24h 唯一
- 處理 events：v1 範圍 `push`（自動 redeploy）；`installation*` 屬 `github-app-install-flow` spec
- `push` event 流程：解 installation_id → 反查 team → 對該 team 內 `repo_url + ref` 對應之 app（可多個）觸發 redeploy
- `app.repo_default_branch` 之變更：v1 透過 `0ops apps update` 顯式設定；不在 webhook 自動 follow 預設 branch 變動
- CLI/MCP 主動 redeploy：走 `preview-confirm-gate` 之 `redeploy` action；preview side_effects 一致
- redeploy 與 webhook 觸發 redeploy 共用同一 `internal/server/services/redeploy/` 邏輯；只有來源 actor 不同（`actor=user_id` vs `actor=system:github_webhook`）
- Per-token / per-team rate limit：屬 `rate-limit-and-abuse` spec；本 spec 引用而不重述

## 2. 範圍

### 2.1 包含
- `POST /webhooks/github` endpoint：HMAC 驗章、timestamp（GitHub 不提供 timestamp，靠 dedup 時間窗）、replay protection
- `push` event 處理：installation 反查、repo + ref → app 列表、觸發 redeploy
- `redeploy` action（preview/confirm，CLI/MCP 主動）
- `internal/server/services/redeploy/`：共用 redeploy 邏輯（webhook + 主動）
- Webhook payload 過大 / malformed 防範
- audit log 規約（webhook actor 為 `system:github_webhook`）
- Webhook secret rotation 時的雙 secret 並存

### 2.2 不包含
- `installation*` event（屬 `github-app-install-flow` § 7）
- GHA → backend callback（屬 `build-pipeline-and-callback` § 6；協定不同）
- Build pipeline 內部行為（屬 `build-pipeline-and-callback` spec）
- Per-app webhook（v1 採 GitHub App 全 repo webhook，非 per-app webhook）
- PR-based preview deployment（v2）
- `release` event 自動 deploy（v1.1）

## 3. 檔案結構

```
0ops/
└── internal/
    └── server/
        ├── routers/
        │   └── webhooks.go            # POST /webhooks/github
        ├── services/
        │   ├── githubwebhook/
        │   │   ├── verify.go          # HMAC 驗章 + replay
        │   │   ├── parse.go           # 解 event type + payload
        │   │   ├── dispatcher.go      # 路由到 redeploy / installation handler
        │   │   ├── push_handler.go    # push event 處理
        │   │   ├── metrics.go
        │   │   └── doc.go
        │   └── redeploy/
        │       ├── action.go          # redeploy Action 實作（preview-confirm-gate 介面）
        │       ├── trigger.go         # 共用 redeploy 邏輯
        │       └── doc.go
```

## 4. Webhook endpoint

### 4.1 路由

```go
r.Post("/webhooks/github", webhook.Handler)
```

- 不經 `AuthBearer` middleware；改由 HMAC 驗章
- 不需 team_slug path（GitHub App webhook 為 App-wide，不分 team）
- 對外 NetworkPolicy：v1 開放 inbound（HMAC 為認證主路徑）；v2 補 GitHub IP allowlist（GitHub `meta` API 提供）

### 4.2 Handler 流程

```
1. 取 raw body（含原始 byte stream，後續 HMAC 用）
   - body 大小 > 5 MB → 400 webhook_payload_too_large
2. 驗 X-Hub-Signature-256：
   - HMAC-SHA256(webhook-signing-secret, raw_body)
   - 比對（constant-time hmac.Equal）
   - rotation 雙 window：current 失敗 → 試 previous（與 secrets-management § 5.1 對齊）
   - 兩者皆失 → 401 webhook_signature_invalid
3. 取 X-GitHub-Event header：
   - 不在白名單（push, installation, installation_repositories, ping）→ 200 ok（ignore；避免 GitHub retry）
4. 取 X-GitHub-Delivery → webhook_dedup INSERT (provider='github', delivery_id)
   - 衝突 → 200 ok
5. 解 payload JSON
6. dispatcher 分流：
   - push → push_handler
   - installation → install_handler（屬 github-app-install-flow spec）
   - installation_repositories → install_repos_handler
   - ping → log + 200 ok
7. 200 OK
```

### 4.3 Timestamp / age 處理

- GitHub webhook 預設**不**送 timestamp header；replay 防護完全靠 `delivery_id` dedup（24h 內唯一）
- 對「24h 後重送同 delivery_id」之攻擊：webhook_dedup 過 24h 自然清理，此時對應 deploy_run 已 terminal，重送只觸發新 redeploy（合理）
- 不採額外時間窗（與 GHA callback 之 ±5 min 不同；GHA callback 可控 timestamp，GitHub webhook 不可控）

## 5. `push` event 處理

### 5.1 Event payload 解析

```json
{
  "ref": "refs/heads/main",
  "before": "old_sha",
  "after": "new_sha",
  "deleted": false,
  "repository": {
    "id": 123,
    "html_url": "https://github.com/vercel/next.js-helloworld",
    "default_branch": "main"
  },
  "installation": {
    "id": 12345
  },
  "pusher": { ... },
  "commits": [...]
}
```

### 5.2 Push handler 流程

```
1. 解 installation.id → SELECT team WHERE github_install_id = ?
   - 0 row → 200 ok（GitHub 端的 install 已不在 0ops，忽略）
2. 解 ref → branch_name（去除 refs/heads/）
   - 非 refs/heads/* → 200 ok（v1 不處理 tag push）
3. 若 deleted=true → 200 ok（branch delete 不觸發 redeploy）
4. 解 repository.html_url → repo_url（normalize：trim trailing slash）
5. SELECT app
   FROM app
   WHERE team_id = ?
     AND repo_url = ?
     AND repo_default_branch = ?
     AND status = 'live'  -- 僅 live app 自動 redeploy
6. 對每個 app row：
   - check rate limit (per-team build：20/hour for free, plan-dependent)
     - 達上限 → audit log + skip；不 fail webhook（仍 200 給 GitHub）
   - check 是否有 in-flight deploy_run（非 terminal）
     - 有 → audit log + skip；避免 build storm
   - trigger redeploy:
     - 內部呼 redeploy.Trigger(ctx, RedeployArgs{
         AppID, CommitSHA: payload.after, Ref: branch_name,
         Source: 'webhook',
         WebhookDeliveryID: deliveryID,
       })
7. audit_log (webhook event level)
8. 200 OK
```

### 5.3 同 push 多 app 場景

- 一個 GitHub repo 可能對應同 team 多個 app（user 把同 repo deploy 兩次）
- 設計：每個 app 各自獨立 deploy_run；並行觸發
- 限：per-team 全 app 並發上限（屬 `rate-limit-and-abuse` spec）

## 6. Redeploy action

### 6.1 args

```go
// internal/shared/dto/deploys.go
type RedeployArgs struct {
    AppSlug   string  `json:"app_slug"`
    Ref       *string `json:"ref,omitempty"`         // 可選；缺則用 app.repo_default_branch
    CommitSHA *string `json:"commit_sha,omitempty"`  // 可選；缺則拉 ref 之 HEAD
}

type RedeployLastResult struct {
    DeployRunID string `json:"deploy_run_id"`
    TraceID     string `json:"trace_id"`
    CommitSHA   string `json:"commit_sha"`
    Source      string `json:"source"`               // user | webhook
    InitialDeploy bool `json:"initial_deploy"`       // false
}
```

### 6.2 Preview SideEffects

```
1. SELECT app + 驗 status != paused
   - paused → 422 app_paused
2. 解 ref / commit_sha：
   - args.commit_sha 存在 → 用之
   - 否則：
     - ref := args.ref ?? app.repo_default_branch
     - GitHub API 拉 ref 之最新 commit_sha（用 installation_token）
3. 計算 side_effects（4 項；與 create_app 類似但不含 R1 之 INSERT app / domain_binding；不含 R2 namespace ensure，因 namespace 已存在）
   1. INSERT deploy_run row（reversible）
   2. 0ops-gitops 不需新 render（manifest 已存在；callback 後 backend render 更新 image_ref）
   3. 觸發 GHA workflow_dispatch（irreversible）
   4. ArgoCD sync 新 image（reversible by manifest revert）
4. action_summary：「重新部署 app `<slug>` (commit `<sha[:7]>`)」
```

### 6.3 Execute

接續 `preview-confirm-gate` 之 reversible/irreversible：
1. INSERT deploy_run（reversible）
2. 簽 ops_token（irreversible 起點）
3. workflow_dispatch（irreversible）
4. 後續同 `create-app-flow` callback-driven path

### 6.4 Webhook 觸發 redeploy 之差異

- 不走 preview-confirm-gate（webhook 為非 user-interactive）
- 直接呼 `redeploy.Trigger(ctx, args)`，內部執行：
  - INSERT deploy_run（actor_user_id=NULL；source='webhook'；webhook_delivery_id=...）
  - 簽 ops_token + dispatch
  - 失敗即 audit + reconciler 收斂
- 不需 user 確認；不顯示 PlanPreview；webhook 為「事件驅動」而非「user 動作」

## 7. App `status=paused` 行為

承 `github-app-install-flow` § 5.3：

| 觸發 | 行為 |
|---|---|
| Webhook push event 到 paused app | audit log + skip；200 給 GitHub |
| CLI/MCP 主動 redeploy paused app | 422 app_paused |
| user 跑 `0ops apps update <slug> --resume`（v1.1） | UPDATE status='live' |
| v1 替代：user 跑 `0ops deploys redeploy <slug>` 不通；需先在 DB 手動改（runbook） | / |

> v1 局限：無 CLI 命令切回 live；user 須走 runbook 或重新跑 `create_app`（覆寫情境，需釐清）

## 8. Webhook secret rotation

### 8.1 Dual secret window

接續 `secrets-management` § 5.1（A 類）：

- backend 啟動時讀 `webhook-signing-secret` Secret 之 `current-key` + `previous-key`
- 驗章先試 current → 失敗試 previous（若 previous 未過期）
- rotation 時 ops 在 GitHub App 設定中：
  1. 先在 GitHub App 改 webhook secret 為新值
  2. 等 30 分鐘（雙 window）
  3. backend Secret rotate → 新 secret 變 current，舊變 previous
  4. 30 分鐘後 finalize（移除 previous）

> 注意 GitHub App 端只能設**一個** webhook secret；rotation 期間 GitHub 用新 secret 簽，backend 雙 window 支援

### 8.2 Rotation 故障處理

- backend 端 secret 比 GitHub 端慢更新：webhook 簽章用 GitHub 端新 secret，backend 用 previous（舊）驗失敗 → 401
- 對策：`secret_use_failed` audit + alert；ops 確認 backend 已更新

## 9. 與其他 spec 接合點

| 接合 | spec |
|---|---|
| Webhook signature secret 之 K8s Secret + rotation | `secrets-management` § 4 |
| `webhook_dedup` 表 | `docs/0ops-plan.md` DB schema |
| Replay protection 邏輯 | ADR-0005 § 4.6 + `build-pipeline-and-callback` § 6.4（共用 webhook_dedup table 但 provider 不同）|
| `installation*` event 路由 | `github-app-install-flow` § 7 |
| Redeploy 共用邏輯 | `create-app-flow` 之 build dispatch + callback |
| Rate limit per-team build | `rate-limit-and-abuse` spec |
| `webhook_signature_invalid` / `webhook_payload_too_large` 失敗碼 | `error-model` § 5.3 |
| audit_log（actor=system:github_webhook） | `audit-log` spec |
| Reconciler 收斂 building 滯留 | `reconciler-and-incident` spec |

## 10. 驗證準則

| 驗證項 | 方式 | 通過條件 |
|---|---|---|
| HMAC 驗章成功 | 用 secret 簽 payload | 200 OK + handler 執行 |
| HMAC 驗章失敗 | 改 1 byte sig | 401 webhook_signature_invalid |
| 雙 secret rotation | rotation 中：用新或舊 secret 簽 | 兩者皆 200 |
| Payload too large | body > 5MB | 400 webhook_payload_too_large |
| Replay（同 delivery_id） | 同 webhook 重送 | 第二次 200 但 push handler 不重執行 |
| 不在白名單 event | 送 `pull_request` event | 200 ok（ignore） |
| Push to live app | mock push event | 對應 app 觸發 redeploy；deploy_run 創建 |
| Push to paused app | mock | audit log skip；不觸發 redeploy |
| Push to repo 不對應 app | mock 不存在的 repo_url | 200 ok（ignore） |
| Push 多 app（同 repo）| mock 同 team 兩 app 用同 repo | 兩個 deploy_run 並行創建 |
| In-flight deploy_run 偵測 | mock app 已有 building 之 deploy_run + 新 push | skip 第二個；audit log |
| Per-team build rate limit | mock 21 push within 1h | 第 21 個 skip；audit |
| Branch delete event | mock deleted=true | 200 ok；不觸發 |
| Tag push（refs/tags/*）| mock | 200 ok；不觸發 |
| Webhook 觸發 redeploy 之 audit | 觀察 audit_log | actor='system:github_webhook'；含 webhook_delivery_id |
| CLI 主動 redeploy preview | `0ops deploys redeploy <slug>` | 顯示 PlanPreview + side_effects |
| CLI 主動 redeploy 之 actor | observation | actor=user_id；source='user' |
| `app.repo_default_branch` 過濾 | push to non-default branch | 不觸發（即使 ref 屬該 repo） |

## 11. SLI 對應

| SLI | 目標 | 量測 |
|---|---|---|
| Webhook 處理 latency | p95 < 500ms | endpoint 端到端 |
| Webhook signature reject rate | < 0.1% | `webhook_signature_invalid / total` |
| Webhook replay rate | < 0.5% | `webhook_dedup conflict / total` |
| Push → deploy_run 創建延遲 | p95 < 1s | webhook 收到至 INSERT deploy_run 之時間 |
| Webhook → live time | 同 build lead time（plan SLO p50 < 10 min） | `live - deploy_run.created` p50 |

## 12. 對 `docs/0ops-plan.md` 的修改清單

1. 「Webhook 安全」段：交叉引用本 spec 為流程 source；plan 內 HMAC + dedup 描述保留為摘要
2. 「Build & deploy」段：`Re-deploy: Webhook 自動 + CLI/API 手動 雙觸發` 之展開於本 spec
3. 「DB schema § deploy_run」：新增欄位 `source text not null default 'user'`（值：`user` / `webhook` / `reconciler`）、`webhook_delivery_id text`
4. 「Tool catalog」`redeploy` 行：交叉引用本 spec § 6
5. ADR-0005 第 5 點關於 `webhook_dedup`：補入「provider 同 table，依 `provider` 欄位區分 `gha-callback` 與 `github`」

## 13. Open issues

- `app.status='paused'` resume CLI 命令：v1 無；建議 v1.1 補 `0ops apps resume <slug>`
- v1 同 push 多 app 並行觸發是否撞 build minute 配額：需與 plan tier 對齊（ADR-0011）；本 spec 不限制
- `release` event 自動 deploy：v1.1 評估；可能與 `ref=tags/v*` 搭配
- `push` 至非 default branch 不觸發：v1 設計；user 若需 deploy from feature branch 須走 CLI 主動 redeploy --ref；v1.1 評估 PR-based preview
- Webhook payload 過大上限 5MB：與 GitHub 預設一致；超過為惡意或異常 push（含大 commits 列表）
- Webhook 來源 IP 驗證：v1 不做（HMAC 為主）；v2 加 GitHub IP allowlist
- Branch delete event：v1 不處理；v1.1 評估自動 archive deploy_run
- 觀察期內 webhook signature 失敗率高：監控；可能 secret rotation 出 bug
- `repo_default_branch` 之變動如何同步：v1 透過 `0ops apps update --branch=...` 顯式設；GitHub `repository.default_branch` 變動 webhook 不主動 follow

## 14. 不可違反的硬性規則

> 違反以下任一項，PR 不可合入。

1. 所有 webhook 必驗 HMAC 簽章；不得跳過驗章接受 payload
2. webhook secret 必走雙 window rotation；不得 in-place 覆寫
3. webhook_dedup 必入；同 delivery_id 必走回放路徑
4. `installation*` 不於本 spec 處理；必路由至 `github-app-install-flow` § 7
5. paused app 不得自動 redeploy（webhook 觸發即 skip + audit）
6. Webhook 觸發之 redeploy 不走 preview-confirm-gate；但必經共用 `redeploy.Trigger` 邏輯（與 CLI 主動 trigger 共用一段程式碼）
7. webhook payload 大小上限 5MB；超過 400 reject
8. webhook source 不論 IP 為何皆接受；驗章為唯一信任源
9. CLI / MCP 主動 redeploy 必經 preview-confirm-gate（user-facing 寫入動作）
10. 任何對 GitHub webhook payload 之解析必先驗章；驗章前不得寫 DB / 不得呼外部 API
