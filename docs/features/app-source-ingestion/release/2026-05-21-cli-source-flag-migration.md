# CLI `--source` flag migration

> **狀態**：Released（M6 milestone）
> **適用版本**：CLI v1.x（M6 release onwards）
> **取代**：`--repo-url`（deprecated，M8 移除）

## 對外變更

新增：
- `0ops apps create --source <path|url|upload://...>`
- `POST /v1/teams/{team_slug}/uploads`（multipart streaming）
- `GET /v1/uploads/{id}/archive`（JWT-protected）
- `dto.Source` sum type (`type` ∈ `{github, upload}`)
- ADR-0013 production file-source ingestion

Deprecated：
- `--repo-url`（CLI 仍接受，印 stderr warning）
- `AppCreateRequest.RepoURL` + `Ref`（API 接受並 normalize 為 `Source.GitHub`）

移除時程：M8（或下個 major release，視 client usage 衰退數據而定，spec § 16 Q6）

## Migration cheat-sheet

| 舊用法 | 新用法 | 說明 |
|---|---|---|
| `--repo-url https://github.com/foo/bar --ref main` | `--source https://github.com/foo/bar --ref main` | GitHub URL；行為等價 |
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

若你有自動化腳本使用 `0ops apps create --repo-url`：
- **無需立即改**：CLI 仍接受，只印 stderr warning
- **建議**：M7 末更新為 `--source`，避免 M8 移除時 CI 中斷
- **server-side**：API 自動把 `repo_url + ref` 之請求 normalize 為 `Source{Type:github}`，無需 client side 配合
