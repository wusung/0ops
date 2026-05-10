# Feature Spec：slo-and-alerting

> **狀態**：draft
> **來源**：`docs/0ops-plan.md`「Observability & SLO」「Burn-rate alert」段；ADR-0006（SLO 99.9%、burn-rate 多窗策略）；本 spec 依賴 `observability-skeleton`、`reconciler-and-incident`、`postgres-ha-and-dr`、`backend-ha-leader-election`、所有 metric 來源 spec
> **適用範圍**：v1 GA 必達之 9 條 SLO/SLI 之精確定義、burn-rate alert rule 表達式、dashboard 規約、failure_classification 監控；不含 metric exposition / trace propagation 本身（屬 `observability-skeleton`）
> **對應 Milestone**：M5（與 incident 表 + reconciler GA 同步上線）

## 1. 結論（先讀本段）

- v1 GA 必達 9 條 SLO；2 條為產品紅旗指標（preview consumption rate / preview→confirm latency）
- API availability 99.9% / 28d 為主目標（month budget ≈ 40 min）
- Burn-rate alert：multi-window multi-burn-rate；fast 1h ≥ 2% / slow 6h ≥ 5%
- Alert routing：fast → PagerDuty critical（page on-call）；slow → 自動開 incident ticket
- Dashboard 採 Grafana；以 ConfigMap manifest 部署（GitOps-managed at `deploy/gitops/observability/`）
- Recording rules 預先聚合常用 PromQL；alert rules 引用 recording rules 降低查詢成本
- `failure_classification = unknown` panel 為強制 dashboard component；> 5% 持續 14d 觸發 ADR-0006 Revisit
- Alert 之 routing label 含 `severity` (critical/warning) + `service` (0ops-backend/postgres) + `team_bucket`（必要時）

## 2. 範圍

### 2.1 包含
- 9 條 SLO/SLI 精確 PromQL 表達式
- Burn-rate alert recording rule + alert rule
- Alertmanager routing（severity / receiver）
- Dashboard 規約（panel layout、row 分組）
- `failure_classification` 監控 panel
- 兩條產品紅旗指標 dashboard
- Recording rule 命名規約

### 2.2 不包含
- Metric exposition（屬 `observability-skeleton` § 4）
- Trace propagation（屬 `observability-skeleton` § 6）
- Logging 結構（屬 `observability-skeleton` § 5）
- Incident 表（屬 `reconciler-and-incident` § 9）
- 對外 status page（v2）
- On-call schedule（屬 ops 範圍）

## 3. 檔案結構

```
0ops/
└── deploy/
    └── gitops/
        └── observability/
            ├── kustomization.yaml
            ├── prometheus-recording-rules.yaml      # ConfigMap
            ├── prometheus-alert-rules.yaml          # ConfigMap
            ├── alertmanager-config.yaml             # Secret（含 PagerDuty integration key）
            ├── grafana-dashboards/
            │   ├── 0ops-overview.yaml               # ConfigMap
            │   ├── 0ops-deploy-pipeline.yaml
            │   ├── 0ops-product-health.yaml         # 紅旗指標
            │   ├── 0ops-postgres.yaml
            │   ├── 0ops-leader-ha.yaml
            │   └── 0ops-failure-classification.yaml
            └── README.md
```

> Observability stack（Prometheus / Alertmanager / Grafana）部署本身屬 ops runbook；本 spec 只提供 ConfigMap manifest

## 4. 9 條 SLO/SLI

### 4.1 表

| # | SLI | SLO | Error budget / 28d | 量測 PromQL |
|---|---|---|---|---|
| 1 | API availability | 99.9% | 40 min | `1 - (sum(rate(0ops_http_requests_total{status=~"5.."}[28d])) / sum(rate(0ops_http_requests_total[28d])))` |
| 2 | API latency p95（read）| < 200ms | — | `histogram_quantile(0.95, sum(rate(0ops_http_request_duration_seconds_bucket{route!~".*:preview$"}[5m])) by (le))` |
| 3 | API latency p95（preview）| < 800ms | — | `histogram_quantile(0.95, sum(rate(0ops_http_request_duration_seconds_bucket{route=~".*:preview$"}[5m])) by (le))` |
| 4 | Build success rate | > 85% | — | `sum(rate(0ops_deploy_run_terminal_total{outcome="success"}[28d])) / sum(rate(0ops_deploy_run_terminal_total[28d]))` |
| 5 | Deploy lead time p50 | < 10 min | — | `histogram_quantile(0.50, sum(rate(0ops_deploy_run_lead_time_seconds_bucket[28d])) by (le))` |
| 6 | MTTR p50（incident）| < 1h | — | `histogram_quantile(0.50, sum(rate(0ops_incident_duration_seconds_bucket[28d])) by (le))` |
| 7 | Tunnel uptime | 99.95% | 21 min | Cloudflare side probe（外部）+ `0ops_cloudflare_tunnel_connectors_ready >= 1` |
| 8 | **Preview consumption rate** | > 80% / 7d | — | `sum(increase(0ops_preview_consumed_total{outcome=~"success|idempotent_replay"}[7d])) / sum(increase(0ops_preview_created_total[7d]))` |
| 9 | **Preview→confirm latency p50** | < 60s | — | `histogram_quantile(0.50, sum(rate(0ops_preview_consume_duration_seconds_bucket[7d])) by (le))` |

> #4 之 `0ops_deploy_run_terminal_total{outcome}` 為 recording rule 派生（見 § 5）；source metric `0ops_deploy_run_state_transitions_total` 在 `reconciler-and-incident` 補入。
> #6 之 `0ops_incident_duration_seconds_bucket` 為 reconciler 結算寫入 metric（incident close 時計算 closed_at - opened_at）。
> #7 Tunnel uptime 之 Cloudflare 端 probe 屬 ops 設定外部 probe；本 spec 假設存在。

### 4.2 紅旗指標解讀

- **Preview consumption rate < 80%**：LLM 跳過 preview 直接 call write tool；或 user 不信 PlanPreview 不 confirm
- **Preview→confirm latency p50 > 60s**：PlanPreview 看不懂；或客戶 IT 流程審批慢

兩條同時惡化 → 觸發 ADR-0002 Revisit（兩階段 preview 是否仍合理）

## 5. Recording rules

### 5.1 命名規約

`<colon-prefixed>:<metric>:<aggregation>_<window>`

例：
- `cluster:0ops_http_requests:rate5m{status="5.."}`：5 分鐘 rate
- `cluster:0ops_http_request_duration_seconds:histogram_quantile_p95_5m{route_class="read"}`：5 分鐘 p95

### 5.2 預先計算（節錄）

```yaml
groups:
- name: 0ops-recording
  interval: 30s
  rules:
    # 5xx rate per route_class
    - record: cluster:0ops_http_requests:rate5m
      expr: sum by (route, method, status) (rate(0ops_http_requests_total[5m]))

    # Error rate (5xx / total)
    - record: cluster:0ops_http_error_rate:5m
      expr: |
        sum(rate(0ops_http_requests_total{status=~"5.."}[5m]))
        / sum(rate(0ops_http_requests_total[5m]))

    - record: cluster:0ops_http_error_rate:1h
      expr: |
        sum(rate(0ops_http_requests_total{status=~"5.."}[1h]))
        / sum(rate(0ops_http_requests_total[1h]))

    - record: cluster:0ops_http_error_rate:6h
      expr: |
        sum(rate(0ops_http_requests_total{status=~"5.."}[6h]))
        / sum(rate(0ops_http_requests_total[6h]))

    # Deploy outcome 派生
    - record: cluster:0ops_deploy_run_terminal:rate28d
      expr: |
        sum by (outcome) (
          rate(0ops_deploy_run_state_transitions_total{to=~"live|failed|rolled_back|failed_permanently|cancelled"}[28d])
        )
```

## 6. Burn-rate alert

### 6.1 接續 ADR-0006 § 4 之 multi-window multi-burn-rate

- Fast：1h window 燒 ≥ 2% 月度 budget → page on-call
- Slow：6h window 燒 ≥ 5% 月度 budget → 開 ticket

### 6.2 計算公式

```
burn_rate = error_rate / (1 - SLO_target)
                       = error_rate / 0.001  (for 99.9% SLO)
```

對 fast：1h 內 burn_rate × 1h / 28d ≥ 2% → 即 1h burn_rate ≥ 2% × 28d / 1h = 13.44

對 slow：6h burn_rate ≥ 5% × 28d / 6h = 5.6

### 6.3 Alert rules

```yaml
groups:
- name: 0ops-burn-rate
  rules:
    - alert: APIErrorBudgetBurnFast
      expr: |
        cluster:0ops_http_error_rate:1h > 13.44 * 0.001
      for: 5m
      labels:
        severity: critical
        service: 0ops-backend
        slo: api_availability
        burn_rate_window: 1h
      annotations:
        summary: "API availability burning > 2% / 28d budget in 1h window"
        description: "Current 1h error rate: {{ $value | humanizePercentage }}. Page on-call."
        runbook_url: "https://0ops.tw/runbooks/burn-rate-fast"

    - alert: APIErrorBudgetBurnSlow
      expr: |
        cluster:0ops_http_error_rate:6h > 5.6 * 0.001
      for: 30m
      labels:
        severity: warning
        service: 0ops-backend
        slo: api_availability
        burn_rate_window: 6h
      annotations:
        summary: "API availability burning > 5% / 28d budget in 6h window"
        description: "Current 6h error rate: {{ $value | humanizePercentage }}. Open ticket."
        runbook_url: "https://0ops.tw/runbooks/burn-rate-slow"
```

### 6.4 其他關鍵 alert

| Alert | 條件 | severity |
|---|---|---|
| `BuildSuccessRateLow` | `terminal_outcome=success / total < 80%` for 6h | warning |
| `DeployLeadTimeP95High` | p95 > 15 min for 1h | warning |
| `TunnelConnectorsLow` | `connectors_ready < 2` for 5m | critical |
| `TunnelDown` | `connectors_ready == 0` for 1m | critical |
| `PostgresReplicationLagHigh` | `pg_replication_lag_seconds > 60` for 5m | critical |
| `LeaderHandoverFrequent` | `rate(0ops_leader_handover_total[1h]) > 1` | warning |
| `LeaderMultipleHolders` | `sum(0ops_leader_status) > 1` | critical |
| `PreviewConsumptionRateLow` | `< 80%` for 7d | warning（產品紅旗）|
| `UnknownFailureClassificationHigh` | `unknown ratio > 5%` for 14d | warning（工程介入）|
| `GhcrPullSecretRefreshFail` | `0ops_ghcr_pull_secret_refresh_total{outcome="failed"}` rate > 0.1/min for 30m | warning |
| `CloudflareAPIThrottled` | `0ops_cloudflare_api_calls_total{outcome="throttled"}` rate > 0.5% for 15m | warning |
| `WebhookSignatureRejectHigh` | `webhook_signature_invalid` rate > 1% for 30m | warning |
| `ReconciliationFailedPermanently` | `0ops_reconciliation_jobs_pending{kind="failed_permanently"} > 0` for 5m | warning |

## 7. Alertmanager routing

### 7.1 Receiver

```yaml
receivers:
  - name: pagerduty-critical
    pagerduty_configs:
      - service_key: <integration-key>
        severity: critical
  - name: oncall-ticket
    webhook_configs:
      - url: https://0ops.tw/internal/incidents/from-alert    # 自動建 incident（屬 reconciler-and-incident）
  - name: slack-engineering
    slack_configs:
      - api_url: <slack-webhook-url>
        channel: '#0ops-engineering'
```

### 7.2 Route

```yaml
route:
  receiver: slack-engineering
  group_by: [alertname, slo, service]
  routes:
    - matchers: [severity="critical"]
      receiver: pagerduty-critical
      continue: true
    - matchers: [severity="warning"]
      receiver: oncall-ticket
      continue: true
```

### 7.3 Silencing / Maintenance window

- ops 計畫性維護（如 K3s 升版、Postgres failover 演練）：silence 對應 alert at Alertmanager UI
- silence 必含 author + reason + 預期結束時間
- audit_log 不寫 silence（屬 Alertmanager 自身 audit）

## 8. Dashboard 規約

### 8.1 主 dashboard：`0ops Overview`

Row 1：**Service health**（總覽）
- API availability gauge（28d）+ remaining budget
- API request rate by status
- p95 latency（read / preview 分行）

Row 2：**Deploy pipeline**
- Build success rate（28d）
- Deploy lead time histogram（24h）
- In-flight deploy_run by stage

Row 3：**Cloudflare / Tunnel**
- Tunnel connectors ready
- Cloudflare API call rate by outcome
- Domain verify success rate

### 8.2 `0ops Product Health`（紅旗指標）

- Preview consumption rate（7d trend）
- Preview→confirm latency p50/p95
- Preview created vs consumed by action（breakdown）

### 8.3 `0ops Failure Classification`

- Pie chart：failed deploy_run by classification（28d）
- `unknown` 比例 single stat（紅 if > 5%）
- Trend line：unknown 比例 7d

### 8.4 `0ops Postgres`

- Replication lag
- WAL archive last_archived_time
- Connection count vs max_connections
- Long-running queries

### 8.5 `0ops Leader / HA`

- `leader_status` per pod
- Leader handover rate
- Lease renew success rate
- SSE active connections per pod

## 9. `failure_classification` 監控

### 9.1 強制 dashboard panel

接續 ADR-0006 § 4 第 8 點與 `reconciler-and-incident` § 7.3：

```promql
# Unknown 比例
sum(rate(0ops_deploy_run_failures_total{classification="unknown"}[7d]))
  / sum(rate(0ops_deploy_run_failures_total[7d]))
```

- panel：single stat + trend line
- 紅燈閾值：> 5%
- alert（warning，14d 持續）：`UnknownFailureClassificationHigh`

### 9.2 工程介入觸發

- alert 觸發 → 自動建 incident（severity=warning）
- 工程師查 audit_log + deploy_run.events 補分類；提交 PR 改本 spec § 7.1 列舉
- 若 root cause 為 reconciler 推進邏輯之 bug → 修 reconciler；不擴增分類

## 10. 與其他 spec 接合點

| 接合 | spec |
|---|---|
| Metric source（http、preview、deploy、tunnel）| `observability-skeleton` § 4 |
| `0ops_deploy_run_state_transitions_total` | `reconciler-and-incident` § 6 |
| `0ops_incident_duration_seconds` | `reconciler-and-incident` § 9.4 |
| `pg_replication_lag_seconds` | `postgres-ha-and-dr` § 5.3 |
| `0ops_leader_status` / `_handover_total` | `backend-ha-leader-election` § 8 |
| `0ops_preview_*` | `preview-confirm-gate` § 10 |
| Alert receiver URL（incident from-alert）| `reconciler-and-incident` § 9.2 |
| Silence audit | Alertmanager 自身 |

## 11. 驗證準則

| 驗證項 | 方式 | 通過條件 |
|---|---|---|
| 9 條 SLO PromQL 可執行 | promtool eval | 全部回傳合法值 |
| Recording rule 預計算 | promtool check rules | 通過 |
| Alert rule 表達式合法 | promtool check rules | 通過 |
| Burn-rate fast 觸發 | mock 5xx > 13.44 × 0.001 持續 5min | alert 觸發 + PagerDuty page |
| Burn-rate slow 觸發 | mock 5xx > 5.6 × 0.001 持續 30min | alert + 自動建 incident |
| TunnelDown alert | mock connectors_ready=0 持續 1min | alert critical |
| LeaderMultipleHolders | mock 兩 pod 都 leader | alert critical |
| Unknown failure panel | mock unknown > 5% 14d | alert + dashboard 紅 |
| Dashboard 載入 | Grafana UI 開 | 全 panel 顯示資料 |
| ConfigMap GitOps deploy | git push observability ConfigMap | ArgoCD sync 後 Prometheus reload 規則 |
| Silence 流程 | ops UI silence 一個 alert 30 min | alert 不再 page；30 min 後恢復 |
| Alertmanager routing | mock critical alert | 同時去 PagerDuty + Slack（continue=true） |
| 跨 28d 計算成本 | promtool 估算 | < 1 CPU sec / query |

## 12. SLI 對應（meta：本 spec 自身）

| SLI | 目標 | 量測 |
|---|---|---|
| Alert false positive rate | < 5% / month | manual review of paged alerts |
| Alert true positive ack 時間 p95 | < 5 min | PagerDuty 端 |
| Recording rule 評估失敗率 | < 0.1% | Prometheus `prometheus_rule_evaluation_failures_total` |
| Dashboard 5xx alert 響應時間 | first byte < 500ms | Grafana 後端 |

## 13. 對 `docs/0ops-plan.md` 的修改清單

1. 「Observability & SLO / SLO/SLI」段：交叉引用本 spec § 4 為 PromQL 唯一 source
2. 「Burn-rate alert」段：交叉引用本 spec § 6 為 alert rule 唯一 source
3. 「Reconciler 收斂迴圈」段：補入「`failure_classification=unknown` panel 為強制 dashboard」交叉引用本 spec § 9
4. 補一段「Alertmanager 與 Grafana 部署於 GitOps repo `deploy/gitops/observability/`，由 ArgoCD 同步」

## 14. Open issues

> 來源：ADR-0006 § 9 之 6 條 OQ + 本 spec 撰寫期間發現

- ADR-0006 OQ#1（OTLP exporter 啟用）：屬 `observability-skeleton`
- ADR-0006 OQ#2（team_bucket 算法）：已選 CRC32
- ADR-0006 OQ#3（Recording / alert rule 倉儲位置）：本 spec 採 `deploy/gitops/observability/`
- ADR-0006 OQ#4（Cardinality 守門員）：v1 不設；M2 後流量觀察
- ADR-0006 OQ#5（Log sampling）：v1 不做
- ADR-0006 OQ#6（Synthetic probe）：v1 採 Cloudflare side probe（屬 ops）；本 spec 假設存在
- 跨 team 之 SLO（per-team availability）：v1 不做（cardinality 限制）；v1.1 評估 `team_bucket` 維度 SLO
- Per-action SLO（如 create_app 之獨立 lead time SLO）：v1 集中於 deploy lead time；v1.1 評估
- Alertmanager 之 silence audit：屬 Alertmanager 自身；不入 0ops audit_log
- 跨 region SLO（v2）：屬 v2
- M5 Postgres failover 演練期間之 SLO 計算豁免：v1 不豁免；演練即啃 budget（迫使 ops 做好）
- Trivy block rate panel（v1 觀察期）：本 spec § 8 未列；待 `build-pipeline-and-callback` § 7.1 補入

## 15. 不可違反的硬性規則

> 違反以下任一項，PR 不可合入。

1. v1 GA 必達 9 條 SLO；少一條即不可上線
2. Burn-rate alert 必為 multi-window multi-burn-rate（fast + slow 雙窗）
3. `unknown` failure_classification panel 為強制 dashboard component
4. PromQL 命名規約 `<colon-prefixed>:` 為 recording rule 慣例；alert rule 僅引用 recording rule（不直接寫長 query）
5. Alert rule 必含 `severity` + `service` + `runbook_url` label；缺 runbook_url 視為運維 bug
6. Critical alert 必同時送 PagerDuty + Slack（continue=true）；warning 必送 ticket + Slack
7. Dashboard 為 GitOps-managed（ConfigMap）；不允許 ops 在 Grafana UI 直改 panel（會被 ArgoCD selfHeal 回）
8. Silence 必含 author + reason + 結束時間；無 reason 之 silence 視為 bug
9. SLO 數值變更必走 ADR-0006 補丁；不得個別 alert rule 自由調
10. 紅旗指標（preview consumption rate / preview→confirm latency）惡化必觸發 ADR-0002 Revisit；不得 silent ignore
