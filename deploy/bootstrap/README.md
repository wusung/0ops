# deploy/bootstrap — 0ops production bootstrap pack

> 對應 spec：`docs/features/production-deployment/spec.md`
> 入口：`./manage.sh prod-up` （或 `bash deploy/bootstrap/up.sh`）

## TL;DR

一台空 K3s host + 一份填好的 `.env.prod` → 一條指令 → `https://api.<domain>/health` HTTP 200。

## Option A：一條指令到底（推薦）

```bash
cp deploy/bootstrap/env.example deploy/bootstrap/.env.prod
$EDITOR deploy/bootstrap/.env.prod    # 填 CF token / domain / image tag / pg password
./manage.sh prod-bootstrap-all        # 串完 setup-oauth → verify-oauth → up → smoke →
                                       # install-runner → runner-validate → e2e production
```

`prod-bootstrap-all` 全步冪等；任一步失敗即停。
重跑 `--resume-from=N` 從第 N 步續。
不想要 self-hosted runner：`--skip-runner`；不想跑最終 e2e：`--skip-e2e`。

完整 runbook：[`docs/runbooks/production-acceptance.md`](../../docs/runbooks/production-acceptance.md)。

## Option B：分步走

```bash
cp deploy/bootstrap/env.example deploy/bootstrap/.env.prod
$EDITOR deploy/bootstrap/.env.prod
./manage.sh prod-setup-oauth
./manage.sh prod-verify-oauth
./manage.sh prod-up
./manage.sh prod-smoke
./manage.sh prod-install-runner    # 可選
gh variable set GHA_RUNNER_LABEL --repo wusung/0ops --body 0ops-builder
./manage.sh prod-runner-validate
E2E_MODE=production OPS_HOST=https://api.<domain> ./manage.sh e2e-create-app
```

## 前置（user 手動，repo 內不自動完成）

| 項目 | 取得方式 |
|---|---|
| K3s host | VPS / homelab，Linux + ssh key 設好 |
| Cloudflare zone `jesontech.com` | 已在 Cloudflare 帳號內 |
| `*.jesontech.com` CNAME | Cloudflare zone → DNS → wildcard CNAME → tunnel hostname |
| Cloudflare Tunnel | one.dash.cloudflare.com → Networks → Tunnels → Create → 拿 token |
| GitHub OAuth App | `./manage.sh prod-setup-oauth` 互動式建立（runbook `docs/runbooks/production-oauth-setup.md`）；或手動 `github.com/settings/developers` |
| `kubeseal` CLI | `brew install kubeseal` / `pacman -S kubeseal` |
| ghcr images 匿名可拉 | public repo 自動成立；private/fork 見下節 § ghcr |

## ghcr（visibility 說明）

release workflow 的 `images` job 對每個 tag 發佈
`ghcr.io/wusung/0ops-server` 與 `ghcr.io/wusung/0ops-migrations`。

- **public repo**（本 repo 現狀）：Actions 以 `GITHUB_TOKEN` 推的 package
  自動關聯 repo 並繼承 public —— 無需手動操作。v0.1.3 實測匿名可拉。
- **private repo / fork**：package 會是 private，cluster 匿名拉 401。
  GitHub **不提供 REST API 改 package visibility**（PATCH 實測 404）；
  只能走 UI：Profile → Packages → <package> → Package settings →
  Danger Zone → Change visibility → Public。

`prod-up` 的 step 0 preflight 會驗兩個 image 可匿名拉，沒過會在動 host 之前
fail-fast 並指向本節。

## 流程

```
up.sh
  ├─ preflight                驗 ghcr images 存在且可匿名拉（fail-fast）
  ├─ install-k3s.sh           ssh 到 PROD_HOST，curl get.k3s.io 安裝 K3s
  ├─ scp kubeconfig 回 local
  ├─ install-argocd.sh        kubectl apply 官方 manifest
  ├─ install-sealed-secrets.sh kubectl apply controller，撈 pubkey
  ├─ seal-secrets.sh          .env.prod → 3 個 SealedSecret YAML
  ├─ kubectl apply tmp/sealed/+ root-app.yaml
  ├─ wait-for-sync.sh         等 ArgoCD 全 Synced+Healthy（postgres/tunnel/observability，5 min timeout）
  └─ smoke.sh                 curl /health → 200
```

> **`ops-server` 不再由 ArgoCD 安裝/更新**（tag-driven CD，見
> `docs/features/production-deployment/spec.md` § 7）。`up.sh` 跑完只代表
> postgres / cloudflare-tunnel / observability 就緒；`ops-server` 的第一次安裝
> 需手動跑一次 `helm upgrade --install ops-server deploy/server -n system-0ops
> --create-namespace -f deploy/server/values-prod.yaml --set image.tag=<tag>
> --set migrations.image.tag=<tag>`，或直接推一個 release tag 讓
> `.github/workflows/release.yml` 的 `deploy` job（跑在 self-hosted runner
> 上）代勞。之後每個 release tag 都會自動重跑這條 `helm upgrade`。

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

`deploy/gitops/argocd/apps/*.yaml` 是 ArgoCD 從 git 直接讀取的 Application 物件
（postgres / cloudflare-tunnel / observability）；ArgoCD 不做 env substitution。
`ops-server` 的域名 / image 則由 `helm upgrade` 直接吃 `deploy/server/values-prod.yaml`
（不經 ArgoCD）。若你的域名 / image 不是 `api.jesontech.com` /
`ghcr.io/winshare/ops-server`，請 fork 本 repo，修：

- `deploy/server/values-prod.yaml` 的 `ingress.host` / `config.publicURL` / `config.domainBase`
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
