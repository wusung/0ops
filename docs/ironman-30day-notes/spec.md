# 0ops 30 天實戰系列 — 系列 spec

**版本**：v2.0（改為使用者視角；v1.0 設計導覽版已作廢）
**日期**：2026-07-05
**格式參照**：iThome 鐵人賽（30 天，每日一篇，由淺入深）
**性質**：內容系列規畫（spec + plan）。本文件定義系列骨架與約束；逐日對映見 `plan.md`。

---

## 結論先行

從**使用者視角**寫 30 篇連載，帶讀者「真正用 0ops 把自己的專案部署上線」：介紹 → 解決方案 → 操作 → 實務 → 進階。不是 0ops 的內部設計/架構/ADR 導覽，而是一份可照著做的實戰手冊——每篇給讀者真實可執行的指令、MCP 工具用法、與端到端流程。

## 定位與角度

- **使用者要能照做**：每篇錨定真實指令（`0ops ...`）、真實 MCP 工具（24 個）、真實 DNS/OAuth 設定步驟。讀者複製指令就能跑。
- **兩種操作路徑並行**：(a) 人類用 `0ops` CLI；(b) AI agent（Claude Code/Codex/Copilot）透過 `0ops-mcp` 工具。同一後端，兩種入口，全系列交錯示範。
- **從痛點出發**：每篇先講「你想做什麼、卡在哪」，再給 0ops 的解法，而非先講 0ops 有什麼功能。

## 目標讀者

想把專案快速部署上線的開發者、重度使用 AI coding CLI 的工程師、少寫 K8s/YAML 的後端、需要 self-host 部署方案或台幣計費的台灣團隊。預設懂 git 與基本容器概念，不預設懂 K8s/ArgoCD/MCP。

## 每日文章格式（對齊 iThome 鐵人賽，參照 `docs/k8s-30days/`）

**檔名**：`NN_[Day N] 標題.md`（NN 補零 01–30），置於 `drafts/`。標題內嵌 `[Day N]`。

**文章骨架**（每篇固定順序，直接沿用 k8s-30days 的結構）：

1. `# [Day N] 標題`
2. 中繼資料區塊，接 `---`：
   - `- 原文連結: <url>`（發佈後補；未發佈填 `（未發佈）`）
   - `- 發布時間: <timestamp>`（未發佈可留空）
3. **前言**：回顧前幾天已學到什麼（承接感）＋ 今天要做的三點。
4. **正文分節**：每節一個小標題，帶可複製指令（`$ 0ops ...`）與**實際輸出**、必要時 Mermaid 圖或 side_effects 範例。從「你想做什麼」逐步走到「你會看到什麼」。
5. **總結**：一句收束今天學到什麼 ＋ 銜接明天。
6. **Q&A**：邀請讀者留言討論。
7. **參考連結**：來源（0ops repo 檔案路徑、官方文件）。
8. （選）**勘誤**：更正區。

- **長度**：每篇 1,500–2,500 字，至少一段可執行指令或一張圖。
- **語言**：正體中文（台灣用詞），指令與技術名詞保留英文原文。

**系列索引**：`drafts/index.json`，陣列 `{day, title, href, file}`（欄位對齊 `docs/k8s-30days/index.json`）；`href` 發佈後補。

## 章節結構（30 篇分四章，仿 k8s-30days 的分章導讀）

章名直接對齊 iThome 鐵人賽慣例（參照 `docs/k8s-30days/`：`介紹與開發環境架設` → `Kubernetes基礎概念與實作` → `Kubernetes進階概念與實作` → `如何管理Kubernetes`），走「介紹+環境 → 基礎概念與實作 → 進階概念與實作 → 如何管理」的學習進程。

| 章 | Day | 主題 |
|---|---|---|
| 介紹與環境架設 | 1–9 | 0ops 是什麼、誰該用、安全網、選型、安裝上手、接 AI CLI、工具授權 |
| 0ops 基礎概念與實作 | 10–18 | 部署 app、看 log、redeploy、綁網域、團隊、生命週期、CI token |
| 0ops 進階概念與實作 | 19–25 | AI 端到端部署、preview/confirm 實戰、push-to-deploy、權限與稽核、排錯 |
| 如何管理 0ops | 26–30 | self-host、生產 OAuth/網域、SSO、audit/合規、運維與回顧 |

## 撰寫約束（正確性為第一）

- **教真實指令，不教漂移文件**：以 CLI 原始碼 `src/internal/cli/root.go` 的 verb 為準。已知 quickstart / runbook 漂移，**不得沿用**：
  - `0ops apps get <slug>`（**不是** `0ops apps show`）
  - `0ops deploys redeploy <slug>`（**不是** `0ops redeploy`）
  - 網域只有 `0ops domains list`；**無** `0ops apps add-domain`（新增/驗證是 API/spec 面，尚未 CLI 化）。
- **標「已落地 vs 規劃中」**：SSO/OIDC、audit export/tamper-evidence、SKILL packs、MCP tool 權限 invocation-time enforcement 等，spec 中標為 placeholder/planned 者，撰文須註明狀態，不得說成已具備。
- **preview/confirm 一律照實描述使用者會打什麼**：CLI 的 typed slug + `required_phrase`（如 `DELETE <slug>`）+ `[y/N]`；MCP 的 `action_summary` + 完整 `side_effects` 審閱 + `confirmation_phrase`。
- **提煉版標來源**：引用 spec/runbook 註明來源路徑（AGENTS.md Documentation 規範）。

## 落點與範圍邊界

- **落點**：`docs/ironman-30day-notes/`（spec + plan + 日後 `drafts/day-NN.md`）。
- **刻意不做**：
  - **不預建 `docs/marketing/`**（MKT.0 的 Expected Paths，propagation-eval 明訂由 MKT.0 產出；本系列暫置獨立目錄）。
  - **本次只規畫，不寫 30 篇全文**。
  - **不實際發佈**（渠道分發屬 founder go/no-go）。

## 成功條件

- 讀者照 Day 7–10 能實際裝好 0ops 並部署一個 app 上線。
- 30 篇每篇有明確任務、可執行步驟、對映真實指令/工具，順序由淺入深無斷層。
- 全系列教的每一條指令都與 `root.go` 一致，無漂移；規劃中能力皆標狀態。
