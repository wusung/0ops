# 0ops Agent Guide

> 狀態：draft
> 來源：`docs/0ops-plan.md`
> 目的：提供代理與貢獻者一致的執行規範，避免偏離既定架構與安全模型。

## 1. 文件定位

本文件不是產品規格書，也不是完整架構說明。
本文件只定義實作與修改 `0ops` 專案時必須遵守的工作規則。

遇到以下情況時，優先順序如下：

1. 使用者明確要求
2. 本文件
3. `docs/0ops-plan.md`
4. 其他推測或慣例

若本文件與 `docs/0ops-plan.md` 衝突，應回頭修正文檔，而不是自行發明新方向。

## 2. 專案目標邊界

`0ops` v1 是一個 **CLI / MCP-first** 的內部 PaaS 控制台。

允許的主要介面：

- `0ops` CLI
- `0ops-mcp` stdio MCP server
- `0ops-server` REST/SSE backend

v1 明確不做：

- Web UI
- 多服務 compose stack
- 客戶自帶 TLS 憑證
- 帳單與配額
- 多分支 preview deployment
- backend 內建 LLM agent

若需求落在上述範圍，應標記為 `v2` 或明確挑戰需求，不可直接實作。

## 3. 技術方向

### 3.1 語言與元件

- 後端語言固定為 Go
- HTTP framework 固定為 `chi`
- CLI 固定為 `cobra`
- MCP server 使用 stdio transport
- 資料庫固定為 Postgres
- SQL 存取以 `sqlc + pgx` 為主

### 3.2 命名約束

- Go module path 不可用數字開頭；專案內部名稱使用 `zeroops` / `server` / `cli` / `mcp` / `shared`
- binary 輸出名稱才使用 `0ops`、`0ops-mcp`、`0ops-server`
- app slug 在 team 內唯一，不可假設全域唯一
- 所有 team 範圍資源必須以 `team_slug` 或 `team_id` 作為第一層邊界

## 4. Repo 結構規則

預期結構以 `docs/0ops-plan.md` 定義為準，核心責任如下：

- `cmd/server`：backend binary 入口
- `cmd/cli`：CLI binary 入口
- `cmd/mcp`：MCP server binary 入口
- `internal/server`：API、middleware、service、preview gate、auth、db
- `internal/cli`：CLI command、interactive confirm、輸出格式
- `internal/mcp`：tool registry、backend client、auth cache
- `internal/shared`：共用 DTO 與 preview schema
- `migrations`：schema migration
- `deploy`：chart、gitops、workflow
- `skills`：AI CLI 整合文件與設定
- `docs`：規格、架構、操作文件

新增程式碼時，優先放入既有責任邊界。不要用 `utils`、`misc`、`common` 這類模糊資料夾逃避設計。

## 5. API 與安全模型

### 5.1 兩階段寫入是硬性規則

所有寫入與刪除操作必須採用：

1. `*:preview`
2. `confirm`

禁止：

- 直接在單一 endpoint 執行破壞性操作
- CLI 用 `--yes` 繞過 preview
- MCP tool 將 preview/confirm 合併為單一步驟

允許：

- `--yes` 只略過互動確認，但仍必須先呼叫 preview，再立刻 confirm
- `--dry-run` 只執行 preview

### 5.2 Idempotency

- `preview_id` 同時扮演 idempotency key
- confirm 重試必須回傳上次結果，不得重做副作用
- `(team_id, idempotency_key)` 必須唯一

### 5.3 多租戶隔離

必須同時滿足兩道防線：

1. 所有 sqlc query 以 `team_id` 為必要條件
2. HTTP middleware 依序完成 `AuthBearer -> ResolveTeam -> CheckMembership -> CheckTokenScope`

禁止：

- 定義不帶 `team_id` 條件的 team-scoped query
- 只依賴前端或 CLI 傳遞 team 資訊，不做 server 端驗證
- 用 user 身分取代 team 邊界

### 5.4 權限模型

- 租戶邊界是 `team`
- role 固定為 `owner / admin / member / viewer`
- token scope 與 role 是正交關係
- 實際權限是 `role × scope` 的交集

## 6. CLI 與 MCP 規範

### 6.1 CLI

- 所有寫入命令預設先 preview，再顯示 `action_summary` 與 `side_effects`
- 輸出支援 `table / json / yaml`
- `0ops teams use <slug>` 只改本地 context，不應觸發 server 寫入

### 6.2 MCP

- 每個 write action 必須拆成 `<action>_preview` 與 `<action>`
- write tool 的 `team_slug` 必填
- 無 token 時，tool 應明確引導先執行 `0ops auth login`
- logging 走 `stderr`，避免污染 stdio protocol

禁止把「由 LLM 呈現 preview 給使用者」這件事省略掉。

## 7. 副作用與收斂

所有外部副作用必須可追蹤、可補償、可收斂。

最低要求：

- deploy/run 類流程必須有狀態機
- 每個階段都要留下 event 與 audit log
- 對卡住的外部流程要有 reconciler
- callback 為主，polling 為退路

副作用順序遵循：

1. 可逆操作
2. 不可逆操作

不要先做 image push 或正式綁定，再回頭補 reversible 準備工作。

## 8. Observability 基線

M2 起必須具備，不可視為「之後再補」：

- `/metrics`
- 結構化日誌
- `trace_id` propagation
- SLO/SLI 可計量欄位

變更任何部署、callback、workflow、domain verify、webhook 流程時，必須一併檢查：

- trace 是否能串起來
- audit log 是否留足夠資訊
- failure classification 是否仍可判讀

## 9. 測試要求

至少覆蓋以下層次：

- `testing` 單元測試
- `httptest` API handler 測試
- DB 整合測試
- CLI / MCP 與 backend DTO contract test

高風險區域必測：

- preview/confirm consume 規則
- idempotent retry
- team 隔離查詢
- role/scope 權限矩陣
- webhook/callback 簽章驗證
- deploy 狀態轉移與 reconciler 收斂

若修改 API DTO、tool schema、CLI output contract，必須同步更新測試與文件。

## 10. 文件更新規則

以下變更不得只改程式碼：

- API 路徑或語意
- preview/confirm 流程
- role 或 scope
- deploy workflow 階段
- domain verify 規則
- repo 結構慣例

至少同步更新：

- `docs/0ops-plan.md` 或其後續拆分文件
- 受影響的操作文件
- 必要的 skill/config 範本

## 11. 實作判斷準則

做決策時，優先檢查：

1. 是否破壞 team 邊界
2. 是否繞過 preview/confirm
3. 是否引入無法補償的副作用
4. 是否讓 CLI、MCP、backend 三者 contract 漂移
5. 是否把 v2 範圍偷渡進 v1

若答案是「會」，停止實作並先修正設計。

## 12. 建議工作方式

對中大型修改，依序執行：

1. 先更新或確認規格文件
2. 再拆 implementation plan
3. 先定義 shared DTO / schema
4. 再實作 backend contract
5. 再接 CLI 與 MCP
6. 最後補 observability、測試、文件

不要先做 CLI 假流程，再補 backend；這會製造雙重規格來源。
