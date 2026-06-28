# 0ops 六階段成熟度分析

**版本**：v1.0
**日期**：2026-06-29
**對應文件**：`docs/0ops-business-plan.md`、`docs/0ops-plan.md`、`README.md`、`AGENTS.md`、`tasks/todo.md`
**方法**：以六個 stage 對 repo 現況做成熟度判讀，每段先寫結論再列證據與缺口。評分為相對成熟度（★1–5），技術判斷不為友善模糊。

---

## 本質

單一 Go module、三 binary 的 agent-native 部署平台：

- `0ops-server`：純 IaaS REST/SSE backend，不跑 LLM
- `0ops`：人類 CLI（cobra），互動式 preview/confirm
- `0ops-mcp`：給 AI CLI 用的 stdio MCP server，工具一對一映射 backend API

定位為「AI coding agent 出貨時原生呼叫的那隻手」——補上 agent 工具帶裡缺的 `ship`。
MCP 是接入機制（how），非身份（who）；身份綁角色不綁協定。312 個 Go 檔、M0–M6 已 ship、v1 backbone 完成。

成熟度呈**倒梯形**：技術階段（Prototype / Production Pipeline / Operation OS）接近種子後水準，
商業階段（Requirement 部分、Marketing Engine、$1M+ Engine）仍在創意期。

| Stage | 成熟度 | 一句話 |
|---|---|---|
| 1 發現需求 Requirement | ★★★★☆ | 問題定義可證偽，但零真實客戶驗證、團隊 credibility 空白 |
| 2 原型 Prototype | ★★★★★ | 早越過原型期，安全模型在 backend 層強制 |
| 3 生產管線 Production Pipeline | ★★★★★ | 技術最強段，端到端鏈路封裝完整 |
| 4 Operation OS | ★★★★☆ | 運維骨架齊全，但缺真實事故與 SLA 數據 |
| 5 Marketing Engine | ★★☆☆☆ | 最弱段，引擎尚未點火（見強化方案） |
| 6 $1M+ Engine | ★★☆☆☆ | 定價模型完整，$1M 全靠未驗證假設 |

---

## Stage 1：發現需求 Requirement — ★★★★☆

**結論**：需求論證最完整的一段，問題定義具體、可證偽，但客戶驗證仍是紙上假設。

證據：

- 四個痛點分層清晰：AI CLI↔PaaS 最後一哩斷層、自建 K8s 學習曲線、台灣市場計費/法規不對等、寫入無安全網（business-plan §二）。
- 痛點規模量化（台灣 5 人以上團隊 8,000–12,000、self-host 友善需求 1,500–3,000）。
- 差異化矩陣對 Vercel/Railway/Zeabur/Sealos 逐一定位。

缺口（致命）：

- **零真實客戶驗證**。財務表 2026 H1/H2 付費團隊皆為 0，design partner「進場但尚未付費」。需求成立與否完全未經市場檢驗。
- **創辦團隊章節自承「未完成，屬對外溝通阻斷項」**（§九 264 行）——這是 requirement 階段最大未填空格，沒有團隊 credibility，需求洞察無法轉為信任。
- 護城河自承「MCP 是先發鑰匙，非護城河本身」——清醒，但也等於承認當前無結構性防禦。

---

## Stage 2：原型 Prototype — ★★★★★

**結論**：早已越過原型期，這是六階段中最扎實的一段。

證據：

- Dogfooding entry criteria 六條（business-plan §十三）全部可驗證，git log 顯示已跑通：vertical slice `GET /v1/apps → CLI → MCP`、`create_app` 兩階段 preview/confirm、冪等重跑。
- 安全模型在 backend 層強制（write tool 無 `preview_id` 直接 4xx），不是 UI 約定——agent 無法繞過。prototype 階段就把架構防呆做對。
- `examples/node-demo` + `tasks/e2e-*.sh` 端到端腳本存在，9 步 e2e 全 PASS。

缺口：

- Buildpack 對冷僻語言偵測失敗仍是已知風險（risks 表標 M），尚無 Dockerfile fallback。

---

## Stage 3：生產管線 Production Pipeline — ★★★★★

**結論**：技術上最強的一段，封裝完整度遠超一般種子前專案。

證據：

- 完整鏈路落地：`pack build → GHCR push → render manifest → commit gitops repo → ArgoCD sync → K3s → Cloudflare Tunnel hostname`。
- `deploy/` 下 Helm charts（server/postgres/cloudflare-tunnel）、`deploy/bootstrap/manage.sh prod-up` 一鍵裝整套、ArgoCD app-of-apps、sealed-secrets。
- self-hosted runner 已工程封裝（install-runner.sh、systemd unit、`prod-runner-validate`），webhook + 手動 redeploy 雙路徑（M4 done）。
- CI 已含 postgres service + migrations，DB 整合測試在 CI 真跑（PR #114）。

缺口：

- M6-Q1「production CI workflow 端到端驗證」卡在 user 端外部資源（runner 註冊、GHA variable），**工程做完但 production 路徑尚未綠燈**。這是 pipeline 從「封裝完成」到「驗證可用」的最後一哩，目前未跨過。
- GitOps repo 高並發衝突風險僅有 retry+rebase，未實測。

---

## Stage 4：Operation OS — ★★★★☆

**結論**：運維骨架齊全且超出 v1 必要範圍，但缺真實事故與 SLA 數據。

證據：

- HA leader election（`internal/server/leader`）、Postgres PITR/HA-DR、reconciler 收斂（delete-convergence P0 已修，PR #117/#118 admin retry-delete）。
- observability skeleton、trace-id end-to-end、slo-and-alerting、audit-log + audit-export-and-integrity feature specs 齊備。
- `docs/runbooks/` 存在（gha-self-hosted-runner 等），incident reconciler 有 feature。
- trust-and-compliance、threat-model、compliance-framework-mapping 文件成系統（最近 PR #126）。

缺口：

- SLO/alerting 是 spec 與骨架，**無真實流量、無事故演練、無 SLA 達成數據**。Operation OS 的真正考驗（incident response、on-call、故障信任）尚未發生。
- 「客戶 production 故障導致信任損失」自標為 H 風險，緩解仍是設計而非實證。

---

## Stage 5：Marketing Engine — ★★☆☆☆

**結論**：六階段中最弱。有渠道規劃，但引擎尚未點火。

證據：

- GTM 三階段、渠道優先序（Winshare 內部 → design partner → case study → 內容社群 → 平台合作）已寫。
- 安裝 UX 做了降低摩擦：一條 curl install + device flow login + 自動寫 MCP config（`0ops onboard`，PR #115）——這是 marketing 的產品端基礎。
- SKILL packs（claude-code/codex/copilot）作為跨生態接入策略。

缺口（嚴重）：

- **無內容、無社群、無 SEO、無公開 demo 資產**。repo 內找不到 marketing 產出物，全是計畫。
- DevRel/Product 角色標 P1 但團隊未組（4 FTE 規劃，實際團隊章節空白）。
- 行銷依賴「先有可重現 design partner 案例再釋出內容」——但 design partner 為 0，等於 marketing engine 的點火條件尚未滿足，形成**雞生蛋死結**。
- 品牌資產未定（`0ops.io/.tw/.dev` 仍在「待申請」§十三）。

### 強化方案：Build-in-Public 決策透明引擎

死結的根源是把「行銷內容」綁死在「需要先有付費客戶案例」。打破方式：**讓行銷內容由工程過程本身產出，而非等待客戶成果**。
0ops 的最大資產是它本身就是用 docs-driven agentic engineering 蓋出來的——ADR、feature spec、tasks/lessons、reconciler 收斂修復史，全是現成的高密度素材。
以三個固定節奏把這些素材轉成可信度資產，無需任何外部客戶即可啟動：

#### 每週：分享一個「為什麼這麼做」的決策

- **素材來源**：`docs/adrs/*.md` 與 feature spec 的決策點（例：為何 backend 不跑 LLM、為何 preview/confirm 在 backend 層強制、為何選 GitOps 為唯一真相）。
- **形式**：一則短文（中英雙語），結構為「面對的限制 → 考慮過的選項 → 選擇與取捨」。直接從既有 ADR 的 Context/Decision/Consequences 提煉。
- **目的**：建立技術品味與判斷力的公開記錄。讀者不是因為案例信任你，是因為看見你的決策過程而信任你——直接填補 §九 缺的團隊 credibility。
- **成本**：近乎零增量。ADR 已是規格來源，提煉版本只需標明來源（符合 AGENTS.md「提煉版需標示來源」）。

#### 每月：公開一個「這次失敗教會什麼」的反思

- **素材來源**：`tasks/lessons.md`、P0 事故修復（例：delete-convergence reconciler 未收斂 PR #117/#118、CLI token 缺 expires_at PR #113、CI 缺 postgres service PR #114）。
- **形式**：一則復盤，結構為「症狀 → 根因 → 為何當初沒看見 → 制度性修正（不是工作區間補丁）」。
- **目的**：反脆弱信號。願意公開失敗的團隊，比只展示成功的團隊更可信——這正是 enterprise self-host 客戶把 repo/token/domain 交付前要看的東西。它把 Operation OS 的事故處理能力（Stage 4 缺實證的部分）轉成對外證據。
- **成本**：lessons.md 是 CLAUDE.md 既定的修正後沉澱機制，每月挑一則最有教育性的對外即可。

#### 每季：提示一個「從問題到解法」的完整路徑

- **素材來源**：一個 milestone 的端到端切片（例：M6 app-source-ingestion 從 spec → 功能拆解 → e2e 9 步全 PASS 的完整旅程）。
- **形式**：一篇長文 + 可重現 demo，結構為「使用者痛點 → 設計約束 → 架構決策鏈 → 實作 → 驗證證據 → 失敗模式與治理」。
- **目的**：取代「需要 design partner 案例才能行銷」的死結——用自己的 milestone 當案例。每季一篇形成可索引的內容資產，餵 SEO 與社群，也是投資人簡報的現成附件。
- **成本**：milestone 完成時資料已散在 spec / todo / e2e 腳本，季度整合為單篇即可。

#### 為何這組節奏能解死結

| 原死結 | 三節奏的破解 |
|---|---|
| 行銷需 design partner 案例 | 改用自身 ADR / lessons / milestone 當素材，零外部依賴即可啟動 |
| 團隊 credibility 空白（§九） | 每週決策文 + 每月失敗復盤，用決策過程而非頭銜建立可信度 |
| Operation OS 缺對外證據 | 每月失敗復盤把事故處理能力轉成公開信任資產 |
| 內容生產成本高、無人力 | 三節奏全部複用既有 docs-driven 產出物，增量成本近零 |
| 無可索引 SEO 資產 | 每週/每月/每季穩定產出，自然累積長尾內容 |

落地約束（沿用 AGENTS.md Documentation 規範）：先寫結論再寫限制、用可驗證描述、提煉版標示來源、同一規則不散落多份文件互相漂移。
三節奏的對外產出建議獨立放 `docs/marketing/` 或外部 blog repo，不污染規格來源文件。

---

## Stage 6：$1M+ Engine — ★★☆☆☆

**結論**：定價與單位經濟模型完整且保守，但距離 $1M ARR 全靠未驗證假設支撐。

證據：

- 三軌定價齊全：Managed（$19/$99/$299）+ Self-host license（$5K–15K/年 Business、$30K+ Enterprise）。雙軌是合理的 $1M 加速器——一個 enterprise license 抵數百個 managed 訂閱。
- 單位經濟假設保守（ARPU $80、GM 70–80%、LTV/CAC 8–15x Beta 期降至 3–5x）。
- 財務表推到 2028 H2 MRR $65K（≈$780K ARR），樂觀情境靠 1–2 個 self-host license 才觸及 $1.5M–2.5M ARR。

缺口（致命）：

- **$1M 路徑的唯一加速器是 self-host enterprise license，但 enterprise 銷售需要的正是團隊 credibility + 合規認證 + 參考客戶——三者目前全缺**。Stage 5 的三節奏引擎正是補 credibility 與參考內容的最低成本路徑。
- 保守情境 2028 ARR 僅 $65K MRR（$780K），**連線性外推都摸不到 $1M**；$1M 完全依賴「拿到大型 license」這個低機率事件。
- 流失率（Beta 5–8%）、轉換率（LOI→paid）全為情境假設，無一段經實測。
- 募資 $500K–1M 的 12 月 KPI 是「3 LOI、2 design partner、1 內部服務 30 天」——這是 stage 1→2 的驗證指標，**不是 $1M engine 指標**。團隊自己也清楚現在離收入引擎還隔好幾個 stage。

---

## 跨階段判讀

- 真正瓶頸不在工程——pipeline 與 operation OS 的封裝完整度已超出多數種子輪專案。
- 瓶頸是**三個互鎖空格**：(a) 創辦團隊 credibility 未填、(b) 零付費客戶驗證、(c) marketing engine 點火條件（需 design partner 案例）與 design partner 取得（需 credibility）互為前提的死結。
- 最高槓桿動作不是再寫 code，而是：
  1. 補齊 business-plan §九 自承的創辦團隊空格。
  2. 啟動 Stage 5 的三節奏 build-in-public 引擎——它同時餵 credibility（每週決策）、信任（每月失敗）、可索引案例（每季路徑），且零外部依賴，是解開 (b)(c) 死結的最低扭矩起動點。
  3. 在內容引擎建立可信度後，再追第一個真實 design partner，把 Stage 6 的 $1M 假設逐段換成實測數據。

技術已經 ready，商業飛輪卡在起動扭矩。
