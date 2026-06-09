# Runbook：delete-app cleanup_residue 卡住

> 對應 spec：`docs/features/delete-app-flow/spec.md` § 6.2
> 對應修復：PR #117（wire cleanup_residue handler）+ admin retry-delete
> 適用範圍：app `delete_app` 後永遠停在 `status='deleting'`，未在預期時間內消失。

## 1. 觸發條件

- `0ops apps list` 某 app 長時間 `deleting`
- `incidents` 表開 `cleanup_residue_stuck`（v1.1）或對應 alert
- 外部回報「刪了卻還在」

## 2. 診斷

### 2.1 看 app 與其 cleanup_residue job

```bash
# app 狀態
0ops apps get <app-slug> --team <team-slug>

# 對應 reconciliation_job（DB 直查；ops only）
#   status=pending            → 正常，等下一個 job_queue tick（backoff 內）
#   status=failed_permanently → 卡死，需 retry（§ 3）
#   無 row                    → saga 沒 enqueue，走 § 4 escalate
psql "$DATABASE_URL" -c "
SELECT kind, status, attempts, next_attempt_at, left(last_error,60) AS err
FROM reconciliation_job
WHERE subject_type='app' AND kind='cleanup_residue'
  AND subject_id = (SELECT id FROM app WHERE slug='<app-slug>')
ORDER BY created_at DESC LIMIT 3"
```

### 2.2 判讀

| job 狀態 | 意義 | 動作 |
|---|---|---|
| `pending`，`next_attempt_at` 在未來 | 退避中，會自動收斂 | 等到 `next_attempt_at` 後再看 |
| `pending`，`next_attempt_at` 已過但沒動 | reconciler 沒在跑 / 非 leader | 查 `reconciler started` log、leader 狀態 |
| `failed_permanently` | 8 次 retry 用盡（spec § 6.2） | 走 § 3 retry-delete |
| 無 row | delete saga 沒 enqueue（舊 bug / 異常） | 走 § 4 |

> 背景：reconciler 的 `job_queue` loop 只掃 `status='pending'`。`failed_permanently`
> 是終態，不會自己被重撿 —— 這就是為何卡死的 delete 需要人工 retry。

## 3. Retry（標準回復）

```bash
0ops admin retry-delete --team-slug <team-slug> --app-slug <app-slug>
```

行為：

- 驗 app 仍在 `deleting`（否則回 `app_not_deleting`，避免誤觸活著的 app）
- re-enqueue 一個全新的 `cleanup_residue` job（`attempts=0`、`next_attempt_at=now`）
- 下一個 `job_queue` tick 由 `HandleResidue` 收斂：ArgoCD prune wait → hard delete
- handler 冪等：重複 retry 安全（先成功者 hard delete，其餘發現 app 已消失即完成）

驗證收斂：

```bash
0ops apps list --team <team-slug>   # app 應在幾個 tick 內消失
```

## 4. 底層 stuck 情境（retry 仍不收斂時）

`HandleResidue` 會卡在 ArgoCD prune wait 當 K8s 資源有 finalizer 沒釋放。常見：

### 4.1 PVC 卡 finalizer

```bash
kubectl -n <app-namespace> get pvc
kubectl -n <app-namespace> get pvc <pvc> -o jsonpath='{.metadata.finalizers}'
# 確認沒有 pod 還掛著該 PVC，再移除 finalizer（謹慎）：
kubectl -n <app-namespace> patch pvc <pvc> -p '{"metadata":{"finalizers":null}}' --type=merge
```

### 4.2 Ingress / namespace 卡 webhook finalizer

```bash
kubectl get ns <app-namespace> -o jsonpath='{.spec.finalizers}'
kubectl -n <app-namespace> get ingress
# admission webhook 失效會卡刪除；確認 webhook 健康或暫移除對應 finalizer
```

### 4.3 ArgoCD Application 仍持有

```bash
argocd app get <team>-<app-slug>
argocd app delete <team>-<app-slug> --cascade
```

清掉底層 finalizer 後，再跑 § 3 的 `retry-delete`。

## 5. Escalate

以下情況開 incident 並通知 platform owner：

- § 4 全做完仍卡 `deleting` 超過 30 min
- `reconciliation_job` 完全沒有對應 row（saga enqueue 路徑異常）
- 多個 app 同時卡（系統性 reconciler / leader 故障，非單一 app）

## 6. 預防

- `delete_app` saga 一定 enqueue `cleanup_residue`（spec § 13 hard rule #8：不得 silent 留殘餘）
- reconciler registry 必註冊 `cleanup_residue` handler —— 由
  `server.RegisterReconcilerHandlers` 的單元測試守住（PR #117 回歸防護）
- v1.1：`cleanup_residue` 比例 > 1% / 28d 應觸發 SLO alert（spec § SLI）
