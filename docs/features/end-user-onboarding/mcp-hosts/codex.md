# MCP host reference：Codex CLI

> 對應 spec：`docs/features/end-user-onboarding/spec.md` § 4
> 最簡走法：`0ops mcp setup codex`（自動寫入下方 snippet）。
> 本檔留作手動 / 偵錯參考。

## 1. 自動

```bash
0ops mcp setup codex
```

`0ops` 會：

- 偵測 `0ops-mcp` binary 位置
- 讀 `~/.config/0ops/auth.json` 拿 `OPS_HOST`
- 寫 `~/.codex/config.toml` 之 `[mcp_servers.0ops]` section（idempotent；備份原檔）

之後重啟 Codex CLI session。

## 2. 手動

開 `~/.codex/config.toml`（若不存在則建立），加入：

```toml
[mcp_servers.0ops]
command = "/home/<you>/.local/bin/0ops-mcp"
env = { OPS_HOST = "https://api.winshare.tw" }
```

對應欄位同 claude-code（見 `claude-code.md`）。

## 3. 驗證

開新 codex session，輸入：

```
列出 0ops 上的 apps
```

Codex 應呼叫 `list_apps` tool。

## 4. 故障

對齊 `claude-code.md` § 4。
