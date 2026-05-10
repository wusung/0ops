---
adr: "0005"
title: Build Pipeline 觀察點與 HMAC Callback 設計
status: Accepted
date: 2026-05-09
tags:
  - build
  - ci
  - github-actions
  - hmac
  - callback
  - security
supersedes: []
superseded-by: []
---

# ADR-0005：Build Pipeline 觀察點與 HMAC Callback 設計

* Status：Accepted
* Date：2026-05-09
* 適用範圍：M0–M5；managed app 的 build → push → manifest render → callback 端到端鏈路
* 來源：`docs/0ops-plan.md`「Build & deploy」「GitOps target」段；接續 [ADR-0002](0002-idempotency-and-compensation.md) 預留之 HMAC callback 細節決策
* 上游依賴：[ADR-0001](0001-multi-tenancy-and-rbac.md)（team-scoped GitHub App install）；[ADR-0002](0002-idempotency-and-compensation.md)（callback over polling、reversible-first 副作用順序）；[ADR-0004](0004-k3s-role-and-orchestrator.md)（image registry 與 ImagePullSecret 對接）；[ADR-0006](0006-observability-baseline.md)（trace_id 跨界傳遞）

## 0. TL;DR（先讀本段）

採用以下九項組合決策：

1. **Build 平台**：GitHub Actions `workflow_dispatch`；不採 self-hosted runner、不採 Tekton、不採第三方 CI。
2. **Image build 工具**：v1 主路徑 = Cloud Native Buildpacks（`pack build` + paketo）；v1.1 補 Dockerfile fallback；不採純 Dockerfile-only、不採 ko / buildah。
3. **Image registry**：GitHub Container Registry（GHCR）；GHCR pull token 由 backend 用 GitHub App installation token 簽發 1h、每 30 min refresh（接續 ADR-0004）。
4. **Trivy 強制度時程**：v1 GA `severity=HIGH,CRITICAL` + `exit-code=0`（觀察）；**v1.1** 改 `exit-code=1`（強制阻擋）；轉換條件為「v1 GA 後 1 個月觀察期 + dashboard 顯示 < 5% PR 因 HIGH/CRITICAL 被阻擋」。
5. **HMAC Callback 簽章協定**：HMAC-SHA256 over `{timestamp}.{payload_json}`；headers `X-0ops-Timestamp` + `X-0ops-Signature: sha256=<hex>`；server 端 timestamp window ±5 min；replay 防護用 `webhook_dedup(provider='gha-callback', delivery_id=deploy_run_id)` 24h。
6. **Build secret 注入**：backend 簽發 **20 min ephemeral token**（HMAC-signed JWT-like），透過 `repository_dispatch.client_payload` 帶入 GHA；token 綁 `deploy_run_id`，consume 即標記。不採 GHA OIDC（v1）、不採長期 GHA org secrets。
7. **GitOps push 衝突策略**：retry + rebase 最多 5 次；超過即進 `compensating` 狀態（接續 ADR-0002 狀態機）；不採 queue serialization、不採 leader-only push。
8. **Polling fallback**：reconciler 對 `deploy_run.status='building'` 滯留 > 30 min 主動拉 GitHub API `workflow_run`；callback 為主路徑，polling 為退路（接續 ADR-0002）。
9. **Callback secret 管理**：`OPS_CALLBACK_SECRET` 為 backend 與 GHA 共享之單一 secret，存於 GitHub repo / org secret；rotation 90 天；rotation 期間支援雙 secret window（舊 + 新）30 分鐘。

行為與 workflow YAML 細節以 `docs/0ops-plan.md`「Build & deploy」段為準，本 ADR 不重述 workflow steps。

## 1. Context and Problem Statement

managed app 的 build 鏈路橫跨四個信任邊界：backend → GitHub Actions runner → GHCR → 0ops-gitops repo → ArgoCD。其中 GHA runner 為**第三方執行環境**，backend 必須以結構化方式驗證每一步的回傳：

* GHA runner 可能被 fork / impersonate；backend 不得僅憑 IP 來源信任 callback。
* GHA runner 可能 retry / 重啟；同一個 callback 可能送達兩次。
* GHA runner 可能不送 callback（runner crash、network partition）；backend 需有獨立收斂路徑。
* Build 步驟需用 backend 簽發的能力（如 GHCR push 權限、deploy_run callback 簽章 secret）；這些能力**不可長期存在 GHA 環境變數**，否則 fork PR 即可竊取。

ADR-0002 已釘住「callback 為主、polling 為輔」與「副作用 reversible 先 → irreversible 後」原則，但具體簽章協定、replay 防護、build secret 注入機制、GitOps push 衝突策略尚未決定。本 ADR 把這些釘成不可變協定。

## 2. Decision Drivers

* **DD1 Build 不在 0ops 核心競爭力**：應使用市場既有 build 基礎設施而非自建；除非有強烈安全 / 成本理由。
* **DD2 Callback 必須抗 replay 與 forgery**：第三方 runner 信任邊界外，簽章必須在 server 端可驗證且抗時間軸攻擊。
* **DD3 Build secret 短期化**：任何在 GHA 環境出現的 secret 必須有「最大暴露時間」上限；不可長期駐留。
* **DD4 Trivy 強制度與發版速度權衡**：v1 GA 即強制 HIGH/CRITICAL block 會撞牆——既有 repo 可能本身有 CVE；需要觀察期再強制。
* **DD5 GitOps push 並發**：多個 team 同時 deploy 會在 `0ops-gitops` 主分支造成 push 衝突；需有確定性的衝突策略。
* **DD6 Callback 失送 vs 重複送達**：兩種失敗模式都需處理；callback-only 與 polling-only 都不安全。
* **DD7 Build platform 與 GitHub App 共生**：plan 已決 `actions:write` scope；若採非 GHA 平台需重新評估 webhook / 觸發機制。
* **DD8 Trace 跨界**：callback payload 必帶 `trace_id`，否則 ADR-0006 的 5 段 trace propagation 鏈會在 GHA 段斷裂。

## 3. Considered Options

針對 (a) build 平台、(d) HMAC callback 簽章協定、(e) build secret 注入模型 做完整比較；(b)(c)(f)(g) 為局部技術選擇，列表帶過。

### 3.1 (a) Build 平台

| Option | 描述 |
|---|---|
| **A1. GitHub Actions `workflow_dispatch`**（採用） | 用 plan 既定 GHA workflow；backend 透過 `repository_dispatch` API 觸發 |
| A2. Self-hosted runner 群 | 自管 runner pool（K3s 或獨立 VM）；用 `act` / GHA 自管 |
| A3. Tekton on K3s | K8s native CI；Pipeline / PipelineRun CRD |
| A4. CircleCI / GitLab CI | 第三方 CI；獨立帳號 / billing |
| A5. BuildKit standalone | 無 CI 框架；backend 直接呼 BuildKit；自寫 orchestration |
| A6. Drone CI / Woodpecker self-hosted | OSS CI；container-native |

### 3.2 (d) HMAC Callback 簽章協定

| Option | 描述 |
|---|---|
| **D1. HMAC-SHA256 + timestamp window + delivery_id dedup**（採用） | `X-0ops-Signature: sha256=<hmac>`、`X-0ops-Timestamp` 偏離 ±5 min；`webhook_dedup` 表 24h 反重放 |
| D2. GitHub OIDC + JWT 驗證 | GHA 注入 OIDC token，backend 驗 issuer + audience |
| D3. mTLS（GHA runner client cert） | runner 持 client cert；backend 驗證 |
| D4. No callback, polling-only | backend 主動拉 GHA workflow_run 狀態 |
| D5. Asymmetric signature（Ed25519） | private key 簽，public key 驗；密鑰非對稱 |

### 3.3 (e) Build Secret 注入模型

| Option | 描述 |
|---|---|
| **E1. Backend-issued ephemeral token via `repository_dispatch.client_payload`**（採用） | backend 簽發 20 min HMAC token，綁 `deploy_run_id`；workflow 從 inputs 取出使用 |
| E2. GitHub OIDC + AWS / GCP STS-style exchange | runner 用 OIDC token 換取雲 IAM 短期 credential |
| E3. Long-lived GHA org / repo secrets | secret 存於 GHA secret store，runner 直接讀 |
| E4. Vault sidecar in workflow | runner 啟動時從 Vault 拉 short-lived credential |
| E5. SOPS-encrypted secrets in repo | secrets 加密 commit；workflow runtime 解密 |

### 3.4 (b)(c)(f)(g) 局部選擇

| 子決策 | 採用 | 否決 | 一句結論 |
|---|---|---|---|
| (b) Image build 工具 | v1 CNB（paketo） + v1.1 Dockerfile fallback | 純 Dockerfile / ko / buildah | CNB 對「無 Dockerfile 的 polyglot repo」即用即跑；Dockerfile 為冷僻語言（Rust / Elixir）退路 |
| (c) Trivy 強制度時程 | v1 觀察（exit 0）→ 1 個月後 v1.1 強制（exit 1） | 立即強制 / 永不強制 | 立即強制會撞既有 repo CVE；永不強制違反 DD4 |
| (f) GitOps push 衝突 | retry + rebase ×5 → `compensating` | queue serialization / leader-only push | queue 增加延遲；leader-only 與 v1 single replica 重複 |
| (g) Image registry | GHCR | ECR / GAR / Docker Hub / 自架 Harbor | GHCR 與 GitHub App 同帳體系；無多帳號管理；free tier 對 v1 規模充足 |

## 4. Decision Outcome

採用 **A1 + D1 + E1**，搭配 (b) CNB-primary、(c) trivy 觀察→強制 1 個月時程、(f) retry-rebase ×5、(g) GHCR。

具體展開：

1. **Build 觸發路徑**：
   * backend `internal/server/services/workflowdispatch/` 用 GitHub App installation token 呼 `POST /repos/{owner}/{repo}/dispatches`。
   * `client_payload` 帶：`deploy_run_id`, `trace_id`, `app_slug`, `team_slug`, `commit_sha`, `ref`, `image_ref`, `ops_token`（見 (E1) 簽章），`callback_url`。
2. **Image build**：
   * 主路徑：`pack build $IMAGE_REF --builder paketobuildpacks/builder-jammy-base --path . --publish --cache-image $IMAGE_REF-cache`。
   * Builder 版本鎖於 workflow YAML；升版需 PR 與測試。
   * v1.1 補 Dockerfile fallback：偵測 repo root 有 `Dockerfile` 即用 BuildKit；偵測順序與 PaketoCNB stack detection 對齊（CNB 失敗 → Dockerfile）。
3. **Image scan（Trivy）**：
   * v1 GA：`severity=HIGH,CRITICAL`、`exit-code=0`、`ignore-unfixed=true`；結果寫入 callback payload `scan_summary`。
   * v1.1 升 `exit-code=1`；轉換條件為「v1 GA 後 1 個月觀察期 + dashboard `0ops_image_scan_block_rate` < 5%」。
   * Trivy DB 版本由 `trivy-action` 內建管理；不另鎖。
4. **Image push**：
   * Registry：`ghcr.io/<owner>/0ops-apps/<team_slug>/<app_slug>:<commit_sha>`。
   * Auth：`GHCR_TOKEN` 由 GitHub App installation token 經 backend 換發；workflow 從 `client_payload.ops_token` 取出後 stdin login。
5. **GitOps render & push**：
   * Render：`./scripts/render-and-push-gitops.sh`（位於 0ops repo `deploy/workflows/`）。
   * Push retry：`git push` 衝突 → `git pull --rebase` 重試最多 5 次。
   * 失敗（> 5 次）：callback 帶 `failure_classification=gitops_push_conflict`，backend 進 `compensating`（接續 ADR-0002）。
6. **HMAC Callback（D1）**：
   * Endpoint：`POST {OPS_BACKEND_URL}/internal/deploy-runs/{run_id}/callback`。
   * Headers：
     * `X-0ops-Timestamp`：Unix epoch seconds。
     * `X-0ops-Signature`：`sha256=<hex(HMAC-SHA256(secret, timestamp + "." + body))>`。
     * `Content-Type: application/json`。
   * Payload schema：
     ```json
     {
       "run_id": "uuid",
       "status": "success | failure | cancelled",
       "trace_id": "01J...",
       "image": "ghcr.io/.../app:sha",
       "build_minutes": 4.2,
       "image_size_bytes": 12345678,
       "failure_classification": "buildpack_detect_failed | ... | unknown",
       "scan_summary": { "high": 0, "critical": 0 }
     }
     ```
   * Server-side 驗證順序：
     1. Parse `X-0ops-Timestamp`；偏離當下 server clock > 5 min → `400 stale_timestamp`。
     2. Recompute HMAC over `{timestamp}.{raw_body}`；與 header 比對（constant-time `hmac.Equal`）→ 不符 `401 invalid_signature`。
     3. `webhook_dedup(provider='gha-callback', delivery_id=run_id)` 查詢；已存在 → `200 ok`（idempotent，不重做副作用）。
     4. 通過 → 進 `deploy_run` 狀態機 transition。
   * Callback secret：`OPS_CALLBACK_SECRET`，存於 GitHub repo secret；rotation 90 天，rotation 期間支援雙 secret window 30 分鐘（new + old 都接受）。
7. **Build Secret 注入（E1）**：
   * `ops_token` 為 backend 簽發的 ephemeral token：HMAC-signed payload `{run_id, expires_at, scopes:["ghcr:push", "callback:write"]}`。
   * TTL：20 min（涵蓋最長 build 4–6 min × 安全餘裕）。
   * 一次性：backend 在 `cli_token` 表記錄 `consumed_at`；同一 token 重用即拒收。
   * 簽章 secret：與 `OPS_CALLBACK_SECRET` 為**獨立** secret（`OPS_TOKEN_SIGNING_SECRET`），blast radius 隔離。
   * 過期或失效時 GHA workflow 立即終止並 callback `failure_classification=build_secret_expired`。
8. **Polling Fallback**：
   * Reconciler 對 `deploy_run.status='building'` 且 `started_at < now() - 30min` 拉 `GET /repos/{owner}/{repo}/actions/runs/{workflow_run_id}`。
   * 拉到結果即 transition 狀態機；callback 後到時走 `webhook_dedup` 直接 200。

## 5. Pros and Cons of the Options

### 5.1 (a) Build 平台

#### A1. GitHub Actions `workflow_dispatch`（採用）

* Good：與 plan 既定 GitHub App + `actions:write` scope 對齊；觸發協定為 GitHub 標準 API。
* Good：runner / 編譯緩存 / artifact 上傳 / image push 全套基礎設施由 GitHub 維運；v1 不需自建。
* Good：fork PR 預設不繼承 secrets；安全模型較自管 runner 嚴格。
* Good：費用模型對 v1 規模合理（GitHub free tier + paid minutes）。
* Bad：build 等待時間受 GHA queue 與 runner 啟動時間影響；冷啟動可能 30s+。
* Bad：GitHub 服務中斷 → 整個 build 鏈路停擺；無法降級。
* Bad：高頻 deploy 場景的並發可能撞 GitHub free tier 限制；商業擴展時需評估 paid plan。
* Bad：runner 在 GitHub 控制下；高安全客戶可能要求 self-hosted runner。

#### A2. Self-hosted runner 群

* Good：完全控制執行環境；high security 客戶友善。
* Good：可內建 builder cache 預熱；冷啟動 0s。
* Bad：runner pool 自管（K8s deployment、auto-scale、token rotation）；ops 工作量大。
* Bad：仍依賴 GHA control plane；GitHub 中斷時觸發層仍失效。
* Bad：違反 DD1（不在核心競爭力）。

#### A3. Tekton on K3s

* Good：K8s native；可與 ArgoCD / GitOps 形成單一控制面。
* Good：runner / Pipeline 完全自管，無 vendor lock-in。
* Bad：Pipeline / PipelineRun CRD 學習曲線；GHA 範例豐富、Tekton 範例相對少。
* Bad：與 plan 既定 GHA 棧背離；遷移成本不對等於收益。
* Bad：本身為 K3s workload；與 ADR-0004 的單 cluster 失效域同步惡化。

#### A4. CircleCI / GitLab CI

* Good：成熟商業 CI；功能完整。
* Bad：另開帳號 / billing；對 v1 規模 over-investment。
* Bad：與 GitHub App 整合非原生。

#### A5. BuildKit standalone

* Good：零中介；backend 直連 BuildKit。
* Bad：需自寫 build orchestration、retry、artifact 上傳；ops 工作量翻倍。
* Bad：違反 DD1。

#### A6. Drone CI / Woodpecker

* Good：OSS、container-native、輕量。
* Bad：社群規模較小；CVE 回應與 GitHub Actions ecosystem 比為弱。
* Bad：仍需 self-host；ops 工作量類似 A2。

### 5.2 (d) HMAC Callback 簽章協定

#### D1. HMAC-SHA256 + timestamp + dedup（採用）

* Good：實作簡單；`crypto/hmac` + `crypto/sha256` 為 stdlib；無第三方依賴。
* Good：timestamp window 防 replay；`webhook_dedup` 防同 delivery_id 重送。
* Good：constant-time `hmac.Equal` 防 timing 攻擊。
* Good：與 GitHub webhook 自身（`X-Hub-Signature-256`）使用相同協定；team 心智成本一致。
* Bad：對稱密鑰；secret 需在 backend 與 GHA 雙端存在；rotation 操作需雙端同步。
* Bad：secret 一旦洩漏即可偽造 callback 至洩漏被偵測為止；blast radius = rotation cadence。

#### D2. GitHub OIDC + JWT

* Good：非對稱；無共享 secret。
* Good：runner 身份可驗證至 repo + workflow + ref 級別。
* Bad：v1 backend 需引入 JWT 驗證 + JWKS 拉取；複雜度高於 HMAC。
* Bad：GitHub OIDC issuer 需在 backend 端 trust；額外 trust anchor 管理。
* Bad：v1 規模 over-engineering；payload 簽章 + replay 防護仍需獨立做。

#### D3. mTLS

* Good：runner 身份可驗證；transport 層加密。
* Bad：runner client cert 分發為新運維面；GHA 環境注入 cert 仍需 secret-like 機制。
* Bad：雙向認證對 backend 入口層改動大；NLB / Ingress 需處理 TLS termination 變更。

#### D4. Polling-only

* Good：backend 不需開放 inbound callback 端點；攻擊面最小。
* Bad：違反 ADR-0002 已決「callback 為主、polling 為輔」；polling-only 對 lead time 影響顯著（每 30s 拉一次 = 平均 15s 延遲）。
* Bad：GitHub API rate limit 在高 deploy 頻率下會撞牆。

#### D5. Asymmetric signature（Ed25519）

* Good：非對稱；GHA 持 private key、backend 持 public key 即可。
* Bad：private key 需在 GHA 環境，仍是 secret 駐留問題（與 D1 對稱密鑰本質相同）。
* Bad：簽章 / 驗章程式庫雖有 stdlib（`crypto/ed25519`），但 webhook 簽章生態多為 HMAC，team 心智轉換成本。

### 5.3 (e) Build Secret 注入

#### E1. Backend-issued ephemeral token via `repository_dispatch.client_payload`（採用）

* Good：TTL 短（20 min）；token 洩漏 blast radius 受限於 build window。
* Good：token 綁 `deploy_run_id`；任何使用都可追溯至特定 build。
* Good：backend 為單一簽發源；rotation 即換 signing secret，不需 GHA 端任何動作。
* Good：與 ADR-0006 trace 鏈路自然整合（token payload 含 trace_id）。
* Bad：backend 需新增 token signing 服務；M0 工作量增加。
* Bad：簽發 / 驗證需測試覆蓋（含 expired / consumed / signature mismatch）。
* Bad：若 backend 簽章 secret 洩漏，攻擊者可偽造 ops_token；secret rotation 必須與 callback secret 對齊節奏。

#### E2. GitHub OIDC + STS-style exchange

* Good：無 secret 駐留 GHA；最 cloud-native。
* Good：與 AWS / GCP IAM 整合最深。
* Bad：v1 主要 secret 是 GHCR pull 與 callback signing；皆 0ops 自管，無雲 IAM 對應。
* Bad：強制走 OIDC 對 v1 over-engineering。

#### E3. Long-lived GHA org secrets

* Good：實作最簡單；workflow 直接讀 secret。
* Bad：違反 DD3；secret 長期駐留 GHA。
* Bad：fork PR 雖預設不繼承，但任何 workflow 修改 + maintainer 誤合併即可洩漏。
* Bad：rotation 操作為 GHA UI 點選，難自動化。

#### E4. Vault sidecar

* Good：成熟 secret management；audit 完整。
* Bad：v1 無 Vault 部署；引入 Vault 為新運維面。
* Bad：Vault 客戶端在 GHA runner 啟動需額外 auth；複雜度疊加。

#### E5. SOPS-encrypted in repo

* Good：secrets 與 code 同步版本；audit log = git log。
* Bad：解密 key 需在 runner；仍是 secret 駐留問題。
* Bad：rotation = 重 commit + 重 deploy；運維節奏慢。

## 6. Consequences

### 6.1 Positive

* GHA + GHCR + GitHub App 同帳體系；無多廠商運維面。
* HMAC-SHA256 callback 與 GitHub webhook 自身同協定；team 心智一致。
* 20 min ephemeral token 把 GHA 環境內 secret 暴露時間上限固化；fork PR / log 洩漏 blast radius 受限。
* Callback over polling 配 30 min reconciler fallback；callback 失送容忍度確定。
* GitOps push retry × 5 + `compensating` 退路；並發衝突確定性處理。
* Trivy 觀察期讓既有 repo 平滑進入；v1.1 強制有量化轉換條件。

### 6.2 Negative

* GitHub 服務中斷 → 整 build 停擺；plan「Risks」未列為獨立風險，需追加。
* Callback secret rotation 需雙端同步（backend + GHA repo secret）；rotation runbook 為 ops 必備。
* Ephemeral token signing 服務需測試覆蓋；簽章 bug 會讓所有 build 失敗。
* Trivy v1 觀察期內若有 HIGH/CRITICAL CVE 進 production，責任邊界需文件化（產品 CVE policy）。
* HMAC 對稱密鑰模型：secret 洩漏 = 所有正在跑的 build 之 callback 可被偽造直至 rotation。
* GHCR free tier 在高頻 deploy 時可能撞限額；商業擴展時需 paid plan。

### 6.3 Neutral

* `pack build` builder 版本鎖於 workflow YAML；不在本 ADR 範圍。
* v1.1 Dockerfile fallback 之偵測順序與行為待 spike；本 ADR 僅約束「v1.1 必補」。
* GHA runner 規格（standard / large）為成本決策；不在本 ADR。
* Build cache 策略（cache-image flag）為性能優化；plan 已給範例，本 ADR 不重述。

## 7. Revisit Triggers

下列任一條件觸發時，應重新評估本 ADR 對應段：

1. **GHA 中斷頻率 > 1 次 / quarter 影響 GA**：重審 (a)，可能引入 self-hosted runner 或多 CI 退路。
2. **Callback secret 洩漏事件**：強制 rotation + 重審 (d)，可能升 D2（GitHub OIDC）。
3. **Trivy v1.1 強制阻擋率 > 5%**：1 個月觀察期結束時若 block rate > 5% → 暫緩升 v1.1，先做 CVE 修復推動。
4. **Trivy v1.1 強制阻擋率 < 0.5%**：可能代表 trivy 規則太鬆或 repo CVE 太少；重審 severity 範圍是否含 MEDIUM。
5. **GitOps push retry 失敗率 > 1%**：> 1% builds 因 push conflict 進 compensating → 重審 (f)，可能引入 queue。
6. **Callback 失送 → polling fallback 比例 > 1%**：重審 (d)，可能改 callback 至每階段（不只完成時）多點觸發。
7. **Build secret 洩漏事件**：重審 (e)，可能升 E2（OIDC）或縮 TTL（< 20 min）。
8. **GHCR rate limit / 配額撞牆**：重審 (g)，可能改 ECR / GAR；或評估 GitHub paid plan。
9. **企業客戶 self-hosted runner 要求**：商業需求觸發 → 重審 (a)，可能引入混合模式（公開 GHA + 企業 self-hosted）。

## 8. More Information

* `deploy/workflows/deploy-app.yml` 完整 YAML：`docs/0ops-plan.md`「Build & deploy」段第 415–466 行。
* Idempotency / `webhook_dedup` 表結構：[ADR-0002 Idempotency 與副作用補償](0002-idempotency-and-compensation.md) 第 4 節。
* GitHub App scope（`actions:write` 等）：[ADR-0001 多租戶模型與 RBAC](0001-multi-tenancy-and-rbac.md) 與 plan「Auth & RBAC / GitHub App 權限 scope」段。
* Image registry 與 ImagePullSecret refresh：[ADR-0004 K3s 角色與 v1 容器編排器選擇](0004-k3s-role-and-orchestrator.md) 第 4.4 節。
* trace_id 跨界傳遞要求：[ADR-0006 Observability baseline](0006-observability-baseline.md) 第 4.4 節。
* GitHub Webhook（push / installation 事件）安全：規劃為獨立 ADR；本 ADR 僅約束 backend → GHA → backend 之 callback，不含 GitHub → backend 之 push webhook。

## 9. Open Questions

下列問題不阻擋本 ADR 通過，但需在 M2（GA 前）敲定：

1. **Paketo builder 版本鎖定策略**：升版週期、回歸測試矩陣、與 stack 偵測行為的 backward compat。
2. **Dockerfile fallback 偵測順序**：v1.1 補 Dockerfile mode 時，CNB detect 失敗 → fallback Dockerfile 還是同時跑兩條取較成功者？
3. **Callback secret rotation 自動化**：90 天 rotation 是否自動跑 PR 改 GHA secret 或人工觸發？rotation runbook 落地位置。
4. **Ephemeral token JWT-like 規格**：用標準 JWT（HS256）還是自訂 payload？對 audit log 反序列化的影響。
5. **`OPS_CALLBACK_SECRET` 與 `OPS_TOKEN_SIGNING_SECRET` 是否同源**：同源簡單但 blast radius 重疊；獨立則 rotation 各自管。本 ADR 暫定獨立。
6. **GHCR 標籤策略**：`<commit_sha>` 為主 tag，是否同時 tag `:branch-{branch_name}` / `:latest`？影響回滾語意。
7. **Build minutes / image size 採樣精度**：plan 已落地 `usage_sample` 表，但 build_minutes / image_size_bytes 落入 `deploy_run` 還是 `usage_sample`？v1 計費未啟動，但寫入路徑需先固化。
8. **Trivy ignore-unfixed 默認值**：v1 設 `true`（忽略無 patch CVE）；v1.1 強制時是否改 `false`？影響 block rate。
