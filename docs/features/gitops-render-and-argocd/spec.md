# Feature Spec：gitops-render-and-argocd

> **狀態**：draft
> **來源**：`docs/0ops-plan.md`「GitOps target」「Build & deploy」「Runtime topology & operability」段；ADR-0004（K3s + GitOps stack）；ADR-0005（GHA → gitops 之 push 路徑）；本 spec 依賴 `preview-confirm-gate`、`shared-dto-and-contract`、`secrets-management`
> **適用範圍**：`0ops-gitops` repo 結構、render template、ArgoCD ApplicationSet、commit message contract、push 衝突 retry 與狀態回報；不含 GitHub Actions workflow 本身（屬 `build-pipeline-and-callback` spec）
> **對應 Milestone**：M2（與 create_app 同步上線；M3 起持續以 add_domain / redeploy 等 action 擴增 manifest 種類）

## 1. 結論（先讀本段）

- `0ops-gitops` 為**獨立 repo**（與主 repo 分離）；ArgoCD 唯一監聽源；backend 為唯一 push 來源
- 目錄結構：`apps/<team_slug>/<app_slug>/` 為 Kustomize base；`argo/applicationset.yaml` 為單一 ApplicationSet 用 git generator 自動發現
- Render 由 backend 端 Go template 完成（**不**用 Helm chart-as-template；chart 留給 backend 自身與 managed-app 標準 chart）
- Render → commit → push 為 `create_app` / `redeploy` / `add_domain` 等 action 之 reversible side_effect（屬 `preview-confirm-gate` § 7.2 之 reversible 階段）
- Push 衝突（多 team 並發）：retry + rebase 最多 5 次；超過進 `compensating`（接續 ADR-0005 第 7 點）
- Commit message 為 machine-parseable contract：`<action>: <team_slug>/<app_slug> @ <deploy_run_id>`；含 trailer `Trace-Id` 與 `Preview-Id`
- Git author 為 backend 服務帳號 `ops-bot <ops-bot@winshare.tw>`；commit signing 用 Ed25519 SSH key（GitHub allowed signers）
- ArgoCD 同步策略：`automated.prune=true / selfHeal=true`；變更從 git push 至 K3s 應用 < 90 秒（ApplicationSet 預設 3 分鐘 reconciliation 間隔以 webhook 縮短）
- 所有 manifest 不含 K3s-specific 資源（接續 ADR-0004 § 4.5）；走 upstream K8s API 確保 v2 遷移可用

## 2. 範圍

### 2.1 包含
- `0ops-gitops` repo 之目錄結構與檔案命名
- `internal/server/services/gitops/` package：clone / pull / render / commit / push 全流程
- Render template 列表（deployment / service / ingress / namespace 規範）
- Commit message 與 trailer 規約
- Push 衝突 retry 策略與 metric
- ArgoCD ApplicationSet manifest 與 sync 行為
- Git 認證（SSH key 或 GitHub App installation token）
- Repo 初始化 runbook

### 2.2 不包含
- GitHub Actions workflow（`deploy-app.yml`）內容（屬 `build-pipeline-and-callback` spec）
- ArgoCD 自身部署（屬 deployment runbook，非 spec）
- Cloudflare Tunnel ingress 設定（屬 `winshare-subdomain-and-tunnel` spec；本 spec 只 render Ingress 物件含 hostname）
- 客戶自有域名 hostname binding（屬 `custom-domain-and-verify` spec；本 spec 只 render `domain_binding` 已 verified 之 hostname）
- K3s namespace ResourceQuota / LimitRange 物件（屬 `k3s-namespace-isolation` spec；本 spec 只 render team namespace 必要的 label）
- 多分支預覽部署（v2）

## 3. 檔案結構

### 3.1 主 repo（`0ops`）

```
0ops/
└── internal/
    └── server/
        └── services/
            └── gitops/
                ├── client.go              # go-git wrapper：clone / fetch / push
                ├── render.go              # text/template 模板執行
                ├── commit.go              # commit message + trailer
                ├── retry.go               # rebase + retry
                ├── templates/             # embed FS：deployment.yaml.tmpl / service.yaml.tmpl / ingress.yaml.tmpl / kustomization.yaml.tmpl
                ├── metrics.go
                └── doc.go
```

### 3.2 `0ops-gitops` repo 結構

```
0ops-gitops/                          # 獨立 repo，與主 repo 分離
├── README.md                         # 「machine-managed; do not hand-edit」
├── apps/
│   └── <team_slug>/
│       └── <app_slug>/
│           ├── deployment.yaml
│           ├── service.yaml
│           ├── ingress.yaml          # 含 winshare 子網域 + 已 verified 客戶域名
│           ├── configmap.yaml        # 可選：env config
│           └── kustomization.yaml
├── teams/
│   └── <team_slug>/
│       ├── namespace.yaml            # 含 PSA label（屬 k3s-namespace-isolation 範圍）
│       └── kustomization.yaml        # 收口同 team 之所有 app
├── argo/
│   ├── applicationset.yaml           # 一份 ApplicationSet，git generator
│   ├── project-default.yaml          # ArgoCD AppProject
│   └── notifications.yaml            # Argo Notifications 規則（Slack / webhook）
└── .github/
    └── CODEOWNERS                    # @ops-bot 為 owner；禁止 hand-edit
```

## 4. Render 行為

### 4.1 觸發點

| Action | render 範圍 |
|---|---|
| `create_app` | 新 `apps/<team>/<app>/{deployment,service,ingress,kustomization}.yaml` + `teams/<team>/kustomization.yaml` 加入新 app |
| `update_app`（PATCH） | 對應 manifest 改欄位（如 `replicas`、`image`、env） |
| `redeploy` | 不 render；只觸發 build → 結束時 backend 把 `image_ref` 更新至 `deployment.yaml`（commit + push）|
| `add_domain` | `ingress.yaml` 增 hostname |
| `remove_domain` | `ingress.yaml` 移 hostname；空後 fallback 至 winshare 子網域 |
| `delete_app` | 移除整個 `apps/<team>/<app>/` 目錄 + `teams/<team>/kustomization.yaml` 對應行；不 render，純 delete + commit |

### 4.2 Template 約束

- 使用 stdlib `text/template`；**不**用 Helm（避免 ArgoCD Helm 模式與 GitOps 真理性衝突）
- Template 之輸入為 `internal/shared/dto.App` + 補強欄位（如 `image_ref`, `team_namespace`）
- 模板輸出必為合法 K8s YAML；render 階段以 `sigs.k8s.io/yaml` 反序列化驗證
- 模板路徑 `internal/server/services/gitops/templates/` 採 `embed.FS`；模板隨 binary 散布

### 4.3 Deployment 模板必含

- `apiVersion: apps/v1`、`kind: Deployment`
- `metadata.namespace: team-<team_slug>`、`metadata.name: <app_slug>`
- `metadata.labels`：`app.0ops.io/slug=<app_slug>`、`app.0ops.io/team=<team_slug>`、`app.0ops.io/managed-by=0ops`
- `spec.replicas`（預設 1）、`strategy.rollingUpdate.maxSurge=1, maxUnavailable=0`
- `spec.template.spec.containers[0]`:
  - `image: ghcr.io/winshare/<team>-<app>:<commit_sha>`
  - `imagePullSecrets: [{name: ghcr-pull}]`（接續 ADR-0004 baseline）
  - resource `requests/limits`（依 plan tier，缺省由 `LimitRange` 套）
  - liveness / readiness probe（依 buildpack 偵測之 `primary_port`）
  - env from `configmap.yaml`

### 4.4 Service 模板必含

- `kind: Service`、`type: ClusterIP`、port 80 → `targetPort: <primary_port>`
- selector: `app.0ops.io/slug=<app_slug>`

### 4.5 Ingress 模板必含

- `kind: Ingress`、`ingressClassName: traefik`（K3s 預設；接續 ADR-0004 § 4.3）
- `rules:` 一條 winshare 子網域（`<app_slug>.winshare.tw`）+ N 條已 verified 客戶域名（從 `domain_binding WHERE verified=true` 取）
- 不持 TLS 段（TLS 在 Cloudflare edge 終止；接續 ADR-0007）

### 4.6 Kustomization 模板

每 app 目錄之 `kustomization.yaml`：
```yaml
apiVersion: kustomize.config.k8s.io/v1beta1
kind: Kustomization
resources:
  - deployment.yaml
  - service.yaml
  - ingress.yaml
  - configmap.yaml          # 條件式：有 env 才列
commonLabels:
  app.0ops.io/slug: <app_slug>
  app.0ops.io/team: <team_slug>
```

每 team 目錄之 `kustomization.yaml`：
```yaml
resources:
  - namespace.yaml
  - <app_slug_1>/
  - <app_slug_2>/
  - ...
```

## 5. Commit / Push 流程

### 5.1 流程

```
1. git clone --depth=50 git@github.com:winshare/0ops-gitops.git /tmp/gitops-<rand>
   或 reuse 本地 cache（每 backend pod 一份；M5 多 replica 各自獨立 cache）
2. 在 worktree 執行 render → 寫檔
3. git add -A
4. git commit -m "<action>: <team>/<app> @ <deploy_run_id>" \
              -m "Preview-Id: <preview_id>" \
              -m "Trace-Id: <trace_id>"
5. git push origin main
   - 衝突（non-fast-forward）→ retry（§ 5.4）
6. 成功 → 寫 deploy_run.events 事件 `gitops_pushed { commit_sha }`
```

### 5.2 Commit message contract

```
create_app: acme-prod/nextdemo @ 01J2K3M4N5P6Q7R8S9T0V1W2X3

Preview-Id: 7f3e2a8b-...
Trace-Id: 0af7651916cd43dd8448eb211c80319c
```

- 第一行：`<action>: <team_slug>/<app_slug> @ <deploy_run_id>`
- 空行
- Trailer 區（kernel 風格）：每行 `Key: value`
- 必含 trailer：`Preview-Id`、`Trace-Id`
- 可選 trailer：`Co-Authored-By`（v2 web UI 使用者署名時）
- machine-parse 規則：`git log --format="%(trailers)"` 可直接取
- 不允許自由文字段（避免 review 噪音 / human-edit 誘惑）

### 5.3 Git author 與簽章

- `user.name = ops-bot`、`user.email = ops-bot@winshare.tw`
- 使用 SSH commit signing：Ed25519 key 對 GitHub allowed signers 已註冊
- key material 存於 K8s Secret `gitops-signing-key`，由 `secrets-management` spec 規範 rotation
- GitHub repo `0ops-gitops` 設 branch protection：required signature、required reviews=0（backend 自簽自合）、no force push

### 5.4 Push 衝突 retry（接續 ADR-0005 § 7）

```
for attempt in 1..5:
    err = git push
    if err == nil: return success
    if err is non-fast-forward:
        git fetch origin main
        git rebase origin/main
        if rebase conflict (file overlap):
            // 同 app 同檔的並發 render；極少見
            return error("gitops_push_conflict")
        // rebase OK → 重試 push
        sleep(50ms × 2^attempt + jitter)
        continue
    return error(other)
return error("gitops_push_conflict")  // 超過 5 次
```

- 同 app 同檔的並發 render 視為衝突，不嘗試 3-way merge（語意不安全）；報錯回 saga 之 `compensating`
- 不同 app 同 team 的並發：rebase 自動成功（不同檔案）

### 5.5 衝突指標

`0ops_gitops_push_attempts_total{outcome}` counter：outcome ∈ `success`/`rebased`/`conflict`/`error`

## 6. ArgoCD ApplicationSet

### 6.1 ApplicationSet manifest

```yaml
apiVersion: argoproj.io/v1alpha1
kind: ApplicationSet
metadata:
  name: 0ops-managed-apps
  namespace: argocd
spec:
  generators:
    - git:
        repoURL: git@github.com:winshare/0ops-gitops.git
        revision: main
        directories:
          - path: apps/*/*
  template:
    metadata:
      name: '{{.path.basename}}-{{.path[1]}}'   # <app_slug>-<team_slug>
    spec:
      project: 0ops-managed
      source:
        repoURL: git@github.com:winshare/0ops-gitops.git
        targetRevision: main
        path: '{{.path.path}}'
      destination:
        server: https://kubernetes.default.svc
        namespace: 'team-{{.path[1]}}'
      syncPolicy:
        automated:
          prune: true
          selfHeal: true
        syncOptions:
          - CreateNamespace=false  # namespace 由 teams/<slug>/namespace.yaml 管理
          - ApplyOutOfSyncOnly=true
```

### 6.2 sync 行為

- ApplicationSet 之 git generator 預設 3 分鐘 reconcile；用 ArgoCD webhook 縮短至 push 後 < 90s 觸發
- `automated.prune=true`：刪 manifest 即刪 K8s 物件（與 `delete_app` 對齊）
- `selfHeal=true`：人為直改 K8s 物件會被 ArgoCD 還原（與 GitOps 真理性對齊）
- 失敗（manifest invalid / K8s API reject / quota 不足）：ArgoCD 端 retry；backend 透過 reconciler 偵測 `argo_sync_timeout` failure_classification

### 6.3 sync status 回報

backend `services/k8sstatus/argo.go` 端透過 ArgoCD API（kube-style CRD 讀 `Application`）查 `application.status.sync.status` 與 `application.status.health.status`：

| Argo state | deploy_run state |
|---|---|
| Synced + Healthy | `live` |
| Synced + Progressing | `syncing` |
| OutOfSync + Progressing | `syncing` |
| Synced + Degraded | `failed` (classification: `health_check_failed`) |
| 任何 sync failed | `failed` (classification: `argo_sync_timeout`) |

具體 reconciler 與狀態機介接屬 `reconciler-and-incident` spec；本 spec 只規範查詢路徑。

## 7. Git 認證

### 7.1 主路徑：SSH deploy key

- backend pod 掛 K8s Secret `gitops-deploy-key`：含 SSH private key（Ed25519）
- 對應 public key 註冊於 `0ops-gitops` repo 為 deploy key（write 權限）
- `~/.ssh/known_hosts` 預埋 GitHub host key（防 MITM；secret 同包含）
- go-git 用 `transport.ssh.NewPublicKeysFromSigner()`

### 7.2 退路：GitHub App installation token

- 若 SSH 不可用（如自架 git server 無 SSH 支援），改用 HTTPS + App installation token
- token 1h TTL，每次 push 前重新取（接續 ADR-0005 § 6.3 ephemeral token 模式）
- v1 主路徑為 SSH；HTTPS 為 fallback

### 7.3 簽章 key

- 與 deploy key **同一把**（簡化）；該 key 同時用於 SSH auth 與 commit signing
- v2 評估分離（auth / signing 不同 key，符合最小權限）

## 8. 與其他 spec 接合點

| 接合 | spec |
|---|---|
| Render 為 reversible side_effect | `preview-confirm-gate` § 7.2 |
| `image_ref = ghcr.io/winshare/<team>-<app>:<commit_sha>` | `build-pipeline-and-callback` spec |
| Ingress hostname 列表 = winshare 子網域 + verified 客戶域名 | `custom-domain-and-verify` spec |
| namespace `team-<slug>` 之 PSA label / Quota | `k3s-namespace-isolation` spec |
| `gitops-deploy-key` rotation | `secrets-management` spec |
| Argo 同步狀態 → `deploy_run` 狀態機 | `reconciler-and-incident` spec |
| trace_id 寫 commit trailer | `observability-skeleton` § 6 |
| `gitops_push_conflict` 失敗碼 | `error-model` § 5.5 |

## 9. Repo 初始化（runbook 摘要）

```
1. GitHub 建 repo winshare/0ops-gitops（private）
2. 初始 commit：README.md + apps/.gitkeep + teams/.gitkeep + argo/applicationset.yaml + .github/CODEOWNERS
3. branch protection：main require signed commit、disallow force push、reviews=0
4. 產 Ed25519 SSH key（一把）；
   - public 加為 repo deploy key（write）
   - private 存 K8s Secret gitops-deploy-key 於 system-0ops namespace
   - public 加為 ops-bot machine user 之 allowed signer
5. ArgoCD 設定：
   - argocd repo add git@github.com:winshare/0ops-gitops.git --ssh-private-key-path=...
   - kubectl apply argo/applicationset.yaml
6. 驗證：手動 push 一個 dummy app/test-team/hello/，10 分鐘內 ArgoCD 應 sync 至 cluster
```

## 10. 驗證準則

| 驗證項 | 方式 | 通過條件 |
|---|---|---|
| Template render 合法 YAML | unit test 對 deployment/service/ingress/kustomization 模板輸出做 yaml unmarshal | 全部解析通過 |
| Template 不含 K3s-specific 欄位 | grep `HelmChart`、`traefik.io/`、`local-path` 於模板輸出 | 無命中 |
| Commit message contract | unit test 解析 `git log --format=%(trailers)` | 含 `Preview-Id` + `Trace-Id` |
| Commit signing | `git verify-commit HEAD` 對 backend push 之 commit | Good signature |
| Push 衝突 retry | mock GitHub server 回 non-fast-forward 3 次後成功 | 第 4 次成功；attempts metric `outcome=rebased` 計數 3 |
| Push 5 次仍衝突 | mock 持續衝突 | error `gitops_push_conflict`；deploy_run 進 compensating |
| ApplicationSet 自動發現 | git push 新 `apps/test-team/test-app/` | ArgoCD < 90s 出現對應 Application |
| Sync 失敗回報 | render manifest 含 invalid resource | ArgoCD `OutOfSync + Failed`；backend reconciler 標 `argo_sync_timeout` |
| Selfheal | 人為 `kubectl edit deployment` 改 image | ArgoCD 還原至 git 版本 |
| Prune | 從 `apps/x/y/` 移除 deployment.yaml 並 push | ArgoCD 刪除對應 Deployment |
| Deploy key 限定 0ops-gitops | 嘗試用同一把 key 對其他 repo push | 失敗（GitHub deploy key 為 repo-scoped） |

## 11. SLI 對應

| SLI | 目標 | 量測 |
|---|---|---|
| Render → push 成功率 | > 99% | `0ops_gitops_push_attempts_total{outcome=success or rebased} / total` |
| Push attempt 平均次數 | < 1.2 / push | `gitops_push_attempts_total / gitops_pushes_total` |
| Push → ArgoCD sync 觸發延遲 | p95 < 90s | webhook 觸發至 Application status 變動之時間 |
| Sync 完成（健康）延遲 | p95 < 5 min | `deploy_run.events` 中 `gitops_pushed → live` 之時間差 |

## 12. 對 `docs/0ops-plan.md` 的修改清單

1. 「GitOps target」段：交叉引用本 spec；補入 `teams/<team_slug>/` 目錄與 `CODEOWNERS`
2. 「Build & deploy」段（與 `build-pipeline-and-callback` spec 接合）：明確 backend 端 render 之分工（GHA 不 render，只 push image）
3. 「ApplicationSet」描述：補入 git generator + git webhook 縮短 reconcile
4. 「Risks & open #5（GitOps repo 衝突）」：交叉引用本 spec § 5.4 之 retry 策略

## 13. Open issues

- 是否需要 PR-based GitOps（backend 開 PR 而非直 push 至 main）：v1 直 push（速度 + 自動化）；v1.1 評估 PR + auto-merge 以利 audit 透明度
- 多 backend pod 的 worktree cache 共享：v1 各自獨立（簡單）；M5 多 replica 後若 push 競爭升高，評估 leader-only push（接續 backend-ha-leader-election spec）
- ApplicationSet 若 git provider 不支援 webhook（自架 git）：reconcile 間隔 3 分鐘 fallback；本 spec 預設 GitHub
- Commit signing key rotation：本 spec 採同一把 key；rotation 期間需雙 key 並存（屬 `secrets-management` spec）
- 1000+ apps 後 ApplicationSet git generator 性能：ArgoCD 預設掃所有目錄；達閾值需評估 sharded ApplicationSet
- 是否補 dry-run validate（kubeval / kubeconform）於 push 前：v1 暫不（render 階段已 yaml unmarshal 驗）；v1.1 評估
- v2 Web UI 對 GitOps 是否提供 raw diff 預覽：屬 v2 範圍

## 14. 不可違反的硬性規則

> 違反以下任一項，PR 不可合入。

1. `0ops-gitops` 為**唯一** ArgoCD 監聽源；不得新增第二個 GitOps repo
2. backend 為**唯一** push 來源；hand-edit `0ops-gitops` 為違反操作（CODEOWNERS + branch protection 雙重攔阻）
3. Render 模板輸出不得含 K3s-specific 資源（`HelmChart` CRD、traefik 私有欄位、local-path PVC）
4. Commit message 第一行格式固定 `<action>: <team_slug>/<app_slug> @ <deploy_run_id>`；trailer 必含 `Preview-Id` + `Trace-Id`
5. 所有 push 必經 SSH key signing；GitHub 端強制 required signature
6. Push retry 上限 5 次；超過必進 `compensating` 狀態，不可放任無限重試
7. ApplicationSet 之 `automated.prune=true / selfHeal=true` 不可關閉（GitOps 真理性硬要求）
8. Render 為 reversible side_effect；compensate 為「revert commit」（git revert + push）；不得就地 force-push 改寫歷史
9. backend 不得直接呼 `kubectl apply` 至 K3s；所有 manifest 變動必經 `0ops-gitops` push → ArgoCD sync
10. `team-<slug>` namespace 物件不得由本 spec render（屬 `k3s-namespace-isolation` spec）；本 spec 只負責 app-level manifest
