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

- 不在 `tasks/todo.md` 加行銷 checkbox（違 AGENTS.md 進度單一來源規約）。
- 不預建 `docs/marketing/` 空殼（屬執行非評估）。
- 不改任何 ADR / spec / runbook（無觸發條件）。
