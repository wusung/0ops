# Feature Spec：secrets-management

> **狀態**：draft
> **來源**：`docs/0ops-plan.md`「Auth & RBAC / Secrets management」「Webhook 安全」段；ADR-0004（baseline ImagePullSecret）；ADR-0005（callback / token signing secret）；ADR-0007（Cloudflare token）
> **適用範圍**：所有 backend 自身與 managed app 共用之 secret 之儲存、分發、rotation、access 控制；不含 client 端 `~/.config/0ops/auth.json`（屬 `auth-and-rbac` spec）
> **對應 Milestone**：M2（與其他 batch 3 同步上線；M5 補 Postgres HA 後評估升級至 External Secrets Operator）

## 1. 結論（先讀本段）

- v1 採 K8s native `Secret` 為儲存後端；放於 `system-0ops` namespace（backend 自身）與 `team-<slug>`（per-team `ghcr-pull`）
- 所有 secret 皆有對應 Go 端結構與 K8s Secret manifest；命名固定於本 spec § 4 表格
- Rotation 策略依 secret 類型分四類：
  - **A 類（共享 HMAC 對稱密鑰）**：90 天；雙 secret window 30 分鐘
  - **B 類（外部第三方 token）**：依第三方規定（GitHub App private key 為 backend 簽 installation token 用，rotation 視 GitHub 規格）
  - **C 類（ephemeral 短期 token）**：不主動 rotate（過期即失效）
  - **D 類（憑證 / SSH key）**：180 天；雙 key 並存 7 天（git signing 與 SSH auth 共用）
- Bootstrap：M2 GA 前 backend 初始化以 `kubectl create secret` 手動產生；ops runbook 紀錄
- 不採 v1 Vault sidecar / SOPS-encrypted in repo / External Secrets Operator（v2 評估）；理由為 v1 規模 over-engineering
- 所有 secret 寫入 / 讀取 / rotation 必入 audit_log（操作者 = `system` 或對應 user）
- backend 啟動時 fail-fast：必要 secret 缺失即 panic + 印出缺失列表
- secret 內容**不得**進 log、metric label、error envelope（與 `error-model` § 9 對齊）

## 2. 範圍

### 2.1 包含
- Secret 全清單（見 § 4）：用途、儲存位置、Go 端結構、rotation 類別
- K8s Secret manifest 規範（namespace、label、type）
- Rotation 程序（含雙 secret window 機制）
- Bootstrap 程序（runbook 摘要）
- backend 對 secret 之讀取路徑（fail-fast、reload 行為）
- Audit log 規約
- Access control（K8s RBAC 對 Secret 的限制）

### 2.2 不包含
- 個別 secret 之**使用語意**（如 callback HMAC 驗章邏輯屬 `build-pipeline-and-callback` spec）
- client 端 `~/.config/0ops/auth.json`（屬 `auth-and-rbac` spec § 4.5）
- argon2id 參數本身（屬 `auth-and-rbac` spec § 4.4；本 spec 只規範 PAT hash 存在 DB 的位置）
- Postgres 自身的 backup encryption key（屬 `postgres-ha-and-dr` spec）
- v2 Vault / External Secrets Operator 部署（v2）
- managed app **內部**之 secret（如 app 自家 API key）—— 屬 v1.1 後之 `secret_binding` 表（plan.md 已預留）

## 3. 檔案結構

```
0ops/
├── internal/
│   └── server/
│       ├── secrets/
│       │   ├── loader.go               # 啟動時讀 K8s Secret，封裝為 typed struct
│       │   ├── reloader.go             # SIGHUP 觸發 hot reload（v1.1）；v1 重啟即可
│       │   ├── rotator.go              # 雙 window rotation（A 類）
│       │   ├── audit.go                # 寫入 audit_log
│       │   ├── types.go                # 各 secret 之 typed struct
│       │   └── doc.go
└── deploy/
    └── chart/
        └── launchpad/
            └── templates/
                └── secrets-secret.yaml.example   # 範本，實際 Secret 由 ops 手動 apply
```

## 4. Secret 全清單

| Secret 名稱 | 位置 | Type | 用途 | 鎖定 spec | Rotation 類別 |
|---|---|---|---|---|---|
| `cloudflare-api-token` | `system-0ops` | Opaque | Cloudflare API 認證（DNS / Custom Hostname / Tunnel） | `winshare-subdomain-and-tunnel` § 8.1 | **A**（90d / 30min window）|
| `cloudflared-tunnel-token` | `cloudflare-tunnel` | Opaque | cloudflared 連 Cloudflare edge 的 tunnel token | `winshare-subdomain-and-tunnel` § 5.3 | **B**（v1 不 rotate；屬 v2 tunnel ID rotation 事件）|
| `github-app-private-key` | `system-0ops` | Opaque | 簽 GitHub App JWT 以換 installation token | `github-app-install-flow` spec | **B**（180d；走 GitHub UI 換新 key + dual key window）|
| `ops-token-signing-secret` | `system-0ops` | Opaque | 簽 / 驗 ops_token（20 min ephemeral） | `build-pipeline-and-callback` § 5.4 | **A**（90d / 30min window）|
| `ops-callback-secret` | `system-0ops` + GitHub repo secret | Opaque | callback 之 emergency fallback HMAC（v1 保留，v2 移除）| 同上 | **A**（90d / 30min window）|
| `gitops-deploy-key` | `system-0ops` | Opaque | SSH private key：對 0ops-gitops 之 push 認證 + commit signing | `gitops-render-and-argocd` § 7 | **D**（180d / 7d window）|
| `gitops-known-hosts` | `system-0ops` | Opaque | GitHub host key（防 MITM） | `gitops-render-and-argocd` § 7.1 | 不 rotate（GitHub host key 變更時 PR 更新） |
| `webhook-signing-secret` | `system-0ops` | Opaque | GitHub Webhook payload 驗章（HMAC）；於 GitHub App 設定中對應 | `webhook-and-redeploy` spec | **A**（90d / 30min window）|
| `ghcr-pull` | `team-<slug>` | dockerconfigjson | per-team GHCR pull credential | `k3s-namespace-isolation` § 8 | **C**（30 min refresh；ephemeral）|
| `postgres-app-credentials` | `system-0ops` | Opaque | application DB 連線（user / password） | `postgres-ha-and-dr` spec | **A**（90d；Postgres role 需支援雙密碼，v1 採重啟換密碼）|
| `postgres-datastore-credentials` | （K3s control plane）| Opaque | K3s kine 之 datastore Postgres 連線 | ADR-0004 baseline | **B**（人工 rotation；屬 datastore runbook） |
| `r2-backup-credentials` | `system-0ops` | Opaque | WAL archive 推 R2 / S3 之 access key | `postgres-ha-and-dr` spec | **A**（90d）|

> Personal team 與 PAT 不在本表：PAT 之雜湊存於 application DB `cli_token.token_hash`，明文不入 Secret；rotation 屬 user 操作（`0ops auth tokens create`）

## 5. Rotation 類別

### 5.1 A 類：共享 HMAC 對稱密鑰

- 週期：90 天
- 雙 secret window：30 分鐘
- 程序：
  1. ops 跑 `0ops-ops rotate-secret <name>`（CLI tool 或 runbook 腳本）
  2. backend 對應 Secret 加入 `current-key` + `previous-key` 兩欄位
  3. Secret 範例：
     ```yaml
     apiVersion: v1
     kind: Secret
     metadata:
       name: ops-token-signing-secret
       namespace: system-0ops
     type: Opaque
     data:
       current-key: <base64(new_secret)>
       previous-key: <base64(old_secret)>
       previous-expires-at: <base64("2026-05-10T13:04:56Z")>
     ```
  4. backend 簽章一律用 `current-key`；驗章接受 `current-key` 與 `previous-key`（後者若 `previous-expires-at < now()` 拒）
  5. 30 分鐘後 ops 跑 `0ops-ops finalize-rotation <name>`：移除 `previous-key`、`previous-expires-at`
  6. backend 偵測 `previous-key` 不存在 → 只用 `current-key`

### 5.2 B 類：外部第三方 token

- 不對應「雙 window」概念（外部簽發為單 token）
- 程序屬人工：依第三方平台 UI 換新；backend Secret 直接覆寫
- backend 在更新 Secret 後**必重啟**（v1 不支援 hot reload）；ops runbook 排程於低流量時段
- v1.1 評估 SIGHUP 熱重載

### 5.3 C 類：ephemeral 短期 token

- 不需 rotation；由 backend 自動簽發 / 過期
- 例：`ghcr-pull`（30 min refresh）、`ops_token`（20 min TTL，一次性）
- 失效不影響運維；refresh 失敗才 alert

### 5.4 D 類：憑證 / SSH key

- 週期：180 天
- 雙 key 並存 7 天
- 程序：
  1. 產生新 Ed25519 key pair
  2. public key 加入：
     - GitHub repo 0ops-gitops 之 deploy key（write）
     - GitHub allowed signers
  3. K8s Secret `gitops-deploy-key` 加入 `current-private-key` + `previous-private-key` 兩欄
  4. backend push 與 commit signing 用 `current-private-key`；fetch 接受任一 key
  5. 7 天後 ops 移除舊 deploy key（GitHub UI）+ K8s Secret 內 `previous-private-key`

## 6. K8s RBAC 與 Secret 存取

### 6.1 Backend ServiceAccount

`system-0ops` namespace 下 ServiceAccount `ops-server`：

```yaml
apiVersion: rbac.authorization.k8s.io/v1
kind: Role
metadata:
  name: ops-server-secrets-read
  namespace: system-0ops
rules:
  - apiGroups: [""]
    resources: ["secrets"]
    verbs: ["get", "watch", "list"]
    resourceNames:                     # 限定可讀 secret
      - cloudflare-api-token
      - github-app-private-key
      - ops-token-signing-secret
      - ops-callback-secret
      - gitops-deploy-key
      - gitops-known-hosts
      - webhook-signing-secret
      - postgres-app-credentials
      - r2-backup-credentials
---
apiVersion: rbac.authorization.k8s.io/v1
kind: RoleBinding
metadata:
  name: ops-server-secrets-read
  namespace: system-0ops
subjects:
  - kind: ServiceAccount
    name: ops-server
roleRef:
  apiGroup: rbac.authorization.k8s.io
  kind: Role
  name: ops-server-secrets-read
```

### 6.2 跨 namespace（管理 ghcr-pull）

backend 需在 `team-<slug>` 寫入 `ghcr-pull`：

```yaml
apiVersion: rbac.authorization.k8s.io/v1
kind: ClusterRole
metadata:
  name: ops-server-ghcr-pull-write
rules:
  - apiGroups: [""]
    resources: ["secrets"]
    verbs: ["get", "create", "update", "patch"]
    resourceNames: ["ghcr-pull"]      # 限定 secret 名稱
---
apiVersion: rbac.authorization.k8s.io/v1
kind: ClusterRoleBinding
metadata:
  name: ops-server-ghcr-pull-write
subjects:
  - kind: ServiceAccount
    name: ops-server
    namespace: system-0ops
roleRef:
  apiGroup: rbac.authorization.k8s.io
  kind: ClusterRole
  name: ops-server-ghcr-pull-write
```

> 此 ClusterRole 僅允許 `ghcr-pull` 一個 secret 名；不允許 backend 看到其他 team 自家可能放的 secret（v1.1 secret_binding 範圍）

### 6.3 ops 人員存取

- ops 個人 K8s 使用者（kubeconfig）需可 read / write 上述 secret 以做 bootstrap 與 rotation
- 不直接給 cluster-admin；改給 `system-0ops:secrets-admin` Role
- audit log：K3s 預設不開 audit log；本 spec 要求啟用 audit policy 對 `secrets` 操作（rule: log Metadata level）

## 7. Bootstrap（M2 GA 前）

### 7.1 順序

```
1. K3s cluster 已就緒（含 system-0ops namespace）
2. 產生：
   - cloudflare-api-token：Cloudflare Dashboard 建 API token（限 jesontech.com zone）
   - github-app-private-key：GitHub UI 為 0ops App 下載 .pem
   - ops-token-signing-secret：openssl rand -base64 64
   - ops-callback-secret：openssl rand -base64 64
   - webhook-signing-secret：openssl rand -base64 64
   - postgres-app-credentials：DBA 端建 user + 密碼
   - r2-backup-credentials：Cloudflare R2 建 bucket + access key
3. 產生 SSH key pair：ssh-keygen -t ed25519 -f gitops-deploy-key -N ""
   - public 加為 0ops-gitops repo deploy key (write)
   - public 加為 GitHub allowed signers (https://github.com/settings/ssh/allowed_signers)
   - private + known_hosts → K8s Secret
4. cloudflared-tunnel-token：cloudflared tunnel token
5. ops-callback-secret 也同步寫入 GitHub repo 0ops 之 secret store（actions 用）
6. kubectl apply 所有 Secret manifest
7. backend chart 部署；啟動時讀 secret，fail-fast 檢查
```

### 7.2 Bootstrap 失敗

- backend panic + 印出缺失 secret 名稱列表
- ops 補完後重啟即可

## 8. backend 對 Secret 的讀取

### 8.1 啟動時 fail-fast

```go
// internal/server/secrets/loader.go
type Set struct {
    CloudflareAPIToken    string
    GitHubAppPrivateKey   []byte
    OpsTokenSigning       *DualKey       // current + previous
    OpsCallback           *DualKey
    WebhookSigning        *DualKey
    GitOpsDeployKey       *DualKey       // SSH private key
    GitOpsKnownHosts      string
    PostgresAppDSN        string
    R2AccessKey           string
    R2SecretKey           string
}

type DualKey struct {
    Current     []byte
    Previous    []byte             // 可 nil
    PreviousExp time.Time          // 若 Previous != nil 必設
}

func Load(ctx context.Context, k8s *kubernetes.Clientset) (*Set, error) {
    // get / parse 全部；任一缺失 / parse 失敗即 return error
    // backend main 收到 error 即 log + os.Exit(2)
}
```

### 8.2 Reload 行為

- v1：不支援 hot reload；rotation 後必重啟 backend pod（rolling 即可）
- v1.1：SIGHUP 觸發重讀 K8s Secret；DualKey 結構天然支援平滑切換
- M5 多 replica：rotation 由 ops 一次性 patch K8s Secret，backend pod rolling 即可（無需 leader）

### 8.3 K8s Secret watch

- 不 watch（v1）；簡化邏輯
- v1.1 改 watch + on-change 觸發 reload

## 9. Audit log

### 9.1 必入 audit_log 之事件

| 事件 | actor | subject | action |
|---|---|---|---|
| Secret 建立（bootstrap） | `system` 或 ops user_id | secret name | `secret_create` |
| Secret rotation 開始（current+previous 並存） | ops user_id | secret name | `secret_rotate_start` |
| Secret rotation 完成（移除 previous） | ops user_id | secret name | `secret_rotate_finalize` |
| Secret 讀取失敗（backend 啟動 fail-fast） | `system` | secret name | `secret_load_failed` |
| Cloudflare API 用該 token 失敗（401 / 403） | `system` | secret name | `secret_use_failed` |
| ops_token / ghcr-pull 等 ephemeral 之 refresh 失敗 | `system` | secret name | `secret_refresh_failed` |

### 9.2 不入 audit_log

- 正常 secret 讀取（每 request 都讀 ops_token 之 verify 不寫）
- 正常 secret refresh（每 30 min ghcr-pull 不寫；只 metric）

### 9.3 審計查詢

`0ops audit list --action=secret_*` 可列出所有 secret 相關事件（屬 `audit-log` spec）

## 10. 違規與例外處理

### 10.1 Secret 內容洩漏疑慮

- 若任一 secret 疑似洩漏（log 異常、外部 token 觸發 alert）：
  1. 立即 rotate（不等 90 天週期）
  2. 對應外部系統撤銷舊 token（Cloudflare / GitHub）
  3. audit_log + post-mortem ticket

### 10.2 Rotation 失敗

- A 類雙 window 期間若新 key 簽章失敗：backend 自動 fallback 至 previous（驗章邏輯允許）
- 僅當 previous 已過期 + current 失效：服務中斷；ops 緊急介入

### 10.3 Bootstrap 漏 secret

- backend fail-fast；CrashLoopBackOff
- ops 透過 K8s 事件偵測 + alert

## 11. 與其他 spec 接合點

| 接合 | spec |
|---|---|
| Cloudflare API token 使用 | `winshare-subdomain-and-tunnel` § 8.1 |
| ops_token / callback secret 使用 | `build-pipeline-and-callback` § 5.4 |
| GitOps SSH key 使用 + signing | `gitops-render-and-argocd` § 7 |
| `ghcr-pull` per-team refresh | `k3s-namespace-isolation` § 8 |
| GitHub App private key（簽 installation token） | `github-app-install-flow` spec |
| GitHub Webhook 簽章 secret | `webhook-and-redeploy` spec |
| Postgres 連線 secret | `postgres-ha-and-dr` spec |
| Audit log 寫入規約 | `audit-log` spec |
| Redaction（secret 不入 log/metric/error） | `error-model` § 9、`observability-skeleton` § 5.3 |
| K3s ServiceAccount + RBAC | `k3s-namespace-isolation` spec |

## 12. 驗證準則

| 驗證項 | 方式 | 通過條件 |
|---|---|---|
| Bootstrap 漏 secret backend fail-fast | 移除一個 secret，重啟 backend | log 印缺失名稱、exit code 2 |
| Secret RBAC 限制 | backend 嘗試 read 一個未列在 resourceNames 的 secret | K8s API 拒（403） |
| 跨 namespace `ghcr-pull` write | backend 對 `team-x` patch `ghcr-pull` | 成功 |
| 跨 namespace 其他 secret write | backend 對 `team-x` 寫 `random-secret` | 拒 |
| A 類 dual window 簽 / 驗 | rotation 中：用 current 簽，用 previous 簽，皆能驗 | 兩個都驗成功 |
| previous 過期 | `previous-expires-at < now()` | previous 簽章拒；only current 可驗 |
| Rotation finalize | 移除 previous-key 後 | backend 偵測；驗章只接受 current |
| Audit log 寫入 | 跑一次 rotation | audit_log 含 `secret_rotate_start` + `secret_rotate_finalize` |
| Secret 不入 log | log 全文 grep secret value | 0 命中 |
| Secret 不入 metric label | `/metrics` grep secret 部分內容 | 0 命中 |
| K3s audit log 對 secret 操作 | `kubectl get --raw /api/v1/namespaces/system-0ops/events` | 含 secret CRUD 事件 |
| Cloudflare API token 失效 | mock 401 | audit_log 寫 `secret_use_failed`；alert 觸發 |

## 13. SLI 對應

| SLI | 目標 | 量測 |
|---|---|---|
| Secret rotation 不中斷服務 | 每次 rotation 中無 5xx 增加 | `0ops_http_requests_total{status=5xx}` 在 rotation window 期間與基線一致 |
| ghcr-pull refresh 成功率 | > 99% / 28d | `0ops_ghcr_pull_secret_refresh_total{outcome=success}` |
| Secret use 失敗率 | < 0.1% / 28d | `secret_use_failed` audit_log 比例 |

## 14. 對 `docs/0ops-plan.md` 的修改清單

1. 「Auth & RBAC / Secrets management」段：交叉引用本 spec 為 v1 完整規範
2. 「Webhook 安全 § 內部 deploy callback」段：`X-0ops-Signature` 之 secret 為 ops_token（短期），fallback 為 `OPS_CALLBACK_SECRET`（長期）；補入本 spec § 4 對應行
3. 「Build & deploy」段：明確 `OPS_CALLBACK_SECRET` 在 v1 為 fallback、v2 移除；同步交叉引用
4. ADR-0004 § 4 baseline 提及之「ImagePullSecret 30 min refresh」：交叉引用本 spec 為 C 類
5. 「DB schema」段：補入 `cli_token.kind` 列舉須含 `ops_token`（與 `auth-and-rbac` § 4.3 重複，本 spec 再強調）

## 15. Open issues

- v2 引入 External Secrets Operator + 雲端 KMS：時程未定；規模 + 多客戶部署時觸發
- SOPS-encrypted manifest in repo：v1 不採；v2 評估（適合 IaC / chart 配置，但 application secret 不適合）
- Vault sidecar：v2 評估
- Postgres role 雙密碼支援：PostgreSQL 16 已支援；application Postgres rotation 改 hot 切換之時程待 v1.1
- ops 操作 Secret 之 audit 來源：K3s audit policy 啟用 + log shipping 至中央；屬 ops runbook
- Backup 加密：R2 server-side encryption 預設啟用；客戶端加密（額外保險）待 v2
- secret_binding 表（per-team app secret 注入 build）：屬 v1.1
- v1.1 SIGHUP hot reload 之並發安全：rotator 與 reloader 共寫 DualKey 之 race；屬 v1.1 設計
- managed app 自家 secret（如 DB password、API key）：v1 不提供 0ops 端管理；客戶用 K8s Secret 自管；v1.1 補 `secret_binding` 表

## 16. 不可違反的硬性規則

> 違反以下任一項，PR 不可合入。

1. Secret 明文不得進 git repo（含 0ops repo 與 0ops-gitops repo）
2. Secret 明文不得進 log message、metric label、error envelope、audit_log args / result（與 redactor 對齊）
3. backend 啟動時必檢查 § 4 表內 production-required secret；缺失即 fail-fast（不啟動半身）
4. Secret 之 K8s RBAC 必走 `resourceNames` 限定；不得開 `secrets/*` 全 read
5. A 類 secret rotation 必走雙 window（current + previous）；不得 in-place 覆寫造成簽章瞬間失效
6. Secret 變更必入 audit_log（actor / subject / action / trace_id）
7. v1 不得部署 Vault / SOPS / External Secrets Operator；屬 v2 範圍
8. ops 個人 kubeconfig 不得 cluster-admin；必走 `secrets-admin` Role
9. K3s audit policy 必對 `secrets` 操作開 Metadata level log；不得關閉
10. ephemeral secret（C 類）之 refresh 失敗不得 silent；必入 `secret_refresh_failed` audit + metric
