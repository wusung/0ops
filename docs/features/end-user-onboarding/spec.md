# Feature Spec：end-user-onboarding

> **狀態**：draft
> **來源**：v1 收尾 #4「end-user 安裝 / AI CLI 接線 UX」
> **適用範圍**：使用者第一次使用 0ops——從零到「在 AI CLI 內一句話 deploy 自己的 app」之全鏈路。
> **對應 spec**：`docs/features/auth-login-flow/spec.md`（device flow login）、
> `docs/features/production-deployment/spec.md`（host 端可達）。

## 1. 結論

三條互不耦合的成品，組成「prompt → deploy」路徑：

1. **One-line installer**：`curl -fsSL https://raw.githubusercontent.com/wusung/0ops/main/scripts/install.sh | sh`
   一條指令把 `0ops` binary 從 GitHub Release 抓到 `~/.local/bin`，並接著跑 `0ops onboard`。
2. **`0ops skill install`** CLI 子命令：把編進 binary 的 skill 寫到使用者層
   `~/.claude/skills/0ops/SKILL.md`（`--project` 改寫專案層）。idempotent；
   `--print-only` 可只 dump 不寫檔。細節見 `docs/features/agent-skill/skill-distribution-spec.md`。
3. **Quickstart 文件**：`docs/quickstart.md` 三段（install → auth login → AI CLI 內 deploy），
   `README.md` 給 30 秒 TL;DR + link。

成功定義：使用者跑一條 curl 後，5 分鐘內可在自己的 AI CLI 內以自然語言觸發 `0ops apps create`。

## 2. 需求範圍

### 2.1 包含

| 元件 | 路徑 |
|---|---|
| installer | `scripts/install.sh` |
| `0ops skill install` CLI | `src/internal/cli/skill.go` + 對應 root.go 註冊 |
| 嵌入的 skill 內容 | `src/internal/cli/skillasset/`（canonical 在 `.claude/skills/0ops/SKILL.md`） |
| `0ops onboard` 串接 | `src/internal/cli/onboard.go`（login → skill install） |
| Quickstart | `docs/quickstart.md` |
| Root README | `README.md` |

### 2.2 不包含（YAGNI）

1. Homebrew formula / apt repo / AUR：留待社群採用率上來再做。
2. Windows installer（PowerShell）：v1 透過 release zip + 手動解壓。
3. Codex / GitHub Copilot CLI 的等價安裝：兩者的 skill 機制未定型，給 `--print-only` + 手動步驟。
4. Auto-update（installer 偵測新版本提示）：v2。
5. 反向解除（`0ops skill uninstall`）：刪一個檔案，手動即可。

## 3. installer 細節

### 3.1 介面

```bash
# 預設：抓 latest release，裝到 ~/.local/bin，
# 並對官方後端（OPS_HOST 預設 https://0ops.jesontech.com）跑 onboard
curl -fsSL https://raw.githubusercontent.com/wusung/0ops/main/scripts/install.sh | sh

# 進階：可設定 env
OPS_HOST=https://api.my-domain.com \
OPS_VERSION=v0.1.1 \
INSTALL_DIR=$HOME/bin \
curl -fsSL ... | sh

# 只裝 binary，不 onboard
NO_ONBOARD=1 curl -fsSL ... | sh
```

`OPS_HOST` 預設即官方 SaaS 後端；self-host / staging / local 以 env 覆寫。
唯一的 opt-out 是 `NO_ONBOARD=1`——`OPS_HOST=`（空字串）會被 `:-` 展開回預設值，
不再等同「只裝 binary」。

### 3.2 行為

1. 偵測 OS（`uname -s`：Linux / Darwin）+ arch（`uname -m`：x86_64 → amd64、aarch64/arm64 → arm64）。
2. 查 `${OPS_VERSION:-latest}` 之 release：`GET https://api.github.com/repos/wusung/0ops/releases/{latest|tags/$OPS_VERSION}`。
3. 解析 release `assets`，取對應 `0ops_<version>_<os>_<arch>.tar.gz` 與 `checksums.txt`。
4. mkdir -p `$INSTALL_DIR`（預設 `$HOME/.local/bin`）。
5. 下載 tar.gz + checksums.txt → 驗 sha256。
6. 解壓 → 把 `0ops` 安到 `$INSTALL_DIR`，chmod +x。
7. 檢查 `$INSTALL_DIR` 在 `$PATH`：若不在，提示 shell rc 加入指令（給 bash / zsh / fish 三條）。
8. 除非 `NO_ONBOARD=1`，跑 `0ops onboard $OPS_HOST`（device-flow login + `skill install`）；
   跳過時改印「下一步」：`0ops auth login --host=<your-0ops>` + `0ops skill install`。
9. `DRY_RUN=1` 在步驟 5 之前中止，並印出會下載什麼、裝到哪、以及會不會跑 onboard。

### 3.3 安全 / 失敗

- 不接受 `INSTALL_DIR` 是 `/` 之 sub-path 但需 sudo；遇權限失敗 → 印改 `INSTALL_DIR=$HOME/bin` 建議。
- checksum 失敗 → 立即 exit 1，不保留半下載檔。
- network 失敗 → exit 4，提示走 GitHub Release 頁面手動下載。
- `--dry-run`（env `DRY_RUN=1`）：印會做什麼但不執行。

## 4. `0ops skill install` 細節

介面與行為的單一事實來源是 `docs/features/agent-skill/skill-distribution-spec.md` § 4。
本 spec 只記 onboarding 觀點的約定：

- 預設目標是使用者層 `~/.claude/skills/0ops/SKILL.md`，因此使用者在**任何** repo 都能觸發 skill。
- `0ops onboard <host>` 在 login 成功後自動跑一次；`--skip-skill` 可略過。
- skill 安裝失敗不讓 onboard 失敗（登入已完成），但必須印出補跑指令。
- 寫檔後需重啟 AI CLI 才生效，訊息須明講。

## 5. Quickstart 結構

`docs/quickstart.md`：

1. 安裝（curl one-liner，內含 onboard）
2. login（`0ops auth login --host=...`，已含在第 1 步）
3. 接 AI CLI（`0ops skill install`，restart）
4. 在 AI CLI 內試「幫我把這個 repo deploy 到 0ops」
5. 故障排除表

`README.md`（root）：30 秒版本 + link 到 quickstart。

## 6. 驗收

1. 在 clean 新 Linux user 帳號跑 curl one-liner → `0ops --version` PASS。
2. `0ops skill install --print-only` 印出 skill 內容與目標路徑，不落檔。
3. `0ops skill install` 跑兩次：第二次回 `already up-to-date`，不產生備份。
4. 手改 `~/.claude/skills/0ops/SKILL.md` 後再跑 → 產生 `.bak.<ts>`，目標回到 canonical 內容。
5. `0ops onboard <host> --skip-login` → skill 仍完成安裝。

## 7. 測試要求

| 範圍 | 形式 |
|---|---|
| `skill.go` | unit test：使用者層安裝 / idempotency / 備份既有檔 / `--project` 不碰 HOME / `--print-only` 不寫檔 |
| `onboard.go` | unit test：onboard 安裝 skill；`--skip-skill` 不寫檔 |
| 嵌入內容 | `TestEmbeddedSkillMatchesRepoSkill` 斷言與 canonical byte-identical |
| `install.sh` | `bash -n` syntax；`DRY_RUN=1` 跑 happy path 不真下載 |
| 文件 | quickstart link 不死連 |

## 8. 不在本 spec 範圍

- end-user 端 OAuth App 註冊：屬 self-hosted ops，非 SaaS 終端 user 工作流；走 `docs/runbooks/production-oauth-setup.md`。
- `0ops` CLI 與 backend 之既有 contract：unchanged。
- skill 內容本身（觸發條件、指令對照、護欄）：屬 `docs/features/agent-skill/spec.md`。
