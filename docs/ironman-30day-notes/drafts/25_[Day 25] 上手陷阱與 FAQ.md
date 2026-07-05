# [Day 25] 上手陷阱與 FAQ

- 原文連結: （未發佈）
- 發布時間:

---

前言

連著兩天的排錯篇（[Day 23]、[Day 24]）處理的是「用到一半卡住」的狀況。但其實更多人卡關的地方，是在**最一開始**——還沒真正用起來，光是裝好、登入、接上 AI CLI 這一段就先撞牆。今天這篇把這些「第一天最容易卡」的上手陷阱與 FAQ 一次收齊，讓你遇到時能一篇查完。

先講一個經驗法則，記住它能省你八成的排查時間：**上手問題絕大多數出在三個地方——PATH、授權、或沒重啟 AI CLI。** 遇到任何上手怪象，先照這三個方向查，再往別處想。

今天要收齊三類 FAQ：

- 安裝階段：`asset not found`、PATH 沒加；
- 登入階段：device flow timeout；
- AI 整合階段：工具回 `unauthorized`、MCP 看不到工具。

安裝階段：裝不起來

**Q：跑 install.sh，回 `asset not found`。**

這通常是自動下載對不到你平台的 release asset。解法是繞過自動下載，直接去 release 頁面手動抓對應你系統的 binary：

前往 `github.com/wusung/0ops/releases`，下載對應你 OS / 架構的檔案，放進 PATH 裡。如果你想指定版本，也可以在跑安裝腳本時用 `OPS_VERSION` 指定（例如 `OPS_VERSION=v0.1.1`），或用 `INSTALL_DIR` 指定安裝位置。

**Q：裝完了，打 `0ops` 卻說 command not found。**

這是 `INSTALL_DIR` 沒有加進 PATH。安裝腳本結束時其實會印出一行「請把這行加到你的 shell rc」的提示——把那行貼進你的 `~/.bashrc` / `~/.zshrc` / `~/.config/fish/config.fish`，重開一個終端機就好。

裝好之後，三條指令驗證一切正常：

```sh
$ 0ops --version
0ops version v0.1.1

$ 0ops auth status
logged in as alice (alice@acme.com) @ https://api.acme.example

$ 0ops teams list
TEAM_SLUG    TEAM_NAME     ROLE     PLAN
acme         Acme Inc.     owner    pro
```

三條都通，代表 binary 在 PATH、登入態有效、也連得上後端。

登入階段：device flow 卡住

**Q：登入時 device flow 一直 timeout，授權不了。**

device flow 的流程是：CLI 印一個 `user_code` 加一個驗證 URL，你去瀏覽器輸入 code 授權，CLI 這邊輪詢等你完成。timeout 通常有兩種原因：

1. **就是超時了**——你太久沒去瀏覽器完成授權。直接重跑登入指令，拿一組新的 code 再來一次：

```sh
$ 0ops auth login --host=https://api.acme.example
```

2. **公司 proxy 擋了 GitHub OAuth**——這個很隱蔽。如果你在公司網路，proxy / 防火牆可能擋掉了到 GitHub OAuth 的連線，導致授權永遠回不來。換個網路（例如手機熱點）試一次，如果通了，就確定是網路環境的問題，得找 IT 開白名單。

AI 整合階段：agent 用不了 0ops

這一類是接上 AI CLI 之後才會遇到的，也是最容易「以為壞了、其實只是沒重啟」的地方。

**Q：AI agent 呼叫 0ops 工具，回 `unauthorized`。**

代表 MCP server 這邊的登入態失效了。解法兩步，**缺一不可**：

```sh
$ 0ops auth login --host=https://api.acme.example
```

然後——**重啟你的 AI CLI**。這一步最常被漏掉。AI CLI（Claude Code / Codex）是在啟動時載入 MCP server 的連線與登入態，你在外面重新登入了，它裡面那份還是舊的，非重啟不會生效。「重新登入 + 重啟 AI CLI」要一起做，只做前半段沒用。

**Q：`unauthorized` 跟 `tool_not_permitted` 是同一件事嗎？**

不是，別搞混：

- **`unauthorized`**：登入態問題——你（或 MCP server）沒有有效的登入。解法是上面說的「重新登入 + 重啟」。
- **`tool_not_permitted`**：授權問題——你登入是有效的，但這個 MCP 工具沒被授權給你用。0ops 的 MCP 工具是 **deny-by-default**（[Day 9] 講過），未授權的工具會回這個。解法是授權那個工具：

```sh
$ 0ops auth grant <tool>
```

（對應地，`0ops auth revoke <tool>` 可以收回授權。）一個是「你是誰沒認出來」，一個是「認出你了但這件事沒開放給你」——分清楚才不會亂投藥。

補一個狀態說明：MCP 工具權限目前是靠登入時的選單 / `grant` / `revoke` 來管理。spec 裡把 **token claim 編碼**與 **invocation-time enforcement**（在每次呼叫的當下即時強制檢查）標為 TODO，屬規劃中，還沒完全落地。所以現階段的權限模型以「授權清單」為主。

**Q：AI CLI 裡根本看不到 0ops 的工具。**

先確認兩件事：一是 `0ops mcp setup <host>` 有沒有真的把設定寫進去（Claude Code 是 `~/.claude.json`、Codex 是 `~/.codex/config.toml`；Copilot 目前只會印手動步驟，不自動寫檔）；二是接完之後**有沒有重啟 AI CLI**（又是這個）。

如果兩件都做了還是看不到，去查對應 host 的專屬說明文件，裡面有各家 CLI 的細節：

```
docs/features/end-user-onboarding/mcp-hosts/<host>.md
```

一張速查表

把今天的 FAQ 收成一張對照，卡住時掃一眼就知道往哪查：

| 症狀 | 最可能的原因 | 先做什麼 |
|---|---|---|
| `asset not found` | 對不到平台 release | 去 releases 頁手動下載 |
| `command not found` | INSTALL_DIR 沒進 PATH | 貼安裝腳本印的 shell-rc 行 |
| device flow timeout | 超時 / 公司 proxy 擋 OAuth | 重跑登入；換網路試 |
| 工具回 `unauthorized` | 登入態失效 | 重新登入 **並重啟 AI CLI** |
| 工具回 `tool_not_permitted` | 工具沒授權 | `0ops auth grant <tool>` |
| AI CLI 看不到工具 | 沒 setup / 沒重啟 | 查 setup + 重啟；看 mcp-hosts doc |

總結

今天把上手最容易踩的坑一次收齊。記住那條經驗法則：**八成的上手問題出在 PATH、授權、或沒重啟 AI CLI——先查這三個。** 另外分清楚 `unauthorized`（登入態問題）與 `tool_not_permitted`（授權問題）是兩回事，才不會對症下錯藥。

介紹、基礎、進階三章到這裡走完了，你已經能獨立把 0ops 用起來、也能自己排錯。明天 [Day 26] 我們進入最後一章「如何管理 0ops」，從最重的一步開始——**self-host 你自己的一套 0ops**，一鍵裝到你自己的 K3s 上。

Q&A

你上手時卡在哪一關最久？是 PATH、授權、還是那個「忘記重啟 AI CLI」？留言分享，說不定能幫到後面的人 : )

參考連結

- 0ops repo：`scripts/install.sh`（一行安裝）、`src/internal/cli/root.go`（`auth` / `mcp` 指令）
- quickstart §5（上手失敗排查）、`docs/features/end-user-onboarding/mcp-hosts/<host>.md`
- 事實源：`docs/ironman-30day-notes/drafts/_source-pack.md`（安裝與上手段、排錯段：使用者自救）
