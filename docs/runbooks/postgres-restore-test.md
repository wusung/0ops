# Runbook：Postgres restore 演練清單

> 對應 spec：`docs/features/postgres-ha-and-dr/spec.md` § 8.3 + § 16 hard rule #5
> 對應 ADR：ADR-0008 § 4 第 6 點

## 1. 目的

驗證 spec § 13 SLI 中下列三條的可達性：

- PITR / failover RTO < 30 min
- RPO < 5 min
- 每季演練保持運維肌肉

## 2. 演練類型

| 類型 | 範圍 | 頻率 | 演練腳本 |
|---|---|---|---|
| Local PITR drill | podman compose 上的迷你 main → staging | 每次 chart 變更 | `deploy/postgres/scripts/pitr-drill.sh` |
| Full PITR drill  | staging cluster；R2 WAL + logical dump | M5 GA 前一次、之後每季 | 本文件 §4 |
| Failover drill   | staging cluster；mock main 不可達 | M5 GA 前一次、之後每季 | `postgres-failover.md` |

## 3. Local PITR drill（每次 chart / script 變更）

```bash
./deploy/postgres/scripts/pitr-drill.sh
```

驗收條件：

- [ ] 腳本 exit 0
- [ ] 報告檔 `/tmp/0ops-pitr-drill-<timestamp>.log` 顯示 canary row 在 `recovery_target_time` 後仍存在；後一筆 DELETE 未生效
- [ ] WAL archive 觸發 ≥ 1 次（`pg_switch_wal()` + archive_command 成功）

## 4. Full PITR drill（M5 GA 前 + 每季）

### 4.1 準備（D-1）

- [ ] 確認 staging cluster 有 spare PVC（≥ main data size × 1.2）
- [ ] R2 `0ops-pg-wal/prod/main/wal/` 與 `0ops-pg-dump/prod/` 連續可用至少 24h
- [ ] oncall 與 stakeholder 已知會
- [ ] 演練窗口已 freeze（避免 prod merge 干擾 audit）

### 4.2 演練（D-day）

依 `postgres-pitr.md` Step 1–6 在 staging 跑一次完整 restore：

- [ ] 寫一筆 canary row 至 prod main（已知 timestamp T_canary）
- [ ] 等 ≥ 5 min（WAL archive 一次完整 segment）
- [ ] 從 prod 拉 base backup + WAL archive 至 staging
- [ ] 設 `recovery_target_time = T_canary + 30s`
- [ ] 啟 staging Postgres，等回放 pause
- [ ] 在 staging 查 canary row → 必須存在
- [ ] 在 staging 查 canary row 之後的「破壞」row（如有）→ 必須不存在
- [ ] 量測 RTO（Step 1 起算到 Step 6 通過）

### 4.3 收尾

- [ ] 把演練 log 附加到本文件 §5
- [ ] 若 RTO > 30 min，開 incident（spec § 13）並 revisit ADR-0008 (f)
- [ ] 若發現 WAL gap → 立即修；revisit `archive_command` 失敗 retry 機制

## 5. 演練紀錄

> 每次演練後 append 一個 ## section；不要刪除歷史紀錄。

### 5.1 2026-05-16 — Local PITR drill（M5.4 chart bootstrap）

- 環境：worktree-only podman compose；R2 mocked by local minio container
- 範圍：腳本路徑覆蓋（chart 第一次建置；prod 演練於 M5 GA 前另排）
- 工件：`deploy/postgres/scripts/pitr-drill.sh`、`docs/runbooks/postgres-pitr.md`
- 狀態：腳本 + runbook 已就位；full prod-style drill 排於 M5 GA 前完成（owner: ops）
- 待跑項：
  - [ ] Full PITR drill on staging cluster（M5 GA 前）
  - [ ] Full failover drill on staging cluster（M5 GA 前）
