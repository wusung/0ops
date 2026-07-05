# 0ops 30 天實戰系列 — 30 篇大綱

**版本**：v1.0
**日期**：2026-07-05
**來源**：`spec.md`、`plan.md`
**用途**：每篇文章的內容骨架，可直接據以撰稿為 `drafts/day-NN.md`。每篇沿用五段格式（你想做什麼 → 動手做 → 你會看到什麼 → 踩坑與要點 → 明日預告）。指令以 `src/internal/cli/root.go` 為準。

---

## 介紹 Introduction（Day 1–4）

### Day 1 — 你的 AI agent 會寫 code，卻不會出貨
- 開場情境：Claude Code 幫你寫完、測完一個服務，然後你卡在「怎麼上線」。
- 拆解「最後一哩」：build、容器化、K8s manifest、網域、TLS——agent 的工具帶裡沒有 `ship` 這隻手。
- 0ops 一句話定位：讓 AI coding agent 出貨時原生呼叫的那隻手。
- 本系列會帶你做到什麼：30 天後你能用一句話或一行指令把專案部署上線。
- **原則**：好工具補的是工作流的斷點，不是再加一個功能。

### Day 2 — 30 秒 demo：一句話讓 AI 把 repo 部署上線
- 展示招牌體驗：在 Claude Code 說「把這個 repo 部署到 0ops，叫 nextdemo」。
- agent 背後做了什麼：`create_app_preview` → 把 `side_effects` 攤給你看 → 你點頭 → `create_app`。
- 結果：拿到 `nextdemo.jesontech.com`，`get_deploy_status` 轉 `live`。
- 對比傳統流程要手動做的十幾步。
- **原則**：好的抽象讓「意圖」直接變「結果」，中間的儀式該被工具吸收。

### Day 3 — 誰該用 0ops、什麼情況最適合
- 三種對號入座：個人 side project 想快速上線、小團隊想少維護 K8s、enterprise 要 self-host + 稽核。
- 台灣情境：台幣計費、法規、資料落地需求。
- 什麼情況「不」需要 0ops：已深度綁定某雲、無 AI CLI 使用習慣、純靜態站。
- 一張適用情境決策清單。
- **原則**：選型先問「我的約束是什麼」，不是「誰功能多」。

### Day 4 — agent 出貨也有安全網：preview/confirm 是什麼
- 為什麼你敢讓 AI 執行部署/刪除：每個寫入操作先 preview，把 `action_summary` + 完整 `side_effects` 給你看。
- CLI 體驗：create 的 `[y/N]`；delete 要打 app slug、再打 `required_phrase`（如 `DELETE nextdemo`）、最後 `[y/N]`。
- AI 體驗：agent 必須先展示 side_effects 取得你同意才呼叫 confirm 工具。
- 這道閘是 backend 強制的，agent 繞不過。
- **原則**：讓 AI 有權執行、但無權略過人的確認。

---

## 解決方案 Solution（Day 5–9）

### Day 5 — 0ops vs Vercel / Railway / 自建 K8s：你該選哪個
- 從需求出發的四維對照：AI 原生接入、self-host 能力、計費在地化、學習曲線。
- Vercel/Railway 勝場、自建 K8s 勝場、0ops 勝場各是什麼。
- 決策表：依你的約束勾選 → 指向建議方案。
- 誠實話：0ops 目前的邊界（production 路徑需自備外部資源）。
- **原則**：對照要用你的使用情境當軸，不是用功能清單當軸。

### Day 6 — 兩種用法：人類用 CLI，AI 用 MCP，共用一個後端
- 雙入口模型：`0ops` CLI（你手動）、`0ops-mcp`（AI agent 呼叫），同一 API + 同一權限。
- 同一件事兩種做法：`0ops apps list` vs agent 呼叫 `list_apps`。
- 什麼時候用哪個：探索/腳本用 CLI，對話式開發用 MCP。
- 權限一致：兩條路都走同一套 RBAC 與 preview/confirm。
- **原則**：一套後端、多種入口，授權與保證不因入口而異。

### Day 7 — 三分鐘安裝上手：一條 curl 全搞定
- 一行安裝：`OPS_HOST=... curl -fsSL .../scripts/install.sh | sh`——裝 binary + device flow 登入 + 自動接 AI CLI。
- device flow 你會看到：`user_code` + 驗證 URL，去瀏覽器授權。
- 驗證：`0ops --version`、`0ops auth status`、`0ops teams list`。
- 變體：`NO_ONBOARD=1`（只裝）、`DRY_RUN=1`（乾跑）、`OPS_VERSION` / `INSTALL_DIR`。
- **原則**：上手第一公里的摩擦，決定產品有沒有第二次機會。

### Day 8 — 把 0ops 接到 Claude Code / Codex / Copilot
- 手動接線：`0ops mcp setup claude-code` / `codex` / `copilot-cli`。
- 設定檔落點：`~/.claude.json`、`~/.codex/config.toml`；Copilot 只印手動步驟。
- `--print-only` 先預覽不寫檔；接完**務必重啟 AI CLI**。
- 驗證 agent 看得到 0ops 工具（`list_teams` 能回）。
- **原則**：整合要冪等、可預覽、可回復——不破壞使用者既有設定。

### Day 9 — 授權與工具權限：deny-by-default，只開你要的
- 兩層權限：GitHub OAuth scope + per-user MCP tool grant。
- 未授權工具回 `tool_not_permitted`；登入時可用互動選單授權。
- 事後調整：`0ops auth grant <tool>` / `0ops auth revoke <tool>`。
- 標狀態：spec 中 invocation-time enforcement / token claim 編碼部分仍 TODO。
- **原則**：給 agent 的能力預設關閉，逐項打開，最小權限。

---

## 操作 Operation（Day 10–18）

### Day 10 — 部署第一個 app：從 GitHub repo 到上線
- 先裝 GitHub App：`0ops teams github install`（preview→confirm→開瀏覽器授權→輪詢）。
- 建 app：`0ops apps create --slug nextdemo --source <github-url>`。
- 看結果：`0ops apps get nextdemo`（status、image_ref、subdomain_url）。
- happy path 心智圖：install → create → build → deploy → live。
- **原則**：先把一條最短路徑跑通，再談進階選項。

### Day 11 — 從本機資料夾部署：不用 GitHub 也能上
- `0ops apps create --slug demo --source ./my-app`——自動打包上傳。
- 打包規則：尊重 `.dockerignore` / `git ls-files`；`--upload-max-bytes`（100 MiB）、`--upload-max-entries`（10000）。
- 適用：私有專案、還沒推 GitHub、快速試。
- 其他旗標：`--ref`、`--builder`、`--dry-run`（只 preview）。
- **原則**：降低前置條件，讓「先跑起來」永遠可行。

### Day 12 — 用自然語言讓 AI agent 部署（MCP 全流程）
- agent 的工具鏈：`list_teams` →（`inspect_repo`）→ `create_app_preview` → 你審 → `create_app` → 輪詢 `get_deploy_status` / `tail_logs`。
- 你在流程中的角色：審 `side_effects`、點頭那一步。
- 一段真實 prompt 與對應工具呼叫序列拆解。
- 失敗時 agent 怎麼回報（`error_summary`）。
- **原則**：把人放在寫入操作的關鍵決策點，其餘交給 agent。

### Day 13 — 查部署狀態與看 log
- 狀態：`0ops deploys status nextdemo`（deploy_id / status / commit_sha / error_summary / 時間戳）。
- 即時 log：`0ops deploys logs nextdemo --follow`（SSE 串流），`--limit`。
- MCP 對應：`get_deploy_status`、`tail_logs`。
- 讀懂常見 status 轉移與失敗訊號。
- **原則**：可觀測的第一步是「知道現在到哪、卡在哪」。

### Day 14 — 重新部署與 push-to-deploy 自動化
- 手動：`0ops deploys redeploy nextdemo --ref main`（或 `--commit-sha`）。
- 自動：push 到 app 的 `repo_url + ref` 由 webhook（actor `system:github_webhook`）觸發 redeploy。
- 兩者差異與適用；redeploy 會起新 run、舊 run 被回收。
- 驗證自動部署：push 一個 commit 看 status 變化。
- **原則**：常態部署該自動化，手動 redeploy 留給例外。

### Day 15 — 綁自訂網域
- DNS 設定：加 CNAME + `_0ops-verify.<host>` TXT 記錄。
- 驗證機制：backend 每 30s 輪詢、雙條件、24h token TTL（可 `--extend` 至 2×）。
- apex 網域要 ALIAS/ANAME/CNAME-flattening。
- 查狀態：`0ops domains list nextdemo`（hostname / kind / verified）。**標示**：新增/驗證目前是 API/spec 面，CLI 只有 `domains list`。
- **原則**：網域歸屬要用不可偽造的驗證，別只靠設定即信任。

### Day 16 — 團隊協作：邀請成員與角色
- 看團隊：`0ops teams list`（team / role / plan）；`0ops teams use <slug>` 切預設。
- 邀請（preview/confirm）：`0ops members preview-invite --role member --github-login <login>` → `0ops members invite --preview-id <id>`。
- 角色分級與能力差異概覽（owner/admin/member/viewer）。
- 移除成員同樣走 preview→confirm。
- **原則**：成員變更是敏感操作，一律 preview 再 confirm。

### Day 17 — 管理 app 生命週期：列出、查詳情、刪除
- 列出：`0ops apps list --all`（`--page-size`、`--cursor`）。
- 查詳情：`0ops apps get <slug>`。
- 安全刪除：`0ops apps delete <slug>`——打 app slug → 高風險再打 `required_phrase`（`DELETE <slug>`）→ `[y/N]`；`--yes` 只跳最後一步。
- 刪除的 side-effects（PV 預設清除）。
- **原則**：不可逆操作要用「打得出全名」證明你知道自己在做什麼。

### Day 18 — token 與 CI：非互動式部署
- 建 token：`0ops auth tokens create --name ci --scopes <...> --expires 90d`。
- 管理：`tokens list` / `tokens revoke <name> --yes`。
- 在腳本/CI：用 `--host` / `--token` / `OPS_OUTPUT=json` 非互動呼叫。
- 範例：GitHub Actions 裡跑 `0ops deploys redeploy`。
- **原則**：自動化用短期、限範圍的 token，不要用個人登入態。

---

## 實務 Practical（Day 19–25）

### Day 19 — 真實情境：讓 Claude Code 從零把 Next.js 部署上線
- 完整旅程：對話開需求 → agent 建 app → 綁網域 → 邀夥伴 → 看 log。
- 串起前面所有操作的一個連貫案例（含真實對話與指令）。
- 每個決策點你做了什麼、agent 做了什麼。
- 上線後驗證：`curl https://nextdemo.<domain>/`。
- **原則**：端到端跑一次，勝過分開學十個指令。

### Day 20 — preview/confirm 實戰：安全地放手讓 agent 寫入
- 怎麼審：`action_summary` 看意圖、完整 `side_effects` 看代價。
- 兩個對照範例：一個「該批准」、一個「該擋下」的 side_effects。
- delete 特別：必須傳 `confirmation_phrase` = preview 的 `required_phrase`，否則 `confirmation_phrase_mismatch`。
- 一張審閱 checklist。
- **原則**：批准前先讀 side_effects，別把 confirm 當橡皮圖章。

### Day 21 — GitHub App 與 push-to-deploy 全自動化
- 安裝：`0ops teams github install`（開 `install_url` 授權 → CLI 輪詢完成）。
- 驗證自動部署：push 一個 commit → 自動 redeploy 上線。
- 管理：`0ops teams github status`；`uninstall`（會暫停該團隊**所有** app，謹慎）。
- 常見卡點與排查。
- **原則**：自動化上線前，先確認「關掉它」的後果你清楚。

### Day 22 — 團隊權限實務與稽核：誰能做什麼、誰做了什麼
- 角色能力矩陣：owner/admin/member/viewer 各能碰什麼。
- 稽核查詢：`0ops audit list --since 24h --action create_app --actor me`；`0ops audit get <id>`。
- 權限差異：`query_audit_log` 全團隊需 admin，viewer 只能 `actor=me`（且脫敏）。
- 用 trace-id 串一次操作的完整軌跡。
- **原則**：權限與稽核是一體兩面——能做什麼要對得上查得到誰做了。

### Day 23 — 排錯篇一：app 卡在 building / syncing
- 先別慌：reconciler 會自動收斂（building 30 分、syncing 15 分逾時後 ~30s 內收斂）。
- 先做：`0ops apps get <slug>` 應收斂為 `live` / `failed`。
- 逐級升級：`gh run rerun <id> --failed` → `0ops deploys redeploy <slug>` → 最後 delete + recreate。
- 一張排錯決策樹。
- **原則**：自癒系統先等一個收斂週期，再動手介入。

### Day 24 — 排錯篇二：卡在 deleting、網址打不開
- deleting 卡住：`0ops admin retry-delete --team-slug <team> --app-slug <app>`（冪等可重跑），`0ops apps list` 確認消失。
- 更深層卡住（PVC/namespace/ingress finalizer、ArgoCD 還держ）需 `kubectl`/`argocd` 清理後再 retry。
- URL 4xx/5xx/連不上：分層查 Cloudflare zone → cloudflared tunnel → traefik ingress → pod `/health` → ArgoCD sync。
- 介入手段：`kubectl -n cloudflare-tunnel rollout restart deploy/cloudflared`。
- **原則**：分層系統的故障要從外到內逐層定位，別跳層猜。

### Day 25 — 上手陷阱與 FAQ
- `asset not found` → 手動下載 release；PATH 沒加 → 貼 shell-rc 行。
- device flow timeout → 重跑（注意公司 proxy 擋 GitHub OAuth）。
- AI 工具回 `unauthorized` → 重跑 `0ops auth login` **並重啟 AI CLI**。
- MCP 看不到 → 查 `docs/features/end-user-onboarding/mcp-hosts/<host>.md`。
- **原則**：八成上手問題出在 PATH、授權、或沒重啟——先查這三個。

---

## 進階 Advanced（Day 26–30）

### Day 26 — self-host 你自己的 0ops：一鍵裝
- 前置清單：K3s host、Cloudflare zone + `*.<domain>` CNAME → tunnel、Cloudflare Tunnel token、GitHub OAuth App、kubeseal。
- 設定：`cp deploy/bootstrap/env.example .env.prod` → 編輯（CF token / 網域 / image tag / pg 密碼）。
- 一鍵：`./manage.sh prod-bootstrap-all`（或分步 `prod-setup-oauth → prod-up → prod-smoke`）。
- 冪等、失敗即停、`--resume-from` / `--skip-runner` / `--skip-e2e`。
- **原則**：self-host 的門檻在外部前置資源，不在裝的指令。

### Day 27 — 生產 OAuth 與網域設定
- OAuth：`./manage.sh prod-setup-oauth`（互動、寫 3 行到 `.env.prod`）→ `prod-verify-oauth`。
- callback 必為 `https://<host>/v1/auth/oauth2/callback`；**Device Flow 要勾**。
- 換 Client ID/Secret 後：`seal-secrets.sh` → `kubectl apply sealed/` → `rollout restart deploy/ops-server` → 再 verify。
- 常見錯：callback 錯（pending 卡住）、Device Flow 沒勾（`unauthorized_client`）。
- **原則**：OAuth 問題九成是 callback URL 或 Device Flow 開關，先查這兩個。

### Day 28 — 企業級：SSO / OIDC 登入與集中撤權
- per-team OIDC 登入模型；owner/admin 用 `0ops sso status` 看設定。
- 集中撤權：`0ops sso deprovision --user <email|id> --yes`（一次撤 membership + 所有 token）。
- **標狀態**：SSO/OIDC 已有 compose e2e（PR #141）；SAML 細節見 spec；SOC2/DPA 仍規劃中，不得宣稱已具備。
- 離職/資安事件的實務操作。
- **原則**：企業要的是「一鍵讓一個人徹底沒有存取」，撤權要集中且徹底。

### Day 29 — 稽核與合規：查詢、匯出、驗證、incident
- 稽核操作：`0ops audit list` / `get`；export/verify（tamper-evidence，已落地 M9.1/M9.6）。
- 事故：`0ops incidents list --status open` / `get` / `close <id> --note "root cause"`（寫入 audit_log）。
- MCP `query_audit_log` / `list_incidents` 為唯讀；close 只在 CLI。
- 合規對映概覽（哪些已落地、哪些規劃中）。
- **原則**：稽核的價值在不可否認——查得到、匯得出、驗得了才算數。

### Day 30 — 進階運維與 30 天回顧
- 運維動作：`./manage.sh pitr-drill`、`prod-verify`、`prod-runner-validate`。
- 回顧：串起 Day 1–29，列「你現在會做的 20 件事」清單。
- 誠實交代限制：production 路徑需自備外部資源、部分能力（SOC2/DPA、部分 CLI verb）規劃中。
- 下一步：往哪深入、去哪回報問題。
- **原則**：好的收尾同時交代「你學會什麼」與「還沒證明什麼」。

---

## 撰稿順序建議

先寫 Day 1–9（介紹＋解決方案，讓讀者能實際裝好上手），再依序推 操作 → 實務 → 進階。每篇撰稿時對照 `src/internal/cli/root.go` 驗證 verb，遇 planned 能力標狀態。
