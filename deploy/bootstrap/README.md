# deploy/bootstrap — 0ops production bootstrap pack

> 對應 spec：`docs/features/production-deployment/spec.md`
> 入口：`./manage.sh prod-up` （或 `bash deploy/bootstrap/up.sh`）

## TL;DR

一台空 K3s host + 一份填好的 `.env.prod` → 一條指令 → `https://api.<domain>/health` HTTP 200。

```bash
# 1. 準備 host（VPS / homelab，Linux + sshd + 你的 ssh pubkey 在 authorized_keys）

# 2. 準備 .env.prod
cp deploy/bootstrap/env.example deploy/bootstrap/.env.prod
$EDITOR deploy/bootstrap/.env.prod   # 填 CF token / OAuth / domain / image tag / pg password

# 3. 跑
./manage.sh prod-up

# 4. 驗
./manage.sh prod-smoke
```

## 前置（user 手動，repo 內不自動完成）

| 項目 | 取得方式 |
|---|---|
| K3s host | VPS / homelab，Linux + ssh key 設好 |
| Cloudflare zone `winshare.tw` | 已在 Cloudflare 帳號內 |
| `*.winshare.tw` CNAME | Cloudflare zone → DNS → wildcard CNAME → tunnel hostname |
| Cloudflare Tunnel | one.dash.cloudflare.com → Networks → Tunnels → Create → 拿 token |
| GitHub OAuth App | github.com/settings/developers → New OAuth App，callback 設 `https://api.<domain>/v1/auth/oauth2/callback` |
| `kubeseal` CLI | `brew install kubeseal` / `pacman -S kubeseal` |

## 流程

```
up.sh
  ├─ install-k3s.sh           ssh 到 PROD_HOST，curl get.k3s.io 安裝 K3s
  ├─ scp kubeconfig 回 local
  ├─ install-argocd.sh        kubectl apply 官方 manifest
  ├─ install-sealed-secrets.sh kubectl apply controller，撈 pubkey
  ├─ seal-secrets.sh          .env.prod → 3 個 SealedSecret YAML
  ├─ kubectl apply tmp/sealed/+ root-app.yaml
  ├─ wait-for-sync.sh         等 ArgoCD 全 Synced+Healthy（5 min timeout）
  └─ smoke.sh                 curl /health → 200
```

## 冪等

| 階段 | 冪等實現 |
|---|---|
| install-k3s | 偵測 `command -v k3s` 已存在則 skip |
| install-argocd | `kubectl apply` 本身冪等 |
| install-sealed-secrets | 同上 |
| seal-secrets | output 覆寫 |
| ArgoCD root app | `automated.prune=true` 確保收斂 |

任何步驟失敗 → 修 env 或 chart → 再次 `prod-up`。

## 非預設域名 / 非預設 image

`deploy/gitops/argocd/apps/*.yaml` 是 ArgoCD 從 git 直接讀取的 Application 物件；
ArgoCD 不做 env substitution。若你的域名 / image 不是 `api.winshare.tw` / `ghcr.io/winshare/ops-server`，
請 fork 本 repo，修：

- `deploy/gitops/argocd/apps/server.yaml` 的 `helm.parameters`（`ingress.host` / `config.publicURL` / `image.tag` 等）
- `deploy/gitops/argocd/root-app.yaml` 與 `apps/*.yaml` 的 `repoURL` 指向你的 fork
- `deploy/bootstrap/.env.prod` 的 `ARGOCD_REPO_URL` / `PROD_API_HOST` 對齊

之後 `prod-up` 會以你的 fork 為 GitOps 來源。

## 安全模型

- `.env.prod` 明文 secret，**絕對不可 commit**。已在 `deploy/bootstrap/.gitignore`。
- `tmp/sealed/*.yaml` 是 ciphertext，理論可 commit 入 git。本 repo 暫不 commit（透過 `.gitignore`），
  由 `up.sh` 每次重新產生。未來導入 GitOps 完整化（user changes via PR）時可改為 commit。
- backend chart 不渲明文 `Secret` template（`chart_test.go` 守住）；
  cluster 內的 `ops-server-env` Secret 一律由 sealed-secrets controller unseal 產生。

## 卸載

```bash
./manage.sh prod-down
```

- 刪 ArgoCD root app → 級聯刪 child（system-0ops、cloudflare-tunnel、observability）
- Postgres namespace 與 PVC 保留（避免誤刪資料）
- 如要全清：`kubectl delete ns postgres`

## 故障

- `up.sh` 任一步驟 fail → 看 `[prod-up]` log → 修 → 重跑
- ArgoCD sync 卡住 → 看 `kubectl -n argocd get application -o wide`
- Smoke fail → `docs/runbooks/winshare-route-failure.md`
