---
cadence: weekly
source: docs/adrs/0002-idempotency-and-compensation.md
slug: ship-with-ai-agent-safely
---

## 中文

# 讓 AI 幫你寫，也幫你出貨

你的 AI 已經會寫 code 了。下一步，讓它直接把成品送上線。

把部署交給 agent 總覺得危險：萬一它重複出貨、刪錯 app？0ops 讓每個動作天生安全，做兩次只發生一次。Vercel 預設人在點按鈕；0ops 預設 agent 在操作。

**五分鐘出貨你的第一個 app：**
`curl -fsSL https://0ops.sh/install | sh`
`0ops apps create`

## English

# Let your AI write it—and ship it

Your AI already writes the code. Let it ship, too.

Handing deploys to an agent feels risky—what if it ships twice or deletes the wrong app? 0ops makes every action safe; do it twice, it happens once.

**Ship your first app in five minutes:**
`curl -fsSL https://0ops.sh/install | sh`
`0ops apps create`
