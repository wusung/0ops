## Runtime topology & operability

### Managed app 隔離模型（K3s）

**Namespace 策略**：每 team 一個 namespace，命名 `team-<team_slug>`。app 為 namespace 內 deployment / service / ingress 的集合，labelled `app.0ops.io/slug=<app_slug>`。
理由：team 是計費 + RBAC 邊界；per-app namespace 在 K3s 上 namespace 數會爆炸（>500 namespace SQLite datastore 嚴重退化）。

**ResourceQuota（per team）**：
```yaml
apiVersion: v1
kind: ResourceQuota
metadata: { name: default, namespace: team-<slug> }
spec:
  hard:
    requests.cpu: "4"            # plan=free
    requests.memory: 8Gi
    limits.cpu: "8"
    limits.memory: 16Gi
    persistentvolumeclaims: "10"
    pods: "30"
```
plan 為 `free / starter / pro`，不同 plan 對應不同 quota；plan 升降級走 `team_membership` 同 owner-only path。

**LimitRange（per namespace）**：default container request `100m / 256Mi`，limit `500m / 1Gi`，避免單 app 寫死的 manifest 吃光 quota。

**NetworkPolicy 預設**：
- Ingress：只允許從 `kube-system/traefik` namespace（K3s 預設 ingress controller）+ 同 namespace 內 pod
- Egress：允許 0.0.0.0/0（managed app 通常需外連 DB / API），但封 RFC1918 內網段除 K8s service CIDR 與 Cloudflare Tunnel pod
- v1.1：team 可加 allowlist egress

**Pod Security Admission**：namespace label `pod-security.kubernetes.io/enforce=baseline`、`warn=restricted`。v2 強制 restricted。

**ImagePullSecret**：team namespace 內預埋 `ghcr-pull` secret，由 backend 用 GitHub App installation token 簽發 short-lived（1h）GHCR token，背景 goroutine 每 30 min refresh。

**Ingress / TLS**：
- `*.winshare.tw`：wildcard cert by Cloudflare（origin 用 cloudflared tunnel），Ingress 不持 TLS
- 客戶自有域名：Cloudflare for SaaS Custom Hostname（**待 ADR-007**），origin 走同一條 tunnel

### Backend 自身部署 topology

**部署位置**：與 managed apps **同一個 K3s cluster** 但**獨立 namespace** `system-0ops`，受同 PSA + NetworkPolicy 規範。

**HA**：v1 single replica；M5 升 2 replica + leader election（`k8s.io/client-go/tools/leaderelection`）。leader 跑 reconciler / 過期清理；follower 純服務 read/write API。

**SSE 多實例**：v1 single replica → SSE 直接由 backend 推；M5 多實例 → ingress sticky session by `set-cookie`，或加 Redis pub/sub（`redis/go-redis/v9`）。決議於 ADR 待補。

**Probe**：
- `/livez`：goroutine deadlock detector（`runtime.NumGoroutine()` 上限警報但不殺）；簡單回 200
- `/readyz`：DB ping + Cloudflare API 試探（30s 快取）；任一失敗 → 503，從 ingress 摘除
- `/health`：alias of `/readyz`，相容既有 K8s 慣例

**滾動更新策略**：rollingUpdate `maxSurge=1, maxUnavailable=0`；新 pod 通過 `/readyz` 才接流量；preStop hook `sleep 5 + drain SSE`。

### Postgres backup / DR

**v1**：
- 主 + 1 read replica（streaming replication），跨 K3s node
- WAL archive 到 R2 / S3（cron job 每 5 min 推 segment）
- 每日邏輯備份（`pg_dump`）保留 30 天
- **PITR**：archive + base backup 達成；RPO 5 min、RTO 30 min（演練於 M5）

**failover**：v1 手動 promote replica；v1.1 評估 `patroni` 自動。

**Migration 安全閘**：CI 跑 `goose status` + `goose validate`；migration 必先在 staging 跑過 24h 才能上 prod；ALTER 大表強制 `CONCURRENTLY` 變體 + lint 攔。

### Rate limit & abuse 偵測

**Per-token rate limit**：
- 寫入類：60 req/min/token
- 讀取類：600 req/min/token
- preview 創建：10 req/min/team（避免 LLM 失控連發）
- 超限回 `429` + `Retry-After` header；CLI 自動退避

**Per-team rate limit**（防個別 token 規避）：
- 全 team 寫入合計：300 req/min
- 全 team build 觸發：20 builds/hour（plan=free），plan 升級放寬

**異常偵測**（v1 量測，v1.1 自動阻擋）：
- token 在 1h 內從 ≥ 3 個 ASN 出現 → audit_log + owner 通知
- 同 IP 對 ≥ 5 個 team 嘗試 401 → 短時封鎖 IP
- preview 創建 / consumption ratio > 10:1 持續 1h → 標記異常

**實作**：`golang.org/x/time/rate` token bucket per (team_id, token_id)；上 metric `0ops_rate_limit_triggered_total{kind, scope}`。

