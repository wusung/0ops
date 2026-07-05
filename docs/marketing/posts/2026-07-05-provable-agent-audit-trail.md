---
cadence: weekly
source: docs/adrs/0015-audit-log-append-only-and-tamper-evidence.md
slug: provable-agent-audit-trail
---

## 中文

# 你的 AI agent 做了什麼，你能證明——一份改不了的紀錄

把出貨交給 AI agent 很方便。但出事的時候——某個服務被刪、某次部署出包、客戶問「這是誰動的」——你答得出來、而且證明得了嗎？

一般的 log，你只能「希望」它是完整的。有後台權限的人可以悄悄改掉一行、抹掉痕跡，你查不出來。對要交付給客戶、要過稽核的團隊，能被竄改又查不出的紀錄，等於沒有紀錄。

0ops 把 agent 的每一個動作記成一份**改不了、也刪不掉**的紀錄。誰、什麼時候、對哪個 app 做了什麼，一清二楚；你隨時能匯出，並驗證它從頭到尾沒被動過手腳——連我們也動不了。

別家給你一份「希望沒被改」的 log；0ops 給你一份「能證明沒被改」的紀錄。

**五分鐘出貨你的第一個 app：**
`curl -fsSL https://0ops.sh/install | sh`
`0ops apps create`

## English

# Prove what your AI agent did—a record no one can quietly edit

Handing deploys to an AI agent is convenient. But when something goes wrong—a service deleted, a bad deploy, a client asking "who changed this?"—can you answer, and prove it?

An ordinary log is something you only *hope* is intact. Anyone with backend access can quietly rewrite a line and erase the trail, and you'd never know. For teams shipping to clients or facing an audit, a record that can be altered without a trace is no record at all.

0ops turns every action your agent takes into a record that **can't be edited or deleted**. Who did what, to which app, and when—all there; export it any time and verify it was never tampered with, not even by us.

Others give you a log you hope wasn't changed. 0ops gives you a record you can prove wasn't.

**Ship your first app in five minutes:**
`curl -fsSL https://0ops.sh/install | sh`
`0ops apps create`
