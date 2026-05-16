# 0ops backend chart (`deploy/server`)

Helm chart for the 0ops HTTP backend (`cmd/server`) with K8s Lease
leader election (M5+ HA topology).

> Spec：`docs/features/backend-ha-leader-election/spec.md`
> ADR：`docs/adrs/0008-backend-ha-and-replication.md`

## TL;DR

- `replicas: 2`（spec § 14 hard rule #1）
- `OPS_LEADER_MODE=lease` 由 chart 注入；reconciler / preview cleanup /
  domain verify polling / ghcr-pull token refresh 等背景任務僅在 leader
  pod 啟動（spec § 5.1）
- Lease 物件名 `0ops-backend-leader`，位於 `system-0ops` namespace
- preStop `sleep 5` 讓 ingress 從 endpoints 移除後再停 SIGTERM；
  backend graceful shutdown 自行 release lease（`ReleaseOnCancel: true`
  硬寫於 `internal/server/leader.LeaseLeader.Run`）

## 渲染期硬閘（spec § 14 hard rules）

| 規則 | 條件 | 模版 |
|---|---|---|
| #1 | `replicas < 2` 拒渲 | `templates/deployment.yaml` |
| #1 / #5 | `leaderElection.mode != "lease"` 拒渲 | 同上 |
| #7 | `preStop.sleepSeconds < 5` 拒渲 | 同上 |

`chart_test.go` 同時守備 `fail` 文字 + 預設 values，確保未來改 chart
時不會繞過上述閘門。

## 部署

```bash
# render only
helm template . > /tmp/ops-server.yaml

# install
helm upgrade --install ops-server . -n system-0ops --create-namespace
```

## Lease RBAC

`templates/role.yaml` 把 backend ServiceAccount 的 lease 權限縮窄到
`resourceNames: [<leaseName>]`，避免它能影響其他 Lease 物件
（spec § 14 hard rule #2）。`create` 與 `list` 必須是 cluster-scoped
RBAC 規則無法用 resourceName 縮限的部分，分離出第二條 rule。

## Dev vs Production

- **Dev compose**（root `compose.yaml`）：single replica，無 K8s。`cmd/server`
  以 `OPS_LEADER_MODE=always`（預設）啟動 `leader.AlwaysLeader`，等同
  pre-M5.5 行為。所有現有測試與 dev smoke 不受影響。
- **Production**（本 chart）：2 replicas，K8s Lease。`OPS_LEADER_MODE=lease`
  由 Deployment env 注入。
