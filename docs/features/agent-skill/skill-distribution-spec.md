# Feature Spec：skill-distribution（把 skill 送到使用者端）

> **狀態**：draft
> **來源**：PR #174 移除 MCP 後留下的功能缺口——end user 拿不到 skill
> **修訂**：`docs/features/agent-skill/spec.md` § 1「不寫使用者層」與 § 2.2「不含使用者層 skill」
> **依賴**：`end-user-onboarding`、`agent-skill`

## 1. 問題

MCP 移除後，`0ops mcp setup` 那條「寫 `~/.claude.json`、全域生效」的接線一併消失，沒有替代品：

- `scripts/install.sh` 只裝 `0ops` 一個 binary。
- `0ops onboard` 只做 device-flow login，不接任何 agent。
- skill 本體在**本 repo** 的 `.claude/skills/0ops/SKILL.md`，end user 的 repo 裡沒有。

結果：使用者跑完一條 curl，到自己的 repo 對 agent 說「幫我上線」，agent 不知道 0ops 是什麼。
`README.md` 與 `docs/quickstart.md` 宣稱的產品主張目前不成立。

## 2. 結論

新增 `0ops skill install`，把 skill 內容從 binary 內解出並寫到使用者端；`0ops onboard` 在登入後自動呼叫。

- **預設寫使用者層** `~/.claude/skills/0ops/SKILL.md`——這才是 MCP 時代全域接線的等價物，
  使用者在任何 repo 都能觸發。
- **`--project` 寫專案層** `<dir>/.claude/skills/0ops/SKILL.md`，給「只想讓這個 repo 認得 0ops」的情境。
- skill 內容以 `go:embed` 編進 binary，隨 release 分發，不依賴使用者 clone 本 repo。

### 2.1 修訂既有決策

`agent-skill` spec § 1 寫「不寫使用者層，避免與他人機器設定衝突」。該決策的適用範圍收斂為
**repo 內那一份**：它服務本 repo 的貢獻者，仍不自動安裝。使用者層安裝改為 end user 的明示動作
（`skill install`，或 `onboard` 代跑），衝突風險以 § 4 的 idempotency 與備份處置。

## 3. 單一事實來源

canonical 檔案是 repo 根的 `.claude/skills/0ops/SKILL.md`（Claude Code 在本 repo 內讀它，
`skill_contract_test.go` 也對它斷言）。

`go:embed` 不能跨出 package 目錄，故另存一份 `src/internal/cli/skillasset/SKILL.md` 供嵌入，
並以 `TestEmbeddedSkillMatchesRepoSkill` 斷言兩者 byte-identical，杜絕漂移。
`./manage.sh skill-sync` 負責同步。

## 4. `0ops skill install` 介面

```
0ops skill install
  [--project <dir>]    寫 <dir>/.claude/skills/0ops/SKILL.md（dir 預設 .）；不給則寫 ~/.claude/skills/
  [--print-only]       只印內容與目標路徑，不寫檔
```

目標存在且內容不同時一律先備份再覆寫，故不設 `--force`。
`skill install` 列入 `skill_contract_test.go` 的 human-only 清單：安裝 agent 自己的護欄檔
屬人類的設定動作，agent 不得代跑。

行為：

1. 解析目標路徑；`mkdir -p` 其目錄（0700）。
2. 目標不存在 → 寫入，印 `installed`。
3. 目標存在且內容相同 → 不寫檔，印 `already up-to-date`（idempotent）。
4. 目標存在且內容不同 → 備份到 `<path>.bak.<YYYYMMDDThhmmssZ>` 後寫入，印備份路徑。
5. 寫入先落臨時檔再 `rename`，故失敗（權限 / disk full）不留半成品，原檔保持原樣，exit 非 0。

## 5. `0ops onboard` 串接

登入成功後自動跑使用者層安裝；`--skip-skill` 可略過。安裝失敗**不**讓 onboard 失敗——
登入已完成，skill 可事後補——但必須印出補跑指令。

## 6. 不包含（YAGNI）

- Codex / Copilot 的等價設定：兩者的 skill 機制未定型，維持手動。
- `0ops skill uninstall`：刪一個檔案，手動即可。
- 版本比對與自動升級：使用者升級 binary 後重跑 `skill install` 即覆寫（有備份）。

## 7. 驗收

1. 乾淨 `HOME` 下跑 `0ops skill install` → `~/.claude/skills/0ops/SKILL.md` 存在且與 repo 版本相同。
2. 連跑兩次 → 第二次回 `already up-to-date`，不產生備份。
3. 手改目標檔後再跑 → 產生 `.bak.<ts>`，目標回到 canonical 內容。
4. `--project` 寫到指定目錄，不碰 `$HOME`。
5. `--print-only` 不落檔。
6. `0ops onboard <host> --skip-login` → 仍完成 skill 安裝。
7. `TestEmbeddedSkillMatchesRepoSkill`：嵌入內容與 repo 檔一致。
