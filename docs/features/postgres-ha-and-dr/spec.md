# Feature Spec：postgres-ha-and-dr

> **狀態**：draft
> **來源**：`docs/0ops-plan.md`「Postgres backup / DR」段；ADR-0008 § 4 第 5 點（拓樸與 RPO/RTO）；本 spec 依賴 `secrets-management`、`backend-ha-leader-election`、`reconciler-and-incident`
> **適用範圍**：**application Postgres**（不含 K3s datastore Postgres，後者屬 ADR-0004）：main + 1 streaming replica、WAL archive、daily pg_dump、PITR、手動 promote runbook
> **對應 Milestone**：M5

## 1. 結論（先讀本段）

- Application Postgres 拓樸：main + 1 streaming replica（async 複製），跨 K3s node
- WAL archive 至 R2 / S3 每 5 min；30 天保留
- Daily `pg_dump`（邏輯備份）30 天保留
- PITR：archive + base backup；RPO 5 min / RTO 30 min（演練於 M5）
- v1 + M5 採**手動 failover**（runbook）；v1.1 評估 Patroni 自動化
- Backend `DATABASE_URL` Secret 由 ops 在 failover 時手動 patch；rolling restart backend 即切換
- Read replica 在 v1 **不暴露**至應用層 read 路徑（避免 replication lag 引入語意 bug）；M5 後評估特定 read endpoint
- Datastore Postgres（K3s control plane）為**獨立 instance**；本 spec 不涵蓋

## 2. 範圍

### 2.1 包含
- Postgres deployment 拓樸（main + replica；StatefulSet）
- 物理複製（streaming replication）配置
- WAL archive 至 R2 / S3
- Daily logical backup（`pg_dump`）
- PITR runbook 摘要
- Failover runbook 摘要
- Backend 端 `DATABASE_URL` 切換流程
- Migration 安全閘（CI lint、staging 24h、CONCURRENTLY 強制）
- Backup 與 restore 之 metric

### 2.2 不包含
- K3s datastore Postgres 之 backup（屬 ADR-0004 + ops runbook）
- Patroni 部署（v1.1 評估）
- Cross-region replica（v2）
- Multi-master / Citus（v3）
- Read replica 暴露邏輯（M5 後評估，屬未來 spec）
- Application 之 schema migration 內容（屬各 feature spec）

## 3. 檔案結構

```
0ops/
└── deploy/
    └── chart/
        └── postgres/                       # Postgres StatefulSet chart
            ├── templates/
            │   ├── statefulset-main.yaml
            │   ├── statefulset-replica.yaml
            │   ├── service-main.yaml
            │   ├── service-replica.yaml
            │   ├── configmap-postgresql.conf.yaml
            │   ├── configmap-pg-hba.yaml
            │   ├── secret.yaml             # placeholder
            │   ├── pvc.yaml
            │   ├── networkpolicy.yaml
            │   ├── cronjob-pg-dump.yaml    # daily pg_dump
            │   └── deployment-wal-archiver.yaml
            └── values.yaml
docs/
└── runbooks/
    ├── postgres-failover.md                # 手動 promote runbook
    ├── postgres-pitr.md                    # PITR 還原 runbook
    └── postgres-restore-test.md            # 演練清單
```

## 4. 拓樸

### 4.1 部署形態

- StatefulSet `postgres-main`，replicas=1
- StatefulSet `postgres-replica`，replicas=1
- 兩 StatefulSet 之 PVC 各自獨立；`storageClassName` 為 K3s `local-path`（v1）；M5 評估升 longhorn / 雲端 CSI
- Anti-affinity：`postgres-main` 與 `postgres-replica` 必跨 K3s node（`requiredDuringSchedulingIgnoredDuringExecution`）

### 4.2 Service

- `postgres-main` Service（ClusterIP）：唯一寫入入口；backend `DATABASE_URL` 指向此
- `postgres-replica` Service（ClusterIP）：v1 不暴露至 backend；ops 直連 debug
- 不採 PgBouncer / pgPool（v1）；連線池由 backend 端 `pgxpool` 管理

### 4.3 PostgreSQL 版本

- 鎖 PostgreSQL 17.x；升版走 PR + staging 24h 演練
- WAL format / replication protocol 在 17.x 內向後相容；minor 升版安全

### 4.4 PostgreSQL 設定

| 設定 | 值 | 說明 |
|---|---|---|
| `wal_level` | `replica` | 必要 for streaming replication |
| `max_wal_senders` | 5 | 預留多 replica + base backup |
| `wal_keep_size` | 1GB | 短期斷線恢復 buffer |
| `archive_mode` | `on` | WAL archive 啟用 |
| `archive_command` | `/scripts/wal-push.sh %p %f` | 推 R2 / S3 |
| `archive_timeout` | `300s`（5 min）| 強制 segment 切換 |
| `hot_standby` | `on` | replica 可讀 |
| `synchronous_commit` | `off` | async replication（性能優先） |
| `max_connections` | 100 | backend 預估 < 50 |

## 5. Streaming replication

### 5.1 Replica 初始化

- `pg_basebackup` 於 replica pod 啟動時執行（init container）
- 從 main 拉 base backup + standby.signal + primary_conninfo
- 啟動後進 hot standby 模式

### 5.2 主從監控

| metric | 來源 |
|---|---|
| `pg_replication_lag_seconds` | replica 之 `pg_last_xact_replay_timestamp()` 與 main 之差 |
| `pg_wal_archive_status` | `pg_stat_archiver`：fail count、last_archived_time |
| `pg_stat_replication` | replica connection 狀態 |

- backend 不直接暴露此 metric；由 `postgres_exporter` sidecar（v1.1 補；v1 採 ops 手動觀察）

### 5.3 Replica lag alert

- lag > 60s 持續 5 min → 建 incident（severity=critical；接續 `reconciler-and-incident` § 9.2）

## 6. WAL archive

### 6.1 路徑

- 推送至 Cloudflare R2（與 ADR-0007 同帳體系）
- bucket：`0ops-pg-wal`
- 路徑：`<env>/<cluster>/wal/<timeline>/<wal_file>`
- v1 採 `archive_command` 直接推（簡化）；v1.1 評估 `wal-g` / `pgbackrest`

### 6.2 archive_command 腳本

```bash
#!/usr/bin/env bash
# /scripts/wal-push.sh
set -euo pipefail
src="$1"
name="$2"
timeline=$(echo "$name" | head -c 8)

aws s3 cp "$src" "s3://0ops-pg-wal/prod/main/wal/$timeline/$name" \
  --endpoint-url=https://<r2-endpoint> \
  --no-progress
```

- credentials 從 K8s Secret `r2-backup-credentials`（屬 `secrets-management` § 4）
- 失敗 → exit 1；PostgreSQL 端 retry（archive_timeout）

### 6.3 保留

- 30 天；R2 bucket lifecycle rule 自動 expire
- 30 天前 WAL 不可用於 PITR；應有 daily pg_dump 涵蓋

## 7. Daily pg_dump

### 7.1 CronJob

```yaml
apiVersion: batch/v1
kind: CronJob
metadata:
  name: postgres-daily-dump
  namespace: system-0ops
spec:
  schedule: "0 18 * * *"     # UTC 18:00 = TST 02:00
  jobTemplate:
    spec:
      template:
        spec:
          containers:
            - name: dumper
              image: postgres:17-alpine
              command: ["/scripts/pg-dump.sh"]
              env:
                - name: PGHOST
                  value: postgres-main
                - name: PGUSER
                  valueFrom:
                    secretKeyRef:
                      name: postgres-app-credentials
                      key: dump_user
                # ...
              volumeMounts:
                - { name: scripts, mountPath: /scripts }
          restartPolicy: OnFailure
```

### 7.2 dump 腳本

```bash
#!/usr/bin/env bash
set -euo pipefail
ts=$(date -u +%Y%m%d-%H%M%S)
out="/tmp/0ops-${ts}.sql.gz"

pg_dump -Fc -Z9 -f "$out" "$PGDATABASE"

aws s3 cp "$out" "s3://0ops-pg-dump/prod/$ts.sql.gz" \
  --endpoint-url=https://<r2-endpoint>

rm "$out"
```

- 30 天保留（R2 lifecycle）
- dump 為 logical；可跨主版本 restore

## 8. PITR runbook

### 8.1 場景

- 誤刪資料（如 `DELETE FROM app WHERE ...` 不慎執行 cluster-wide）
- 邏輯損壞（migration bug）
- 需還原至特定時間點

### 8.2 流程摘要（細節於 `docs/runbooks/postgres-pitr.md`）

```
1. 停 backend（rolling 至 0 replica，避免新寫入）
2. 拉最近 base backup + WAL archive 至 staging Postgres
3. 設 recovery_target_time = <YYYY-MM-DD HH:MM:SS UTC>
4. 啟動 staging Postgres，等回放至 target time
5. 驗證資料正確性（手動 sample query）
6. 將 staging 資料 copy 至 main（pg_dump + pg_restore；或 promote staging 為 new main）
7. backend DATABASE_URL 指向 new main，rolling restart
```

預估 RTO：30 min（v1 演練目標）

### 8.3 演練

- M5 GA 前必演練一次完整 PITR；錄入 runbook
- 每季演練一次（屬 ops 排程）

## 9. Failover runbook

### 9.1 場景

- main pod crash 不可恢復
- main 所在 K3s node 失效
- main 資料損壞（rare；通常走 PITR）

### 9.2 流程摘要（細節於 `docs/runbooks/postgres-failover.md`）

```
1. 確認 main 無法恢復（K8s pod status / pg_isready 失敗 > 5 min）
2. 在 replica pod 執行 pg_ctl promote
   - 或 touch /var/lib/postgresql/data/standby.signal 之相反操作
3. 等 replica 完成 promote（< 30s）
4. UPDATE backend 之 DATABASE_URL Secret 指向 replica service
5. backend rolling restart（new pod 用新 DATABASE_URL）
6. （故障 main 修復後）作為新 replica 接回
```

預估 RTO：30 min（含人為決策時間）

### 9.3 v1.1 Patroni 評估

- Patroni 自動化 leader election + promote
- 引入 Patroni 為新運維面；需獨立 ADR 補充
- 候選 DCS（Distributed Configuration Store）：K8s API（避免引入新組件）

## 10. Migration safety

### 10.1 CI 攔阻

`make migrate-lint`：
- `goose status` + `goose validate`
- 自定 lint：`ALTER TABLE ... ADD COLUMN ... NOT NULL DEFAULT ...` 不允許（需拆 nullable + backfill + NOT NULL 三步）
- `ALTER INDEX ...` / `CREATE INDEX ...` 必含 `CONCURRENTLY`
- `DROP COLUMN` 必標 deprecated 至少 1 個 release 才允許

### 10.2 Staging 24h

- migration PR merge 後在 staging 環境跑過 24h 才能 deploy production
- staging 與 production 共用同一 chart（不同 K3s namespace + Postgres instance）
- 24h 內若有任何 staging incident → 阻擋 production deploy

### 10.3 Zero-downtime 模式（接續 plan.md）

```
1. 新欄位先 add column ... null
2. 雙寫 + backfill
3. 切換讀路徑
4. 標記舊欄位 deprecated（CI lint 警告）
5. 下一個 release drop column
```

## 11. 與其他 spec 接合點

| 接合 | spec |
|---|---|
| `r2-backup-credentials` Secret | `secrets-management` § 4 |
| `postgres-app-credentials` Secret + rotation | `secrets-management` § 4（A 類）|
| Replica lag → incident | `reconciler-and-incident` § 9.2 |
| Backend HA 與 Postgres 故障域 | `backend-ha-leader-election` |
| K3s NetworkPolicy（postgres pod 對 backend 開 5432）| `k3s-namespace-isolation` § 6 |
| Migration 走 goose | `dev-environment` spec § 5.2（migrations image 待 ADR-0009）|
| audit_log 對 PITR / failover 操作 | `audit-log` spec |
| metric `pg_replication_lag_seconds` | `observability-skeleton`（v1.1 補 postgres_exporter）|

## 12. 驗證準則

| 驗證項 | 方式 | 通過條件 |
|---|---|---|
| Streaming replication 起 | `pg_isready` on main + replica | 兩者皆 ready；`pg_stat_replication` 顯示 1 standby |
| WAL archive 觸發 | 跑 `SELECT pg_switch_wal()` | R2 bucket 5s 內出現新 WAL file |
| WAL archive 失敗回退 | mock R2 503 | archive_command 失敗；backend log warn；retry |
| Daily pg_dump | CronJob 觸發 | R2 bucket 出現 `<ts>.sql.gz`；可下載解壓 |
| PITR 演練 | M5 GA 前跑一次完整 PITR | 還原到指定時間，sample query 正確 |
| Failover 演練 | mock main 不可達 + 手動 promote replica | RTO < 30 min；backend 切換成功；無資料丟失 > RPO 5 min |
| Replica lag 偵測 | mock 大量寫入造成 lag > 60s | incident 自動建立 |
| Migration CONCURRENTLY lint | mock 一個 `CREATE INDEX` 無 `CONCURRENTLY` | CI fail |
| Migration NOT NULL 拆步 lint | mock 一步 ADD COLUMN NOT NULL | CI fail |
| Staging 24h 攔 | 跳過 staging 直接 deploy production | CI / CD pipeline 拒 |
| Anti-affinity | `kubectl get pod -o wide` | main 與 replica 不在同 node |
| Backend `DATABASE_URL` 變更後 rolling | patch Secret + rollout | new pod 用新 DSN；舊 pod graceful drain |

## 13. SLI 對應

| SLI | 目標 | 量測 |
|---|---|---|
| Replica lag p95 | < 5s | `pg_replication_lag_seconds` p95 |
| WAL archive 成功率 | > 99.9% | `pg_stat_archiver.fail_count / archived_count` |
| Daily pg_dump 成功率 | 100% / 28d | CronJob status |
| PITR / failover RTO | < 30 min | 演練實測 |
| RPO | < 5 min | WAL archive timeout = 5 min |
| Backend 因 Postgres failover 中斷時間 | < 30 min（可接受 ~70% 月度 budget）| ops 演練 |

## 14. 對 `docs/0ops-plan.md` 的修改清單

1. 「Postgres backup / DR」段：交叉引用本 spec 為實作 source
2. ADR-0008 § 4 第 5 點：plan.md 補入「`synchronous_commit=off` async 複製」（明確 RPO 5 min 之含義）
3. 補 chart 範本路徑 `deploy/chart/postgres/`
4. 「Risks & open」：補入「v1 + M5 手動 failover 對 SLO budget 為高風險（單次 30 min ≈ 75% 月度 budget）」

## 15. Open issues

> 來源：ADR-0008 § 9 之 8 條 OQ 中與 Postgres 相關者 + 本 spec 撰寫期間發現

- ADR-0008 OQ#4（Patroni 範圍）：v1.1 評估
- ADR-0008 OQ#3（Read replica 暴露路徑）：v1 不暴露；M5 後評估
- WAL-G / pgbackrest 替代 archive_command：v1.1 評估
- Postgres 跨 region replica：v2
- PostgreSQL 17 升 18：屬 ops 升版時程
- pgBouncer 引入：v1 不採；M5 後若 connection 數爆炸再評估
- Postgres 物理升版（major version）流程：屬 ops runbook，需獨立準備
- Replica 是否需 cascading（多級 replica）：v1 single replica；v1.1 評估
- WAL 加密（在 R2 server-side 之外）：v2 評估
- Backup 完整性驗證（restore test 自動化）：v1 手動；v1.1 自動化
- 連線數監控：max_connections=100；backend pgxpool 上限 50；剩 50 給 ops + 監控
- Postgres 之 Pod resource request / limit：本 spec 預留 chart 範本，數值由 ops runbook 落地

## 16. 不可違反的硬性規則

> 違反以下任一項，PR 不可合入。

1. Application Postgres 必為 main + replica 拓樸（M5 GA 前到位）；不得 single Postgres 上 production
2. Anti-affinity 必 `requiredDuringSchedulingIgnoredDuringExecution`；main 與 replica 必跨 node
3. WAL archive `archive_timeout` 必 ≤ 300s；保證 RPO ≤ 5 min
4. Daily pg_dump 必跑（CronJob）；失敗即 alert
5. PITR / failover runbook 必演練（M5 GA 前 + 每季）；無演練即不可宣稱 RTO 30 min
6. Migration 必經 staging 24h 才上 prod；CI 攔
7. ALTER 大表必 CONCURRENTLY；CI 攔
8. ADD COLUMN NOT NULL 必拆 3 步；CI 攔
9. WAL archive 至 R2 bucket lifecycle 必設 30 天；不得無限增長（成本）
10. `DATABASE_URL` 變更後必 rolling restart backend；不得熱切換 connection pool（避免 mid-request 切換造成 inconsistency）
