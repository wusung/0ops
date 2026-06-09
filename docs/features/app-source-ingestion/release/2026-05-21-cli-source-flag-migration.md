# CLI `--source` flag migration

> **狀態**：Released（M6 milestone）；github alias 已於 M8 移除（2026-06-09）
> **適用版本**：CLI v1.x（M6 release onwards）
> **取代**：`--repo-url` 之 github URL 用法（M8 移除）；`--repo-url` 僅保留 dev `file://`（ADR-0012）

## M8 更新（2026-06-09）— github-via-`repo_url` 已移除

- `--repo-url <github URL>` / API `repo_url=<github>` / MCP `repo_url=<github>`：**移除**。
  github source 一律改走 `source` sum type（`source.github`）。
- server 端 `repo_url(github) → Source{github}` normalize shim：**刪除**。
- MCP `create_app_preview`：新增 `source` 欄位（等同 API DTO）；`repo_url`/`ref` 僅留 dev `file://`。
- 保留不動：ADR-0012 dev `file://`（`--repo-url file://` 與 `--source file://` 皆可用，spec § 2.2）。
- 觸發後行為：給 github URL 之 `repo_url` 一律報 `unsupported_source`「use source.github」。

## 對外變更

新增：
- `0ops apps create --source <path|url|upload://...>`
- `POST /v1/teams/{team_slug}/uploads`（multipart streaming）
- `GET /v1/uploads/{id}/archive`（JWT-protected）
- `dto.Source` sum type (`type` ∈ `{github, upload}`)
- ADR-0013 production file-source ingestion

Deprecated → Removed（M8，2026-06-09）：
- `--repo-url <github>`：移除（改用 `--source`）。`--repo-url file://`（dev, ADR-0012）保留。
- `AppCreateRequest.RepoURL` + `Ref` 之 github 語意：移除（不再 normalize 為 `Source.GitHub`）。
  欄位本身保留，僅服務 dev `file://` 路徑。

## Migration cheat-sheet

| 舊用法 | 新用法 | 說明 |
|---|---|---|
| `--repo-url https://github.com/foo/bar --ref main` | `--source https://github.com/foo/bar --ref main` | **M8 起 `--repo-url` github 不再接受，必須改 `--source`** |
| 無對應（本地路徑無法部署） | `--source ./my-app` | 自動 pack tar.zst + 上傳 |
| 無對應 | `--source upload://upl_xxx` | 複用既有 upload（無需重傳）|
| `--repo-url file:///workspace/x --ref main` | （不變）`--repo-url file:///workspace/x --ref main` | dev-only ADR-0012 路徑；無 `--source` 對等 |

## Source kind 分派

| 輸入前綴 | Kind | 機制 |
|---|---|---|
| `./`、`../`、`/`、`mydir`（裸名）| LocalPath | CLI pack + upload → `Source{upload, upload_id}` |
| `upload://upl_xxx` | UploadID | 直接 `Source{upload}` 不上傳 |
| `https://github.com/...`、`git@github.com:...` | GitHubURL | 直接 `Source{github}` |
| `file://...` | FileURL | Legacy `RepoURL` 路徑（dev only）|
| 其他 | rejected | error |

## 新增 CLI flags

| Flag | 預設 | 作用 |
|---|---|---|
| `--source` | `""` | 主要 input |
| `--upload-max-bytes` | 100 MiB | LocalPath 上傳之 tar size cap |
| `--upload-max-entries` | 10000 | LocalPath 上傳之 entry count cap |

## SLO 目標

- Upload p95 < 30 秒（100MB cap、10MB/s 下載基準）
- preview→live 不受新 path 影響（仍 < 10 分鐘 p50）
- Server-side quota（per-team）：
  - Free: 1 GB inert / 50 pinned / 200 daily uploads
  - Starter: 2 GB / 100 / 500
  - Pro: 5 GB / 500 / 2000
  - Team: 20 GB / 2000 / 10000

## 友善錯誤訊息

CLI 對 server 端 `*UploadError` 包裝 user-actionable hint：

| Server code | CLI 提示 |
|---|---|
| `payload_too_large` | 「加 `.dockerignore` 排除 vendor 目錄」 |
| `team_quota_exceeded` | 「等舊上傳過期或升級 plan」 |
| `unauthorized` | 「跑 `0ops auth login` refresh token」 |
| `unsupported_archive_format` | 「CLI bug，回報」 |

## v1 限制（已知，文件化）

1. **`.dockerignore` parser**：僅支援字面比對 + prefix + basename match；不支援 `**`、negation
2. **`git ls-files --recurse-submodules` 未啟用**：submodule 內容不會包進 tarball；下游 build 需要 submodule 之專案無法用 upload 路徑
3. **`.git` metadata-only**：CLI 只 pack `.git/HEAD` + refs；無完整 history。build pipeline 若依賴 commit history（如 release-please）無法用 upload 路徑
4. **TOCTOU on team quota**：兩個並發 upload 可能同時通過 quota check 而合計超 cap；server-side max archive size 限制 overshoot 上限
5. **Reserve-max quota**：團隊 inert 近 cap 時，即使 1-byte upload 也會被拒（檢查邏輯為 `inert + max_archive > cap`）
6. **Dev e2e 不覆蓋 GHA workflow**：dev compose 中無 GHA runner，deploy_run 不會 'live'；production CI 為唯一 truth source

## 引用 ADR

- [ADR-0013 Production File-Source Ingestion](../../../adrs/0013-production-file-source-ingestion.md) —— 本 release 之根決策
- [ADR-0012 Local file repo dev mode](../../../adrs/0012-local-file-repo-dev-mode.md) —— §3.1 已被 supersede
- [ADR-0005 Build pipeline and callback](../../../adrs/0005-build-pipeline-and-callback.md) —— GHA + GHCR + ArgoCD 路徑保持不動
- [Feature spec](../spec.md) —— 完整技術細節

## 補充：對既有 CI / scripts 之影響

若你有自動化腳本使用 `0ops apps create --repo-url <github>`：
- **M8 起必須改**：`--repo-url` 的 github URL 已移除，會直接報錯導向 `--source`。
- **改法**：`--repo-url https://github.com/...` → `--source https://github.com/...`（行為等價）。
- **server-side**：API / MCP 收到 github `repo_url` 一律回 `unsupported_source`，不再 normalize。
- **dev 例外**：`--repo-url file://...`（ADR-0012）不受影響，照舊可用。
