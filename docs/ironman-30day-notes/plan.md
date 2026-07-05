# 0ops 30 天實戰系列 — 逐日寫作規畫

**版本**：v2.0（使用者視角）
**日期**：2026-07-05
**來源**：`spec.md`（系列定位與約束）
**用途**：30 篇連載的可執行藍圖。每篇列標題、你會學到的操作、動手內容（真實指令/工具）、對映來源、產出形式。撰寫時逐日展開為 `drafts/day-NN.md`。

**正確性提醒**：全系列指令以 `src/internal/cli/root.go` 為準。教 `0ops apps get`／`0ops deploys redeploy`／`0ops domains list`，不教漂移的 `apps show`／`redeploy`／`apps add-domain`。

---

## 介紹 Introduction（Day 1–4）

### Day 1 — 你的 AI agent 會寫 code，卻不會出貨
- **你會學到**：0ops 解決什麼問題——AI CLI 能寫能改能測，就是不能自己把成果部署上線；0ops 補上這最後一哩的 `ship`。
- **動手內容**：先不裝，用一段對話情境帶出痛點（「Claude Code 幫我寫完了，然後呢？」）。
- **對映來源**：`README.md` 定位、`docs/0ops-business-plan.md` §二 四痛點（使用者面）。
- **產出形式**：痛點敘事 + 「寫完 → 出貨」斷層示意圖。

### Day 2 — 30 秒 demo：一句話讓 AI 把 repo 部署上線
- **你會學到**：0ops 的招牌體驗——在 Claude Code 說「把這個 repo 部署到 0ops」，agent 自動走 preview→confirm→上線。
- **動手內容**：展示自然語言 → `create_app_preview` → 你確認 → `create_app` → 拿到 `<slug>.jesontech.com` 的完整對話截圖流程。
- **對映來源**：`docs/quickstart.md` §4、MCP 工具鏈（`server.go` registerTools）。
- **產出形式**：一次真實 AI 部署對話的逐格拆解。

### Day 3 — 誰該用 0ops、什麼情況最適合
- **你會學到**：對號入座——重度 AI CLI 使用者、想少寫 YAML、需要 self-host 或台幣計費的台灣團隊。以及什麼情況「不」需要它。
- **動手內容**：使用情境對照（個人 side project / 小團隊 / enterprise self-host）。
- **對映來源**：`docs/0ops-business-plan.md` §一差異化、§七方案分層。
- **產出形式**：適用情境決策清單。

### Day 4 — agent 出貨也有安全網：preview/confirm 是什麼
- **你會學到**：為什麼你可以放心讓 AI 執行部署/刪除——每個寫入操作都先 preview 給你看 `side_effects`，你確認才執行。
- **動手內容**：展示一次 create 的 `[y/N]`、一次 delete 要打的 `DELETE <slug>` phrase。
- **對映來源**：`docs/features/preview-confirm-gate/spec.md`（使用者體驗面）、CLI delete 流程。
- **產出形式**：使用者會看到/打什麼的實際畫面。

---

## 解決方案 Solution（Day 5–9）

### Day 5 — 0ops vs Vercel / Railway / 自建 K8s：你該選哪個
- **你會學到**：從使用者需求出發的選型——什麼時候 0ops 勝出、什麼時候別的更適合。
- **動手內容**：需求 → 方案對照表（AI 原生、self-host、計費、學習曲線）。
- **對映來源**：`docs/0ops-business-plan.md` §五競品表（使用者決策面）。
- **產出形式**：選型決策表。

### Day 6 — 兩種用法：人類用 CLI，AI 用 MCP，共用一個後端
- **你會學到**：0ops 的雙入口——`0ops` CLI 給你手動操作，`0ops-mcp` 給 AI agent 呼叫，背後同一套 API 與權限。
- **動手內容**：同一件事（列出 apps）兩種做法對照：`0ops apps list` vs agent 呼叫 `list_apps`。
- **對映來源**：`src/internal/cli/root.go`、`src/internal/mcp/server/server.go`。
- **產出形式**：雙路徑對照圖。

### Day 7 — 三分鐘安裝上手：一條 curl 全搞定
- **你會學到**：裝 binary + 登入 + 自動接上 AI CLI，一行指令完成。
- **動手內容**：
  ```sh
  OPS_HOST=https://api.<your-0ops> \
    curl -fsSL https://raw.githubusercontent.com/wusung/0ops/main/scripts/install.sh | sh
  0ops --version
  0ops auth status
  ```
  說明 device flow 會印 `user_code` + 驗證 URL；`NO_ONBOARD=1`/`DRY_RUN=1`/`OPS_VERSION` 變體。
- **對映來源**：`README.md`、`docs/quickstart.md` §1、`scripts/install.sh`、`src/internal/cli/onboard.go`。
- **產出形式**：安裝逐步 + device flow 畫面。

### Day 8 — 把 0ops 接到 Claude Code / Codex / Copilot
- **你會學到**：手動接線與設定檔落點，接完要重啟 AI CLI。
- **動手內容**：
  ```sh
  0ops mcp setup claude-code      # → ~/.claude.json mcpServers."0ops"
  0ops mcp setup codex            # → ~/.codex/config.toml
  0ops mcp setup copilot-cli      # 只印手動步驟
  0ops mcp setup claude-code --print-only
  ```
- **對映來源**：`src/internal/cli/mcpsetup.go`、`docs/features/end-user-onboarding/mcp-hosts/*.md`。
- **產出形式**：三家 AI CLI 接線對照。

### Day 9 — 授權與工具權限：deny-by-default，只開你要的
- **你會學到**：MCP 工具預設拒絕，未授權工具回 `tool_not_permitted`；怎麼授權/收回。
- **動手內容**：`0ops auth grant <tool>` / `0ops auth revoke <tool>`；登入時的互動授權選單。標明 spec 中 invocation-time enforcement 部分仍 TODO。
- **對映來源**：`docs/features/mcp-tool-permissions/spec.md`、`src/internal/cli/auth.go`。
- **產出形式**：授權操作 + 權限模型（兩層：OAuth scope + per-tool grant）。

---

## 操作 Operation（Day 10–18）

### Day 10 — 部署第一個 app：從 GitHub repo 到上線
- **你會學到**：裝 GitHub App → create → 上線的最短路徑。
- **動手內容**：
  ```sh
  0ops teams github install        # preview→confirm→開瀏覽器授權→輪詢
  0ops apps create --slug nextdemo --source <github-url>
  0ops apps get nextdemo
  ```
- **對映來源**：`docs/features/{github-app-install-flow,create-app-flow}/spec.md`、`root.go`。
- **產出形式**：zero→live happy path。

### Day 11 — 從本機資料夾部署：不用 GitHub 也能上
- **你會學到**：local source 自動打包上傳（尊重 `.dockerignore`/`git ls-files`），適合私有或未推 GitHub 的專案。
- **動手內容**：`0ops apps create --slug demo --source ./my-app`；`--upload-max-bytes`（預設 100 MiB）、`--upload-max-entries`、`--ref`、`--builder`。
- **對映來源**：`root.go` apps create、`docs/features/app-source-ingestion/spec.md`（操作面）。
- **產出形式**：local 部署步驟 + 打包規則。

### Day 12 — 用自然語言讓 AI agent 部署（MCP 全流程）
- **你會學到**：agent 怎麼串工具——`list_teams` →（`inspect_repo`）→ `create_app_preview` → 你審 `side_effects` → `create_app` → 輪詢 `get_deploy_status`。
- **動手內容**：一段真實 prompt 與 agent 的工具呼叫序列；你在中間點頭的那一步。
- **對映來源**：`server.go` registerTools、`docs/quickstart.md` §4。
- **產出形式**：MCP 工具鏈序列圖。

### Day 13 — 查部署狀態與看 log
- **你會學到**：部署到哪了、失敗看哪、即時追 log。
- **動手內容**：`0ops deploys status nextdemo`（deploy_id/status/commit_sha/error_summary）；`0ops deploys logs nextdemo --follow`（SSE）；MCP 對應 `get_deploy_status`/`tail_logs`。
- **對映來源**：`root.go` deploys、MCP 讀工具。
- **產出形式**：狀態欄位解讀 + follow log 示範。

### Day 14 — 重新部署與 push-to-deploy 自動化
- **你會學到**：手動 redeploy 與 GitHub push 自動觸發。
- **動手內容**：`0ops deploys redeploy nextdemo --ref main`（或 `--commit-sha`）；說明 push 到 app 的 `repo_url + ref` 由 webhook（actor `system:github_webhook`）自動 redeploy。
- **對映來源**：`root.go` deploys redeploy、`docs/features/webhook-and-redeploy/spec.md`。
- **產出形式**：手動 vs 自動 redeploy 對照。

### Day 15 — 綁自訂網域
- **你會學到**：DNS 設定與驗證機制（雙條件、24h token、apex 注意）。
- **動手內容**：在 DNS 加 CNAME + `_0ops-verify.<host>` TXT；backend 每 30s 輪詢；`0ops domains list nextdemo` 看 verified 狀態。**明確標示**：新增/驗證目前是 API/spec 面，CLI 只有 `domains list`。
- **對映來源**：`docs/features/custom-domain-and-verify/spec.md`、`root.go` domains list。
- **產出形式**：DNS 記錄範例 + 驗證流程。

### Day 16 — 團隊協作：邀請成員與角色
- **你會學到**：preview/confirm 邀請、角色分級。
- **動手內容**：`0ops teams list`（team/role/plan）；`0ops members preview-invite --role member --github-login <login>` → `0ops members invite --preview-id <id>`；`0ops teams use <slug>` 切預設團隊。
- **對映來源**：`src/internal/cli/members.go`、`root.go` teams。
- **產出形式**：邀請流程 + 角色表。

### Day 17 — 管理 app 生命週期：列出、查詳情、刪除
- **你會學到**：日常管理與安全刪除。
- **動手內容**：`0ops apps list --all`；`0ops apps get <slug>`；`0ops apps delete <slug>`——會要你打 app slug、若高風險再打 `required_phrase`（如 `DELETE nextdemo`）、最後 `[y/N]`；`--yes` 只跳過最後那步。
- **對映來源**：`root.go` apps、`docs/features/delete-app-flow/spec.md`。
- **產出形式**：刪除的三道確認實際畫面。

### Day 18 — token 與 CI：非互動式部署
- **你會學到**：建 API token 在 CI/腳本裡跑 0ops。
- **動手內容**：`0ops auth tokens create --name ci --scopes <...> --expires 90d`；`list`/`revoke`；用 `--host`/`--token`/`OPS_OUTPUT=json` 在腳本裡呼叫。
- **對映來源**：`src/internal/cli/auth.go` tokens。
- **產出形式**：CI 部署腳本範例。

---

## 實務 Practical（Day 19–25）

### Day 19 — 真實情境：讓 Claude Code 從零把 Next.js 部署上線
- **你會學到**：一個完整的 AI 端到端旅程串起前面所有操作。
- **動手內容**：從對話開專案 → agent 建 app → 綁網域 → 邀夥伴 → 看 log 的完整 walkthrough。
- **對映來源**：綜合 create-app-flow、quickstart §4、MCP 工具鏈。
- **產出形式**：完整案例（含真實對話與指令）。

### Day 20 — preview/confirm 實戰：安全地放手讓 agent 寫入
- **你會學到**：怎麼審 `action_summary` + 完整 `side_effects` 再點頭；delete 為何要傳 `confirmation_phrase`。
- **動手內容**：對照「該批准」與「該擋下」的兩個 side_effects 範例；`confirmation_phrase_mismatch` 會發生什麼。
- **對映來源**：`docs/features/preview-confirm-gate/spec.md`、`server.go` write 工具。
- **產出形式**：審閱 checklist。

### Day 21 — GitHub App 與 push-to-deploy 全自動化
- **你會學到**：裝 GitHub App 的完整流程與自動部署上線。
- **動手內容**：`0ops teams github install`（開 `install_url` 授權→輪詢）；驗證 push 觸發 redeploy；`0ops teams github status`/`uninstall`（uninstall 會暫停該團隊所有 app）。
- **對映來源**：`docs/features/github-app-install-flow/spec.md`、`src/internal/cli/github.go`。
- **產出形式**：安裝與自動部署驗證。

### Day 22 — 團隊權限實務與稽核：誰能做什麼、誰做了什麼
- **你會學到**：owner/admin/member/viewer 的能力邊界、怎麼查稽核。
- **動手內容**：`0ops audit list --since 24h --action create_app --actor me`；`0ops audit get <id>`；權限差異（`query_audit_log` 全團隊需 admin，viewer 只看 `actor=me`）。
- **對映來源**：`src/internal/cli/audit.go`、`docs/features/mcp-tool-permissions/spec.md`。
- **產出形式**：角色能力矩陣 + 稽核查詢範例。

### Day 23 — 排錯篇一：app 卡在 building / syncing
- **你會學到**：先等 reconciler 自動收斂，再逐級升級處置。
- **動手內容**：`0ops apps get <slug>` 應收斂為 `live`/`failed`（building 30 分、syncing 15 分逾時後 ~30s 內收斂）；升級：`gh run rerun <id> --failed`、`0ops deploys redeploy <slug>`、最後 delete+recreate。
- **對映來源**：`docs/runbooks/create-app-stuck.md`（教正確 verb，非 runbook 舊寫法）。
- **產出形式**：排錯決策樹。

### Day 24 — 排錯篇二：卡在 deleting、網址打不開
- **你會學到**：deleting 卡住的標準復原、URL 4xx/5xx 的分層排查。
- **動手內容**：`0ops admin retry-delete --team-slug <team> --app-slug <app>`（冪等可重跑），`0ops apps list` 確認消失；URL 故障從 Cloudflare→tunnel→traefik→pod `/health`→ArgoCD 分層查。
- **對映來源**：`docs/runbooks/{delete-app-residue,winshare-route-failure}.md`。
- **產出形式**：兩條排錯路徑。

### Day 25 — 上手陷阱與 FAQ
- **你會學到**：安裝/登入/MCP 看不到 的最常見坑。
- **動手內容**：`asset not found`→手動下載 release；PATH 沒加→貼 shell-rc 行；device flow timeout→重跑（注意公司 proxy）；AI 工具回 `unauthorized`→重跑 `0ops auth login` **並重啟 AI CLI**；MCP 看不到→查 mcp-hosts 文件。
- **對映來源**：`docs/quickstart.md` §5 疑難表。
- **產出形式**：FAQ 速查表。

---

## 進階 Advanced（Day 26–30）

### Day 26 — self-host 你自己的 0ops：一鍵裝
- **你會學到**：用 `manage.sh` 把整套裝到自己的 K3s。
- **動手內容**：前置（K3s host、Cloudflare zone + tunnel token、GitHub OAuth App、kubeseal）；
  ```bash
  cp deploy/bootstrap/env.example deploy/bootstrap/.env.prod
  $EDITOR deploy/bootstrap/.env.prod
  ./manage.sh prod-bootstrap-all     # 或分步 prod-setup-oauth→prod-up→prod-smoke
  ```
- **對映來源**：`deploy/bootstrap/README.md`、`manage.sh`。
- **產出形式**：self-host 上手步驟 + 前置清單。

### Day 27 — 生產 OAuth 與網域設定
- **你會學到**：設 GitHub OAuth（含 Device Flow 勾選）與 Cloudflare tunnel 網域。
- **動手內容**：`./manage.sh prod-setup-oauth`（互動寫 `.env.prod`）→ `prod-verify-oauth`；callback 必為 `https://<host>/v1/auth/oauth2/callback`；換 secret 後 `seal-secrets.sh`→apply→`rollout restart`。
- **對映來源**：`docs/runbooks/production-oauth-setup.md`。
- **產出形式**：OAuth 設定逐步 + 常見錯（callback 錯、Device Flow 沒勾）。

### Day 28 — 企業級：SSO / OIDC 登入與集中撤權
- **你會學到**：per-team OIDC 登入、owner 一鍵撤掉某人所有存取。
- **動手內容**：`0ops sso status`（owner/admin 看設定）；`0ops sso deprovision --user <email|id> --yes`（撤 membership + 所有 token）。**標狀態**：SSO/OIDC 已有 e2e（PR #141），SAML 細節見 spec，SOC2/DPA 仍規劃中。
- **對映來源**：`src/internal/cli/sso.go`、`docs/features/sso-saml/spec.md`。
- **產出形式**：SSO 使用者/管理者流程。

### Day 29 — 稽核與合規：查詢、匯出、驗證、incident
- **你會學到**：稽核紀錄的操作面與事故管理。
- **動手內容**：`0ops audit list/get`；audit export/verify（tamper-evidence，標已落地 M9.1/M9.6）；`0ops incidents list --status open`、`get`、`close <id> --note`。標示 MCP `query_audit_log`/`list_incidents` 為唯讀，close 只在 CLI。
- **對映來源**：`src/internal/cli/{audit,incidents}.go`、`docs/features/audit-export-and-integrity/spec.md`。
- **產出形式**：合規操作清單 + 已落地/規劃中標註。

### Day 30 — 進階運維與 30 天回顧
- **你會學到**：PITR drill 等運維動作，並回顧整個系列學到的操作地圖。
- **動手內容**：`./manage.sh pitr-drill`、`prod-verify`、`prod-runner-validate`；串起 Day 1–29 的使用者能力清單；誠實交代目前限制（production 路徑需自備外部資源、部分能力規劃中）。
- **對映來源**：`manage.sh`、`tasks/todo.md` 未竟項。
- **產出形式**：學習地圖總結 + 你現在會做的 20 件事清單。

---

## 撰寫執行建議

- **節奏**：先出介紹＋解決方案（Day 1–9）建立聲量與可上手基礎，再逐日推操作/實務/進階。
- **每篇必含可執行內容**：至少一段能複製的指令或一次真實工具呼叫；避免只講概念。
- **草稿落點**：`docs/ironman-30day-notes/drafts/day-NN.md`。
- **正確性把關**：撰稿時對照 `src/internal/cli/root.go` 確認 verb；遇 spec 標 planned/placeholder 的能力，文中標狀態。
- **不做**：本規畫不含發佈與渠道分發決策（founder go/no-go）。
