# MCP host reference：Claude Code

> 對應 spec：`docs/features/end-user-onboarding/spec.md` § 4
> 最簡走法：`0ops mcp setup claude-code`（自動寫入下方 snippet）。
> 本檔留作手動 / 偵錯參考。

## 1. 自動

```bash
0ops mcp setup claude-code
# 或：
0ops mcp setup claude
```

`0ops` 會：

- 偵測 `0ops-mcp` binary 位置
- 讀 `~/.config/0ops/auth.json` 拿 `OPS_HOST`
- 寫 `~/.claude.json` 之 `mcpServers."0ops"` entry（idempotent；備份原檔）

之後 **重啟 Claude Code**（或重新載入 MCP servers）即可。

## 2. 手動

開 `~/.claude.json`，在 `mcpServers` 物件下新增：

```json
{
  "mcpServers": {
    "0ops": {
      "command": "/home/<you>/.local/bin/0ops-mcp",
      "env": {
        "OPS_HOST": "https://api.winshare.tw"
      }
    }
  }
}
```

對應欄位：

| 欄位 | 值 |
|---|---|
| `command` | `0ops-mcp` 絕對路徑（installer 預設在 `~/.local/bin/0ops-mcp`） |
| `env.OPS_HOST` | 你的 0ops backend，self-host 例：`https://api.<your-domain>` |

`0ops-mcp` 會讀 `~/.config/0ops/auth.json` 拿 bearer token（device flow login 後自動產生）。

## 3. 驗證

```bash
# Claude Code 重啟後，在 MCP server 列表應看到 "0ops" connected
# 然後在 Claude Code 內試：
"幫我列出 0ops 上的 apps"
# 預期 Claude 呼叫 list_apps tool 並回 user 的 app 列表
```

## 4. 故障

| 症狀 | 排查 |
|---|---|
| MCP server 列表沒看到 "0ops" | claude code log 看是否報 `command not found`；確認 `command` 路徑可執行 |
| Tool 呼叫回 `unauthorized` | 跑 `0ops auth login` 確認 `~/.config/0ops/auth.json` 存在；重啟 Claude Code 才會重讀 |
| Tool 呼叫 timeout | 確認 `OPS_HOST` 可達；`curl $OPS_HOST/health` 應回 200 |
