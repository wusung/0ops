# Marketing Engine（build-in-public content lane）

> 操作手冊。事實源為 [`../features/build-in-public-engine/spec.md`](../features/build-in-public-engine/spec.md)；
> **寫作契約**為 [`WRITING-PRINCIPLES.md`](WRITING-PRINCIPLES.md)（對外推廣、用戶視角、零內部代號）。本檔僅摘要日常操作。

行銷內容不是人工手寫，而是既有 task-runner 的一條 content lane：由 agent loop 產出、由客觀 gate 驗收。人的角色縮到「觸發節奏」一步。

## 三節奏（推廣角度）

| 節奏 | task 前綴 | 素材種子 | 模板 | 推廣角度 |
|---|---|---|---|---|
| 週更 | `MKT.W` | 下一個未用 `docs/adrs/00XX-*.md` | `templates/weekly-promo.md` | 把某能力翻成用戶價值 |
| 月更 | `MKT.M` | 下一個未用 `tasks/lessons.md` L0XX | `templates/monthly-promo.md` | 把某風險翻成「我們替你擋掉」的信任 |
| 季更 | `MKT.Q` | 一個 milestone 切片 | `templates/quarterly-promo.md` | 把 milestone 翻成「你的問題→怎麼解」成果 |

素材只是「本篇推廣什麼」的種子，不是要被解釋的技術對象（見 WRITING-PRINCIPLES.md）。

## 指令

- `./manage.sh mkt-next <weekly|monthly|quarterly>` — 節奏產生器：讀 `sources-ledger.md` 挑下一個 available 素材，註冊一筆 content task（registry 三檔）。冪等：同素材不重複註冊。
- `./manage.sh mkt-verify <post-path>` — 客觀內容驗收 gate（G1–G6）。
- `./manage.sh mkt-publish <queue-item.yaml> [--publish]` — dry-run 散佈器：由 queue 變體印出 FB 粉專 / Threads payload；不連網。`--publish` 本輪被 guard。

寫作本身由 `./manage.sh task-run MKT.W{n}` 派 agent 依模板 + `WRITING-PRINCIPLES.md` 產出，走既有 superpowers 序列。

## Verify gate（G1–G6）

- **G1 雙語**：同時存在非空 `## 中文` 與 `## English` 段。
- **G2 結構**：cadence 合法，且兩語段各有一個 `# 標題`。
- **G3 對外安全**：**禁止內部代號洩漏**（`ADR-XXXX` / `file.go:line` 出現即 fail），且**必須有 CTA**（安裝指令 / 試用連結）。對應 WRITING-PRINCIPLES.md 原則 2、6。
- **G4 邊界**：改動只落在 `docs/marketing/**`（bootstrap 以 `MKT_VERIFY_SKIP_G4=1` 略過）。
- **G5 帳本**：`sources-ledger.md` 該素材已標 consumed；`editorial-calendar.md` 有本篇一列。
- **G6 社群長度**：若已產生 queue 變體，Threads ≤ 500 字。

## 邊界硬規則

content agent 可讀全 `docs/`，但**只能寫 `docs/marketing/**`**，嚴禁回寫污染規格來源（G4 強制）。散佈到社群為對外不可逆動作，本輪一律只到 dry-run，人工核准後才發（真發屬 MKT.2）。

## 目錄

- `WRITING-PRINCIPLES.md` — 寫作契約（對外推廣、用戶視角、零內部代號）
- `sources-ledger.md` — 素材帳本（available/consumed）
- `editorial-calendar.md` — 出刊記錄
- `published-ledger.md` — 發佈 permalink + dedup key 記錄
- `templates/` — 三節奏推廣模板
- `posts/` — 推廣文案產出
- `queue/` — 社群短文變體（散佈輸入）
