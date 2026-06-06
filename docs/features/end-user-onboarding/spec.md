# Feature Spec：end-user-onboarding

> **狀態**：draft
> **來源**：v1 收尾 #4「end-user 安裝 / AI CLI 接線 UX」
> **適用範圍**：使用者第一次使用 0ops——從零到「在 AI CLI 內一句話 deploy 自己的 app」之全鏈路。
> **對應 spec**：`docs/features/auth-login-flow/spec.md`（device flow login）、
> `docs/features/production-deployment/spec.md`（host 端可達）。

## 1. 結論

三條互不耦合的成品，組成「prompt → deploy」路徑：

1. **One-line installer**：`curl -fsSL https://raw.githubusercontent.com/wusung/0ops/main/scripts/install.sh | sh`
   一條指令把 `0ops` + `0ops-mcp` 兩個 binary 從 GitHub Release 抓到 `~/.local/bin`。
2. **`0ops mcp setup <host>`** CLI 子命令：偵測 / 建立 / 補對應 AI CLI 的 MCP server config，
   一行接好 claude code / codex。idempotent；`--print-only` 可只 dump 不寫檔。
3. **Quickstart 文件**：`docs/quickstart.md` 三段（install → auth login → AI CLI 內 deploy），
   `README.md` 給 30 秒 TL;DR + link。

成功定義：使用者跑一條 curl 後，5 分鐘內可在自己的 AI CLI 內以自然語言觸發 `create_app`。

## 2. 需求範圍

### 2.1 包含

| 元件 | 路徑 |
|---|---|
| installer | `scripts/install.sh` |
| `0ops mcp setup` CLI | `src/internal/cli/mcpsetup.go` + 對應 root.go 註冊 |
| Claude Code config 寫入器 | 同上，target `$XDG_CONFIG_HOME/claude-code/.claude.json` 或 `~/.claude.json` |
| Codex CLI config 寫入器 | 同上，target `$HOME/.codex/config.toml` |
| Quickstart | `docs/quickstart.md` |
| MCP host references | `docs/features/end-user-onboarding/mcp-hosts/{claude-code,codex,copilot-cli}.md` |
| Root README | `README.md`（新建） |

### 2.2 不包含（YAGNI）

1. Homebrew formula / apt repo / AUR：留待社群採用率上來再做。
2. Windows installer（PowerShell）：v1 透過 release zip + 手動解壓。
3. GitHub Copilot CLI 自動寫入：MCP 支援尚未穩定（v1 知識範圍），給文件 + 手動步驟。
4. Auto-update（installer 偵測新版本提示）：v2。
5. 反向解除（`0ops mcp uninstall`）：v2；手動編輯 config 即可。

## 3. installer 細節

### 3.1 介面

```bash
# 預設：抓 latest release，裝到 ~/.local/bin
curl -fsSL https://raw.githubusercontent.com/wusung/0ops/main/scripts/install.sh | sh

# 進階：可設定 env
OPS_VERSION=v0.1.1 \
INSTALL_DIR=$HOME/bin \
curl -fsSL ... | sh
```

### 3.2 行為

1. 偵測 OS（`uname -s`：Linux / Darwin）+ arch（`uname -m`：x86_64 → amd64、aarch64/arm64 → arm64）。
2. 查 `${OPS_VERSION:-latest}` 之 release：`GET https://api.github.com/repos/wusung/0ops/releases/{latest|tags/$OPS_VERSION}`。
3. 解析 release `assets`，取對應 `0ops_<version>_<os>_<arch>.tar.gz` 與 `checksums.txt`。
4. mkdir -p `$INSTALL_DIR`（預設 `$HOME/.local/bin`）。
5. 下載 tar.gz + checksums.txt → 驗 sha256。
6. 解壓 → 把 `0ops` 與 `0ops-mcp` 安到 `$INSTALL_DIR`，chmod +x。
7. 檢查 `$INSTALL_DIR` 在 `$PATH`：若不在，提示 shell rc 加入指令（給 bash / zsh / fish 三條）。
8. 印「下一步」：`0ops auth login --host=<your-0ops>` + `0ops mcp setup claude-code`。

### 3.3 安全 / 失敗

- 不接受 `INSTALL_DIR` 是 `/` 之 sub-path 但需 sudo；遇權限失敗 → 印改 `INSTALL_DIR=$HOME/bin` 建議。
- checksum 失敗 → 立即 exit 1，不保留半下載檔。
- network 失敗 → exit 4，提示走 GitHub Release 頁面手動下載。
- `--dry-run`（env `DRY_RUN=1`）：印會做什麼但不執行。

## 4. `0ops mcp setup` 細節

### 4.1 介面

```
0ops mcp setup <host>
  [--ops-host=<url>]      backend host；不傳則讀 auth.json
  [--mcp-binary=<path>]   0ops-mcp binary path；預設搜 $PATH 內 0ops-mcp
  [--print-only]          只印對應 config 片段，不寫檔
  [--config=<path>]       覆寫目標 config 檔路徑

host:
  claude-code | claude   寫 ~/.claude.json mcpServers."0ops"
  codex                  寫 ~/.codex/config.toml [mcp_servers.0ops]
  copilot-cli            目前不支援自動寫入；改印手動步驟與檔路徑（exit 0）
```

### 4.2 行為

1. 解析 host → 對應 default config 路徑。
2. 讀 auth.json 拿 `OPS_HOST`（若 `--ops-host` 沒傳）。
3. 偵測 `0ops-mcp` binary 路徑（`--mcp-binary` > `which 0ops-mcp` > 與 `0ops` binary 同目錄）。
4. 讀現有 config（若存在）→ deep-merge / set `mcpServers.0ops`（claude-code）或 `mcp_servers.0ops`（codex）。
5. 寫回；備份原檔到 `<config>.bak.<timestamp>`。
6. 印確認訊息 + 下一步（restart MCP host）。

### 4.3 Idempotency

- 重跑：原 entry 已是相同值 → 不寫檔，印「already up-to-date」。
- 原 entry 不同 → prompt 是否覆蓋；`--yes` 旁路 prompt。

### 4.4 不可變約定

- 不修改 0ops 範圍外的 key（claude-code 的 `claude.json` 可能含 user 自己的 settings）。
- 寫入失敗（權限 / disk full）→ 不留半成品，原檔保持原樣。

## 5. Quickstart 結構

`docs/quickstart.md`：

1. 安裝（curl one-liner）
2. login（`0ops auth login --host=...`）
3. 接 AI CLI（`0ops mcp setup claude-code`，restart）
4. 在 AI CLI 內試「幫我把這個 repo deploy 到 0ops」
5. 故障：連到 reference snippets + runbook

`README.md`（root）：30 秒版本 + link 到 quickstart。

## 6. 驗收

1. 在 clean 新 Linux user 帳號跑 curl one-liner → `0ops --version` PASS。
2. `0ops mcp setup claude-code --print-only` 印合法 JSON snippet。
3. `0ops mcp setup claude-code` 跑兩次：第二次回 already up-to-date。
4. 故意把 `~/.claude.json` 寫成壞 JSON → setup 報 parse error 不覆蓋。
5. `0ops mcp setup copilot-cli` 印手動指引並 exit 0。

## 7. 測試要求

| 範圍 | 形式 |
|---|---|
| `mcpsetup.go` | unit test：新建 config / merge 既有 config / 偵測 idempotency / 拒覆蓋壞 JSON |
| `install.sh` | `bash -n` syntax；`DRY_RUN=1` 跑 happy path 不真下載 |
| 文件 | quickstart link 不死連 |

## 8. 不在本 spec 範圍

- end-user 端 OAuth App 註冊：屬 self-hosted ops，非 SaaS 終端 user 工作流；走 `docs/runbooks/production-oauth-setup.md`。
- `0ops` CLI 與 backend 之既有 contract：unchanged。
- MCP server 內部 tool：unchanged。
