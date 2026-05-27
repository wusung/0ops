---
adr: "0010"
title: CLI 套件分發策略
status: Accepted
date: 2026-05-10
tags:
  - cli
  - distribution
  - goreleaser
  - homebrew
supersedes: []
superseded-by: []
---

# ADR-0010：CLI 套件分發策略

* Status：Accepted
* Date：2026-05-10
* 適用範圍：M0–M5；`0ops` CLI 與 `0ops-mcp` binary 之散布、安裝、自更新通知
* 來源：`docs/0ops-plan.md`「TBD」段「CLI 套件分發」；本 ADR 將其釘定
* 上游依賴：[ADR-0003](0003-mcp-sdk-selection.md)（MCP binary 一同散布）；無下游依賴

## 0. TL;DR（先讀本段）

採用以下六項組合決策：

1. **主路徑**：`goreleaser` 預編 binary，發佈至 GitHub Release；支援 linux/amd64、linux/arm64、darwin/amd64、darwin/arm64、windows/amd64 五平台。
2. **次路徑**：`go install github.com/winshare/zeroops/cmd/cli@latest` 與 `@<version>` 並行支援；無平台限制（凡 Go toolchain）。
3. **Homebrew tap**：`winshare/0ops`（自管 tap repo）；goreleaser 自動產生 formula 並 PR；用戶 `brew install winshare/0ops/0ops`。
4. **Windows / Linux package manager**：v1 暫不（Scoop / apt / yum 為 v1.1 評估）；用戶 v1 用 `go install` 或下載 release artifact。
5. **自更新通知**：`0ops version` 與 `0ops` 任意命令啟動時，背景查 GitHub Release latest tag；版本落後即印一行 hint（不阻擋）。每 24h 至多查一次（cache `~/.config/0ops/version-check`）。
6. **版本同步**：`0ops` / `0ops-mcp` / `0ops-server` 三 binary 共用同一 version（goreleaser 一次發三 artifact）；`backend version != cli version` 時 backend 回 warn header（v1.1）。

行為與 goreleaser YAML 細節以 `src/.goreleaser.yaml` 為準，本 ADR 不重述。

## 1. Context and Problem Statement

`0ops` CLI 為使用者主要互動介面；`0ops-mcp` 為 AI CLI 的 stdio 配套。兩 binary 散布需考慮：

1. **多平台**：開發者多用 macOS arm64 (M1/M2)；CI 多 linux amd64；少數 Windows。
2. **AI CLI 整合**：claude code / codex / copilot 之配套（`mcp-config.json`）依賴 `0ops-mcp` 在 `$PATH`；安裝路徑不一致即破。
3. **版本同步**：CLI / MCP / backend 三方需相容；版本落差會破 contract test。
4. **更新流程**：v1 期間 binary 變動頻繁；user 不主動更新即會撞 deprecation。

Plan 已標 `goreleaser` + `go install` + Homebrew tap 為候選；本 ADR 把「主 / 次 / 退路」三條路徑釘定，並補入自更新通知機制。

## 2. Decision Drivers

* **DD1 macOS arm64 為主開發平台**：散布管道必須對 arm64 一級支援。
* **DD2 AI CLI 配套需 binary 在 PATH**：散布路徑需與 brew / Scoop / `go install` 三家慣例一致。
* **DD3 CI 場景需 reproducible binary**：CI 不應靠 `go install @latest` 不穩定的 latest；需 pin 版本。
* **DD4 v1 規模 ops 簡化**：不為 v1 引入完整 package manager（apt repo / yum repo）；M5 後評估。
* **DD5 商業擴展時的 distribution 矩陣**：日後可能加 Windows Store / Chocolatey；本 ADR 不阻擋擴展。
* **DD6 自更新不能干擾 stdio**：MCP binary 為 stdio 協定；版本檢查不能寫 stdout。
* **DD7 GitHub Release 為 source of truth**：所有 distribution channel 之 artifact 來源必為同一 GitHub Release。

## 3. Considered Options

針對 (a) 主散布管道 與 (e) 自更新通知做完整 alternative 比較；(b)(c)(d)(f) 列表帶過。

### 3.1 (a) 主散布管道

| Option | 描述 |
|---|---|
| **A1. goreleaser → GitHub Release（採用）+ Homebrew tap（自動）+ go install（並行）** | 三條路徑共存，主推 brew / release artifact |
| A2. 純 `go install` | 只走 Go toolchain；無預編 binary |
| A3. 自建 install script（curl pipe sh） | `curl -fsSL https://0ops.tw/install.sh \| sh` 偵測平台下載 |
| A4. 純 container image（`0ops` via `podman run`） | binary 在 image 內 |
| A5. snap / flatpak / Windows Store | 各 OS 原生 store |

### 3.2 (e) 自更新通知

| Option | 描述 |
|---|---|
| **E1. 每 24h 背景查 GitHub Release，版本落後印 hint**（採用） | 非阻擋；user 可 `--no-update-check` 關閉 |
| E2. 每次啟動都查 | 對 CI 不友善；增加每次延遲 |
| E3. 完全不查 | user 自行追版本 |
| E4. 自動下載新版 | 安全與信任邊界 |
| E5. 強制 update（拒絕舊版操作） | 強制；breaking |

### 3.3 (b)(c)(d)(f) 局部選擇

| 子決策 | 採用 | 否決 | 一句結論 |
|---|---|---|---|
| (b) Homebrew tap | 自管 `winshare/0ops` tap repo | 推 homebrew-core / 不提供 | tap 自管即時；homebrew-core 審核時程長；不提供 = 主流 macOS 用戶不便 |
| (c) Windows | v1 用 `go install` / 下載 release zip | Scoop / Chocolatey / Windows Store | v1 規模 over-engineering |
| (d) Linux package | 同上 | apt / yum / snap | 同上 |
| (f) 版本對齊 | 三 binary 同 version；backend warn header v1.1 | 各自獨立版本 / 強制阻擋 | DD3 同步需求 + DD2 AI CLI 整合 |

## 4. Decision Outcome

採用 **A1 + E1**，搭配 (b) 自管 tap、(c)(d) v1 暫不 OS package、(f) 三 binary 同 version。

具體展開：

1. **goreleaser 設定**（`src/.goreleaser.yaml`，與 Go module 同層；monorepo 慣例）：
   * 三 binary 一次 build：`0ops`（`./cmd/cli`）、`0ops-mcp`（`./cmd/mcp`）、`0ops-server`（`./cmd/server`）
   * 五平台：`linux/amd64`、`linux/arm64`、`darwin/amd64`、`darwin/arm64`、`windows/amd64`
   * Static binary（CGO 關）；`-ldflags="-s -w -X main.Version=<version>"`
   * Archive：`.tar.gz`（unix）/ `.zip`（windows）；含三 binary + LICENSE + README
   * Checksum：SHA256；GitHub Release 附一份 `checksums.txt`
2. **GitHub Release 自動化**：
   * Push tag `v<x>.<y>.<z>` → GHA workflow 跑 `goreleaser release`
   * Release notes 由 `goreleaser` 從 commit message 自動產（conventional commits 風格）
3. **Homebrew tap**：
   * Tap repo `winshare/homebrew-0ops`（外部 GitHub repo）
   * goreleaser `brews:` 區塊自動產生 formula 並 commit + push
   * 用戶：`brew tap winshare/0ops` + `brew install 0ops`
4. **`go install` 並行**：
   * `go install github.com/winshare/zeroops/cmd/cli@latest` 取最新；`@v0.5.0` 取特定版
   * binary 名稱 = `cli`（go install 之預設）；user 需自行改名為 `0ops`（README 註明）
   * MCP：`go install github.com/winshare/zeroops/cmd/mcp@latest` 同樣
5. **自更新通知（E1）**：
   * 任一 `0ops` 命令啟動時，主流程結束**後**背景 goroutine 查 GitHub API `GET /repos/winshare/zeroops/releases/latest`
   * 比對 `latest_tag` vs `main.Version`；不同即印一行至 stderr：`新版本 v0.5.1 已發佈：brew upgrade 0ops`
   * Cache：`~/.config/0ops/version-check.json`（含 `last_check_at` + `latest_known`）；24h 內不重查
   * `--no-update-check` 全域 flag 關閉
   * `OPS_NO_UPDATE_CHECK=1` 環境變數同效
   * MCP binary **不**做 update check（stdio 不能污染）；交給 CLI 處理
6. **三 binary 版本同步**：
   * 同一 git tag → 同一 goreleaser 發行 → 三 binary 同 version
   * `0ops version` / `0ops-mcp --version` / `0ops-server --version` 皆印同字串
   * v1.1：backend handler 對 `User-Agent: 0ops-cli/v0.4.0` 比對自身 version；落差 > 1 minor 時回 response header `X-0ops-Version-Warning`
7. **Signing**：
   * v1：goreleaser 之 GPG signing（artifact 簽章）；用戶可用 `gpg --verify` 驗
   * v2 評估：cosign + GitHub Sigstore（針對 container image）

## 5. Pros and Cons of the Options

### 5.1 (a) 主散布管道

#### A1. goreleaser + Homebrew + go install（採用）

* Good：覆蓋 macOS（brew）、Linux（release artifact）、CI（go install pin）三主場景。
* Good：goreleaser 為 Go 生態標準；自動化產出多平台 binary、checksum、release notes。
* Good：Homebrew tap 為 macOS 用戶 onboarding 最低摩擦。
* Good：`go install` 提供 Go developer 之熟悉路徑；無平台限制。
* Bad：散布管道增多 = 安裝指引文件需覆蓋多種；user 需選擇。
* Bad：Homebrew tap 自管 = 多一個 repo 維運。
* Bad：goreleaser config 為新 syntax，工程師需學習。

#### A2. 純 go install

* Good：無 release pipeline；最簡單。
* Bad：CI 不可重現（@latest 變動）；違反 DD3。
* Bad：非 Go 開發者無 toolchain；macOS 用戶體驗差。

#### A3. 自建 install script（curl pipe sh）

* Good：跨平台一條命令；user 體驗順。
* Bad：`curl | sh` 安全風險（信任 0ops.tw 域名 + script 內容）；user 需審 script。
* Bad：腳本本身需維護；多平台分支增加複雜度。
* Bad：與 goreleaser 配合時實質為 release artifact 之 wrapper；非主路徑。

#### A4. 純 container image

* Good：依賴零；user 跑 `podman run ghcr.io/winshare/0ops:v0.5.0 apps list`
* Bad：對 CLI 體驗不友善（每次 docker / podman startup 開銷）。
* Bad：需 mount `~/.config/0ops` / GitHub workdir；命令長。
* Bad：MCP stdio 與 container stdio 之 wiring 複雜。

#### A5. OS native store

* Good：用戶在 OS 既有管道安裝。
* Bad：snap / flatpak / Windows Store 各自審核流程；onboarding 慢。
* Bad：v1 規模 over-investment。

### 5.2 (e) 自更新通知

#### E1. 24h 背景查 + 印 hint（採用）

* Good：user 不需主動關注版本；落後即收通知。
* Good：非阻擋；CI 場景不被打斷（仍可加 `--no-update-check`）。
* Good：24h cache 降低 GitHub API rate limit 壓力。
* Bad：背景 HTTP 查可能在離線環境噪音（fail silently）；可接受。
* Bad：通知 hint 印至 stderr；script 處理 stderr 之 user 可能需 noise filter。

#### E2. 每次啟動都查

* Good：始終最新。
* Bad：CI 場景每次 +50ms 開銷；違反 DD3。
* Bad：GitHub API 配額消耗大。

#### E3. 不查

* Good：最簡單。
* Bad：user 不知道版本落後；違反 v1 期間頻繁更新需求。

#### E4. 自動下載新版

* Good：user 無感升級。
* Bad：信任邊界問題；user 應主動審計新版內容。
* Bad：權限問題（brew / system path 寫入需 sudo）。

#### E5. 強制 update

* Good：保證版本同步。
* Bad：violet UX；user 在關鍵時刻被打斷。
* Bad：違反「CLI 應幫 user 把事情做完」原則。

## 6. Consequences

### 6.1 Positive

* 三條散布管道覆蓋主要場景；macOS / Linux / CI / Windows 皆可；DD1 + DD2 達成。
* goreleaser 自動化發行；無人工 step。
* 24h 自更新 hint 平衡「user 知道有新版」與「不打斷工作流」。
* 三 binary 同 version；contract test 一致；DD3 + DD6 達成。
* GitHub Release 為單一 source of truth；checksum / signing / changelog 集中。

### 6.2 Negative

* Homebrew tap 為新運維面（雖小）；tap repo 之 PR / issue 需追蹤。
* `go install` 與 brew 並行可能造成 user 同時裝兩份；v1 採文件提示優先 brew，避免 PATH 衝突。
* 自更新查 GitHub API 可能撞 rate limit（unauthenticated 60/hr per IP）；24h cache 已大幅緩解。
* v1 不支援 Windows native package；少數 Windows 用戶體驗不佳；v1.1 補。
* 三 binary 同 release 即一次升版三個；MCP binary 升版不影響 backend，但用戶需重啟 MCP host。

### 6.3 Neutral

* `goreleaser` 之 `before:hooks` 跑 lint / test 為 CI 慣例；不在本 ADR。
* GitHub Release 之 pre-release（v0.x.0-alpha）規約屬版本管理；不在本 ADR。
* Container image（ghcr.io/winshare/0ops-server）為 backend 部署用，與 CLI 分發不同；屬 `build-pipeline-and-callback` spec。

## 7. Revisit Triggers

下列任一條件觸發時，應重新評估本 ADR 對應段：

1. **Homebrew tap 維運成本過高**：tap PR 積壓 > 7d → 評估 push 至 homebrew-core。
2. **Windows 用戶比例 > 10%**：v1.1 / v2 補 Scoop / Chocolatey；重審 (c)。
3. **GitHub Release rate limit / 配額爆**：商業擴展時 >> 1000 unique installs / day；評估自架 binary CDN。
4. **自更新 hint 用戶反饋負面**：> 5% 反映干擾 → 重審 E1 之觸發條件（如改為 7d 而非 24h）。
5. **三 binary version 落差問題凸顯**：backend version warn header（v1.1）證實有效後考慮升強制；重審 (f)。
6. **Sigstore / cosign 普及**：v2 評估升 transparent log 簽章。
7. **AI CLI 廠商提供 MCP binary auto-discover**：屆時 MCP 散布可能有新 channel；重審 (a)。

## 8. More Information

* MCP binary 散布與 SKILL.md：[ADR-0003 MCP SDK 選型](0003-mcp-sdk-selection.md) 第 4 節
* CLI 與 backend 之 contract：[`docs/features/shared-dto-and-contract/spec.md`](../features/shared-dto-and-contract/spec.md) § 8（v1 附加相容）
* `0ops version` 命令行為：[`docs/features/read-api-vertical-slice/spec.md`](../features/read-api-vertical-slice/spec.md) § 5.1（外加本 ADR § 4 第 5 點之自更新邏輯）
* GitHub Release 為 source of truth：goreleaser `releases:` 章節

## 9. Open Questions

下列問題不阻擋本 ADR 通過，但需在 v1 GA 前敲定：

1. **goreleaser 之 conventional commits 配置**：commit message style 與 release notes 自動分類規則。
2. **Homebrew tap repo 之 ops 角色**：誰 review 自動產生的 formula PR（自動 merge 還是人工）。
3. **`go install` 後 binary 改名**：user 須 `mv $GOPATH/bin/cli $GOPATH/bin/0ops`；是否提供 `0ops install-rename` helper command。
4. **GitHub Release pre-release 命名**：alpha / beta / rc 規約。
5. **GPG signing key 管理**：rotation 策略；屬 `secrets-management` 範圍但 release 簽章為獨立 key。
6. **自更新 hint 之 i18n**：v1 採英文 + 繁中混排；UTF-8 終端兼容性需測。
7. **`0ops --version` 之 build info 結構**：是否含 commit_sha、build_date、go_version；對應 backend `0ops_build_info` metric。
8. **未來容器化 CLI**（v2 web UI 場景）：CLI 在 web 端跑時的散布管道（WASM？）；屬 v2 範圍。
