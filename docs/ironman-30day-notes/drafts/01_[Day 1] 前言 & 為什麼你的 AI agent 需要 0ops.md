# [Day 1] 前言 & 為什麼你的 AI agent 需要 0ops

- 原文連結: （未發佈）
- 發布時間:

---

前言

第一次認真撞到「AI 幫我把 code 寫完了，然後呢？」這個問題，大概是這一兩年的事。那陣子筆者幾乎每天都掛在 Claude Code、Codex 這類 AI CLI 上做開發——讀 repo、生功能、改 bug、跑測試，速度快到有點不真實。但每次寫到最後要「真正上線」那一步，筆者就得默默切回終端機，自己 build image、推 registry、寫一疊 K8s YAML、設網域、掛 TLS。AI 把前面加速了十倍，出貨這最後一哩卻還是純手工，一步都少不了。

後來筆者才想通，缺的不是又一個部署平台，而是一隻「能讓 AI agent 自己出貨、又不會闖禍」的手。這也是接下來 30 天想跟大家一起搞懂的東西——0ops。

未來 30 天的學習筆記

希望在未來 30 天裡，能每天不間斷地跟大家分享，怎麼「實際用 0ops」把自己的專案送上線：怎麼裝、怎麼用、怎麼部署、卡住了怎麼自己排錯，而不是去講它內部怎麼實作。這次的學習筆記大致分為四個方向：

介紹與環境架設

- [Day 1] 前言 & 為什麼你的 AI agent 需要 0ops
- [Day 2] 30 秒 demo：一句話讓 AI 把 repo 部署上線
- [Day 3] 誰該用 0ops：使用情境與選型
- [Day 4] agent 出貨的安全網 - preview/confirm 是什麼
- [Day 5] 0ops vs Vercel / Railway / 自建 K8s
- [Day 6] 兩種用法 - CLI 與 MCP 雙入口
- [Day 7] 三分鐘安裝上手 - 一條 curl 搞定
- [Day 8] 接上你的 AI CLI - Claude Code / Codex / Copilot
- [Day 9] 工具授權 - deny-by-default 與 grant

0ops 基礎概念與實作

- [Day 10] 部署第一個 app - 從 GitHub repo 到上線
- [Day 11] 從本機資料夾部署 - local source
- [Day 12] 用自然語言部署 - MCP 工具鏈全流程
- [Day 13] 查部署狀態與看 log
- [Day 14] 重新部署與 push-to-deploy 自動化
- [Day 15] 綁定你的自訂網域
- [Day 16] 團隊協作 - 邀請成員與角色
- [Day 17] 管理 app 生命週期 - 列出 / 查詳情 / 安全刪除
- [Day 18] token 與 CI - 非互動式部署

0ops 進階概念與實作

- [Day 19] Demo：讓 Claude Code 從零部署 Next.js 上線
- [Day 20] preview/confirm 實戰 - 安全地放手讓 agent 寫入
- [Day 21] GitHub App 與 push-to-deploy 全自動化
- [Day 22] 團隊權限與稽核 - 誰能做什麼、誰做了什麼
- [Day 23] 排錯（一）- app 卡在 building / syncing
- [Day 24] 排錯（二）- 卡在 deleting 與網址打不開
- [Day 25] 上手陷阱與 FAQ

如何管理 0ops

- [Day 26] self-host 你自己的 0ops - 一鍵裝
- [Day 27] 生產 OAuth 與網域設定
- [Day 28] 企業級 SSO / OIDC 登入與集中撤權
- [Day 29] 稽核與合規 - 查詢 / 匯出 / incident
- [Day 30] 0ops 30 天學習總結

所以，什麼是 0ops

一句話：0ops 是讓 AI coding agent 出貨時原生呼叫的那隻手，補上 agent 工具帶裡缺的 `ship`。

它是一套 agent-native 的部署平台，由三支 binary 組成：

- `0ops-server`：純 IaaS 後端，把「從原始碼到上線」整條管線包起來（build、推映像、產 K8s manifest、GitOps、上網域），本身不跑 LLM。
- `0ops`：給人用的 CLI，你可以手動下指令操作。
- `0ops-mcp`：給 AI CLI 用的 MCP server，讓 Claude Code / Codex 這類 agent 能直接呼叫部署能力。

換句話說，同一套後端能力，開了兩個入口：人用 CLI，AI 用 MCP，走的是同一條路、同一套權限。

為何使用 0ops

筆者一開始也猶豫過：部署工具那麼多，Vercel、Railway、Zeabur 都很好用，何必再學一套？

問題出在，那些工具幾乎都是為「人」設計的——它們預設有一個人坐在瀏覽器前面點按鈕、連 GitHub、盯著 dashboard。可是當你的開發主體慢慢從「人」變成「AI agent」，這個前提就崩了。Agent 需要的，是一組可程式化、語意明確、而且安全可控的介面，能讓它自己把東西送上線，同時又不會在你沒看到的時候手一滑砍掉正式環境。0ops 的整個設計，就是繞著這件事轉的。

0ops 的優點

agent 原生 Agent-native
部署能力以 MCP 工具的形式暴露，AI agent 可以直接呼叫，不需要一個人去點瀏覽器、連 dashboard。

有安全網 Safe by default
所有寫入操作（建立、重新部署、刪除）都是兩階段——先 preview 把「會發生什麼事」完整攤給你看，你確認了才真的執行。這道閘是後端強制的，agent 繞不過去。（這點 Day 4 會專篇細講。）

可以自架 Self-hostable
除了代管版，你也能把整套 0ops 裝在自己的 K3s 上，適合有資料落地或合規需求的團隊。（Day 26 起會帶你一步步做。）

人與 AI 共用一套後端 One backend, two entrances
CLI 與 MCP 背後是同一套 API 與權限，你手動下的指令，跟 agent 呼叫的工具，行為與授權完全一致。

Q&A

筆者自己也還在把這套工具摸熟，如果你對文章有任何疑問或建議，非常歡迎留言給我唷 : )

參考連結

- 0ops repo：`README.md`（產品定位與一行安裝）
- `docs/0ops-business-plan.md` §二（四個痛點）
- `docs/quickstart.md`（快速上手）
