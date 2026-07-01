# 六階段分析 — 跨文件傳播評估

**版本**：v1.0
**日期**：2026-06-29
**來源**：`docs/six-stage-analysis/analysis.md`
**問題**：六階段分析的結論，是否要求更新其他 `**/*.md`？

## 評估原則

六階段分析本質是對既有文件的**判讀（assessment）**，依設計不改變底層事實。
因此 Stage 1–4、6 的成熟度評分本身不產生傳播需求——它們是觀點，不是新規格。
唯一的淨新策略性產物是 **Marketing Engine 的 build-in-public 三節奏引擎**（使用者新增），
且它直接對應 business-plan 自承的死結（§五 moat 無結構性防禦、§八 內容綁 design partner 案例、§九 團隊 credibility 空白）。
傳播範圍即以此為界。

## 判定結果

| 候選文件 | 是否更新 | 理由 |
|---|---|---|
| `docs/0ops-business-plan.md` §八 GTM | **是** | 三節奏是 GTM 機制，原渠道 #4「內容與社群」過於模糊；§八階段二原文「取得可重現案例後再釋出內容」正是死結本身。已 operationalize 渠道 #4、加註內容不等 design partner 案例、新增「Build-in-Public 決策透明引擎」小節並回指 analysis.md。 |
| `docs/six-stage-analysis/analysis.md` | 否 | 即來源文件，已含完整論述，無需自我複製。 |
| `tasks/todo.md` | 否 | AGENTS.md 規定「進度只在本檔更新；docs/ 不得再新增 checkbox」，且 todo 是**工程**進度單一事實來源。行銷節奏屬營運，非工程 milestone，納入會造成範圍漂移。待節奏實際啟動時由 founder 決定是否另立追蹤。 |
| `docs/marketing/` | 否（僅建議） | analysis 與 business-plan 皆已標示此為三節奏對外產出的建議落點。實際建立 spec 屬「啟動行銷」動作，超出本次「評估傳播需求」範圍，不預先 scaffold 空殼。 |
| `docs/0ops-business-plan.md` §九 團隊 | 否 | 該章已自承「未完成，屬對外溝通阻斷項」。分析強化此判斷但未提供新事實；補實際團隊成員是 founder 動作，非文件同步。三節奏作為 credibility 緩解已在 §八 連結。 |
| ADR / feature spec / runbook | 否 | 分析未觸及任何架構決策、API、schema、權限、deploy workflow、domain verify 或安裝方式。依 AGENTS.md Documentation 規約，無觸發同步條件。 |

## 本次實際變更

1. `docs/0ops-business-plan.md`
   - §八階段二：技術內容不等 design partner 案例，三節奏與驗證並行。
   - §八渠道 #4：指向 Build-in-Public 引擎為主軸。
   - §八新增「Build-in-Public 決策透明引擎」小節（三節奏 + 落點 + 回指 analysis.md § Stage 5）。
2. `docs/six-stage-analysis/propagation-eval.md`（本檔）：記錄評估與刻意不改項。

## 刻意不做（避免範圍漂移）

- 不預建 `docs/marketing/` 空殼（屬執行非評估；由 task `MKT.0` 產出）。
- 不改任何 ADR / spec / runbook（無觸發條件）。
- 持續出刊（每週/每月/每季）不進 task runner——屬持續營運，task runner 為一次性工程/spec 產出。落於編輯日曆 / `/schedule`。

---

## 追加評估（2026-06-29）：todos / tasks 落點

**問題**：六階段需求是否要加進 todos / task runner？

**先前不精確處更正**：原「刻意不做」列「不在 `tasks/todo.md` 加行銷 checkbox（違 AGENTS.md 進度單一來源規約）」過度概括。AGENTS.md 規約是「**工程進度** checkbox 只在 todo.md、docs/ 不得新增 checkbox」；而 `tasks/todo.md` 本身設有「治理 / 商業（文件層 backlog）」段，明標「不是工程任務；founder 決策範疇」，正是非工程決策項的合法託管處。故 founder 決策類 checkbox 加在該段並不違規。

**拆分判定**：需求依性質落三處，非單一去向。

| 部分 | 性質 | 落點 | 本次動作 |
|---|---|---|---|
| 引擎 bootstrap（spec + scaffold + 首篇證明） | 一次性、產出 doc、可 verify | `tasks/task-list.md` + `task-status.md`（runner 事實源；precedent：M9.0/M9.2 doc-only 任務） | 新增 task `MKT.0`（Pending，deps `-`，paths `docs/marketing/**`） |
| 行銷 go/no-go（持續節奏 + 渠道/品牌承諾） | founder 決策 | `tasks/todo.md` § 治理 / 商業 backlog | 新增 1 checkbox，回指 `MKT.0` |
| 每週/每月/每季實際出刊 | 持續營運、非一次性 | 編輯日曆 / `/schedule` | 不入 task runner（明確排除） |

**為何 bootstrap 適合 task runner 而出刊不適合**：task runner 每筆皆一次性、有 Expected Paths、走 verify gate。bootstrap 符合（產出 `docs/marketing/**`、首篇可追溯至真實 ADR 即 verify 通過）；出刊是無終點的週期動作，塞進一次性 task runner 是 category error。

**為何 bootstrap 不需 founder gate**：最便宜節奏（每週決策文）僅需既有 ADR 為素材，零外部依賴，是 analysis § Stage 5 指認的死結破解點，可獨立於 go/no-go 先行。`MKT.0` 因此設為可直接 `make task-run` 的 Pending backlog，不等決策。

---

## 追加評估（2026-06-29）：build-in-public 內容是否推廣至社群渠道（FB / WhatsApp / Reddit 等）

**問題**：三節奏產出的內容，是否要主動推廣到 Facebook / WhatsApp / Reddit 等社群？

**結論（先寫）**：**不是「推到所有社群」，而是「以自有正典為錨，只 syndicate 到與 ICP 高度吻合的 2–3 個渠道」。** FB 與 WhatsApp 對本內容型態與本買家皆不適配（僅留窄例外）；Reddit 條件式可但成本非近零；真正高吻合卻未被列入問題的是 Hacker News / LinkedIn / dev 圈 X。盲鋪多渠道會直接違背「增量成本近零」這個引擎命根。

**判定框架**：渠道價值 = ICP 吻合度 × 內容形式吻合度 × 邊際成本。三者任一不成立即不選為主渠道。

**內容型態**：高密度技術決策文（ADR 提煉 / 失敗復盤 / milestone 深掘，中英雙語）。
**買家畫像**：把 repo / token / domain 交出去的 platform/devops 工程師、enterprise self-host 決策者、投資人；地理為台灣 / 東南亞。

| 渠道 | 判定 | 理由 |
|---|---|---|
| **自有正典**（own blog / `docs.0ops.*`，中英雙語） | **必做（先決）** | SEO 長尾與可索引案例只累積在自有資產；§八「每季可索引 SEO 資產」「投資人簡報附件」僅在此成立。社群貼文不是你擁有的資產，只是指標。無此基礎，社群分發沒有耐久標的。 |
| **Hacker News**（Show HN + 決策文） | **高** | 「為何 backend 不跑 LLM」「為何 preview/confirm 在後端強制」正是 HN 主場；單篇同時觸及 enterprise tech buyer 與投資人。問題未列，但比 FB/WhatsApp 重要得多。成本：單次投放，須慎防 promotional tone。 |
| **LinkedIn**（founder 親署） | **高** | enterprise self-host 決策者與投資人在此；founder 署名決策文直接補 §九 團隊 credibility 空白。低邊際成本。 |
| **Reddit**（限 r/devops, r/kubernetes, r/selfhosted, r/golang） | **條件式** | ICP 重疊高，但 Reddit 重罰自我推銷，需真實參與而非貼連結 → 成本**非近零**，需先累積社群信用。慎用，非啟動期首選。 |
| **X / Twitter**（dev / build-in-public 圈） | **中** | build-in-public 文化主場，thread 適配每週決策文；AI CLI / dev tooling 受眾在此。可作次要 syndication。 |
| **Facebook** | **否（主渠道）** | platform/devops 工程師不在 FB 消費技術決策內容，受眾—內容雙重錯配。窄例外：台灣在地技術社團（如 Golang Taiwan、DevOps 類 FB 社團）走 founder 個人網絡，因台灣 GTM。不列為內容分發主軸。 |
| **WhatsApp** | **否（分發）** | 1:1 / 群組私訊，是 nurture 不是 discovery，零 SEO、零索引。唯一角色：東南亞已熱的 design partner lead 直接溝通（當地商務主流）。不是 build-in-public 的分發面。 |

**關鍵原則**：

1. **正典先行**：先立一個自有 home（單一事實源），社群皆為指向 home 的指標，非 home 本身——沿用本專案「同一規則不散落多份互相漂移」原則。
2. **「近零成本」前提更正**：該前提只對內容**生成**成立（複用既有 docs）；**分發**到多渠道非零——每渠道需格式改寫、社群規範、持續互動。DevRel 尚未到位（§九 P1 空缺）。啟動即鋪 5 個渠道直接打破前提。
3. **先 2–3 後擴**：啟動只開「1 正典 + 1–2 syndication」，量哪個渠道轉化再擴；以資料而非直覺決定下一個渠道。
4. **渠道不等於節奏**：渠道清單屬持續營運，與三節奏出刊同歸編輯日曆 / `/schedule` + founder 決策，明確排除於一次性 task runner。

**治理落點**：本題為「持續出刊／分發」範疇，沿用本檔既定排除——不入 task runner。`MKT.0` bootstrap 只負責產出正典內容並定義分發 policy 骨架（`docs/marketing/**`），**不替 founder 拍板渠道清單**。FB/WhatsApp 的窄例外與 Reddit 的條件式參與，皆為 founder go/no-go 範疇。
