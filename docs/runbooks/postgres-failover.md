# Runbook：Postgres 手動 failover

> 對應 spec：`docs/features/postgres-ha-and-dr/spec.md` § 9
> 對應 ADR：ADR-0008 § 4 第 7 點
> 適用範圍：application Postgres（main + 1 streaming replica）

## 1. 觸發條件（spec § 9.1）

任一條件成立即啟動本 runbook：

1. `postgres-main` pod 連續 5 min `pg_isready` 失敗且 `kubectl describe pod` 顯示不可恢復狀態（CrashLoopBackOff、ImagePullBackOff 之外的 root cause）
2. `postgres-main` 所在 K3s node 失效（`kubectl get node` `NotReady` 持續 5 min）
3. main 資料損壞（rare；若為邏輯損壞改走 `postgres-pitr.md`）

> 若僅是 pod restart 中（< 5 min），等 readiness 自然恢復；不要 failover。
> 若不確定，先看 `kubectl logs postgres-main-0 --previous`。

## 2. 流程

整體預估時間：30 min（含人為決策）。

### Step 1 — 確認 main 無法恢復（≤ 5 min）

```bash
kubectl -n system-0ops get pod -l app=postgres -o wide
kubectl -n system-0ops describe pod postgres-main-0
kubectl -n system-0ops exec postgres-replica-0 -- pg_isready -h postgres-main.system-0ops.svc.cluster.local
```

任一條件成立進 Step 2：

- `pg_isready` 連 5 次（每 10s）皆失敗
- main pod 顯示 `Failed` / `Unknown` / node 不可達

### Step 2 — Promote replica（≤ 30s）

在 replica pod 內執行 `pg_ctl promote`（PostgreSQL 17 推薦語法）：

```bash
kubectl -n system-0ops exec -it postgres-replica-0 -- \
  su - postgres -c 'pg_ctl promote -D /var/lib/postgresql/data/pgdata'
```

等待 `pg_isready` 返回 `accepting connections`：

```bash
until kubectl -n system-0ops exec postgres-replica-0 -- pg_isready; do sleep 2; done
```

驗證 promote 已生效（`pg_is_in_recovery` 為 `f`）：

```bash
kubectl -n system-0ops exec postgres-replica-0 -- \
  psql -U ops -d ops -c 'SELECT pg_is_in_recovery();'
```

### Step 3 — 切 Service endpoints（≤ 1 min）

最快路徑：把 `postgres-main` Service 的 selector 改成指向 replica pod 標籤（spec § 4.2 保留 `postgres-main` 為唯一寫入入口；不要改 backend 端 DSN）。

```bash
kubectl -n system-0ops patch service postgres-main \
  --type='json' \
  -p='[{"op":"replace","path":"/spec/selector/role","value":"replica"}]'
```

> 這是 emergency override。promote 後正規修復路徑見 Step 6。

### Step 4 — Backend rolling restart（≤ 5 min）

Backend 必須 rolling restart 才能保證連線池全部換到新 endpoints（spec § 16 hard rule #10：不可熱切換 connection pool）。

```bash
kubectl -n system-0ops rollout restart deployment/0ops-server
kubectl -n system-0ops rollout status  deployment/0ops-server --timeout=5m
```

驗證 backend 連到的是 primary（非 replica）— EnsurePrimary 在啟動時會 panic 若連到 replica：

```bash
kubectl -n system-0ops logs -l app=0ops-server --tail=50 | grep -i 'postgres primary check'
```

### Step 5 — 確認業務恢復（≤ 2 min）

```bash
curl -sf https://api.0ops.io/health
```

從 CLI 確認 read + write 都 OK：

```bash
0ops apps list --team <team>
```

### Step 6 — 修復原 main 並接回作為新 replica（可延後到下次 maintenance window）

1. 刪除原 main 的 PVC（資料損壞情境下；若僅 node 失效則 cordon node 後重排程即可）
2. 把 `postgres-main` Service selector 還原為 `role: main`
3. 把原 replica pod 標籤改為 `role: main`（或新建 main StatefulSet）
4. 啟動新 replica（按原 `statefulset-replica.yaml` 重新 init）
5. 跑一次完整 `pg_basebackup`（容器內 `replica-init.sh` 自動處理）

詳細步驟記錄在 `docs/runbooks/postgres-restore-test.md`。

## 3. RTO / RPO 預算（spec § 13）

- 目標 RTO < 30 min；上方流程約 8–10 min 不含人為決策。
- 目標 RPO < 5 min；由 `archive_timeout=300s` + async streaming 共同保障。
- 單次 failover 約占 99.9% 月度 budget 75%（spec § 14 第 4 點）；故 v1.1 評估 Patroni 自動化。

## 4. 失敗回退（worst case）

- promote 後 replica 也起不來 → 改走 `postgres-pitr.md` 從 WAL archive + base backup 重建
- service patch 失敗 → 改 `kubectl -n system-0ops set` 直接更新 Endpoints；或臨時 patch backend DSN（但仍須 rolling restart）
- backend 啟動 panic（EnsurePrimary 失敗）→ 確認 replica `pg_is_in_recovery=f`；若仍為 `t`，promote 沒有成功，回 Step 2

## 5. 演練要求（spec § 16 hard rule #5）

- M5 GA 前必演練一次完整 failover 並把結果錄到 `postgres-restore-test.md`
- 每季 ops 排程演練一次；演練前必預告 oncall + relevant stakeholders
