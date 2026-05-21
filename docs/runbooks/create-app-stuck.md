# Runbook：create_app stuck in building / syncing

> 對應 spec：`docs/features/create-app-flow/spec.md`、`docs/features/reconciler-and-incident/spec.md` § 8
> 對應 ADR：ADR-0002（idempotency / 狀態機）、ADR-0005（build pipeline）
> 適用範圍：`apps.status` 或 `deploy_runs.status` 卡在中間態超過 reconciler 預算

## 1. 觸發條件

任一條件成立進本 runbook：

1. `deploy_runs.status = 'building'` 持續 > **30 min**（spec § 8.1 `BuildingTimeout`）
2. `deploy_runs.status = 'syncing'` 持續 > **15 min**（spec § 8.2 `SyncingTimeout`）
3. CLI `0ops apps show <slug>` 回 `building` / `syncing` 已超過上述閾值
4. 對應 `incidents` 表已開 `cleanup_residue_stuck` 或類似 kind 但未自動收斂

> Reconciler 預期會在 30s 間隔內掃到 stuck row 並自動拉 GHA workflow / ArgoCD app 狀態（`DeployStatusScanner.Tick` / `ArgoSyncScanner.Tick`）。**先給 reconciler 至少 1 個 interval（30s）後再進 Step 1**，避免人工介入打亂自動收斂。

## 2. 狀態機速查

`deploy_runs.status` 合法值（`normalizeDeployStatus`）：

```
queued → preparing → building → pushing → rendering → syncing → live
                                                              ↘ failed / canceled / rolled_back
```

對應行為：

| Status | 推進者 | Timeout | Reconciler 行為 |
|---|---|---|---|
| `building` | GHA workflow callback | 30 min | `DeployStatusScanner` 拉 GHA run 結果，轉 `live` / `failed` |
| `syncing` | ArgoCD application sync | 15 min | `ArgoSyncScanner` 拉 ArgoCD app health，轉 `live` / `failed` / 留 `syncing`（progressing） |

## 3. 排查流程

整體預估時間：15 min。

### Step 1 — 撈卡住的 run（≤ 2 min）

```bash
psql "$DATABASE_URL" -c "
SELECT id, app_id, status, github_workflow_run_id, started_at, now() - started_at AS age
FROM deploy_runs
WHERE status IN ('building','syncing','pushing','rendering','preparing','queued')
  AND started_at < now() - INTERVAL '15 minutes'
ORDER BY age DESC
LIMIT 20;"
```

記下卡住的 `id`、`status`、`github_workflow_run_id`。

### Step 2 — 判讀 stuck 原因（≤ 5 min）

#### 2A. Status = `building`

```bash
gh run view <github_workflow_run_id> --json status,conclusion,jobs --jq '{status, conclusion, failed: [.jobs[] | select(.conclusion=="failure") | .name]}'
```

判讀：

- GHA workflow `status=completed conclusion=success` → 後端沒收到 callback。先看 `gha-callback-signature-failure.md` 排查
- GHA workflow `status=completed conclusion=failure` → reconciler 下個 tick 會轉 `failed`；等 30s 後重查
- GHA workflow `status=in_progress` 但已超時 → workflow 卡 build cache、self-hosted runner offline；用 `gh run cancel <id>` 取消，再 `0ops redeploy <app-slug>`
- GHA workflow `status=queued` 已 > 10 min → runner 容量不足；查 GitHub runner page

#### 2B. Status = `syncing`

```bash
kubectl -n <app-namespace> argocd app get <app-name> -o yaml | yq '.status | {sync: .sync.status, health: .health.status, message: .health.message}'
```

判讀（對應 `mapArgoCDDeployStatus`）：

- `sync=Synced health=Healthy` → reconciler 下個 tick 會轉 `live`；等 30s
- `sync=Synced health=Progressing` → 持續 progressing；查 pod log 看是否 image pull 慢、init container fail
- `sync=Synced health=Degraded` → 將會轉 `failed`；先撈 pod log 留證
- `sync=OutOfSync` → ArgoCD 沒 reconcile；手動 `argocd app sync <app-name>`

### Step 3 — 介入手段（≤ 5 min）

依嚴重程度由輕到重：

1. **等 reconciler 自動收斂**：上述 timeout 過後 30s 內必掃到；80% 情境會自動轉 terminal state
2. **重觸 GHA workflow**：`gh run rerun <github_workflow_run_id> --failed`（限 GHA workflow 自身 fail 可重跑的情境）
3. **重發 deploy**：`0ops redeploy <app-slug>`（走 preview/confirm 流程，產生新 `deploy_runs` row；舊 row 由 reconciler 收尾）
4. **強制標 canceled**（escape hatch，需 ops 確認）：

```sql
-- 僅當上述都不可行時。會繞過 reconciler 的 classification 流程。
UPDATE deploy_runs
SET status = 'canceled',
    finished_at = now(),
    failure_classification = 'manual_intervention'
WHERE id = '<deploy-run-id>'
  AND status IN ('building','syncing','pushing','rendering','preparing','queued');
```

5. **刪除整個 app 重建**：`0ops apps delete <slug>` 後重 create；僅在 app 完全無法恢復時用

### Step 4 — 確認業務恢復（≤ 3 min）

```bash
0ops apps show <slug>     # status 應為 live 或 failed（非中間態）
curl -sf https://<app-host>/health 2>/dev/null
```

## 4. 失敗回退

- 強制 `UPDATE` 後 reconciler 不再掃 → 預期；該 row 已是 terminal state
- 強制 cancel 後 ArgoCD 仍持有舊 application → ops 手動 `argocd app delete <app-name> --cascade`，再走 redeploy
- 多筆 row 同時 stuck（> 5 在 15 min 內）→ 可能 reconciler 自己 down；查 server log `reconciler.runner` 是否還在 tick：`kubectl logs -l app=0ops-server | grep -E "deploy_status|argo_sync" | tail -20`

## 5. 演練要求

無強制演練。建議每季排一次「模擬 GHA runner offline」演練：
- 暫停 self-hosted runner 後觸發一次 deploy
- 觀察 30 min 後 reconciler 是否自動轉 `failed`
- 演練後恢復 runner，重 deploy 驗證綠路徑
