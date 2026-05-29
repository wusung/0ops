## 1. 結論（先讀本段）

- v1 範圍 ADR-0006 § 4 點 5 要求 trace_id 五段傳遞：HTTP middleware → slog context → GHA `repository_dispatch` payload → callback → `audit_log.trace_id`。
- 2026-05-29 audit 結果：deploy_run → GHA payload → callback → deploy_run 鏈路完整；但 middleware 端、preview 行、callback 端寫 audit 有三個 critical gap，等同 ADR-0005 § 4.6 「callback trace_id 必須 propagate 到 audit_log」未落實。
- 本 spec 範圍：修補 C1/C2/C3 三個 critical gap，補一支 e2e integration test 鎖住全鏈路；moderate gap M1/M2 列為 follow-up，不在此 PR。
- 單一 PR 交付，避免中間 broken 狀態（test 要全 fix 才能 green）。
- 不引入新 ADR — 既有 ADR-0005/0006 已定義契約，本 spec 為合規性補丁。

## 2. 範圍

### 2.1 包含

- `src/cmd/server/main.go` `requestTrace` middleware：把 resolved trace_id 透過 `audit.WithTraceID` 注入 request `context.Context`
- `preview` 表新增 `trace_id` 欄位 + 對應 `src/internal/server/db/members.go` `InsertPreview` inline SQL 寫入路徑
- `src/internal/server/redeploy/action.go` `Confirm`：建立 preview 行時帶入 trace_id
- `src/internal/server/apps.go` GHA callback handler：解析 payload `trace_id` 後立即 `audit.WithTraceID(ctx, payload.TraceID)`，後續 `ApplyDeployCallback` / `audit.Log` 沿用該 ctx
- 新檔 `src/internal/server/trace_propagation_test.go`：e2e integration test，golden path + negative case

### 2.2 不包含

- M1 `reconciliation_job.trace_id` 欄位：async job 已可由 `deploy_run.trace_id` 反查；屬 follow-up，不在此 PR
- M2 slog `ContextHandler` 自動注入 trace_id：屬 DX 改善，不影響資料正確性；follow-up
- preview 歷史資料回填：v1 dev only，prod 尚未上線，無歷史資料

## 3. 現況 Audit（2026-05-29）

| 鏈路段 | 狀態 | 證據 |
|---|---|---|
| HTTP 入口 → request log `trace_id` 欄 | ✅ | `src/cmd/server/main.go:192-207` `requestTrace` |
| HTTP 入口 → handler ctx | ⚠️ C1 | middleware 只 set response header，未 `r.WithContext(audit.WithTraceID(...))` |
| Preview 行持久化 | ⚠️ C2 | `migrations/00001_init.sql:101-115` 無 `trace_id` 欄位 |
| deploy_run 持久化 | ✅ | `migrations/00001_init.sql:76` `deploy_run.trace_id NOT NULL` |
| GHA `repository_dispatch` payload | ✅ | `src/internal/server/services/workflowdispatch/client.go:30` `ClientPayload.TraceID` |
| GHA callback → deploy_run | ✅ | `src/internal/server/apps.go:564-600` 校驗 + `ApplyDeployCallback` |
| Callback → audit_log | ⚠️ C3 | callback handler **完全未寫** audit_log entry（只更新 deploy_run + metrics）；ADR-0005 § 4.6 / ADR-0006 § 4 點 5 隱含 callback 必須留下 audit 痕跡 |
| Reconciler → audit_log | ✅ partial | `src/internal/server/services/reconciler/incident.go:130-142` 經 `OpenParams.TraceID` 傳遞 |

## 4. 變更詳細

### 4.1 C1 — middleware 注入 trace_id 到 ctx

**檔**：`src/cmd/server/main.go` `requestTrace`

**改動**：解出 `traceID` 後在傳給 `next.ServeHTTP` 之前：

```go
ctx := audit.WithTraceID(r.Context(), traceID)
next.ServeHTTP(w, r.WithContext(ctx))
```

**影響**：下游 handler 直接 `audit.TraceIDFromContext(ctx)` 取得；`audit.Service.Log` fallback chain `Entry.TraceID → WithTraceID → middleware.GetReqID → sentinel` 順序不變，但 (2) 從「沒人裝」變成「middleware 預設裝」。

### 4.2 C2 — preview.trace_id 欄位

**Migration**（新 goose 編號接續最新）：

```sql
-- +goose Up
ALTER TABLE preview ADD COLUMN trace_id text NOT NULL DEFAULT '00000000000000000000000000000000';

-- +goose Down
ALTER TABLE preview DROP COLUMN trace_id;
```

**Sentinel `'00000000000000000000000000000000'`** 與 `audit_log.trace_id` 既有 sentinel 一致（見 `src/internal/server/services/audit/log.go`），語意為「未填 / 未知」，與「空字串」區分。

**`src/internal/server/db/members.go`** `InsertPreview` inline SQL（line 214）：欄位列 + placeholder 加 `trace_id`；同檔 `Preview` struct + scan rows 加對應欄位。preview 不走 sqlc generated code，無 codegen 需重生。

**`src/internal/server/redeploy/action.go:Confirm`**：建立 preview 行時帶入已從 ctx / 參數取得的 traceID。

**Postgres 11+ 行為**：`ALTER TABLE ADD COLUMN ... NOT NULL DEFAULT ...` 不重寫資料頁，O(1) 變更。

### 4.3 C3 — callback handler 補 audit.Log 並用 payload trace_id

**檔**：`src/internal/server/apps.go` GHA callback handler

**現況**：handler 只呼 `ApplyDeployCallback`（更新 deploy_run）+ metric；無 audit_log 寫入。

**改動**：

1. 解析 `payload.TraceID` 並驗證非空後：

   ```go
   ctx := audit.WithTraceID(r.Context(), *traceID)
   ```

2. 把 ctx 傳給 `store.ApplyDeployCallback(ctx, …)`（取代既有 `r.Context()`）
3. `ApplyDeployCallback` 成功後加 audit 寫入：

   ```go
   _ = auditSvc.Log(ctx, audit.Entry{
       TeamID:      <由 deploy_run join 取得>,
       Source:      audit.SourceSystem,
       SubjectType: "deploy_run",
       SubjectID:   runID,
       Action:      "deploy.callback",
       Outcome:     <success / failed 由 status 映射>,
       Args:        <status, failure_classification>,
       Result:      <error_summary 等>,
   })
   ```

   `audit.Log` 失敗只記 metric 不阻斷 callback（spec § 15 hard rule #10：audit 不阻流，但 reconciler 會補）。

4. 介接 `auditSvc`：handler 透過 `deployRunCallbackHandler(store appsStore, auditSvc auditWriteService)` 簽章接入；`NewRouterWithReconciler` 之後的 router 工廠把 audit service 傳下來。

**新增 service 介面**：

```go
type auditWriteService interface {
    Log(ctx context.Context, entry audit.Entry) error
}
```

放在 `apps.go` 既有 service interface 區段，與 `auditQueryService` 並列。生產實作直接用 `*audit.Service`。

**符合 ADR**：ADR-0005 § 4.6 callback trace_id propagate 到 audit_log；ADR-0006 § 4 點 5 五段傳遞最後一段成立。

### 4.4 E2E integration test

**新檔**：`src/internal/server/trace_propagation_test.go`

**設定**：testcontainer postgres + httptest server + mock `workflowdispatch.Client`（capture payload）。沿用 `src/internal/server/` 內既有 integration test 模式（如有；若無，建立最小可重用 helper）。

**Golden path**：

1. 對 backend 發 redeploy preview 請求，header `X-Trace-ID: 7a9d3c8e4b1f4a2e9c6d5b8a3f0e1d2c`
2. 對 backend 發 confirm 請求
3. 斷言：
   - `preview.trace_id` = const
   - `deploy_run.trace_id` = const
   - captured workflow payload `client_payload.trace_id` = const
4. 對 callback endpoint 發 POST，payload 帶同 trace_id，正確簽章
5. 斷言：
   - callback 寫入後 `audit_log` 最新 entry `trace_id` = const

**Negative case（鎖 C3）**：

1. 走完 golden path 1-3
2. 對 callback endpoint 發 POST，payload 帶**不同** trace_id（const-callback）
3. 斷言：
   - `deploy_run.trace_id` 被 callback overwrite 為 const-callback（既有行為）
   - `audit_log` 新增一筆 `action='deploy.callback'`、`subject_id=runID`、`trace_id=const-callback`（驗 C3 fix；證明 audit entry 來自 payload trace_id 而非 middleware 注入的 request trace_id）

## 5. 風險與排雷

| 風險 | 評估 | 處置 |
|---|---|---|
| C2 migration 對 prod | v1 未上線無歷史資料；Postgres 11+ ADD COLUMN NOT NULL DEFAULT 為 O(1) | 無需 backfill；migration 直接套 |
| C3 ctx 傳遞遺漏 | callback handler 內若有 audit / log 呼叫吃舊 ctx 即 silent regression | negative test case 強制鎖定；review 時搜 handler 內所有 audit/log call |
| `audit.WithTraceID` 二次呼叫覆寫 | 設計即為 inner 蓋過 outer | callback 用 payload trace_id 覆寫 middleware 設的 chi req id 是預期行為 |
| `Preview` struct 與其他讀取點不同步 | members.go 內 GetPreview / scan 多處需同步加 `trace_id` 欄 | review 時 grep `Preview{` 與 `SELECT.*FROM preview`，確保 struct 與 scan 對齊 |

## 6. PR 切分

單一 PR：所有 C1/C2/C3 + e2e test 同批。

理由：

- ADR-0006 § 4 點 5 五段傳遞為單一觀察性需求
- e2e test 要 C1/C2/C3 全 fix 才能 green
- 切多 PR 反而留中間 broken 狀態

## 7. 驗收

- e2e test 在 `src/internal/server/trace_propagation_test.go` 全綠
- `preview` 表 schema 含 `trace_id NOT NULL DEFAULT sentinel`
- `requestTrace` middleware diff 含 `r.WithContext(audit.WithTraceID(...))`
- callback handler diff 含 `audit.WithTraceID(ctx, payload.TraceID)`
- `tasks/todo.md` 「trace_id 全鏈路驗證」勾起
- `tasks/lessons.md` 追加：本次發現「verify 任務在 audit 結束後常變 fix 任務」的 pattern

## 8. Follow-up（不在本 PR）

- M1：`reconciliation_job` 加 `trace_id` 欄位 — async job 直接由 job table 反查；目前要 join deploy_run
- M2：`slog` `ContextHandler` 自動讀 ctx 注入 `trace_id` 欄 — 移除手動 `slog.With("trace_id", ...)` 需求

## 9. 對外文件同步

- `docs/0ops-plan.md`：trace_id 章節若仍寫「待 audit」，改為現況描述
- 不新增 ADR；既有 ADR-0005/0006 已定義契約，本 spec 為合規性補丁
