# Feature Spec：agent-skill（repo-level Claude Code skill）

> **狀態**：draft
> **來源**：MCP 入口移除（branch `chore/remove-mcp`）後，agent 端唯一入口為 `0ops` CLI；本 spec 定義隨 repo 分發的 Claude Code skill
> **依賴**：`preview-confirm-gate`、`auth-and-rbac`、`audit-export-and-integrity`、`create-app-flow`、`delete-app-flow`
> **取代**：`mcp-tool-description-lint`、`mcp-tool-permissions` 兩 spec 所承載的 agent 護欄職責

## 1. 結論（先讀本段）

- skill 落於 repo 內 `.claude/skills/0ops/SKILL.md`，隨 repo 分發；**這一份**不自動寫使用者層，
  避免與他人機器設定衝突。end user 端的分發另由 `0ops skill install` 承擔，
  見 `docs/features/agent-skill/skill-distribution-spec.md`（修訂本條的適用範圍）。
- skill 只描述「何時觸發、該下哪條 `0ops` 指令、哪些指令禁止 agent 執行、preview/confirm 如何取得人類同意」。不含任何 API 實作、不含 token。
- 護欄由三層承擔：**backend**（preview TTL、actor 綁定、typed-slug guard）、**CLI**（互動 prompt、`--dry-run`）、**skill**（agent 行為規約）。skill 是最弱的一層，因此不得成為唯一防線——任何破壞性能力都必須在 backend 側已具備 preview/confirm。
- MCP 版的 description lint（R1/R2/R3）無對應自動檢查；改以本 spec § 5 的規約與 § 7 的 skill 內容測試固化。

## 2. 範圍

### 2.1 包含
- skill 觸發條件與指令對照表
- preview → confirm 的 agent 行為規約
- agent 禁止執行的指令清單
- 前置條件檢查（`0ops auth status`、`0ops teams list`）與錯誤處置

### 2.2 不包含
- CLI 本身的旗標與輸出格式（屬各 feature spec）
- 後端授權模型（`0ops auth grant/revoke` 的 tool 名稱仍沿用 MCP 時期命名，另案處理）
- 其他 agent（Codex / Copilot）的等價設定；使用者層安裝見 skill-distribution-spec.md

## 3. 觸發條件

使用者意圖涉及下列任一者即載入：部署 repo、查部署狀態或 log、列出/建立/刪除 app、查網域、管理團隊成員、GitHub App 安裝、查 incident 或 audit log。明確提到 `0ops` 亦觸發。

不觸發：一般 Git/建置問題、與 0ops 後端本身開發相關的任務（那屬 `AGENTS.md`）。

## 4. 指令對照

| 意圖 | 指令 | 性質 |
|---|---|---|
| 列出 app | `0ops apps list` | 唯讀 |
| 查單一 app | `0ops apps get <slug>` | 唯讀 |
| 建立並部署 | `0ops apps create --slug <s> --source <path\|github-url> [--ref <r>]` | preview/confirm |
| 刪除 app | `0ops apps delete <slug>` | 破壞性，typed-slug |
| 部署狀態 | `0ops deploys status <slug>` | 唯讀 |
| 部署 log | `0ops deploys logs <slug> [--limit N] [--follow]` | 唯讀 |
| 重新部署 | `0ops deploys redeploy <slug> [--ref r] [--commit-sha sha]` | preview/confirm |
| repo metadata | `0ops repo inspect <slug>` | 唯讀 |
| 網域 | `0ops domains list <slug>` | 唯讀 |
| incident | `0ops incidents list [--status open\|closed\|all]` / `get <id>` | 唯讀 |
| 關閉 incident | `0ops incidents close <id> --note "<root cause>"` | 寫入，需使用者指示 |
| audit | `0ops audit list [--since 24h] [--actor me] [--action create_app]` / `get <id>` | 唯讀 |
| team | `0ops teams list` / `teams use <slug>` | 唯讀 / 本機設定 |
| GitHub App | `0ops teams github status` / `install` / `uninstall` | 唯讀 / preview/confirm |
| 成員 | `0ops members list` | 唯讀 |
| 邀請成員 | `0ops members preview-invite …` → `members invite --preview-id <id>` | 兩階段 |
| 移除成員 | `0ops members preview-remove --user-id <id>` → `members remove --preview-id <id>` | 兩階段，破壞性 |

全域旗標：`--host`、`--team`、`--token`、`--output table|json`（環境變數 `OPS_HOST` / `OPS_TEAM` / `OPS_BEARER_TOKEN` / `OPS_OUTPUT`）。agent 解析結果時一律加 `--output json`。

## 5. Agent 行為規約（對應 MCP lint R1–R3）

- **R1 對應**：任何 preview/confirm 指令，agent 必須先跑 `--dry-run`（`apps create` / `deploys redeploy`）或 `preview-*` 子指令（`members`），把副作用清單呈給使用者，取得明確同意後才執行 confirm。
- **R2 對應**：破壞性指令（`apps delete`、`members remove`、`teams github uninstall`）不得在同一回合內自行完成。agent 不得使用 `--yes` 略過確認；`apps delete` 的 typed-slug 由使用者輸入。
- **R3 對應**：跨 team 操作前必須確認目標 team。agent 在任何寫入前先跑 `0ops teams list`（或讀 `--team` 明示值）並向使用者複述將影響哪個 team。

## 6. 禁止 agent 執行

- `0ops audit verify`：稽核鏈完整性驗證刻意只開放人類操作（`audit-export-and-integrity` spec § 7.3）。agent 可說明如何執行，不得代跑。
- `0ops admin bootstrap-owner`、`0ops admin retry-delete`：一次性 / 殘留清理的管理操作，需人工判斷後執行。
- `0ops auth grant|revoke|logout`、`0ops auth tokens *`：憑證與授權管理。
- `0ops sso status|deprovision`：SSO 設定與集中撤權，owner 專屬。
- 任何帶 `--token` 明文值的指令：憑證由 `auth.json` 或環境變數供應，不得出現在指令列。

## 7. 驗收

- `0ops apps --help` 列出的每個子指令，在 skill 指令對照表中都有對應列（防 CLI 新增指令後 skill 失準）。
- skill 內出現的每條 `0ops` 指令都是實際存在的子指令。
- skill 不含 `mcp` 字樣（MCP 入口已移除）。
- 每個非 human-only 的 CLI 子指令都必須出現在 skill；human-only 清單於測試中明列，新增指令若兩者皆無會紅燈。
- 以上由 `src/internal/cli/skill_contract_test.go` 靜態檢查。
