# Runbook：error budget burn-rate alert 處理流程

> 對應 spec：`docs/features/slo-and-alerting/spec.md`、`docs/features/observability-skeleton/spec.md`
> 對應 ADR：ADR-0006（observability baseline）
> 適用 alert：`APIErrorBudgetBurnFast`、`APIErrorBudgetBurnSlow`
> Alert 來源：`deploy/gitops/observability/prometheus-alert-rules.yaml`
>
> 注記：alert YAML 中的 `runbook_url` 目前指向 `https://0ops.tw/runbooks/burn-rate-{fast,slow}`
> （docs site 尚未架），實際內容以本檔為準；docs site 上線後可改為頁面跳轉或於本檔加 anchor。

## 1. 觸發條件

| Alert | Window | Threshold | for | Severity |
|---|---|---|---|---|
| `APIErrorBudgetBurnFast` | 1h | `cluster:zeroops_http_error_rate:1h > 13.44 * 0.001` | 5 min | critical |
| `APIErrorBudgetBurnSlow` | 6h | `cluster:zeroops_http_error_rate:6h > 5.6 * 0.001` | 30 min | warning |

兩條規則依 multi-window multi-burn-rate（MWMB）模式設計：

- **Fast (1h)**：燒掉月度 budget 2% 內已超預期；critical，pager oncall
- **Slow (6h)**：燒掉月度 budget 5% 內已超預期；warning，工作時間處理即可

兩條同時 fire 表示問題已持續 ≥ 1h 且影響面廣；視為 incident severity 1。

## 2. 5-min 初判流程

整體預估時間：5 min 結論「是否升 incident」。

### Step 1 — 看當前 burn rate 與被影響的 SLO（≤ 1 min）

```bash
# Prometheus query — 取 burn rate 與 budget remaining
curl -sG "$PROM_URL/api/v1/query" \
  --data-urlencode 'query=cluster:zeroops_http_error_rate:1h' \
  | jq '.data.result[].value[1]'

curl -sG "$PROM_URL/api/v1/query" \
  --data-urlencode 'query=1 - (cluster:zeroops_http_error_rate:30d / 0.001)' \
  | jq '.data.result[].value[1]'  # remaining budget ratio
```

判讀：

- burn rate ≥ 14.4× → 月度 budget < 2 day 內燒完，必須立即 mitigate
- burn rate 6× ~ 14× → fast alert 區，需 1h 內 mitigate
- burn rate 1× ~ 6× → slow alert 區，可工作時間處理

### Step 2 — 對齊近期變更（≤ 2 min）

```bash
# 最近 2h 內合進 main 的 PR
gh pr list --repo <org>/<repo> --state merged --search "merged:>$(date -u -d '2 hours ago' +%FT%TZ)" \
  --json number,title,mergedAt,author --jq '.[] | "\(.mergedAt)  #\(.number)  \(.author.login)  \(.title)"'

# 最近 deploy_runs
psql "$DATABASE_URL" -c "
SELECT id, app_id, status, finished_at, failure_classification
FROM deploy_runs
WHERE finished_at > now() - INTERVAL '2 hours'
ORDER BY finished_at DESC
LIMIT 20;"
```

分支：

- 有近期 deploy 重疊 → 直接走 rollback decision tree（Step 3）
- 無近期 deploy → 看 infra（Cloudflare throttling、tunnel down、postgres replica lag），各自走對應 runbook

### Step 3 — Rollback decision tree（≤ 2 min）

```
近 2h 有 deploy？
├─ 是 → 該 deploy 之 commit 是否涉及 hot path（API handler / middleware）？
│       ├─ 是 → 立即 rollback：`0ops redeploy <app-slug> --commit <previous-commit-sha>`
│       └─ 否 → 觀察 30 min；若 burn rate 不降再 rollback
└─ 否 → 是否同步收到其他 alert（Tunnel / Cloudflare / Postgres）？
        ├─ 是 → 走對應 runbook（winshare-route-failure / postgres-failover）
        └─ 否 → 開 incident，拉 oncall + on-call SRE 同步分頭排查
```

## 3. 升 incident 條件

任一條件成立應升 incident：

1. Fast + Slow 兩條同時 fire ≥ 30 min
2. burn rate ≥ 14.4× 且 5 min 內 mitigation 未生效
3. 月度 budget 剩餘 < 10%
4. 已影響付費客戶（從 `apps` 表 `plan_tier` 撈，比對受影響的 hostname）

開 incident 的最低資料：
- 影響面（hostnames / teams 數）
- 起點時間（alert `firstSeenAt`）
- 已嘗試的 mitigation 與結果
- 預估恢復時間（若已知 rollback target）

## 4. 常見 root cause 對應表

| 症狀 | 可能原因 | 對應 runbook |
|---|---|---|
| burn 同時 `TunnelConnectorsLow` | cloudflared connector 不足 | `winshare-route-failure.md`（infra 落地後補完） |
| burn 同時 `deploy_runs` 大量 `failed` | 後端推送失敗 / build pipeline 異常 | `create-app-stuck.md` + `gha-callback-signature-failure.md` |
| burn 但 backend `/health` 200 | Cloudflare 端 / DNS 端問題 | Cloudflare dashboard + `CloudflareAPIThrottled` alert |
| burn 且 postgres replica lag 高 | 寫入 backend 阻塞 | `postgres-failover.md` |
| 只有 slow burn fire | 漸進式劣化，無 spike | 看 7d trend；可能是 dependency 升版漂移 |

## 5. 失敗回退

- rollback 後 burn 不降 → 問題在 infra 不在 code；走 postgres / tunnel 各對應 runbook
- 找不到對應 root cause 但 fast alert 連 fire 1h → 對 hot path 啟用 emergency feature flag（若有）或拉低 traffic（rate-limit 加嚴）
- alert 自己 flapping（fire → resolve → fire）→ 不是真的劣化，是 alert 規則本身雜訊大；開 ticket 調 threshold，不要每次 page oncall

## 6. 演練要求

- 每季排一次「人為注入 5% 錯誤率持續 10 min」演練，驗證 fast alert 在預期時間內 fire 且 page 到對的人
- 演練前在 oncall channel 公告，避免真誤觸 incident process
- 演練後把 fire/resolve 時間錄到 SLO review 文件
