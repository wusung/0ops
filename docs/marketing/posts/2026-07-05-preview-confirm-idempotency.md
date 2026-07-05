---
cadence: weekly
source: docs/adrs/0002-idempotency-and-compensation.md
slug: ship-with-ai-agent-safely
---

## 中文

# 讓 AI 幫你寫，也幫你出貨——部署交給 agent，壞不了事

你的 AI 已經會寫 code 了。下一步，讓它直接把成品送上線——一個真實網址，五分鐘內可用。

但把「部署」交給 agent，總覺得危險：萬一它重複出貨、刪錯 app、重試到一半搞砸？那筆帳算你的。

0ops 是專為 AI agent 打造的部署平台。你的 agent 直接呼叫它，每個動作天生安全——叫它做兩次，只會發生一次；推 image、綁網域這種收不回的動作，絕不重複觸發。這份安全強制在平台後端，不是一個 AI 能一句話跳過的提醒。你不用盯著它。

Vercel、Railway 預設「人在點按鈕」。0ops 預設「agent 在操作」，並讓 agent 的失誤變無害。

**五分鐘出貨你的第一個 app：**
`curl -fsSL https://0ops.sh/install | sh`
`0ops apps create`

## English

# Let your AI write it—and ship it. Hand deploys to your agent, safely.

Your AI already writes the code. The next step is letting it ship—to a real URL, live in five minutes.

But handing deploys to an agent feels risky: what if it ships twice, deletes the wrong app, or fumbles a half-finished retry? You're the one who has to explain it.

0ops is the deploy platform built for AI agents. Your agent calls it directly, and every action is safe by design—tell it to do something twice, it happens once; the irreversible stuff, like pushing an image or binding a domain, never fires twice. That safety is enforced inside the platform, not a reminder a chatty AI can skip. You don't have to babysit it.

Vercel and Railway assume a human clicking buttons. 0ops assumes an agent is driving—and makes the agent's mistakes harmless.

**Ship your first app in five minutes:**
`curl -fsSL https://0ops.sh/install | sh`
`0ops apps create`
