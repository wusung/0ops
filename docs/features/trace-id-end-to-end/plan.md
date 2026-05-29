# trace_id End-to-End Propagation — Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** 落實 ADR-0006 § 4 點 5 五段 trace_id 傳遞（HTTP → preview → deploy_run → GHA payload → callback → audit_log）。修補 C1（middleware ctx 注入）、C2（preview.trace_id 欄位）、C3（callback handler 補 audit.Log + 用 payload trace_id），加 e2e integration test 鎖住。

**Architecture:** 沿用既有 `audit.WithTraceID` / `audit.TraceIDFromContext` helper（`src/internal/server/services/audit/log.go:113-124`）作為跨層 trace_id 傳遞契約。middleware 端把 resolved trace_id 寫進 ctx；handler 內隨需求覆寫（callback 場景用 payload trace_id 覆寫 chi req id）；DB 端 `preview.trace_id` 與既有 `deploy_run.trace_id` 對齊。callback handler 新接 `auditWriteService` 介面，依 spec § 15 hard rule #10 audit 不阻流（log 失敗只記 metric）。

**Tech Stack:** Go 1.24+, `chi` router middleware, `pgx/v5`, goose migrations, standard `testing` package, `httptest` server for integration tests. testcontainer-go for postgres（沿用 `src/internal/server/` 既有 integration test 模式）。

---

## File Map

| 檔 | 動作 | 責任 |
|---|---|---|
| `src/migrations/00012_preview_trace_id.sql` | Create | 加 `preview.trace_id` 欄位 |
| `src/internal/server/db/members.go` | Modify | `Preview` struct、`CreatePreview`、`GetPreview` 加 trace_id |
| `src/internal/server/db/members_test.go` | Modify / Create | preview trace_id round-trip 測試 |
| `src/cmd/server/main.go` | Modify | `requestTrace` middleware 注入 ctx |
| `src/cmd/server/main_test.go` 或新檔 | Create | middleware 注入 ctx 驗證 |
| `src/internal/server/apps.go` | Modify | `deployRunCallbackHandler` 簽章 + `auditWriteService` interface + audit.Log call |
| `src/internal/server/callbacks_test.go` | Modify | 既有測試補傳 audit writer；新增 ctx-overwrite 測試 |
| `src/internal/server/trace_propagation_test.go` | Create | e2e integration test（golden + negative） |
| 所有 `CreatePreview` caller | Modify | 沿傳 ctx，無新參數（trace_id 從 ctx 讀） |
| `tasks/todo.md` | Modify | 勾起「trace_id 全鏈路驗證」 |
| `tasks/lessons.md` | Modify | 加 lesson「verify 任務常需 fix」 |

---

## Task 1: C2 — preview.trace_id Migration

**Files:**
- Create: `src/migrations/00012_preview_trace_id.sql`

- [ ] **Step 1: Write the migration**

Create `src/migrations/00012_preview_trace_id.sql`:

```sql
-- +goose Up

alter table preview
    add column trace_id text not null default '00000000000000000000000000000000';

-- +goose Down

alter table preview drop column trace_id;
```

**Why default sentinel：** 與 `audit_log.trace_id` 既有 missing sentinel 一致（見 `src/internal/server/services/audit/log.go:132` const `missingTraceSentinel`）。NOT NULL + DEFAULT 在 Postgres 11+ 為 O(1) 變更，不重寫 table。

- [ ] **Step 2: Apply migration locally and verify**

Run（依 `manage.sh` 慣例；本 repo 用 manage.sh 不用 Make）:

```bash
./manage.sh dev up                 # 確保 postgres 起來
./manage.sh dev migrate            # 跑 goose up
./manage.sh dev psql -c "\\d preview" | grep trace_id
```

Expected output 含 `trace_id | text | not null | '00000000000000000000000000000000'`。

- [ ] **Step 3: Commit**

```bash
git add src/migrations/00012_preview_trace_id.sql
git commit -m "feat(db): add preview.trace_id column with sentinel default"
```

---

## Task 2: C2 — Preview struct & CreatePreview accept trace_id

**Files:**
- Modify: `src/internal/server/db/members.go:50-60` (struct), `:190-230` (CreatePreview), `:233-273` (GetPreview)
- Test: `src/internal/server/db/members_test.go`（若無，新建）

- [ ] **Step 1: Write the failing test**

Add to `members_test.go`（或新建檔）：

```go
func TestCreatePreviewPersistsTraceIDFromContext(t *testing.T) {
    repo, cleanup := newTestRepo(t)
    defer cleanup()
    teamID, actorID := seedTeamAndOwner(t, repo)

    const traceID = "7a9d3c8e4b1f4a2e9c6d5b8a3f0e1d2c"
    ctx := audit.WithTraceID(context.Background(), traceID)

    pv, err := repo.CreatePreview(ctx, teamID, actorID, "app.create", json.RawMessage(`{}`), "")
    if err != nil {
        t.Fatalf("CreatePreview: %v", err)
    }
    if pv.TraceID != traceID {
        t.Fatalf("returned Preview.TraceID = %q, want %q", pv.TraceID, traceID)
    }

    got, err := repo.GetPreview(context.Background(), pv.ID)
    if err != nil {
        t.Fatalf("GetPreview: %v", err)
    }
    if got.TraceID != traceID {
        t.Fatalf("loaded Preview.TraceID = %q, want %q", got.TraceID, traceID)
    }
}

func TestCreatePreviewMissingTraceIDFallsBackToSentinel(t *testing.T) {
    repo, cleanup := newTestRepo(t)
    defer cleanup()
    teamID, actorID := seedTeamAndOwner(t, repo)

    pv, err := repo.CreatePreview(context.Background(), teamID, actorID, "app.create", json.RawMessage(`{}`), "")
    if err != nil {
        t.Fatalf("CreatePreview: %v", err)
    }
    const sentinel = "00000000000000000000000000000000"
    if pv.TraceID != sentinel {
        t.Fatalf("Preview.TraceID = %q, want sentinel %q", pv.TraceID, sentinel)
    }
}
```

**Helpers**：`newTestRepo` 與 `seedTeamAndOwner` 沿用既有 `members_test.go` 或 `apps_infra_test.go` 模式（testcontainer postgres）。若不存在則本 Task 內補最小 helper（不在範圍內的 setup 拷貝）。

- [ ] **Step 2: Run test to verify it fails**

```bash
go -C src test ./internal/server/db/... -run TestCreatePreview -v
```

Expected: FAIL（`Preview` struct 無 `TraceID` 欄位 → compile error；或 SELECT scan 欄數不對）。

- [ ] **Step 3: Update Preview struct**

Edit `src/internal/server/db/members.go:50-60`：

```go
//nolint:revive // exported for public API
type Preview struct {
    ID          string
    TeamID      string
    ActorUserID string
    Action      string
    Args        json.RawMessage
    LastResult  json.RawMessage
    TraceID     string
    CreatedAt   time.Time
    ExpiresAt   time.Time
    ConsumedAt  *time.Time
}
```

- [ ] **Step 4: Update CreatePreview to read trace_id from ctx and persist**

Edit `src/internal/server/db/members.go:190-230`. 改動兩處：

1. import `"github.com/yourorg/0ops/internal/server/services/audit"` —— 確認 import path（讀 `members.go` 開頭 import block 對齊）
2. INSERT SQL 加 `trace_id` 欄與 placeholder，RETURNING 加 `trace_id`：

```go
//nolint:revive // exported for public API
func (r *Repository) CreatePreview(ctx context.Context, teamID, actorUserID, action string, args json.RawMessage, actionSummary string) (Preview, error) {
    parsedTeamID, err := parseUUID(teamID)
    if err != nil {
        return Preview{}, fmt.Errorf("parse team id: %w", err)
    }
    parsedActorID, err := parseUUID(actorUserID)
    if err != nil {
        return Preview{}, fmt.Errorf("parse actor id: %w", err)
    }
    if len(args) == 0 {
        args = json.RawMessage(`{}`)
    }

    key, err := randomKey()
    if err != nil {
        return Preview{}, err
    }

    traceID := audit.TraceIDFromContext(ctx)

    var (
        id        pgtype.UUID
        createdAt pgtype.Timestamptz
        expiresAt pgtype.Timestamptz
        rowTrace  string
    )
    if err := r.pool.QueryRow(ctx, `
INSERT INTO preview (team_id, actor_user_id, action, args, action_summary, side_effects, idempotency_key, expires_at, trace_id)
VALUES ($1, $2, $3, $4::jsonb, $5, '[]'::jsonb, $6, now() + interval '10 minute', $7)
RETURNING id, created_at, expires_at, trace_id
`, parsedTeamID, parsedActorID, action, []byte(args), actionSummary, key, traceID).Scan(&id, &createdAt, &expiresAt, &rowTrace); err != nil {
        return Preview{}, err
    }

    return Preview{
        ID:          id.String(),
        TeamID:      teamID,
        ActorUserID: actorUserID,
        Action:      action,
        Args:        args,
        TraceID:     rowTrace,
        CreatedAt:   createdAt.Time,
        ExpiresAt:   expiresAt.Time,
    }, nil
}
```

**Why read trace_id from ctx instead of param：** `CreatePreview` 已被多處 caller 呼叫；改 signature 會擴散。讀 ctx 是 Go 既有 pattern，與 `audit.Service.Log` 一致。middleware 在 Task 4 注入 ctx，所有 caller 自動受惠。

- [ ] **Step 5: Update GetPreview to load trace_id**

Edit `src/internal/server/db/members.go:233-273`：

```go
//nolint:revive // exported for public API
func (r *Repository) GetPreview(ctx context.Context, previewID string) (Preview, error) {
    parsedPreviewID, err := parseUUID(previewID)
    if err != nil {
        return Preview{}, fmt.Errorf("parse preview id: %w", err)
    }

    var (
        id          pgtype.UUID
        teamID      pgtype.UUID
        actorUserID pgtype.UUID
        action      string
        args        []byte
        lastResult  []byte
        traceID     string
        createdAt   pgtype.Timestamptz
        expiresAt   pgtype.Timestamptz
        consumedAt  pgtype.Timestamptz
    )

    if err := r.pool.QueryRow(ctx, `
SELECT id, team_id, actor_user_id, action, args, last_result, trace_id, created_at, expires_at, consumed_at
FROM preview
WHERE id = $1
`, parsedPreviewID).Scan(&id, &teamID, &actorUserID, &action, &args, &lastResult, &traceID, &createdAt, &expiresAt, &consumedAt); err != nil {
        if errors.Is(err, pgx.ErrNoRows) {
            return Preview{}, ErrPreviewNotFound
        }
        return Preview{}, err
    }

    return Preview{
        ID:          id.String(),
        TeamID:      teamID.String(),
        ActorUserID: actorUserID.String(),
        Action:      action,
        Args:        json.RawMessage(args),
        LastResult:  json.RawMessage(lastResult),
        TraceID:     traceID,
        CreatedAt:   createdAt.Time,
        ExpiresAt:   expiresAt.Time,
        ConsumedAt:  timestamptzPtr(consumedAt),
    }, nil
}
```

- [ ] **Step 6: Run tests to verify they pass**

```bash
go -C src test ./internal/server/db/... -run TestCreatePreview -v
```

Expected: PASS（兩支測試都綠）。

- [ ] **Step 7: Run full package tests to catch regressions**

```bash
go -C src test ./internal/server/db/... -v
```

Expected: PASS（既有 preview 測試不應受影響；若有 select trace_id 漏改的點會在這裡爆）。

- [ ] **Step 8: Commit**

```bash
git add src/internal/server/db/members.go src/internal/server/db/members_test.go
git commit -m "feat(db): persist preview.trace_id via ctx; round-trip test"
```

---

## Task 3: C1 — Middleware injects trace_id into request ctx

**Files:**
- Modify: `src/cmd/server/main.go:189-214` (`requestTrace`)
- Test: `src/cmd/server/main_test.go`（若無，新建）

- [ ] **Step 1: Write the failing test**

Add to `src/cmd/server/main_test.go`：

```go
package main

import (
    "context"
    "net/http"
    "net/http/httptest"
    "testing"

    "github.com/yourorg/0ops/internal/server/services/audit"
    // 對齊既有 import path；若 main.go import 為 internal/audit 則沿用
)

func TestRequestTraceInjectsTraceIDIntoContext(t *testing.T) {
    var gotFromCtx string
    handler := http.HandlerFunc(func(_ http.ResponseWriter, r *http.Request) {
        gotFromCtx = audit.TraceIDFromContext(r.Context())
    })

    middleware := requestTrace(testLogger(t))
    req := httptest.NewRequest(http.MethodGet, "/x", nil)
    req.Header.Set("X-Trace-ID", "7a9d3c8e4b1f4a2e9c6d5b8a3f0e1d2c")
    middleware(handler).ServeHTTP(httptest.NewRecorder(), req)

    if gotFromCtx != "7a9d3c8e4b1f4a2e9c6d5b8a3f0e1d2c" {
        t.Fatalf("TraceIDFromContext = %q, want header value", gotFromCtx)
    }
}

func TestRequestTraceFallsBackToGeneratedID(t *testing.T) {
    var gotFromCtx string
    handler := http.HandlerFunc(func(_ http.ResponseWriter, r *http.Request) {
        gotFromCtx = audit.TraceIDFromContext(r.Context())
    })

    middleware := requestTrace(testLogger(t))
    req := httptest.NewRequest(http.MethodGet, "/x", nil)
    // no header — middleware will fall back to chi GetReqID or sentinel
    middleware(handler).ServeHTTP(httptest.NewRecorder(), req)

    if gotFromCtx == "" {
        t.Fatalf("expected non-empty trace_id from middleware fallback")
    }
    _ = context.Background()
}

func testLogger(t *testing.T) *slog.Logger {
    t.Helper()
    return slog.New(slog.NewTextHandler(io.Discard, nil))
}
```

- [ ] **Step 2: Run test to verify it fails**

```bash
go -C src test ./cmd/server/... -run TestRequestTrace -v
```

Expected: FAIL — `gotFromCtx` 為 sentinel 或空字串，因 middleware 沒注入 ctx。

- [ ] **Step 3: Update requestTrace to inject ctx**

Edit `src/cmd/server/main.go:189-214`：

```go
func requestTrace(logger *slog.Logger) func(http.Handler) http.Handler {
    return func(next http.Handler) http.Handler {
        return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
            traceID := strings.TrimSpace(r.Header.Get("X-Trace-ID"))
            if traceID == "" {
                traceID = strings.TrimSpace(r.Header.Get("X-Request-ID"))
            }
            if traceID == "" {
                traceID = middleware.GetReqID(r.Context())
            }
            if traceID == "" {
                traceID = "trace-missing"
            }
            w.Header().Set("X-Trace-ID", traceID)

            ctx := audit.WithTraceID(r.Context(), traceID)

            start := time.Now()
            next.ServeHTTP(w, r.WithContext(ctx))
            logger.Info("http request completed",
                "trace_id", traceID,
                "method", r.Method,
                "path", r.URL.Path,
                "duration_ms", time.Since(start).Milliseconds(),
            )
        })
    }
}
```

確認 `audit` import 已在 `main.go` import block；若無，加入（path 對齊既有 server import 寫法）。

- [ ] **Step 4: Run tests to verify they pass**

```bash
go -C src test ./cmd/server/... -run TestRequestTrace -v
```

Expected: PASS（兩支綠）。

- [ ] **Step 5: Commit**

```bash
git add src/cmd/server/main.go src/cmd/server/main_test.go
git commit -m "feat(server): inject trace_id into request ctx via middleware"
```

---

## Task 4: C3 — Callback handler emits audit.Log with payload trace_id

**Files:**
- Modify: `src/internal/server/apps.go:522-623` (`deployRunCallbackHandler`) + interface 區段 + router factory 簽章 + call sites
- Modify: `src/internal/server/apps_test.go` (callback handler 既有 fake store 簽章)
- Modify: `src/internal/server/callbacks_test.go` (補 audit writer)

- [ ] **Step 1: Search call sites of deployRunCallbackHandler and NewRouter\* factories**

```bash
rg -n "deployRunCallbackHandler|NewRouterWithReconciler|NewRouterWithIngestion|NewRouterWithAudit|NewRouterWithRateLimitAndAudit|newRouterFull" src/internal/server src/cmd
```

Make a note of every call site to update in Step 4.

- [ ] **Step 2: Write the failing test**

Add to `src/internal/server/callbacks_test.go`（補在現有測試後）：

```go
func TestDeployCallbackWritesAuditLogWithPayloadTraceID(t *testing.T) {
    t.Setenv("OPS_CALLBACK_SECRET", "test-webhook-secret")
    store, _ := newFakeStore()
    auditWriter := &fakeAuditWriter{}

    srv := httptest.NewServer(NewRouterWithCallbackAudit(store, auditWriter))
    t.Cleanup(srv.Close)

    body := `{"run_id":"deploy-1","status":"success","trace_id":"callback-trace-xyz"}`
    ts := strconv.FormatInt(time.Now().UTC().Unix(), 10)
    mac := hmac.New(sha256.New, []byte("test-webhook-secret"))
    _, _ = mac.Write([]byte(ts + "." + body))
    sig := "sha256=" + hex.EncodeToString(mac.Sum(nil))

    req, _ := http.NewRequest(http.MethodPost, srv.URL+"/internal/deploy-runs/deploy-1/callback", strings.NewReader(body))
    req.Header.Set("Content-Type", "application/json")
    req.Header.Set("X-0ops-Timestamp", ts)
    req.Header.Set("X-0ops-Signature", sig)
    req.Header.Set("X-Trace-ID", "request-trace-aaa") // simulates middleware path

    resp, err := http.DefaultClient.Do(req)
    if err != nil {
        t.Fatalf("do: %v", err)
    }
    defer resp.Body.Close()
    if resp.StatusCode != http.StatusOK {
        t.Fatalf("status = %d", resp.StatusCode)
    }

    if len(auditWriter.entries) != 1 {
        t.Fatalf("expected 1 audit entry, got %d", len(auditWriter.entries))
    }
    entry := auditWriter.entries[0]
    if entry.Action != "deploy.callback" {
        t.Errorf("Action = %q, want deploy.callback", entry.Action)
    }
    if entry.SubjectID != "deploy-1" {
        t.Errorf("SubjectID = %q, want deploy-1", entry.SubjectID)
    }
    // Trace id must come from callback payload, NOT from X-Trace-ID header
    if entry.TraceID != "callback-trace-xyz" {
        t.Errorf("TraceID = %q, want callback-trace-xyz (from payload, not request header)", entry.TraceID)
    }
}

type fakeAuditWriter struct {
    entries []audit.Entry
}

func (f *fakeAuditWriter) Log(_ context.Context, entry audit.Entry) error {
    f.entries = append(f.entries, entry)
    return nil
}
```

**Note**：`NewRouterWithCallbackAudit` 是新 router factory 暴露 audit writer 給 callback；Step 4 會建立。

- [ ] **Step 3: Run test to verify it fails**

```bash
go -C src test ./internal/server/... -run TestDeployCallbackWritesAuditLogWithPayloadTraceID -v
```

Expected: FAIL（compile error：`NewRouterWithCallbackAudit` 不存在；或 audit entries 為空）。

- [ ] **Step 4: Add auditWriteService interface + thread through router factory + callback handler**

In `src/internal/server/apps.go`, locate the service interface block near `auditQueryService` (search for `auditQueryService` to find the area). Add：

```go
type auditWriteService interface {
    Log(ctx context.Context, entry audit.Entry) error
}
```

確認 import block 含 `"github.com/yourorg/0ops/internal/server/services/audit"`。

修改 `deployRunCallbackHandler` 簽章與內部：

```go
func deployRunCallbackHandler(store appsStore, auditWriter auditWriteService) http.HandlerFunc {
    return func(w http.ResponseWriter, r *http.Request) {
        body, err := io.ReadAll(r.Body)
        if err != nil {
            apperror.Write(w, "validation_failed", apperror.ClassBadRequest, "invalid payload", nil)
            return
        }

        runID := strings.TrimSpace(chi.URLParam(r, "run_id"))
        if runID == "" {
            apperror.Write(w, "validation_failed", apperror.ClassBadRequest, "run_id is required", nil)
            return
        }

        var req deployCallbackRequest
        if err := json.Unmarshal(body, &req); err != nil {
            apperror.Write(w, "validation_failed", apperror.ClassBadRequest, "invalid json payload", nil)
            return
        }
        slog.Info("callback received", "run_id", runID, "status", req.Status, "trace_id", trimStringPtr(req.TraceID))
        // ... 既有 timestamp/signature/runID/status 校驗保持不變 ...

        traceID := trimStringPtr(req.TraceID)
        if traceID == nil {
            apperror.Write(w, "validation_failed", apperror.ClassBadRequest, "trace_id is required", map[string]any{"field": "trace_id"})
            return
        }

        // Overwrite ctx trace_id with payload's — payload is the source of truth here
        ctx := audit.WithTraceID(r.Context(), *traceID)

        // ... 既有 failureClassification / deliveryID / RegisterWebhookDelivery 校驗保持不變，
        //     但把 r.Context() 改成 ctx ...

        err = store.ApplyDeployCallback(ctx, db.DeployCallbackParams{
            RunID:                 runID,
            Status:                status,
            TraceID:               traceID,
            ErrorSummary:          trimStringPtr(req.ErrorSummary),
            FailureClassification: failureClassification,
            Event:                 buildDeployCallbackEvent(req, status),
        })
        if err != nil {
            // ... 既有 error handling ...
            return
        }

        // ADR-0006 § 4 段 5：callback 必須留下 audit 痕跡。
        // spec § 15 hard rule #10：audit 不阻流；log 失敗只記 slog warn。
        if auditWriter != nil {
            outcome := audit.OutcomeSuccess
            if status == "failed" {
                outcome = audit.OutcomeFailure
            }
            entry := audit.Entry{
                TeamID:      "", // populated by store.ApplyDeployCallback path? 由 deploy_run 反查
                Source:      audit.SourceSystem,
                SubjectType: "deploy_run",
                SubjectID:   runID,
                Action:      "deploy.callback",
                Outcome:     outcome,
                Args:        map[string]any{"status": req.Status, "failure_classification": failureClassification},
                Result:      map[string]any{"error_summary": trimStringPtr(req.ErrorSummary)},
            }
            if err := auditWriter.Log(ctx, entry); err != nil {
                slog.Warn("audit log write failed", "run_id", runID, "err", err)
            }
        }

        // ... 既有 metric / response 不變 ...
    }
}
```

**Important — TeamID resolution**：`audit.Entry.TeamID` is required (`prepareInsert` validates non-empty)。`ApplyDeployCallback` 已 join deploy_run 並更新；我們要在新增一個 lookup：在 callback handler 內擴 `appsStore` interface 加 `GetDeployRunTeamID(ctx, runID) (string, error)`，或讓 `ApplyDeployCallback` return team_id。**選後者**：擴 `db.DeployCallbackParams` 對應 return 或讓 `ApplyDeployCallback` 改回傳 `(teamID string, err error)`。確切 signature 在實作時讀 `src/internal/server/db/apps.go:374` 後決定，但 plan 鎖定方向：callback handler 拿到 team_id 才能寫 audit。

If too invasive: pure additive — add `store.GetDeployRunTeamID(ctx, runID)` method on `*Repository` + interface. 較不動現有 callback DB 路徑。**建議走這條**。

更新後 callback handler 寫 audit 前：

```go
teamID, err := store.GetDeployRunTeamID(ctx, runID)
if err != nil || teamID == "" {
    slog.Warn("audit skipped: team lookup failed", "run_id", runID, "err", err)
} else {
    entry.TeamID = teamID
    if err := auditWriter.Log(ctx, entry); err != nil {
        slog.Warn("audit log write failed", "run_id", runID, "err", err)
    }
}
```

- [ ] **Step 5: Add NewRouterWithCallbackAudit factory + thread audit writer through all router factories**

`src/internal/server/apps.go` 既有 router factories（`NewRouterWithAudit`、`NewRouterWithRateLimitAndAudit`、`NewRouterWithReconciler`、`NewRouterWithIngestion`、`newRouterFull`）都接 `auditSvc auditQueryService`。

擴 `newRouterFull` 簽章追加 `callbackAuditWriter auditWriteService`，往下傳給 `deployRunCallbackHandler(store, callbackAuditWriter)`。所有 outer factory 加對應參數，舊呼叫點傳 `nil`（auditWriter nil 時 handler 內 skip — 已在 Step 4 寫好 nil guard）。

新增測試專用 factory：

```go
func NewRouterWithCallbackAudit(store routerStore, auditWriter auditWriteService) http.Handler {
    return newRouterFull(store, githubClient, nil, nil, nil, nil, nil, nil, nil, nil, nil, auditWriter)
}
```

簽章對齊 `newRouterFull` 順序；最後一個位置加 auditWriter。

- [ ] **Step 6: Add GetDeployRunTeamID method on Repository + interface**

Add to `appsStore` interface in `apps.go`：

```go
GetDeployRunTeamID(ctx context.Context, runID string) (string, error)
```

Add to `src/internal/server/db/apps.go`：

```go
func (r *Repository) GetDeployRunTeamID(ctx context.Context, runID string) (string, error) {
    parsedRunID, err := parseUUID(runID)
    if err != nil {
        return "", fmt.Errorf("parse run id: %w", err)
    }
    var teamID pgtype.UUID
    if err := r.pool.QueryRow(ctx,
        `SELECT team_id FROM deploy_run WHERE id = $1`,
        parsedRunID).Scan(&teamID); err != nil {
        return "", err
    }
    return teamID.String(), nil
}
```

Update fake stores in `apps_test.go`, `cli/apps_test.go`, `mcp/server/server_test.go` to satisfy new interface method (return empty string + nil).

- [ ] **Step 7: Run test to verify it passes**

```bash
go -C src test ./internal/server/... -run TestDeployCallbackWritesAuditLogWithPayloadTraceID -v
```

Expected: PASS。

- [ ] **Step 8: Run full server package test to catch interface regressions**

```bash
go -C src test ./internal/server/... -v
```

Expected: PASS（含既有 callback test、apps test）。

- [ ] **Step 9: Commit**

```bash
git add src/internal/server/apps.go src/internal/server/db/apps.go src/internal/server/callbacks_test.go src/internal/server/apps_test.go src/internal/cli/apps_test.go src/internal/mcp/server/server_test.go
git commit -m "feat(callback): emit audit.Log with payload trace_id"
```

---

## Task 5: E2E Integration Test — Full Chain

**Files:**
- Create: `src/internal/server/trace_propagation_test.go`

- [ ] **Step 1: Survey existing integration test setup**

```bash
rg -n "testcontainer|pgxpool\.New|TestMain" src/internal/server | head -30
```

Identify the helper that spins up postgres + applies migrations + builds `*db.Repository`. Reuse it. If multiple patterns exist, prefer the one used by `apps_infra_test.go` (highest fidelity).

- [ ] **Step 2: Write the failing test — golden path**

Create `src/internal/server/trace_propagation_test.go`：

```go
package server

import (
    "encoding/json"
    "net/http"
    "net/http/httptest"
    "strings"
    "testing"

    // ... imports for audit, db, hmac signing helpers ...
)

const (
    requestTraceID  = "7a9d3c8e4b1f4a2e9c6d5b8a3f0e1d2c"
    callbackTraceID = "callbacka1b2c3d4e5f60718293a4b5c"
)

func TestTracePropagationFullChain(t *testing.T) {
    repo, cleanup := newIntegrationRepo(t)
    defer cleanup()
    teamID, actorID := seedTeamAndOwner(t, repo)
    appID := seedApp(t, repo, teamID)

    capturedPayload := make(chan workflowdispatch.ClientPayload, 1)
    fakeDispatch := &fakeDispatchClient{onDispatch: func(p workflowdispatch.ClientPayload) {
        capturedPayload <- p
    }}

    srv := httptest.NewServer(newTestRouter(repo, fakeDispatch, nil))
    t.Cleanup(srv.Close)

    // 1. Preview redeploy with X-Trace-ID header
    previewID := postPreview(t, srv.URL, requestTraceID, teamID, appID, actorID)

    // 2. Confirm redeploy → triggers deploy_run + GHA dispatch
    runID := postConfirm(t, srv.URL, requestTraceID, previewID, teamID, actorID)

    // 3. Assert preview.trace_id
    pv, err := repo.GetPreview(t.Context(), previewID)
    if err != nil {
        t.Fatalf("GetPreview: %v", err)
    }
    if pv.TraceID != requestTraceID {
        t.Fatalf("preview.trace_id = %q, want %q", pv.TraceID, requestTraceID)
    }

    // 4. Assert deploy_run.trace_id
    runRow := loadDeployRun(t, repo, runID)
    if runRow.TraceID != requestTraceID {
        t.Fatalf("deploy_run.trace_id = %q, want %q", runRow.TraceID, requestTraceID)
    }

    // 5. Assert GHA workflow payload trace_id
    select {
    case p := <-capturedPayload:
        if p.TraceID != requestTraceID {
            t.Fatalf("workflow payload trace_id = %q, want %q", p.TraceID, requestTraceID)
        }
    default:
        t.Fatalf("expected workflow dispatch, got none")
    }
}
```

- [ ] **Step 3: Write the failing negative case — C3 rocks**

Add to the same file:

```go
func TestCallbackAuditUsesPayloadTraceIDNotRequestTraceID(t *testing.T) {
    repo, cleanup := newIntegrationRepo(t)
    defer cleanup()
    teamID, actorID := seedTeamAndOwner(t, repo)
    appID := seedApp(t, repo, teamID)
    runID := seedDeployRun(t, repo, teamID, appID, requestTraceID)

    auditWriter := audit.NewService(repo, repo, audit.NopObserver())
    srv := httptest.NewServer(NewRouterWithCallbackAudit(repo, auditWriter))
    t.Cleanup(srv.Close)

    body := callbackBody(t, runID, "success", callbackTraceID)
    ts, sig := signCallback(t, body)

    req, _ := http.NewRequest(http.MethodPost, srv.URL+"/internal/deploy-runs/"+runID+"/callback", strings.NewReader(body))
    req.Header.Set("Content-Type", "application/json")
    req.Header.Set("X-0ops-Timestamp", ts)
    req.Header.Set("X-0ops-Signature", sig)
    req.Header.Set("X-Trace-ID", requestTraceID) // middleware will inject this; payload should win

    resp, err := http.DefaultClient.Do(req)
    if err != nil {
        t.Fatalf("do: %v", err)
    }
    defer resp.Body.Close()
    if resp.StatusCode != http.StatusOK {
        t.Fatalf("callback status = %d", resp.StatusCode)
    }

    // Assert audit_log entry: action='deploy.callback', trace_id=callback payload's
    entries := repo.ListAuditByAction(t.Context(), teamID, "deploy.callback")
    if len(entries) != 1 {
        t.Fatalf("expected 1 audit entry, got %d", len(entries))
    }
    if entries[0].TraceID != callbackTraceID {
        t.Fatalf("audit_log.trace_id = %q, want %q (payload, not request header)", entries[0].TraceID, callbackTraceID)
    }
    if entries[0].SubjectID != runID {
        t.Errorf("audit_log.subject_id = %q, want %q", entries[0].SubjectID, runID)
    }
}
```

**Helpers needed**：`postPreview`, `postConfirm`, `loadDeployRun`, `callbackBody`, `signCallback`, `seedTeamAndOwner`, `seedApp`, `seedDeployRun`, `newTestRouter`, `newIntegrationRepo`, `fakeDispatchClient`, `repo.ListAuditByAction`. Place helpers in same file or `trace_propagation_helpers_test.go`. Sign helper mirrors `callbacks_test.go:34-37`.

- [ ] **Step 4: Run tests to verify they fail (showing current state)**

```bash
go -C src test ./internal/server/... -run TestTracePropagation -v
go -C src test ./internal/server/... -run TestCallbackAuditUsesPayloadTraceIDNotRequestTraceID -v
```

Expected: FAIL — preview.trace_id 不存在欄位（已修但若 Task 1-2 順序錯就會 fail）；或 fakeDispatchClient 收不到 payload；或 audit entries 為空。

If all prior Tasks were committed correctly, **the only remaining failure should be helpers / wiring**.

- [ ] **Step 5: Implement helpers and wiring until both tests pass**

iteratively implement helpers. Aim for minimum viable plumbing — DO NOT add new business logic in helpers.

- [ ] **Step 6: Run tests to verify they pass**

```bash
go -C src test ./internal/server/... -run "TestTracePropagation|TestCallbackAuditUsesPayloadTraceIDNotRequestTraceID" -v
```

Expected: PASS（both）。

- [ ] **Step 7: Commit**

```bash
git add src/internal/server/trace_propagation_test.go
git commit -m "test(server): e2e trace_id propagation + callback audit"
```

---

## Task 6: Sync tasks/ tracking docs

**Files:**
- Modify: `tasks/todo.md`
- Modify: `tasks/lessons.md`

- [ ] **Step 1: Mark todo.md item complete**

Edit `tasks/todo.md` 「v1 收尾殘留」section：

```markdown
- [x] **trace_id 全鏈路驗證**
  - 結果：C1/C2/C3 fix + e2e test 已 ship；M1 (`reconciliation_job.trace_id`) 與 M2 (slog ContextHandler) 列為 v1.x follow-up
  - PR：<本 PR 編號>
```

- [ ] **Step 2: Add lesson**

Append to `tasks/lessons.md`：

```markdown
## verify 任務在 audit 結束後常變 fix 任務（2026-05-29）

`trace_id 全鏈路驗證` task 原始描述為 verify-only，但 audit 結果發現
3 個 critical gap（middleware ctx 注入、preview.trace_id 欄位、callback
缺 audit.Log）已違 ADR-0005 / ADR-0006。

**Pattern**：標 verify 的 task 進場前要區分：
- 真 verify（only grep + 寫 test 鎖住現狀）
- 隱含 fix（audit 結束發現規格未實作 → 應建獨立 task 並改 PR 範圍）

**動作**：audit 階段結束後立即更新 spec（accurate ground truth），
避免 plan 階段用舊假設展開。本次發現「C3 不是 wrong trace，是 no audit
at all」就是延遲修正導致 plan 重寫的成本。
```

- [ ] **Step 3: Commit**

```bash
git add tasks/todo.md tasks/lessons.md
git commit -m "docs(tasks): close trace_id verification; record verify→fix lesson"
```

---

## Self-Review Checklist (run before declaring plan done)

- [ ] All 4 fix scopes from spec § 1 (C1/C2/C3 + e2e test) covered by Tasks 1-5
- [ ] Each step shows actual code (no "implement X" placeholders)
- [ ] All file paths use absolute repo-relative form (e.g. `src/internal/server/...`)
- [ ] No reference to symbols not defined elsewhere in plan or codebase
- [ ] Type consistency: `Preview.TraceID string`, `audit.Entry.TraceID string`, `audit.WithTraceID(ctx, traceID string)` all match
- [ ] Run commands exact: `go -C src test ./internal/server/db/...` (per repo convention `go -C src`)
- [ ] Test names match between "write test" and "run test" steps
- [ ] No M1/M2 leakage into Tasks (deferred per spec § 8)

## Post-Plan Hand-off

After all Tasks green:

1. Push `feat/trace-id-end-to-end` to origin
2. `gh pr create` — title `feat(trace-id): end-to-end propagation across middleware/preview/callback`, body 連 `docs/features/trace-id-end-to-end/spec.md` 與本 plan
3. Verify CI green
4. Self-merge once review passes
