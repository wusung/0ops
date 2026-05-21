# Feature Spec：app-source-ingestion

> **狀態**：draft
> **來源**：user 2026-05-20 指示「加上支援 file:// 做為 production 標準 support 功能」；brainstorming 拍板方案 A1（pre-upload tarball）
> **適用範圍**：app source 之 ingestion 機制；`create_app` / `redeploy` / `update_app` 共用
> **對應 Milestone**：M6（建議；最終以 milestone 規劃為準）
> **上游 ADR**：[ADR-0002](../../adrs/0002-idempotency-and-compensation.md)（preview/confirm + saga）、[ADR-0005](../../adrs/0005-build-pipeline-and-callback.md)（GHA + callback HMAC）、[ADR-0012](../../adrs/0012-local-file-repo-dev-mode.md)（dev mode file:// host bind mount）
> **新增 ADR**：ADR-0013「Production File-Source Ingestion」（本 spec 對應之決策；supersede ADR-0012 §3.1「production 必拒」條款）

## 1. 結論（先讀本段）

- production 新增第二類 app source：`upload`。對外 API 不再以 stringly-typed `repo_url` 為主，改為 sum type `source = github | upload`。
- 舊欄位 `repo_url` 保留為向後相容；server 收到時內部 normalize 為 `{type:"github", url:...}`。`file://` 在 production API 一律拒絕。
- **server 進程永遠不解析 host filesystem 路徑**。upload 內容只能進 server-controlled ingest tree，由 server uid 寫入、租戶子前綴強制，租戶間無 mount 共享。
- CLI 端 `--source` 接受裸路徑 / 相對路徑 / git URL；裸路徑/相對路徑自動走「本地 tar → POST /v1/uploads → 取得 upload_id → preview 帶 source.type=upload」流程，user 體感不變。
- Build pipeline 維持 ADR-0005（GHA + GHCR + ArgoCD）。新增 workflow 變體 `deploy-app-from-upload.yml`：workflow 用 server 簽的 short-lived JWT 反向 fetch tarball，後續 build / push / callback 與既有路徑共用。
- ADR-0012 dev mode（`file://` + `LOCAL_FILE_REPO_ENABLED` + host bind mount + LocalBuildDispatcher）**保留**為 dev-only 路徑，不與 production upload path 共用 schema、不共用 dispatcher，不混淆兩條安全模型。
- `runtime.AssertProductionSafe()` 語意調整：production 仍拒 `LOCAL_FILE_REPO_ENABLED=true`，但 upload path 不受其管轄。
- 端到端 SLO 目標延續 create-app-flow（preview 至 live < 10 分鐘 p50）；upload 階段不應佔超過 30 秒（100MB cap，10MB/s 下載基準）。

## 2. 範圍

### 2.1 包含

- `POST /v1/uploads` authenticated endpoint：multipart tarball / git bundle 上傳
- Upload storage layout：server-controlled ingest tree（租戶子前綴、content-addressable、TTL、GC）
- `source` sum type DTO 定義（CLI / API / MCP 對齊；shared-dto-and-contract 同步）
- CLI 端 `--source` 解析與分派
- CLI 端 tarball 打包（`git ls-files` 為主、`.dockerignore` fallback、size cap、預打包預覽）
- `create_app` preview / confirm 改為接 `source`；舊 `repo_url` 路徑保留為 deprecated alias
- `redeploy` / `update_app` 對 upload source 之契約（既有 upload 可被新 deploy_run 重複引用）
- GHA workflow `deploy-app-from-upload.yml`：tarball download → pack build → push GHCR → callback
- Server 簽 short-lived JWT 給 workflow（scope=download-upload，TTL 15 min）
- Inspector 介面新增 `UploadInspector`：讀 server-side ingest tree 取 metadata（commit_sha 可選、builder hint、framework detect）
- 配額與 audit：每 team 之 upload size 與 count 上限、abuse 限流
- runtime.AssertProductionSafe 語意調整與測試覆蓋
- 觀測 metric：upload latency、size、reject ratio、build path 分流（github vs upload）

### 2.2 不包含

- ADR-0012 dev mode 之 file:// + `LOCAL_FILE_REPO_ENABLED` + LocalBuildDispatcher：保留現況，不在本 spec 改動
- LocalBuildDispatcher 升格為 production dispatcher：不做（保留 ADR-0005）
- 新增其他 source kind（`gitlab`、`bitbucket`、`s3`、`oci-image`）：schema 預留 enum 擴充點，實作不在本 spec
- 對外 unauthenticated upload / public ingestion：不做
- 大檔分片上傳 / resume：v1 簡化為單次 multipart，超過 cap 直接 reject
- CLI 端對 binary blob / large monorepo 之 chunking：v1 由 ignore filter 解決
- Upload 之版本比對 / diff：v1 每次 upload 為獨立 immutable artifact

## 3. 檔案結構

```
0ops/
├── internal/server/
│   ├── apps.go                                # source sum type validation；舊 repo_url alias 處理
│   └── services/
│       ├── createapp/
│       │   ├── source.go                      # 新：Source / SourceKind 抽象；factory 分派 Inspector
│       │   ├── upload_inspect.go              # 新：UploadInspector
│       │   └── ingestion/                     # 新：upload 子模組
│       │       ├── store.go                   # 新：ingest tree 寫入 / 讀取 / GC
│       │       ├── handler.go                 # 新：POST /v1/uploads handler
│       │       ├── handler_test.go            # 新
│       │       ├── token.go                   # 新：short-lived JWT 簽發 / 驗證
│       │       └── token_test.go              # 新
│       └── ...
├── internal/cli/
│   ├── apps_create.go                         # 改：--source 旗標；裸路徑分派
│   └── upload.go                              # 新：tarball 打包 + multipart upload client
├── internal/shared/dto/
│   ├── source.go                              # 新：Source sum type
│   └── apps.go                                # 改：AppCreateRequest 加 Source；repo_url deprecation
├── internal/shared/runtime/
│   └── production_safety.go                   # 改：語意調整 + 新測試
├── deploy/workflows/
│   ├── deploy-app.yml                         # 既有：github source
│   └── deploy-app-from-upload.yml             # 新：upload source
├── docs/adrs/
│   └── 0013-production-file-source-ingestion.md   # 新 ADR
└── docs/features/app-source-ingestion/
    ├── spec.md                                # 本檔
    └── draft/                                 # 歷史草稿
```

## 4. Source Sum Type Schema

### 4.1 DTO（`internal/shared/dto/source.go`）

```go
type SourceKind string

const (
    SourceKindGitHub SourceKind = "github"
    SourceKindUpload SourceKind = "upload"
)

// Source is a discriminated union; exactly one of GitHub / Upload is non-nil.
type Source struct {
    Type   SourceKind   `json:"type"`
    GitHub *SourceGitHub `json:"github,omitempty"`
    Upload *SourceUpload `json:"upload,omitempty"`
}

type SourceGitHub struct {
    URL string `json:"url"`            // https://github.com/<owner>/<repo>
    Ref string `json:"ref"`            // branch / tag / commit sha
}

type SourceUpload struct {
    UploadID string `json:"upload_id"` // upl_<26-char ulid>
    Ref      string `json:"ref,omitempty"` // optional logical tag for audit (e.g. "main", "v1.2.3"); not used for dispatch
}
```

### 4.2 AppCreateRequest 變更

```go
type AppCreateRequest struct {
    Slug    string  `json:"slug"`
    Source  *Source `json:"source,omitempty"`   // 新欄位

    // Deprecated: use Source instead. Retained for v1 backward compat.
    // Server normalizes RepoURL+Ref into Source{Type:github, GitHub:{...}}.
    RepoURL string  `json:"repo_url,omitempty"`
    Ref     string  `json:"ref,omitempty"`

    Builder string  `json:"builder,omitempty"`
}
```

### 4.3 驗證規則

| 條件 | 行為 |
|---|---|
| `Source != nil` 且 `RepoURL != ""` | 422 `source_conflict`：擇一 |
| `Source.Type == github` 且 `Source.GitHub == nil` | 422 `source_invalid` |
| `Source.Type == upload` 且 `Source.Upload == nil` | 422 `source_invalid` |
| `Source.Type` 未知 | 422 `source_kind_unsupported` |
| `Source == nil` 且 `RepoURL` 為空 | 422 `source_required` |
| `RepoURL` 為 `file://`（production） | 422 `unsupported_source` |
| `RepoURL` 為 `file://`（dev + LOCAL_FILE_REPO_ENABLED） | 走 ADR-0012 dev path（不變） |

## 5. Upload API

### 5.1 Endpoint：`POST /v1/uploads`

- Auth：標準 bearer token（與 apps API 同 RBAC）
- Content-Type：`multipart/form-data`
- Fields：
  - `team_id`（form field 或自 token claim 解析）
  - `archive`（multipart file；tar.zst 或 tar.gz；server 偵測 magic byte）
  - `sha256`（optional；client 端 hash，server 驗證）
- Response 201：
  ```json
  {
    "upload_id": "upl_01HZX0...",
    "team_id": "team_...",
    "size_bytes": 4521098,
    "sha256": "...",
    "expires_at": "2026-05-21T10:30:00Z",
    "received_at": "2026-05-20T10:30:00Z"
  }
  ```
- 失敗碼：
  - 413 `payload_too_large`（超過 100MB）
  - 422 `unsupported_archive_format`（非 tar.zst / tar.gz）
  - 422 `archive_corrupt`（解壓失敗）
  - 422 `sha256_mismatch`
  - 429 `upload_rate_limited`
  - 507 `team_quota_exceeded`

### 5.2 副作用

- 解壓到 `<INGEST_ROOT>/<team_id>/<upload_id>/` 之臨時目錄
- `git init`（若內容無 `.git` 則建一個 empty repo，避免 LocalInspector 之 commit_sha 假設失效）
- 寫入 metadata row：`uploads` 表（id, team_id, size_bytes, sha256, status, expires_at, ...）
- audit log：`app_source.upload.created`

### 5.3 不做

- 不立即觸發 build（preview/confirm gate 未走完前 upload 為 inert artifact）
- 不解析語言 / framework（延後到 UploadInspector 在 preview 階段做）

## 6. CLI 行為

### 6.1 `0ops apps create`

```bash
# 裸路徑（自動走 upload pipeline）
0ops apps create --source /home/foxdie/Projects/my-app --slug demo --yes

# 相對路徑（同上；CLI 解析絕對路徑後送 upload）
0ops apps create --source ./examples/node-demo --slug demo --yes

# git URL（直接送 server，走 github source）
0ops apps create --source https://github.com/foo/bar --ref main --slug demo --yes

# upload_id（已上傳的 artifact 直接複用）
0ops apps create --source upload://upl_01HZX0... --slug demo --yes

# 舊寫法（deprecated 但可用）
0ops apps create --repo-url https://github.com/foo/bar --ref main --slug demo
```

### 6.2 CLI 端 source 分派

| `--source` 前綴 | CLI 行為 |
|---|---|
| `/`, `./`, `../` | tar 本地路徑 → POST /v1/uploads → 取 upload_id → preview |
| `file://` | production：reject「dev-only writeoff」；dev：直接送 server 走 ADR-0012 path |
| `upload://upl_...` | 直接送，不重新 upload |
| `https://github.com/` / `git@github.com:` | 走 github source |
| 其他 | reject `unsupported_source_kind` |

### 6.3 CLI 端 tarball 打包

- 若目錄為 git repo：`git ls-files --recurse-submodules`（含 untracked 但 staged 之檔）+ `.git/HEAD`、`.git/refs/heads/<current-branch>`、`.git/packed-refs`（僅 commit-sha 必要 metadata；非整個 `.git` 目錄）
- 若非 git repo：用 `.dockerignore` filter；無 `.dockerignore` 則 fallback 到「排除 `node_modules/`, `__pycache__/`, `.venv/`, `target/`, `dist/`, `build/`」
- 壓縮：tar.zst（zstd level 3）
- 大小檢查：超過 100MB 直接 fail 並提示加 `.dockerignore`
- 檔案數檢查：超過 10000 直接 fail
- CLI 印出預覽：`Packaging 47 files, 3.2 MB → uploading...`

## 7. Server 端 Ingestion 流程

### 7.1 Ingest tree 佈局

```
<INGEST_ROOT>/                      # ENV: APP_SOURCE_INGEST_ROOT；預設 /var/lib/0ops/uploads
└── <team_id>/
    └── <upload_id>/
        ├── _archive.tar.zst        # 原檔保留（可重新解壓做驗證）
        ├── _meta.json              # size, sha256, received_at, archive_format
        └── tree/                   # 解壓後內容
            ├── .git/
            ├── package.json
            └── ...
```

- `APP_SOURCE_INGEST_ROOT` 為 server container 內部路徑；**不允許從 host bind mount tenant-shared 目錄**。production deployment chart 強制此路徑指向 server pod 之 PVC。v1 採「single PVC + team 子目錄」（運維面最小、可擴展至數百 team）；租戶隔離靠目錄 + filesystem mode 0700 + server 程式碼路徑校驗三層。enterprise tier「per-team PVC」列入 Open Questions / 後續 ADR。
- 寫入時 owner uid = server process uid；mode 0700
- 任何 read 一律走 `internal/server/services/createapp/ingestion/store.go` API；**禁止 handler 端直接 os.Open 拼字串**

### 7.2 Preview / Confirm 串接

```
client POST /v1/apps/preview { source: { type:"upload", upload:{ upload_id:"upl_..." } } }
  ↓
server validates upload_id：team scope、未過期、status=ready
  ↓
SourceFactory(source) → UploadInspector
  ↓
UploadInspector reads <INGEST_ROOT>/<team>/<id>/tree/
  → detect builder (paketo / explicit --builder)
  → read .git/HEAD for ref display
  → return RepoMetadata
  ↓
preview side_effects 計算（與 github source 相同 5 項）
  ↓
client POST /v1/apps/confirm { preview_id:..., preview_token:... }
  ↓
server emits deploy_run + workflow_dispatch:
  workflow=deploy-app-from-upload.yml
  payload:{
    deploy_run_id, app_slug, team_id,
    upload_id, fetch_token:"jwt://...",
    fetch_url:"https://<API>/v1/uploads/<id>/archive"
  }
```

### 7.3 Upload 引用（pin）

- preview 階段 upload 仍 inert（可被同 team 之另一 preview 同時引用）
- confirm 成功插 deploy_run → upload `pinned_at = now()`，TTL 延長至 `deploy_run.terminal_at + 7 days`
- deploy_run terminal（`live` / `failed` / `rolled_back`）後 7 天 GC

## 8. Build Pipeline 整合

### 8.1 GHA workflow `deploy-app-from-upload.yml`

```yaml
on:
  workflow_dispatch:
    inputs:
      deploy_run_id: { required: true }
      app_slug: { required: true }
      team_id: { required: true }
      upload_id: { required: true }
      fetch_token: { required: true }   # short-lived JWT
      fetch_url: { required: true }     # https://<api>/v1/uploads/<id>/archive

jobs:
  build:
    steps:
      - name: Fetch tarball from 0ops
        run: |
          curl -sSL -H "Authorization: Bearer ${{ inputs.fetch_token }}" \
               -o /tmp/source.tar.zst \
               "${{ inputs.fetch_url }}"
          mkdir -p ./src && tar --zstd -xf /tmp/source.tar.zst -C ./src

      - name: Pack build
        run: |
          pack build "${{ env.IMAGE_REF }}" --path ./src --builder paketobuildpacks/builder:base

      - name: Push GHCR
        run: docker push "${{ env.IMAGE_REF }}"

      - name: Callback 0ops
        run: |
          curl -X POST "${{ vars.OPS_CALLBACK_URL }}" \
               -H "X-OPS-Signature: $(...)" \
               -d '{"state":"live", ...}'
```

### 8.2 短期 JWT（fetch_token）

- Issuer：0ops server
- Subject：`upload:<upload_id>`
- Audience：`gha-build`
- TTL：15 分鐘
- Claims：`team_id`, `upload_id`, `deploy_run_id`, `scope: download-upload`
- Sign：HS256 with `OPS_BUILD_TOKEN_SECRET`（與 callback HMAC 不同 secret）
- 驗章端：`GET /v1/uploads/<id>/archive`：要 JWT、scope=download-upload、deploy_run_id 對得上、未過期

### 8.3 與 github source 之差異

| 階段 | github source | upload source |
|---|---|---|
| Inspector | GitHubInspector（GitHub App token） | UploadInspector（讀 ingest tree） |
| Workflow | `deploy-app.yml` | `deploy-app-from-upload.yml` |
| Source fetch | workflow checkout（GitHub App） | workflow curl + bearer JWT |
| Build step | pack build | pack build（同） |
| Push | GHCR | GHCR（同） |
| Callback | HMAC（同） | HMAC（同） |
| deploy_run state | 同 | 同 |

差異僅在 Inspector 與 workflow 入口；build 之後完全共用。

## 9. 路徑安全與租戶隔離

### 9.1 不變式

1. server 程式碼**永遠**不接受外部輸入之 host path
2. ingest tree 寫入只能透過 `ingestion.Store.Put(teamID, archive)`；該函式內部組路徑 `<INGEST_ROOT>/<teamID>/<uploadID>/`，外部無法注入 `..`
3. ingest tree 讀取只能透過 `ingestion.Store.Open(teamID, uploadID, relPath)`；該函式 `filepath.Clean` + `filepath.Rel(<root>, ...)` 後校驗非 `..` 開頭
4. UploadInspector 對 ingest tree 之讀取一律走 `ingestion.Store`，禁止 `os.Open`
5. 不接受 symlink 跨 team 邊界：解壓時 reject 任何 symlink target 在 `<INGEST_ROOT>/<teamID>/` 之外
6. INGEST_ROOT 必須與 ADR-0012 之 `LOCAL_FILE_REPO_ROOT` **不同目錄**，避免兩條 path 之 ACL / GC 混淆

### 9.2 解壓時 hardening

- tar entry size cap（單檔 50MB；超過 reject）
- tar entry path traversal check（`..` 或絕對路徑 reject）
- tar entry symlink target 必須在同 upload tree 內
- tar entry mode：mask 到 0644 / 0755（避免奇怪 setuid）
- tar entry owner：強制設為 server uid（解壓不保留原 uid/gid）

## 10. 生命週期與 GC

| 狀態 | 觸發 | 動作 |
|---|---|---|
| `received` | POST /v1/uploads 成功 | 寫入 metadata；expires_at = now + 24h |
| `pinned` | deploy_run 引用且 confirm 成功 | expires_at = deploy_run.terminal_at + 7d |
| `expired` | now > expires_at | 標記 expired；下次 GC 刪 |
| `gc'd` | GC 跑完 | 刪除 ingest tree + metadata（保留 audit log） |

GC：reconciler 每小時掃一次 `WHERE expires_at < now() AND status != 'gc''d'`；刪除 ingest tree（先 rename 到 `_trash/`，下次再實刪，避免 race）。

## 11. 配額與限制

| 限制 | 預設 | 可調 |
|---|---|---|
| 單檔大小 | 100 MB | ENV `APP_SOURCE_UPLOAD_MAX_BYTES` |
| 單 tar entry 大小 | 50 MB | ENV `APP_SOURCE_TAR_ENTRY_MAX_BYTES` |
| Tar entry 總數 | 10000 | ENV `APP_SOURCE_TAR_MAX_ENTRIES` |
| Team 同時 pinned upload | 50 | per-plan（plan-tier-capability-matrix）|
| Team 每日 upload count | 200 | per-plan + rate-limit-and-abuse |
| Team 累計 inert（未 pinned）大小 | 1 GB | per-plan |

超限：413 / 429 / 507 並寫 audit。

## 12. runtime.AssertProductionSafe 語意調整

```go
// 既有：
//   panic if OPS_ENV=production AND any LOCAL_*_ENABLED=true
//
// 新：
//   panic if OPS_ENV=production AND any of:
//     - LOCAL_FILE_REPO_ENABLED=true        // dev mode host bind mount
//     - LOCAL_BUILD_ENABLED=true            // LocalBuildDispatcher
//     - LOCAL_REGISTRY != ""                // localhost:5000 registry
//
//   APP_SOURCE_INGEST_ROOT、OPS_BUILD_TOKEN_SECRET 為 production 必要 env：
//   panic if OPS_ENV=production AND any unset.
//
//   APP_SOURCE_UPLOAD_ENABLED 預設 true；可顯式設 false 關閉 upload feature
//   （e.g. 災後 incident response）。
```

測試覆蓋：`internal/shared/runtime/production_safety_test.go` 加 matrix。

## 13. 與既有 ADR / spec 對齊

| 參照 | 對齊方式 |
|---|---|
| ADR-0002 | preview/confirm gate 通用機制不變；upload source 為 preview side_effect 計算之輸入分支 |
| ADR-0005 | GHA + GHCR + ArgoCD 路徑不變；新增 workflow 變體共用 callback HMAC 規約 |
| ADR-0012 | dev mode `file://` 路徑保留；本 spec 對應 ADR-0013 supersede 0012 §3.1「production 必拒」條款；其餘 0012 規約（DD1-DD7）對 dev path 仍有效 |
| create-app-flow | spec § 4.2 / § 5.1 / § 6 之 deploy_run state machine 對 upload source 不變；§ 5.2 五項 side_effects 計算延伸 upload 分支 |
| preview-confirm-gate | Action 介面三函式（SideEffects/Precheck/Execute）不變；UploadInspector 為 SideEffects 之 dependency |
| build-pipeline-and-callback | callback HMAC、retry、簽章驗證不變 |
| shared-dto-and-contract | DTO 加 Source sum type；CLI / API / MCP 三端對齊 |
| error-model | 加 `source_conflict` / `source_invalid` / `source_kind_unsupported` / `source_required` / `unsupported_source` / `payload_too_large` / `unsupported_archive_format` / `archive_corrupt` / `sha256_mismatch` / `upload_rate_limited` / `team_quota_exceeded` 錯誤碼 |
| rate-limit-and-abuse | upload endpoint 納入 quota 與 limiter |
| audit-log | upload created/pinned/expired/gc 事件納 audit |

## 14. 失敗矩陣

| 階段 | 失敗 | 對外回應 | 內部處理 |
|---|---|---|---|
| Upload | size > cap | 413 | 不寫 metadata |
| Upload | sha256 mismatch | 422 | 不寫 metadata；audit |
| Upload | 解壓失敗 | 422 | 刪臨時目錄；audit |
| Upload | path traversal / symlink escape | 422 | 刪臨時目錄；audit `app_source.upload.malicious` |
| Preview | upload_id 不存在 | 404 `source_not_found` | — |
| Preview | upload_id 屬他 team | 404（同上，不洩漏存在性） | audit `app_source.upload.cross_team_access_denied` |
| Preview | upload expired | 410 `source_expired` | — |
| Confirm | upload pin 與 deploy_run 寫入競態 | DB unique constraint → 409 `preview_consumed` | 與既有 preview-confirm-gate 失敗處理一致 |
| Build (workflow) | fetch_token 過期 | workflow fail → callback `failed/source_unavailable` | deploy_run 走 compensating |
| Build (workflow) | fetch 不到 archive（GC 過快） | workflow fail → callback `failed/source_gone` | 警告 GC 規則錯；audit |
| Build | pack build 失敗 | callback `failed/<class>` | 與 github source 共用 |

## 15. 觀測

新增 metric：

| Metric | Type | Label |
|---|---|---|
| `app_source_upload_total` | counter | `result=success/rejected`, `reject_reason` |
| `app_source_upload_size_bytes` | histogram | — |
| `app_source_upload_duration_seconds` | histogram | — |
| `app_source_inert_bytes` | gauge | `team_id` |
| `app_source_pinned_bytes` | gauge | `team_id` |
| `app_source_gc_deleted_total` | counter | — |
| `deploy_run_total` | counter（既有） | 加 `source_kind=github/upload` label |

Dashboard：observability-skeleton 加 upload pane（latency p50/p95、reject 率、per-team inert size）。

## 16. Open Questions

1. **GHA self-hosted runner 之 fetch 路徑**：若 team 設了 self-hosted runner（plan tier capability），workflow 從 runner 到 0ops API 之網路路徑可能受限；是否需提供 `OPS_API_PUBLIC_URL` vs `OPS_API_INTERNAL_URL` 區分？v1 暫定走 `OPS_API_PUBLIC_URL`，self-hosted runner 必須能訪問 public API。

   [Status 2026-05-21]: 待 production CI rollout 驗證；dev e2e (T22) 不覆蓋 workflow 端

2. **CLI 端 git submodule**：v1 走 `git ls-files --recurse-submodules`，但 submodule 之 .git 目錄不 tar；workflow 端是否需要重新 init submodule？v1 暫定不支援 submodule 之 build；偵測到含 submodule 之 upload → warning（不 fail）。

   [Resolved 2026-05-21]: v1 不支援 submodule 內容打包；T16 實作採 `--cached --others --exclude-standard`（無 `--recurse-submodules`）；docs/release migration doc § "v1 限制" 文件化

3. **OCI artifact registry 取代本機 ingest tree**：未來若引入 OCI artifact spec（每個 upload 為 OCI artifact），是否完整取代本機 ingest tree？屬獨立 ADR。

   [Status 2026-05-21]: v1 不採；獨立 ADR 待寫

4. **CLI 端 .git 完整保留 vs metadata-only**：v1 metadata-only（commit_sha + ref name）。若 builder 需要完整 git history（如 release-please）會失效；v1 已知限制，列入後續。

   [Resolved 2026-05-21]: v1 metadata-only 確認；T16 僅 pack `.git/HEAD` + refs；依賴完整 git history 之 builder 無法用 upload 路徑（migration doc § "v1 限制" 第 3 條）

5. **跨 deploy_run 之 upload 復用**：同 upload_id 多次 redeploy 之 image_ref schema —— 仍維持 `<deploy_run_id>` tag，每次 build 產生新 image；不複用既有 image。是否值得加 `upload_id` 作為 deduplication key？v1 不做，避免 stale image 觀感問題。

   [Status 2026-05-21]: T13 + T18 已支援 upload_id 複用；`--source upload://upl_xxx` 為 user-facing entry point；不加 dedup key 之決定不變

6. **deprecated `repo_url` 之 removal timeline**：v1 保留；M8（或下個 major）移除。需 changelog + CLI deprecation warning。

   [Status 2026-05-21]: M8 目標不變；T23 release migration doc + CLI warning 文案已加

## 17. Release status

| Milestone | Status | Notes |
|---|---|---|
| M6 implementation (T1-T22) | Completed 2026-05-21 | 23 PRs merged sequentially |
| M6 release migration | Released 2026-05-21 | See [release/2026-05-21-cli-source-flag-migration.md](release/2026-05-21-cli-source-flag-migration.md) |
| Production CI workflow validation | Pending | T15 deploy-app-from-upload.yml 待第一次 production deploy 驗 |
| `repo_url` deprecation removal | Target M8 | Q6 |
