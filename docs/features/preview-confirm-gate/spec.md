# Feature Spec：preview-confirm-gate

> **狀態**：draft
> **來源**：`docs/0ops-plan.md`「關鍵設計 #3」「Backend：preview gate」段；ADR-0002（Idempotency 與副作用補償）；ADR-0001（cross-team 隔離）；本 spec 依賴 `shared-dto-and-contract`、`error-model`、`observability-skeleton`、`auth-and-rbac`
> **適用範圍**：所有寫入 / 刪除類 endpoint 之 `:preview` 與主端點共用之 backend gate；不含個別 action 之 side_effects 計算邏輯（屬該 action feature spec）
> **對應 Milestone**：M2（與 `create_app` 兩階段同步上線）

## 1. 結論（先讀本段）

- Gate 為 `internal/server/preview/` package，提供統一 `Produce` / `Consume` 兩函式；個別 action handler 只實作 `SideEffects(ctx, args) ([]SideEffect, error)` 與 `Execute(ctx, args) (any, error)` 兩 callback
- `:preview` endpoint 行為：驗 args → call `SideEffects()` → 寫 `preview` row → 回 `PlanPreview`（與 `shared-dto-and-contract` § 5.1 一致）
- 主端點行為：取 `preview_id` → tx 內 `SELECT FOR UPDATE` 鎖 row → 重檢先決條件 → 順序執行副作用 → mutate `last_result` + `consumed_at` → 回 `last_result` 內容
- TTL 10 分鐘；背景 goroutine 每 60s 清過期未消費；consumed 保留 7 天
- 副作用順序：reversible → irreversible 兩階段；reversible 失敗反向 undo；irreversible 失敗不再 undo 但保留 audit + reconciler hook
- `last_result` 為 jsonb；含 `idempotent_replay: false`（首次）或 `true`（重試）旗標；client 透過此旗標識別重試
- 跨 team / 跨 actor preview 取用一律 `404 preview_not_found`（與 `auth-and-rbac` § 7 一致）
- preview_id 熵：UUID v4（128 bit）；不可順序遞增、不可基於時間明顯可猜
- v1 不允許跨 binary（CLI ↔ MCP）共享 preview_id：confirm 端驗 `actor_user_id == ctx.actor_user_id`，CLI 與 MCP 雖共用 token 但 `actor_user_id` 一致即可，本 spec 不阻擋；但 token 不同則 fail（與 token 偷換偵測對齊）

## 2. 範圍

### 2.1 包含
- `internal/server/preview/` package：Producer / Consumer / GC / Action 介面
- `preview` 表寫入 / 鎖定 / 清理路徑
- `:preview` 與主端點之 router 共用 helper
- 副作用順序框架（reversible / irreversible 標註與執行）
- `last_result` 結構與 idempotent_replay 旗標
- 跨 team / 跨 actor 隔離與 enumeration 防範
- TTL、GC、metric 點
- 與 `audit-log` spec 之 hook 點 contract（preview_consumed → audit）

### 2.2 不包含
- 個別 action 之 args schema、side_effects 計算、execute 邏輯（屬各 feature spec：create-app / delete-app / add-domain / ...）
- 狀態機 forward / compensating 階段轉移之 `deploy_run` 行為（屬 `reconciler-and-incident` spec；本 spec 只規範「執行 callback 結束 = 寫 last_result」）
- HMAC callback 端點（屬 `build-pipeline-and-callback` spec）
- Reconciler 之 reconciliation_job 表本身（屬 `reconciler-and-incident` spec；本 spec 只規範 preview 不可 reconcile，唯有 forward action 可）
- `Idempotency-Key` header 處理之 RFC 9110 對齊細節（待 ADR-0002 OQ#1 spike）

## 3. 檔案結構

```
0ops/
├── internal/
│   └── server/
│       ├── preview/
│       │   ├── action.go            # Action interface
│       │   ├── produce.go           # POST :preview helper
│       │   ├── consume.go           # POST main helper（含 tx + FOR UPDATE）
│       │   ├── side_effects.go      # SideEffect type、reversible/irreversible 執行框架
│       │   ├── gc.go                # 背景 goroutine：過期清理 + consumed 保留 7 天
│       │   ├── lastresult.go        # last_result 序列化 / 解析
│       │   ├── metrics.go           # preview_created/consumed/expired counter
│       │   └── doc.go
│       └── routers/
│           └── *.go                 # 個別 router 引用 preview.Produce/Consume
└── migrations/
    └── 000X_preview_indexes.sql     # (team_id, expires_at) 索引、(team_id, idempotency_key) 唯一
```

## 4. Action 介面

### 4.1 Go 介面

```go
package preview

type SideEffect struct {
    Description string
    Resource    string
    Reversible  bool                                  // true = saga 反向可 undo
    Forward     func(ctx context.Context) error       // 執行此副作用
    Compensate  func(ctx context.Context) error       // Reversible=true 時必填；undo 動作
}

type Action interface {
    Name() string                                     // 對應 rbac.Action 字串
    SideEffects(ctx context.Context, args any) (
        summary string,
        effects []SideEffect,
        err error,
    )                                                  // dry-run；不可有副作用
    Execute(ctx context.Context, args any, effects []SideEffect) (
        result any,                                    // 寫入 last_result
        err error,
    )                                                  // 真執行；接收 SideEffects 同樣 effects 切片
    Precheck(ctx context.Context, args any) error     // confirm tx 內重檢；失敗回 *apperror.Error
}
```

### 4.2 Action 註冊

```go
// internal/server/preview/registry.go
var actions = map[string]Action{}

func Register(a Action) {
    if _, dup := actions[a.Name()]; dup {
        panic("duplicate action: " + a.Name())
    }
    actions[a.Name()] = a
}

func Lookup(name string) (Action, bool) { ... }
```

- 各 action package（`internal/server/services/createapp/`、`deleteapp/`、`adddomain/` ...）在 `init()` 呼 `preview.Register(&Action{})`
- backend 啟動時驗：`rbac.Action` 列舉 ⊆ `actions` map keys；缺一即 panic（fail-fast）

## 5. `:preview` endpoint 流程

### 5.1 Router pattern

```go
// internal/server/routers/apps.go (節錄)
r.Post("/v1/teams/{team_slug}/apps:preview", preview.Handler(rbac.ActionCreateApp))

// internal/server/preview/produce.go
func Handler(action rbac.Action) http.HandlerFunc {
    return func(w http.ResponseWriter, r *http.Request) {
        a, _ := Lookup(string(action))
        var args ArgsForAction(action)
        if err := json.NewDecoder(r.Body).Decode(&args); err != nil { ... }
        Produce(w, r, a, args)
    }
}
```

### 5.2 Produce 函式行為

```
1. 驗 args（per-action JSON Schema 或 struct validate）
2. a.SideEffects(ctx, args) → (summary, effects[], err)
3. err 非 nil → 回 4xx envelope（依 err 類型）
4. INSERT INTO preview (
       id, team_id, actor_user_id, action,
       args, action_summary, side_effects, idempotency_key,
       expires_at, created_at
   ) VALUES (
       uuid_generate_v4(), $team_id, $actor_user_id, $action,
       $args_jsonb, $summary, $side_effects_jsonb, $preview_id,
       now() + interval '10 minutes', now()
   )
5. 回 200 + PlanPreview JSON
```

- `idempotency_key` 預設 = `preview_id`；client 帶 `Idempotency-Key` header 與 `preview_id` 衝突 → `422 idempotency_key_conflict`
- `side_effects_jsonb` 只存 `Description / Resource / Reversible` 三欄（不存 Forward / Compensate function pointer）；執行時由 action 端重新提供

### 5.3 Side_effects 計算約束

- `SideEffects()` callback **不可**有副作用（不可呼 GitHub API、不可寫 DB、不可 mutate state）
- 允許：read-only DB 查詢（如「slug 是否已存在」用於 `slug_taken` 判斷與顯示警告）、純計算
- 違反者由 code review 把關；無法自動偵測

### 5.4 Preview ID 熵

- 採 `crypto/rand` 產生 UUID v4（128 bit 熵，stdlib `google/uuid` 或自寫）
- 不採時間戳 + 序號（可猜）；不採短 hash
- 序列化為小寫字串 `xxxxxxxx-xxxx-4xxx-yxxx-xxxxxxxxxxxx`（與 ADR-0001 enumeration 防範對齊）

## 6. 主端點 confirm 流程

### 6.1 Router pattern

```go
r.Post("/v1/teams/{team_slug}/apps", preview.Handler(rbac.ActionCreateApp))

// internal/server/preview/consume.go
func Handler(action rbac.Action) http.HandlerFunc {
    return func(w http.ResponseWriter, r *http.Request) {
        var env shared.ConfirmEnvelope
        if err := json.NewDecoder(r.Body).Decode(&env); err != nil { ... }
        if env.PreviewID == "" {
            apperror.WriteJSON(w, r.Context(),
                apperror.NewBadRequest("missing_preview_id", "..."))
            return
        }
        a, _ := Lookup(string(action))
        Consume(w, r, a, env.PreviewID)
    }
}
```

### 6.2 Consume 函式行為（核心）

```sql
BEGIN;

SELECT id, team_id, actor_user_id, action, args, side_effects,
       expires_at, consumed_at, last_result
  FROM preview
 WHERE id = $preview_id AND team_id = $team_id
  FOR UPDATE;
-- 0 row → 404 preview_not_found（含跨 team / 跨 actor 偽造）
-- 取出後驗：
--   actor_user_id != ctx.actor_user_id → 404 preview_not_found
--   expires_at < now() AND consumed_at IS NULL → 410 preview_expired
--   action != current handler action → 409 (內部不一致；不應發生)
--   consumed_at IS NOT NULL → 直接回 last_result（200 + idempotent_replay: true），不執行副作用，COMMIT
```

接續：
```
1. a.Precheck(ctx, args) — 重檢先決條件（slug 仍可用、role/scope 仍夠、token 未撤銷）
   - 失敗 → 回對應 4xx（如 slug_taken → 409，role 降級 → 403）
   - 失敗仍 mutate preview consumed_at + last_result（含 error envelope）以保留審計；下次重試直接回此 last_result

2. 副作用執行：
   for each e in effects where e.Reversible == true:
       err = e.Forward(ctx)
       if err != nil → goto compensate(已執行的 reversible 部分)
   for each e in effects where e.Reversible == false:
       err = e.Forward(ctx)
       if err != nil → goto rollback_irreversible（不 undo 已 push 之 image，但寫 audit）

3. result = a.Execute(ctx, args, effects)  -- 通常為 final assemble；亦可為空
4. UPDATE preview SET consumed_at = now(), last_result = $result_jsonb WHERE id = $preview_id
5. COMMIT
6. 回 200 + last_result JSON（含 idempotent_replay: false）
```

### 6.3 `last_result` 結構

```json
{
  "idempotent_replay": false,
  "data": {
    "id": "app-uuid",
    "slug": "nextdemo",
    "deploy_run_id": "deploy-uuid",
    "trace_id": "..."
  }
}
```

- 重試時 backend 仍回同樣 JSON 但 `idempotent_replay: true`
- `data` 結構由 action 自決定；對 client 而言等同主端點正常回應之 body
- 失敗的 last_result：`{"idempotent_replay": false, "error": {...envelope...}}`；HTTP status 維持當時失敗碼（重試亦回同 status）
- 注意：`idempotent_replay: true` 仍回 HTTP 200（含原本失敗的回放也回 200，但 body.error 顯示原失敗）；理由：HTTP 層語意「這次請求成功處理（回放）」，業務層失敗在 body
  - 例外：原失敗時若 status 為 4xx/5xx，重試回放亦回原 status；本 spec 採此路徑（與 RFC 9110 idempotency draft 對齊；待 ADR-0002 OQ#1 確認）

### 6.4 Tx 失敗處理

- DB tx commit 失敗（少見；如 connection lost）：副作用已執行；**不**回滾副作用（reversible 已執行的部分留下）
- 此情境寫 audit_log + reconciler 收斂（reconciliation_job 偵測「副作用發生但 preview 未 consumed」）
- v1 簡化處理：不 trigger reconciler，直接回 503 給 client；client 重試後 preview 仍未 consumed → 重做副作用（風險：重複副作用）
- v1.1 評估：用 outbox 確保副作用與 preview consume 之原子性

## 7. 副作用順序執行（saga 簡化版）

### 7.1 三階段

```
forward_reversible:    e in effects where Reversible == true
forward_irreversible:  e in effects where Reversible == false
compensate:            e in forward_reversible (reverse order, all e.Compensate())
```

### 7.2 失敗矩陣

| 階段失敗 | 處置 |
|---|---|
| forward_reversible 第 N 步 | 對前 N-1 步逆序呼 `Compensate()`；preview 標 consumed + last_result 含 error；deploy_run 標 `compensating → rolled_back` |
| forward_irreversible 第 M 步 | 已過 point-of-no-return；不 undo；preview 標 consumed + last_result 含 error + 部分成功欄位；deploy_run 標 `rolled_back`（含 audit）；reconciler 後續可能補做收斂 |
| Compensate 自身失敗 | 寫 audit + reconciliation_job（kind = `cleanup_residue`）；preview last_result 標 `compensation_failed`；待 ADR-0002 OQ#3 釐清 |

### 7.3 Action 端責任

- 個別 action 須在 `SideEffects()` 正確標 `Reversible`；錯標即 saga 邊界錯
- code review 必看每個 effect 的 Reversible 標籤；本 spec § 12 列為硬性規則
- v1 範例：
  | Effect | Reversible |
  |---|---|
  | gitops branch 開、Cloudflare DNS draft、deploy_run row INSERT | ✓ |
  | image push GHCR、tunnel hostname binding、ArgoCD ApplicationSet manifest commit 至 main | ✗ |

## 8. TTL 與 GC

### 8.1 寫入時 TTL

- `expires_at = created_at + 10 minutes`（常數於 `preview/produce.go` 內）；未來改變需 ADR
- `(team_id, expires_at)` 索引便於 GC 掃描

### 8.2 背景 GC

```go
// internal/server/preview/gc.go
func RunGC(ctx context.Context, db *pgxpool.Pool, log *slog.Logger) {
    t := time.NewTicker(60 * time.Second)
    defer t.Stop()
    for {
        select {
        case <-ctx.Done(): return
        case <-t.C:
            // 1. 過期未 consumed → DELETE
            del, err := db.Exec(ctx, `DELETE FROM preview
                WHERE expires_at < now() AND consumed_at IS NULL`)
            // 2. consumed > 7 天 → DELETE
            del2, err := db.Exec(ctx, `DELETE FROM preview
                WHERE consumed_at IS NOT NULL AND consumed_at < now() - interval '7 days'`)
            // 3. metric
            metrics.PreviewGCDeleted.Add(float64(del.RowsAffected() + del2.RowsAffected()))
        }
    }
}
```

- 觸發點：backend 啟動時起一個 goroutine
- M5 多 replica 後僅 leader 跑 GC（與 `backend-ha-leader-election` spec 對齊）；v1 single replica 不必判
- GC 失敗（DB 不通）log warn 不 fatal；下個 tick 重試

## 9. 跨 team / 跨 actor 隔離

### 9.1 Preview 取用守則

- `WHERE id = $1 AND team_id = $2` 為硬性 SQL 條件（在 `Consume` 路徑強制）
- `team_id` 由 `ResolveTeam` middleware 設於 ctx，**不**由 client 傳入
- 跨 team 取用：SQL 0 row → `404 preview_not_found`（與 ADR-0001 enumeration 防範一致）
- 跨 actor 取用（actor_user_id 不符）：應用層額外驗；同樣回 `404 preview_not_found`（避免洩漏「這 preview 確實存在」）
  - 例外：`audit_log` 內仍記為 `forbidden_actor` 以利 dashboard 偵測

### 9.2 PAT 跨 team 場景

- PAT 已綁定 `team_id`；對另一 team 之 endpoint 即失敗於 `ResolveTeam → CheckMembership`，根本到不了 preview gate
- 設計上 preview 只屬於 confirm 該 preview 之 client 的同一 team；無跨 team 合法用例

### 9.3 Preview 流轉至 MCP / CLI 之間

- 同一 user 在 CLI 拿 preview_id、轉到 MCP 端 confirm：本 spec 允許（actor_user_id 一致）
- 不同 user 或不同 token 偷拿 preview_id confirm：actor_user_id 不一致 → 404
- v1 實作上：`auth.json` 是 user-machine local；他人取得需先取得整份 auth.json，已超出本 spec 防範範圍

## 10. Metric 點

依 `observability-skeleton` § 4.3 已釘 3 條：

| Metric | 增點 |
|---|---|
| `0ops_preview_created_total{action}` | `Produce` 成功寫 row 後 +1 |
| `0ops_preview_consumed_total{action, outcome}` | `Consume` 結束後 +1；outcome ∈ {success, failed, idempotent_replay} |
| `0ops_preview_expired_total{action}` | GC `DELETE` 因過期未 consumed 之 row 數累加（不含 consumed > 7 天的清理）|

額外 latency histogram：

| Metric | type | label | help |
|---|---|---|---|
| `0ops_preview_consume_duration_seconds` | histogram | action, outcome | Consume 路徑（含副作用執行） |
| `0ops_preview_side_effect_duration_seconds` | histogram | action, resource, reversible, outcome | 個別 effect Forward 時間 |

## 11. 與其他 spec 的接合點

| 接合 | spec |
|---|---|
| PlanPreview / ConfirmEnvelope DTO | `shared-dto-and-contract` § 5 |
| 4xx code（missing/expired/not_found/forbidden_team） | `error-model` § 5.1 |
| trace_id propagation 寫入 last_result | `observability-skeleton` § 6.4 |
| audit_log 寫入觸發點（consume 完成 / 失敗 / replay） | `audit-log` spec |
| reconciler 偵測 `applying` 滯留 | `reconciler-and-incident` spec |
| 對 LLM 的 `*_preview` description 必含 ALWAYS clause | `mcp-tool-description-lint` § 4.1 |
| 寫入 tool description 必含 NEVER clause | 同上 § 4.2 |

## 12. 驗證準則

| 驗證項 | 方式 | 通過條件 |
|---|---|---|
| Action 註冊完整 | backend 啟動 | `rbac.Action` 列舉每項都註冊；缺一 panic |
| Preview 產出含 PlanPreview | `:preview` 呼叫 | 200 + JSON 結構符合 `shared-dto-and-contract` § 5.1 |
| 缺 preview_id confirm | 主端點不帶 body | 400 missing_preview_id |
| 過期 preview confirm | 把 expires_at 改成過去 | 410 preview_expired |
| 跨 team 取 preview | team A 之 user confirm team B 的 preview | 404 preview_not_found |
| 跨 actor 取 preview | 同 team 異 user 之 token confirm | 404 preview_not_found；audit_log 含 `forbidden_actor` |
| 重試 = last_result 回放 | 同 preview_id confirm 第二次 | 回相同 body + `idempotent_replay: true`；副作用未重做（如 GitHub API mock 計數仍為 1） |
| FOR UPDATE race | 並發兩個 goroutine confirm 同 preview | 一成功（first commit）、一回 last_result（idempotent_replay: true） |
| Reversible 失敗反向 undo | mock 第二個 reversible effect fail | 第一個 effect 之 Compensate 被呼一次 |
| Irreversible 失敗不 undo | mock 第一個 irreversible effect fail | 已執行的 reversible 不被 undo；audit_log 寫 `rolled_back` |
| GC 過期未 consumed | 等 11 分鐘 + GC tick | preview row 被 DELETE；metric 計數 +1 |
| GC consumed 7 天保留 | 模擬 8 天前 consumed | DELETE；metric 累加（不在 expired 計數）|
| `last_result` 含 idempotent_replay | 首次與重試 | 首次 false、重試 true |
| Idempotency-Key 與 preview_id 衝突 | header 帶不同 key | 422 idempotency_key_conflict |
| Side_effects 純函式 | code review + lint（規則式抽查） | `SideEffects()` 內無 INSERT/UPDATE/DELETE/外部 HTTP 呼叫 |
| Preview ID 熵 | 100 個產出之分布 | 通過 NIST 隨機性簡單檢定（不含 timestamp / counter pattern） |
| Tx 失敗處理 | mock commit 失敗 | 503；副作用已執行（v1 已知行為） |

## 13. SLI 對應

| SLI | 目標 | 量測 |
|---|---|---|
| `:preview` p95 latency | < 800ms（與 plan SLO 一致） | `0ops_http_request_duration_seconds{route="*:preview"}` p95 |
| Confirm consume 成功率 | > 99% / 28d | `0ops_preview_consumed_total{outcome="success"} / total` |
| Preview consumption rate | > 80% / 7d（產品紅旗，與 ADR-0006 對齊） | `consumed / created` |
| Preview→confirm latency p50 | < 60s | `consumed_at - created_at` 之 p50（背景 query） |

## 14. 對 `docs/0ops-plan.md` 的修改清單

1. 「Backend：preview gate」段：交叉引用本 spec § 5 / § 6 為流程 source of truth
2. 「關鍵設計 #3」段：補入「`last_result.idempotent_replay` 旗標」「跨 actor 偷取 preview 回 404」
3. 「DB schema § preview」：補入「`idempotency_key` 預設 = `id`；`actor_user_id` 必驗於 confirm」（細節補在註解）
4. 「Risks & open #6（Preview race）」：交叉引用本 spec § 6.2 之 `FOR UPDATE` 流程

## 15. Open issues

> 來源：ADR-0002 § 9 之 6 條 OQ + 本 spec 撰寫期間新發現

- ADR-0002 OQ#1（`Idempotency-Key` 與 `preview_id` 衝突 422 vs RFC 9110）：本 spec 採 422；待 RFC 9110 idempotency draft 穩定後重審
- ADR-0002 OQ#2（last_result 過期 vs audit_log 引用）：v1 採 hard delete；audit_log 已存 preview_id + result snapshot，不需 reference stub
- ADR-0002 OQ#3（compensation 失敗的失敗）：本 spec 列為 `compensation_failed` 狀態 + reconciliation_job；具體狀態名待 `reconciler-and-incident` spec 釘
- ADR-0002 OQ#4（FOR UPDATE SKIP LOCKED）：v1 不開；reconciler 不掃 preview 表，無高頻爭用
- ADR-0002 OQ#5（args 標準化）：v1 不採 B4，無此需求
- ADR-0002 OQ#6（跨 binary preview_id 重用）：本 spec § 9.3 已釘允許（actor_user_id 一致即可）
- Tx commit 失敗 vs 副作用已執行：v1 簡化為 503 + client 重試風險；v1.1 評估 outbox 補強
- Side_effects 純函式 lint 工具：本 spec 暫靠 review；自動 lint 待 v1.1
- last_result 內可能含 secret（如 GitHub installation token）：須與 `error-model` § 9 之 redactor 對齊；本 spec 約束「last_result 寫入前必經 redactor」

## 16. 不可違反的硬性規則

> 違反以下任一項，PR 不可合入。

1. 寫入 / 刪除類主端點必經 `preview.Consume`；不可繞過直接執行副作用
2. `preview` 表 SQL 必含 `WHERE team_id = $1`；codegen 不得產出無 team_id 條件之 query
3. confirm tx 必使用 `SELECT ... FOR UPDATE` 鎖 preview row；不可降為 `SELECT` 或 `SELECT ... FOR SHARE`
4. `SideEffects()` callback 不得有副作用（無 INSERT/UPDATE/DELETE 寫 DB、無對外 HTTP 呼叫）
5. 副作用執行順序固定：所有 reversible 先、所有 irreversible 後；單一 effect 之 `Reversible` 旗標一旦標即不可在執行期間動態改變
6. 跨 team 或跨 actor 取用 preview 必回 `404 preview_not_found`，不得回 `403`（enumeration 防範）
7. `last_result` 寫入前必經 redactor（與 `error-model` § 9 對齊）；token / secret 不得進 last_result
8. preview_id 必為 UUID v4（128 bit 熵）；不得使用時間戳序列、短 hash、可猜模式
9. TTL 10 分鐘為常數；變更需 ADR 補丁；不得個別 endpoint 自行覆寫
10. action 註冊與 `rbac.Action` 列舉必對齊；backend 啟動時 fail-fast 檢查
