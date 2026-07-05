# Feature Spec：build-in-public-engine

> **狀態**：draft
> **來源**：`docs/six-stage-analysis/analysis.md` § Stage 5（破死結強化方案）；`docs/0ops-business-plan.md` §八「Build-in-Public 決策透明引擎」；既有 task-runner harness（`tasks/run/*.sh`、`manage.sh task-run`）；素材來源 `docs/adrs/*.md`、`tasks/lessons.md`、milestone 端到端切片
> **適用範圍**：把行銷內容產出模型化為既有 task-runner 的一條 content lane——由 agent loop 產出、由客觀 gate 驗收，而非人工手寫。含內容產生器、驗收契約、散佈 lane（做到 dry-run）；不含實際發文到 Meta（需外部憑證，屬 MKT.2）。
> **對應 Milestone**：MKT.0（零外部依賴，本輪）＋ MKT.1（散佈 lane，dry-run）＋ MKT.2（排程 + 真實發佈，future，gated）

## 1. 結論（先讀本段）

- 行銷引擎 = 既有 task-runner 的一條 content lane，**不是人工出刊**。造產品的同一個 agent loop 產出行銷內容，引擎本身即 build-in-public 的證據。
- 三節奏對應三種 content-task 型別：週更（ADR）、月更（lesson）、季更（milestone 切片）。task 命名 `MKT.W{n}` / `MKT.M{n}` / `MKT.Q{n}`。
- 人的角色縮到「觸發節奏」一步：`./manage.sh mkt-next <cadence>` 自動挑下一個未用素材、註冊一筆 content task；其餘（寫作、自我審查）走既有 superpowers 序列，品質由 `tasks/mkt/verify.sh` 客觀擋，PR/merge 由 runner 完成。
- 品質非人工把關而是**機器 gate**：雙語齊、模板結構齊、至少一個可查證工程錨點、diff 只落在 `docs/marketing/**`、帳本已更新——任一不過 exit 非零，runner 標 Failed（沿用既有反假綠紀律）。
- 散佈到 FB/Threads 是**對外不可逆**動作，MKT.0/1 一律「dry-run + 人工核准後才發」，全自動發佈留給 MKT.2 且 gated 在團隊 credibility / design partner 時機。
- 邊界硬規則：content agent 可讀全 `docs/`，但只能寫 `docs/marketing/**`，嚴禁回寫污染規格來源。
- 本 spec supersede `tasks/todo.md` 舊註記「持續出刊走編輯日曆而非 task runner」——決策改為走 task runner loop。

## 2. 範圍

### 2.1 包含
- content lane 的 registry / generator / execution / verify 四個面向定義
- 三種 content-task 型別與素材映射表
- `tasks/mkt/next.sh`（節奏產生器）、`tasks/mkt/verify.sh`（客觀驗收 gate）、`manage.sh mkt-next` 包裝
- `docs/marketing/` scaffold（README、editorial-calendar、sources-ledger、templates、posts、queue）
- MKT.1 散佈 lane 做到 **dry-run**：社群短文衍生 + `tasks/mkt/publish.sh` dry-run + queue/published-ledger 骨架
- First proof：由 loop 產出第一篇 canonical 長文（`MKT.W1`，素材 ADR-0002）

### 2.2 不包含
- 實際呼叫 Meta Graph / Threads API 發文（需 Meta app、FB 粉專、Threads 帳號、token）→ MKT.2
- 自動排程器（cron / GitHub Action 定期觸發 `mkt-next`）→ MKT.2
- FB app review / 商業驗證流程（外部 lead-time 項）→ MKT.2 前置
- 內容之外的 SEO/社群互動經營策略（屬 business-plan，非本引擎機制）

## 3. 架構：content lane 疊在既有 runner 上

四個面向，前三者重用既有機制、只加 MKT 專屬邏輯：

1. **Registry**：`tasks/task-list.md` + `tasks/task-status.md` + `tasks/todo.md`（既有）。content task 以 `MKT.W/M/Q{n}` 命名，Expected Paths 固定 `docs/marketing/**`。
2. **Generator**：`tasks/mkt/next.sh <weekly|monthly|quarterly>`——讀 `docs/marketing/sources-ledger.md`，挑該節奏下一個未用素材，append registry 三檔對應列 + todo acceptance bullets。冪等：同素材不重複註冊。
3. **Execution**：`./manage.sh task-run MKT.W{n}`（既有 runner）——開 worktree + 由 `prompt.sh` 組出強制走 superpowers 序列的 prompt，agent 依模板寫 canonical 長文，runner 負責 commit/PR/merge。
4. **Verify**：`tasks/mkt/verify.sh <post-path>`——客觀 gate，runner 於 verify 階段呼叫；沿用既有 `--verify-only` 契約精神。

```
mkt-next <cadence>  ──►  registry 新增 MKT.X{n}  ──►  task-run MKT.X{n}
   (挑未用素材)            (task-list/status/todo)        (worktree + superpowers loop)
                                                              │
                                          agent 寫 canonical 長文 (docs/marketing/posts/)
                                                              │
                                                   verify.sh 客觀 gate
                                                              │
                                                   runner PR / merge
```

## 4. content-task 型別與素材映射

| 節奏 | task 前綴 | 素材來源 | 模板 | canonical 結構 |
|---|---|---|---|---|
| 週更「為什麼這麼做」 | `MKT.W` | 下一個未用 `docs/adrs/00XX-*.md` | `templates/weekly-decision.md` | 限制 → 選項 → 取捨 |
| 月更「失敗教會什麼」 | `MKT.M` | 下一個未用 `tasks/lessons.md` L0XX / P0 修復 | `templates/monthly-postmortem.md` | 症狀 → 根因 → 為何當初沒看見 → 制度性修正 |
| 季更「從問題到解法」 | `MKT.Q` | 一個 milestone 端到端切片 | `templates/quarterly-path.md` | 痛點 → 設計約束 → 架構決策鏈 → 實作 → 驗證證據 → 失敗模式 |

素材挑選：`sources-ledger.md` 以 `consumed` / `available` 標記每個 ADR / lesson / milestone。產生器挑該節奏下第一個 `available`，寫入 task 後 agent 於完成時將其標為 `consumed`（列為 verify G5）。

## 5. verify gate 契約（`tasks/mkt/verify.sh`，客觀、機器可判）

輸入 canonical 長文路徑；逐項檢查，任一失敗印出原因並 exit 非零：

- **G1 雙語**：檔內同時存在非空的 zh 與 en 區塊（front-matter `lang` 或 `## 中文` / `## English` 段）。
- **G2 模板結構**：該節奏 canonical 結構的必填標題全部存在。
- **G3 工程錨點**：至少一處命中 `ADR-\d{4}` 或 `[\w./-]+\.go:\d+` 或 `\b[0-9a-f]{7,40}\b`（commit sha）——強制內容綁真實工程事實，杜絕空泛行銷語。
- **G4 邊界**：改動清單（含 untracked）只落在 `docs/marketing/**`；任何一條 path 在外即 fail（防污染規格來源）。**G4 僅適用於內容產出 task（`MKT.W/M/Q{n}`）**；建 lane 的 bootstrap task（MKT.0）額外允許改 `tasks/mkt/**` 與 registry 三檔，其內容產出子步驟仍受 G1–G3、G5 約束。
- **G5 帳本**：`sources-ledger.md` 已把本次素材標 `consumed`；`editorial-calendar.md` 已有本篇一列。
- **G6 社群長度**（若已產生 queue 變體）：Threads 變體 ≤ 500 字。

## 6. MKT.1 散佈 lane（做到 dry-run）

散佈是 canonical 長文合併後的獨立階段，不塞進寫作 task：

- **衍生步驟**：agent 由 canonical 長文改寫出 `docs/marketing/queue/<post-id>.yaml`，含 `fb` 與 `threads` 兩變體（hook + 一個洞見 + canonical 回鏈）；Threads ≤ 500 字。
- **publisher**：`tasks/mkt/publish.sh <queue-item>`
  - 預設 **dry-run**：印出解析後 payload、目標通道（FB 粉專 / Threads）、將發之 API 呼叫；**不連網**。
  - `--publish`：本輪不實作真發；設計上 refuse 除非（a）憑證 env 齊備 且（b）`MKT_PUBLISH_CONFIRMED=1` 人工核准旗標存在。
  - **冪等**：dedup key = `sha256(post-id + channel)`；`docs/marketing/published-ledger.md` 記 permalink；重跑跳過已發。
  - **憑證**：僅走 env / sealed-secrets，絕不進 repo；沿用 `AssertProductionSafe()` 模式，偵測憑證被 commit 即 panic。
- **API client**：以介面隔離，dry-run 走 stub 實作；真實 Meta Graph（FB 粉專 `POST /{page-id}/feed`）與 Threads（兩步 `me/threads` → `me/threads_publish`）client 留 TODO，token 接線屬 MKT.2。

## 7. 邊界與安全規則

- content agent 讀全 `docs/`、只寫 `docs/marketing/**`（G4 強制）。
- 發佈公開社群 = 對外且不可逆（刪除後仍可能被快取/索引）→ MKT.0/1 永遠人工核准後才發，且本輪只到 dry-run；全自動發佈屬 MKT.2 且 gated。
- FB 只支援粉專發文（個人牆 API 不支援）；Threads 發到已授權帳號。兩者皆需 Meta app 與 token（外部依賴）。

## 8. First proof（MKT.0 完成定義）

- 執行 `./manage.sh mkt-next weekly` 產生 `MKT.W1`（素材 ADR-0002 idempotency-and-compensation，即 preview/confirm 冪等核心 moat）。
- 執行 `./manage.sh task-run MKT.W1`，agent 產出 `docs/marketing/posts/2026-07-05-preview-confirm-idempotency.md`（中英雙語，週更結構）。
- 通過 `tasks/mkt/verify.sh`（G1–G5 全綠）。
- `sources-ledger.md` 將 ADR-0002 標 `consumed`；`editorial-calendar.md` 有對應列。
- 全程改動只落在 `docs/marketing/**`、`tasks/mkt/**` 與 registry 三檔。
- 產出由 loop 生成，非人工手寫——這是引擎能自走的證明。

## 9. Future（MKT.2，gated，不在本輪）

- 排程器：cron / GitHub Action 定期觸發 `mkt-next`，把「觸發節奏」這唯一人工步也自動化。
- 真實發佈：FB 粉專 + Threads publisher 接真 token，維持人工核准 gate（或依明確持久授權轉全自動）。
- 前置：Meta app 註冊、0ops FB 粉專、Threads 帳號、FB app review + 商業驗證；出刊 go/no-go 解禁（團隊 credibility / design partner 時機）。
