# Feature Sub-Spec：local-file-repo（dev mode 本地 repo 與本地 build pipeline）

> **狀態**：accepted
> **來源**：本 sub-spec 由 user 指示「`repo_url` 加上支援 `file:///` 寫法，可以測試」直接產出；
> 上游主 spec 為 `docs/features/dev-environment/spec.md`；
> 上游關聯 spec：`docs/features/create-app-flow/spec.md`、`docs/features/build-pipeline-and-callback/spec.md`、`docs/features/reconciler-and-incident/spec.md`
> **適用範圍**：本機 `./manage.sh dev` 與 CI 之 dev compose；**production 必須拒絕**
> **對應 Milestone**：M2 vertical slice 之 dev 驗證（spec § 1）
> **關聯 ADR**：[ADR-0012 local-file-repo-dev-mode](../../adrs/0012-local-file-repo-dev-mode.md)

## 1. 結論（先讀本段）

- `repo_url` 接受 `file:///<absolute-path>`，但僅限三個 env 同時為 `true` 時：`LOCAL_FILE_REPO_ENABLED`、`LOCAL_BUILD_ENABLED`、`LOCAL_REGISTRY` 非空
- production 配置必須三者皆未設或 `false`；server 啟動時若 `LOCAL_FILE_REPO_ENABLED=true` 且 `OPS_ENV=production` 立即 panic
- 路徑必須位於 server 容器內已掛載之白名單根目錄（預設 `/workspace/examples`）下；任何 escape（`..`、symlink）一律 422 拒絕
- preview 階段：file:// 走 `LocalInspector`（讀本地 `.git` 取 commit_sha / default_branch、讀檔系統做 paketo 偵測），略過 GitHub App token 流程
- confirm 階段：file:// 走 `LocalBuildDispatcher` 取代 `workflowdispatch.Client`；流程為 `pack build` → `podman push localhost:5000/...` → 自打 callback 推進 deploy_run state 至 `live`
- callback 仍走 `OPS_CALLBACK_SECRET` 簽章；不為 dev 開後門
- deploy_run 抵達 `live` 表示 DB 狀態 + local registry 已有 image，**不對外可訪問**；對外訪問非本 sub-spec 範圍
- 提供 `examples/node-demo/` Express 範例與 `./manage.sh dev-create-example` 一鍵跑通

## 2. 範圍

### 2.1 包含

- `validateRepoURL` / `validateRequest` 接受 `file:///` 之 env-gated 分支
- `Inspector` 介面與 `LocalInspector` 實作（read `.git` + 檔系統 paketo detect）
- `LocalBuildDispatcher`：實作 `createapp.Dispatcher`；pack build + podman push + 自打 callback 序列
- `examples/node-demo/`：可被 paketo NodeJS buildpack 直接偵測之 Express hello world
- compose dev wiring：registry service、podman socket mount、env 注入
- manage.sh subcommand：`dev-example-init`、`dev-create-example`
- end-to-end 驗收腳本：`tasks/local-build-e2e.sh`
- spec / ADR 文件

### 2.2 不包含

- 真實對外可訪問之 traefik / k3d / kind（屬下一個增量）
- BYO Dockerfile builder 類型（非 paketo 路徑；屬獨立 ADR）
- production 路徑改動：ADR-0005 build-pipeline-and-callback 仍為唯一 production 規約
- Cloudflare Tunnel / K3s 物件之 dev 模擬（維持 `K3S_DISABLE_ISOLATION` / `CF_DISABLE_TUNNEL` 之 nil 路徑）
- inspect_repo HTTP endpoint 重寫（本 sub-spec 僅動內部 `Inspector` 介面；endpoint 仍只回 DB row）
- `0ops apps create` CLI 新增旗標；現有 `--repo-url` 直接接受 file://

## 3. 檔案結構

```
0ops/
├── examples/
│   └── node-demo/
│       ├── package.json
│       ├── index.js
│       ├── README.md
│       ├── .gitignore
│       └── bootstrap.sh                              # git init && commit
├── internal/
│   └── server/
│       └── services/
│           ├── createapp/
│           │   ├── args.go                           # 改：file:// + env gate
│           │   ├── service.go                        # 改：validateRequest 同步 + Inspector 注入
│           │   ├── inspector.go                      # 新：Inspector 介面 + factory
│           │   ├── local_inspect.go                  # 新：file:// 走本地檔系統 + git
│           │   └── local_inspect_test.go             # 新
│           └── localbuild/
│               ├── dispatcher.go                     # 新：createapp.Dispatcher
│               ├── dispatcher_test.go                # 新
│               ├── callback_client.go                # 新：簽章自打
│               ├── config.go                         # 新：env gate
│               └── doc.go
├── compose.yaml                                       # 改：+ registry + podman.sock + env
├── compose.override.yaml.example                      # 改：示範 dev 設定
├── manage.sh                                           # 改：dev-example-init / dev-create-example
└── tasks/
    └── local-build-e2e.sh                             # 新
```

## 4. Env gate

新增三個 env，命名採「正向 `_ENABLED_`」反向於既有 `*_DISABLE_*`；理由：
file:// dev 路徑為**新增能力**，production 預設關（unset 即關）才是安全的；
而 `K3S_DISABLE_ISOLATION` 等是**移除既有保護**，命名反映其風險。

```
LOCAL_FILE_REPO_ENABLED   # 必設 true 才開放 file:// validator 與 LocalInspector
LOCAL_BUILD_ENABLED       # 必設 true 才將 dispatcher 替換為 LocalBuildDispatcher
LOCAL_REGISTRY            # 例 localhost:5000；空字串視為未啟用
LOCAL_FILE_REPO_ROOT      # 白名單根目錄；預設 /workspace/examples
```

本 sub-spec 同時新增 `OPS_ENV` env（`development` / `staging` / `production`，預設 `development`）
作為 production 防呆閘門；既有 codebase 尚未使用此 env，新增由本 sub-spec 引入並於
`internal/shared/runtime/env.go`（新檔）導出 `IsProduction() bool`。

啟動時硬性檢查（spec § 12 之延伸）：

| 條件 | 行為 |
|---|---|
| `OPS_ENV=production` 且任一上述 `LOCAL_*` env 為 true | server 啟動立即 panic |
| `LOCAL_FILE_REPO_ENABLED=true` 但 `LOCAL_BUILD_ENABLED=false` | log warn；preview 過 confirm 後 deploy_run 卡 `queued`（無 dispatcher） |
| `LOCAL_BUILD_ENABLED=true` 但 podman socket 不可達 | log warn；dispatcher factory 回 nil；行為退回現有 nil-tolerant |

## 5. Preview 階段：`LocalInspector`

### 5.1 觸發條件

`Inspector` factory 依 `repo_url` scheme 分派：

```
strings.HasPrefix(repoURL, "file://")  → LocalInspector  (需 LOCAL_FILE_REPO_ENABLED)
strings.HasPrefix(repoURL, "https://github.com/")  → GitHubInspector (既有路徑)
strings.HasPrefix(repoURL, "git@github.com:")      → GitHubInspector (既有路徑)
```

### 5.2 路徑安全驗證

```go
func validateLocalPath(rawURL string) (string, error) {
    raw := strings.TrimPrefix(rawURL, "file://")
    if !filepath.IsAbs(raw) {
        return "", ErrRepoPathInvalid
    }
    clean := filepath.Clean(raw)
    resolved, err := filepath.EvalSymlinks(clean)  // 跟 symlink 後再驗
    if err != nil {
        return "", ErrRepoPathNotFound
    }
    root := os.Getenv("LOCAL_FILE_REPO_ROOT")
    rel, err := filepath.Rel(root, resolved)
    if err != nil || strings.HasPrefix(rel, "..") {
        return "", ErrRepoPathInvalid
    }
    return resolved, nil
}
```

### 5.3 Metadata 取得

`LocalInspector.Inspect(ctx, path)` 行為：

1. 確認 `<path>/.git` 存在；否則 422 `repo_not_git`
2. `git -C <path> rev-parse HEAD` → `commit_sha`
3. `git -C <path> symbolic-ref refs/remotes/origin/HEAD` 或 `git -C <path> rev-parse --abbrev-ref HEAD` → `default_branch`（fallback `main`）
4. paketo 靜態偵測（與 production GitHubInspector 共用 detector module）：
   - `package.json` → `paketobuildpacks/builder-jammy-base` + port 3000
   - `pyproject.toml` / `requirements.txt` → 同 builder + port 8000
   - `go.mod` → 同 builder + port 8080
   - 全空 → 422 `buildpack_detect_failed`

回 `RepoMetadata{ CommitSHA, DefaultBranch, Builder, PrimaryPort, GitHubAppStatus: "not_applicable" }`。

### 5.4 Preview side_effects 對應

side_effects 5 項（create-app-flow spec § 5.2）在 file:// 下做語意調整：

| Effect | file:// 行為 |
|---|---|
| 1. 0ops-gitops render & push | `gitops = nil`（既有 dev 路徑）→ skip |
| 2. K8s namespace & ghcr-pull | `k3sClient = nil`（既有 dev 路徑）→ skip |
| 3. DB INSERT app + domain_binding | 照常執行 |
| 4. GHA workflow_dispatch | **改走 LocalBuildDispatcher**（本 sub-spec 核心） |
| 5. ArgoCD sync | 本地 dispatcher 直接 fake callback 推進至 `live` |

`action_summary` 範本不變，但 `Subdomain` 行加 `(local-only, not externally routable)` 標註。

## 6. Confirm 階段：`LocalBuildDispatcher`

### 6.1 Dispatcher 介面相容

```go
// 已存在於 internal/server/services/createapp/service.go:63
type Dispatcher interface {
    Dispatch(ctx context.Context, payload workflowdispatch.ClientPayload) error
}
```

`LocalBuildDispatcher` 實作此介面；`createapp.Service` 無需改動。

### 6.2 Dispatch 行為

```
Dispatch(payload):
    go run(payload)   // 非阻塞；同 production GHA dispatch 之 fire-and-forget 語意
    return nil

run(payload):
    localPath := resolveLocalPath(payload)   // 見 § 6.5
    1. callback(building)
    2. exec: pack build <imageRef> --path <localPath> --builder <builder> --publish
       失敗 → callback(failed, build_error, stderr_truncated)
       (--publish 直接 push；省掉 push 步驟之 race window)
    3. callback(pushing)            // 紀錄性 transition；--publish 已完成 push
    4. callback(rendering)
    5. callback(syncing)
    6. callback(live)
```

`pack build` 與 `podman` 在 server 容器內透過 host podman socket（`/var/run/docker.sock` mount）執行，
image store / push 視角皆為 host podman；`<imageRef>` 之 registry 前綴
`<LOCAL_REGISTRY>` 因此採 host 視角（compose 將 registry service expose 在 host `5000:5000`，
故 `localhost:5000` 對 host podman 可達）。server 容器內若需直接 HTTP 探活 registry（如 e2e 腳本），
則用 compose 內 DNS `registry:5000`；兩者地址不同但指向同一 registry。

每個 callback 之間 sleep 200ms 模擬狀態傳遞（非必要，但讓 SSE log tail 體感正常）。

### 6.3 Callback 簽章

```
POST <callbackBaseURL>/internal/deploy-runs/<run_id>/callback
Headers:
  X-Ops-Signature: hmac-sha256=<hex(HMAC(OPS_CALLBACK_SECRET, body))>
  X-Ops-Run-Id: <run_id>
Body:
  { "status": "...", "image_ref": "...", "build_minutes": 0.1, "error_summary": "..." }
```

`OPS_CALLBACK_SECRET` 與 production callback 共用同一 secret；不為 dev 開後門。

### 6.4 Dispatcher 注入策略

`createapp.Service` 之 `Dispatcher` 為單一注入欄位，不可 per-request 切換。
為支援 dev / production 混存（即使 dev mode env 開啟，使用者仍可能傳 https://github.com 之 repo），
採 **RoutingDispatcher** 包兩個 sub-dispatcher：

```go
// internal/server/services/createapp/routing_dispatcher.go (新)
type RoutingDispatcher struct {
    GitHubDispatcher Dispatcher          // workflowdispatch.Client
    LocalDispatcher  Dispatcher          // localbuild.Dispatcher
    Store            interface {
        GetDeployRunRepoURL(ctx, runID) (string, error)
    }
}

func (r *RoutingDispatcher) Dispatch(ctx, payload) error {
    repoURL, err := r.Store.GetDeployRunRepoURL(ctx, payload.RunID)
    if err != nil { return err }
    if strings.HasPrefix(repoURL, "file://") {
        return r.LocalDispatcher.Dispatch(ctx, payload)
    }
    return r.GitHubDispatcher.Dispatch(ctx, payload)
}
```

`workflowdispatch.ClientPayload` 不新增欄位（介面契約穩定，per ADR-0012 § 3.2）；
RoutingDispatcher 在 `Dispatch` 內反查 deploy_run / app 取 repo_url 決定路由。

`apps.go::newWorkflowDispatchClient()` 改為：

```go
if localBuildEnabled() {
    return &RoutingDispatcher{
        GitHubDispatcher: workflowdispatch.NewClientFromEnv(http.DefaultClient),
        LocalDispatcher:  localbuild.NewDispatcher(...),
        Store:            store,
    }
}
return workflowdispatch.NewClientFromEnv(http.DefaultClient)
```

LocalDispatcher 從 store 取 `app.RepoURL`，`strings.TrimPrefix("file://")` 後 EvalSymlinks
再過一次 § 5.2 之 `LOCAL_FILE_REPO_ROOT` 白名單驗證（防 TOCTOU：preview 階段過了，
confirm 之間 repo_url 不可變更，但 dispatcher 仍二次驗證以閉合信任邊界）。

### 6.5 Retry 策略

callback 自打：3 次指數退避（200ms / 800ms / 3.2s）。
全失敗：直接 `UPDATE deploy_run SET status='failed', failure_classification='callback_unreachable'`；
不再 retry（dispatcher 是 server 自家進程，callback 不可達等於 server 自己壞了）。

`pack build` / `podman push`：不 retry（用戶手動 redeploy）。

## 7. Image Ref 格式

production：`ghcr.io/winshare/0ops-apps/<team>/<slug>:<deploy_run_id>`

dev file://：`<LOCAL_REGISTRY>/0ops-apps/<team>/<slug>:<deploy_run_id>`
  例 `localhost:5000/0ops-apps/personal/node-demo:dr_abc123`

`createapp.Service.Confirm()` 內 `imageRef` 計算改為呼叫 `dispatcher.ImageRefFor(team, slug, runID)`；
`LocalBuildDispatcher` 與 `workflowdispatch.Client` 各自實作；保留 nil-tolerant fallback。

## 8. Example repo：`examples/node-demo/`

### 8.1 內容

`package.json`：

```json
{
  "name": "node-demo",
  "version": "0.1.0",
  "engines": { "node": "20.x" },
  "scripts": { "start": "node index.js" },
  "dependencies": { "express": "^4.19.2" }
}
```

`index.js`：

```js
const express = require("express");
const app = express();
app.get("/", (_req, res) => res.send("hello from 0ops node-demo"));
app.listen(process.env.PORT || 3000);
```

`.gitignore`：`node_modules/`

`bootstrap.sh`：

```sh
#!/usr/bin/env bash
set -euo pipefail
cd "$(dirname "$0")"
test -d .git && exit 0
git init -q
git checkout -q -b main
git add .
git -c user.email=dev@0ops.local -c user.name=dev commit -q -m "initial node-demo"
echo "examples/node-demo initialized as git repo"
```

### 8.2 paketo 偵測預期

```
Builder:     paketobuildpacks/builder-jammy-base
PrimaryPort: 3000
DefaultBranch: main
```

## 9. Compose / manage.sh

### 9.1 compose.yaml 增量

```yaml
services:
  registry:
    image: docker.io/library/registry:2
    ports:
      - "5000:5000"
    healthcheck:
      test: ["CMD", "wget", "-q", "-O-", "http://localhost:5000/v2/"]
      interval: 10s

  server:
    environment:
      LOCAL_FILE_REPO_ENABLED: ${LOCAL_FILE_REPO_ENABLED:-true}
      LOCAL_BUILD_ENABLED: ${LOCAL_BUILD_ENABLED:-true}
      LOCAL_REGISTRY: ${LOCAL_REGISTRY:-localhost:5000}
      LOCAL_FILE_REPO_ROOT: /workspace/examples
    volumes:
      - ./examples:/workspace/examples:Z
      - /run/user/${UID:-1000}/podman/podman.sock:/var/run/docker.sock:Z
    depends_on:
      registry:
        condition: service_healthy
```

### 9.2 manage.sh 增量

```make
dev-example-init: ## 初始化 examples/node-demo 為 git repo
	bash examples/node-demo/bootstrap.sh

dev-create-example: dev-example-init ## 用 file:// 跑一次 create_app 至 live
	bash tasks/local-build-e2e.sh
```

### 9.3 `tasks/local-build-e2e.sh` 契約

```
1. podman compose up -d
2. 等 server healthy
3. 用 cmd/devtools/seed-cli-token 取 bearer token
4. 用 0ops CLI 對 file:///workspace/examples/node-demo 跑 apps create --yes
5. 輪詢 deploys status 至 live（timeout 90s）
6. curl http://localhost:5000/v2/0ops-apps/personal/node-demo/tags/list
   驗 image 存在
7. 列印 deploy_run 與 image_ref 供人工檢視
```

## 10. State Machine 對應

`reconciler/statemachine.go` 已定義之 transitions 全部沿用；本 sub-spec **不新增** state、不改 transition 規則。
LocalBuildDispatcher 觸發的 callback 序列為現有 `queued → preparing → building → pushing → rendering → syncing → live` 之直接遍歷。

`preparing` 在 createapp.Confirm tx 末端設置（既有行為，不動）；
LocalBuildDispatcher 從 `building` 起接手。

## 11. 觀測

新增 metrics：

```
0ops_localbuild_invocations_total{outcome=success|build_error|push_error|callback_unreachable}
0ops_localbuild_duration_seconds{stage=build|push|callback_chain}
```

log 鍵：`local_build.run_id`、`local_build.image_ref`、`local_build.stage`。

不接 alert（dev only）。

## 12. 測試矩陣

| 層 | 測試 | 必須涵蓋 |
|---|---|---|
| `validateRepoURL` 單元 | file:// 開/關 gate × {合法 / 不存在 / escape / 非 git} | `args_test.go` 既有表格擴展 |
| `LocalInspector` 單元 | tmp git repo fixture × {node / python / go / 空} | `local_inspect_test.go` |
| `LocalBuildDispatcher` 單元 | mock exec.Cmd × {全綠 / build fail / push fail / callback timeout} | `dispatcher_test.go` |
| `createapp.Service` 整合 | env gate ON 注入 LocalBuildDispatcher；OFF 不退化既有測試 | `service_test.go` 新增矩陣 |
| `apps.go` 啟動 | `OPS_ENV=production` + `LOCAL_FILE_REPO_ENABLED=true` 應 panic | `server_test.go` |
| e2e | `tasks/local-build-e2e.sh` 在 podman 環境中跑完 | CI matrix 預設 skip；`./manage.sh m5-6-local-build-e2e` 觸發 |

## 13. 對 `docs/features/create-app-flow/spec.md` 之延伸

非破壞性延伸。本 sub-spec 補充：

- § 4.2 validation：file:// 為**第三類** repo_url scheme，gated 在 dev env
- § 5.1 step 4：inspect_repo 行為依 scheme 分派至 GitHub 或 Local Inspector
- § 5.2 effect 4：dev mode 改走 LocalBuildDispatcher，仍視為 saga 的 irreversible 邊界（本地 image push 完即不可 undo，與 production 對齊）
- § 6 confirm：Dispatcher 介面契約不變

create-app-flow 主 spec 不改邏輯，僅在 § 5.1 加一句指向本 sub-spec 之 reference。

## 14. 對 `docs/features/build-pipeline-and-callback/spec.md` 之關係

build-pipeline-and-callback 為 production GHA 規範；本 sub-spec **不修改** 任何 production 規約。
LocalBuildDispatcher 為 dev-only 之替代 dispatcher，與 production GHA dispatcher 共用 `Dispatcher` 介面 + 共用 callback HMAC 簽章 + 共用 deploy_run state machine。

## 15. Host 環境前提（rootless podman + pack lifecycle）

LocalBuildDispatcher 之 `pack build` 在 server 容器內 exec，pack 透過
`DOCKER_HOST` 對 mounted host podman socket 起 lifecycle ephemeral
container（buildpack 階段 detect / restore / analyze / build / export）。
此 lifecycle container 由 host podman spawn，**不在** compose 之
`0ops_default` network；socket 由 pack 自動 bind 至 lifecycle container 的
`/var/run/docker.sock`。

### 15.1 rootless podman 之 uid 不對稱

rootless podman 採 user-namespace mapping：

- host podman socket 屬主：host uid `$UID`（通常 1000），perms `srw-rw----`
- server 容器內 process 為 root（uid 0），由 podman map 到 host uid 1000，可讀
- pack 之 lifecycle container 內 process 為 `cnb`（uid 1000），由 host
  podman 之 subuid map 到 host uid `100999`（不等於 host 1000）；對同一個
  bind mount 之 socket，lifecycle 內看到 owner 為 nobody、無 r/w 權限

結果：lifecycle 之 ANALYZING phase fail：

```
ERROR: failed to initialize analyzer: getting previous image:
permission denied while trying to connect to the docker API at
unix:///var/run/docker.sock
```

### 15.2 解法（dev only；production 不適用此節）

採以下其一即可，**`./manage.sh m5-6-podman-socket-loosen` 為預設選項**：

| 方案 | 動作 | 持久性 | 風險 |
|---|---|---|---|
| **A. 鬆綁 socket perms** | `chmod 666 /run/user/$UID/podman/podman.sock` | podman.socket 重啟後重置 | 同機任何 local user 可控 podman daemon（dev 機器尚可接受） |
| B. 使用 rootful socket | 啟 `sudo systemctl start podman.socket` 並改 compose mount 為 `/run/podman/podman.sock` | 持久 | 增加 host root daemon 維護面 |
| C. server container `userns=keep-id` | compose 加 `userns_mode: keep-id` | 跟著 compose | server container 內 root 不再是 host uid 1000；其他 mount 需重審 |

本 sub-spec 採 A：簡單、可重執行、與既有 compose 配置改動最小；風險僅限 dev
host 之 local user 邊界，與「server container 已有 socket mount」之風險面
相同。

`tasks/local-build-e2e.sh` 之 preflight step 會 verify socket world-rw；
不通則直接 `exit 1` 並印 `./manage.sh m5-6-podman-socket-loosen` 指引（**不**自動
chmod，避免無 host 寫權限的 CI runner 誤跑）。

### 15.3 重執行頻率

`podman.socket` 在 host 重開機 / `systemctl --user restart podman.socket`
後重置 perms。實務上每次重啟 host 之後跑一次 `./manage.sh m5-6-podman-socket-loosen`
即可；E2E 腳本之 preflight 會在偵測到後 fail-fast 提示。

## 16. 不可違反的硬性規則

1. **production 必拒 file://**：`OPS_ENV=production` 與任一 `LOCAL_*_ENABLED=true` 並存 → server panic
2. **路徑必須在白名單根目錄下**：跟 symlink 後再驗根目錄；任何 escape 一律 422
3. **podman socket 視為 high-trust mount**：只在 dev compose 設定；override 檔不得在 production 模板中出現
4. **callback HMAC 不為 dev 開後門**：dev 與 production 共用 `OPS_CALLBACK_SECRET`
5. **不新增 deploy_run state**：sub-spec 必走既有 `reconciler/statemachine.go` transitions
6. **不改 `Dispatcher` 介面契約**：保持與 production GHA dispatcher 二進位相容
7. **不為 file:// 路徑變更 `image_ref` schema 之 `<team>/<slug>:<deploy_run_id>` 後綴**：僅變更 registry 前綴
