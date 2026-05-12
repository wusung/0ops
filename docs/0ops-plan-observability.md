## Observability & SLO

### SLO/SLI（v1 GA 必達）

| SLI | 量測點 | SLO | Error budget |
|---|---|---|---|
| API availability | `/v1/*` 5xx 比率 | 99.9% / 28d | 40 min |
| API latency p95（read） | chi histogram，`route=GET …apps` | < 200ms | — |
| API latency p95（preview） | chi histogram，`route=*:preview` | < 800ms | — |
| Build success rate | `deploy_run.status` aggregate | > 85% / 28d | — |
| Deploy lead time p50 | `deploy_run.created → finished` | < 10 min | — |
| MTTR p50 | incident（reconciliation_job + audit_log） | < 1h | — |
| Tunnel uptime | Cloudflare side probe | 99.95% | 21 min |
| **Preview consumption rate** | `preview.consumed_at not null / total` | > 80% / 7d | — |
| **Preview→confirm latency p50** | `preview.created → consumed_at` | < 60s | — |

最後兩項是 0ops 特有產品健康度指標：consumption rate 太低代表 LLM 跳 preview 或 user 不信任輸出；latency 太長代表 PlanPreview 看不懂或客戶 IT 流程審批。任一惡化在 PR / dashboard 觸發 review。

### Burn-rate alert
- **Fast burn**：1h window 燒 ≥ 2% / 28d budget → page on-call
- **Slow burn**：6h window 燒 ≥ 5% → 開 ticket
- 用 `prometheus/client_golang` exposition + Grafana / Mimir Alertmanager

### Metrics 暴露（M2 必上）
`/metrics` Prometheus exposition；固定 label 集 `route, method, status, team_bucket`。
`team_bucket` 為 `team_id` 的 hash mod N（v1 N=64），避免高 cardinality 爆炸。

```
0ops_http_requests_total{route,method,status,team_bucket}
0ops_http_request_duration_seconds_bucket{route,method,le}
0ops_preview_created_total{action}
0ops_preview_consumed_total{action,outcome}        # outcome=success|failed|expired
0ops_preview_expired_total{action}
0ops_deploy_run_duration_seconds_bucket{stage,outcome}    # stage=build|push|render|sync
0ops_deploy_run_failures_total{stage,classification}
0ops_domain_verify_attempts_total{outcome}
0ops_cloudflare_api_calls_total{op,outcome}
0ops_github_api_rate_remaining{install_id_bucket}
0ops_reconciliation_jobs_pending{kind}
```

### Trace propagation
1. 入口 middleware 注入 OTel `traceparent`
2. `slog` handler 自動把 `trace_id` 寫進每行 log
3. `repository_dispatch` payload 帶 `trace_id` 到 GHA
4. GHA callback 帶 `trace_id` 回 backend
5. `audit_log.trace_id` 與 `deploy_run.trace_id` 落地

一條 user action（CLI 一行 / claude code 一句）跨 backend → GHA → ArgoCD → K3s 可重組。

### Deploy 狀態機

```
queued
  ↓
preparing      (Cloudflare DNS draft、gitops branch 開分支)
  ↓
building       (GHA pack build)
  ↓
pushing        (image push GHCR)
  ↓
rendering      (manifest render + commit gitops main)
  ↓
syncing        (ArgoCD sync K3s)
  ↓
live           (health check 通過)

任何一步失敗：
  → compensating  (按反向順序回滾 reversible side-effect)
  → rolled_back   (irreversible 留下 audit；標記 deploy_run.failed)
```

`failure_classification` 列舉：
- `buildpack_detect_failed`
- `build_compile_error`
- `build_timeout`
- `registry_push_failed`
- `gitops_push_conflict`
- `argo_sync_timeout`
- `health_check_failed`
- `cloudflare_api_failed`
- `unknown`

CFR（Change Failure Rate）= 「`failed` 中 classification ∈ {build_compile_error, health_check_failed} 的比例」；`unknown` > 5% 強制工程師補分類，避免 SLO 黑箱。

### Logging
- `log/slog` JSON handler；MCP binary 走 stderr
- 標準欄位：`time, level, msg, trace_id, team_id, actor_user_id, route, status, latency_ms, err`
- 不記 token、不記 webhook payload 全文（只記 `delivery_id` + 摘要）

### Reconciler 收斂迴圈
- `reconciliation_job` 表 + 背景 goroutine（10s tick）
- 對 `deploy_run.status='applying'` 滯留 > 15min 主動拉 GHA workflow_run / ArgoCD app status 收斂
- 對 `domain_binding.verified=false AND expires_at > now()` 跑 DNS 查詢
- 失敗 retry 走指數退避 `min(60s × 2^attempts, 30min)`，> 8 次轉 `last_error`（`reconciliation_job.status='failed_permanently'`）並寫 audit_log 觸發 owner 通知（v1 為 stdout/log；v1.1 為 webhook / email）

