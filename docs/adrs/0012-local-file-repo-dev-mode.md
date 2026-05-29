---
adr: "0012"
title: Local File Repo 與 Local Build Pipeline（dev mode）
status: Accepted
date: 2026-05-17
tags:
  - dev
  - build-pipeline
  - file-repo
  - dispatcher
  - testability
supersedes: []
superseded-by:
  - "0013"   # §3.1 only — see ADR-0013 §4.2 for boundary
---

# ADR-0012：Local File Repo 與 Local Build Pipeline（dev mode）

* Status：Accepted
* Date：2026-05-17
* 適用範圍：`./manage.sh dev` 與 CI dev compose；**production 必須拒絕**
* 來源：user 指示「`repo_url` 加上支援 `file:///` 寫法，可以測試」；
  上游 ADR：[ADR-0005 構建管道與回調](0005-build-pipeline-and-callback.md)（production GHA 規約不動）；
  [ADR-0002 冪等性與補償](0002-idempotency-and-compensation.md)（preview/confirm 介面契約不動）
* 上游依賴：ADR-0002、ADR-0005

## 0. TL;DR（先讀本段）

採用以下五項組合決策：

1. **接受 file:// 為第三類 `repo_url` scheme**，但僅 dev mode（三個 env 同時為 true 才開）；production 啟動硬擋
2. **新增 `Inspector` 介面取代 `inspect_repo` 內聯邏輯**；GitHubInspector 為既有路徑、LocalInspector 為 file:// 路徑；factory 依 scheme 分派
3. **新增 `LocalBuildDispatcher` 實作既有 `createapp.Dispatcher` 介面**；用 podman + paketo `pack build` + 本地 registry 取代 GHA workflow_dispatch；不改介面契約
4. **callback HMAC 與 deploy_run state machine 一律共用 production 設計**，不為 dev 開後門、不新增 state
5. **路徑安全採白名單根目錄**：`LOCAL_FILE_REPO_ROOT`（預設 `/workspace/examples`），跟 symlink 後驗根；任何 escape 一律 422

行為與檔案結構細節以對應 sub-spec
[`docs/features/dev-environment/local-file-repo.md`](../features/dev-environment/local-file-repo.md) 為準；變更須走 ADR 補丁。

## 1. Context and Problem Statement

`create_app` 是 v1 M2 最重要的端到端 demo（create-app-flow spec § 1）。production 流程必須：
GitHub repo → GitHub App token → GHA workflow_dispatch → GHCR → ArgoCD → K3s → Cloudflare Tunnel。

dev 環境缺乏 GitHub App / GHA / GHCR / K3s / Cloudflare，現況用三個 `*_DISABLE_*` env 把外部依賴 nil-out
（`GITHUB_APP_DISABLE_INSTALL_CHECK`、`K3S_DISABLE_ISOLATION`、`CF_DISABLE_TUNNEL`）。
結果是 `./manage.sh dev` 下：preview 過、confirm 寫 DB、`deploy_run` 卡在 `queued`，無人推進至 `live`。
工程師無法在 host 本機驗證「create_app → live」整段。

需釘住三件事：

1. dev 環境如何提供「無 GitHub」的 repo 來源 — 候選 file://、git-daemon、local gitea
2. dev 環境如何在「無 GHA」之下推進 deploy_run state — 候選本地 dispatcher、reconciler 模擬、人工 curl
3. 邊界如何防止 dev 入口被 production 誤啟用 — 候選 build-time gate、runtime env panic、tag-based binary

ADR-0005 已釘 production build pipeline 為 GHA dispatch + callback HMAC；本 ADR 在不改該規約之前提下，補 dev mode 之替代路徑。

## 2. Decision Drivers

* **DD1 vertical slice 可在 host 跑通**：M2 標準包含「create_app → live」端到端 demo；dev 必須可演示
* **DD2 介面契約穩定**：production 與 dev 共用 `Dispatcher` 與 `Inspector` 介面；不為 dev 開分支型別
* **DD3 production 必拒**：dev 入口若被 production env 載入應立即 fail，不留 silent fallback
* **DD4 共用 callback 簽章**：dev callback 仍走 `OPS_CALLBACK_SECRET` HMAC；驗 production callback handler 對 dev 流量同樣安全
* **DD5 無新 state**：deploy_run state machine 為 system-wide invariant；dev 不得新增 state、不得跳過 state
* **DD6 podman 為現有引擎**：dev environment spec § 4 已規定 podman；不引入 docker 為 dev build engine
* **DD7 路徑安全可審計**：file:// 路徑為類 SSRF / local file read 攻擊面；白名單根目錄 + symlink 解析必先做

## 3. Decision Outcome

### 3.1 三項 env gate

```
LOCAL_FILE_REPO_ENABLED   # 開放 file:// validator 與 LocalInspector
LOCAL_BUILD_ENABLED       # 啟用 LocalBuildDispatcher
LOCAL_REGISTRY            # 例 localhost:5000
LOCAL_FILE_REPO_ROOT      # 白名單根目錄；預設 /workspace/examples
```

啟動硬性檢查：`OPS_ENV=production` 與任一 `LOCAL_*_ENABLED=true` 並存 → server panic。

### 3.2 介面分派

```go
// internal/server/services/createapp/inspector.go (新)
type Inspector interface {
    Inspect(ctx context.Context, repoURL, ref string) (RepoMetadata, error)
}
// factory: file:// → LocalInspector；https://github.com/* → GitHubInspector

// internal/server/services/createapp/service.go (既有)
type Dispatcher interface {
    Dispatch(ctx context.Context, payload workflowdispatch.ClientPayload) error
}
// factory (apps.go): LOCAL_BUILD_ENABLED + file:// 觸發時 → LocalBuildDispatcher；
//                    否則保留現有 workflowdispatch.NewClientFromEnv（nil-tolerant）
```

### 3.3 LocalBuildDispatcher 流程

`pack build → podman push → 自打 callback × 5`，遍歷
`building → pushing → rendering → syncing → live`。
任一步失敗 → `callback(failed, <classification>, stderr_truncated)`。

### 3.4 路徑安全

`filepath.EvalSymlinks(filepath.Clean(path))` → `filepath.Rel(root, resolved)` 必須非 `..` 開頭，
否則 422 `repo_path_invalid`。

### 3.5 image_ref schema

production：`ghcr.io/winshare/0ops-apps/<team>/<slug>:<deploy_run_id>`
dev：`<LOCAL_REGISTRY>/0ops-apps/<team>/<slug>:<deploy_run_id>`

僅 registry 前綴變更；後綴 schema 不動，保持與既有 `image_ref` 解析相容。

## 4. 與 ADR-0005 之關係

ADR-0005 為 production GHA dispatch + callback HMAC 之決策。本 ADR 在三件事上對齊：

1. **callback HMAC 不開後門**：dev callback 共用 `OPS_CALLBACK_SECRET`；handler 對 dev / production 一視同仁
2. **deploy_run state machine 不分叉**：dev 走完整 state 序列；不引入 `dev_*` 狀態
3. **Dispatcher 介面契約不變**：dev / production 共用同一介面；factory 在 boot 階段二擇一注入

本 ADR **不修改**任何 production 路徑；ADR-0005 之 GHA workflow YAML、HMAC 計算、callback retry 規則全數保留。

## 5. Pros and Cons of the Options

### 5.1 候選方案

| 方案 | repo 來源 | dispatcher | 對外可訪問 |
|---|---|---|---|
| **A. file:// + LocalBuildDispatcher（本 ADR）** | host 檔系統 | server 內部 fire-and-forget goroutine | ✗（本地 registry only） |
| B. git-daemon 服務 + LocalBuildDispatcher | 容器內 git-daemon:9418 | 同 A | ✗ |
| C. local gitea + GHA self-hosted runner | 完整 mock 整個 GitHub | self-hosted runner | ✗ |
| D. 人工 curl callback | 任意 | 不做 | ✗ |
| E. k3d + 本地 registry + ArgoCD | git URL | 同 A | ✓ |

### 5.2 取捨

**A（採用）**

* Pros：實作量最小；server 直接讀掛載目錄；不引入額外 service；e2e 腳本可在無外部依賴下跑
* Cons：repo 必須真實 git init（避免協議差異）；用戶要記得 bootstrap

**B**

* Pros：模擬 git 協議更貼近 production
* Cons：多一個 service 與 healthcheck；對「測試 create_app 流程」沒額外價值

**C**

* Pros：production-fidelity 最高
* Cons：M2 範圍過大；gitea + self-hosted runner 維護成本不亞於整套 production；違反 DD1 之「host 跑通」

**D**

* Pros：零實作
* Cons：對 demo 流程不友善；違反 DD1

**E**

* Pros：唯一能達「對外可訪問」之 dev 體驗
* Cons：k3d + ArgoCD 屬 M2 後增量；本 ADR 範圍受 user 指示為「可以測試」即可

選 A，將 E 列為後續可獨立增量。

## 6. Consequences

### 6.1 正面

* `./manage.sh dev-create-example` 一鍵跑通 create_app → live；新貢獻者上手成本降至 < 5 分鐘
* `Inspector` 介面化使 GitHubInspector 與 LocalInspector 皆可單元測試（既有 inspect_repo 內聯邏輯不可測）
* `LocalBuildDispatcher` 透過共用 `Dispatcher` 介面驗證 `createapp.Service` 對 dispatcher 之契約（mock 與 real 二者切換零修改）
* deploy_run state machine 在 dev 真實跑完整序列；過去 callback / reconciler 路徑無 dev 覆蓋

### 6.2 負面

* podman socket mount 為 high-trust 介面；compose override 若被誤抄至 production 模板將顯著擴大攻擊面（mitigation：boot panic + lint：production chart 不得包含 LOCAL_* env）
* 三個新 env 增加配置面；mitigation：sub-spec § 4 啟動硬性檢查、`.env.example` 註記只在 dev 啟用
* `pack build` 首次需下載 paketo builder image（~600MB）；mitigation：在 README 註記首次成本，registry 為 cache
* rootless podman + pack lifecycle 之 uid mapping 不對稱：lifecycle ephemeral
  container（host podman 起，user namespace 之 cnb uid 1000 → host
  100999）對 mounted socket（host uid 1000 owner）無 r/w 權限，導致
  ANALYZING phase failure。Mitigation：sub-spec § 15 文件化 host setup 步驟
  + `./manage.sh podman-socket-loosen` + e2e preflight fail-fast。風險面與
  既有 socket mount 同層；M5.6.2 接受 chmod 666 為 dev-only 預設方案

### 6.3 待補

* 未涵蓋「對外可訪問」之 dev 體驗（k3d / traefik）— 屬獨立 ADR 與 sub-spec
* 未涵蓋 BYO Dockerfile builder — 屬獨立 ADR

## 7. Revisit Triggers

* 引入 k3d / kind 作為 dev K8s → 需評估 LocalBuildDispatcher 是否被 ArgoCD 替代
* production 改採非 GHA 之 build pipeline（如 Tekton、self-hosted） → 整個 Dispatcher 抽象需重新審視
* 引入 BYO Dockerfile builder → LocalBuildDispatcher 與 LocalInspector 之 paketo 假設失效
* podman 被替換為其他容器引擎 → dispatcher exec.Cmd 假設失效
* 觀察到 dev 與 production 之 deploy_run state 序列發生分歧 → 違反 DD5，需立即修

## 8. More Information

* Sub-spec：[`docs/features/dev-environment/local-file-repo.md`](../features/dev-environment/local-file-repo.md)
* 上游 dev environment spec：[`docs/features/dev-environment/spec.md`](../features/dev-environment/spec.md)
* 上游 create_app flow：[`docs/features/create-app-flow/spec.md`](../features/create-app-flow/spec.md) § 4.2 / § 5.1 / § 6
* 上游 build pipeline：[`docs/features/build-pipeline-and-callback/spec.md`](../features/build-pipeline-and-callback/spec.md)
* state machine：[`docs/features/reconciler-and-incident/spec.md`](../features/reconciler-and-incident/spec.md) § 6
* ADR-0002（冪等與補償）：[0002-idempotency-and-compensation.md](0002-idempotency-and-compensation.md)
* ADR-0005（構建管道與回調）：[0005-build-pipeline-and-callback.md](0005-build-pipeline-and-callback.md)

## 9. Open Questions

下列問題不阻擋本 ADR 通過，但需在 M3 規劃前敲定：

1. **e2e CI 是否預設啟用**：本 ADR 採「CI matrix 預設 skip；`./manage.sh e2e-local-build` 觸發」；若 M3 要把 e2e 納入必跑，需評估 podman-in-podman 之 CI 成本
2. **podman socket mount 之 rootless / rootful 兼容**：CI runner 多為 rootful；本機開發為 rootless；compose volume 路徑差異需在 README 註記，是否需 helper script 自動探測（M5.6.2 已採 chmod 666 + e2e preflight 之手動 setup 解法；rootful socket 留作 future 替代）
3. **paketo builder image cache**：是否在 compose 加 `pack-builder-cache` named volume，避免每次 `dev-clean` 後重抓 600MB
4. **「對外可訪問」之 dev 體驗**：是否在 M3 引入 k3d + traefik 之 ADR-0013；本 ADR 已預留 image_ref schema 不變、state 不變之介面
5. **多 example repo 與 paketo language 矩陣**：v1 先 Node；M2 後是否補 Python / Go / Static 對應 example，作為 paketo detector 之 regression fixture

> 已決議（user 全權委託，2026-05-17）：
> - file:// 為第三類 scheme，dev only，production 拒絕
> - 採方案 A（LocalBuildDispatcher fire-and-forget goroutine）
> - 不引入 k3d；「live」= DB live + local registry image 存在，不對外可訪問
> - 共用 callback HMAC 與 deploy_run state machine
> - 路徑採白名單根目錄；symlink 解析後驗根
>
> 已決議（user 拍板，2026-05-19，M5.6.2）：
> - rootless podman socket 之 uid mapping 不對稱由「文件化 chmod 666 +
>   `./manage.sh podman-socket-loosen` + e2e preflight fail-fast」解決
> - 不採 rootful socket（增 host 維護面）；不採 userns=keep-id（牽動其他 mount
>   uid 假設）
> - chmod 666 風險面與既有 socket mount 同層；只允許在 OPS_ENV != production
>   的機器上執行
