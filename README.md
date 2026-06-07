# 0ops

Internal PaaS control plane driven by **CLI + MCP**. Give your AI CLI one prompt — 0ops takes a repo
to a running app on `*.winshare.tw` or your own domain.

## TL;DR

```sh
# 1. install + login + AI CLI 接線（一條 curl）
OPS_HOST=https://api.<your-0ops> \
  curl -fsSL https://raw.githubusercontent.com/wusung/0ops/main/scripts/install.sh | sh
# device flow login → 自動偵測 claude / codex CLI → 寫 MCP config

# 2. 重啟 AI CLI

# 3. 在 AI CLI 內，直接說：
#    "幫我把這個 repo deploy 到 0ops，叫 nextdemo"
```

不設 `OPS_HOST` → 只裝 binary；事後手動 `0ops auth login` + `0ops mcp setup`，
或補一條 `0ops onboard https://api.<your-0ops>`。

Full walkthrough: [`docs/quickstart.md`](docs/quickstart.md).

## What's in this repo

| Path | Purpose |
|---|---|
| `src/` | Go binaries：`cmd/server` (backend)、`cmd/cli` (`0ops`)、`cmd/mcp` (`0ops-mcp`) |
| `scripts/install.sh` | One-line installer（end-user 取用） |
| `deploy/server` / `deploy/postgres` / `deploy/chart/cloudflare-tunnel` | Helm charts（self-host） |
| `deploy/bootstrap/` | self-host：`./manage.sh prod-up` 一鍵裝整套到 K3s |
| `deploy/gitops/argocd/` | ArgoCD app-of-apps |
| `docs/features/*/spec.md` | feature specs（規格來源） |
| `docs/adrs/*` | architecture decision records |
| `docs/runbooks/*.md` | ops runbooks |
| `tasks/` | task harness + 進度（`todo.md` / `lessons.md`） |

## Self-host

完整端到端走法見 [`deploy/bootstrap/README.md`](deploy/bootstrap/README.md)。要點：

- 一台 K3s host + 一份填好的 `deploy/bootstrap/.env.prod` → `./manage.sh prod-up`
- ArgoCD GitOps 自管；sealed-secrets 處理 secret；Cloudflare Tunnel 對外
- production GitHub OAuth App 走 `./manage.sh prod-setup-oauth`

## Status

- v1 backbone (M0-M6) shipped；source ingestion、preview/confirm、HA、PITR、reconciler 都在
- Production rollout 端的 chart / bootstrap / runbook 已封裝完整
- end-user 安裝 + AI CLI 接線 UX：本檔 + `docs/quickstart.md` + `0ops mcp setup`

詳：`tasks/todo.md` v1 收尾殘留段。

## Contributing

- 規範：`AGENTS.md`
- 文件讀序：`docs/0ops-plan.md` → `docs/agents-guide.md` → `docs/adrs/`
- 提交前自查清單：見 `AGENTS.md` § Review Heuristics

## License

Brian@sakilu.com retains all rights. v1 全閉源；OSS-core 範圍待 founder 決策。
