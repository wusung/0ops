# Feature Spec：backend-ha-leader-election

> **狀態**：draft
> **來源**：`docs/0ops-plan.md`「Backend 自身部署 topology」段；ADR-0008（HA / leader election / SSE cursor）；本 spec 依賴 `read-api-vertical-slice`、`reconciler-and-incident`、`preview-confirm-gate`、`k3s-namespace-isolation`、`postgres-ha-and-dr`、`secrets-management`
> **適用範圍**：M5 backend 升 2 replica + leader election；leader / follower 角色分工；SSE stateless cursor reconnection；rolling update + graceful shutdown
> **對應 Milestone**：M5

## 1. 結論（先讀本段）

- M5 前：backend `Deployment` replicas=1；無 leader election 程式碼
- M5：replicas=2；兩 pod 共用同 binary；啟動時皆呼 `leaderelection.RunOrDie()`
- 採 K8s Lease object（`k8s.io/client-go/tools/leaderelection`）；無外部依賴
- Lease 位於 `system-0ops` namespace，name `0ops-backend-leader`；duration 15s / renew deadline 10s / retry period 2s
- **角色分工**：
  - leader：reconciler、preview cleanup、domain verify polling、ghcr-pull token refresh、metrics emission for background tasks
  - follower：同樣服務 read/write API、SSE；不跑背景 goroutine
- SSE 採 stateless cursor reconnection：W3C `Last-Event-ID` header / `?cursor=` query；任一 pod 都可接續（接續 `read-api-vertical-slice` § 4.4）
- 計畫內 failover：leader 收 SIGTERM → 立即 release lease；新 leader < 5s 接手
- 非計畫內 failover：lease duration 15s 超時 → 新 leader 接手
- HPA 不開啟（M5 手動 replicas=2）；M6 後評估
- backend 不主動分發 read 請求至特定 pod；Service ClusterIP 隨機分流
- 多 pod 之 metric 採 `pod_name` label（已加入 ADR-0006 cardinality 例外，本 spec 釘細節）

## 2. 範圍

### 2.1 包含
- `internal/server/leaderelection/` package：Leader 介面、K8s Lease 實作
- 主程式進入 leader election 流程
- Leader / follower 之 lifecycle（goroutine 啟停、metric register/unregister）
- SSE 行為對 leader handover 之透明性
- preStop hook（drain SSE + release lease）
- Metric pod_name label 規範
- Failover RTO / handover latency 目標

### 2.2 不包含
- Reconciler 各 kind 之內部邏輯（屬 `reconciler-and-incident` spec）
- Preview cleanup 內部邏輯（屬 `preview-confirm-gate` § 8.2）
- ghcr-pull refresh 內部邏輯（屬 `k3s-namespace-isolation` § 8.2）
- Postgres HA（屬 `postgres-ha-and-dr` spec）
- HPA 設定（M6+）
- Multi-region / multi-AZ（v2）
- Service mesh（v2）

## 3. 檔案結構

```
0ops/
├── internal/
│   └── server/
│       └── leader/                   # 套件名 leader（M5.5 落地對齊 tasks/task-list.md Expected Paths）
│           ├── doc.go                # 套件概述 + ADR-0008 引用
│           ├── leader.go             # Leader 介面 + AlwaysLeader 實作
│           ├── identity.go           # PodIdentity()：POD_NAME / hostname + 8-char uuid 後綴
│           ├── lease.go              # LeaseLeader：client-go leaderelection.RunOrDie
│           └── metrics.go            # Observer 介面 + NopObserver + PrometheusProvider（client-go MetricsProvider 橋接）
├── cmd/
│   └── server/
│       └── leader.go                 # buildLeader(mode, identity, ...) + reconcilerLeaderGate + metricsLeaderObserver
└── deploy/
    └── server/                       # M5 chart：Deployment replicas=2 + Lease RBAC
        ├── Chart.yaml
        ├── values.yaml
        ├── chart_test.go             # 渲染期硬閘（spec § 14 #1/#5/#7）+ 預設 values 守備
        └── templates/
            ├── deployment.yaml       # replicas/strategy/preStop/probes/OPS_LEADER_MODE=lease
            ├── service.yaml          # ClusterIP 8080
            ├── serviceaccount.yaml   # backend SA
            ├── role.yaml             # coordination.k8s.io/leases verbs，resourceName 限定到 leaseName
            └── rolebinding.yaml      # SA ↔ Role binding
```

> 早期 spec draft 列 `internal/server/leaderelection/`；M5.5 落地對齊
> `tasks/task-list.md` row M5.5 Expected Paths（`internal/server/leader/**`、
> `deploy/server/**`），harness verify 契約以 task-list 為準。

## 4. Leader 介面

### 4.1 Go 介面

```go
// internal/server/leaderelection/leader.go
type Leader interface {
    IsLeader() bool                      // 任一時刻查當前是否為 leader
    Identity() string                    // 當前 pod identity
    Subscribe() <-chan Event             // event stream（gain/lost）
}

type Event struct {
    Kind      EventKind   // gain | lost
    Timestamp time.Time
}

type EventKind int

const (
    EventGain EventKind = iota
    EventLost
)
```

### 4.2 K8s Lease 實作

```go
// internal/server/leaderelection/lease.go
import (
    "k8s.io/client-go/kubernetes"
    "k8s.io/client-go/tools/leaderelection"
    "k8s.io/client-go/tools/leaderelection/resourcelock"
)

func RunOrDie(ctx context.Context, k8s *kubernetes.Clientset, identity string, onGain, onLost func()) {
    rl := &resourcelock.LeaseLock{
        LeaseMeta: metav1.ObjectMeta{
            Name:      "0ops-backend-leader",
            Namespace: "system-0ops",
        },
        Client: k8s.CoordinationV1(),
        LockConfig: resourcelock.ResourceLockConfig{
            Identity: identity,
        },
    }

    leaderelection.RunOrDie(ctx, leaderelection.LeaderElectionConfig{
        Lock:            rl,
        LeaseDuration:   15 * time.Second,
        RenewDeadline:   10 * time.Second,
        RetryPeriod:     2 * time.Second,
        ReleaseOnCancel: true,         // SIGTERM 時立即 release
        Callbacks: leaderelection.LeaderCallbacks{
            OnStartedLeading: func(ctx context.Context) { onGain() },
            OnStoppedLeading: func() { onLost() },
            OnNewLeader:      func(id string) { /* metric */ },
        },
    })
}
```

### 4.3 v1 single replica 之退化

- v1 為 single replica；不跑 leader election 程式碼
- `leader.Leader` 介面之實作有兩種：`AlwaysLeader`（v1）、`LeaseLeader`（M5）
- backend `main.go` 依環境變數 `OPS_LEADER_MODE=always|lease` 選擇；v1 預設 `always`，M5 chart 設 `lease`

## 5. Leader / follower 角色分工

### 5.1 Leader-only goroutines

啟動於 `OnStartedLeading` callback：

| Goroutine | 來源 spec |
|---|---|
| `reconciler.RunDeployStatus` | `reconciler-and-incident` § 5 |
| `reconciler.RunDomainVerify` | `custom-domain-and-verify` § 6 |
| `reconciler.RunGhcrRefresh` | `k3s-namespace-isolation` § 8.2 |
| `preview.RunGC` | `preview-confirm-gate` § 8.2 |
| `secrets.OpsTokenRefresher` | `secrets-management`（v1.1）|
| `audit.RotatePartition`（每月 1 號） | `audit-log` § 9 |

### 5.2 Follower 同跑

| 服務 | 說明 |
|---|---|
| HTTP API（read / write）| 兩 pod 都接 |
| SSE（tail_logs）| 兩 pod 都可服務 |
| Webhook endpoint | 兩 pod 都接（`POST /webhooks/github` / `/internal/deploy-runs/.../callback`）|
| `/metrics` / `/healthz` / `/readyz` | 兩 pod 都暴露 |

### 5.3 OnGain / OnLost callback（M5.5 落地：Pull-not-Push）

M5.5 落地時 reconciler runner 已是 IsLeader() pull gate（spec § 5 圖示與
`internal/server/services/reconciler/runner.go` 之 `skipped_not_leader`
metric）；leader 套件不再 spawn / cancel 背景 ctx，僅由 Observer
callback 推 metric / log，背景 goroutine 自己 poll IsLeader()
決定該 tick 是否動工。下面 callback 範例代表「最小應做事」而非要求
push 模型。

```go
// main.go (節錄)
func main() {
    leader := leaderelection.New(...)

    go leader.RunOrDie(ctx, identity,
        /*onGain*/ func() {
            startBackgroundGoroutines(ctx)
            metrics.LeaderGauge.Set(1)
        },
        /*onLost*/ func() {
            cancelBackgroundCtx()       // 各 goroutine 透過 ctx.Done() 退出
            metrics.LeaderGauge.Set(0)
            log.Warn("lost leader lease; entering follower mode")
        },
    )

    // 啟動 HTTP server（與 leader 狀態無關）
    httpServer.ListenAndServe()
}
```

### 5.4 Lost lease 後行為

- 立即 cancel 背景 goroutine context；各 goroutine 在 < 5s 內收到 ctx.Done() 並 return
- HTTP API 繼續服務（follower 模式）
- 不 panic；不重啟（避免 SIGTERM 風暴）
- 等下次 lease 可取（其他 leader 也 SIGTERM 或 crash）再進 leader

## 6. SSE stateless cursor

### 6.1 與 `read-api-vertical-slice` § 4.4 對齊

- 每 SSE event `id:` 為 RFC3339Nano timestamp
- Client 重連帶 `Last-Event-ID: <id>` header（W3C SSE）或 `?cursor=<id>` query
- Backend 任一 pod 接收 reconnect → K8s log API `SinceTime: cursor` 接續

### 6.2 Pod handover 對 SSE 影響

| 場景 | client 體感 |
|---|---|
| 計畫內 rolling update（leader 切換）| SSE 連線中斷 → CLI / MCP reconnect → 任一 pod 接續，cursor 重續 |
| 非計畫內 leader pod crash | 同上；ingress 自然把流量導至剩下 pod |
| Pod 啟動慢（Readiness 未過）| 不接流量；SSE 全集中於 ready pod |

### 6.3 Keepalive

- Backend SSE 端每 15s 送 `: keepalive\n\n`（comment line）防 ingress idle timeout
- 預設 traefik idle timeout 10 min；keepalive 15s 安全餘裕
- v1.1 評估設 ingress timeout 至 1h（與 SSE 預期使用時長對齊）

## 7. 滾動更新策略

### 7.1 Deployment manifest

```yaml
apiVersion: apps/v1
kind: Deployment
metadata:
  name: ops-server
  namespace: system-0ops
spec:
  replicas: 2                     # M5
  strategy:
    type: RollingUpdate
    rollingUpdate:
      maxSurge: 1
      maxUnavailable: 0
  template:
    spec:
      terminationGracePeriodSeconds: 60
      containers:
        - name: ops-server
          image: ghcr.io/winshare/ops-server:<version>
          lifecycle:
            preStop:
              exec:
                command: ["/bin/sh", "-c", "sleep 5 && /ops-server drain"]
          readinessProbe:
            httpGet: { path: /readyz, port: 8080 }
            periodSeconds: 5
            failureThreshold: 3
          livenessProbe:
            httpGet: { path: /livez, port: 8080 }
            periodSeconds: 10
            failureThreshold: 3
```

### 7.2 preStop hook

- `sleep 5`：給 ingress / Service 時間將該 pod 從 endpoints 移除
- `/ops-server drain`：自簽指令，執行：
  - 主動發 `event: end` 至所有 SSE client（提示 reconnect）
  - 等 in-flight HTTP request 結束（max 30s）
  - 若 leader：cancel leader lease（K8s client-go RunOrDie 之 ctx 結束自動 release）

### 7.3 Failover 時間

| 階段 | 時間 |
|---|---|
| SIGTERM → preStop start | 0s |
| preStop sleep 5s + drain（含 SSE end notify）| ~10s |
| K8s remove from endpoints | < 5s（並行）|
| Lease release | < 1s（cancel ctx 後立即）|
| 其他 pod 取得 lease | retryPeriod 2s |
| 新 leader OnStartedLeading 啟動 goroutine | < 1s |
| **總計（leader handover）** | **< 5s** |

## 8. Metric pod_name label

### 8.1 例外加 label

ADR-0006 § 4 第 2 點：`{route, method, status, team_bucket}` 為固定 label set；本 spec 對 backend HA 相關 metric 增加 `pod_name` label。
M5.5 落地時所有 metric 命名統一採專案既有 `zeroops_` 前綴（與 `internal/server/observability/metrics.go` 22+ 條現役 metric 對齊）：

| Metric | 加 `pod_name` | M5.5 範圍 |
|---|---|---|
| `zeroops_leader_status`（gauge 0/1） | 是 | 是 |
| `zeroops_leader_handover_total`（counter） | 是 | 是 |
| `zeroops_leader_lease_renew_total{pod_name, outcome}` | 是 | 是（outcome ∈ {acquired, lost, slow_acquire}，由 client-go MetricsProvider 經 `leader.PrometheusProvider` 橋接） |
| `zeroops_sse_active_connections`（gauge） | 是 | 否（屬 `read-api-vertical-slice` § 4.4 之 SSE 計收，M5.5 不引入） |

> `pod_name` cardinality：v1 = 2 pod；HPA 後可能達 5–10；可控。其他既有 metric **不**加 `pod_name`，避免 cardinality 爆炸。
>
> `lease_renew_total` 的 `pod_name` 在 v1 落地為空字串 / `unknown`：client-go 的
> `MetricsProvider.NewLeaderMetric()` 為 process-global onlyOnce 結構，
> 不會在每次 On/Off/Slowpath 呼叫帶 identity；HA 儀表板可改 join
> `zeroops_leader_status` 取得 pod-level 視角。

## 9. 與其他 spec 接合點

| 接合 | spec |
|---|---|
| Reconciler kinds 啟動時機 | `reconciler-and-incident` § 5 |
| Preview cleanup 啟動時機 | `preview-confirm-gate` § 8.2 |
| Domain verify polling 啟動時機 | `custom-domain-and-verify` § 6 |
| ghcr-pull refresh 啟動時機 | `k3s-namespace-isolation` § 8.2 |
| SSE cursor 與 K8s log SinceTime | `read-api-vertical-slice` § 4.4 |
| Deployment chart 之 PSA / Quota | `k3s-namespace-isolation` § 4.1 |
| Postgres failover 對 backend 影響 | `postgres-ha-and-dr` § 9 |
| Audit 對 leader handover | `audit-log` spec |
| Incident 對 lease false failover | `reconciler-and-incident` § 9.2 |
| Lease 寫入 K3s control plane Postgres（kine）| ADR-0004 |

## 10. 驗證準則

| 驗證項 | 方式 | 通過條件 |
|---|---|---|
| 兩 pod 起動：僅一 leader | `kubectl get lease -n system-0ops` | 一個 holderIdentity；`metrics{leader_status=1}` 僅一 pod |
| Leader 跑背景 goroutine | leader pod log | 含 reconciler / cleanup tick；follower 無 |
| Follower 不跑背景 | follower pod log | 無上述 tick |
| 兩 pod 都接 API | `for i in 1..100: curl ...` 觀察 backend log | 兩 pod 各約 50% |
| 兩 pod 都接 SSE | mock 多 SSE client | 連線分散在兩 pod |
| 計畫內 failover < 5s | `kubectl rollout restart` | leader 切換時間 < 5s（從 lease release 到 new leader） |
| 非計畫內 failover < 15s | `kubectl delete pod <leader>` | 新 leader 在 lease duration 後接手 |
| Background goroutine 切換 | failover 後第一個 reconciler tick | 出現於新 leader pod log；舊 leader 無 |
| SSE 透明於 handover | mock SSE client 在 failover 期間連線 | reconnect 後 cursor 接續正確；無 log line 重複 / 漏 |
| Lease lost 不 panic | mock K3s API 短暫不可達 > 15s | backend log 警告 + 進 follower；下次 retry 可重 leader |
| Lease ReleaseOnCancel | SIGTERM 後檢查 lease | holderIdentity 立即清空 |
| Keepalive | mock SSE 閒置 5 min | client 收到 keepalive comment；無 idle disconnect |
| preStop drain | rolling update 時 SSE client | 收到 `event: end`；reconnect 至新 pod |

## 11. SLI 對應

| SLI | 目標 | 量測 |
|---|---|---|
| Backend availability | 99.9% / 28d（plan SLO）| `0ops_http_requests_total{status!~"5.."} / total` |
| Leader handover latency p95 | < 5s | `0ops_leader_handover_total` 觀察時間 |
| Lease false failover 頻率 | < 1 次 / week | `0ops_leader_handover_total` rate |
| Lease renew 成功率 | > 99.9% | `0ops_leader_lease_renew_total{outcome=success}` |
| SSE 連線總數 / pod | < 1000 | `sse_active_connections` |
| Background goroutine miss tick | 0 | reconciler tick 預期 vs 實際 |

## 12. 對 `docs/0ops-plan.md` 的修改清單

1. 「Backend 自身部署 topology」段：交叉引用本 spec；補入「leader handover < 5s」「lease 機制詳見本 spec」
2. 「Backend 自身部署 topology / SSE 多實例」段：以 `read-api-vertical-slice` § 4.4 + 本 spec § 6 為 source；plan 內 sticky / Redis 描述標 outdated
3. 「Risks & open #9（單一 chi service 可擴展性）」：交叉引用本 spec
4. ADR-0006 metric cardinality 段：補入「`pod_name` label 為 leader-related metric 之例外」
5. 「Probe」段：對齊本 spec § 7 之 readiness / liveness 行為

## 13. Open issues

> 來源：ADR-0008 § 9 之 8 條 OQ + 本 spec 撰寫期間發現

- ADR-0008 OQ#1（Lease duration 調參）：v1 起手 15/10/2s；M5 後依 production observation 調
- ADR-0008 OQ#2（HPA 開啟條件）：M5 不開；M6 評估 CPU + RPS 雙條件
- ADR-0008 OQ#3（Read replica 暴露）：屬 `postgres-ha-and-dr`
- ADR-0008 OQ#5（K3s control plane HA）：v1 K3s 為 single node；backend HA 但 K3s 仍 SPOF；M5 後評估 K3s embedded etcd HA
- ADR-0008 OQ#6（SSE 連線總數上限）：spike 2 pod × N connection 之 OOM / fd 上限
- ADR-0008 OQ#7（cross-cluster failover）：v2
- ADR-0008 OQ#8（Ingress idle timeout 與 keepalive）：本 spec § 6.3 採 15s keepalive；M5 前實測 traefik 行為
- Lease holder identity 之 audit：本 spec 採 metric + log；audit_log 不寫（避免每 2s 一筆）
- 多 leader bug 偵測：透過 `0ops_leader_status` 之 sum > 1 → alert
- v1 → M5 升級流程：ops 在 chart 升 replicas=2 + ENV `OPS_LEADER_MODE=lease` 即可；無需重新部署 secret / DB
- HPA + leader 對非 leader pod 之擴展：擴出來的 pod 永遠 follower（合理）；縮回時可能砍到 leader（觸發 handover）

## 14. 不可違反的硬性規則

> 違反以下任一項，PR 不可合入。

1. M5+ backend 必跑 leader election；single replica 不再允許於 production
2. Lease 必使用 K8s Lease object；不得用 Postgres advisory lock / Redis / 其他
3. Lease duration 15s 為起手；變更需 ADR
4. `ReleaseOnCancel: true` 必設；保證 SIGTERM 立即釋放
5. 背景 goroutine 必 leader-only；follower 跑背景任務即視為 race bug
6. SSE 必走 stateless cursor；不得引入 ingress sticky cookie
7. preStop hook 必含 `sleep 5 + drain`；不得 immediate exit
8. Lost lease 後不 panic / 不 restart；只進 follower
9. Metric `pod_name` label 僅限本 spec § 8.1 列出之 metric；其他 metric 加 `pod_name` 視為 cardinality 違反
10. 同一時刻 leader 必只有一個；多 leader 偵測（`leader_status` sum > 1）必 critical alert
