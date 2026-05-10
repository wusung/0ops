---
adr: "0003"
title: MCP SDK 選型（Go）
status: Accepted
date: 2026-05-09
tags:
  - mcp
  - sdk
  - dependency
  - integration
supersedes: []
superseded-by: []
---

# ADR-0003：MCP SDK 選型（Go）

* Status：Accepted
* Date：2026-05-09
* 適用範圍：M0–M5；`0ops-mcp` binary（`cmd/mcp/`、`internal/mcp/`）的 SDK 依賴選擇
* 來源：`docs/0ops-plan.md`「TBD」段「Go MCP SDK 選型」與「MCP server」段；本 ADR 取代 plan 暫定（`mark3labs/mcp-go`）方向
* 相依 ADR：[ADR-0001](0001-multi-tenancy-and-rbac.md)（team_slug 為 MCP write tool 必填參數）；[ADR-0002](0002-idempotency-and-compensation.md)（`*_preview` / `*` 拆兩 tool 與 description 句式）

## 0. TL;DR（先讀本段）

採用以下五項組合決策：

1. **v1 SDK**：官方 `github.com/modelcontextprotocol/go-sdk`（v1.x）。Plan 暫定的 `mark3labs/mcp-go` 改為 fallback。
2. **Transport**：v1 僅 stdio；不接 Streamable HTTP / SSE transport。
3. **Tool registry pattern**：啟動時靜態註冊；每個 tool 為單獨 file，實作統一介面（`Name() / Schema() / Description() / Call()`）。
4. **Description lint**：server 啟動時掃描自身 tool description，違反句式（`*_preview` 必含「ALWAYS call this BEFORE」、寫入 tool 必含「NEVER call this tool without」）即啟動失敗。
5. **Streaming（log follow 等）**：M0 spike 內驗證官方 SDK 支援度；若不支援，log follow 退為**分頁拉取 + cursor**，不阻擋 M0 結束。

行為與 tool description 句式細節以 `docs/0ops-plan.md`「MCP server」段為準，本 ADR 不重述。

## 1. Context and Problem Statement

`0ops-mcp` 是本機 binary（stdio transport），三家 AI CLI（claude code、codex、copilot）皆透過此 binary 操作 backend API。SDK 選型直接決定：

* protocol 規格相容性（影響三家 client 互通行為）。
* Tool registry 介面（影響 `internal/mcp/tools/` 的 boilerplate 與單檔規模）。
* SSE / streaming 支援（影響 `tail_logs` tool 的 UX：即時還是分頁）。
* 長期維護面（bus factor、breaking change 政策、CVE 修復速度）。

Plan 撰寫當下（2026 年初）官方 `modelcontextprotocol/go-sdk` 尚未 stable，因此 plan 暫定 `mark3labs/mcp-go`，並把實際選型延後至 ADR-0003 的 M0 spike。截至 2026-05 官方 SDK 已 v1.0.0 stable（與 Google 合作維護、保證日後不再有 breaking API 變動），社群 SDK 仍持續演進但已非唯一可生產化選項。本 ADR 是在這個新事實下重做 SDK 選型。

## 2. Decision Drivers

* **DD1 三家 AI CLI 規格相容**：claude code、codex、copilot 對 MCP spec 的遵守版本可能不一；SDK 對 spec 的覆蓋度與相容版本範圍直接影響可用性。
* **DD2 長期維護面**：v1 binary 將被內外部使用者長期執行；SDK 維護者規模、breaking change 政策、issue 回應速度為硬指標。
* **DD3 Transport 範圍**：v1 僅需 stdio；HTTP / Streamable 為 v2+ 範圍；不應為當下不需要的 transport 多帶依賴。
* **DD4 Streaming 支援**：`tail_logs` 是唯一 SSE-like tool；若 SDK 原生支援 streaming，UX 較好；不支援則退為分頁 + cursor。
* **DD5 Description lint 可行性**：ADR-0002 第 4 節要求 backend 對缺 `preview_id` 直接 4xx，但對 LLM 友善仍需 description 強制句式；SDK 需能讓 server 在啟動時 reflective 掃描自身 tool description。
* **DD6 Migration 軌跡**：未來若需轉 SDK，從官方→社群、社群→官方的 import path 與 handler signature 改動量。
* **DD7 Dependency footprint**：MCP binary 為靜態散布的 CLI 配套 binary；SDK transitive deps 直接影響 binary 大小與 CVE 暴露面。

## 3. Considered Options

針對 (a) SDK 主體選擇做完整比較；(b) Streaming 支援為次要深度比較；(c)(d)(e) 列表帶過。

### 3.1 (a) SDK 主體

| Option | 描述 |
|---|---|
| **A1. 官方 `modelcontextprotocol/go-sdk` v1.x**（採用） | 官方維護、與 Google 合作；v1.0.0 stable、breaking change 凍結；spec 全覆蓋（除 client-side OAuth） |
| A2. `mark3labs/mcp-go` | 社群實作（原作者 Ed Zynda）；spec 多版本向後相容；提供 Streamable HTTP transport |
| A3. 自寫 minimal MCP（直接對 spec） | 從 spec 起手實作 stdio + JSON-RPC 框架 |
| A4. 雙 SDK 並列（M0 spike、M1 二選一） | M0 寫兩份 prototype 比對結果，正式碼擇一 |

### 3.2 (b) Streaming 模型（log follow tool）

| Option | 描述 |
|---|---|
| **B1. SDK 原生 streaming（可行則採）+ 分頁 fallback**（採用） | M0 spike 驗證官方 SDK 是否支援 server-side incremental tool result；不支援即退為 B3 |
| B2. 純 SDK 原生 streaming | 強制要求 SDK 支援；不支援即阻擋 |
| B3. 分頁拉取 + cursor | tool 不 streaming；client 重複呼叫帶 cursor 取下一段 |
| B4. 非標準擴展（自簽協定） | SDK 之外塞自家 streaming 機制 |

### 3.3 (c)(d)(e) 局部選擇

| 子決策 | 採用 | 否決 | 一句結論 |
|---|---|---|---|
| (c) Tool registry pattern | 啟動時靜態註冊 + 統一介面（`Name/Schema/Description/Call`） | 動態 discovery / plugin runtime | v1 tool 數量固定（< 25），動態 discovery 為 over-engineering |
| (d) Description lint | server 啟動時 reflective 掃描，違反句式 fail-fast | CI lint only / 不檢查 | startup-time 檢查可保證任何部署版本都受約束；CI lint 對 vendored binary 失效 |
| (e) Auth source | 讀 `~/.config/0ops/auth.json`（perm 0600） | env var / 啟動參數 / OS keychain | 與 CLI 共用同一檔；env var 在多 AI CLI 環境難管理；keychain 跨平台複雜，留 v2 |

## 4. Decision Outcome

採用 **A1 + B1**，搭配 (c) 靜態 tool registry、(d) startup-time description lint、(e) `~/.config/0ops/auth.json`。

具體展開：

1. **Module dependency**：`go.mod` 加入 `github.com/modelcontextprotocol/go-sdk v1.x`；不引入 `mark3labs/mcp-go`（除非 spike 觸發 fallback 條件）。
2. **Transport**：`cmd/mcp/main.go` 啟動時 wire 官方 SDK 的 stdio transport；logging 走 `log/slog` + stderr。
3. **Tool registry**：`internal/mcp/tools/<tool_name>.go` 一檔一 tool；`internal/mcp/registry.go` 在 `init()` 或啟動時呼叫 SDK 的 register API；每個 tool 提供：
   * `Name() string`
   * `Schema() json.RawMessage`（input / output JSON Schema）
   * `Description() string`（含強制句式，見 ADR-0002 與 plan「MCP server / Tool description 強制約定」段）
   * `Call(ctx, args) (Result, error)`
4. **寫入類拆兩 tool**：`<action>_preview` 與 `<action>`；後者必須帶前者回的 `preview_id`，否則 backend 回 `400`（接續 ADR-0002）。
5. **Description lint at startup**：
   * `*_preview` description 必含字串 `ALWAYS call this BEFORE`
   * 非 preview 的寫入 tool description 必含 `NEVER call this tool without`
   * 違反任一條：`mcp-server` 印明確錯誤後 `os.Exit(2)`，不啟動 stdio loop
6. **Streaming**：
   * M0 spike 在 `tail_logs` tool 上驗證官方 SDK 的 streaming response API；若可用，採 streaming。
   * 不可用則 fallback 分頁：tool 接 `cursor` arg，回 `{ lines[], next_cursor }`，client 重複呼。
   * 不論哪條路，背後 backend 仍以 SSE 從 K8s 拉 log；MCP 端僅處理協定層。
7. **Auth**：`internal/mcp/auth/` 啟動時讀 `~/.config/0ops/auth.json`；無檔或 token 過期時所有 tool 直接回錯誤訊息：`「Bearer token 不可用，請於 host shell 跑 0ops auth login 重新登入」`，不嘗試 refresh / interactive flow（stdio binary 不能介入 terminal）。
8. **Spike 結果記錄**：M0 結束時，於 `docs/runbooks/mcp-sdk-spike-results.md` 落地測試矩陣（三家 AI CLI × 五條代表性 tool 流程）；矩陣不通過任一格 → 觸發本 ADR Revisit。

## 5. Pros and Cons of the Options

### 5.1 (a) SDK 主體

#### A1. 官方 `modelcontextprotocol/go-sdk` v1.x（採用）

* Good：v1.0.0 stable + breaking change 凍結；長期維護面風險最低。
* Good：與 Google 合作維護；bus factor 顯著高於單一作者社群專案。
* Good：spec 全覆蓋（除 client-side OAuth，0ops 不需要）；對三家 AI CLI 行為差異有最大公約數的容忍度。
* Good：作為「官方 reference」，AI CLI 廠商的 client 端測試矩陣有較高機率涵蓋此 SDK 行為。
* Bad：社群文章 / 範例多以 `mark3labs/mcp-go` 為主，學習資源相對少。
* Bad：v1.0.0 雖 stable 但生產級採用案例累積時間較短，corner case 偵測仰賴自身 spike。
* Bad：HTTP / Streamable transport 焦點較少（v1 不影響，v2 若需要再評估）。

#### A2. `mark3labs/mcp-go`

* Good：社群採用基數大、範例豐富；學習資源最完整。
* Good：spec 向後相容多版本（2024-11-05 → 2025-11-25），對使用舊規格的 client 較寬容。
* Good：原生 Streamable HTTP transport，未來 v2 若需 remote MCP 可直接用。
* Bad：單一作者社群專案；bus factor 與 CVE 回應速度為硬風險。
* Bad：隨 spec 演進可能出現非小幅 breaking change，需追隨升版。
* Bad：與 AI CLI 廠商的 client 端測試矩陣為「事實相容」而非「設計相容」。

#### A3. 自寫 minimal MCP

* Good：完全控制依賴與 binary 大小；無第三方 CVE 風險。
* Bad：MCP spec 演進需自追隨；維運成本高。
* Bad：不在 0ops 核心競爭力範圍；浪費工程時間。
* Bad：與 AI CLI 廠商 client 互通需自行測試所有版本，工作量大。

#### A4. 雙 SDK 並列

* Good：M0 結束前可定量比較。
* Bad：M0 工作量翻倍；違反「先窄後寬」原則。
* Bad：兩份 prototype 容易產生 abstraction debt（中間層遮蔽真實 SDK 行為差異）。

### 5.2 (b) Streaming 模型

#### B1. SDK 原生 + 分頁 fallback（採用）

* Good：兩條路皆 spec-compliant；不引入自簽協定。
* Good：M0 spike 即可決議，不阻擋啟動。
* Bad：M0 工作多一條決策路徑；spike 結果落地需要文件追蹤。

#### B2. 純 SDK 原生

* Good：UX 最佳；log 即時推送。
* Bad：若 SDK 不支援會阻擋 M0；違反「不阻擋啟動」原則。

#### B3. 純分頁

* Good：實作簡單；任何 SDK 都支援。
* Bad：log 流體驗為「按下 Enter 才有下一段」，與 CLI `--follow` 體感落差大。
* Bad：客戶端需自行管理 cursor，新 tool 設計負擔上升。

#### B4. 非標準擴展

* Good：完全自由設計。
* Bad：失去 spec 相容；三家 AI CLI 不會理解；違反 DD1。

## 6. Consequences

### 6.1 Positive

* SDK 維護面交給官方 + Google，0ops 不需追蹤 spec 演進。
* v1.0.0 breaking change 凍結讓 `0ops-mcp` 二進位散布的版本衝擊降到最低。
* Tool registry + 統一介面讓 `internal/mcp/tools/` 為單檔單 tool，符合 AGENTS.md「責任邊界」原則。
* Description lint at startup 提供 fail-fast 保證——任何部署版本的 description 句式違規都在啟動時被攔下，不依賴 CI。
* Auth 共享 `~/.config/0ops/auth.json` 讓 CLI / MCP 對使用者單一登入語意。

### 6.2 Negative

* 範例與社群文章多以 `mcp-go` 為主，新加入工程師需翻譯心智模型；建議 `internal/mcp/README.md` 補一段「為何不是 mcp-go」與 import path 對照。
* SDK 對 streaming 的支援度為 M0 結束前的未知數；若退為分頁，`tail_logs` UX 與 plan「使用者腳本範例 Pattern A」描述的「即時 follow」不完全一致，需在 CLI 包裝端處理。
* 官方 SDK 的 corner case 仰賴自身 spike；contract test 與 deterministic transcript fixture 是必要投資（已列於 plan「Verification / 整合」段）。
* startup-time description lint 對 hot-reload dev 體驗略差（每次改 description 需重啟 binary）；可接受。

### 6.3 Neutral

* HTTP / Streamable HTTP transport 不在 v1 範圍；未來若 remote MCP 為需求，需獨立 ADR（評估官方 SDK 屆時的 HTTP 支援度，或 fallback `mcp-go`）。
* Tool description 強制句式為產品決策（接 ADR-0002），不隨 SDK 變動。
* Tool registry pattern 為實作慣例，未來若 tool 數量 > 50 可重審動態 discovery；現階段 < 25 不必。

## 7. Revisit Triggers

下列任一條件觸發時，應重新評估本 ADR：

1. **官方 SDK 對三家 AI CLI 互通性失敗**：M0 spike 矩陣（claude code / codex / copilot × 5 條代表性 tool 流程）任一格不通且無法繞過 → fallback `mark3labs/mcp-go`，本 ADR 之 (a) 段被 supersede 為 A2。
2. **官方 SDK CVE 回應 SLA 不達標**：HIGH/CRITICAL CVE 修復 > 7 天未釋出 patch → 評估雙 SDK 並列或 fallback。
3. **Streaming spike 失敗**：B1 退為 B3 後，使用者 NPS / dashboard 顯示 `tail_logs` 為主要不滿來源 → 評估自簽 streaming（B4）或升 v2 用 HTTP transport SDK。
4. **Description lint 機制失效**：官方 SDK 不暴露 reflective 取得 tool description 的 API → 改為 CI 階段 lint，本 ADR (d) 段需 supersede。
5. **HTTP / Streamable HTTP transport 變為必要**：v2 規劃 remote MCP（如 web-based copilot）→ 重審 (a)，可能改 A2 或多 SDK 並列。
6. **官方 SDK 出現 breaking change**：違反 v1 stability 承諾 → 全面重審。

## 8. More Information

* MCP server 行為與 tool description 強制句式：`docs/0ops-plan.md`「MCP server」段。
* 寫入類兩階段（`*_preview` / `*`）的 backend 強制：[ADR-0002 Idempotency 與副作用補償](0002-idempotency-and-compensation.md) 第 4 節。
* `team_slug` 為所有 write tool 的必填 arg（避免 LLM 預設打錯 team）：[ADR-0001 多租戶模型與 RBAC](0001-multi-tenancy-and-rbac.md) 第 4 節。
* M0 spike 結果落地檔：`docs/runbooks/mcp-sdk-spike-results.md`（M0 結束前產生）。
* 對應 skill packs（`skills/claude-code/0ops/`、`skills/codex/0ops/`、`skills/copilot/0ops/`）需在 SKILL.md 與本 ADR 同步引用同一段 description 強制句式。

## 9. Open Questions

下列問題不阻擋本 ADR 通過，但需在 M0 spike 內敲定：

1. **官方 SDK streaming API 的具體形態**：是 generator-style、callback、還是 chunked tool result？影響 `tail_logs` 實作。
2. **Description reflective 掃描可行性**：官方 SDK 是否暴露 `Server.Tools()` 或等價列舉 API；若不暴露需在 register 時自行記憶 description 字串。
3. **Tool input/output JSON Schema 來源**：手寫 vs `jsonschema` codegen vs `oapi-codegen` 共享 backend OpenAPI 子集；M0 結束前釘住一條路徑。
4. **Codex / Copilot CLI 對 MCP 1.x 的支援版本**：claude code 已穩定支援；codex / copilot 為 plan 之 TBD（見 plan「TBD」段「Copilot CLI 是否原生支援 MCP」）；spike 矩陣需含此驗證。
5. **MCP binary 的 panic 行為**：stdio binary panic 後 client 端如何感知？是否需要 graceful shutdown + 錯誤 envelope 回傳，還是直接 exit 讓 client 重連？
6. **SDK 升版策略**：v1.x 內次版本升級的測試範圍；是否每次都跑全 spike 矩陣，或僅關鍵路徑。
