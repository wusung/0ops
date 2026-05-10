---
adr: "0006"
title: Observability Baseline（SLO / Metrics / Trace / Logging）
status: Accepted
date: 2026-05-09
tags:
  - observability
  - slo
  - metrics
  - tracing
  - logging
  - foundation
supersedes: []
superseded-by: []
---

# ADR-0006：Observability Baseline（SLO / Metrics / Trace / Logging）

* Status：Accepted
* Date：2026-05-09
* 適用範圍：M0–M5；所有 backend service / reconciler / MCP binary / CLI 客戶端的觀測層基線
* 來源：`docs/0ops-plan.md`「Observability & SLO」段已確定方向，本 ADR 將其正式化並補上被否決選項的理由
* 上游依賴：[ADR-0001 多租戶模型與 RBAC](0001-multi-tenancy-and-rbac.md)（team 為一階導致 metric label 設計需處理 cardinality）；[ADR-0002 Idempotency 與副作用補償](0002-idempotency-and-compensation.md)（preview / deploy_run 狀態機產出對應 metrics 的語意）

## 0. TL;DR（先讀本段）

採用以下八項組合決策，作為所有 service 觀測層的不可變基線：

1. **Metrics exposition**：Prometheus pull model；`/metrics` endpoint；client lib `prometheus/client_golang`。
2. **Label cardinality 策略**：固定 label set `{route, method, status, team_bucket}`；`team_bucket = hash(team_id) mod 64`，**不**直接以 `team_id` 或 `team_slug` 作 label。
3. **SLO/SLI 表**：v1 GA 必達 9 條；含 API availability、API latency p95（read / preview）、build success rate、deploy lead time、MTTR、tunnel uptime，加上兩條 0ops 特有產品指標（preview consumption rate、preview→confirm latency）。
4. **Burn-rate alert**：multi-window multi-burn-rate；fast 1h ≥ 2%/28d budget → page on-call；slow 6h ≥ 5%/28d budget → 開 ticket。
5. **Trace propagation**：W3C `traceparent` 為唯一標準；五段鏈路（HTTP middleware → slog context → GHA `repository_dispatch` payload → callback → `audit_log.trace_id`）必須端到端可重組。
6. **Structured logging**：`log/slog` JSON handler；MCP binary 走 stderr；標準欄位 `time/level/msg/trace_id/team_id/actor_user_id/route/status/latency_ms/err`。
7. **Redaction**：不記 token、不記 webhook payload 全文（僅 `delivery_id` + 摘要）；`Authorization` header 必 mask；`Set-Cookie` / `secret_*` 欄位必 mask。
8. **Failure classification 強制**：`deploy_run.failure_classification` 不可為 null；`unknown` 比例 > 5% 時觸發工程介入（非告警，dashboard panel）。

行為與 metric 名稱清單以 `docs/0ops-plan.md`「Observability & SLO」段為準，本 ADR 不重述完整 metric 列表。

## 1. Context and Problem Statement

0ops 的觀測層需在 M2（GA 前）就位，而非 M3+ 補上。原因有三：

* **多租戶 blast radius**：一個 team 的異常（preview spam、build storm、Cloudflare API 配額耗盡）會排擠其他 team；無 team 維度量測就無法定位。
* **跨系統長鏈路**：一條使用者操作（CLI 一行 / claude code 一句）跨 backend → GHA → ArgoCD → K3s 四個系統；事後 root-cause 仰賴 trace_id 端到端可重組。
* **產品健康度新指標**：`preview consumption rate`（preview 創建後是否真的被 consume）與 `preview→confirm latency`（LLM 是否能在 10 分鐘 TTL 內呈現給 user 並取得確認）為 0ops 特有；無對應 metric 即無法判斷產品設計成不成立。

需在程式碼下手前釘住四件事：

1. Metric label 維度策略（以 team 為 label 還是 bucket，bucket 多大）。
2. SLO 目標精度（99.5 / 99.9 / 99.95 哪個量級，與 plan 階段相稱）。
3. Burn-rate alert 視窗策略（fast/slow 雙視窗 vs 單視窗 vs 計數門檻）。
4. Trace propagation 過跨界（HTTP → GHA → callback）的協議與容錯。

ADR-0001 已將 team 設為一階租戶，本 ADR 在其上建立可量測的「per-team 健康度」與「跨系統 root-cause」能力。

## 2. Decision Drivers

* **DD1 多租戶可觀測性 vs cardinality 成本**：team 為一階意味著「每 team 一條時序」會在 N team 規模下爆炸；需平衡可觀測性與儲存成本。
* **DD2 SLO 必須驅動 on-call 行為**：若 SLO 永遠不違反，等於沒寫；若違反過於敏感，on-call 會 numb。需與 v1 規模相稱。
* **DD3 跨界 trace 不可斷**：HTTP → GHA → callback → ArgoCD 任一段斷裂即失去 root-cause；協定選擇需是 W3C 標準（GHA 預設可帶 header），而非 0ops 自訂。
* **DD4 產品健康度可見**：`preview consumption rate` 是 0ops 對 LLM 配合度的 canary；不在 dashboard 上意味著產品問題會延遲被發現。
* **DD5 v1 規模成本約束**：v1 為 single-replica backend；不應為 v3 規模預留 over-engineered 觀測棧。
* **DD6 Redaction 預設保守**：token / webhook payload 一旦進 log 就難以從 retention 系統撤回；redaction 必須 default-deny。
* **DD7 Failure classification 不可為黑盒**：CFR（Change Failure Rate）的可信度 = `unknown` 比例；放任 unknown 等於放棄 DORA。

## 3. Considered Options

針對 (a) label cardinality、(c) SLO 精度、(d) burn-rate 視窗策略做完整 alternative 比較；(b)(e)(f) 為局部技術選擇，列表帶過。

### 3.1 (a) Label cardinality 策略（team 維度）

| Option | 描述 |
|---|---|
| **A1. team_bucket = hash(team_id) mod 64**（採用） | 固定 64 條 series；任何 team 數量都不爆炸；可大致定位 hotspot team 群體 |
| A2. 直接以 `team_id` 作 label | 完全可分；N team 即 N 條 series；超過 ~1000 即 cardinality 警報 |
| A3. 以 `team_slug` 作 label | 等同 A2；slug 改名還會留下 stale series |
| A4. 不以 team 維度切 | 最便宜；但 hotspot 無法定位 |
| A5. Top-N + others | 動態維護 top-N team 為獨立 label，其餘 bucket 為 `others` | 

### 3.2 (c) SLO 目標精度

| Option | 描述 |
|---|---|
| **C1. API availability 99.9% / 28d**（採用） | 月度 budget ≈ 40 分鐘 5xx；v1 single-replica 量級可達 |
| C2. 99.95% / 28d | budget ≈ 21 分鐘；對 single-replica + K3s 操作風險過高 |
| C3. 99.5% / 28d | budget ≈ 3.5 小時；過鬆；on-call 永遠不會被叫 |
| C4. 不訂 SLO，靠 5xx 計數 | 最簡單；但無 budget 概念，無法平衡發版速度與穩定性 |

### 3.3 (d) Burn-rate alert 視窗策略

| Option | 描述 |
|---|---|
| **D1. Multi-window multi-burn-rate（fast 1h ≥ 2% + slow 6h ≥ 5%）**（採用） | fast 抓即時 spike，slow 抓慢性流血；標準 SRE 實踐 |
| D2. 單視窗（24h ≥ X%） | 反應慢；spike 一小時就燒完月度 budget 也只在隔天看到 |
| D3. 5xx 計數門檻 | 與流量無關；低流量誤觸、高流量過鬆 |
| D4. 三視窗（5min/1h/6h） | 對 v1 流量規模 over-engineering；5min 視窗噪音多 |

### 3.4 (b)(e)(f) 局部選擇

| 子決策 | 採用 | 否決 | 一句結論 |
|---|---|---|---|
| (b) Trace propagation 協定 | W3C `traceparent` + `tracestate`（OTel） | 自訂 `X-0ops-Trace-Id` / 不傳 | GHA / ArgoCD / 商用 APM 皆認 W3C；自訂會破壞跨界互通 |
| (e) Metrics naming convention | `0ops_<domain>_<noun>_<unit>`（Prometheus best practice） | OTel dot-style / 無 prefix | Prometheus 命名空間慣例與 client_golang 對齊；OTel dot-style 在 Prom exposition 仍會被改寫 |
| (f) Logging baseline | `log/slog` JSON + 固定欄位 + stderr（MCP） | line format / unstructured / zap / zerolog | stdlib `slog` 已足夠；新增第三方 logger 增加依賴與一致性風險 |

## 4. Decision Outcome

採用 **A1 + C1 + D1**，搭配 (b) W3C traceparent、(e) `0ops_*` 命名、(f) `log/slog` JSON。

具體展開（細節以 `docs/0ops-plan.md`「Observability & SLO」段為準，本 ADR 不重述完整 metric 列表）：

1. **Metric exposition**：
   * `prometheus/client_golang`；`/metrics` endpoint 開放給 Prometheus pull。
   * 固定 label set：`route, method, status, team_bucket`；不允許其他高 cardinality 維度（user_id、preview_id、commit_sha 等）進 label，必要時走 trace 或 log。
   * `team_bucket` 計算：`fmt.Sprintf("%02d", crc32(team_id) % 64)`；rebucket 不變動既有 series 名稱。
2. **SLI 量測點**：plan 第「Observability & SLO」表 9 條全採；觀察兩條為產品健康度紅旗：
   * `preview consumption rate > 80% / 7d`：低於門檻代表 LLM 跳 preview 或 user 不信任 PlanPreview。
   * `preview → confirm latency p50 < 60s`：高於門檻代表 PlanPreview 看不懂或客戶 IT 流程審批。
3. **Burn-rate alert**：
   * Fast：`(error_rate over 1h) × (28d / 1h) ≥ 2 × budget` → PagerDuty critical。
   * Slow：`(error_rate over 6h) × (28d / 6h) ≥ 5 × budget` → 自動開 ticket。
   * 實作：`prometheus/client_golang` exposition + Grafana / Mimir Alertmanager。
4. **Trace propagation**（5 段，缺一即失敗）：
   1. 入口 HTTP middleware（OTel `otelhttp`）注入 `traceparent`。
   2. `slog` handler 自動把 `trace_id` 寫進每行 log（`internal/server/observability/logging.go`）。
   3. `repository_dispatch` payload 帶 `trace_id` 到 GHA workflow env。
   4. GHA callback 帶 `trace_id` 回 backend `/internal/deploy-runs/{id}/callback`。
   5. `audit_log.trace_id` 與 `deploy_run.trace_id` 落地。
5. **Logging 標準欄位**：`time, level, msg, trace_id, team_id, actor_user_id, route, status, latency_ms, err`。`team_id` 走 raw（不 bucket），因為 log 為非聚合資料、cardinality 在 log retention 系統處理。
6. **Redaction**：
   * `Authorization` header 永遠 `Bearer ***`；不允許 raw token 出現在任何 log 路徑。
   * Webhook payload 只記 `delivery_id` + `event_type` + 摘要欄位；不記 raw body。
   * Outgoing HTTP request 對 third-party API（GitHub、Cloudflare）log 不含 secret query string；URL 過濾。
   * 敏感欄位列表中央化於 `internal/server/observability/redactor.go`，`slog` ReplaceAttr hook 接此 redactor。
7. **Reconciler 觀測**：
   * `0ops_reconciliation_jobs_pending{kind}` gauge。
   * `0ops_reconciliation_attempts_total{kind, outcome}` counter（outcome ∈ {success, retry, failed_permanently}）。
   * `failed_permanently` 進 audit_log + owner 通知（v1 為 stdout/log；v1.1 為 webhook / email）。
8. **Failure classification 強制**：
   * `deploy_run.failure_classification` 不可為 null；CI lint 攔截「無 classification 的 final state 寫入」。
   * Dashboard 必有 `unknown` 占比 panel；> 5% 觸發工程介入而非告警。

## 5. Pros and Cons of the Options

### 5.1 (a) Label cardinality 策略

#### A1. team_bucket = hash mod 64（採用）

* Good：N team 規模下時序數量恆定（`64 × |routes| × |status|`）；儲存成本可預測。
* Good：hotspot team 群體仍可被觀察（單一 bucket 異常即可定位涉及哪些 team，回到 log 細查）。
* Good：team rename / slug 變更不影響 series（hash 基於 team_id UUID）。
* Good：bucket 數量為冪二（64）對 hash mod 友善；未來擴大不破壞既有 series 命名。
* Bad：單一 team 異常會被同 bucket 其他 team 的流量稀釋；root-cause 仍需回 log。
* Bad：64 為設計取捨；M5 後若需更細需考慮提升至 128 或 256，需 series 命名版本化。

#### A2. 直接以 team_id 作 label

* Good：完全可分；單一 team 異常立即可見。
* Bad：N team 即 N 條 series；> 1000 team 時 Prometheus head series memory 顯著膨脹。
* Bad：team 刪除 / archive 後仍會留下 stale series 於 retention window。
* Bad：違反 Prometheus 官方 cardinality 建議（label 不可為高基數標識）。

#### A3. team_slug 作 label

* 同 A2 缺點 + slug 改名留下 stale series（更糟）。

#### A4. 不以 team 維度切

* Good：最便宜，cardinality 與 team 數無關。
* Bad：違反 DD1（多租戶可觀測性）；hotspot 無法定位。

#### A5. Top-N + others

* Good：兼顧頭部 team 可見性與 cardinality 控制。
* Bad：「誰在 top-N」是動態決定，需獨立服務維護；複雜度高。
* Bad：bucket 切換時 series 不連續，dashboard / alert 規則需特殊處理。

### 5.2 (c) SLO 目標精度

#### C1. API availability 99.9% / 28d（採用）

* Good：月度 budget ≈ 40 分鐘；對 v1 single-replica + 計畫內 release 滾動的容忍度合理。
* Good：與業界 internal PaaS / dev tool 級服務一致；招募工程師對此目標心智成本低。
* Good：違反時觸發 fast burn alert 的閾值（2% budget 在 1h 內）= 每月最多被 page 數次，符合可持續 on-call。
* Bad：對 enterprise 等級客戶（要求 99.95+）為不足；升級需獨立 ADR。
* Bad：v1 single-replica 計畫內滾動更新（rollingUpdate maxSurge=1）也會啃 budget；M5 升 2 replica + leader election 後實際達成率才會穩定。

#### C2. 99.95% / 28d

* Good：對 enterprise 客戶友善。
* Bad：21 分鐘 budget 對 single-replica 的 K3s 操作（節點重啟、CSI 短暫不可用）幾乎無容忍度。
* Bad：v1 不應做承諾不到的 SLO；違反 DD2（SLO 驅動 on-call）。

#### C3. 99.5% / 28d

* Good：3.5 小時 budget；幾乎永不違反。
* Bad：on-call 永遠不被叫；SLO 等同未寫；違反 DD2。

#### C4. 不訂 SLO，靠 5xx 計數

* Good：最簡單。
* Bad：無 budget 概念；無法做「發版加速 vs 穩定性」的工程取捨討論。
* Bad：違反 DD2；burn-rate alert 無從建立。

### 5.3 (d) Burn-rate alert 視窗策略

#### D1. Multi-window multi-burn-rate（fast 1h + slow 6h）（採用）

* Good：fast window 抓 spike（如 deploy 引入新 bug）；slow window 抓慢性流血（如記憶體洩漏導致 5xx 上升）。
* Good：標準 SRE 實踐（Google SRE Workbook ch.5）；on-call 對此模型熟悉。
* Good：兩個閾值（2% / 5%）對應兩種反應級別（page / ticket），噪音可控。
* Bad：兩個視窗的 budget 計算需 Alertmanager / Prometheus alert rule 表達式較長；維護負擔略高。
* Bad：fast window 1h 對極短噴發（< 5 min）反應仍偏慢；極端場景需 D4。

#### D2. 單視窗

* Good：alert rule 簡單。
* Bad：spike 燒完一小時 budget 也只在隔天才被告警，違反 DD2。

#### D3. 5xx 計數門檻

* Good：實作門檻最低。
* Bad：與流量無關；低流量時誤觸（單一錯誤 = 100% 錯誤率），高流量時過鬆。
* Bad：無 budget 概念。

#### D4. 三視窗（5min / 1h / 6h）

* Good：對極短 spike 反應最快。
* Bad：5min 視窗在 v1 流量下統計不顯著（bucket 內樣本少）；噪音多。
* Bad：規模未到，over-engineering。

## 6. Consequences

### 6.1 Positive

* `team_bucket` 策略讓 metric 儲存成本獨立於 team 數；商業擴展時不需重做觀測層。
* SLO 表 9 條 + 兩條產品紅旗指標讓「服務健康」與「產品設計成不成立」可同時被量測。
* W3C trace propagation 讓「使用者一句話」到 K3s pod 的鏈路可重組；MTTR 量測有資料源。
* `failure_classification` 強制非 null + `unknown` panel 讓 CFR 不會成為黑盒；DORA 量測可信度有保證。
* `slog` JSON + 固定 redaction 讓 log 上 retention 系統時 schema 穩定；查詢 / index 規則可標準化。

### 6.2 Negative

* `team_bucket` 在「單一 hotspot team」場景需回到 log 細查；可預期工程師會抱怨「為什麼不能直接看 team_slug 的 latency」；需 dashboard / runbook 教育。
* SLO 99.9% 對 single-replica 計畫內 rollout 是緊湊的；M2 上線後若 burn rate 持續高，可能需提前進 M5（HA）而非按計畫排程。
* `traceparent` 跨界傳遞依賴 GHA workflow env 不被使用者覆寫；若客戶自帶 workflow 模板移除 env，trace 會在 GHA 段斷掉；需 plan 中 `deploy/workflows/deploy-app.yml` 為 single source。
* Redaction 中央化 redactor 為 single point of correctness bug；新增敏感欄位時需更新 redactor 才安全；review checklist 必納入。
* `team_bucket` 從 64 升 128/256 需考慮 metric 名稱版本化（或保留 64 永久不調整）；前者破壞 dashboard，後者限制未來精度——M5 需重審。

### 6.3 Neutral

* Dashboard 具體 panel layout 與 Alertmanager routing 屬 runbook 範圍，不在本 ADR。
* OTLP exporter 是否啟用、是否走 collector vs 直連，屬 deploy/infra 決策；本 ADR 僅約束「OTel API 為標準」。
* Log retention / shipping 後端（Loki / ES / Datadog 等）為 v2 範圍；v1 僅落地到 stdout/stderr。
* CLI / MCP binary 端的 metric exposition 不在本 ADR；CLI 為短命程序、MCP 為 stdio 無 HTTP server，皆不暴露 `/metrics`。

## 7. Revisit Triggers

下列任一條件觸發時，應重新評估本 ADR 對應段：

1. **Cardinality 失控**：Prometheus head series 突破 100k → 重審 (a)，可能需引入 recording rule 或 cardinality limit。
2. **SLO 持續違反**：連 2 個 28d 週期 budget 用罄 > 80% → 重審 (c)；可能需降 SLO 目標或加速 M5 HA。
3. **SLO 永不違反**：連 3 個 28d 週期 budget 用罄 < 10% → 重審 (c)，可能需提升至 99.95%。
4. **Burn-rate alert 噪音 / 漏報**：fast burn 月均 page > 4 次或 < 0.5 次 → 重審 (d) 閾值或視窗策略。
5. **產品紅旗指標惡化**：`preview consumption rate < 50%` 持續 7d → 不只觸發 review，可能需重審 ADR-0002 之兩階段強制（preview 設計不為 LLM 接受）。
6. **Trace 鏈路斷裂率 > 1%**：GHA → callback → audit_log 任一段 trace_id 缺失率超門檻 → 重審 (b)，可能需改 push-to-collector 模型。
7. **`unknown` failure classification > 5% 持續 14d**：違反 DD7；觸發強制工程介入分類，必要時為新類別獨立 ADR 補丁。

## 8. More Information

* 完整 metric 名稱清單與 SLI 量測點：`docs/0ops-plan.md`「Observability & SLO」段。
* 跨 team 隔離 SQL 與 middleware 行為（影響 `team_id` 與 `team_bucket` 來源可信度）：[ADR-0001 多租戶模型與 RBAC](0001-multi-tenancy-and-rbac.md) 第 4 節。
* `preview` / `deploy_run` 狀態機產出對應 metric 的語意：[ADR-0002 Idempotency 與副作用補償](0002-idempotency-and-compensation.md) 第 4 節。
* HMAC callback 與 trace_id 跨界規約：規劃為 ADR-0005（待寫）；本 ADR 假設 callback 必帶 `trace_id`。
* MCP binary 的 logging 走 stderr：[ADR-0003 MCP SDK 選型](0003-mcp-sdk-selection.md) 第 4 節。
* Backend HA 對 SLO 達成率的影響：規劃為 ADR-0008（待寫）。

## 9. Open Questions

下列問題不阻擋本 ADR 通過，但需在 M2（observability GA）前敲定：

1. **OTLP exporter 啟用條件**：v1 是否啟用 OTLP collector，還是 trace 僅落地到 audit_log + slog？影響 trace 跨 instance 重組能力（M5 多 replica 後尤甚）。
2. **`team_bucket` 算法選擇**：CRC32 vs FNV-1a vs SHA256 truncate 的取捨；分布均勻性 spike 確認後敲定，寫入 `internal/server/observability/metrics.go` 常數。
3. **Recording rule 與 alert rule 倉儲位置**：放於 `deploy/observability/` 還是與 chart 並存？需與 ADR-0008 的 backend 部署 topology 同步決議。
4. **Cardinality 守門員**：是否在 Prometheus 端設 `metric_relabel_configs` 強制 drop 高 cardinality label？v1 規模可能不必，但需有 runbook 應對誤入 label。
5. **Log sampling**：高流量 endpoint 是否需 log sampling（如 success 5xx 全記、success 200 抽 1%）？v1 規模不需，但 retention 成本上升時需重審。
6. **Synthetic probe**：v1 是否有 synthetic check（外部 prober 打 `/health`）作為 availability SLO 的獨立驗證源？目前依賴 server 自身 5xx 計數，存在 self-reporting bias。
