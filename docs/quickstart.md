# 0ops Quickstart

> 對應 spec：`docs/features/end-user-onboarding/spec.md`
> 目標：5 分鐘內，在你的 agent 內一句話 deploy 一個 app 到 0ops。

## 1. 安裝 + 設定（一條 curl，1 分鐘）

```sh
curl -fsSL https://raw.githubusercontent.com/wusung/0ops/main/scripts/install.sh | sh
```

預設連官方後端 `https://0ops.jesontech.com`，一次做完：

1. 下載 `0ops` binary（驗 sha256）到 `~/.local/bin`
2. 跑 `0ops onboard $OPS_HOST`：
   - GitHub Device Flow login（印 user_code + verification URL；你在瀏覽器授權）
   - 把 0ops skill 裝到 `~/.claude/skills/0ops/SKILL.md`（idempotent；內容不同才備份原檔）
3. 印「重啟 AI CLI」指引

自架後端就在前面加 `OPS_HOST=https://api.<your-0ops>`；只想裝 binary 不登入就加
`NO_ONBOARD=1`，之後再補一條 `0ops onboard <host>`。

進階：

```sh
NO_ONBOARD=1 curl ... | sh                                    # 只裝 binary，跳過 onboard
OPS_HOST=https://api.<your-0ops> curl ... | sh                # 自架後端
OPS_HOST=http://127.0.0.1:18080 curl ... | sh                 # 對 local dev compose
OPS_VERSION=v0.1.1 INSTALL_DIR=$HOME/bin curl ... | sh        # 指定版本與路徑
DRY_RUN=1 curl ... | sh                                       # 只印會做什麼（含會不會 onboard、連哪個 host），不真下載
```

裝完跑 `0ops --version` 驗。預設 `~/.local/bin` 若不在 PATH，腳本會印 shell rc 加哪一行。

## 2. 登入 / 接 AI CLI 已內含在第 1 步

預設走 GitHub Device Flow，不需瀏覽器 redirect：

```sh
0ops auth login --host=https://api.<your-0ops>
# 印出 user_code + verification URL
# 在瀏覽器輸入 code → 授權 → CLI 自動取得 bearer token，寫入 ~/.config/0ops/auth.json
```

之後所有 `0ops` 子命令自動帶 token。

驗證：

```sh
0ops auth status
0ops teams list
```

## 3. 接你的 AI CLI（30 秒）

`onboard` 已經做完了。要單獨補跑或裝到別處：

```sh
0ops skill install                       # 使用者層 ~/.claude/skills/0ops/SKILL.md（預設）
0ops skill install --project .           # 只裝進這個 repo 的 .claude/skills/
0ops skill install --print-only          # 只看內容與目標路徑，不寫檔
```

安裝行為：目標不存在就寫；內容相同不動（印 `already up-to-date`）；
內容不同先備份成 `SKILL.md.bak.<時間戳>` 再覆寫。

**重要**：寫完後重啟 AI CLI（或重新載入 skills）才會生效。

Codex / Copilot CLI 目前沒有等價的自動安裝，需手動把 `0ops skill install --print-only`
的內容放進各自的設定。

## 4. 一句話 deploy（3 分鐘）

在你的 AI CLI 內試（自然語言隨意）：

> 「幫我把這個 repo deploy 到 0ops，叫 nextdemo」

AI 應依 skill 先跑 `0ops apps create --dry-run` 呈報副作用 → 你同意後才跑實際建立 →
backend 派 GHA → build → deploy → 對外 URL 可用。預設用 `<slug>.jesontech.com`；
要自有網域走 `0ops apps add-domain`（CLI 或自然語言指令皆可）。

驗：

```sh
0ops apps list
0ops deploys status nextdemo
curl https://nextdemo.<your-domain>/
```

## 5. 故障

| 症狀 | 看這 |
|---|---|
| `curl install.sh` 在 `asset not found` | release 命名不符；走 `https://github.com/wusung/0ops/releases` 手動下載 |
| `INSTALL_DIR not in PATH` 警告 | 照腳本印的 shell rc 範例加一行 |
| AI CLI 不認得 0ops | 跑 `0ops skill install`，然後**重啟 AI CLI** |
| `0ops auth login` device flow timeout | 重跑；公司 proxy 擋 GitHub OAuth 也會 fail |
| AI CLI 內下指令回 unauthorized | 再跑 `0ops auth login` |
| URL 對外 4xx / 5xx / connection refused | `docs/runbooks/winshare-route-failure.md` |

## 6. 下一步

- 對自己網域：`docs/features/custom-domain-and-verify/spec.md`
- 部署 0ops 自己的 backend（self-host）：`deploy/bootstrap/README.md`
- 看所有 CLI subcommands：`0ops --help` / `0ops apps --help` / …
