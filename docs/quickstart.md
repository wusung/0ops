# 0ops Quickstart

> 對應 spec：`docs/features/end-user-onboarding/spec.md`
> 目標：5 分鐘內，在你的 AI CLI 內一句話 deploy 一個 app 到 0ops。

## 1. 安裝 + 設定（一條 curl，1 分鐘）

```sh
OPS_HOST=https://api.<your-0ops> \
  curl -fsSL https://raw.githubusercontent.com/wusung/0ops/main/scripts/install.sh | sh
```

`OPS_HOST` 設了就一次做完：

1. 下載 `0ops` + `0ops-mcp` binary（驗 sha256）到 `~/.local/bin`
2. 跑 `0ops onboard $OPS_HOST`：
   - GitHub Device Flow login（印 user_code + verification URL；你在瀏覽器授權）
   - 自動偵測已裝的 AI CLI（`claude` / `codex`）
   - 對每個 AI CLI 寫 MCP server config（idempotent；備份原檔）
3. 印「重啟 AI CLI」指引

不設 `OPS_HOST` → 只裝 binary，後續手動 `0ops auth login` + `0ops mcp setup`，
或補一條 `0ops onboard https://api.<your-0ops>`。

進階：

```sh
NO_ONBOARD=1 OPS_HOST=... curl ... | sh                       # 只裝 binary，跳過 onboard
OPS_HOST=http://127.0.0.1:18080 curl ... | sh                 # 對 local dev compose
OPS_VERSION=v0.1.1 INSTALL_DIR=$HOME/bin curl ... | sh        # 指定版本與路徑
DRY_RUN=1 curl ... | sh                                       # 只印會做什麼，不真下載
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

選你用的 AI CLI 跑一條：

```sh
0ops mcp setup claude-code        # Claude Code（推薦）
0ops mcp setup codex              # OpenAI Codex CLI
0ops mcp setup copilot-cli        # GitHub Copilot CLI（暫只印手動指引）
```

`mcp setup` 會：

- 偵測 `0ops-mcp` binary 位置
- 從 auth.json 拿 host
- 寫對應 host 的 config（claude-code: `~/.claude.json`；codex: `~/.codex/config.toml`）
- idempotent；重跑不會重寫

**重要**：寫完後重啟 AI CLI（或重新載入 MCP servers）才會生效。

預覽模式（不寫檔）：

```sh
0ops mcp setup claude-code --print-only
```

## 4. 一句話 deploy（3 分鐘）

在你的 AI CLI 內試（自然語言隨意）：

> 「幫我把這個 repo deploy 到 0ops，叫 nextdemo」

AI 應呼叫 `create_app_preview` → 你 confirm → 呼叫 `create_app` → backend 派 GHA →
build → deploy → 對外 URL 可用。預設用 `<slug>.winshare.tw`；
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
| MCP host 看不到 0ops server | 對應 reference：`docs/features/end-user-onboarding/mcp-hosts/<host>.md` |
| `0ops auth login` device flow timeout | 重跑；公司 proxy 擋 GitHub OAuth 也會 fail |
| AI CLI 內呼叫 tool 回 unauthorized | 再跑 `0ops auth login`、**重啟 AI CLI** |
| URL 對外 4xx / 5xx / connection refused | `docs/runbooks/winshare-route-failure.md` |

## 6. 下一步

- 對自己網域：`docs/features/custom-domain-and-verify/spec.md`
- 部署 0ops 自己的 backend（self-host）：`deploy/bootstrap/README.md`
- 看所有 MCP tools 與 CLI subcommands：`0ops --help` / `0ops apps --help` / …
