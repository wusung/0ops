# Runbook：winshare subdomain 路由失敗

> 對應 spec：`docs/features/winshare-subdomain-and-tunnel/spec.md`
> 對應 spec：`docs/features/production-deployment/spec.md`
> 對應 ADR：ADR-0007（customer-domain TLS）
> 適用範圍：`<slug>.jesontech.com` 與 `0ops.jesontech.com` 外部 HTTP 不可達 / 回 4xx 5xx

## 1. 觸發條件

任一條件成立時進本 runbook：

1. 外部 curl `https://<slug>.jesontech.com` 回 `Connection refused` / `522` / `530`（tunnel 斷）
2. 外部 curl 回 `404`（DNS / ingress 沒對到 app）
3. 外部 curl 回 `5xx`（後端有但 health 不過）
4. `TunnelDown` 或 `TunnelConnectorsLow` Prometheus alert fire（rules `zeroops_cloudflare_tunnel_connectors_ready`）
5. acceptance `E2E_MODE=production bash tasks/e2e-create-app.sh` fail
6. `./manage.sh prod-smoke` fail

## 2. 排查向量

依「離客戶最近 → 離客戶最遠」順序逐層排除。每層附可直接複貼指令。

### 2.1 Cloudflare zone

```bash
# wildcard CNAME 是否指向 tunnel hostname
dig +short '*.jesontech.com' CNAME
# 應回 <tunnel-uuid>.cfargotunnel.com 或自設 tunnel hostname

# 該 hostname 公開 TLS handshake 是否正常
openssl s_client -connect "${HOST:-nextdemo.jesontech.com}:443" \
  -servername "${HOST:-nextdemo.jesontech.com}" </dev/null 2>&1 | head -20

# Cloudflare proxy 是否 enabled（橘雲）
dig +short '@1.1.1.1' "${HOST:-nextdemo.jesontech.com}" A
# 應回 Cloudflare anycast IP (104.x / 172.67.x)
```

故障：

- DNS 沒回 CNAME → 進 Cloudflare dashboard → DNS → 補 `*.jesontech.com` CNAME → tunnel hostname → proxy=on。
- 回 origin IP（不是 Cloudflare）→ proxy 關了，dashboard 把橘雲開回來。
- WAF / firewall rule 阻擋 → Cloudflare dashboard → Security → WAF → 看 events 找 block 規則。
- 觀察 dashboard `Analytics → Traffic` 該 hostname 是否有 hit；無 hit 表示流量沒到 Cloudflare。

### 2.2 Cloudflared tunnel

```bash
export KUBECONFIG=~/.kube/0ops-prod

# 至少 2 connector ready（spec § redundancy；chart 預設 3）
kubectl -n cloudflare-tunnel get pod -l app=cloudflared
kubectl -n cloudflare-tunnel get pod -l app=cloudflared \
  -o jsonpath='{range .items[*]}{.metadata.name}{"\t"}{.status.containerStatuses[0].ready}{"\n"}{end}'

# log 看是否報 origin 不通
kubectl -n cloudflare-tunnel logs -l app=cloudflared --tail=200 \
  | grep -E 'failed to dial|connection refused|origin|ERR|Error'

# /metrics 端點看 connector ready 數
kubectl -n cloudflare-tunnel exec deploy/cloudflared -- \
  wget -qO- http://127.0.0.1:2000/metrics | grep cloudflared_tunnel_active_streams

# tunnel token secret 是否在
kubectl -n cloudflare-tunnel get secret cloudflared-tunnel-token -o jsonpath='{.data.token}' | head -c 12
```

故障：

- 0 connector ready → 確認 `cloudflared-tunnel-token` Secret 存在且 token 未過期；
  Cloudflare zerotrust dashboard → Networks → Tunnels 看狀態。
- Token 失效 → 在 Cloudflare dashboard rotate token → 修 `.env.prod` 的 `CF_TUNNEL_TOKEN` → 重跑 `bash deploy/bootstrap/seal-secrets.sh && kubectl apply -f deploy/bootstrap/tmp/sealed/` → 重啟 cloudflared：
  ```bash
  kubectl -n cloudflare-tunnel rollout restart deploy/cloudflared
  ```
- log 報 `failed to dial origin` → 走 § 2.3 查 traefik / ingress。

### 2.3 K3s ingress (traefik)

```bash
# 0ops.jesontech.com 由 ops-server chart ingress 提供
kubectl -n system-0ops get ingress ops-server -o yaml | head -40

# 該 app 對應 ingress（app-* namespace 由 backend 動態建立）
kubectl get ingress -A | grep "${SLUG:-nextdemo}"

# ingress backend service 是否有 healthy endpoints
kubectl -n "${NS:-app-nextdemo}" get endpoints

# traefik 自身 log
kubectl -n kube-system logs -l app.kubernetes.io/name=traefik --tail=200 | grep -i error
```

故障：

- Ingress 物件缺失 → 走 `docs/runbooks/create-app-stuck.md` § 2B（ArgoCD 未 sync）。
- Endpoints 為空 → app pod 沒 ready，走 § 2.4。
- traefik log 報 backend connect refused → app pod 拒連，走 § 2.4。

### 2.4 App pod

```bash
# 該 app pod 狀態
kubectl -n "${NS:-app-nextdemo}" get pod

# health 端點
kubectl -n "${NS:-app-nextdemo}" exec deploy/app -- wget -qO- http://127.0.0.1:8080/health || \
  kubectl -n "${NS:-app-nextdemo}" port-forward svc/app 18080:8080 &
  curl -sf http://127.0.0.1:18080/health
```

故障：

- Pod 不 ready → `kubectl describe` 看 events；常見原因：image pull fail / OOM / liveness fail。
- `/health` 不 200 → 看 app log；非 0ops 範疇，回報 app owner。

### 2.5 ArgoCD sync state

```bash
# 全部 app 狀態
kubectl -n argocd get application

# 對應 app 為何 OutOfSync
kubectl -n argocd describe application "${APP:-ops-server}" | tail -40

# 強制 sync（謹慎用，可能 mask 真正問題）
kubectl -n argocd patch application "${APP:-ops-server}" --type merge \
  -p '{"operation":{"sync":{"revision":"main"}}}'
```

OutOfSync → 走 `docs/runbooks/create-app-stuck.md` § 2B。

## 3. 介入手段

### 3.1 重啟 cloudflared connector（rolling，零中斷）

```bash
kubectl -n cloudflare-tunnel rollout restart deploy/cloudflared
kubectl -n cloudflare-tunnel rollout status deploy/cloudflared --timeout=120s
```

`Deployment.spec.strategy.rollingUpdate` 預設 maxUnavailable=25%，3 replica 至少維持 2 個 ready。

### 3.2 手動 patch ingress backend service port（緊急）

```bash
# 限：spec change 來不及走 ArgoCD sync 的緊急情境
kubectl -n "${NS:-system-0ops}" patch ingress ops-server --type=json -p='[
  {"op": "replace",
   "path": "/spec/rules/0/http/paths/0/backend/service/port/number",
   "value": 8080}
]'
```

緊急 patch 後，必須同 PR 改 chart values 並合回 git，否則 ArgoCD `selfHeal=true` 會把 patch 還原。

### 3.3 緊急切離 winshare 走 fallback hostname

v1 無 fallback hostname（spec § Open Questions Q3）。
緊急狀態建議：
1. Cloudflare dashboard → page rule 將 `jesontech.com` 全站導向 status page。
2. 公告維運。
3. 修正後撤 page rule。

## 4. 失敗回退

| 情境 | 回退步驟 |
|---|---|
| tunnel 整個 down，無 fallback hostname | 走 § 3.3 + 公告；估計 RTO ≤ 30 min |
| Cloudflare zone-level outage（CF 全域故障） | 不可控；Cloudflare status 通報 + 暫停 SLO 計時 |
| K3s host 整個失聯 | 觸發 ADR-0009 PITR：在新 host 走 `prod-up` + Postgres restore（runbook `postgres-restore-test.md`） |
| cloudflared image pull fail（registry 故障） | values.yaml image.pullPolicy 暫改 IfNotPresent；ghcr 修好後改回 |

## 5. 演練要求

依 spec § SLO 月度 chaos 演練：

```bash
# 1) Kill 1 connector → 應仍有 ≥ 2 ready
kubectl -n cloudflare-tunnel delete pod -l app=cloudflared --field-selector=status.phase=Running \
  --grace-period=0 --force | head -n1
# 期望 < 30s 內 readyReplicas 恢復 3

# 2) Kill 2 connector → TunnelDown alert 應在 1 min 內 fire
kubectl -n cloudflare-tunnel scale deploy/cloudflared --replicas=1
# 監看 alertmanager UI；確認 TunnelConnectorsLow 進入 firing
kubectl -n cloudflare-tunnel scale deploy/cloudflared --replicas=3  # 還原
```

演練後填 `tasks/lessons.md`：發現的失敗模式、修正動作、需 ADR 追加項。
