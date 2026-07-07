# [Day 7] 三分鐘安裝上手 - 一條 curl 搞定

- 原文連結: （未發佈）
- 發布時間:

---

前言

前面六天筆者一直在跟大家聊「為什麼」——為什麼 AI agent 需要一隻 `ship` 的手（Day 1）、招牌的 30 秒 demo 長怎樣（Day 2）、誰該用（Day 3）、preview/confirm 這道安全網怎麼運作（Day 4）、跟 Vercel / Railway / 自建 K8s 怎麼選（Day 5），還有「人用 CLI、AI 用 MCP」這個雙入口模型（Day 6）。

講了這麼多，筆者自己也覺得該動手了。今天這一篇的目標很單純：**讓你在三分鐘內真的把 0ops 裝到自己的機器上**。我們會一起做完三件事：

1. 用一條 `curl` 指令，一次搞定「裝 binary + device flow 登入 + 自動接上你的 AI CLI」；
2. 走一遍 device flow 授權，看看你會在畫面上看到什麼；
3. 用三個指令驗證裝好了、也登入了。

順帶把安裝腳本的幾個實用變體（只裝不登入、乾跑、指定版本）也一起講掉，這些都是筆者自己踩過之後覺得值得先知道的。

一條 curl 全搞定

先看主角。筆者第一次跑的時候也有點半信半疑，但安裝真的就這一行：

```sh
$ OPS_HOST=https://api.<your-0ops> \
    curl -fsSL https://raw.githubusercontent.com/wusung/0ops/main/scripts/install.sh | sh
```

`OPS_HOST` 指向你要連的 0ops 後端。如果你是連代管版，填官方給你的 API host；如果你在本機用 compose 起了一份 0ops（Day 26 會教怎麼 self-host），就填 `http://127.0.0.1:18080`。

這條指令背後其實一次做了三件事，不是單純下載而已：

```mermaid
flowchart LR
    A[curl install.sh] --> B[下載對應平台 binary]
    B --> C[device flow 登入]
    C --> D[自動接上偵測到的 AI CLI]
    D --> E[可以開始用 0ops]
```

換句話說，這一行同時扮演了「安裝器」和「onboarding 精靈」。裝完 binary 之後，它會直接把你帶進登入流程，登入完再幫你把 0ops 接到本機偵測到的 AI CLI 上。你不用記三、四個指令，一行到底。

device flow：你會在畫面上看到什麼

因為終端機沒辦法直接跳出瀏覽器登入視窗，0ops 用的是 GitHub 的 **device flow**。它的體驗是這樣：腳本會在終端機印出一組 `user_code`，還有一個驗證用的 URL，像這樣（實際碼會不同）：

```text
To authenticate, open the URL below in your browser and enter the code:

    https://github.com/login/device
    user_code: WDJB-MJHT

Waiting for authorization...
```

你把那個 URL 貼到瀏覽器、輸入 `user_code`、按下授權，終端機這邊就會結束等待、完成登入。整個過程你不用把任何密碼貼進終端機，授權是在 GitHub 官方頁面上完成的。

登入拿到的 token 會存在 `~/.config/0ops/auth.json`，之後你下的每個 `0ops` 子指令都會自動附帶它，不用每次重登。

驗證：裝好了嗎、登入了嗎

裝完之後，用三個唯讀指令確認一切就緒。第一個看版本，確認 binary 真的在 PATH 上：

```sh
$ 0ops --version
0ops version v0.1.1
```

第二個看登入狀態，確認 token 有效、連得到後端：

```sh
$ 0ops auth status
Logged in as: your-github-login
Host:         https://api.<your-0ops>
Token:        ~/.config/0ops/auth.json (valid)
```

第三個列出你能存取的團隊，這一步同時驗證了「你的身分後端認得、而且你至少有一個團隊可以操作」：

```sh
$ 0ops teams list
TEAM_SLUG   TEAM_NAME     ROLE    PLAN
acme        Acme Inc.     owner   pro
```

三個指令都有正常輸出，就代表安裝、登入、連線這條鏈路整條通了。接下來任何一個 `0ops apps ...` 指令都能跑。

安裝腳本的實用變體

install.sh 認得幾個環境變數，讓你依情境調整行為。常用的有這幾個：

```sh
# 只裝 binary，不進登入 / 接 AI CLI 的 onboarding
$ NO_ONBOARD=1 curl -fsSL .../scripts/install.sh | sh

# 乾跑：印出它會做什麼，但不真的下載安裝
$ DRY_RUN=1 curl -fsSL .../scripts/install.sh | sh

# 指定版本與安裝路徑
$ OPS_VERSION=v0.1.1 INSTALL_DIR=$HOME/bin \
    curl -fsSL .../scripts/install.sh | sh
```

各變數的用途：

- `NO_ONBOARD=1`：只想把 binary 放好、之後再自己手動登入的話用它。手動登入是 `0ops auth login --host=https://api.<host>`，接著一樣用 `0ops auth status` 確認。
- `DRY_RUN=1`：在企業環境裡、想先看清楚腳本會碰哪些路徑再決定要不要跑，這個很實用。
- `OPS_VERSION`：釘住特定版本，避免每次安裝都拉到最新。
- `INSTALL_DIR`：預設會裝到腳本選的目錄；想放在 `$HOME/bin` 這種你自己 PATH 裡的路徑就用它。

如果裝完 `0ops --version` 找不到指令，通常是 `INSTALL_DIR` 沒在你的 PATH 上——這點筆者自己第一次也卡了一下。腳本結束時會印一行建議加進 shell rc 的設定，把那行貼進 `~/.bashrc` 或 `~/.config/fish/config.fish` 再重開終端機就好。這個上手陷阱我們 Day 25 還會再系統性整理一次。

總結

今天我們用一條 `curl` 把 0ops 裝好、用 device flow 登入、再用 `0ops --version` / `0ops auth status` / `0ops teams list` 三連驗證確認就緒，也認識了 `NO_ONBOARD` / `DRY_RUN` / `OPS_VERSION` / `INSTALL_DIR` 幾個變體。裝好之後，這條 `curl` 其實已經順手幫你把 0ops 接上偵測到的 AI CLI 了；但這個「接線」到底寫了什麼、寫到哪個檔、Claude Code / Codex / Copilot 各自有什麼差異，值得單獨講清楚。明天 [Day 8]，我們就來把 AI CLI 的接線這一段拆開來看。

Q&A

我自己也還在把玩這套工具，如果你在安裝或 device flow 這一步卡住，或想到更順的裝法，歡迎留言一起討論 : )

參考連結

- 0ops repo：`scripts/install.sh`（一行安裝 + onboarding）
- `docs/quickstart.md`（安裝與登入、上手排錯 §5）
- 0ops repo：`src/internal/cli/root.go`（`auth`、`teams` verb）
