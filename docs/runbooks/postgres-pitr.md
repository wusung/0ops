# Runbook：Postgres PITR 還原

> 對應 spec：`docs/features/postgres-ha-and-dr/spec.md` § 8
> 對應 ADR：ADR-0008 § 4 第 6 點
> 適用範圍：application Postgres 邏輯損壞 / 誤刪資料還原

## 1. 觸發條件（spec § 8.1）

- 誤刪 cluster-wide 資料（例：`DELETE FROM app WHERE ...` 不慎 commit）
- 邏輯損壞（migration bug、應用層 bug 寫壞資料）
- 需還原至特定時間點（incident、合規）

> 物理硬體故障 / pod crash 走 `postgres-failover.md`，不走 PITR。

## 2. 前置條件

- WAL archive 在 R2 `0ops-pg-wal/<env>/main/wal/...` 連續可用至少回到 target time
- 有可用 base backup（每日 `pg_dump` 或近期 `pg_basebackup` snapshot）
- 已停 backend write（否則 PITR 視窗內仍會被覆寫）

## 3. 流程

整體預估 RTO：30 min（含人為決策）。

### Step 1 — Stop backend writes（≤ 2 min）

縮 backend replicas 至 0，停所有 write 流入 main：

```bash
kubectl -n system-0ops scale deployment/0ops-server --replicas=0
kubectl -n system-0ops rollout status deployment/0ops-server --timeout=2m
```

確認沒有殘留連線：

```bash
kubectl -n system-0ops exec postgres-main-0 -- \
  psql -U ops -d ops -c "SELECT count(*) FROM pg_stat_activity WHERE state <> 'idle';"
```

### Step 2 — 啟 staging Postgres（≤ 5 min）

在 `system-0ops-staging` namespace 起一個 PostgreSQL 17 pod（命名 `postgres-pitr-restore`）。Helm 引用同一 chart，但：

- `values.yaml` override `auth.renderPlaceholder: true`（不依賴外部 Secret）
- `walArchive.bucket` 維持 prod 一致（要拉的是 prod WAL）
- main StatefulSet 為 replicas=1；replica StatefulSet 設 replicas=0（restore 只需 main 角色）

### Step 3 — 拉 base backup + WAL archive（≤ 10 min）

進 pod 內：

```bash
# 1) 拉最近一份 logical dump 作為 base
aws s3 cp --endpoint-url $DUMP_S3_ENDPOINT \
  s3://0ops-pg-dump/prod/<最近一份>.sql.gz /tmp/base.sql.gz

# 2) 解壓並建空 schema（如果 logical dump）；或直接 pg_basebackup（physical）
pg_restore -Fc -d ops /tmp/base.sql.gz

# 3) 把 WAL archive 同步到 pgdata/restored_wals/
aws s3 sync --endpoint-url $WAL_S3_ENDPOINT \
  s3://0ops-pg-wal/prod/main/wal/ /var/lib/postgresql/data/pgdata/restored_wals/
```

### Step 4 — 寫 recovery configuration（≤ 2 min）

PostgreSQL 17 用 `postgresql.auto.conf` + `recovery.signal`：

```bash
cat >> /var/lib/postgresql/data/pgdata/postgresql.auto.conf <<EOF
restore_command = 'cp /var/lib/postgresql/data/pgdata/restored_wals/%f %p'
recovery_target_time = 'YYYY-MM-DD HH:MM:SS UTC'   # 替換為實際 target
recovery_target_action = 'pause'                    # 達 target 後 pause，不自動 promote
EOF
touch /var/lib/postgresql/data/pgdata/recovery.signal
```

### Step 5 — 啟動 staging 並等回放（≤ 5 min）

```bash
pg_ctl start -D /var/lib/postgresql/data/pgdata
```

監看 log：

```bash
tail -f /var/lib/postgresql/data/log/postgresql.log
```

預期出現 `recovery stopping before commit of transaction ...` 與 `paused at the end of recovery`。

### Step 6 — 驗證（≤ 5 min）

在 staging 跑 sample query 確認資料為 target time 狀態：

```bash
psql -U ops -d ops -c "SELECT count(*) FROM app WHERE deleted_at IS NULL;"
psql -U ops -d ops -c "SELECT id, slug FROM app ORDER BY created_at DESC LIMIT 20;"
```

驗證通過後 promote staging 並切 backend：

```bash
psql -U ops -d ops -c 'SELECT pg_wal_replay_resume();'   # 結束 pause
pg_ctl promote -D /var/lib/postgresql/data/pgdata
```

### Step 7 — 切 backend 至 staging（≤ 5 min）

兩條路徑擇一：

**A. 把 staging 物理位置升為 prod**（資料量大時推薦）：
- 改 `postgres-main` Service 的 Endpoints 指向 staging pod
- backend rolling restart

**B. logical copy 回原 main**（資料量小時）：
- `pg_dump` staging → `pg_restore` 至原 main（先 truncate 受損 table）
- backend rolling restart

兩種路徑都必須 rolling restart backend（spec § 16 hard rule #10）。

```bash
kubectl -n system-0ops scale deployment/0ops-server --replicas=2
kubectl -n system-0ops rollout status deployment/0ops-server --timeout=5m
```

### Step 8 — 驗證 backend 健康

```bash
curl -sf https://api.0ops.io/health
0ops apps list --team <team>
```

確認 backend log 內 `postgres primary check` 通過（EnsurePrimary）。

## 4. 失敗回退

- WAL replay 超過 target time 而未 pause → 沒設 `recovery_target_action='pause'`；重做 Step 4
- WAL gap（找不到下一段）→ 找到 archive 缺失的 segment；無法填補時只能用 logical dump 為 base，接受 dump → target 之間的 RPO 損失
- staging promote 失敗 → 走 Step 7 path B 把 logical dump 回 main

## 5. 演練要求（spec § 8.3 + § 16 hard rule #5）

- M5 GA 前必演練一次完整 PITR；錄入 `postgres-restore-test.md`
- 每季 ops 排程演練一次
- 本機可用 `deploy/postgres/scripts/pitr-drill.sh` 跑壓縮版 drill（podman compose），結果寫入 `postgres-restore-test.md`
