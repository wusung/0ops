---
adr: "0009"
title: Migrations Image 策略
status: Accepted
date: 2026-05-10
tags:
  - migrations
  - dev-environment
  - container-image
supersedes: []
superseded-by: []
---

# ADR-0009：Migrations Image 策略

* Status：Accepted
* Date：2026-05-10
* 適用範圍：M0；本機 dev `migrate` compose service 與 production migration 執行所用之 container image
* 來源：`docs/features/dev-environment/spec.md` § 5.2 / § 12 之 open issue「`migrations` service image 與 migration 執行模式尚未定稿」；本 ADR 將其釘定
* 上游依賴：[ADR-0004](0004-k3s-role-and-orchestrator.md)（PostgreSQL via kine 為 K3s datastore；application Postgres 為獨立 instance）；[ADR-0008](0008-backend-ha-and-replication.md)（migration 安全閘）

## 0. TL;DR（先讀本段）

採用以下四項組合決策：

1. **Image 組成**：自建 minimal multi-stage image，runtime 僅含 `goose` binary 與 `migrations/` 目錄；不採 `golang:alpine + go run`、不採 goose 官方 image。
2. **Migration tool**：`pressly/goose`（與 plan.md 既定一致）；不採 `golang-migrate/migrate`、不採自寫。
3. **Image 命名**：`localhost/0ops-migrations:<runtime>`（dev compose）/ `ghcr.io/winshare/0ops-migrations:<commit_sha>`（production）；版本與主 binary 同步。
4. **Lifecycle 整合**：dev 由 `compose.yaml` 之 `migrate` service 跑一次性 job；production 由 K8s `Job`（pre-deploy 階段觸發；屬 backend chart `deploy/chart/launchpad/`）；皆走 idempotent `goose up`。

行為與 Dockerfile 細節以 `docs/features/dev-environment/spec.md` 為準，本 ADR 不重述 multi-stage YAML。

## 1. Context and Problem Statement

`docs/features/dev-environment/spec.md` § 5.2 對 `migrate` service 之 image 欄位明確標為「需新增專屬 ADR 或在實作時於本 spec 明確定稿；不得再引用與主題無關的 ADR-002」。本 ADR 是該 open issue 之決議。

需釘住三件事：

1. dev 與 production 是否共用同一 image？共用則可保證行為一致；分離則可各自最佳化。
2. Image 內容為「`golang:alpine` + 完整 toolchain + `go run`」還是「distroless + 預編 `goose` binary」？前者方便 dev iterate，後者 attack surface 小。
3. Migration 執行模式為一次性 Job 還是 entrypoint 內 idempotent 執行？前者明確、後者 forgiving。

ADR-0004 已將 application Postgres 與 K3s datastore Postgres 分開；本 ADR 之 migrations image 僅針對 application Postgres。Datastore 由 K3s 自管，本 ADR 不涵蓋。

## 2. Decision Drivers

* **DD1 dev/prod parity**：dev 跑通的 migration 必能在 prod 跑通；image 行為一致為硬要求。
* **DD2 Attack surface 最小化**：production migration image 不應含 build toolchain；與主 binary（distroless + nonroot）對齊。
* **DD3 啟動速度**：dev iterate 頻繁；image cold start 不應超過 5s。
* **DD4 Migration tool 既定為 goose**：plan.md `dev-environment` spec 已決；本 ADR 不重審。
* **DD5 Repo 保持單一 module**：不為 migrations 另起 sub-module；migrations 只是 SQL 檔 + goose 配置，無 Go 程式碼。
* **DD6 與主 binary 同步版本**：migration 與 backend code 之相容性綁同一 commit；不允許 production migrations 落後 backend code。
* **DD7 ImagePullSecret 共用**：production migration image 推 GHCR；與主 binary 共用 `ghcr-pull` Secret，不另設。

## 3. Considered Options

針對 (a) image 組成做完整 alternative 比較；(b)(c)(d) 列表帶過。

### 3.1 (a) Image 組成

| Option | 描述 |
|---|---|
| **A1. Multi-stage：builder（golang:1.23-alpine 拉 goose binary）→ runtime（distroless static + goose + migrations/）**（採用） | 與主 binary multi-stage 對稱；runtime 僅 binary + SQL；無 shell |
| A2. `golang:1.23-alpine` 全 toolchain runtime + `go run github.com/pressly/goose/v3/cmd/goose` | dev 友善；prod 明顯肥大 |
| A3. 拉官方 `ghcr.io/pressly/goose` image | 無自建；版本鎖於 upstream release |
| A4. `alpine:3.20` runtime + `apk add goose` | 簡單；版本受 alpine 套件鎖 |
| A5. backend binary 內嵌 migration（啟動時自動跑）| 無獨立 image；簡化部署 |

### 3.2 (b)(c)(d) 局部選擇

| 子決策 | 採用 | 否決 | 一句結論 |
|---|---|---|---|
| (b) Migration tool | `pressly/goose` | `golang-migrate` / 自寫 | plan.md 已定 |
| (c) 執行模式 | 一次性 Job（`goose up`，idempotent）| entrypoint always-run / hand-shell | Job 提供明確 status code 與 audit trail |
| (d) 版本綁定 | 與主 binary 同 commit_sha tag | 獨立版本 | DD6 dev/prod parity 要求 |

## 4. Decision Outcome

採用 **A1**，搭配 (b) goose、(c) Job 模式、(d) commit_sha 同步。

具體展開：

1. **Dockerfile**：`migrations/Dockerfile`，multi-stage：
   - `FROM golang:1.23-alpine AS builder` → `go install github.com/pressly/goose/v3/cmd/goose@v3.x.y`（version pin）
   - `FROM gcr.io/distroless/static-debian12:nonroot AS runtime` → `COPY --from=builder /go/bin/goose /usr/local/bin/goose` + `COPY migrations/ /migrations/`
   - `ENTRYPOINT ["/usr/local/bin/goose", "-dir", "/migrations", "postgres"]`
   - dev 由 compose.yaml 傳 `command: ["$DATABASE_URL", "up"]`；prod 由 K8s Job spec 傳同樣 args
2. **Build 與發佈**：
   - Dev：`make build-images` 含 `podman build -f migrations/Dockerfile -t localhost/0ops-migrations:runtime .`
   - Production：與主 binary 同一 GHA workflow；commit_sha 一致
3. **Compose service**（接續 dev-environment spec § 5.2）：
   - `image: localhost/0ops-migrations:runtime`（取代待補空白）
   - `command: ["$DATABASE_URL", "up"]`
   - `restart: no`、`depends_on: { db: { condition: service_healthy } }`
4. **Production Job**（屬 backend chart `deploy/chart/launchpad/templates/job-migration.yaml`）：
   - 由 ArgoCD pre-sync hook 觸發；backend Deployment 之 readiness 在 Job complete 後才 ready
   - Job restart policy `OnFailure`；backoff limit 3
5. **Goose 版本鎖定**：寫入 `migrations/Dockerfile` 之 `go install` URL `@v3.x.y` 顯式 pin；升版走 PR + staging 24h
6. **Migration policy** 接續 ADR-0008 § 8 與 plan.md「Migration 安全閘」段：CI lint、staging 24h、CONCURRENTLY 強制、ADD COLUMN NOT NULL 拆 3 步

## 5. Pros and Cons of the Options

### 5.1 (a) Image 組成

#### A1. Multi-stage distroless（採用）

* Good：runtime 僅 `goose` binary（~10MB）+ `migrations/` SQL 檔；無 shell、無 toolchain；與主 binary 安全姿態一致。
* Good：dev / prod image 結構相同；DD1 parity 強保證。
* Good：cold start < 1s；DD3 達標。
* Good：goose 版本顯式 pin 於 Dockerfile；可審計。
* Bad：每次 goose 升版需 rebuild image；屬可接受成本（goose 升版頻率低）。
* Bad：distroless 無 shell，debug 需用 `kubectl exec` 困難；可接受（goose CLI output 通常足夠）。

#### A2. `golang:alpine` + `go run`

* Good：dev iterate 最快；改 SQL 檔即可重跑。
* Bad：prod image > 300MB（含 toolchain）；DD2 attack surface 大。
* Bad：`go run` 每次重編；prod 啟動慢。

#### A3. 官方 `pressly/goose` image

* Good：無自建。
* Bad：版本鎖於 upstream；無法配合主 binary 同步 commit_sha tag。
* Bad：image 預設 entrypoint 與我們需求不匹配；仍需 wrap。
* Bad：upstream image 含 alpine shell；DD2 不滿足（雖比 A2 好）。

#### A4. alpine + apk

* Good：簡單。
* Bad：apk 套件版本與 upstream Go 釋出有 lag；版本控制弱。
* Bad：仍含 shell。

#### A5. backend binary 內嵌 migration

* Good：部署簡化；無獨立 Job。
* Bad：backend 啟動時自動跑 migration；多 replica 場景 race（兩 pod 同時跑）。
* Bad：rollback 困難（migration 已執行但 binary 想退版）。
* Bad：與 ADR-0008 之 zero-downtime migration policy 衝突（policy 要求 migration 與 code 分階段）。
* Bad：違反 DD6 之另一面：migration 執行時機不可控。

## 6. Consequences

### 6.1 Positive

* migration image 與主 binary 安全姿態一致；distroless + nonroot；attack surface 一致最小化。
* dev 跑通即 prod 跑通；DD1 dev/prod parity 強保證。
* Goose 版本顯式 pin；升版受審計。
* 版本與 commit_sha 同步；rollback 一致。
* Production K8s Job 模式提供明確 status code + log；audit 完整。

### 6.2 Negative

* 每次 SQL migration 改動需 rebuild image；CI build matrix 多一條（雖快）。
* Distroless image debug 需 ephemeral container（K8s 1.26+）；ops 學習成本。
* Goose version pin 升版需 PR；無自動 patch tracking。

### 6.3 Neutral

* `migrations/` 目錄結構（單檔 SQL vs 拆分）為 dev convention；不在本 ADR。
* Migration 命名規約（`000X_description.sql`）為 goose 慣例；不在本 ADR。
* 是否在 image 內含 `goose status` 之自動 dump（debug 用）：v1 不做；ops 用 `kubectl exec --image=alpine` 介入。

## 7. Revisit Triggers

下列任一條件觸發時，應重新評估本 ADR 對應段：

1. **Goose 維護中斷**：upstream 半年未更新或 CVE 不修 → 評估 fallback 至 `golang-migrate/migrate`。
2. **Migration image cold start > 5s**：違反 DD3 → 重審 image 組成。
3. **Production migration job 失敗率 > 1%**：可能為 image 自身 bug → 重審 multi-stage 設定。
4. **Multi-cluster / multi-region**（v2）：migration 跨 cluster 協調可能需要新模式。
5. **Schema 變更頻率劇增**（每月 > 20 migration）：可能需引入 schema management 工具（如 Atlas）；重審 (b)。

## 8. More Information

* dev compose `migrate` service 設定：[`docs/features/dev-environment/spec.md`](../features/dev-environment/spec.md) § 5.2
* Migration 安全閘（CI lint、staging 24h）：[ADR-0008 Backend HA 與 Postgres 複製](0008-backend-ha-and-replication.md) 第 8 節 + [`docs/features/postgres-ha-and-dr/spec.md`](../features/postgres-ha-and-dr/spec.md) § 10
* application Postgres 拓樸：[`docs/features/postgres-ha-and-dr/spec.md`](../features/postgres-ha-and-dr/spec.md)
* K3s datastore Postgres 之 schema 管理：屬 K3s upstream，[ADR-0004 K3s 角色](0004-k3s-role-and-orchestrator.md)；本 ADR 不涵蓋

## 9. Open Questions

下列問題不阻擋本 ADR 通過，但需在 M2（GA 前）敲定：

1. **Goose 之具體 minor 版本**：v3.x.y 之 y 值；本 ADR 採「最新 stable + PR 升版」
2. **Production K8s Job 之 ServiceAccount 權限**：需 Postgres 連線；應與 backend ServiceAccount 共用 `postgres-app-credentials` Secret
3. **Migration 失敗時 rollback 流程**：Job 失敗 → backend Deployment 不 ready → ArgoCD sync stuck；ops 介入手動 rollback；屬 runbook
4. **Test data seeding**：dev / staging 是否需要 seed data？v1 dev 採 empty DB；staging 由 ops 手動匯
5. **Migration squash**：未來累積 100+ migration 後是否 squash？v1 不做；v2 評估
