# [Day 1] 前言 & 為什麼你的 AI agent 需要 0ops

- 原文連結: （未發佈）
- 發布時間:

---

前言

這一兩年，用 AI coding agent 寫程式已經從新鮮事變成日常。Claude Code、Codex、Copilot CLI 這類工具，能讀你的 repo、寫功能、改 bug、跑測試，甚至幫你重構整個模組。但每次寫到最後，我都會撞到同一道牆：**程式寫好了，然後呢？**

Agent 可以把功能做到「在我機器上跑得起來」，可是要真正上線——build image、推 registry、寫 K8s manifest、設網域、掛 TLS、接 CI——這一段它幫不上忙。你得自己切回終端機，手動把這十幾個步驟走一遍。AI 幫你把開發加速了十倍，出貨這一哩卻還是純手工。

這個系列的 30 天，就是要把這道牆拆掉。我會帶你從零開始，用 **0ops** 這套工具，讓你（或你的 AI agent）用一句話、一行指令，把專案部署上線。

未來 30 天的學習筆記

這次的筆記聚焦在「**實際使用 0ops**」——怎麼裝、怎麼用、怎麼部署、怎麼排錯，而不是它內部怎麼實作。整體分為五章：

介紹與定位

- [Day 1] 前言 & 為什麼你的 AI agent 需要 0ops
- [Day 2] 30 秒 demo：一句話讓 AI 把 repo 部署上線
- [Day 3] 誰該用 0ops：使用情境與選型
- [Day 4] agent 出貨的安全網 - preview/confirm 是什麼

解決方案與上手

- [Day 5] 0ops vs Vercel / Railway / 自建 K8s
- [Day 6] 兩種用法 - CLI 與 MCP 雙入口
- [Day 7] 三分鐘安裝上手 - 一條 curl 搞定
- [Day 8] 接上你的 AI CLI - Claude Code / Codex / Copilot
- [Day 9] 工具授權 - deny-by-default 與 grant

核心操作

- [Day 10] 部署第一個 app - 從 GitHub repo 到上線
- [Day 11] 從本機資料夾部署 - local source
- [Day 12] 用自然語言部署 - MCP 工具鏈全流程
- [Day 13] 查部署狀態與看 log
- [Day 14] 重新部署與 push-to-deploy 自動化
- [Day 15] 綁定你的自訂網域
- [Day 16] 團隊協作 - 邀請成員與角色
- [Day 17] 管理 app 生命週期 - 列出 / 查詳情 / 安全刪除
- [Day 18] token 與 CI - 非互動式部署

實務與排錯

- [Day 19] Demo：讓 Claude Code 從零部署 Next.js 上線
- [Day 20] preview/confirm 實戰 - 安全地放手讓 agent 寫入
- [Day 21] GitHub App 與 push-to-deploy 全自動化
- [Day 22] 團隊權限與稽核 - 誰能做什麼、誰做了什麼
- [Day 23] 排錯（一）- app 卡在 building / syncing
- [Day 24] 排錯（二）- 卡在 deleting 與網址打不開
- [Day 25] 上手陷阱與 FAQ

進階與自架

- [Day 26] self-host 你自己的 0ops - 一鍵裝
- [Day 27] 生產 OAuth 與網域設定
- [Day 28] 企業級 SSO / OIDC 登入與集中撤權
- [Day 29] 稽核與合規 - 查詢 / 匯出 / incident
- [Day 30] 0ops 30 天學習總結

所以，什麼是 0ops

一句話：**0ops 是讓 AI coding agent 出貨時原生呼叫的那隻手，補上 agent 工具帶裡缺的 `ship`。**

具體來說，它是一套 agent-native 的部署平台，由三個部分組成：

- `0ops-server`：後端，把「從原始碼到上線」的整條管線包起來（build、推映像、產 K8s manifest、GitOps、上網域）。它是純粹的 IaaS 後端，本身**不跑 LLM**。
- `0ops`：給人用的 CLI，你可以手動下指令操作。
- `0ops-mcp`：給 AI CLI 用的 MCP server，讓 Claude Code / Codex 這類 agent 能直接呼叫部署能力。

換句話說，同一套後端能力，開了兩個入口：**人用 CLI，AI 用 MCP**。你在終端機打 `0ops apps create`，跟你在 Claude Code 裡說「幫我把這個 repo 部署上線」，走的是同一條路、同一套權限。

為什麼需要它

你可能會問：部署工具那麼多，Vercel、Railway、Zeabur 都很好用，為什麼還要 0ops？

因為那些工具是**為人設計的**——它們假設有一個人坐在瀏覽器前面點按鈕、連 GitHub、看 dashboard。但當你的開發主體變成 AI agent 時，這個假設就不成立了。Agent 需要的是一組**可程式化、有明確語意、而且安全可控**的介面，能讓它自己完成部署，同時又不會在你沒看到的情況下砍掉正式環境。

0ops 的設計正是繞著這件事轉：

- **agent 原生**：部署能力以 MCP 工具的形式暴露，agent 可以直接呼叫，不需要一個人去點瀏覽器。
- **有安全網**：所有寫入操作（建立、重新部署、刪除）都是**兩階段**——先 preview 把「會發生什麼事」攤給你看，你確認了才真的執行。這道閘是後端強制的，agent 繞不過去。（這點 Day 4 會詳談。）
- **可自架**：除了代管版，你也能把整套 0ops 裝在自己的 K3s 上，適合有資料落地或合規需求的團隊。（Day 26 起會帶你做。）

這 30 天你會學到什麼

讀完這個系列，你應該能夠：

- 三分鐘裝好 0ops、登入、把它接上你的 AI CLI；
- 用一行指令或一句話把 GitHub repo 或本機專案部署上線；
- 綁自訂網域、邀請團隊成員、看 log、重新部署；
- 在 app 卡住、網址打不開時自己排錯；
- 甚至把整套 0ops 自架在你自己的機器上。

我會盡量讓每一篇都「能照著做」——每天至少給你一段可以複製貼上的指令，以及你會看到的實際輸出。

總結

今天先把場景講清楚：AI agent 讓開發變快了，但「出貨」這最後一哩還是斷的，而 0ops 想補的就是這一哩。明天 [Day 2]，我們不講理論，直接看一個 30 秒的 demo——在 Claude Code 裡打一句話，看 agent 怎麼把一個 repo 部署到 `<你的 app>.jesontech.com` 上線。

Q&A

我自己也還在把玩這套工具，如果你對文章有任何疑問或建議，歡迎留言一起討論 : )

參考連結

- 0ops repo：`README.md`（產品定位與一行安裝）
- `docs/0ops-business-plan.md` §二（四個痛點）
- `docs/quickstart.md`（快速上手）
