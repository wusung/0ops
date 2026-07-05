# 0ops 使用面事實包（寫手唯一事實源）

撰寫 30 天系列時，指令與工具以本檔為準（源自 `src/internal/cli/root.go` 等）。遇衝突以本檔為權威，不沿用漂移文件。

## 正確性紅線（務必遵守）

- 真實 CLI verb：`0ops apps get`（**非** `apps show`）、`0ops deploys redeploy`（**非** `0ops redeploy`）、`0ops domains list`（**無** `apps add-domain`）。
- 規劃中能力須標狀態，不得說成已具備：SOC2/DPA、部分 audit export、SKILL packs、MCP tool 權限 invocation-time enforcement（spec 標 TODO）。
- 招牌保證是後端強制的 preview/confirm，agent 繞不過。

## 安裝與上手

一行安裝（裝 binary + device flow 登入 + 自動接 AI CLI）：
```sh
OPS_HOST=https://api.<your-0ops> \
  curl -fsSL https://raw.githubusercontent.com/wusung/0ops/main/scripts/install.sh | sh
```
- device flow：印 `user_code` + 驗證 URL，去瀏覽器授權。
- 驗證：`0ops --version`、`0ops auth status`、`0ops teams list`。
- 變體：`NO_ONBOARD=1`（只裝 binary）、`DRY_RUN=1`（乾跑不下載）、`OPS_VERSION=v0.1.1`、`INSTALL_DIR=$HOME/bin`、`OPS_HOST=http://127.0.0.1:18080`（本機 compose）。
- token 存 `~/.config/0ops/auth.json`，子指令自動附帶。

手動登入：`0ops auth login --host=https://api.<host>` → `0ops auth status`。

接 AI CLI：
```sh
0ops mcp setup claude-code     # → ~/.claude.json mcpServers."0ops"
0ops mcp setup codex           # → ~/.codex/config.toml [mcp_servers.0ops]
0ops mcp setup copilot-cli     # 只印手動步驟（不自動寫檔）
0ops mcp setup claude-code --print-only
```
接完務必**重啟 AI CLI**。`0ops onboard <host>` 旗標：`--skip-login`、`--skip-mcp`、`--hosts`、`--mcp-binary`、`--yes`(預設 true)。

## CLI 指令表（root=`0ops`）

全域旗標（多數群組）：`--team`、`--host`、`--token`、`--output table|json|yaml`（預設 table，`OPS_OUTPUT`）。

**apps**
- `list`（`--page-size` 50、`--cursor`、`--all`）→ slug/name/repo_url/status
- `get <slug>` → id/team_id/slug/name/repo_url/repo_default_branch/image_ref/builder/status/時間戳
- `create` → 旗標 `--slug`(必)、`--source`(本機路徑 / `upload://<id>` / github URL)、`--repo-url`(dev `file://` only)、`--ref`(預設 main)、`--builder`、`--yes`(跳確認)、`--dry-run`(只 preview)、`--upload-max-bytes`(預設 100 MiB)、`--upload-max-entries`(預設 10000)。本機 source 自動打包上傳（尊重 `.dockerignore`/`git ls-files`）。輸出 app_id/app_slug/deploy_run_id/trace_id/subdomain_url/initial_deploy
- `delete <slug>` → 不可逆；印 side-effects；要打 app slug；高風險再打 `required_phrase`；最後 `[y/N]`。`--yes` 只跳最後 y/N

**repo**：`inspect <app-slug>` → app_slug/repo_url/repo_default_branch/builder

**deploys**
- `status <app-slug>` → deploy_id/status/commit_sha/ref/error_summary/started_at/finished_at
- `logs <app-slug>` → `--limit` 100、`--follow`(SSE)
- `redeploy <app-slug>` → `--ref`、`--commit-sha`、`--yes`、`--dry-run`；輸出 deploy_run_id/trace_id/commit_sha/ref/source/subdomain_url

**domains**：`list <app-slug>` → hostname/kind/verified/verified_at（**只有 list**，新增/驗證是 API/spec 面）

**teams**：`list`(team_slug/team_name/role/plan)、`use <slug>`(設預設團隊)
- `teams github install`（`--team`、`--yes`、`--status`、`--poll-interval`；preview→confirm→開瀏覽器→輪詢）、`uninstall`（暫停該團隊所有 app）、`status`

**members**：`list`(user_id/github_login/email/role)、`preview-invite`(`--role` 預設 member、`--github-login`、`--email`)、`invite --preview-id`、`preview-remove --user-id`、`remove --preview-id`

**admin**：`bootstrap-owner`(`--team-slug`/`--team-name`/`--github-login` 必、`--email`)、`retry-delete --team-slug --app-slug`（復原卡 deleting 的 app）

**auth**：`login`(`--github-login`/`--email`)、`logout`、`status`、`grant <tool>`/`revoke <tool>`（MCP 工具授權）、`tokens list`、`tokens create`(`--name`/`--scopes`/`--expires` 預設 90d)、`tokens revoke <name> --yes`

**audit**：`list`(`--since`/`--until`/`--action`/`--actor`(含 me)/`--trace`/`--page-size` 最大 200/`--cursor`/`--all`)、`get <id>`。另有 audit export/verify 機制

**incidents**：`list`(`--status` open|closed|all/`--kind`/`--severity`)、`get <id>`、`close <id> --note`（寫 audit_log）

**sso**：`status`（owner/admin 看 team OIDC 設定）、`deprovision --user <email|id> --yes`（owner-only 集中撤權：membership + 所有 token）

## MCP 工具（共 24，binary `0ops-mcp` 走 stdio）

啟動時 lint 檢查每個工具描述，失敗 exit 2。寫入工具兩階段（`*_preview` → 帶 `preview_id` confirm，confirm 對 preview_id 冪等，後端拒絕過期/未核准 id）。

唯讀（10）：`list_teams`、`list_apps`(需 team_slug)、`get_app`、`inspect_repo`、`get_deploy_status`、`tail_logs`(limit)、`list_domains`、`list_members`、`list_incidents`(filters；唯讀，close 只在 CLI)、`query_audit_log`(全團隊需 admin；viewer 只 actor=me，脫敏)

兩階段寫入（7 對，preview 回 `action_summary`+完整 `side_effects`+`expires_at`）：
- `create_app_preview`(team_slug/slug/source 或 repo_url、ref、builder) → `create_app`（回 app_id/deploy_run_id/subdomain_url）
- `redeploy_preview`(app_slug、opt ref/commit_sha) → `redeploy`
- `delete_app_preview`(需 confirm) → `delete_app`（CRITICAL 風險：必傳 `confirmation_phrase` = preview 的 `required_phrase`，如 `DELETE <slug>`，否則回 `confirmation_phrase_mismatch`；預設清 PV）
- `invite_member_preview`(role、opt github_login/email) → `invite_member`
- `remove_member_preview`(user_id) → `remove_member`
- `install_github_app_preview` → `install_github_app`（回 `install_url` 讓使用者開瀏覽器完成 OAuth）
- `uninstall_github_app_preview` → `uninstall_github_app`（server 端刪安裝並暫停該團隊所有 app）

AI agent 串一次部署：`list_teams` →（`inspect_repo`）→ `create_app_preview`（把 action_summary + 完整 side_effects 給使用者、取得明確同意）→ `create_app`(帶 preview_id) → 輪詢 `get_deploy_status`/`tail_logs` 到 `live`。預設 URL `<slug>.jesontech.com`。

## 端到端 happy path

1. 一行 curl 安裝 + 登入 + 接 AI CLI，重啟 AI CLI。
2. 裝 GitHub App：`0ops teams github install`（preview→confirm→開 `install_url` 授權→輪詢完成）。
3. 部署：AI 自然語言（→ create_app_preview→confirm→create_app）或 `0ops apps create --slug nextdemo --source <github-url|本機路徑>`。
4. 驗證：`0ops apps get nextdemo`、`0ops deploys status nextdemo`、`curl https://nextdemo.<domain>/`。
5. 自訂網域（pro）：DNS 加 CNAME + `_0ops-verify.<host>` TXT；後端每 30s 輪詢（雙條件）、24h token TTL（可 `--extend` 至 2×）；apex 需 ALIAS/ANAME/CNAME-flattening；`0ops domains list nextdemo`。
6. 邀夥伴：`0ops members preview-invite --role member --github-login <login>` → `0ops members invite --preview-id <id>`。
7. Log：`0ops deploys logs nextdemo --follow`。
8. Redeploy：push 到 app 的 `repo_url + ref` 由 webhook（actor `system:github_webhook`）自動觸發；或 `0ops deploys redeploy nextdemo`。

## 實務情境

- AI CLI 整合：`0ops mcp setup`/`onboard` 接線後說自然語言，agent 呼叫 MCP 工具。Claude Code→`~/.claude.json`、Codex→`~/.codex/config.toml`、Copilot 目前手動。
- SKILL packs：roadmap 內容（`skills/*/SKILL.md` 佈局，DevRel 負責，待可重現案例後釋出），**尚未** CLI/MCP 化，屬規劃中。
- 團隊/RBAC：角色見 `0ops teams list` 與 `0ops members list`。owner/admin gate：`query_audit_log` 全團隊需 admin；`0ops sso status`/`deprovision` owner/admin。成員管理走 preview/confirm。
- MCP tool 權限 deny-by-default：兩層（GitHub OAuth scope + per-user tool grant）。未授權回 `tool_not_permitted`。登入互動選單或 `--grants=...` 授權，或 `0ops auth grant/revoke <tool>`。（spec 標 token claim 編碼 / invocation-time enforcement 為 TODO。）
- preview/confirm UX：
  - create/redeploy（CLI）：計畫摘要後 `[y/N]`；`--yes` 跳過；`--dry-run` 只 preview。
  - delete（CLI）：side-effects 警告 → 「Type the app slug to confirm:」（須完全相符）→ 高風險再「Type "<required_phrase>" to confirm」（如 `DELETE nextdemo`）→ 最後 `[y/N]`；`--yes` 只跳最後 y/N。
  - 經 AI（MCP）：agent 須展示 action_summary + 完整 side_effects 取得同意才呼叫 confirm；delete 另須傳正確 `required_phrase` 為 `confirmation_phrase`。

## 進階 / self-host（`./manage.sh <cmd>`）

- `prod-bootstrap-all`：一鍵 setup-oauth→verify-oauth→up→smoke→install-runner→runner-validate→e2e production。冪等、失敗即停；`--resume-from=N`、`--skip-runner`、`--skip-e2e`。
- `prod-up`：裝 K3s + ArgoCD + sealed-secrets + apply root app + smoke。
- `prod-down`：移除 ArgoCD root app + system-0ops / cloudflare-tunnel namespace（Postgres ns/PVC 保留）。
- `prod-verify`：等所有 ArgoCD app Synced+Healthy + smoke。
- `prod-smoke`：HTTP-200 smoke（api / demo host）。
- `prod-setup-oauth`：互動註冊 GitHub OAuth App，寫 `.env.prod`。
- `prod-verify-oauth`：驗 `GITHUB_OAUTH_CLIENT_ID` + Device Flow 啟用。
- `prod-install-runner`/`prod-runner-status`/`prod-runner-validate`：self-hosted GHA runner。
- `pitr-drill`：Postgres PITR 演練。

self-host 上手（`deploy/bootstrap/README.md`）：前置由使用者備妥（K3s host、Cloudflare zone `jesontech.com`、`*.jesontech.com` CNAME→tunnel、Cloudflare Tunnel token、GitHub OAuth App、kubeseal、可匿名拉取的 ghcr image）。
```bash
cp deploy/bootstrap/env.example deploy/bootstrap/.env.prod
$EDITOR deploy/bootstrap/.env.prod        # CF token / 網域 / image tag / pg 密碼
./manage.sh prod-bootstrap-all            # 或分步 prod-setup-oauth → prod-verify-oauth → prod-up → prod-smoke
```
分步 Option B 另做 `gh variable set GHA_RUNNER_LABEL --repo wusung/0ops --body 0ops-builder` 與 `E2E_MODE=production OPS_HOST=https://api.<domain> ./manage.sh e2e-create-app`。ops-server 首裝可 `helm upgrade --install ops-server deploy/server -n system-0ops --set image.tag=<tag>`。解除安裝 `./manage.sh prod-down`。

SSO 登入：per-team OIDC；owner/admin `0ops sso status`；owner `0ops sso deprovision --user <email|id>`（狀態：OIDC 已有 compose e2e PR #141；SAML 見 spec；SOC2/DPA 規劃中）。

audit/incident（operator）：`0ops audit list --since 24h --action create_app`、`0ops audit get <id>`；`0ops incidents list --status open`、`get <id>`、`close <id> --note "root cause"`。audit export/verify 為 tamper-evidence（M9.1/M9.6 已落地）。

生產 OAuth（`docs/runbooks/production-oauth-setup.md`）：`./manage.sh prod-setup-oauth`（開預填 GitHub URL、輸入 Client ID/Secret、寫 3 行到 `.env.prod`）或手動 `github.com/settings/applications/new`，callback `https://<PROD_API_HOST>/v1/auth/oauth2/callback`，**Enable Device Flow 要勾**。換 secret 後：`seal-secrets.sh`→`kubectl apply -f deploy/bootstrap/tmp/sealed/`→`kubectl -n system-0ops rollout restart deploy/ops-server`→`prod-verify-oauth`。

## 排錯（使用者自救）

- 上手失敗（quickstart §5）：`asset not found`→手動下載 `github.com/wusung/0ops/releases`；`INSTALL_DIR not in PATH`→加印出的 shell-rc 行；device-flow timeout→重跑（公司 proxy 擋 GitHub OAuth 也會失敗）；AI 工具回 `unauthorized`→重跑 `0ops auth login` 並**重啟 AI CLI**；MCP 看不到→查 `docs/features/end-user-onboarding/mcp-hosts/<host>.md`。
- create_app 卡 building/syncing（`create-app-stuck.md`）：reconciler ~30s 內自動收斂（building 30 分、syncing 15 分逾時）——先等一個週期；`0ops apps get <slug>` 應收斂 live/failed。升級：`gh run rerun <id> --failed`、`0ops deploys redeploy <slug>`、最後 delete+recreate。
- 卡 deleting（`delete-app-residue.md`）：`0ops admin retry-delete --team-slug <team> --app-slug <app>`（冪等可重跑），`0ops apps list` 確認消失。更深層（PVC/namespace/ingress finalizer、ArgoCD 還держ）需 kubectl/argocd 清理後再 retry。
- URL 4xx/5xx/連不上（`winshare-route-failure.md`）：分層查 Cloudflare zone（wildcard CNAME + orange-cloud）→ cloudflared tunnel（≥2 ready）→ K3s traefik ingress → app pod `/health` → ArgoCD sync；介入 `kubectl -n cloudflare-tunnel rollout restart deploy/cloudflared`。
- 生產 OAuth 卡 pending（`production-oauth-setup.md` §5）：99% callback URL 錯→修為 `https://<host>/v1/auth/oauth2/callback` 再 `prod-up`；`unauthorized_client`→OAuth App 沒勾 Device Flow。
