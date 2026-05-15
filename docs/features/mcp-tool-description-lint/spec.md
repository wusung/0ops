# Feature Spec：mcp-tool-description-lint

> **狀態**：draft
> **來源**：`docs/0ops-plan.md`「MCP server」「Tool description 強制約定」「Skill packs」段；ADR-0003（MCP SDK 選型）；ADR-0002（兩階段寫入強制）
> **適用範圍**：`0ops-mcp` binary 啟動時對自身 tool description 的 lint、SKILL.md 範本與 verbatim 同步、三家 AI CLI 相容性矩陣產出格式
> **對應 Milestone**：M1（read-only tools 上線時即必須有 lint；M0 spike 結果決定 streaming 路徑）

## 1. 結論（先讀本段）

- MCP server 啟動時 lint 自身 tool description；違反任一句式立即 `os.Exit(2)` 並印明確錯誤；不允許 dev override
- Lint 規則三條：
  1. tool 名稱以 `_preview` 結尾 → description 必含 verbatim 子字串 `ALWAYS call this BEFORE`
  2. tool 名稱對應寫入 / 刪除 action（見 § 4.3 列表）且**非** `_preview` → description 必含 verbatim 子字串 `NEVER call this tool without`
  3. tool description 必含 `team_slug`（write tool 之 input schema 必含 required `team_slug`，由本 spec § 4.4 釘）
- SKILL.md 範本以「源檔 + verbatim 同步腳本」管理：`skills/_template/preview-confirm-clauses.md` 為 source of truth；三家 SKILL.md 透過 `make skills-sync` 重新嵌入；CI lint 偵測 drift
- 三家 AI CLI 相容性矩陣產出於 `docs/runbooks/mcp-sdk-spike-results.md`；矩陣 5×3（5 條代表性流程 × 3 家 CLI）；任一格紅燈即觸發 ADR-0003 Revisit
- M0 spike 範圍：streaming API 形態（ADR-0003 OQ#1）、reflective tool 列舉（OQ#2）、JSON Schema 來源（OQ#3）；spike 結果落地於 runbook 後本 spec 第 6 段以 PR 補入具體 API 名
- Tool registry 為靜態註冊：`internal/mcp/registry.go` 在 `init()` 集中註冊；新增 tool 必同步加入 registry table 與 lint 測試

## 2. 範圍

### 2.1 包含
- `internal/mcp/lint/` package：startup-time description lint 規則與執行
- `internal/mcp/registry.go`：tool 靜態註冊與 reflective 列舉接口
- SKILL.md 範本與 verbatim 同步機制（`skills/_template/`、`make skills-sync`、CI drift 檢測）
- 三家 AI CLI 相容性矩陣：5 條代表性流程 × 3 家 CLI 之測試項目定義、結果落地檔位置、紅燈 → Revisit 流程
- Tool 命名規則（`<action>` / `<action>_preview`）與 input schema 必填欄位
- 失敗時 `os.Exit(2)` 與錯誤訊息格式
- M0 spike 之 ADR-0003 OQ#1–3 結果回填本 spec 的接口

### 2.2 不包含
- MCP transport 設定本身（屬 `cmd/mcp/main.go` 與 ADR-0003 § 4.2）
- Tool 個別實作邏輯（屬各 feature spec）
- backend 對 `<action>` endpoint 缺 `preview_id` 的 4xx 行為（屬 `preview-confirm-gate` spec 與 `error-model` § 5.1）
- Skill pack 完整內容（範例對話、安裝指引）；本 spec 只規範 verbatim clause 部分
- MCP auth 行為（屬 `auth-and-rbac` spec § 8.2）
- Streaming 實作細節（屬 `read-api-vertical-slice` spec：`tail_logs` tool）；本 spec 只釘 spike 結果落地接口

## 3. 檔案結構

```
0ops/
├── cmd/
│   └── mcp/
│       └── main.go                        # 啟動：register → lint → stdio loop
├── internal/
│   └── mcp/
│       ├── registry.go                    # 靜態 tool 表 + Tools() 列舉接口
│       ├── lint/
│       │   ├── rules.go                   # 三條規則 + ApplyAll(tools []Tool) error
│       │   ├── rules_test.go              # 單元測試（含 fail / pass fixture）
│       │   └── doc.go
│       └── tools/
│           ├── list_apps.go               # 範例 read tool
│           ├── create_app_preview.go      # 範例 *_preview tool
│           ├── create_app.go              # 範例寫入 tool
│           └── ...
├── skills/
│   ├── _template/
│   │   ├── preview-confirm-clauses.md     # source of truth：兩段 verbatim clause（中英文）
│   │   └── README.md                      # 同步機制說明
│   ├── claude-code/
│   │   └── 0ops/
│   │       ├── SKILL.md                   # 含 <!-- @sync:preview-confirm --> ... <!-- @end --> 區段
│   │       └── mcp-config.json
│   ├── codex/
│   │   └── 0ops/
│   │       ├── SKILL.md
│   │       └── codex-config.toml.snippet
│   └── copilot/
│       └── 0ops/
│           ├── SKILL.md
│           └── README.md
├── scripts/
│   └── skills-sync.sh                     # 從 _template 嵌入到三家 SKILL.md
├── docs/
│   └── runbooks/
│       └── mcp-sdk-spike-results.md       # M0 spike 矩陣結果落地（5×3）
└── Makefile                               # target: skills-sync, skills-lint, mcp-lint-test
```

## 4. Lint 規則

### 4.1 規則一：`*_preview` description 必含 ALWAYS clause

**Rule ID**：`R1-preview-always-before`

**檢查**：tool `Name()` 以 `_preview` 結尾 → `Description()` 必含 verbatim 子字串 `ALWAYS call this BEFORE`

**理由**：ADR-0002 § 1 第 1 條與 plan.md「MCP server / Tool description 強制約定」段；LLM 對 `ALWAYS` 全大寫 prompt 遵守率較高（Anthropic 2024 prompt-engineering 公開資料）

**錯誤訊息範例**：
```
[mcp-lint] R1-preview-always-before: tool "create_app_preview" description must contain
the verbatim string `ALWAYS call this BEFORE`. Found:

  Preview side effects of create_app. Returns PlanPreview with action_summary, ...

Fix: 把 plan.md「MCP server / Tool description 強制約定」段第一個範本貼回 description。
```

### 4.2 規則二：寫入 tool description 必含 NEVER clause

**Rule ID**：`R2-write-never-without-preview`

**檢查**：tool `Name()` 命中「寫入 / 刪除 action 列表」（§ 4.3）且**非** `_preview` → `Description()` 必含 verbatim 子字串 `NEVER call this tool without`

**理由**：同上；用 `NEVER` 雙保險 LLM 不繞過 preview

**錯誤訊息範例**：
```
[mcp-lint] R2-write-never-without-preview: tool "create_app" is a write action and must
contain the verbatim string `NEVER call this tool without`. Found:

  Execute create_app using preview_id. Idempotent on the same preview_id.

Fix: 把 plan.md「MCP server / Tool description 強制約定」段第二個範本貼回 description。
```

### 4.3 寫入 / 刪除 action 列表

由 `internal/shared/rbac.Action` 列舉導出（`shared-dto-and-contract` spec § 6.3）。本 spec 列出 v1 對應集合：

| Action | `_preview` tool | `<action>` tool |
|---|---|---|
| create_app | `create_app_preview` | `create_app` |
| update_app | `update_app_preview` | `update_app` |
| delete_app | `delete_app_preview` | `delete_app` |
| redeploy | `redeploy_preview` | `redeploy` |
| add_domain | `add_domain_preview` | `add_domain` |
| remove_domain | `remove_domain_preview` | `remove_domain` |
| invite_member | `invite_member_preview` | `invite_member` |
| remove_member | `remove_member_preview` | `remove_member` |
| install_github_app | `install_github_app_preview` | `install_github_app` |
| uninstall_github_app | `uninstall_github_app_preview` | `uninstall_github_app` |

新增 action 時：先改 `internal/shared/rbac.Action`、再加 tool 檔、registry 註冊、lint 測試 fixture 同步增加。

### 4.4 規則三：write tool input schema 必含 `team_slug`

**Rule ID**：`R3-write-team-slug-required`

**檢查**：tool `Name()` 命中寫入 / 刪除 action（含 `_preview`）→ `Schema()` JSON 之 `properties.team_slug` 存在且 `required` 陣列含 `team_slug`

**理由**：plan.md MCP server 段「寫入類拆兩個 tool」與 ADR-0001 § 4 之 URL routing 約束；MCP tool 若不收 `team_slug`，LLM 預設打錯 team

**錯誤訊息範例**：
```
[mcp-lint] R3-write-team-slug-required: tool "create_app" must require `team_slug` in
input schema. Found schema lacks `team_slug` in `required` array.
```

### 4.5 Read tool 規則

Read tool **不**強制句式；`Description()` 只需 ≥ 30 字、首句以動詞開頭（如 `List `, `Get `, `Inspect `）；對 LLM 友善但不 lint fail。

### 4.6 執行時機

- 入口：`cmd/mcp/main.go` 在 register 完所有 tool 後、進入 stdio loop 前
- 失敗：`fmt.Fprintln(os.Stderr, ...)` 印所有違反規則（不只第一條）+ `os.Exit(2)`
- exit code 2 與 `error-model` § 6.3 之 `bad_request` class 對應；表示「這不是 runtime 錯，是設定錯」

```go
// cmd/mcp/main.go (節錄)
func main() {
    server := newServer()
    registerAllTools(server)
    if errs := lint.ApplyAll(server.Tools()); len(errs) > 0 {
        for _, e := range errs {
            fmt.Fprintln(os.Stderr, e)
        }
        os.Exit(2)
    }
    server.RunStdio()
}
```

### 4.7 Reflective `Tools()` API 依賴

Lint 仰賴 SDK 暴露 `server.Tools()` 或等價函式取得已註冊 tool 與其 description。ADR-0003 OQ#2 列為 spike 項：

- 若官方 SDK 暴露 → 直接用
- 若不暴露 → registry 自行記憶 description，提供 `registry.AllTools() []Tool` 介面（lint 從 registry 取，非 SDK）
- M0 spike 結果落地後本 § 4.7 以 PR 補入具體 API 名稱

## 5. SKILL.md verbatim 同步

### 5.1 為何需要

ADR-0003 § 4 第 8 點與 plan.md「Tool description 強制約定」段要求 SKILL.md 內也重述同一段 verbatim，雙保險。問題是三份 SKILL.md 各自手寫易漂移，需自動同步。

### 5.2 Source of truth

`skills/_template/preview-confirm-clauses.md`：

```markdown
<!-- This file is the source of truth for the preview/confirm verbatim clauses
     embedded in skills/{claude-code,codex,copilot}/0ops/SKILL.md.
     Run `make skills-sync` after editing. CI fails on drift. -->

## EN clause

For any write or delete tool (`create_app`, `delete_app`, `redeploy`, `add_domain`,
`remove_domain`, `update_app`, `invite_member`, `install_github_app`,
`uninstall_github_app`):

1. ALWAYS call the matching `<action>_preview` tool first.
2. Show the user the `action_summary` and the FULL `side_effects` list.
3. Wait for explicit approval ("yes" / "go" / "確認"). Anything ambiguous = rejection.
4. Only then call `<action>` with the returned `preview_id`.
5. NEVER call `<action>` without a fresh, user-approved `preview_id`. The backend
   will reject with HTTP 4xx and you should surface the error verbatim.

## ZH clause

對任何寫入或刪除類 tool（同上列表）：

1. 一律先呼對應的 `<action>_preview` tool。
2. 把 `action_summary` 與**完整** `side_effects` 列表呈現給使用者。
3. 等待使用者明確同意（"yes" / "go" / "確認"）。語焉不詳即視為拒絕。
4. 同意後才以回傳的 `preview_id` 呼 `<action>`。
5. 絕不在無 preview_id 或 preview_id 過期時直接呼 `<action>`；backend 會以 HTTP 4xx 拒絕，請原樣顯示錯誤訊息。
```

### 5.3 嵌入 marker

每份 `SKILL.md` 內含：

```markdown
<!-- @sync:preview-confirm-en -->
... EN clause 自動覆寫區 ...
<!-- @end -->

<!-- @sync:preview-confirm-zh -->
... ZH clause 自動覆寫區 ...
<!-- @end -->
```

### 5.4 同步腳本

`scripts/skills-sync.sh`：
- 讀 `skills/_template/preview-confirm-clauses.md`
- 對三份 SKILL.md：用 `awk` / `sed` 在 `@sync:*` 與 `@end` 之間替換內容
- exit 0 = 已同步；非 0 = 無 marker 或讀檔失敗

`make skills-sync` 呼叫此腳本。

### 5.5 CI drift 檢測

`make skills-lint` 步驟：
1. 把當前 SKILL.md 暫存
2. 跑 `skills-sync.sh`
3. `git diff --exit-code skills/`
4. 非 0 → fail；提示 `跑 make skills-sync 後 commit`

CI 在 `.github/workflows/lint.yml` 觸發此 target。

## 6. 三家 AI CLI 相容性矩陣

### 6.1 5 條代表性流程

| 編號 | 流程 | 涉及 tool | 驗證點 |
|---|---|---|---|
| F1 | 列出 apps | `list_apps` | tool 註冊成功、回傳 JSON 可解析 |
| F2 | 建立 app（含 preview/confirm） | `create_app_preview` + `create_app` | LLM 是否確實先呼 preview、是否呈現 `side_effects`、是否取得使用者同意 |
| F3 | log follow | `tail_logs` | streaming（B1）或分頁（B3）UX 可用 |
| F4 | 刪 app（含 preview/confirm） | `delete_app_preview` + `delete_app` | 同 F2，加上 LLM 是否對 `delete` 動詞額外提醒 |
| F5 | error 呈現 | 任一 tool 故意觸發 401 / 403 / 422 | LLM 是否原樣顯示 envelope 之 `code` + `trace_id` |

### 6.2 矩陣格式（runbook）

`docs/runbooks/mcp-sdk-spike-results.md` 模板：

```markdown
# MCP SDK Spike Results（M0）

| Flow ↓ \ CLI → | Claude Code | Codex CLI | Copilot CLI |
|---|---|---|---|
| F1 list_apps | ✅ 通過 | ✅ 通過 | ⚠️ 部分（見備註） |
| F2 create_app | ✅ 通過 | ⚠️ LLM 未呈現完整 side_effects | ❌ 不支援 MCP（走 wrap CLI fallback） |
| F3 tail_logs | ✅ streaming | ❌ 分頁 fallback | ❌ wrap CLI |
| F4 delete_app | ✅ 通過 | ⚠️ 同 F2 | ❌ wrap CLI |
| F5 error envelope | ✅ 原樣顯示 | ✅ 原樣顯示 | ⚠️ wrap 後僅顯示 stdout |

## 備註

- Codex F2 / F4：LLM 把 `side_effects` 摘要為「會建立資源」而非完整列出；屬 LLM 行為而非 SDK 缺陷。SKILL.md 加強 prompt 措辭。
- Copilot：截至 spike 日（YYYY-MM-DD）尚未原生支援 MCP，採 plan「Skill packs / Copilot CLI」之 wrap CLI fallback。

## 紅燈處置

- F2 / F4 在 Codex 之 ⚠️：透過 SKILL.md 改善描述後重測；30 天內未升 ✅ → 升級為 ❌ 並觸發 ADR-0003 Revisit。
- Copilot 全行 ❌：屬已知非主路徑；plan.md TBD 已標 fallback；不觸發 Revisit。
```

### 6.3 紅燈閾值與 Revisit Trigger

- 任一 ✅ → ❌ 退化（已通過後失敗）：立即觸發 ADR-0003 Revisit Trigger #1
- 任一 ⚠️ 持續 30 天未升級：升 ❌ 並觸發 Revisit
- 矩陣每次 SDK 升版（v1.x → v1.y）必重跑 F1–F5；結果 PR 更新 runbook
- F3 streaming → 分頁 fallback：屬 ADR-0003 第 6 點 spike 結果，不視為紅燈；於 NPS / dashboard 觀察 30 天再決定是否觸發 Revisit Trigger #3

## 7. 啟動失敗 UX

```
$ 0ops-mcp
[mcp-lint] R1-preview-always-before: tool "create_app_preview" description must contain
the verbatim string `ALWAYS call this BEFORE`.

[mcp-lint] R2-write-never-without-preview: tool "delete_app" is a write action and must
contain the verbatim string `NEVER call this tool without`.

[mcp-lint] 2 violations. Refer to docs/0ops-plan.md "MCP server / Tool description 強制
約定" section for the verbatim templates. Aborting startup.
```

- exit 2
- claude code / codex / copilot 端會看到 stdio 立即關閉；實際呈現依 host 而異
- 不嘗試 fallback 啟動（一旦 description 出錯，產品安全模型即破）

## 8. 與其他 spec 的接合點

| 接合點 | 對應 spec | 內容 |
|---|---|---|
| Tool input schema 必含 `team_slug` | `shared-dto-and-contract` § 6 | `rbac.Action` 與 schema 同步 |
| Description 範本字串 source | `docs/0ops-plan.md`「MCP server / Tool description 強制約定」段 | 本 spec 引用，不重述 |
| 寫入 tool 之 backend 4xx 行為 | `preview-confirm-gate` spec、`error-model` § 5.1 | lint 規則只保證 description；行為由 backend 強制 |
| MCP auth 失敗訊息 | `auth-and-rbac` spec § 8.2 | 引導使用者跑 `0ops auth login` |
| `tail_logs` streaming 形態 | `read-api-vertical-slice` spec | spike 結果決定後，本 spec § 6.1 F3 列為 streaming 或分頁 |

## 9. 驗證準則

| 驗證項 | 方式 | 通過條件 |
|---|---|---|
| Lint 規則單元測試 | `go test ./internal/mcp/lint/...` | 三條規則對 fail / pass fixture 各 100% 偵測 |
| MCP server 啟動 lint 通過 | `0ops-mcp` 啟動 | 不 exit、進 stdio loop |
| Lint 失敗 → exit 2 | 故意改 description 違反 R1 | 啟動印錯誤、exit code = 2 |
| Action 列表覆蓋 | 對照 `internal/shared/rbac.Action` 與 § 4.3 表 | 兩者完全相符（測試讀 enum 比對） |
| `team_slug` schema 必填（R3） | 對 9 個 write action 共 18 個 tool 之 Schema 解析 | 全部含 `team_slug` 為 required |
| SKILL.md 同步 | `make skills-sync && git diff` | 無變動 |
| SKILL.md drift 偵測 | 故意改 SKILL.md 但不跑 sync | `make skills-lint` fail，輸出包含 `跑 make skills-sync` |
| 矩陣 runbook 存在 | M0 結束時 | `docs/runbooks/mcp-sdk-spike-results.md` 存在且 5×3 表完整填值 |
| Reflective Tools API | M0 spike | OQ#2 結果落地、PR 更新本 spec § 4.7 |
| Read tool 不被 R1/R2 誤觸 | `list_apps` description 不含強制句式 | lint 通過 |

## 10. 對 `docs/0ops-plan.md` 的修改清單

1. 「MCP server / Tool description 強制約定」段：交叉引用本 spec § 4 為 lint 規則 source of truth；plan 的範本字串為唯一允許 verbatim
2. 「Skill packs」段：補入「三份 SKILL.md 之 verbatim 段透過 `make skills-sync` 由 `skills/_template/preview-confirm-clauses.md` 產生；CI lint 偵測 drift」
3. 「TBD」段：交叉引用本 spec § 6 矩陣與 § 4.7 reflective API 為 M0 spike 落地點
4. 「Verification / 整合」段：補「MCP description lint」「skills sync drift」兩項 CI gate

## 11. Open issues

> 來源：ADR-0003 § 9 之 6 條 OQ + 本 spec 撰寫期間新發現

- ADR-0003 OQ#1（streaming API 形態）：spike 後 PR 更新本 spec § 6.1 F3 與 `read-api-vertical-slice` spec
- ADR-0003 OQ#2（Reflective tool 列舉 API）：spike 後 PR 更新本 spec § 4.7
- ADR-0003 OQ#3（JSON Schema 來源）：本 spec 採「手寫」；spike 後若選 codegen，需另起 spec
- ADR-0003 OQ#4（Codex / Copilot 對 MCP 1.x 支援版本）：spike 矩陣（§ 6）落地
- ADR-0003 OQ#5（MCP binary panic 行為）：lint exit 2 屬啟動失敗；runtime panic 行為待 spike
- ADR-0003 OQ#6（SDK 升版策略）：v1.x 次版本升級的測試範圍；建議「每次升版重跑 F1–F5」，於 release runbook 列入
- 中文 LLM（如國內 GLM / Qwen 接 claude code）對 `ALWAYS` / `NEVER` 全大寫 prompt 的遵守率：v1 不測；M2 後若有需求補
- Tool description 是否需 i18n（同時提供英文與中文版）：v1 採英文（與 plan.md 範本一致）；國內 LLM 接入時再評估

## 12. 不可違反的硬性規則

> 違反以下任一項，PR 不可合入。

1. `0ops-mcp` 啟動時必跑 lint；無 lint 直接進 stdio loop 不可
2. lint 違反任一規則必 `os.Exit(2)`；不允許 dev override（無 `--skip-lint` flag）
3. `*_preview` tool description 必 verbatim 含 `ALWAYS call this BEFORE`；非 preview 寫入 / 刪除 tool 必 verbatim 含 `NEVER call this tool without`
4. 寫入 / 刪除 tool 之 input schema 必 required `team_slug`
5. 三家 SKILL.md 之 `@sync:preview-confirm-{en,zh}` 區段內容必由 `make skills-sync` 從 `skills/_template/preview-confirm-clauses.md` 產生；CI drift 即 fail
6. 新增 action 必同步：`rbac.Action` 列舉 + `<action>` / `<action>_preview` tool 檔 + `internal/mcp/registry.go` 註冊 + 本 spec § 4.3 表 + lint 測試 fixture
7. 矩陣 runbook 任一 ✅ 退化為 ❌ 必觸發 ADR-0003 Revisit；不允許在 runbook 改色而不開 ADR review
8. plan.md「Tool description 強制約定」段之範本字串為 verbatim source；description 之強制句式不得再就地改寫
