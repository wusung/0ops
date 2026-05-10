# 0ops Contribution Rules

## Purpose

本文件定義 `0ops` 專案的貢獻規範。
只聚焦三類規則：

- 測試
- 提交
- 文件

命名、目錄結構、租戶邊界等架構級規則由 `docs/adrs/*.md` 與 `docs/0ops-plan.md`
落地，本文件不重述。

若與其他文件衝突，優先順序如下：

1. 使用者明確要求
2. 本文件
3. `docs/adrs/*.md`（已接受之 ADR）
4. `docs/0ops-plan.md`

ADR 之所以高於 plan：plan 為長期演進的廣泛規劃文件，可能殘留 ADR 已 supersede
的措辭；正式決策以 ADR 為準。

## Document Reading Order

閱讀專案文件時，固定依以下順序進行：

1. `docs/0ops-business-plan.md`
2. `docs/0ops-plan.md`
3. `docs/agents-guide.md`
4. `docs/adr-reading-strategy.md` ⭐ **必讀**（定義 ADR 讀取時機與深度）
5. `docs/adrs/*.md`
6. `tasks/todo.md`（完成的 task 需先查閱，確認收尾狀態與後續項目）
7. `tasks/lessons.md`（經驗談／複盤參考）

### ADR 讀取策略

ADR 為不可違反的架構決策。讀取分三層深度：

- **Skim**（<1 分鐘，TL;DR Only）：低風險修改、模組內部重構、簡單 bug 修復
- **Read**（3-5 分鐘，完整 ADR）：新 API、schema 變更、跨模組影響
- **Deep**（5-10 分鐘，Consequences + Open Questions）：架構變更、權限邏輯、重大決策變更

**讀法規約**：

- **每次 agent 啟動前**：依 `docs/adr-reading-strategy.md` 第 1 節「讀取時機決策矩陣」判斷深度
- **識別相關 ADR**：使用第 2 節「快速參考表」快速定位涉及 ADR（無需依序讀全部）
- **依檔名數字順序深讀**：當需 Deep 讀時，若多個 ADR 涉及應按 0001 → 0002 → ... 順序
  （後者可能依賴前者；例：ADR-0008 之 leader 職責源自 ADR-0002 冪等性決策）
- **實作或修改時**：發現違反 ADR 立即停止 → 深讀該 ADR Consequences 與 Open Questions → 
  評估是否應新增 ADR 而非違反現有決策
- 新增 ADR 採 MADR 9-section 結構並接續編號；ADR 之間有跨引用時必須在 More
  Information 段顯式列出；新 ADR 應同步更新 `adr-reading-strategy.md` 第 2 節

若當前任務有對應 `{FEATURE}`，完成共用文件閱讀後，必須一併閱讀以下兩類文件：

1. `docs/features/{FEATURE}/*.md`
2. `docs/features/{FEATURE}/release/**.md`

```mermaid
flowchart TD
    A["docs/0ops-business-plan.md"]
    B["docs/0ops-plan.md"]
    C["docs/agents-guide.md"]
    D["docs/adrs/0001 → 0008<br/>(依檔名數字順序)"]
    E["docs/features/{FEATURE}/*.md"]
    F["docs/features/{FEATURE}/release/**.md"]

    A --> B --> C --> D
    D --> E
    D --> F
```

## Testing

任何行為變更都必須補對應測試。最低要求：

- 單元測試：`testing`
- API handler 測試：`net/http/httptest`
- DB 整合測試
- CLI / MCP 與 backend DTO contract test

高風險區域必測：

- preview / confirm 流程
- idempotent retry
- team 隔離
- role / scope 權限矩陣
- webhook 或 callback 簽章驗證
- deploy 狀態轉移與 reconciler 收斂

若修改以下項目，不可只改程式不補測試：

- API request / response DTO
- MCP tool schema
- CLI output contract
- migration
- middleware 權限邏輯

## Commits

- 提交必須單一目的，避免把重構、格式化、功能修改混成一筆。
- Commit message 使用命令式、具體描述結果，不寫空泛訊息。
- 若變更會影響 API、schema、workflow 或文件，必須在同一批提交中一併處理，不要留技術債到下一筆。
- 不可提交與當前任務無關的順手修正。
- 不可用提交訊息取代文件；系統行為改變仍要更新文件。

建議格式：

- `feat: add app create preview flow`
- `fix: enforce team scope in app queries`
- `docs: clarify MCP confirm contract`
- `test: cover deploy callback signature validation`

避免：

- `update`
- `fix stuff`
- `wip`
- `misc changes`

## Documentation

文件是規格來源的一部分，不是收尾附件。

以下變更必須同步更新文件：

- API 路徑或語意
- preview / confirm 規則
- 目錄責任分工
- role / scope 權限定義
- deploy workflow 階段
- domain verify 規則
- 安裝或整合方式

優先更新位置：

- `docs/0ops-plan.md`
- `docs/` 下對應主題文件
- `skills/` 內相關整合說明或設定片段

新增文件時遵守：

- 先寫結論，再寫限制
- 用可驗證描述，避免口語化模糊句
- 避免同一規則散落多份文件而互相漂移
- 若新增文件是某份主文件的提煉版，需明確標示來源

## Review Heuristics

提交前自查：

1. 命名是否反映實際責任
2. 檔案是否放在正確邊界
3. 測試是否覆蓋改動風險
4. commit 是否單一目的且可審查
5. 文件是否與實作同步

任一答案為否，先修正再提交。
