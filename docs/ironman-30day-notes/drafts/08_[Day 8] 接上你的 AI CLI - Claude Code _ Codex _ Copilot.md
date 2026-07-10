# [Day 8] 接上你的 AI CLI - Claude Code / Codex / Copilot

- 原文連結: （未發佈）
- 發布時間:

---

前言

昨天 [Day 7] 我們用一條 `curl` 把 0ops 裝好、登入，也驗證了 `0ops teams list` 能回東西。那條安裝指令其實已經順手把 0ops 接上偵測到的 AI CLI 了——但「接線」到底做了什麼、寫到哪裡，筆者覺得值得單獨拆開講。因為 Day 6 聊過，0ops 的另一個入口是 **MCP**：讓 Claude Code、Codex 這類 agent 直接呼叫 0ops 的部署工具。這條線沒接好，你在對話裡說「幫我部署」時，agent 根本看不到那些工具——這也是筆者一開始最容易忽略的地方。

今天筆者想跟大家一起做三件事：

1. 手動把 0ops 接到 Claude Code / Codex / Copilot（如果安裝時沒接、或你想重接）；
2. 搞懂設定寫到哪個檔、`--print-only` 怎麼先預覽再決定；
3. 驗證 agent 真的看得到 0ops 的工具。

一個提醒先放在最前面：**接完一定要重啟 AI CLI**，不然新設定不會被讀到。

手動接線：一個 host 一個指令

接線的指令是 `0ops mcp setup <host>`，一個 AI CLI 對應一個子命令：

```sh
$ 0ops mcp setup claude-code     # 寫進 ~/.claude.json 的 mcpServers."0ops"
$ 0ops mcp setup codex           # 寫進 ~/.codex/config.toml 的 [mcp_servers.0ops]
$ 0ops mcp setup copilot-cli     # 只印手動步驟，不自動寫檔
```

三者的差異筆者覺得值得記一下：

- **claude-code**：把一個名為 `0ops` 的 MCP server 條目寫進 `~/.claude.json` 的 `mcpServers`。
- **codex**：寫進 `~/.codex/config.toml`，section 是 `[mcp_servers.0ops]`（TOML 格式）。
- **copilot-cli**：目前**不自動改你的設定檔**，只把你該手動貼的步驟印出來，讓你自己完成。這是刻意保守——Copilot 的整合還在手動階段。

執行 claude-code 的接線，輸出大概像這樣：

```sh
$ 0ops mcp setup claude-code
Detected Claude Code config: ~/.claude.json
Adding MCP server "0ops" -> 0ops-mcp (stdio)
Wrote ~/.claude.json

Done. Restart Claude Code for the change to take effect.
```

注意最後那句——它自己就在提醒你重啟。

先預覽不寫檔：--print-only

如果你不放心讓它直接動你的設定檔，筆者自己也偏好先用 `--print-only` 看它「打算寫什麼」，但不真的落地：

```sh
$ 0ops mcp setup claude-code --print-only
Would add to ~/.claude.json:

  "mcpServers": {
    "0ops": {
      "command": "0ops-mcp",
      "type": "stdio"
    }
  }

(dry run — no file written)
```

確認內容 OK 了，再拿掉 `--print-only` 真正寫入。這個設計呼應 Day 4 的原則：整合這種會動你既有設定的操作，要能**先預覽、可回復**，而不是直接改了再說。

如果你更想一次把「登入 + 接 AI CLI」串起來，也可以用 `0ops onboard <host>`。它就是 Day 7 那條 `curl` 背後跑的 onboarding 流程，幾個好用旗標：

- `--skip-login`：只接 MCP，不重跑登入；
- `--skip-mcp`：只登入，不接 AI CLI；
- `--hosts`：指定要接哪些 AI CLI（例如只接 claude-code）；
- `--mcp-binary`：指定 `0ops-mcp` binary 的路徑；
- `--yes`：跳過互動確認（預設就是 true）。

MCP server 長什麼樣

被寫進設定檔的那個 `0ops` 條目，實際指向的是一支叫 `0ops-mcp` 的 binary，用 **stdio** 跟 AI CLI 溝通。Day 6 說過它暴露 24 個工具（10 個唯讀、7 對兩階段寫入）。AI CLI 啟動時會把這支 binary 拉起來、透過標準輸入輸出跟它對話，agent 就能呼叫 `list_teams`、`create_app_preview` 這些工具。

```mermaid
flowchart LR
    A[你在 Claude Code 說一句話] --> B[Claude Code]
    B -->|stdio| C[0ops-mcp binary]
    C -->|HTTP + 你的 token| D[0ops-server]
```

關鍵在於：agent 走的是這支 `0ops-mcp`，而它用的授權跟你 CLI 用的是同一份（`~/.config/0ops/auth.json`）。所以 Day 6 講的「同一套後端、同一套權限」在這裡具體落地——agent 能碰的東西，不會超過你這個登入身分能碰的。

驗證：agent 真的看得到工具了嗎

接完、**重啟 AI CLI** 之後，最簡單的驗證是叫 agent 呼叫一個唯讀工具。在 Claude Code 裡直接說：

> 用 0ops 列出我的團隊

如果接線成功，agent 會呼叫 `list_teams` 並回給你團隊清單，內容應該跟你在終端機打 `0ops teams list` 一致：

```text
（Claude Code 回覆）
我用 0ops 的 list_teams 查到你有 1 個團隊：
- acme (Acme Inc.) — role: owner, plan: pro
```

看到這個回覆，就代表 0ops 的 MCP 工具已經在 agent 的工具帶裡了。如果 agent 說它沒有 0ops 相關工具，先檢查兩件事：一是有沒有真的**重啟** AI CLI，二是設定檔有沒有寫進去（可以重跑一次 `--print-only` 對照）。這類「MCP 看不到」的問題，Day 25 會給一張完整的排查清單，也會指到各 host 專屬的說明文件 `docs/features/end-user-onboarding/mcp-hosts/<host>.md`。

總結

今天我們把「接 AI CLI」這條線拆清楚了：`0ops mcp setup claude-code / codex / copilot-cli` 各自寫到 `~/.claude.json`、`~/.codex/config.toml`，Copilot 目前只印手動步驟；`--print-only` 讓你先預覽再落地；接完務必重啟 AI CLI，再叫 agent 呼叫 `list_teams` 驗證。整合做到冪等、可預覽、可回復，不破壞你既有的設定——這是 Day 4 那條原則的延伸。

線接好了，但這裡藏了一個安全問題：agent 現在到底能呼叫**哪些**工具？全部 24 個都能碰嗎？明天 [Day 9]，我們來看 0ops 的 deny-by-default 授權模型——預設什麼都不給，你要哪個工具才 grant 哪個。

Q&A

如果你接 Codex 或 Copilot 時遇到跟 Claude Code 不一樣的狀況，歡迎留言分享你的設定檔長怎樣，一起把各 host 的坑補齊 : )

參考連結

- 0ops repo：`src/internal/cli/root.go`（`mcp setup`、`onboard` verb 與旗標）
- 0ops repo：`0ops-mcp`（MCP server binary，stdio）
- `docs/features/end-user-onboarding/mcp-hosts/`（各 AI CLI host 的接線說明）
