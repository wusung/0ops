---
adr: "0002"
title: Idempotency 與副作用補償
status: Accepted
date: 2026-05-09
tags:
  - idempotency
  - two-phase-write
  - saga
  - compensation
  - reconciler
supersedes: []
superseded-by: []
---

# ADR-0002：Idempotency 與副作用補償

* Status：Accepted
* Date：2026-05-09
* 適用範圍：M0–M5；所有 backend 寫入 / 刪除動作之上游約束
* 來源：`docs/0ops-plan.md`「關鍵設計 #3 #4」「Backend：preview gate」「Deploy 狀態機」「Reconciler 收斂迴圈」四段已確定方向，本 ADR 將其正式化並補上被否決選項的理由
* 上游依賴：[ADR-0001 多租戶模型與 RBAC](0001-multi-tenancy-and-rbac.md)（cross-team idempotency 隔離）

## 0. TL;DR（先讀本段）

採用以下九項組合決策，作為所有寫入/刪除類 endpoint 的不可變前提：

1. **兩階段寫入強制執行**：寫入/刪除類動作必經 `*:preview`（dry-run 計算 side_effects）→ 主端點（消費 `preview_id` 真執行）；backend 對主端點缺 `preview_id` 直接 `400`。
2. **preview_id 兼 idempotency key**：不另設 client 端生成的 key，`(team_id, idempotency_key)` 為 DB 唯一索引；client 可帶 `Idempotency-Key` header 但與 `preview_id` 衝突即 `422`。
3. **重試行為 = last_result 回放**：`consumed_at != null` 的 preview 重試一律回 `last_result`，不重做副作用、不擲 409。
4. **Preview TTL 10 分鐘**；背景 goroutine 每 60s 清過期 row。
5. **Confirm race 處理**：transaction 內 `SELECT ... FOR UPDATE` 鎖 preview row；先決條件（slug 仍可用、token 仍有效、role/scope 仍夠）在同一 tx 內重檢。
6. **副作用順序**：reversible（gitops branch、Cloudflare DNS draft）先 → irreversible（image push、tunnel binding）後；任一步失敗反向回滾 reversible 部分。
7. **Saga 簡化版補償**：每個寫入動作有狀態機（forward 階段 + `compensating → rolled_back` 階段）；不採 distributed transaction，不採純 outbox。
8. **Reconciler 收斂迴圈**：對 `applying` 滯留 > 15 min 的 row 主動拉外部狀態收斂；指數退避 `min(60s × 2^attempts, 30min)`，> 8 次轉 `failed_permanently`。
9. **Callback over polling**：build / external action 完成後用 HMAC callback 推進狀態機；polling 為退路，避免 callback 永遠不來。

行為與 schema 細節以 `docs/0ops-plan.md`「Backend：preview gate」「Deploy 狀態機」「Reconciler 收斂迴圈」段為準，本 ADR 不重述。

## 1. Context and Problem Statement

0ops backend 的寫入路徑同時面對三類不可信來源：

* **CLI 客戶端** — 使用者可能跑 `--yes` 跳過互動，亦可能腳本化重試。
* **MCP 客戶端 / LLM** — claude code、codex、copilot 三家對 tool description 遵守度不一；LLM 可能跳過 preview 直接呼主 tool，可能在錯誤後盲目重試，可能複用舊 `preview_id`。
* **GitHub Actions / 外部回調** — workflow 重試、network glitch、HMAC callback 可能丟失或重送。

寫入動作的副作用跨四個系統：GitHub Actions（image build / push）、GHCR（image registry）、Cloudflare（DNS、tunnel hostname）、`0ops-gitops` repo（manifest commit）+ ArgoCD（K3s sync）。其中 `image push`、`tunnel binding` 不可逆；其餘可在 saga 反向回滾。

需在程式碼下手前釘住三件事：

1. 同一動作多次抵達 backend 時的安全語意（idempotent 或 reject）。
2. 副作用部分成功時的補償語意（哪一步失敗、回滾到哪、留下什麼 audit）。
3. 外部回調丟失時的收斂語意（多久察覺、用什麼資料源拉狀態、何時放棄）。

ADR-0001 已釘住「跨 team 邊界不可滲漏」的前提；本 ADR 在其上建立「同 team 內寫入動作的可重試與可補償語意」。

## 2. Decision Drivers

* **DD1 LLM 客戶端不可信賴**：preview / confirm 約定僅是 SKILL 層級提示；backend 必須以結構性方式強制（缺 preview_id 直接 4xx）。
* **DD2 多系統副作用、部分不可逆**：image push 與 tunnel binding 是 point-of-no-return；副作用順序需讓回滾邊界明確。
* **DD3 操作鏈路長**：build 4–6 min，中間節點（GHA runner、callback、ArgoCD、K3s）任一可能斷；不能假設 happy path。
* **DD4 跨 team idempotency 攻擊面**：`preview_id` 不得跨 team 被取用；DB 唯一鍵與 SQL `WHERE team_id` 雙重鎖定（接續 ADR-0001 第一道防線）。
* **DD5 Audit 完整性**：每次 attempt 都需 `trace_id` + `preview_id` 落地；重試的 `last_result` 回放亦需可被稽核識別為「重試」而非「重做」。
* **DD6 GHA callback 不保證送達**：HMAC callback 為 push 模型，但需有 polling fallback 對 `building` 滯留 > 30 min 的 row 收斂。
* **DD7 SSE / 非冪等通道副作用**：log streaming 等讀取側 SSE 不在本 ADR 範圍；本 ADR 僅約束「寫入類」。

## 3. Considered Options

針對 (a) idempotency key 來源 與 (b) 重試行為 做完整 alternative 比較；(c)(d)(e) 為局部技術選擇，列表帶過。

### 3.1 (a) Idempotency key 來源

| Option | 描述 |
|---|---|
| **A1. preview_id 兼 key**（採用） | preview 創建時即配發 key；client 透過 `preview_id` 同時取得「行動授權」與「重試令牌」 |
| A2. 獨立 `Idempotency-Key` header | RFC-style，client 自生 UUID；preview 與 key 為兩件事 |
| A3. Request hash | `hash(args)` 作 key；無 client 介入 |
| A4. 不設 idempotency | 每次重試都重做副作用 |

### 3.2 (b) 重試行為

| Option | 描述 |
|---|---|
| **B1. last_result replay**（採用） | `consumed_at != null` 重試直接回 `preview.last_result`，不再走副作用 |
| B2. 409 Conflict | 已 consumed 一律回 `409`，client 自行處理 |
| B3. Re-execute | 不檢查 consumed，每次都重做（即取消 idempotency） |
| B4. Compare & replay | 比對 `args hash`，相同回 last_result，不同回 `409` |

### 3.3 (c)(d)(e) 局部選擇

| 子決策 | 採用 | 否決 | 一句結論 |
|---|---|---|---|
| (c) Two-phase enforcement | preview required at confirm（缺則 4xx） | single-phase + Idempotency-Key only / reservation pattern | LLM 不可信賴需 backend 強制；reservation 模型對 `workflow_dispatch` 等不可撤銷外部動作不適用 |
| (d) 副作用順序 | reversible 先 → irreversible 後 | 平行觸發 / commit-after-all | 平行觸發無法定義回滾邊界；commit-after-all 對長鏈路（4–6 min）會造成超大 reservation window |
| (e) Compensation 模型 | saga simplified（per-stage compensating action）+ reconciler | 兩階段 commit（2PC）/ transactional outbox-only / 手動 cleanup | 跨系統（GitHub、Cloudflare、K8s）無 distributed transaction；outbox 可補事件投遞但不替代狀態機；手動 cleanup 在 LLM 流量下不可持續 |

## 4. Decision Outcome

採用 **A1 + B1**，搭配 (c) preview-required enforcement、(d) reversible-first 順序、(e) saga simplified + reconciler。

具體展開（細節以 `docs/0ops-plan.md` 為準，本 ADR 不重述狀態機列舉與 reconciler tick 細節）：

1. **API contract**：
   * `POST /v1/teams/{team}/{resource}:preview { args }` → `{ preview_id, action, action_summary, side_effects[], expires_at }`。
   * `POST /v1/teams/{team}/{resource} { preview_id, ... }` → 真執行；缺 `preview_id` 回 `400 missing_preview_id`，過期回 `410 preview_expired`，跨 team 回 `404`（接續 ADR-0001 enumeration 防範），consumed 回 `last_result`。
2. **Schema 約束**：
   * `preview(team_id, idempotency_key)` UNIQUE（預設 `idempotency_key = preview_id`）。
   * `preview.actor_user_id` not null；confirm 時驗 actor 一致（防 token 被偷）。
   * `preview.last_result jsonb` consumed 後 mutate；`consumed_at` 設為 confirm tx 提交時間。
3. **Concurrency**：
   * Confirm tx 內 `SELECT * FROM preview WHERE id = $1 AND team_id = $2 FOR UPDATE`。
   * 先決條件（slug 是否仍可用、token 是否仍有效、role/scope 是否仍夠）在同一 tx 內重檢；任一不過 → 回 4xx 並標 preview consumed-with-error。
4. **副作用順序**：
   * Reversible 先：gitops branch 開、Cloudflare DNS record draft、deploy_run row 寫入。
   * Irreversible 後：image push GHCR、tunnel hostname binding、ArgoCD sync trigger。
   * 任一 reversible 後失敗 → 進 `compensating` 階段反向 undo；任一 irreversible 後失敗 → 進 `rolled_back` 但保留 audit（無法物理 undo 已發生的副作用，例如 image 已 push）。
5. **TTL 與清理**：preview 10 分鐘；背景 goroutine 每 60s `DELETE FROM preview WHERE expires_at < now() AND consumed_at IS NULL`；consumed 但保留 7 天供稽核。
6. **Reconciler**：
   * `reconciliation_job` DB-backed queue；指數退避 `min(60s × 2^attempts, 30min)`；> 8 次轉 `failed_permanently`。
   * 對 `applying` 滯留 > 15 min 的寫入動作主動拉外部狀態收斂。
   * 對 `deploy_run.status='building'` 滯留 > 30 min 主動拉 GitHub API `workflow_run` 狀態。
7. **Callback**：
   * GHA → backend HMAC POST `/internal/deploy-runs/{id}/callback`；backend 驗 signature + timestamp window ±5 min + `webhook_dedup` 反重放。
   * 推進狀態機；polling fallback 為退路。

## 5. Pros and Cons of the Options

### 5.1 (a) Idempotency key 來源

#### A1. preview_id 兼 key（採用）

* Good：client（含 LLM）只需理解一個概念——「拿到 preview_id 即可重試」。
* Good：跨 team 隔離與 idempotency 隔離共用同一個 SQL 鎖定（`WHERE id = $1 AND team_id = $2`），防線一致。
* Good：preview 過期 = idempotency 自動失效，無需另寫 key TTL 邏輯。
* Good：強迫經過 preview 階段（client 沒有「跳過 preview 但帶 idempotency key」的旁路）。
* Bad：client 想用「同一 args 跨 preview 重試」需另發 preview，無法重用上次 key。
* Bad：preview 與 idempotency 兩個概念耦合；未來若想為非 preview 路徑加 idempotency 需另設機制。

#### A2. 獨立 Idempotency-Key header

* Good：符合 Stripe / RFC 9110 慣例；client SDK 既有支援。
* Good：preview 與 idempotency 解耦，未來擴展彈性大。
* Bad：LLM 客戶端需理解兩個概念；遵守率下降。
* Bad：DB 需另設 `idempotency_key` 表與 TTL 清理；維運面增重。
* Bad：缺乏「強迫 preview」的天然機制；需另在 middleware 檢查 preview 存在。

#### A3. Request hash

* Good：完全 server-side，client 無感。
* Bad：args 微差（如 timestamp、optional flag）即視為新請求，重試語意不可控。
* Bad：無法表達「我故意要重試這個操作」與「我發了一個新請求」之差別。
* Bad：對含 random seed / nonce 的 args 形同無 idempotency。

#### A4. 不設 idempotency

* Good：實作最簡單。
* Bad：LLM 重試會直接重做副作用（image push、Cloudflare DNS 重複註冊）。
* Bad：違反 DD1（LLM 不可信賴）；不可選。

### 5.2 (b) 重試行為

#### B1. last_result replay（採用）

* Good：client（含 LLM）的程式碼在「第一次成功」與「第 n 次重試」邏輯路徑相同。
* Good：副作用恰好執行一次；image push / DNS 註冊不會重複。
* Good：對 LLM 友善——失敗後盲目重試也安全。
* Bad：`last_result` 需 mutate-once 並避免 race（confirm tx 內 `FOR UPDATE` 已處理）。
* Bad：`last_result` 含敏感欄位時需小心暴露面；需審查 response payload。
* Bad：consumed 後若 args 已過時（例如 slug 已被別 team 改名）回放仍回舊結果，client 不會自動察覺；需 audit + revisit trigger。

#### B2. 409 Conflict

* Good：語意清楚：「這個 preview 已用過，請重新 preview」。
* Good：強迫 client 重新走 preview 流程，避免回放錯誤過時結果。
* Bad：對 LLM 不友善；遇 409 重試行為不可預測。
* Bad：CI 場景重試需多繞一圈，net throughput 下降。

#### B3. Re-execute

* Good：實作最簡單。
* Bad：違背 idempotency 目的；不可選。

#### B4. Compare & replay

* Good：保留 last_result 的友善性，又能偵測 args 改變。
* Bad：args hash 對含 nonce / timestamp 欄位不穩定；需先標準化 args。
* Bad：複雜度高於 B1；v1 over-engineering。
* Bad：標準化 args 的規則本身會成為新的 ADR 與 codegen 對象。

## 6. Consequences

### 6.1 Positive

* 寫入路徑語意一致：每個 `*:write` endpoint 都靠同一條 `preview → consume` 機制；新 endpoint 只需宣告 args schema 與 side_effects 計算函式。
* LLM 重試容錯：「呼錯 / 呼兩次 / 中途斷」都收斂到 last_result 回放或 410 expired，無 silent 重複副作用。
* Saga 反向回滾在 reversible 階段保證乾淨；irreversible 階段失敗有完整 audit + reconciler 收斂。
* Reconciler 對 callback 丟失 / GHA timeout 有兜底；不依賴 callback 必送達。
* 接續 ADR-0001：跨 team preview 取用在 schema 與 middleware 兩層被擋。

### 6.2 Negative

* 寫入端點數量翻倍（每個 write action = preview + main）；OpenAPI / MCP tool registry / CLI command 對應實作成本上升。
* `last_result` 為 jsonb 黑盒；schema evolution 時舊 `last_result` 可能與新 response 結構不一致，audit 工具需處理 versioning。
* `SELECT ... FOR UPDATE` 使 confirm 路徑序列化於同 preview row，雖窄但對極高頻率場景需評估行鎖回壓。
* Reconciler 退避策略（max 30 min, max 8 attempts ≈ 2 小時 + 30 min × N）對「網路長期不通」的場景可能過短，需與 SLO 對齊調參。
* 副作用順序強制 reversible-first 對未來新增的副作用類型（如 v2 Vault secret push）需明確分類，否則 saga 邊界會模糊。

### 6.3 Neutral

* HMAC callback 簽章演算法、timestamp window、`webhook_dedup` 表細節屬 ADR-0005 範圍；本 ADR 僅約束「callback 為主、polling 為輔」。
* `failure_classification` 列舉與 CFR 計算屬 Observability spec 範圍；本 ADR 僅約束「失敗必有 classification 落地」。
* deploy_run 狀態機具體 stage 名稱（queued / preparing / applying / committing / done）屬實作細節，可在 spec 演進；本 ADR 約束的是「forward + compensating 兩階段拓撲」。

## 7. Revisit Triggers

下列任一條件觸發時，應重新評估本 ADR 對應段：

1. **單一 args 跨 preview 重試需求**：客戶端要求「同一 args 即使 preview 過期也視為同一次操作」→ 重審 (a)，可能引入 A2（獨立 Idempotency-Key header）。
2. **last_result 過時誤導**：稽核發現 ≥ 0.5% 的 last_result 回放回了「實際應重做」的結果（例如 slug 被改名、token 被撤銷），需強制 client 重新 preview → 重審 (b)，可能改 B2 或 B4。
3. **Saga 邊界爆炸**：新副作用類型（Vault secret、外部 IAM 同步）的 reversible / irreversible 分類爭議多 → 重審 (d)(e)，可能引入 outbox 補強事件投遞。
4. **Reconciler 退避不夠**：SLO 顯示 `failed_permanently` 中 ≥ 5% 為「網路恢復後可成功」案例 → 調整退避上限或拉長 max attempts；非 ADR 改動但需重審本 ADR 第 6.2 段假設。
5. **Callback 到達率惡化**：GHA → backend callback 到達率低於 99%（單月）→ 重審 polling 與 callback 角色；可能將 polling 升為主路徑。
6. **跨 team idempotency 攻擊出現**：發現 preview_id 列舉 / 暴力嘗試 → 重審 preview_id 熵與 SQL 鎖定（接續 ADR-0001 enumeration 防範）。

## 8. More Information

* Preview gate 與狀態機行為細節：`docs/0ops-plan.md`「Backend：preview gate」「Deploy 狀態機」「Reconciler 收斂迴圈」段。
* 跨 team 隔離 SQL 模板與 middleware：[ADR-0001 多租戶模型與 RBAC](0001-multi-tenancy-and-rbac.md) 第 4 節。
* HMAC callback 簽章與 build pipeline 觀察點：規劃為 ADR-0005（待寫）；本 ADR 僅約束「callback 為主、polling fallback」。
* Observability metric（`0ops_preview_*`、`0ops_deploy_run_*`、`0ops_reconciliation_jobs_pending`）：規劃為 ADR-0006（待寫）。
* MCP tool description 強制句式（`*_preview` 必含「ALWAYS call this BEFORE」等）：規劃為 ADR-0003 範圍。

## 9. Open Questions

下列問題不阻擋本 ADR 通過，但需在 M0 結束前敲定：

1. **`Idempotency-Key` header 與 `preview_id` 衝突回應碼**：plan 定 `422`，是否與 RFC 9110 idempotency draft 對齊待 spike。
2. **last_result 過期策略**：consumed preview 保留 7 天後 hard delete，但若 audit_log 仍引用 `preview_id`，是否需保留 reference-only stub？
3. **Compensation 失敗的失敗**：rollback 自身失敗時的行為——進 `rolled_back_with_residue` 還是 `failed_permanently`？對 reconciler 重試上限的影響需釐清。
4. **`SELECT ... FOR UPDATE` skip locked 變體**：高頻率 reconciler tick 對 preview 表是否需 `FOR UPDATE SKIP LOCKED` 避免阻塞？v1 量級先不開，留 revisit。
5. **Args 標準化規則**：未來若引入 B4（compare & replay），args 標準化（移除 nonce、排序 map key、UTC 化 timestamp）需獨立規範。
6. **跨 binary `preview_id` 重用**：CLI 拿到 preview_id 是否能傳給 MCP server 在另一 session confirm（語意上等同同一 actor 的不同 client）？目前隱含禁止，需明確化。
