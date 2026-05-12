# Feature Spec：winshare-subdomain-and-tunnel

> **狀態**：draft
> **來源**：`docs/0ops-plan.md`「Runtime topology / Ingress / TLS」段；ADR-0007（客戶域名雖在另一 spec，但 Cloudflare 入口設計共用）；ADR-0004（K3s 同 cluster co-location）
> **適用範圍**：Cloudflare zone `winshare.tw` 之 wildcard cert、Cloudflare Tunnel connector pool、`*.winshare.tw` 子網域之 hostname 註冊與 binding 順序；不含客戶自有域名（屬 `custom-domain-and-verify` spec）
> **對應 Milestone**：M2（與 create_app 同步上線；deploy_run 之 reversible 副作用「Cloudflare DNS draft」即指本 spec 之 hostname 註冊）

## 1. 結論（先讀本段）

- 0ops 入口為**單一 Cloudflare account / 單一 zone `winshare.tw`**；所有 `*.winshare.tw` 流量、所有客戶自有域名（透過 Cloudflare for SaaS）均經此 zone
- TLS 在 Cloudflare edge 終止；origin 只跑 HTTP；K3s ingress (`traefik`) 不持 TLS
- Cloudflare Tunnel **connector pool**：3 個 cloudflared replica 跑於 `cloudflare-tunnel` namespace（Deployment + 3 replica + anti-affinity）；單 tunnel 多 connector 達到 HA
- Tunnel ID 為固定值（v1 不 rotation）；DNS hostname 形如 `<id>.cfargotunnel.com` 為客戶 CNAME target source
- `*.winshare.tw` wildcard cert 由 Cloudflare 自動簽（zone 自管，不走 Custom Hostname 路徑）
- 子網域 binding 順序：先 Cloudflare DNS record（reversible draft）→ 再 K3s ingress hostname 寫入；確保 cert 已就緒才開放公開
- Cloudflare API client 集中於 `internal/server/services/cloudflare/`；retry + backoff（429）；rate limit 共享於同 zone 操作
- Backend 與 Cloudflare 之間以 API token 認證；token 存於 `cloudflare-api-token` Secret；rotation 屬 `secrets-management` spec
- 入口 NetworkPolicy：tunnel pod → traefik → team-<slug> 三段；其餘 ingress 拒絕（接續 `k3s-namespace-isolation` § 6）

## 2. 範圍

### 2.1 包含
- `internal/server/services/cloudflare/` package：DNS API、Tunnel API、Custom Hostname API（後者由 `custom-domain-and-verify` spec 引用）
- `*.winshare.tw` 子網域註冊流程（create_app 之 reversible side_effect）
- Cloudflare Tunnel connector pool 部署 manifest（`cloudflare-tunnel` namespace）
- Tunnel 與 K3s ingress 介接（hostname 路由 → Service）
- 子網域刪除（delete_app）之撤銷流程
- Cloudflare API rate limit 處理、retry / backoff
- 觀測：`0ops_cloudflare_api_calls_total{op, outcome}` metric

### 2.2 不包含
- 客戶自有域名與 Custom Hostname API 之 onboarding（屬 `custom-domain-and-verify` spec）
- Cloudflare API token 與簽章 secret 之 rotation（屬 `secrets-management` spec）
- Cloudflare 帳號層 billing（屬 ops runbook）
- DNS-01 ACME challenge（v1 不採；ADR-0007 已決議走 Cloudflare for SaaS）
- 多 Cloudflare account 切分（v1 單 account）
- WAF / Bot Management 規則設定（v1 採 Cloudflare 預設，v2 補）

## 3. 檔案結構

```
0ops/
├── internal/
│   └── server/
│       └── services/
│           └── cloudflare/
│               ├── client.go           # *http.Client + token；otelhttp transport
│               ├── dns.go              # zone DNS record CRUD
│               ├── tunnel.go           # Tunnel CRUD + connector status
│               ├── customhostname.go   # Custom Hostname（給 custom-domain-and-verify spec 用）
│               ├── ratelimit.go        # 429 retry + backoff
│               ├── metrics.go
│               └── doc.go
└── deploy/
    └── chart/
        └── cloudflare-tunnel/         # cloudflared connector pool 之 Helm chart
            ├── templates/
            │   ├── namespace.yaml
            │   ├── deployment.yaml
            │   ├── service.yaml
            │   ├── configmap.yaml
            │   ├── secret.yaml
            │   └── networkpolicy.yaml
            └── values.yaml
```

## 4. Cloudflare zone 與 wildcard cert

### 4.1 Zone 設定

- Zone `winshare.tw` 由 0ops 帳號擁有
- Universal SSL 啟用；wildcard cert 自動簽發（涵蓋 `*.winshare.tw` 與 `winshare.tw`）
- DNSSEC 啟用（v1.1 評估；v1 暫不開避免 onboarding 複雜化）
- Always Use HTTPS 啟用；Min TLS Version = 1.2

### 4.2 入口 record

| Record | 用途 |
|---|---|
| `winshare.tw` A → tunnel IP | 主站 landing（v2 web UI） |
| `*.winshare.tw` CNAME → `<tunnel_id>.cfargotunnel.com` | 通配，供所有 managed app 子網域 |
| `tunnel.winshare.tw` CNAME → `<tunnel_id>.cfargotunnel.com` | 顯式 tunnel 端點，供 debug |

> wildcard CNAME 至 tunnel：所有 `<app_slug>.winshare.tw` 自動解析；不需個別子網域 record

## 5. Cloudflare Tunnel connector pool

### 5.1 Deployment manifest

```yaml
apiVersion: v1
kind: Namespace
metadata:
  name: cloudflare-tunnel
  labels:
    app.0ops.io/managed-by: 0ops
    pod-security.kubernetes.io/enforce: baseline
    pod-security.kubernetes.io/warn: restricted
---
apiVersion: apps/v1
kind: Deployment
metadata:
  name: cloudflared
  namespace: cloudflare-tunnel
spec:
  replicas: 3
  selector:
    matchLabels:
      app: cloudflared
  template:
    metadata:
      labels:
        app: cloudflared
    spec:
      affinity:
        podAntiAffinity:
          preferredDuringSchedulingIgnoredDuringExecution:
            - weight: 100
              podAffinityTerm:
                labelSelector:
                  matchLabels:
                    app: cloudflared
                topologyKey: kubernetes.io/hostname
      containers:
        - name: cloudflared
          image: cloudflare/cloudflared:2025.1.0   # 鎖版本
          args:
            - tunnel
            - --no-autoupdate
            - run
            - --token
            - $(TUNNEL_TOKEN)
          env:
            - name: TUNNEL_TOKEN
              valueFrom:
                secretKeyRef:
                  name: cloudflared-tunnel-token
                  key: token
          resources:
            requests: { cpu: 100m, memory: 128Mi }
            limits:   { cpu: 500m, memory: 256Mi }
          readinessProbe:
            httpGet:
              path: /ready
              port: 2000
```

- 3 replica + podAntiAffinity：單節點故障不致整個 tunnel 斷
- `cloudflared` 自身為 outbound 連線（連 Cloudflare edge）；不需 Service 對外

### 5.2 Tunnel 路由設定

cloudflared 之 ingress 規則（透過 Cloudflare Dashboard 或 API 設定，非 K8s 內）：

```yaml
ingress:
  - hostname: "*.winshare.tw"
    service: http://traefik.kube-system.svc.cluster.local:80
  - hostname: "winshare.tw"
    service: http://traefik.kube-system.svc.cluster.local:80
  # 客戶自有域名透過 Custom Hostname 自動註冊；不需此處列出
  - service: http_status:404           # catch-all
```

- 所有流量導向 K3s `kube-system/traefik` Service
- traefik 依 Ingress 物件之 host 規則路由至 team namespace

### 5.3 Tunnel ID 管理

- v1 採固定 tunnel ID（建立一次，不 rotate）
- ID 存於 K8s Secret `cloudflared-tunnel-token`（含 `token` 欄位，base64-encoded JSON）
- 對應 hostname `<id>.cfargotunnel.com` 為客戶域名 CNAME 之 target；rotate 即影響全部客戶域名（v2 事件）

## 6. 子網域 onboarding 流程（create_app）

### 6.1 流程位置

`create_app` 之 reversible side_effects：
1. INSERT app row + INSERT domain_binding(`hostname=<app>.winshare.tw`, `kind=primary`, `verified=true`, `is_apex=false`)
2. **Cloudflare DNS check**（reversible）：因 `*.winshare.tw` 為 wildcard CNAME，**不需要**對個別子網域寫 DNS record；本步驟僅驗 wildcard 仍存在
3. Render & push gitops 之 Ingress（含 hostname `<app>.winshare.tw`）
4. ArgoCD sync → traefik 偵測新 Ingress → 該 hostname 立即可路由

> 因 wildcard，wins 子網域**幾乎瞬時上線**；不需等 Cloudflare API；唯一風險為 wildcard cert 失效，但屬 zone 層級，0ops 不主動處理

### 6.2 Reversible 屬性

雖然個別子網域不需 DNS API 呼叫，本 side_effect **仍標 Reversible**：
- Forward：no-op（或記 audit log「子網域已分配」）
- Compensate：刪除 domain_binding row；ingress 由 gitops compensate 自動移除

> 設計理由：保持 saga 對稱性；未來若引入 per-subdomain DNS record（如 health check）不需重新調整 reversible 邊界

## 7. 子網域刪除流程（delete_app）

### 7.1 流程

1. 刪除 0ops-gitops 內 `apps/<team>/<app>/` 目錄 → ArgoCD prune Ingress → traefik 不再路由該 hostname
2. 刪除 domain_binding row（hostname = `<app>.winshare.tw` + 任何客戶域名 binding）
3. 對客戶域名（若有）呼 Cloudflare API `DELETE /custom_hostnames/{id}`（屬 `custom-domain-and-verify` spec）
4. wildcard cert 不變

### 7.2 撤銷 grace

- 子網域不適用 7 天 grace（與客戶域名不同）：app 刪除即子網域立即不可訪問
- 客戶域名適用 7 天 grace（屬 ADR-0007 § 4 第 6 點）

## 8. Cloudflare API client

### 8.1 認證

- 採 API token（非 Global API Key）；最小化 scope：
  - `Zone:DNS:Edit`（zone = winshare.tw）
  - `Custom Hostname:Edit`（zone 同上）
  - `Tunnel:Edit`
- token 存於 K8s Secret `cloudflare-api-token`（key = `token`）
- backend 啟動時讀取；不放入 env var
- 90 天 rotation；rotation 雙 token 並存 30 分鐘（屬 `secrets-management` spec）

### 8.2 Rate limit 與 retry

- Cloudflare API 預設 1200 requests / 5 min per account
- Client 端 token bucket 限制：1000 / 5 min（保留餘裕）
- 收 429：讀 `Retry-After` header；指數退避 + jitter（base 1s, factor 2, max 60s, max 5 retries）
- 5 次仍失敗 → 對 caller 回 `cloudflare_rate_limited`（`error-model` § 5.5）；caller 端決定是否進 saga compensating

### 8.3 Metric

| Metric | type | labels |
|---|---|---|
| `0ops_cloudflare_api_calls_total` | counter | `op` ∈ {dns_create, dns_delete, hostname_create, hostname_delete, tunnel_status, ...}, `outcome` ∈ {success, error, throttled} |
| `0ops_cloudflare_api_call_duration_seconds` | histogram | `op` |
| `0ops_cloudflare_tunnel_connectors_ready` | gauge | （單一值，0..3） |

## 9. NetworkPolicy

### 9.1 `cloudflare-tunnel` namespace

```yaml
apiVersion: networking.k8s.io/v1
kind: NetworkPolicy
metadata:
  name: cloudflared-default
  namespace: cloudflare-tunnel
spec:
  podSelector:
    matchLabels: { app: cloudflared }
  policyTypes: [Ingress, Egress]
  ingress: []                                   # cloudflared 為 outbound only
  egress:
    - to:
        - ipBlock: { cidr: 0.0.0.0/0 }          # Cloudflare edge IP
      ports:
        - { protocol: TCP, port: 7844 }         # Cloudflare Tunnel ingress port
        - { protocol: TCP, port: 443 }          # API
    - to:
        - namespaceSelector:
            matchLabels: { kubernetes.io/metadata.name: kube-system }
      ports:
        - { protocol: TCP, port: 80 }           # → traefik
```

### 9.2 `team-<slug>` 對 cloudflared 來源之 ingress

接續 `k3s-namespace-isolation` § 6.2：team namespace 之 ingress 允許 `cloudflare-tunnel` namespace 之 pod。

## 10. 失敗與降級

### 10.1 Tunnel pod 全掛

- traefik 可能仍可訪問（K3s LB / NodePort），但 Cloudflare edge 至 origin 路徑斷
- alert：`0ops_cloudflare_tunnel_connectors_ready` < 2 持續 5 min → page on-call
- 自癒：K8s 自動 restart pod；節點問題需 ops 介入

### 10.2 Cloudflare API 中斷

- backend 對 Cloudflare API 之呼叫 (Custom Hostname add/remove) 失敗 → caller 端 retry 5 次後 saga compensating
- 對 `*.winshare.tw` 子網域 onboarding：因不呼 Cloudflare API，**不受 API 中斷影響**；wildcard 已就緒
- alert：`0ops_cloudflare_api_calls_total{outcome=error}` rate > 1% / 5min → ticket

### 10.3 Wildcard cert 失效

- 屬 Cloudflare 責任；通常不會發生
- backend 偵測：`/healthz` 對自家 `<healthz>.winshare.tw` 之 SSL 驗證；連續 3 次失敗 → page
- 處置：人工聯繫 Cloudflare support；無法自動修復

## 11. 與其他 spec 接合點

| 接合 | spec |
|---|---|
| Cloudflare API token rotation | `secrets-management` spec |
| Custom Hostname API（客戶域名） | `custom-domain-and-verify` spec |
| Ingress hostname 寫入 manifest | `gitops-render-and-argocd` § 4.5 |
| `cloudflare-tunnel` namespace 之 PSA 與 NetworkPolicy 來源 | `k3s-namespace-isolation` spec |
| `0ops_cloudflare_api_calls_total` metric | `observability-skeleton` § 4.4 |
| Saga reversible 「Cloudflare DNS draft」之語意 | `preview-confirm-gate` § 7 |
| `cloudflare_api_error` / `cloudflare_rate_limited` 失敗碼 | `error-model` § 5.5 |
| Tunnel connector ready 之 SLI（Tunnel uptime 99.95%） | `slo-and-alerting` spec |

## 12. 驗證準則

| 驗證項 | 方式 | 通過條件 |
|---|---|---|
| Cloudflare API token 認證成功 | mock + 真連 staging zone | API call 200 |
| 子網域立即可訪問 | create_app 後 < 60s 從外部 curl | `https://<app>.winshare.tw` 回 200（健康 app） |
| Wildcard cert 有效 | `openssl s_client -connect <app>.winshare.tw:443` | 證書 SAN 含 `*.winshare.tw`；valid |
| Tunnel 3 connector ready | `kubectl get deploy -n cloudflare-tunnel` | replicas=3, ready=3 |
| 單 connector 掛 | `kubectl delete pod -n cloudflare-tunnel cloudflared-xxx` | 流量持續可訪問；K8s 自動拉起 |
| Cloudflare API rate limit retry | mock 429 三次後成功 | client 端 retry 計數 = 3，最終 200 |
| Rate limit 5 次仍失敗 | mock 持續 429 | error envelope `cloudflare_rate_limited`；caller 進 compensating |
| Tunnel ingress 路由 | 對 `<app>.winshare.tw` 之 request | 經 cloudflared → traefik → team namespace pod |
| `delete_app` 後 hostname 不可訪問 | delete 後 < 90s 從外部 curl | 連線失敗或 404 |
| `delete_app` 不刪 wildcard | wildcard CNAME 仍存在 | DNS 查詢通過 |
| API token 範圍最小 | 嘗試用此 token 對其他 zone 操作 | Cloudflare API 拒（403） |
| Token rotation 雙 window | 同時並存兩 token | 兩個都可用，30 分鐘後舊 token 失效 |

### 12.1 可重跑 smoke harness（M2-09）

使用 `tasks/m2-nextdemo-smoke.sh` 執行完整 smoke：

1. `create_app` preview
2. `create_app` confirm
3. deploy callback（HMAC 簽章）
4. `https://nextdemo.winshare.tw` 可達性檢查（要求 HTTP 200）

執行方式：

```bash
export OPS_API_BASE='https://<backend-host>'
export OPS_BEARER_TOKEN='<token>'
export OPS_TEAM_SLUG='<team-slug>'
export OPS_CALLBACK_SECRET='<callback-secret>'
export OPS_APP_SLUG='nextdemo'
make smoke-nextdemo
```

## 13. SLI 對應

| SLI | 目標 | 量測 |
|---|---|---|
| Tunnel uptime | 99.95% / 28d（plan SLO） | Cloudflare 端 probe + `cloudflare_tunnel_connectors_ready >= 1` |
| Cloudflare API success rate | > 99% | `0ops_cloudflare_api_calls_total{outcome=success} / total` |
| Cloudflare API throttle rate | < 0.5% | `outcome=throttled / total` |
| Subdomain onboarding latency | p95 < 60s | create_app 完成至 `https://<app>.winshare.tw` HTTP 200 |
| Connector ready replicas | >= 2 / 3（隨時） | `connectors_ready` gauge |

## 14. 對 `docs/0ops-plan.md` 的修改清單

1. 「Runtime topology / Ingress / TLS」段：交叉引用本 spec 為 `*.winshare.tw` 入口 source
2. 「Risks & open #8（Cloudflare Tunnel 單點）」：補入「3 connector 部署 + anti-affinity；單點為 tunnel ID 本身（v1 不 rotate）」
3. 補一段「`cloudflare-tunnel` namespace 為標準 namespace（與 system-0ops 平行），由 chart 部署」
4. Ingress / TLS 表：明確「`*.winshare.tw` wildcard cert 由 Cloudflare zone 自動」「客戶域名走 Custom Hostname API」之分流

## 15. Open issues

- Tunnel ID rotation 政策：v1 不 rotate；v2 rotation 時所有客戶域名 CNAME 失效，需通知機制（屬 v2）
- Cloudflare for SaaS plan 配額：含於 ADR-0007 OQ#1；本 spec 假設「夠用」
- Cloudflare 多 region：v1 單 account 即多 region（Cloudflare 自有全球 PoP）；不需 0ops 做 region routing
- DNSSEC 啟用：v1 不開（onboarding 複雜化）；v2 評估
- WAF / Bot Management：v1 用 Cloudflare 預設；v2 補規則
- Cloudflare account 異常（被停權）：屬 disaster recovery 範圍，無自動退路
- Tunnel 多版本 / 升級：cloudflared 鎖版；升版走 PR + 階段性 rollout
- backend 對 cloudflared connector ready 之查詢：v1 透過 K8s API 查 `Deployment.status.readyReplicas`；v1.1 評估直接用 `cloudflared metrics`（port 2000）

## 16. 不可違反的硬性規則

> 違反以下任一項，PR 不可合入。

1. 所有 0ops 流量入口必經 Cloudflare（zone winshare.tw）；不得開放 K3s NodePort / LB 直接對外
2. K3s ingress（traefik）不持任何 TLS cert；僅接 HTTP
3. cloudflared connector 至少 3 replica；單 replica 部署為違反
4. Cloudflare API token 為最小 scope（DNS:Edit + Custom Hostname:Edit + Tunnel:Edit on winshare.tw zone）；不得用 Global API Key
5. Cloudflare API token 必存於 K8s Secret；不得放入 env var、不得進 log
6. Cloudflare API client 必走 retry + backoff；不得 fire-and-forget
7. wildcard CNAME `*.winshare.tw → <tunnel_id>.cfargotunnel.com` 為 zone 必設 record；不得刪除（會中斷所有子網域）
8. tunnel ID v1 不 rotate；rotation 屬 v2 事件需 ADR + 客戶通知 runbook
9. cloudflared `--no-autoupdate`；版本鎖於 manifest，升級走 PR
10. Cloudflare for SaaS Custom Hostname 之操作必透過本 spec 之 client（不得 Cloudflare Dashboard 手動設）；確保 audit + GitOps trail
