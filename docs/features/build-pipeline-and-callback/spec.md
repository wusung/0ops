# Feature Spec：build-pipeline-and-callback

> **狀態**：draft
> **來源**：`docs/0ops-plan.md`「Build & deploy」段；ADR-0005（GHA + HMAC callback）；ADR-0002（callback over polling）；ADR-0006（trace_id 跨界）；本 spec 依賴 `preview-confirm-gate`、`shared-dto-and-contract`、`gitops-render-and-argocd`、`secrets-management`、`error-model`
> **適用範圍**：backend 觸發 GHA `workflow_dispatch` → CNB pack build → Trivy scan → image push GHCR → 跨界 trace 之 GHA workflow YAML、HMAC callback endpoint、ephemeral token 簽發
> **對應 Milestone**：M2（與 create_app / redeploy 同步上線）

## 1. 結論（先讀本段）

- backend 觸發 build：`POST /repos/{owner}/{repo}/dispatches` with `event_type: deploy-app`、`client_payload: { run_id, app_slug, team_slug, commit_sha, ref, image_ref, ops_token, callback_url, trace_id }`
- GHA workflow `deploy-app.yml` 為 single source；位於 0ops repo `deploy/workflows/`，**不**位於使用者 repo（避免 fork PR 篡改）
- 5 階段：GHCR login → `pack build` → trivy scan → render & push gitops → callback backend
- Trivy v1 GA `severity=HIGH,CRITICAL exit-code=0`（觀察期）；1 個月後條件達成升 `exit-code=1`（v1.1）
- `ops_token` 為 backend 簽發 20 min 短期 HMAC token；綁 `run_id`；GHCR push 與 callback 共用此 token
- HMAC callback：`HMAC-SHA256({timestamp}.{body})`；header `X-0ops-Timestamp` + `X-0ops-Signature: sha256=<hex>`；timestamp window ±5 min；`webhook_dedup(provider='gha-callback', delivery_id)` 反 replay（`delivery_id` 取 `X-0ops-Delivery-ID`，缺值 fallback `run_id`）
- Callback secret 90 天 rotation；rotation 期間雙 secret 並存 30 分鐘
- Polling fallback：reconciler 對 `building` 滯留 > 30 min 主動拉 GitHub workflow_run 狀態（屬 `reconciler-and-incident` spec；本 spec 釘介接點）
- 同一個 callback `run_id` 重送 → 走 `webhook_dedup` 直接回 200，不重做 state machine

## 2. 範圍

### 2.1 包含
- backend 端 `internal/server/services/workflowdispatch/`：觸發 GHA、簽發 ops_token、查 workflow_run
- backend 端 `/internal/deploy-runs/{run_id}/callback` endpoint：HMAC 驗章、timestamp、replay、state machine 推進
- `deploy/workflows/deploy-app.yml` workflow YAML 結構與每階段行為
- `client_payload` schema 與簽章
- callback request / response 格式
- ops_token 簽發 / 驗證 / 一次性消費
- callback secret 與 token signing secret 之分離與 rotation 流程
- Trivy 觀察→強制升級條件量測

### 2.2 不包含
- GitHub App installation token 取得本身（屬 `github-app-install-flow` spec；本 spec 假設 backend 可取）
- Render manifest 與 push gitops（屬 `gitops-render-and-argocd` spec；本 spec 在 workflow 內呼用 `./scripts/render-and-push-gitops.sh`）
- Trivy block rate dashboard 與 alert（屬 `slo-and-alerting` spec）
- Reconciler polling 實作（屬 `reconciler-and-incident` spec）
- v1.1 Dockerfile fallback（屬 v1.1）
- Promote dev→prod 機制（v2）

## 3. 檔案結構

```
0ops/
├── deploy/
│   └── workflows/
│       ├── deploy-app.yml                      # GHA workflow（5 階段）
│       └── scripts/
│           └── render-and-push-gitops.sh       # 由 gitops-render-and-argocd spec 規範
├── internal/
│   └── server/
│       ├── services/
│       │   └── workflowdispatch/
│       │       ├── dispatch.go                 # POST /dispatches API 呼叫
│       │       ├── opstoken.go                 # 20 min ephemeral HMAC token 簽發 / 驗證
│       │       ├── client_payload.go           # client_payload struct + marshal
│       │       ├── workflow_run_pull.go        # polling fallback：GET /actions/runs
│       │       └── doc.go
│       ├── routers/
│       │   └── callback.go                     # POST /internal/deploy-runs/{id}/callback
│       └── auth/
│           └── webhook.go                      # HMAC 驗章 + timestamp window（共用 GitHub webhook 之程式碼路徑）
└── migrations/
    └── 000X_callback_indexes.sql               # webhook_dedup(provider, delivery_id) 已於 plan.md 定；本 spec 加入 (provider='gha-callback') 之常用查詢索引
```

## 4. GHA workflow 結構

### 4.1 觸發

backend 端：
```go
// internal/server/services/workflowdispatch/dispatch.go
type ClientPayload struct {
    RunID         string `json:"run_id"`           // deploy_run.id
    AppSlug       string `json:"app_slug"`
    TeamSlug      string `json:"team_slug"`
    CommitSHA     string `json:"commit_sha"`
    Ref           string `json:"ref"`              // branch / tag
    ImageRef      string `json:"image_ref"`        // ghcr.io/.../<team>-<app>:<commit_sha>
    OpsToken      string `json:"ops_token"`        // 20 min HMAC，綁 run_id
    CallbackURL   string `json:"callback_url"`     // backend public URL + path
    TraceID       string `json:"trace_id"`         // W3C trace_id
}
```

呼 GitHub：`POST /repos/winshare/0ops/dispatches` with body：
```json
{
  "event_type": "deploy-app",
  "client_payload": { ... ClientPayload ... }
}
```

注意：**target repo 為 0ops 自身 repo（`winshare/0ops`），非使用者 repo**；workflow 由 0ops 維護，不允許使用者修改。

### 4.2 Workflow YAML（節錄）

```yaml
name: deploy-app
on:
  repository_dispatch:
    types: [deploy-app]

permissions:
  contents: write       # checkout 0ops repo + push 0ops-gitops
  packages: write       # GHCR push（與 ops_token 互補）
  id-token: write       # OIDC（v1 不用，留 v1.1 evaluation）

env:
  RUN_ID:        ${{ github.event.client_payload.run_id }}
  APP_SLUG:      ${{ github.event.client_payload.app_slug }}
  TEAM_SLUG:     ${{ github.event.client_payload.team_slug }}
  COMMIT_SHA:    ${{ github.event.client_payload.commit_sha }}
  REF:           ${{ github.event.client_payload.ref }}
  IMAGE_REF:     ${{ github.event.client_payload.image_ref }}
  OPS_TOKEN:     ${{ github.event.client_payload.ops_token }}
  CALLBACK_URL:  ${{ github.event.client_payload.callback_url }}
  TRACE_ID:      ${{ github.event.client_payload.trace_id }}

jobs:
  deploy:
    runs-on: ubuntu-latest
    timeout-minutes: 20            # 硬上限；超過自動 cancel → callback failure
    steps:
      # 1. checkout 使用者 repo（非 0ops 自身）
      - name: Checkout target repo
        uses: actions/checkout@v4
        with:
          repository: ${{ github.event.client_payload.repo }}     # owner/name
          ref: ${{ env.REF }}
          token: ${{ secrets.GITHUB_APP_TOKEN }}                  # GitHub App installation token
          path: target

      # 2. GHCR login（用 ops_token，TTL 20 min）
      - name: Login GHCR
        run: |
          echo "${OPS_TOKEN}" | docker login ghcr.io -u ops-bot --password-stdin

      # 3. CNB build
      - name: Pack build
        working-directory: target
        run: |
          pack build "${IMAGE_REF}" \
            --builder paketobuildpacks/builder-jammy-base \
            --path . \
            --publish \
            --cache-image "${IMAGE_REF}-cache" \
            --env "BP_OCI_SOURCE=${COMMIT_SHA}"

      # 4. Trivy scan
      - name: Trivy scan
        uses: aquasecurity/trivy-action@v0
        id: trivy
        with:
          image-ref: ${{ env.IMAGE_REF }}
          severity: HIGH,CRITICAL
          exit-code: '0'                # v1 觀察；v1.1 改 '1'
          ignore-unfixed: 'true'
          format: json
          output: trivy-report.json

      - name: Parse Trivy results
        id: scan_summary
        run: |
          HIGH=$(jq '[.Results[]?.Vulnerabilities[]? | select(.Severity=="HIGH")] | length' trivy-report.json)
          CRITICAL=$(jq '[.Results[]?.Vulnerabilities[]? | select(.Severity=="CRITICAL")] | length' trivy-report.json)
          echo "high=$HIGH" >> $GITHUB_OUTPUT
          echo "critical=$CRITICAL" >> $GITHUB_OUTPUT

      # 5. Render & push gitops
      - name: Checkout 0ops repo (for scripts)
        uses: actions/checkout@v4
        with:
          path: ops

      - name: Render & commit gitops
        run: ./ops/deploy/workflows/scripts/render-and-push-gitops.sh
        env:
          GITOPS_DEPLOY_KEY: ${{ secrets.GITOPS_DEPLOY_KEY }}     # SSH private key

      # 6. Callback backend (always 跑，不論成敗)
      - name: Callback backend
        if: always()
        env:
          STATUS: ${{ job.status }}
          HIGH: ${{ steps.scan_summary.outputs.high }}
          CRITICAL: ${{ steps.scan_summary.outputs.critical }}
        run: |
          PAYLOAD=$(jq -nc \
            --arg run_id "$RUN_ID" \
            --arg status "$STATUS" \
            --arg trace_id "$TRACE_ID" \
            --arg image "$IMAGE_REF" \
            --argjson high "${HIGH:-0}" \
            --argjson critical "${CRITICAL:-0}" \
            '{run_id:$run_id, status:$status, trace_id:$trace_id, image:$image,
              scan_summary:{high:$high, critical:$critical}}')
          TS=$(date +%s)
          SIG=$(printf '%s' "${TS}.${PAYLOAD}" | openssl dgst -sha256 -hmac "$OPS_TOKEN" -hex | awk '{print $2}')
          curl -fsS -X POST "$CALLBACK_URL" \
            -H "Content-Type: application/json" \
            -H "X-0ops-Timestamp: $TS" \
            -H "X-0ops-Signature: sha256=$SIG" \
            --data "$PAYLOAD"
```

> **callback 簽章使用 `OPS_TOKEN`**（非 `OPS_CALLBACK_SECRET`）：本 spec § 6 解釋為何用 ephemeral token 簽，而非長期共享 secret。

### 4.3 失敗點與 callback status 對應

| 階段失敗 | `job.status` | `failure_classification`（backend 端設定） |
|---|---|---|
| checkout target repo | failure | `repo_checkout_failed` |
| GHCR login | failure | `registry_auth_failed` |
| pack build 偵測語言失敗 | failure | `buildpack_detect_failed` |
| pack build 編譯失敗 | failure | `build_compile_error` |
| timeout（20 min） | cancelled | `build_timeout` |
| GHCR push | failure | `registry_push_failed` |
| Trivy fail（v1.1 起） | failure | `image_scan_blocked` |
| render & push gitops | failure | `gitops_push_conflict` |
| 其他 | failure | `unknown` |

backend 端 callback handler 依 `job.status` + payload 內 `failure_classification`（若 GHA workflow 內判得出）填欄位；無法判時 backend 端用 GHA log API 進一步分類（reconciler 完成）。

## 5. `ops_token` 簽發

### 5.1 結構

```go
// internal/server/services/workflowdispatch/opstoken.go
type OpsTokenPayload struct {
    RunID     string    `json:"run_id"`
    ExpiresAt time.Time `json:"expires_at"`
    Scopes    []string  `json:"scopes"`        // {"ghcr:push", "callback:write"}
    TraceID   string    `json:"trace_id"`
}

// 簽發
func IssueOpsToken(p OpsTokenPayload, signingSecret []byte) string {
    body, _ := json.Marshal(p)
    bodyB64 := base64.RawURLEncoding.EncodeToString(body)
    mac := hmac.New(sha256.New, signingSecret)
    mac.Write([]byte(bodyB64))
    sigB64 := base64.RawURLEncoding.EncodeToString(mac.Sum(nil))
    return bodyB64 + "." + sigB64    // 形如 JWT 但只兩段（無 alg header）
}

// 驗證
func ParseOpsToken(token string, signingSecret []byte) (*OpsTokenPayload, error) {
    parts := strings.SplitN(token, ".", 2)
    if len(parts) != 2 { return nil, errInvalidFormat }
    expectedSig := hmac.New(sha256.New, signingSecret); ...
    if !hmac.Equal(...) { return nil, errInvalidSignature }
    var p OpsTokenPayload
    json.Unmarshal(...)
    if time.Now().After(p.ExpiresAt) { return nil, errExpired }
    return &p, nil
}
```

### 5.2 一次性

- backend 在 `cli_token` 表額外列 row（`kind = 'ops_token'`，與 device flow / pat 同表）；簽發即 INSERT
- `consumed_at != null` 即視為已用過；callback handler 驗 token 通過後立即 UPDATE consumed
- 同一 token 重用（如 GHA retry）：callback handler 偵測 consumed → 走 `webhook_dedup` 邏輯回 200（與 ADR-0002 last_result 回放對齊）

> **schema 注意**：`cli_token.kind` 列舉於 `auth-and-rbac` spec § 4.3 已新增 `device | pat`；本 spec 再加 `ops_token`。

### 5.3 TTL

- 20 min：涵蓋最長 build 4–6 min × 安全餘裕（含 GHA queue + 可能 retry）
- 硬上限；不接受個別 endpoint 自訂 TTL

### 5.4 Signing secret 分離

| Secret | 用途 | rotation |
|---|---|---|
| `OPS_TOKEN_SIGNING_SECRET` | 簽 / 驗 ops_token；server 端持有 | 90 天，雙 window 30 分鐘 |
| `OPS_CALLBACK_SECRET` | （v1 已不直接用；保留為退路）GHA 與 backend 共享之長期 callback secret | 90 天 |

> 本 spec 採 callback 用 ops_token 簽（短期）為主；`OPS_CALLBACK_SECRET` 保留作為 ops_token 簽發失敗時的 emergency fallback（callback 仍可送，backend 僅以 `OPS_CALLBACK_SECRET` 驗章）；v2 移除。

## 6. Callback endpoint

### 6.1 路由

```go
r.Post("/internal/deploy-runs/{run_id}/callback", callback.Handler)
```

- **不**經 `AuthBearer` middleware（callback 為 GHA 來源，無 bearer）；改由 HMAC 驗章
- 路徑 prefix `/internal/`：對外 LB 設 NetworkPolicy 只允許 GHA IP 範圍 + 同 cluster 內網（v1 寬鬆，相依 HMAC 驗章；v2 補 IP allowlist）

### 6.2 Handler 流程

```
1. 解 path param run_id
2. SELECT deploy_run WHERE id = $run_id
   - 0 row → 404 (但回 200 給 GHA 避免 retry storm，背景 log warn)
3. 解 X-0ops-Timestamp
   - parse 失敗 → 400 stale_timestamp
   - |now - ts| > 300s → 400 stale_timestamp
4. 取 raw body
5. 計算 expected_sig:
   - 主路徑：用該 deploy_run 之 ops_token 為 key（ops_token 從 deploy_run.events 或 cli_token 表取）
   - fallback：用 OPS_CALLBACK_SECRET 為 key
   - 主成功 → 進；fallback 成功 → log warn (ops_token 失效)；兩者皆失 → 401 invalid_signature
6. 解 body JSON 為 CallbackPayload（`trace_id` 必填）
7. webhook_dedup INSERT (provider='gha-callback', delivery_id = header `X-0ops-Delivery-ID`，缺值用 run_id)
   - 衝突（已存在）→ 200 ok（idempotent；不再執行 state machine）
8. 推進 deploy_run state machine：
   - status='success' → transition 'building → pushing → ... → live'（與 plan deploy 狀態機對齊）
   - status='failure' → transition 'failed'，含 failure_classification
   - status='cancelled' → transition 'cancelled'
9. 寫 audit_log
10. 回 200 ok
```

### 6.3 Callback payload schema

```json
{
  "run_id": "01J2K3M4N5P6Q7R8S9T0V1W2X3",
  "status": "success | failure | cancelled",
  "trace_id": "0af7651916cd43dd8448eb211c80319c",
  "image": "ghcr.io/winshare/0ops-apps/acme-prod/nextdemo:abc123",
  "build_minutes": 4.2,
  "image_size_bytes": 12345678,
  "scan_summary": {
    "high": 0,
    "critical": 0,
    "exit_code": 0
  },
  "failure_classification": "buildpack_detect_failed",
  "gitops_commit_sha": "def456..."
}
```

| 欄位 | 必填 | 說明 |
|---|---|---|
| `run_id` | 是 | UUID v4；對應 deploy_run.id |
| `status` | 是 | success / failure / cancelled |
| `trace_id` | 是 | 跨界 trace 第 4 段（接續 ADR-0006 § 4.4） |
| `image` | success 必填 | 推到 GHCR 的完整 image ref |
| `build_minutes` | 是 | 從 GHA `${{ steps.x.outputs.* }}` 計算或 backend 從 workflow_run 拉 |
| `image_size_bytes` | success 必填 | pack build output 大小；GHA 端 `docker image inspect` 取 |
| `scan_summary.high` / `critical` | 是 | Trivy 結果計數；觀察期亦填 |
| `failure_classification` | failure 時必填 | 對應 plan deploy_run schema 之列舉 |
| `gitops_commit_sha` | success 必填 | 該 deploy 對應的 0ops-gitops commit；與 `gitops-render-and-argocd` § 5.2 對齊 |

### 6.4 Replay protection

- `webhook_dedup(provider, delivery_id)` 主鍵：`(gha-callback, run_id)` 唯一
- 同 run_id 重送 → INSERT 衝突 → handler 直接 200 不重做 state machine
- 24h TTL（與 plan.md webhook_dedup 一致）；24h 後若仍有重送（極少見）會被視為新事件，但 deploy_run 已終態（live/failed/rolled_back），state machine 處理為 no-op

### 6.5 Timestamp window

- 必填 header `X-0ops-Timestamp`：Unix epoch seconds
- 偏離 server clock > 300s 一律拒收（含過去與未來）
- backend 與 GHA runner 時鐘以 NTP 同步（GHA runner 為 GitHub 維護）

## 7. Trivy 觀察→強制升級

### 7.1 觀察期（v1 GA～GA + 1 month）

- `exit-code: '0'`：scan 結果不阻擋 build
- 結果填入 `scan_summary` callback；backend 寫入 `deploy_run.scan_high` / `deploy_run.scan_critical`（schema 待 plan.md 補欄位）
- Metric `0ops_image_scan_block_rate{severity}` 定義為「假設 exit-code=1 時會被擋住的 build 比例」（即 scan_high > 0 OR scan_critical > 0）

### 7.2 升級條件

- GA 後 28d 觀察期結束
- `0ops_image_scan_block_rate < 5%` 持續 7d
- 滿足條件後 PR 改 workflow YAML `exit-code: '0' → '1'`；同時更新 ADR-0005 第 4 點為 v1.1

### 7.3 v1.1 強制階段

- `exit-code: '1'`：scan 失敗 → step fail → callback `failure_classification=image_scan_blocked`
- `failure_classification` 列舉新增 `image_scan_blocked`（於 plan.md 與 `reconciler-and-incident` spec 同步）
- CLI / MCP 端錯誤訊息：「Build blocked by image scan: HIGH=N, CRITICAL=M. See ghcr-trivy-report-<run_id>.json」

## 8. 與其他 spec 接合點

| 接合 | spec |
|---|---|
| ops_token 簽發為 reversible side_effect 之一部分 | `preview-confirm-gate` § 7.2 |
| GHCR push（image_ref 出現於 deployment.yaml） | `gitops-render-and-argocd` § 4.3 |
| `OPS_TOKEN_SIGNING_SECRET` / `OPS_CALLBACK_SECRET` 之 K8s Secret 與 rotation | `secrets-management` spec |
| Polling fallback（building > 30 min） | `reconciler-and-incident` spec |
| `failure_classification` 列舉與 CFR | `reconciler-and-incident` spec、`slo-and-alerting` spec |
| trace_id 第 3、4 段（dispatch payload + callback body） | `observability-skeleton` § 6.4 |
| Callback HMAC 與 GitHub webhook 共用驗章程式碼 | `webhook-and-redeploy` spec |
| `cli_token.kind = ops_token` 列舉 | `auth-and-rbac` spec § 4.3 |
| `scan_summary` 進 audit_log | `audit-log` spec |

## 9. 驗證準則

| 驗證項 | 方式 | 通過條件 |
|---|---|---|
| `repository_dispatch` 觸發 | mock GitHub API，呼 dispatch | request body 含完整 client_payload，type = `deploy-app` |
| ops_token 簽 / 驗 round-trip | unit test | 簽完即驗成功；改 1 byte 即 fail |
| ops_token 過期 | TTL 20 min；模擬 21 分鐘後 | `errExpired` |
| ops_token 一次性 | 同 token 第二次驗 | `errConsumed`；handler 走 webhook_dedup 路徑 |
| HMAC callback 驗章成功 | 用 ops_token 簽 → handler | 200 + state machine 推進 |
| HMAC callback 驗章失敗 | 改一字元 sig | 401 invalid_signature |
| Timestamp 過期 | TS 設成 6 分鐘前 | 400 stale_timestamp |
| Replay protection | 同 run_id 重送 | 第二次 200 但不再推 state machine（事件數不增） |
| Workflow YAML 合法 | `actionlint deploy/workflows/deploy-app.yml` | 通過 |
| 失敗分類正確 | mock pack build fail | callback `failure_classification = build_compile_error` |
| Trivy scan 結果填入 | mock 一個 HIGH CVE 的 image | callback `scan_summary.high = 1`；backend 寫入 deploy_run |
| Trivy v1 觀察期不擋 | exit-code=0；HIGH/CRITICAL > 0 | step success；callback success |
| Trivy v1.1 強制阻擋 | exit-code=1；HIGH > 0 | step fail；callback `failure_classification=image_scan_blocked` |
| Callback fallback secret | ops_token 過期；用 `OPS_CALLBACK_SECRET` 簽 | handler 接受並 log warn |
| Polling fallback | mock callback 永不送 + workflow_run 拉到 success | 30 min 後 reconciler 推進 state machine |
| trace_id 跨界 | dispatch 帶 trace_id；callback 必含同 trace_id | 兩次 trace_id 相同；audit_log / deploy_run 一致 |
| Build timeout | mock 20 min 仍未完成 | GHA cancel + callback status=cancelled |

## 10. SLI 對應

| SLI | 目標 | 量測 |
|---|---|---|
| Build success rate | > 85% / 28d（plan SLO） | `deploy_run.status = live` / `total terminal` |
| Build lead time（dispatch → live） | p50 < 10 min（plan SLO） | `live - created` p50 |
| Callback delivery rate | > 99% 不需 polling | `callback received via HMAC / total terminal` |
| Polling fallback rate | < 1% | `callback recovered via polling / total` |
| ops_token signing latency | p99 < 10ms | issue 至 return 的時間 |
| Replay 命中率 | < 0.1% callback | `webhook_dedup conflict / total callback` |
| Trivy block rate（v1 觀察期） | dashboard panel | `(scan_high+scan_critical > 0) / total` |

## 11. 對 `docs/0ops-plan.md` 的修改清單

1. 「Build & deploy」段（line 415-472）：交叉引用本 spec § 4 為 workflow YAML 完整 source；plan 內 YAML 改為摘要片段
2. 「DB schema § deploy_run」：新增欄位 `scan_high int`、`scan_critical int`、`gitops_commit_sha text`
3. 「DB schema § cli_token」：列舉 `kind` 補入 `ops_token`
4. 「Risks & open」：新增「GitHub 中斷影響 build」之獨立風險條目（接續 ADR-0005 § 6.2）
5. `failure_classification` 列舉：補入 `repo_checkout_failed`、`registry_auth_failed`、`image_scan_blocked`、`build_secret_expired`

## 12. Open issues

> 來源：ADR-0005 § 9 之 8 條 OQ + 本 spec 撰寫期間發現

- ADR-0005 OQ#1（Paketo builder 版本鎖定策略）：本 spec 採「workflow YAML 內 hardcode；升版需 PR」；具體升版週期待 runbook 落地
- ADR-0005 OQ#2（Dockerfile fallback 偵測順序）：v1.1 範圍
- ADR-0005 OQ#3（Callback secret rotation 自動化）：屬 `secrets-management` spec
- ADR-0005 OQ#4（ops_token JWT-like 規格）：本 spec § 5.1 採自訂兩段格式（避免 JWT alg confusion 漏洞）；audit_log 反序列化需處理此格式
- ADR-0005 OQ#5（callback / token signing secret 同源）：本 spec 採獨立兩 secret
- ADR-0005 OQ#6（GHCR tag 策略）：本 spec 採 `<commit_sha>` 為唯一 tag；不加 `:latest` / `:branch-*`（避免 race / 回滾語意模糊）；v1.1 評估補 `:branch-<branch>`
- ADR-0005 OQ#7（build_minutes 落 deploy_run vs usage_sample）：本 spec 採落 deploy_run（即時 + 個別 deploy 量測）；usage_sample 為長期計費資料源（v2）
- ADR-0005 OQ#8（Trivy `ignore-unfixed`）：v1 設 `true`；v1.1 強制時保持 `true`（避免不可修復 CVE 永久阻擋）
- GHA runner 規格 `ubuntu-latest`：v1 用 standard runner；高並發時 evaluate `large` paid runner
- callback NetworkPolicy IP allowlist：v1 開放（HMAC 為認證主路徑）；v2 補 GHA IP range allowlist（GitHub 提供 meta API）
- callback fallback secret 移除時程：v1 保留為 emergency；v2 評估移除

## 13. 不可違反的硬性規則

> 違反以下任一項，PR 不可合入。

1. `deploy-app.yml` 為 0ops 自身 repo 維護；**不**位於使用者 repo
2. workflow `permissions:` 必為最小化（`contents:write` + `packages:write`）；不得加 `pull-requests:write`、`secrets:read` 等不必要權限
3. callback endpoint 必驗：HMAC 簽章 + timestamp ±5 min + replay dedup 三層；缺一不可
4. `ops_token` TTL 必 ≤ 20 min；不得個別動作放寬
5. ops_token 為一次性；consumed_at 設定即不可重用（重用走 webhook_dedup 回放）
6. callback handler 必為 idempotent；同 run_id 重送不重做 state machine
7. callback 之 `trace_id` 必填；缺者視為 propagation 違反，backend 端 reject 並 log warn（同時為 ADR-0006 跨界第 4 段斷裂偵測）
8. `OPS_TOKEN_SIGNING_SECRET` 與 `OPS_CALLBACK_SECRET` 必為獨立 secret（K8s Secret 兩個 key）
9. Trivy 升 `exit-code=1` 必走 ADR 補丁 + plan.md 同步；不得只改 workflow YAML 一處
10. callback 不經 `AuthBearer` middleware；不得把 callback 路徑暴露在公開 API 範圍（路徑 prefix `/internal/`）
