# Feature Spec：create-app-flow

> **狀態**：draft
> **來源**：`docs/0ops-plan.md`「使用者腳本範例 Pattern A/B」「Tool catalog」「Deploy 狀態機」段；ADR-0002（preview-confirm + saga）；本 spec 為**整合 spec**，依賴 `preview-confirm-gate`、`gitops-render-and-argocd`、`k3s-namespace-isolation`、`build-pipeline-and-callback`、`winshare-subdomain-and-tunnel`、`secrets-management`、`shared-dto-and-contract`、`error-model`、`auth-and-rbac`
> **適用範圍**：`create_app` action 之端到端流程；含 args schema、preview side_effects 計算、execute 階段順序、deploy_run state machine 對應、失敗矩陣
> **對應 Milestone**：M2（vertical slice 完整通；M2 完成標準的核心 demo）

## 1. 結論（先讀本段）

- `create_app` 為 0ops v1 最重要的整合 action；走 `preview-confirm-gate` 通用 framework，個別實作 `Action` 介面三函式（`SideEffects` / `Precheck` / `Execute`）於 `internal/server/services/createapp/`
- args 為 `{slug, repo_url, ref, builder?}`；`slug` 須符合 `^[a-z0-9][a-z0-9-]{1,30}[a-z0-9]$`；不得與 team 內既有 app 重複
- preview side_effects 5 項（見 § 5）：`inspect_repo` 為純讀（不算 side_effect）；execute 階段才產生實際副作用
- deploy_run state machine：`queued → preparing → building → pushing → rendering → syncing → live`；任一失敗轉 `compensating → rolled_back` 或 `failed`
- 副作用順序（reversible → irreversible）：
  - **Reversible**：domain_binding INSERT、deploy_run INSERT、ops_token 簽發、`team-<slug>` namespace 與 `ghcr-pull` 之首次建立、0ops-gitops branch + render
  - **Irreversible**：GHA workflow_dispatch 觸發後（image push GHCR、Cloudflare Tunnel hostname binding 已隱含於 wildcard）
- `last_result` 含 `app_id`, `app_slug`, `deploy_run_id`, `trace_id`, `subdomain_url`；client 可直接用 `subdomain_url` 觀察上線
- 整體目標：preview 至 live < 10 分鐘（plan SLO p50）；至少 4 分鐘為 build 主導
- 首次 create_app（team 無 GitHub App install 或無對該 repo 之 access）：preview 階段即 fail，回 `github_app_not_installed` / `github_repo_not_accessible`，引導使用者跑 `0ops teams github install`

## 2. 範圍

### 2.1 包含
- `create_app` action 之 args schema、validation、`Action` 介面三函式實作
- preview 階段之 side_effects 計算與 stack 偵測（呼叫 `inspect_repo` 子流程）
- confirm 階段之副作用順序與失敗處理
- 與 deploy_run state machine 之對應（每個 stage 之語意）
- 跨 5 份 batch 3 spec 之介接點
- 觀測：每個 stage 之 latency / success metric
- CLI / MCP 之輸出格式對應

### 2.2 不包含
- preview-confirm gate 通用機制本身（屬 `preview-confirm-gate` spec）
- inspect_repo 端點本身（屬 `read-api-vertical-slice` § 4.3）
- GHA workflow YAML 本身（屬 `build-pipeline-and-callback` spec）
- gitops render template 本身（屬 `gitops-render-and-argocd` spec）
- namespace 建立物件本身（屬 `k3s-namespace-isolation` spec）
- Cloudflare API call 本身（屬 `winshare-subdomain-and-tunnel` spec）
- callback HMAC 驗章本身（屬 `build-pipeline-and-callback` spec）
- redeploy 與 update_app 行為（自有 spec；redeploy 屬 `webhook-and-redeploy`）
- delete_app（屬 `delete-app-flow` spec）

## 3. 檔案結構

```
0ops/
└── internal/
    └── server/
        └── services/
            └── createapp/
                ├── action.go              # 實作 preview.Action 介面
                ├── args.go                # AppCreateArgs 與 validation
                ├── side_effects.go        # 計算 5 項 side_effects
                ├── precheck.go            # confirm tx 內重檢
                ├── execute.go             # 副作用執行 orchestration
                ├── state_machine.go       # deploy_run stage 轉移
                ├── metrics.go
                └── doc.go
```

## 4. Args schema

### 4.1 DTO（補入 `shared-dto-and-contract` 之 dto/apps.go）

```go
// internal/shared/dto/apps.go

type AppCreateArgs struct {
    Slug    string   `json:"slug"`
    RepoURL string   `json:"repo_url"`
    Ref     string   `json:"ref"`              // branch / tag；預設 default_branch
    Builder *string  `json:"builder,omitempty"` // 可選；缺則用 inspect_repo 偵測之 paketo builder
}

type AppCreateLastResult struct {
    AppID         string `json:"app_id"`
    AppSlug       string `json:"app_slug"`
    DeployRunID   string `json:"deploy_run_id"`
    TraceID       string `json:"trace_id"`
    SubdomainURL  string `json:"subdomain_url"`     // https://<slug>.winshare.tw
    InitialDeploy bool   `json:"initial_deploy"`    // create_app 為 true；redeploy / update 為 false
}
```

### 4.2 Validation

- `slug`：`^[a-z0-9][a-z0-9-]{1,30}[a-z0-9]$`；長度 3..32；保留字 `system`、`api`、`auth`、`v1`、`me` 不得使用
- `repo_url`：必為 `https://github.com/<owner>/<repo>` 或 `git@github.com:<owner>/<repo>.git`；regex 校驗
- `ref`：可省略；省略時 `inspect_repo` 結果之 `default_branch`
- `builder`：可省略；省略時 inspect_repo 偵測；指定值必為 paketo allowlist（`paketobuildpacks/builder-*`）

驗證失敗 → `422 validation_failed`，details 含 fields 列表。

## 5. Preview 階段：`SideEffects()` 計算

### 5.1 流程

```
1. validate args (§ 4.2)
2. SELECT team WHERE slug = ctx.team_slug
   - team.github_install_id IS NULL → 422 github_app_not_installed
3. SELECT app WHERE team_id = ? AND slug = args.slug
   - 命中 → 422 slug_taken
4. 呼 inspect_repo（內部 function call，不呼自家 endpoint）：
   - GitHub App installation token 取得
   - 對 repo_url + ref 做 metadata 取得（commit_sha, default_branch）
   - paketo 靜態掃描（package.json / pyproject.toml / go.mod / ...）
   - 結果含：commit_sha, default_branch, builder, primary_port, github_app_status
5. 若 github_app_status = installed_no_access → 403 github_repo_not_accessible
6. 若 buildpack.detected_languages 為空 → 422 buildpack_detect_failed
7. 計算 5 項 side_effects（§ 5.2）
8. 計算 action_summary（§ 5.3）
9. 回 PlanPreview
```

### 5.2 5 項 side_effects

| Effect | Reversible | Description | Resource |
|---|---|---|---|
| 1. 在 0ops-gitops 建 `apps/<team>/<slug>/` 並 push | true | `Render and push manifest to 0ops-gitops` | `gitops:apps/<team>/<slug>` |
| 2. 在 K3s 確保 `team-<slug>` namespace 與 `ghcr-pull` 就緒（首次 create_app 時） | true | `Provision K8s namespace and image pull secret` | `k8s:team-<slug>` |
| 3. INSERT app row + domain_binding(`<slug>.winshare.tw`, primary) | true | `Persist app record and primary subdomain binding` | `db:app/<slug>` |
| 4. 觸發 GitHub Actions workflow_dispatch（含簽 ops_token） | **false** | `Trigger GitHub Actions build (image push to GHCR is irreversible)` | `gha:workflow-run` |
| 5. ArgoCD 同步至 K3s 上線 | true（manifest revert）/ false（pod 啟動已副作用） | `ArgoCD syncs manifest to K3s` | `argocd:application` |

> Effect 4 為 saga 邊界：之前所有為 reversible，之後（含其本身）為 irreversible。GHA workflow 本身可 cancel，但一旦進入 `pack build --publish` 即 image push 不可 undo。
> Effect 5 之 manifest 可從 gitops revert（reversible），但 K8s pod 啟動產生之外部副作用（如連外 DB 寫入）不可 undo——本 spec 採「manifest revert 即 reversible」的窄定義；運維責任由使用者承擔。

### 5.3 action_summary 範本

```
建立 app `<slug>`（@ team `<team_slug>`）
  Repo: <repo_url> @ <ref> (commit <commit_sha[:7]>)
  Stack: <builder> (port <primary_port>)
  Subdomain: <slug>.winshare.tw
```

## 6. Confirm 階段：`Precheck` + `Execute`

### 6.1 Precheck（tx 內重檢）

於 `preview-confirm-gate` § 6.2 之 `SELECT FOR UPDATE` 之後執行：

```
1. SELECT team FOR SHARE → team 仍存在 + 未 archived + github_install_id 仍存在
2. SELECT app WHERE team_id = ? AND slug = ?
   - 命中 → 409 precondition_changed (slug_taken)
3. 驗 actor_role >= member、token 仍有 apps:write scope
4. 驗 ops_token signing secret 可用（§ secrets-management 之 OPS_TOKEN_SIGNING）
```

任一失敗 → 回對應 4xx；preview 仍標 consumed + last_result 含 error envelope。

### 6.2 Execute（副作用執行）

接續 `preview-confirm-gate` § 7 之 reversible-first 順序：

#### Reversible 階段

```go
// pseudo
func executeReversible(ctx context.Context, args AppCreateArgs) error {
    // R1: INSERT app + domain_binding（同 tx，與 preview consume 共用 tx 之外的單獨 tx）
    appID, err := db.InsertApp(...)
    if err != nil { return wrap(err, "db_insert") }

    // R2: 確保 K3s namespace + ghcr-pull（若 team 首次 create_app）
    if isFirstAppForTeam(ctx, teamID) {
        if err := k3s.EnsureNamespace(ctx, teamSlug); err != nil { return wrap(err, "k8s_namespace") }
        if err := k3s.EnsureGhcrPull(ctx, teamSlug); err != nil { return wrap(err, "k8s_ghcr_pull") }
    }

    // R3: 0ops-gitops render + commit + push
    deployRunID := uuid.New().String()
    if err := db.InsertDeployRun(deployRunID, appID, /*status=*/"queued"); err != nil {...}
    advanceDeployRun(deployRunID, "preparing")

    if err := gitops.RenderAndPush(ctx, GitOpsArgs{
        Team: teamSlug, App: args.Slug,
        ImageRef: imageRefFor(teamSlug, args.Slug, commitSHA),
        DeployRunID: deployRunID, TraceID: traceID,
        ApprovalCommit: previewID,
    }); err != nil {
        return wrap(err, "gitops_push")
    }

    return nil
}
```

#### Irreversible 階段

```go
func executeIrreversible(ctx context.Context, args AppCreateArgs) error {
    // I1: 簽 ops_token（HMAC; 20 min TTL；綁 deploy_run_id）
    opsToken := workflowdispatch.IssueOpsToken(deployRunID, traceID)

    // I2: 觸發 GitHub Actions workflow_dispatch
    advanceDeployRun(deployRunID, "building")
    if err := workflowdispatch.Dispatch(ctx, ClientPayload{
        RunID: deployRunID, AppSlug: args.Slug, TeamSlug: teamSlug,
        CommitSHA: commitSHA, Ref: args.Ref,
        ImageRef: imageRefFor(...),
        OpsToken: opsToken,
        CallbackURL: callbackURL(deployRunID),
        TraceID: traceID,
    }); err != nil {
        return wrap(err, "gha_dispatch")
    }

    // I3 之後皆為 callback-driven 推進；execute 不等 build 完成
    return nil
}
```

#### Compensate（reversible 階段任一失敗）

```go
func compensate(ctx context.Context, completedReversible []string) {
    // 反向順序 undo
    for i := len(completedReversible) - 1; i >= 0; i-- {
        switch completedReversible[i] {
        case "gitops_push":
            gitops.RevertCommit(ctx, deployRunID)   // git revert + push
        case "k8s_ghcr_pull":
            // 不 undo（可能其他 app 也用；leave alone）
        case "k8s_namespace":
            // 不 undo（同上）
        case "db_insert":
            db.DeleteApp(appID)
            db.DeleteDomainBinding(...)
        }
    }
    advanceDeployRun(deployRunID, "rolled_back")
}
```

> 注意：`k8s_namespace` 與 `k8s_ghcr_pull` 之 compensate 為 no-op；理由：同 team 之其他 app 可能依賴；殘留無害。Reconciler 會在 team 無 app 時擇期清理（屬 v1.1）。

### 6.3 Last_result 寫入

execute 成功（reversible + irreversible 全通）時：

```go
result := AppCreateLastResult{
    AppID:        appID,
    AppSlug:      args.Slug,
    DeployRunID:  deployRunID,
    TraceID:      traceID,
    SubdomainURL: fmt.Sprintf("https://%s.winshare.tw", args.Slug),
    InitialDeploy: true,
}
return result, nil
```

回給 client：HTTP 200 + `{idempotent_replay: false, data: <result>}`（與 `preview-confirm-gate` § 6.3 一致）

> **注意**：execute 在 `building` 階段結束就回 client；後續 `pushing → rendering → syncing → live` 由 GHA callback + ArgoCD reconciler 推進。client 可透過 `0ops deploys status <slug>` 或 `tail_logs` 觀察。

## 7. Deploy_run state machine

### 7.1 完整轉移圖

```
                 +-----------+
                 |  queued   |
                 +-----+-----+
                       | execute() called
                       v
                 +-----------+
                 | preparing |   ← gitops branch + render（reversible）
                 +-----+-----+
                       | OK
                       v
                 +-----------+
                 | building  |   ← GHA dispatched；callback 為下一步觸發
                 +-----+-----+
                       | callback (success)
                       v
                 +-----------+
                 |  pushing  |   ← image 已 push GHCR；callback payload 含 image
                 +-----+-----+
                       | (auto)
                       v
                 +-----------+
                 | rendering |   ← gitops 內 deployment.yaml image_ref 更新 commit
                 +-----+-----+
                       | OK
                       v
                 +-----------+
                 |  syncing  |   ← ArgoCD detect git change + sync
                 +-----+-----+
                       | argocd Healthy
                       v
                 +-----------+
                 |   live    |
                 +-----------+

任一失敗:
  preparing/building 之前 → compensating → rolled_back（reversible undo）
  building 之後失敗 → failed（含 failure_classification）
```

> `pushing → rendering` 在 v1 為「同一個 callback」中的 backend 端內部 transition：callback 帶 `image_ref` 時 backend 立即進 rendering（更新 deployment.yaml 之 image tag），push 0ops-gitops。

### 7.2 各 stage 推進來源

| Stage | 推進者 | 觸發條件 |
|---|---|---|
| queued | execute() 進入時 | `INSERT deploy_run` |
| preparing | execute() 開始 reversible | `gitops branch open` 之前 |
| building | execute() irreversible 結束 | GHA dispatched 成功 |
| pushing | callback handler | callback `status=success` + `image` 欄位填 |
| rendering | callback handler | image push 確認後 backend 端 render image_ref → push gitops |
| syncing | reconciler / ArgoCD watcher | 偵測到 `Application.status.sync.status=Synced` |
| live | reconciler | 偵測到 `Application.status.health.status=Healthy` |
| failed | callback / reconciler | callback `status=failure` 或 stage timeout |
| compensating | execute() reversible 失敗 | 進入 reversible undo |
| rolled_back | execute() compensate 完成 | undo 全部完成 |

## 8. CLI / MCP 對應

### 8.1 CLI

```
$ 0ops apps create nextdemo \
    --repo=https://github.com/vercel/next.js-helloworld \
    --ref=main

正在偵測 stack...
偵測結果：paketo Node.js builder, port 3000

即將執行：建立 app `nextdemo`（@ team `acme-prod`）
  Repo: https://github.com/vercel/next.js-helloworld @ main (commit abc1234)
  Stack: paketobuildpacks/builder-jammy-base (port 3000)
  Subdomain: nextdemo.winshare.tw
副作用：
  1. Render and push manifest to 0ops-gitops（reversible）
  2. Provision K8s namespace and image pull secret（reversible）
  3. Persist app record and primary subdomain binding（reversible）
  4. Trigger GitHub Actions build（irreversible：image push to GHCR）
  5. ArgoCD syncs manifest to K3s（reversible）

preview 將於 10 分鐘後過期。

確認執行? [y/N] y

✓ deploy-run #abc123 已觸發（預計 4–6 分鐘）
  trace_id: 0af7651916cd43dd8448eb211c80319c
  subdomain: https://nextdemo.winshare.tw

觀察進度：
  0ops deploys status nextdemo
  0ops deploys logs nextdemo --follow
```

### 8.2 MCP

對應 `create_app_preview` + `create_app` 兩 tool。tool description 由 `mcp-tool-description-lint` § 4.1 / § 4.2 強制句式。

`create_app` tool 之 `IsError=false` 回傳 `Content[0].text` 為：
```json
{
  "idempotent_replay": false,
  "data": {
    "app_id": "...",
    "app_slug": "nextdemo",
    "deploy_run_id": "...",
    "trace_id": "...",
    "subdomain_url": "https://nextdemo.winshare.tw",
    "initial_deploy": true
  }
}
```

LLM 自行呈現給 user 並提示「可以追蹤 deploy_run_id」。

## 9. 失敗矩陣

| 失敗點 | 階段 | 處置 | failure_classification |
|---|---|---|---|
| GitHub App not installed | preview | 422 | `github_app_not_installed`（不入 deploy_run）|
| GitHub repo no access | preview | 403 | `github_repo_not_accessible`（不入 deploy_run）|
| buildpack 偵測失敗 | preview | 422 | `buildpack_detect_failed`（不入 deploy_run）|
| slug 已存在 | preview / precheck | 409 | `slug_taken` |
| Token 失效 / role 降級 | precheck | 401 / 403 | （不入 deploy_run）|
| INSERT app 失敗 | reversible R1 | 503 | compensate；preview last_result 含 error |
| K3s namespace ensure 失敗 | reversible R2 | 503 | compensate |
| GitOps push 衝突 5 次 | reversible R3 | 503 | compensate；`gitops_push_conflict` |
| GHA dispatch 失敗 | irreversible I2 | 503 | failed；`gha_dispatch_failed` |
| Build 失敗（callback) | building | callback 推 failed | 各 sub-class（plan §「Deploy 狀態機」列舉）|
| Build timeout | building | reconciler 推 failed | `build_timeout` |
| GitOps push 失敗（rendering）| rendering | reconciler 推 failed | `gitops_push_conflict` |
| ArgoCD sync timeout | syncing | reconciler 推 failed | `argo_sync_timeout` |
| Health check failed | syncing → live | reconciler 推 failed | `health_check_failed` |

## 10. SLI 對應

| SLI | 目標 | 量測 |
|---|---|---|
| create_app preview p95 | < 800ms（含 inspect_repo cache hit）| `0ops_http_request_duration_seconds{route="*:preview", action="create_app"}` |
| create_app preview p95（cache miss）| < 1500ms | 同上 |
| create_app confirm（execute 結束至 client 收回應）| p95 < 5s | 同 route，action="create_app" |
| Build success rate（含 create_app + redeploy）| > 85% / 28d | `deploy_run.status=live / total` |
| Deploy lead time（execute → live）p50 | < 10 min | `live - created_at` p50 |
| Deploy lead time p95 | < 15 min | 同上 |
| Compensate rate | < 5% | `deploy_run.status=rolled_back / total` |

## 11. 與其他 spec 接合點（總覽）

| 接合 | spec | 說明 |
|---|---|---|
| Preview / Confirm 框架 | `preview-confirm-gate` | Action 介面三函式 |
| inspect_repo（preview 內呼叫）| `read-api-vertical-slice` § 4.3 | 共用 5 分鐘快取 |
| Validation / DTO | `shared-dto-and-contract` | AppCreateArgs |
| 4xx codes | `error-model` § 5 | preview / precheck / execute 各失敗碼 |
| Auth / role / scope | `auth-and-rbac` § 6 | member 起跳，apps:write |
| Render & push | `gitops-render-and-argocd` | R3 step |
| Namespace / Quota / ghcr-pull | `k3s-namespace-isolation` | R2 step |
| GHA dispatch + callback | `build-pipeline-and-callback` | I1 / I2 step + callback handler |
| Subdomain（wildcard）| `winshare-subdomain-and-tunnel` | R1 之 domain_binding |
| ops_token signing | `secrets-management` § 4 | I1 step |
| Audit log 寫入 | `audit-log` spec | 每 stage transition |
| Reconciler 收斂滯留 | `reconciler-and-incident` spec | building > 30min, syncing > 15min |
| trace_id 跨界 | `observability-skeleton` § 6.4 | 從 CLI / MCP 進入到 audit_log 全鏈路 |

## 12. 驗證準則

| 驗證項 | 方式 | 通過條件 |
|---|---|---|
| Slug validation | 各種非法 slug | 422 + details fields |
| Slug 保留字拒絕 | `slug=system` / `me` | 422 |
| Repo URL 格式 | 非 GitHub URL | 422 |
| GitHub App 未綁 | mock team 無 install | 422 github_app_not_installed |
| Repo 無 access | mock installation 不含該 repo | 403 github_repo_not_accessible |
| Buildpack 偵測失敗 | mock empty result | 422 buildpack_detect_failed |
| Slug 重複（preview）| 已存在 slug | 422 slug_taken |
| Slug 重複（precheck race）| preview OK，confirm 前另人建同 slug | 409 precondition_changed |
| Reversible 全通 | mock 全 OK | last_result 正常；deploy_run 進 building |
| GitOps push 失敗 | mock 5 次衝突 | compensate；undo R1+R2；rolled_back |
| GHA dispatch 失敗 | mock 503 | failed；`gha_dispatch_failed` |
| Callback success | mock GHA HMAC OK | deploy_run 進 pushing → rendering → syncing → live |
| End-to-end happy path | 真連 staging（GitHub App + Cloudflare） | < 10 分鐘 nextdemo.winshare.tw 回 200 |
| Preview replay | 同 preview_id confirm 兩次 | 第二次 idempotent_replay=true，副作用未重做 |
| MCP create_app description lint | `0ops-mcp` 啟動 | description 含 ALWAYS / NEVER clause |
| trace_id 端到端 | 從 CLI 一個 create_app | audit_log 5 條都同一 trace_id；GHA log 含同 trace_id；callback payload trace_id 一致 |

## 13. 對 `docs/0ops-plan.md` 的修改清單

1. 「使用者腳本範例 Pattern A」段：交叉引用本 spec § 8.1 為 CLI 互動 source；plan 範例保留為示意
2. 「Tool catalog」之 `create_app` 行：交叉引用本 spec
3. 「Deploy 狀態機」段：補入「`pushing → rendering` 為 backend 端內部 transition（callback 觸發）」
4. 「Goals (v1)」之「`5 分鐘部署完成`」：以本 spec § 10 之 SLI 為精確 target（p50 < 10 min；5 分鐘為 happy path 觀察值）
5. plan tier 對 `create_app` 之配額（每 team 最大 app 數）：屬 ADR-0011 範圍

## 14. Open issues

- 「team 首次 create_app」之偵測：v1 採「count(app WHERE team_id) == 0」判；race 場景兩個並發首 app 可能各自 ensure namespace（K8s `kubectl apply` 為 idempotent，安全）
- inspect_repo 之 5 分鐘快取在 user 短時間內推 commit 之 stale：v1 不主動 invalidate；user 須等 5 分鐘或重新 inspect
- preview 階段之 `inspect_repo` 是否計入 preview latency SLO（800ms p95）：本 spec 採「計入」；inspect_repo cache miss 可能撞 1500ms p95，需 dashboard 區分 hit/miss
- 「render → push」之失敗（v1 reversible R3）vs「callback 後 rendering」之失敗（callback 觸發）：兩者皆使用 gitops 但失敗階段分類不同；本 spec 第 9 章已分；plan.md 應補
- GHA workflow 對「使用者已合併但尚未推上 GHCR」的 retry：v1 GHA 預設不 retry；reconciler polling 偵測後決定 retry（屬 `reconciler-and-incident`）
- `personal-{login}` team 首次 create_app 與 GitHub App install：personal team 預設無 install；user 須先跑 `0ops teams github install`，本 spec preview 即提示
- 5 個 side_effects 在 preview 顯示順序：本 spec § 5.2 之 1..5 為展示順序（與執行順序一致）
- 大型 monorepo 之 `pack build` 時間 > 20 min 之處理：v1 採 timeout failed；v1.1 評估自選 builder + cache 預熱

## 15. 不可違反的硬性規則

> 違反以下任一項，PR 不可合入。

1. `create_app` 必經 `preview-confirm-gate` 通用 framework；不得另寫 confirm endpoint
2. preview 階段不得有副作用（不得寫 DB / 不得呼 GitHub API 改狀態 / 不得呼 Cloudflare API）；inspect_repo 為純讀
3. 副作用順序固定 reversible → irreversible；GHA dispatch 後即進 irreversible，前面任一失敗必走 compensate
4. last_result 必含 `subdomain_url`；CLI / MCP 由此呈現給 user
5. trace_id 必貫穿：preview → confirm → DB 寫入 → gitops commit → GHA dispatch → callback → audit_log；任一段缺即 propagation bug
6. `slug` validation regex 與保留字列表為硬性；變更需 ADR
7. team 無 GitHub App install 時 `create_app` 必於 preview 即 fail（不得進 confirm 才 fail）
8. compensate 必反向順序執行；不得平行
9. failure_classification 必填於任何 `failed` / `rolled_back` 之 deploy_run；`unknown` 視為 bug
10. `<slug>.winshare.tw` 為 reserved primary domain_binding；不得個別實作改為其他子網域
