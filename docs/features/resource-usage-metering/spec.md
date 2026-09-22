# Feature Spec：resource-usage-metering

> **狀態**：accepted（主軌與輔軌已實作；MCP tool 與 watch degraded job 為 deferred，見 § 13）
> **來源**：使用者需求「記錄用戶開的 cpu / gpu / ram 的使用量」→ 二輪收斂為「以 allocation 為主」；[ADR-0018](../../adrs/0018-runtime-metering-basis.md)（計量基準）；`docs/0ops-plan-schema.md` § usage_sample（v1 預埋、v2 暴露）；ADR-0011（plan tier 配額）；ADR-0006（observability baseline）
> **適用範圍**：managed app 之**資源分配帳本**（秒級、可作為日後計費底稿）與**實際用量觀測**（輔軌）；不含計價、發票、扣款
> **對應 Milestone**：待排。主軌（§ 4–§ 7）為可獨立落地之最小集；輔軌（§ 8）可延後

## 1. 結論（先讀本段）

- 計費級精度**不來自採樣頻率，來自 K8s 物件自帶的時間戳**。本 spec 之主軌為 **allocation ledger**：每個 pod 一筆區間紀錄，
  `費用基底 = Σ (pod 宣告之 cpu/memory/gpu) × (pod 存活秒數)`。兩個乘數皆為已知定值，積分為閉式解，**精度到秒**，與輪詢或 watch 的頻率無關。
- 這是 Fly / Render / Heroku / Lambda 的同構作法。RAM 與 GPU 在業界一律按分配計（cgroup 記憶體無累積計數器、GPU 無人按 SM 利用率收費），故主軌一次涵蓋 cpu / ram / gpu 三者。
- **分配值權威來源為 `pod.spec.containers[].resources`**，不是 `app` 表、不是 tier 預設。K8s LimitRange 的 `defaultRequest` 於 admission 階段即寫入 pod spec，故讀 pod spec 同時涵蓋「使用者指定」與「LimitRange 補上」兩種情形，無需分流。
- **事件來源為 pod watch（informer）為主 + 每 5 分鐘 full relist 對帳**。watch 提供精確的終止狀態；relist 負責 backend 停機期間的補回與孤兒區間收斂。**relist 不是採樣**——它讀的是 pod 物件上的 `startTime` / `finishedAt`，不是讀取當下的牆鐘。
- **無法精確得知終止時間者必 under-bill**：以最後可信時間（`last_seen_at`）關閉區間並標 `estimated = true`。寧可少記，不可多記。
- 實際用量（metrics-server 之 CPU/RAM 瞬時值）降為**輔軌**（§ 8），用途是「這個 app 開太大 / 逼近 OOM」的觀測，**永不進入 allocation 積分**，亦不得作為帳務底稿。
- **v1 不產生任何帳單語意**（ADR-0011 DD5：flat tier、不引入 metered billing）。本 feature 只保證帳本正確且可重算；計價屬 v2。

### 1.1 為何不以 metrics-server 為主軌

| | allocation（主軌） | metrics-server（輔軌） |
|---|---|---|
| 資料性質 | 區間事件 + 定值 | 瞬時 gauge |
| 精度 | 秒（來自 K8s timestamp） | 採樣間隔（5 min） |
| 短命 pod | 完整記錄 | 存活 < 間隔即完全不存在 |
| 漏採 / 停機 | relist 可補回 | 該段永久遺失，只能插值猜 |
| 記憶體語意 | 分配量，明確 | `memory.current` 含 page cache，不宜計費 |
| GPU | 佔用數 × 時間，可得 | 無此指標 |
| 官方立場 | — | metrics-server 明示不供計費用途、不保留歷史 |

## 2. 範圍

### 2.1 包含
- 新表 `usage_allocation_interval`（pod 級區間帳本）與 `usage_daily_rollup`（日聚合，永存）
- `internal/server/services/usage/`：pod informer、relist 對帳、區間開關、日積分
- reconciler 新增 loop：`usage_reconcile`（5 min relist 對帳）、`usage_rollup`（1 h）、`usage_gc`
- backend ServiceAccount 之 pod `list` / `watch` 讀權
- 讀取 API / CLI / MCP（team 與 app 層級之 allocation 報表）
- 輔軌：`usage_sample` 之寫入路徑（§ 8，可延後）
- 帳本正確性之驗證矩陣（含停機補回、滾動更新、resize）

### 2.2 不包含
- 計價、發票、扣款、overage 擋下（ADR-0011 明定 v2 才接 Stripe）
- `egress_bytes`：無資料來源（Cloudflare Tunnel analytics 屬 v2），欄位不新建
- GPU 實際利用率 / 顯存（需 DCGM exporter + GPU node pool，v2）
- build 用量（`build_minutes` / `image_size_bytes` 已落 `deploy_run`，ADR-0005 OQ#7；本 spec 不搬動）
- 配額**執行**（ResourceQuota 由 K8s 擋，`k3s-namespace-isolation` § 5）
- 告警規則與 dashboard（屬 `slo-and-alerting`）
- backend 自身（`system-0ops`）之資源：屬平台營運成本，不入租戶帳本

## 3. 檔案結構

```
src/
├── migrations/
│   └── 00020_usage_allocation.sql
└── internal/server/
    ├── services/usage/
    │   ├── doc.go
    │   ├── ledger.go            # 區間開 / 關 / 更新 last_seen 的純邏輯
    │   ├── ledger_test.go
    │   ├── watcher.go           # pod informer：ADD / UPDATE / DELETE → ledger op
    │   ├── watcher_test.go
    │   ├── reconcile.go         # full relist 對帳：補開、收斂孤兒
    │   ├── reconcile_test.go
    │   ├── rollup.go            # 區間 → 日聚合（UTC 日界切割）
    │   ├── rollup_test.go
    │   ├── sampler.go           # 輔軌：metrics-server 瞬時用量（§ 8）
    │   └── metrics.go
    ├── services/k3s/
    │   └── pods.go              # ListManagedPods / WatchManagedPods（dynamic informer）
    ├── services/reconciler/
    │   └── usage.go             # loop wiring
    ├── db/
    │   └── usage.go             # repository（全 query 強制 team_id）
    └── usage_handlers.go
```

## 4. Allocation Ledger

### 4.1 區間的定義

一個 pod ⇒ **一筆** `usage_allocation_interval`，以 `pod_uid` 唯一。

| 邊界 | 取值 | 理由 |
|---|---|---|
| `started_at` | `pod.status.startTime` | kubelet 接收 pod 之時刻；此時 node 資源已被 scheduler bind 並保留（image pull 期間資源即已佔用），與雲廠計費起點一致 |
| `ended_at` | 依序取第一個可得者：① 所有 container `state.terminated.finishedAt` 之最大值 ② `metadata.deletionTimestamp` ③ `last_seen_at` | ①② 為 K8s 權威時間；③ 為保守 fallback，必標 `estimated` |

- **container restart 不開新區間**：`pod_uid` 不變，分配值不變，restart 不影響資源佔用。
- **滾動更新 / resize / replicas 變動天然正確**：舊 pod 關區間、新 pod 開區間，無需特殊處理。這是 allocation 模型相對於採樣模型的結構性優勢。
- pod 處於 `Pending` 且尚無 `startTime`（未 bind，例如被 ResourceQuota 擋下）：**不開區間**，未佔用任何 node 資源。

### 4.2 分配值取值

於**開區間時**自 `pod.spec` 一次定讞，此後不再變動（K8s pod 的 resources 不可原地修改；in-place resize 為 v1.33+ alpha，本 spec 不支援，列 Open issue）：

```
cpu_millicores = Σ containers[].resources.requests.cpu      # 含 LimitRange 注入值
memory_bytes   = Σ containers[].resources.requests.memory
gpu_count      = Σ containers[].resources.limits["nvidia.com/gpu"]
gpu_type       = pod.spec.nodeSelector 之 GPU 標籤（無則 NULL）
```

- initContainer 不計入（與 containers 並行期短、K8s 之 effective request 取 max 而非和；v1 簡化為忽略，誤差偏低，符合 under-bill 原則）。
- 解析一律用 `k8s.io/apimachinery/pkg/api/resource.Quantity`，不自寫字串解析。canonical 單位（millicores / bytes）入庫。

### 4.3 歸屬

pod → app 只認 label `app.0ops.io/slug` + `app.0ops.io/team`（`gitops/templates/deployment.yaml.tmpl` 已渲染），**不解析 pod 名稱**。label 指向不存在之 app：不開區間，計入 `zeroops_usage_orphan_pods` gauge。

### 4.4 事件來源：watch 為主、relist 為輔

**watch（informer）**
- `ADD`：pod 已有 `startTime` 且 label 合格 → 開區間（以 `pod_uid` upsert，冪等）
- `UPDATE`：更新 `last_seen_at`；進入 terminal phase 則以 § 4.1 規則關區間
- `DELETE`：以 § 4.1 規則關區間（DELETE 事件攜帶物件最終狀態，故通常可取到權威時間）

**relist 對帳（5 min loop，leader-only）**
1. full list 全部 managed pod
2. DB 中無對應 open 區間者 → 補開（`started_at` 仍取 pod 自身 `startTime`，故 backend 停機期間啟動的 pod **不遺失起始時間**）
3. DB 中 open 但 cluster 已不存在者 → 以 `last_seen_at` 關閉，標 `estimated = true`、`closed_reason = 'reconciled_missing'`
4. 對存在者更新 `last_seen_at`

> **精度不受 relist 頻率影響**：relist 只決定「多久發現一次」，不決定「記成幾點」。時間一律取自 pod 物件。唯一受頻率影響的是第 3 條——backend 停機期間結束的 pod，其終止時間無從得知，誤差上限為一輪間隔且方向恆為 under-bill。

**降級**：informer 建立失敗（API server 不可達、RBAC 不足）→ 退化為 relist-only，功能不中斷，精度降為「終止時間誤差 ≤ 5 min 且 under-bill」；計 `zeroops_usage_watch_degraded` gauge = 1 並 log warn。

> 原規格要求「連續 30 min 降級寫一筆 `reconciliation_job`」，**實作時否決**：`reconciliation_job` 是補償工作佇列（有 handler、有重試、有終態），而 watch 降級不是可由 job 收斂的工作——沒有任何 handler 能「修好」它。降級的正確出口是 gauge + alert rule（歸 `slo-and-alerting`）。此處留為 **deferred**：alert rule 未寫。

### 4.5 Leader 換手與重啟

- 所有寫入 leader-only。換手後新 leader 之第一次 relist 即完成對帳。
- `last_seen_at` 持久化於 DB（非記憶體），故重啟不損失 fallback 依據。
- 重複事件安全：開區間為 `on conflict (pod_uid) do nothing`；關區間為 `where ended_at is null`。

## 5. DB Schema（migration `00020`）

```sql
create table usage_allocation_interval (
    pod_uid         uuid primary key,                       -- K8s pod UID
    team_id         uuid not null references team(id) on delete cascade,
    app_id          uuid not null references app(id) on delete cascade,
    namespace       text not null,
    pod_name        text not null,
    cpu_millicores  integer not null,
    memory_bytes    bigint  not null,
    gpu_count       integer not null default 0,
    gpu_type        text,
    started_at      timestamptz not null,                   -- pod.status.startTime
    ended_at        timestamptz,                            -- NULL = 仍在佔用
    last_seen_at    timestamptz not null,
    estimated       boolean not null default false,         -- ended_at 非 K8s 權威時間
    closed_reason   text,                                   -- terminated|deleted|reconciled_missing
    closed_at       timestamptz,                            -- 帳本關閉此區間的牆鐘時間（migration 00021）
    created_at      timestamptz not null default now(),
    check (ended_at is null or ended_at >= started_at)
);

create index usage_alloc_team_app_idx  on usage_allocation_interval (team_id, app_id, started_at desc);
create index usage_alloc_open_idx      on usage_allocation_interval (last_seen_at) where ended_at is null;

create table usage_daily_rollup (
    team_id                uuid not null references team(id) on delete cascade,
    app_id                 uuid not null references app(id) on delete cascade,
    day                    date not null,                   -- UTC
    cpu_millicore_seconds  bigint        not null default 0,
    memory_byte_seconds    numeric(30,0) not null default 0,
    gpu_count_seconds      bigint        not null default 0,
    pod_seconds            bigint        not null default 0,
    estimated_seconds      bigint        not null default 0, -- 其中來自 estimated 區間者
    interval_count         integer       not null default 0,
    computed_at            timestamptz   not null default now(),
    primary key (team_id, app_id, day)
);
```

- `usage_allocation_interval` **保留 13 個月**（與 audit 對齊，足以支撐帳務爭議回溯）；逾期由 GC 刪除，日聚合永存。
- `app_id` 為 NOT NULL；app 刪除即 cascade。**v1 接受**（計費未啟動）；v2 開計費前須改為保留 slug 快照，列 Open issue。
- `estimated_seconds > 0` 之日必於 API 標示，表示該日帳本含保守估計。

## 6. 積分（rollup）

對區間 `[started_at, coalesce(ended_at, now()))` 與 UTC 日 D 之交集 `dt`：

```
cpu_millicore_seconds += cpu_millicores × dt
memory_byte_seconds   += memory_bytes   × dt
gpu_count_seconds     += gpu_count      × dt
pod_seconds           += dt
estimated_seconds     += dt  if interval.estimated
```

- **跨日區間必切割於 UTC 日界**，不得整筆歸入單一日。
- 閉式計算，**無採樣誤差、無插值、無缺樣截斷**。上一版 spec 的 `covered_seconds` 概念隨採樣模型一併移除。
- 只 rollup **已封閉的日**（`day < today_utc`）；當日數值由讀取 API 以同一函式即時算（`Integrate` 純函式為 rollup 與即時查詢**共用**，禁止兩套實作）。封閉日若尚未結算（rollup loop 每小時跑，UTC 午夜後有空窗），讀取 API 同樣即時算——**不得回報為 0**，那是與「無用量」不同的主張。
- **冪等**：以 `(team_id, app_id, day)` 全量重算 upsert，不得增量累加。
- **仍開放之區間會進入 rollup**（clamp 至日界），因為長命 pod 在該日的佔用量於日界即已確定。但這使 rollup 可能在區間定案前就寫出：
  - pod 實際於 D 日 10:00 結束、backend 當時失聯 → D 日 rollup 記滿 24h → 之後補上 `ended_at` → **該日必須重算**，否則永久超收。
  - 區間於 D 日結算後才補開（停機補回）→ **該日必須重算**，否則永久少計。
  - 故 pending 判定不是「有無 rollup 列」，而是「rollup 的 `computed_at` 是否晚於該日所有相關區間的最後定案時間（`COALESCE(closed_at, created_at)`）」。`closed_at` 記錄**帳本得知區間結束的時刻**（牆鐘），與 `ended_at`（pod 自己的時間）不同，後者無法判斷 rollup 是否過時。

## 7. API / MCP / CLI

| 介面 | 內容 |
|---|---|
| `GET /v1/teams/{slug}/usage?from=&to=` | team 各 app 日 allocation + 合計；預設近 30 日 |
| `GET /v1/apps/{slug}/usage?from=&to=&detail=interval` | 單 app；`detail=interval` 回原始區間（13 個月內） |
| ~~MCP `get_usage`~~ | **deferred**：本 repo 尚無任何 MCP server 實作（`docs/features/mcp-*` 為規格層），無處掛載。待 MCP 落地時一併補，屆時須過 `mcp-tool-description-lint` |
| CLI `0ops usage [--from --to] [--app <slug>] [--interval] [--observed]` | 表格：app / core-hours / GiB-hours / GPU-hours / pod-hours / 估計比例；單一入口，不另開 `apps usage` 子命令 |

- read-only，不走 preview/confirm gate。RBAC：team viewer 以上（承 `auth-and-rbac`）。
- 單位換算只在呈現層：core-hours = `cpu_millicore_seconds / 1000 / 3600`；GiB-hours = `memory_byte_seconds / 2^30 / 3600`。
- DTO 帶 `billing_disclaimer` 常數字串。
- 依 AGENTS.md：DTO / MCP schema / CLI output / migration 變更皆必補 contract test。

## 8. 輔軌：實際用量觀測（可延後）

- 沿用既有 `usage_sample` 表（`00001_init.sql` 已建、至今零讀寫），reconciler 5 min loop 自 `metrics.k8s.io/v1beta1` PodMetrics 拉瞬時 cpu / memory，寫入。
- 用途限於「開太大 / 逼近 limit」之觀測；API 以 `used_*` 欄位與主軌 `allocated_*` **併列但分欄**呈現。
- metrics-server 不可用 → 該 tick 零寫入、計 metric，**不得回退為分配值冒充用量**。
- `usage_sample` 保留 30 天，GC 與主軌共用 loop。`egress_bytes` 維持 NULL。
- 本軌**不影響**主軌任何數值；主軌不依賴 metrics-server 存在。

## 9. 可觀測性

> 命名前綴以實作現況 `zeroops_` 為準（Prometheus metric 名不得以數字開頭；`observability-skeleton` 文件中的 `0ops_` 寫法在 code 中一律落為 `zeroops_`）。**禁止 pod_uid / app_id / team_id 進 label**。

| Metric | Type | Labels | 說明 |
|---|---|---|---|
| `zeroops_usage_intervals_open` | gauge | — | 目前開放中之區間數（≈ 計費中 pod 數） |
| `zeroops_usage_intervals_opened_total` | counter | source | source ∈ {watch, reconcile} |
| `zeroops_usage_intervals_closed_total` | counter | reason | reason ∈ {terminated, deleted, reconciled_missing} |
| `zeroops_usage_watch_degraded` | gauge | — | 1 = informer 不可用，退化 relist-only |
| `zeroops_usage_reconcile_duration_seconds` | histogram | — | 單次 full relist 對帳耗時 |
| `zeroops_usage_orphan_pods` | gauge | — | label 指向不存在 app 之 pod 數 |
| `zeroops_usage_rollup_lag_days` | gauge | — | 最舊未 rollup 之封閉日距今天數 |
| `zeroops_usage_intervals_expired_total` | counter | — | 逾 13 個月保留期而刪除之區間數 |
| `zeroops_usage_samples_written_total` | counter | — | 觀測輔軌寫入列數（永不進入計量） |
| `zeroops_usage_sample_failures_total` | counter | reason | 觀測 tick 零寫入之原因；`metrics_api_unavailable` 通常代表 metrics-server 未安裝，帳本不受影響 |

`reconciled_missing` 之比率是帳本品質的主要指標：長期 > 5% 表示 watch 有問題，應告警（規則歸 `slo-and-alerting`）。若新增 Prometheus rule，須放 `deploy/gitops/observability/prometheus-*-rules.yaml` 並通過 `make lint-prom-rules`（promtool 經 podman，lessons L001）。

## 10. 與其他 spec 接合點

| 接合 | spec |
|---|---|
| pod label 契約（`app.0ops.io/slug` / `team`） | `gitops-render-and-argocd`（`deployment.yaml.tmpl`） |
| LimitRange 預設值（分配值之實際來源） | `k3s-namespace-isolation` § 5（`100m` / `256Mi`） |
| per-app 宣告 spec（落地後仍由 pod spec 讀取，本 spec 無須改動） | `app-resource-spec` |
| tier 配額（用量 vs 上限比對） | ADR-0011 § 3.1 |
| leader-only loop 與 tick 慣例 | `reconciler-and-incident`；`services/reconciler/runner.go` |
| degraded 之 job 寫入 | `reconciler-and-incident`（`reconciliation_job.kind`） |
| metric 命名與 label cardinality | `observability-skeleton` |
| 保留期與資料分類（internal） | `compliance-framework-mapping` § 9；`0ops-plan-schema.md` |
| K8s RBAC 最小權限 | `k3s-namespace-isolation`、`security-hardening` |

backend ServiceAccount 需新增 **ClusterRole**：core `pods` 之 `list` + `watch`。必須是 cluster-scoped——team namespace 為動態建立，無法事先逐一 RoleBinding。輔軌另需 `metrics.k8s.io/pods` 之 `get,list`（已授予）。`deploy/bootstrap/install-k3s.sh` 於安裝後檢查 metrics-server 是否存在，缺席時警示而非失敗——計量不受影響。

> 登錄處：backend chart 之 `deploy/server/templates/clusterrole.yaml`，唯讀性由 `chart_test.go` 之 `TestUsageReaderClusterRoleIsReadOnly` 強制（任何寫入 verb 或 wildcard 進入即 build fail）。`security-hardening` spec 並無集中式最小權限表，故不另行鏡射，避免兩份文件漂移。
>
> 既有缺口（非本 feature 引入）：backend 以 in-cluster SA 連線，但 chart 目前只授予 lease Role，`EnsureNamespace` / `EnsureResourceQuota` 所需的 namespace 寫權並未由 chart 授予。本 feature 未修補此缺口以控制範圍，但它會讓 production 的 namespace 建立路徑失敗，應另案處理。

## 11. 驗證準則

| 驗證項 | 方式 | 通過條件 |
|---|---|---|
| 秒級精度 | pod 存活 37 秒後刪除 | 區間 `ended_at - started_at = 37s ± 1s`；`estimated = false` |
| 時間戳來源 | 延後 3 分鐘才送達之 DELETE 事件 | `ended_at` = pod 之 `finishedAt`，**非**事件送達時刻 |
| 短命 pod | 存活 8 秒之 pod | 區間完整存在（採樣模型下會完全漏記） |
| 分配值取自 pod spec | app 未指定 resources | `cpu_millicores = 100`、`memory_bytes = 256Mi`（LimitRange 注入值），非 tier 上限 |
| restart 不開新區間 | container crash 重啟 3 次 | 仍為一筆區間，`started_at` 不變 |
| 滾動更新 | 觸發 redeploy | 舊 pod 區間關閉、新 pod 區間開啟，兩者時間相接不重疊、不留空窗 |
| Pending 不計費 | ResourceQuota 擋下之 pod（無 startTime） | 不開區間 |
| 停機補回起始時間 | 停 backend → 起 pod → 啟 backend | relist 補開之區間 `started_at` 等於 pod 之 `startTime`（非 backend 啟動時刻） |
| 停機期間結束者 under-bill | 停 backend → 刪 pod → 啟 backend | 區間以 `last_seen_at` 關閉、`estimated = true`、`closed_reason = reconciled_missing`，且 `ended_at ≤` 真實刪除時間 |
| 降級為 relist-only | 停用 watch | 帳本仍持續；`zeroops_usage_watch_degraded = 1`（**alert rule 為 deferred**，見 § 4.4） |
| orphan pod | label 指向已刪 app | 不開區間；`zeroops_usage_orphan_pods = 1` |
| 跨日切割 | 區間 23:50 → 次日 00:10 | 兩日各得 600 秒，不整筆歸單日 |
| rollup 冪等 | 同日連跑兩次 | 結果 byte-identical；`interval_count` 不翻倍 |
| 即時與 rollup 一致 | 封閉日之 API 值 vs rollup 值 | 完全相等（共用 `Integrate` 函式） |
| team 隔離 | A team token 查 B team | 404；DB query 必含 `team_id` |
| 重複事件冪等 | 同一 pod 之 ADD 重送 5 次 | 單一區間 |
| leader-only | 兩 backend 實例 | 僅 leader 寫入，無重複區間 |
| e2e | `tasks/e2e-usage-metering.sh` + `manage.sh e2e-usage-metering` | 走真實路徑：建 app → pod 起訖 → `0ops apps usage` 回傳與實際存活秒數相符之 core-hours；**不得以 SQL 偽造區間** |

e2e 依 `e2e-testing` spec L001：經 `OPS_HOST` 打 compose stack；K8s 面由 in-repo mock（`src/cmd/devtools/mock-k8s-pods`，提供 list + watch 兩端點並可腳本化 pod 起訖）供應，production compose 永不含 mock。真 K3s 路徑於 staging 手動覆蓋，列 deferred 並記於 `release/`。

## 12. 對既有文件的修改清單

1. ✅ `docs/0ops-plan-schema.md` § `usage_sample`：已改註為輔軌；已新增 `usage_allocation_interval` 與 `usage_daily_rollup`；`active` 註解已修正
2. ✅ `docs/0ops-plan-schema.md` § 保留期與資料分類表：已補兩表
3. ✅ `docs/features/observability-skeleton/spec.md` § 4.4：已補 10 條 metric 表與前綴說明（`0ops-plan-observability.md` 為衍生文件，不重複維護）
4. ~~`docs/features/k3s-namespace-isolation/spec.md`~~：該 spec § 3.2 明示不定義 backend 自身 chart，故不改；ClusterRole 之登錄處為本 spec § 10 與 chart 內測試
5. ✅ `docs/features/app-resource-spec/spec.md` § 8：已補
6. ✅ `docs/features/compliance-framework-mapping/spec.md` § 9：已補三表（含 cascade 刪除與 PDPA 路徑說明）
7. ✅ ADR-0005 OQ#7：已標註結案
8. ✅ 已補 [ADR-0018](../../adrs/0018-runtime-metering-basis.md)「Runtime 計量以 allocation 為基準」，並登錄於 `docs/adr-reading-strategy.md` § 2 快速參考表

## 13. Open issues

- app 刪除後歷史帳本保留（`on delete set null` + slug 快照）：v2 開計費前必須解決
- in-place pod resize（K8s v1.33+）落地後，單一 pod 之分配值會中途變動，屆時區間需可分段；schema 已可容納（關舊區間 + 開新區間），但需 watch 端偵測 `resources` 變更
- initContainer 資源忽略之誤差是否需修正為 K8s effective request（`max(init, sum(containers))`）：v1 偏低可接受
- GPU `gpu_type` 之取值來源（nodeSelector 標籤 vs node 之 GPU label）：v2 建 GPU node pool 時拍板
- 分配 vs 實際用量差距過大（開太大）之主動提示：屬產品面，待 v2 UI
- 是否需 `pod_seconds` 以外之「instance 級」計價單位（如 Fly 的 machine-hours）：待定價模型拍板

## 14. 不可違反的硬性規則

> 違反以下任一項，PR 不可合入。

1. 區間之 `started_at` / `ended_at` 必取自 K8s 物件之時間戳；**不得**使用 backend 收到事件之牆鐘時間
2. 分配值必取自 `pod.spec.containers[].resources`；不得改由 `app` 表、tier 預設或 LimitRange 設定檔回推
3. 終止時間不可得時必 under-bill：取 `last_seen_at`、標 `estimated = true`，不得向後推估或補滿至發現時刻
4. 一 pod 一區間，以 `pod_uid` 唯一；container restart 不得開新區間
5. 跨日區間必切割於 UTC 日界；rollup 必冪等（全量重算 upsert，禁止累加）
6. rollup 與即時查詢必共用同一 `Integrate` 純函式，不得兩套實作
7. 輔軌之實際用量（metrics-server）不得進入 allocation 積分，亦不得作為帳務底稿；主軌不得依賴 metrics-server 存在
8. 所有寫入 leader-only；所有 query 必含 `team_id` 謂詞，跨 team 讀取回 404
9. `pod_uid` / `app_id` / `team_id` / pod 名不得作為 Prometheus label
10. v1 任何輸出不得宣稱為帳單、費用或應付金額；DTO 與 CLI 必帶「非計費資料」註記
