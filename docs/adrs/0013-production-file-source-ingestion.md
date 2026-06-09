---
adr: "0013"
title: Production File-Source Ingestion
status: Accepted
date: 2026-05-20
tags:
  - production
  - app-source
  - upload
  - ingestion
supersedes:
  - "0012"     # §3.1「production 必拒」條款
superseded-by: []
---

# ADR-0013：Production File-Source Ingestion

* Status：Accepted
* Date：2026-05-20
* 適用範圍：M6+；production `create_app` / `redeploy` / `update_app` 之 app source 入口
* 來源：user 指示「加上支援 file:// 做為 production 標準 support 功能」；brainstorming 拍板方案 A1（pre-upload tarball）；對應 spec [`docs/features/app-source-ingestion/spec.md`](../features/app-source-ingestion/spec.md)
* 上游依賴：[ADR-0002](0002-idempotency-and-compensation.md)（preview/confirm gate）；[ADR-0005](0005-build-pipeline-and-callback.md)（GHA + callback HMAC）；[ADR-0012](0012-local-file-repo-dev-mode.md)（dev mode file:// path，本 ADR supersede 其 §3.1）

## 0. TL;DR（先讀本段）

採用以下六項組合決策：

1. **`source` sum type 取代 `repo_url`**：對外 API 改為 `source = { type: "github" | "upload", ... }`；舊 `repo_url` 欄位保留為 deprecated alias，server normalize 至 `github` kind；`file://` 在 production 一律 422。
2. **Upload-based ingest**：CLI 本地路徑 → `POST /v1/uploads`（tarball）→ `upload_id` → preview 帶 `source.type=upload`；server 進程永遠不解析 host filesystem 路徑。
3. **Ingest tree 路徑安全三層**：server-controlled PVC；`ingestion.Store` API 唯一存取點；解壓時 path traversal / symlink / entry size 三層 hardening。
4. **GHA workflow 變體 + short-lived JWT**：新增 `deploy-app-from-upload.yml`；workflow 以 server 簽 short-lived JWT（`OPS_BUILD_TOKEN_SECRET`，TTL 15 min，scope=download-upload）反向 fetch tarball；build / push / callback 與既有路徑共用。
5. **ADR-0012 dev path 保留不變**：`file://` + `LOCAL_FILE_REPO_ENABLED` + `LocalBuildDispatcher` 仍為 dev-only 路徑；不共用 schema、不共用 dispatcher、不混淆兩條安全模型。
6. **ADR-0012 §3.1「production 必拒 file://」條款 supersede**：production `file://` 錯誤碼改為 422 `unsupported_source`（原條款之守護目的由 upload path 安全模型承接）；`runtime.AssertProductionSafe()` 語意調整為拒 `LOCAL_*_ENABLED=true`、同時要求 `APP_SOURCE_INGEST_ROOT` / `OPS_BUILD_TOKEN_SECRET` 存在。

行為與 API/schema 細節以 spec [`docs/features/app-source-ingestion/spec.md`](../features/app-source-ingestion/spec.md) 為準；本 ADR 釘住決策邊界，不重述 spec。

## 1. Context and Problem Statement

ADR-0012 在「production 必須拒絕 file:// schema」（§3.1）之前提下建立 dev mode local file repo。該決策服務 M2 demo 需求。

M6 收到新需求：production deployment 不應強制依賴 GitHub repo；用戶必須能以本地原始碼部署 managed app，且安全模型需等同或優於 github source。

現況缺三件事：

1. **production-safe 的本地原始碼傳遞機制**：`file://` 讓 server 解析 host path，屬 SSRF / local file read 攻擊面；需要一條 server 完全不碰 host path 的替代路徑。
2. **可演進的 source 對外 schema**：stringly-typed `repo_url` 難以承載多 source kind（upload、gitlab、s3）；需 sum type 設計。
3. **GHA build pipeline 的 source fetch 機制**：既有 `deploy-app.yml` 用 GitHub App token checkout；upload source 需要不同的 fetch 協定（server-signed JWT + tarball download），同時複用 build/push/callback 路徑。

ADR-0012 §3.1「production 必拒」條款是對「server 不受 host path 威脅」的保護，不是對「production 用戶能否部署本地程式碼」的永久限制。本 ADR 以 upload-based ingest 達到同等安全保護，故 supersede 該條款。

## 2. Decision Drivers

沿用 ADR-0012 DD2 / DD3 / DD5 / DD7；新增兩項：

* **DD2 介面契約穩定**（沿用）：production 與 dev 共用 `Dispatcher` 與 `Inspector` 介面；upload source 新增 `UploadInspector` 但不改介面型別
* **DD3 production 必安全**（語意調整）：production 不得暴露 host filesystem 解析；upload path 以 server-controlled ingest tree 承接此目標，取代「直接拒絕 file://」的粗粒度守護
* **DD5 無新 state**（沿用）：deploy_run state machine 為 system-wide invariant；upload source 不得新增 state、不得跳過 state
* **DD7 路徑安全可審計**（沿用）：ingest tree 寫入 / 讀取一律走 `ingestion.Store` API；handler 端禁止 `os.Open` 拼字串；所有路徑 `filepath.Clean` + `filepath.Rel` + 非 `..` 開頭三層校驗
* **DD8 server 不解析 host path**（新）：server 進程永遠不接受外部輸入之 host filesystem 路徑；upload 為 tarball blob，server 只看 Content-Type magic byte 與解壓後的 tree，不知道亦不處理 client 端的原始路徑
* **DD9 對外 schema 可演進**（新）：`source` 對外 API 採 sum type（discriminated union）；新增 source kind（gitlab、s3、oci）不破壞已有 client；`repo_url` deprecated alias 僅保留 v1 向後相容

## 3. Decision Outcome

### 3.1 `source` sum type

```go
// internal/shared/dto/source.go
type SourceKind string

const (
    SourceKindGitHub SourceKind = "github"
    SourceKindUpload SourceKind = "upload"
)

type Source struct {
    Type   SourceKind    `json:"type"`
    GitHub *SourceGitHub `json:"github,omitempty"`
    Upload *SourceUpload `json:"upload,omitempty"`
}
```

`AppCreateRequest` 加 `Source *Source`；舊 `RepoURL` / `Ref` 保留並標 deprecated。server 收到 `RepoURL` 時 normalize 為 `Source{Type:github, GitHub:{URL:..., Ref:...}}`。`file://` 在 production 一律 422 `unsupported_source`。

> **Status update（M8，2026-06-09）**：上述 github-via-`repo_url` normalize shim 已依原訂
> deprecation 時程（spec § 16 Q6）移除。github source 一律走 `Source`；github `repo_url`
> 現回 `unsupported_source`。`RepoURL`/`Ref` 欄位僅保留 ADR-0012 dev `file://` 路徑。
> 詳見 `docs/features/app-source-ingestion/release/2026-05-21-cli-source-flag-migration.md`「M8 更新」段。

### 3.2 Upload-based ingest

`POST /v1/uploads`：multipart tarball（tar.zst 或 tar.gz）→ 解壓至 `<APP_SOURCE_INGEST_ROOT>/<team_id>/<upload_id>/tree/`，寫 `uploads` metadata row，回傳 `upload_id`。

Ingest tree 佈局強制：

```
<APP_SOURCE_INGEST_ROOT>/
└── <team_id>/
    └── <upload_id>/
        ├── _archive.tar.zst
        ├── _meta.json
        └── tree/
```

### 3.3 Ingest tree 路徑安全三層

1. **存取點唯一**：所有讀寫一律走 `ingestion.Store` API；`Store.Put(teamID, archive)` 內部組路徑，`Store.Open(teamID, uploadID, relPath)` `filepath.Clean` + `filepath.Rel` + 非 `..` 開頭校驗。
2. **解壓 hardening**：tar entry path traversal 拒絕（`..` 或絕對路徑）；symlink target 必須在同 upload tree 內；單 entry 50MB cap；mode mask 0644/0755；owner 強制設 server uid。
3. **跨 team 邊界**：`APP_SOURCE_INGEST_ROOT` 必須與 ADR-0012 `LOCAL_FILE_REPO_ROOT` 不同目錄；mode 0700；租戶間無 mount 共享。

### 3.4 GHA workflow 變體 + short-lived JWT

新增 `deploy/workflows/deploy-app-from-upload.yml`：

* workflow inputs：`deploy_run_id`, `upload_id`, `fetch_token`（short-lived JWT）, `fetch_url`
* Step 1：`curl -H "Authorization: Bearer ${{ inputs.fetch_token }}" ${{ inputs.fetch_url }}` 下載 tarball
* Step 2 以後：pack build → push GHCR → callback HMAC，與 `deploy-app.yml` 完全共用

Short-lived JWT（`OPS_BUILD_TOKEN_SECRET`，HS256，TTL 15 min）：

* Subject：`upload:<upload_id>`；Audience：`gha-build`
* Claims：`team_id`, `upload_id`, `deploy_run_id`, `scope: download-upload`
* 驗章端：`GET /v1/uploads/<id>/archive`，要 JWT scope + deploy_run_id 對應 + 未過期
* 與 `OPS_CALLBACK_SECRET` 為獨立 secret；blast radius 隔離

### 3.5 Upload 生命週期

| 狀態 | 觸發 | expires_at |
|---|---|---|
| `received` | POST /v1/uploads 成功 | now + 24h |
| `pinned` | confirm 成功插 deploy_run | deploy_run.terminal_at + 7d |
| `expired` | now > expires_at | GC 刪除 ingest tree + metadata |

### 3.6 ADR-0012 dev path 保留

`file://` + `LOCAL_FILE_REPO_ENABLED` + `LocalBuildDispatcher` 保留為 dev-only；不共用 upload schema、不共用 dispatcher、不共用 ingest tree。兩條路徑之 `INGEST_ROOT` 必須不同目錄，避免 ACL / GC 混淆。

### 3.7 `runtime.AssertProductionSafe()` 語意調整

```
// 既有（ADR-0012 §3.1）：
//   panic if OPS_ENV=production AND any LOCAL_*_ENABLED=true
//
// 新（本 ADR）：
//   panic if OPS_ENV=production AND any of:
//     - LOCAL_FILE_REPO_ENABLED=true
//     - LOCAL_BUILD_ENABLED=true
//     - LOCAL_REGISTRY != ""
//   panic if OPS_ENV=production AND any unset of:
//     - APP_SOURCE_INGEST_ROOT
//     - OPS_BUILD_TOKEN_SECRET
```

`file://` 的 production 拒絕改由 `validateAppCreateRequest` 的 422 `unsupported_source` 承接；`AssertProductionSafe` 不再對 `file://` 直接 panic。

## 4. 與 ADR-0005 / ADR-0012 之關係

### 4.1 ADR-0005（構建管道與回調）—— 不動

ADR-0005 之 GHA dispatch + HMAC callback 規約對 upload source 完整保留：

* callback HMAC 不開後門：upload source 之 GHA callback 共用 `OPS_CALLBACK_SECRET` 與同一 handler
* deploy_run state machine 不分叉：upload source 走完整 state 序列（queued → building → pushing → rendering → syncing → live）
* `Dispatcher` 介面契約不變：`RoutingDispatcher` factory 依 source kind 選擇 workflow variant，不改介面型別

ADR-0005 的 `deploy-app.yml`、HMAC 計算、callback retry 規則全數保留；本 ADR 新增 `deploy-app-from-upload.yml` 作為平行變體。

本 ADR 引入之 `OPS_BUILD_TOKEN_SECRET`（scope: `download-upload`、TTL 15 min）與 ADR-0005 之 `OPS_TOKEN_SIGNING_SECRET`（scope: `ghcr-push` + `callback-write`、TTL 1h）為**獨立 secrets**，production 必須兩者皆設；不複用、不交叉簽章；`runtime.AssertProductionSafe()`（T4）對兩者各檢一次。

### 4.2 ADR-0012（Local File Repo dev mode）—— 部分 supersede

**Supersede 的條款**：§3.1「production 必拒 file://」中「啟動硬性檢查：production + LOCAL_FILE_REPO_ENABLED → server panic」那段文字中，原意圖涵蓋的「production 不得透過任何 file-like path 繞過安全邊界」的守護目的，由本 ADR 之 upload ingest 安全模型承接。

具體語意：ADR-0012 §3.1 原文「production 必拒」條款失效，以本 ADR §3.7 的 `AssertProductionSafe` 新語意取代。

**仍然有效的條款**：ADR-0012 §3.2–§3.5（介面分派、LocalBuildDispatcher 流程、路徑安全、image_ref schema）對 dev path 完整保留；DD1–DD7 中的 DD2 / DD3 / DD5 / DD7 已被本 ADR 繼承（語意調整後）；DD1 / DD4 / DD6 仍僅 dev path 適用。

## 5. Pros and Cons of the Options

對應 spec § 1 三個替代方案：

| 方案 | 描述 | 採用 |
|---|---|---|
| **A1. Pre-upload tarball（本 ADR）** | CLI tar → POST /v1/uploads → upload_id → preview | ✅ |
| A2. Server-side git clone from URL | `create_app` 帶 git URL（非 GitHub），server clone | ✗ |
| A3. OCI artifact（push image，不走 GHA build） | CLI build image → push OCI registry → server pull | ✗ |

### A1（採用）

**Pros**：
* server 完全不解析 host path（DD8）；tarball 為 content-addressable blob，安全邊界清晰
* CLI 體感最接近「就把這個目錄部署」；`--source ./my-app` 即可
* GHA build/push/callback 路徑共用；新增僅為 workflow 入口與 fetch step
* Upload artifact 可被多次 `redeploy` 引用，不需重新 tar
* DTO `source` sum type 可演進；A2/A3/gitlab/s3 可後續新增 kind

**Cons**：
* 100MB tarball cap 對大型 monorepo 不友善；需 `.dockerignore` 教育成本
* PVC 運維面新增：ingest tree 需 GC reconciler、quota 管理、disaster recovery 路徑
* GHA self-hosted runner 若在私有網路，需能訪問 `GET /v1/uploads/<id>/archive`（spec § 16 open question）
* CLI 端 git submodule 不支援（v1 已知限制）

### A2（否決）

* Server 需走 git clone；可能暴露 server 對外發出 HTTP/SSH 請求，引入 SSRF 攻擊面
* 非 GitHub git URL 的 auth 機制未定義；對私有 git server 無通用解
* 違反 DD8（server 需解析 / 處理 URL 中的 host 路徑）

### A3（否決）

* 要求 CLI 端有 container runtime 與能 push 的 OCI registry；本地依賴大幅增加
* image 為 build artifact 而非 source；失去 paketo buildpacks 對語言 / framework 的探偵能力
* ADR-0005 GHA + paketo 路徑整體廢棄；重構成本過大

## 6. Consequences

### 6.1 正面

* production 解除 GitHub repo 強制依賴；`upload` 與 `github` 為對等的 source kind
* `source` sum type 為多 source kind 奠基；`gitlab` / `s3` / `oci` 可作 open extension point
* CLI 體感：`0ops apps create --source ./my-app --slug demo` 即可部署本地原始碼，與雲端部署同一流程
* `ingestion.Store` 封裝使路徑安全可單元測試；GHA workflow 變體可 e2e 測試（compose + `make`）
* Upload artifact 可跨 deploy_run 復用（同 upload_id 多次 redeploy）

### 6.2 負面

* **PVC 運維面**：server pod 需掛 PVC（`APP_SOURCE_INGEST_ROOT`）；production chart 需明確配置 storage class、容量、backup 策略；v1「single PVC + team 子目錄」在 team 數量大時可能成 I/O 瓶頸
* **Tarball 大小成本**：upload 為 server 存儲成本；100MB cap + team 配額（未 pinned 累計 1GB）為一線防護，超出需 plan-tier 管控
* **GHA fetch network**：short-lived JWT fetch URL 必須從 GHA runner 可達；self-hosted runner 在私有網路時需額外 `OPS_API_PUBLIC_URL` 配置（spec § 16 Q1）
* **CLI 體感變化**：舊 `--repo-url` 移至 deprecated；`--source` 為新主旗標；需 deprecation warning + 文件（T23）
* **deprecated `repo_url` removal timeline**：v1 保留；M8（或下個 major）移除；需 changelog

### 6.3 中性

* `OPS_BUILD_TOKEN_SECRET` 為新 secret；rotation 90 天，與 `OPS_CALLBACK_SECRET` 獨立管理
* `APP_SOURCE_UPLOAD_ENABLED` 預設 true；可設 false 做 incident response 快速關閉 upload feature
* upload path 的 Inspector（`UploadInspector`）與 github path（`GitHubInspector`）共用 `Inspector` 介面；`SourceFactory` 依 kind 分派，不改介面型別

## 7. Revisit Triggers

* **OCI artifact registry 成熟**：若未來引入 OCI artifact spec（每個 upload 為 OCI artifact），評估是否完整取代 ingest tree PVC（屬獨立 ADR）
* **self-hosted runner 網路拓撲**：若企業客戶之 self-hosted runner 無法訪問 public API，需評估 `OPS_API_INTERNAL_URL` 分流機制（spec § 16 Q1）
* **GitHub 依賴完整移除**：若未來目標為完全脫離 GitHub App（source + build + push），整個 ADR-0005 + ADR-0013 dispatch 路徑需重新評估（屬 M8+ 架構變更）
* **PVC I/O 瓶頸**：team 數量大、upload 並發高時 single PVC + team 子目錄可能成瓶頸 → 評估 per-team PVC 或 object storage（S3/Ceph）替代
* **100MB cap 頻繁觸發**：若監測到 `app_source_upload_total{result=rejected, reject_reason=payload_too_large}` 顯著上升 → 評估調整 cap 或引入大檔分片上傳（spec § 2.2 排除項）

## 8. More Information

* **Feature spec**：[`docs/features/app-source-ingestion/spec.md`](../features/app-source-ingestion/spec.md)（行為、API schema、失敗矩陣、觀測 metric 細節以本檔為準）
* **ADR-0002 冪等性與補償**：[0002-idempotency-and-compensation.md](0002-idempotency-and-compensation.md)（preview/confirm gate；upload source 為 side_effects 計算之分支輸入，框架不動）
* **ADR-0005 構建管道與回調**：[0005-build-pipeline-and-callback.md](0005-build-pipeline-and-callback.md)（GHA dispatch + HMAC callback；本 ADR 新增 workflow 變體但不改協定）
* **ADR-0012 Local File Repo dev mode**：[0012-local-file-repo-dev-mode.md](0012-local-file-repo-dev-mode.md)（dev path 保留；§3.1 production 必拒條款 superseded by 本 ADR）
* **Implementation plan**：[`docs/features/app-source-ingestion/draft/2026-05-20-app-source-ingestion-plan.md`](../features/app-source-ingestion/draft/2026-05-20-app-source-ingestion-plan.md)（23 task 實作計畫）

## 9. Open Questions

與 spec § 16 對齊：

1. **GHA self-hosted runner 之 fetch 路徑**：若 team 設了 self-hosted runner，workflow 從 runner 到 0ops API 之網路路徑可能受限；v1 暫定走 `OPS_API_PUBLIC_URL`，self-hosted runner 必須能訪問 public API。是否需 `OPS_API_INTERNAL_URL` 區分留待後續 ADR。
2. **CLI 端 git submodule**：v1 走 `git ls-files --recurse-submodules`，但 submodule `.git` 目錄不 tar；workflow 端重新 init submodule 之 build 支援暫不實作；偵測到含 submodule 之 upload 發 warning（不 fail）。
3. **OCI artifact registry 取代 ingest tree**：未來若引入 OCI artifact spec，是否完整取代 PVC？屬獨立 ADR，本 ADR 預留 `Store` 介面抽象作為替換點。
4. **CLI 端 .git 完整保留 vs metadata-only**：v1 metadata-only（commit_sha + ref name）。若 builder 需要完整 git history 會失效；v1 已知限制。
5. **跨 deploy_run 之 upload 復用**：同 upload_id 多次 redeploy 每次仍產生新 image（`deploy_run_id` tag）；`upload_id` 作為 deduplication key 之 image cache 優化 v1 不做。
6. **`repo_url` deprecation 之最終下車站**：本 ADR 規約 v1 保留、M8（或下個 major）移除（見 §6.2）。實際下車時機應視 M6→M8 期間 `repo_url` 之 client 使用率衰退數據而定；若 M7 末 client 仍 >5% 使用 `repo_url`，需評估展延一個 major。tracking metric 與 alert threshold 屬獨立 observability spec。
