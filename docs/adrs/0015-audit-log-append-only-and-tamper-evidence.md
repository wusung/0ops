---
adr: "0015"
title: Audit Log Append-Only and Tamper-Evidence
status: Proposed
date: 2026-06-28
tags:
  - audit
  - compliance
  - security
  - integrity
supersedes: []
superseded-by: []
---

# ADR-0015：Audit Log Append-Only and Tamper-Evidence

* Status：Proposed（對應 spec 為 draft；尚未實作。承 `docs/trust-and-compliance/plan.md` § 6 規則 1，狀態誠實標示，不得對外宣稱已具備）
* Date：2026-06-28
* 適用範圍：`audit_log` 寫入路徑之完整性保證；DB role 權限模型；partition / archive 互動
* 來源：`docs/trust-and-compliance/plan.md` § 5.1（P0）；`docs/features/threat-model/spec.md` § 5.7 AD1（H 級威脅）；對應 spec [`docs/features/audit-export-and-integrity/spec.md`](../features/audit-export-and-integrity/spec.md)
* 上游依賴：[ADR-0001](0001-multi-tenancy-and-rbac.md)（team 隔離、RBAC）；[ADR-0009](0009-migrations-image-strategy.md)（goose migration）；既有 [`docs/features/audit-log/spec.md`](../features/audit-log/spec.md)（schema、partition、redact、reconciliation_job fallback）

## 0. TL;DR（先讀本段）

採用以下四項組合決策：

1. **DB-level append-only**：application DB role 撤 `UPDATE` / `DELETE` on `audit_log`（與其所有 partition）；只保留 `INSERT` / `SELECT`。任何竄改/刪除企圖在 DB 權限層被拒，而非靠程式自律。
2. **Per-row hash chain（tamper-evidence）**：每 row 加 `prev_hash` / `row_hash`；`row_hash = H(canonical(row 內容含 prev_hash))`，對 **redact 後**內容計算，使 export 與 verify 結果一致。chain 偵測「整段被刪」或「row 被改」。
3. **Archive 走獨立 ops role**：`delete_app` 永久保留與 partition drop（audit-log § 9）所需的「搬移/刪 partition」由獨立 `audit_ops` role 執行，與 app role 隔離；app role 永遠無刪權。
4. **Chain 範圍為 per-team 單鏈，跨 partition 連續**：hash chain 以 `(team_id)` 為鏈，跨月 partition 連續串接；partition drop 時保留被 drop partition 的最後一個 `row_hash` 作為 anchor 寫入 `audit_chain_anchor`，使 verify 不因 drop 而誤判斷裂。

行為與 schema/migration/API 細節以 spec [`docs/features/audit-export-and-integrity/spec.md`](../features/audit-export-and-integrity/spec.md) 為準；本 ADR 釘住決策邊界，不重述 spec。

## 1. Context and Problem Statement

既有 `audit-log` spec 已建立業務行為帳本：全寫入/刪除入帳、redact、13mo partition、`delete_app` 永久 archive、reconciliation_job 重寫 fallback。但威脅模型 § 5.7 標出一條 **H 級殘留威脅 AD1**：持有 application DB 連線（或 infra 權限）者可 `UPDATE` / `DELETE` audit_log row 掩蓋行為，且**無痕跡可偵測**。

對 enterprise 與 SOC2 / 個資法取證而言，「帳本可被竄改且查不出」等於沒有帳本。需要兩層保護：

1. **預防**：讓正常運作的 application 路徑根本沒有 audit_log 的改/刪權限。
2. **偵測**：即使有人繞過 app role（直連 DB superuser），其竄改也應能被事後驗證偵測。

現況缺：app role 仍持 `UPDATE`/`DELETE`（M5 schema 預設 grant）；無任何 row 完整性證明；archive / partition drop 流程目前假設執行者有刪權，與「append-only」直接衝突，需重新切分權限。

## 2. Decision Drivers

* **DD1 預防優於自律**：完整性不能只靠「程式不會去改」；要在 DB 權限層強制 app role 無改/刪權。
* **DD2 偵測可獨立驗證**：完整性證明必須能被第三方（審計員）獨立重算，不依賴 0ops 自我聲明。
* **DD3 與既有 redact 一致**：hash 必須對 redact 後內容計算，否則 export（redact 後）與 verify 結果不一致，且原始未 redact 內容不該離開 DB。
* **DD4 不破壞既有保留/archive 語意**：13mo drop 與 `delete_app` 永久保留（audit-log § 9）必須在 append-only 下仍可運作。
* **DD5 寫入路徑零行為變更**：`audit.Log()` 介面（audit-log § 5.3）對呼叫端不變；hash 計算在寫入層內部完成。
* **DD6 效能可接受**：hash chain 引入寫入序列化風險（需讀前一筆 `row_hash`）；設計須避免成為高頻寫入瓶頸。

## 3. Decision Outcome

### 3.1 Append-only DB role 模型

| Role | audit_log 權限 | 用途 |
|---|---|---|
| `app`（server 運行身分） | `INSERT`, `SELECT` | 正常寫入與查詢；**無 `UPDATE`/`DELETE`** |
| `migrate`（goose 遷移身分） | DDL（建表、加欄、建 partition） | schema 演進；不跑業務寫入 |
| `audit_ops`（背景 archive/rollover 身分） | `SELECT`, `INSERT`（archive 表）, partition `DETACH`/`DROP` | partition rollover、delete_app row 搬 archive |

`app` role 之 grant 由 migration 明確撤銷；未來新增 partition 時繼承父表權限（PostgreSQL partition 權限繼承），確保新月份 partition 同樣 app 無刪權。

### 3.2 Hash chain schema 與計算

`audit_log` 新增兩欄（spec 定 migration 編號）：

```sql
ALTER TABLE audit_log
  ADD COLUMN prev_hash bytea,   -- 同 team 前一筆之 row_hash；team 首筆為 NULL
  ADD COLUMN row_hash  bytea;   -- 本筆之 hash
```

`row_hash = SHA-256( canonical_json({ team_id, actor_user_id, source, subject_type, subject_id, action, args, result, preview_id, trace_id, outcome, http_status, created_at, prev_hash }) )`

* `args` / `result` 為**已 redact** 內容（DD3）。
* `canonical_json`：欄位固定排序、無多餘空白、時間 RFC3339 UTC，確保決定性。
* 寫入層在同一 transaction 內：取該 team 最新 `row_hash`（`SELECT ... ORDER BY id DESC LIMIT 1 FOR UPDATE` 或維護 per-team head 表）→ 算 `row_hash` → INSERT。

### 3.3 Chain 範圍與 partition / archive anchor

* Chain 以 `(team_id)` 為單位，跨月 partition 連續。
* `audit_chain_anchor(team_id, anchored_partition, last_row_hash, anchored_at)`：partition drop 前，`audit_ops` 把該 partition 該 team 的最後 `row_hash` 寫入 anchor；verify 跨越被 drop 區間時以 anchor 接續，不誤判斷裂。
* `delete_app` row 搬 `audit_log_archive` 時保留其 `prev_hash`/`row_hash`；archive 表獨立 verify。

### 3.4 Export 與 verify

* Export（`GET .../audit/export`）輸出 redact 後 row + chain head hash + 範圍；審計員可離線重算驗證。
* `0ops audit verify`：重算指定範圍 chain，比對 `row_hash` 與 anchor，回報斷裂位置（若有）。
* 細節（RBAC scope、格式、串流上限）以 spec 為準。

### 3.5 完整性違規之處置

verify 偵測到斷裂/不符時：發 `audit_integrity_violation` 事件（入 audit_log 本身 + metric + 告警）；不自動「修復」（修復等於再次竄改）；屬 incident，走 reconciler-and-incident 通報。

## 4. 與既有 audit-log spec 之關係

* audit-log § 5.3 `audit.Log()` 介面**不變**（DD5）；hash 計算為實作內部新增。
* audit-log § 9 保留期（13mo drop、delete_app 永久 archive）**保留**，但執行身分由 app role 改為 `audit_ops` role（§ 3.1）。
* audit-log § 10 「寫入失敗 → reconciliation_job 重寫」fallback **保留**；補充：重寫時 chain 順序以 `created_at` + 補寫標記維持（spec 定 tie-break）。
* audit-log § 15 硬性規則新增一條（append-only），由本 ADR 授權 spec 落地。

## 5. Pros and Cons of the Options

| 方案 | 描述 | 採用 |
|---|---|---|
| **A. DB append-only + per-row hash chain（本 ADR）** | 撤 app role 改/刪權 + hash chain 偵測 | ✅ |
| B. 外部 WORM / append-only object storage | audit 即時複寫到 WORM（如 S3 Object Lock） | ✗（v1） |
| C. 僅靠存取控制 + 稽核 DB 操作 | 不改 schema，靠 DB 權限 + DB 自身 audit | ✗ |

### A（採用）
**Pros**：預防（無刪權）+ 偵測（hash chain）雙層；審計員可離線獨立驗證（DD2）；不需新基礎設施；與既有 partition/redact 相容。
**Cons**：hash chain 引入 per-team 寫入序列化點（DD6 風險）；superuser 仍可同時改 row 與重算後續整條 chain（但需 app role 外的高權限 + 對全 chain 重算，門檻與可偵測性大幅提高）；migration 需謹慎處理既有無 hash 的歷史 row（backfill 或標 chain 起點）。

### B（否決，列 Revisit）
真正防得了 superuser，但 v1 引入外部 WORM 儲存與複寫管線成本過高；對應威脅（內部高權限者）尚非首批 enterprise 的主訴求。列 Revisit Trigger。

### C（否決）
不改 schema 最省事，但無 tamper-evidence；DB 自身 audit 可被同一 superuser 關閉，達不到 DD2「可獨立驗證」。

## 6. Consequences

### 6.1 正面
* AD1 從 H 降為「需 superuser + 全 chain 重算且仍可能被 anchor/外部備份偵測」的殘留風險。
* 提供 SOC2 CC7 / 個資法「安全維護」可出示證據（接 `compliance-framework-mapping`）。
* Export + verify 使 audit 成為對外可交付的取證資產（解 AD3）。

### 6.2 負面
* 寫入路徑新增 per-team head 讀取；高頻同 team 寫入需評估鎖競爭（DD6）；spec 須給 head 維護策略（專表 vs 索引查詢）與壓測門檻。
* archive / rollover 須改用 `audit_ops` role；部署需多配一組受限憑證。
* 既有歷史 row 無 hash：migration 須定義 backfill 規則或以「chain 起點」標記，且起點之前不提供完整性保證（明示）。

### 6.3 中性
* superuser 級別之防護不在本 ADR 範圍；由方案 B（Revisit）或 infra 層 DB 存取治理承接。
* `audit_integrity_violation` 為新事件類別，入既有 audit + incident 通道。

## 7. Revisit Triggers

* **enterprise 要求防 superuser**：若 design partner / SOC2 audit 明確要求抵抗 DB superuser 竄改 → 評估方案 B（外部 WORM / Object Lock）或定期 anchor 到外部 transparency log。
* **寫入吞吐瓶頸**：若 per-team hash chain 序列化成為高頻 team 的寫入瓶頸（metric：audit insert p95 上升）→ 評估 chain 分段或非同步 anchor。
* **跨 region 複寫**：managed 多 region 時 chain 連續性與 region 間順序需重新設計。

## 8. More Information

* **Feature spec**：[`docs/features/audit-export-and-integrity/spec.md`](../features/audit-export-and-integrity/spec.md)（schema、migration、export API、verify CLI、驗證準則以本檔為準）
* **既有 audit-log spec**：[`docs/features/audit-log/spec.md`](../features/audit-log/spec.md)（寫入點、partition、redact、保留期；本 ADR 在其上加完整性保證）
* **威脅模型**：[`docs/features/threat-model/spec.md`](../features/threat-model/spec.md) § 5.7 AD1/AD2/AD3
* **統籌計畫**：[`docs/trust-and-compliance/plan.md`](../trust-and-compliance/plan.md) § 5.1
* **ADR-0001**：[0001-multi-tenancy-and-rbac.md](0001-multi-tenancy-and-rbac.md)（team 隔離；chain 以 team_id 為單位承此邊界）

## 9. Open Questions

1. **per-team chain head 維護**：用獨立 `audit_chain_head(team_id, last_hash, last_id)` 表（寫入時 `UPDATE ... RETURNING`）還是每次查最後一筆？前者較快但 head 表本身需保護；spec 決定。
2. **歷史 row backfill**：對既有無 hash row 是否 backfill 計算 chain，還是以遷移時點為 chain 起點？backfill 需鎖表，起點方案則起點前無保證。建議起點方案 + 明示限制。
3. **anchor 外部化時機**：是否定期把 per-team chain head 簽章後寫到 append-only 外部（git commit / 物件儲存 / transparency log）以抵抗 superuser？v1 不做，列 Revisit。
4. **verify 的計算成本**：跨 13 個月全量 verify 可能昂貴；是否提供「自上次 anchor 起增量 verify」。
5. **redact 規則演進對既有 hash 的影響**：若 redactor 規則變更，舊 row 的 redact 後內容改變會使重算 hash 不符；須凍結「hash 對寫入當下 redact 結果計算」且不回溯重算（spec 釘死）。
