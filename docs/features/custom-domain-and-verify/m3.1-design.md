# M3.1 — domainverify Package Design

> Status: Draft
> Scope: Task M3.1「客戶自有域名 DNS verify (24h TTL + extend)」
> Source: `docs/features/custom-domain-and-verify/spec.md` § 1-15、`docs/adrs/0007-customer-domain-tls.md`

## 1. 結論（先讀本段）

- 在 `internal/server/services/domainverify/` 落地 spec § 3 列出之模組；本 task 只交付**包級可獨立測試的核心邏輯**：apex 偵測、hostname 驗證、verification token、雙條件 DNS verify、24h TTL + extend × 2、7 天 grace 狀態機、poller。
- **不**含 router (`internal/server/routers/domains.go`)、`migrations/000X_domain_binding_indexes.sql` 與 apps.go saga 整合；上述項屬 M3 wrap-up 範圍（schema 補欄位涉及 cross-cutting，與 M3.2 GitHub App / M5.x reconciler GA 一併規劃）。`doc.go` 顯式列出未整合銜接點。
- 用 interface 抽離 Store / Resolver / Cloudflare client / Clock；測試以 fake 注入，不依 live DB / live DNS。
- 所有 hard rule § 15 由「型別 / function 簽章層」鎖死，不靠呼叫端紀律：apex 必經 `apex.Detect`、token 必經 `token.GenerateToken`、verify 必經 `verify.DualCondition`、extend 必經 `extend.Apply`。
- 不可變紀錄：依 ADR-0002 副作用為 reversible。本 task 只跑記憶體 / 接口層，無實際 Cloudflare 呼叫（介接點以 interface 預留）。

## 2. 範圍切割

### 2.1 本 task 交付
| 檔案 | 責任 |
|---|---|
| `doc.go` | 套件 doc，列出未整合銜接點 |
| `hostname.go` | hostname 字串驗證：RFC 1035、長度、reserved suffix `.winshare.tw` |
| `apex.go` | `golang.org/x/net/publicsuffix` 包裝 apex 偵測 |
| `apex_providers.go` | 不相容 DNS 供應商靜態清單 + 顯示用 helper |
| `token.go` | `crypto/rand` 32-byte hex token 產生 |
| `verify.go` | 雙條件 DNS verify 邏輯（CNAME/A + TXT），`Resolver` interface |
| `state.go` | `domain_binding.status` enum + 轉態合法性 |
| `extend.go` | extend × 2 規則（pure function 與 store-backed）|
| `service.go` | Add / Verify / Extend / Remove 主流程 over `Store` 介面 |
| `poller.go` | 30s ticker + leader hook；分派 `verifyPending` / `checkUnhealthy` / `cleanupExpired` |
| `grace.go` | 7 天 grace 規則（pure function）+ `checkUnhealthy` 整合 |
| `metrics.go` | `BindMetrics(...)` 與 `BindDurationMetric(...)` 介接點 |
| `*_test.go` | 上述每塊對應 fake-backed test |

### 2.2 不在本 task

| 項 | 為何延後 | 影響 |
|---|---|---|
| `migrations/0000X_domain_binding_extras.sql`（補 `is_apex` / `extends_used` / `health_check_failed_at` / `status`） | 改 schema 屬 M3 wrap-up / M3.2 並列任務範圍 | service 層暫以「邏輯欄位 + DomainBinding 記憶體型別」承接，poller test 用 in-memory store；router 接入時補 migration |
| `internal/server/routers/domains.go` | 路由整合屬 M3 wrap-up；M2.x 既有路由都集中在 `apps.go` chi router 內 | service 已暴露純函式 API；wiring 為 follow-up |
| `internal/server/apps.go` `add_domain` / `remove_domain` preview handler | 需 routerStore / preview/confirm 整合；屬整合工作 | 同上 |
| Cloudflare Custom Hostname API client 擴充 (`POST/PATCH/DELETE /custom_hostnames`) | 屬 `cloudflare` 套件擴充；本任務以 `CloudflareHostnameAPI` interface 預留 | follow-up wiring 時實作 |
| 0ops-gitops ingress.yaml 修改（驗證後 push） | 屬 `gitops-render-and-argocd` 套件擴充 | 同上 |
| audit_log 寫入 | 屬 `audit-log` spec 套件；本任務以 `Auditor` interface 預留 | 同上 |
| MCP `add_domain_preview` / `verify_domain` tools | 屬 `internal/mcp/`；follow-up | 同上 |
| CLI `0ops domains verify ... --extend` | 屬 `internal/cli/`；follow-up | 同上 |

> 切割原則：本 task 是 **policy 與 state machine 核心**；I/O 邊界（DB schema、Cloudflare HTTP、gitops Git push、HTTP router）由 interface 預留，留給 M3 wrap-up 接入。

## 3. 核心 type 與 interface

```go
// state.go
type Status string

const (
    StatusPending   Status = "pending"
    StatusVerified  Status = "verified"
    StatusUnhealthy Status = "unhealthy"
    StatusExpired   Status = "expired"
    StatusReleased  Status = "released"
)

// service.go
type Binding struct {
    ID                  string
    AppID               string
    TeamID              string
    Hostname            string
    Kind                string
    Status              Status
    Verified            bool
    VerificationToken   string
    IsApex              bool
    ExtendsUsed         int
    ExpiresAt           time.Time
    HealthCheckFailedAt *time.Time
    CFHostnameID        string
    CreatedAt           time.Time
    VerifiedAt          *time.Time
}

type Store interface {
    GetByHostname(ctx, hostname) (Binding, error)
    Insert(ctx, Binding) error
    UpdateVerified(ctx, id string, verifiedAt time.Time) error
    UpdateExpired(ctx, id string) error
    UpdateExtendsUsed(ctx, id string, extendsUsed int, expiresAt time.Time) error
    UpdateUnhealthyMark(ctx, id string, failedAt *time.Time) error
    UpdateReleased(ctx, id string) error
    ListPending(ctx) ([]Binding, error)        // verified=false, expires_at > now()
    ListVerified(ctx) ([]Binding, error)       // verified=true, kind='extra'
    ListExpiredCandidates(ctx) ([]Binding, error) // verified=false, expires_at <= now()
}

type Resolver interface {
    LookupCNAME(ctx, host) (string, error)
    LookupHost(ctx, host) ([]string, error)
    LookupTXT(ctx, host) ([]string, error)
}

type CloudflareHostnameAPI interface {
    RegisterCustomHostname(ctx, hostname string) (cfHostnameID string, err error)
    ActivateCustomHostname(ctx, cfHostnameID string) error
    DeleteCustomHostname(ctx, cfHostnameID string) error
}

type Auditor interface {
    Record(ctx, event AuditEvent) error
}

type LeaderProbe interface {
    IsLeader(ctx) bool
}

type Service struct {
    store        Store
    resolver     Resolver
    cf           CloudflareHostnameAPI
    auditor      Auditor
    now          func() time.Time
    tunnelTarget string  // e.g. "<tunnel_id>.cfargotunnel.com"
}
```

## 4. 關鍵 spec 對應

| spec § | 文件位置 | 落地 type / func |
|---|---|---|
| § 1 結論 | `doc.go` | 摘錄為 doc comment |
| § 4 狀態機 | `state.go` | `Status` 列舉 + `CanTransition(from, to)` |
| § 5.1 args | `service.go` `AddArgs` | `validateRequest` |
| § 5.2 preview 驗證 | `service.go` `Service.Add` | `hostname.Validate` → `apex.Detect` → `Store.GetByHostname` → token gen |
| § 5.3 side_effects | `service.go` 回傳 `Plan` | 不相容供應商列表整合自 `apex_providers.go` |
| § 6.3 雙條件 | `verify.go` `DualCondition` | apex 走 `LookupHost` ∩ tunnel IPs；非 apex 走 `LookupCNAME` suffix check |
| § 6.4 主動觸發 | `service.go` `Service.Verify` | 同 DualCondition |
| § 7 extend | `extend.go` `Apply` | `extends_used < 2 && expires_at > now() && verified=false` |
| § 8.2 7 天 grace | `grace.go` `Evaluate` + `service.go` `Service.CheckUnhealthy` | 雙條件失敗 → mark；mark > 7 天 → release |
| § 9 plan tier | `service.go` `Service.Add` | 接 `PlanGate` interface（pro 才開）|
| § 11 驗證準則 | 對應 `*_test.go` | 直接以表格驗收項命名測試 |
| § 15 hard rules | 各 unit | type 層鎖死 |

## 5. 設計取捨

### 5.1 為何把 schema 補欄位延後
- spec § 4 要求補 4 欄位 (`is_apex` / `extends_used` / `health_check_failed_at` / `status`)。改 schema 是「跨 task 一致性」議題：M3.2 GitHub App 流程亦會碰 `team`/`app` row，與 M3 wrap-up 一起對齊 migration 比較不漂移。
- 此 task 在 `Binding` struct 層落實所有欄位語意，並提供完整 Store interface 供後續整合，無 schema drift 風險。

### 5.2 為何不寫 Cloudflare hostname HTTP client
- 既存 `internal/server/services/cloudflare/` 套件只實作 wildcard tunnel route。Custom Hostname API 是新 endpoint 群（`POST /zones/{id}/custom_hostnames`）。混進此 task 會擴大 blast radius 至 `cloudflare` package 與 tunnel chart/test。本 task 以 `CloudflareHostnameAPI` interface 預留，假實作驗 service 流程。

### 5.3 為何 poller 在本 task
- spec § 6.1 明文 30s polling 是 reconciler 心臟；M3.1 標題「24h TTL + extend」要 TTL 過期 → expired 自動轉態，必須有 polling 收斂。`poller.go` 在 `LeaderProbe` 抽象下能單測，不需 leader election 機制。

### 5.4 為何不接 metrics 至 Prometheus registry
- 沿 `internal/server/services/cloudflare/client.go` 既有 pattern：在套件內以 `BindMetrics(func(...))` 暴露注入點，real `cmd/server/main.go` wiring 屬 follow-up 整合，本 task 提供 hook 即可，避免改 `internal/server/observability/`。

## 6. Hard rules 落地驗證

| § 15 | 落地點 | 測試 |
|---|---|---|
| #1 preview-confirm | 不在本 task；service 暴露 `BuildPlan` / `Add` 兩段，符合形狀 | `service_test.go::TestAddBuildsPreviewBeforeConfirm` |
| #2 plan tier 檢查 | `Service.Add` 先呼 `PlanGate.AllowExtra(team.plan)` | `service_test.go::TestAddRejectsFreePlan` |
| #3 reserved suffix | `hostname.Validate` 拒 `.winshare.tw` 結尾 | `hostname_test.go::TestRejectsReservedSuffix` |
| #4 apex 必經 publicsuffix | `apex.Detect` 唯一入口 | `apex_test.go::TestDetectsApexViaPublicSuffix` |
| #5 token 32-byte hex | `token.Generate` 用 `crypto/rand` 32 bytes | `token_test.go::TestTokenIs32Bytes` |
| #6 雙條件硬性 | `verify.DualCondition` 同時驗 CNAME/A + TXT | `verify_test.go::TestRejectsWhenTXTMissing` |
| #7 TTL 24h + extend × 2 | `extend.Apply` 硬編 `2` 與 24h tick | `extend_test.go::TestRejectsThirdExtend` |
| #8 7 天 grace | `grace.Evaluate` 硬編 7 days | `grace_test.go::TestReleasesAfter7Days` |
| #9 cert material 隔離 | type `Binding` 無 cert 欄位；無 secret 入 log | （static review）|
| #10 必經 client 操作 | `CloudflareHostnameAPI` 為唯一 entry point | 介接點預留 |

## 7. 驗證準則對應（spec § 11 → tests）

| spec 驗證項 | 測試檔案 / case |
|---|---|
| Apex 偵測 | `apex_test.go::TestDetectsApex` |
| 非 apex 偵測 | `apex_test.go::TestDetectsNonApex` |
| Plan tier free 拒 | `service_test.go::TestAddRejectsFreePlan` |
| Plan tier pro 通過 | `service_test.go::TestAddAcceptsProPlan` |
| 重複 hostname | `service_test.go::TestAddRejectsDuplicateHostname` |
| Reserved suffix | `hostname_test.go::TestRejectsReservedSuffix` |
| 雙條件通過 | `verify_test.go::TestDualConditionPasses` |
| 雙條件缺 TXT | `verify_test.go::TestRejectsWhenTXTMissing` |
| 24h TTL 過 | `poller_test.go::TestCleanupExpiredMarksRowExpired` |
| Extend 1 次 | `extend_test.go::TestFirstExtendAddsTwentyFourHours` |
| Extend 第 3 次 | `extend_test.go::TestRejectsThirdExtend` |
| Active hostname CNAME 移除 | `grace_test.go::TestMarksHealthCheckFailed` |
| 7 天 grace 結束 | `grace_test.go::TestReleasesAfter7Days` |
| Grace 中恢復 CNAME | `grace_test.go::TestClearsHealthCheckOnRecovery` |
| Apex 提示不相容供應商 | `apex_providers_test.go::TestProvidersListNonEmpty` + `service_test.go::TestApexPlanIncludesProviderList` |
| `verify_domain` 主動觸發 | `service_test.go::TestVerifyMarksBindingVerified` |
| Leader-only polling | `poller_test.go::TestPollerSkipsTickWhenNotLeader` |

## 8. 未在 task 內整合的銜接點（在 `doc.go` 註明）

- Schema migration 補欄位（`is_apex` / `extends_used` / `health_check_failed_at` / `status`）
- `internal/server/routers/domains.go` 路由（preview / confirm / verify / extend / remove）
- `internal/server/apps.go` routerStore 擴充 `Service` 依賴
- `internal/server/services/cloudflare/` 補 Custom Hostname HTTP client
- `internal/server/services/gitops/` 補 ingress.yaml hostname 列表 render
- `audit_log` Auditor 實作
- `internal/cli/` `0ops domains verify ... --extend`
- `internal/mcp/` `add_domain_preview` / `verify_domain` tools

完成此 task 後，M3 wrap-up 任務應以此 design doc 為輸入接續整合。
