# Marketing Engine（build-in-public content lane）

> 操作手冊。事實源為 [`../features/build-in-public-engine/spec.md`](../features/build-in-public-engine/spec.md)；本檔僅摘要日常操作。

行銷內容不是人工手寫，而是既有 task-runner 的一條 content lane：由 agent loop 產出、由客觀 gate 驗收。人的角色縮到「觸發節奏」一步。

## 三節奏

| 節奏 | task 前綴 | 素材來源 | 模板 |
|---|---|---|---|
| 週更「為什麼這麼做」 | `MKT.W` | 下一個未用 `docs/adrs/00XX-*.md` | `templates/weekly-decision.md` |
| 月更「失敗教會什麼」 | `MKT.M` | 下一個未用 `tasks/lessons.md` L0XX | `templates/monthly-postmortem.md` |
| 季更「從問題到解法」 | `MKT.Q` | 一個 milestone 端到端切片 | `templates/quarterly-path.md` |

## 指令

- `./manage.sh mkt-next <weekly|monthly|quarterly>` — 節奏產生器：讀 `sources-ledger.md` 挑下一個 available 素材，註冊一筆 content task（registry 三檔）。冪等：同素材不重複註冊。
- `./manage.sh mkt-verify <post-path>` — 客觀內容驗收 gate（G1–G6）。
- `./manage.sh mkt-publish <queue-item.yaml> [--publish]` — dry-run 散佈器：由 queue 變體印出 FB 粉專 / Threads payload；不連網。`--publish` 本輪被 guard。

寫作本身由 `./manage.sh task-run MKT.W{n}` 派 agent 依模板產出，走既有 superpowers 序列。

## Verify gate（G1–G6）

- **G1 雙語**：同時存在非空 `## 中文` 與 `## English` 段。
- **G2 模板結構**：該節奏 canonical 必填標題全在。
- **G3 工程錨點**：至少一處 `ADR-XXXX` / `file.go:line` / commit sha。
- **G4 邊界**：改動只落在 `docs/marketing/**`（bootstrap 以 `MKT_VERIFY_SKIP_G4=1` 略過）。
- **G5 帳本**：`sources-ledger.md` 該素材已標 consumed；`editorial-calendar.md` 有本篇一列。
- **G6 社群長度**：若已產生 queue 變體，Threads ≤ 500 字。

## 邊界硬規則

content agent 可讀全 `docs/`，但**只能寫 `docs/marketing/**`**，嚴禁回寫污染規格來源（G4 強制）。散佈到社群為對外不可逆動作，本輪一律只到 dry-run，人工核准後才發（真發屬 MKT.2）。

## 目錄

- `sources-ledger.md` — 素材帳本（available/consumed）
- `editorial-calendar.md` — 出刊記錄
- `published-ledger.md` — 發佈 permalink + dedup key 記錄
- `templates/` — 三節奏 canonical 模板
- `posts/` — canonical 長文產出
- `queue/` — 社群短文變體（散佈輸入）
