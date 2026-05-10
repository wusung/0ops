# Feature Spec：delete-app-flow

> **狀態**：draft
> **來源**：`docs/0ops-plan.md` Tool catalog `delete_app`、Deploy 狀態機；ADR-0001（admin role 起跳）；ADR-0002（preview-confirm + reversible-first）；本 spec 依賴 `preview-confirm-gate`、`gitops-render-and-argocd`、`k3s-namespace-isolation`、`winshare-subdomain-and-tunnel`、`custom-domain-and-verify`、`error-model`、`audit-log`
> **適用範圍**：`delete_app` action 之端到端流程；含資源清理順序、cascade 規則、runbook
> **對應 Milestone**：M5

## 1. 結論（先讀本段）

- `delete_app` 為**不可逆操作**；最低 role = `admin`；scope = `apps:delete`
- args = `{slug, force?}`；`force` 為 v1.1 評估，v1 不支援
- preview side_effects 5 項：app row delete、domain_binding delete（含 custom hostname unbind）、gitops manifest 移除、ArgoCD prune K8s 資源、reconciler 清理 in-flight deploy_run
- 副作用順序（reversible → irreversible）：先處理可 revert 之 manifest 操作；K8s 資源 prune 後**不可** undo
- 一旦 ArgoCD prune 觸發即視為 irreversible（pod 刪除、PVC 刪除[含資料]）
- Compensate 階段失敗：標 `cleanup_residue` reconciliation_job；audit + 通知 ops 介入
- 不刪 namespace（team 仍可有其他 app）；team 之最後一個 app 刪除後 namespace 留存（接續 `k3s-namespace-isolation` § 9.3）
- Custom domain 立即 unbind（不適用 7 天 grace；屬 user 主動）
- Runbook：刪除前 backend 端不暴露「資料完全保留」承諾；user 自行 backup PVC（v1 預設 PVC `Retain` reclaim policy；v1.1 評估）
- audit_log 完整記錄；CLI 預設要求二次確認（即使 `--yes`，仍印警示）

## 2. 範圍

### 2.1 包含
- `delete_app` Action 實作（preview/confirm）
- 資源清理順序與 K8s prune 行為
- In-flight deploy_run 處理（cancel ongoing builds）
- Custom domain 立即解除
- Cleanup residue 之 reconciliation_job
- CLI / MCP 互動規範（強警示 + 二次確認）
- Audit log 規約

### 2.2 不包含
- Team archive / delete（屬 v1.1 / v2）
- PVC reclaim policy 與資料保留（v1 用 K8s 預設；屬 ops runbook）
- Database row hard delete vs soft delete（本 spec 採 hard delete + audit_log 留索引）
- Webhook event `repository.deleted`（v1 不處理）

## 3. 檔案結構

```
0ops/
└── internal/
    └── server/
        └── services/
            └── deleteapp/
                ├── action.go              # preview.Action 實作
                ├── side_effects.go
                ├── execute.go             # orchestration（reversible → irreversible）
                ├── cleanup.go             # 殘餘清理 reconciler
                ├── metrics.go
                └── doc.go
```

## 4. Args & Preview

### 4.1 Args

```go
type AppDeleteArgs struct {
    AppSlug string `json:"app_slug"`
    Confirm string `json:"confirm,omitempty"`   // 必須等於 app_slug，作為「打字確認」防誤刪（CLI 端強制；MCP 端由 LLM 引導）
}
```

### 4.2 Preview SideEffects

```
1. validate args.app_slug
   - args.Confirm != args.AppSlug → 422 confirm_mismatch
2. SELECT app + 驗 status
   - app.status = 'deleting' → 422 already_deleting
3. 計算 5 項 side_effects（§ 4.3）
4. action_summary：「刪除 app `<slug>`（@ team `<team>`）— 此操作無法 undo」
```

### 4.3 5 項 side_effects

| # | Effect | Reversible | Description |
|---|---|---|---|
| 1 | Cancel any in-flight deploy_run（若有） | true | `Cancel any in-flight builds for this app` |
| 2 | Unbind custom hostnames | true（manifest revert） | `Unbind N custom hostnames from Cloudflare` |
| 3 | DELETE domain_binding rows（primary + extras） | true | `Remove domain bindings from DB` |
| 4 | Delete gitops manifest 目錄 + push | true（git revert） | `Remove app manifest from 0ops-gitops` |
| 5 | ArgoCD prune K8s resources（Deployment / Service / Ingress / PVC） | **false** | `ArgoCD will prune Kubernetes resources (irreversible)` |

> Effect 5 之**不可逆**性：pod 與 PVC 刪除即資料消失（除非 PVC 設 `Retain` reclaim policy；v1 不保證）；user 必須自行 backup

## 5. Confirm / Execute

### 5.1 Precheck（tx 內重檢）

```
1. SELECT app FOR UPDATE
   - app.status = 'deleting' → 409 precondition_changed
2. 驗 actor_role >= admin、scope = apps:delete
3. 驗 token 仍有效
```

### 5.2 Execute（reversible → irreversible）

#### Reversible 階段

```go
func executeReversible(ctx context.Context, args AppDeleteArgs, app *db.App) error {
    // R1: cancel in-flight deploy_run
    inflight, _ := db.SelectDeployRunsInProgress(app.ID)
    for _, dr := range inflight {
        // 嘗試 cancel GHA workflow_run（best effort）
        github.CancelWorkflowRun(dr.WorkflowRunID)
        // mark deploy_run cancelled
        db.UpdateDeployRun(dr.ID, "cancelled", "delete_app_initiated")
    }

    // R2: unbind custom hostnames
    bindings, _ := db.SelectDomainBindings(app.ID)
    for _, b := range bindings {
        if b.Kind == "extra" {
            cloudflare.DeleteCustomHostname(b.CFHostnameID)
        }
        // primary winshare 子網域不需 Cloudflare API call
    }

    // R3: DELETE domain_binding rows
    db.DeleteDomainBindings(app.ID)

    // R4: render & push gitops（移除 apps/<team>/<app>/）
    gitops.RemoveAppDir(ctx, GitOpsArgs{
        Team: teamSlug, App: app.Slug,
        DeployRunID: deleteRunID,
        TraceID: traceID,
    })

    // app.status = 'deleting'
    db.UpdateApp(app.ID, "deleting")
    return nil
}
```

#### Irreversible 階段

```go
func executeIrreversible(ctx context.Context, app *db.App) error {
    // I1: ArgoCD prune（自動觸發 by gitops 移除）
    // 不需主動呼叫；ArgoCD selfHeal+prune 會自動偵測並刪 K8s 資源
    // backend 等 ArgoCD 完成（reconciler 偵測 Application 不存在）；timeout 5 min

    // I2: hard delete app row（保留 audit_log + deploy_run 歷史）
    db.DeleteApp(app.ID)

    return nil
}
```

#### Compensate（reversible 失敗）

```go
func compensate(ctx, completed []string) {
    for i := len(completed) - 1; i >= 0; i-- {
        switch completed[i] {
        case "gitops_remove":
            gitops.RevertCommit(ctx)   // git revert + push；ArgoCD 把 K8s 物件 re-create
        case "delete_domain_bindings":
            db.RestoreDomainBindings(...)   // 從 audit log 恢復
        case "unbind_custom_hostnames":
            // 重新註冊 Cloudflare hostname；client 端需重做 DNS 驗證
            // v1 不自動恢復；標 cleanup_residue
        case "cancel_deploy_run":
            // 不重啟；audit 記錄
        }
    }
    db.UpdateApp(app.ID, "delete_compensated")
}
```

### 5.3 Last_result

```json
{
  "idempotent_replay": false,
  "data": {
    "app_id": "...",
    "app_slug": "...",
    "deleted_at": "2026-05-10T13:04:56Z",
    "trace_id": "..."
  }
}
```

## 6. Cleanup residue

### 6.1 觸發

- Compensate 失敗：reversible 階段 undo 不全；user 已 confirm 但資料部分損壞
- Irreversible 階段失敗（罕見：ArgoCD prune 因 K8s API 中斷）：app row 仍 active 但 manifest 已移除

### 6.2 Reconciler

`reconciliation_job(kind='cleanup_residue', subject_id=app_id)`：

- 30s tick 偵測：
  - `app.status='deleting'` 且 manifest 已 push 移除
  - 等 ArgoCD Application 物件不存在 → 即可 hard delete app row
  - 5 min 後仍存在 → audit + alert ops
- 退避：`min(60s × 2^attempts, 30min)`；> 8 次 → `failed_permanently`，需 ops 介入

### 6.3 Manual cleanup runbook

`docs/runbooks/delete-app-residue.md`（待 M5 撰寫）：列出常見 stuck 情境（PVC 卡 finalizer、ingress 卡 webhook 等）與 kubectl 指令

## 7. CLI / MCP 行為

### 7.1 CLI

```
$ 0ops apps delete nextdemo

警告：刪除 app `nextdemo` 為不可逆操作。
此操作將：
  - 取消所有正在進行中的部署
  - 解除 N 個自有域名（Cloudflare 端 cert 釋放）
  - 從 0ops-gitops 移除 manifest
  - ArgoCD prune K8s 資源（含 Deployment / Service / Ingress / PVC；資料無法 undo）

請輸入 app slug 確認: nextdemo
[type slug to proceed; ctrl-C to abort]

即將執行：刪除 app `nextdemo`（@ team `acme-prod`）— 此操作無法 undo
副作用：
  1. Cancel any in-flight builds for this app
  2. Unbind 1 custom hostnames from Cloudflare
  3. Remove domain bindings from DB
  4. Remove app manifest from 0ops-gitops
  5. ArgoCD will prune Kubernetes resources (irreversible)

確認執行? [y/N] y

✓ app `nextdemo` 已刪除（trace_id: ...）
```

> CLI 強制兩段確認：（1）打字輸入 slug、（2）y/N。`--yes` flag 跳過第二段但**不**跳過第一段（強警示）。

### 7.2 MCP

- LLM call `delete_app_preview(team_slug=, app_slug=, confirm=<app_slug>)`
- LLM 顯示完整 PlanPreview + 警示語給 user
- LLM 必須要求 user 明確同意（與其他 write tool 一致；`mcp-tool-description-lint` § 4.2 強制）
- LLM 收到同意後 call `delete_app(team_slug=, preview_id=)`

## 8. 與其他 spec 接合點

| 接合 | spec |
|---|---|
| Preview/Confirm 框架 | `preview-confirm-gate` |
| Custom hostname unbind | `custom-domain-and-verify` § 8.1 |
| Cloudflare API client | `winshare-subdomain-and-tunnel` § 8 |
| Gitops manifest 移除 + revert | `gitops-render-and-argocd` § 4.1 + § 5 |
| ArgoCD prune | `gitops-render-and-argocd` § 6.2 |
| In-flight build cancel | `build-pipeline-and-callback` |
| Reconciler `cleanup_residue` | `reconciler-and-incident` spec |
| audit_log（含 actor / preview_id / trace_id）| `audit-log` spec |
| `confirm_mismatch` / `already_deleting` 失敗碼 | `error-model` § 5 |
| Role admin + scope apps:delete | `auth-and-rbac` § 6 |
| MCP description NEVER clause | `mcp-tool-description-lint` § 4.2 |

## 9. 驗證準則

| 驗證項 | 方式 | 通過條件 |
|---|---|---|
| Confirm slug mismatch | preview args.confirm != app_slug | 422 confirm_mismatch |
| Member role 拒 | role=member | 403 forbidden_role |
| Admin role 過 | role=admin | preview / confirm 通過 |
| Already deleting | mock app.status='deleting' | 422 already_deleting |
| In-flight build cancel | mock 1 個 building deploy_run | GHA cancelWorkflowRun 被呼；deploy_run 標 cancelled |
| Custom hostname unbind | mock 2 extra domain | Cloudflare DELETE 被呼 2 次 |
| Gitops manifest removed | check after confirm | `apps/<team>/<slug>/` 目錄不存在 |
| ArgoCD prune | wait < 5 min | K8s 資源不存在 |
| App row hard delete | post confirm | DB 內 app row 不存在 |
| audit_log 完整 | preview / confirm 後 | 含 preview_id, trace_id, action='delete_app' |
| Compensate（gitops 失敗） | mock 第 5 步 push 失敗 | revert 前面 R1-R3；app 標 delete_compensated |
| Cleanup residue | mock ArgoCD timeout | reconciliation_job 創建；retry 8 次後 failed_permanently |
| CLI 二次打字確認 | `0ops apps delete nextdemo` 不打字 | abort |
| CLI --yes 仍要打字 | mock --yes | 仍要求打字 slug |
| MCP description 含 NEVER | lint check | 通過 |
| Replay（preview_id 重 confirm） | confirm 兩次 | 第二次 idempotent_replay=true；副作用未重做 |

## 10. SLI 對應

| SLI | 目標 | 量測 |
|---|---|---|
| Delete confirm → app row gone | p95 < 5 min | confirm time → DB delete |
| Cleanup residue 比例 | < 1% / 28d | `cleanup_residue` reconciliation_job / total deletes |
| Custom hostname unbind 失敗率 | < 0.5% | Cloudflare DELETE 失敗 / total |
| ArgoCD prune timeout 比例 | < 0.5% | timeout 後進 cleanup_residue |

## 11. 對 `docs/0ops-plan.md` 的修改清單

1. 「Tool catalog」`delete_app`：交叉引用本 spec；補入「最低 role=admin」（plan.md 已標）
2. 「DB schema § app」：新增 `status text not null default 'live'`；列舉 `live | paused | deleting | delete_compensated`
3. 「Deploy 狀態機」段：補入「`delete_app` 不進主狀態機；獨立流程」
4. 「Risks & open」：新增「資料 loss 風險（PVC 刪除）；user 須自行 backup」

## 12. Open issues

- PVC reclaim policy：v1 採 K8s 預設（通常 Delete）；v1.1 評估提供「保留資料」選項
- `force` flag：v1 不支援；v1.1 評估 admin 跳過 in-flight cancel 強制刪
- 已 paused app 之 delete 行為：v1 採同樣流程（仍要 unbind / prune）；v1.1 評估快速路徑
- Custom domain 7 天 grace 與 delete 衝突：本 spec 採「delete 即立即 unbind」（user 主動）；ADR-0007 之 grace 僅對 reconciler-driven 撤銷適用
- Cleanup residue 之 ops 通知機制：v1 為 stdout/log；v1.1 webhook / email
- 同時 delete 多 app（rate limit）：屬 `rate-limit-and-abuse` spec
- Deploy_run 歷史保留：v1 採「app 刪除後 deploy_run row 留 90 天」；屬 plan.md 保留期規則
- App restore（誤刪後 7 天內恢復）：v1 不支援；屬 v2

## 13. 不可違反的硬性規則

> 違反以下任一項，PR 不可合入。

1. delete_app 必經 preview-confirm-gate
2. role 最低 admin；member / viewer 不可刪
3. preview args 必含 `confirm` 欄位且須等於 app_slug；防誤刪
4. CLI 必強制打字 slug 確認；`--yes` 不得跳過此段
5. 副作用順序：先 cancel in-flight build → unbind 域名 → 移除 manifest → ArgoCD prune；不得倒序
6. Custom hostname unbind 不適用 7 天 grace（user 主動）
7. App row 為 hard delete；不採 soft delete（避免 slug 重用衝突）
8. Cleanup residue 必走 reconciler；不得 silent 留下殘餘
9. 一旦 ArgoCD 開始 prune K8s 資源即不可 undo；compensate 邊界在 prune 前
10. delete_app 之 audit_log 為**永久保留**（不適用 13 個月過期；屬 plan.md 補規則）
