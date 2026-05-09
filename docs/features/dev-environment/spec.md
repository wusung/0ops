# Feature Spec：dev-environment

> **狀態**：draft
> **來源**：`docs/0ops-plan.md` 之「已決策參數」「Go 技術棧（v1 預設選型）」「專案結構」三段
> **適用範圍**：本機開發、CI build context；不含 production 部署、不含 managed app 的 image 策略
> **對應 Milestone**：M0（scaffold + dev env）

## 1. 結論（先讀本段）

- 容器引擎固定 `podman` rootless + `podman compose`（v2 內建）
  - 禁止 `docker`、禁止 `podman-compose`（v1 wrapper、行為與 v2 不一致）
- compose 入口固定為 root `compose.yaml`（podman compose v2 預設探索檔名）
- 三 binary 各自有 `cmd/{server,cli,mcp}/Dockerfile`
- Dockerfile 採 multi-stage：`golang:1.23-alpine` builder → `gcr.io/distroless/static-debian12:nonroot` runtime
- `0ops-server` 額外提供 `dev` stage，內含 `air` 熱重載；CLI 與 MCP 不提供 dev stage（CLI 互動式、MCP 為 stdio，皆 host 執行）
- 提供 root `.dockerignore` 與 `.env.example`；`.env` 由貢獻者複製後填寫，禁止 commit
- 開發 workflow 經 `Makefile` 收口；契約 target：`make dev` / `make dev-down` / `make migrate` / `make lint-compose` / `make lint-docker` / `make build-images`

## 2. 範圍

### 2.1 包含
- 本機 dev runtime（`db` + `migrate` + `server` 三個 compose service）
- 三 binary 之 release-grade Dockerfile
- compose service 拓樸與 healthcheck 規約
- `.dockerignore`、`.env.example`
- 對應 `Makefile` target
- CI 對 compose 與 Dockerfile 的 lint 規則

### 2.2 不包含
- production 部署 chart（屬 `deploy/chart/launchpad/`）
- managed app 的 chart 與 build pipeline（屬 `deploy/chart/managed-app/` 與 `deploy/workflows/deploy-app.yml`）
- ArgoCD ApplicationSet 與 GitOps repo（屬 `deploy/gitops/`）
- 0ops CLI 與 0ops-mcp 之 dev 容器化（host 執行；其 Dockerfile 僅供 release）
- secret 管理（屬 Auth & RBAC 章節之 `Secrets management`）

## 3. 檔案結構

```
0ops/
├── compose.yaml                        # podman compose 入口（root）
├── .dockerignore                       # build context 排除清單（root）
├── .env.example                        # 開發環境變數範本（root）
├── Makefile                            # 開發 target 收口（root）
└── cmd/
    ├── server/Dockerfile               # 0ops-server image：builder → dev → runtime
    ├── cli/Dockerfile                  # 0ops image：builder → runtime
    └── mcp/Dockerfile                  # 0ops-mcp image：builder → runtime
```

## 4. 容器引擎決策

| 維度 | 決策 | 理由 |
|---|---|---|
| 容器引擎 | `podman` rootless | 無 daemon、與 OCI 對齊、rootless 預設安全；全域既定規則 |
| compose 工具 | `podman compose`（v2） | 與 docker compose v2 行為一致；非外部 wrapper script |
| Image 命名 | `localhost/0ops-{server,cli,mcp}:{dev,runtime}` | rootless registry-less 本機 image，避免命名空間衝突 |
| 檔名選擇 | `Dockerfile`（不使用 `Containerfile`） | podman 兩者皆讀；保留與 docker 相容性，降低貢獻者切換成本 |
| Userns | `keep-id`（預設） | 解決 host volume mount 權限衝突（rootless podman 必要） |
| SELinux | volume 加 `:Z` 標記 | 在 SELinux enforcing 環境（含 CachyOS、Fedora）必要 |

## 5. Compose 服務拓樸

```
db (postgres:17-alpine)
  └─ healthcheck: pg_isready
      ↓
migrate (one-shot, goose)
  └─ depends_on: db (service_healthy)
      ↓
server (build: cmd/server, target=dev)
  └─ depends_on: migrate (service_completed_successfully)
  └─ ports: 8080:8080
  └─ volumes: source mount for air hot-reload
  └─ healthcheck: GET /health
```

### 5.1 `db`
| 欄位 | 值 |
|---|---|
| image | `docker.io/library/postgres:17-alpine` |
| volume | named volume `pgdata` → `/var/lib/postgresql/data` |
| env 來源 | `.env`：`POSTGRES_USER` / `POSTGRES_PASSWORD` / `POSTGRES_DB` |
| ports | 預設僅 compose network 內可存取；本機 debug 可開 `5432:5432`（透過 `compose.override.yaml`） |
| healthcheck | `pg_isready -U $$POSTGRES_USER -d $$POSTGRES_DB`，interval 5s、timeout 3s、retries 10 |

### 5.2 `migrate`
| 欄位 | 值 |
|---|---|
| image | 由 ADR-002 決議（候選：自建 `migrations/Dockerfile` 含 `goose` + sql 檔；或直接以 `golang:1.23-alpine` 跑 `go run` 注入 goose） |
| restart | `no`（一次性） |
| depends_on | `db: { condition: service_healthy }` |
| command | `goose -dir /migrations postgres "$DATABASE_URL" up` |
| 失敗行為 | exit code 非 0 時，`server` 因 `depends_on.service_completed_successfully` 不啟動 |

> 在 ADR-002 敲定前，`migrations/` 目錄是否含 `Dockerfile` 為待定；spec 第 3 節結構暫不列出，待決議後同步補入。

### 5.3 `server`
| 欄位 | 值 |
|---|---|
| build | `{ context: ., dockerfile: cmd/server/Dockerfile, target: dev }` |
| ports | `8080:8080` |
| volumes | `.:/app:Z`（SELinux relabel）+ named volume `go-mod-cache:/go/pkg/mod` |
| env 來源 | `.env`（含 `DATABASE_URL`、`OPS_LISTEN_ADDR`、`OPS_LOG_LEVEL`、`OPS_CALLBACK_SECRET` 等） |
| healthcheck | `wget -qO- http://localhost:8080/health || exit 1`，interval 10s、timeout 3s、retries 5、start_period 30s |
| depends_on | `migrate: { condition: service_completed_successfully }` |

## 6. Dockerfile 設計

### 6.1 共通結構（以 `cmd/server/Dockerfile` 為例）

```dockerfile
# syntax=docker/dockerfile:1.7

# --- deps：拉模組，最大化快取 ---
FROM golang:1.23-alpine AS deps
WORKDIR /src
COPY go.mod go.sum ./
RUN --mount=type=cache,target=/go/pkg/mod \
    go mod download

# --- builder：編譯靜態 binary ---
FROM deps AS builder
COPY . .
RUN --mount=type=cache,target=/go/pkg/mod \
    --mount=type=cache,target=/root/.cache/go-build \
    CGO_ENABLED=0 GOOS=linux \
    go build -trimpath -ldflags="-s -w" \
    -o /out/0ops-server ./cmd/server

# --- dev：熱重載（僅 server 提供） ---
FROM golang:1.23-alpine AS dev
RUN apk add --no-cache git wget && \
    go install github.com/air-verse/air@latest
WORKDIR /app
EXPOSE 8080
CMD ["air", "-c", ".air.toml"]

# --- runtime：distroless 靜態 binary ---
FROM gcr.io/distroless/static-debian12:nonroot AS runtime
COPY --from=builder /out/0ops-server /usr/local/bin/0ops-server
USER nonroot:nonroot
EXPOSE 8080
ENTRYPOINT ["/usr/local/bin/0ops-server"]
```

### 6.2 各 binary 差異

| binary | cmd path | runtime PORT | dev stage |
|---|---|---|---|
| `0ops-server` | `./cmd/server` | 8080 | 是（air） |
| `0ops` | `./cmd/cli` | — | 否 |
| `0ops-mcp` | `./cmd/mcp` | — | 否 |

CLI 與 MCP 的 Dockerfile 只保留 `deps` / `builder` / `runtime` 三 stage。

### 6.3 Image lint
- `hadolint cmd/*/Dockerfile`：強制
- 必達規則：固定 base image tag（禁 `latest`）、`USER` 非 root、`COPY` 不得 `--chown=0`、不得使用 `ADD <url>`

## 7. `.dockerignore`

```
.git
.github
.idea
.vscode
docs
**/*.md
**/testdata
**/*.test
**/*_test.go
build
dist
tmp
*.log
.env
.env.*
!.env.example
```

## 8. `.env.example`

```
# Postgres
POSTGRES_USER=ops
POSTGRES_PASSWORD=ops_dev_pw
POSTGRES_DB=ops
DATABASE_URL=postgres://ops:ops_dev_pw@db:5432/ops?sslmode=disable

# 0ops-server
OPS_LISTEN_ADDR=:8080
OPS_LOG_LEVEL=debug
OPS_CALLBACK_SECRET=dev-callback-secret-change-me

# 外部整合（dev 預設留空，需要時填寫）
OPS_GITHUB_APP_ID=
OPS_GITHUB_APP_PRIVATE_KEY_PATH=
OPS_CLOUDFLARE_API_TOKEN=
OPS_CLOUDFLARE_ACCOUNT_ID=
```

## 9. Makefile target 契約

| target | 行為 | 等價指令 |
|---|---|---|
| `dev` | 啟動 dev stack | `podman compose up -d` |
| `dev-down` | 停止 stack（保留 volume） | `podman compose down` |
| `dev-clean` | 停止並刪除 volume | `podman compose down -v` |
| `dev-logs` | 跟 server log | `podman compose logs -f server` |
| `dev-shell` | 進入 server 容器 | `podman compose exec server sh` |
| `migrate` | 套用 migration up | `podman compose run --rm migrate up` |
| `migrate-down` | 回滾一格 | `podman compose run --rm migrate down` |
| `build-images` | 三 binary runtime image | `podman build --target runtime ...` ×3 |
| `lint-compose` | 驗證 compose schema | `podman compose config -q` |
| `lint-docker` | 驗證 Dockerfile | `hadolint cmd/*/Dockerfile` |

## 10. 驗證準則

依 AGENTS.md「Testing」章節，本 feature 必達驗證：

| 驗證項 | 方式 | 通過條件 |
|---|---|---|
| compose schema 合法 | `podman compose config -q` | exit 0、無 warning |
| Dockerfile lint 通過 | `hadolint cmd/*/Dockerfile` | exit 0 |
| migrate idempotent | 連跑兩次 `make migrate` | 第二次 `goose status` 顯示無 pending |
| server `/health` 回應 | `make dev` 後 30s 內 `curl localhost:8080/health` | HTTP 200 |
| Image 為 distroless（無 shell） | `podman run --rm localhost/0ops-server:runtime sh` | 失敗（預期） |
| Image 以 nonroot 執行 | `podman run --rm localhost/0ops-server:runtime id` | uid != 0 |
| `.env` 不入版本控制 | `.dockerignore` 與 `.gitignore` 雙含 `.env` | grep 通過 |
| rootless podman 啟動成功 | `podman info --format '{{.Host.Security.Rootless}}'` | `true` |

## 11. 對 `docs/0ops-plan.md` 的修改清單

本 spec 需於 `docs/0ops-plan.md` 同步以下三段（已實施）：

1. **「已決策參數」表**：新增「容器引擎 / 開發環境 → podman + podman compose」與「容器化檔案 → root compose.yaml + 各 binary Dockerfile」兩列
2. **「Go 技術棧」表**：新增「容器化」「Container engine」「Dockerfile lint」三列
3. **「專案結構」段**：root 加入 `compose.yaml`、`.dockerignore`、`.env.example`；`cmd/{server,cli,mcp}` 各加 `Dockerfile`；移除 `infra/compose.dev.yaml` 與整個 `infra/` 目錄

## 12. Open issues

- **`compose.override.yaml` 機制**：是否提供範本給貢獻者覆寫個人設定（如 host port 衝突、開放 5432 給本機 GUI 客戶端）→ M0 spike 後決議
- **migrate 服務 image 策略**：自建 minimal image vs 直接以 `golang:alpine` + `go run` 跑 goose → 待 ADR-002 決定 migration 工具後敲定
- **rootless userns mapping**：distroless `nonroot`（uid 65532）與 host volume mount 的權限對齊細節 → 寫入 `docs/runbooks/dev-env-troubleshooting.md`
- **跨平台**：macOS 上 podman 走 VM（podman machine）的 file mount 效能與 SELinux `:Z` 行為差異 → 補 runbook
- **CI image 快取**：GitHub Actions 上以 podman 跑 build 是否需自建 runner（GHA 預設 runner 無 podman）→ 與 `deploy/workflows/` 一併規劃

## 13. 不可違反的硬性規則

> 違反以下任一項，PR 不可合入。

1. 不得新增 `docker-compose.yml` / `docker-compose.yaml` 檔
2. 不得在文件、scripts、Makefile 出現 `docker` 命令（應為 `podman`）
3. 不得使用 `podman-compose`（v1 wrapper）；只用 `podman compose`（v2）
4. Dockerfile base image 必須鎖版本 tag，禁用 `:latest`
5. runtime stage 必須 `USER nonroot:nonroot` 或等價非 root 使用者
6. `.env` 必須出現在 `.dockerignore` 與 `.gitignore`
7. `compose.yaml` 內任何 service 的 `image` 或 `build` 變更，必須同步更新本 spec 第 5 段
