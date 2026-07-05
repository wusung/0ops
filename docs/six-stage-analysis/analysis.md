# 0ops 六階段成熟度分析

**版本**：v1.1（supersedes v1.0 2026-06-29；v1.0 見 git 歷史）
**日期**：2026-07-04
**對應文件**：`docs/0ops-business-plan.md`、`docs/0ops-plan.md`、`README.md`、`AGENTS.md`、`tasks/{todo,task-status,task-list}.md`、`docs/features/**`、`deploy/**`、`.github/workflows/**`、git log（HEAD `c8e81b8`，2026-07-01）
**方法**：以六個 stage 對現況做成熟度判讀，每段先寫結論再列證據與缺口。評分為相對成熟度（★1–5），技術判斷不為友善模糊。
**與 v1.0 差異**：納入 2026-06-29 後一週共 11 筆 commit（M9.1–M9.6 全 Done、SSO/OIDC e2e PASS、task-runner 更名 PR #144）。

---

## 本質

單一 Go module、三 binary 的 agent-native 部署平台：`0ops-server`（純 IaaS REST/SSE backend，不跑 LLM）、`0ops`（人類 CLI）、`0ops-mcp`（給 AI CLI 的 stdio MCP server，工具一對一映射 backend API）。定位為「AI coding agent 出貨時原生呼叫的那隻手」——補上 agent 工具帶缺的 `ship`。MCP 是接入機制非身份；身份綁角色不綁協定。

成熟度呈**倒梯形**：技術三段（Prototype / Production Pipeline / Operation OS）接近種子後水準，商業三段（Requirement 部分、Marketing、$1M+ Engine）仍在創意期。**本週判讀關鍵**：過去一週純工程推進（M9 wave）把技術側再往上頂，但 MKT.0 仍 Pending、`docs/marketing/` 仍不存在——**倒梯形本週加深了，不是收斂**。

| Stage | 成熟度 | 一句話 |
|---|---|---|
| 1 發現需求 Requirement | ★★★★☆ | 問題定義可證偽，但零真實付費驗證、團隊 credibility 自承空白 |
| 2 原型 Prototype | ★★★★★ | 早越過原型期，安全模型 backend 強制；本週新增 SSO/OIDC compose e2e PASS |
| 3 生產管線 Production Pipeline | ★★★★★ | 端到端鏈路封裝完整，唯 production 路徑卡 user 端外部資源未綠燈 |
| 4 Operation OS | ★★★★☆ | 運維骨架齊全且本週 M9 wave 補上大量 audit/supply-chain code，仍缺真實流量與 SLA 數據 |
| 5 Marketing Engine | ★★☆☆☆ | 最弱段，引擎未點火，MKT.0 Pending、零對外產出物 |
| 6 $1M+ Engine | ★★☆☆☆ | 定價模型完整保守，$1M 全靠未驗證假設與低機率大 license |

---

## Stage 1：發現需求 Requirement — ★★★★☆

**結論**：需求論證最完整的一段，問題定義具體、可證偽，但客戶驗證仍是紙上假設，團隊 credibility 自承空白。

證據：

- 四痛點分層清晰且可證偽：AI CLI↔PaaS 最後一哩、自建 K8s 學習曲線（量化「簡單 Next.js 部署要寫 5 個 YAML」）、台灣計費/法規不對等、寫入無安全網（business-plan §二 行 45-73）。
- 市場規模量化：台灣 5 人以上團隊 8,000–12,000、self-host 友善需求 1,500–3,000；TAM USD 164B→548B（Precedence Research）；SAM/SOM 自承為滲透率假設推算（§二、§四 行 123-128）。
- 差異化矩陣對 Vercel/Railway/Render/Heroku/Fly.io/Zeabur/Sealos 逐一定位（§五 行 146-159 + 附錄 B）。

缺口（致命）：

- **致命**：零真實付費驗證。財務表 2026 H1/H2 付費團隊皆 0，design partner「進場但尚未付費」；全 repo grep design partner/LOI 皆為前瞻條件句，無任何已簽 LOI 或付費製品（§十 行 314-321）。
- **致命**：創辦團隊章節自承「本節目前未完成，屬於對外溝通阻斷項」（§九 行 281-282）——requirement 階段最大未填空格，無 credibility 則需求洞察無法轉信任。
- **一般**：護城河自承「MCP 是先發鑰匙非結構性防禦」——清醒，但等於承認當前無結構性 moat。

---

## Stage 2：原型 Prototype — ★★★★★

**結論**：早已越過原型期，這是六階段中最扎實的一段；本週再添一條對真實系統跑通招牌保證的 e2e。

證據：

- Dogfooding entry criteria 六條可驗證（business-plan 行 414-422），M1/M2 全 Done；vertical slice `GET /v1/apps → CLI → MCP`、create_app 兩階段 preview/confirm、冪等重跑皆落地。
- 安全模型在 backend 層強制，非 UI 約定：`delete_handlers.go` 對缺失/失效 preview 直接回 4xx（`preview_not_found`→404、`preview_consumed`/`preview_expired`→409、`confirm_mismatch`/`confirmation_phrase_mismatch`→400）；create_app 同型（`apps.go:500`、L419-430 preview 階段即擋 `github_app_not_installed`）。agent 無法繞過。M9.3 risk_level typed-confirmation 明載「不繞過既有 preview_id 後端強制、fail-closed」。
- e2e 腳本存在且 PASS：`e2e-create-app.sh`（37 步）、`e2e-source-upload.sh`（M6 9 步全 PASS）、**本週新增 `e2e-sso.sh` 對真 compose 棧跑通完整 OIDC dance + 集中撤權，task-status 記 PASS**（commit `9d52c76`/`6963062`，2026-06-30）；Go composition test `e2e_create_app_test.go`。

缺口：

- **一般**：create_app 真實 public URL 路徑未在 CI 驗（local mode `[SKIP]`，改以 callback HMAC + 路由自檢代替，需 staging/production 外部資源）。
- **一般**：buildpack 冷僻語言偵測失敗仍為已知風險（`docs/0ops-plan-risks.md`），尚無 Dockerfile fallback。

---

## Stage 3：生產管線 Production Pipeline — ★★★★★

**結論**：技術上最強的一段，端到端鏈路封裝完整度遠超種子前專案；唯 production 路徑最後一哩卡 user 端外部資源，尚未綠燈。

證據：

- 完整鏈路各環節皆有落地檔案：`pack build → GHCR push（release.yml images job push ghcr.io/wusung/0ops-*）→ render-and-push-gitops.sh（@sha256 digest pin，fail-closed）→ ArgoCD（root-app.yaml + install-argocd.sh + wait-for-sync.sh）→ K3s（install-k3s.sh + Helm chart）→ Cloudflare Tunnel（deploy/chart/cloudflare-tunnel/ 含 chart_test.go）`。
- deploy/ 下 Helm charts（server/postgres/cloudflare-tunnel，各附 `_test.go`）、`manage.sh`（13.7KB）`prod-bootstrap-all` 一鍵裝整套、sealed-secrets、ArgoCD app-of-apps。
- self-hosted runner 工程封裝（`deploy/runner/` install-runner.sh + systemd unit + `prod-runner-validate` + runbook），workflow opt-in `runs-on: ${{ vars.GHA_RUNNER_LABEL || 'ubuntu-latest' }}`。
- CI 含 postgres:17 service + goose migrations，DB 整合測試在 CI 真跑（`ci.yml` test job，PR #114）；另 govulncheck job（M9.4）。

缺口：

- **致命**：production 路徑未綠燈。M6 Q1 + v1 收尾殘留明載工程封裝全完成，剩 user 端動作（Cloudflare zone/tunnel token/K3s host、`.env.prod`、`prod-bootstrap-all`、`gh variable set GHA_RUNNER_LABEL`、`prod-runner-validate`）；`production-acceptance.md §9` 三條 curl 200 尚未執行（`tasks/todo.md:101-140`）。從「封裝完成」到「驗證可用」的最後一哩尚未跨過。
- **一般**：GitOps repo 高並發衝突僅 retry+rebase，未實測。

---

## Stage 4：Operation OS — ★★★★☆

**結論**：運維骨架齊全且超出 v1 必要範圍，本週 M9 wave 再補大量 audit/supply-chain 可驗證 code，但真正考驗（真實事故、on-call、SLA 達成）仍未發生。

證據：

- HA leader election（`internal/server/leader/`，`AlwaysLeader`/`LeaseLeader`，`OPS_LEADER_MODE=lease` 切換，附 `_test.go`，M5.5 Done）。
- Postgres PITR/HA-DR（`deploy/postgres/` primary+replica statefulset、WAL/pg-dump/pitr-drill scripts、cronjob，runbooks failover/pitr/restore-test，M5.4 Done）。
- reconciler 收斂有真實 P0 修復：delete_app 永遠卡 `deleting`（root cause 空 HandlerRegistry），PR #117 修因 + PR #118 `admin retry-delete`，live 清掉卡 10 天的 node-demo；`residue_handler.go` 接上 reconciler。
- trace-id 全鏈路 code 落地（PR #103-106，header→ctx→preview→deploy_run→callback→audit_log 已驗）；observability skeleton + prometheus alert/recording rules manifest（M2.6 Done）。
- **本週落地（M9.1–M9.6 全 Done）**：audit hash-chain（`services/audit/chain.go`）、append-only role migration 00014、export/verify CLI、audit outbox webhook（`services/audit/notify/`）、supply-chain digest pin + callback image_digest、SSO/OIDC + 集中撤權。
- `docs/runbooks/` 15 份成系統；trust-and-compliance/threat-model/compliance mapping 文件封板（M9.0/M9.2）。

缺口：

- **致命**：SLO/alerting 僅 spec 與骨架——**無真實流量、無 burn-rate 數據、無事故演練實績、無 SLA 達成率**。alert rules 與 runbooks 已寫但未經真實流量觸發；`list_incidents` 為 runtime tool 但 repo 無真實事故記錄；PITR 有 drill 腳本但無執行報告落檔。
- **嚴重**：現有事故實證（delete-convergence P0、onboarding 連環 bug、SSO 撤權 e2e）全屬 dev/dogfooding 環境，非 production 客戶流量。「客戶 production 故障導致信任損失」自標高風險，緩解仍是設計。
- **一般**：M9.6 audit 簽章金鑰 at-rest 加密 deferred，依賴尚無本體的 secrets-management（僅有 spec）。

---

## Stage 5：Marketing Engine — ★★☆☆☆

**結論**：六階段中最弱。有渠道規劃，引擎尚未點火；本週工程狂奔，行銷側零推進。

證據：

- GTM 三階段 + 渠道優先序（Winshare 內部 → design partner → case study → 內容社群 → 平台合作）已寫（§八 行 239-266）。
- 安裝 UX 降摩擦已 ship：一條 curl install + device flow login + 自動偵測 CLI 寫 MCP config（README 行 8-25，PR #115 已驗 24 tools）；`end-user-onboarding/spec.md` + mcp-hosts 文件。
- SKILL packs（claude-code/codex/copilot）作為跨生態接入策略。

缺口（嚴重）：

- **嚴重**：無內容、無社群、無 SEO、無公開 demo 資產。`docs/marketing/` 目錄**不存在**（ls 確認）；唯一 demo 類製品 `examples/node-demo/` 為 e2e 用途非行銷資產。
- **嚴重**：MKT.0 build-in-public 引擎 bootstrap 仍 `Pending`（task-status 行 42，deps `-`，Expected Paths `docs/marketing/**` 未產出）；bootstrap task 已於 2026-06-29 建立（commit `7e74a01`）但一週未執行。
- **致命**：行銷依賴「先有可重現 design partner 案例再釋出內容」，但 design partner 為 0，形成**雞生蛋死結**（analysis §Stage 5、business-plan §八行 273 明述）。

### 強化方案：Build-in-Public 決策透明引擎

死結根源是把「行銷內容」綁死在「需先有付費客戶案例」。破解：**讓行銷內容由工程過程本身產出**，零外部依賴即可啟動——0ops 本身就是 docs-driven agentic engineering 蓋出來的，ADR / lessons / reconciler 修復史全是現成高密度素材。三固定節奏：每週「為什麼這麼做」決策文（取材 `docs/adrs/`）補團隊 credibility；每月「失敗教會什麼」復盤（取材 `tasks/lessons.md`、P0 修復如 PR #117/#118）把事故處理轉對外證據；每季「從問題到解法」milestone 深掘（如 M6 或本週 M9 SSO e2e）當可索引案例。成本近零，因全部複用既有產出物。對外產出獨立放 `docs/marketing/` 或 blog。**本週新增素材已就緒**：M9 wave（audit tamper-evidence、supply-chain signing、SSO 集中撤權）正是 enterprise self-host 買家最想看的信任主題，是每季/每月節奏的現成頭條。

---

## Stage 6：$1M+ Engine — ★★☆☆☆

**結論**：定價與單位經濟模型完整且保守，但距 $1M ARR 全靠未驗證假設支撐。

證據：

- 三軌定價齊全：Managed（Starter $19 / Pro $99 / Team $299）+ Self-host license（Business $5K–15K/年、Enterprise $30K+）（§七 行 201-218 + 附錄 B）。程式層部分落地（`uploads_quota.go` per-tier 配額、`ratelimit.Plan`、ADR-0011 Plan Tier 矩陣），但**無任何 billing/invoice/stripe 金流實作**（grep 僅命中 quota/rbac，計費列 2027 v2）。
- 單位經濟假設保守（ARPU $80、GM 70–80%、LTV $1,500–2,000、LTV/CAC 8–15x Beta 降至 3–5x、payback 2–4 月，全表自述為假設）。
- 財務表推到 2028 H2 MRR $65K（≈$780K ARR）；樂觀情境 $1.5M–2.5M 靠「1–2 個大型 self-host license」。

缺口（致命）：

- **致命**：$1M 唯一加速器是 self-host enterprise license，但其前提（團隊 credibility + 合規認證 + 參考客戶）三者全缺——且多數合規能力（SOC2、tamper-evidence export、SSO/OIDC）在附錄 D 標「規劃中」（SSO 本週雖有 e2e，但 SOC2/DPA 仍未有）。
- **致命**：保守情境 2028 ARR 僅 $780K，連線性外推都摸不到 $1M；$1M 完全依賴「拿到大 license」低機率事件。
- **嚴重**：流失率（Beta 5–8%）、轉換率（LOI→paid）全為情境假設，自承「不是已驗證數據」。
- **嚴重**：募資 12 月 KPI（3 LOI、2 design partner、1 內部服務 30 天、≥10 次真實 create/redeploy）是 stage 1→2 驗證指標，不是 $1M engine 指標。

---

## 跨階段判讀

- **真正瓶頸不在工程**：pipeline 與 Operation OS 封裝完整度已超多數種子輪專案，本週 M9 wave 再度證明工程產能充沛。瓶頸是商業飛輪的起動扭矩。
- **三個互鎖空格（死結）**：(a) 創辦團隊 credibility 未填（§九 自承）、(b) 零付費客戶驗證、(c) marketing 點火需 design partner 案例、而 design partner 取得需 credibility——三者互為前提。畫成依賴環：credibility → design partner → 案例 → 內容 → credibility，環上無一節已閉合。
- **本週訊號**：一週 11 筆 commit 全在技術/工程/文件側（M9 audit/supply-chain/SSO、task-runner 更名），商業側唯一動作是「建了 MKT.0 task 但沒跑」+「寫了社群傳播評估文」。**工程與商業成熟度落差本週擴大**，倒梯形加深——這是最該被 founder 看見的形態訊號。
- **最高槓桿動作（排序）**：
  1. 補齊 business-plan §九 自承的創辦團隊空格——這是解開死結環的唯一非工程節點，且是 enterprise license（$1M 加速器）的前提。
  2. 執行 MKT.0，啟動 build-in-public 三節奏——它同時餵 credibility（每週決策）、信任（每月失敗）、可索引案例（每季路徑），零外部依賴，是解 (b)(c) 死結的最低扭矩起動點。**本週 M9 信任主題正是現成頭條素材**，執行成本比一週前更低。
  3. 在內容引擎建立可信度後追第一個真實 design partner，把 Stage 6 的 $1M 假設逐段換成實測。

技術已經 ready 且持續超前，商業飛輪仍卡在起動扭矩——且本週差距擴大而非收斂。
