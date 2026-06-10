# Feature Spec：k3s-namespace-isolation

> **狀態**：draft
> **來源**：`docs/0ops-plan.md`「Runtime topology & operability」段；ADR-0001（per-team 邊界）；ADR-0004（K3s baseline）
> **適用範圍**：K3s cluster 中 backend 自身（`system-0ops`）與 managed apps（`team-<slug>`）之 namespace、ResourceQuota、LimitRange、NetworkPolicy、PSA、ImagePullSecret 配置
> **對應 Milestone**：M2（與 create_app 同步上線；ResourceQuota / PSA 為 GA 阻擋項）

## 1. 結論（先讀本段）

- Namespace 命名固定：`system-0ops`（backend 自身）、`team-<team_slug>`（每 team 一個）；不得自由命名
- 每 team 對應**一個** namespace（不採 per-app namespace；plan.md 已標 K3s namespace 數爆炸風險）
- ResourceQuota + LimitRange + NetworkPolicy + PSA label + ImagePullSecret 為 namespace 建立時**同 transaction** apply；缺一即 namespace 建立失敗
- ResourceQuota 數值依 plan tier（`free` / `starter` / `pro` / `team`）；本 spec 列出 v1 預設值，但 tier→capability 之上游決策仍待 ADR-0011
- PSA：`enforce=baseline / warn=restricted`；v2 升 `enforce=restricted`
- NetworkPolicy 預設：ingress 限 traefik + 同 namespace；egress 允 0.0.0.0/0 但封 RFC1918
- ImagePullSecret `ghcr-pull` 由 backend 用 GitHub App installation token 簽發；30 min refresh
- Namespace 物件由 `0ops-gitops` repo 之 `teams/<team_slug>/namespace.yaml` 管理（與 `gitops-render-and-argocd` § 4.1 對齊）
- Backend 自身 namespace `system-0ops` 不在 GitOps 範圍；由 backend 自身 chart `deploy/chart/launchpad/` 直接 apply（屬 deployment runbook）

## 2. 範圍

### 2.1 包含
- `system-0ops` 與 `team-<slug>` namespace 之 manifest 結構與 label
- ResourceQuota 各 plan tier 預設值（pending ADR-0011 拍板）
- LimitRange 預設值
- NetworkPolicy ingress / egress 規則
- PSA label 設定與升級路徑
- `ghcr-pull` ImagePullSecret 之建立、refresh、rotation
- Namespace 建立 / 刪除流程（與 `create_app` first-time、team archival 對齊）
- Backend 自身 `system-0ops` 與 managed app 隔離規則

### 2.2 不包含
- `apps/<team>/<app>/` 應用層 manifest（屬 `gitops-render-and-argocd` spec）
- ArgoCD ApplicationSet（屬 `gitops-render-and-argocd` spec）
- K3s cluster 自身的 datastore / control plane / 節點配置（屬 ADR-0004 與 deployment runbook）
- Backend HA / leader election（屬 `backend-ha-leader-election` spec）
- 客戶域名 TLS / Cloudflare Tunnel（屬 `winshare-subdomain-and-tunnel` 與 `custom-domain-and-verify` spec）
- Postgres / kine datastore HA（屬 `postgres-ha-and-dr` spec）
- 自動 scaling / HPA（v2）

## 3. 檔案結構

### 3.1 GitOps 內 namespace manifest

```
0ops-gitops/
├── teams/
│   └── <team_slug>/
│       ├── namespace.yaml            # Namespace 物件
│       ├── resourcequota.yaml        # ResourceQuota
│       ├── limitrange.yaml           # LimitRange
│       ├── networkpolicy-ingress.yaml
│       ├── networkpolicy-egress.yaml
│       ├── kustomization.yaml        # 收口 + 引向 apps/<team>/*
│       └── README.md                 # 「machine-managed; do not edit」
```

### 3.2 Backend 自身 chart

```
0ops/
└── deploy/
    └── chart/
        └── launchpad/                # backend 自身 Helm chart
            ├── templates/
            │   ├── namespace.yaml    # system-0ops
            │   ├── deployment.yaml
            │   ├── service.yaml
            │   ├── configmap.yaml
            │   ├── secrets-secret.yaml      # placeholder；實際 secrets 由 sealed-secrets / external secrets 注入
            │   ├── networkpolicy.yaml       # system-0ops 之 NP
            │   └── psa-labels.yaml          # system-0ops 之 PSA
            └── values.yaml
```

> Backend 自身的 chart 不在本 spec 完整定義；本 spec 只規範 `system-0ops` 之 namespace 物件與 PSA / NP 必要設定。

## 4. Namespace 物件

### 4.1 `system-0ops`（backend 自身）

```yaml
apiVersion: v1
kind: Namespace
metadata:
  name: system-0ops
  labels:
    app.0ops.io/managed-by: 0ops
    pod-security.kubernetes.io/enforce: baseline
    pod-security.kubernetes.io/warn: restricted
    pod-security.kubernetes.io/audit: restricted
    kubernetes.io/metadata.name: system-0ops
```

- 不設 ResourceQuota / LimitRange（避免 backend 自身被 quota 吃住）
- NetworkPolicy 設定（§ 6.1）

### 4.2 `team-<team_slug>`

```yaml
apiVersion: v1
kind: Namespace
metadata:
  name: team-<team_slug>
  labels:
    app.0ops.io/managed-by: 0ops
    app.0ops.io/team-slug: <team_slug>
    app.0ops.io/team-id: <team_id_uuid>      # backend 寫入；變更 slug 時 id 不變
    app.0ops.io/plan: <plan>                  # free | starter | pro | team
    pod-security.kubernetes.io/enforce: baseline
    pod-security.kubernetes.io/warn: restricted
    pod-security.kubernetes.io/audit: restricted
    kubernetes.io/metadata.name: team-<team_slug>
```

- `app.0ops.io/team-slug` 變動時（v1 不允許 team rename，但 personal team owner 可能改 GitHub login）：本 spec 採「namespace 名稱固定為建立時 slug」，與 `auth-and-rbac` § 5.2 對齊；不隨 GitHub login 改名而動
- `app.0ops.io/plan` 變動於 plan 升降級時：backend 端 patch namespace label 即可

## 5. ResourceQuota

### 5.1 各 tier 值（source: ADR-0011 § 3.1）

| Resource | free | starter | pro | team |
|---|---|---|---|---|
| `requests.cpu` | 1 | 4 | 16 | 64 |
| `requests.memory` | 2Gi | 8Gi | 32Gi | 128Gi |
| `limits.cpu` | 2 | 8 | 32 | 128 |
| `limits.memory` | 4Gi | 16Gi | 64Gi | 256Gi |
| `persistentvolumeclaims` | 2 | 10 | 40 | 100 |
| `pods` | 5 | 30 | 120 | 300 |
| `services` | 4 | 20 | 80 | 200 |

> 數值由 ADR-0011 釘定；變更走 ADR 補丁。`limits.*` 為 `requests.*` 之 2 倍（K3s LimitRange 慣例）。

### 5.2 ResourceQuota manifest 範例（free tier）

```yaml
apiVersion: v1
kind: ResourceQuota
metadata:
  name: default
  namespace: team-<team_slug>
  labels:
    app.0ops.io/plan: free
    app.0ops.io/managed-by: 0ops
spec:
  hard:
    requests.cpu: "1"
    requests.memory: 2Gi
    limits.cpu: "2"
    limits.memory: 4Gi
    persistentvolumeclaims: "2"
    pods: "5"
    services: "4"
```

### 5.3 Plan tier 升降級

- backend 偵測 `team.plan` 變動 → render `teams/<team_slug>/resourcequota.yaml` → push gitops → ArgoCD sync
- 降級時若實際使用量已超新 quota：K8s 不會殺 pod，但新 pod 會被擋；audit_log 記錄並通知 owner
- 升級為 reversible side_effect；降級也是 reversible（增 quota）

## 6. NetworkPolicy

### 6.1 `system-0ops`

```yaml
apiVersion: networking.k8s.io/v1
kind: NetworkPolicy
metadata:
  name: backend-default
  namespace: system-0ops
spec:
  podSelector: {}
  policyTypes: [Ingress, Egress]
  ingress:
    - from:
        - namespaceSelector:
            matchLabels:
              kubernetes.io/metadata.name: kube-system    # traefik / metrics
        - namespaceSelector:
            matchLabels:
              kubernetes.io/metadata.name: argocd          # ArgoCD 拉狀態
        - namespaceSelector:
            matchLabels:
              kubernetes.io/metadata.name: system-0ops    # 同 namespace
  egress:
    - to:
        - ipBlock:
            cidr: 0.0.0.0/0
            except:
              - 10.0.0.0/8
              - 172.16.0.0/12
              - 192.168.0.0/16
        - namespaceSelector:
            matchLabels:
              kubernetes.io/metadata.name: kube-system    # K8s API、CoreDNS
        - namespaceSelector: {}                            # 對所有 team-<slug> 之 read-only access（K8s API 走 kube-system）
```

> backend 對 K3s API 的呼叫走 `kubernetes.default.svc`（在 default service CIDR），需 explicit 允許；本 spec 採「allow all namespaceSelector + RFC1918 except」搭配。

### 6.2 `team-<team_slug>`

```yaml
apiVersion: networking.k8s.io/v1
kind: NetworkPolicy
metadata:
  name: default-deny-ingress
  namespace: team-<team_slug>
spec:
  podSelector: {}
  policyTypes: [Ingress]
  ingress:
    - from:
        - namespaceSelector:
            matchLabels:
              kubernetes.io/metadata.name: kube-system    # traefik
        - namespaceSelector:
            matchLabels:
              kubernetes.io/metadata.name: cloudflare-tunnel  # tunnel pod 也屬 ingress 來源
        - podSelector: {}                                   # 同 namespace
---
apiVersion: networking.k8s.io/v1
kind: NetworkPolicy
metadata:
  name: default-egress
  namespace: team-<team_slug>
spec:
  podSelector: {}
  policyTypes: [Egress]
  egress:
    - to:
        - ipBlock:
            cidr: 0.0.0.0/0
            except:
              - 10.0.0.0/8
              - 172.16.0.0/12
              - 192.168.0.0/16
        - namespaceSelector:
            matchLabels:
              kubernetes.io/metadata.name: kube-system    # CoreDNS
```

### 6.3 v1.1 擴充

- per-team egress allowlist（`0ops apps update <slug> --egress-allow=...`）：team owner 可主動加 RFC1918 內網位址（如連自家 VPN 內 DB）；屬 v1.1
- ingress 控制更細（如限 IP）：v1 不開

## 7. Pod Security Admission

### 7.1 v1 設定

所有 0ops 管理之 namespace 預設：
- `pod-security.kubernetes.io/enforce: baseline`
- `pod-security.kubernetes.io/warn: restricted`
- `pod-security.kubernetes.io/audit: restricted`

### 7.2 V1 阻擋 baseline 違反

baseline 違反例（會被 K8s API 拒）：
- privileged container
- hostPath / hostPort / hostNetwork / hostPID
- 非 default 的 securityContext.capabilities.add（除少數例外）

### 7.3 v2 升 restricted

restricted 額外要求：
- `runAsNonRoot: true`
- `seccompProfile.type: RuntimeDefault`
- `allowPrivilegeEscalation: false`
- 嚴格 readOnlyRootFilesystem 雖非強制但 warn

升級條件（與 ADR-0004 § 7.5 對齊）：
- backend 自身已通過 restricted（M2 即達成；distroless + nonroot 已對齊）
- managed app 普遍違反率 < 5%（量測：當前 7d 推送之 deploy_run，PSA warn 行為比例）

## 8. ImagePullSecret

### 8.1 `ghcr-pull` 結構

```yaml
apiVersion: v1
kind: Secret
metadata:
  name: ghcr-pull
  namespace: team-<team_slug>
  labels:
    app.0ops.io/managed-by: 0ops
type: kubernetes.io/dockerconfigjson
data:
  .dockerconfigjson: <base64({
    "auths": {
      "ghcr.io": {
        "auth": "<base64(username:installation_token)>",
        "email": "ops-bot@jesontech.com"
      }
    }
  })>
```

- `username`：GitHub App 之 bot account login（`ops-bot[bot]`）
- `installation_token`：GitHub App installation token（1h TTL）；由該 team 之 `team.github_install_id` 簽發

### 8.2 Refresh 邏輯

- backend 啟動時起背景 goroutine `ghcr_token_refresher`
- 每 30 min 對所有 `team WHERE github_install_id IS NOT NULL` 簽發新 token；patch 對應 namespace 之 `ghcr-pull` Secret
- 失敗（installation 無效 / GitHub API rate limited）：保留舊 secret 直至過期；audit_log 記錄；30 min 後重試
- M5 多 replica 後僅 leader 跑 refresh（與 `backend-ha-leader-election` § 1 對齊）

### 8.3 Namespace 建立時的 bootstrap

- `create_app` 首次（team 第一個 app）→ namespace 建立 + ImagePullSecret 同步建立
- 若 team 尚未綁 GitHub App（`team.github_install_id IS NULL`）：禁止 `create_app`；preview 即 fail，提示先 install GitHub App
- ImagePullSecret 由 backend 直接 apply（**不**走 GitOps），因為 token 高頻 rotation 與 GitOps audit trail 不匹配；屬本 spec 第 12 條硬性規則

## 9. Namespace 建立 / 刪除

### 9.1 建立流程

```
create_app（team 之首個 app）→ saga reversible side_effects:
  1. INSERT app row
  2. (若 team 為首次) backend 直接 apply：
     - kubectl apply ghcr-pull Secret
     - 不需 apply Namespace；namespace 由下一步 GitOps 建立
  3. render & push gitops:
     - teams/<team_slug>/namespace.yaml（含 PSA label）
     - teams/<team_slug>/resourcequota.yaml
     - teams/<team_slug>/limitrange.yaml
     - teams/<team_slug>/networkpolicy-{ingress,egress}.yaml
     - teams/<team_slug>/kustomization.yaml
     - apps/<team_slug>/<app_slug>/*
  4. ArgoCD 同步
  5. 觸發 build（接續 build-pipeline-and-callback spec）
```

### 9.2 ImagePullSecret 與 GitOps 順序

ImagePullSecret 必須在 namespace 建立**前**或**同時**就緒，否則第一個 pod pull 會失敗。流程：

- backend 直 apply Secret（with `kubectl create namespace --dry-run=client -o yaml | kubectl apply -f -` 同步 namespace 預先存在）
- 同時 GitOps 端 namespace 建立；ArgoCD 同步時 Secret 已存在
- v1 簡化：backend 端 `kubectl apply Namespace + Secret`（兩物件），ArgoCD selfHeal 時若 namespace 已存在會跳過建立但仍套 label / annotation；不衝突

> v1.1 評估：用 ArgoCD `sync-wave` annotation 強制順序；目前 v1 兩物件並行 apply 即可。

### 9.3 刪除流程（`delete_app` 之 last app）

- 若 team 在 0ops 上之最後一個 app 被刪：保留 namespace（不刪）
  - 理由：team 仍存在；後續可能再 create_app；保留 ImagePullSecret 與 quota 設定
- Team archive（透過 `0ops teams archive`，v1.1 範圍）：保留 namespace 但 quota 設 0；現有 pod 持續跑直到自然死亡；新 pod 擋住
- 物理刪 namespace 屬 v2 範圍（含 `delete_team`）

## 10. 與其他 spec 接合點

| 接合 | spec |
|---|---|
| Namespace / quota manifest 透過 GitOps | `gitops-render-and-argocd` § 4.1 |
| GitHub App installation token 取得 | `github-app-install-flow` spec |
| ImagePullSecret 30 min refresh leader-only | `backend-ha-leader-election` § 1 |
| Quota 升降級 metric 與 alert | `slo-and-alerting` spec |
| `team.plan` 變動觸發 namespace label patch | `audit-log` spec（記錄 plan 變更）|
| `system-0ops` namespace 由 backend chart 建 | deployment runbook（非 spec）|
| Audit log 對 quota 違反之 deploy 失敗 | `reconciler-and-incident` spec |

## 11. 驗證準則

| 驗證項 | 方式 | 通過條件 |
|---|---|---|
| Namespace 命名固定 | `kubectl get ns -l app.0ops.io/managed-by=0ops` | 名稱皆為 `system-0ops` 或 `team-*` |
| PSA label 完整 | `kubectl describe ns team-...` | `enforce=baseline / warn=restricted / audit=restricted` 三 label 齊 |
| ResourceQuota apply | `kubectl get resourcequota -n team-...` | 存在；數值符合 plan tier 表 |
| LimitRange apply | 同上 | 存在；container default `100m / 256Mi`、limit `500m / 1Gi` |
| NetworkPolicy 阻擋 | mock 一個 team 的 pod 嘗試對另一 team 之 service | 連線失敗 |
| NetworkPolicy 允許 traefik | 從 `kube-system/traefik` 嘗試對 `team-<slug>` 之 ingress | 通 |
| ImagePullSecret 存在 | `kubectl get secret ghcr-pull -n team-...` | 存在；type 正確 |
| ImagePullSecret refresh | mock GitHub App API 簽發 token；等 30 min 觸發 | secret 之 `data.dockerconfigjson` 變動 |
| Refresh leader-only（M5） | 啟兩 backend pod；觀察哪個跑 refresh | 僅 leader pod log 出現 refresh；follower 不執行 |
| PSA enforce baseline | 嘗試 apply 一個 privileged pod 至 team-<slug> | K8s API 拒；error 含 `violates PodSecurity baseline` |
| Plan tier 升級 | mock `team.plan: free → pro` | quota row 更新；ArgoCD sync 後 `kubectl describe` 顯示新值 |
| Plan tier 降級含現有使用 | 模擬 already 5 pod，降 free（pods=30） | 既有 pod 留；新 pod 擋於 `pods` 達 30 時 |
| Namespace 建立順序 | mock create_app（team 首次） | Secret 先 apply，Namespace 後或同時，pod 第一次拉 image 不失敗 |
| 對 GitOps prune 友善 | 從 `teams/<team>/` 移除 resourcequota.yaml | ArgoCD 移除 ResourceQuota；team 失去 quota（v1 不允許 → 標 sync 失敗）|

## 12. SLI 對應

| SLI | 目標 | 量測 |
|---|---|---|
| ImagePullSecret refresh 成功率 | > 99% / 28d | `0ops_ghcr_pull_secret_refresh_total{outcome=success/failed}` |
| Pod CrashLoop 因 ImagePullBackOff 比例 | < 0.5% deploy_run | `failure_classification=registry_push_failed` 中 sub-cause `image_pull_backoff` |
| Quota violation 導致 deploy 失敗 | < 1% deploy_run / 7d | reconciler 偵測；audit_log 含 `quota_exceeded` |
| PSA 違反導致 deploy 失敗 | < 0.5% deploy_run / 7d | 同上；`psa_baseline_violation` |

## 13. 對 `docs/0ops-plan.md` 的修改清單

1. 「Runtime topology / Managed app 隔離模型」段：交叉引用本 spec；補入「ImagePullSecret 不走 GitOps，由 backend 直 apply」之說明
2. 「Backend 自身部署 topology」段：補入「`system-0ops` namespace 由 backend chart 建，不在 GitOps」
3. 「Risks & open #7（K3s 單 cluster）」：交叉引用本 spec § 4 之 PSA / Quota 為 baseline
4. ResourceQuota 之 `requests.cpu` / `requests.memory` 表（plan.md line 803-816）：以本 spec § 5.1 為唯一 source；plan.md 之單一 free 範例改為「以 spec 為準」

## 14. Open issues

> 來源：ADR-0004 § 9 之 7 條 OQ 中與 namespace / quota 相關者 + 本 spec 撰寫期間發現

- ADR-0011（plan tier 矩陣）未拍板；本 spec § 5.1 之數值為提案，需 user 拍板後固化
- ImagePullSecret refresh 與 namespace 建立順序在 v1 用 backend 直 apply：v1.1 評估 ArgoCD `sync-wave` 之純 GitOps 路徑
- Per-team egress allowlist：v1.1 範圍；本 spec 預留 NetworkPolicy 結構，但 v1 不暴露 CLI / MCP
- PSA `enforce=restricted` 升級時程：與 managed app 違反率對齊；v2 範圍
- CSI 選擇（local-path / longhorn）：管理 PVC 數量，影響 quota；屬 ADR-0004 OQ#4
- 多節點 K3s 後 NetworkPolicy 行為驗證：v1 single-node OK；M2 後 multi-node 重測
- Cloudflare Tunnel pod 之 namespace 命名（本 spec 假設 `cloudflare-tunnel`）：屬 `winshare-subdomain-and-tunnel` spec
- Namespace label `app.0ops.io/team-id` 之 UUID 格式統一（小寫含連字號 vs raw）：與 DTO 對齊（`shared-dto-and-contract` § 4.2）

## 15. 不可違反的硬性規則

> 違反以下任一項，PR 不可合入。

1. Namespace 命名固定：`system-0ops` 或 `team-<team_slug>`；不得自由命名
2. 每 team **一個** namespace；不採 per-app namespace
3. Namespace 建立必同時就緒：ResourceQuota、LimitRange、NetworkPolicy、PSA label、ImagePullSecret 五者；缺一即視為建立失敗，回滾
4. PSA label 必含 `enforce=baseline / warn=restricted / audit=restricted`；不得只設一個或取消
5. ImagePullSecret 不走 GitOps；由 backend 直 apply；GitOps 內不得出現 `ghcr-pull` Secret manifest
6. ImagePullSecret refresh 30 min 為最大週期；不得設更長週期（GHCR 1h token 留半小時 buffer）
7. M5 後 ImagePullSecret refresh 必僅 leader pod 跑（避免並發 patch 造成 race）
8. ResourceQuota 數值必依 plan tier；不得個別 team 自由覆寫（除非 user 拍板特例，需 audit）
9. NetworkPolicy 預設拒絕跨 team；改放寬必經 ADR + plan.md 同步
10. backend 不得直接 `kubectl apply` 修改 `team-<slug>` 之 application manifest（deployment / service / ingress）；應用層必經 GitOps
