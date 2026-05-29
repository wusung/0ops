# Runbook：winshare subdomain 路由失敗（skeleton）

> 對應 spec：`docs/features/winshare-subdomain-and-tunnel/spec.md`
> 對應 ADR：ADR-0007（customer-domain TLS）
> 適用範圍：`<slug>.winshare.tw` 外部 HTTP 不可達 / 回 4xx 5xx
>
> **狀態**：skeleton。Cloudflare wildcard CNAME + `deploy/chart/cloudflare-tunnel/` + K3s wildcard ingress
> 在 `tasks/todo.md`「`nextdemo.winshare.tw` 真實外部 HTTP 200」條目完成前 infra 尚未部署上線，
> 本檔僅列出觸發條件與已知排查向量；infra 落地後須補完具體 kubectl/cloudflared/argocd 指令。

## 1. 觸發條件（預期）

任一條件成立時進本 runbook：

1. 外部 curl `https://<slug>.winshare.tw` 回 `Connection refused` / `522` / `530`（tunnel 斷）
2. 外部 curl 回 `404`（DNS / ingress 沒對到 app）
3. 外部 curl 回 `5xx`（後端有但 health 不過）
4. `TunnelDown` 或 `TunnelConnectorsLow` Prometheus alert fire（rules `zeroops_cloudflare_tunnel_connectors_ready`）
5. create_app acceptance `E2E_MODE=production ./manage.sh e2e-create-app` fail

## 2. 排查向量（待 infra 落地後補完整步驟）

依「離客戶最近 → 離客戶最遠」順序逐層排除：

### 2.1 Cloudflare zone

- wildcard CNAME `*.winshare.tw` 是否指向 tunnel hostname
- Cloudflare proxy 是否 enabled（橘雲）
- WAF / firewall rule 是否誤擋
- 觀察 Cloudflare dashboard `Analytics → Traffic` 該 hostname 是否有 hit

### 2.2 Cloudflared tunnel

- `kubectl -n cloudflare-tunnel get pod -l app=cloudflared` 應有 ≥ 2 ready connector（spec § redundancy）
- `kubectl logs -l app=cloudflared --tail=100` 看是否報 `failed to dial origin` 或 `connection refused`
- 對應 `TunnelConnectorsLow` / `TunnelDown` alert 之觸發條件
- tunnel credential secret 是否還在效期

### 2.3 K3s ingress

- 該 app 對應 `Ingress` 是否存在：`kubectl -n <app-namespace> get ingress`
- ingress backend `service` 是否指向 healthy pod
- ingress controller log（traefik 或 nginx）是否報 backend connect fail

### 2.4 App pod

- `kubectl -n <app-namespace> get pod` 是否 ready
- `/health` endpoint 是否 200

### 2.5 ArgoCD sync state

- ArgoCD application 是否 `Synced + Healthy`；若 OutOfSync 走 `create-app-stuck.md` § 2B

## 3. 介入手段（待補）

infra 落地後補：
- 重啟 cloudflared connector 的步驟與最小衝擊順序
- 手動 patch ingress 的安全閘
- 緊急切離 winshare 走 fallback hostname 的流程（如有）

## 4. 失敗回退（待補）

infra 落地後補：
- 整個 tunnel down 時，是否有 backup hostname / fallback DNS
- Cloudflare zone-level outage 時的 customer-facing 通報模板

## 5. 演練要求（待補）

infra 落地後補：依 spec § SLO 排月度 chaos 演練（kill 1 connector → 應仍維持 redundancy；kill 2 connector → TunnelDown alert 應 fire 內 1 min）。

## 6. 落地時補完此 runbook 的觸發條件

當 `tasks/todo.md`「`nextdemo.winshare.tw` 真實外部 HTTP 200」條目完成（M2 驗收基準），
應於同一 PR 內把本檔 § 2-5 的「待補」部分填完並把本節（§ 6）刪除。
