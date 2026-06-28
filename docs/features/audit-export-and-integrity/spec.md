# Feature Spec：audit-export-and-integrity

> **狀態**：draft
> **來源**：`docs/trust-and-compliance/plan.md` § 3.2 / § 4 / § 5.1（P0）；`docs/features/threat-model/spec.md` § 5.7（AD1/AD2/AD3）；引用 **ADR-0015**（append-only + hash chain 架構決策，另案撰寫，本 spec 僅引用編號）
> **適用範圍**：audit_log 之 append-only 強化、per-row tamper-evidence（hash chain）、completeness 對外聲明、export（CSV/JSON）端點、完整性驗證 CLI；不含 SIEM 串接、對外 webhook 通知（屬 `audit-event-notification`，v2/P3）
> **依賴**：`audit-log`（schema、partition、redactor、reconciliation_job fallback 為本 spec 之基底，皆已實作於 migration `00007`、`00008`）、`auth-and-rbac`（role/scope）、`error-model`（redactor）、`observability-skeleton`（trace_id）
> **對應 Milestone**：P0（plan § 5.1）；append-only 為 P0 立即項，hash chain + export 接續同 spec 內交付
> **讀法**：§ 1 結論 → § 4 schema/hash → § 5 append-only → § 6 export → § 7 verify → § 13 硬性規則

## 1. 結論（先讀本段）

- 本 spec 解 `threat-model` § 5.7 三條威脅，皆為「對外可出示」缺口，非新業務行為：
  - **AD1（H 級）**：高權限者持 app DB 連線可 `UPDATE`/`DELETE` audit_log 掩蓋行為。解法雙層：(a) **append-only**——application DB role 撤 `UPDATE`/`DELETE` on audit_log（migration `00013` + role 分離）；(b) **per-row tamper-evidence**——每 row 加 `prev_hash`/`row_hash`，以 SHA-256 串成 hash chain，竄改任一 row 使其後全鏈 hash 不符。
  - **AD2**：寫入失敗→無紀錄（抵賴）。本 spec 不重做 fallback（`audit-log` 已以 reconciliation_job 重寫，落於 `00008`），只補「無漏」對外聲明與可驗證機制：completeness 由 hash chain 的 `audit_chain_head.row_count` + 連續 `id` 缺口偵測佐證。
  - **AD3**：無對外可出示證據。解法：**export（CSV/JSON）** 端點，帶 RBAC + 時間範圍限定 + 既有 redact 已套用 + integrity 摘要（chain tip hash + 範圍 + row_count）。
- **hash 對 redact 後內容計算**：raw 內容從不落地（`audit-log` § 8），故 row_hash 必須涵蓋「實際儲存的 redacted 欄位」，確保 export 出示的內容與 verify 重算一致。
- **鏈策略選定 per-(team, month-partition) chain**，以 `audit_chain_head` anchor 表錨定每條鏈的 genesis 與 tip。理由：與既有月度 partition + 13mo drop 相容——drop 整個 partition 不會留下 dangling `prev_hash`；全域單鏈於 drop 後無法續驗，列為 Open issue（§ 12）被否選項。
- **append-only 與 archive 並存**：app role 無刪權；`delete_app` 永久保留之搬遷（`audit-log` § 9.2）與 partition drop 改由獨立 **archive role / ops 程序**執行，不破壞 app role 的無刪不變式。
- export 與 verify 為**取證/審計交付介面**，非 agent 操作面：export 用新 scope `audit:export`；verify 為 CLI/operator 工具，**不**暴露 MCP（§ 7.3 理由）。

## 2. 範圍

### 2.1 包含
- audit_log 加欄位 `prev_hash bytea`、`row_hash bytea`；新增 `audit_chain_head` anchor 表（migration `00013`）。
- hash 計算規則（涵蓋欄位、決定性序列化、與 redaction 先後、genesis anchor）。
- DB role 分離（`0ops_app` / `0ops_migrate` / `0ops_archive`）與 grant 撤銷清單。
- append-only 下的 archive / partition drop 程序（archive role）。
- export API `GET /v1/teams/{slug}/audit/export`（CSV/JSON、串流、整合摘要）。
- 完整性驗證 CLI `0ops audit verify`。
- 與 partition drop / 13mo 保留 / archive 的互動規則。

### 2.2 不包含
- audit 寫入點與既有 query API（屬 `audit-log`；本 spec 不改寫入語意，僅在 INSERT 時補算 hash）。
- 對外 webhook / email 通知（屬 `audit-event-notification`，P2）。
- SIEM / syslog push（plan § 3.2，P3）。
- 外部 transparency log anchor（§ 12 Open issue）。
- reconciliation_job fallback 之新增（已存在；本 spec 只引用作 completeness 佐證）。

## 3. 檔案結構

```
0ops/
├── src/
│   ├── internal/
│   │   ├── server/
│   │   │   ├── services/audit/
│   │   │   │   ├── chain.go            # 本 spec 引入：hash 計算 + 決定性序列化 + chain head 讀寫
│   │   │   │   ├── chain_test.go
│   │   │   │   ├── export.go           # 本 spec 引入：CSV/JSON 串流 + integrity 摘要
│   │   │   │   ├── export_test.go
│   │   │   │   ├── verify.go           # 本 spec 引入：chain 重算與斷裂偵測（CLI 共用）
│   │   │   │   ├── verify_test.go
│   │   │   │   ├── log.go              # 既有：Insert 路徑改為呼 chain.go 補 hash（最小改動）
│   │   │   │   ├── partition.go        # 既有：rollover 改由 archive role；新 partition 套 REVOKE
│   │   │   │   └── redact.go           # 既有：hash 在 redact 之後算
│   │   │   ├── db/
│   │   │   │   └── audit.go            # 既有：InsertAuditLog 帶 prev/row hash；加 ChainHead*、ExportRows
│   │   │   ├── audit_handlers.go       # 既有：query handler
│   │   │   └── audit_export_handlers.go # 本 spec 引入：GET .../audit/export
│   │   ├── cli/
│   │   │   └── audit.go                # 既有：加 `verify`、`export` 子命令
│   │   └── shared/
│   │       └── dto/audit.go            # 既有：加 ExportManifest / IntegritySummary
│   └── migrations/
│       └── 00013_audit_log_hash_chain.sql  # 本 spec 引入
```

> **接合既有實作**：實際 package 路徑為 `internal/server/services/audit/`（非 `audit-log` § 3 所列 `internal/server/audit/`）；本 spec 以實際路徑為準，差異見 § 11 findings。

## 4. Schema 與 hash chain

### 4.1 欄位變更（migration `00013`）

```sql
alter table audit_log
    add column prev_hash bytea,   -- 同一 (team, partition) 鏈中前一 row 的 row_hash；genesis row 為 anchor
    add column row_hash  bytea;   -- 本 row 的 SHA-256；NOT NULL 由 application 寫入保證（見 § 4.4）
```

- 既有 row（backfill 自 `00007`）之 `prev_hash`/`row_hash` 為 `NULL`：標示為 **pre-chain** 區段，verify 對其只做存在性檢查、不做 linkage（§ 7.2）。新鏈自 `00013` 上線後的首筆起算。
- 不對 audit_log 加 `CHECK row_hash IS NOT NULL`：partition 內混有 pre-chain 與 chained row；NOT NULL 不變式由 application 寫入路徑（§ 4.4）保證，並由 verify 偵測缺漏。

### 4.2 anchor 表 `audit_chain_head`

```sql
create table audit_chain_head (
    team_id         uuid not null references team(id) on delete cascade,
    partition_month date not null,              -- partition 起始月（UTC，與 created_at 分區邊界對齊）
    genesis_hash    bytea not null,             -- H(domain_sep || team_id || partition_month)
    tip_hash        bytea not null,             -- 當前鏈尾 row_hash；INSERT 時更新
    row_count       bigint not null default 0,  -- completeness 佐證（AD2）
    first_row_id    bigint,
    last_row_id     bigint,
    updated_at      timestamptz not null default now(),
    primary key (team_id, partition_month)
);
```

- 一條 `(team_id, partition_month)` = 一條獨立 hash chain。
- **`audit_chain_head` 永不 drop**：即使對應 partition 於 13mo 後 drop（§ 8），head row 保留作為「該月曾存在、共 N 筆、tip 為 H」的durable 證明，使 verify 在 row 已消失後仍能出示鏈尾與筆數（AD2 completeness）。

### 4.3 hash 計算（決定性）

- 演算法：**SHA-256**。
- `genesis_hash = SHA256( "0ops-audit-chain-v1" || team_id_bytes || partition_month_iso )`——純由不變的 partition 座標導出，無需儲存秘密；任何驗證者可獨立重算。
- `row_hash = SHA256( prev_hash || canonical(core) )`，其中 genesis row 的 `prev_hash = genesis_hash`。
- `core` 涵蓋欄位（**全部為 redact 後之儲存值**）：`id, team_id, actor_user_id, source, subject_type, subject_id, action, args, result, preview_id, trace_id, outcome, http_status, created_at`。`prev_hash`/`row_hash` 本身不入 `core`。
- **決定性序列化 `canonical()`**：
  1. 物件 key 以 UTF-8 byte 順序遞增排序（含 `args`/`result` 內巢狀 JSON 遞迴排序）。
  2. `created_at` 一律 RFC3339 UTC、奈秒截斷至微秒（與 Postgres `timestamptz` 精度一致）。
  3. `NULL` 欄位顯式輸出 `null`（不省略），避免「缺欄位」與「值為 null」碰撞。
  4. 數值不含前導零、無 `+`；`bytea` 不入 core（故無編碼歧義）。
  5. 欄位以固定 schema 順序串接，分隔符 `0x1F`（unit separator），防止欄位邊界注入（如 `a|b` vs `ab|`）。
- **hash 在 redact 之後算**：`log.go` 路徑為 `redact → canonical → hash → INSERT`；確保儲存內容、export 出示內容、verify 重算內容三者位元一致（§ 1）。

### 4.4 寫入路徑（最小改動 `log.go` / `db/audit.go`）

INSERT 於單一 transaction 內：

```
BEGIN
  -- 同一 (team, month) 鏈序列化：鎖 head row
  SELECT tip_hash, genesis_hash FROM audit_chain_head
    WHERE team_id=$1 AND partition_month=$2 FOR UPDATE;
  -- 不存在 → INSERT head（genesis_hash 由 § 4.3 導出，tip=genesis）
  prev := COALESCE(tip_hash, genesis_hash)
  row_hash := SHA256(prev || canonical(core))   -- core 含 INSERT 後分配的 id（見下）
  INSERT INTO audit_log (..., prev_hash=prev, row_hash=row_hash)
  UPDATE audit_chain_head SET tip_hash=row_hash, row_count=row_count+1,
         last_row_id=<id>, first_row_id=COALESCE(first_row_id,<id>), updated_at=now()
COMMIT
```

- `id` 由 `bigserial` 於 INSERT 時分配；`core` 含 `id`，故採 INSERT ... RETURNING id 後，hash 在 application 端以 RETURNING 之 id 補算寫回為單一 statement 困難——改為：先 `nextval` 取 id，再以該 id 組 core 算 hash，最後帶 `id`/`prev_hash`/`row_hash` 顯式 INSERT（不依賴 default）。此為 `db/audit.go` 之 `InsertAuditLog` 唯一語意變更。
- 序列化點為 per-`(team, month)` 的 head row lock；非全域。webhook_received 等高頻事件之 per-team 競爭由 head row lock 串接，contention 與 `audit-log` § 14「webhook_received 高頻」open issue 同一風險面，未放大跨 team。
- **append-only 不需 UPDATE audit_log**：chaining 僅在 INSERT 時寫入 `prev_hash`/`row_hash`；tip 更新落在 `audit_chain_head`（app role 對該表保有 `UPDATE`）。故撤 audit_log 之 `UPDATE` 不影響鏈寫入。

## 5. Append-only 強化

### 5.1 DB role 分離（本 spec 引入；現況為單一 owner 連線）

| Role | 用途 | audit_log 權限 | audit_chain_head 權限 |
|---|---|---|---|
| `0ops_app` | backend runtime 連線（受 agent 間接驅動） | `SELECT, INSERT`（**撤 `UPDATE, DELETE`**） | `SELECT, INSERT, UPDATE` |
| `0ops_migrate` | goose migration（deploy 期，DDL） | 全權（建 partition、backfill、加欄位） | 全權 |
| `0ops_archive` | ops audit-rollover / archive job（特權批次，非 runtime） | `SELECT, INSERT, DELETE` + partition `DROP`（經 `0ops_migrate` 委派 owner 或 `SECURITY DEFINER` 函式） | `SELECT, UPDATE` |

```sql
-- 00013：撤 app role 對 audit_log 的改/刪權（parent + 既有 partitions + 未來 partitions）
revoke update, delete on audit_log from 0ops_app;
-- 對既有每個 partition 逐一 revoke（防直連 partition 繞過 parent 檢查）
revoke update, delete on
    audit_log_history, audit_log_2026_01, audit_log_2026_02, audit_log_2026_03,
    audit_log_2026_04, audit_log_2026_05, audit_log_2026_06, audit_log_2026_07,
    audit_log_2026_08 from 0ops_app;
-- 未來 partition：rollover job（partition.go）建表後即套 revoke；並設 default privileges
alter default privileges for role 0ops_migrate in schema public
    revoke update, delete on tables from 0ops_app;
grant select, insert on audit_log to 0ops_app;
```

- **信任假設**：append-only 防的是「持 app runtime 憑證者」（threat-model A4 經 app 連線、AG2 token 外洩後的爆炸半徑）。持 `0ops_migrate`/`0ops_archive` 憑證者（deploy/ops 特權，不暴露於 runtime 或 agent）在威脅模型內視為信任核心，不在 append-only 防護對象內；此為明示殘餘風險（§ 9）。
- **部署相依**：runtime 連線字串必須切換為 `0ops_app`（目前為 owner role）。此為 ops config 變更，非程式變更，列 § 10 接合與 § 12 Open issue；migration 上線與連線切換須同批，否則撤權對 owner 無效。

### 5.2 archive / partition drop 在 append-only 下的程序

- `delete_app` 永久保留（`audit-log` § 9.2）：rollover 於 drop partition 前，由 `0ops_archive` role 執行 `ArchiveDeleteAppRows`（`db/audit.go` 既有）——`INSERT INTO audit_log_archive SELECT ... WHERE action='delete_app'`，**連同 `prev_hash`/`row_hash` 一併複製**（archive 表加此二欄，`00013` alter）。
- partition 整體移除以 `DROP TABLE audit_log_<month>` 完成（`0ops_archive` 特權）；不需逐 row `DELETE`，故 app role 無 `DELETE` 不矛盾。
- app role 無 `DELETE` 不影響 archive：archive 由獨立 role 執行；rollover job 不以 app 連線跑。

## 6. Export API

### 6.1 Endpoint

```
GET /v1/teams/{team_slug}/audit/export?format=csv|json&since=&until=&cursor=
```

| Param | 說明 |
|---|---|
| `format` | `csv`（預設）或 `json` |
| `since` | RFC3339；**必填**（防無界全表掃描） |
| `until` | RFC3339；預設 now；`until - since` 上限 13 個月（= 保留窗，超出 422） |
| `cursor` | 串流續傳游標（大範圍分塊；不透明，編 `(created_at, id)`） |

### 6.2 RBAC（決定：新增 scope `audit:export`）

- 最低 role：`admin`；scope：**`audit:export`**（新增，非沿用 `audit:read`）。
- **理由**：export 為 bulk 取證萃取，外流風險與單筆/分頁 read 不同量級；SOC2/取證情境要求「export」為可獨立授予/撤銷之特權，故與 `audit:read` 分離，預設不綁定。`auth-and-rbac` 之 `rbac.Scope` 加 `ScopeAuditExport = "audit:export"`，`Action` 加 `ActionExportAudit{MinRole: RoleAdmin, RequiredScope: ScopeAuditExport}`。
- 無 self-service export（不提供 member 匯出自己；export 為 team 級審計動作，恆 admin）。

### 6.3 串流與大小限制

- 回應為 chunked streaming（不全量 buffer）；DB 端以 keyset pagination（`(created_at, id)`）逐塊掃，避免大 OFFSET。
- 單一 response 軟上限 100k row 或 50 MB（先到為準）；達上限回傳 `next_cursor`，客戶端續拉。
- `Content-Type`：`text/csv`（csv）/ `application/json`（json）。

### 6.4 Integrity 摘要（AD3 核心）

- 每次 export 回傳涵蓋範圍的 integrity 摘要（出示給審計員的「這批未被竄改」證據）：

```jsonc
// IntegritySummary（dto/audit.go）
{
  "team_slug": "acme",
  "range": {"since": "...", "until": "..."},
  "row_count": 1234,
  "chains": [   // 範圍觸及之每條 (team, month) 鏈
    {"month": "2026-05", "genesis_hash": "<hex>", "tip_hash": "<hex>", "row_count": 920},
    {"month": "2026-06", "genesis_hash": "<hex>", "tip_hash": "<hex>", "row_count": 314}
  ],
  "generated_at": "...",
  "generator": "0ops-server/<version>"
}
```

- **JSON format**：envelope `{ "manifest": IntegritySummary, "entries": [AuditLogEntry...] }`。
- **CSV format**：CSV body 為 entries；manifest 置於 response header `X-0ops-Audit-Integrity`（base64(JSON))，避免污染 CSV 欄位。
- 摘要中的 `tip_hash`/`row_count` 取自 `audit_chain_head`；審計員可獨立跑 `0ops audit verify`（§ 7）對該批 hash 重算比對，形成「export ↔ chain ↔ verify」閉環。

## 7. 完整性驗證 CLI

### 7.1 命令

```
$ 0ops audit verify --team=acme --since=2026-05-01 --until=2026-07-01
chain acme/2026-05  rows=920  genesis=ab3f…  tip=9c21…  OK
chain acme/2026-06  rows=314  genesis=77de…  tip=BREAK at id=18472 (row_hash mismatch)
verify FAILED: 1 chain broken
exit 1
```

- 逐 `(team, month)` 鏈：自 genesis_hash 起，依 `id` 遞增重算 `row_hash = SHA256(prev || canonical(core))`，比對 (a) 儲存 `row_hash`、(b) 下一 row 的 `prev_hash == 本 row row_hash`（linkage）、(c) 鏈尾 == `audit_chain_head.tip_hash`、(d) 重算筆數 == `row_count`（偵測整段 row 被刪：`id` gap 或 count 不符）。
- 偵測能力：單 row 竄改（row_hash 不符）、刪 row（linkage 斷 + count 不符）、插入 row（linkage 斷）、重排（id 序與 hash 序不符）、整鏈尾截斷（tip 不符）。
- **跨 partition verify**：每鏈獨立驗，互不依賴；`--since/--until` 跨多月時逐鏈報告，任一鏈 BREAK → exit 1。
- **pre-chain 區段**（§ 4.1，`row_hash IS NULL`）：只報 `rows=N pre-chain (no tamper-evidence)`，不計入 BREAK。

### 7.2 archive 與已 drop partition 的驗證界線

- 已 drop 之 partition：row 不存在，無法重算 linkage；verify 對該月以 `audit_chain_head` 出示 `genesis/tip/row_count` 作為「曾存在且鏈尾為 H」之 attestation，標 `archived/dropped (anchor-only)`。
- `audit_log_archive` 中的 `delete_app` row：保有 `row_hash`，可做**單 row hash 完整性**重算（核對該 row 自身未被竄改）；但鄰接 row 已隨 partition drop 消失，**無法做 linkage 驗證**——此為明示限制（§ 9）。

### 7.3 MCP 暴露決策：**不暴露**

- `verify` 與 `export` **不**註冊為 MCP tool。理由：
  - 二者為 operator/取證動作，非 agent 日常操作面；暴露給被污染 agent（threat-model AG1/AG2）會擴大攻擊面（如以 verify 當 tamper oracle、以 export 大量外流 audit）。
  - 既有 agent 面已有 `query_audit_log`（read，分頁、受 `audit:read`）足夠日常查詢。
- export 經 REST API + `audit:export` scope 提供（人類/CI 取證）；verify 為 CLI/ops 本地工具。MCP 面維持現狀不增。

## 8. 與 partition drop / 13mo 保留 / archive 的互動

| 情境 | 行為 | 不變式 |
|---|---|---|
| 月度 partition drop（>13mo） | `0ops_archive` 先 archive `delete_app` row（含 hash），再 `DROP TABLE` partition | app role 無 `DELETE`；drop 由 archive role；`audit_chain_head` 該月 row **保留** |
| chain 連續性 | per-(team,month) 獨立鏈，drop 不留 dangling `prev_hash` | 全域單鏈被否（§ 12） |
| completeness（AD2） | `audit_chain_head.row_count` + id gap 偵測；drop 後以 anchor 出示曾有 N 筆 | head row 永不 drop |
| `delete_app` 永久保留 | archive 表保 `row_hash`；verify 做單 row 完整性 | linkage 無法跨已 drop 鄰接（§ 7.2 限制） |
| rollover job 建新 partition | 建表後即套 `REVOKE UPDATE,DELETE FROM 0ops_app`（§ 5.1） | 新 partition 不漏撤權 |

## 9. 殘餘風險與明示接受

| 殘餘風險 | 為何接受 | 重新評估觸發 |
|---|---|---|
| 持 `0ops_migrate`/`0ops_archive` 憑證者仍可改/刪 + 改鏈 | 該等憑證屬 deploy/ops 特權，不暴露 runtime/agent；屬信任核心 | 特權憑證疑似外洩；SOC2 要求外部 anchor |
| hash chain 為 DB 內 self-anchor，無外部公證 | 防 app role 竄改已足；外部 transparency log anchor 列 P3（§ 12） | 取證需第三方不可否認性 |
| archive 之 `delete_app` row 無 linkage 驗證 | partition drop 後鄰接消失為保留政策必然；單 row hash 仍在 | 法務要求 delete_app 完整鏈 |
| pre-chain 既有 row 無 tamper-evidence | `00013` 前資料無法回溯補鏈 | 不重新評估（歷史事實） |

## 10. 與其他 spec 接合點

| 接合 | spec / 位置 |
|---|---|
| audit_log schema / partition / archive 基底 | `audit-log` § 4 / § 9；migration `00007` |
| reconciliation_job 重寫 fallback（AD2 佐證） | `audit-log` § 14；`reconciler-and-incident` § 4；migration `00008` |
| redactor（hash 在其後算） | `error-model` § 9；`audit-log` § 8 |
| trace_id 入 core | `observability-skeleton` § 6.4 |
| RBAC scope `audit:export`（新增） | `auth-and-rbac` § 6；`rbac.Scope` / `rbac.Action` |
| 威脅 AD1/AD2/AD3 | `threat-model` § 5.7、§ 6 |
| 統籌計畫 P0 定位 | `trust-and-compliance/plan.md` § 3.2 / § 5.1 |
| append-only + hash chain 架構決策 | **ADR-0015**（另案） |
| ops 連線 role 切換 | deploy config / ops runbook（`DATABASE_URL` → `0ops_app`） |

## 11. 驗證準則

| 驗證項 | 方式 | 通過條件 |
|---|---|---|
| append-only：app role 撤權後 UPDATE 被拒 | 以 `0ops_app` 連線 `UPDATE audit_log SET action=...` | `permission denied`；row 不變 |
| append-only：app role DELETE 被拒 | 以 `0ops_app` `DELETE FROM audit_log` | `permission denied` |
| 新 partition 自動撤權 | rollover 建下月 partition 後，以 app role UPDATE 該 partition | `permission denied` |
| hash chain：正常鏈通過 | INSERT 數筆後跑 `audit verify` | 全鏈 OK；tip == head.tip_hash |
| hash chain：單 row 竄改可偵測 | 以特權直改某 row `args` | verify 報該 id `row_hash mismatch`，exit 1 |
| hash chain：刪 row 可偵測 | 特權刪中段 row | verify 報 linkage 斷 + row_count 不符 |
| hash chain：插入/重排可偵測 | 特權插入偽 row | verify 報 linkage 斷 |
| 決定性序列化穩定 | 同 row 跨機器/跑兩次算 hash | 位元相同（key 排序、null 顯式、UTC 截斷） |
| hash 對 redact 後算 | 含 token 之 token_create | core 內 `args.token='***'`；export 與 verify 一致 |
| export RBAC：admin+export 通過 | admin + `audit:export` 呼 export | 200 + 串流 + manifest |
| export RBAC：缺 scope 拒 | admin 但無 `audit:export` | 403 forbidden_scope |
| export RBAC：viewer 拒 | viewer + `audit:export` | 403 forbidden_role |
| export 範圍上限 | `until-since` > 13mo | 422 |
| export 串流上限 + 續傳 | 超 100k row 範圍 | 回 `next_cursor`；續拉拼接完整 |
| export integrity 摘要正確 | export 後比對 manifest.tip_hash | == `audit_chain_head` 對應月 tip；verify 該批 OK |
| 跨 partition verify | since 跨多月 | 逐鏈獨立報告；任一 BREAK → exit 1 |
| archive row 單 row 完整性 | drop 後驗 archive 中 delete_app | row_hash 重算相符；標 anchor-only |
| anchor 永存 | drop partition 後查 head | head row 在；出示 genesis/tip/row_count |
| MCP 面不增 | 列 MCP tool catalog | 無 export/verify tool |
| pre-chain 區段不誤報 | 驗 `00013` 前既有 row | 報 pre-chain，不 BREAK |

## 12. Open issues

- **被否的鏈策略——全域單鏈**：寫入競爭收斂為全域單點、且 partition drop 後首筆 `prev_hash` 指向已消失 row 而無法續驗；故否選，採 per-(team,month) 鏈。若未來需「全域不可否認」可疊加外部 anchor 而非改內鏈結構。
- 外部 transparency log anchor（定期將各 chain tip 公證至 append-only 外部 log）：P3；提供第三方不可否認性，超出 app-role 防護需求。
- hash 演算法敏捷性（SHA-256 → 未來遷移）：core 序列化加 `chain_version` 前綴已預留；實際遷移程序待 `audit-event-notification` 之後評估。
- `0ops_archive` 取得 partition `DROP` 之最小權限模型（直接 owner 委派 vs `SECURITY DEFINER` 函式）：依部署 Postgres 版本與 HA 拓樸（ADR-0008）定案。
- runtime 連線切換為 `0ops_app` 的 deploy 編排（migration 上線與連線切換同批的具體機制）：ops runbook 落地。
- export 之非同步大匯出（>數百萬 row 之離線產檔 + 下載連結）：v1 採串流續傳；超大租戶再評估非同步 job。
- `audit_chain_head` 於 `team on delete cascade`——team 刪除時 head 隨之消失，與「audit 取證永存」可能衝突：待 `delete-app-flow` / 法務確認 team 刪除之 audit 保留政策。

## 13. 不可違反的硬性規則

> 違反以下任一項，PR 不可合入。承 `trust-and-compliance/plan.md` § 6、`threat-model` § 11。

1. application DB role（`0ops_app`）一旦上線，**不得**保有 audit_log（含所有 partition、未來 partition）之 `UPDATE`/`DELETE` 權限（plan § 6 規則 2）。
2. archive / partition drop **不得**以 app role 執行；須由獨立特權 role（`0ops_archive`）或 migration role 執行。
3. `row_hash` 必對 **redact 後**之儲存內容計算；raw 內容不得為計算來源（否則 export/verify 不一致）。
4. 每筆新 audit row（`00013` 後）必填 `prev_hash`/`row_hash`；寫入路徑不得 INSERT 無 hash 的 row（pre-chain 僅限歷史既有資料）。
5. 鏈策略為 per-(team, month-partition)；`audit_chain_head` row **永不 drop**（completeness 與 anchor attestation 依此）。
6. export 須帶 `audit:export` scope + admin role；**不得**沿用 `audit:read` 放行 bulk export。
7. export 必附 integrity 摘要（chain tip + 範圍 + row_count）；不得只回原始 row。
8. export `since` 必填、範圍上限 13 個月；不得提供無界全表匯出。
9. `verify`/`export` **不得**暴露為 MCP tool（agent 攻擊面控制）。
10. 涉及 schema 變更（`prev_hash`/`row_hash`、`audit_chain_head`）與 role 變更須以 **ADR-0015** 為決策來源（plan § 6 規則 6）；本 spec 不得在 ADR 未落地前宣稱機制「已具備」。
